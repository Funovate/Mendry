ALTER TABLE project_llm_providers
    ADD COLUMN api_mode text NOT NULL DEFAULT 'chat_completions',
    ADD CONSTRAINT project_llm_providers_api_mode_known
        CHECK (api_mode IN ('chat_completions', 'responses'));

COMMENT ON COLUMN project_llm_providers.api_mode IS
    'OpenAI-compatible generation endpoint: chat_completions uses /v1/chat/completions; responses uses /v1/responses.';
