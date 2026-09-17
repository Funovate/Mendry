package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	defaultModelTimeout = 60 * time.Second
	// 推理模型会在同一输出预算中消耗 reasoning tokens；仍保留上限，避免分类器无界生成。
	// 推理模型在同一预算内消耗 reasoning tokens；8192 给 thinking + 输出留出足够空间。
	maxClassifierOutputTokens = 8192
	maxTitleRunes             = 240
	maxFingerprintRunes       = 255
	maxObservationMessage     = 65536
	maxObservationNameBytes   = 255
	maxModelInputBytes        = 12000
	maxGroupingFields         = 12
	maxGroupingNameRunes      = 64
	maxGroupingValueRunes     = 256
)

// NormalizedInbound 是 webhook 入站经过语义归一化后的持久化输入。
type NormalizedInbound struct {
	Title       string
	Fingerprint string
	Message     string
}

// FingerprintAnalyzer 从任意 webhook 原文提取标题和稳定分组字段。
// 实现必须在模型失败时保留可用的 deterministic fallback。
type FingerprintAnalyzer interface {
	Normalize(context.Context, string, string, string) (NormalizedInbound, error)
}

// ModelRequest 是 webhook classifier 所需的最小模型调用契约。
type ModelRequest struct {
	ProjectID    string
	SystemPrompt string
	UserMessage  string
	MaxTokens    int
	Temperature  float64
}

// ModelResponse 是 provider 返回的未解释 JSON 文本。
type ModelResponse struct {
	Content string
}

// ModelPort 执行一次没有 tools 的 bounded classifier 调用。
type ModelPort interface {
	Complete(context.Context, ModelRequest) (ModelResponse, error)
}

type fallbackAnalyzer struct{}

type modelAnalyzer struct {
	model   ModelPort
	timeout time.Duration
}

type classifierResponse struct {
	Title          string            `json:"title"`
	GroupingFields map[string]string `json:"grouping_fields"`
}

// NewFingerprintAnalyzer 构造带 deterministic fallback 的 webhook analyzer，使用 60 秒默认超时。
func NewFingerprintAnalyzer(model ModelPort) FingerprintAnalyzer {
	return NewFingerprintAnalyzerWithTimeout(model, defaultModelTimeout)
}

// NewFingerprintAnalyzerWithTimeout 构造带指定模型调用超时和 deterministic fallback 的 webhook analyzer。
func NewFingerprintAnalyzerWithTimeout(model ModelPort, timeout time.Duration) FingerprintAnalyzer {
	if model == nil {
		return fallbackAnalyzer{}
	}
	if timeout <= 0 {
		timeout = defaultModelTimeout
	}
	return modelAnalyzer{model: model, timeout: timeout}
}

// NormalizeInbound 保留 plain-text 调用方的兼容入口；JSON 使用 canonical fallback。
func NormalizeInbound(raw string) (title, fingerprint, message string, err error) {
	normalized, err := normalizeFallback("", "", raw)
	if err != nil {
		return "", "", "", err
	}
	return normalized.Title, normalized.Fingerprint, normalized.Message, nil
}

func (fallbackAnalyzer) Normalize(_ context.Context, projectID, sourceID, raw string) (NormalizedInbound, error) {
	return normalizeFallback(projectID, sourceID, raw)
}

