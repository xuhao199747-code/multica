CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_comment_spec_version ON comment (workspace_id, spec_version_id) WHERE spec_version_id IS NOT NULL;
