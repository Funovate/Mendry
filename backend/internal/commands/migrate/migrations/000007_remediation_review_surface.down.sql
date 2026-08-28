-- 仅撤销 000007 新增的 review-surface 列，不删除 000006 的 remediation 表。

ALTER TABLE remediation_artifact
    DROP CONSTRAINT IF EXISTS remediation_artifact_excerpt_bounded,
    DROP COLUMN IF EXISTS excerpt;

ALTER TABLE remediation_plan
    DROP CONSTRAINT IF EXISTS remediation_plan_rollback_bounded,
    DROP CONSTRAINT IF EXISTS remediation_plan_affected_files_bounded,
    DROP CONSTRAINT IF EXISTS remediation_plan_evidence_refs_bounded,
    DROP CONSTRAINT IF EXISTS remediation_plan_key_bounded,
    DROP COLUMN IF EXISTS rollback_strategy,
    DROP COLUMN IF EXISTS affected_files,
    DROP COLUMN IF EXISTS evidence_refs,
    DROP COLUMN IF EXISTS plan_key;

ALTER TABLE remediation_decision
    DROP CONSTRAINT IF EXISTS remediation_decision_next_action_bounded,
    DROP CONSTRAINT IF EXISTS remediation_decision_evidence_citations_bounded,
    DROP CONSTRAINT IF EXISTS remediation_decision_missing_evidence_bounded,
    DROP CONSTRAINT IF EXISTS remediation_decision_contradictions_bounded,
    DROP COLUMN IF EXISTS recommended_next_action,
    DROP COLUMN IF EXISTS evidence_citations,
    DROP COLUMN IF EXISTS missing_evidence,
    DROP COLUMN IF EXISTS contradictions;
