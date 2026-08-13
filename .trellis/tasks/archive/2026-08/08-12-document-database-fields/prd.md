# 补齐数据库和结构体字段注释

## Goal

让 PostgreSQL schema 和由 sqlc 生成的 Go 数据结构能够直接说明每张表、每个字段的业务语义，维护者无需从约束、查询或 Trellis 文档中反推含义。

## Background

- `backend/internal/commands/migrate/migrations/000001_initialize_schema.up.sql:9` 创建 migration history 表，但没有表或字段注释。
- `backend/internal/commands/migrate/migrations/000002_create_mvp_data.up.sql:10` 创建 `users` 和 `incidents`，但没有任何 `COMMENT ON TABLE` / `COMMENT ON COLUMN`。
- `backend/internal/modules/auth/adapter/postgres/authdb/models.go:11` 和 `backend/internal/modules/incidents/adapter/postgres/incidentdb/models.go:11` 是 sqlc 生成文件，当前结构体及字段没有注释，且不得手工修改。
- sqlc v1.31.1 会读取 PostgreSQL schema 中的 table/column comment 并生成到 Go model，因此数据库 comment 是两层注释的单一事实来源。

## Requirements

- 新增一个连续版本的 forward migration，为 `fixthe_schema_migrations`、`users`、`incidents` 添加表注释，并为三张表的全部 28 个字段添加字段注释。
- 注释说明业务用途，并在必要时明确生成方、允许值、时间语义、敏感性、并发版本或不可变性；避免只把字段名翻译成中文。
- 解释性文字使用中文，保留 PostgreSQL、UUIDv7、SHA-256、credential、sqlc 等规范技术名称和代码值。
- 不修改已经存在的 `000001`、`000002` migration；不手工编辑 sqlc 生成文件。
- 重新运行 sqlc，使数据库表模型 `User`、`Incident` 及其全部字段获得由 schema comment 派生的注释。sqlc v1.31.1 不会把 column comment 传播到临时 query parameter struct；这些 adapter 内生成载体不手工修改，也不作为业务数据模型暴露。
- 增加静态 migration 测试，防止表注释或字段注释从 migration 源文件中被遗漏。
- 增加 PostgreSQL integration 断言，验证应用 migration 后 `obj_description` / `col_description` 中的三张表和全部字段均有非空注释。
- 更新数据库规范，明确新表和字段必须有语义注释，并保持 schema comment 为生成代码注释的来源。

## Out Of Scope

- 不新增、删除或改变业务字段、约束、索引、默认值和查询行为。
- 不为所有通用 Go 配置或基础设施结构体逐字段补充机械式注释；本任务只处理与数据库 schema 对应的 sqlc 数据结构。
- 不修改数据库中已经记录的旧 migration checksum。

## Acceptance Criteria

- [x] 新 migration 包含 3 条 `COMMENT ON TABLE` 和 28 条 `COMMENT ON COLUMN`，每条注释均非空且具有语义。
- [x] `make generate` 后的 `User`、`Incident` 类型及其全部字段带有对应注释，`make generate-check` 通过。
- [x] migration 单元测试能验证完整的表/字段注释覆盖，PostgreSQL integration test 能从 catalog 验证实际注释覆盖。
- [x] `go vet ./...`、`go test ./...`、`go test -race ./...` 和 `go build ./cmd/...` 通过。
- [x] 现有 schema 和查询行为没有变化。

## Verification

- `go vet ./...`、`go test ./...`、`go test -race ./...`、`go build ./cmd/...` 和 `make generate-check` 已通过。
- `go test -tags=integration -run '^$' ./tests/integration` 已通过，证明 PostgreSQL integration test 可编译。
- 当前环境未配置 `FIXTHE_TEST_POSTGRES_URL` 与 `FIXTHE_TEST_POSTGRES_ISOLATION`，因此未连接真实隔离 PostgreSQL 执行 catalog 断言。
