package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// SpecVersionResponse is one published version of a spec, embedded in the
// detail payload as `current_version`. Version publishing endpoints are owned
// by the spec state machine work; CRUD only reads the current version.
type SpecVersionResponse struct {
	ID        string `json:"id"`
	Number    int32  `json:"number"`
	Content   string `json:"content"`
	Changelog string `json:"changelog"`
	CreatedBy string `json:"created_by"`
	CreatedAt string `json:"created_at"`
}

// SpecSummaryResponse is the list-endpoint shape. It deliberately carries no
// version content; clients fetch the detail endpoint for the current version.
type SpecSummaryResponse struct {
	ID                   string  `json:"id"`
	WorkspaceID          string  `json:"workspace_id"`
	ProjectID            *string `json:"project_id"`
	Title                string  `json:"title"`
	Identifier           string  `json:"identifier"`
	Status               string  `json:"status"`
	CurrentVersionID     *string `json:"current_version_id"`
	CurrentVersionNumber int32   `json:"current_version_number"`
	CreatedAt            string  `json:"created_at"`
	UpdatedAt            string  `json:"updated_at"`
}

// SpecResponse is the detail shape: the summary fields plus the resolved
// current version, null when the spec has no published versions yet.
type SpecResponse struct {
	SpecSummaryResponse
	CurrentVersion *SpecVersionResponse `json:"current_version"`
}

func specToSummaryResponse(s db.Spec) SpecSummaryResponse {
	return SpecSummaryResponse{
		ID:               uuidToString(s.ID),
		WorkspaceID:      uuidToString(s.WorkspaceID),
		ProjectID:        uuidToPtr(s.ProjectID),
		Title:            s.Title,
		Identifier:       s.Identifier,
		Status:           s.Status,
		CurrentVersionID: uuidToPtr(s.CurrentVersionID),
		CreatedAt:        timestampToString(s.CreatedAt),
		UpdatedAt:        timestampToString(s.UpdatedAt),
	}
}

// specDetailResponse lifts a spec row into the detail shape, resolving the
// current version row when one is set. A dangling current_version_id (version
// deleted by other means) surfaces as an error instead of a null the client
// would mistake for "no versions yet".
func (h *Handler) specDetailResponse(r *http.Request, s db.Spec) (SpecResponse, error) {
	resp := SpecResponse{SpecSummaryResponse: specToSummaryResponse(s)}
	if !s.CurrentVersionID.Valid {
		return resp, nil
	}
	version, err := h.Queries.GetSpecVersion(r.Context(), db.GetSpecVersionParams{
		ID:     s.CurrentVersionID,
		SpecID: s.ID,
	})
	if err != nil {
		return SpecResponse{}, err
	}
	resp.CurrentVersionNumber = version.Number
	resp.CurrentVersion = &SpecVersionResponse{
		ID:        uuidToString(version.ID),
		Number:    version.Number,
		Content:   version.Content,
		Changelog: version.Changelog,
		CreatedBy: uuidToString(version.CreatedBy),
		CreatedAt: timestampToString(version.CreatedAt),
	}
	return resp, nil
}

type CreateSpecRequest struct {
	Title     string  `json:"title"`
	ProjectID *string `json:"project_id"`
}

type UpdateSpecRequest struct {
	Title     *string `json:"title"`
	ProjectID *string `json:"project_id"`
}

// ---------------------------------------------------------------------------
// Loader
// ---------------------------------------------------------------------------

// loadSpecForUser resolves a spec by stored identifier text (e.g. "MUL-S-1")
// or UUID, scoped to the request's workspace. Unlike issue identifiers, spec
// identifiers are stored TEXT (unique per workspace), so the lookup is an
// exact match with no prefix/number splitting.
func (h *Handler) loadSpecForUser(w http.ResponseWriter, r *http.Request, specID string) (db.Spec, bool) {
	if _, ok := requireUserID(w, r); !ok {
		return db.Spec{}, false
	}

	workspaceID := h.resolveWorkspaceID(r)
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return db.Spec{}, false
	}
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace_id")
		return db.Spec{}, false
	}

	// Identifier first: a raw UUID never collides with the "PREFIX-S-N"
	// identifier shape, but an identifier string is not a UUID, so trying the
	// cheap exact match first keeps both paths working.
	spec, err := h.Queries.GetSpecByIdentifierInWorkspace(r.Context(), db.GetSpecByIdentifierInWorkspaceParams{
		Identifier:  specID,
		WorkspaceID: wsUUID,
	})
	if err == nil {
		return spec, true
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		slog.Warn("GetSpecByIdentifierInWorkspace failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to load spec")
		return db.Spec{}, false
	}

	specUUID, err := util.ParseUUID(specID)
	if err != nil {
		// Neither an identifier nor a UUID in this workspace → 404, matching
		// the issue loader's behavior for unresolvable input.
		writeError(w, http.StatusNotFound, "spec not found")
		return db.Spec{}, false
	}
	spec, err = h.Queries.GetSpecInWorkspace(r.Context(), db.GetSpecInWorkspaceParams{
		ID:          specUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "spec not found")
		return db.Spec{}, false
	}
	return spec, true
}

