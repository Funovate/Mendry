ALTER TABLE project_llm_providers
    DROP CONSTRAINT project_llm_providers_provider_known,
    DROP CONSTRAINT project_llm_providers_api_mode_known,
    ADD CONSTRAINT project_llm_providers_provider_known
        CHECK (provider IN ('openai', 'anthropic')),
    ADD CONSTRAINT project_llm_providers_api_mode_known
        CHECK (api_mode IN ('chat_completions', 'responses', 'messages')),
    ADD CONSTRAINT project_llm_providers_api_mode_matches_provider
        CHECK ((provider = 'anthropic') = (api_mode = 'messages'));

COMMENT ON COLUMN project_llm_providers.api_mode IS
    'Generation endpoint: chat_completions uses /v1/chat/completions and responses uses /v1/responses (openai); messages uses the Anthropic /v1/messages API (anthropic).';
