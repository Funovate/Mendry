-- Remediation persistence tables
-- additive only, no existing data affected

CREATE TABLE remediation_series (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id uuid NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    lifecycle_generation bigint NOT NULL,
    deployed_commit text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (incident_id, lifecycle_generation, deployed_commit)
);

CREATE INDEX idx_remediation_series_incident ON remediation_series(incident_id);

CREATE TABLE remediation_run (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    series_id uuid NOT NULL REFERENCES remediation_series(id) ON DELETE CASCADE,
    attempt_number int NOT NULL,
    state text NOT NULL,
    started_at timestamptz NOT NULL DEFAULT now(),
    ended_at timestamptz,
    elapsed_ms bigint,
    model_calls int NOT NULL DEFAULT 0,
    model_tokens_in bigint NOT NULL DEFAULT 0,
    model_tokens_out bigint NOT NULL DEFAULT 0,
    model_cost_cents bigint NOT NULL DEFAULT 0,
    model_provider text NOT NULL DEFAULT '',
    model_name text NOT NULL DEFAULT '',
    tool_calls int NOT NULL DEFAULT 0,
    evidence_bytes bigint NOT NULL DEFAULT 0,
    repository_bytes bigint NOT NULL DEFAULT 0,
    version bigint NOT NULL DEFAULT 1,
    UNIQUE (series_id, attempt_number)
);

CREATE INDEX idx_remediation_run_series ON remediation_run(series_id);
CREATE INDEX idx_remediation_run_state ON remediation_run(state);

COMMENT ON COLUMN remediation_run.model_cost_cents IS
    'Aggregated provider-reported model usage cost in cents, when available; never stores provider request or response payloads.';
COMMENT ON COLUMN remediation_run.model_provider IS
    'Safe LLM provider identifier used for the run, such as openai; excludes API keys, credentials, and raw provider payloads.';
COMMENT ON COLUMN remediation_run.model_name IS
    'Safe model identifier used for the run, such as gpt-5.6; excludes raw provider request and response payloads.';

CREATE TABLE remediation_decision (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid NOT NULL REFERENCES remediation_run(id) ON DELETE CASCADE,
    sequence int NOT NULL,
    fixability_class text NOT NULL,
    confidence_score numeric(3,2) NOT NULL,
    reasoning text NOT NULL,
    decided_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (run_id, sequence)
);

CREATE INDEX idx_remediation_decision_run ON remediation_decision(run_id);

CREATE TABLE remediation_plan (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid NOT NULL REFERENCES remediation_run(id) ON DELETE CASCADE,
    sequence int NOT NULL,
    title text NOT NULL,
    rationale text NOT NULL,
    risk_class text NOT NULL,
    is_recommended boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (run_id, sequence)
);

CREATE INDEX idx_remediation_plan_run ON remediation_plan(run_id);

CREATE TABLE remediation_artifact (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid NOT NULL REFERENCES remediation_run(id) ON DELETE CASCADE,
    artifact_type text NOT NULL,
    reference_path text NOT NULL,
    content_hash text NOT NULL,
    size_bytes bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_remediation_artifact_run ON remediation_artifact(run_id);

CREATE TABLE remediation_tool_invocation (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid NOT NULL REFERENCES remediation_run(id) ON DELETE CASCADE,
    sequence int NOT NULL,
    tool_name text NOT NULL,
    phase text NOT NULL,
    invoked_at timestamptz NOT NULL DEFAULT now(),
    duration_ms bigint,
    outcome text NOT NULL,
    UNIQUE (run_id, sequence)
);

CREATE INDEX idx_remediation_tool_invocation_run ON remediation_tool_invocation(run_id);
