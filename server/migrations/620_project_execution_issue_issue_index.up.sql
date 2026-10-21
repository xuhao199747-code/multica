CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_project_execution_issue_issue
ON project_execution_issue (issue_id);
