-- Durable provider-neutral working-memory checkpoints for remediation runs.
-- Lock risk: one metadata-only ALTER TABLE on remediation_run plus two new
-- additive tables, one function, and one trigger; existing rows receive
-- conservative defaults and no run history is rewritten.
-- Transaction: the migration runner applies the schema and history row atomically.
-- Compatibility: legacy runs remain readable with agent_loop_mode 'legacy' and
-- no checkpoint rows; checkpoint rows exist only for runs that wrote them.
-- Rollback: remove only the tables, column, constraints, function, and trigger
-- introduced here; checkpoint history is not collapsed or reassigned.

ALTER TABLE projects
    ADD COLUMN agent_loop_mode text NOT NULL DEFAULT 'legacy',
    ADD COLUMN agent_loop_policy_version bigint NOT NULL DEFAULT 1,
    ADD CONSTRAINT projects_agent_loop_mode_known
        CHECK (agent_loop_mode IN ('legacy', 'resilient_v1')),
    ADD CONSTRAINT projects_agent_loop_policy_version_positive
        CHECK (agent_loop_policy_version > 0);

ALTER TABLE remediation_run
    ADD COLUMN agent_loop_mode text NOT NULL DEFAULT 'legacy',
    ADD COLUMN agent_loop_policy_version bigint NOT NULL DEFAULT 1,
    ADD CONSTRAINT remediation_run_agent_loop_mode_known
        CHECK (agent_loop_mode IN ('legacy', 'resilient_v1')),
    ADD CONSTRAINT remediation_run_agent_loop_policy_version_positive
        CHECK (agent_loop_policy_version > 0);

COMMENT ON COLUMN remediation_run.agent_loop_mode IS
    'Project remediation policy mode snapshotted at run creation; a mid-run configuration change cannot alter semantics, and legacy remains the default so existing projects are unaffected.';
COMMENT ON COLUMN remediation_run.agent_loop_policy_version IS
    'Version of the project remediation policy snapshotted by a root run and inherited unchanged by every continuation.';

ALTER TABLE remediation_evidence
    ADD COLUMN baseline_lifecycle_generation bigint,
    ADD COLUMN baseline_deployed_commit text,
    ADD CONSTRAINT remediation_evidence_baseline_pair
        CHECK ((baseline_lifecycle_generation IS NULL) = (baseline_deployed_commit IS NULL));

DROP INDEX remediation_evidence_scope_dedup_idx;
CREATE UNIQUE INDEX remediation_evidence_baseline_dedup_idx
    ON remediation_evidence(
        project_id, incident_id, run_id, baseline_lifecycle_generation,
        baseline_deployed_commit, deduplication_key
    )
    NULLS NOT DISTINCT;

COMMENT ON COLUMN remediation_evidence.baseline_lifecycle_generation IS
    'Incident lifecycle generation observed atomically when the evidence was admitted; NULL legacy rows fail closed for pre-run continuation reads.';
COMMENT ON COLUMN remediation_evidence.baseline_deployed_commit IS
    'Deployed commit observed atomically with baseline_lifecycle_generation when the evidence was admitted.';

CREATE TABLE remediation_checkpoint_event (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid NOT NULL REFERENCES remediation_run(id) ON DELETE CASCADE,
    sequence bigint NOT NULL,
    trigger_reason text NOT NULL,
    payload jsonb NOT NULL,
    content_hash text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT remediation_checkpoint_event_run_sequence_unique
        UNIQUE (run_id, sequence),
    CONSTRAINT remediation_checkpoint_event_sequence_positive
        CHECK (sequence > 0),
    CONSTRAINT remediation_checkpoint_event_trigger_reason_bounded
        CHECK (char_length(trigger_reason) <= 64),
    CONSTRAINT remediation_checkpoint_event_content_hash_sha256
        CHECK (content_hash ~ '^[0-9a-f]{64}$')
);

CREATE INDEX idx_remediation_checkpoint_event_run
    ON remediation_checkpoint_event(run_id);

CREATE TABLE remediation_working_memory (
    run_id uuid PRIMARY KEY REFERENCES remediation_run(id) ON DELETE CASCADE,
    sequence bigint NOT NULL,
    context_version bigint NOT NULL,
    observed_run_version bigint NOT NULL,
    phase text NOT NULL,
    content_hash text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT remediation_working_memory_sequence_positive
        CHECK (sequence > 0),
    CONSTRAINT remediation_working_memory_context_version_nonnegative
        CHECK (context_version >= 0),
    CONSTRAINT remediation_working_memory_observed_run_version_positive
        CHECK (observed_run_version > 0),
    CONSTRAINT remediation_working_memory_phase_bounded
        CHECK (char_length(phase) <= 64),
    CONSTRAINT remediation_working_memory_content_hash_sha256
        CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT remediation_working_memory_event_backing
        FOREIGN KEY (run_id, sequence)
        REFERENCES remediation_checkpoint_event (run_id, sequence)
        ON DELETE CASCADE
);

