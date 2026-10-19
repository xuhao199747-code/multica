package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// projectIDsContain reports whether the resolved project id appears in the
// configuration list (canonical string form, validated at write time).
func projectIDsContain(ids []string, project pgtype.UUID) bool {
	canonical := util.UUIDToString(project)
	for _, id := range ids {
		if id == canonical {
			return true
		}
	}
	return false
}

// reviewSummaryNoteBudget bounds one summary task's handoff note so the
// queued-task broadcast stays a sane size. Reports that would exceed it are
// split into more queued tasks — never truncated.
var reviewSummaryNoteBudget = 48 * 1024

// reviewSummaryBatchReserve is counted against a fresh batch before any
// issue line: the batch header and the closing instruction must fit inside
// the same budget as the rows.
const reviewSummaryBatchReserve = 512

// TickSummaries runs one daily-summary pass over every agent with a summary
// schedule: enumerate unresolved work, dispatch a single summary run, and
// persist the local-date watermark atomically with the dispatch. The
// watermark is what makes the schedule at-least-once but not twice-per-day.
func (s *ReviewAgentService) TickSummaries(ctx context.Context) error {
	agents, err := s.Tasks.Queries.ListReviewSummaryAgents(ctx)
	if err != nil {
		return err
	}
	for _, row := range agents {
		if err = s.summaryForAgent(ctx, row, time.Now()); err != nil {
			return fmt.Errorf("review summary agent %s: %w", util.UUIDToString(row.ID), err)
		}
	}
	return nil
}

