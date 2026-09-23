-- Fails while any project still uses the anthropic provider; switch those projects back to openai first.
ALTER TABLE project_llm_providers
    DROP CONSTRAINT project_llm_providers_api_mode_matches_provider,
    DROP CONSTRAINT project_llm_providers_api_mode_known,
    DROP CONSTRAINT project_llm_providers_provider_known,
    ADD CONSTRAINT project_llm_providers_provider_known
        CHECK (provider IN ('openai')),
    ADD CONSTRAINT project_llm_providers_api_mode_known
        CHECK (api_mode IN ('chat_completions', 'responses'));

COMMENT ON COLUMN project_llm_providers.api_mode IS
    'OpenAI-compatible generation endpoint: chat_completions uses /v1/chat/completions; responses uses /v1/responses.';
