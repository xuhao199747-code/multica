package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestStartProjectWorkflowExecutionFromChatAtomicBridge covers the P0
// confirmation-to-dispatch bridge: one member action must create the
// execution record, the development issue, the execution-issue link, and
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
	if started.Issue.ID == "" {
		t.Fatal("expected a development issue")
	}
	if started.TaskID == nil || *started.TaskID == "" {
		t.Fatal("expected a queued agent task")
	}

	// Durable link exists.
	var linkCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM project_execution_issue
		WHERE execution_record_id = $1 AND issue_id = $2 AND role = 'development'
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

	w = httptest.NewRecorder()
	testHandler.StartProjectWorkflowExecutionFromChat(w, withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/workflow/executions/start", map[string]any{
		"source_chat_session_id": sessionID,
		"snapshot":               map[string]any{"goal": "g", "scope": []string{}, "acceptance_criteria": []string{}, "risks": []string{}},
	}), "id", project.ID))
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 without developer, got %d %s", w.Code, w.Body.String())
	}
	var executionCount int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM project_execution_record WHERE source_chat_session_id = $1`, sessionID).Scan(&executionCount); err != nil || executionCount != 0 {
		t.Fatalf("execution count = %d, err = %v, want 0", executionCount, err)
	}
}
