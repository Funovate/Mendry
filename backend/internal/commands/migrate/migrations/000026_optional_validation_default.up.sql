-- Keep migration 000022 immutable. Only new projects receive this default;
-- existing project policies and run snapshots retain their validation behavior.
ALTER TABLE projects
    ALTER COLUMN remediation_validation_profile
    SET DEFAULT '{"enabled":false,"imageDigest":"","workingDirectory":"","preparation":[],"requiredCommands":[],"cpuLimit":2,"memoryLimitMiB":4096,"workspaceLimitMiB":10240}'::jsonb;

COMMENT ON COLUMN projects.remediation_validation_profile IS
    'Optional local validation configuration. enabled=false publishes a constrained draft change for repository CI; enabled=true freezes immutable runner configuration.';
