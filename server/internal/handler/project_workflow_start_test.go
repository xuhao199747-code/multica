package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func createProjectWorkflowPlanMessageForTest(t *testing.T, sessionID, role, content string) string {
	t.Helper()
	var messageID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO chat_message (chat_session_id, role, content)
		VALUES ($1, $2, $3)
		RETURNING id::text
	`, sessionID, role, content).Scan(&messageID); err != nil {
		t.Fatalf("create chat message: %v", err)
	}
	return messageID
}

func projectWorkflowPlanConfirmationForTest(title string, snapshot map[string]any) string {
	payload, err := json.Marshal(map[string]any{
		"version": 1,
		"title":   title,
		"snapshot": snapshot,
	})
	if err != nil {
		panic(err)
	}
	return "Proposed plan.\n<!-- multica-project-workflow-plan-confirmation\n" + string(payload) + "\n-->"
}

// TestStartProjectWorkflowExecutionFromChatAtomicBridge covers the P0
// confirmation-to-dispatch bridge: one member action must create the
// execution record, the primary issue, the execution-issue link, and
// the queued agent run together.
func TestStartProjectWorkflowExecutionFromChatAtomicBridge(t *testing.T) {
	w := httptest.NewRecorder()
	testHandler.CreateProject(w, newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{"title": "Start bridge project"}))
	if w.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", w.Code, w.Body.String())
	}
	var project ProjectResponse
	if err := json.NewDecoder(w.Body).Decode(&project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		testHandler.DeleteProject(httptest.NewRecorder(), withURLParam(newRequest("DELETE", "/api/projects/"+project.ID, nil), "id", project.ID))
	})
	enableProjectWorkflowForTest(t, project.ID)

	prdID := createHandlerTestAgent(t, "start-bridge-prd", nil)
	developerID := createHandlerTestAgent(t, "start-bridge-developer", nil)
	bind := func(agentID, role string) {
		w := httptest.NewRecorder()
		testHandler.CreateProjectAgentBinding(w, withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/workflow/agents", map[string]any{"agent_id": agentID, "role": role}), "id", project.ID))
		if w.Code != http.StatusCreated {
			t.Fatalf("bind %s: %d %s", role, w.Code, w.Body.String())
		}
	}
	bind(prdID, "prd")
	bind(developerID, "developer")
	sessionID := createChatSessionWithProjectForTest(t, prdID, project.ID)

	body := map[string]any{
		"source_chat_session_id": sessionID,
		"snapshot": map[string]any{
			"goal":                "Ship the start bridge",
			"scope":               []string{"backend"},
			"acceptance_criteria": []string{"one test passes"},
			"risks":               []string{"none"},
		},
	}
	body["source_message_id"] = createProjectWorkflowPlanMessageForTest(
		t,
		sessionID,
		"assistant",
		projectWorkflowPlanConfirmationForTest("Ship the start bridge", body["snapshot"].(map[string]any)),
	)
	w = httptest.NewRecorder()
	testHandler.StartProjectWorkflowExecutionFromChat(w, withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/workflow/executions/start", body), "id", project.ID))
	if w.Code != http.StatusCreated {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	var started struct {
		Execution ProjectExecutionResponse `json:"execution"`
		Issue     IssueResponse            `json:"issue"`
		TaskID    *string                  `json:"task_id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&started); err != nil {
		t.Fatal(err)
	}
	if started.Execution.State != "development" {
		t.Fatalf("execution state = %q, want development", started.Execution.State)
	}
	if started.Execution.SourceChatSessionID != sessionID {
		t.Fatalf("source chat = %s, want %s", started.Execution.SourceChatSessionID, sessionID)
	}
	if got := started.Execution.Snapshot["goal"]; got != "Ship the start bridge" {
		t.Fatalf("execution snapshot goal = %#v, want message plan goal", got)
	}
	if started.Issue.ID == "" {
		t.Fatal("expected a primary workflow issue")
	}
	if started.TaskID == nil || *started.TaskID == "" {
		t.Fatal("expected a queued agent task")
	}

	// Durable link exists.
	var linkCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM project_execution_issue
		WHERE execution_record_id = $1 AND issue_id = $2 AND role = 'main'
	`, started.Execution.ID, started.Issue.ID).Scan(&linkCount); err != nil || linkCount != 1 {
		t.Fatalf("execution-issue link count = %d, err = %v, want 1", linkCount, err)
	}

	// The queued run belongs to the bound developer and is tied to the issue.
	var taskAgent, taskIssue string
	if err := testPool.QueryRow(context.Background(), `
		SELECT agent_id::text, issue_id::text FROM agent_task_queue WHERE id = $1
	`, *started.TaskID).Scan(&taskAgent, &taskIssue); err != nil {
		t.Fatalf("load queued task: %v", err)
	}
	if taskAgent != developerID || taskIssue != started.Issue.ID {
		t.Fatalf("queued task agent/issue = %s/%s, want %s/%s", taskAgent, taskIssue, developerID, started.Issue.ID)
	}

	// Idempotent replay: repeating the same confirmation returns the existing
	// execution and must not create a second one.
	w = httptest.NewRecorder()
	testHandler.StartProjectWorkflowExecutionFromChat(w, withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/workflow/executions/start", body), "id", project.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("replay: %d %s", w.Code, w.Body.String())
	}
	var replayed ProjectExecutionResponse
	if err := json.NewDecoder(w.Body).Decode(&replayed); err != nil {
		t.Fatal(err)
	}
	if replayed.ID != started.Execution.ID {
		t.Fatalf("replay returned execution %s, want %s", replayed.ID, started.Execution.ID)
	}
	var executionCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM project_execution_record WHERE source_chat_session_id = $1
	`, sessionID).Scan(&executionCount); err != nil || executionCount != 1 {
		t.Fatalf("execution count = %d, err = %v, want 1", executionCount, err)
	}
}

