-- Lock 风险：只创建 migration metadata table，并仅获取 CREATE TABLE IF NOT EXISTS
-- 必需的锁；当前 migration 不接触任何业务表。
-- Transaction：runner 持有进程级 advisory lock，并在同一 transaction 中执行
-- 本文件和记录 checksum。
-- 兼容性：可在 API 发布前后执行；本脚手架检查点尚不依赖业务 schema。
-- Rollback：删除 migration history 不具备数据安全性，因此不提供自动 down
-- migration；修正必须使用新的 forward migration。

CREATE TABLE IF NOT EXISTS fixthe_schema_migrations (
    version bigint PRIMARY KEY,
    name text NOT NULL,
    checksum text NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT fixthe_schema_migrations_version_positive CHECK (version > 0),
    CONSTRAINT fixthe_schema_migrations_name_not_empty CHECK (length(name) > 0),
    CONSTRAINT fixthe_schema_migrations_checksum_sha256 CHECK (checksum ~ '^[0-9a-f]{64}$')
);
