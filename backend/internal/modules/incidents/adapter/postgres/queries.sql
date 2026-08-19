-- name: GetIncidentSourceScope :one
SELECT source.environment_id, source.name
FROM project_sources AS source
WHERE source.project_id = sqlc.arg(project_id)
  AND source.id = sqlc.arg(source_id)
  AND source.enabled;

-- name: CreateIncident :one
WITH created_incident AS (
    INSERT INTO incidents (
        id, project_id, environment_id, source_id, title, fingerprint, status,
        priority, source, first_seen, last_seen, occurrence_count, host_count,
        muted, notification_summary, lifecycle_generation, deployed_commit
    ) VALUES (
        sqlc.arg(incident_id), sqlc.arg(project_id), sqlc.arg(environment_id),
        sqlc.arg(source_id), sqlc.arg(title), sqlc.arg(fingerprint), sqlc.arg(status),
        sqlc.arg(priority), sqlc.arg(source_name), sqlc.arg(first_seen), sqlc.arg(last_seen),
        sqlc.arg(occurrence_count), sqlc.arg(host_count), sqlc.arg(muted),
        sqlc.arg(notification_summary), sqlc.arg(lifecycle_generation), sqlc.arg(deployed_commit)
    )
    RETURNING id, project_id, environment_id, source_id, incident_number, title,
              fingerprint, status, priority, source, first_seen, last_seen,
              occurrence_count, host_count, muted, notification_summary,
              lifecycle_generation, deployed_commit, version, created_at, updated_at
), created_audit AS (
    INSERT INTO audit_events (
        id, project_id, actor_user_id, action, target_type, target_id, summary, metadata
    )
    SELECT sqlc.arg(audit_id), project_id, sqlc.narg(actor_user_id), 'incident.created',
           'incident', id, 'Incident created.',
           jsonb_build_object('incidentNumber', incident_number, 'status', status)
    FROM created_incident
)
SELECT id, project_id, environment_id, source_id, incident_number, title,
       fingerprint, status, priority, source, first_seen, last_seen,
       occurrence_count, host_count, muted, notification_summary,
       lifecycle_generation, deployed_commit, version, created_at, updated_at
FROM created_incident;

-- name: GetIncidentByNumber :one
SELECT id, project_id, environment_id, source_id, incident_number, title,
       fingerprint, status, priority, source, first_seen, last_seen,
       occurrence_count, host_count, muted, notification_summary,
       lifecycle_generation, deployed_commit, version, created_at, updated_at
FROM incidents
WHERE project_id = sqlc.arg(project_id)
  AND incident_number = sqlc.arg(incident_number);

-- name: GetIncidentByGlobalNumber :one
SELECT id, project_id, environment_id, source_id, incident_number, title,
       fingerprint, status, priority, source, first_seen, last_seen,
       occurrence_count, host_count, muted, notification_summary,
       lifecycle_generation, deployed_commit, version, created_at, updated_at
FROM incidents
WHERE incident_number = sqlc.arg(incident_number);

-- name: GetIncidentByID :one
SELECT id, project_id, environment_id, source_id, incident_number, title,
       fingerprint, status, priority, source, first_seen, last_seen,
       occurrence_count, host_count, muted, notification_summary,
       lifecycle_generation, deployed_commit, version, created_at, updated_at
FROM incidents
WHERE id = sqlc.arg(incident_id);

-- name: ListIncidents :many
SELECT id, project_id, environment_id, source_id, incident_number, title,
       fingerprint, status, priority, source, first_seen, last_seen,
       occurrence_count, host_count, muted, notification_summary,
       lifecycle_generation, deployed_commit, version, created_at, updated_at,
       COUNT(*) OVER() AS total_count
FROM incidents
WHERE project_id = sqlc.arg(project_id)
ORDER BY last_seen DESC, incident_number DESC
LIMIT sqlc.arg(result_limit);

-- name: GetIncidentByFingerprint :one
SELECT id, project_id, environment_id, source_id, incident_number, title,
       fingerprint, status, priority, source, first_seen, last_seen,
       occurrence_count, host_count, muted, notification_summary,
       lifecycle_generation, deployed_commit, version, created_at, updated_at
FROM incidents
WHERE project_id = sqlc.arg(project_id)
  AND fingerprint = sqlc.arg(fingerprint);

-- name: RecordIncidentOccurrence :one
WITH changed_incident AS (
    UPDATE incidents AS incident
    SET last_seen = sqlc.arg(last_seen),
        occurrence_count = incident.occurrence_count + 1,
        version = incident.version + 1,
        updated_at = clock_timestamp()
    WHERE incident.project_id = sqlc.arg(project_id)
      AND incident.fingerprint = sqlc.arg(fingerprint)
      AND incident.status = 'Open'
    RETURNING incident.id, incident.project_id, incident.environment_id,
              incident.source_id, incident.incident_number, incident.title,
              incident.fingerprint, incident.status, incident.priority,
              incident.source, incident.first_seen, incident.last_seen,
              incident.occurrence_count, incident.host_count, incident.muted,
              incident.notification_summary, incident.lifecycle_generation,
              incident.deployed_commit, incident.version,
              incident.created_at, incident.updated_at
), created_audit AS (
    INSERT INTO audit_events (
        id, project_id, actor_user_id, action, target_type, target_id, summary, metadata
    )
    SELECT sqlc.arg(audit_id), project_id, sqlc.narg(actor_user_id),
           'incident.occurrence.recorded', 'incident', id,
           'Incident occurrence recorded.',
           jsonb_build_object('incidentNumber', incident_number, 'status', status)
    FROM changed_incident
)
SELECT id, project_id, environment_id, source_id, incident_number, title,
       fingerprint, status, priority, source, first_seen, last_seen,
       occurrence_count, host_count, muted, notification_summary,
       lifecycle_generation, deployed_commit, version, created_at, updated_at
FROM changed_incident;

-- name: UpdateIncidentStatus :one
WITH changed_incident AS (
    UPDATE incidents AS incident
    SET status = sqlc.arg(status),
        lifecycle_generation = sqlc.arg(lifecycle_generation),
        deployed_commit = sqlc.arg(deployed_commit),
        version = incident.version + 1,
        updated_at = clock_timestamp()
    WHERE incident.project_id = sqlc.arg(project_id)
      AND incident.incident_number = sqlc.arg(incident_number)
    RETURNING incident.id, incident.project_id, incident.environment_id,
              incident.source_id, incident.incident_number, incident.title,
              incident.fingerprint, incident.status, incident.priority,
              incident.source, incident.first_seen, incident.last_seen,
              incident.occurrence_count, incident.host_count, incident.muted,
              incident.notification_summary, incident.lifecycle_generation,
              incident.deployed_commit, incident.version,
              incident.created_at, incident.updated_at
), created_audit AS (
    INSERT INTO audit_events (
        id, project_id, actor_user_id, action, target_type, target_id, summary, metadata
    )
    SELECT sqlc.arg(audit_id), project_id, sqlc.arg(actor_user_id), 'incident.status.updated',
           'incident', id, 'Incident status updated.',
           jsonb_build_object('incidentNumber', incident_number, 'status', status)
    FROM changed_incident
)
SELECT id, project_id, environment_id, source_id, incident_number, title,
       fingerprint, status, priority, source, first_seen, last_seen,
       occurrence_count, host_count, muted, notification_summary,
       lifecycle_generation, deployed_commit, version, created_at, updated_at
FROM changed_incident;
