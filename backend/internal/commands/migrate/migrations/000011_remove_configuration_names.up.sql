-- 移除 project source / trigger 上没有领域身份意义的用户别名。
-- Lock 风险：短暂 ALTER project_sources 与 project_triggers，删除约束和列；不改写其它业务行。
-- Transaction：runner 在同一 transaction 中执行 schema 变更和 history 写入。
-- 兼容性：这是协调部署迁移；旧二进制在迁移后仍读写已删除列会失败。
-- 历史 incidents.source 快照保持原值，之后新事故使用配置 source kind。
-- Rollback：删除的别名没有可靠来源可重建，不提供自动 down；修正使用新的 forward migration。

ALTER TABLE project_sources
    DROP CONSTRAINT project_sources_project_name_unique,
    DROP CONSTRAINT project_sources_name_bounded,
    DROP COLUMN name;

ALTER TABLE project_triggers
    DROP CONSTRAINT project_triggers_project_name_unique,
    DROP CONSTRAINT project_triggers_name_bounded,
    DROP COLUMN name;
