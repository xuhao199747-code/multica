package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWorkflowExecutorCannotCreateIssue(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	if _, err := testPool.Exec(context.Background(), `CREATE TABLE IF NOT EXISTS agent_workflow_role (agent_id uuid NOT NULL, role text NOT NULL, updated_at timestamptz NOT NULL DEFAULT clock_timestamp())`); err != nil {
		t.Fatalf("create workflow role table: %v", err)
	}
	agentID := createHandlerTestAgent(t, "workflow-executor", nil)
	taskID := createHandlerTestTaskForAgent(t, agentID)
	if _, err := testPool.Exec(context.Background(), `INSERT INTO agent_workflow_role (agent_id, role) VALUES ($1, 'executor')`, agentID); err != nil {
		t.Fatalf("set executor role: %v", err)
	}

	w := httptest.NewRecorder()
	r := newRequest(http.MethodPost, "/api/issues?workspace_id="+testWorkspaceID, map[string]any{"title": "executor must not coordinate"})
	r.Header.Set("X-Agent-ID", agentID)
	r.Header.Set("X-Task-ID", taskID)
	testHandler.CreateIssue(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("CreateIssue status = %d, want %d: %s", w.Code, http.StatusForbidden, w.Body.String())
	}
}

func TestWorkflowExecutorCannotAssignIssue(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	if _, err := testPool.Exec(context.Background(), `CREATE TABLE IF NOT EXISTS agent_workflow_role (agent_id uuid NOT NULL, role text NOT NULL, updated_at timestamptz NOT NULL DEFAULT clock_timestamp())`); err != nil {
		t.Fatalf("create workflow role table: %v", err)
	}
	executorID := createHandlerTestAgent(t, "workflow-executor-assignment", nil)
	targetID := createHandlerTestAgent(t, "workflow-assignment-target", nil)
	taskID := createHandlerTestTaskForAgent(t, executorID)
	if _, err := testPool.Exec(context.Background(), `INSERT INTO agent_workflow_role (agent_id, role) VALUES ($1, 'executor')`, executorID); err != nil {
		t.Fatalf("set executor role: %v", err)
	}

	created := httptest.NewRecorder()
	testHandler.CreateIssue(created, newRequest(http.MethodPost, "/api/issues?workspace_id="+testWorkspaceID, map[string]any{"title": "only Mika assigns"}))
	if created.Code != http.StatusCreated {
		t.Fatalf("create issue status = %d: %s", created.Code, created.Body.String())
	}
	var issue IssueResponse
	if err := json.NewDecoder(created.Body).Decode(&issue); err != nil {
		t.Fatalf("decode issue: %v", err)
	}

	w := httptest.NewRecorder()
	r := withURLParam(newRequest(http.MethodPut, "/api/issues/"+issue.ID+"?workspace_id="+testWorkspaceID, map[string]any{"assignee_type": "agent", "assignee_id": targetID}), "id", issue.ID)
	r.Header.Set("X-Agent-ID", executorID)
	r.Header.Set("X-Task-ID", taskID)
	testHandler.UpdateIssue(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("UpdateIssue status = %d, want %d: %s", w.Code, http.StatusForbidden, w.Body.String())
	}
}
