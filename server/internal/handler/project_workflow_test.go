package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
