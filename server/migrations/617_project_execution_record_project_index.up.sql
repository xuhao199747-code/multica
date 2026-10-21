CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_project_execution_record_project_updated
ON project_execution_record (workspace_id, project_id, updated_at DESC);
