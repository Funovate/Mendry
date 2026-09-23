ALTER TABLE project_llm_providers
    DROP CONSTRAINT project_llm_providers_api_mode_known,
    DROP COLUMN api_mode;
