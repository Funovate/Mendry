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
    trigger_reason,
    agent_loop_mode,
    agent_loop_policy_version
)
SELECT sqlc.arg(series_id), sqlc.arg(attempt_number), sqlc.arg(state),
       sqlc.arg(context_version), sqlc.arg(trigger_reason),
       project.agent_loop_mode, project.agent_loop_policy_version
FROM remediation_series AS series
JOIN incidents AS incident ON incident.id = series.incident_id
JOIN projects AS project ON project.id = incident.project_id
WHERE series.id = sqlc.arg(series_id)
RETURNING *;

-- name: CreateRemediationNextRun :one
INSERT INTO remediation_run (
    series_id,
    attempt_number,
    state,
    continuation_of_run_id,
    trigger_reason,
    continuation_reason,
    context_version,
    agent_loop_mode,
    agent_loop_policy_version
) VALUES (
    sqlc.arg(series_id), sqlc.arg(attempt_number), 'queued',
    sqlc.arg(continuation_of_run_id), sqlc.arg(trigger_reason),
    sqlc.arg(continuation_reason), sqlc.arg(context_version),
    (SELECT agent_loop_mode FROM remediation_run WHERE id = sqlc.arg(continuation_of_run_id)),
    (SELECT agent_loop_policy_version FROM remediation_run WHERE id = sqlc.arg(continuation_of_run_id))
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
    ended_at = CASE WHEN $3::boolean THEN now() ELSE NULL END,
    elapsed_ms = CASE WHEN $3::boolean THEN EXTRACT(EPOCH FROM (now() - started_at)) * 1000 ELSE NULL END,
    terminal_reason = CASE WHEN $3::boolean THEN sqlc.arg(terminal_reason)::text ELSE '' END,
    retryable = CASE WHEN $3::boolean THEN sqlc.arg(retryable)::boolean ELSE false END,
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

-- name: GetLatestRemediationDecisionID :one
SELECT id FROM remediation_decision
WHERE run_id = $1
ORDER BY sequence DESC
LIMIT 1;

-- name: CreateRemediationSubmittedDiagnosis :one
INSERT INTO remediation_submitted_diagnosis (
    run_id,
    sequence,
    fixability_class,
    confidence_score,
    reasoning,
    contradictions,
    missing_evidence,
    evidence_citations,
    recommended_next_action,
    correction_kind,
    correction_evidence,
    correction_count,
    corrected,
    gate_outcome,
    decision_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
RETURNING *;

-- name: GetRemediationSubmittedDiagnosesByRunID :many
SELECT * FROM remediation_submitted_diagnosis
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

-- name: GetRemediationLifecycleEffect :one
SELECT * FROM remediation_lifecycle_effect
WHERE run_id = sqlc.arg(run_id)
  AND effect_kind = sqlc.arg(effect_kind)
  AND idempotency_key = sqlc.arg(idempotency_key);

-- name: UpsertRemediationLifecycleEffect :one
INSERT INTO remediation_lifecycle_effect (
    run_id, effect_kind, idempotency_key, state, attempt, baseline_commit,
    workspace_id, base_tree_hash, result_tree_hash, artifact_ref, content_hash,
    command_id, command_version, validation_known, validation_passed, branch_ref, target_branch,
    commit_hash, draft_change_ref, compare_url, error_code, summary
) VALUES (
    sqlc.arg(run_id), sqlc.arg(effect_kind), sqlc.arg(idempotency_key), sqlc.arg(state),
    sqlc.arg(attempt), sqlc.arg(baseline_commit), sqlc.arg(workspace_id),
    sqlc.arg(base_tree_hash), sqlc.arg(result_tree_hash), sqlc.arg(artifact_ref),
    sqlc.arg(content_hash), sqlc.arg(command_id), sqlc.arg(command_version),
    sqlc.arg(validation_known), sqlc.arg(validation_passed), sqlc.arg(branch_ref), sqlc.arg(target_branch),
    sqlc.arg(commit_hash), sqlc.arg(draft_change_ref), sqlc.arg(compare_url),
    sqlc.arg(error_code), sqlc.arg(summary)
)
ON CONFLICT (run_id, effect_kind, idempotency_key)
DO UPDATE SET
    state = CASE WHEN remediation_lifecycle_effect.state = 'succeeded'
        THEN remediation_lifecycle_effect.state ELSE EXCLUDED.state END,
    attempt = CASE WHEN remediation_lifecycle_effect.state = 'succeeded'
        THEN remediation_lifecycle_effect.attempt ELSE EXCLUDED.attempt END,
    baseline_commit = CASE WHEN remediation_lifecycle_effect.state = 'succeeded'
        THEN remediation_lifecycle_effect.baseline_commit ELSE EXCLUDED.baseline_commit END,
    workspace_id = CASE WHEN remediation_lifecycle_effect.state = 'succeeded'
        THEN remediation_lifecycle_effect.workspace_id ELSE EXCLUDED.workspace_id END,
    base_tree_hash = CASE WHEN remediation_lifecycle_effect.state = 'succeeded'
        THEN remediation_lifecycle_effect.base_tree_hash ELSE EXCLUDED.base_tree_hash END,
    result_tree_hash = CASE WHEN remediation_lifecycle_effect.state = 'succeeded'
        THEN remediation_lifecycle_effect.result_tree_hash ELSE EXCLUDED.result_tree_hash END,
    artifact_ref = CASE WHEN remediation_lifecycle_effect.state = 'succeeded'
        THEN remediation_lifecycle_effect.artifact_ref ELSE EXCLUDED.artifact_ref END,
    content_hash = CASE WHEN remediation_lifecycle_effect.state = 'succeeded'
        THEN remediation_lifecycle_effect.content_hash ELSE EXCLUDED.content_hash END,
    command_id = CASE WHEN remediation_lifecycle_effect.state = 'succeeded'
        THEN remediation_lifecycle_effect.command_id ELSE EXCLUDED.command_id END,
    command_version = CASE WHEN remediation_lifecycle_effect.state = 'succeeded'
        THEN remediation_lifecycle_effect.command_version ELSE EXCLUDED.command_version END,
    validation_known = CASE WHEN remediation_lifecycle_effect.state = 'succeeded'
        THEN remediation_lifecycle_effect.validation_known ELSE EXCLUDED.validation_known END,
    validation_passed = CASE WHEN remediation_lifecycle_effect.state = 'succeeded'
        THEN remediation_lifecycle_effect.validation_passed ELSE EXCLUDED.validation_passed END,
    branch_ref = CASE WHEN remediation_lifecycle_effect.state = 'succeeded'
        THEN remediation_lifecycle_effect.branch_ref ELSE EXCLUDED.branch_ref END,
    target_branch = CASE WHEN remediation_lifecycle_effect.state = 'succeeded'
        THEN remediation_lifecycle_effect.target_branch ELSE EXCLUDED.target_branch END,
    commit_hash = CASE WHEN remediation_lifecycle_effect.state = 'succeeded'
        THEN remediation_lifecycle_effect.commit_hash ELSE EXCLUDED.commit_hash END,
    draft_change_ref = CASE WHEN remediation_lifecycle_effect.state = 'succeeded'
        THEN remediation_lifecycle_effect.draft_change_ref ELSE EXCLUDED.draft_change_ref END,
    compare_url = CASE WHEN remediation_lifecycle_effect.state = 'succeeded'
        THEN remediation_lifecycle_effect.compare_url ELSE EXCLUDED.compare_url END,
    error_code = CASE WHEN remediation_lifecycle_effect.state = 'succeeded'
        THEN remediation_lifecycle_effect.error_code ELSE EXCLUDED.error_code END,
    summary = CASE WHEN remediation_lifecycle_effect.state = 'succeeded'
        THEN remediation_lifecycle_effect.summary ELSE EXCLUDED.summary END,
    updated_at = CASE WHEN remediation_lifecycle_effect.state = 'succeeded'
        THEN remediation_lifecycle_effect.updated_at ELSE clock_timestamp() END
RETURNING *;

-- name: ListRemediationLifecycleEffects :many
SELECT * FROM remediation_lifecycle_effect
WHERE run_id = $1
ORDER BY updated_at ASC, id ASC;

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
    outcome,
    outcome_ref,
    evidence_ids,
    error_code
) VALUES (sqlc.arg(run_id), sqlc.arg(sequence), sqlc.arg(tool_name), sqlc.arg(phase), sqlc.arg(duration_ms), sqlc.arg(outcome), NULLIF(sqlc.arg(outcome_ref)::text, ''), sqlc.arg(evidence_ids), NULLIF(sqlc.arg(error_code)::text, ''))
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
    occurred_at, content_hash, byte_count, provenance, payload,
    baseline_lifecycle_generation, baseline_deployed_commit
)
SELECT
    sqlc.arg(project_id), sqlc.arg(environment_id), sqlc.arg(source_id), sqlc.arg(incident_id),
    sqlc.narg(run_id), sqlc.narg(observation_id), sqlc.arg(provider), sqlc.arg(evidence_kind),
    sqlc.arg(deduplication_key), sqlc.arg(classification), sqlc.arg(outcome),
    sqlc.arg(available), sqlc.arg(primary_evidence), sqlc.arg(temporal_correlation),
    sqlc.arg(operational_correlation), sqlc.narg(occurred_at), sqlc.arg(content_hash),
    sqlc.arg(byte_count), sqlc.arg(provenance), sqlc.arg(payload),
    COALESCE(series.lifecycle_generation, incident.lifecycle_generation),
    COALESCE(series.deployed_commit, incident.deployed_commit)