func TestStartProjectWorkflowExecutionFromChatRejectsTamperedMessageSnapshot(t *testing.T) {
	w := httptest.NewRecorder()
	testHandler.CreateProject(w, newRequest(http.MethodPost, "/api/projects?workspace_id="+testWorkspaceID, map[string]any{"title": "Message snapshot project"}))
	if w.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", w.Code, w.Body.String())
	}
	var project ProjectResponse
	if err := json.NewDecoder(w.Body).Decode(&project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		testHandler.DeleteProject(httptest.NewRecorder(), withURLParam(newRequest(http.MethodDelete, "/api/projects/"+project.ID, nil), "id", project.ID))
	})
	enableProjectWorkflowForTest(t, project.ID)

	prdID := createHandlerTestAgent(t, "message-snapshot-prd", nil)
	developerID := createHandlerTestAgent(t, "message-snapshot-developer", nil)
	for _, binding := range []struct{ agentID, role string }{{prdID, "prd"}, {developerID, "developer"}} {
		w = httptest.NewRecorder()
		testHandler.CreateProjectAgentBinding(w, withURLParam(newRequest(http.MethodPost, "/api/projects/"+project.ID+"/workflow/agents", map[string]any{
			"agent_id": binding.agentID,
			"role":     binding.role,
		}), "id", project.ID))
		if w.Code != http.StatusCreated {
			t.Fatalf("bind %s: %d %s", binding.role, w.Code, w.Body.String())
		}
	}
	sessionID := createChatSessionWithProjectForTest(t, prdID, project.ID)
	messageID := createProjectWorkflowPlanMessageForTest(
		t,
		sessionID,
		"assistant",
		projectWorkflowPlanConfirmationForTest("Confirmed plan", map[string]any{
			"goal":                "Use the displayed plan",
			"scope":               []string{"chat UI"},
			"acceptance_criteria": []string{"the message snapshot starts the work"},
			"risks":               []string{"stale client data"},
		}),
	)

	w = httptest.NewRecorder()
	testHandler.StartProjectWorkflowExecutionFromChat(w, withURLParam(newRequest(http.MethodPost, "/api/projects/"+project.ID+"/workflow/executions/start", map[string]any{
		"source_chat_session_id": sessionID,
		"source_message_id":      messageID,
		"snapshot": map[string]any{
			"goal":                "Start something from a later user reply instead",
			"scope":               []string{"unrelated"},
			"acceptance_criteria": []string{"not the displayed plan"},
			"risks":               []string{},
		},
	}), "id", project.ID))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("tampered snapshot: expected 400, got %d %s", w.Code, w.Body.String())
	}

	userMessageID := createProjectWorkflowPlanMessageForTest(
		t,
		sessionID,
		"user",
		projectWorkflowPlanConfirmationForTest("Not an assistant plan", map[string]any{
			"goal":                "A user cannot authorize a plan marker",
			"scope":               []string{"chat"},
			"acceptance_criteria": []string{"reject the message"},
			"risks":               []string{},
		}),
	)
	w = httptest.NewRecorder()
	testHandler.StartProjectWorkflowExecutionFromChat(w, withURLParam(newRequest(http.MethodPost, "/api/projects/"+project.ID+"/workflow/executions/start", map[string]any{
		"source_chat_session_id": sessionID,
		"source_message_id":      userMessageID,
		"snapshot": map[string]any{
			"goal":                "A user cannot authorize a plan marker",
			"scope":               []string{"chat"},
			"acceptance_criteria": []string{"reject the message"},
			"risks":               []string{},
		},
	}), "id", project.ID))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("user source message: expected 400, got %d %s", w.Code, w.Body.String())
	}
	var executionCount int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM project_execution_record WHERE source_chat_session_id = $1`, sessionID).Scan(&executionCount); err != nil || executionCount != 0 {
		t.Fatalf("execution count = %d, err = %v, want 0", executionCount, err)
	}
}

// TestStartProjectWorkflowExecutionFromChatRequiresDeveloper ensures a
// missing developer binding fails cleanly before any record is written.
func TestStartProjectWorkflowExecutionFromChatRequiresDeveloper(t *testing.T) {
	w := httptest.NewRecorder()
	testHandler.CreateProject(w, newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{"title": "Start bridge no developer"}))
	if w.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", w.Code, w.Body.String())
	}
	var project ProjectResponse
	if err := json.NewDecoder(w.Body).Decode(&project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		testHandler.DeleteProject(httptest.NewRecorder(), withURLParam(newRequest("DELETE", "/api/projects/"+project.ID, nil), "id", project.ID))
	})
	enableProjectWorkflowForTest(t, project.ID)
	prdID := createHandlerTestAgent(t, "start-bridge-nodev-prd", nil)
	w = httptest.NewRecorder()
	testHandler.CreateProjectAgentBinding(w, withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/workflow/agents", map[string]any{"agent_id": prdID, "role": "prd"}), "id", project.ID))
	if w.Code != http.StatusCreated {
		t.Fatalf("bind prd: %d %s", w.Code, w.Body.String())
	}
	sessionID := createChatSessionWithProjectForTest(t, prdID, project.ID)
	snapshot := map[string]any{
		"goal":                "No developer should not create work",
		"scope":               []string{"workflow"},
		"acceptance_criteria": []string{"developer binding is required"},
		"risks":               []string{},
	}
	messageID := createProjectWorkflowPlanMessageForTest(
		t,
		sessionID,
		"assistant",
		projectWorkflowPlanConfirmationForTest("No developer", snapshot),
	)

	w = httptest.NewRecorder()
	testHandler.StartProjectWorkflowExecutionFromChat(w, withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/workflow/executions/start", map[string]any{
		"source_chat_session_id": sessionID,
		"source_message_id":      messageID,
		"snapshot":               snapshot,
	}), "id", project.ID))
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 without developer, got %d %s", w.Code, w.Body.String())
	}
	var executionCount int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM project_execution_record WHERE source_chat_session_id = $1`, sessionID).Scan(&executionCount); err != nil || executionCount != 0 {
		t.Fatalf("execution count = %d, err = %v, want 0", executionCount, err)
	}
}

