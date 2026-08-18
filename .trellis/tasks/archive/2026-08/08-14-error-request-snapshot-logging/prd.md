# 错误请求参数与服务端原因日志

## Goal

当后端 API 请求处理失败时，运维/开发者需要能够还原触发失败的入参（body/query），
并在非预期服务端失败时看到可定位的错误原因，从而定位并复现问题。当前
`httpserver.AccessLog` 只记录 method/route/status/duration，
`.trellis/spec/backend/logging-guidelines.md` 明确禁止记录 request/response body、
query string、headers、cookies。本任务为该规则新增一个显式、范围受限的例外：
仅在请求失败（`status >= 400`）时，记录一份经过脱敏、截断的入参快照。

初版失败快照只解决了“什么请求触发了失败”，没有解决“服务端为什么失败”。
HTTP adapter 将未知应用错误映射为通用 `500 internal_error` 时丢弃了原始 `err`，
导致 GET 等无 body/query 的请求只产生两条几乎相同的访问记录，无法诊断。

SQL 慢查询/报错定位机制经确认已足够（`WithOperation()` 声明的稳定 operation
name + `duration_ms`/`error_class` + `trace_id` 贯穿 HTTP 请求与其触发的 DB
query span，无需原始 SQL），本任务不涉及该部分改动。

## Background（已确认事实）

- 中间件链由 `httpserver.Boundary()`（`internal/platform/httpserver/middleware.go:230`）
  按固定顺序组装：`WithRequestID` → `AccessLog` → `Recover` → `CORS` → `LimitBody` →
  `NormalizeMuxErrors` → 实际 handler。`AccessLog` 包裹 `Recover`，因此无论是
  handler 主动调用 `WriteError` 返回 4xx/5xx，还是 handler panic 被 `Recover`
  转换为 500，`AccessLog` 里的 `statusRecorder` 最终都能看到正确的最终
  status——这是判断"本次请求是否失败"的正确挂载点。
- 所有现有 API handler 的请求体都是 `application/json`，仓库内没有
  multipart/form-data 或文件上传 endpoint（`DecodeJSON` 强制要求
  `Content-Type: application/json`，否则直接 415）。请求体大小由
  `FIXTHE_HTTP_MAX_BODY_BYTES` 限制（默认 1048576 字节，配置范围 1KB–10MB，见
  `internal/platform/config/config.go:487`），通过 `LimitBody` 中间件用
  `http.MaxBytesReader` 包裹 `request.Body`。
- 目前唯一已知的敏感请求字段是 `auth` 模块的 `password`
  （`internal/modules/auth/adapter/http/handler.go:65-71`，`loginRequest`/
  `createUserRequest`）。该字段在 handler 内解码后立刻转为 `[]byte` 并
  `payload.Password = ""` + `defer clear(password)`，说明项目对凭据字段有严格的
  内存卫生要求；请求体原始字节在到达 handler 之前就会被中间件截获用于快照，因此
  快照机制必须在日志层面独立完成脱敏，不能依赖 handler 的清零时机。
- `observability` 包（`internal/platform/observability/logging.go`）是所有稳定
  event/field 常量的唯一来源；新增日志字段/事件必须在这里补充常量并配 JSON
  record 测试（现有约定）。
- 现有"不记录原始 URL/query string"规则和"cookies/headers/session token 永不
  进日志"规则是独立、更严格的规则，本次例外只针对 body 与 query，不涉及
  header/cookie（尤其 session cookie），两者继续保持现状禁止。
- 2026-08-14 的实际失败 `trace=78becb96` 中，认证 Redis `get` 成功后接口直接
  返回 500，没有 PostgreSQL query、panic recovery 或应用错误记录。该现场证明
  仅凭 `http.request.completed` + `http.request.failed` 无法还原未知应用错误。
- `projects` HTTP adapter 的 `writeApplicationError` 在 default 分支把任意未知
  `err` 转为通用 500，但没有 logger 参数，也没有记录原始错误；此处仍持有完整
  cause chain，是记录服务端错误的最后一个可靠边界。
