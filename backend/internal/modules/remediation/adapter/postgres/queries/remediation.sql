-- name: CreateRemediationSeries :one
INSERT INTO remediation_series (
    incident_id,
    lifecycle_generation,
    deployed_commit
) VALUES ($1, $2, $3)
ON CONFLICT (incident_id, lifecycle_generation, deployed_commit)
DO UPDATE SET id = remediation_series.id
RETURNING *;

-- name: GetRemediationSeries :one
SELECT * FROM remediation_series
WHERE incident_id = $1 AND lifecycle_generation = $2 AND deployed_commit = $3;

-- name: GetRemediationSeriesByID :one
SELECT * FROM remediation_series WHERE id = $1;

-- name: LockRemediationSeriesForRun :one
SELECT series.*
FROM remediation_series AS series
JOIN remediation_run AS run ON run.series_id = series.id
WHERE run.id = sqlc.arg(run_id)
FOR UPDATE OF series;

-- name: GetRemediationToolPolicy :one
SELECT project_id, source_id, version, policy_hash, entries
FROM remediation_tool_policy
WHERE project_id = $1 AND source_id = $2;

-- name: CreateRemediationRun :one
INSERT INTO remediation_run (
    series_id,
    attempt_number,
    state,
    context_version,
    trigger_reason
) VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: CreateRemediationNextRun :one
INSERT INTO remediation_run (
    series_id,
    attempt_number,
    state,
    continuation_of_run_id,
    trigger_reason,
    continuation_reason,
    context_version
) VALUES (
    sqlc.arg(series_id), sqlc.arg(attempt_number), 'queued',
    sqlc.arg(continuation_of_run_id), sqlc.arg(trigger_reason),
    sqlc.arg(continuation_reason), sqlc.arg(context_version)
)
RETURNING *;

-- name: GetRemediationRun :one
SELECT * FROM remediation_run WHERE id = $1;

-- name: GetRemediationRunsBySeriesID :many
SELECT * FROM remediation_run
WHERE series_id = $1
ORDER BY attempt_number ASC;

-- name: GetLatestRemediationPlanningCheckpoint :one
SELECT run.*
FROM remediation_run AS run
WHERE run.series_id = sqlc.arg(series_id)
  AND run.context_version = sqlc.arg(context_version)
  AND run.attempt_number <= sqlc.arg(through_attempt_number)
  AND (
      SELECT decision.fixability_class
      FROM remediation_decision AS decision
      WHERE decision.run_id = run.id
      ORDER BY decision.sequence DESC
      LIMIT 1
  ) = 'code_fixable'
ORDER BY run.attempt_number DESC
LIMIT 1;

-- name: LockRemediationIncidentContextVersion :one
SELECT version FROM incidents WHERE id = $1 FOR SHARE;

-- name: UpdateRemediationRunState :one
UPDATE remediation_run
SET state = $2,
    ended_at = CASE WHEN $3::boolean THEN now() ELSE ended_at END,
    elapsed_ms = CASE WHEN $3::boolean THEN EXTRACT(EPOCH FROM (now() - started_at)) * 1000 ELSE elapsed_ms END,
    terminal_reason = CASE WHEN $3::boolean THEN sqlc.arg(terminal_reason)::text ELSE terminal_reason END,
    retryable = CASE WHEN $3::boolean THEN sqlc.arg(retryable)::boolean ELSE retryable END,
    version = version + 1
WHERE id = $1 AND version = $4
RETURNING *;

-- name: IncrementRunCounters :one
UPDATE remediation_run
SET model_calls = model_calls + $2,
    model_tokens_in = model_tokens_in + $3,
    model_tokens_out = model_tokens_out + $4,
    model_cost_cents = model_cost_cents + sqlc.arg(model_cost_cents),
    model_provider = CASE
        WHEN length(sqlc.arg(model_provider)::text) > 0 THEN sqlc.arg(model_provider)::text
        ELSE model_provider
    END,
    model_name = CASE
        WHEN length(sqlc.arg(model_name)::text) > 0 THEN sqlc.arg(model_name)::text
        ELSE model_name
    END,
    tool_calls = tool_calls + sqlc.arg(tool_calls),
    evidence_bytes = evidence_bytes + sqlc.arg(evidence_bytes),
    repository_bytes = repository_bytes + sqlc.arg(repository_bytes),
    version = version + 1
