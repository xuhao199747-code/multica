CREATE TABLE project_agent_binding (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('mika', 'prd', 'developer', 'reviewer')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
