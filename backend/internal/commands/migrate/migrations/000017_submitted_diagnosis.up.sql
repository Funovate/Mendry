-- Submitted-diagnosis audit rows plus durable tool-outcome authority for
-- exhaustion proofs (D4/D6/INC-2270).
-- Lock risk: one new additive table and three additive columns on
-- remediation_tool_invocation, with no data backfill or run-history rewrite.
-- Transaction: the migration runner applies the schema and history row atomically.
-- Compatibility: legacy and resilient_v1 runs both write audit rows; existing
-- runs/decisions/evidence remain readable, and the table never stores raw model
-- turns, prompts, or conversation text.
-- Rollback: drop the audit table, then the three additive invocation columns;
-- existing accepted decisions and coarse invocation history remain intact.

ALTER TABLE remediation_tool_invocation
    ADD COLUMN outcome_ref text,
    ADD COLUMN evidence_ids text[] NOT NULL DEFAULT ARRAY[]::text[],
    ADD COLUMN error_code text,
    ADD CONSTRAINT remediation_tool_invocation_outcome_ref_bounded
        CHECK (outcome_ref IS NULL OR char_length(outcome_ref) BETWEEN 1 AND 128),
    ADD CONSTRAINT remediation_tool_invocation_evidence_ids_bounded
        CHECK (cardinality(evidence_ids) <= 64),
    ADD CONSTRAINT remediation_tool_invocation_error_code_bounded
        CHECK (error_code IS NULL OR char_length(error_code) BETWEEN 1 AND 128),
    ADD CONSTRAINT remediation_tool_invocation_run_outcome_ref_unique
        UNIQUE (run_id, outcome_ref);

COMMENT ON COLUMN remediation_tool_invocation.outcome_ref IS
    'Service-issued action reference exposed in the bounded tool observation and used to prove a capability attempt; null for historical invocations.';
COMMENT ON COLUMN remediation_tool_invocation.evidence_ids IS
    'Bounded persisted evidence IDs produced by this invocation; payloads and raw tool output are never stored here.';
COMMENT ON COLUMN remediation_tool_invocation.error_code IS
    'Sanitized stable tool error code when outcome is error; never raw connector text or credentials.';

CREATE TABLE remediation_submitted_diagnosis (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid NOT NULL REFERENCES remediation_run (id) ON DELETE CASCADE,
    sequence int NOT NULL,
    fixability_class text NOT NULL,
    confidence_score numeric(3,2) NOT NULL,
    reasoning text NOT NULL,
    contradictions text[] NOT NULL DEFAULT ARRAY[]::text[],
    missing_evidence text[] NOT NULL DEFAULT ARRAY[]::text[],
    evidence_citations text[] NOT NULL DEFAULT ARRAY[]::text[],
    recommended_next_action text NOT NULL DEFAULT '',
    correction_kind text NOT NULL DEFAULT '',
    correction_evidence jsonb NOT NULL DEFAULT '[]'::jsonb,
    correction_count int NOT NULL DEFAULT 0,
    corrected boolean NOT NULL DEFAULT false,
    gate_outcome text NOT NULL DEFAULT '',
    decision_id uuid REFERENCES remediation_decision (id) ON DELETE SET NULL,
    submitted_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT remediation_submitted_diagnosis_run_sequence_unique
        UNIQUE (run_id, sequence),
    CONSTRAINT remediation_submitted_diagnosis_sequence_positive
        CHECK (sequence > 0),
    CONSTRAINT remediation_submitted_diagnosis_fixability_known
        CHECK (fixability_class IN ('code_fixable', 'external_dependency', 'configuration',
                                    'data', 'infrastructure', 'insufficient_evidence',
                                    'unsafe_to_automate')),
    CONSTRAINT remediation_submitted_diagnosis_confidence_valid
        CHECK (confidence_score >= 0 AND confidence_score <= 1),
    CONSTRAINT remediation_submitted_diagnosis_reasoning_bounded
        CHECK (char_length(reasoning) <= 131072),
    CONSTRAINT remediation_submitted_diagnosis_contradictions_bounded
        CHECK (cardinality(contradictions) <= 32),
    CONSTRAINT remediation_submitted_diagnosis_missing_evidence_bounded
        CHECK (cardinality(missing_evidence) <= 32),
    CONSTRAINT remediation_submitted_diagnosis_evidence_citations_bounded
        CHECK (cardinality(evidence_citations) <= 64),
    CONSTRAINT remediation_submitted_diagnosis_next_action_bounded
        CHECK (char_length(recommended_next_action) <= 2000),
    CONSTRAINT remediation_submitted_diagnosis_correction_kind_known
        CHECK (correction_kind IN ('', 'evidence_correction', 'exhaustion')),
    CONSTRAINT remediation_submitted_diagnosis_correction_evidence_array
        CHECK (jsonb_typeof(correction_evidence) = 'array'),
    CONSTRAINT remediation_submitted_diagnosis_correction_count_valid
        CHECK (correction_count >= 0 AND correction_count <= 32),
    CONSTRAINT remediation_submitted_diagnosis_corrected_consistent
        CHECK (
            (correction_kind = '' AND corrected = false AND correction_count = 0
                AND jsonb_array_length(correction_evidence) = 0)
            OR
            (correction_kind = 'evidence_correction' AND corrected = true AND correction_count > 0
                AND correction_count = jsonb_array_length(correction_evidence))
            OR
            (correction_kind = 'exhaustion' AND corrected = true AND correction_count = 1
                AND jsonb_array_length(correction_evidence) = 0)
        ),
    CONSTRAINT remediation_submitted_diagnosis_gate_outcome_known
        CHECK (gate_outcome IN ('', 'planning_eligible', 'rejected'))
);

