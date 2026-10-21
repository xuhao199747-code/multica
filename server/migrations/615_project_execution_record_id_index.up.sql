CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_project_execution_record_id
ON project_execution_record (id);
