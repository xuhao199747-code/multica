CREATE TABLE project_execution_issue (
    execution_record_id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    project_id UUID NOT NULL,
    issue_id UUID NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('development', 'review', 'repair', 'delivery')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
