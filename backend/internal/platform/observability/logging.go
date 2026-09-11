// Package observability 统一构造结构化日志并维护稳定的 event 与 field 契约。
package observability

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"

	"mendry/backend/internal/platform/buildinfo"

	"github.com/lmittmann/tint"
	"github.com/mattn/go-isatty"
	"go.opentelemetry.io/otel/trace"
)

// 稳定日志 field 供所有边界共享，避免同一语义出现多个 key。
const (
	FieldEvent                 = "event"
	FieldService               = "service"
	FieldVersion               = "version"
	FieldCommit                = "commit"
	FieldEnvironment           = "environment"
	FieldComponent             = "component"
	FieldOperation             = "operation"
	FieldOutcome               = "outcome"
	FieldProvider              = "provider"
	FieldStatusCode            = "status_code"
	FieldBytesRetrieved        = "bytes_retrieved"
	FieldDurationMS            = "duration_ms"
	FieldTraceID               = "trace_id"
	FieldSpanID                = "span_id"
	FieldRequestID             = "request_id"
	FieldDBOperation           = "db.operation.name"
	FieldDBQueryText           = "db.query.text"
	FieldRowsAffected          = "rows_affected"
	FieldTransactionID         = "transaction_id"
	FieldErrorClass            = "error_class"
	FieldCleanupErrorClass     = "cleanup_error_class"
	FieldIsolation             = "isolation"
	FieldReadOnly              = "read_only"
	FieldAttempt               = "attempt"
	FieldHealth                = "health"
	FieldPoolAcquired          = "pool_acquired"
	FieldPoolIdle              = "pool_idle"
	FieldPoolTotal             = "pool_total"
	FieldRedisCommand          = "redis.command.name"
	FieldCommandCount          = "command_count"
	FieldLLMOperation          = "llm.operation.name"
	FieldLLMModel              = "llm.model"
	FieldGitOperation          = "git.operation.name"
	FieldHTTPHost              = "http.host"
	FieldHTTPPath              = "http.path"
	FieldHTTPMethod            = "http.method"
	FieldHTTPURL               = "http.url"
	FieldHTTPFinalURL          = "http.final_url"
	FieldHTTPStatus            = "http.status"
	FieldHTTPRequest           = "http.request"
	FieldHTTPRequestTruncated  = "http.request_truncated"
	FieldHTTPRequestHeaders    = "http.request_headers"
	FieldHTTPRequestQuery      = "http.request_query"
	FieldHTTPResponse          = "http.response"
	FieldHTTPResponseTruncated = "http.response_truncated"
	FieldHTTPResponseHeaders   = "http.response_headers"
	FieldRequestBody           = "request_body"
	FieldRequestQuery          = "request_query"
	FieldBodyTruncated         = "body_truncated"
	FieldQueryTruncated        = "query_truncated"
	FieldBodyParseError        = "body_parse_error"
	FieldErrorType             = "error_type"
	FieldErrorCode             = "error_code"
	FieldErrorStage            = "error_stage"
	FieldErrorMessage          = "error_message"
	FieldErrorMessageTruncated = "error_message_truncated"
	FieldErrorCauses           = "error_causes"
	FieldErrorStack            = "error_stack"
	FieldErrorStackSource      = "error_stack_source"
	FieldPanicType             = "panic_type"
	FieldPanicValue            = "panic_value"
	FieldStack                 = "stack"
	FieldRunID                 = "run_id"
	FieldSeriesID              = "series_id"
	FieldIncidentID            = "incident_id"
	FieldTopicID               = "topic_id"
	FieldLifecycleGeneration   = "lifecycle_generation"
	FieldContextVersion        = "context_version"
	FieldPhase                 = "phase"
	FieldFromState             = "from_state"
	FieldToState               = "to_state"
	FieldSequence              = "sequence"
	FieldPayloadKind           = "payload_kind"
	FieldPayload               = "payload"
	FieldPayloadBytes          = "payload_bytes"
	FieldPayloadLoggedBytes    = "payload_logged_bytes"
	FieldPayloadTruncated      = "payload_truncated"
	FieldPayloadSHA256         = "payload_sha256"
	FieldToolName              = "tool_name"
	FieldToolRejectionCode     = "tool_rejection_code"
	FieldModelTokensIn         = "model_tokens_in"
	FieldModelTokensOut        = "model_tokens_out"
	FieldModelTokens           = "model_tokens"
	FieldModelCalls            = "model_calls"
	FieldModelFinishReason     = "finish_reason"
	FieldToolCalls             = "tool_calls"
	FieldToolCount             = "tool_count"
	FieldToolSchemaBytes       = "tool_schema_bytes"
	FieldRequestBytes          = "request_bytes"
	FieldModelCacheHitTokens   = "model_cache_hit_tokens"
	FieldModelCacheMissTokens  = "model_cache_miss_tokens"
	FieldRepositoryBytes       = "repository_bytes"
	FieldEvidenceBytes         = "evidence_bytes"
	FieldBudgetExhaustedReason = "budget_exhausted_reason"
	FieldTerminalState         = "terminal_state"
	FieldCredentialSecretID    = "credential_secret_id"
	FieldCredentialKind        = "credential_kind"
	FieldTransport             = "transport"
	FieldRetryable             = "retryable"
	FieldGateOutcome           = "gate_outcome"
	FieldGateReason            = "gate_reason"
	FieldAvailable             = "available"
	FieldSSHCommand            = "ssh.command"
)

