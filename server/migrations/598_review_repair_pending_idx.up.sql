-- Dispatcher scan: only the two states that can produce work are indexed.
CREATE INDEX CONCURRENTLY review_repair_pending_idx ON review_repair(workspace_id, created_at) WHERE status IN ('pending','awaiting_rereview');
