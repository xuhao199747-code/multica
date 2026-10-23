CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_self_update_job_active ON self_update_job ((true)) WHERE phase NOT IN ('succeeded', 'failed', 'needs_decision');
