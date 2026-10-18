package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func marshalReviewConfig(config *service.ReviewAgentConfig) []byte {
	if config == nil {
		return nil
	}
	encoded, _ := json.Marshal(config)
	return encoded
}

func (h *Handler) validateReviewConfig(w http.ResponseWriter, r *http.Request, workspaceID pgtype.UUID, config *service.ReviewAgentConfig) bool {
	if config == nil {
		return true
	}
	if err := config.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return false
	}
	recipient, ok := parseUUIDOrBadRequest(w, config.RecipientID, "recipient_id")
	if !ok {
		return false
	}
	_, err := h.Queries.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{UserID: recipient, WorkspaceID: workspaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusBadRequest, "recipient must belong to the workspace")
		return false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to validate recipient")
		return false
	}
	for _, projectID := range config.ProjectIDs {
		id, ok := parseUUIDOrBadRequest(w, projectID, "project_id")
		if !ok {
			return false
		}
		_, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{ID: id, WorkspaceID: workspaceID})
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusBadRequest, "project must belong to the workspace")
			return false
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to validate project")
			return false
		}
	}
	return true
}
