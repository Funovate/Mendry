-- 为项目配置增加唯一的 OpenAI 兼容 LLM provider。
-- Lock 风险：新建空表并短暂更新 configuration 查询契约；不改写已有
-- environment/repository/source/trigger 行。
-- Transaction：runner 在同一 transaction 中执行 schema、注释和 history 写入。
-- 兼容性：000001 至 000008 保持不可变。已有项目可以没有 LLM 行；GET 使用
-- LEFT JOIN，PUT 才要求完整的 base URL、凭据引用和模型。
-- Rollback：删除表会丢掉已保存的模型选择，因此不提供自动 down；
-- 修正使用新的 forward migration。

CREATE TABLE project_llm_providers (
    id uuid PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES projects (id),
    provider text NOT NULL,
    base_url text NOT NULL,
    credential_secret_id uuid NOT NULL,
    model text NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT project_llm_providers_id_uuidv7 CHECK (get_byte(uuid_send(id), 6) >> 4 = 7),
    CONSTRAINT project_llm_providers_provider_known CHECK (provider IN ('openai')),
    CONSTRAINT project_llm_providers_base_url_bounded CHECK (char_length(base_url) BETWEEN 1 AND 2048),
    CONSTRAINT project_llm_providers_model_bounded CHECK (char_length(model) BETWEEN 1 AND 200),
    CONSTRAINT project_llm_providers_version_positive CHECK (version > 0),
    CONSTRAINT project_llm_providers_updated_after_created CHECK (updated_at >= created_at),
    CONSTRAINT project_llm_providers_project_unique UNIQUE (project_id),
    CONSTRAINT project_llm_providers_project_id_id_unique UNIQUE (project_id, id),
    CONSTRAINT project_llm_providers_credential_same_project FOREIGN KEY (project_id, credential_secret_id)
        REFERENCES project_secrets (project_id, id)
);

COMMENT ON TABLE project_llm_providers IS
    '保存项目唯一的 OpenAI 兼容 LLM 接入点：base URL、加密 API key 引用和已选模型。';
COMMENT ON COLUMN project_llm_providers.id IS
    '应用生成的 UUIDv7 LLM 配置内部标识。';
COMMENT ON COLUMN project_llm_providers.project_id IS
    'LLM 配置所属项目，每个项目在 MVP 中最多一条。';
COMMENT ON COLUMN project_llm_providers.provider IS
    'LLM 提供方类型；当前仅允许 openai，表示 OpenAI 兼容的 Chat Completions / Models API。';
COMMENT ON COLUMN project_llm_providers.base_url IS
    '提供方 HTTP origin，例如 https://api.openai.com；不得内嵌用户名、密码或 token。';
COMMENT ON COLUMN project_llm_providers.credential_secret_id IS
    '同项目加密 API key 引用，kind 必须为 http_bearer；不保存明文。';
COMMENT ON COLUMN project_llm_providers.model IS
    '管理员从 /v1/models 选定的模型标识，例如 gpt-5.6。';
COMMENT ON COLUMN project_llm_providers.version IS
    'LLM 配置的单调递增版本号。';
COMMENT ON COLUMN project_llm_providers.created_at IS
    'LLM 配置首次创建时的 PostgreSQL 时钟时间。';
COMMENT ON COLUMN project_llm_providers.updated_at IS
    'LLM 配置最近一次持久化变更时的 PostgreSQL 时钟时间。';
