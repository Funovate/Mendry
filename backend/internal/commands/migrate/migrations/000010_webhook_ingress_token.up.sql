-- 为 signed_webhook trigger 增加不可猜测的入站 token 存储。
-- Lock 风险：短暂 ALTER project_triggers 并新增部分唯一索引；不改写已有业务行。
-- Transaction：runner 在同一 transaction 中执行 schema、注释和 history 写入。
-- 兼容性：000001 至 000009 保持不可变。已有 signed_webhook 行可以没有 token；
-- PUT configuration 或专门的 rotate 接口才会生成。
-- Rollback：删除列会丢掉已分发的入站 URL，因此不提供自动 down；
-- 修正使用新的 forward migration。

ALTER TABLE project_triggers
    ADD COLUMN ingress_token_hash bytea,
    ADD COLUMN ingress_token_ciphertext bytea,
    ADD COLUMN ingress_token_nonce bytea;

ALTER TABLE project_triggers
    ADD CONSTRAINT project_triggers_ingress_token_all_or_nothing CHECK (
        (ingress_token_hash IS NULL AND ingress_token_ciphertext IS NULL AND ingress_token_nonce IS NULL)
        OR (
            ingress_token_hash IS NOT NULL
            AND ingress_token_ciphertext IS NOT NULL
            AND ingress_token_nonce IS NOT NULL
            AND octet_length(ingress_token_hash) = 32
        )
    );

CREATE UNIQUE INDEX project_triggers_ingress_token_hash_unique_idx
    ON project_triggers (ingress_token_hash)
    WHERE ingress_token_hash IS NOT NULL;

COMMENT ON COLUMN project_triggers.ingress_token_hash IS
    '入站 webhook token 的 SHA-256 查找哈希；明文永不落库，未知哈希与未启用 trigger 对外不可区分。';
COMMENT ON COLUMN project_triggers.ingress_token_ciphertext IS
    '同一 AES-256-GCM 密文，仅供项目管理员揭示完整入站 URL；关联数据绑定项目、trigger 和 webhook_token 上下文。';
COMMENT ON COLUMN project_triggers.ingress_token_nonce IS
    '入站 token 密文对应的 12 字节 GCM nonce；与 hash、ciphertext 必须同时为空或同时存在。';
