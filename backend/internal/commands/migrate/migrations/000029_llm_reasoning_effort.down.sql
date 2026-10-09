ALTER TABLE project_llm_providers
    DROP CONSTRAINT project_llm_providers_reasoning_effort_known,
    DROP COLUMN reasoning_effort;
