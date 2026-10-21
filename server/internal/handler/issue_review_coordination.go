package handler

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/issuestatus"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const mikaReviewCoordinationHandoff = `This issue has just entered review after an execution run. Own the workflow: inspect the delivered evidence, start the workspace review agent, and then follow the result through repair and re-review when changes are needed. Do not merely summarize the status. Escalate only a decision that requires the member.`

// enqueueMikaReviewCoordination gives Mika an actual task when an issue first
// enters review. The issue remains assigned to its executor: Mika is an
// additional coordinator run, not a reassignment that would erase delivery
// ownership or make the reviewer look like the code author.
func (h *Handler) enqueueMikaReviewCoordination(ctx context.Context, issue, previous db.Issue, actorType, actorID string) {
	if issuestatus.Effective(ctx, h.Queries, issue.WorkspaceID, issue.Status) != "in_review" ||
		issuestatus.Effective(ctx, h.Queries, previous.WorkspaceID, previous.Status) == "in_review" {
		return
	}
	// Project workflow executions own review through their immutable snapshot
	// and bounded state machine. Letting the legacy workspace-wide coordinator
	// also enqueue Mika here would create a second, unscoped path into review.
	if _, err := h.Queries.GetProjectExecutionIssueByIssue(ctx, db.GetProjectExecutionIssueByIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	}); err == nil {
		return
	} else if !errors.Is(err, pgx.ErrNoRows) {
		slog.Warn("review coordination: check project workflow issue failed", "issue_id", uuidToString(issue.ID), "error", err)
		return
	}

	mika, err := h.Queries.GetAgentBySystemKey(ctx, db.GetAgentBySystemKeyParams{
		WorkspaceID: issue.WorkspaceID,
		SystemKey:   pgtype.Text{String: service.MikaSystemKey, Valid: true},
	})
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("review coordination: load Mika failed", "issue_id", uuidToString(issue.ID), "error", err)
		}
		return
	}
	// A Mika-owned issue already has a coordinating execution context. Do not
	// generate a self-loop when Mika itself marks it in review.
	if issue.AssigneeID.Valid && issue.AssigneeID == mika.ID {
		return
	}

	coordinationIssue := issue
	coordinationIssue.AssigneeType = pgtype.Text{String: "agent", Valid: true}
	coordinationIssue.AssigneeID = mika.ID
	if _, err := h.TaskService.EnqueueTaskForIssueWithHandoff(ctx, coordinationIssue, mikaReviewCoordinationHandoff, memberActorUserID(actorType, actorID)); err != nil && !errors.Is(err, service.ErrDuplicatePendingTask) {
		slog.Warn("review coordination: enqueue Mika failed", "issue_id", uuidToString(issue.ID), "mika_agent_id", uuidToString(mika.ID), "error", err)
	}
}
