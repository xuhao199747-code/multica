CREATE TABLE project_workflow_settings (
    project_id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT false,
    project_context TEXT NOT NULL DEFAULT '',
    delivery_mode TEXT NOT NULL DEFAULT 'notify_only'
        CHECK (delivery_mode IN ('notify_only', 'pull_request', 'manual_release', 'automated_release')),
    delivery_config JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
