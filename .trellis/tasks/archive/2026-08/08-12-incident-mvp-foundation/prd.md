# 收敛事故管理 MVP 基础设施

## Goal

把当前静态原型推进为以项目为数据和授权边界、配置可持久化、事故与事件可追溯的
事故管理 MVP。用户登录后只能发现自己有权访问的项目，并能按项目角色查看日志事件、
处理事故或管理项目成员与采集配置。

MVP 运行时只依赖 PostgreSQL 和 Redis。PostgreSQL 持久化用户、项目、成员关系、环境、
Git 仓库、采集源、触发器、加密凭据、事件、事故和审计记录；Redis 只保存可过期、
可撤销的服务端登录 session。

## Background

- 当前数据库只有 `users` 和全局 `incidents`，事故没有项目、环境或采集源归属。
- 当前 `/api/v1/incidents` 是全局接口，任意已登录用户都能发现同一批事故。
- 高清原型中的项目、Git remote、SSH/MCP 采集源、触发方式和凭据引用仅存在于
  React state/fixture，刷新后丢失，也没有后端授权边界。
- 原始产品设计已经把 Project 定义为 Source、Observation、Incident、retention 和
  audit 的主要归属边界；当前实现偏离了该约束。
- 已应用的 migration `000001` 至 `000003` 不可修改，修正必须通过新的 forward
  migration 完成。

## Requirements

- R1：API 运行时只要求 PostgreSQL 和 Redis；迁移只要求 PostgreSQL。仓库不得要求
  RabbitMQ、worker、job outbox 或远程 OTLP collector。
- R2：保留结构化日志、HTTP 超时与优雅关闭、PostgreSQL 连接池、显式 migration、
  健康检查、统一 JSON 错误、request ID、panic recovery、请求体限制和可配置 CORS。
- R3：本地用户密码 hash 持久化在 PostgreSQL，session 存入 Redis 并通过安全的
  `HttpOnly` cookie 传递；首个系统管理员由显式 bootstrap 命令创建。
- R4：Project 是环境、仓库、采集源、触发器、凭据、Observation、Incident 和审计
  记录的强制归属边界。未提供或无权访问 project context 的业务查询不得退化为全局查询。
- R5：系统管理员可以创建项目并管理所有项目；普通用户只有成为项目成员后才能发现和
  访问该项目。项目成员角色为 `admin`、`operator`、`viewer`：项目 admin 管理成员和
  配置，operator 查看事件和事故并更新事故流程，viewer 只读。应用用例层必须再次
  执行授权，不能只依赖路由 middleware 或前端隐藏按钮。
- R6：项目配置持久化项目 key/name、一个或多个 environment 及可选 service、Git remote、
  SCM provider、transport credential reference、production branch 和 immutable deployed
  commit。项目 key 是稳定 URL 标识，不因显示名称修改而变化。
- R7：项目持久化 SSH、Cloud 或 MCP 采集源。SSH 配置覆盖 host、port、user、auth
  reference、project folder、log path 和 read mode；MCP 配置覆盖 endpoint、transport、
  headers、credential reference、evidence profile、query scope 和 capabilities；配置 API
  不得回传 credential 明文。
- R8：项目持久化 signed webhook 或 custom rule 触发器。Webhook 配置覆盖 signing
  credential reference、event types 和 deduplication key；custom rule 覆盖 name、
  grouping window 和 match expression。
- R9：凭据值必须使用部署密钥加密后写入 PostgreSQL。写入后读取接口只返回稳定 ID、
  名称、类型和更新时间；不得返回明文、ciphertext、nonce 或可用于离线分析的内部字段。
- R10：Observation 作为项目 Event Stream 的持久事件，必须归属 project、environment
  和 source，并提供有界、按时间倒序的项目内读取接口。日志内容不得跨项目泄露。
