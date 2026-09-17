package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"

	"mendry/backend/internal/platform/observability"
)

const requestIDHeader = "X-Request-ID"

type requestIDContextKey struct{}

// RequestID 返回 RequestID middleware 放入 context 的请求标识。
func RequestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDContextKey{}).(string)
	return value
}

// WithRequestID 验证或生成 request ID，并在每个响应中回显。
func WithRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		requestID := request.Header.Get(requestIDHeader)
		if !validRequestID(requestID) {
			requestID = newRequestID()
		}
		writer.Header().Set(requestIDHeader, requestID)
		ctx := context.WithValue(request.Context(), requestIDContextKey{}, requestID)
		metadataWriter := &requestMetadataWriter{ResponseWriter: writer, requestID: requestID, started: started}
		next.ServeHTTP(metadataWriter, request.WithContext(ctx))
	})
}

type requestMetadataWriter struct {
	http.ResponseWriter
	requestID string
	started   time.Time
}

func (writer *requestMetadataWriter) responseMetadata() (string, time.Time) {
	return writer.requestID, writer.started
}

func (writer *requestMetadataWriter) Unwrap() http.ResponseWriter { return writer.ResponseWriter }

func validRequestID(value string) bool {
	if len(value) < 8 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') &&
			character != '-' && character != '_' && character != '.' {
			return false
		}
	}
	return true
}

func newRequestID() string {
	identifier := make([]byte, 16)
	if _, err := rand.Read(identifier); err != nil {
		// crypto/rand 失败时仍返回安全格式；空值会破坏错误关联，但不应让请求 panic。
		return "request-id-unavailable"
	}
	return hex.EncodeToString(identifier)
}

// LimitBody 为所有请求设置统一读取上限；具体 handler 通过 DecodeJSON 将超限映射为 413。
func LimitBody(maxBytes int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Body != nil {
			request.Body = http.MaxBytesReader(writer, request.Body, maxBytes)
		}
		next.ServeHTTP(writer, request)
	})
}

// Recover 捕获 handler panic，记录原始 value/stack，并在尚未提交响应时返回稳定错误。
func Recover(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		tracker := &commitTracker{ResponseWriter: writer}
		defer func() {
			panicValue := recover()
			if panicValue == nil {
				return
			}
			observability.Log(request.Context(), logger, slog.LevelError,
				observability.EventHTTPPanicRecovered, "handler panic recovered",
				slog.String(observability.FieldComponent, "httpserver"),
				slog.String(observability.FieldRequestID, RequestID(request.Context())),
				slog.String(observability.FieldPanicType, fmt.Sprintf("%T", panicValue)),
				slog.String(observability.FieldPanicValue, fmt.Sprint(panicValue)),
				slog.String(observability.FieldStack, string(debug.Stack())),
			)
			if !tracker.committed {
				WriteError(writer, request, Error{
					Status: http.StatusInternalServerError, Code: "internal_error", Message: "An internal error occurred.",
				})
			}
		}()
		next.ServeHTTP(tracker, request)
	})
}

type commitTracker struct {
	http.ResponseWriter
	committed bool
}

func (writer *commitTracker) WriteHeader(status int) {
	if writer.committed {
		return
	}
	writer.committed = true
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *commitTracker) Write(body []byte) (int, error) {
	if !writer.committed {
		writer.WriteHeader(http.StatusOK)
	}
	return writer.ResponseWriter.Write(body)
}

func (writer *commitTracker) Unwrap() http.ResponseWriter { return writer.ResponseWriter }

// NormalizeMuxErrors 将 net/http mux 的默认 404/405 文本替换为统一 JSON 错误。
func NormalizeMuxErrors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		normalizer := &muxErrorWriter{ResponseWriter: writer, request: request}
		next.ServeHTTP(normalizer, request)
	})
}

type muxErrorWriter struct {
	http.ResponseWriter
	request    *http.Request
	suppressed bool
}

