CREATE TABLE project_execution_record (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    source_chat_session_id UUID NOT NULL,
    source_agent_id UUID NOT NULL,
    snapshot JSONB NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('awaiting_confirmation', 'development', 'review', 'repair', 'needs_member_decision', 'delivering', 'completed', 'blocked', 'cancelled')),
    review_round INTEGER NOT NULL DEFAULT 0 CHECK (review_round >= 0),
    delivery_result JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