FROM incidents AS incident
LEFT JOIN remediation_run AS owning_run ON owning_run.id = sqlc.narg(run_id)
LEFT JOIN remediation_series AS series ON series.id = owning_run.series_id
WHERE incident.id = sqlc.arg(incident_id)
  AND incident.project_id = sqlc.arg(project_id)
ON CONFLICT (
    project_id, incident_id, run_id, baseline_lifecycle_generation,
    baseline_deployed_commit, deduplication_key
)
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
  AND ((evidence.run_id IS NULL
        AND evidence.baseline_lifecycle_generation = series.lifecycle_generation
        AND evidence.baseline_deployed_commit = series.deployed_commit)
       OR EXISTS (
           SELECT 1 FROM remediation_run AS evidence_run
           WHERE evidence_run.id = evidence.run_id
             AND evidence_run.series_id = series.id
             AND evidence_run.attempt_number <= run.attempt_number
       ));

-- name: GetRemediationEvidencePage :one
SELECT evidence.*, COALESCE(evidence_run.attempt_number, 0) AS source_attempt
FROM remediation_evidence AS evidence
JOIN incidents AS incident
    ON incident.id = evidence.incident_id AND incident.project_id = evidence.project_id
JOIN remediation_run AS run
    ON run.id = sqlc.arg(run_id)
