-- Lock 风险：COMMENT 只更新 PostgreSQL catalog，不重写表数据；执行时会短暂获取
-- 目标 relation 的 metadata lock，应避开长事务长期持有冲突锁的时段。
-- Transaction：runner 在同一 transaction 中写入全部表/字段注释并记录 checksum；
-- 任一 COMMENT 失败都会回滚本 migration 的全部 metadata 变更。
-- 兼容性：本 migration 不改变 column、constraint、index 或 query contract，可在
-- 依赖现有 schema 的 API binary 发布前后执行。
-- Rollback：删除注释会重新引入不可维护状态，因此不提供自动 down migration；
-- 注释修正使用新的 forward migration。

COMMENT ON TABLE fixthe_schema_migrations IS
    '记录已经成功提交的 forward migration，用于校验执行顺序和 SQL 内容不可变性。';
COMMENT ON COLUMN fixthe_schema_migrations.version IS
    'migration 的连续版本号，对应文件名中的六位数字前缀。';
COMMENT ON COLUMN fixthe_schema_migrations.name IS
    'migration 文件名中的稳定逻辑名称，不包含版本号和扩展名。';
COMMENT ON COLUMN fixthe_schema_migrations.checksum IS
    'migration SQL 原始内容的 SHA-256 十六进制摘要，用于拒绝已执行文件发生漂移。';
COMMENT ON COLUMN fixthe_schema_migrations.applied_at IS
    'runner 写入 migration history 记录时的 PostgreSQL 时钟时间。';

COMMENT ON TABLE users IS
    '保存可登录 FixThe 的本地用户身份、credential 摘要和授权角色。';
COMMENT ON COLUMN users.id IS
    '应用生成的 UUIDv7 用户内部标识，不作为登录凭据。';
COMMENT ON COLUMN users.username IS
    '规范化后的唯一登录名，仅允许小写字母开头及受限字符。';
COMMENT ON COLUMN users.password_hash IS
    '密码的自适应单向 hash；属于敏感 credential，不得存储明文或写入日志。';
COMMENT ON COLUMN users.role IS
    '授权角色，取值为 admin、operator 或 viewer。';
COMMENT ON COLUMN users.enabled IS
    '账户是否允许认证；false 表示账户已停用。';
COMMENT ON COLUMN users.version IS
    '用户聚合的单调递增版本号，供更新时执行乐观并发控制。';
COMMENT ON COLUMN users.created_at IS
    '用户记录首次创建时的 PostgreSQL 时钟时间。';
COMMENT ON COLUMN users.updated_at IS
    '用户记录最近一次持久化变更时的 PostgreSQL 时钟时间。';

COMMENT ON TABLE incidents IS
    '保存按稳定 fingerprint 聚合后的事故当前状态和列表摘要。';
COMMENT ON COLUMN incidents.id IS
    '应用生成的 UUIDv7 事故内部标识，不作为面向用户的事故编号。';
COMMENT ON COLUMN incidents.incident_number IS
    'PostgreSQL identity 生成的唯一递增事故编号，展示时可格式化为 INC-<number>。';
COMMENT ON COLUMN incidents.title IS
    '供事故列表和详情页展示的简短问题标题。';
COMMENT ON COLUMN incidents.fingerprint IS
    '归一化事件计算出的稳定去重键；相同 fingerprint 的事件聚合到同一事故。';
COMMENT ON COLUMN incidents.status IS
    '事故当前生命周期状态，取值为 Open、Recovered 或 Closed。';
COMMENT ON COLUMN incidents.priority IS
    '事故当前优先级，取值为 Info、P2 或 P1。';
COMMENT ON COLUMN incidents.source IS
    '首次创建该事故的事件来源稳定标识。';
COMMENT ON COLUMN incidents.first_seen IS
    '该 fingerprint 在当前事故中首次被观测到的时间。';
COMMENT ON COLUMN incidents.last_seen IS
    '该 fingerprint 在当前事故中最近一次被观测到的时间，也是默认列表排序依据。';
COMMENT ON COLUMN incidents.occurrence_count IS
    '聚合到当前事故的事件总数，首次创建时为 1。';
COMMENT ON COLUMN incidents.host_count IS
    '当前事故所影响的去重 host 数量，不得大于 occurrence_count。';
COMMENT ON COLUMN incidents.muted IS
    '是否静默当前事故的对外通知；静默不影响事件接收、聚合或生命周期更新。';
COMMENT ON COLUMN incidents.notification_summary IS
    '当前生效通知策略的简短可读摘要，用于事故界面展示。';
COMMENT ON COLUMN incidents.version IS
    '事故聚合的单调递增版本号，供更新时执行乐观并发控制。';
COMMENT ON COLUMN incidents.created_at IS
    '事故记录首次创建时的 PostgreSQL 时钟时间。';
COMMENT ON COLUMN incidents.updated_at IS
    '事故记录最近一次持久化变更时的 PostgreSQL 时钟时间。';
