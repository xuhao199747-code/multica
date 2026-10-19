package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ManualReviewInstructions asks the reviewer for explicit, per-issue decisions.
// The dispatcher independently reviews each target before any code is changed.
const ManualReviewInstructions = `At the end of every posted reply include an HTML comment in exactly this format: <!-- multica-review {"reviews":[{"issue_id":"ISSUE_UUID","verdict":"needs_changes","findings":"specific issue"}]} -->. Use the actual issue UUID obtained from Multica, not its display number. Include only issues that require code changes. If no issues need changes, use an empty reviews array. Do not claim repairs were dispatched or performed merely because you listed them. The platform independently reviews eligible issues before scheduling repairs.`

type manualReviewReport struct {
	Reviews []struct {
		IssueID  string `json:"issue_id"`
		Verdict  string `json:"verdict"`
		Findings string `json:"findings"`
	} `json:"reviews"`
}

func parseManualReviewReport(output string) (manualReviewReport, bool) {
	var report manualReviewReport
	if len(output) > 65536 {
		return report, false
	}
	if start := strings.Index(output, "<!-- multica-review "); start >= 0 {
		output = output[start+len("<!-- multica-review "):]
		end := strings.Index(output, "-->")
		if end < 0 {
			return report, false
		}
		output = output[:end]
	}
	decoder := json.NewDecoder(strings.NewReader(output))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&report) != nil || report.Reviews == nil || len(report.Reviews) > 100 {
		return manualReviewReport{}, false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return manualReviewReport{}, false
	}
	for _, item := range report.Reviews {
		if item.Verdict != "needs_changes" || strings.TrimSpace(item.Findings) == "" {
			return manualReviewReport{}, false
		}
		if _, err := util.ParseUUID(item.IssueID); err != nil {
			return manualReviewReport{}, false
		}
	}
	return report, true
}

func manualReviewFindings(ctx context.Context, q *db.Queries, source, agent, issue pgtype.UUID) (string, error) {
	task, err := q.GetAgentTask(ctx, source)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if task.AgentID != agent {
		return "", nil
	}
	var payload protocol.TaskCompletedPayload
	_ = json.Unmarshal(task.Result, &payload)
	outputs := []string{payload.Output}
	comments, err := q.ManualReviewComments(ctx, db.ManualReviewCommentsParams{SourceTaskID: source, AuthorID: agent})
	if err != nil {
		return "", err
	}
	outputs = append(outputs, comments...)
	for _, output := range outputs {
		report, ok := parseManualReviewReport(output)
		if !ok {
			continue
		}
		for _, item := range report.Reviews {
			id, _ := util.ParseUUID(item.IssueID)
			if id == issue {
				findings := []rune(item.Findings)
				if len(findings) > 2000 {
					findings = findings[:2000]
				}
				return string(findings), nil
			}
		}
	}
	return "", nil
}

func (s *ReviewAgentService) queueManualReviewTargetsTx(ctx context.Context, q *db.Queries, task db.AgentTaskQueue, result []byte, notice **db.InboxItem, issueStatus *string) error {
	if !task.IssueID.Valid || !task.TriggerCommentID.Valid || !task.OriginatorUserID.Valid {
		return nil
	}
	if _, ok := reviewTaskRecipient(task); ok {
		return nil
	}
	agent, err := q.GetAgent(ctx, task.AgentID)
	if err != nil {
		return err
	}
	var cfg ReviewAgentConfig
	if json.Unmarshal(agent.ReviewConfig, &cfg) != nil || cfg.Validate() != nil || !cfg.InstantEnabled {
		return nil
	}
	comment, err := q.GetComment(ctx, task.TriggerCommentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if comment.IssueID != task.IssueID || comment.AuthorType != "member" || comment.AuthorID != task.OriginatorUserID {
		return nil
	}
	if _, err := q.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{UserID: task.OriginatorUserID, WorkspaceID: agent.WorkspaceID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	var payload protocol.TaskCompletedPayload
	_ = json.Unmarshal(result, &payload)
	outputs := []string{payload.Output}
	comments, err := q.ManualReviewComments(ctx, db.ManualReviewCommentsParams{SourceTaskID: task.ID, AuthorID: task.AgentID})
	if err != nil {
		return err
	}
	outputs = append(outputs, comments...)
	for _, output := range outputs {
		report, ok := parseManualReviewReport(output)
		if !ok {
			continue
		}
		var skipped int
		seen := make(map[string]bool)
		for _, item := range report.Reviews {
			id, _ := util.ParseUUID(item.IssueID)
			key := util.UUIDToString(id)
			if seen[key] {
				continue
			}
			seen[key] = true
			n, err := q.QueueManualReviewTarget(ctx, db.QueueManualReviewTargetParams{AgentID: task.AgentID, EventKey: fmt.Sprintf("manual:%s:%s", util.UUIDToString(task.ID), key), SourceTaskID: task.ID, IssueID: id, WorkspaceID: agent.WorkspaceID})
			if err != nil {
				return err
			}
			if n == 0 {
				skipped++
			}
		}
		if skipped > 0 {
			return s.manualReviewAttentionTx(ctx, q, task, agent.WorkspaceID, cfg, "Some review targets could not be queued because they are unavailable, outside the configured scope, or not in an eligible status.", notice, issueStatus)
		}
		return nil
	}
	return s.manualReviewAttentionTx(ctx, q, task, agent.WorkspaceID, cfg, "The review reply did not contain a valid structured result; no automatic repair was scheduled. Open the task and retry the review.", notice, issueStatus)
}

func (s *ReviewAgentService) manualReviewAttentionTx(ctx context.Context, q *db.Queries, task db.AgentTaskQueue, workspaceID pgtype.UUID, cfg ReviewAgentConfig, reason string, notice **db.InboxItem, issueStatus *string) error {
	recipient, err := util.ParseUUID(cfg.RecipientID)
	if err != nil {
		return err
	}
	if _, err = q.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{UserID: recipient, WorkspaceID: workspaceID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	issue, err := q.GetIssue(ctx, task.IssueID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	item, err := q.CreateInboxItem(ctx, reviewInboxParams(workspaceID, recipient, issue.ID, issue.Title, "review_agent_result", "attention", reason, map[string]any{"task_id": util.UUIDToString(task.ID), "review_outcome": "needs_attention"}, task.AgentID))
	if err != nil {
		return err
	}
	*notice, *issueStatus = &item, issue.Status
	return nil
}
