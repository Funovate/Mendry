package observability

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// 出站依赖失败的稳定 class，供日志检索；不得改成依赖原始错误文本。
const (
	OutboundCanceled = "canceled"
	OutboundTimeout  = "timeout"
	OutboundDNS      = "dns"
	OutboundTLS      = "tls"
	OutboundNetwork  = "network"
	OutboundHTTP4xx  = "http_4xx"
	OutboundHTTP5xx  = "http_5xx"
	OutboundDecode   = "decode"
	OutboundInternal = "internal"
	OutboundCommand  = "command"
)

const (
	outboundResponseMaxBytes = 4096
	redactedPlaceholder      = "[redacted]"
)

// 对象 key 的大小写不敏感包含匹配。*_tokens 是用量计数，不是密钥，单独放行。
var sensitiveFieldPatterns = []string{
	"password", "secret", "authorization", "apikey", "api_key", "api-key", "credential",
}

var sensitiveTokenFieldPatterns = []string{
	"token",
}

// OpenAI 兼容错误文本常把 key 嵌在普通 message 里，key 名脱敏抓不到。
var secretValuePattern = regexp.MustCompile(`(?i)(sk-[A-Za-z0-9_-]{8,}|Bearer\s+[A-Za-z0-9._~+/=-]{8,})`)

// git stderr 会回显认证后的 HTTPS URL 或 PEM 私钥；整段 userinfo / 私钥块都要拿掉。
var httpsUserinfoPattern = regexp.MustCompile(`(?i)https://[^/\s:@]+:[^/\s@]+@`)
var pemPrivateKeyPattern = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)

// LLMRequest 是一次出站 LLM HTTP 调用的有界观测记录。
// Host 和 Path 必须已经去掉 userinfo 与 query。Request/Response 只能是脱敏截断后的载荷。
type LLMRequest struct {
	Operation string
	Host      string
	Path      string
	Model     string
	Status    int
	Duration  time.Duration
	Request   []byte
	Response  []byte
	Err       error
}

// GitRequest 是一次出站 git 命令的有界观测记录。
// Host 和 Path 必须来自公开 remote URL，不得带 userinfo 或 query。
// Request 只能是公开命令身份；Response 是捕获到的 stdout/stderr。
type GitRequest struct {
	Operation string
	Host      string
	Path      string
	Duration  time.Duration
	Request   []byte
	Response  []byte
	Err       error
}

// HTTPIdentity 从原始 URL 提取可记录的 host 和 path。
// 解析失败或缺少 host 时返回 invalid，避免把含凭据或 query 的原文写入日志。
func HTTPIdentity(rawURL string) (host, path string) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return "invalid", "invalid"
	}
	path = parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	return parsed.Host, path
}

// ClassifyOutbound 把出站 HTTP 失败收成低基数 class。
// 有 HTTP 状态时优先按 4xx/5xx 分类；传输层错误看 cause；读到响应后再失败视为 decode。
func ClassifyOutbound(err error, status int) string {
	if err == nil {
		return ""
	}
	if status >= 400 && status < 500 {
		return OutboundHTTP4xx
	}
	if status >= 500 {
		return OutboundHTTP5xx
	}
	if errors.Is(err, context.Canceled) {
		return OutboundCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return OutboundTimeout
	}
	var dnsError *net.DNSError
	if errors.As(err, &dnsError) {
		return OutboundDNS
	}
	if isTLSError(err) {
		return OutboundTLS
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		if networkError.Timeout() {
			return OutboundTimeout
		}
		return OutboundNetwork
	}
	if status > 0 {
		return OutboundDecode
	}
	return OutboundInternal
}

// SnapshotHTTPPayload 把出站请求或响应收成可记录文本：JSON 先按字段脱敏，再扫密钥形态和 git userinfo/PEM，最后截到 4KB。
func SnapshotHTTPPayload(raw []byte) (body string, truncated bool) {
	if len(raw) == 0 {
		return "", false
	}
	text := string(raw)
	var parsed any
	if err := json.Unmarshal(raw, &parsed); err == nil {
		serialized, marshalErr := json.Marshal(redactJSON(parsed))
		if marshalErr == nil {
			text = string(serialized)
		}
	}
	text = secretValuePattern.ReplaceAllString(text, redactedPlaceholder)
	text = redactGitSecrets(text)
	return truncateUTF8(text, outboundResponseMaxBytes)
}

// LogLLMRequest 记录一次出站 LLM 调用的操作身份、结果和脱敏后的对方回包。
func LogLLMRequest(ctx context.Context, logger *slog.Logger, rec LLMRequest) {
	if logger == nil {
		return
	}
	class := ClassifyOutbound(rec.Err, rec.Status)
	outcome := "success"
	level := slog.LevelDebug
	if rec.Err != nil {
		outcome = "failure"
		level = outboundFailureLevel(class)
	}
	attrs := []slog.Attr{
		slog.String(FieldComponent, "openai"),
		slog.String(FieldLLMOperation, rec.Operation),
		slog.String(FieldHTTPHost, rec.Host),
		slog.String(FieldHTTPPath, rec.Path),
		slog.Int64(FieldDurationMS, rec.Duration.Milliseconds()),
		slog.String(FieldOutcome, outcome),
	}
	if rec.Model != "" {
		attrs = append(attrs, slog.String(FieldLLMModel, rec.Model))
	}
	if rec.Status > 0 {
		attrs = append(attrs, slog.Int(FieldHTTPStatus, rec.Status))
	}
	if class != "" {
		attrs = append(attrs, slog.String(FieldErrorClass, class))
	}
	if snapshot, truncated := SnapshotHTTPPayload(rec.Request); snapshot != "" {
		attrs = append(attrs, slog.String(FieldHTTPRequest, snapshot))
		if truncated {
			attrs = append(attrs, slog.Bool(FieldHTTPRequestTruncated, true))
		}
	}
	if snapshot, truncated := SnapshotHTTPPayload(rec.Response); snapshot != "" {
		attrs = append(attrs, slog.String(FieldHTTPResponse, snapshot))
		if truncated {
			attrs = append(attrs, slog.Bool(FieldHTTPResponseTruncated, true))
		}
	}
	Log(ctx, logger, level, EventLLMRequestCompleted, "request completed", attrs...)
}