- `.trellis/spec/backend/error-handling.md` 已规定 lower package 不得“既记录又返回”
  同一个错误，最终错误应由 transport/process boundary 记录。因此原因日志应归属
  HTTP adapter/shared HTTP error boundary，而不是散落在 service/repository。

## Requirements

- R1. 仅当响应 `status >= 400` 时，输出一条独立的快照日志记录，事件名
  `http.request.failed`；不改变现有 `http.request.completed` 完成记录的字段和
  行为。
- R2. 日志级别按状态码区分：`4xx` → `WARN`，`5xx` → `ERROR`。
- R3. 快照来源仅限请求体（`application/json`）与 query string；不采集
  header、cookie、响应体。
- R4. 请求体按黑名单脱敏：维护一份大小写不敏感的敏感字段名模式常量集
  （至少覆盖 `password`、`token`、`secret`、`authorization`、
  `apikey`/`api_key`、`credential`），结构化解析 JSON 后递归匹配 key，命中则
  替换为固定占位符（如 `"[redacted]"`），未命中字段原样记录。
- R5. 请求体不是合法 JSON 时，不记录原始内容，只记录 `parse_error: true`
  标记（因为无法结构化识别敏感字段，直接转储有泄露风险）。
- R6. query string 按同一黑名单对参数名做脱敏（命中参数值替换为占位符），其余
  参数原样记录。
- R7. body 与 query 各自独立截断，上限 4KB；超出部分丢弃并标记
  `truncated: true`。
- R8. 新增的 event/field 常量加入 `internal/platform/observability/logging.go`，
  并补充 JSON record 测试。
- R9. `.trellis/spec/backend/logging-guidelines.md` 新增一节，明确写出这是对
  "不记录 body/query"规则的唯一、受限例外，覆盖触发条件、脱敏规则、截断规则，
  避免以后被误读为规则失效或被扩大适用范围。
- R10. 每个由非预期应用错误映射成 `500 internal_error` 的请求，必须在仍持有
  原始 `err` 的 HTTP 边界输出一条可与 request/trace 关联的服务端原因记录；不得
  只留下 status=500。
- R11. 服务端原因记录与 `http.request.failed` 入参快照职责分离：前者解释
  “为什么失败”，后者保留“什么输入触发失败”。不得依赖 access middleware 从
  status code 反推出 cause。
- R12. 原因记录至少包含稳定 event、component、request ID、错误类型、顶层
  `err.Error()` 原文和逐层展开的原始 cause chain；客户端响应继续只返回通用
  `internal_error`，不暴露内部诊断。
- R13. 预期的 4xx 应用错误不得升级为服务端错误原因记录，避免把正常拒绝路径
  当成系统故障；其稳定错误码和失败入参仍由现有响应/快照覆盖。
- R14. panic recovery 必须记录原始 panic value 和 stack，同时继续保证 panic
  内容不会进入客户端响应。
- R15. 除错误对象自身的原始文本和 panic value/stack 外，原因日志不得主动附加
  request/response body、query、header、cookie、SQL、SQL bind values、Redis
  key/value、凭据或其他已禁止数据。用户已明确接受第三方/底层错误文本自身可能
  携带敏感细节的风险，以换取自用日志的完整诊断能力。
- R16. 普通 Go error 的原因记录必须补充可定位到函数、源文件和行号的
  `error_stack`；仅在 HTTP 日志调用点开启 `slog` source 不满足该要求，因为它
  只能定位日志边界，不能定位错误发生/包装位置。
- R17. PostgreSQL、Redis 和 feature repository 等已知错误包装边界必须在包装
  error 当时捕获调用栈。HTTP 边界优先输出 cause chain 中最深的已捕获栈；若整个
  error graph 尚无栈，则在 `WriteInternalError` 上报时捕获兜底栈，并用
  `error_stack_source=http_boundary` 明确它不是底层来源栈。
