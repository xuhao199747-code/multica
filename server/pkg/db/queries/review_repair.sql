-- Repair-loop state for actionable review outcomes. One row per repair round;
-- review task -> repair task -> re-review task, bounded to three rounds per
-- review episode in application code. All transitions are compare-and-swap so
-- a replayed terminal callback or a racing dispatcher cannot move a row twice.

-- name: CreateReviewRepair :one
INSERT INTO review_repair (id, workspace_id, issue_id, review_task_id, review_agent_id, recipient_id, round, status, findings)
VALUES (@id, @workspace_id, @issue_id, @review_task_id, @review_agent_id, @recipient_id, @round, @status, @findings)
RETURNING *;

-- name: GetReviewRepairByReviewTask :one
SELECT * FROM review_repair WHERE review_task_id = @review_task_id;

-- name: GetReviewRepair :one
SELECT * FROM review_repair WHERE id = @id;

-- name: SetReviewRepairIssueRevision :exec
UPDATE review_repair SET issue_revision = $2 WHERE id = $1;

-- Dispatcher scan: only states that can produce work. Claimed per row with
-- FOR UPDATE SKIP LOCKED by the dispatch transaction.
-- name: ListDispatchableReviewRepairs :many
SELECT * FROM review_repair
WHERE (@workspace_id::uuid IS NULL OR workspace_id = @workspace_id)
  AND status IN ('pending', 'awaiting_rereview')
ORDER BY created_at, id LIMIT 100;

-- name: LockReviewRepair :one
SELECT * FROM review_repair WHERE id = @id FOR UPDATE;

-- Atomic state machine transition: writes nothing (caller treats pgx.ErrNoRows
-- as "already moved / claimed by someone else") unless the row is still in the
-- expected from_status. blocked_reason is only carried into failed/blocked.
-- name: TransitionReviewRepair :one
UPDATE review_repair SET
  status = @to_status,
  blocked_reason = CASE WHEN @to_status IN ('failed', 'blocked') THEN COALESCE(sqlc.narg(blocked_reason)::text, blocked_reason) ELSE blocked_reason END,
  updated_at = clock_timestamp()
WHERE id = @id AND status = @from_status
RETURNING *;

-- Executor selection: the most recent terminal non-review task on the issue,
-- excluding the reviewing agent itself (self-repair would make the re-review
-- a self-review). The verdict's affected task is validated separately.
-- name: GetReviewRepairExecutorTask :one
SELECT t.* FROM agent_task_queue t JOIN issue i ON i.id=t.issue_id
WHERE t.issue_id = @issue_id AND i.assignee_type='agent' AND t.agent_id=i.assignee_id
  AND t.agent_id <> @exclude_agent_id
  AND COALESCE(t.context->>'review_agent', 'false') <> 'true'
  AND COALESCE(t.context->>'review_repair', 'false') <> 'true'
  AND t.status IN ('completed', 'failed', 'cancelled')
ORDER BY t.created_at DESC, t.id DESC LIMIT 1;

-- name: MarkReviewRepairDispatched :execrows
UPDATE review_repair SET status='dispatched', repair_task_id=$2, updated_at=clock_timestamp()
WHERE id=$1 AND status='pending';

-- name: CompleteReviewRepairTask :execrows
UPDATE review_repair SET status='awaiting_rereview', updated_at=clock_timestamp()
WHERE repair_task_id=$1 AND status='dispatched';

-- name: MarkReviewRepairRereviewDispatched :execrows
UPDATE review_repair SET status='rereview_dispatched', rereview_task_id=$2, updated_at=clock_timestamp()
WHERE id=$1 AND status='awaiting_rereview';

-- name: CompleteReviewRepairRereview :execrows
UPDATE review_repair SET status=$2, updated_at=clock_timestamp()
WHERE rereview_task_id=$1 AND status='rereview_dispatched';

-- name: RepointReviewRepairRetry :exec
UPDATE review_repair SET
 repair_task_id = CASE WHEN repair_task_id=$1 THEN $2 ELSE repair_task_id END,
 rereview_task_id = CASE WHEN rereview_task_id=$1 THEN $2 ELSE rereview_task_id END,
 updated_at=clock_timestamp()
WHERE (status='dispatched' AND repair_task_id=$1)
   OR (status='rereview_dispatched' AND rereview_task_id=$1);
