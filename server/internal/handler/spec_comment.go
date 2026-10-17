package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/logger"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// SpecCommentRequest is the body of POST /api/specs/{id}/versions/{versionId}/comments.
// It mirrors CreateCommentRequest minus the agent-trigger controls: spec
// comments never enqueue runs, so there is nothing to suppress or steer.
type SpecCommentRequest struct {
	Content       string   `json:"content"`
	Type          string   `json:"type"`
	ParentID      *string  `json:"parent_id"`
	AttachmentIDs []string `json:"attachment_ids"`
}

// loadSpecVersionForUser resolves the {id}/{versionId} URL pair to a spec
// version in the caller's workspace, mirroring how loadSpecForUser gates spec
// CRUD. The spec loader already wrote any auth/404 response on failure.
func (h *Handler) loadSpecVersionForUser(w http.ResponseWriter, r *http.Request) (db.Spec, db.SpecVersion, bool) {
	spec, ok := h.loadSpecForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return db.Spec{}, db.SpecVersion{}, false
	}
	versionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "number"), "version id")
	if !ok {
		return db.Spec{}, db.SpecVersion{}, false
	}
	version, err := h.Queries.GetSpecVersion(r.Context(), db.GetSpecVersionParams{
		ID:     versionID,
		SpecID: spec.ID,
	})
	if err != nil {
		// Unknown id, or a version that belongs to a different spec.
		writeError(w, http.StatusNotFound, "spec version not found")
		return db.Spec{}, db.SpecVersion{}, false
	}
	return spec, version, true
}

