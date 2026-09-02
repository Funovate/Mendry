package observability

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// RemediationPayloadMaxBytes 是单个 remediation DEBUG payload 的最大记录字节数。
const RemediationPayloadMaxBytes = 64 * 1024

// PayloadSnapshot 描述完整脱敏 payload 与实际记录前缀之间的关系。
// SHA256 只对完整脱敏文本计算，禁止指纹化进入边界前的原始 secret-bearing 输入。
type PayloadSnapshot struct {
	Text        string
	Bytes       int
	LoggedBytes int
	Truncated   bool
	SHA256      string
}

var remediationAssignmentPattern = regexp.MustCompile(`(?im)(\b(?:password|token|secret|authorization|api[-_]?key|credential|value)\b\s*[:=]\s*)(?:"[^"]*"|'[^']*'|[^\s,;]+)`)

// SnapshotRemediationPayload 先确定性序列化和完整脱敏，再计算大小、哈希与 UTF-8 安全前缀。
func SnapshotRemediationPayload(value any) (PayloadSnapshot, error) {
	text, err := remediationPayloadText(value)
	if err != nil {
		return PayloadSnapshot{}, err
	}
	if text == "" {
		return PayloadSnapshot{}, nil
	}
	text = redactRemediationText(text)
	sum := sha256.Sum256([]byte(text))
	logged, truncated := truncateUTF8(text, RemediationPayloadMaxBytes)
	return PayloadSnapshot{
		Text:        logged,
		Bytes:       len(text),
		LoggedBytes: len(logged),
		Truncated:   truncated,
		SHA256:      hex.EncodeToString(sum[:]),
	}, nil
}

func remediationPayloadText(value any) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "", nil
	case string:
		return typed, nil
	case []byte:
		return string(typed), nil
	default:
		serialized, err := json.Marshal(value)
		if err != nil {
			return "", fmt.Errorf("serialize remediation payload: %w", err)
		}
		var parsed any
		if err := json.Unmarshal(serialized, &parsed); err != nil {
			return "", fmt.Errorf("normalize remediation payload: %w", err)
		}
		redacted, err := json.Marshal(redactRemediationJSON(parsed))
		if err != nil {
			return "", fmt.Errorf("serialize redacted remediation payload: %w", err)
		}
		return string(redacted), nil
	}
}

func redactRemediationJSON(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		for key, child := range typed {
			if isRemediationSensitiveKey(key) {
				redacted[key] = redactedPlaceholder
				continue
			}
			redacted[key] = redactRemediationJSON(child)
		}
		return redacted
	case []any:
		redacted := make([]any, len(typed))
		for index, child := range typed {
			redacted[index] = redactRemediationJSON(child)
		}
		return redacted
	case string:
		return redactRemediationText(typed)
	default:
		return value
	}
}

func isRemediationSensitiveKey(key string) bool {
	return isSensitiveKey(key) || strings.Contains(strings.ToLower(key), "value")
}

func redactRemediationText(text string) string {
	text = strings.ToValidUTF8(text, "\uFFFD")
	text = secretValuePattern.ReplaceAllString(text, redactedPlaceholder)
	text = redactGitSecrets(text)
	return remediationAssignmentPattern.ReplaceAllString(text, "${1}"+redactedPlaceholder)
}
