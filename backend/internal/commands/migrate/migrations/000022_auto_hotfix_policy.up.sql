-- Project-level automatic hotfix policy and immutable execution inputs.
-- Existing projects remain analysis-only and retain the conservative limits.

ALTER TABLE remediation_lifecycle_effect
    DROP CONSTRAINT remediation_lifecycle_effect_kind_known,
    ADD CONSTRAINT remediation_lifecycle_effect_kind_known CHECK (
        effect_kind IN ('workspace', 'patch', 'validation', 'publication', 'git_publish', 'change_request', 'workspace_cleanup')
    );

ALTER TABLE remediation_run
    ADD COLUMN execution_mode text NOT NULL DEFAULT 'analysis_only',
    ADD COLUMN validation_commands jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN validation_image_digest text NOT NULL DEFAULT '',
    ADD COLUMN execution_profile jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN publication_snapshot jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN change_policy jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN publication_target_branch text NOT NULL DEFAULT '',
    ADD COLUMN publication_branch_prefix text NOT NULL DEFAULT 'hotfix/remediation',
    ADD CONSTRAINT remediation_run_execution_mode_known
        CHECK (execution_mode IN ('analysis_only', 'auto_hotfix'));

COMMENT ON COLUMN remediation_run.execution_mode IS
    'Immutable project execution mode captured by a root run and inherited by continuations.';
COMMENT ON COLUMN remediation_run.validation_commands IS
    'Required validation command ID/version map captured at root creation.';
COMMENT ON COLUMN remediation_run.validation_image_digest IS
    'Immutable validation image digest captured at root creation.';
COMMENT ON COLUMN remediation_run.execution_profile IS
    'Complete immutable validation argv, timeout, working directory, and resource profile captured at root creation.';
COMMENT ON COLUMN remediation_run.publication_snapshot IS
    'Credential-free repository identity plus credential references and versions captured at root creation.';
COMMENT ON COLUMN remediation_run.change_policy IS
    'Immutable allowed path and change-size policy captured at root creation.';
COMMENT ON COLUMN remediation_run.publication_target_branch IS
    'Production branch captured as the review target at root creation.';

ALTER TABLE projects
    ADD COLUMN remediation_execution_mode text NOT NULL DEFAULT 'analysis_only',
    ADD COLUMN remediation_validation_profile jsonb NOT NULL DEFAULT '{"enabled":false,"imageDigest":"","workingDirectory":"","preparation":[],"requiredCommands":[],"cpuLimit":2,"memoryLimitMiB":4096,"workspaceLimitMiB":10240}'::jsonb,
    ADD COLUMN remediation_publication jsonb NOT NULL DEFAULT '{"branchPrefix":"hotfix/remediation","gitCredentialSecretId":"","apiCredentialSecretId":"","apiBaseUrl":""}'::jsonb,
    ADD COLUMN remediation_change_policy jsonb NOT NULL DEFAULT '{"allowedPaths":["**"],"deniedPaths":[],"maxChangedFiles":10,"maxChangedLines":400}'::jsonb,
    ADD CONSTRAINT projects_remediation_execution_mode_known
        CHECK (remediation_execution_mode IN ('analysis_only', 'auto_hotfix')),
    ADD CONSTRAINT projects_auto_hotfix_resilient
        CHECK (remediation_execution_mode <> 'auto_hotfix' OR agent_loop_mode = 'resilient_v1'),
    ADD CONSTRAINT projects_remediation_validation_profile_object
        CHECK (jsonb_typeof(remediation_validation_profile) = 'object'),
    ADD CONSTRAINT projects_remediation_publication_object
        CHECK (jsonb_typeof(remediation_publication) = 'object'),
    ADD CONSTRAINT projects_remediation_change_policy_object
        CHECK (jsonb_typeof(remediation_change_policy) = 'object');

COMMENT ON COLUMN projects.remediation_execution_mode IS
    'Project remediation execution policy. Existing projects default to analysis_only.';
COMMENT ON COLUMN projects.remediation_validation_profile IS
    'Optional local validation configuration. enabled=false publishes a constrained draft change for repository CI; enabled=true freezes immutable runner configuration.';
COMMENT ON COLUMN projects.remediation_publication IS
    'Branch naming and project-owned Git/provider credential references; never plaintext credentials.';
COMMENT ON COLUMN projects.remediation_change_policy IS
    'Allowed/denied path rules and server-enforced file/line limits for automatic publication.';
