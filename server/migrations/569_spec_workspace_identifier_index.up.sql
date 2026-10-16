CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_spec_workspace_identifier
ON spec (workspace_id, identifier);
