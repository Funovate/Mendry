// Package httpserver 封装共享 HTTP server 的构造、访问日志和有界优雅关闭。
package httpserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"fixthe/backend/internal/platform/errtrace"
	"fixthe/backend/internal/platform/observability"
)

// Options 声明 HTTP server 的依赖和所有资源边界。
type Options struct {
	Address           string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	Handler           http.Handler
	Logger            *slog.Logger
	Listen            ListenFunc
	Component         string
}

// ListenFunc 抽象 listener 创建；生产环境使用 net.Listen，测试可注入隔离实现。
type ListenFunc func(network, address string) (net.Listener, error)

// Server 管理一个 HTTP listener 从启动到有界 drain 的完整生命周期。
type Server struct {
	server          *http.Server
	shutdownTimeout time.Duration
	logger          *slog.Logger
	listen          ListenFunc
	component       string
}

// New 在创建资源前验证依赖，并返回尚未开始监听的 Server。
func New(options Options) (*Server, error) {
	if options.Handler == nil {
		return nil, fmt.Errorf("http handler is required")
	}
	if options.Logger == nil {
		return nil, fmt.Errorf("http logger is required")
	}
	if options.ShutdownTimeout <= 0 {
		return nil, fmt.Errorf("http shutdown timeout must be positive")
	}
	if options.Component == "" {
		return nil, fmt.Errorf("http component is required")
	}

	listen := options.Listen
	if listen == nil {
		listen = net.Listen
	}

	return &Server{
		server: &http.Server{
			Addr:              options.Address,
			Handler:           options.Handler,
			ReadHeaderTimeout: options.ReadHeaderTimeout,
			ReadTimeout:       options.ReadTimeout,
			WriteTimeout:      options.WriteTimeout,
			IdleTimeout:       options.IdleTimeout,
		},
		shutdownTimeout: options.ShutdownTimeout,
		logger:          options.Logger,
		listen:          listen,
		component:       options.Component,
	}, nil
}

// Run 启动监听并阻塞到 server 失败或 ctx 取消。
// ctx 取消后先停止接收新请求，再在 ShutdownTimeout 内等待处理中请求完成。
func (s *Server) Run(ctx context.Context) error {
	listener, err := s.listen("tcp", s.server.Addr)
	if err != nil {
		return fmt.Errorf("listen on configured HTTP address: %w", err)
	}
	observability.Log(ctx, s.logger, slog.LevelInfo, observability.EventProcessStarted, "process started",
		slog.String(observability.FieldComponent, s.component),
	)
	observability.Log(ctx, s.logger, slog.LevelInfo, observability.EventHTTPServerReady, "server ready",
		slog.String(observability.FieldComponent, "httpserver"),
		slog.String("address", listener.Addr().String()),
	)

	// channel 必须带缓冲；若 shutdown 自身失败并提前返回，Serve goroutine 仍可
	// 写入最终结果并退出，不会因无人接收而泄漏。
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- s.server.Serve(listener)
	}()

	select {
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
	}

	observability.Log(ctx, s.logger, slog.LevelInfo, observability.EventProcessStopping, "server stopping",
		slog.String(observability.FieldComponent, "httpserver"),
	)

	// 根 ctx 此时已经取消，drain 必须使用独立的有界 context，否则 Shutdown 会
	// 立即失败并中断仍在处理的请求。
	shutdownContext, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
	defer cancel()

	if err := s.server.Shutdown(shutdownContext); err != nil {
		// 优雅关闭超时后强制释放 listener 和连接，确保进程不会无限阻塞退出。
		_ = s.server.Close()
		return fmt.Errorf("shutdown HTTP server: %w", err)
	}

	err = <-serveErrors
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP during shutdown: %w", err)
	}
	return nil
}