func (h *Handler) CreateSpecComment(w http.ResponseWriter, r *http.Request) {
	r = h.withWakeupActor(r)
	spec, version, ok := h.loadSpecVersionForUser(w, r)
	if !ok {
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var req SpecCommentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// See CreateComment: strip NUL / invalid-UTF-8 bytes PostgreSQL's TEXT
	// column rejects before the empty check.
	req.Content = sanitizeNullBytes(req.Content)
	if req.Content == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}
	if req.Type == "" {
		req.Type = "comment"
	}
	if !isClientAuthorableCommentType(req.Type) {
		writeError(w, http.StatusBadRequest, "invalid comment type")
		return
	}

	// Anchor validation: a reply's parent must live on the SAME spec version.
	// A thread cannot span anchors, so parentage is checked against the
	// version, not just the workspace (DAWE-8).
	var parentID pgtype.UUID
	if req.ParentID != nil {
		parsed, ok := parseUUIDOrBadRequest(w, *req.ParentID, "parent_id")
		if !ok {
			return
		}
		parentID = parsed
		parent, err := h.Queries.GetCommentInWorkspace(r.Context(), db.GetCommentInWorkspaceParams{
			ID:          parentID,
			WorkspaceID: spec.WorkspaceID,
		})
		if err != nil || parent.SpecVersionID != version.ID {
			writeError(w, http.StatusBadRequest, "invalid parent comment")
			return
		}
	}

	attachmentIDs, ok := parseUUIDSliceOrBadRequest(w, req.AttachmentIDs, "attachment_ids")
	if !ok {
		return
	}

	authorType, authorID := h.resolveActor(r, userID, uuidToString(spec.WorkspaceID))

	// Thread-level behavior (auto-unresolve on reply) keys off the root, same
	// as issue comments. GetThreadRoot walks parent_id under workspace scope
	// only, so it is anchor-agnostic.
	var rootComment *db.Comment
	if parentID.Valid {
		if root, err := h.Queries.GetThreadRoot(r.Context(), db.GetThreadRootParams{
			CommentID:   parentID,
			WorkspaceID: spec.WorkspaceID,
		}); err == nil {
			rootComment = &root
		}
	}

	createParams := db.CreateSpecVersionCommentParams{
		ID:            dbid.NewV7(),
		SpecID:        spec.ID,
		SpecVersionID: version.ID,
		WorkspaceID:   spec.WorkspaceID,
		AuthorType:    authorType,
		AuthorID:      parseUUID(authorID),
		Content:       req.Content,
		Type:          req.Type,
		ParentID:      parentID,
	}
	var created db.CreateSpecVersionCommentRow
	var err error
	if len(attachmentIDs) > 0 {
		// Mirror the issue path: comment + attachment link is one transaction,
		// locked spec -> comment -> child. A requested attachment deleted while
		// the spec lock was contended is refused before the comment commits.
		tx, beginErr := h.beginWakeupWrite(r.Context())
		if beginErr != nil {
			slog.Warn("create spec comment failed", append(logger.RequestAttrs(r), "error", beginErr, "spec_id", uuidToString(spec.ID))...)
			writeError(w, http.StatusInternalServerError, "failed to create comment: "+beginErr.Error())
			return
		}
		defer tx.Rollback(r.Context())
		qtx := h.Queries.WithTx(tx)
		created, err = qtx.CreateSpecVersionComment(r.Context(), createParams)
		if err == nil {
			var missing pgtype.UUID
			missing, err = lockSpecCommentAttachments(r.Context(), qtx, spec.WorkspaceID, attachmentIDs)
			if err == nil && missing.Valid {
				writeError(w, http.StatusConflict, "attachment "+uuidToString(missing)+" is no longer available")
				return
			}
		}
		if err == nil {
			err = qtx.LinkAttachmentsToSpecComment(r.Context(), db.LinkAttachmentsToSpecCommentParams{
				CommentID:     created.ID,
				WorkspaceID:   spec.WorkspaceID,
				AttachmentIds: attachmentIDs,
			})
		}
		if err == nil {
			err = tx.Commit(r.Context())
		}
	} else {
		created, err = wakeupWrite(h, r, func(q *db.Queries) (db.CreateSpecVersionCommentRow, error) {
			return q.CreateSpecVersionComment(r.Context(), createParams)
		})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		// Spec or version vanished (or never matched this workspace).
		writeError(w, http.StatusNotFound, "spec version not found")
		return
	}
	if err != nil {
		slog.Warn("create spec comment failed", append(logger.RequestAttrs(r), "error", err, "spec_id", uuidToString(spec.ID))...)
		writeError(w, http.StatusInternalServerError, "failed to create comment: "+err.Error())
		return
	}
	comment := created.Comment()

	groupedAtt := h.groupAttachments(r, []pgtype.UUID{comment.ID})
	resp := commentToResponse(comment, nil, groupedAtt[uuidToString(comment.ID)])
	slog.Info("spec comment created", append(logger.RequestAttrs(r), "comment_id", uuidToString(comment.ID), "spec_id", uuidToString(spec.ID), "spec_version_id", uuidToString(version.ID))...)
	// No issue_* keys: listeners branch on their absence to route spec-comment
	// notifications through the spec subscriber list instead of the issue one.
	h.publish(protocol.EventCommentCreated, uuidToString(spec.WorkspaceID), authorType, authorID, map[string]any{
		"comment":             resp,
		"spec_id":             uuidToString(spec.ID),
		"spec_version_id":     uuidToString(version.ID),
		"spec_identifier":     spec.Identifier,
		"spec_title":          spec.Title,
		"spec_version_number": version.Number,
	})

	// A reply in a resolved thread re-opens it, exactly like issue comments.
	h.TaskService.AutoUnresolveThreadOnReply(r.Context(), rootComment, uuidToString(spec.WorkspaceID), authorType, authorID, h.wakeupSourceTaskID(r))

	writeJSON(w, http.StatusCreated, resp)
}

