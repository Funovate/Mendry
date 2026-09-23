package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	projectapplication "mendry/backend/internal/modules/projects/application"
	projectdomain "mendry/backend/internal/modules/projects/domain"
	"mendry/backend/internal/modules/remediation/domain"
	"mendry/backend/internal/platform/observability"
)

const (
	// ModelID 是测试和缺省展示用的 OpenAI 兼容模型标识。
	ModelID                  = "gpt-5.6"
	providerName             = "openai"
	defaultBaseURL           = "https://api.openai.com"
	defaultTimeout           = 5 * time.Minute
	maxResponseBytes         = 1 << 20
	maxToolCount             = 128
	maxToolNameBytes         = 128
	maxProviderToolNameBytes = 64
	maxToolDescBytes         = 4096
	maxToolSchemaBytes       = 64 << 10
	maxToolSchemaDepth       = 8
	maxToolArgumentBytes     = 64 << 10
	maxRetryAttempts         = 3
	initialRetryBackoff      = 250 * time.Millisecond
	maxRetryBackoff          = 2 * time.Second
	maxOutputRetries         = 1
	outputRetryMaxTokens     = 16384
)

// ErrModelTurnTimeout 表示整个逻辑模型轮次耗尽共享时限；它保持
// context.DeadlineExceeded 兼容性，供上层与 run elapsed exhaustion 区分。
var ErrModelTurnTimeout = fmt.Errorf("openai model turn timed out: %w", context.DeadlineExceeded)

type APIMode string

const (
	APIModeChatCompletions APIMode = "chat_completions"
	APIModeResponses       APIMode = "responses"
)

// ProviderConfig 是适配器读取的无凭据 LLM 配置。
type ProviderConfig struct {
	BaseURL            string
	Model              string
	CredentialSecretID string
	APIMode            APIMode
}

// ConfigLoader 按项目加载已保存的 LLM 接入点。
type ConfigLoader interface {
	LoadLLMProvider(ctx context.Context, projectID string) (ProviderConfig, error)
}

// SecretLoader 按项目和 secret ID 加载密文。
type SecretLoader interface {
	GetEncryptedSecret(ctx context.Context, projectID, secretID string) (projectdomain.EncryptedSecret, error)
}

// Options 构造 OpenAI 适配器。StaticAPIKey / BaseURL / Model 仅供测试。
type Options struct {
	Configs      ConfigLoader
	Secrets      SecretLoader
	Cipher       projectapplication.Cipher
	HTTPClient   *http.Client
	Logger       *slog.Logger
	Timeout      time.Duration
	BaseURL      string
	Model        string
	StaticAPIKey string
	APIMode      APIMode
}

// Client 用净 HTTP 调用 Chat Completions，不导出 SDK 类型。
type Client struct {
	configs      ConfigLoader
	secrets      SecretLoader
	cipher       projectapplication.Cipher
	http         *http.Client
	logger       *slog.Logger
	timeout      time.Duration
	baseURL      string
	model        string
	staticAPIKey string
	apiMode      APIMode
}

