, stage_tasks AS (
    SELECT *, row_number() OVER (PARTITION BY state ORDER BY state_entered_at ASC NULLS LAST, started_at, id) AS stage_rank
    FROM runs WHERE NOT terminal OR recency = 1
), usage AS MATERIALIZED (
    SELECT u.* FROM remediation_token_usage u JOIN scoped r ON r.id = u.run_id
    WHERE u.recorded_at >= $4::timestamptz AND u.recorded_at <= $3::timestamptz
), buckets AS (
    SELECT starts AS start_at, ends AS end_at
    FROM unnest($5::timestamptz[], $6::timestamptz[]) AS b(starts, ends)
), bucket_usage AS (
    SELECT b.start_at, b.end_at, coalesce(sum(u.tokens_in), 0) AS tokens_in, coalesce(sum(u.tokens_out), 0) AS tokens_out
    FROM buckets b LEFT JOIN usage u ON u.recorded_at >= b.start_at AND u.recorded_at < b.end_at
    GROUP BY b.start_at, b.end_at
), failures AS (
    SELECT CASE WHEN state = 'budget_exhausted' THEN 'budget_exhausted'
        WHEN terminal_reason IN ('model_output_exhausted', 'provider_timeout', 'provider_transport', 'provider_rate_limit',
            'provider_http_5xx', 'provider_http_4xx', 'provider_decode', 'provider_failure', 'provider_configuration',
            'provider_authentication', 'provider_response_too_large', 'transient_provider', 'policy_rejection',
            'elapsed', 'canceled', 'invalid_envelope', 'persistence_failure', 'configuration_failure',
            'authorization_failure') THEN terminal_reason ELSE 'unclassified' END AS reason, count(*) AS count
    FROM runs WHERE state IN ('failed', 'budget_exhausted') AND ended_at >= $7::timestamptz AND ended_at <= $3::timestamptz
    GROUP BY 1
)
SELECT jsonb_build_object(
    'collectionStartedAt', (SELECT enabled_at FROM overview_collection),
    'counts', (SELECT jsonb_build_object(
        'processed', count(DISTINCT incident_id) FILTER (WHERE terminal),
        'todayProcessed', count(DISTINCT incident_id) FILTER (WHERE terminal AND ended_at >= $2::timestamptz AND ended_at <= $3::timestamptz),
        'activeIncidents', count(DISTINCT incident_id) FILTER (WHERE NOT terminal),
        'activeTasks', count(*) FILTER (WHERE NOT terminal),
        'successful', count(*) FILTER (WHERE recency = 1 AND state IN ('diagnosis_ready_for_review', 'completed_non_code', 'awaiting_human_review')),
        'failed', count(*) FILTER (WHERE recency = 1 AND state IN ('failed', 'budget_exhausted')),
        'budgetExhausted', count(*) FILTER (WHERE recency = 1 AND state = 'budget_exhausted'),
        'waiting', count(*) FILTER (WHERE recency = 1 AND incident_status = 'Open' AND state IN ('diagnosis_ready_for_review', 'awaiting_human_review', 'blocked_manual_review')),
        'recovered', (SELECT count(*) FROM incidents WHERE project_id = $1::uuid AND status = 'Recovered')
    ) FROM runs),
    'tokens', (SELECT jsonb_build_object(
        'input', coalesce(sum(model_tokens_in), 0), 'output', coalesce(sum(model_tokens_out), 0),
        'todayInput', (SELECT coalesce(sum(tokens_in), 0) FROM usage WHERE recorded_at >= $2::timestamptz),
        'todayOutput', (SELECT coalesce(sum(tokens_out), 0) FROM usage WHERE recorded_at >= $2::timestamptz),
        'unrecordedTasks', count(*) FILTER (WHERE model_calls > 0 AND model_tokens_in + model_tokens_out = 0)
    ) FROM runs),
    'stages', coalesce((SELECT jsonb_agg(stage ORDER BY state) FROM (
        SELECT state, jsonb_build_object('state', state, 'count', count(*),
            'tasks', jsonb_agg(task ORDER BY stage_rank) FILTER (WHERE stage_rank <= 3)) AS stage
        FROM stage_tasks GROUP BY state
    ) stages), '[]'::jsonb),
    'trend', coalesce((SELECT jsonb_agg(jsonb_build_object(
        'start', start_at, 'end', end_at, 'input', tokens_in, 'output', tokens_out,
        'covered', end_at > c.enabled_at,
        'partial', start_at < c.enabled_at AND end_at > c.enabled_at
    ) ORDER BY start_at) FROM bucket_usage CROSS JOIN overview_collection c), '[]'::jsonb),
    'attention', (SELECT jsonb_build_object(
        'sampleCount', count(*),
        'medianSeconds', percentile_cont(0.5) WITHIN GROUP (ORDER BY greatest(extract(epoch FROM (ended_at - started_at)), 0)),
        'p95Seconds', percentile_cont(0.95) WITHIN GROUP (ORDER BY greatest(extract(epoch FROM (ended_at - started_at)), 0)),
        'budgetExhausted', count(*) FILTER (WHERE state = 'budget_exhausted'),
        'failures', coalesce((SELECT jsonb_agg(jsonb_build_object('reason', reason, 'count', count) ORDER BY count DESC, reason) FROM failures), '[]'::jsonb),
        'longest', coalesce((SELECT jsonb_agg(task ORDER BY state_entered_at ASC NULLS LAST, started_at, id) FROM (
            SELECT * FROM runs WHERE NOT terminal ORDER BY state_entered_at ASC NULLS LAST, started_at, id LIMIT 5
        ) longest), '[]'::jsonb)
    ) FROM runs WHERE terminal AND ended_at >= $7::timestamptz AND ended_at <= $3::timestamptz)
)
