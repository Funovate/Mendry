-- Add project-owned operational evidence and the service-owned gate result.
-- The tables are additive: existing observations, incidents, and remediation
-- runs remain readable before the new evidence writer is enabled.

CREATE TABLE remediation_evidence (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    environment_id uuid NOT NULL,
    source_id uuid NOT NULL,
    incident_id uuid NOT NULL REFERENCES incidents (id) ON DELETE CASCADE,
    run_id uuid REFERENCES remediation_run (id) ON DELETE SET NULL,
    observation_id uuid REFERENCES observations (id) ON DELETE SET NULL,
    provider text NOT NULL,
    evidence_kind text NOT NULL,
    deduplication_key text NOT NULL,
    classification text NOT NULL DEFAULT 'contextual',
    outcome text NOT NULL DEFAULT 'success',
    available boolean NOT NULL DEFAULT true,
    primary_evidence boolean NOT NULL DEFAULT false,
    temporal_correlation boolean NOT NULL DEFAULT false,
    operational_correlation boolean NOT NULL DEFAULT false,
    occurred_at timestamptz,
    ingested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    content_hash text NOT NULL,
    byte_count bigint NOT NULL,
    provenance jsonb NOT NULL DEFAULT '{}'::jsonb,
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT remediation_evidence_provider_bounded CHECK (char_length(provider) BETWEEN 1 AND 64),
    CONSTRAINT remediation_evidence_kind_bounded CHECK (char_length(evidence_kind) BETWEEN 1 AND 96),
    CONSTRAINT remediation_evidence_dedup_bounded CHECK (char_length(deduplication_key) BETWEEN 1 AND 255),
    CONSTRAINT remediation_evidence_classification_known CHECK (classification IN ('direct_fault', 'correlated_supporting', 'contextual', 'unrelated', 'contradictory')),
    CONSTRAINT remediation_evidence_outcome_known CHECK (outcome IN ('success', 'empty', 'unavailable', 'invalid', 'redirect_rejected', 'oversized', 'timeout', 'permission_denied', 'truncated')),
    CONSTRAINT remediation_evidence_hash_format CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT remediation_evidence_byte_count_valid CHECK (byte_count >= 0 AND byte_count <= 8388608),
    CONSTRAINT remediation_evidence_provenance_object CHECK (jsonb_typeof(provenance) = 'object'),
    CONSTRAINT remediation_evidence_payload_bounded CHECK (octet_length(payload::text) <= 8388608),
    CONSTRAINT remediation_evidence_updated_after_created CHECK (updated_at >= created_at),
    CONSTRAINT remediation_evidence_project_environment_source_same_scope
        FOREIGN KEY (project_id, environment_id, source_id)
        REFERENCES project_sources (project_id, environment_id, id)
);

CREATE UNIQUE INDEX remediation_evidence_project_dedup_idx
    ON remediation_evidence (project_id, deduplication_key);
CREATE INDEX remediation_evidence_incident_idx
    ON remediation_evidence (project_id, incident_id, created_at DESC, id DESC);
CREATE INDEX remediation_evidence_run_idx
    ON remediation_evidence (run_id, created_at ASC, id ASC);
CREATE INDEX remediation_evidence_observation_idx
    ON remediation_evidence (observation_id);

CREATE TABLE remediation_evidence_assessment (
    run_id uuid PRIMARY KEY REFERENCES remediation_run (id) ON DELETE CASCADE,
    project_id uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    incident_id uuid NOT NULL REFERENCES incidents (id) ON DELETE CASCADE,
    confidence_cap numeric(3,2) NOT NULL,
    effective_confidence numeric(3,2) NOT NULL,
    planning_eligible boolean NOT NULL,
    outcome text NOT NULL,
    reasons text[] NOT NULL DEFAULT ARRAY[]::text[],
    missing_evidence text[] NOT NULL DEFAULT ARRAY[]::text[],
    contradictions text[] NOT NULL DEFAULT ARRAY[]::text[],
    direct_evidence_ids text[] NOT NULL DEFAULT ARRAY[]::text[],
    assessed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT remediation_evidence_assessment_confidence_cap_valid CHECK (confidence_cap >= 0 AND confidence_cap <= 1),
    CONSTRAINT remediation_evidence_assessment_effective_valid CHECK (effective_confidence >= 0 AND effective_confidence <= 1),
    CONSTRAINT remediation_evidence_assessment_outcome_bounded CHECK (char_length(outcome) BETWEEN 1 AND 64),
    CONSTRAINT remediation_evidence_assessment_arrays_bounded CHECK (
        cardinality(reasons) <= 32 AND cardinality(missing_evidence) <= 32 AND
        cardinality(contradictions) <= 32 AND cardinality(direct_evidence_ids) <= 64
    )
);

CREATE INDEX remediation_evidence_assessment_project_incident_idx
    ON remediation_evidence_assessment (project_id, incident_id, assessed_at DESC);

COMMENT ON TABLE remediation_evidence IS
    'Project-authorized operational evidence collected for an incident or remediation run; credential/control material is excluded from payload by the owning adapter.';
COMMENT ON COLUMN remediation_evidence.deduplication_key IS
    'Stable connector-owned append key used to make callback/detail persistence idempotent without storing a URL capability.';
COMMENT ON COLUMN remediation_evidence.classification IS
    'Trusted evidence classification used by the service-owned diagnosis gate, never assigned by model text.';
COMMENT ON COLUMN remediation_evidence.provenance IS
    'Bounded source and provider provenance metadata without credentials, tokens, or arbitrary fetch authority.';
COMMENT ON COLUMN remediation_evidence.payload IS
    'Complete bounded operational evidence JSON, including log content and original provider time fields.';
COMMENT ON TABLE remediation_evidence_assessment IS
    'Service-owned evidence gate decision for one remediation run; separate from model confidence and model response text.';