// NewClient 验证依赖并构造 LLM 适配器。
func NewClient(options Options) (*Client, error) {
	if options.StaticAPIKey == "" && (options.Configs == nil || options.Secrets == nil || options.Cipher == nil) {
		return nil, fmt.Errorf("openai client requires project LLM config lookup or a test-only static key")
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{}
	} else {
		// 逻辑轮次 context 是总时限的唯一所有者；复制后清除 Client.Timeout，
		// 避免每次 retry 都重新获得一份独立超时预算。
		cloned := *client
		cloned.Timeout = 0
		client = &cloned
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Client{
		configs:      options.Configs,
		secrets:      options.Secrets,
		cipher:       options.Cipher,
		http:         client,
		logger:       options.Logger,
		timeout:      timeout,
		baseURL:      strings.TrimRight(strings.TrimSpace(options.BaseURL), "/"),
		model:        strings.TrimSpace(options.Model),
		staticAPIKey: options.StaticAPIKey,
		apiMode:      options.APIMode,
	}, nil
}

var _ domain.LLMProviderPort = (*Client)(nil)

type chatRequest struct {
	Model          string            `json:"model"`
	Temperature    float64           `json:"temperature"`
	MaxTokens      int               `json:"max_tokens,omitempty"`
	ResponseFormat map[string]string `json:"response_format"`
	Messages       []chatMessage     `json:"messages"`
	Tools          []chatTool        `json:"tools,omitempty"`
}

type responsesRequest struct {
	Model           string               `json:"model"`
	MaxOutputTokens int                  `json:"max_output_tokens,omitempty"`
	Input           []responsesInputItem `json:"input"`
	Tools           []responsesTool      `json:"tools,omitempty"`
	Text            *responsesText       `json:"text,omitempty"`
}

type responsesInputItem struct {
	Type      string `json:"type,omitempty"`
	Role      string `json:"role,omitempty"`
	Content   string `json:"content,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Output    string `json:"output,omitempty"`
}

type responsesTool struct {
	Type        string                 `json:"type"`
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type responsesText struct {
	Format responseFormat `json:"format"`
}

type responsesResponse struct {
	Status            string `json:"status"`
	IncompleteDetails struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Output []struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		CallID    string `json:"call_id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
		Content   []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
	Usage responsesUsage `json:"usage"`
}

type responsesUsage struct {
	InputTokens        int64 `json:"input_tokens"`
	OutputTokens       int64 `json:"output_tokens"`
	TotalTokens        int64 `json:"total_tokens"`
	InputTokensDetails struct {
		CachedTokens *int64 `json:"cached_tokens"`
	} `json:"input_tokens_details"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
}

type chatTool struct {
	Type     string                 `json:"type"`
	Function chatFunctionDefinition `json:"function"`
}

type chatFunctionDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type chatToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function chatFunctionCall `json:"function"`
}

type chatFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// providerToolNames 将 provider-facing function name 与 remediation gateway、
// policy 和审计记录使用的逻辑名称隔离。
type providerToolNames struct {
	logicalToProvider map[string]string
	providerToLogical map[string]string
}

func newProviderToolNames() providerToolNames {
	return providerToolNames{
		logicalToProvider: make(map[string]string),
		providerToLogical: make(map[string]string),
	}
}

func (names *providerToolNames) add(logical string) (string, error) {
	if provider, exists := names.logicalToProvider[logical]; exists {
		return provider, nil
	}
	base := providerToolNameBase(logical)
	for suffix := 1; ; suffix++ {
		candidate := base
		if suffix > 1 {
			tail := fmt.Sprintf("_%d", suffix)
			prefixLength := maxProviderToolNameBytes - len(tail)
			if prefixLength <= 0 {
				return "", fmt.Errorf("provider tool name is too long")
			}
			if len(base) > prefixLength {
				base = base[:prefixLength]
			}
			candidate = base + tail
		}
		if owner, exists := names.providerToLogical[candidate]; exists {
			if owner == logical {
				return candidate, nil
			}
			continue
		}
		names.logicalToProvider[logical] = candidate
		names.providerToLogical[candidate] = logical
		return candidate, nil
	}
}

func (names providerToolNames) providerName(logical string) string {
	if provider, exists := names.logicalToProvider[logical]; exists {
		return provider
	}
	if strings.TrimSpace(logical) == "" {
		return ""
	}
	return providerToolNameBase(logical)
}

func (names providerToolNames) logicalName(provider string) (string, bool) {
	logical, exists := names.providerToLogical[provider]
	return logical, exists
}

func providerToolNameBase(logical string) string {
	var name strings.Builder
	for _, r := range logical {
		if isProviderToolNameCharacter(r) {
			name.WriteRune(r)
		} else {
			name.WriteByte('_')
		}
	}
	value := name.String()
	if value == "" {
		value = "tool"
	}
	if len(value) > maxProviderToolNameBytes {
		value = value[:maxProviderToolNameBytes]
	}
	return value
}

func isProviderToolName(value string) bool {
	if value == "" || len(value) > maxProviderToolNameBytes {
		return false
	}
	for _, r := range value {
		if !isProviderToolNameCharacter(r) {
			return false
		}
	}
	return true
}

func isProviderToolNameCharacter(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' ||
		r >= '0' && r <= '9' || r == '_' || r == '-'
}

type chatResponse struct {
	Choices []chatChoice `json:"choices"`
	Usage   chatUsage    `json:"usage"`
}

type chatChoice struct {
	FinishReason string              `json:"finish_reason"`
	Message      chatResponseMessage `json:"message"`
}

type chatResponseMessage struct {
	Content   string         `json:"content"`
	ToolCalls []chatToolCall `json:"tool_calls"`
}

type chatUsage struct {
	PromptTokens          int64  `json:"prompt_tokens"`
	CompletionTokens      int64  `json:"completion_tokens"`
	TotalTokens           int64  `json:"total_tokens"`
	PromptCacheHitTokens  *int64 `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens *int64 `json:"prompt_cache_miss_tokens"`
	PromptTokensDetails   struct {
		CachedTokens *int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

// Complete 把 ModelTurn 映射为一次有界的 Chat Completions 逻辑轮次。
func (c *Client) Complete(ctx context.Context, req domain.ModelTurn) (domain.ModelResult, error) {
	// 一次 Complete 只分配一份 deadline；配置加载、所有 HTTP attempts、
	// response read 与 retry backoff 共同消耗它，父 context 的更早 deadline 自动优先。
	turnCtx, cancel := context.WithTimeoutCause(ctx, c.timeout, ErrModelTurnTimeout)
	defer cancel()
	ctx = turnCtx

	session, err := c.resolveSession(ctx, req.ProjectID)
	if err != nil {
		if errors.Is(context.Cause(ctx), ErrModelTurnTimeout) {
			return domain.ModelResult{}, &domain.ProviderRuntimeError{
				Code: "provider_timeout", Retryable: true, Cause: ErrModelTurnTimeout,
			}
		}
		return domain.ModelResult{}, err
	}
	defer session.close()

	maxTokens := req.MaxTokens
	if maxTokens < 0 {
		maxTokens = 0
	}
	tools, toolNames, toolSchemaBytes, err := buildChatTools(req.Tools)
	if err != nil {
		return domain.ModelResult{}, fmt.Errorf("encode openai tools: %w", err)
	}
	baseResult := domain.ModelResult{
		Provider: providerName, Model: session.model,
		ToolCount: len(tools), ToolSchemaBytes: toolSchemaBytes,
	}
	messages, err := buildChatMessages(req, toolNames)
	if err != nil {
		return baseResult, fmt.Errorf("encode openai messages: %w", err)
	}

	var aggregate domain.ModelResult
	for outputAttempt := 0; outputAttempt <= maxOutputRetries; outputAttempt++ {
		attemptResult, attemptErr := c.completeWithTokens(
			ctx, session, req, maxTokens, tools, toolNames, toolSchemaBytes, messages,
		)
		aggregate = mergeModelResults(aggregate, attemptResult)
		if attemptErr == nil {
			return aggregate, nil
		}
		if !errors.Is(attemptErr, domain.ErrModelOutputExhausted) || outputAttempt == maxOutputRetries {
			return aggregate, attemptErr
		}
		// 输出预算重试仍属于同一个 Complete；复用 turnCtx，避免第二次尝试
		// 重新获得完整的 provider timeout。
		maxTokens = max(maxTokens, outputRetryMaxTokens)
	}
	return aggregate, fmt.Errorf("openai output retry attempts exhausted")
}

func (c *Client) completeWithTokens(
	ctx context.Context,
	session llmSession,
	req domain.ModelTurn,
	maxTokens int,
	tools []chatTool,
	toolNames providerToolNames,
	toolSchemaBytes int64,
	messages []chatMessage,
) (domain.ModelResult, error) {
	result := domain.ModelResult{
		Provider: providerName, Model: session.model,
		ToolCount: len(tools), ToolSchemaBytes: toolSchemaBytes,
	}
	var requestPayload any = chatRequest{
		Model:          session.model,
		Temperature:    req.Temperature,
		MaxTokens:      maxTokens,
		ResponseFormat: map[string]string{"type": "json_object"},
		Messages:       messages,
		Tools:          tools,
	}
	if session.apiMode == APIModeResponses {
		responses, buildErr := buildResponsesRequest(session.model, maxTokens, messages, tools)
		if buildErr != nil {
			return result, buildErr
		}
		requestPayload = responses
	}
	payload, err := json.Marshal(requestPayload)
	if err != nil {
		return result, fmt.Errorf("encode openai request: %w", err)
	}
	result.ModelCalls = 1
	result.RequestBytes = int64(len(payload))
	body, status, elapsed, err := c.doChatCompletion(ctx, session, payload, result)
	if err != nil {
		return result, err
	}
	logResult := func(callErr error) {
		c.logRequest(ctx, session.endpoint(), session.model, payload, status, elapsed, body, result, callErr)
	}
	var decoded chatResponse
	if session.apiMode == APIModeResponses {
		decoded, err = decodeResponsesResponse(body)
	} else {
		err = json.Unmarshal(body, &decoded)
	}
	if err != nil {
		wrapped := fmt.Errorf("decode openai response: %w", err)
		logResult(wrapped)
		return result, wrapped
	}
	applyUsageMetrics(&result, decoded.Usage)
	if len(decoded.Choices) == 0 {
		wrapped := fmt.Errorf("openai response contained no choices")
		logResult(wrapped)
		return result, wrapped
	}
	result.FinishReason = decoded.Choices[0].FinishReason
	toolCalls := make([]domain.ToolCall, 0, len(decoded.Choices[0].Message.ToolCalls))
	seenCallIDs := make(map[string]struct{}, len(decoded.Choices[0].Message.ToolCalls))
	if len(decoded.Choices[0].Message.ToolCalls) > maxToolCount {
		wrapped := fmt.Errorf("openai response contained too many tool calls")
		logResult(wrapped)
		return result, wrapped
	}
	for _, call := range decoded.Choices[0].Message.ToolCalls {
		if !isProviderToolName(call.Function.Name) || strings.TrimSpace(call.ID) == "" {
			wrapped := fmt.Errorf("openai response contained an invalid tool call")
			logResult(wrapped)
			return result, wrapped
		}
		if len(call.Function.Name) > maxProviderToolNameBytes || len(call.ID) > maxToolNameBytes {
			wrapped := fmt.Errorf("openai response contained an oversized tool call")
			logResult(wrapped)
			return result, wrapped
		}
		if _, exists := seenCallIDs[call.ID]; exists {
			wrapped := fmt.Errorf("openai response contained duplicate tool call ids")
			logResult(wrapped)
			return result, wrapped
		}
		seenCallIDs[call.ID] = struct{}{}
		if len(call.Function.Arguments) > maxToolArgumentBytes {
			wrapped := fmt.Errorf("openai tool call arguments exceeded the bound")
			logResult(wrapped)
			return result, wrapped
		}
		logicalName, exists := toolNames.logicalName(call.Function.Name)
		if !exists {
			wrapped := fmt.Errorf("openai response contained an unknown tool call")
			logResult(wrapped)
			return result, wrapped
		}
		var arguments map[string]interface{}
		if err := json.Unmarshal([]byte(call.Function.Arguments), &arguments); err != nil || arguments == nil {
			wrapped := fmt.Errorf("decode openai tool call arguments: invalid object")
			logResult(wrapped)
			return result, wrapped
		}
		toolCalls = append(toolCalls, domain.ToolCall{
			ID: call.ID, Name: logicalName, Arguments: arguments,
		})
	}
	content := decoded.Choices[0].Message.Content
	if strings.TrimSpace(content) != "" && len(toolCalls) > 0 {
		wrapped := fmt.Errorf("openai response contained tool calls and content together")
		logResult(wrapped)
		return result, wrapped
	}
	if strings.TrimSpace(content) == "" && len(toolCalls) == 0 {
		wrapped := fmt.Errorf("openai response contained no content or tool calls")
		if result.FinishReason == "length" {
			wrapped = fmt.Errorf("openai response exhausted output token budget: %w", domain.ErrModelOutputExhausted)
		}
		logResult(wrapped)
		return result, wrapped
	}
	logResult(nil)
	result.Content = content
	result.ToolCalls = toolCalls
	return result, nil
}

func buildResponsesRequest(model string, maxTokens int, messages []chatMessage, tools []chatTool) (responsesRequest, error) {
	input := make([]responsesInputItem, 0, len(messages))
	for _, message := range messages {
		switch message.Role {
		case "tool":
			input = append(input, responsesInputItem{Type: "function_call_output", CallID: message.ToolCallID, Output: message.Content})
		case "assistant":
			if message.Content != "" {
				input = append(input, responsesInputItem{Role: message.Role, Content: message.Content})
			}
			for _, call := range message.ToolCalls {
				input = append(input, responsesInputItem{Type: "function_call", CallID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments})
			}
		case "system", "user":
			input = append(input, responsesInputItem{Role: message.Role, Content: message.Content})
		default:
			return responsesRequest{}, fmt.Errorf("encode openai responses input: invalid role")
		}
	}
	responseTools := make([]responsesTool, 0, len(tools))
	for _, tool := range tools {
		responseTools = append(responseTools, responsesTool{
			Type: "function", Name: tool.Function.Name, Description: tool.Function.Description,
			Parameters: tool.Function.Parameters,
		})
	}
	return responsesRequest{
		Model: model, MaxOutputTokens: maxTokens, Input: input, Tools: responseTools,
		Text: &responsesText{Format: responseFormat{Type: "json_object"}},
	}, nil
}

func decodeResponsesResponse(body []byte) (chatResponse, error) {
	var response responsesResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return chatResponse{}, err
	}
	choice := chatChoice{FinishReason: response.Status}
	if response.Status == "incomplete" && response.IncompleteDetails.Reason == "max_output_tokens" {
		choice.FinishReason = "length"
	}
	var content strings.Builder
	for _, output := range response.Output {
		switch output.Type {
		case "message":
			for _, item := range output.Content {
				if item.Type == "output_text" {
					content.WriteString(item.Text)
				}
			}
		case "function_call":
			callID := output.CallID
			if callID == "" {
				callID = output.ID
			}
			choice.Message.ToolCalls = append(choice.Message.ToolCalls, chatToolCall{
				ID: callID, Type: "function", Function: chatFunctionCall{Name: output.Name, Arguments: output.Arguments},
			})
		}
	}
	choice.Message.Content = content.String()
	usage := chatUsage{
		PromptTokens: response.Usage.InputTokens, CompletionTokens: response.Usage.OutputTokens,
		TotalTokens: response.Usage.TotalTokens,
	}
	usage.PromptTokensDetails.CachedTokens = response.Usage.InputTokensDetails.CachedTokens
	return chatResponse{Choices: []chatChoice{choice}, Usage: usage}, nil
}

func applyUsageMetrics(result *domain.ModelResult, usage chatUsage) {
	if result == nil {
		return
	}
	result.UsageTokensIn = usage.PromptTokens
	result.UsageTokensOut = usage.CompletionTokens
	result.UsageTokens = usage.TotalTokens
	if result.UsageTokens == 0 {
		result.UsageTokens = result.UsageTokensIn + result.UsageTokensOut
	}
	if usage.PromptCacheHitTokens != nil || usage.PromptCacheMissTokens != nil {
		result.CacheTokensReported = true
		result.CacheHitTokens = nonNegativeInt64(usage.PromptCacheHitTokens)
		result.CacheMissTokens = nonNegativeInt64(usage.PromptCacheMissTokens)
		return
	}
	if usage.PromptTokensDetails.CachedTokens != nil {
		result.CacheTokensReported = true
		result.CacheHitTokens = nonNegativeInt64(usage.PromptTokensDetails.CachedTokens)
		result.CacheMissTokens = max(usage.PromptTokens-result.CacheHitTokens, 0)
	}
}

func mergeModelResults(previous, current domain.ModelResult) domain.ModelResult {
	merged := current
	merged.ModelCalls += previous.ModelCalls
	merged.UsageTokensIn += previous.UsageTokensIn
	merged.UsageTokensOut += previous.UsageTokensOut
	merged.UsageTokens += previous.UsageTokens
	merged.UsageCostCents += previous.UsageCostCents
	merged.CacheTokensReported = previous.CacheTokensReported || current.CacheTokensReported
	merged.CacheHitTokens += previous.CacheHitTokens
	merged.CacheMissTokens += previous.CacheMissTokens
	if merged.Provider == "" {
		merged.Provider = previous.Provider
	}
	if merged.Model == "" {
		merged.Model = previous.Model
	}
	if merged.RequestBytes == 0 {
		merged.RequestBytes = previous.RequestBytes
	}
	if merged.ToolCount == 0 {
		merged.ToolCount = previous.ToolCount
	}
	if merged.ToolSchemaBytes == 0 {
		merged.ToolSchemaBytes = previous.ToolSchemaBytes
	}
	return merged
}

func nonNegativeInt64(value *int64) int64 {
	if value == nil || *value < 0 {
		return 0
	}
	return *value
}

// doChatCompletion 负责一次 chat completion 的有限重试。只有网络层错误和
// 明确的临时 HTTP 状态会重放；请求参数、鉴权和响应协议错误保持立即失败。
// 每次尝试单独记录 outbound observation，避免重试把真实 provider 失败隐藏掉。
func (c *Client) doChatCompletion(ctx context.Context, session llmSession, payload []byte, metrics domain.ModelResult) ([]byte, int, time.Duration, error) {
	endpoint := session.endpoint()
	for attempt := 1; attempt <= maxRetryAttempts; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return nil, 0, 0, fmt.Errorf("create openai request: %w", err)
		}
		httpReq.Header.Set("Authorization", "Bearer "+string(session.apiKey))
		httpReq.Header.Set("Content-Type", "application/json")

		started := time.Now()
		resp, err := c.http.Do(httpReq)
		elapsed := time.Since(started)
		if err != nil {
			wrapped := fmt.Errorf("call openai: %w", classifyOpenAITransportFailure(ctx, err))
			c.logRequest(ctx, endpoint, session.model, payload, 0, elapsed, nil, metrics, wrapped)
			if attempt == maxRetryAttempts || !retryableOpenAITransport(ctx, err) {
				return nil, 0, elapsed, wrapped
			}
			if err := waitForOpenAIRetry(ctx, attempt, ""); err != nil {
				return nil, 0, elapsed, classifyOpenAITransportFailure(ctx, err)
			}
			continue
		}

		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
		_ = resp.Body.Close()
		if readErr != nil {
			wrapped := fmt.Errorf("read openai response: %w", classifyOpenAITransportFailure(ctx, readErr))
			c.logRequest(ctx, endpoint, session.model, payload, resp.StatusCode, elapsed, body, metrics, wrapped)
			if attempt == maxRetryAttempts || !retryableOpenAITransport(ctx, readErr) {
				return nil, resp.StatusCode, elapsed, wrapped
			}
			if err := waitForOpenAIRetry(ctx, attempt, resp.Header.Get("Retry-After")); err != nil {
				return nil, resp.StatusCode, elapsed, classifyOpenAITransportFailure(ctx, err)
			}
			continue
		}
		if int64(len(body)) > maxResponseBytes {
			wrapped := fmt.Errorf("openai response exceeded %d bytes", maxResponseBytes)
			c.logRequest(ctx, endpoint, session.model, payload, resp.StatusCode, elapsed, body[:maxResponseBytes], metrics, wrapped)
			return nil, resp.StatusCode, elapsed, wrapped
		}
		if resp.StatusCode != http.StatusOK {
			wrapped := fmt.Errorf("openai returned status %d: %w", resp.StatusCode, openAIStatusFailure(resp.StatusCode))
			c.logRequest(ctx, endpoint, session.model, payload, resp.StatusCode, elapsed, body, metrics, wrapped)
			if attempt == maxRetryAttempts || !retryableOpenAIStatus(resp.StatusCode) {
				return nil, resp.StatusCode, elapsed, wrapped
			}
			if err := waitForOpenAIRetry(ctx, attempt, resp.Header.Get("Retry-After")); err != nil {
				return nil, resp.StatusCode, elapsed, classifyOpenAITransportFailure(ctx, err)
			}
			continue
		}
		return body, resp.StatusCode, elapsed, nil
	}
	return nil, 0, 0, fmt.Errorf("openai request attempts exhausted")
}