// AccessLog 为每个请求记录一次完成事件。默认只写身份字段；status >= 400 且
// 不是客户端关闭 499 时，再写一份脱敏、截断后的 http.request.failed 入参快照。
// requestDebug 打开后，未脱敏的请求/响应转储和 escaped path 挂在同一条
// INFO completed 记录上，并跳过 failed 快照，避免明文和 [redacted] 各写一份。
// 默认日志使用 mux 匹配后的 route pattern，不记录可能携带敏感参数且高基数的原始 URL。
func AccessLog(logger *slog.Logger, maxBodyBytes int64, requestDebug bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: writer, status: http.StatusOK}

		var capture *bodyCapture
		if request.Body != nil && request.Body != http.NoBody {
			capture = newBodyCapture(request.Body, maxBodyBytes)
			request.Body = capture
		}

		// 关闭调试时保持原来的 statusRecorder 分配路径，避免每个请求都缓冲响应。
		handlerWriter := http.ResponseWriter(recorder)
		var capturedResponse *responseCapture
		if requestDebug {
			capturedResponse = newResponseCapture(recorder, maxBodyBytes)
			handlerWriter = capturedResponse
		}

		next.ServeHTTP(handlerWriter, request)

		route := request.Pattern
		if route == "" {
			route = "unmatched"
		}

		attrs := []slog.Attr{
			slog.String(observability.FieldComponent, "httpserver"),
			slog.String(observability.FieldRequestID, RequestID(request.Context())),
			slog.String("method", request.Method),
			slog.String("route", route),
			slog.Int("status", recorder.status),
			slog.Int64(observability.FieldDurationMS, time.Since(started).Milliseconds()),
		}
		if requestDebug {
			attrs = appendRequestDebugAttrs(attrs, request, capture, capturedResponse)
		}
		observability.Log(request.Context(), logger, slog.LevelInfo, observability.EventHTTPCompleted, "request completed", attrs...)

		if recorder.internalError != nil {
			logInternalError(request, logger, route, recorder.status, recorder.internalError, recorder.internalErrorStack)
		}
		if !requestDebug && recorder.status >= http.StatusBadRequest && recorder.status != statusClientClosedRequest {
			logFailureSnapshot(request, logger, route, recorder.status, capture)
		}
	})
}

func appendRequestDebugAttrs(attrs []slog.Attr, request *http.Request, capture *bodyCapture, response *responseCapture) []slog.Attr {
	requestHeaders := http.Header(nil)
	if request != nil {
		requestHeaders = request.Header
	}
	attrs = append(attrs, slog.String(observability.FieldHTTPRequestHeaders, formatHeaderDump(requestHeaders)))
	if request != nil && request.URL != nil {
		attrs = append(attrs, slog.String(observability.FieldHTTPPath, request.URL.EscapedPath()))
		if request.URL.RawQuery != "" {
			attrs = append(attrs, slog.String(observability.FieldHTTPRequestQuery, request.URL.RawQuery))
		}
	}
	if capture != nil {
		if body := capture.bytes(); len(body) > 0 {
			attrs = append(attrs, slog.String(observability.FieldHTTPRequest, string(body)))
		}
		if capture.truncated() {
			attrs = append(attrs, slog.Bool(observability.FieldHTTPRequestTruncated, true))
		}
	}
	responseHeaders := http.Header(nil)
	if response != nil {
		responseHeaders = response.Header()
	}
	attrs = append(attrs, slog.String(observability.FieldHTTPResponseHeaders, formatHeaderDump(responseHeaders)))
	if response != nil {
		if body := response.bytes(); len(body) > 0 {
			attrs = append(attrs, slog.String(observability.FieldHTTPResponse, string(body)))
		}
		if response.truncated() {
			attrs = append(attrs, slog.Bool(observability.FieldHTTPResponseTruncated, true))
		}
	}
	return attrs
}

// formatHeaderDump 把 header 编成确定性的 Name: value 文本；同名多值各占一行。
// 调试转储不丢 hop-by-hop header，也不脱敏 Cookie / Authorization / Set-Cookie。
func formatHeaderDump(headers http.Header) string {
	if len(headers) == 0 {
		return ""
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)

	var builder strings.Builder
	for _, name := range names {
		canonical := http.CanonicalHeaderKey(name)
		for _, value := range headers[name] {
			if builder.Len() > 0 {
				builder.WriteByte('\n')
			}
			builder.WriteString(canonical)
			builder.WriteString(": ")
			builder.WriteString(value)
		}
	}
	return builder.String()
}

const maximumErrorCauses = 32

const (
	errorStackSourceWrappedError = "wrapped_error"
	errorStackSourceHTTPBoundary = "http_boundary"
)

type errorCause struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// logInternalError 保留 HTTP 边界收到的原始错误文本；客户端响应仍由 WriteInternalError 隔离。
func logInternalError(
	request *http.Request,
	logger *slog.Logger,
	route string,
	status int,
	cause error,
	boundaryStack errtrace.Trace,
) {
	errorStack := boundaryStack
	stackSource := errorStackSourceHTTPBoundary
	if captured, ok := errtrace.FromError(cause); ok {
		errorStack = captured
		stackSource = errorStackSourceWrappedError
	}

	observability.Log(request.Context(), logger, slog.LevelError, observability.EventHTTPError, "request error",
		slog.String(observability.FieldComponent, "httpserver"),
		slog.String(observability.FieldRequestID, RequestID(request.Context())),
		slog.String("method", request.Method),
		slog.String("route", route),
		slog.Int("status", status),
		slog.String(observability.FieldErrorType, fmt.Sprintf("%T", cause)),
		slog.String(observability.FieldErrorMessage, cause.Error()),
		slog.Any(observability.FieldErrorCauses, collectErrorCauses(cause)),
		slog.String(observability.FieldErrorStack, errorStack.String()),
		slog.String(observability.FieldErrorStackSource, stackSource),
	)
}

