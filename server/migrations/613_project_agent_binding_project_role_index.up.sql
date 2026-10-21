CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_project_agent_binding_project_role
ON project_agent_binding (workspace_id, project_id, role);
