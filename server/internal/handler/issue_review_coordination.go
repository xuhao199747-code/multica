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

// mikaBlockedCoordinationHandoff is used when a project-workflow issue fails or
// stalls instead of reaching review: the run died (provider/auth, environment
// prepare) or the issue is blocked, and no coordinator is watching it because
// the legacy workspace-wide path deliberately skips workflow-owned issues.
const mikaBlockedCoordinationHandoff = `This project-workflow issue has failed or stalled without reaching review. Diagnose the cause (a failed/blocked execution, provider auth, or environment preparation), restart or reroute the work, and report the concrete next step. Do not mark it done or in_review until delivery evidence is submitted through the workflow delivery command. Escalate only a decision that requires the member.`

// enqueueMikaReviewCoordination gives Mika an actual task when an issue first
// enters review. The issue remains assigned to its executor: Mika is an
// additional coordinator run, not a reassignment that would erase delivery
// ownership or make the reviewer look like the code author.
func (h *Handler) enqueueMikaReviewCoordination(ctx context.Context, issue, previous db.Issue, actorType, actorID string) {
	h.enqueueMikaCoordination(ctx, issue, previous, actorType, actorID, false)
}

// enqueueMikaEvidenceGapCoordination wakes Mika when the evidence guard
// refused a bare status flip on a workflow issue. The issue never actually
// moved, so the usual "first transition into the state" dedup must not
// suppress this run: the gap itself is the trigger.
func (h *Handler) enqueueMikaEvidenceGapCoordination(ctx context.Context, issue db.Issue, actorType, actorID string) {
	h.enqueueMikaCoordination(ctx, issue, db.Issue{ID: issue.ID, WorkspaceID: issue.WorkspaceID, Status: "todo"}, actorType, actorID, true)
}

func (h *Handler) enqueueMikaCoordination(ctx context.Context, issue, previous db.Issue, actorType, actorID string, forceEvidenceGap bool) {
	eff := issuestatus.Effective(ctx, h.Queries, issue.WorkspaceID, issue.Status)
	prevEff := issuestatus.Effective(ctx, h.Queries, previous.WorkspaceID, previous.Status)
	isWorkflow := false
	if _, err := h.Queries.GetProjectExecutionIssueByIssue(ctx, db.GetProjectExecutionIssueByIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	}); err == nil {
		isWorkflow = true
	} else if !errors.Is(err, pgx.ErrNoRows) {
		slog.Warn("review coordination: check project workflow issue failed", "issue_id", uuidToString(issue.ID), "error", err)
		return
	}

	handoff := ""
	switch {
	case forceEvidenceGap && isWorkflow:
		handoff = mikaBlockedCoordinationHandoff
	case eff == issuestatus.InReview && prevEff != issuestatus.InReview && !isWorkflow:
		// Plain issue entering review: coordinate review. Workflow-owned review
		// entries advance through the workflow's own delivery/review commands.
		handoff = mikaReviewCoordinationHandoff
	case isWorkflow && (eff == issuestatus.InReview && prevEff != issuestatus.InReview):
		// Workflow issue entered review without a delivery command — that is the
		// evidence-gap defect. Coordinate to collect real evidence before review.
		handoff = mikaReviewCoordinationHandoff
	case isWorkflow && eff == issuestatus.Blocked && prevEff != issuestatus.Blocked:
		handoff = mikaBlockedCoordinationHandoff
	}
	if handoff == "" {
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
	if _, err := h.TaskService.EnqueueTaskForIssueWithHandoff(ctx, coordinationIssue, handoff, memberActorUserID(actorType, actorID)); err != nil && !errors.Is(err, service.ErrDuplicatePendingTask) {
		slog.Warn("review coordination: enqueue Mika failed", "issue_id", uuidToString(issue.ID), "mika_agent_id", uuidToString(mika.ID), "error", err)
	}
}
