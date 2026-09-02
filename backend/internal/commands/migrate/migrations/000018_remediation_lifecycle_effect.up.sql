-- Remediation lifecycle effect projections
-- Lock risk: one additive table; no existing rows are rewritten.
-- Transaction: migration runner applies schema and migration history atomically.
-- Compatibility: legacy runs do not write this table; resilient lifecycle code
-- requires its companion store and ports before entering patching.
-- Rollback: retain rows for audit; disabling resilient_v1 is the application rollback.

CREATE TABLE remediation_lifecycle_effect (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid NOT NULL REFERENCES remediation_run (id) ON DELETE CASCADE,
    effect_kind text NOT NULL,
    idempotency_key text NOT NULL,
    state text NOT NULL DEFAULT 'started',
    attempt int NOT NULL DEFAULT 1,
    baseline_commit text NOT NULL DEFAULT '',
    workspace_id text NOT NULL DEFAULT '',
    base_tree_hash text NOT NULL DEFAULT '',
    result_tree_hash text NOT NULL DEFAULT '',
    artifact_ref text NOT NULL DEFAULT '',
    content_hash text NOT NULL DEFAULT '',
    command_id text NOT NULL DEFAULT '',
    command_version bigint NOT NULL DEFAULT 0,
    validation_known boolean NOT NULL DEFAULT false,
    validation_passed boolean NOT NULL DEFAULT false,
    branch_ref text NOT NULL DEFAULT '',
    target_branch text NOT NULL DEFAULT '',
    commit_hash text NOT NULL DEFAULT '',
    draft_change_ref text NOT NULL DEFAULT '',
    compare_url text NOT NULL DEFAULT '',
    error_code text NOT NULL DEFAULT '',
    summary text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT remediation_lifecycle_effect_kind_known CHECK (
        effect_kind IN ('workspace', 'patch', 'validation', 'publication', 'workspace_cleanup')
    ),
    CONSTRAINT remediation_lifecycle_effect_state_known CHECK (
        state IN ('started', 'succeeded', 'recoverable', 'failed')
    ),
    CONSTRAINT remediation_lifecycle_effect_attempt_positive CHECK (attempt > 0),
    CONSTRAINT remediation_lifecycle_effect_key_bounded CHECK (char_length(idempotency_key) BETWEEN 1 AND 256),
    CONSTRAINT remediation_lifecycle_effect_baseline_bounded CHECK (char_length(baseline_commit) <= 256),
    CONSTRAINT remediation_lifecycle_effect_workspace_bounded CHECK (char_length(workspace_id) <= 256),
    CONSTRAINT remediation_lifecycle_effect_tree_hash_bounded CHECK (char_length(base_tree_hash) <= 128 AND char_length(result_tree_hash) <= 128),
    CONSTRAINT remediation_lifecycle_effect_artifact_bounded CHECK (char_length(artifact_ref) <= 512 AND char_length(content_hash) <= 128),
    CONSTRAINT remediation_lifecycle_effect_command_bounded CHECK (char_length(command_id) <= 128 AND command_version >= 0),
    CONSTRAINT remediation_lifecycle_effect_publication_bounded CHECK (
        char_length(branch_ref) <= 256 AND char_length(target_branch) <= 256 AND char_length(commit_hash) <= 256 AND
        char_length(draft_change_ref) <= 512 AND char_length(compare_url) <= 1024
    ),
    CONSTRAINT remediation_lifecycle_effect_error_bounded CHECK (char_length(error_code) <= 128),
    CONSTRAINT remediation_lifecycle_effect_summary_bounded CHECK (char_length(summary) <= 1024),
    CONSTRAINT remediation_lifecycle_effect_run_key_unique UNIQUE (run_id, effect_kind, idempotency_key)
);

CREATE INDEX idx_remediation_lifecycle_effect_run
    ON remediation_lifecycle_effect (run_id, updated_at DESC, id);