// 稳定 event name 是日志检索、指标和告警使用的机器契约。
const (
	EventProcessStarting             = "process.starting"
	EventProcessStarted              = "process.started"
	EventProcessStopping             = "process.stopping"
	EventProcessStopped              = "process.stopped"
	EventHTTPServerReady             = "http.server.ready"
	EventHTTPCompleted               = "http.request.completed"
	EventHTTPFailed                  = "http.request.failed"
	EventHTTPError                   = "http.request.error"
	EventHTTPPanicRecovered          = "http.request.panic_recovered"
	EventDBQueryCompleted            = "db.query.completed"
	EventDBTransactionCompleted      = "db.transaction.completed"
	EventDBPoolState                 = "db.pool.state"
	EventMigrationApplied            = "db.migration.applied"
	EventMigrationsCompleted         = "db.migrations.completed"
	EventMigrationLockCleanupFailed  = "db.migration.lock_cleanup_failed"
	EventRedisCommandCompleted       = "redis.command.completed"
	EventRedisPoolState              = "redis.pool.state"
	EventLLMRequestCompleted         = "llm.request.completed"
	EventGitRequestCompleted         = "git.request.completed"
	EventRemediationFailed           = "remediation.failed"
	EventRemediationRunStarted       = "remediation.run.started"
	EventRemediationContinuationGate = "remediation.continuation.gate"
	EventRemediationStateTransition  = "remediation.state.transitioned"
	EventRemediationContextComplete  = "remediation.context.completed"
	EventRemediationModelComplete    = "remediation.model_turn.completed"
	EventRemediationToolComplete     = "remediation.tool.completed"
	EventRemediationRunCompleted     = "remediation.run.completed"
	EventRemediationContextPayload   = "remediation.context.payload"
	EventRemediationModelPayload     = "remediation.model_turn.payload"
	EventRemediationToolPayload      = "remediation.tool.payload"
	EventSSHEvidenceCompleted        = "ssh.evidence.completed"
	EventTencentCLSRequestCompleted  = "tencent_cls.request.completed"
	EventTencentCLSDetailCompleted   = "tencent_cls.detail.completed"
	EventTencentCLSEvidenceProjected = "tencent_cls.evidence.projected"
)

// LoggerOptions 声明 logger 的输出格式、级别和进程身份。
type LoggerOptions struct {
	Writer      io.Writer
	FileWriter  io.Writer
	Level       string
	Format      string
	Service     string
	Environment string
	Build       buildinfo.Info
}

// NewLogger 创建带稳定进程身份字段的 slog console 或 JSON logger。
func NewLogger(options LoggerOptions) (*slog.Logger, error) {
	if options.Writer == nil {
		return nil, fmt.Errorf("log writer is required")
	}
	if options.Service == "" {
		return nil, fmt.Errorf("log service is required")
	}
	if options.Environment == "" {
		return nil, fmt.Errorf("log environment is required")
	}

	level, err := parseLevel(options.Level)
	if err != nil {
		return nil, err
	}

	handler, err := newLogHandler(options.Writer, options.Format, level, true)
	if err != nil {
		return nil, err
	}
	if options.FileWriter != nil {
		fileHandler, err := newLogHandler(options.FileWriter, options.Format, level, false)
		if err != nil {
			return nil, err
		}
		handler = fanoutHandler{handlers: []slog.Handler{handler, fileHandler}, outputMu: &sync.Mutex{}}
	}
	return slog.New(handler).With(
		FieldService, options.Service,
		FieldVersion, options.Build.Version,
		FieldCommit, options.Build.Commit,
		FieldEnvironment, options.Environment,
	), nil
}

