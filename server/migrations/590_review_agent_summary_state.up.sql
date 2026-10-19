-- Per-agent watermark for the daily review summary. Persisted atomically with
-- dispatch so a repeated local hour (DST) or a scheduler restart cannot fire a
-- second summary for the same local date. No inline PRIMARY KEY: per repo
-- rules every migration-created index is its own concurrent build (0594).
CREATE TABLE review_agent_summary_state (
 agent_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 last_date text NOT NULL,
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