// TestStartProjectWorkflowExecutionFromChatSkipsCancelledExecution ensures a
// cancelled execution does not block a fresh start from the same chat. The
// active-execution lookup must not treat cancelled rows as active.
func TestStartProjectWorkflowExecutionFromChatSkipsCancelledExecution(t *testing.T) {
	w := httptest.NewRecorder()
	testHandler.CreateProject(w, newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{"title": "Start bridge after cancel"}))
	if w.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", w.Code, w.Body.String())
	}
	var project ProjectResponse
	if err := json.NewDecoder(w.Body).Decode(&project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		testHandler.DeleteProject(httptest.NewRecorder(), withURLParam(newRequest("DELETE", "/api/projects/"+project.ID, nil), "id", project.ID))
	})
	enableProjectWorkflowForTest(t, project.ID)

	prdID := createHandlerTestAgent(t, "start-bridge-cancel-prd", nil)
	developerID := createHandlerTestAgent(t, "start-bridge-cancel-developer", nil)
	for _, binding := range []struct{ agentID, role string }{{prdID, "prd"}, {developerID, "developer"}} {
		w = httptest.NewRecorder()
		testHandler.CreateProjectAgentBinding(w, withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/workflow/agents", map[string]any{"agent_id": binding.agentID, "role": binding.role}), "id", project.ID))
		if w.Code != http.StatusCreated {
			t.Fatalf("bind %s: %d %s", binding.role, w.Code, w.Body.String())
		}
	}
	sessionID := createChatSessionWithProjectForTest(t, prdID, project.ID)

	body := map[string]any{
		"source_chat_session_id": sessionID,
		"snapshot": map[string]any{
			"goal":                "First attempt",
			"scope":               []string{"workflow start"},
			"acceptance_criteria": []string{"a new run starts after cancellation"},
			"risks":               []string{},
		},
	}
	body["source_message_id"] = createProjectWorkflowPlanMessageForTest(
		t,
		sessionID,
		"assistant",
		projectWorkflowPlanConfirmationForTest("First attempt", body["snapshot"].(map[string]any)),
	)
	w = httptest.NewRecorder()
	testHandler.StartProjectWorkflowExecutionFromChat(w, withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/workflow/executions/start", body), "id", project.ID))
	if w.Code != http.StatusCreated {
		t.Fatalf("first start: %d %s", w.Code, w.Body.String())
	}
	var first struct {
		Execution ProjectExecutionResponse `json:"execution"`
	}
	if err := json.NewDecoder(w.Body).Decode(&first); err != nil {
		t.Fatal(err)
	}

	// Simulate the member cancelling the execution before it advances.
	if _, err := testPool.Exec(context.Background(), `UPDATE project_execution_record SET state = 'cancelled' WHERE id = $1`, first.Execution.ID); err != nil {
		t.Fatal(err)
	}

	// A fresh confirmation from the same chat must create a new execution,
	// not return the cancelled one.
	body["source_message_id"] = createProjectWorkflowPlanMessageForTest(
		t,
		sessionID,
		"assistant",
		projectWorkflowPlanConfirmationForTest("Second attempt after cancel", body["snapshot"].(map[string]any)),
	)
	w = httptest.NewRecorder()
	testHandler.StartProjectWorkflowExecutionFromChat(w, withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/workflow/executions/start", body), "id", project.ID))
	if w.Code != http.StatusCreated {
		t.Fatalf("restart after cancel: %d %s", w.Code, w.Body.String())
	}
	var second struct {
		Execution ProjectExecutionResponse `json:"execution"`
	}
	if err := json.NewDecoder(w.Body).Decode(&second); err != nil {
		t.Fatal(err)
	}
	if second.Execution.ID == first.Execution.ID {
		t.Fatalf("restart returned the cancelled execution %s", second.Execution.ID)
	}
	var executionCount int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM project_execution_record WHERE source_chat_session_id = $1`, sessionID).Scan(&executionCount); err != nil || executionCount != 2 {
		t.Fatalf("execution count = %d, err = %v, want 2", executionCount, err)
	}
}