func newLogHandler(writer io.Writer, format string, level slog.Level, allowColor bool) (slog.Handler, error) {
	switch format {
	case "console":
		return newConsoleHandler(writer, level, allowColor && writerSupportsColor(writer)), nil
	case "json":
		return slog.NewJSONHandler(writer, &slog.HandlerOptions{
			Level: level,
			ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
				// 对齐项目日志 envelope，避免依赖 slog 默认的 time key。
				if attr.Key == slog.TimeKey {
					attr.Key = "timestamp"
				}
				return attr
			},
		}), nil
	default:
		return nil, fmt.Errorf("unsupported log format")
	}
}

// fanoutHandler 把同一条完整 slog record 投递到独立 destination，同时保留
// 各 handler 自己的格式化、颜色和多行原子写入语义。
type fanoutHandler struct {
	handlers []slog.Handler
	outputMu *sync.Mutex
}

func (h fanoutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (h fanoutHandler) Handle(ctx context.Context, record slog.Record) error {
	// 同一把锁覆盖全部 destination，确保并发 record 在 stdout 与文件中的
	// 顺序一致；派生 handler 也必须共享这把锁。
	h.outputMu.Lock()
	defer h.outputMu.Unlock()

	var result error
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, record.Level) {
			result = errors.Join(result, handler.Handle(ctx, record.Clone()))
		}
	}
	return result
}

func (h fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handlers := make([]slog.Handler, 0, len(h.handlers))
	for _, handler := range h.handlers {
		handlers = append(handlers, handler.WithAttrs(attrs))
	}
	return fanoutHandler{handlers: handlers, outputMu: h.outputMu}
}

func (h fanoutHandler) WithGroup(name string) slog.Handler {
	handlers := make([]slog.Handler, 0, len(h.handlers))
	for _, handler := range h.handlers {
		handlers = append(handlers, handler.WithGroup(name))
	}
	return fanoutHandler{handlers: handlers, outputMu: h.outputMu}
}

func newConsoleHandler(writer io.Writer, level slog.Level, color bool) slog.Handler {
	handler := tint.NewTextHandler(writer, &tint.Options{
		Level: level,
		// 本地 console 需要年月日，便于跨天对照日志。
		TimeFormat: "2006-01-02 15:04:05",
		NoColor:    !color,
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			// console 保留短关联线索和诊断字段，完整机器契约仍由 JSON 保留。
			switch attr.Key {
			case FieldEvent, FieldService, FieldVersion, FieldCommit, FieldEnvironment, FieldSpanID, FieldComponent:
				return slog.Attr{}
			case FieldTraceID:
				return compactConsoleID(attr, "trace")
			case FieldRequestID:
				return compactConsoleID(attr, "req")
			case FieldTransactionID:
				return compactConsoleID(attr, "tx")
			case FieldDurationMS:
				if attr.Value.Kind() == slog.KindInt64 {
					return slog.String("took", fmt.Sprintf("%dms", attr.Value.Int64()))
				}
			case FieldDBOperation, FieldLLMOperation, FieldGitOperation:
				attr.Key = "operation"
			case FieldDBQueryText:
				return slog.Attr{}
			case FieldRedisCommand:
				attr.Key = "command"
			case FieldLLMModel:
				attr.Key = "model"
			case FieldHTTPHost:
				attr.Key = "host"
			case FieldHTTPPath:
				attr.Key = "path"
			case FieldHTTPStatus:
				attr.Key = "status"
			case FieldHTTPRequest:
				attr.Key = "request"
			case FieldHTTPRequestTruncated:
				attr.Key = "request_truncated"
			case FieldHTTPResponse:
				attr.Key = "response"
			case FieldHTTPResponseTruncated:
				attr.Key = "response_truncated"
			case FieldCommandCount:
				attr.Key = "count"
			case FieldRowsAffected:
				attr.Key = "rows"
			case FieldPoolAcquired:
				attr.Key = "acquired"
			case FieldPoolIdle:
				attr.Key = "idle"
			case FieldPoolTotal:
				attr.Key = "total"
			case FieldErrorClass:
				attr.Key = "error"
			case FieldCleanupErrorClass:
				attr.Key = "cleanup_error"
			default:
			}
			return attr
		},
	})
	return consoleHandler{next: handler, writer: writer, outputMu: &sync.Mutex{}}
}

