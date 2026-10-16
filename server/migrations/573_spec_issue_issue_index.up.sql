CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_spec_issue_issue
ON spec_issue (issue_id, spec_id);
