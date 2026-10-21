CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_project_agent_binding_one_mika
ON project_agent_binding (project_id)
WHERE role = 'mika';