func (s *ReviewAgentService) summaryForAgent(ctx context.Context, row db.ListReviewSummaryAgentsRow, now time.Time) error {
	var cfg ReviewAgentConfig
	if json.Unmarshal(row.ReviewConfig, &cfg) != nil {
		return nil
	}
	if cfg.Validate() != nil || !cfg.SummaryEnabled {
		return nil
	}
	lastDate, err := s.Tasks.Queries.GetReviewAgentSummaryState(ctx, row.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		lastDate = ""
	}
	due, date, err := ReviewSummaryDue(now, lastDate, cfg.SummaryTime, cfg.Timezone)
	if err != nil || !due {
		return nil
	}
	tx, err := s.Tasks.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Serialize against configuration edits, same as instant dispatch.
	var locked pgtype.UUID
	if err = tx.QueryRow(ctx, `SELECT id FROM agent WHERE id=$1 AND workspace_id=$2 AND archived_at IS NULL FOR NO KEY UPDATE`, row.ID, row.WorkspaceID).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	q := s.Tasks.Queries.WithTx(tx)
	agent, err := q.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: row.ID, WorkspaceID: row.WorkspaceID})
	if err != nil {
		return err
	}
	if json.Unmarshal(agent.ReviewConfig, &cfg) != nil || cfg.Validate() != nil || !cfg.SummaryEnabled || !agent.RuntimeID.Valid {
		return nil
	}
	// The pre-lock due-ness check is only a fast path. Two app instances can
	// both pass it for the same local date, and a schedule edit can commit
	// between the pre-lock read and this lock: the watermark AND the due
	// computation must be re-validated here against the freshly-read
	// configuration. The loser sees the winner's date (or a now-not-yet-due
	// schedule) and backs off without dispatching a duplicate.
	lastDate, err = q.GetReviewAgentSummaryState(ctx, row.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		lastDate = ""
	}
	due, date, err = ReviewSummaryDue(now, lastDate, cfg.SummaryTime, cfg.Timezone)
	if err != nil || !due {
		return nil
	}
	recipient, err := util.ParseUUID(cfg.RecipientID)
	if err != nil {
		return err
	}
	if _, err = q.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{UserID: recipient, WorkspaceID: row.WorkspaceID}); err != nil {
		return nil
	}
	if err = (&IssueWakeupService{Tasks: s.Tasks}).authorize(ctx, q, row.WorkspaceID, agent.OwnerID, agent); err != nil {
		return nil
	}
	var projectIDs []pgtype.UUID
	if cfg.Scope == "projects" {
		projectIDs = make([]pgtype.UUID, 0, len(cfg.ProjectIDs))
		for _, id := range cfg.ProjectIDs {
			parsed, err := util.ParseUUID(id)
			if err != nil {
				return err
			}
			projectIDs = append(projectIDs, parsed)
		}
	}
	issues, err := q.ListReviewScopeIssues(ctx, db.ListReviewScopeIssuesParams{WorkspaceID: row.WorkspaceID, ProjectIds: projectIDs})
	if err != nil {
		return err
	}
	pending, err := q.ListPendingReviewIssues(ctx, row.ID)
	if err != nil {
		return err
	}
	byID := make(map[string]db.ListReviewScopeIssuesRow, len(issues))
	ordered := make([]db.ListReviewScopeIssuesRow, 0, len(issues))
	for _, issue := range issues {
		byID[util.UUIDToString(issue.ID)] = issue
		ordered = append(ordered, issue)
	}
	for _, issueID := range pending {
		if _, seen := byID[util.UUIDToString(issueID)]; seen {
			continue
		}
		// Pending events may reference issues outside the current status
		// window (e.g. already reviewed); load them so they are claimed
		// rather than left for the instant dispatcher to double-count. The
		// scope must be re-validated here: the events were captured under an
		// earlier configuration, and the summary must not pull an issue the
		// current project list no longer covers.
		issue, err := q.LockWakeupIssue(ctx, issueID)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if issue.WorkspaceID != row.WorkspaceID {
			continue
		}
		if cfg.Scope == "projects" && !projectIDsContain(cfg.ProjectIDs, issue.ProjectID) {
			continue
		}
		row := db.ListReviewScopeIssuesRow{ID: issue.ID, ProjectID: issue.ProjectID, Status: issue.Status, Title: issue.Title, Priority: issue.Priority, Number: issue.Number}
		byID[util.UUIDToString(issue.ID)] = row
		ordered = append(ordered, row)
	}
	// Every in-scope, unsettled issue belongs in the day's report. A cap
	// here would permanently skip the overflow while the watermark still
	// advanced — a silent miss the summary must never create.
	included := make([]db.ListReviewScopeIssuesRow, 0, len(ordered))
	blockedByActiveTask := false
	for _, issue := range ordered {
		if issue.Status == "cancelled" {
			continue
		}
		var active int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM agent_task_queue WHERE agent_id=$1 AND issue_id=$2 AND context->>'review_agent'='true' AND status IN ('queued','dispatched','running')`, row.ID, issue.ID).Scan(&active); err != nil {
			return err
		}
		if active > 0 {
			// Instant delivery already covers this issue right now.
			blockedByActiveTask = true
			continue
		}
		included = append(included, issue)
	}
	// Every in-scope, unsettled issue belongs in the day's report, and the
	// report goes out whole or not at all: a partial report combined with an
	// advanced watermark would drop the still-active issues from the day
	// forever. Wait for the active runs to settle — the next tick re-checks
	// under the lock — then deliver the complete report the same local day.
	if blockedByActiveTask {
		return nil
	}
	if len(included) == 0 {
		// No unresolved work at all: the day is genuinely empty and the
		// watermark advances.
		if err = q.UpsertReviewAgentSummaryState(ctx, db.UpsertReviewAgentSummaryStateParams{AgentID: row.ID, WorkspaceID: row.WorkspaceID, LastDate: date}); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	prefix := s.Tasks.getIssuePrefix(row.WorkspaceID)
	// One task's handoff note is bounded so the queued-task broadcast stays a
	// sane size — but coverage is never traded for size: an oversized report
	// splits into more queued tasks, one per batch, instead of dropping the
	// overflow while still advancing the watermark. Each batch reserves room
	// for its header and instruction footer up front so the written note
	// always fits the budget it was counted against.
	batches := make([][]db.ListReviewScopeIssuesRow, 0, 1)
	var current []db.ListReviewScopeIssuesRow
	currentBytes := 0
	for _, issue := range included {
		estimate := len(prefix) + 24 + len(issue.Title) + len(issue.Status) + 88
		if len(current) > 0 && currentBytes+estimate > reviewSummaryNoteBudget {
			batches = append(batches, current)
			current = nil
			currentBytes = 0
		}
		if len(current) == 0 {
			currentBytes = reviewSummaryBatchReserve
		}
		current = append(current, issue)
		currentBytes += estimate
	}
	if len(current) > 0 {
		batches = append(batches, current)
	}
	var tasks []db.AgentTaskQueue
	for i, batch := range batches {
		var note strings.Builder
		fmt.Fprintf(&note, "Daily review summary for recipient %s. Batch %d of %d: %d of %d unresolved issue(s):", cfg.RecipientID, i+1, len(batches), len(batch), len(included))
		for _, issue := range batch {
			sha, err := q.GetIssueReviewHeadSha(ctx, issue.ID)
			if errors.Is(err, pgx.ErrNoRows) {
				sha = ""
			} else if err != nil {
				return err
			}
			shaNote := sha
			if shaNote == "" {
				shaNote = "unknown"
			}
			// The identifier (PREFIX-number) is what makes a same-titled row
			// locatable; the title alone is not unique.
			fmt.Fprintf(&note, "\n- %s-%d %s [%s] (head %s)", prefix, issue.Number, issue.Title, issue.Status, shaNote)
		}
		note.WriteString("\nFor each issue read the current task, test and linked-PR state and explicitly identify missing, failed or unknown checks. Do not merge or deploy without explicit user authorization.")
		first := batch[0]
		head, err := q.GetIssueReviewHeadSha(ctx, first.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			head = ""
		} else if err != nil {
			return err
		}
		contextJSON, _ := json.Marshal(map[string]any{"review_agent": true, "review_summary": true, "review_recipient_id": cfg.RecipientID, "review_issue_count": len(batch), "review_issue_total": len(included), "review_batch": i + 1, "review_batches": len(batches)})
		task, err := q.CreateWakeupTask(ctx, db.CreateWakeupTaskParams{ID: dbid.NewV7(), AgentID: agent.ID, RuntimeID: agent.RuntimeID, IssueID: first.ID, Priority: priorityToInt(first.Priority), TriggerSummary: pgtype.Text{String: fmt.Sprintf("Daily review summary (batch %d/%d, %d issues)", i+1, len(batches), len(batch)), Valid: true}, HandoffNote: pgtype.Text{String: note.String(), Valid: true}, OriginatorUserID: agent.OwnerID, AccountableUserID: agent.OwnerID, OriginatorSource: pgtype.Text{String: "trigger_owner", Valid: true}, HeadSha: headShaText(head), WakeupContext: contextJSON})
		if err != nil {
			return err
		}
		batchIDs := make([]pgtype.UUID, 0, len(batch))
		for _, issue := range batch {
			batchIDs = append(batchIDs, issue.ID)
		}
		if _, err = tx.Exec(ctx, `UPDATE review_agent_event SET processed_at=now(),dispatched_task_id=$2 WHERE agent_id=$3 AND processed_at IS NULL AND issue_id=ANY($1::uuid[])`, batchIDs, task.ID, row.ID); err != nil {
			return err
		}
		tasks = append(tasks, task)
	}
	if err = q.UpsertReviewAgentSummaryState(ctx, db.UpsertReviewAgentSummaryStateParams{AgentID: row.ID, WorkspaceID: row.WorkspaceID, LastDate: date}); err != nil {
		return err
	}
	details := map[string]any{"agent_id": util.UUIDToString(agent.ID), "issue_count": len(included), "batch_count": len(batches), "summary_date": date}
	item, err := q.CreateInboxItem(ctx, reviewInboxParams(row.WorkspaceID, recipient, included[0].ID, fmt.Sprintf("Daily review summary: %d unresolved issue(s)", len(included)), "review_agent_summary", "info", fmt.Sprintf("Summary queued in %d run(s) covering %d issue(s).", len(batches), len(included)), details, agent.ID))
	if err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	for _, task := range tasks {
		s.Tasks.broadcastTaskEvent(ctx, protocol.EventTaskQueued, task)
		s.Tasks.NotifyTaskEnqueued(ctx, task)
	}
	s.publishReviewInbox(item, included[0].Status)
	return nil
}
