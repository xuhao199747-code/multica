package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

// Review decisions a user makes on an issue, pinned to the exact PR head they
// inspected. Decisions never grant merge or deploy rights; they are tracking
// state for the review workflow.

type ReviewDecisionInput struct {
	PullRequestID string
	HeadSha       string
	Decision      string
	Note          string
}

func validReviewDecision(decision string) bool {
	switch decision {
	case "approved", "changes_requested", "deferred":
		return true
	}
	return false
}

// RecordDecision validates the PR link, inserts the decision, and supersedes
// the issue's earlier unresolved decisions so exactly one current call exists.
func (s *ReviewAgentService) RecordDecision(ctx context.Context, issue db.Issue, decidedBy pgtype.UUID, in ReviewDecisionInput) (db.ReviewDecision, error) {
	var zero db.ReviewDecision
	if !validReviewDecision(in.Decision) {
		return zero, fmt.Errorf("invalid decision")
	}
	if in.HeadSha == "" || len(in.HeadSha) > 255 {
		return zero, fmt.Errorf("invalid head_sha")
	}
	var prID pgtype.UUID
	if in.PullRequestID != "" {
		parsed, err := util.ParseUUID(in.PullRequestID)
		if err != nil {
			return zero, fmt.Errorf("invalid pull_request_id")
		}
		prID = parsed
		if _, err = s.Tasks.Queries.GetIssuePullRequestLink(ctx, db.GetIssuePullRequestLinkParams{IssueID: issue.ID, PullRequestID: prID}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return zero, fmt.Errorf("pull request is not linked to this issue")
			}
			return zero, err
		}
		if _, err = s.Tasks.Queries.GetGitHubPullRequestInWorkspace(ctx, db.GetGitHubPullRequestInWorkspaceParams{ID: prID, WorkspaceID: issue.WorkspaceID}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return zero, fmt.Errorf("pull request not found in workspace")
			}
			return zero, err
		}
	}
	tx, err := s.Tasks.TxStarter.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback(ctx)
	q := s.Tasks.Queries.WithTx(tx)
	decision, err := q.CreateReviewDecision(ctx, db.CreateReviewDecisionParams{
		ID:            dbid.NewV7(),
		WorkspaceID:   issue.WorkspaceID,
		IssueID:       issue.ID,
		PullRequestID: prID,
		HeadSha:       in.HeadSha,
		Decision:      in.Decision,
		Note:          pgtype.Text{String: in.Note, Valid: in.Note != ""},
		DecidedBy:     decidedBy,
	})
	if err != nil {
		return zero, err
	}
	if _, err = q.SupersedeUnresolvedReviewDecisions(ctx, db.SupersedeUnresolvedReviewDecisionsParams{IssueID: issue.ID, ID: decision.ID}); err != nil {
		return zero, err
	}
	if err = tx.Commit(ctx); err != nil {
		return zero, err
	}
	return decision, nil
}

func (s *ReviewAgentService) ListDecisions(ctx context.Context, issueID pgtype.UUID) ([]db.ReviewDecision, error) {
	return s.Tasks.Queries.ListReviewDecisionsForIssue(ctx, issueID)
}

// ResolveDecision closes a decision the review workflow no longer needs
// tracked. Only the current (unsuperseded, unresolved) decision resolves.
func (s *ReviewAgentService) ResolveDecision(ctx context.Context, workspaceID, id pgtype.UUID) (db.ReviewDecision, error) {
	return s.Tasks.Queries.ResolveReviewDecision(ctx, db.ResolveReviewDecisionParams{ID: id, WorkspaceID: workspaceID})
}