// LogGitRequest 记录一次出站 git 命令的操作身份、结果和脱敏后的命令输出。
// logger 为 nil 时是 no-op，方便单测构造适配器。
func LogGitRequest(ctx context.Context, logger *slog.Logger, rec GitRequest) {
	if logger == nil {
		return
	}
	class := ClassifyGit(ctx, rec.Err)
	outcome := "success"
	level := slog.LevelDebug
	if rec.Err != nil {
		outcome = "failure"
		level = outboundFailureLevel(class)
	}
	attrs := []slog.Attr{
		slog.String(FieldComponent, "git"),
		slog.String(FieldGitOperation, rec.Operation),
		slog.String(FieldHTTPHost, rec.Host),
		slog.String(FieldHTTPPath, rec.Path),
		slog.Int64(FieldDurationMS, rec.Duration.Milliseconds()),
		slog.String(FieldOutcome, outcome),
	}
	if class != "" {
		attrs = append(attrs, slog.String(FieldErrorClass, class))
	}
	if snapshot, truncated := SnapshotHTTPPayload(rec.Request); snapshot != "" {
		attrs = append(attrs, slog.String(FieldHTTPRequest, snapshot))
		if truncated {
			attrs = append(attrs, slog.Bool(FieldHTTPRequestTruncated, true))
		}
	}
	if snapshot, truncated := SnapshotHTTPPayload(rec.Response); snapshot != "" {
		attrs = append(attrs, slog.String(FieldHTTPResponse, snapshot))
		if truncated {
			attrs = append(attrs, slog.Bool(FieldHTTPResponseTruncated, true))
		}
	}
	Log(ctx, logger, level, EventGitRequestCompleted, "request completed", attrs...)
}

// ClassifyGit 把 git 命令失败收成低基数 class。
// CommandContext 超时经常返回 signal: killed 而不是 context 错误，因此优先看 ctx.Err()。
// 不得从 git 退出码发明 HTTP 状态。
func ClassifyGit(ctx context.Context, err error) string {
	if err == nil {
		return ""
	}
	if class := classifyContextError(ctx.Err()); class != "" {
		return class
	}
	if class := classifyContextError(err); class != "" {
		return class
	}
	return OutboundCommand
}

func classifyContextError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return OutboundCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return OutboundTimeout
	}
	return ""
}

func redactGitSecrets(text string) string {
	text = httpsUserinfoPattern.ReplaceAllString(text, "https://"+redactedPlaceholder+"@")
	return pemPrivateKeyPattern.ReplaceAllString(text, redactedPlaceholder)
}

func outboundFailureLevel(class string) slog.Level {
	switch class {
	case OutboundCanceled:
		return slog.LevelInfo
	case OutboundTimeout, OutboundHTTP4xx:
		return slog.LevelWarn
	default:
		return slog.LevelError
	}
}

func isTLSError(err error) bool {
	var (
		unknownAuth x509.UnknownAuthorityError
		hostname    x509.HostnameError
		invalid     x509.CertificateInvalidError
		systemRoots x509.SystemRootsError
		record      tls.RecordHeaderError
	)
	return errors.As(err, &unknownAuth) ||
		errors.As(err, &hostname) ||
		errors.As(err, &invalid) ||
		errors.As(err, &systemRoots) ||
		errors.As(err, &record)
}

func redactJSON(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		for key, child := range typed {
			if isSensitiveKey(key) {
				redacted[key] = redactedPlaceholder
				continue
			}
			redacted[key] = redactJSON(child)
		}
		return redacted
	case []any:
		redacted := make([]any, len(typed))
		for i, child := range typed {
			redacted[i] = redactJSON(child)
		}
		return redacted
	default:
		return value
	}
}

func isSensitiveKey(key string) bool {
	lowered := strings.ToLower(key)
	if strings.HasSuffix(lowered, "_tokens") || lowered == "tokens" {
		return false
	}
	for _, pattern := range sensitiveFieldPatterns {
		if strings.Contains(lowered, pattern) {
			return true
		}
	}
	for _, pattern := range sensitiveTokenFieldPatterns {
		if strings.Contains(lowered, pattern) {
			return true
		}
	}
	return false
}

func truncateUTF8(value string, limit int) (string, bool) {
	if len(value) <= limit {
		return value, false
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut], true
}

// ClassifyCommand 保留 context 失败分类，并将其他命令失败归类为 command；SSH evidence 执行使用它避免 generic internal 隐藏进程边界。
func ClassifyCommand(ctx context.Context, err error) string {
	if err == nil {
		return ""
	}
	if class := classifyContextError(ctx.Err()); class != "" {
		return class
	}
	if class := classifyContextError(err); class != "" {
		return class
	}
	return OutboundCommand
}
