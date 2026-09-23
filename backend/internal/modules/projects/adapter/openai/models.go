// Package openai 列出 OpenAI 兼容或 Anthropic /v1/models，供项目配置向导选择模型。
// 凭据只在调用期间注入，不写入日志或返回值。
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"mendry/backend/internal/modules/projects/domain"
	"mendry/backend/internal/platform/observability"
)

const (
	defaultTimeout   = 15 * time.Second
	maxResponseBytes = 1 << 20
	opModelsList     = "models.list"
	opChatProbe      = "chat.completions"
	opResponsesProbe = "responses"
	opMessagesProbe  = "messages"
	opRuleGenerate   = "log_rule.generate"
	// 推理模型会从同一输出预算中消费 reasoning tokens，探测上限需给最终短回复留出空间。
	chatProbeMaxTokens = 128
	// anthropicVersion 是 Anthropic Messages API 要求的版本头。
	anthropicVersion = "2023-06-01"
)

// setAuth 按协议注入凭据：Anthropic 使用 x-api-key，OpenAI 兼容端点使用 Bearer。
func setAuth(request *http.Request, apiKey []byte, apiMode domain.LLMAPIMode) {
	if apiMode == domain.LLMAPIModeMessages {
		request.Header.Set("x-api-key", string(apiKey))
		request.Header.Set("anthropic-version", anthropicVersion)
		return
	}
	request.Header.Set("Authorization", "Bearer "+string(apiKey))
}

func providerName(apiMode domain.LLMAPIMode) string {
	if apiMode == domain.LLMAPIModeMessages {
		return domain.LLMProviderAnthropic
	}
	return domain.LLMProviderOpenAI
}

// Lister 调用 OpenAI 兼容 Models API。
type Lister struct {
	http   *http.Client
	logger *slog.Logger
}

// NewLister 构造模型列表适配器。logger 可选；缺省时不写观测记录。
func NewLister(client *http.Client, logger *slog.Logger) *Lister {
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	return &Lister{http: client, logger: logger}
}

type modelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// ListModels 返回排序后的模型 ID；失败不回传响应体或 API key。
func (l *Lister) ListModels(ctx context.Context, baseURL string, apiKey []byte, apiMode domain.LLMAPIMode) ([]string, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/v1/models"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create openai models request: %w", err)
	}
	setAuth(request, apiKey, apiMode)
	started := time.Now()
	status, body, err := l.roundTrip(request)
	if err != nil {
		l.logRequest(ctx, apiMode, opModelsList, endpoint, "", nil, status, time.Since(started), body, err)
		return nil, err
	}
	var decoded modelsResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		wrapped := fmt.Errorf("decode openai models: %w", err)
		l.logRequest(ctx, apiMode, opModelsList, endpoint, "", nil, status, time.Since(started), body, wrapped)
		return nil, wrapped
	}
	l.logRequest(ctx, apiMode, opModelsList, endpoint, "", nil, status, time.Since(started), body, nil)
	models := make([]string, 0, len(decoded.Data))
	seen := make(map[string]struct{}, len(decoded.Data))
	for _, item := range decoded.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		models = append(models, id)
	}
	sort.Strings(models)
	return models, nil
}

type chatProbeRequest struct {
	Model       string              `json:"model"`
	MaxTokens   int                 `json:"max_tokens"`
	Temperature float64             `json:"temperature"`
	Messages    []map[string]string `json:"messages"`
}

type chatProbeResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

type responsesRequest struct {
	Model           string              `json:"model"`
	MaxOutputTokens int                 `json:"max_output_tokens"`
	Input           []map[string]string `json:"input"`
}

// messagesRequest 是 Anthropic Messages API 请求；system 为顶层字段而非消息。
// 不发送 temperature：较新的 Claude 模型拒绝非默认采样参数。
type messagesRequest struct {
	Model     string              `json:"model"`
	MaxTokens int                 `json:"max_tokens"`
	System    string              `json:"system,omitempty"`
	Messages  []map[string]string `json:"messages"`
}

type messagesResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

type responsesResponse struct {
	Output []struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

// ProbeChat 用固定 hi 探测已选模型能否通过配置的生成接口返回文本。
// 不回传模型文本、响应体或 API key。
func (l *Lister) ProbeChat(ctx context.Context, baseURL string, apiKey []byte, model string, apiMode domain.LLMAPIMode) error {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	endpoint := baseURL + "/v1/chat/completions"
	operation := opChatProbe
	var requestPayload any = chatProbeRequest{
		Model:       strings.TrimSpace(model),
		MaxTokens:   chatProbeMaxTokens,
		Temperature: 0,
		Messages:    []map[string]string{{"role": "user", "content": "hi"}},
	}
	if apiMode == domain.LLMAPIModeResponses {
		endpoint = baseURL + "/v1/responses"
		operation = opResponsesProbe
		requestPayload = responsesRequest{
			Model: strings.TrimSpace(model), MaxOutputTokens: chatProbeMaxTokens,
			Input: []map[string]string{{"role": "user", "content": "hi"}},
		}
	}
	if apiMode == domain.LLMAPIModeMessages {
		endpoint = baseURL + "/v1/messages"
		operation = opMessagesProbe
		requestPayload = messagesRequest{
			Model: strings.TrimSpace(model), MaxTokens: chatProbeMaxTokens,
			Messages: []map[string]string{{"role": "user", "content": "hi"}},
		}
	}
	payload, err := json.Marshal(requestPayload)
	if err != nil {
		return fmt.Errorf("encode openai generation probe: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create openai generation probe: %w", err)
	}
	setAuth(request, apiKey, apiMode)
	request.Header.Set("Content-Type", "application/json")
	started := time.Now()
	status, body, err := l.roundTrip(request)
	if err != nil {
		l.logRequest(ctx, apiMode, operation, endpoint, model, payload, status, time.Since(started), body, err)
		return err
	}
	content, err := decodeGenerationText(body, apiMode)
	if err != nil {
		l.logRequest(ctx, apiMode, operation, endpoint, model, payload, status, time.Since(started), body, err)
		return err
	}
	if strings.TrimSpace(content) == "" {
		empty := fmt.Errorf("openai generation probe returned no content")
		l.logRequest(ctx, apiMode, operation, endpoint, model, payload, status, time.Since(started), body, empty)
		return empty
	}
	l.logRequest(ctx, apiMode, operation, endpoint, model, payload, status, time.Since(started), body, nil)
	return nil
}

func (l *Lister) GenerateLogRule(ctx context.Context, baseURL string, apiKey []byte, model string, apiMode domain.LLMAPIMode, intent, sample string) (domain.CustomRule, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	endpoint := baseURL + "/v1/chat/completions"
	prompt := `Return exactly one compact JSON object for a deterministic log monitoring rule. ` +
		`Fields: id (lowercase letter followed by lowercase letters, digits, _ or -; max 64), name, matchType (contains or regex), pattern, excludePattern, threshold, windowSeconds, cooldownSeconds. ` +
		`Do not include markdown. Prefer contains unless regex is necessary. Never place secrets or complete sample lines in pattern. ` +
		"Monitoring intent:\n" + intent + "\nRedacted log sample:\n" + sample
	const system = "You design bounded deterministic production log rules."
	inputs := []map[string]string{{"role": "system", "content": system}, {"role": "user", "content": prompt}}
	var requestPayload any = chatProbeRequest{
		Model: strings.TrimSpace(model), MaxTokens: 700, Temperature: 0, Messages: inputs,
	}
	if apiMode == domain.LLMAPIModeResponses {
		endpoint = baseURL + "/v1/responses"
		requestPayload = responsesRequest{Model: strings.TrimSpace(model), MaxOutputTokens: 700, Input: inputs}
	}
	if apiMode == domain.LLMAPIModeMessages {
		endpoint = baseURL + "/v1/messages"
		requestPayload = messagesRequest{
			Model: strings.TrimSpace(model), MaxTokens: 700, System: system,
			Messages: []map[string]string{{"role": "user", "content": prompt}},
		}
	}
	payload, err := json.Marshal(requestPayload)
	if err != nil {
		return domain.CustomRule{}, fmt.Errorf("encode log rule request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return domain.CustomRule{}, fmt.Errorf("create log rule request: %w", err)
	}
	setAuth(request, apiKey, apiMode)
	request.Header.Set("Content-Type", "application/json")
	started := time.Now()
	status, body, err := l.roundTrip(request)
	if err != nil {
		l.logRequest(ctx, apiMode, opRuleGenerate, endpoint, model, nil, status, time.Since(started), nil, err)
		return domain.CustomRule{}, err
	}
	content, err := decodeGenerationText(body, apiMode)
	if err != nil {
		wrapped := fmt.Errorf("decode log rule response: %w", err)
		l.logRequest(ctx, apiMode, opRuleGenerate, endpoint, model, nil, status, time.Since(started), nil, wrapped)
		return domain.CustomRule{}, wrapped
	}
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(strings.TrimSpace(content), "```")
	var rule domain.CustomRule
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(content)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&rule); err != nil {
		wrapped := fmt.Errorf("decode generated log rule: %w", err)
		l.logRequest(ctx, apiMode, opRuleGenerate, endpoint, model, nil, status, time.Since(started), nil, wrapped)
		return domain.CustomRule{}, wrapped
	}
	l.logRequest(ctx, apiMode, opRuleGenerate, endpoint, model, nil, status, time.Since(started), nil, nil)
	return rule, nil
}

func decodeGenerationText(body []byte, apiMode domain.LLMAPIMode) (string, error) {
	if apiMode == domain.LLMAPIModeMessages {
		var decoded messagesResponse
		if err := json.Unmarshal(body, &decoded); err != nil {
			return "", fmt.Errorf("decode anthropic messages: %w", err)
		}
		var text strings.Builder
		for _, content := range decoded.Content {
			if content.Type == "text" {
				text.WriteString(content.Text)
			}
		}
		return text.String(), nil
	}
	if apiMode == domain.LLMAPIModeResponses {
		var decoded responsesResponse
		if err := json.Unmarshal(body, &decoded); err != nil {
			return "", fmt.Errorf("decode openai responses: %w", err)
		}
		var text strings.Builder
		for _, output := range decoded.Output {
			if output.Type != "message" {
				continue
			}
			for _, content := range output.Content {
				if content.Type == "output_text" {
					text.WriteString(content.Text)
				}
			}
		}
		return text.String(), nil
	}
	var decoded chatProbeResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", fmt.Errorf("decode openai chat completion: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return "", fmt.Errorf("openai chat completion returned no choices")
	}
	return decoded.Choices[0].Message.Content, nil
}

func (l *Lister) roundTrip(request *http.Request) (int, []byte, error) {
	response, err := l.http.Do(request)
	if err != nil {
		return 0, nil, fmt.Errorf("call openai: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return response.StatusCode, nil, fmt.Errorf("read openai response: %w", err)
	}
	if int64(len(body)) > maxResponseBytes {
		return response.StatusCode, nil, fmt.Errorf("openai response exceeded %d bytes", maxResponseBytes)
	}
	if response.StatusCode != http.StatusOK {
		return response.StatusCode, body, fmt.Errorf("openai returned status %d", response.StatusCode)
	}
	return response.StatusCode, body, nil
}

func (l *Lister) logRequest(ctx context.Context, apiMode domain.LLMAPIMode, operation, endpoint, model string, request []byte, status int, duration time.Duration, response []byte, err error) {
	host, path := observability.HTTPIdentity(endpoint)
	observability.LogLLMRequest(ctx, l.logger, observability.LLMRequest{
		Provider:  providerName(apiMode),
		Operation: operation,
		Host:      host,
		Path:      path,
		Model:     strings.TrimSpace(model),
		Status:    status,
		Duration:  duration,
		Request:   request,
		Response:  response,
		Err:       err,
	})
}
