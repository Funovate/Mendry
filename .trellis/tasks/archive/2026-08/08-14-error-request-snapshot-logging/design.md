# 技术设计：错误请求参数快照日志

> 2026-08-14 修订：初版只保存失败入参，实际 500 现场证明应用错误原因会在
> HTTP 映射时丢失。本设计增加独立的服务端原因事件；原有快照设计继续保留。

## 架构总览

在 `httpserver.AccessLog`（`internal/platform/httpserver/server.go:133`）内部新增一段
"失败快照"逻辑，与现有"完成记录"逻辑共享同一个 `statusRecorder`，但完全独立：

1. `AccessLog` 在调用 `next.ServeHTTP` 之前，用一个透明的 `bodyCapture` reader 包裹
   `request.Body`（如果非空）。`bodyCapture` 只做旁路缓冲，不改变字节内容、不影响
   下游任何读取行为。
2. `next.ServeHTTP` 正常执行（`Recover` → `CORS` → `LimitBody` → `NormalizeMuxErrors`
   → handler），`LimitBody` 会在 `bodyCapture` 外层再包一层
   `http.MaxBytesReader`，两者互不冲突：`bodyCapture` 只旁观流经的字节，不参与
   限流判断。
3. `next.ServeHTTP` 返回后，`AccessLog` 照常写出 `http.request.completed`
   记录（字段/行为完全不变，满足 R1）。
4. 若 `recorder.status >= 400`，额外构建并写出一条 `http.request.failed` 记录：
   - 从 `bodyCapture` 取出已缓冲的原始字节（若 handler 从未读取 body，则为空）。
   - 从 `request.URL.Query()` 取出 query 参数（同步可得，无需 capture）。
   - 对两者分别做"结构化解析 → 脱敏 → 序列化 → 截断"处理。
   - 级别按状态码：`4xx` → `WARN`，`5xx` → `ERROR`（R2）。

选择在 `AccessLog` 内实现而不是新增一个独立 middleware，是因为：
- 失败判定必须在 `Recover` 之外、看到最终 status 之后才能做（`AccessLog` 已经是
  这个位置，见 PRD Background）。
- 避免再包一层 `http.Handler`，减少一次 `ResponseWriter`/`Request` 传递和潜在的
  `Unwrap()` 遗漏风险。

## 中间件签名变更

`AccessLog` 需要知道快照缓冲的上限，复用现有的 `MaxBodyBytes`（已经是系统对请求体的
硬性上限，见 `internal/platform/config/config.go:487`），避免引入新的配置项：

```go
func AccessLog(logger *slog.Logger, maxBodyBytes int64, next http.Handler) http.Handler
```

`Boundary()`（`internal/platform/httpserver/middleware.go:246`）调用处相应改为
`AccessLog(options.Logger, options.MaxBodyBytes, handler)`。

受影响的现有调用点（均为测试，均使用 2xx/3xx 场景，只需补一个参数即可通过）：
- `internal/platform/httpserver/server_test.go:32`
- `internal/platform/httpserver/server_test.go:72`

## bodyCapture：透明旁路缓冲

新文件 `internal/platform/httpserver/snapshot.go`：

```go
type bodyCapture struct {
    source io.ReadCloser
    buffer bytes.Buffer
    limit  int64
}

func (c *bodyCapture) Read(p []byte) (int, error) {
    n, err := c.source.Read(p)
    if n > 0 && int64(c.buffer.Len()) < c.limit {
        remaining := c.limit - int64(c.buffer.Len())
        if int64(n) < remaining {
            c.buffer.Write(p[:n])
        } else {
            c.buffer.Write(p[:remaining])
        }
    }
    return n, err
}

func (c *bodyCapture) Close() error { return c.source.Close() }
```

- `limit` 传入 `maxBodyBytes`（≤10MB，配置已保证有界），因此这里的截断分支只是
  防御性代码，正常情况下永远不会触发（`http.MaxBytesReader` 会先于此处的
  handler 逻辑报错）。
- `request.Body == nil || request.Body == http.NoBody` 时不包裹，快照的 body
  字段直接省略。
