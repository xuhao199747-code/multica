-- name: GetSpecInWorkspace :one
SELECT * FROM spec
WHERE id = $1 AND workspace_id = $2;

-- name: LockSpecInWorkspace :one
SELECT * FROM spec
WHERE id = $1 AND workspace_id = $2
FOR UPDATE;

-- name: GetSpecByIdentifierInWorkspace :one
SELECT * FROM spec
WHERE identifier = $1 AND workspace_id = $2;

-- name: ListSpecs :many
SELECT sqlc.embed(s), COALESCE(v.number, 0)::integer AS current_version_number
FROM spec s
LEFT JOIN spec_version v ON v.id = s.current_version_id AND v.spec_id = s.id
WHERE s.workspace_id = $1
  AND (sqlc.narg('project_id')::uuid IS NULL OR s.project_id = sqlc.narg('project_id')::uuid)
ORDER BY s.updated_at DESC, s.id DESC;

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

-- name: DeleteSpecVersionsBySpec :exec
DELETE FROM spec_version
WHERE spec_id = $1;

-- name: DeleteSpecIssuesBySpec :exec
DELETE FROM spec_issue
WHERE spec_id = $1;

-- name: DeleteSpecSubscribersBySpec :exec
DELETE FROM spec_subscriber
WHERE spec_id = $1;

-- name: IncrementSpecCounter :one
UPDATE workspace SET spec_counter = spec_counter + 1
WHERE id = $1
RETURNING spec_counter;

-- name: GetSpecVersion :one
SELECT * FROM spec_version
WHERE id = $1 AND spec_id = $2;

-- name: GetSpecVersionByNumber :one
SELECT * FROM spec_version
WHERE spec_id = $1 AND number = $2;

-- name: GetLatestSpecVersion :one
SELECT * FROM spec_version
WHERE spec_id = $1
ORDER BY number DESC
LIMIT 1;

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

-- name: CreateSpecVersionNext :one
-- Callers lock the spec row before assigning the next version number.
INSERT INTO spec_version (spec_id, number, content, changelog, created_by)
SELECT $1, COALESCE(MAX(number), 0) + 1, $2, $3, $4
FROM spec_version
WHERE spec_id = $1
RETURNING *;

-- name: UpdateSpecVersionContentWhereNumber :one
-- Guards on number so an edit aimed at the latest version fails instead of
-- silently rewriting a newer snapshot when a publish races it.
UPDATE spec_version
SET content = $3,
    changelog = COALESCE(sqlc.narg('changelog'), changelog)
WHERE id = $1 AND spec_id = $2 AND number = $4
RETURNING *;

-- name: SetSpecCurrentVersion :one
UPDATE spec
SET current_version_id = $3,
    updated_at = now()
WHERE id = $1 AND workspace_id = $2
RETURNING *;

-- name: UpdateSpecStatusWhereStatus :one
-- The expected from-status is part of the predicate so an illegal or
-- raced transition updates nothing and the caller can report a conflict.
UPDATE spec
SET status = $3,
    updated_at = now()
WHERE id = $1 AND workspace_id = $2 AND status = $4
RETURNING *;

-- name: ListSpecIssues :many
SELECT * FROM spec_issue
WHERE spec_id = $1
ORDER BY created_at ASC;

-- name: ListSpecsForIssues :many
-- Bulk variant for issue list/detail endpoints: fold linked spec summaries
-- into each issue row without an N+1 round-trip per issue. Workspace-guarded
-- like the single-spec lookups.
SELECT si.issue_id, s.id, s.identifier, s.title, s.status
FROM spec_issue si
JOIN spec s ON s.id = si.spec_id
WHERE si.issue_id = ANY(sqlc.arg('issue_ids')::uuid[])
  AND s.workspace_id = sqlc.arg('workspace_id')::uuid
ORDER BY si.issue_id, s.identifier ASC;

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

-- name: DeleteSpecIssuesByIssue :exec
-- Issue deletion sweeps its spec links in the same application transaction;
-- the junction has no foreign key (MUL-3515), so nothing cascades for free.
DELETE FROM spec_issue
WHERE issue_id = $1;

-- name: ListSpecIssueRefs :many
-- Spec-side view of the link list: full issue identity (number resolved to
-- the workspace identifier by the handler) in one round-trip.
SELECT i.id, i.number, i.title, i.status
FROM spec_issue si
JOIN issue i ON i.id = si.issue_id
WHERE si.spec_id = $1
  AND i.workspace_id = $2
ORDER BY si.created_at ASC;
-- name: GetSpecVersionSpec :one
SELECT spec_id FROM spec_version WHERE id = $1;

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


-- name: DeleteSpecCommentAttachments :many
DELETE FROM attachment a
WHERE a.workspace_id = sqlc.arg(workspace_id)
  AND a.comment_id IN (SELECT c.id FROM comment c JOIN spec_version v ON v.id = c.spec_version_id WHERE v.spec_id = sqlc.arg(spec_id) AND c.workspace_id = sqlc.arg(workspace_id))
RETURNING url;

-- name: DeleteSpecCommentReactions :exec
DELETE FROM comment_reaction r
WHERE r.workspace_id = sqlc.arg(workspace_id)
  AND r.comment_id IN (SELECT c.id FROM comment c JOIN spec_version v ON v.id = c.spec_version_id WHERE v.spec_id = sqlc.arg(spec_id) AND c.workspace_id = sqlc.arg(workspace_id));

-- name: DeleteSpecComments :exec
DELETE FROM comment
WHERE workspace_id = sqlc.arg(workspace_id)
  AND spec_version_id IN (SELECT id FROM spec_version WHERE spec_id = sqlc.arg(spec_id));
