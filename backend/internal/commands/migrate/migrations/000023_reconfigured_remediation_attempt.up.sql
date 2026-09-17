-- Allow an operator to start a fresh attempt under the current project policy.
-- Existing run snapshots remain immutable; this only widens the safe origin enum.
ALTER TABLE remediation_run
    DROP CONSTRAINT remediation_run_trigger_reason_known,
    ADD CONSTRAINT remediation_run_trigger_reason_known
        CHECK (trigger_reason IN ('', 'automatic', 'manual', 'automatic_continue', 'manual_continue', 'manual_reconfigure'));