func (a modelAnalyzer) Normalize(ctx context.Context, projectID, sourceID, raw string) (NormalizedInbound, error) {
	if err := validateInbound(raw); err != nil {
		return NormalizedInbound{}, err
	}
	if _, ok := decodeJSON(raw); !ok {
		return normalizeFallback(projectID, sourceID, raw)
	}
	fallback, err := normalizeFallback(projectID, sourceID, raw)
	if err != nil {
		return NormalizedInbound{}, err
	}
	modelPayload := classifierPayload(raw)
	requestContext, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	response, err := a.model.Complete(requestContext, ModelRequest{
		ProjectID: projectID, SystemPrompt: classifierSystemPrompt,
		UserMessage: "Normalize this webhook payload:\n" + modelPayload,
		MaxTokens:   maxClassifierOutputTokens, Temperature: 0,
	})
	if err != nil {
		return fallback, nil
	}
	parsed, err := decodeClassifierResponse(response.Content)
	if err != nil {
		return fallback, nil
	}
	fingerprint, err := hashGrouping("ai:v1", projectID, sourceID, parsed.GroupingFields)
	if err != nil {
		return fallback, nil
	}
	title := fallback.Title
	if strings.TrimSpace(parsed.Title) != "" {
		title = limitText(collapseSpace(parsed.Title), maxTitleRunes, maxTitleRunes*utf8.UTFMax)
	}
	return NormalizedInbound{Title: title, Fingerprint: fingerprint, Message: fallback.Message}, nil
}

func normalizeFallback(projectID, sourceID, raw string) (NormalizedInbound, error) {
	if err := validateInbound(raw); err != nil {
		return NormalizedInbound{}, err
	}
	message := limitText(raw, maxObservationMessage, maxObservationMessage)
	if value, ok := decodeJSON(raw); ok {
		grouping := sanitizeJSON(value, false)
		canonical, err := json.Marshal(grouping)
		if err != nil {
			return NormalizedInbound{}, fmt.Errorf("encode webhook fallback: %w", err)
		}
		fingerprint := hashBytes("fallback:v1", projectID, sourceID, canonical)
		return NormalizedInbound{
			Title:       jsonTitle(value),
			Fingerprint: fingerprint,
			Message:     message,
		}, nil
	}
	line, err := firstNonEmptyLine(raw)
	if err != nil {
		return NormalizedInbound{}, err
	}
	return NormalizedInbound{Title: limitText(line, maxTitleRunes, maxTitleRunes*utf8.UTFMax), Fingerprint: limitText(line, maxFingerprintRunes, maxObservationNameBytes), Message: message}, nil
}

func validateInbound(raw string) error {
	if !utf8.ValidString(raw) || strings.TrimSpace(raw) == "" {
		return ErrInvalidInput
	}
	return nil
}

func decodeJSON(raw string) (any, bool) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, false
	}
	return value, true
}

func decodeClassifierResponse(raw string) (classifierResponse, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var response classifierResponse
	if err := decoder.Decode(&response); err != nil {
		return classifierResponse{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return classifierResponse{}, fmt.Errorf("classifier response contains multiple values")
	}
	if len(response.GroupingFields) == 0 || len(response.GroupingFields) > maxGroupingFields {
		return classifierResponse{}, fmt.Errorf("classifier grouping fields are invalid")
	}
	normalizedFields := make(map[string]string, len(response.GroupingFields))
	for name, value := range response.GroupingFields {
		normalizedName := strings.ToLower(strings.TrimSpace(name))
		normalizedValue := collapseSpace(value)
		if !bounded(normalizedName, 1, maxGroupingNameRunes) || !bounded(normalizedValue, 1, maxGroupingValueRunes) {
			return classifierResponse{}, fmt.Errorf("classifier grouping field is invalid")
		}
		if _, exists := normalizedFields[normalizedName]; exists {
			return classifierResponse{}, fmt.Errorf("classifier grouping field names collide")
		}
		normalizedFields[normalizedName] = normalizedValue
	}
	response.GroupingFields = normalizedFields
	response.Title = collapseSpace(response.Title)
	if response.Title != "" && !bounded(response.Title, 1, maxTitleRunes) {
		return classifierResponse{}, fmt.Errorf("classifier title is invalid")
	}
	return response, nil
}

func classifierPayload(raw string) string {
	if value, ok := decodeJSON(raw); ok {
		encoded, err := json.Marshal(sanitizeJSON(value, true))
		if err == nil {
			return limitText(string(encoded), maxModelInputBytes, maxModelInputBytes)
		}
	}
	return limitText(collapseSpace(raw), maxModelInputBytes, maxModelInputBytes)
}

func sanitizeJSON(value any, redactSensitive bool) any {
	switch current := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(current))
		for key, item := range current {
			if volatileKey(key) {
				continue
			}
			if sensitiveKey(key) {
				if redactSensitive {
					result[key] = "[redacted]"
				}
				continue
			}
			result[key] = sanitizeJSON(item, redactSensitive)
		}
		return result
	case []any:
		result := make([]any, len(current))
		for index, item := range current {
			result[index] = sanitizeJSON(item, redactSensitive)
		}
		return result
	default:
		return value
	}
}

