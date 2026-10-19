-- Review-agent summary watermark. Persisted atomically with summary dispatch
-- so a repeated local hour or a scheduler restart cannot double-deliver.

-- name: ListReviewSummaryAgents :many
SELECT id, workspace_id, review_config FROM agent
WHERE archived_at IS NULL AND kind = 'user' AND review_config->>'summary_enabled' = 'true';

-- name: GetReviewAgentSummaryState :one
SELECT last_date FROM review_agent_summary_state WHERE agent_id = $1;

-- name: UpsertReviewAgentSummaryState :exec
INSERT INTO review_agent_summary_state (agent_id, workspace_id, last_date, updated_at)
VALUES ($1, $2, $3, clock_timestamp())
ON CONFLICT (agent_id) DO UPDATE SET last_date = EXCLUDED.last_date, updated_at = clock_timestamp();

-- name: DeleteReviewAgentSummaryState :exec
DELETE FROM review_agent_summary_state WHERE agent_id = $1;

-- Distinct issues that still have unclaimed work for this agent. The summary
-- claims these rows so the instant dispatcher cannot double-dispatch them.
-- name: ListPendingReviewIssues :many
SELECT DISTINCT issue_id FROM review_agent_event
WHERE agent_id = $1 AND processed_at IS NULL;

-- Issues currently sitting in a review-relevant state inside the agent's
-- scope; the daily summary re-examines them even when their events were
-- already claimed by instant delivery.
-- name: ListReviewScopeIssues :many
SELECT id, project_id, status, title, priority, number FROM issue
WHERE workspace_id = $1 AND status IN ('in_review', 'blocked')
  AND (sqlc.narg('project_ids')::uuid[] IS NULL OR project_id = ANY(sqlc.narg('project_ids')::uuid[]))
ORDER BY updated_at DESC;

-- User review decisions pinned to an exact PR head.

-- name: DiscardReviewAgentEvents :execrows
UPDATE review_agent_event SET processed_at = clock_timestamp()
WHERE id = ANY($1::uuid[]) AND processed_at IS NULL;

-- name: GetIssuePullRequestLink :one
SELECT pull_request_id FROM issue_pull_request
WHERE issue_id = $1 AND pull_request_id = $2;

-- name: CreateReviewDecision :one
INSERT INTO review_decision (id, workspace_id, issue_id, pull_request_id, head_sha, decision, note, decided_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING *;

-- name: GetReviewDecisionInWorkspace :one
SELECT * FROM review_decision WHERE id = $1 AND workspace_id = $2;

-- name: ListReviewDecisionsForIssue :many
SELECT * FROM review_decision WHERE issue_id = $1 ORDER BY created_at DESC;

-- A newer decision supersedes unresolved decisions for the same PR only.
-- name: SupersedeUnresolvedReviewDecisions :execrows
UPDATE review_decision AS target SET superseded_at = clock_timestamp()
WHERE target.issue_id = $1 AND target.id <> $2 AND target.superseded_at IS NULL AND target.resolved_at IS NULL
  AND target.pull_request_id IS NOT DISTINCT FROM (SELECT d.pull_request_id FROM review_decision d WHERE d.id = $2);

-- name: ResolveReviewDecision :one
UPDATE review_decision SET resolved_at = clock_timestamp()
WHERE id = $1 AND workspace_id = $2 AND superseded_at IS NULL AND resolved_at IS NULL
RETURNING *;
