package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Spec version publishing and the spec state machine.
//
// Versions are immutable snapshots: publishing appends a row with the next
// number, and the only mutation path is editing the latest version of a spec
// still in draft. No endpoint addresses an individual version for writes, so
// once a spec leaves draft every published snapshot is frozen by
// construction. Transitions follow
//
//	draft → in_review → approved → deprecated
//
// with draft → deprecated allowed directly; entering in_review or approved
// requires at least one published version.

type SpecVersionItem struct {
	ID        string `json:"id"`
	Number    int32  `json:"number"`
	Content   string `json:"content"`
	Changelog string `json:"changelog"`
	CreatedAt string `json:"created_at"`
}

// SpecStateItem is the transition response: enough spec identity to refresh
// a status badge without dragging the current version body along.
type SpecStateItem struct {
	ID               string  `json:"id"`
	Identifier       string  `json:"identifier"`
	Title            string  `json:"title"`
	Status           string  `json:"status"`
	CurrentVersionID *string `json:"current_version_id"`
	UpdatedAt        string  `json:"updated_at"`
}

type PublishSpecVersionRequest struct {
	Content   string `json:"content"`
	Changelog string `json:"changelog"`
}

type TransitionSpecRequest struct {
	To string `json:"to"`
}

type UpdateLatestSpecVersionRequest struct {
	Content   string  `json:"content"`
	Changelog *string `json:"changelog"`
}

func specVersionItem(v db.SpecVersion) SpecVersionItem {
	return SpecVersionItem{
		ID:        uuidToString(v.ID),
		Number:    v.Number,
		Content:   v.Content,
		Changelog: v.Changelog,
		CreatedAt: timestampToString(v.CreatedAt),
	}
}

func specStateItem(s db.Spec) SpecStateItem {
	return SpecStateItem{
		ID:               uuidToString(s.ID),
		Identifier:       s.Identifier,
		Title:            s.Title,
		Status:           s.Status,
		CurrentVersionID: uuidToPtr(s.CurrentVersionID),
		UpdatedAt:        timestampToString(s.UpdatedAt),
	}
}

// loadSpecForVersionRoutes resolves the spec by URL id, scoping to the
// request workspace. Mirrors the loader pattern of the issue handlers.
func (h *Handler) loadSpecForVersionRoutes(w http.ResponseWriter, r *http.Request, id string) (db.Spec, bool) {
	if _, ok := requireUserID(w, r); !ok {
		return db.Spec{}, false
	}
	ws, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace_id")
	if !ok {
		return db.Spec{}, false
	}
	sid, ok := parseUUIDOrBadRequest(w, id, "spec id")
	if !ok {
		return db.Spec{}, false
	}
	s, err := h.Queries.GetSpecInWorkspace(r.Context(), db.GetSpecInWorkspaceParams{ID: sid, WorkspaceID: ws})
	if err != nil {
		writeError(w, http.StatusNotFound, "spec not found")
		return db.Spec{}, false
	}
	return s, true
}

