package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// issueIdentifier renders the workspace-scoped identifier the issue table
// derives from its counter (e.g. "MUL-42"). Kept beside the spec link code:
// the link endpoints are the ones that mint identifiers outside issue.go.
func issueIdentifier(prefix string, number int32) string {
	return prefix + "-" + strconv.Itoa(int(number))
}

// SpecLinkResponse is the spec summary embedded in issue responses: enough to
// render a chip and link without the version body.
type SpecLinkResponse struct {
	ID         string `json:"id"`
	Identifier string `json:"identifier"`
	Title      string `json:"title"`
	Status     string `json:"status"`
}

// ---------------------------------------------------------------------------
// Handlers — spec ↔ issue links
// ---------------------------------------------------------------------------

// ListSpecIssues lists the issues linked to a spec.
func (h *Handler) ListSpecIssues(w http.ResponseWriter, r *http.Request) {
	spec, ok := h.loadSpecForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}

	rows, err := h.Queries.ListSpecIssueRefs(r.Context(), db.ListSpecIssueRefsParams{
		SpecID:      spec.ID,
		WorkspaceID: spec.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list linked issues")
		return
	}
	prefix := h.getIssuePrefix(r.Context(), spec.WorkspaceID)
	out := make([]IssueRefResponse, len(rows))
	for i, row := range rows {
		out[i] = IssueRefResponse{
			ID:         uuidToString(row.ID),
			Identifier: issueIdentifier(prefix, row.Number),
			Title:      row.Title,
			Status:     row.Status,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"issues": out, "total": len(out)})
}

// LinkSpecIssue links an issue to a spec. The junction insert is idempotent
// (ON CONFLICT DO NOTHING), so re-linking an existing pair is a successful
// no-op rather than an error.
func (h *Handler) LinkSpecIssue(w http.ResponseWriter, r *http.Request) {
	spec, ok := h.loadSpecForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}

	var req struct {
		IssueID string `json:"issue_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.IssueID == "" {
		writeError(w, http.StatusBadRequest, "issue_id is required")
		return
	}
	// loadIssueForUser accepts an identifier or UUID and scopes to the same
	// workspace as the spec, so a foreign issue is a 404 exactly like an
	// unknown one — the junction must never join rows across workspaces.
	issue, ok := h.loadIssueForUser(w, r, req.IssueID)
	if !ok {
		return
	}

	if err := h.Queries.AddSpecIssue(r.Context(), db.AddSpecIssueParams{
		SpecID:  spec.ID,
		IssueID: issue.ID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to link issue")
		return
	}

	prefix := h.getIssuePrefix(r.Context(), issue.WorkspaceID)
	writeJSON(w, http.StatusCreated, IssueRefResponse{
		ID:         uuidToString(issue.ID),
		Identifier: issueIdentifier(prefix, issue.Number),
		Title:      issue.Title,
		Status:     issue.Status,
	})
}

// UnlinkSpecIssue removes one issue link from a spec. Unlinking a pair that
// is not linked is a successful no-op, mirroring how SetIssueLabel treats
// detaching an unattached label.
func (h *Handler) UnlinkSpecIssue(w http.ResponseWriter, r *http.Request) {
	spec, ok := h.loadSpecForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "issueId"))
	if !ok {
		return
	}

	if err := h.Queries.RemoveSpecIssue(r.Context(), db.RemoveSpecIssueParams{
		SpecID:  spec.ID,
		IssueID: issue.ID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to unlink issue")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// specsByIssue bulk-loads linked spec summaries for the given issue IDs and
// returns a map keyed by issue UUID string. Mirrors labelsByIssue: rendering
// spec chips is non-critical, so an error yields an empty map rather than
// failing the whole issue call.
func (h *Handler) specsByIssue(r *http.Request, wsUUID pgtype.UUID, issueIDs []pgtype.UUID) map[string][]SpecLinkResponse {
	out := map[string][]SpecLinkResponse{}
	if len(issueIDs) == 0 {
		return out
	}
	rows, err := h.Queries.ListSpecsForIssues(r.Context(), db.ListSpecsForIssuesParams{
		IssueIds:    issueIDs,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		return out
	}
	for _, row := range rows {
		issueID := uuidToString(row.IssueID)
		out[issueID] = append(out[issueID], SpecLinkResponse{
			ID:         uuidToString(row.ID),
			Identifier: row.Identifier,
			Title:      row.Title,
			Status:     row.Status,
		})
	}
	return out
}