func collectErrorCauses(root error) []errorCause {
	pending := unwrapErrors(root)
	causes := make([]errorCause, 0, min(len(pending), maximumErrorCauses))
	for len(pending) > 0 && len(causes) < maximumErrorCauses {
		current := pending[0]
		pending = pending[1:]
		if current == nil {
			continue
		}
		causes = append(causes, errorCause{Type: fmt.Sprintf("%T", current), Message: current.Error()})
		pending = append(pending, unwrapErrors(current)...)
	}
	return causes
}

func unwrapErrors(err error) []error {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		return joined.Unwrap()
	}
	if cause := errors.Unwrap(err); cause != nil {
		return []error{cause}
	}
	return nil
}

// logFailureSnapshot 记录一条独立的 http.request.failed 事件：仅在请求失败时
// 触发，携带脱敏、截断后的请求体与 query 快照，4xx 为 WARN，5xx 为 ERROR。
func logFailureSnapshot(request *http.Request, logger *slog.Logger, route string, status int, capture *bodyCapture) {
	level := slog.LevelWarn
	if status >= http.StatusInternalServerError {
		level = slog.LevelError
	}

	attrs := []slog.Attr{
		slog.String(observability.FieldComponent, "httpserver"),
		slog.String(observability.FieldRequestID, RequestID(request.Context())),
		slog.String("method", request.Method),
		slog.String("route", route),
		slog.Int("status", status),
	}

	if capture != nil {
		body := buildBodySnapshot(capture.bytes())
		if body.present {
			attrs = append(attrs, slog.Bool(observability.FieldBodyParseError, body.parseError))
			if !body.parseError {
				attrs = append(attrs,
					slog.String(observability.FieldRequestBody, body.body),
					slog.Bool(observability.FieldBodyTruncated, body.truncated),
				)
			}
		}
	}

	query := buildQuerySnapshot(request.URL.Query())
	if query.present {
		attrs = append(attrs,
			slog.String(observability.FieldRequestQuery, query.query),
			slog.Bool(observability.FieldQueryTruncated, query.truncated),
		)
	}

	observability.Log(request.Context(), logger, level, observability.EventHTTPFailed, "request failed", attrs...)
}

// statusRecorder 只记录第一次提交的 status，同时保留底层 ResponseWriter 的能力。
type statusRecorder struct {
	http.ResponseWriter
	status             int
	wroteHeader        bool
	internalError      error
	internalErrorStack errtrace.Trace
}

func (r *statusRecorder) recordInternalError(cause error) {
	if r.internalError == nil {
		r.internalError = cause
		// Skip recordInternalError, captureInternalError, and WriteInternalError so
		// the fallback begins at the HTTP adapter that submitted the failure.
		r.internalErrorStack = errtrace.Capture(3)
	}
}

// WriteHeader 只记录第一次提交的 status，与 net/http 的响应语义保持一致。
func (r *statusRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.status = status
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(status)
}

// Write 在未显式提交 header 时按 net/http 语义记录 200。
func (r *statusRecorder) Write(body []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(body)
}

// Unwrap 暴露底层 writer，使 http.ResponseController 仍可使用 Flush、Hijack 等能力。
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// responseCapture 只在 request debug 打开时包装 statusRecorder，按 MaxBodyBytes
// 缓冲响应体。关闭调试时不得构造它，以免每个请求都复制响应。
type responseCapture struct {
	*statusRecorder
	buffer bytes.Buffer
	limit  int64
	cut    bool
}

func newResponseCapture(recorder *statusRecorder, limit int64) *responseCapture {
	return &responseCapture{statusRecorder: recorder, limit: limit}
}

func (r *responseCapture) Write(body []byte) (int, error) {
	n, err := r.statusRecorder.Write(body)
	if n > 0 {
		r.capture(body[:n])
	}
	return n, err
}

func (r *responseCapture) capture(body []byte) {
	if r.limit <= 0 {
		r.cut = true
		return
	}
	remaining := r.limit - int64(r.buffer.Len())
	if remaining <= 0 {
		r.cut = true
		return
	}
	if int64(len(body)) > remaining {
		r.buffer.Write(body[:remaining])
		r.cut = true
		return
	}
	r.buffer.Write(body)
}

func (r *responseCapture) bytes() []byte { return r.buffer.Bytes() }

func (r *responseCapture) truncated() bool { return r.cut }

// Unwrap 只揭开 responseCapture 这一层，让后续 walker 仍能看到 statusRecorder
// 的 status / internalError，再继续拿到 Flush / Hijack。
func (r *responseCapture) Unwrap() http.ResponseWriter {
	return r.statusRecorder
}