CREATE TABLE remediation_evidence_read_cursor (
    token_hash bytea PRIMARY KEY,
    run_id uuid NOT NULL REFERENCES remediation_run(id) ON DELETE CASCADE,
    evidence_id uuid NOT NULL REFERENCES remediation_evidence(id) ON DELETE CASCADE,
    content_hash text NOT NULL,
    byte_offset bigint NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT remediation_evidence_read_cursor_hash_sha256
        CHECK (octet_length(token_hash) = 32),
    CONSTRAINT remediation_evidence_read_cursor_content_hash_sha256
        CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT remediation_evidence_read_cursor_offset_positive
        CHECK (byte_offset > 0)
);

CREATE INDEX idx_remediation_evidence_read_cursor_expiry
    ON remediation_evidence_read_cursor(expires_at);

CREATE FUNCTION remediation_checkpoint_event_immutable() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION 'remediation_checkpoint_event rows are immutable and cannot be updated';
    END IF;
    -- DELETE triggers have NEW = NULL. Returning NEW would silently cancel the
    -- delete and break remediation_run ON DELETE CASCADE cleanup.
    RETURN OLD;
END;
$$;

CREATE TRIGGER remediation_checkpoint_event_immutable_trigger
    BEFORE UPDATE OR DELETE ON remediation_checkpoint_event
    FOR EACH ROW
    EXECUTE FUNCTION remediation_checkpoint_event_immutable();

COMMENT ON TABLE remediation_checkpoint_event IS
    'Append-only immutable working-memory checkpoint events; each event carries a canonical SHA-256 content hash, a monotonic per-run sequence, and the full checkpoint payload.';
COMMENT ON COLUMN remediation_checkpoint_event.id IS
    'Application-generated event UUID; events are never updated once inserted.';
COMMENT ON COLUMN remediation_checkpoint_event.run_id IS
    'Owning remediation run; deleting the run cascades its checkpoint history.';
COMMENT ON COLUMN remediation_checkpoint_event.sequence IS
    'Monotonic per-run sequence assigned by the store; the latest snapshot references the newest sequence.';
COMMENT ON COLUMN remediation_checkpoint_event.trigger_reason IS
    'Checkpoint trigger classification such as threshold, phase_boundary, recovery, or process_shutdown; never a model trace or error body.';
COMMENT ON COLUMN remediation_checkpoint_event.payload IS
    'Canonical WorkingMemoryCheckpointV1 JSON; summaries inside it never become evidence and verified facts retain their evidence IDs.';
COMMENT ON COLUMN remediation_checkpoint_event.content_hash IS
    'SHA-256 of the canonical payload; loading recomputes and rejects mismatches.';
COMMENT ON COLUMN remediation_checkpoint_event.created_at IS
    'PostgreSQL clock time when the event was appended.';

COMMENT ON TABLE remediation_working_memory IS
    'Latest working-memory snapshot per run; its sequence must be backed by a checkpoint event, and updating the snapshot is atomic with appending that event.';
COMMENT ON COLUMN remediation_working_memory.run_id IS
    'Owning remediation run; at most one snapshot per run.';
COMMENT ON COLUMN remediation_working_memory.sequence IS
    'Sequence of the backing checkpoint event; the foreign key rejects a snapshot whose sequence has no event.';
COMMENT ON COLUMN remediation_working_memory.context_version IS
    'Incident and evidence context version observed by the checkpoint; must match the run context version.';
COMMENT ON COLUMN remediation_working_memory.observed_run_version IS
    'Durable remediation_run.version observed by the checkpoint; older values require projection rebuild and future values are corrupt.';
COMMENT ON COLUMN remediation_working_memory.phase IS
    'Checkpoint phase such as diagnosing or planning; bounded, provider-neutral text.';
COMMENT ON COLUMN remediation_working_memory.content_hash IS
    'SHA-256 of the canonical checkpoint payload; must equal the backing event hash.';
COMMENT ON COLUMN remediation_working_memory.updated_at IS
    'PostgreSQL clock time of the latest snapshot update.';

COMMENT ON TABLE remediation_evidence_read_cursor IS
    'Short-lived server-authoritative evidence paging capabilities; only a random token digest is stored and every offset is bound to run, evidence, content hash, and expiry.';