JOIN remediation_series AS series
    ON series.id = run.series_id AND series.incident_id = incident.id
LEFT JOIN remediation_run AS evidence_run
    ON evidence_run.id = evidence.run_id
WHERE evidence.id = sqlc.arg(evidence_id)
  AND ((evidence.run_id IS NULL
        AND evidence.baseline_lifecycle_generation = series.lifecycle_generation
        AND evidence.baseline_deployed_commit = series.deployed_commit)
       OR EXISTS (
           SELECT 1 FROM remediation_run AS series_run
           WHERE series_run.id = evidence.run_id
             AND series_run.series_id = series.id
             AND series_run.attempt_number <= run.attempt_number
       ));

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
  AND evidence.baseline_lifecycle_generation = incident.lifecycle_generation
  AND evidence.baseline_deployed_commit = incident.deployed_commit
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
WHERE ((evidence.run_id IS NULL
        AND evidence.baseline_lifecycle_generation = series.lifecycle_generation
        AND evidence.baseline_deployed_commit = series.deployed_commit)
       OR EXISTS (
           SELECT 1 FROM remediation_run AS evidence_run
           WHERE evidence_run.id = evidence.run_id
             AND evidence_run.series_id = series.id
             AND evidence_run.attempt_number <= run.attempt_number
       ))
ORDER BY evidence.created_at ASC, evidence.id ASC
LIMIT sqlc.arg(result_limit);

-- name: ListContinuationRuntimeEvidence :many
SELECT evidence.*
FROM remediation_evidence AS evidence
JOIN incidents AS incident
    ON incident.id = evidence.incident_id AND incident.project_id = evidence.project_id
JOIN remediation_series AS series
    ON series.id = sqlc.arg(series_id) AND series.incident_id = incident.id
