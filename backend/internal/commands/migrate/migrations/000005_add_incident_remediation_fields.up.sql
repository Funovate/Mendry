-- 添加 incidents 的 lifecycle_generation 和 deployed_commit 字段，支持 remediation 系列唯一键。
-- Lock 风险：短暂锁定 incidents 以新增列并回填默认值；应在低流量时执行。
-- Transaction：runner 在同一 transaction 中执行 schema、backfill 和 migration history 写入。
-- 兼容性：000001 至 000004 保持不可变。已有事故回填为 lifecycle_generation=1 和空 deployed_commit。
-- Rollback：可以安全 down，删除两列不影响事故核心数据。

ALTER TABLE incidents
    ADD COLUMN lifecycle_generation bigint NOT NULL DEFAULT 1,
    ADD COLUMN deployed_commit text NOT NULL DEFAULT '';

ALTER TABLE incidents
    ALTER COLUMN lifecycle_generation DROP DEFAULT,
    ALTER COLUMN deployed_commit DROP DEFAULT;

COMMENT ON COLUMN incidents.lifecycle_generation IS
    '事故生命周期代数，recovery→reopen 时递增，用于 remediation 系列唯一键。';
COMMENT ON COLUMN incidents.deployed_commit IS
    '触发 remediation 时从 repository 配置捕获的生产部署 commit hash。';
