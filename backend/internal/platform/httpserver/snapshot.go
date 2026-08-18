package httpserver

import (
	"bytes"
	"encoding/json"
	"io"
	"net/url"
	"sort"
	"strings"
)

const (
	redactedPlaceholder = "[redacted]"
	snapshotMaxBytes    = 4096
)

// sensitiveFieldPatterns 是错误快照脱敏使用的黑名单：大小写不敏感的包含匹配，
// 宁可多脱敏也不可漏脱敏。value 覆盖项目凭据创建请求的明文载荷字段；
// api-key 覆盖 apikey/api_key 的连字符写法，Contains("apikey") 匹配不到它。
var sensitiveFieldPatterns = []string{
	"password", "token", "secret", "authorization", "apikey", "api_key", "api-key", "credential", "value",
}

func isSensitiveKey(key string) bool {
	lowered := strings.ToLower(key)
	for _, pattern := range sensitiveFieldPatterns {
		if strings.Contains(lowered, pattern) {
			return true
		}
	}
	return false
}

// bodyCapture 透明旁路缓冲流经的请求体字节，不改变内容、不影响下游读取行为
// （包括后续包裹它的 http.MaxBytesReader）。
type bodyCapture struct {
	source io.ReadCloser
	buffer bytes.Buffer
	limit  int64
}

func newBodyCapture(source io.ReadCloser, limit int64) *bodyCapture {
	return &bodyCapture{source: source, limit: limit}
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

func (c *bodyCapture) bytes() []byte { return c.buffer.Bytes() }

// bodySnapshot 是脱敏截断后的请求体快照。
type bodySnapshot struct {
	body       string
	present    bool
	truncated  bool
	parseError bool
}

// buildBodySnapshot 对捕获到的原始请求体做"解析 -> 脱敏 -> 序列化 -> 截断"。
// 解析失败时不记录任何原始内容，只标记 parseError，避免把无法结构化识别的
// 内容直接转储导致敏感信息泄露。
func buildBodySnapshot(raw []byte) bodySnapshot {
	if len(raw) == 0 {
		return bodySnapshot{}
	}

	var parsed any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return bodySnapshot{present: true, parseError: true}
	}

	redacted := redactJSON(parsed)
	serialized, err := json.Marshal(redacted)
	if err != nil {
		return bodySnapshot{present: true, parseError: true}
	}

	body, truncated := truncateUTF8(string(serialized), snapshotMaxBytes)
	return bodySnapshot{body: body, present: true, truncated: truncated}
}

// querySnapshot 是脱敏截断后的 query 参数快照。
type querySnapshot struct {
	query     string
	present   bool
	truncated bool
}

// buildQuerySnapshot 对 query 参数按黑名单脱敏后序列化并截断。
func buildQuerySnapshot(values url.Values) querySnapshot {
	if len(values) == 0 {
		return querySnapshot{}
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	redacted := make(map[string][]string, len(keys))
	for _, key := range keys {
		if isSensitiveKey(key) {
			replaced := make([]string, len(values[key]))
			for i := range replaced {
				replaced[i] = redactedPlaceholder
			}
			redacted[key] = replaced
			continue
		}
		redacted[key] = values[key]
	}

	serialized, err := json.Marshal(redacted)
	if err != nil {
		return querySnapshot{}
	}

	query, truncated := truncateUTF8(string(serialized), snapshotMaxBytes)
	return querySnapshot{query: query, present: true, truncated: truncated}
}

// redactJSON 递归脱敏已解析的 JSON 结构：对象按 key 匹配黑名单，数组逐元素
// 递归，其余类型原样返回。
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

// truncateUTF8 在合法 UTF-8 边界处把 value 截断到最多 limit 字节。
func truncateUTF8(value string, limit int) (string, bool) {
	if len(value) <= limit {
		return value, false
	}
	cut := limit
	for cut > 0 && !isUTF8Boundary(value, cut) {
		cut--
	}
	return value[:cut], true
}

func isUTF8Boundary(value string, index int) bool {
	if index >= len(value) {
		return true
	}
	return value[index]&0xC0 != 0x80
}