func retryableOpenAITransport(ctx context.Context, err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || ctx.Err() != nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError)
}

func retryableOpenAIStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func classifyOpenAITransportFailure(ctx context.Context, cause error) error {
	if cause == nil {
		return nil
	}
	if errors.Is(context.Cause(ctx), ErrModelTurnTimeout) {
		return &domain.ProviderRuntimeError{
			Code: "provider_timeout", Retryable: true,
			Cause: errors.Join(cause, ErrModelTurnTimeout),
		}
	}
	if ctx.Err() != nil || !retryableOpenAITransport(ctx, cause) {
		return cause
	}
	var networkFailure net.Error
	if errors.As(cause, &networkFailure) && networkFailure.Timeout() {
		return &domain.ProviderRuntimeError{Code: "provider_timeout", Retryable: true, Cause: cause}
	}
	return &domain.ProviderRuntimeError{Code: "provider_transport", Retryable: true, Cause: cause}
}

func openAIStatusFailure(status int) *domain.ProviderRuntimeError {
	code := "provider_http_4xx"
	retryable := false
	switch status {
	case http.StatusTooManyRequests:
		code, retryable = "provider_rate_limit", true
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		code, retryable = "provider_timeout", true
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable:
		code, retryable = "provider_http_5xx", true
	}
	return &domain.ProviderRuntimeError{Code: code, Retryable: retryable}
}

