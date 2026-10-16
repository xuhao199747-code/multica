-- name: GetSpecInWorkspace :one
SELECT * FROM spec
WHERE id = $1 AND workspace_id = $2;

-- name: ListSpecs :many
SELECT * FROM spec
WHERE workspace_id = $1
  AND (sqlc.narg('project_id')::uuid IS NULL OR project_id = sqlc.narg('project_id')::uuid)
ORDER BY updated_at DESC, id DESC;

-- name: CreateSpec :one
INSERT INTO spec (
    workspace_id, project_id, title, identifier, status, created_by
) VALUES (
    $1, $2, $3, $4, $5, $6
)
RETURNING *;

-- name: UpdateSpec :one
UPDATE spec
SET title = COALESCE(sqlc.narg('title'), title),
    project_id = sqlc.narg('project_id'),
    updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: DeleteSpec :exec
DELETE FROM spec
WHERE id = $1 AND workspace_id = $2;

-- name: GetSpecVersion :one
SELECT * FROM spec_version
WHERE id = $1 AND spec_id = $2;

-- name: ListSpecVersions :many
SELECT * FROM spec_version
WHERE spec_id = $1
ORDER BY number DESC;

-- name: CreateSpecVersion :one
INSERT INTO spec_version (
    spec_id, number, content, changelog, created_by
) VALUES (
    $1, $2, $3, $4, $5
)
RETURNING *;

-- name: SetSpecCurrentVersion :one
UPDATE spec
SET current_version_id = $3,
    updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: ListSpecIssues :many
SELECT * FROM spec_issue
WHERE spec_id = $1
ORDER BY created_at ASC;

-- name: ListIssueSpecs :many
SELECT * FROM spec_issue
WHERE issue_id = $1
ORDER BY created_at ASC;

-- name: AddSpecIssue :exec
INSERT INTO spec_issue (spec_id, issue_id)
VALUES ($1, $2)
ON CONFLICT (spec_id, issue_id) DO NOTHING;

-- name: RemoveSpecIssue :exec
DELETE FROM spec_issue
WHERE spec_id = $1 AND issue_id = $2;

-- name: ListSpecSubscribers :many
SELECT * FROM spec_subscriber
WHERE spec_id = $1
ORDER BY created_at ASC;

-- name: ListMemberSpecSubscriptions :many
SELECT * FROM spec_subscriber
WHERE member_id = $1
ORDER BY created_at DESC;

-- name: AddSpecSubscriber :exec
INSERT INTO spec_subscriber (spec_id, member_id)
VALUES ($1, $2)
ON CONFLICT (spec_id, member_id) DO NOTHING;

-- name: RemoveSpecSubscriber :exec
DELETE FROM spec_subscriber
WHERE spec_id = $1 AND member_id = $2;
