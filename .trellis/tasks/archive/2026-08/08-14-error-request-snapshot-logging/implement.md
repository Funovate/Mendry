# 实施计划：错误请求参数快照日志

## 有序任务清单

1. **observability 常量**（`internal/platform/observability/logging.go`）
   - 新增 `EventHTTPFailed`、`FieldRequestBody`、`FieldRequestQuery`、
     `FieldBodyTruncated`、`FieldQueryTruncated`、`FieldBodyParseError`。
   - 在该包已有的 JSON record 测试文件中补一条覆盖新 event/field 的用例
     （沿用现有测试对 `EventHTTPCompleted` 等常量的验证方式）。

2. **脱敏与截断核心逻辑**（新增 `internal/platform/httpserver/snapshot.go`）
   - `sensitiveFieldPatterns` 黑名单常量。
   - `bodyCapture` 类型（透明旁路缓冲 reader，见 design.md）。
   - `redactJSON(any) any`：递归脱敏 map/slice。
   - `redactQuery(url.Values) map[string]string`（或等价结构）：按黑名单脱敏
     query 参数。
   - `buildBodySnapshot([]byte) (body string, truncated bool, parseError bool)`
     和 `buildQuerySnapshot(url.Values) (query string, truncated bool)`
     （或合并为一个返回结构体）：串联"解析 → 脱敏 → 序列化 → 截断"。
   - 新增 `internal/platform/httpserver/snapshot_test.go`：覆盖嵌套脱敏、数组内
     脱敏、非法 JSON、超长截断、query 脱敏（对应 design.md 测试策略）。

3. **接入 AccessLog**（`internal/platform/httpserver/server.go`）
   - `AccessLog` 签名改为 `AccessLog(logger *slog.Logger, maxBodyBytes int64, next http.Handler) http.Handler`。
   - 调用前包裹 `request.Body`（非空时）为 `bodyCapture`。
   - `next.ServeHTTP` 返回后，`recorder.status >= 400` 时构建快照并调用
     `observability.Log`，级别按 4xx/5xx 区分，事件 `EventHTTPFailed`。
   - 确保 `http.request.completed` 记录的现有字段/顺序/触发条件不受影响。

4. **更新调用点**（`internal/platform/httpserver/middleware.go`）
   - `Boundary()` 内 `AccessLog(options.Logger, handler)` 改为
     `AccessLog(options.Logger, options.MaxBodyBytes, handler)`。

5. **修复受影响的现有测试**
   - `internal/platform/httpserver/server_test.go:32`、`:72` 两处 `AccessLog(...)`
     调用补齐 `maxBodyBytes` 参数（可用任意正数，如 `1<<20`）。
   - 全量跑一遍 `internal/platform/httpserver` 包测试确认无回归。

6. **新增/扩展失败路径集成测试**
   - 4xx（如 `DecodeJSON` 校验失败、`WriteError` 主动 400）→ 断言
     `http.request.failed` 出现、级别 WARN、字段正确。
   - 5xx（含 `Recover` 捕获的 panic）→ 断言级别 ERROR。
   - 2xx/3xx → 断言不产生 `http.request.failed`。
   - 含 `password` 字段的请求体触发失败 → 断言日志中该字段值为
     `"[redacted]"`，且不出现明文密码。
   - 非法 JSON body 触发失败 → 断言 `body_parse_error: true` 且日志不含原始
     字节。

7. **更新规范文档**（`.trellis/spec/backend/logging-guidelines.md`）
   - 新增一节，明确这是"不记录 request/response body"规则的唯一、受限例外：
     触发条件（`status >= 400`）、覆盖范围（仅 body + query，不含
     header/cookie/response body）、脱敏规则（黑名单 + 占位符）、截断规则
     （4KB，脱敏后截断）、事件名 `http.request.failed`。

8. **验证命令**
   - `cd backend && go build ./...`
   - `cd backend && go vet ./...`
   - `cd backend && go test ./internal/platform/httpserver/... ./internal/platform/observability/...`
   - `cd backend && go test ./...`（全量回归）

## 2026-08-14 服务端原因日志修订

9. **扩展 observability 契约**（`internal/platform/observability/logging.go`）
   - 新增 `EventHTTPError` 以及 `error_type`、`error_message`、`error_causes`、
     `panic_type`、`panic_value`、`stack` 字段常量。
   - 扩展 JSON/console handler 测试，保证原始诊断字段不会被 console handler
     丢弃或改成含糊字段。

