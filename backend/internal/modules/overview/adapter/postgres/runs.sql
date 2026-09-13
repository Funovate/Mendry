WITH scoped AS MATERIALIZED (
    SELECT r.*, i.id AS incident_id, i.incident_number, i.title, i.status AS incident_status,
           s.lifecycle_generation,
           row_number() OVER (PARTITION BY i.id ORDER BY r.started_at DESC, r.attempt_number DESC, r.id DESC) AS recency
    FROM remediation_run r
    JOIN remediation_series s ON s.id = r.series_id
    JOIN incidents i ON i.id = s.incident_id
    WHERE i.project_id = $1::uuid
), runs AS MATERIALIZED (
    SELECT *, state IN ('failed', 'budget_exhausted', 'completed_non_code', 'blocked_manual_review',
                         'diagnosis_ready_for_review', 'awaiting_human_review') AS terminal,
        jsonb_build_object(
            'runId', id, 'incidentId', 'INC-' || incident_number, 'title', title, 'state', state,
            'attemptNumber', attempt_number, 'generation', lifecycle_generation,
            'startedAt', started_at, 'endedAt', ended_at, 'stateEnteredAt', state_entered_at,
            'tokensIn', model_tokens_in, 'tokensOut', model_tokens_out,
            'usageRecorded', model_tokens_in + model_tokens_out > 0,
            'model', model_name, 'retryable', retryable, 'latest', recency = 1
        ) AS task
    FROM scoped
)
