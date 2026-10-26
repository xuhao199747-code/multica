CREATE UNIQUE INDEX CONCURRENTLY idx_project_execution_issue_one_main
ON project_execution_issue (execution_record_id)
WHERE role = 'main';