10. **共享内部错误写入与原因采集**（`internal/platform/httpserver`）
    - 新增 `WriteInternalError(writer, request, err)`，保持客户端通用 500 envelope。
    - 让 `statusRecorder` 通过 `ResponseWriter.Unwrap()` 接收本次请求的第一个未知
      error；后续重复提交不得覆盖或重复记录。
    - 请求完成后输出一次 `http.request.error`，带 method/route/status、顶层原始
      message/type 及有界展开的 cause 列表。
    - 扩展 `Recover`：记录原始 panic value/type 与 `debug.Stack()`，响应不变。

11. **接通所有 HTTP adapter 的未知 500**
    - auth、projects、incidents、observations 的 application error default 分支改为
      `WriteInternalError`。
    - 所有 `WriteJSON` / `WriteListJSON` 返回 error 后的通用 500 分支传入真实编码/
      写入 error；预期 4xx 保持 `WriteError`。

12. **原因日志回归测试**
    - 共享边界：原始顶层 error、wrapped cause、joined cause、单次记录、客户端不
      泄漏、同一 request/trace 关联。
    - panic：value/type/stack 完整，客户端仍为通用 500。
    - auth：`Authenticate` 未知错误产生原因事件，覆盖本次真实现场路径。
    - 现有 4xx mapping 测试确认不产生服务端原因事件。

13. **规范与全量验证**
    - 更新 `error-handling.md` / `logging-guidelines.md`，记录用户选择的原始错误与
      panic 诊断策略及客户端隔离边界。
    - 运行原有第 8 项全部命令，额外覆盖四个 HTTP adapter 包测试。

## 2026-08-14 普通错误来源栈修订

14. **共享来源栈能力**（新增 `internal/platform/errtrace`）
    - 用 `runtime.Callers` 有界捕获程序计数器，用 `runtime.CallersFrames` 格式化
      函数、文件和行号。
    - 有界遍历单 cause / joined cause graph，返回层级最深的已捕获栈。
    - 单元测试覆盖 skip 语义、格式内容和最深 cause 选择。

15. **在错误包装点捕获真实现场**
    - PostgreSQL、Redis `safeError` 全部改用捕获栈的 constructor。
    - auth/projects/incidents/observations PostgreSQL `repositoryError` 全部改用捕获栈
      的 constructor，保留现有 error type/text/Unwrap 行为。

16. **HTTP 原因日志输出栈**
    - `statusRecorder` 保存 `WriteInternalError` 的 HTTP 边界兜底栈。
    - 优先输出 error graph 中最深的已捕获栈并标记
      `error_stack_source=wrapped_error`；否则输出兜底栈并标记
      `error_stack_source=http_boundary`。
    - 新增 `error_stack`、`error_stack_source` 稳定字段常量，console/JSON 均保留。
    - 测试断言服务端日志含函数/文件/行号，客户端响应不含任何栈信息。

17. **重新执行质量门**
    - gofmt、定向包测试、`go vet ./...`、`go test ./...`、`go test -race ./...`、
      `go build ./cmd/...`、`make generate-check` 和 Trellis task validation。

## 2026-08-14 GoLand 可点击 console 栈修订

18. **console 原始多行栈 projection**
    - `consoleHandler` 从未分组 attributes 中提取 `error_stack` 与 panic `stack`，
      tint 仍渲染其余单行诊断字段。
    - 事件行写完后将栈原文直接写入 console writer，使每个
      `path/file.go:line` frame 成为可点击的独立物理行。
    - 所有派生 handler 共享 writer mutex，保证一条事件与其栈连续输出。
    - 更新 console/JSON 测试，并重新执行第 17 项质量门。

## 风险点 / 回滚

- 风险文件：`internal/platform/httpserver/server.go`、`middleware.go`
  （`AccessLog`/`Boundary` 是所有 HTTP 请求的公共路径，改动影响面广，但改动
  本身是纯增量追加，不修改现有分支逻辑）。
- 回滚方式：本任务为单次、无 schema/配置变更的纯代码增量，出现问题可直接
  `git revert` 对应提交，无需数据迁移或兼容层处理。

## 完成后检查

- 按 Trellis workflow 进入 Phase 3：更新 spec、提交前跑 2.2 质量检查、按
  仓库约定提交（不在本次任务中主动执行 `git push`）。
