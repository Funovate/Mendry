// Package openai 列出 OpenAI 兼容 /v1/models，供项目配置向导选择模型。
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

	"mendry/backend/internal/platform/observability"
)

const (
	defaultTimeout   = 15 * time.Second
	maxResponseBytes = 1 << 20
	opModelsList     = "models.list"
	opChatProbe      = "chat.completions"
	// 推理模型会从同一输出预算中消费 reasoning tokens，探测上限需给最终短回复留出空间。
	chatProbeMaxTokens = 128
)

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
func (l *Lister) ListModels(ctx context.Context, baseURL string, apiKey []byte) ([]string, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/v1/models"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create openai models request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+string(apiKey))
	started := time.Now()
	status, body, err := l.roundTrip(request)
	if err != nil {
		l.logRequest(ctx, opModelsList, endpoint, "", nil, status, time.Since(started), body, err)
		return nil, err
	}
	var decoded modelsResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		wrapped := fmt.Errorf("decode openai models: %w", err)
		l.logRequest(ctx, opModelsList, endpoint, "", nil, status, time.Since(started), body, wrapped)
		return nil, wrapped
	}
	l.logRequest(ctx, opModelsList, endpoint, "", nil, status, time.Since(started), body, nil)
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

// ProbeChat 用固定 hi 探测已选模型能否完成一次对话。
// 不回传模型文本、响应体或 API key。
func (l *Lister) ProbeChat(ctx context.Context, baseURL string, apiKey []byte, model string) error {
	endpoint := strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/v1/chat/completions"
	payload, err := json.Marshal(chatProbeRequest{
		Model:       strings.TrimSpace(model),
		MaxTokens:   chatProbeMaxTokens,
		Temperature: 0,
		Messages:    []map[string]string{{"role": "user", "content": "hi"}},
	})
	if err != nil {
		return fmt.Errorf("encode openai chat probe: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create openai chat probe: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+string(apiKey))
	request.Header.Set("Content-Type", "application/json")
	started := time.Now()
	status, body, err := l.roundTrip(request)
	if err != nil {
		l.logRequest(ctx, opChatProbe, endpoint, model, payload, status, time.Since(started), body, err)
		return err
	}
	var decoded chatProbeResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		wrapped := fmt.Errorf("decode openai chat probe: %w", err)
		l.logRequest(ctx, opChatProbe, endpoint, model, payload, status, time.Since(started), body, wrapped)
		return wrapped
	}
	if len(decoded.Choices) == 0 || strings.TrimSpace(decoded.Choices[0].Message.Content) == "" {
		empty := fmt.Errorf("openai chat probe returned no content")
		l.logRequest(ctx, opChatProbe, endpoint, model, payload, status, time.Since(started), body, empty)
		return empty
	}
	l.logRequest(ctx, opChatProbe, endpoint, model, payload, status, time.Since(started), body, nil)
	return nil
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

func (l *Lister) logRequest(ctx context.Context, operation, endpoint, model string, request []byte, status int, duration time.Duration, response []byte, err error) {
	host, path := observability.HTTPIdentity(endpoint)
	observability.LogLLMRequest(ctx, l.logger, observability.LLMRequest{
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