WHERE id = $1
RETURNING *;

-- name: CreateRemediationDecision :one
INSERT INTO remediation_decision (
    run_id,
    sequence,
    fixability_class,
    confidence_score,
    reasoning,
    contradictions,
    missing_evidence,
    evidence_citations,
    recommended_next_action
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetRemediationDecisionsByRunID :many
SELECT * FROM remediation_decision
WHERE run_id = $1
ORDER BY sequence ASC;

-- name: CreateRemediationPlan :one
INSERT INTO remediation_plan (
    run_id,
    sequence,
    title,
    rationale,
    risk_class,
    is_recommended,
    plan_key,
    evidence_refs,
    affected_files,
    rollback_strategy
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: GetRemediationPlansByRunID :many
SELECT * FROM remediation_plan
WHERE run_id = $1
ORDER BY sequence ASC;

-- name: CreateRemediationArtifact :one
INSERT INTO remediation_artifact (
    run_id,
    artifact_type,
    reference_path,
    content_hash,
    size_bytes,
    excerpt
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetRemediationArtifactsByRunID :many
SELECT * FROM remediation_artifact
WHERE run_id = $1
ORDER BY created_at ASC;

-- name: CreateRemediationToolInvocation :one
INSERT INTO remediation_tool_invocation (
    run_id,
    sequence,
    tool_name,
    phase,
    duration_ms,
    outcome
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetRemediationToolInvocationsByRunID :many
SELECT * FROM remediation_tool_invocation
WHERE run_id = $1
ORDER BY sequence ASC;

-- name: CreateRemediationAuditEvent :execrows
INSERT INTO audit_events (
    id, project_id, actor_user_id, action, target_type, target_id, summary, metadata
)
SELECT
    sqlc.arg(audit_id),
    incidents.project_id,
    NULL,
    sqlc.arg(action),
    'remediation_run',
    remediation_run.id,
    sqlc.arg(summary),
    sqlc.arg(metadata)::jsonb
FROM remediation_run
JOIN remediation_series ON remediation_series.id = remediation_run.series_id
JOIN incidents ON incidents.id = remediation_series.incident_id
WHERE remediation_run.id = sqlc.arg(run_id);

-- name: CreateRemediationEvidence :one
INSERT INTO remediation_evidence (
    project_id, environment_id, source_id, incident_id, run_id, observation_id,
    provider, evidence_kind, deduplication_key, classification, outcome,
    available, primary_evidence, temporal_correlation, operational_correlation,
    occurred_at, content_hash, byte_count, provenance, payload
) VALUES (
    sqlc.arg(project_id), sqlc.arg(environment_id), sqlc.arg(source_id), sqlc.arg(incident_id),
    sqlc.narg(run_id), sqlc.narg(observation_id), sqlc.arg(provider), sqlc.arg(evidence_kind),
    sqlc.arg(deduplication_key), sqlc.arg(classification), sqlc.arg(outcome),
    sqlc.arg(available), sqlc.arg(primary_evidence), sqlc.arg(temporal_correlation),
    sqlc.arg(operational_correlation), sqlc.narg(occurred_at), sqlc.arg(content_hash),
    sqlc.arg(byte_count), sqlc.arg(provenance), sqlc.arg(payload)
)
ON CONFLICT (project_id, incident_id, run_id, deduplication_key)
DO UPDATE SET
    run_id = COALESCE(EXCLUDED.run_id, remediation_evidence.run_id),
    observation_id = COALESCE(EXCLUDED.observation_id, remediation_evidence.observation_id),
    updated_at = clock_timestamp()
RETURNING *;

-- name: GetRemediationEvidenceForRun :one
SELECT evidence.*
FROM remediation_evidence AS evidence
JOIN incidents AS incident
    ON incident.id = evidence.incident_id AND incident.project_id = evidence.project_id
JOIN remediation_run AS run
    ON run.id = sqlc.arg(run_id)
JOIN remediation_series AS series
    ON series.id = run.series_id AND series.incident_id = incident.id
WHERE evidence.id = sqlc.arg(evidence_id)
  AND (evidence.run_id IS NULL OR evidence.run_id = run.id);

-- name: GetLatestRemediationObservationForIncident :one
SELECT observations.*
FROM remediation_evidence AS evidence
JOIN incidents AS incident
    ON incident.id = evidence.incident_id AND incident.project_id = evidence.project_id
JOIN observations
    ON observations.id = evidence.observation_id
   AND observations.project_id = evidence.project_id
WHERE evidence.incident_id = sqlc.arg(incident_id)
  AND evidence.run_id IS NULL
  AND evidence.evidence_kind = 'normalized_alert'
ORDER BY evidence.created_at DESC, evidence.id DESC
LIMIT 1;

-- name: ListRemediationEvidenceForObservation :many
SELECT evidence.*
FROM remediation_evidence AS evidence
JOIN incidents AS incident
    ON incident.id = evidence.incident_id AND incident.project_id = evidence.project_id
WHERE evidence.incident_id = sqlc.arg(incident_id)
  AND evidence.observation_id = sqlc.arg(observation_id)
  AND evidence.run_id IS NULL
ORDER BY
    CASE WHEN evidence.provider = 'tencent_cls'
        AND evidence.evidence_kind = 'provider_detail'
        AND evidence.classification = 'direct_fault'
        AND evidence.available
        AND evidence.outcome = 'success'
        AND evidence.provenance->>'adapter' = 'tencent_cls'
        AND evidence.provenance->>'detail_capability_validated' = 'true'
        AND evidence.provenance->>'detail_resolution' = 'validated_provider_detail_get_alert_detail'
        THEN 0 ELSE 1 END,
    evidence.created_at ASC, evidence.id ASC
LIMIT sqlc.arg(result_limit);

-- name: GetRemediationRunProject :one
SELECT incidents.project_id
FROM remediation_run
JOIN remediation_series ON remediation_series.id = remediation_run.series_id
JOIN incidents ON incidents.id = remediation_series.incident_id
WHERE remediation_run.id = sqlc.arg(run_id);

-- name: ListRemediationEvidenceForRun :many
SELECT evidence.*
FROM remediation_evidence AS evidence
JOIN incidents AS incident
    ON incident.id = evidence.incident_id AND incident.project_id = evidence.project_id
JOIN remediation_run AS run
    ON run.id = sqlc.arg(run_id)
JOIN remediation_series AS series
    ON series.id = run.series_id AND series.incident_id = incident.id
WHERE evidence.run_id IS NULL OR evidence.run_id = run.id
ORDER BY evidence.created_at ASC, evidence.id ASC
LIMIT sqlc.arg(result_limit);

-- name: UpsertRemediationEvidenceAssessment :one
INSERT INTO remediation_evidence_assessment (
    run_id, project_id, incident_id, confidence_cap, effective_confidence,
    planning_eligible, outcome, reasons, missing_evidence, contradictions,
    direct_evidence_ids
) VALUES (
    sqlc.arg(run_id), sqlc.arg(project_id), sqlc.arg(incident_id),
    sqlc.arg(confidence_cap), sqlc.arg(effective_confidence), sqlc.arg(planning_eligible),
    sqlc.arg(outcome), sqlc.arg(reasons), sqlc.arg(missing_evidence),
    sqlc.arg(contradictions), sqlc.arg(direct_evidence_ids)
)
ON CONFLICT (run_id)
DO UPDATE SET
    confidence_cap = EXCLUDED.confidence_cap,
    effective_confidence = EXCLUDED.effective_confidence,
    planning_eligible = EXCLUDED.planning_eligible,
    outcome = EXCLUDED.outcome,
    reasons = EXCLUDED.reasons,
    missing_evidence = EXCLUDED.missing_evidence,
    contradictions = EXCLUDED.contradictions,
    direct_evidence_ids = EXCLUDED.direct_evidence_ids,
    assessed_at = clock_timestamp()
RETURNING *;
