CREATE INDEX CONCURRENTLY review_decision_issue_idx ON review_decision(issue_id, created_at DESC);
