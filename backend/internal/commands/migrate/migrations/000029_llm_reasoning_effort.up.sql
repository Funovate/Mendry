ALTER TABLE project_llm_providers
    ADD COLUMN reasoning_effort text NOT NULL DEFAULT 'default',
    ADD CONSTRAINT project_llm_providers_reasoning_effort_known
        CHECK (reasoning_effort IN ('default', 'low', 'medium', 'high'));

COMMENT ON COLUMN project_llm_providers.reasoning_effort IS
    'Optional provider reasoning intensity. default omits the provider parameter; explicit values are low, medium, or high.';
