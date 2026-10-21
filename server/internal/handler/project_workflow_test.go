package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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

	w = httptest.NewRecorder()
	testHandler.SubmitProjectExecutionDelivery(w, withURLParam(newRequest("POST", "/api/projects/"+project.ID+"/workflow/executions/delivery", map[string]any{
		"execution_id": execution.ID, "issue_id": dispatched.Issue.ID, "commit": "abc123", "tests": "go test ./...", "summary": "Implemented it",
	}), "id", project.ID))
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
