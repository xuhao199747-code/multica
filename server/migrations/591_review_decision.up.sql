-- User review decisions on an issue, pinned to the exact PR head they were
-- made against. A newer decision supersedes earlier unresolved ones in
-- application code; no foreign keys per repository rules. No inline PRIMARY
-- KEY: per repo rules every migration-created index is its own concurrent
-- build (0592/0593/0595).
CREATE TABLE review_decision (
 id uuid NOT NULL DEFAULT gen_random_uuid(),
 workspace_id uuid NOT NULL,
 issue_id uuid NOT NULL,
 pull_request_id uuid,
 head_sha text NOT NULL,
 decision text NOT NULL,
 note text,
 decided_by uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 superseded_at timestamptz,
 resolved_at timestamptz
);