func waitForOpenAIRetry(ctx context.Context, attempt int, retryAfter string) error {
	delay, fromHeader := retryAfterDelay(retryAfter)
	if !fromHeader {
		delay = retryBackoff(attempt)
	}
	if delay > maxRetryBackoff {
		delay = maxRetryBackoff
	}
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		cause := context.Cause(ctx)
		if cause == nil {
			cause = ctx.Err()
		}
		return fmt.Errorf("wait before retrying openai request: %w", cause)
	case <-timer.C:
		return nil
	}
}

func retryAfterDelay(value string) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second, true
	}
	if when, err := http.ParseTime(value); err == nil {
		delay := time.Until(when)
		if delay < 0 {
			delay = 0
		}
		return delay, true
	}
	return 0, false
}

func retryBackoff(attempt int) time.Duration {
	delay := initialRetryBackoff
	for index := 1; index < attempt && delay < maxRetryBackoff; index++ {
		delay *= 2
	}
	if delay > maxRetryBackoff {
		delay = maxRetryBackoff
	}
	if delay <= time.Millisecond {
		return delay
	}
	// Full jitter reduces synchronized retry bursts while retaining a bounded
	// delay. Retry-After is handled separately and is not randomized.
	return delay/2 + time.Duration(rand.Int63n(int64(delay/2)+1))
}

