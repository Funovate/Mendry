, filtered AS (
    SELECT * FROM runs
    WHERE ($2::text = '' OR state = $2)
      AND CASE $3::text
        WHEN 'flow' THEN NOT terminal OR recency = 1
        WHEN 'attention' THEN recency = 1 AND incident_status = 'Open' AND state IN ('diagnosis_ready_for_review', 'awaiting_human_review', 'blocked_manual_review')
        WHEN 'failed' THEN recency = 1 AND state IN ('failed', 'budget_exhausted')
        WHEN 'budget' THEN recency = 1 AND state = 'budget_exhausted'
        ELSE true END
), page AS (
    SELECT * FROM filtered
    ORDER BY CASE WHEN $4::text = 'tokens' THEN model_tokens_in + model_tokens_out END DESC,
             started_at DESC, attempt_number DESC, id DESC
    LIMIT $5 OFFSET $6
)
SELECT jsonb_build_object(
    'total', (SELECT count(*) FROM filtered),
    'items', coalesce((SELECT jsonb_agg(task ORDER BY
        CASE WHEN $4::text = 'tokens' THEN model_tokens_in + model_tokens_out END DESC,
        started_at DESC, attempt_number DESC, id DESC) FROM page), '[]'::jsonb)
)