// consoleHandler 将稳定 component field 提升为可扫描的消息前缀，同时保持
// slog.With 与 slog.WithGroup 的 Handler 派生语义。所有派生实例共享 outputMu，
// 确保事件行及其原始多行 stack 不会被并发日志穿插。
type consoleHandler struct {
	next      slog.Handler
	writer    io.Writer
	outputMu  *sync.Mutex
	component string
	grouped   bool
	stacks    []string
}

func (h consoleHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h consoleHandler) Handle(ctx context.Context, record slog.Record) error {
	component := h.component
	attrs := make([]slog.Attr, 0, record.NumAttrs())
	stacks := append([]string(nil), h.stacks...)
	// 先读 event，再决定是否把 http.request / http.response 投影成后续物理行；
	// slog 不保证 attr 顺序，出站 llm/git 记录复用同一对 body key。
	event := recordEvent(record)
	payloadKind := recordStringAttr(record, FieldPayloadKind)
	debugValues := make(map[string]string)
	record.Attrs(func(attr slog.Attr) bool {
		if !h.grouped && attr.Key == FieldComponent && attr.Value.Kind() == slog.KindString {
			component = attr.Value.String()
			return true
		}
		if !h.grouped && consoleStackField(attr.Key) && attr.Value.Kind() == slog.KindString {
			if stack := attr.Value.String(); stack != "" {
				stacks = append(stacks, stack)
			}
			return true
		}
		if !h.grouped && inboundHTTPDebugField(event, attr.Key) && attr.Value.Kind() == slog.KindString {
			debugValues[attr.Key] = attr.Value.String()
			return true
		}
		if !h.grouped && remediationPayloadField(event, attr.Key) && attr.Value.Kind() == slog.KindString {
			debugValues[attr.Key] = attr.Value.String()
			return true
		}
		attrs = append(attrs, attr)
		return true
	})
	for _, key := range inboundHTTPDebugFieldOrder {
		if block := formatInboundHTTPDebugBlock(key, debugValues[key]); block != "" {
			stacks = append(stacks, block)
		}
	}
	if payload := debugValues[FieldPayload]; payload != "" {
		stacks = append(stacks, formatPayloadBlock(event, payloadKind, payload))
	}

	if component != "" {
		record.Message = fmt.Sprintf("[%s] %s", component, record.Message)
	}
	compact := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	compact.AddAttrs(attrs...)

	h.outputMu.Lock()
	defer h.outputMu.Unlock()
	if err := h.next.Handle(ctx, compact); err != nil {
		return err
	}
	for _, stack := range stacks {
		if _, err := io.WriteString(h.writer, stack); err != nil {
			return err
		}
		if !strings.HasSuffix(stack, "\n") {
			if _, err := io.WriteString(h.writer, "\n"); err != nil {
				return err
			}
		}
	}
	return nil
}

func recordEvent(record slog.Record) string {
	var event string
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == FieldEvent && attr.Value.Kind() == slog.KindString {
			event = attr.Value.String()
			return false
		}
		return true
	})
	return event
}

func recordStringAttr(record slog.Record, key string) string {
	var value string
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == key && attr.Value.Kind() == slog.KindString {
			value = attr.Value.String()
			return false
		}
		return true
	})
	return value
}

func consoleStackField(key string) bool {
	return key == FieldErrorStack || key == FieldStack || key == FieldDBQueryText
}

// inboundHTTPDebugField 只剥离入站 completed 记录上的大块转储字段。
// 出站 llm/git 记录复用 http.request / http.response，必须继续走 tint 的
// request= / response= 短别名，不能改成后续物理行。
var inboundHTTPDebugFieldOrder = []string{
	FieldHTTPRequestHeaders,
	FieldHTTPRequestQuery,
	FieldHTTPRequest,
	FieldHTTPResponseHeaders,
	FieldHTTPResponse,
}

