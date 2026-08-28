-- Rollback 000005: 移除 incidents 的 lifecycle_generation 和 deployed_commit 字段。

ALTER TABLE incidents
    DROP COLUMN lifecycle_generation,
    DROP COLUMN deployed_commit;