// validateSpecProject checks that projectID belongs to the spec's workspace,
// mirroring UpdateIssue's project guard: a missing/foreign project is a 400,
// anything else a 500.
func (h *Handler) validateSpecProject(w http.ResponseWriter, r *http.Request, projectID pgtype.UUID, workspaceID pgtype.UUID) bool {
	if _, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{
		ID:          projectID,
		WorkspaceID: workspaceID,
	}); err != nil {
		if !isNotFound(err) {
			slog.Error("spec: validate project scope",
				append(logger.RequestAttrs(r), "project_id", uuidToString(projectID), "error", err)...)
			writeError(w, http.StatusInternalServerError, "failed to validate project")
			return false
		}
		writeError(w, http.StatusBadRequest, "project not found in this workspace")
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// Handlers — spec CRUD
// ---------------------------------------------------------------------------

func (h *Handler) ListSpecs(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}

	var projectID pgtype.UUID
	if raw := r.URL.Query().Get("project_id"); raw != "" {
		id, ok := parseUUIDOrBadRequest(w, raw, "project_id")
		if !ok {
			return
		}
		projectID = id
	}

	specs, err := h.Queries.ListSpecs(r.Context(), db.ListSpecsParams{
		WorkspaceID: wsUUID,
		ProjectID:   projectID,
	})
	if err != nil {
		slog.Warn("ListSpecs failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to list specs")
		return
	}
	resp := make([]SpecSummaryResponse, 0, len(specs))
	for _, row := range specs {
		item := specToSummaryResponse(row.Spec)
		item.CurrentVersionNumber = row.CurrentVersionNumber
		resp = append(resp, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"specs": resp, "total": len(resp)})
}

