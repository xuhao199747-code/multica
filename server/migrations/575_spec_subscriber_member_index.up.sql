CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_spec_subscriber_member
ON spec_subscriber (member_id, spec_id);
