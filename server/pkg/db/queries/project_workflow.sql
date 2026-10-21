-- name: GetProjectWorkflowSettings :one
SELECT * FROM project_workflow_settings
WHERE project_id = $1 AND workspace_id = $2;

-- name: UpsertProjectWorkflowSettings :one
INSERT INTO project_workflow_settings (
    project_id, workspace_id, enabled, project_context, delivery_mode, delivery_config
) VALUES (
    $1, $2, $3, $4, $5, $6
)
ON CONFLICT (project_id) DO UPDATE SET
    enabled = EXCLUDED.enabled,
    project_context = EXCLUDED.project_context,
    delivery_mode = EXCLUDED.delivery_mode,
    delivery_config = EXCLUDED.delivery_config,
    updated_at = now()
RETURNING *;

-- name: ListProjectAgentBindings :many
SELECT * FROM project_agent_binding
WHERE project_id = $1 AND workspace_id = $2
ORDER BY created_at ASC, id ASC;

-- name: CreateProjectAgentBinding :one
INSERT INTO project_agent_binding (
    workspace_id, project_id, agent_id, role
) VALUES (
    $1, $2, $3, $4
)
RETURNING *;

-- name: DeleteProjectAgentBinding :execrows
DELETE FROM project_agent_binding
WHERE id = $1 AND project_id = $2 AND workspace_id = $3;

-- name: DeleteProjectWorkflowExecutionIssues :exec
DELETE FROM project_execution_issue
WHERE project_id = $1 AND workspace_id = $2;

-- name: DeleteProjectWorkflowExecutions :exec
DELETE FROM project_execution_record
WHERE project_id = $1 AND workspace_id = $2;

-- name: DeleteProjectAgentBindings :exec
DELETE FROM project_agent_binding
WHERE project_id = $1 AND workspace_id = $2;

-- name: DeleteProjectWorkflowSettings :exec
DELETE FROM project_workflow_settings
WHERE project_id = $1 AND workspace_id = $2;

-- name: GetProjectMikaChatSession :one
SELECT * FROM chat_session
WHERE workspace_id = $1 AND project_id = $2 AND agent_id = $3 AND creator_id = $4
  AND status = 'active'
ORDER BY created_at ASC
LIMIT 1;