- R11：Incident 必须归属 project、environment 和 source。事故接口改为
  `/api/v1/projects/{projectKey}/incidents...`，并保持创建、列表、详情和状态更新能力；
  旧的全局 `/api/v1/incidents` 不再暴露业务数据。
- R12：成员、配置和事故状态变更写入项目审计记录，至少保存 actor、action、target、
  安全摘要和发生时间；审计内容不得包含密码、session token、credential 明文或 ciphertext。
- R13：项目、成员、配置、Observation 和 Incident API 使用稳定、版本化 JSON contract；
  输入错误、禁止访问和资源不存在使用可测试的状态码。对非成员，项目不存在和无权访问
  都返回相同的 not-found 边界，避免项目枚举。
- R14：提供从空数据库到可访问项目的本地路径：migration、管理员 bootstrap、项目创建、
  成员/配置写入、可选开发事件与事故种子、API 和前端。前端联调只在项目边界后端完成后开始。

## Out Of Scope

- 异步任务、重试、死信、事务 outbox、RabbitMQ 和 worker。
- 真实 SSH/MCP/Cloud 连接执行、轮询调度、webhook ingress 验签和自动事故分组；本次先
  持久化并授权这些配置，建立后续 connector runtime 的可靠输入。
- Redis 缓存、限流或幂等键；Redis 在本次只用于登录 session。
- 远程 OpenTelemetry exporter、指标平台、分布式 trace、通知投递、LLM 和修复 PR 流程。
- 用户管理 UI、密码重置、SSO、生产级密钥轮换/KMS 和生产部署高可用。
- 在项目边界后端完成前，将静态原型大面积接入 API。

## Disposition

Archived 2026-08-20. The implement checklist is complete and this is the live PG+Redis project-boundary foundation. Later webhook-contract revisions belong to `08-19-signed-webhook-ingress`, not leftover work here. In the MVP-tree cleanup, record this task as the replacement for `08-10-incident-service-foundation`.

## Acceptance Criteria

- [ ] 新增 forward migration `000004`；`000001` 至 `000003` 内容保持不变。迁移后包含
      projects、environments、memberships、secrets、repositories、sources、triggers、
      observations、audit events，并为 incidents 建立 project/environment/source 归属。
- [ ] migration 对可能存在的旧 incident 行有明确且可执行的兼容处理；不会产生 project、
      environment 或 source 为空的 incident。
- [ ] 系统管理员可以创建项目；成员只看到自己有权访问的项目；非成员无法通过列表、详情、
      配置、事件或事故接口确认项目是否存在。
- [ ] 项目 admin 可以管理成员和配置，operator 可以读取事件/事故并更新事故状态，viewer
      只能读取；HTTP 和应用层授权测试均覆盖拒绝路径。
- [ ] 项目 key/name、environment/service、Git remote/provider/credential/branch/commit、
      SSH/Cloud/MCP source 和 signed-webhook/custom-rule trigger 刷新后仍能从 API 读回。
- [ ] credential 明文只出现在写请求和进程内短生命周期变量中；数据库只保存加密值，所有
      读取响应与日志都不包含明文、ciphertext、nonce、密码或 session token。
- [ ] 项目 Event Stream 能按时间倒序读取持久 Observation，且每条记录具有 environment、
      source、level、message、occurredAt 等稳定字段，不发生跨项目读取。
- [ ] 事故创建、列表、详情和状态更新只通过 project-scoped 路由工作；传入其他项目的
      incident ID 返回 not found，旧全局路由不返回事故数据。
- [ ] 成员、配置和事故状态写操作生成不含秘密的项目 audit event，viewer 可以只读查看。
- [ ] API 仍满足统一错误、request ID、body limit、CORS、session cookie 和安全登录约束。
- [ ] 本地运行文档覆盖 migration、encryption key、管理员 bootstrap、项目与开发数据创建。
- [ ] `make check`、`make build`、`make generate-check` 和相关 integration 测试通过；后端
      项目边界完成前不开始前端 incident API 集成。
