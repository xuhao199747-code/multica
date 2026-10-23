CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_self_update_job_idempotency ON self_update_job (idempotency_key);
