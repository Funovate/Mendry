-- Rollback remediation continuation metadata only.

ALTER TABLE remediation_run
    DROP CONSTRAINT remediation_run_continuation_of_run_fk,
    DROP CONSTRAINT remediation_run_trigger_reason_bounded,
    DROP CONSTRAINT remediation_run_trigger_reason_known,
    DROP CONSTRAINT remediation_run_continuation_reason_bounded,
    DROP CONSTRAINT remediation_run_context_version_nonnegative,
    DROP CONSTRAINT remediation_run_terminal_reason_bounded,
    DROP COLUMN continuation_of_run_id,
    DROP COLUMN trigger_reason,
    DROP COLUMN continuation_reason,
    DROP COLUMN context_version,
    DROP COLUMN terminal_reason,
    DROP COLUMN retryable;
