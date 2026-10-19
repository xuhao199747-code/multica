package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type ReviewDecisionResponse struct {
	ID            string  `json:"id"`
	IssueID       string  `json:"issue_id"`
	PullRequestID *string `json:"pull_request_id"`
	HeadSha       string  `json:"head_sha"`
	Decision      string  `json:"decision"`
	Note          *string `json:"note"`
	DecidedBy     string  `json:"decided_by"`
	CreatedAt     string  `json:"created_at"`
	SupersededAt  *string `json:"superseded_at,omitempty"`
	ResolvedAt    *string `json:"resolved_at,omitempty"`
	// Stale is true when the decision pins a PR head that no longer matches
	// the linked PR's current snapshot — the call was made against outdated
	// evidence and does not describe the head under review now.
	Stale bool `json:"stale"`
}

type ListReviewDecisionsResponse struct {
	Decisions []ReviewDecisionResponse `json:"decisions"`
}

func reviewDecisionToResponse(d db.ReviewDecision, currentHead string) ReviewDecisionResponse {
	resp := ReviewDecisionResponse{
		ID:            uuidToString(d.ID),
		IssueID:       uuidToString(d.IssueID),
		PullRequestID: util.UUIDToPtr(d.PullRequestID),
		HeadSha:       d.HeadSha,
		Decision:      d.Decision,
		Note:          util.TextToPtr(d.Note),
		DecidedBy:     uuidToString(d.DecidedBy),
		CreatedAt:     timestampToString(d.CreatedAt),
		SupersededAt:  util.TimestampToPtr(d.SupersededAt),
		ResolvedAt:    util.TimestampToPtr(d.ResolvedAt),
		Stale:         currentHead != "" && d.HeadSha != currentHead,
	}
	return resp
}

// currentPullRequestHead returns the linked PR's current snapshot head for
// staleness display. Issue-less or deleted PRs yield "" (nothing to compare).
func (h *Handler) currentPullRequestHead(ctx context.Context, prID pgtype.UUID, workspaceID pgtype.UUID) string {
	if !prID.Valid {
		return ""
	}
	pr, err := h.Queries.GetGitHubPullRequestInWorkspace(ctx, db.GetGitHubPullRequestInWorkspaceParams{ID: prID, WorkspaceID: workspaceID})
	if err != nil {
		return ""
	}
	return pr.HeadSha
}

func (h *Handler) ListReviewDecisions(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	decisions, err := (&service.ReviewAgentService{Tasks: h.TaskService}).ListDecisions(r.Context(), issue.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list review decisions")
		return
	}
	out := make([]ReviewDecisionResponse, 0, len(decisions))
	for _, d := range decisions {
		out = append(out, reviewDecisionToResponse(d, h.currentPullRequestHead(r.Context(), d.PullRequestID, issue.WorkspaceID)))
	}
	writeJSON(w, http.StatusOK, ListReviewDecisionsResponse{Decisions: out})
}

type CreateReviewDecisionRequest struct {
	PullRequestID string `json:"pull_request_id"`
	HeadSha       string `json:"head_sha"`
	Decision      string `json:"decision"`
	Note          string `json:"note"`
}

func (h *Handler) CreateReviewDecision(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	var req CreateReviewDecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	decidedBy, ok := parseUUIDOrBadRequest(w, requestUserID(r), "decided_by")
	if !ok {
		return
	}
	decision, err := (&service.ReviewAgentService{Tasks: h.TaskService}).RecordDecision(r.Context(), issue, decidedBy, service.ReviewDecisionInput{
		PullRequestID: req.PullRequestID,
		HeadSha:       req.HeadSha,
		Decision:      req.Decision,
		Note:          req.Note,
	})
	if err != nil {
		writeError(w, reviewDecisionErrorStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, reviewDecisionToResponse(decision, h.currentPullRequestHead(r.Context(), decision.PullRequestID, issue.WorkspaceID)))
}

func (h *Handler) ResolveReviewDecision(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	decisionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "decisionID"), "decision_id")
	if !ok {
		return
	}
	decision, err := (&service.ReviewAgentService{Tasks: h.TaskService}).ResolveDecision(r.Context(), issue.WorkspaceID, decisionID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "review decision not found or already settled")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve review decision")
		return
	}
	writeJSON(w, http.StatusOK, reviewDecisionToResponse(decision, h.currentPullRequestHead(r.Context(), decision.PullRequestID, issue.WorkspaceID)))
}

func reviewDecisionErrorStatus(err error) int {
	switch err.Error() {
	case "invalid decision", "invalid head_sha", "invalid pull_request_id", "pull request is not linked to this issue", "pull request not found in workspace":
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}
