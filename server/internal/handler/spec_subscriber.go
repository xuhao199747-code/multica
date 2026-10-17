package handler

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// SpecSubscriberResponse is the JSON shape for one spec subscription.
// Unlike issue subscribers, a spec subscription is member-only: the inbox
// notification the subscription feeds is a member surface, so there is no
// user_type column to carry an agent identity.
type SpecSubscriberResponse struct {
	SpecID    string `json:"spec_id"`
	UserID    string `json:"user_id"`
	CreatedAt string `json:"created_at"`
}

func specSubscriberToResponse(s db.SpecSubscriber) SpecSubscriberResponse {
	return SpecSubscriberResponse{
		SpecID:    uuidToString(s.SpecID),
		UserID:    uuidToString(s.MemberID),
		CreatedAt: timestampToString(s.CreatedAt),
	}
}

// ListSpecSubscribers returns all members subscribed to a spec.
func (h *Handler) ListSpecSubscribers(w http.ResponseWriter, r *http.Request) {
	spec, ok := h.loadSpecForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}

	rows, err := h.Queries.ListSpecSubscribers(r.Context(), spec.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list subscribers")
		return
	}
	out := make([]SpecSubscriberResponse, len(rows))
	for i, row := range rows {
		out[i] = specSubscriberToResponse(row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"subscribers": out, "total": len(out)})
}

// SubscribeToSpec subscribes a member to a spec. The default target is the
// caller (an agent caller subscribes its underlying member, mirroring
// SubscribeToIssue's resolveActor flow); a request body may name another
// member of the same workspace. Subscribing is idempotent.
func (h *Handler) SubscribeToSpec(w http.ResponseWriter, r *http.Request) {
	spec, ok := h.loadSpecForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}

	workspaceID := uuidToString(spec.WorkspaceID)
	callerActorType, callerActorID := h.resolveActor(r, requestUserID(r), workspaceID)
	targetUserType := callerActorType
	targetUserID := callerActorID
	var req struct {
		UserID   *string `json:"user_id"`
		UserType *string `json:"user_type"`
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&req)
	}
	if req.UserID != nil && *req.UserID != "" {
		targetUserID = *req.UserID
	}
	if req.UserType != nil && *req.UserType != "" {
		targetUserType = *req.UserType
	}

	if !h.isWorkspaceEntity(r.Context(), targetUserType, targetUserID, workspaceID) {
		writeError(w, http.StatusForbidden, "target user is not a member of this workspace")
		return
	}

	if err := h.Queries.AddSpecSubscriber(r.Context(), db.AddSpecSubscriberParams{
		SpecID:   spec.ID,
		MemberID: parseUUID(targetUserID),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to subscribe")
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"subscribed": true})
}

// UnsubscribeFromSpec removes a member's subscription from a spec. The target
// resolution mirrors SubscribeToSpec; unsubscribing a non-subscription is a
// successful no-op.
func (h *Handler) UnsubscribeFromSpec(w http.ResponseWriter, r *http.Request) {
	spec, ok := h.loadSpecForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}

	workspaceID := uuidToString(spec.WorkspaceID)
	callerActorType, callerActorID := h.resolveActor(r, requestUserID(r), workspaceID)
	targetUserType := callerActorType
	targetUserID := callerActorID
	var req struct {
		UserID   *string `json:"user_id"`
		UserType *string `json:"user_type"`
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&req)
	}
	if req.UserID != nil && *req.UserID != "" {
		targetUserID = *req.UserID
	}
	if req.UserType != nil && *req.UserType != "" {
		targetUserType = *req.UserType
	}

	if !h.isWorkspaceEntity(r.Context(), targetUserType, targetUserID, workspaceID) {
		writeError(w, http.StatusForbidden, "target user is not a member of this workspace")
		return
	}

	if err := h.Queries.RemoveSpecSubscriber(r.Context(), db.RemoveSpecSubscriberParams{
		SpecID:   spec.ID,
		MemberID: parseUUID(targetUserID),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to unsubscribe")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"subscribed": false})
}
