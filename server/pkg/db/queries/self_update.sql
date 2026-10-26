-- name: GetSelfUpdateInstallation :one
SELECT * FROM self_update_installation WHERE singleton = true;

-- name: UpsertSelfUpdateInstallation :one
INSERT INTO self_update_installation (singleton, deployed_release_tag, deployed_custom_sha, backend_digest, web_digest, project_id)
VALUES (true, $1, $2, $3, $4, $5)
ON CONFLICT (singleton) DO UPDATE SET
    deployed_release_tag = EXCLUDED.deployed_release_tag,
    deployed_custom_sha = EXCLUDED.deployed_custom_sha,
    backend_digest = EXCLUDED.backend_digest,
    web_digest = EXCLUDED.web_digest,
    project_id = EXCLUDED.project_id,
    updated_at = now()
RETURNING *;

-- name: HeartbeatSelfUpdateWorker :one
INSERT INTO self_update_installation (singleton, worker_heartbeat_at)
VALUES (true, now())
ON CONFLICT (singleton) DO UPDATE SET worker_heartbeat_at = now(), updated_at = now()
RETURNING *;

-- name: CreateSelfUpdateJob :one
INSERT INTO self_update_job (requester_id, target_tag, target_sha, source_sha, idempotency_key)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (idempotency_key) DO UPDATE SET idempotency_key = EXCLUDED.idempotency_key
RETURNING *;

-- name: ClaimSelfUpdateJob :one
UPDATE self_update_job SET phase = 'preparing', started_at = now(), updated_at = now()
WHERE id = (
    SELECT id FROM self_update_job WHERE phase = 'queued'
    ORDER BY created_at, id FOR UPDATE SKIP LOCKED LIMIT 1
)
RETURNING *;

-- name: AdvanceSelfUpdateJob :one
UPDATE self_update_job SET
    phase = sqlc.arg(phase),
    staging_branch = sqlc.arg(staging_branch),
    candidate_sha = sqlc.arg(candidate_sha),
    approved_sha = sqlc.arg(approved_sha),
    pushed_custom_sha = sqlc.arg(pushed_custom_sha),
    step = sqlc.arg(step), result = sqlc.arg(result),
    repair_count = sqlc.arg(repair_count), retry_count = sqlc.arg(retry_count),
    updated_at = now(),
    finished_at = CASE WHEN sqlc.arg(phase)::text IN ('succeeded', 'failed', 'needs_decision') THEN now() ELSE NULL END
WHERE id = sqlc.arg(id) AND phase = sqlc.arg(expected_phase)
    AND repair_count = sqlc.arg(expected_repair_count) AND retry_count = sqlc.arg(expected_retry_count)
    AND phase NOT IN ('succeeded', 'failed', 'needs_decision')
    AND sqlc.arg(repair_count)::integer = repair_count + CASE WHEN sqlc.arg(phase)::text = 'repairing' THEN 1 ELSE 0 END
    AND sqlc.arg(retry_count)::integer = retry_count + CASE WHEN phase = 'repairing' AND sqlc.arg(phase)::text = 'preparing' THEN 1 ELSE 0 END
RETURNING *;

-- name: GetSelfUpdateJob :one
SELECT * FROM self_update_job WHERE id = $1;

-- name: FindSelfUpdateJobByIdempotencyKey :one
SELECT * FROM self_update_job WHERE idempotency_key = $1;
