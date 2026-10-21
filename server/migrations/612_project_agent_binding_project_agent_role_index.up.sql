CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_project_agent_binding_project_agent_role
ON project_agent_binding (project_id, agent_id, role);