func inboundHTTPDebugField(event, key string) bool {
	if event != EventHTTPCompleted && event != EventTencentCLSRequestCompleted {
		return false
	}
	switch key {
	case FieldHTTPRequest, FieldHTTPResponse, FieldHTTPRequestHeaders, FieldHTTPResponseHeaders, FieldHTTPRequestQuery:
		return true
	default:
		return false
	}
}

func remediationPayloadField(event, key string) bool {
	if key != FieldPayload {
		return false
	}
	switch event {
	case EventRemediationContextPayload, EventRemediationModelPayload, EventRemediationToolPayload, EventTencentCLSEvidenceProjected:
		return true
	default:
		return false
	}
}

func formatPayloadBlock(event, kind, payload string) string {
	label := "remediation_payload"
	if event == EventTencentCLSEvidenceProjected {
		label = "tencent_cls_evidence"
	}
	if kind != "" {
		label += "[" + kind + "]"
	}
	return label + ":\n" + payload
}

func formatInboundHTTPDebugBlock(key, value string) string {
	if value == "" {
		return ""
	}
	label := inboundHTTPDebugLabel(key)
	if label == "" {
		return value
	}
	return label + ":\n" + value
}

func inboundHTTPDebugLabel(key string) string {
	switch key {
	case FieldHTTPRequestHeaders:
		return "request_headers"
	case FieldHTTPRequestQuery:
		return "request_query"
	case FieldHTTPRequest:
		return "request"
	case FieldHTTPResponseHeaders:
		return "response_headers"
	case FieldHTTPResponse:
		return "response"
	default:
		return ""
	}
}

func (h consoleHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	component := h.component
	filtered := make([]slog.Attr, 0, len(attrs))
	stacks := append([]string(nil), h.stacks...)
	for _, attr := range attrs {
		if !h.grouped && attr.Key == FieldComponent && attr.Value.Kind() == slog.KindString {
			component = attr.Value.String()
			continue
		}
		if !h.grouped && consoleStackField(attr.Key) && attr.Value.Kind() == slog.KindString {
			if stack := attr.Value.String(); stack != "" {
				stacks = append(stacks, stack)
			}
			continue
		}
		filtered = append(filtered, attr)
	}
	return consoleHandler{
		next:      h.next.WithAttrs(filtered),
		writer:    h.writer,
		outputMu:  h.outputMu,
		component: component,
		grouped:   h.grouped,
		stacks:    stacks,
	}
}

func (h consoleHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return consoleHandler{
		next:      h.next.WithGroup(name),
		writer:    h.writer,
		outputMu:  h.outputMu,
		component: h.component,
		grouped:   true,
		stacks:    h.stacks,
	}
}

func compactConsoleID(attr slog.Attr, key string) slog.Attr {
	attr.Key = key
	if attr.Value.Kind() != slog.KindString {
		return attr
	}
	const maxLength = 8
	value := attr.Value.String()
	if len(value) > maxLength {
		value = value[:maxLength]
	}
	attr.Value = slog.StringValue(value)
	return attr
}

func writerSupportsColor(writer io.Writer) bool {
	terminal, ok := writer.(interface{ Fd() uintptr })
	if !ok {
		return false
	}
	fd := terminal.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

// Log 写入一条带稳定 event name 的结构化记录。
// attrs 只能包含调用边界允许的低基数字段。除 http.request.failed 的脱敏截断
// 入参快照、llm.request.completed / git.request.completed 的脱敏截断出站快照、
// remediation.*.payload 的 64 KiB 脱敏调试快照，
// 以及 MENDRY_HTTP_REQUEST_DEBUG 打开时挂在 http.request.completed 上的未脱敏
// 入站请求/响应转储外，不得携带 payload、secret 或原始 URL。
func Log(ctx context.Context, logger *slog.Logger, level slog.Level, event, message string, attrs ...slog.Attr) {
	base := []slog.Attr{slog.String(FieldEvent, event)}
	spanContext := trace.SpanContextFromContext(ctx)
	if spanContext.IsValid() {
		base = append(base,
			slog.String(FieldTraceID, spanContext.TraceID().String()),
			slog.String(FieldSpanID, spanContext.SpanID().String()),
		)
	}
	logger.LogAttrs(ctx, level, message, append(base, attrs...)...)
}

func parseLevel(value string) (slog.Level, error) {
	switch value {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unsupported log level")
	}
}
