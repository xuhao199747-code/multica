package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestUpdateIssueEnteringReviewQueuesMikaCoordination(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	executor := createHandlerTestAgent(t, "Review coordination executor", []byte("[]"))
	mika := markAsMika(t, createHandlerTestAgent(t, "Review coordination Mika", []byte("[]")))
	issueID := insertAgentAssignedIssue(t, executor, 93241, "review coordination trigger")

	w := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodPut, "/api/issues/"+issueID, map[string]any{"status": "in_review"}), "id", issueID)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := taskCountFor(t, issueID, mika); got != 1 {
		t.Fatalf("Mika coordination tasks = %d, want 1", got)
	}

	// A no-op write must not manufacture another coordinator run.
	w = httptest.NewRecorder()
	req = withURLParam(newRequest(http.MethodPut, "/api/issues/"+issueID, map[string]any{"status": "in_review"}), "id", issueID)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("second UpdateIssue: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := taskCountFor(t, issueID, mika); got != 1 {
		t.Fatalf("Mika coordination tasks after no-op = %d, want 1", got)
	}
}

// A project-workflow issue that enters review WITHOUT a delivery command is the
// evidence-gap defect: it must still wake the review coordinator instead of
// sailing into review silently.
func TestWorkflowIssueEnteringReviewQueuesMikaCoordination(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	executor := createHandlerTestAgent(t, "Workflow review executor", []byte("[]"))
	mika := markAsMika(t, createHandlerTestAgent(t, "Workflow review Mika", []byte("[]")))
	issueID := insertAgentAssignedIssue(t, executor, 93242, "workflow review coordination trigger")

	// Link the issue into a project execution record so it reads as workflow-owned.
	projectID, executionID := linkWorkflowIssueForCoordinationTest(t, issueID, executor)
	_ = projectID
	_ = executionID

	w := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodPut, "/api/issues/"+issueID, map[string]any{"status": "in_review"}), "id", issueID)
	testHandler.UpdateIssue(w, req)
	// The evidence guard refuses the bare status flip (409) — that is the
	// intended block. The coordination run must still be enqueued so the
	// evidence gap is triaged instead of slipping into review silently.
	if w.Code != http.StatusConflict {
		t.Fatalf("UpdateIssue: expected 409 evidence conflict, got %d: %s", w.Code, w.Body.String())
	}
	if got := taskCountFor(t, issueID, mika); got != 1 {
		t.Fatalf("Mika coordination tasks = %d, want 1", got)
	}
}

// linkWorkflowIssueForCoordinationTest creates a throwaway project + execution
// record and links the given issue to it as the workflow main issue, so the
// coordination guard sees the issue as workflow-owned.
func linkWorkflowIssueForCoordinationTest(t *testing.T, issueID, agentID string) (string, string) {
	t.Helper()
	w := httptest.NewRecorder()
	testHandler.CreateProject(w, newRequest(http.MethodPost, "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Coordination test project",
	}))
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

	ctx := context.Background()
	var sessionID string
	agentUUID := parseUUID(agentID)
	if err := testPool.QueryRow(ctx, `
		INSERT INTO chat_session (workspace_id, agent_id, creator_id, title, status, explicitly_created_at)
		VALUES ($1, $2, $3, 'Coordination test chat', 'active', now())
		RETURNING id
	`, testWorkspaceID, agentUUID, testUserID).Scan(&sessionID); err != nil {
		t.Fatalf("create chat session: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM chat_session WHERE id = $1`, sessionID)
	})
	record, err := testHandler.Queries.CreateProjectExecutionRecord(ctx, db.CreateProjectExecutionRecordParams{
		WorkspaceID:         parseUUID(project.WorkspaceID),
		ProjectID:           parseUUID(project.ID),
		SourceChatSessionID: parseUUID(sessionID),
		SourceAgentID:       parseUUID(agentID),
		Snapshot:            []byte(`{"goal":"test"}`),
		CreatedBy:           parseUUID(testUserID),
	})
	if err != nil {
		t.Fatalf("create execution record: %v", err)
	}
	if _, err := testHandler.Queries.LinkProjectExecutionIssue(ctx, db.LinkProjectExecutionIssueParams{
		ExecutionRecordID: record.ID,
		WorkspaceID:       parseUUID(project.WorkspaceID),
		ProjectID:         parseUUID(project.ID),
		IssueID:           parseUUID(issueID),
		Role:              "main",
	}); err != nil {
		t.Fatalf("link issue to execution: %v", err)
	}
	return project.ID, uuidToString(record.ID)
}
