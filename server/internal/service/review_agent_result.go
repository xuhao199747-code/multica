package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/multica-ai/multica/server/pkg/redact"
)

// reviewResultBodyBound caps the report excerpt carried in the result inbox
// item; the full run output stays on the task/issue for the detail view.
const reviewResultBodyBound = 800

// reviewTaskRecipient returns the configured recipient for a review-agent
// task, or ok=false for ordinary tasks.
func reviewTaskRecipient(task db.AgentTaskQueue) (pgtype.UUID, bool) {
	if len(task.Context) == 0 {
		return pgtype.UUID{}, false
	}
	var context struct {
		ReviewAgent       bool   `json:"review_agent"`
		ReviewRecipientID string `json:"review_recipient_id"`
	}
	if json.Unmarshal(task.Context, &context) != nil || !context.ReviewAgent {
		return pgtype.UUID{}, false
	}
	recipient, err := util.ParseUUID(context.ReviewRecipientID)
	if err != nil {
		return pgtype.UUID{}, false
	}
	return recipient, true
}

// reviewResultExcerpt extracts a bounded, redacted excerpt of the run's final
// output for the result notification.
func reviewResultExcerpt(result []byte) string {
	var payload protocol.TaskCompletedPayload
	if err := json.Unmarshal(result, &payload); err != nil {
		return ""
	}
	body := util.UnescapeBackslashEscapes(payload.Output)
	if body == "" {
		return ""
	}
	if utf8.RuneCountInString(body) > reviewResultBodyBound {
		body = string([]rune(body)[:reviewResultBodyBound]) + "…"
	}
	return redact.Text(body)
}

// reviewResultInboxTx creates the result notification inside the task's
// terminal transition transaction, so a crash between the status flip and the
// notification can never silently drop the outcome. Returns a nil item when
// there is nothing to deliver (ordinary task, or recipient/issue gone).
func (s *ReviewAgentService) reviewResultInboxTx(ctx context.Context, qtx *db.Queries, task db.AgentTaskQueue, result []byte, failed bool, errMsg, failureReason string) (*db.InboxItem, string, error) {
	recipient, ok := reviewTaskRecipient(task)
	if !ok {
		return nil, "", nil
	}
	if !task.IssueID.Valid {
		return nil, "", nil
	}
	issue, err := qtx.GetIssue(ctx, task.IssueID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	if _, err = qtx.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{UserID: recipient, WorkspaceID: issue.WorkspaceID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Recipient left the workspace: nothing to deliver to.
			return nil, "", nil
		}
		return nil, "", err
	}
	if failed {
		if _, err := qtx.CompleteReviewRepairRereview(ctx, db.CompleteReviewRepairRereviewParams{RereviewTaskID: task.ID, Status: "failed"}); err != nil {
			return nil, "", err
		}
	}
	var escalation string
	if !failed {
		var metadata struct {
			Summary bool  `json:"review_summary"`
			Round   int32 `json:"review_repair_round"`
		}
		var payload protocol.TaskCompletedPayload
		if json.Unmarshal(task.Context, &metadata) == nil && !metadata.Summary && json.Unmarshal(result, &payload) == nil {
			verdict, valid := parseReviewRepairVerdict(payload.Output)
			if metadata.Round > 0 {
				status := "blocked"
				if valid && verdict.Verdict == "pass" {
					status = "passed"
				}
				if valid && verdict.Verdict == "needs_changes" && metadata.Round < 3 {
					status = "superseded"
				}
				if _, err := qtx.CompleteReviewRepairRereview(ctx, db.CompleteReviewRepairRereviewParams{RereviewTaskID: task.ID, Status: status}); err != nil {
					return nil, "", err
				}
			}
			if valid && verdict.Verdict == "needs_changes" && metadata.Round >= 3 {
				escalation = "Repair reached the three-round limit; user review is required. " + verdict.Findings
			}
			if valid && verdict.Verdict == "needs_decision" {
				escalation = "User decision required. " + verdict.Findings
			}
			if !valid {
				escalation = "Review result was not structured; user review is required."
			}
			if valid && verdict.Verdict == "needs_changes" && metadata.Round < 3 {
				findings, err := json.Marshal(verdict)
				if err != nil {
					return nil, "", err
				}
				_, err = qtx.CreateReviewRepair(ctx, db.CreateReviewRepairParams{ID: dbid.NewV7(), WorkspaceID: issue.WorkspaceID, IssueID: issue.ID, ReviewTaskID: task.ID, ReviewAgentID: task.AgentID, RecipientID: recipient, Round: metadata.Round + 1, Status: "pending", Findings: findings})
				if err != nil {
					return nil, "", fmt.Errorf("create review repair request: %w", err)
				}
			}
		}
	}
	outcome := "completed"
	severity := "info"
	body := reviewResultExcerpt(result)
	if body == "" {
		body = "Review run finished. Open the issue for the full report."
	}
	if escalation != "" {
		outcome = "needs_attention"
		severity = "attention"
		body = redact.Text(escalation)
	}
	if failed {
		outcome = "failed"
		severity = "attention"
		reason := failureReason
		if reason == "" {
			reason = "unknown"
		}
		detail := redact.Text(errMsg)
		if detail == "" {
			detail = "no error detail recorded"
		}
		body = fmt.Sprintf("Review run failed (%s): %s", reason, detail)
	}
	details := map[string]any{
		"agent_id":       util.UUIDToString(task.AgentID),
		"task_id":        util.UUIDToString(task.ID),
		"issue_id":       util.UUIDToString(task.IssueID),
		"review_outcome": outcome,
	}
	item, err := qtx.CreateInboxItem(ctx, reviewInboxParams(issue.WorkspaceID, recipient, issue.ID, issue.Title, "review_agent_result", severity, body, details, task.AgentID))
	if err != nil {
		return nil, "", err
	}
	return &item, issue.Status, nil
}