func buildChatMessages(req domain.ModelTurn, toolNames providerToolNames) ([]chatMessage, error) {
	messages := make([]chatMessage, 0, len(req.Messages)+2)
	messages = append(messages, chatMessage{Role: "system", Content: req.SystemPrompt})
	for _, message := range req.Messages {
		converted := chatMessage{
			Role:       message.Role,
			Content:    message.Content,
			ToolCallID: message.ToolCallID,
		}
		for _, call := range message.ToolCalls {
			providerName := toolNames.providerName(call.Name)
			if providerName == "" {
				return nil, fmt.Errorf("tool call %q has no valid name", call.Name)
			}
			arguments, err := json.Marshal(call.Arguments)
			if err != nil {
				return nil, fmt.Errorf("encode tool call %q: %w", call.Name, err)
			}
			converted.ToolCalls = append(converted.ToolCalls, chatToolCall{
				ID: call.ID, Type: "function",
				Function: chatFunctionCall{Name: providerName, Arguments: string(arguments)},
			})
		}
		messages = append(messages, converted)
	}
	userMessage := req.Continuation
	if userMessage == "" {
		userMessage = req.UserMessage
	}
	messages = append(messages, chatMessage{Role: "user", Content: userMessage})
	return messages, nil
}

func buildChatTools(definitions []domain.ToolDefinition) ([]chatTool, providerToolNames, int64, error) {
	toolNames := newProviderToolNames()
	if len(definitions) == 0 {
		return nil, toolNames, 0, nil
	}
	if len(definitions) > maxToolCount {
		return nil, toolNames, 0, fmt.Errorf("too many tools")
	}
	tools := make([]chatTool, 0, len(definitions))
	seen := make(map[string]struct{}, len(definitions))
	var schemaBytes int64
	for _, definition := range definitions {
		if strings.TrimSpace(definition.Name) == "" || len(definition.Name) > maxToolNameBytes || strings.ContainsRune(definition.Name, 0) {
			return nil, toolNames, 0, fmt.Errorf("tool name is invalid")
		}
		if len(definition.Description) > maxToolDescBytes || strings.ContainsRune(definition.Description, 0) {
			return nil, toolNames, 0, fmt.Errorf("tool description exceeds bound")
		}
		if _, exists := seen[definition.Name]; exists {
			return nil, toolNames, 0, fmt.Errorf("duplicate tool name %q", definition.Name)
		}
		seen[definition.Name] = struct{}{}
		if definition.Parameters == nil {
			return nil, toolNames, 0, fmt.Errorf("tool %q has no parameter schema", definition.Name)
		}
		encoded, err := json.Marshal(definition.Parameters)
		if err != nil || len(encoded) > maxToolSchemaBytes {
			return nil, toolNames, 0, fmt.Errorf("tool %q schema exceeds bound", definition.Name)
		}
		schemaBytes += int64(len(encoded))
		if err := validateToolSchemaValue(definition.Parameters, 0); err != nil {
			return nil, toolNames, 0, fmt.Errorf("tool %q schema is invalid: %w", definition.Name, err)
		}
		providerName, err := toolNames.add(definition.Name)
		if err != nil {
			return nil, toolNames, 0, fmt.Errorf("tool %q has no provider-safe name: %w", definition.Name, err)
		}
		tools = append(tools, chatTool{
			Type: "function",
			Function: chatFunctionDefinition{
				Name: providerName, Description: definition.Description,
				Parameters: definition.Parameters,
			},
		})
	}
	return tools, toolNames, schemaBytes, nil
}

