-- 为 000006/000007 引入的 remediation 表补齐表和字段注释。
-- Lock 风险：COMMENT 只更新 PostgreSQL catalog，不重写表数据；执行时会短暂获取
-- 目标 relation 的 metadata lock，应避开长事务长期持有冲突锁的时段。
-- Transaction：runner 在同一 transaction 中写入全部表/字段注释并记录 checksum；
-- 任一 COMMENT 失败都会回滚本 migration 的全部 metadata 变更。
-- 兼容性：000001 至 000007 保持不可变。本文件不改变 column、constraint、index
-- 或 query contract，可在依赖现有 schema 的 API binary 发布前后执行。
-- Rollback：删除注释会重新引入不可维护状态，因此不提供自动 down migration；
-- 注释修正使用新的 forward migration。已有英文列注释在此统一为中文语义说明。

COMMENT ON TABLE remediation_series IS
    '一次事故在固定 lifecycle_generation 与 deployed_commit 下的 remediation 系列；唯一键阻止同一基线重复建根 run。';
COMMENT ON COLUMN remediation_series.id IS
    '应用生成的系列 UUID，是其下全部 run 的外键目标。';
COMMENT ON COLUMN remediation_series.incident_id IS
    '所属事故的内部 UUIDv7，对应 incidents.id，不暴露为 INC-<number>。';
COMMENT ON COLUMN remediation_series.lifecycle_generation IS
    '写入系列时捕获的事故生命周期代数；与 incident 当前值一起构成系列唯一键。';
COMMENT ON COLUMN remediation_series.deployed_commit IS
    '写入系列时捕获的生产部署 commit；后续仓库配置变更不得改写已有系列键。';
COMMENT ON COLUMN remediation_series.created_at IS
    '系列首次创建时的 PostgreSQL 时钟时间。';

COMMENT ON TABLE remediation_run IS
    '系列内一次有界尝试的状态、预算计数和模型元数据；不含原始 prompt、响应或凭据。';
COMMENT ON COLUMN remediation_run.id IS
    '应用生成的 run UUID，是 decision、plan、artifact 和 tool invocation 的外键目标。';
COMMENT ON COLUMN remediation_run.series_id IS
    '所属 remediation 系列；同一系列内 attempt_number 单调递增且唯一。';
COMMENT ON COLUMN remediation_run.attempt_number IS
    '系列内的尝试序号，从 1 起；重试创建新 run，不改写终态 run。';
COMMENT ON COLUMN remediation_run.state IS
    'run 生命周期状态，例如 queued、diagnosing、diagnosis_ready_for_review、failed。';
COMMENT ON COLUMN remediation_run.started_at IS
    'run 记录创建或开始推进时的 PostgreSQL 时钟时间。';
COMMENT ON COLUMN remediation_run.ended_at IS
    'run 进入终态时的时钟时间；进行中为空。';
COMMENT ON COLUMN remediation_run.elapsed_ms IS
    '进入终态时累计的墙钟毫秒数；进行中为空。';
COMMENT ON COLUMN remediation_run.model_calls IS
    '本 run 已完成的模型调用次数，计入预算。';
COMMENT ON COLUMN remediation_run.model_tokens_in IS
    '提供方报告的累计输入 token；缺省为 0，不存原始请求。';
COMMENT ON COLUMN remediation_run.model_tokens_out IS
    '提供方报告的累计输出 token；缺省为 0，不存原始响应。';
COMMENT ON COLUMN remediation_run.model_cost_cents IS
    '提供方报告的累计费用（分）；可用时记录，从不保存原始请求或响应。';
COMMENT ON COLUMN remediation_run.model_provider IS
    '安全的提供方标识，例如 openai；不含 API key、凭据或原始载荷。';
COMMENT ON COLUMN remediation_run.model_name IS
    '安全的模型标识，例如 gpt-5.6；不含原始请求或响应。';
COMMENT ON COLUMN remediation_run.tool_calls IS
    '本 run 已执行的工具调用次数，计入预算。';
COMMENT ON COLUMN remediation_run.evidence_bytes IS
    '本 run 已读入的证据字节累计，计入预算。';
COMMENT ON COLUMN remediation_run.repository_bytes IS
    '本 run 已读入的仓库字节累计，计入预算。';
COMMENT ON COLUMN remediation_run.version IS
    'run 聚合的单调递增版本号，供状态迁移时执行乐观并发控制。';

COMMENT ON TABLE remediation_decision IS
    '一次 schema 校验后的诊断结论及其证据引用；不含原始日志、prompt 或凭据。';
COMMENT ON COLUMN remediation_decision.id IS
    '应用生成的诊断记录 UUID。';
COMMENT ON COLUMN remediation_decision.run_id IS
    '所属 remediation run。';
COMMENT ON COLUMN remediation_decision.sequence IS
    '同一 run 内诊断记录的追加序号。';
COMMENT ON COLUMN remediation_decision.fixability_class IS
    '可修复性分类，例如 code_fixable、configuration、insufficient_evidence。';
COMMENT ON COLUMN remediation_decision.confidence_score IS
    '模型给出的置信度，范围 0.00 到 1.00。';
