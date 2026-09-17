-- Persist immutable remediation execution constraints for provider-triggered runs.
-- Lock risk: one additive metadata column on remediation_run; existing rows receive
-- the repair-capable default and no historical attempt is rewritten.
-- Transaction: migration runner applies schema and history atomically.
-- Compatibility: old rows and non-AWS providers keep analysis_only=false.
-- Rollback: remove the column only after binaries stop reading it.

ALTER TABLE remediation_run
    ADD COLUMN analysis_only boolean NOT NULL DEFAULT false;

COMMENT ON COLUMN remediation_run.analysis_only IS
    'Immutable execution constraint inherited by continuation attempts; true permits diagnosis and proposed solutions but forbids patch, validation, publication, repair, and deployment effects.';
