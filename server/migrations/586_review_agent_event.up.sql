CREATE TABLE review_agent_event (
 id uuid NOT NULL DEFAULT gen_random_uuid(),
 workspace_id uuid NOT NULL,
 agent_id uuid NOT NULL,
 issue_id uuid NOT NULL,
 event_key text NOT NULL,
 event_type text NOT NULL,
 source_task_id uuid,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 processed_at timestamptz,
 dispatched_task_id uuid
);
