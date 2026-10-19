package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/multica-ai/multica/server/pkg/redact"
)

// TickRepairs finds saved repair requests. Each request is claimed under a
// database row lock, and the created task and claim commit together.
func (s *ReviewAgentService) TickRepairs(ctx context.Context, workspaceID pgtype.UUID) error {
	rows, err := s.Tasks.Queries.ListDispatchableReviewRepairs(ctx, workspaceID)
	if err != nil {
		return err
	}
	var errs []error
	for _, row := range rows {
		if row.Status != "pending" && row.Status != "awaiting_rereview" {
			continue
		}
		if err := s.dispatchRepair(ctx, row.ID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *ReviewAgentService) dispatchRepair(ctx context.Context, id pgtype.UUID) error {
	tx, err := s.Tasks.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := s.Tasks.Queries.WithTx(tx)
	row, err := q.LockReviewRepair(ctx, id)
	if err != nil {
		return err
	}
	if row.Status != "pending" && row.Status != "awaiting_rereview" {
		return nil
	}
	reviewer, err := q.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: row.ReviewAgentID, WorkspaceID: row.WorkspaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return s.blockRepair(ctx, tx, q, row, "The review agent is no longer available.")
	}
	if err != nil {
		return err
	}
	var cfg ReviewAgentConfig
	if json.Unmarshal(reviewer.ReviewConfig, &cfg) != nil || cfg.Validate() != nil || !cfg.InstantEnabled || cfg.RecipientID != util.UUIDToString(row.RecipientID) {
		return s.blockRepair(ctx, tx, q, row, "Review settings changed or automatic review was disabled; user action is required.")
	}
	issue, err := q.GetIssue(ctx, row.IssueID)
	if err != nil {
		return err
	}
	if issue.Status == "cancelled" || issue.Status == "done" {
		return s.blockRepair(ctx, tx, q, row, "The issue was closed before automatic work could continue.")
	}
	if issue.Status == "blocked" {
		return s.blockRepair(ctx, tx, q, row, "The issue is blocked; user action is required before automatic work can continue.")
	}
	if issue.Status != "in_review" && issue.Status != "in_progress" {
		return s.blockRepair(ctx, tx, q, row, "The issue status changed; user action is required before automatic work can continue.")
	}
	if cfg.Scope == "projects" {
		inScope := false
		for _, project := range cfg.ProjectIDs {
			if project == util.UUIDToString(issue.ProjectID) {
				inScope = true
				break
			}
		}
		if !inScope {
			return s.blockRepair(ctx, tx, q, row, "The issue is no longer in the review scope.")
		}
	}
	if row.Status == "awaiting_rereview" {
		return s.dispatchRereview(ctx, tx, q, row)
	}
	original, err := q.GetReviewRepairExecutorTask(ctx, db.GetReviewRepairExecutorTaskParams{IssueID: row.IssueID, ExcludeAgentID: row.ReviewAgentID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return s.blockRepair(ctx, tx, q, row, "No eligible original execution agent found; user action is required.")
		}
		return err
	}
	agent, err := q.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: original.AgentID, WorkspaceID: row.WorkspaceID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return s.blockRepair(ctx, tx, q, row, "The original execution agent is no longer available.")
		}
		return err
	}
	if err := (&IssueWakeupService{Tasks: s.Tasks}).authorize(ctx, q, row.WorkspaceID, row.RecipientID, agent); err != nil {
		if errors.Is(err, ErrWakeupForbidden) {
			return s.blockRepair(ctx, tx, q, row, "The assigned agent is unavailable or the recipient cannot invoke it.")
		}
		return err
	}
	var acquired bool
	if err := tx.QueryRow(ctx, `SELECT lock_task_owner_rows($1,$2,$3)`, agent.ID, row.IssueID, agent.RuntimeID).Scan(&acquired); err != nil {
		return err
	}
	if !acquired {
		return nil
	}
	var active int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM agent_task_queue WHERE agent_id=$1 AND issue_id=$2 AND status IN ('queued','dispatched','running')`, agent.ID, row.IssueID).Scan(&active); err != nil {
		return err
	}
	if active > 0 {
		return nil
	}
	var verdict reviewRepairVerdict
	if err := json.Unmarshal(row.Findings, &verdict); err != nil {
		return err
	}
	contextJSON, err := json.Marshal(map[string]any{"review_repair": true, "review_repair_id": util.UUIDToString(row.ID), "review_repair_round": row.Round})
	if err != nil {
		return err
	}
	task, err := q.CreateWakeupTask(ctx, db.CreateWakeupTaskParams{ID: dbid.NewV7(), AgentID: agent.ID, RuntimeID: agent.RuntimeID, IssueID: issue.ID, Priority: priorityToInt(issue.Priority), TriggerSummary: pgtype.Text{String: "Repair review findings", Valid: true}, HandoffNote: pgtype.Text{String: fmt.Sprintf("Fix the review findings, run relevant tests, and report changes, test results and blockers. Do not merge or deploy. Findings: %s", verdict.Findings), Valid: true}, OriginatorUserID: row.RecipientID, AccountableUserID: row.RecipientID, OriginatorSource: pgtype.Text{String: "trigger_owner", Valid: true}, DelegatedFromTaskID: original.ID, WakeupContext: contextJSON})
	if err != nil {
		return err
	}
	count, err := q.MarkReviewRepairDispatched(ctx, db.MarkReviewRepairDispatchedParams{ID: row.ID, RepairTaskID: task.ID})
	if err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("repair dispatch claim lost")
	}
	item, err := q.CreateInboxItem(ctx, reviewInboxParams(row.WorkspaceID, row.RecipientID, row.IssueID, issue.Title, "review_repair_queued", "info", "Repair task queued.", map[string]any{"task_id": util.UUIDToString(task.ID), "review_repair_id": util.UUIDToString(row.ID)}, agent.ID))
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.publishReviewInbox(item, issue.Status)
	s.Tasks.broadcastTaskEvent(ctx, protocol.EventTaskQueued, task)
	s.Tasks.NotifyTaskEnqueued(ctx, task)
	return nil
}

func (s *ReviewAgentService) dispatchRereview(ctx context.Context, tx pgx.Tx, q *db.Queries, row db.ReviewRepair) error {
	agent, err := q.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: row.ReviewAgentID, WorkspaceID: row.WorkspaceID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return s.blockRepair(ctx, tx, q, row, "The review agent is no longer available for re-review.")
		}
		return err
	}
	if err := (&IssueWakeupService{Tasks: s.Tasks}).authorize(ctx, q, row.WorkspaceID, row.RecipientID, agent); err != nil {
		if errors.Is(err, ErrWakeupForbidden) {
			return s.blockRepair(ctx, tx, q, row, "The review agent is unavailable or the recipient cannot invoke it.")
		}
		return err
	}
	issue, err := q.GetIssue(ctx, row.IssueID)
	if err != nil {
		return err
	}
	contextJSON, err := json.Marshal(map[string]any{"review_agent": true, "review_recipient_id": util.UUIDToString(row.RecipientID), "review_repair_round": row.Round, "review_repair_id": util.UUIDToString(row.ID)})
	if err != nil {
		return err
	}
	task, err := q.CreateWakeupTask(ctx, db.CreateWakeupTaskParams{ID: dbid.NewV7(), AgentID: agent.ID, RuntimeID: agent.RuntimeID, IssueID: issue.ID, Priority: priorityToInt(issue.Priority), TriggerSummary: pgtype.Text{String: "Re-review repaired issue", Valid: true}, HandoffNote: pgtype.Text{String: "Review the repair and its tests. Return exactly one JSON object with exactly two fields, for example {\"verdict\":\"pass\",\"findings\":\"Evidence and test results.\"}. verdict must be pass, needs_changes, or needs_decision. findings must be one JSON string, never an array or object. Do not include markdown or other text. Do not merge or deploy.", Valid: true}, OriginatorUserID: row.RecipientID, AccountableUserID: row.RecipientID, OriginatorSource: pgtype.Text{String: "trigger_owner", Valid: true}, DelegatedFromTaskID: row.RepairTaskID, WakeupContext: contextJSON})
	if err != nil {
		return err
	}
	n, err := q.MarkReviewRepairRereviewDispatched(ctx, db.MarkReviewRepairRereviewDispatchedParams{ID: row.ID, RereviewTaskID: task.ID})
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("rereview dispatch claim lost")
	}
	item, err := q.CreateInboxItem(ctx, reviewInboxParams(row.WorkspaceID, row.RecipientID, row.IssueID, issue.Title, "review_repair_rereview", "info", "Repair finished; re-review queued.", map[string]any{"task_id": util.UUIDToString(task.ID), "review_repair_id": util.UUIDToString(row.ID)}, agent.ID))
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.publishReviewInbox(item, issue.Status)
	s.Tasks.broadcastTaskEvent(ctx, protocol.EventTaskQueued, task)
	s.Tasks.NotifyTaskEnqueued(ctx, task)
	return nil
}

// recordRereviewFailureTx settles a terminal re-review from a bulk failure path.
func (s *ReviewAgentService) recordRereviewFailureTx(ctx context.Context, q *db.Queries, task db.AgentTaskQueue) (*db.InboxItem, string, error) {
	var metadata struct {
		Review bool   `json:"review_agent"`
		ID     string `json:"review_repair_id"`
	}
	if json.Unmarshal(task.Context, &metadata) != nil || !metadata.Review || metadata.ID == "" {
		return nil, "", nil
	}
	id, err := util.ParseUUID(metadata.ID)
	if err != nil {
		return nil, "", err
	}
	row, err := q.LockReviewRepair(ctx, id)
	if err != nil {
		return nil, "", err
	}
	if row.Status != "rereview_dispatched" || row.RereviewTaskID != task.ID {
		return nil, "", nil
	}
	return s.reviewResultInboxTx(ctx, q, task, nil, true, task.Error.String, task.FailureReason.String)
}

func (s *ReviewAgentService) blockRepair(ctx context.Context, tx pgx.Tx, q *db.Queries, row db.ReviewRepair, reason string) error {
	_, err := q.TransitionReviewRepair(ctx, db.TransitionReviewRepairParams{ID: row.ID, FromStatus: row.Status, ToStatus: "blocked", BlockedReason: pgtype.Text{String: reason, Valid: true}})
	if err != nil {
		return err
	}
	issue, err := q.GetIssue(ctx, row.IssueID)
	if err != nil {
		return err
	}
	item, err := q.CreateInboxItem(ctx, reviewInboxParams(row.WorkspaceID, row.RecipientID, row.IssueID, issue.Title, "review_repair_blocked", "attention", reason, map[string]any{"review_repair_id": util.UUIDToString(row.ID)}, row.ReviewAgentID))
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.publishReviewInbox(item, issue.Status)
	return nil
}

// transitionRepairIssueTx only reverses a status change made by this repair
// when no intervening issue update has changed its revision.
func (s *ReviewAgentService) transitionRepairIssueTx(ctx context.Context, q *db.Queries, task db.AgentTaskQueue, started bool) (*db.Issue, error) {
	var metadata struct {
		Repair bool   `json:"review_repair"`
		ID     string `json:"review_repair_id"`
	}
	if json.Unmarshal(task.Context, &metadata) != nil || !metadata.Repair {
		return nil, nil
	}
	id, err := util.ParseUUID(metadata.ID)
	if err != nil {
		return nil, err
	}
	row, err := q.LockReviewRepair(ctx, id)
	if err != nil {
		return nil, err
	}
	if row.Status != "dispatched" || row.RepairTaskID != task.ID {
		return nil, nil
	}
	issue, err := q.LockWakeupIssue(ctx, row.IssueID)
	if err != nil {
		return nil, err
	}
	status := "in_review"
	if started {
		if row.IssueRevision.Valid || issue.Status != "in_review" || issue.AssigneeType.String != "agent" || issue.AssigneeID != task.AgentID {
			return nil, nil
		}
		status = "in_progress"
	} else if !row.IssueRevision.Valid || issue.Status != "in_progress" || issue.Revision != row.IssueRevision.Int64 {
		return nil, nil
	}
	updated, err := q.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{ID: issue.ID, WorkspaceID: issue.WorkspaceID, Status: status, SourceTaskID: task.ID})
	if err != nil {
		return nil, err
	}
	if started {
		if err := q.SetReviewRepairIssueRevision(ctx, db.SetReviewRepairIssueRevisionParams{ID: row.ID, IssueRevision: pgtype.Int8{Int64: updated.Revision, Valid: true}}); err != nil {
			return nil, err
		}
	}
	return &updated, nil
}

// recordRepairFailureTx records a terminal repair failure with its notification.
func (s *ReviewAgentService) recordRepairFailureTx(ctx context.Context, q *db.Queries, task db.AgentTaskQueue, reason string) (*db.InboxItem, string, error) {
	var metadata struct {
		Repair bool   `json:"review_repair"`
		ID     string `json:"review_repair_id"`
	}
	if json.Unmarshal(task.Context, &metadata) != nil || !metadata.Repair {
		return nil, "", nil
	}
	id, err := util.ParseUUID(metadata.ID)
	if err != nil {
		return nil, "", err
	}
	row, err := q.LockReviewRepair(ctx, id)
	if err != nil {
		return nil, "", err
	}
	if row.Status != "dispatched" || row.RepairTaskID != task.ID {
		return nil, "", nil
	}
	reason = redact.Text(reason)
	if reason == "" {
		reason = "Repair task failed."
	}
	_, err = q.TransitionReviewRepair(ctx, db.TransitionReviewRepairParams{ID: row.ID, FromStatus: row.Status, ToStatus: "failed", BlockedReason: pgtype.Text{String: reason, Valid: true}})
	if err != nil {
		return nil, "", err
	}
	issue, err := q.GetIssue(ctx, row.IssueID)
	if err != nil {
		return nil, "", err
	}
	item, err := q.CreateInboxItem(ctx, reviewInboxParams(row.WorkspaceID, row.RecipientID, row.IssueID, issue.Title, "review_repair_failed", "attention", reason, map[string]any{"review_repair_id": metadata.ID}, task.AgentID))
	if err != nil {
		return nil, "", err
	}
	return &item, issue.Status, nil
}