WHERE evidence.evidence_kind = 'runtime'
  AND evidence.run_id IS NOT NULL
  AND EXISTS (
      SELECT 1 FROM remediation_run AS evidence_run
      WHERE evidence_run.id = evidence.run_id
        AND evidence_run.series_id = series.id
        AND evidence_run.attempt_number <= sqlc.arg(through_attempt_number)
  )
ORDER BY evidence.created_at ASC, evidence.id ASC
LIMIT sqlc.arg(result_limit);

-- name: ListContinuationEvidenceIndex :many
SELECT evidence.id, evidence.evidence_kind, evidence.provider, evidence.classification,
       evidence.content_hash, COALESCE(evidence_run.attempt_number, 0) AS source_attempt
FROM remediation_evidence AS evidence
JOIN incidents AS incident
    ON incident.id = evidence.incident_id AND incident.project_id = evidence.project_id
JOIN remediation_series AS series
    ON series.id = sqlc.arg(series_id) AND series.incident_id = incident.id
LEFT JOIN remediation_run AS evidence_run
    ON evidence_run.id = evidence.run_id
WHERE evidence.evidence_kind <> 'runtime'
  AND ((evidence.run_id IS NULL
        AND evidence.baseline_lifecycle_generation = series.lifecycle_generation
        AND evidence.baseline_deployed_commit = series.deployed_commit)
       OR EXISTS (
           SELECT 1 FROM remediation_run AS series_run
           WHERE series_run.id = evidence.run_id
             AND series_run.series_id = series.id
             AND series_run.attempt_number <= sqlc.arg(through_attempt_number)
       ))
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

-- name: CreateRemediationCheckpointEvent :one
INSERT INTO remediation_checkpoint_event (
    run_id,
    sequence,
    trigger_reason,
    payload,
    content_hash
) VALUES (
    sqlc.arg(run_id), sqlc.arg(sequence), sqlc.arg(trigger_reason),
    sqlc.arg(payload), sqlc.arg(content_hash)
)
RETURNING *;

-- name: GetRemediationCheckpointLatestSequence :one
SELECT COALESCE(MAX(sequence), 0)::bigint AS sequence
FROM remediation_checkpoint_event
WHERE run_id = $1;

-- name: GetRemediationCheckpointEvent :one
SELECT * FROM remediation_checkpoint_event
WHERE run_id = $1 AND sequence = $2;

-- name: ListRemediationCheckpointEvents :many
SELECT * FROM remediation_checkpoint_event
WHERE run_id = $1
ORDER BY sequence DESC
LIMIT sqlc.arg(result_limit);

-- name: GetRemediationWorkingMemory :one
SELECT * FROM remediation_working_memory WHERE run_id = $1;

-- name: UpsertRemediationWorkingMemory :one
INSERT INTO remediation_working_memory (
    run_id,
    sequence,
    context_version,
    observed_run_version,
    phase,
    content_hash
) VALUES (
    sqlc.arg(run_id), sqlc.arg(sequence), sqlc.arg(context_version),
    sqlc.arg(observed_run_version),
    sqlc.arg(phase), sqlc.arg(content_hash)
)
ON CONFLICT (run_id)
DO UPDATE SET
    sequence = EXCLUDED.sequence,
    context_version = EXCLUDED.context_version,
    observed_run_version = EXCLUDED.observed_run_version,
    phase = EXCLUDED.phase,
    content_hash = EXCLUDED.content_hash,
    updated_at = clock_timestamp()
RETURNING *;

-- name: CreateRemediationEvidenceReadCursor :exec
INSERT INTO remediation_evidence_read_cursor (
    token_hash, run_id, evidence_id, content_hash, byte_offset, expires_at
) VALUES (
    sqlc.arg(token_hash), sqlc.arg(run_id), sqlc.arg(evidence_id),
    sqlc.arg(content_hash), sqlc.arg(byte_offset), sqlc.arg(expires_at)
);

-- name: GetRemediationEvidenceReadCursor :one
SELECT * FROM remediation_evidence_read_cursor
WHERE token_hash = sqlc.arg(token_hash)
  AND run_id = sqlc.arg(run_id)
  AND evidence_id = sqlc.arg(evidence_id)
  AND content_hash = sqlc.arg(content_hash);

-- name: DeleteExpiredRemediationEvidenceReadCursors :exec
DELETE FROM remediation_evidence_read_cursor
WHERE expires_at < clock_timestamp();
