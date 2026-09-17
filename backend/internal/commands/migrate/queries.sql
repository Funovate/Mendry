-- name: EnsureMigrationHistory :exec
CREATE TABLE IF NOT EXISTS fixthe_schema_migrations (
    version bigint PRIMARY KEY,
    name text NOT NULL,
    checksum text NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT fixthe_schema_migrations_version_positive CHECK (version > 0),
    CONSTRAINT fixthe_schema_migrations_name_not_empty CHECK (length(name) > 0),
    CONSTRAINT fixthe_schema_migrations_checksum_sha256 CHECK (checksum ~ '^[0-9a-f]{64}$')
);

-- name: TryAcquireMigrationLock :one
SELECT pg_try_advisory_lock($1);

-- name: ReleaseMigrationLock :one
SELECT pg_advisory_unlock($1);

-- name: ListAppliedMigrations :many
SELECT version, checksum
FROM fixthe_schema_migrations
ORDER BY version;

-- name: RecordAppliedMigration :exec
INSERT INTO fixthe_schema_migrations (version, name, checksum)
VALUES ($1, $2, $3);
