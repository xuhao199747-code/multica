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
)

type ReviewAgentService struct{ Tasks *TaskService }

type reviewCandidate struct{ ID, WorkspaceID, AgentID, IssueID pgtype.UUID }

func (s *ReviewAgentService) Tick(ctx context.Context) error {
	return s.TickWorkspace(ctx, pgtype.UUID{})
}

// TickWorkspace bounds each scan; a workspace argument allows isolated tests.
func (s *ReviewAgentService) TickWorkspace(ctx context.Context, ws pgtype.UUID) error {
	tx, err := s.Tasks.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT id,workspace_id,agent_id,issue_id FROM review_agent_event WHERE processed_at IS NULL AND ($1::uuid IS NULL OR workspace_id=$1) ORDER BY created_at,id LIMIT 100`, ws)
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
		if err = s.dispatch(ctx, c); err != nil {
			return err
		}
	}
	return nil
}

func (s *ReviewAgentService) dispatch(ctx context.Context, c reviewCandidate) error {
	candidate, err := s.Tasks.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: c.AgentID, WorkspaceID: c.WorkspaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
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
	if json.Unmarshal(agent.ReviewConfig, &cfg) != nil || cfg.Validate() != nil || !cfg.InstantEnabled || agent.RuntimeID != candidate.RuntimeID {
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
			return nil
		}
	}
	recipient, err := util.ParseUUID(cfg.RecipientID)
	if err != nil {
		return err
	}
	if _, err = q.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{UserID: recipient, WorkspaceID: c.WorkspaceID}); err != nil {
		return nil
	}
	if err = (&IssueWakeupService{Tasks: s.Tasks}).authorize(ctx, q, c.WorkspaceID, agent.OwnerID, agent); err != nil {
		return nil
	}
	var active, recent int
	if err = tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status IN ('queued','dispatched','running')),count(*) FILTER (WHERE created_at>now()-interval '1 hour') FROM agent_task_queue WHERE agent_id=$1 AND issue_id=$2 AND context->>'review_agent'='true'`, c.AgentID, c.IssueID).Scan(&active, &recent); err != nil {
		return err
	}
	if active > 0 || recent >= 6 {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT id,event_type FROM review_agent_event WHERE agent_id=$1 AND issue_id=$2 AND processed_at IS NULL ORDER BY id FOR UPDATE`, c.AgentID, c.IssueID)
	if err != nil {
		return err
	}
	var ids []pgtype.UUID
	var kinds []string
	for rows.Next() {
		var eventID pgtype.UUID
		var kind string
		if err = rows.Scan(&eventID, &kind); err != nil {
			break
		}
		ids = append(ids, eventID)
		kinds = append(kinds, kind)
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
	note := fmt.Sprintf("Review this issue using its current state. Events: %v. Recipient: %s. Report the issue, evidence, tests, linked PR and exact head; explicitly identify missing, failed or unknown checks. Do not merge or deploy without explicit user authorization.", kinds, cfg.RecipientID)
	task, err := q.CreateWakeupTask(ctx, db.CreateWakeupTaskParams{ID: dbid.NewV7(), AgentID: agent.ID, RuntimeID: agent.RuntimeID, IssueID: issue.ID, Priority: priorityToInt(issue.Priority), TriggerSummary: pgtype.Text{String: "Review agent", Valid: true}, HandoffNote: pgtype.Text{String: note, Valid: true}, OriginatorUserID: agent.OwnerID, AccountableUserID: agent.OwnerID, OriginatorSource: pgtype.Text{String: "trigger_owner", Valid: true}, HeadSha: headShaText(head), WakeupContext: contextJSON})
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE review_agent_event SET processed_at=now(),dispatched_task_id=$2 WHERE id=ANY($1::uuid[]) AND processed_at IS NULL`, ids, task.ID); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	s.Tasks.broadcastTaskEvent(ctx, protocol.EventTaskQueued, task)
	s.Tasks.NotifyTaskEnqueued(ctx, task)
	return nil
}
