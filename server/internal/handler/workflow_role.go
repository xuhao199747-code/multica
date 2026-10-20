package handler

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/util"
)

// workflowActorMayCoordinate keeps role enforcement opt-in per agent while
// existing workspaces migrate. Agents without a configured workflow role keep
// their legacy behaviour; configured execution, PRD, and review agents may
// never create or assign work.
func (h *Handler) workflowActorMayCoordinate(ctx context.Context, actorType, actorID string) bool {
	if actorType != "agent" {
		return true
	}
	id, err := util.ParseUUID(actorID)
	if err != nil {
		return false
	}
	var role string
	err = h.DB.QueryRow(ctx, `SELECT role FROM agent_workflow_role WHERE agent_id = $1`, id).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return true
	}
	if err != nil {
		return false
	}
	return role == "coordinator"
}
