ALTER TABLE projects
    ALTER COLUMN remediation_validation_profile
    SET DEFAULT '{"imageDigest":"","workingDirectory":"","preparation":[],"requiredCommands":[],"cpuLimit":2,"memoryLimitMiB":4096,"workspaceLimitMiB":10240}'::jsonb;

COMMENT ON COLUMN projects.remediation_validation_profile IS
    'Versioned immutable-image validation command configuration copied into new run checkpoints.';
