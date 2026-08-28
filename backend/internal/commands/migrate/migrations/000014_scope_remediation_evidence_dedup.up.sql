-- Scope evidence idempotency to its owning incident and remediation run.
-- Lock 风险：短暂锁定 remediation_evidence 的唯一索引，并只修复违反
-- run/incident ownership 的历史行；payload 与 incident ownership 保持不变。

DROP INDEX remediation_evidence_project_dedup_idx;

-- 项目级 dedup 曾允许后续 run 抢占旧 incident 的 evidence。解除错误的
-- run 绑定使历史 evidence 保持在原 incident，并让新 run 重新采集自己的记录。
UPDATE remediation_evidence AS evidence
SET run_id = NULL,
    updated_at = clock_timestamp()
FROM remediation_run AS run
JOIN remediation_series AS series ON series.id = run.series_id
WHERE evidence.run_id = run.id
  AND evidence.incident_id <> series.incident_id;

CREATE UNIQUE INDEX remediation_evidence_scope_dedup_idx
    ON remediation_evidence (project_id, incident_id, run_id, deduplication_key)
    NULLS NOT DISTINCT;

COMMENT ON COLUMN remediation_evidence.deduplication_key IS
    'Stable connector-owned append key unique within the owning incident and run; retries stay idempotent without reassigning evidence across ownership boundaries.';
