-- Add safe continuation metadata to remediation attempts.
-- Lock risk: one metadata-only ALTER TABLE on remediation_run; existing rows
-- receive conservative defaults and no run history is rewritten.
-- Transaction: the migration runner applies the schema and history row atomically.
-- Compatibility: prior attempts remain readable with an empty origin/reason,
-- context version 0, no terminal retryability, and no predecessor.
-- Rollback: remove only the columns and constraints introduced here; attempt
-- history is not collapsed or reassigned.

ALTER TABLE remediation_run
    ADD COLUMN continuation_of_run_id uuid,
    ADD COLUMN trigger_reason text NOT NULL DEFAULT '',
    ADD COLUMN continuation_reason text NOT NULL DEFAULT '',
    ADD COLUMN context_version bigint NOT NULL DEFAULT 0,
    ADD COLUMN terminal_reason text NOT NULL DEFAULT '',
    ADD COLUMN retryable boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT remediation_run_continuation_of_run_fk
        FOREIGN KEY (continuation_of_run_id)
        REFERENCES remediation_run (id)
        ON DELETE SET NULL,
    ADD CONSTRAINT remediation_run_trigger_reason_bounded
        CHECK (char_length(trigger_reason) <= 32),
    ADD CONSTRAINT remediation_run_trigger_reason_known
        CHECK (trigger_reason IN ('', 'automatic', 'manual', 'automatic_continue', 'manual_continue')),
    ADD CONSTRAINT remediation_run_continuation_reason_bounded
        CHECK (char_length(continuation_reason) <= 512),
    ADD CONSTRAINT remediation_run_context_version_nonnegative
        CHECK (context_version >= 0),
    ADD CONSTRAINT remediation_run_terminal_reason_bounded
        CHECK (char_length(terminal_reason) <= 128);

COMMENT ON COLUMN remediation_run.continuation_of_run_id IS
    'Direct predecessor run for a continuation attempt; NULL identifies a root attempt and prior runs remain immutable history.';
COMMENT ON COLUMN remediation_run.trigger_reason IS
    'Safe origin classification: automatic, manual, automatic_continue, or manual_continue; never a webhook body or provider message.';
COMMENT ON COLUMN remediation_run.continuation_reason IS
    'Bounded operator or system reason for creating this continuation; contains no credentials, prompts, payloads, or unrestricted error text.';
COMMENT ON COLUMN remediation_run.context_version IS
    'Incident and evidence context version observed when this attempt was admitted; used to require newer context for automatic continuation.';
COMMENT ON COLUMN remediation_run.terminal_reason IS
    'Safe terminal classification recorded by remediation; never stores a raw provider, model, connector, or database error.';
COMMENT ON COLUMN remediation_run.retryable IS
    'Service-owned eligibility marker for bounded automatic continuation; false is conservative for legacy and non-transient outcomes.';
