CREATE TABLE spec_version (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    spec_id UUID NOT NULL,
    number INTEGER NOT NULL,
    content TEXT NOT NULL,
    changelog TEXT NOT NULL,
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
