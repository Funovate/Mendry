-- 为 console review chain 补齐 diagnosis / plan / suggested-diff 可读字段。
-- Lock 风险：对空或少量 remediation 行做 ADD COLUMN，默认值回填；应在低流量时执行。
-- Transaction：runner 在同一 transaction 中执行 schema 与 history 写入。
-- 兼容性：000001 至 000006 保持不可变。已有 decision/plan/artifact 行保持可读，新列为空默认值。
-- Rollback：down 只删除本文件新增的列，不触碰既有 incident/project 或 000006 表。

ALTER TABLE remediation_decision
    ADD COLUMN contradictions text[] NOT NULL DEFAULT ARRAY[]::text[],
    ADD COLUMN missing_evidence text[] NOT NULL DEFAULT ARRAY[]::text[],
    ADD COLUMN evidence_citations text[] NOT NULL DEFAULT ARRAY[]::text[],
    ADD COLUMN recommended_next_action text NOT NULL DEFAULT '';

ALTER TABLE remediation_plan
    ADD COLUMN plan_key text NOT NULL DEFAULT '',
    ADD COLUMN evidence_refs text[] NOT NULL DEFAULT ARRAY[]::text[],
    ADD COLUMN affected_files text[] NOT NULL DEFAULT ARRAY[]::text[],
    ADD COLUMN rollback_strategy text NOT NULL DEFAULT '';

ALTER TABLE remediation_artifact
    ADD COLUMN excerpt text NOT NULL DEFAULT '';

ALTER TABLE remediation_decision
    ADD CONSTRAINT remediation_decision_contradictions_bounded CHECK (cardinality(contradictions) <= 32),
    ADD CONSTRAINT remediation_decision_missing_evidence_bounded CHECK (cardinality(missing_evidence) <= 32),
    ADD CONSTRAINT remediation_decision_evidence_citations_bounded CHECK (cardinality(evidence_citations) <= 64),
    ADD CONSTRAINT remediation_decision_next_action_bounded CHECK (char_length(recommended_next_action) <= 2000);

ALTER TABLE remediation_plan
    ADD CONSTRAINT remediation_plan_key_bounded CHECK (char_length(plan_key) <= 128),
    ADD CONSTRAINT remediation_plan_evidence_refs_bounded CHECK (cardinality(evidence_refs) <= 64),
    ADD CONSTRAINT remediation_plan_affected_files_bounded CHECK (cardinality(affected_files) <= 64),
    ADD CONSTRAINT remediation_plan_rollback_bounded CHECK (char_length(rollback_strategy) <= 4000);

ALTER TABLE remediation_artifact
    ADD CONSTRAINT remediation_artifact_excerpt_bounded CHECK (octet_length(excerpt) <= 65536);

COMMENT ON COLUMN remediation_decision.contradictions IS
    '模型给出的互相矛盾证据摘要；不含原始日志、prompt 或凭据。';
COMMENT ON COLUMN remediation_decision.missing_evidence IS
    '模型声明仍缺失的证据标识；只存短文本，不含原始日志。';
COMMENT ON COLUMN remediation_decision.evidence_citations IS
    '诊断引用的证据 ID 列表；必须能解析到已采集证据，禁止编造。';
COMMENT ON COLUMN remediation_decision.recommended_next_action IS
    '给值班人的下一步建议，例如收集更多日志或人工审查。';
COMMENT ON COLUMN remediation_plan.plan_key IS
    '模型信封中的候选计划标识，供 recommended 对齐；不是内部 UUID。';
COMMENT ON COLUMN remediation_plan.evidence_refs IS
    '该计划依赖的证据 ID 列表。';
COMMENT ON COLUMN remediation_plan.affected_files IS
    '计划声称会改动的仓库相对路径，不含凭据或绝对主机路径。';
COMMENT ON COLUMN remediation_plan.rollback_strategy IS
    '建议回滚方式的短文本；不是可执行脚本。';
COMMENT ON COLUMN remediation_artifact.excerpt IS
    '建议 unified diff 的有界摘录，最多 64KiB，供 console GET 读取；完整内容以 content_hash 寻址，禁止写入 audit_events。';