func (writer *muxErrorWriter) WriteHeader(status int) {
	if status != http.StatusNotFound && status != http.StatusMethodNotAllowed {
		writer.ResponseWriter.WriteHeader(status)
		return
	}
	// feature handler 通过 WriteError 主动返回的 JSON 404/405 已拥有稳定业务
	// code；只有 ServeMux 默认的纯文本错误需要在公共边界改写。
	if strings.HasPrefix(writer.Header().Get("Content-Type"), "application/json") {
		writer.ResponseWriter.WriteHeader(status)
		return
	}
	writer.Header().Del("Content-Type")
	writer.Header().Del("X-Content-Type-Options")
	apiError := Error{Status: status, Code: "not_found", Message: "The requested resource was not found."}
	if status == http.StatusMethodNotAllowed {
		apiError.Code = "method_not_allowed"
		apiError.Message = "The request method is not allowed."
	}
	writer.suppressed = true
	WriteError(writer.ResponseWriter, writer.request, apiError)
}

func (writer *muxErrorWriter) Write(body []byte) (int, error) {
	if writer.suppressed {
		return len(body), nil
	}
	return writer.ResponseWriter.Write(body)
}

func (writer *muxErrorWriter) Unwrap() http.ResponseWriter { return writer.ResponseWriter }

// CORS 允许一个显式跨源 console 携带 cookie；空 origin 表示仅同源。
func CORS(allowedOrigin string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		origin := request.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(writer, request)
			return
		}
		writer.Header().Add("Vary", "Origin")
		originAllowed := origin == allowedOrigin
		if allowedOrigin == "" {
			originAllowed = sameOrigin(origin, request)
		}
		if !originAllowed {
			WriteError(writer, request, Error{Status: http.StatusForbidden, Code: "origin_not_allowed", Message: "Request origin is not allowed."})
			return
		}

		writer.Header().Set("Access-Control-Allow-Origin", origin)
		writer.Header().Set("Access-Control-Allow-Credentials", "true")
		if request.Method == http.MethodOptions && request.Header.Get("Access-Control-Request-Method") != "" {
			writer.Header().Add("Vary", "Access-Control-Request-Method")
			writer.Header().Add("Vary", "Access-Control-Request-Headers")
			writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Request-ID")
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func sameOrigin(origin string, request *http.Request) bool {
	parsedOrigin, err := url.Parse(origin)
	expectedScheme := "http"
	if request.TLS != nil {
		expectedScheme = "https"
	}
	return err == nil && parsedOrigin.Scheme == expectedScheme &&
		parsedOrigin.Host == request.Host && parsedOrigin.User == nil && parsedOrigin.Path == "" &&
		parsedOrigin.RawQuery == "" && parsedOrigin.Fragment == ""
}

// BoundaryOptions 声明 API 公共 HTTP middleware 的依赖和资源限制。
type BoundaryOptions struct {
	Handler           http.Handler
	Logger            *slog.Logger
	MaxBodyBytes      int64
	CORSAllowedOrigin string
	// RequestDebug 打开后由 AccessLog 把完整请求/响应挂到 completed 记录；
	// 零值保持现有默认关闭路径，既有 Boundary 测试无需改动。
	RequestDebug bool
}

// Boundary 按固定顺序组装 request ID、access log、recovery、CORS、body limit
// 和 mux error normalization，避免不同 composition root 产生行为差异。
func Boundary(options BoundaryOptions) (http.Handler, error) {
	if options.Handler == nil {
		return nil, errors.New("HTTP boundary handler is required")
	}
	if options.Logger == nil {
		return nil, errors.New("HTTP boundary logger is required")
	}
	if options.MaxBodyBytes <= 0 {
		return nil, errors.New("HTTP boundary max body bytes must be positive")
	}

	handler := NormalizeMuxErrors(options.Handler)
	handler = LimitBody(options.MaxBodyBytes, handler)
	handler = CORS(options.CORSAllowedOrigin, handler)
	handler = Recover(options.Logger, handler)
	handler = AccessLog(options.Logger, options.MaxBodyBytes, options.RequestDebug, handler)
	handler = WithRequestID(handler)
	return handler, nil
}
