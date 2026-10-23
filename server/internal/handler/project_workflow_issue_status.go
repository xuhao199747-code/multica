package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type workflowEvidenceRequiredError struct{ command string }

func (e *workflowEvidenceRequiredError) Error() string {
	return "submit project workflow evidence before changing issue status: " + e.command
}

// Guard every issue status write, including batch and description-edit paths.
// Project executions advance only through structured delivery/review evidence;
// a plain status change must not strand them behind a misleading review label.
func requireWorkflowEvidenceForIssueStatus(ctx context.Context, q *db.Queries, workspaceID pgtype.UUID, params db.UpdateIssueParams) error {
	if !params.Status.Valid || (params.Status.String != "in_review" && params.Status.String != "done") {
		return nil
	}
	link, err := q.GetProjectExecutionIssueByIssue(ctx, db.GetProjectExecutionIssueByIssueParams{IssueID: params.ID, WorkspaceID: workspaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load workflow issue: %w", err)
	}
	if link.Role != "development" && link.Role != "repair" && link.Role != "review" {
		return nil
	}
	record, err := q.GetProjectExecutionRecord(ctx, db.GetProjectExecutionRecordParams{ID: link.ExecutionRecordID, ProjectID: link.ProjectID, WorkspaceID: link.WorkspaceID})
	if err != nil {
		return fmt.Errorf("load workflow execution: %w", err)
	}
	var evidence map[string]any
	if err := json.Unmarshal(record.DeliveryResult, &evidence); err != nil {
		return fmt.Errorf("decode workflow evidence: %w", err)
	}
	if receipts, ok := evidence["submitted_issue_ids"].([]any); ok {
		for _, receipt := range receipts {
			if receipt == uuidToString(params.ID) {
				return nil
			}
		}
	}
	command := ""
	if link.Role == "development" || link.Role == "repair" {
		command = fmt.Sprintf("multica project workflow delivery %s --execution %s --issue %s --commit <commit-ref> --tests <test-evidence> --summary <summary>", uuidToString(link.ProjectID), uuidToString(record.ID), uuidToString(params.ID))
	}
	if link.Role == "review" {
		command = fmt.Sprintf("multica project workflow review %s --execution %s --issue %s --outcome passed|needs_changes|needs_member_decision --findings <review-evidence>", uuidToString(link.ProjectID), uuidToString(record.ID), uuidToString(params.ID))
	}
	if command != "" {
		return &workflowEvidenceRequiredError{command: command}
	}
	return nil
}

// Keep a receipt for each linked issue, independent of the execution's latest
// evidence projection or phase. One issue cannot borrow another's handoff.
func recordWorkflowIssueEvidence(evidence map[string]any, issueID pgtype.UUID) {
	receipts, _ := evidence["submitted_issue_ids"].([]any)
	id := uuidToString(issueID)
	for _, receipt := range receipts {
		if receipt == id {
			return
		}
	}
	evidence["submitted_issue_ids"] = append(receipts, id)
}
