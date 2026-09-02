-- Lock 风险：创建项目域新表，并短暂锁定 incidents 以新增、回填和收紧归属字段；
-- 应在发布 project-scoped API 前、低流量时执行。旧事故回填不删除业务数据。
-- Transaction：runner 在同一 transaction 中执行 schema、legacy backfill、constraint
-- 和 migration history 写入；任一步失败都会回滚整个 migration。
-- 兼容性：000001 至 000003 保持不可变。存在旧事故时会创建确定的 legacy
-- project/environment/source，并向现有 enabled system admin 授予项目 admin 权限。
-- Rollback：项目归属、配置和加密凭据不可安全推断回全局模型，因此不提供自动 down
-- migration；修正必须使用新的 forward migration。

CREATE TABLE projects (
    id uuid PRIMARY KEY,
    project_key text NOT NULL,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT projects_id_uuidv7 CHECK (get_byte(uuid_send(id), 6) >> 4 = 7),
    CONSTRAINT projects_key_format CHECK (project_key ~ '^[a-z][a-z0-9-]{1,62}[a-z0-9]$'),
    CONSTRAINT projects_name_bounded CHECK (char_length(name) BETWEEN 1 AND 120),
    CONSTRAINT projects_description_bounded CHECK (char_length(description) <= 1000),
    CONSTRAINT projects_version_positive CHECK (version > 0),
    CONSTRAINT projects_updated_after_created CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX projects_key_unique_idx ON projects (project_key);

CREATE TABLE project_environments (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES projects (id),
    environment_key text NOT NULL,
    name text NOT NULL,
    service text,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT project_environments_id_uuidv7 CHECK (get_byte(uuid_send(id), 6) >> 4 = 7),
    CONSTRAINT project_environments_key_format CHECK (environment_key ~ '^[a-z][a-z0-9-]{0,62}$'),
    CONSTRAINT project_environments_name_bounded CHECK (char_length(name) BETWEEN 1 AND 120),
    CONSTRAINT project_environments_service_bounded CHECK (service IS NULL OR char_length(service) BETWEEN 1 AND 120),
    CONSTRAINT project_environments_version_positive CHECK (version > 0),
    CONSTRAINT project_environments_updated_after_created CHECK (updated_at >= created_at),
    CONSTRAINT project_environments_project_unique UNIQUE (project_id),
    CONSTRAINT project_environments_project_id_id_unique UNIQUE (project_id, id),
    CONSTRAINT project_environments_project_key_unique UNIQUE (project_id, environment_key)
);

CREATE TABLE project_memberships (
    project_id uuid NOT NULL REFERENCES projects (id),
    user_id uuid NOT NULL REFERENCES users (id),
    role text NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (project_id, user_id),
    CONSTRAINT project_memberships_role_known CHECK (role IN ('admin', 'operator', 'viewer')),
    CONSTRAINT project_memberships_version_positive CHECK (version > 0),
    CONSTRAINT project_memberships_updated_after_created CHECK (updated_at >= created_at)
);

CREATE INDEX project_memberships_user_projects_idx ON project_memberships (user_id, project_id);

CREATE TABLE project_secrets (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES projects (id),
    name text NOT NULL,
    kind text NOT NULL,
    ciphertext bytea NOT NULL,
    nonce bytea NOT NULL,
    key_version integer NOT NULL DEFAULT 1,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT project_secrets_id_uuidv7 CHECK (get_byte(uuid_send(id), 6) >> 4 = 7),
    CONSTRAINT project_secrets_name_bounded CHECK (char_length(name) BETWEEN 1 AND 120),
    CONSTRAINT project_secrets_kind_known CHECK (kind IN ('ssh_password', 'ssh_private_key', 'http_bearer', 'http_header', 'webhook_hmac', 'git_credential')),
    CONSTRAINT project_secrets_ciphertext_bounded CHECK (octet_length(ciphertext) BETWEEN 17 AND 65536),
    CONSTRAINT project_secrets_nonce_gcm CHECK (octet_length(nonce) = 12),
    CONSTRAINT project_secrets_key_version_positive CHECK (key_version > 0),
    CONSTRAINT project_secrets_version_positive CHECK (version > 0),
    CONSTRAINT project_secrets_updated_after_created CHECK (updated_at >= created_at),
    CONSTRAINT project_secrets_project_id_id_unique UNIQUE (project_id, id),
    CONSTRAINT project_secrets_project_name_unique UNIQUE (project_id, name)
);

CREATE TABLE project_repositories (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES projects (id),
    remote_url text NOT NULL,
    scm_provider text NOT NULL,
    transport text NOT NULL,
    credential_secret_id uuid,
    production_branch text NOT NULL,
    deployed_commit text NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT project_repositories_id_uuidv7 CHECK (get_byte(uuid_send(id), 6) >> 4 = 7),
    CONSTRAINT project_repositories_remote_bounded CHECK (char_length(remote_url) BETWEEN 1 AND 2048),
    CONSTRAINT project_repositories_provider_known CHECK (scm_provider IN ('github', 'gitlab', 'yunxiao', 'gitee', 'generic')),
    CONSTRAINT project_repositories_transport_known CHECK (transport IN ('https', 'ssh')),
    CONSTRAINT project_repositories_branch_bounded CHECK (char_length(production_branch) BETWEEN 1 AND 255),
    CONSTRAINT project_repositories_commit_format CHECK (deployed_commit ~ '^[0-9a-fA-F]{7,64}$'),
    CONSTRAINT project_repositories_version_positive CHECK (version > 0),
    CONSTRAINT project_repositories_updated_after_created CHECK (updated_at >= created_at),
    CONSTRAINT project_repositories_project_unique UNIQUE (project_id),
    CONSTRAINT project_repositories_project_id_id_unique UNIQUE (project_id, id),
    CONSTRAINT project_repositories_credential_same_project FOREIGN KEY (project_id, credential_secret_id)
        REFERENCES project_secrets (project_id, id)
);

CREATE TABLE project_sources (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES projects (id),
    environment_id uuid NOT NULL,
    name text NOT NULL,
    kind text NOT NULL,
    credential_secret_id uuid,
    config jsonb NOT NULL DEFAULT '{}'::jsonb,
    capabilities text[] NOT NULL DEFAULT ARRAY[]::text[],
    enabled boolean NOT NULL DEFAULT true,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT project_sources_id_uuidv7 CHECK (get_byte(uuid_send(id), 6) >> 4 = 7),
    CONSTRAINT project_sources_name_bounded CHECK (char_length(name) BETWEEN 1 AND 120),
    CONSTRAINT project_sources_kind_known CHECK (kind IN ('ssh', 'cloud', 'mcp')),
    CONSTRAINT project_sources_config_object CHECK (jsonb_typeof(config) = 'object'),
    CONSTRAINT project_sources_capabilities_known CHECK (capabilities <@ ARRAY['push_ingestion', 'pull_collection', 'context_collection', 'metric_collection']::text[]),
    CONSTRAINT project_sources_version_positive CHECK (version > 0),
    CONSTRAINT project_sources_updated_after_created CHECK (updated_at >= created_at),
    CONSTRAINT project_sources_project_unique UNIQUE (project_id),
    CONSTRAINT project_sources_project_id_id_unique UNIQUE (project_id, id),
    CONSTRAINT project_sources_project_environment_id_id_unique UNIQUE (project_id, environment_id, id),
    CONSTRAINT project_sources_project_name_unique UNIQUE (project_id, name),
    CONSTRAINT project_sources_environment_same_project FOREIGN KEY (project_id, environment_id)
        REFERENCES project_environments (project_id, id),
    CONSTRAINT project_sources_credential_same_project FOREIGN KEY (project_id, credential_secret_id)
        REFERENCES project_secrets (project_id, id)
);

CREATE TABLE project_triggers (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES projects (id),
    environment_id uuid NOT NULL,
    name text NOT NULL,
    kind text NOT NULL,
    signing_secret_id uuid,
    config jsonb NOT NULL DEFAULT '{}'::jsonb,
    enabled boolean NOT NULL DEFAULT true,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT project_triggers_id_uuidv7 CHECK (get_byte(uuid_send(id), 6) >> 4 = 7),
    CONSTRAINT project_triggers_name_bounded CHECK (char_length(name) BETWEEN 1 AND 120),
    CONSTRAINT project_triggers_kind_known CHECK (kind IN ('signed_webhook', 'custom_rule')),
    CONSTRAINT project_triggers_config_object CHECK (jsonb_typeof(config) = 'object'),
    CONSTRAINT project_triggers_version_positive CHECK (version > 0),
    CONSTRAINT project_triggers_updated_after_created CHECK (updated_at >= created_at),
    CONSTRAINT project_triggers_project_unique UNIQUE (project_id),
    CONSTRAINT project_triggers_project_id_id_unique UNIQUE (project_id, id),
    CONSTRAINT project_triggers_project_name_unique UNIQUE (project_id, name),
    CONSTRAINT project_triggers_environment_same_project FOREIGN KEY (project_id, environment_id)
        REFERENCES project_environments (project_id, id),
    CONSTRAINT project_triggers_secret_same_project FOREIGN KEY (project_id, signing_secret_id)
        REFERENCES project_secrets (project_id, id)
);

CREATE TABLE observations (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES projects (id),
    environment_id uuid NOT NULL,
    source_id uuid NOT NULL,
    service text,
    occurred_at timestamptz NOT NULL,
    level text NOT NULL,
    message text NOT NULL,
    host text,
    request_id text,
    fingerprint text NOT NULL,
    attributes jsonb NOT NULL DEFAULT '{}'::jsonb,
    ingested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT observations_id_uuidv7 CHECK (get_byte(uuid_send(id), 6) >> 4 = 7),
    CONSTRAINT observations_service_bounded CHECK (service IS NULL OR char_length(service) BETWEEN 1 AND 120),
    CONSTRAINT observations_level_known CHECK (level IN ('debug', 'info', 'warn', 'error', 'critical')),
    CONSTRAINT observations_message_bounded CHECK (char_length(message) BETWEEN 1 AND 65536),
    CONSTRAINT observations_host_bounded CHECK (host IS NULL OR char_length(host) BETWEEN 1 AND 255),
    CONSTRAINT observations_request_id_bounded CHECK (request_id IS NULL OR char_length(request_id) BETWEEN 1 AND 255),
    CONSTRAINT observations_fingerprint_bounded CHECK (char_length(fingerprint) BETWEEN 1 AND 255),
    CONSTRAINT observations_attributes_object CHECK (jsonb_typeof(attributes) = 'object'),
    CONSTRAINT observations_project_environment_source_same_scope FOREIGN KEY (project_id, environment_id, source_id)
        REFERENCES project_sources (project_id, environment_id, id)
);

CREATE INDEX observations_project_stream_idx ON observations (project_id, occurred_at DESC, id DESC);

CREATE TABLE audit_events (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES projects (id),
    actor_user_id uuid REFERENCES users (id),
    action text NOT NULL,
    target_type text NOT NULL,
    target_id uuid,
    summary text NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT audit_events_id_uuidv7 CHECK (get_byte(uuid_send(id), 6) >> 4 = 7),
    CONSTRAINT audit_events_action_format CHECK (action ~ '^[a-z][a-z0-9_.]{2,95}$'),
    CONSTRAINT audit_events_target_type_format CHECK (target_type ~ '^[a-z][a-z0-9_]{1,62}$'),
    CONSTRAINT audit_events_summary_bounded CHECK (char_length(summary) BETWEEN 1 AND 240),
    CONSTRAINT audit_events_metadata_object CHECK (jsonb_typeof(metadata) = 'object')
);

CREATE INDEX audit_events_project_stream_idx ON audit_events (project_id, occurred_at DESC, id DESC);

ALTER TABLE incidents
    ADD COLUMN project_id uuid,
    ADD COLUMN environment_id uuid,
    ADD COLUMN source_id uuid;

INSERT INTO projects (id, project_key, name, description)
SELECT '01900000-0000-7000-8000-000000000001'::uuid,
       'legacy',
       'Legacy incidents',
       'Automatically created while assigning pre-project incidents.'
WHERE EXISTS (SELECT 1 FROM incidents);

INSERT INTO project_environments (id, project_id, environment_key, name)
SELECT '01900000-0000-7000-8000-000000000002'::uuid,
       '01900000-0000-7000-8000-000000000001'::uuid,
       'production',
       'Production'
WHERE EXISTS (SELECT 1 FROM incidents);

INSERT INTO project_sources (id, project_id, environment_id, name, kind, config, capabilities)
SELECT '01900000-0000-7000-8000-000000000003'::uuid,
       '01900000-0000-7000-8000-000000000001'::uuid,
       '01900000-0000-7000-8000-000000000002'::uuid,
       'legacy-source',
       'cloud',
       '{"schemaVersion":1,"provider":"legacy"}'::jsonb,
       ARRAY['push_ingestion']::text[]
WHERE EXISTS (SELECT 1 FROM incidents);

INSERT INTO project_memberships (project_id, user_id, role)
SELECT '01900000-0000-7000-8000-000000000001'::uuid, users.id, 'admin'
FROM users
WHERE users.role = 'admin'
  AND users.enabled
  AND EXISTS (SELECT 1 FROM incidents);

UPDATE incidents
SET project_id = '01900000-0000-7000-8000-000000000001'::uuid,
    environment_id = '01900000-0000-7000-8000-000000000002'::uuid,
    source_id = '01900000-0000-7000-8000-000000000003'::uuid
WHERE project_id IS NULL;

ALTER TABLE incidents
    ALTER COLUMN project_id SET NOT NULL,
    ALTER COLUMN environment_id SET NOT NULL,
    ALTER COLUMN source_id SET NOT NULL,
    ADD CONSTRAINT incidents_project_fk FOREIGN KEY (project_id) REFERENCES projects (id),
    ADD CONSTRAINT incidents_project_environment_source_same_scope
        FOREIGN KEY (project_id, environment_id, source_id)
        REFERENCES project_sources (project_id, environment_id, id);

DROP INDEX incidents_fingerprint_unique_idx;
CREATE UNIQUE INDEX incidents_project_fingerprint_unique_idx ON incidents (project_id, fingerprint);
DROP INDEX incidents_list_idx;
CREATE INDEX incidents_project_list_idx ON incidents (project_id, last_seen DESC, incident_number DESC);

COMMENT ON TABLE projects IS
    '项目是成员授权、采集配置、事件、事故和审计记录的首要隔离边界。';
COMMENT ON COLUMN projects.id IS
    '应用生成的 UUIDv7 项目内部标识，关联表使用该值而不是可变显示名称。';
COMMENT ON COLUMN projects.project_key IS
    '稳定、唯一且适合 URL 的项目 key，创建后不得因显示名称变化而改变。';
COMMENT ON COLUMN projects.name IS
    '项目面向用户的显示名称。';
COMMENT ON COLUMN projects.description IS
    '项目的可选用途说明，空字符串表示未填写。';
COMMENT ON COLUMN projects.version IS
    '项目聚合的单调递增版本号。';
COMMENT ON COLUMN projects.created_at IS
    '项目首次创建时的 PostgreSQL 时钟时间。';
COMMENT ON COLUMN projects.updated_at IS
    '项目最近一次持久化变更时的 PostgreSQL 时钟时间。';

COMMENT ON TABLE project_environments IS
    '保存项目内用于隔离采集配置和业务数据的部署环境。';
COMMENT ON COLUMN project_environments.id IS
    '应用生成的 UUIDv7 环境内部标识。';
COMMENT ON COLUMN project_environments.project_id IS
    '环境所属项目；所有关联 source 和业务数据必须具有相同项目。';
COMMENT ON COLUMN project_environments.environment_key IS
    '项目内稳定且适合配置引用的环境 key。';
COMMENT ON COLUMN project_environments.name IS
    '环境面向用户的显示名称。';
COMMENT ON COLUMN project_environments.service IS
    '该环境采集数据的默认可选 service 标签。';
COMMENT ON COLUMN project_environments.version IS
    '环境配置的单调递增版本号。';
COMMENT ON COLUMN project_environments.created_at IS
    '环境首次创建时的 PostgreSQL 时钟时间。';
COMMENT ON COLUMN project_environments.updated_at IS
    '环境最近一次持久化变更时的 PostgreSQL 时钟时间。';

COMMENT ON TABLE project_memberships IS
    '保存本地用户在单个项目中的 admin、operator 或 viewer 授权。';
COMMENT ON COLUMN project_memberships.project_id IS
    '成员关系所属项目。';
COMMENT ON COLUMN project_memberships.user_id IS
    '获得项目访问能力的本地用户。';
COMMENT ON COLUMN project_memberships.role IS
    '项目内角色，独立于用户的系统管理员角色。';
COMMENT ON COLUMN project_memberships.version IS
    '成员授权的单调递增版本号。';
COMMENT ON COLUMN project_memberships.created_at IS
    '成员首次加入项目时的 PostgreSQL 时钟时间。';
COMMENT ON COLUMN project_memberships.updated_at IS
    '成员角色最近一次变化时的 PostgreSQL 时钟时间。';

COMMENT ON TABLE project_secrets IS
    '保存项目 connector 和 Git transport 使用的加密 credential；不保存明文。';
COMMENT ON COLUMN project_secrets.id IS
    '应用生成的 UUIDv7 secret 引用标识，可安全出现在配置响应中。';
COMMENT ON COLUMN project_secrets.project_id IS
    'credential 所属项目，跨项目引用由组合外键拒绝。';
COMMENT ON COLUMN project_secrets.name IS
    '项目内唯一的 credential 显示名称。';
COMMENT ON COLUMN project_secrets.kind IS
    'credential 的用途类型，用于限制可引用位置和加密关联数据。';
COMMENT ON COLUMN project_secrets.ciphertext IS
    'AES-256-GCM 输出的密文与 authentication tag，不得进入 API、日志或审计。';
COMMENT ON COLUMN project_secrets.nonce IS
    '每次写入随机生成的 96-bit GCM nonce，不得作为 API 字段返回。';
COMMENT ON COLUMN project_secrets.key_version IS
    '加密该值的部署密钥版本，用于未来受控轮换。';
COMMENT ON COLUMN project_secrets.version IS
    'credential metadata 或加密值的单调递增版本号。';
COMMENT ON COLUMN project_secrets.created_at IS
    'credential 首次创建时的 PostgreSQL 时钟时间。';
COMMENT ON COLUMN project_secrets.updated_at IS
    'credential 最近一次替换或 metadata 变化时的 PostgreSQL 时钟时间。';

COMMENT ON TABLE project_repositories IS
    '保存项目唯一的 Git remote、SCM 和生产部署基线配置。';
COMMENT ON COLUMN project_repositories.id IS
    '应用生成的 UUIDv7 repository 配置内部标识。';
COMMENT ON COLUMN project_repositories.project_id IS
    'repository 配置所属项目，每个项目在 MVP 中最多一条。';
COMMENT ON COLUMN project_repositories.remote_url IS
    'Git remote 地址；不得内嵌用户名、密码或 token。';
COMMENT ON COLUMN project_repositories.scm_provider IS
    'remote 所属 SCM provider 的稳定类型。';
COMMENT ON COLUMN project_repositories.transport IS
    '访问 remote 使用的 https 或 ssh transport。';
COMMENT ON COLUMN project_repositories.credential_secret_id IS
    '可选的同项目加密 credential 引用，不包含 credential 值。';
COMMENT ON COLUMN project_repositories.production_branch IS
    '项目配置的生产分支名称。';
COMMENT ON COLUMN project_repositories.deployed_commit IS
    '当前生产部署对应的不可变 Git commit hash。';
COMMENT ON COLUMN project_repositories.version IS
    'repository 配置的单调递增版本号。';
COMMENT ON COLUMN project_repositories.created_at IS
    'repository 配置首次创建时的 PostgreSQL 时钟时间。';
COMMENT ON COLUMN project_repositories.updated_at IS
    'repository 配置最近一次持久化变更时的 PostgreSQL 时钟时间。';

COMMENT ON TABLE project_sources IS
    '保存项目内 SSH、Cloud 或 MCP 采集源的授权配置，不执行实际采集。';
COMMENT ON COLUMN project_sources.id IS
    '应用生成的 UUIDv7 source 内部标识。';
COMMENT ON COLUMN project_sources.project_id IS
    'source 所属项目。';
COMMENT ON COLUMN project_sources.environment_id IS
    'source 所属的同项目 environment。';
COMMENT ON COLUMN project_sources.name IS
    '项目内唯一的 source 显示名称。';
COMMENT ON COLUMN project_sources.kind IS
    'source 类型，取值为 ssh、cloud 或 mcp。';
COMMENT ON COLUMN project_sources.credential_secret_id IS
    '可选的同项目加密 credential 引用。';
COMMENT ON COLUMN project_sources.config IS
    '经过应用层 typed validation 的版本化非秘密 connector 配置 JSON。';
COMMENT ON COLUMN project_sources.capabilities IS
    'connector 声明的不可越权能力集合。';
COMMENT ON COLUMN project_sources.enabled IS
    'source 是否允许后续 connector runtime 使用；false 保留配置但停止新采集。';
COMMENT ON COLUMN project_sources.version IS
    'source 配置的单调递增版本号。';
COMMENT ON COLUMN project_sources.created_at IS
    'source 首次创建时的 PostgreSQL 时钟时间。';
COMMENT ON COLUMN project_sources.updated_at IS
    'source 最近一次持久化变更时的 PostgreSQL 时钟时间。';

COMMENT ON TABLE project_triggers IS
    '保存项目内 signed webhook 或 custom rule 触发配置。';
COMMENT ON COLUMN project_triggers.id IS
    '应用生成的 UUIDv7 trigger 内部标识。';
COMMENT ON COLUMN project_triggers.project_id IS
    'trigger 所属项目。';
COMMENT ON COLUMN project_triggers.environment_id IS
    'trigger 产生事件时使用的同项目 environment。';
COMMENT ON COLUMN project_triggers.name IS
    '项目内唯一的 trigger 显示名称。';
COMMENT ON COLUMN project_triggers.kind IS
    'trigger 类型，取值为 signed_webhook 或 custom_rule。';
COMMENT ON COLUMN project_triggers.signing_secret_id IS
    'signed webhook 使用的同项目 HMAC secret 引用。';
COMMENT ON COLUMN project_triggers.config IS
    '经过应用层 typed validation 的版本化非秘密触发配置 JSON。';
COMMENT ON COLUMN project_triggers.enabled IS
    'trigger 是否允许后续 ingestion runtime 接收或匹配新事件。';
COMMENT ON COLUMN project_triggers.version IS
    'trigger 配置的单调递增版本号。';
COMMENT ON COLUMN project_triggers.created_at IS
    'trigger 首次创建时的 PostgreSQL 时钟时间。';
COMMENT ON COLUMN project_triggers.updated_at IS
    'trigger 最近一次持久化变更时的 PostgreSQL 时钟时间。';

COMMENT ON TABLE observations IS
    '保存项目 Event Stream 中经过归一化的持久事件。';
COMMENT ON COLUMN observations.id IS
    '应用生成的 UUIDv7 observation 内部标识。';
COMMENT ON COLUMN observations.project_id IS
    'observation 所属项目，是读取隔离的首要条件。';
COMMENT ON COLUMN observations.environment_id IS
    '事件产生时对应的同项目 environment。';
COMMENT ON COLUMN observations.source_id IS
    '产生该事件的同项目同环境 source。';
COMMENT ON COLUMN observations.service IS
    '事件的可选 service 标签；为空时可使用 environment 默认值展示。';
COMMENT ON COLUMN observations.occurred_at IS
    '事件在来源系统中发生的时间。';
COMMENT ON COLUMN observations.level IS
    '归一化日志级别，取值为 debug、info、warn、error 或 critical。';
COMMENT ON COLUMN observations.message IS
    '归一化后的事件正文，仍受项目访问和 retention 约束。';
COMMENT ON COLUMN observations.host IS
    '来源提供的可选 host 标识。';
COMMENT ON COLUMN observations.request_id IS
    '来源提供的可选 request correlation 标识。';
COMMENT ON COLUMN observations.fingerprint IS
    '事件归一化后计算的稳定分组 key。';
COMMENT ON COLUMN observations.attributes IS
    '经过 allowlist 和大小限制的非秘密扩展属性。';
COMMENT ON COLUMN observations.ingested_at IS
    '事件被 FixThe 持久化时的 PostgreSQL 时钟时间。';

COMMENT ON TABLE audit_events IS
    '保存项目成员、配置和事故状态变更的只读安全审计摘要。';
COMMENT ON COLUMN audit_events.id IS
    '应用生成的 UUIDv7 audit event 内部标识。';
COMMENT ON COLUMN audit_events.project_id IS
    '审计事件所属项目。';
COMMENT ON COLUMN audit_events.actor_user_id IS
    '执行变更的本地用户；系统迁移等无用户动作可为空。';
COMMENT ON COLUMN audit_events.action IS
    '应用定义的低基数 action，例如 project.member.updated。';
COMMENT ON COLUMN audit_events.target_type IS
    '变更目标的稳定资源类型。';
COMMENT ON COLUMN audit_events.target_id IS
    '变更目标的可选 UUID 标识。';
COMMENT ON COLUMN audit_events.summary IS
    '应用生成的安全可读摘要，不复制请求体或 credential。';
COMMENT ON COLUMN audit_events.metadata IS
    '仅含 allowlisted identifier 和 state 的 JSON object。';
COMMENT ON COLUMN audit_events.occurred_at IS
    '业务变更成功提交时记录的 PostgreSQL 时钟时间。';

COMMENT ON COLUMN incidents.project_id IS
    '事故所属项目，是所有事故查询和 fingerprint 唯一性的首要边界。';
COMMENT ON COLUMN incidents.environment_id IS
    '事故聚合事件所属的同项目 environment。';
COMMENT ON COLUMN incidents.source_id IS
    '事故聚合事件所属的同项目同环境 source。';
