package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func enableProjectWorkflowForTest(t *testing.T, projectID string) {
	t.Helper()
	w := httptest.NewRecorder()
	testHandler.UpdateProjectWorkflowSettings(w, withURLParam(newRequest("PATCH", "/api/projects/"+projectID+"/workflow", map[string]any{
		"enabled": true, "delivery_mode": "notify_only", "delivery_config": map[string]any{},
	}), "id", projectID))
	if w.Code != http.StatusOK {
		t.Fatalf("enable project workflow: %d %s", w.Code, w.Body.String())
	}
}

func TestProjectWorkflowSettingsDefaultAndUpdate(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Workflow settings project",
	})
	testHandler.CreateProject(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateProject: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var project ProjectResponse
	if err := json.NewDecoder(w.Body).Decode(&project); err != nil {
		t.Fatalf("decode project: %v", err)
	}
	defer func() {
		req := newRequest("DELETE", "/api/projects/"+project.ID, nil)
		req = withURLParam(req, "id", project.ID)
		testHandler.DeleteProject(httptest.NewRecorder(), req)
	}()

	w = httptest.NewRecorder()
	req = newRequest("GET", "/api/projects/"+project.ID+"/workflow", nil)
	req = withURLParam(req, "id", project.ID)
	testHandler.GetProjectWorkflowSettings(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetProjectWorkflowSettings: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var defaults ProjectWorkflowSettingsResponse
	if err := json.NewDecoder(w.Body).Decode(&defaults); err != nil {
		t.Fatalf("decode defaults: %v", err)
	}
	if defaults.Enabled {
		t.Error("default workflow must be disabled")
	}
	if defaults.DeliveryMode != "notify_only" {
		t.Errorf("default delivery mode = %q, want notify_only", defaults.DeliveryMode)
	}

	w = httptest.NewRecorder()
	req = newRequest("PATCH", "/api/projects/"+project.ID+"/workflow", map[string]any{
		"enabled":         true,
		"project_context": "This project uses review before delivery.",
		"delivery_mode":   "manual_release",
		"delivery_config": map[string]any{"run_health_check": true},
	})
	req = withURLParam(req, "id", project.ID)
	testHandler.UpdateProjectWorkflowSettings(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateProjectWorkflowSettings: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var updated ProjectWorkflowSettingsResponse
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode updated settings: %v", err)
	}
	if !updated.Enabled || updated.DeliveryMode != "manual_release" {
		t.Errorf("updated settings = %+v, want enabled manual_release", updated)
	}
}

func TestProjectAgentBindingAllowsReusableAgentsButOneMika(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Workflow binding project",
	})
	testHandler.CreateProject(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateProject: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var project ProjectResponse
	if err := json.NewDecoder(w.Body).Decode(&project); err != nil {
		t.Fatalf("decode project: %v", err)
	}
	t.Cleanup(func() {
		req := withURLParam(newRequest("DELETE", "/api/projects/"+project.ID, nil), "id", project.ID)
		testHandler.DeleteProject(httptest.NewRecorder(), req)
	})

	mikaID := createHandlerTestAgent(t, "project-workflow-mika", nil)
	developerID := createHandlerTestAgent(t, "project-workflow-developer", nil)
	createBinding := func(agentID, role string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/workflow/agents", map[string]any{
			"agent_id": agentID,
			"role":     role,
		}), "id", project.ID)
		testHandler.CreateProjectAgentBinding(w, req)
		return w
	}

	if w := createBinding(mikaID, "mika"); w.Code != http.StatusCreated {
		t.Fatalf("create Mika binding: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if w := createBinding(developerID, "developer"); w.Code != http.StatusCreated {
		t.Fatalf("create developer binding: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if w := createBinding(developerID, "mika"); w.Code != http.StatusConflict {
		t.Errorf("second Mika binding: expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestConfirmProjectExecutionFreezesSnapshot(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{"title": "Execution snapshot project"})
	testHandler.CreateProject(w, req)
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
	agentID := createHandlerTestAgent(t, "execution-snapshot-prd", nil)
	w = httptest.NewRecorder()
	testHandler.CreateProjectAgentBinding(w, withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/workflow/agents", map[string]any{"agent_id": agentID, "role": "prd"}), "id", project.ID))
	if w.Code != http.StatusCreated {
		t.Fatalf("bind PRD: %d %s", w.Code, w.Body.String())
	}
	sessionID := createChatSessionWithProjectForTest(t, agentID, project.ID)
	w = httptest.NewRecorder()
	req = withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/workflow/executions", map[string]any{
		"source_chat_session_id": sessionID,
		"source_agent_id":        agentID,
		"snapshot":               map[string]any{"goal": "Ship project workflow", "scope": []string{"backend"}, "acceptance_criteria": []string{"a test passes"}, "risks": []string{"migration"}},
	}), "id", project.ID)
	testHandler.ConfirmProjectExecution(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("ConfirmProjectExecution: %d %s", w.Code, w.Body.String())
	}
	var execution ProjectExecutionResponse
	if err := json.NewDecoder(w.Body).Decode(&execution); err != nil {
		t.Fatal(err)
	}
	if execution.State != "development" || execution.Snapshot["goal"] != "Ship project workflow" {
		t.Fatalf("execution = %+v", execution)
	}
}

// The source identity comes from the selected chat, including direct Mica handoffs.
func TestConfirmProjectExecutionDerivesSourceAgentFromChat(t *testing.T) {
	for _, role := range []string{"mika", "prd", "developer"} {
		t.Run(role, func(t *testing.T) {
			projectID := dbfx.Project(t, "Source identity "+role)
			enableProjectWorkflowForTest(t, projectID)
			agentID := createHandlerTestAgent(t, "source-identity-"+role, nil)
			w := httptest.NewRecorder()
			testHandler.CreateProjectAgentBinding(w, withURLParam(newRequest("POST", "/", map[string]any{"agent_id": agentID, "role": role}), "id", projectID))
			if w.Code != http.StatusCreated {
				t.Fatalf("bind: %d %s", w.Code, w.Body.String())
			}
			sessionID := createChatSessionWithProjectForTest(t, agentID, projectID)
			body := map[string]any{"source_chat_session_id": sessionID, "snapshot": map[string]any{"goal": "Direct handoff", "scope": []string{"docs"}, "acceptance_criteria": []string{"reviewed"}, "risks": []string{}}}
			// An explicitly supplied, mismatched identity must still be rejected.
			body["source_agent_id"] = createHandlerTestAgent(t, "wrong-source-"+role, nil)
			w = httptest.NewRecorder()
			testHandler.ConfirmProjectExecution(w, withURLParam(newRequest("POST", "/", body), "id", projectID))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("mismatched source: %d %s", w.Code, w.Body.String())
			}
			delete(body, "source_agent_id")
			w = httptest.NewRecorder()
			testHandler.ConfirmProjectExecution(w, withURLParam(newRequest("POST", "/", body), "id", projectID))
			if role == "developer" {
				if w.Code != http.StatusBadRequest {
					t.Fatalf("developer source must be rejected: %d %s", w.Code, w.Body.String())
				}
				return
			}
			if w.Code != http.StatusCreated {
				t.Fatalf("derive source: %d %s", w.Code, w.Body.String())
			}
			var execution ProjectExecutionResponse
			if err := json.NewDecoder(w.Body).Decode(&execution); err != nil {
				t.Fatal(err)
			}
			if execution.SourceAgentID != agentID {
				t.Fatalf("source agent = %s, want %s", execution.SourceAgentID, agentID)
			}
		})
	}
}

