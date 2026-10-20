CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_spec_version_spec_number
ON spec_version (spec_id, number);