COMMENT ON COLUMN remediation_decision.reasoning IS
    '因果推理摘要；禁止写入原始 prompt、完整日志或凭据。';
COMMENT ON COLUMN remediation_decision.decided_at IS
    '该诊断写入时的 PostgreSQL 时钟时间。';
COMMENT ON COLUMN remediation_decision.contradictions IS
    '模型给出的互相矛盾证据摘要；不含原始日志、prompt 或凭据。';
COMMENT ON COLUMN remediation_decision.missing_evidence IS
    '模型声明仍缺失的证据标识；只存短文本，不含原始日志。';
COMMENT ON COLUMN remediation_decision.evidence_citations IS
    '诊断引用的证据 ID 列表；必须能解析到已采集证据，禁止编造。';
COMMENT ON COLUMN remediation_decision.recommended_next_action IS
    '给值班人的下一步建议，例如收集更多日志或人工审查。';

COMMENT ON TABLE remediation_plan IS
    'code_fixable 诊断下的候选或推荐修复计划；建议 diff 存在 artifact，不内联到本行。';
COMMENT ON COLUMN remediation_plan.id IS
    '应用生成的计划记录 UUID。';
COMMENT ON COLUMN remediation_plan.run_id IS
    '所属 remediation run。';
COMMENT ON COLUMN remediation_plan.sequence IS
    '同一 run 内计划记录的追加序号。';
COMMENT ON COLUMN remediation_plan.title IS
    '计划的短标题，供 console 列表展示。';
COMMENT ON COLUMN remediation_plan.rationale IS
    '推荐或不推荐该计划的理由摘要。';
COMMENT ON COLUMN remediation_plan.risk_class IS
    '变更风险分类，取值为 ordinary、high_risk 或 denied_control_plane。';
COMMENT ON COLUMN remediation_plan.is_recommended IS
    '是否为该 run 当前推荐计划；同一 run 至多一条为真。';
COMMENT ON COLUMN remediation_plan.created_at IS
    '计划写入时的 PostgreSQL 时钟时间。';
COMMENT ON COLUMN remediation_plan.plan_key IS
    '模型信封中的候选计划标识，供 recommended 对齐；不是内部 UUID。';
COMMENT ON COLUMN remediation_plan.evidence_refs IS
    '该计划依赖的证据 ID 列表。';
COMMENT ON COLUMN remediation_plan.affected_files IS
    '计划声称会改动的仓库相对路径，不含凭据或绝对主机路径。';
COMMENT ON COLUMN remediation_plan.rollback_strategy IS
    '建议回滚方式的短文本；不是可执行脚本。';

COMMENT ON TABLE remediation_artifact IS
    '建议 diff 等产物的内容寻址引用；完整内容不写入 audit JSON，摘录有界。';
COMMENT ON COLUMN remediation_artifact.id IS
    '应用生成的产物记录 UUID。';
COMMENT ON COLUMN remediation_artifact.run_id IS
    '所属 remediation run。';
COMMENT ON COLUMN remediation_artifact.artifact_type IS
    '产物类型，例如 suggested_diff。';
COMMENT ON COLUMN remediation_artifact.reference_path IS
    '应用拥有的产物存储路径或逻辑引用，不是仓库远程 URL。';
COMMENT ON COLUMN remediation_artifact.content_hash IS
    '产物内容的稳定哈希，供后续回放校验。';
COMMENT ON COLUMN remediation_artifact.size_bytes IS
    '产物字节数，用于预算和展示，不内联完整内容。';
COMMENT ON COLUMN remediation_artifact.created_at IS
    '产物记录写入时的 PostgreSQL 时钟时间。';
COMMENT ON COLUMN remediation_artifact.excerpt IS
    '建议 unified diff 的有界摘录，最多 64KiB，供 console GET 读取；完整内容以 content_hash 寻址，禁止写入 audit_events。';

COMMENT ON TABLE remediation_tool_invocation IS
    '一次只读工具调用的元数据；参数已归一化，禁止保存秘密字段或原始大段输出。';
COMMENT ON COLUMN remediation_tool_invocation.id IS
    '应用生成的工具调用 UUID。';
COMMENT ON COLUMN remediation_tool_invocation.run_id IS
    '所属 remediation run。';
COMMENT ON COLUMN remediation_tool_invocation.sequence IS
    '同一 run 内工具调用的追加序号。';
COMMENT ON COLUMN remediation_tool_invocation.tool_name IS
    '逻辑工具标识，例如 repository.read_file 或 evidence.search。';
COMMENT ON COLUMN remediation_tool_invocation.phase IS
    '调用发生时的 run 阶段，例如 diagnosing。';
COMMENT ON COLUMN remediation_tool_invocation.invoked_at IS
    '调用开始时的 PostgreSQL 时钟时间。';
COMMENT ON COLUMN remediation_tool_invocation.duration_ms IS
    '调用耗时毫秒数；失败或未完成时可为 NULL。';
COMMENT ON COLUMN remediation_tool_invocation.outcome IS
    '调用结果分类，例如 ok、rejected、unavailable 或 error。';