- 若 handler 从未读取 body（例如在读取前就返回错误），`buffer` 为空，快照里
  body 为空字符串，不视为异常。
- 单 goroutine 同步访问，无需加锁：`Read` 由处理请求的 goroutine 调用，
  `AccessLog` 在 `next.ServeHTTP` 返回后（同一 goroutine）读取 `buffer`。

## 脱敏与截断算法

### 敏感字段黑名单

`snapshot.go` 内维护一个包级常量：

```go
var sensitiveFieldPatterns = []string{
    "password", "token", "secret", "authorization", "apikey", "api_key", "credential",
}
```

匹配规则：大小写不敏感的**包含匹配**（`strings.Contains(strings.ToLower(key), pattern)`）。
例如 `Password`、`user_password`、`PASSWORD_HASH`、`authToken`、`refresh_token`
都会命中。这是黑名单策略下有意选择的宽松匹配（宁可多脱敏，不可漏脱敏）。

### Body 处理流程

1. 若 `bodyCapture` 缓冲为空 → 快照不含 body 字段。
2. 尝试 `json.Unmarshal(buffered, &v)`（`v` 为 `any`，可处理 object/array/标量）。
   - 失败 → 不记录任何原始内容，只设置 `body_parse_error: true`
     （R5；避免把无法结构化识别的内容直接转储导致泄露）。
   - 成功 → 对解析结果做**递归脱敏**：
     - `map[string]any`：对每个 key 检查黑名单，命中则该 key 的 value 整体替换
       为字符串 `"[redacted]"`；未命中则递归处理 value。
     - `[]any`：对每个元素递归处理（数组本身没有 key，不做替换判断）。
     - 其他类型（字符串/数字/布尔/nil）：原样保留。
   - 用 `json.Marshal` 把脱敏后的结构重新序列化为字符串。
3. 若序列化结果长度 > 4096 字节，在合法 UTF-8 边界处截断到 4096 字节并设置
   `body_truncated: true`；否则 `body_truncated: false`。

关键设计取舍：**脱敏发生在完整解析后的结构上，截断只作用于脱敏后的最终字符串**。
这样即使原始 body 远大于 4KB，敏感字段也一定会被替换掉，不会出现"因为截断点
恰好落在明文密码中间，导致部分明文残留"的情况。截断上限复用请求体已有的
`http.MaxBytesReader` 上限作为解析前的安全边界（一次性交给 `json.Unmarshal`
的字节数不会超过 `maxBodyBytes`，已经是现有系统的硬上限）。

### Query 处理流程

1. `request.URL.Query()` 得到 `url.Values`（`map[string][]string`）。
2. 为保证输出确定性，对 key 排序后遍历；命中黑名单的 key，其所有 value 替换为
   `"[redacted]"`；未命中原样保留。
3. 用 `json.Marshal` 序列化为字符串。
4. 同 body：若超过 4096 字节，截断并设置 `query_truncated: true`。
5. 无 query 参数时快照不含 query 字段。

## 新增 observability 常量

`internal/platform/observability/logging.go`：

```go
const (
    EventHTTPFailed = "http.request.failed"
)

const (
    FieldRequestBody    = "request_body"
    FieldRequestQuery   = "request_query"
    FieldBodyTruncated  = "body_truncated"
    FieldQueryTruncated = "query_truncated"
    FieldBodyParseError = "body_parse_error"
)
```

`http.request.failed` 记录字段：`component`、`request_id`、`method`、`route`、
`status`，以及上述新增字段（body/query 相关字段按"存在才写入"处理，避免
和 `http.request.completed` 产生除新增字段外的字段差异）。`trace_id`/`span_id`
通过 `observability.Log()` 自动注入，与 `http.request.completed` 保持同一
关联方式。

## 服务端错误原因记录

### 边界与事件职责

新增独立事件 `http.request.error`：

- `http.request.completed`：所有请求的完成状态和耗时。
- `http.request.failed`：4xx/5xx 的脱敏入参快照。
- `http.request.error`：仅未知应用错误映射成 500 时的原始错误原因。
- `http.request.panic_recovered`：panic value 与 stack。

