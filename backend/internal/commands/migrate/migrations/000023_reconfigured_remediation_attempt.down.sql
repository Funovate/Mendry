ALTER TABLE remediation_run
    DROP CONSTRAINT IF EXISTS remediation_run_trigger_reason_known,
    ADD CONSTRAINT remediation_run_trigger_reason_known
        CHECK (trigger_reason IN ('', 'automatic', 'manual', 'automatic_continue', 'manual_continue'));