func validateToolSchemaValue(value any, depth int) error {
	if depth > maxToolSchemaDepth {
		return fmt.Errorf("schema nesting exceeds bound")
	}
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, child := range typed {
			if len(key) > maxToolNameBytes || strings.ContainsRune(key, 0) {
				return fmt.Errorf("schema key exceeds bound")
			}
			if err := validateToolSchemaValue(child, depth+1); err != nil {
				return err
			}
		}
	case []interface{}:
		for _, child := range typed {
			if err := validateToolSchemaValue(child, depth+1); err != nil {
				return err
			}
		}
	case []string:
		for _, child := range typed {
			if strings.ContainsRune(child, 0) || len(child) > maxToolDescBytes {
				return fmt.Errorf("schema string exceeds bound")
			}
		}
	case string, bool, float64, float32, int, int32, int64, uint, uint32, uint64, nil:
	default:
		return fmt.Errorf("schema contains unsupported value")
	}
	return nil
}

func (c *Client) logRequest(ctx context.Context, endpoint, model string, request []byte, status int, duration time.Duration, response []byte, metrics domain.ModelResult, err error) {
	host, path := observability.HTTPIdentity(endpoint)
	operation := "chat.completions"
	if strings.HasSuffix(path, "/responses") {
		operation = "responses"
	}
	observability.LogLLMRequest(ctx, c.logger, observability.LLMRequest{
		Operation:           operation,
		Host:                host,
		Path:                path,
		Model:               strings.TrimSpace(model),
		Status:              status,
		Duration:            duration,
		Request:             request,
		Response:            response,
		RequestBytes:        metrics.RequestBytes,
		ToolCount:           metrics.ToolCount,
		ToolSchemaBytes:     metrics.ToolSchemaBytes,
		CacheTokensReported: metrics.CacheTokensReported,
		CacheHitTokens:      metrics.CacheHitTokens,
		CacheMissTokens:     metrics.CacheMissTokens,
		Err:                 err,
	})
}

