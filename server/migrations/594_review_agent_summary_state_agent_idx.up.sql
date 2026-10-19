-- Unique surrogate for the table's natural key; backs the summary watermark
-- upsert's ON CONFLICT (agent_id).
CREATE UNIQUE INDEX CONCURRENTLY review_agent_summary_state_agent_idx ON review_agent_summary_state(agent_id);
