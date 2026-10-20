CREATE TABLE spec_issue (
    spec_id UUID NOT NULL,
    issue_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
