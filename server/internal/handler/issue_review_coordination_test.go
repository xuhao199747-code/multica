package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
