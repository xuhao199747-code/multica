CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_spec_issue_pair
ON spec_issue (spec_id, issue_id);