CREATE INDEX idx_remediation_submitted_diagnosis_run
    ON remediation_submitted_diagnosis (run_id);

COMMENT ON TABLE remediation_submitted_diagnosis IS
    'Append-only audit of each model-submitted diagnosis envelope before the evidence gate, with bounded correction metadata; the accepted decision remains the review authority and raw model turns are never stored.';
COMMENT ON COLUMN remediation_submitted_diagnosis.id IS
    'Application-generated audit row UUID; rows are never updated once inserted.';
COMMENT ON COLUMN remediation_submitted_diagnosis.run_id IS
    'Owning remediation run; deleting the run cascades its submitted-diagnosis audit rows.';
COMMENT ON COLUMN remediation_submitted_diagnosis.sequence IS
    'Monotonic per-run sequence assigned by the store in append order.';
COMMENT ON COLUMN remediation_submitted_diagnosis.fixability_class IS
    'The model-submitted fixability from the original pre-gate envelope, preserved even when the evidence gate later rejects it.';
COMMENT ON COLUMN remediation_submitted_diagnosis.confidence_score IS
    'The model-submitted numeric confidence from the original pre-gate envelope.';
COMMENT ON COLUMN remediation_submitted_diagnosis.reasoning IS
    'Bounded structured causal reasoning from the envelope; never raw provider text or conversation history.';
COMMENT ON COLUMN remediation_submitted_diagnosis.contradictions IS
    'Model-declared contradiction summaries from the envelope, bounded to the same limit as remediation_decision.';
COMMENT ON COLUMN remediation_submitted_diagnosis.missing_evidence IS
    'Model-declared missing evidence identifiers from the envelope.';
COMMENT ON COLUMN remediation_submitted_diagnosis.evidence_citations IS
    'Model-cited evidence ID list from the envelope; must resolve against run-owned evidence.';
COMMENT ON COLUMN remediation_submitted_diagnosis.recommended_next_action IS
    'Model-recommended next action from the envelope, bounded and credential-free.';
COMMENT ON COLUMN remediation_submitted_diagnosis.correction_kind IS
    'Bounded correction classification: empty, evidence_correction (citation metadata), or exhaustion (accepted proof).';
COMMENT ON COLUMN remediation_submitted_diagnosis.correction_evidence IS
    'Bounded JSON array of corrected citations as {"evidenceId","storedClassification"} pairs showing only the authoritative stored classification.';
COMMENT ON COLUMN remediation_submitted_diagnosis.correction_count IS
    'Number of correction entries recorded, so audit can show a correction occurred without storing raw model text.';
COMMENT ON COLUMN remediation_submitted_diagnosis.corrected IS
    'True when the submission required a correction (citation mismatch or exhaustion acceptance).';
COMMENT ON COLUMN remediation_submitted_diagnosis.gate_outcome IS
    'Service-owned evidence gate verdict: empty when no gate ran, planning_eligible, or rejected; never derived from model text.';
COMMENT ON COLUMN remediation_submitted_diagnosis.decision_id IS
    'Accepted remediation_decision produced from this submission when one exists, so audit can join submitted to accepted; null when the submission was corrected without a decision.';
COMMENT ON COLUMN remediation_submitted_diagnosis.submitted_at IS
    'PostgreSQL clock time when the submitted diagnosis was recorded.';