func (h *Handler) ListSpecVersionComments(w http.ResponseWriter, r *http.Request) {
	spec, version, ok := h.loadSpecVersionForUser(w, r)
	if !ok {
		return
	}
	comments, err := h.Queries.ListCommentsForSpecVersion(r.Context(), db.ListCommentsForSpecVersionParams{
		SpecVersionID: version.ID,
		WorkspaceID:   spec.WorkspaceID,
	})
	if err != nil {
		slog.Warn("list spec version comments failed", append(logger.RequestAttrs(r), "error", err, "spec_version_id", uuidToString(version.ID))...)
		writeError(w, http.StatusInternalServerError, "failed to list comments")
		return
	}
	resp := h.specCommentListResponse(r, comments)
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) ListSpecComments(w http.ResponseWriter, r *http.Request) {
	spec, ok := h.loadSpecForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	rows, err := h.Queries.ListCommentsForSpec(r.Context(), db.ListCommentsForSpecParams{
		SpecID:      spec.ID,
		WorkspaceID: spec.WorkspaceID,
	})
	if err != nil {
		slog.Warn("list spec comments failed", append(logger.RequestAttrs(r), "error", err, "spec_id", uuidToString(spec.ID))...)
		writeError(w, http.StatusInternalServerError, "failed to list comments")
		return
	}
	comments := make([]db.Comment, 0, len(rows))
	numbers := make(map[string]int32, len(rows))
	for _, row := range rows {
		comments = append(comments, row.Comment())
		numbers[uuidToString(row.ID)] = row.SpecVersionNumber
	}
	resp := h.specCommentListResponse(r, comments)
	for i := range resp {
		resp[i].SpecVersionNumber = int(numbers[resp[i].ID])
	}
	writeJSON(w, http.StatusOK, resp)
}

// specCommentListResponse maps comment rows to wire responses with reactions
// and attachments grouped in one query each, mirroring the issue list shape.
func (h *Handler) specCommentListResponse(r *http.Request, comments []db.Comment) []CommentResponse {
	ids := make([]pgtype.UUID, 0, len(comments))
	for _, c := range comments {
		ids = append(ids, c.ID)
	}
	grouped := h.groupReactions(r, ids)
	groupedAtt := h.groupAttachments(r, ids)
	resp := make([]CommentResponse, 0, len(comments))
	for _, c := range comments {
		cid := uuidToString(c.ID)
		resp = append(resp, commentToResponse(c, grouped[cid], groupedAtt[cid]))
	}
	return resp
}

// lockSpecCommentAttachments is the spec-side sibling of
// lockCommentAttachments: it pins the requested unbound attachment set and
// reports the first id that is not among them. The caller already holds the
// spec row lock taken by CreateSpecVersionComment.
func lockSpecCommentAttachments(ctx context.Context, qtx *db.Queries, workspaceID pgtype.UUID, ids []pgtype.UUID) (missing pgtype.UUID, err error) {
	locked, err := qtx.LockAttachmentsForSpecCommentLink(ctx, db.LockAttachmentsForSpecCommentLinkParams{
		WorkspaceID:   workspaceID,
		AttachmentIds: ids,
	})
	if err != nil {
		return pgtype.UUID{}, err
	}
	have := make(map[pgtype.UUID]struct{}, len(locked))
	for _, id := range locked {
		have[id] = struct{}{}
	}
	for _, id := range ids {
		if _, ok := have[id]; !ok {
			return id, nil
		}
	}
	return pgtype.UUID{}, nil
}

// touchSpecForComment bumps the owning spec's updated_at after a spec comment
// is edited or deleted — the activity parity the issue touch statements give
// issue comments. Best-effort: a failure is logged and swallowed, matching how
// DeleteComment treats a missing parent issue.
func (h *Handler) touchSpecForComment(ctx context.Context, comment db.Comment) {
	if !comment.SpecVersionID.Valid {
		return
	}
	specID, err := h.Queries.GetSpecVersionSpec(ctx, comment.SpecVersionID)
	if err != nil {
		slog.Warn("touch spec for comment: load version failed", "comment_id", uuidToString(comment.ID), "error", err)
		return
	}
	if _, err := h.Queries.TouchSpecForComment(ctx, db.TouchSpecForCommentParams{
		SpecID:      specID,
		WorkspaceID: comment.WorkspaceID,
	}); err != nil {
		slog.Warn("touch spec for comment failed", "comment_id", uuidToString(comment.ID), "spec_id", uuidToString(specID), "error", err)
	}
}
