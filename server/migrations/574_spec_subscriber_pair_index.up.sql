CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_spec_subscriber_pair
ON spec_subscriber (spec_id, member_id);
