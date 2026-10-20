package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type ReviewAgentService struct{ Tasks *TaskService }

type reviewCandidate struct{ ID, WorkspaceID, AgentID, IssueID pgtype.UUID }

func (s *ReviewAgentService) Tick(ctx context.Context) error {
	return errors.Join(s.TickWorkspace(ctx, pgtype.UUID{}), s.TickRepairs(ctx, pgtype.UUID{}))
}

// TickWorkspace bounds each scan; a workspace argument allows isolated tests.
func (s *ReviewAgentService) TickWorkspace(ctx context.Context, ws pgtype.UUID) error {
	if err := s.reconcileInReview(ctx, ws); err != nil {
		return err
	}
	tx, err := s.Tasks.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	// Scan only rows that can dispatch right now. Events sitting behind an
	// active review task or the per-issue hourly rate cap would otherwise
	// occupy the scan window forever and starve processable events behind
	// them; they stay pending and become visible once the gate clears.
	rows, err := tx.Query(ctx, `SELECT e.id,e.workspace_id,e.agent_id,e.issue_id FROM review_agent_event e WHERE e.processed_at IS NULL AND ($1::uuid IS NULL OR e.workspace_id=$1)
	 AND NOT EXISTS (SELECT 1 FROM agent_task_queue t WHERE t.agent_id=e.agent_id AND t.issue_id=e.issue_id AND t.context->>'review_agent'='true' AND t.status IN ('queued','dispatched','running'))
	 AND (SELECT count(*) FROM agent_task_queue r WHERE r.agent_id=e.agent_id AND r.issue_id=e.issue_id AND r.context->>'review_agent'='true' AND r.created_at>now()-interval '1 hour') < 6
	 ORDER BY e.created_at,e.id LIMIT 100`, ws)
	if err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	var candidates []reviewCandidate
	for rows.Next() {
		var c reviewCandidate
		if err = rows.Scan(&c.ID, &c.WorkspaceID, &c.AgentID, &c.IssueID); err != nil {
			break
		}
		candidates = append(candidates, c)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	_ = tx.Rollback(ctx)
	if err != nil {
		return err
	}
	for _, c := range candidates {
		if dispatchErr := s.dispatch(ctx, c); dispatchErr != nil {
			// One bad candidate must not starve the candidates queued behind
			// it; the event stays pending and is retried on the next tick.
			err = errors.Join(err, fmt.Errorf("review event %s: %w", util.UUIDToString(c.ID), dispatchErr))
		}
	}
	return err
}

// reconcileInReview recovers the handoff that is otherwise missed when an
// issue entered review before its reviewer was configured. It intentionally
// creates only the first review for an issue: later review cycles are driven
// by the status-change trigger and the repair workflow, so this must not turn
// into a periodic re-review loop.
func (s *ReviewAgentService) reconcileInReview(ctx context.Context, ws pgtype.UUID) error {
	tx, err := s.Tasks.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO review_agent_event(workspace_id,agent_id,issue_id,event_key,event_type)
	 SELECT i.workspace_id,a.id,i.id,'review.reconciled:' || i.id::text,'review.reconciled'
	 FROM issue i
	 JOIN agent a ON a.workspace_id=i.workspace_id
	 WHERE i.status='in_review'
	   AND ($1::uuid IS NULL OR i.workspace_id=$1)
	   AND a.archived_at IS NULL
	   AND a.review_config->>'instant_enabled'='true'
	   AND (a.review_config->>'scope'='workspace' OR
	        (a.review_config->>'scope'='projects' AND a.review_config->'project_ids' ? i.project_id::text))
	   AND NOT EXISTS (
	     SELECT 1 FROM review_agent_event e
	     WHERE e.agent_id=a.id AND e.issue_id=i.id
	   )
	   AND NOT EXISTS (
	     SELECT 1 FROM agent_task_queue t
	     WHERE t.agent_id=a.id AND t.issue_id=i.id AND t.context->>'review_agent'='true'
	   )
	 ON CONFLICT (agent_id,event_key) DO NOTHING`, ws)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// discard marks events as processed without dispatching. Terminal skips —
// archived agents, disabled or invalid configuration, recipients who left —
// can never dispatch later; leaving them pending would re-scan them every
// tick and eventually starve live events behind the scan limit.
func (s *ReviewAgentService) discard(ctx context.Context, ids []pgtype.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := s.Tasks.Queries.DiscardReviewAgentEvents(ctx, ids)
	return err
}

func (s *ReviewAgentService) dispatch(ctx context.Context, c reviewCandidate) error {
	candidate, err := s.Tasks.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: c.AgentID, WorkspaceID: c.WorkspaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return s.discard(ctx, []pgtype.UUID{c.ID})
	}
	if err != nil {
		return err
	}
	if candidate.ArchivedAt.Valid {
		return s.discard(ctx, []pgtype.UUID{c.ID})
	}
	tx, err := s.Tasks.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var fenced bool
	if err = tx.QueryRow(ctx, `SELECT lock_task_owner_rows($1,$2,$3)`, c.AgentID, c.IssueID, candidate.RuntimeID).Scan(&fenced); err != nil {
		return err
	}
	if !fenced {
		return nil
	}
	// Serialize a dispatch against edits that disable or change the scope.
	var lockedAgent pgtype.UUID
	if err = tx.QueryRow(ctx, `SELECT id FROM agent WHERE id=$1 AND workspace_id=$2 FOR NO KEY UPDATE`, c.AgentID, c.WorkspaceID).Scan(&lockedAgent); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	q := s.Tasks.Queries.WithTx(tx)
	issue, err := q.LockWakeupIssue(ctx, c.IssueID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if issue.Status == "cancelled" {
		return s.discardTx(ctx, tx, c)
	}
	var id pgtype.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM review_agent_event WHERE id=$1 AND processed_at IS NULL FOR UPDATE SKIP LOCKED`, c.ID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	agent, err := q.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: c.AgentID, WorkspaceID: c.WorkspaceID})
	if err != nil {
		return err
	}
	var cfg ReviewAgentConfig
	if json.Unmarshal(agent.ReviewConfig, &cfg) != nil || cfg.Validate() != nil || !cfg.InstantEnabled {
		return s.discardTx(ctx, tx, c)
	}
	// A runtime rebind that commits between the pre-lock read and this
	// re-check is a recoverable race, not stale work: the next tick re-reads
	// the new runtime and dispatches then. Discarding here would lose a
	// review trigger the rebound runtime could have run.
	if agent.RuntimeID != candidate.RuntimeID {
		return nil
	}
	if cfg.Scope == "projects" {
		found := false
		for _, project := range cfg.ProjectIDs {
			if project == util.UUIDToString(issue.ProjectID) {
				found = true
				break
			}
		}
		if !found {
			return s.discardTx(ctx, tx, c)
		}
	}
	recipient, err := util.ParseUUID(cfg.RecipientID)
	if err != nil {
		return err
	}
	if _, err = q.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{UserID: recipient, WorkspaceID: c.WorkspaceID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return s.discardTx(ctx, tx, c)
		}
		return err
	}
	if err = (&IssueWakeupService{Tasks: s.Tasks}).authorize(ctx, q, c.WorkspaceID, agent.OwnerID, agent); err != nil {
		// Only a definitive authorization refusal makes the event stale.
		// A database error from the permission lookups is transient: the
		// event stays pending and is retried, not discarded.
		if errors.Is(err, ErrWakeupForbidden) {
			return s.discardTx(ctx, tx, c)
		}
		return err
	}
	var active, recent int
	if err = tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status IN ('queued','dispatched','running')),count(*) FILTER (WHERE created_at>now()-interval '1 hour') FROM agent_task_queue WHERE agent_id=$1 AND issue_id=$2 AND context->>'review_agent'='true'`, c.AgentID, c.IssueID).Scan(&active, &recent); err != nil {
		return err
	}
	if active > 0 || recent >= 6 {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT id,event_type,source_task_id FROM review_agent_event WHERE agent_id=$1 AND issue_id=$2 AND processed_at IS NULL ORDER BY id FOR UPDATE`, c.AgentID, c.IssueID)
	if err != nil {
		return err
	}
	var ids []pgtype.UUID
	var kinds []string
	var manualSources []pgtype.UUID
	for rows.Next() {
		var eventID pgtype.UUID
		var kind string
		var source pgtype.UUID
		if err = rows.Scan(&eventID, &kind, &source); err != nil {
			break
		}
		ids = append(ids, eventID)
		kinds = append(kinds, kind)
		if kind == "manual.review" && source.Valid {
			manualSources = append(manualSources, source)
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	head, err := q.GetIssueReviewHeadSha(ctx, issue.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		head = ""
	} else if err != nil {
		return err
	}
	contextJSON, _ := json.Marshal(map[string]any{"review_agent": true, "review_recipient_id": cfg.RecipientID, "review_events": kinds})
	note := fmt.Sprintf("Review this issue using its current state. Events: %v. Recipient: %s. Report the issue, evidence, tests, linked PR and exact head; explicitly identify missing, failed or unknown checks. Do not merge or deploy without explicit user authorization. Your final output must be exactly one JSON object with exactly two fields, for example {\"verdict\":\"needs_changes\",\"findings\":\"Describe the evidence and required changes here.\"}. verdict must be pass, needs_changes, or needs_decision. findings must be one JSON string, never an array or object, and must describe the evidence and required changes when not pass. Do not include markdown or other text.", kinds, cfg.RecipientID)
	for _, source := range manualSources {
		findings, lookupErr := manualReviewFindings(ctx, q, source, agent.ID, issue.ID)
		if lookupErr != nil {
			return lookupErr
		}
		if findings != "" {
			note += "\nPreviously reported findings to verify: " + findings
		}
	}
	task, err := q.CreateWakeupTask(ctx, db.CreateWakeupTaskParams{ID: dbid.NewV7(), AgentID: agent.ID, RuntimeID: agent.RuntimeID, IssueID: issue.ID, Priority: priorityToInt(issue.Priority), TriggerSummary: pgtype.Text{String: "Review agent", Valid: true}, HandoffNote: pgtype.Text{String: note, Valid: true}, OriginatorUserID: agent.OwnerID, AccountableUserID: agent.OwnerID, OriginatorSource: pgtype.Text{String: "trigger_owner", Valid: true}, HeadSha: headShaText(head), WakeupContext: contextJSON})
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE review_agent_event SET processed_at=now(),dispatched_task_id=$2 WHERE id=ANY($1::uuid[]) AND processed_at IS NULL`, ids, task.ID); err != nil {
		return err
	}
	item, err := q.CreateInboxItem(ctx, reviewInboxParams(c.WorkspaceID, recipient, issue.ID, issue.Title, "review_agent_instant", reviewInboxSeverity(kinds), "Review run queued for this issue.", map[string]any{
		"agent_id": util.UUIDToString(agent.ID), "issue_id": util.UUIDToString(issue.ID), "events": kinds, "head_sha": head,
	}, agent.ID))
	if err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	s.Tasks.broadcastTaskEvent(ctx, protocol.EventTaskQueued, task)
	s.Tasks.NotifyTaskEnqueued(ctx, task)
	s.publishReviewInbox(item, issue.Status)
	return nil
}

func (s *ReviewAgentService) discardTx(ctx context.Context, tx pgx.Tx, c reviewCandidate) error {
	if _, err := tx.Exec(ctx, `UPDATE review_agent_event SET processed_at=now() WHERE id=$1 AND processed_at IS NULL`, c.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func reviewInboxSeverity(kinds []string) string {
	for _, kind := range kinds {
		if kind == "issue.blocked" || kind == "task.failed" {
			return "attention"
		}
	}
	return "info"
}

func reviewInboxParams(workspaceID, recipient, issueID pgtype.UUID, title, itemType, severity, body string, details map[string]any, actor pgtype.UUID) db.CreateInboxItemParams {
	encoded, _ := json.Marshal(details)
	return db.CreateInboxItemParams{
		ID:            dbid.NewV7(),
		WorkspaceID:   workspaceID,
		RecipientType: "member",
		RecipientID:   recipient,
		Type:          itemType,
		Severity:      severity,
		IssueID:       issueID,
		Title:         title,
		Body:          pgtype.Text{String: body, Valid: body != ""},
		ActorType:     pgtype.Text{String: "agent", Valid: true},
		ActorID:       actor,
		Details:       encoded,
	}
}

func (s *ReviewAgentService) publishReviewInbox(item db.InboxItem, issueStatus string) {
	if s.Tasks == nil || s.Tasks.Bus == nil {
		return
	}
	s.Tasks.Bus.Publish(events.Event{
		Type: protocol.EventInboxNew, WorkspaceID: util.UUIDToString(item.WorkspaceID), ActorType: "system",
		Payload: map[string]any{"item": map[string]any{
			"id": util.UUIDToString(item.ID), "workspace_id": util.UUIDToString(item.WorkspaceID),
			"recipient_type": item.RecipientType, "recipient_id": util.UUIDToString(item.RecipientID),
			"type": item.Type, "severity": item.Severity, "issue_id": util.UUIDToPtr(item.IssueID),
			"title": item.Title, "body": util.TextToPtr(item.Body), "read": item.Read, "archived": item.Archived,
			"created_at": util.TimestampToString(item.CreatedAt), "actor_type": util.TextToPtr(item.ActorType),
			"actor_id": util.UUIDToPtr(item.ActorID), "details": json.RawMessage(item.Details), "issue_status": issueStatus,
		}},
	})
}
