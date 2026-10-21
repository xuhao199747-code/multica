CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_project_execution_issue_record_issue
ON project_execution_issue (execution_record_id, issue_id);