func TestProjectExecutionRequiresEnabledWorkflow(t *testing.T) {
	w := httptest.NewRecorder()
	testHandler.CreateProject(w, newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{"title": "Disabled workflow project"}))
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
	agentID := createHandlerTestAgent(t, "disabled-workflow-prd", nil)
	sessionID := createChatSessionWithProjectForTest(t, agentID, project.ID)
	w = httptest.NewRecorder()
	testHandler.ConfirmProjectExecution(w, withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/workflow/executions", map[string]any{
		"source_chat_session_id": sessionID,
		"source_agent_id":        agentID,
		"snapshot":               map[string]any{"goal": "Must not start", "scope": []string{}, "acceptance_criteria": []string{}, "risks": []string{}},
	}), "id", project.ID))
	if w.Code != http.StatusConflict {
		t.Fatalf("disabled workflow confirmation: expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestProjectDeliveryQueuesMicaReviewCoordination(t *testing.T) {
	w := httptest.NewRecorder()
	testHandler.CreateProject(w, newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{"title": "Workflow review queue project"}))
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
	mikaID := createHandlerTestAgent(t, "workflow-coordinator-mika", nil)
	developerID := createHandlerTestAgent(t, "workflow-queue-developer", nil)
	prdID := createHandlerTestAgent(t, "workflow-queue-prd", nil)
	reviewerID := createHandlerTestAgent(t, "workflow-queue-reviewer", nil)
	bind := func(agentID, role string) {
		w := httptest.NewRecorder()
		testHandler.CreateProjectAgentBinding(w, withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/workflow/agents", map[string]any{"agent_id": agentID, "role": role}), "id", project.ID))
		if w.Code != http.StatusCreated {
			t.Fatalf("bind %s: %d %s", role, w.Code, w.Body.String())
		}
	}
	bind(mikaID, "mika")
	bind(developerID, "developer")
	bind(prdID, "prd")
	bind(reviewerID, "reviewer")
	w = httptest.NewRecorder()
	testHandler.ListProjectAgentBindings(w, withURLParam(newRequest("GET", "/api/projects/"+project.ID+"/workflow/agents", nil), "id", project.ID))
	var bound struct {
		Bindings []ProjectAgentBindingResponse `json:"bindings"`
	}
	if err := json.NewDecoder(w.Body).Decode(&bound); err != nil {
		t.Fatal(err)
	}
	if len(bound.Bindings) != 4 {
		t.Fatalf("bindings = %+v, want four roles", bound.Bindings)
	}
	var boundDeveloperID string
	for _, binding := range bound.Bindings {
		if binding.Role == "developer" {
			boundDeveloperID = binding.AgentID
		}
	}
	if boundDeveloperID != developerID {
		t.Fatalf("developer binding agent_id = %q, want %q; bindings = %+v", boundDeveloperID, developerID, bound.Bindings)
	}

	sessionID := createChatSessionWithProjectForTest(t, prdID, project.ID)
	w = httptest.NewRecorder()
	testHandler.ConfirmProjectExecution(w, withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/workflow/executions", map[string]any{
		"source_chat_session_id": sessionID,
		"source_agent_id":        prdID,
		"snapshot":               map[string]any{"goal": "Queue a real review", "scope": []string{"server"}, "acceptance_criteria": []string{"coordination task"}, "risks": []string{}},
	}), "id", project.ID))
	if w.Code != http.StatusCreated {
		t.Fatalf("confirm execution: %d %s", w.Code, w.Body.String())
	}
	var execution ProjectExecutionResponse
	if err := json.NewDecoder(w.Body).Decode(&execution); err != nil {
		t.Fatal(err)
	}

	w = httptest.NewRecorder()
	testHandler.DispatchProjectExecution(w, withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/workflow/executions/dispatch", map[string]any{
		"execution_id": execution.ID, "agent_id": developerID, "role": "development", "title": "Build the workflow",
	}), "id", project.ID))
	if w.Code != http.StatusCreated {
		t.Fatalf("dispatch developer: %d %s", w.Code, w.Body.String())
	}
	var dispatched struct {
		Issue IssueResponse `json:"issue"`
	}
	if err := json.NewDecoder(w.Body).Decode(&dispatched); err != nil {
		t.Fatal(err)
	}
	var brief map[string]any
	if dispatched.Issue.Description == nil {
		t.Fatal("missing workflow task brief")
	}
	if err := json.Unmarshal([]byte(*dispatched.Issue.Description), &brief); err != nil {
		t.Fatal(err)
	}
	command, _ := brief["required_completion_command"].(string)
	if !strings.Contains(command, "workflow delivery "+project.ID+" --execution "+execution.ID) {
		t.Fatalf("missing scoped delivery command: %s", command)
	}
	w = httptest.NewRecorder()
	testHandler.CreateProject(w, newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{"title": "Other workflow project"}))
	if w.Code != http.StatusCreated {
		t.Fatalf("create other project: %d %s", w.Code, w.Body.String())
	}
	var otherProject ProjectResponse
	if err := json.NewDecoder(w.Body).Decode(&otherProject); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		testHandler.DeleteProject(httptest.NewRecorder(), withURLParam(newRequest("DELETE", "/api/projects/"+otherProject.ID, nil), "id", otherProject.ID))
	})
	w = httptest.NewRecorder()
	testHandler.UpdateIssue(w, withURLParam(newRequest("PATCH", "/api/issues/"+dispatched.Issue.ID, map[string]any{"project_id": otherProject.ID}), "id", dispatched.Issue.ID))
	if w.Code != http.StatusConflict {
		t.Fatalf("move workflow issue: expected 409, got %d: %s", w.Code, w.Body.String())
	}

	// A status-only handoff used to strand the execution in development.
	for _, status := range []string{"in_review", "done"} {
		for _, withDescription := range []bool{false, true} {
			body := map[string]any{"status": status}
			if withDescription {
				body["description"] = "Must not bypass evidence by editing description"
			}
			w = httptest.NewRecorder()
			testHandler.UpdateIssue(w, withURLParam(newRequest("PATCH", "/", body), "id", dispatched.Issue.ID))
			if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "workflow delivery") {
				t.Fatalf("status-only %s (description=%v): %d %s", status, withDescription, w.Code, w.Body.String())
			}
		}
	}
	w = httptest.NewRecorder()
	testHandler.BatchUpdateIssues(w, newRequest("PATCH", "/", map[string]any{"issue_ids": []string{dispatched.Issue.ID}, "updates": map[string]any{"status": "in_review"}}))
	var batch struct {
		Updated int `json:"updated"`
	}
	if err := json.NewDecoder(w.Body).Decode(&batch); err != nil {
		t.Fatal(err)
	}
	if batch.Updated != 0 {
		t.Fatalf("batch bypassed delivery evidence: %d", batch.Updated)
	}

	w = httptest.NewRecorder()
	testHandler.DispatchProjectExecution(w, withURLParam(newRequest("POST", "/", map[string]any{"execution_id": execution.ID, "agent_id": developerID, "role": "development", "title": "Second delivery needs its own evidence"}), "id", project.ID))
	if w.Code != http.StatusCreated {
		t.Fatalf("second developer: %d %s", w.Code, w.Body.String())
	}
	var secondDispatch struct {
		Issue IssueResponse `json:"issue"`
	}
	if err := json.NewDecoder(w.Body).Decode(&secondDispatch); err != nil {
		t.Fatal(err)
	}
	// Hold the execution row so both requests reach the same transition.
	// Exactly one may submit; two successes used to overwrite evidence and queue twice.
	lock, err := testPool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(context.Background())
	if _, err := lock.Exec(context.Background(), "SELECT id FROM project_execution_record WHERE id=$1 FOR UPDATE", execution.ID); err != nil {
		t.Fatal(err)
	}
	responses := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		req := withURLParam(newRequest("POST", "/", map[string]any{"execution_id": execution.ID, "issue_id": dispatched.Issue.ID, "commit": "abc123", "tests": "go test ./...", "summary": "Implemented it"}), "id", project.ID)
		go func() {
			recorder := httptest.NewRecorder()
			testHandler.SubmitProjectExecutionDelivery(recorder, req)
			responses <- recorder
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting int
		if err := testPool.QueryRow(context.Background(), "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%project_execution_record%'").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("delivery requests did not reach execution lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := lock.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	first, second := <-responses, <-responses
	if first.Code == http.StatusConflict {
		first, second = second, first
	}
	if first.Code != http.StatusOK || second.Code != http.StatusConflict {
		t.Fatalf("concurrent delivery statuses = %d, %d; want one 200 and one 409", first.Code, second.Code)
	}
	w = first
	if w.Code != http.StatusOK {
		t.Fatalf("submit delivery: %d %s", w.Code, w.Body.String())
	}
	var result struct {
		Execution          ProjectExecutionResponse `json:"execution"`
		CoordinationIssue  IssueResponse            `json:"coordination_issue"`
		CoordinationTaskID *string                  `json:"coordination_task_id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Execution.State != "review" || result.CoordinationIssue.AssigneeID == nil || *result.CoordinationIssue.AssigneeID != mikaID || result.CoordinationTaskID == nil || *result.CoordinationTaskID == "" {
		t.Fatalf("delivery result did not queue Mica coordination: %+v", result)
	}
	w = httptest.NewRecorder()
	testHandler.UpdateIssue(w, withURLParam(newRequest("PATCH", "/", map[string]any{"status": "in_review"}), "id", dispatched.Issue.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("status after evidence: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	testHandler.UpdateIssue(w, withURLParam(newRequest("PATCH", "/", map[string]any{"status": "in_review"}), "id", secondDispatch.Issue.ID))
	if w.Code != http.StatusConflict {
		t.Fatalf("another issue borrowed evidence: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	testHandler.ListProjectExecutionRecords(w, withURLParam(newRequest("GET", "/api/projects/"+project.ID+"/workflow/executions", nil), "id", project.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("list workflow executions: %d %s", w.Code, w.Body.String())
	}
	var listed struct {
		ExecutionIssues []struct {
			ExecutionID string `json:"execution_id"`
			IssueID     string `json:"issue_id"`
			Role        string `json:"role"`
		} `json:"execution_issues"`
	}
	if err := json.NewDecoder(w.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	foundDeveloper, foundCoordinator := false, false
	for _, item := range listed.ExecutionIssues {
		if item.ExecutionID != execution.ID {
			continue
		}
		foundDeveloper = foundDeveloper || item.IssueID == dispatched.Issue.ID && item.Role == "development"
		foundCoordinator = foundCoordinator || item.IssueID == result.CoordinationIssue.ID && item.Role == "review"
	}
	if !foundDeveloper || !foundCoordinator {
		t.Fatalf("execution issue status is incomplete: %+v", listed.ExecutionIssues)
	}
	w = httptest.NewRecorder()
	testHandler.UpdateIssue(w, withURLParam(newRequest("PATCH", "/api/issues/"+result.CoordinationIssue.ID, map[string]any{"project_id": otherProject.ID}), "id", result.CoordinationIssue.ID))
	if w.Code != http.StatusConflict {
		t.Fatalf("move Mica coordination issue: expected 409, got %d: %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	testHandler.DispatchProjectExecution(w, withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/workflow/executions/dispatch", map[string]any{
		"execution_id": execution.ID, "agent_id": reviewerID, "role": "review", "title": "Review the workflow",
	}), "id", project.ID))
	if w.Code != http.StatusCreated {
		t.Fatalf("dispatch review: %d %s", w.Code, w.Body.String())
	}
	var reviewDispatch struct {
		Issue IssueResponse `json:"issue"`
	}
	if err := json.NewDecoder(w.Body).Decode(&reviewDispatch); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	testHandler.UpdateIssue(w, withURLParam(newRequest("PATCH", "/", map[string]any{"status": "in_review"}), "id", reviewDispatch.Issue.ID))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "workflow review") {
		t.Fatalf("review status without verdict: %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	testHandler.RecordProjectReview(w, withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/workflow/executions/review", map[string]any{
		"execution_id": execution.ID, "issue_id": reviewDispatch.Issue.ID, "outcome": "passed", "findings": "Reviewed commit and tests.",
	}), "id", project.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("record review: %d %s", w.Code, w.Body.String())
	}
	var reviewResult struct {
		Execution          ProjectExecutionResponse `json:"execution"`
		CoordinationTaskID *string                  `json:"coordination_task_id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&reviewResult); err != nil {
		t.Fatal(err)
	}
	if reviewResult.Execution.State != "delivering" || reviewResult.CoordinationTaskID == nil || *reviewResult.CoordinationTaskID == "" {
		t.Fatalf("review result did not queue delivery coordination: %+v", reviewResult)
	}
}