`AccessLog` 不能从 status code 还原 Go error，因此业务 HTTP adapter 在仍持有
`err` 的 default 分支调用共享 helper：

```go
httpserver.WriteInternalError(writer, request, err)
```

helper 不直接写日志，也不要求每个 Handler 注入 logger。它先沿
`ResponseWriter.Unwrap()` 链找到 `AccessLog` 的 `statusRecorder` 并记录 error，
再向客户端写现有通用 `500 internal_error` envelope。请求处理返回后，
`AccessLog` 使用已经确定的 route pattern/status 统一输出一次原因事件。由公共
边界拥有最终日志，符合 lower package 不“既记录又返回”的现有约定。

若 helper 在没有 `AccessLog` 的单元测试/独立 handler 中使用，找不到 recorder
时仍正常写 500，只是不产生边界日志。

### 原始错误契约

`http.request.error` 为 ERROR 级别，包含：

- `component=httpserver`、request/trace 关联字段、method、route、status。
- `error_type`：顶层具体 Go 类型（`%T`）。
- `error_message`：顶层 `err.Error()` 原文。
- `error_causes`：依次展开 `Unwrap() error` / `Unwrap() []error` 后得到的原始
  `{type,message}` 列表。遍历设置固定数量上限，防止病态 error graph 造成无限
  循环或无界日志；顶层 message 仍保留完整 `errors.Join` 文本。

用户已明确选择完整自用诊断优先：不清洗上述 error 文本。客户端响应仍只包含
通用 `internal_error`，原文只进入服务端日志。日志实现不得额外附加请求 payload、
header/cookie、SQL/bind values、Redis key/value；但底层 error 文本本身可能包含
这些内容的剩余风险已被接受。

预期 4xx 分类继续调用 `WriteError`，不携带 Go error，也不产生
`http.request.error`。

### Panic 诊断

`Recover` 保存 `recover()` 的返回值并记录：

- `panic_type`：`%T`。
- `panic_value`：`fmt.Sprint(value)` 原文。
- `stack`：`runtime/debug.Stack()` 原文。

panic value/stack 永不进入 HTTP 响应。panic 仍使用已有
`http.request.panic_recovered` 事件，避免把 panic 同时登记为普通应用 error。

### 普通错误来源栈

Go 标准库 error 只保存文本和 `Unwrap` 关系，不保存程序计数器。HTTP 边界在
handler 已经返回后调用 `debug.Stack()`，只能看到 handler/日志边界，无法恢复已经
退栈的 repository、driver 或连接池调用。因此来源栈必须在错误第一次被具有业务
语义地包装时捕获。

新增无依赖的 `internal/platform/errtrace` 包：

- `Capture(skip)` 使用 `runtime.Callers` 保存有界程序计数器。
- `Trace.String()` 使用 `runtime.CallersFrames` 延迟格式化函数名、源文件和行号。
- `FromError(err)` 有界遍历 `Unwrap() error` / `Unwrap() []error` graph，选择层级
  最深的 stack-aware error，保证 PostgreSQL `safeError` 的连接获取现场优先于外层
  repository wrapper。

PostgreSQL/Redis `safeError` 和 auth/projects/incidents/observations 的
`repositoryError` 改用本包捕获栈，并通过 `StackTrace()` 暴露。它们仍保留现有
Error/Unwrap 语义和具体错误类型。

`statusRecorder` 在接收 `WriteInternalError` 时同时捕获一份 HTTP 上报栈。
`http.request.error` 输出：

- cause graph 中存在已捕获栈：`error_stack_source=wrapped_error`，输出最深栈。
- 不存在：`error_stack_source=http_boundary`，输出 handler 调用
  `WriteInternalError` 的位置。该栈只承诺定位上报边界，不冒充底层失败来源。

两个分支均使用 `error_stack` 字段；它与 error 原文一样只写私有服务端日志，通用
500 envelope 不变。`slog.HandlerOptions.AddSource` 不启用，因为它只会重复指向
`observability.Log`，不能满足来源定位要求。

### GoLand 可点击的 console 栈