func (h *Handler) CreateSpec(w http.ResponseWriter, r *http.Request) {
	var req CreateSpecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}

	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	creatorID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var projectID pgtype.UUID
	if req.ProjectID != nil {
		pid, ok := parseUUIDOrBadRequest(w, *req.ProjectID, "project_id")
		if !ok {
			return
		}
		if !h.validateSpecProject(w, r, pid, wsUUID) {
			return
		}
		projectID = pid
	}

	// Mint the identifier inside the create transaction: the workspace row
	// lock taken by IncrementSpecCounter serializes concurrent creates, so
	// two simultaneous requests cannot allocate the same counter value.
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start spec create transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	counter, err := qtx.IncrementSpecCounter(r.Context(), wsUUID)
	if err != nil {
		slog.Warn("CreateSpec: IncrementSpecCounter failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to allocate spec identifier")
		return
	}
	prefix := h.getIssuePrefix(r.Context(), wsUUID)
	spec, err := qtx.CreateSpec(r.Context(), db.CreateSpecParams{
		WorkspaceID: wsUUID,
		ProjectID:   projectID,
		Title:       sanitizeNullBytes(title),
		Identifier:  fmt.Sprintf("%s-S-%d", prefix, counter),
		Status:      "draft",
		CreatedBy:   parseUUID(creatorID),
	})
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a spec with that identifier already exists")
			return
		}
		slog.Warn("CreateSpec failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to create spec")
		return
	}
	// The creator auto-subscribes inside the same transaction: the spec must
	// never exist without its creator watching it. (DAWE-7)
	if err := qtx.AddSpecSubscriber(r.Context(), db.AddSpecSubscriberParams{
		SpecID:   spec.ID,
		MemberID: parseUUID(creatorID),
	}); err != nil {
		slog.Warn("CreateSpec: creator auto-subscribe failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to subscribe creator")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit spec creation")
		return
	}

	actorType, actorID := h.resolveActor(r, creatorID, workspaceID)
	h.publish(protocol.EventSpecCreated, workspaceID, actorType, actorID, map[string]any{
		"spec": specToSummaryResponse(spec),
	})

	resp, err := h.specDetailResponse(r, spec)
	if err != nil {
		slog.Warn("CreateSpec: resolve detail failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to build spec response")
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (h *Handler) GetSpec(w http.ResponseWriter, r *http.Request) {
	spec, ok := h.loadSpecForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	resp, err := h.specDetailResponse(r, spec)
	if err != nil {
		slog.Warn("GetSpec: resolve current version failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to build spec response")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) UpdateSpec(w http.ResponseWriter, r *http.Request) {
	spec, ok := h.loadSpecForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	var req UpdateSpecRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// Track which fields were explicitly present in JSON (even if null).
	var rawFields map[string]json.RawMessage
	json.Unmarshal(bodyBytes, &rawFields)

	// UpdateSpec's project_id is a BARE sqlc.narg set — an omitted parameter
	// writes NULL — so pre-fill it from the loaded row to make omitted mean
	// unchanged. An explicit JSON null clears project membership, mirroring
	// UpdateIssue's project_id semantics.
	params := db.UpdateSpecParams{
		ID:          spec.ID,
		WorkspaceID: spec.WorkspaceID,
		ProjectID:   spec.ProjectID,
	}
	if req.Title != nil {
		title := strings.TrimSpace(*req.Title)
		if title == "" {
			writeError(w, http.StatusBadRequest, "title must not be empty")
			return
		}
		params.Title = strToText(sanitizeNullBytes(title))
	}
	if _, ok := rawFields["project_id"]; ok {
		if req.ProjectID != nil {
			projectUUID, ok := parseUUIDOrBadRequest(w, *req.ProjectID, "project_id")
			if !ok {
				return
			}
			if !h.validateSpecProject(w, r, projectUUID, spec.WorkspaceID) {
				return
			}
			params.ProjectID = projectUUID
		} else {
			params.ProjectID = pgtype.UUID{Valid: false} // explicit null = clear project membership
		}
	}

	updated, err := h.Queries.UpdateSpec(r.Context(), params)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "spec not found")
			return
		}
		slog.Warn("UpdateSpec failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to update spec")
		return
	}
	resp, err := h.specDetailResponse(r, updated)
	if err != nil {
		slog.Warn("UpdateSpec: resolve detail failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to build spec response")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) DeleteSpec(w http.ResponseWriter, r *http.Request) {
	spec, ok := h.loadSpecForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if spec.Status != "draft" {
		writeError(w, http.StatusConflict, "only draft specs can be deleted")
		return
	}

	// The junction tables intentionally use application-level cleanup rather
	// than database cascades; keep every dependent sweep and the catalog
	// deletion atomic so a half-deleted spec cannot survive a mid-delete error.
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start spec delete transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	// Publishing and comment creation also lock this row. Recheck the status
	// after acquiring it so deletion cannot race a transition or new comment.
	spec, err = qtx.LockSpecInWorkspace(r.Context(), db.LockSpecInWorkspaceParams{ID: spec.ID, WorkspaceID: spec.WorkspaceID})
	if err != nil {
		writeError(w, http.StatusNotFound, "spec not found")
		return
	}
	if spec.Status != "draft" {
		writeError(w, http.StatusConflict, "only draft specs can be deleted")
		return
	}
	urls, err := qtx.DeleteSpecCommentAttachments(r.Context(), db.DeleteSpecCommentAttachmentsParams{SpecID: spec.ID, WorkspaceID: spec.WorkspaceID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to remove spec attachments")
		return
	}
	if err := qtx.DeleteSpecCommentReactions(r.Context(), db.DeleteSpecCommentReactionsParams{SpecID: spec.ID, WorkspaceID: spec.WorkspaceID}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to remove spec comment reactions")
		return
	}
	if err := qtx.DeleteSpecComments(r.Context(), db.DeleteSpecCommentsParams{SpecID: spec.ID, WorkspaceID: spec.WorkspaceID}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to remove spec comments")
		return
	}

	for _, cleanup := range []func() error{
		func() error { return qtx.DeleteSpecVersionsBySpec(r.Context(), spec.ID) },
		func() error { return qtx.DeleteSpecIssuesBySpec(r.Context(), spec.ID) },
		func() error { return qtx.DeleteSpecSubscribersBySpec(r.Context(), spec.ID) },
	} {
		if err := cleanup(); err != nil {
			slog.Warn("DeleteSpec: dependent cleanup failed", append(logger.RequestAttrs(r), "error", err)...)
			writeError(w, http.StatusInternalServerError, "failed to remove spec dependents")
			return
		}
	}
	if err := qtx.DeleteSpec(r.Context(), db.DeleteSpecParams{
		ID:          spec.ID,
		WorkspaceID: spec.WorkspaceID,
	}); err != nil {
		slog.Warn("DeleteSpec failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to delete spec")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit spec deletion")
		return
	}
	h.deleteS3Objects(r.Context(), urls)
	w.WriteHeader(http.StatusNoContent)
}