COMMENT ON TABLE remediation_lifecycle_effect IS
    'Latest bounded projection for a resilient workspace, patch, validation, or publication effect; raw patch/output and credentials are excluded.';
COMMENT ON COLUMN remediation_lifecycle_effect.id IS
    'Application-owned identity of one lifecycle effect projection.';
COMMENT ON COLUMN remediation_lifecycle_effect.run_id IS
    'Remediation attempt that owns the effect; cascade cleanup follows run lifecycle.';
COMMENT ON COLUMN remediation_lifecycle_effect.effect_kind IS
    'Bounded external effect class used to select recovery behavior and idempotency scope.';
COMMENT ON COLUMN remediation_lifecycle_effect.idempotency_key IS
    'Coordinator-generated stable key that makes retries and process restart address the same external effect.';
COMMENT ON COLUMN remediation_lifecycle_effect.state IS
    'Latest effect state; succeeded projections are reused and cannot be replaced by a later failure.';
COMMENT ON COLUMN remediation_lifecycle_effect.attempt IS
    'Bounded logical attempt count for the effect, independent of adapter-local transport retries.';
COMMENT ON COLUMN remediation_lifecycle_effect.baseline_commit IS
    'Immutable deployed source baseline used by workspace and publication replay.';
COMMENT ON COLUMN remediation_lifecycle_effect.workspace_id IS
    'Opaque run-owned workspace identity; no host path or engine authority is stored.';
COMMENT ON COLUMN remediation_lifecycle_effect.base_tree_hash IS
    'Content identity of the clean workspace tree before a patch.';
COMMENT ON COLUMN remediation_lifecycle_effect.result_tree_hash IS
    'Content identity expected after a patch or verified by publication replay.';
COMMENT ON COLUMN remediation_lifecycle_effect.artifact_ref IS
    'Content-addressed patch or approved artifact reference, never the artifact body.';
COMMENT ON COLUMN remediation_lifecycle_effect.content_hash IS
    'Hash of the referenced patch or effect artifact used for replay verification.';
COMMENT ON COLUMN remediation_lifecycle_effect.command_id IS
    'Administrator-approved validation command identity; arbitrary shell text is excluded.';
COMMENT ON COLUMN remediation_lifecycle_effect.command_version IS
    'Immutable validation command version snapshotted for this run.';
COMMENT ON COLUMN remediation_lifecycle_effect.validation_known IS
    'Whether the effect has an authoritative validation result rather than a model claim.';
COMMENT ON COLUMN remediation_lifecycle_effect.validation_passed IS
    'Authoritative validation outcome when validation_known is true.';
COMMENT ON COLUMN remediation_lifecycle_effect.branch_ref IS
    'Policy-approved change branch reference returned by the trusted publisher.';
COMMENT ON COLUMN remediation_lifecycle_effect.target_branch IS
    'Publication target branch snapshotted before the external effect; configuration changes cannot retarget this run.';
COMMENT ON COLUMN remediation_lifecycle_effect.commit_hash IS
    'Published change commit identity returned by the trusted publisher.';
COMMENT ON COLUMN remediation_lifecycle_effect.draft_change_ref IS
    'Optional provider-native draft change request identity; no merge authority.';
COMMENT ON COLUMN remediation_lifecycle_effect.compare_url IS
    'Optional safe compare link without embedded userinfo, tokens, or credentials.';
COMMENT ON COLUMN remediation_lifecycle_effect.error_code IS
    'Sanitized low-cardinality recovery or terminal error code.';
COMMENT ON COLUMN remediation_lifecycle_effect.summary IS
    'Bounded operator-safe effect summary; raw output and model text are excluded.';
COMMENT ON COLUMN remediation_lifecycle_effect.created_at IS
    'Time the effect projection was first created.';
COMMENT ON COLUMN remediation_lifecycle_effect.updated_at IS
    'Time the latest state or safe identity projection was written.';