// PublishSpecVersion appends the next immutable version snapshot and points
// spec.current_version_id at it. Deprecated specs are frozen: modifying one
// means un-deprecating it first (a new transition), not appending here.
func (h *Handler) PublishSpecVersion(w http.ResponseWriter, r *http.Request) {
	s, ok := h.loadSpecForVersionRoutes(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if s.Status == "deprecated" {
		writeError(w, http.StatusConflict, "deprecated specs cannot accept new versions")
		return
	}
	var req PublishSpecVersionRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}
	if strings.TrimSpace(req.Changelog) == "" {
		writeError(w, http.StatusBadRequest, "changelog is required")
		return
	}
	user, ok := requireUserID(w, r)
	if !ok {
		return
	}
	creator, ok := parseUUIDOrBadRequest(w, user, "user id")
	if !ok {
		return
	}
	v, err := h.Queries.CreateSpecVersionNext(r.Context(), db.CreateSpecVersionNextParams{
		SpecID:    s.ID,
		Content:   strings.TrimSpace(req.Content),
		Changelog: strings.TrimSpace(req.Changelog),
		CreatedBy: creator,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to publish version")
		return
	}
	if _, err := h.Queries.SetSpecCurrentVersion(r.Context(), db.SetSpecCurrentVersionParams{
		ID:               s.ID,
		WorkspaceID:      s.WorkspaceID,
		CurrentVersionID: v.ID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update spec current version")
		return
	}
	writeJSON(w, http.StatusCreated, specVersionItem(v))
}

func (h *Handler) ListSpecVersions(w http.ResponseWriter, r *http.Request) {
	s, ok := h.loadSpecForVersionRoutes(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	rows, err := h.Queries.ListSpecVersions(r.Context(), s.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list versions")
		return
	}
	out := make([]SpecVersionItem, len(rows))
	for i, v := range rows {
		out[i] = specVersionItem(v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": out, "total": len(out)})
}

// GetSpecVersion returns one snapshot by its version number.
func (h *Handler) GetSpecVersion(w http.ResponseWriter, r *http.Request) {
	s, ok := h.loadSpecForVersionRoutes(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	num, err := strconv.ParseInt(chi.URLParam(r, "number"), 10, 32)
	if err != nil || num < 1 {
		writeError(w, http.StatusBadRequest, "invalid version number")
		return
	}
	v, err := h.Queries.GetSpecVersionByNumber(r.Context(), db.GetSpecVersionByNumberParams{SpecID: s.ID, Number: int32(num)})
	if err != nil {
		writeError(w, http.StatusNotFound, "version not found")
		return
	}
	writeJSON(w, http.StatusOK, specVersionItem(v))
}

// UpdateLatestSpecVersion edits the newest version in place. It exists for
// the drafting workflow only: once a spec leaves draft its snapshots are
// immutable and every change must be a new version. The route accepts the
// literal "latest" and never a version id or number, so there is no write
// path to any earlier snapshot.
func (h *Handler) UpdateLatestSpecVersion(w http.ResponseWriter, r *http.Request) {
	s, ok := h.loadSpecForVersionRoutes(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if s.Status != "draft" {
		writeError(w, http.StatusConflict, "only draft specs can be edited; publish a new version instead")
		return
	}
	var req UpdateLatestSpecVersionRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}
	latest, err := h.Queries.GetLatestSpecVersion(r.Context(), s.ID)
	if err != nil {
		writeError(w, http.StatusNotFound, "spec has no versions to edit")
		return
	}
	var changelog pgtype.Text
	if req.Changelog != nil {
		if strings.TrimSpace(*req.Changelog) == "" {
			writeError(w, http.StatusBadRequest, "changelog cannot be empty")
			return
		}
		changelog = pgtype.Text{String: strings.TrimSpace(*req.Changelog), Valid: true}
	}
	v, err := h.Queries.UpdateSpecVersionContentWhereNumber(r.Context(), db.UpdateSpecVersionContentWhereNumberParams{
		ID:        latest.ID,
		SpecID:    s.ID,
		Content:   strings.TrimSpace(req.Content),
		Number:    latest.Number,
		Changelog: changelog,
	})
	if err != nil {
		// A concurrent publish bumped the latest version between the read
		// and this write; the guarded update refused to touch the snapshot
		// the request was validated against.
		writeError(w, http.StatusConflict, "latest version changed concurrently; retry")
		return
	}
	writeJSON(w, http.StatusOK, specVersionItem(v))
}

// TransitionSpecStatus applies one state-machine step. Legality is checked
// against the loaded spec, then enforced atomically by
// UpdateSpecStatusWhereStatus so a concurrent change cannot smuggle an
// illegal transition through.
func (h *Handler) TransitionSpecStatus(w http.ResponseWriter, r *http.Request) {
	s, ok := h.loadSpecForVersionRoutes(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	var req TransitionSpecRequest
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	to := strings.TrimSpace(req.To)
	if to != "draft" && to != "in_review" && to != "approved" && to != "deprecated" {
		writeError(w, http.StatusBadRequest, "unknown target status")
		return
	}
	legal := map[string]bool{
		"draft→in_review":      s.Status == "draft",
		"draft→deprecated":     s.Status == "draft",
		"in_review→approved":   s.Status == "in_review",
		"in_review→deprecated": s.Status == "in_review",
		"approved→deprecated":  s.Status == "approved",
	}
	if !legal[s.Status+"→"+to] {
		writeError(w, http.StatusConflict, "illegal transition from "+s.Status+" to "+to)
		return
	}
	if to == "in_review" || to == "approved" {
		if _, err := h.Queries.GetLatestSpecVersion(r.Context(), s.ID); err != nil {
			writeError(w, http.StatusConflict, "spec needs at least one published version before "+to)
			return
		}
	}
	updated, err := h.Queries.UpdateSpecStatusWhereStatus(r.Context(), db.UpdateSpecStatusWhereStatusParams{
		ID:          s.ID,
		WorkspaceID: s.WorkspaceID,
		Status:      to,
		Status_2:    s.Status,
	})
	if err != nil {
		writeError(w, http.StatusConflict, "spec status changed concurrently; retry")
		return
	}
	writeJSON(w, http.StatusOK, specStateItem(updated))
}
