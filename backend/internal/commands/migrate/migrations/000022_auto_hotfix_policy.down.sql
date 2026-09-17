ALTER TABLE remediation_lifecycle_effect
    DROP CONSTRAINT IF EXISTS remediation_lifecycle_effect_kind_known,
    ADD CONSTRAINT remediation_lifecycle_effect_kind_known CHECK (
        effect_kind IN ('workspace', 'patch', 'validation', 'publication', 'workspace_cleanup')
    );

ALTER TABLE remediation_run
    DROP CONSTRAINT IF EXISTS remediation_run_execution_mode_known,
    DROP COLUMN IF EXISTS publication_branch_prefix,
    DROP COLUMN IF EXISTS publication_target_branch,
    DROP COLUMN IF EXISTS change_policy,
    DROP COLUMN IF EXISTS publication_snapshot,
    DROP COLUMN IF EXISTS execution_profile,
    DROP COLUMN IF EXISTS validation_image_digest,
    DROP COLUMN IF EXISTS validation_commands,
    DROP COLUMN IF EXISTS execution_mode;

ALTER TABLE projects
    DROP CONSTRAINT IF EXISTS projects_remediation_change_policy_object,
    DROP CONSTRAINT IF EXISTS projects_remediation_publication_object,
    DROP CONSTRAINT IF EXISTS projects_remediation_validation_profile_object,
    DROP CONSTRAINT IF EXISTS projects_auto_hotfix_resilient,
    DROP CONSTRAINT IF EXISTS projects_remediation_execution_mode_known,
    DROP COLUMN IF EXISTS remediation_change_policy,
    DROP COLUMN IF EXISTS remediation_publication,
    DROP COLUMN IF EXISTS remediation_validation_profile,
    DROP COLUMN IF EXISTS remediation_execution_mode;
