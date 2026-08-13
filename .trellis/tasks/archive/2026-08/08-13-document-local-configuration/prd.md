# 补齐本地配置字段注释

## Goal

让开发者只阅读 `backend/.env.example` 就能理解每个本地配置项控制的行为、填写格式和关键约束，不必反查 Go 配置加载器。

## Background

- `backend/internal/platform/config/config.go` 集中定义并验证 37 个 API / migration 运行配置项，包括进程、HTTP、PostgreSQL 和 Redis 配置。
- `backend/.env.example` 已列出这些运行配置项及 4 个 integration test 配置项，但大多只有示例值；目前只有 CORS、连接 URL 和测试隔离配置具有局部说明。
- `backend/README.md` 将 `.env.example` 指定为受支持变量和默认值的入口，因此该文件应当能独立回答本地配置问题。

## Requirements

- 按进程通用、HTTP、PostgreSQL、Redis 和 integration test 对配置项分组。
- 为 `backend/.env.example` 中每个配置项提供相邻的中文语义注释，说明它实际控制的行为，而不是简单翻译变量名。
- 在适用时说明值格式或单位、允许值或范围、是否必填、与其他字段的大小关系，以及 credential、endpoint 等安全注意事项。
- 注释必须与 `backend/internal/platform/config/config.go` 和 integration test target 校验中的当前默认值及约束一致。
- 保留可直接用于本地开发的示例值，不加入真实 credential、生产 endpoint 或未经实现的配置行为。
- 本任务只改文档和示例配置，不改变配置 key、默认值、校验规则或运行时行为。

## Out Of Scope

- 不为 Go 配置结构体逐字段增加重复的机械式注释；本地配置使用者的权威入口是 `backend/.env.example`。
- 不新增配置项、配置文件解析机制或 secret 管理能力。
- 不修改数据库 schema 字段注释。

## Acceptance Criteria

- [x] `backend/.env.example` 中全部 37 个运行配置项和 4 个 integration test 配置项均有相邻的用途说明。
- [x] 注释覆盖代码中存在的关键格式、取值范围、交叉字段约束和敏感信息边界，且不与加载器行为矛盾。
- [x] 配置 key 与示例值保持不变，使用者仍可直接基于该文件建立本地环境。
- [x] 自动化检查能够发现 `.env.example` 漏列或重复列出运行配置 key；后端现有配置测试保持通过。