func jsonTitle(value any) string {
	object, ok := value.(map[string]any)
	if !ok {
		return "Webhook alert"
	}
	preferred := []string{"title", "alarm", "message", "error", "summary", "name", "condition"}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, wanted := range preferred {
		for _, key := range keys {
			item := object[key]
			if strings.EqualFold(strings.ReplaceAll(key, "_", ""), wanted) {
				if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
					return limitText(collapseSpace(text), maxTitleRunes, maxTitleRunes*utf8.UTFMax)
				}
			}
		}
	}
	return "Webhook alert"
}

func hashGrouping(prefix, projectID, sourceID string, fields map[string]string) (string, error) {
	canonical, err := json.Marshal(struct {
		ProjectID string            `json:"project_id"`
		SourceID  string            `json:"source_id"`
		Fields    map[string]string `json:"fields"`
	}{ProjectID: projectID, SourceID: sourceID, Fields: fields})
	if err != nil {
		return "", err
	}
	return hashBytes(prefix, projectID, sourceID, canonical), nil
}

func hashBytes(prefix, projectID, sourceID string, payload []byte) string {
	digest := sha256.Sum256(bytes.Join([][]byte{[]byte(projectID), []byte{0}, []byte(sourceID), []byte{0}, payload}, nil))
	return prefix + ":" + hex.EncodeToString(digest[:])
}

func volatileKey(key string) bool {
	normalized := strings.ToLower(strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, key))
	for _, candidate := range []string{"id", "uin", "requestid", "traceid", "spanid", "eventid", "timestamp", "occurredat", "detailurl", "url"} {
		if normalized == candidate || (strings.HasSuffix(normalized, candidate) && candidate != "id") {
			return true
		}
	}
	return false
}

func sensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, key))
	for _, candidate := range []string{"password", "token", "secret", "authorization", "apikey", "credential"} {
		if strings.Contains(normalized, candidate) {
			return true
		}
	}
	return normalized == "value"
}

const classifierSystemPrompt = "You normalize webhook alerts for incident grouping. Return exactly one JSON object with only title and grouping_fields. grouping_fields must contain stable semantic scalar strings such as category, alarm, service, error_code, error_type, operation, or resource. Exclude UINs, request IDs, timestamps, URLs, secrets, and per-event values. Do not diagnose root cause and do not include the final hash."

func firstNonEmptyLine(raw string) (string, error) {
	for _, line := range strings.Split(raw, "\n") {
		collapsed := collapseSpace(line)
		if collapsed == "" {
			continue
		}
		return collapsed, nil
	}
	return "", ErrInvalidInput
}

func limitText(value string, maxRunes, maxBytes int) string {
	if utf8.RuneCountInString(value) > maxRunes {
		value = string([]rune(value)[:maxRunes])
	}
	if len(value) <= maxBytes {
		return value
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}

func collapseSpace(value string) string {
	fields := strings.FieldsFunc(value, unicode.IsSpace)
	return strings.Join(fields, " ")
}

func bounded(value string, minimum, maximum int) bool {
	length := utf8.RuneCountInString(strings.TrimSpace(value))
	return length >= minimum && length <= maximum
}
