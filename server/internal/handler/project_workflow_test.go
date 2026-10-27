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

// assertIssueStatus fetches an issue and confirms its stored status key.
func assertIssueStatus(t *testing.T, issueID string, want string) {
	t.Helper()
	w := httptest.NewRecorder()
	testHandler.GetIssue(w, withURLParam(newRequest("GET", "/api/issues/"+issueID, nil), "id", issueID))
	if w.Code != http.StatusOK {
		t.Fatalf("GetIssue(%s): %d %s", issueID, w.Code, w.Body.String())
	}
	var issue IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&issue); err != nil {
		t.Fatal(err)
	}
	if issue.Status != want {
		t.Fatalf("issue %s status = %q, want %q", issueID, issue.Status, want)
	}
}