type llmSession struct {
	baseURL string
	model   string
	apiKey  []byte
	apiMode APIMode
}

func (s llmSession) endpoint() string {
	if s.apiMode == APIModeResponses {
		return s.baseURL + "/v1/responses"
	}
	return s.baseURL + "/v1/chat/completions"
}

func (s llmSession) close() {
	clearBytes(s.apiKey)
}

func (c *Client) resolveSession(ctx context.Context, projectID string) (llmSession, error) {
	if c.staticAPIKey != "" {
		baseURL := c.baseURL
		if baseURL == "" {
			baseURL = defaultBaseURL
		}
		model := c.model
		if model == "" {
			model = ModelID
		}
		apiMode := c.apiMode
		if apiMode == "" {
			apiMode = APIModeChatCompletions
		}
		if apiMode != APIModeChatCompletions && apiMode != APIModeResponses {
			return llmSession{}, fmt.Errorf("openai API mode is invalid")
		}
		key := []byte(c.staticAPIKey)
		return llmSession{baseURL: baseURL, model: model, apiKey: key, apiMode: apiMode}, nil
	}
	if strings.TrimSpace(projectID) == "" {
		return llmSession{}, fmt.Errorf("openai config requires a project id")
	}
	cfg, err := c.configs.LoadLLMProvider(ctx, projectID)
	if err != nil {
		return llmSession{}, fmt.Errorf("load llm provider: %w", err)
	}
	if strings.TrimSpace(cfg.CredentialSecretID) == "" {
		return llmSession{}, fmt.Errorf("llm credential is required")
	}
	encrypted, err := c.secrets.GetEncryptedSecret(ctx, projectID, cfg.CredentialSecretID)
	if err != nil {
		return llmSession{}, fmt.Errorf("load llm credential: %w", err)
	}
	if encrypted.ProjectID != projectID || encrypted.ID != cfg.CredentialSecretID {
		return llmSession{}, fmt.Errorf("llm credential ownership is invalid")
	}
	if encrypted.Kind != projectdomain.SecretHTTPBearer {
		return llmSession{}, fmt.Errorf("llm credential must be http_bearer")
	}
	plaintext, err := c.cipher.Decrypt(encrypted.ProjectID, encrypted.ID, encrypted.Kind, encrypted.Ciphertext, encrypted.Nonce)
	if err != nil {
		return llmSession{}, fmt.Errorf("decrypt llm credential: %w", err)
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		return llmSession{}, fmt.Errorf("llm model is required")
	}
	apiMode := cfg.APIMode
	if apiMode == "" {
		apiMode = APIModeChatCompletions
	}
	if apiMode != APIModeChatCompletions && apiMode != APIModeResponses {
		clearBytes(plaintext)
		return llmSession{}, fmt.Errorf("llm API mode is invalid")
	}
	return llmSession{baseURL: baseURL, model: model, apiKey: plaintext, apiMode: apiMode}, nil
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
