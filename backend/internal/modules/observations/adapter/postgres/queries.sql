-- name: GetObservationSourceScope :one
SELECT source.environment_id, environment.service
FROM project_sources AS source
JOIN project_environments AS environment
    ON environment.project_id = source.project_id AND environment.id = source.environment_id
WHERE source.project_id = sqlc.arg(project_id)
  AND source.id = sqlc.arg(source_id)
  AND source.enabled;

-- name: CreateObservation :one
INSERT INTO observations (
    id, project_id, environment_id, source_id, service, occurred_at, level,
    message, host, request_id, fingerprint, attributes
)
VALUES (
    sqlc.arg(observation_id), sqlc.arg(project_id), sqlc.arg(environment_id),
    sqlc.arg(source_id), sqlc.narg(service), sqlc.arg(occurred_at), sqlc.arg(level),
    sqlc.arg(message), sqlc.narg(host), sqlc.narg(request_id), sqlc.arg(fingerprint),
    sqlc.arg(attributes)
)
RETURNING id, project_id, environment_id, source_id, service, occurred_at, level,
          message, host, request_id, fingerprint, attributes, ingested_at;

-- name: ListObservations :many
SELECT id, project_id, environment_id, source_id, service, occurred_at, level,
       message, host, request_id, fingerprint, attributes, ingested_at,
       COUNT(*) OVER() AS total_count
FROM observations
WHERE project_id = sqlc.arg(project_id)
ORDER BY occurred_at DESC, id DESC
LIMIT sqlc.arg(result_limit)
OFFSET sqlc.arg(result_offset);