- R18. `error_stack`、`error_stack_source` 与现有 error/cause 字段一样仅进入私有
  服务端日志，客户端响应不得包含函数名、文件路径、行号或任何栈文本。
- R19. console 格式必须把普通错误 `error_stack` 和 panic `stack` 输出为真实的
  多行 Go stack trace，不得保留为带引号且将换行转义成 `\\n` 的单行属性。每个
  source frame 必须独占一行并保留 `path/file.go:line` 形态，使 GoLand console
  能识别并点击跳转；JSON 格式继续保留原有结构化字符串字段。

## Acceptance Criteria

- [x] 2xx/3xx 响应：不产生 `http.request.failed` 记录，`http.request.completed`
      记录字段不变。
- [x] 4xx 响应（含 handler 主动 `WriteError` 和 `DecodeJSON` 校验失败）：产生一条
      `http.request.failed`，级别 `WARN`，包含脱敏后的 body/query。
- [x] 5xx 响应（含 handler 主动返回和 panic 经 `Recover` 转换）：产生一条
      `http.request.failed`，级别 `ERROR`。
- [x] 请求体含 `password` 等黑名单字段并触发失败：日志中对应字段值为占位符，
      不出现明文。
- [x] 请求体不是合法 JSON 且触发失败（如 415/400 invalid JSON）：日志不包含原始
      body，包含 `parse_error: true`。
- [x] body 或 query 超过 4KB：日志中对应字段被截断且标记 `truncated: true`，
      不会把完整超限内容写入日志。
- [x] query string 中的黑名单参数在快照里被替换为占位符。
- [x] header、cookie（尤其 session cookie）在任何快照记录中都不出现。
- [x] `logging-guidelines.md` 包含新增章节，描述本例外的触发条件、脱敏规则、
      截断规则和适用边界。
- [x] 新增 event/field 常量有 JSON record 测试覆盖。
- [x] 未知应用错误映射成 500 时，日志包含一条独立、可按同一 request/trace
      检索的服务端原因记录，并能区分顶层操作和底层 cause；客户端仍收到通用
      `internal_error`。
- [x] 同一请求的原因记录和失败快照各自只出现一次，不因 helper/middleware
      重叠而重复记录同一错误。
- [x] 已知 4xx 分类不产生服务端原因记录；未知 5xx 产生 ERROR 级别原因记录。
- [x] 原因记录测试确认客户端响应不出现原始错误；日志除原始 error/cause 字段外
      不主动附加请求 secret、数据库参数、session token 或其他禁止字段。
- [x] panic 日志包含原始 value 和 stack，客户端响应不含二者。
- [x] stack-aware 普通错误产生的 `http.request.error` 包含函数、源文件和行号，
      `error_stack_source=wrapped_error`；优先选择 cause chain 中最深的已捕获栈。
- [x] 尚未接入 stack-aware wrapper 的普通错误仍包含 handler 上报位置的兜底栈，
      并明确标记 `error_stack_source=http_boundary`，不得冒充底层来源。
- [x] 客户端 500 响应不包含 `error_stack`、函数名、文件路径、行号或 source 标记。
- [x] console 错误与 panic 栈包含真实换行，source frame 为独立的
      `path/file.go:line` 行，不出现字面量 `\\n`，可被 GoLand console 识别。
- [x] 并发 console 日志中，一条事件及其紧随的栈作为同一个输出临界区，不会被
      另一条 slog 事件插入；JSON 日志字段和客户端响应保持不变。

## Out of Scope

- SQL 日志 / 慢查询定位机制的任何改动（已确认现状足够）。
- 响应体快照（本次只覆盖入参，不覆盖出参/response body）。
- Header、Cookie（尤其 session cookie/token）的记录——继续保持现状禁止。
- 成功请求（2xx/3xx）的行为变化。
- 白名单式脱敏（本次采用黑名单，规模变化后可再评估升级）。
