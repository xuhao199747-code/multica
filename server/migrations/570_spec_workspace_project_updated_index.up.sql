CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_spec_workspace_project_updated
ON spec (workspace_id, project_id, updated_at DESC);