JSON handler 继续把 `error_stack` / panic `stack` 保存为结构化字符串；JSON 中的
换行按格式规范转义是正确行为。console handler 不能把这两个字段交给 tint 当普通
attribute 渲染，否则 tint 会输出 `error_stack="...\\n..."`，GoLand 无法把其中
的 source frame 识别为可点击路径。

`consoleHandler.Handle` 在未分组的 record attributes 中提取两个 stack 字段：

1. 其余属性仍由 tint 输出为现有单行事件，`error_stack_source` 保留在该行。
2. stack string 随后不加引号、不转义地直接写入同一个 writer，保持标准的
   `function\n\t/absolute/path/file.go:line` 物理行格式。
3. `consoleHandler` 及其 `WithAttrs` / `WithGroup` 派生实例共享 writer 和 mutex；
   每条事件行及其栈在同一个临界区写完，防止并发请求把别的事件插进栈中间。

该变化只属于人类可读 console projection，不修改稳定 JSON record，也不把栈加入
HTTP response。

## 兼容性与回滚

- 纯增量变更：`http.request.completed` 的字段/触发条件不变（R1），现有日志
  消费方（告警规则、仪表盘）不受影响。
- 新事件 `http.request.failed` 只在 `status >= 400` 时出现，旧版本日志管道
  只需忽略未知 event 即可安全共存。
- 新事件 `http.request.error` 只在 adapter 明确提交未知 500 cause 时出现；旧日志
  消费方可忽略。`http.request.panic_recovered` 增加诊断字段但事件名不变。
- 无数据库/配置 schema 变更，无需 feature flag：改动可通过单次 revert 完整回滚。
- 性能影响：`bodyCapture` 只在内存中做一次 `bytes.Buffer.Write`，量级与请求体
  相同（≤10MB 配置上限），且只在失败路径上做 JSON 解析/脱敏/序列化，成功路径
  （占绝大多数请求）不受影响。

## 测试策略

- `snapshot_test.go`（新增）：
  - 嵌套 JSON 中的 `password`/`token` 等字段被替换为 `[redacted]`，兄弟字段保留。
  - 数组内对象的敏感字段同样被脱敏。
  - 非法 JSON body → `body_parse_error: true`，日志中不出现原始字节。
  - body/query 超过 4KB → 对应字段被截断、`*_truncated: true`。
  - query 中黑名单参数被替换，其余参数保留。
- `server_test.go` / `middleware_test.go`（扩展现有用例）：
  - 4xx 响应（`WriteError` 返回 400）产生 `http.request.failed`，级别 `WARN`。
  - 5xx 响应（含 `Recover` 转换的 panic）产生 `http.request.failed`，级别 `ERROR`。
  - 2xx/3xx 响应不产生 `http.request.failed`。
  - 现有两个 `AccessLog` 调用点补齐新增的 `maxBodyBytes` 参数后仍然通过。
- 手动/集成层面：针对 `auth` 模块登录失败场景（`invalid_credentials`），验证
  日志中 `password` 字段确实被替换而不是明文出现。
- `server_test.go`：`WriteInternalError` 产生且仅产生一条
  `http.request.error`，包含顶层 message/type、展开后的 cause，并与 completion、
  failure snapshot 使用同一 request/trace；stack-aware cause 输出来源栈，普通
  cause 输出有明确 source 的 HTTP 边界兜底栈；客户端不含原文或栈。
- `errtrace` / PostgreSQL / repository 测试：捕获栈包含构造错误的测试函数及
  文件/行号，cause graph 选择最深的已捕获栈。
- `middleware_test.go`：panic 事件包含原始 value 和 stack，客户端仍为通用 500。
- `logging_test.go`：console 的普通错误与 panic source frame 使用真实换行，断言
  不存在字面量 `\\n`；并发写入测试断言事件和栈块不交错。JSON 测试继续断言
  `error_stack` / `stack` 字段原值不变。
- 各 HTTP adapter 的未知错误与 JSON 编码失败分支改用 `WriteInternalError`；至少
  用认证中间件回归测试覆盖本次现场对应的“Redis 成功后 Authenticate 返回未知
  error”路径。
