CREATE INDEX CONCURRENTLY review_agent_event_pending_idx ON review_agent_event(created_at, id) WHERE processed_at IS NULL;
