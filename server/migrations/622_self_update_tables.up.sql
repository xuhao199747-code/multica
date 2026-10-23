CREATE TABLE IF NOT EXISTS self_update_installation (
    singleton boolean NOT NULL DEFAULT true CHECK (singleton),
    deployed_release_tag text NOT NULL DEFAULT '',
    deployed_custom_sha text NOT NULL DEFAULT '',
    backend_digest text NOT NULL DEFAULT '',
    web_digest text NOT NULL DEFAULT '',
    project_id uuid,
    worker_heartbeat_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS self_update_job (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    requester_id uuid NOT NULL,
    target_tag text NOT NULL,
    target_sha text NOT NULL,
    source_sha text NOT NULL,
    staging_branch text NOT NULL DEFAULT '',
    candidate_sha text NOT NULL DEFAULT '',
    approved_sha text NOT NULL DEFAULT '',
    pushed_custom_sha text NOT NULL DEFAULT '',
    phase text NOT NULL DEFAULT 'queued' CHECK (phase IN (
        'queued', 'preparing', 'awaiting_review', 'repairing', 'pushing',
        'backing_up', 'deploying', 'verifying', 'succeeded', 'failed', 'needs_decision'
    )),
    step jsonb NOT NULL DEFAULT '{}'::jsonb,
    result jsonb NOT NULL DEFAULT '{}'::jsonb,
    repair_count integer NOT NULL DEFAULT 0 CHECK (repair_count BETWEEN 0 AND 1),
    retry_count integer NOT NULL DEFAULT 0 CHECK (retry_count BETWEEN 0 AND 1),
    idempotency_key text NOT NULL CHECK (length(idempotency_key) > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    finished_at timestamptz
);
