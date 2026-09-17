-- Project/source-scoped MCP read allowlists. Connector configuration remains
-- separate so changing an endpoint or credential cannot grant a new tool.
CREATE TABLE remediation_tool_policy (
    project_id uuid NOT NULL,
    source_id uuid NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    policy_hash text NOT NULL,
    entries jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (project_id, source_id),
    CONSTRAINT remediation_tool_policy_source_same_project
        FOREIGN KEY (project_id, source_id)
        REFERENCES project_sources (project_id, id)
        ON DELETE CASCADE,
    CONSTRAINT remediation_tool_policy_version_positive CHECK (version > 0),
    CONSTRAINT remediation_tool_policy_hash_bounded CHECK (policy_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT remediation_tool_policy_entries_array CHECK (jsonb_typeof(entries) = 'array'),
    CONSTRAINT remediation_tool_policy_entries_bounded CHECK (jsonb_array_length(entries) <= 128),
    CONSTRAINT remediation_tool_policy_updated_after_created CHECK (updated_at >= created_at)
);

COMMENT ON TABLE remediation_tool_policy IS
    '项目与 source 绑定的 MCP 工具只读 allowlist；不保存 connector 凭据或原始工具结果。';
COMMENT ON COLUMN remediation_tool_policy.project_id IS
    'policy 所属项目；必须与 source 属于同一 project。';
COMMENT ON COLUMN remediation_tool_policy.source_id IS
    'policy 绑定的 project source；source 配置变更不会自动扩大 allowlist。';
COMMENT ON COLUMN remediation_tool_policy.version IS
    '管理员 policy 版本；remediation run 在 catalog 构建时捕获该版本。';
COMMENT ON COLUMN remediation_tool_policy.policy_hash IS
    'entries 规范 JSON 的 SHA-256，用于 run catalog 和审计关联。';
COMMENT ON COLUMN remediation_tool_policy.entries IS
    '原始 MCP 工具名、允许 phase 和 read effect 的有界 JSON 数组。';
