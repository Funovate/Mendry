package openai

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	coreapplication "mendry/backend/internal/modules/agentcore/application"
	"mendry/backend/internal/modules/agentcore/domain"
	"mendry/backend/internal/platform/observability"
)

const (
	// ModelID 是 local 默认使用的 gpt-5.6 模型标识。
	ModelID = "gpt-5.6"

	defaultBaseURL       = "https://api.openai.com"
	defaultTimeout       = 5 * time.Minute
	defaultMaxTokens     = 8192
	outputRetryMaxTokens = 16384
	maxRetryAttempts     = 3
	maxResponseBytes     = 1 << 20
	maxToolCount         = 128
	maxToolNameBytes     = 256
	maxProviderNameBytes = 64
	maxDescriptionBytes  = 4096
	maxSchemaBytes       = 64 << 10
	maxSchemaDepth       = 8
	maxArgumentBytes     = 64 << 10
	maxMessageBytes      = 64 << 10
	maxHistoryItems      = 256
	maxHistoryBytes      = 256 << 10
	anthropicVersion     = "2023-06-01"
	statusOverloaded     = 529
	initialBackoff       = 250 * time.Millisecond
	maxBackoff           = 2 * time.Second
)

// ErrModelTurnTimeout 表示一个完整逻辑模型轮次耗尽共享时限。
var ErrModelTurnTimeout = fmt.Errorf("openai model turn timed out: %w", context.DeadlineExceeded)

// ErrModelOutputExhausted 表示 provider 在产生有效内容或 tool call 前耗尽输出预算。
var ErrModelOutputExhausted = errors.New("model output token budget exhausted")

// ResponseFormat 声明兼容 provider 的 response_format 能力。
type ResponseFormat string

const (
	ResponseFormatNone       ResponseFormat = "none"
	ResponseFormatJSONObject ResponseFormat = "json_object"
)

type APIMode string

const (
	APIModeChatCompletions APIMode = "chat_completions"
	APIModeResponses       APIMode = "responses"
	// APIModeMessages 使用 Anthropic Messages API（/v1/messages）。
	APIModeMessages APIMode = "messages"
)

// Binding 是 composition 注入的可信 provider 连接信息。
// APIKey 由 adapter 在使用后清零；调用方不得复用或修改该 backing array。
type Binding struct {
	BaseURL        string
	Model          string
	APIKey         []byte
	Headers        map[string]string
	ResponseFormat ResponseFormat
	APIMode        APIMode
}

// BindingLoader 根据 opaque binding ID 返回一次调用所需的可信绑定。
// loader 可以在每个逻辑轮次重新解析配置，但不得从 model/event 输入取凭据。
type BindingLoader interface {
	LoadBinding(context.Context, string) (Binding, error)
}

// StaticBindingLoader 让 local composition 注入一个 copy-safe 固定 binding。
type StaticBindingLoader struct {
	Binding Binding
}

// LoadBinding 返回固定 binding 的深拷贝；binding ID 只用于选择，不会改变凭据。
func (l StaticBindingLoader) LoadBinding(_ context.Context, _ string) (Binding, error) {
	return cloneBinding(l.Binding), nil
}

// Options 构造 neutral OpenAI-compatible adapter。
type Options struct {
	Bindings         BindingLoader
	HTTPClient       *http.Client
	Logger           *slog.Logger
	Timeout          time.Duration
	MaxResponseBytes int64
}

// Client 用净 HTTP 调用 Chat Completions，不导出 provider SDK 类型。
type Client struct {
	bindings         BindingLoader
	http             *http.Client
	logger           *slog.Logger
	timeout          time.Duration
	maxResponseBytes int64
}

// NewClient 验证依赖并构造无业务耦合的 LLM adapter。
func NewClient(options Options) (*Client, error) {
	if options.Bindings == nil {
		return nil, errors.New("openai binding loader is required")
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{}
	} else {
		// 逻辑轮次 context 是总时限的唯一所有者；复制后清除 Client.Timeout，
		// 避免每个 retry 获得一份独立 timeout。
		clone := *client
		clone.Timeout = 0
		client = &clone
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	if timeout > 20*time.Minute {
		return nil, errors.New("openai timeout exceeds the maximum")
	}
	maxBody := options.MaxResponseBytes
	if maxBody <= 0 {
		maxBody = maxResponseBytes
	}
	if maxBody > maxResponseBytes {
		return nil, errors.New("openai response bound exceeds the maximum")
	}
	return &Client{bindings: options.Bindings, http: client, logger: options.Logger, timeout: timeout, maxResponseBytes: maxBody}, nil
}

var _ domain.ModelProvider = (*Client)(nil)

// ProviderRuntimeError 是不泄漏 response/key 的稳定 provider failure 分类。
type ProviderRuntimeError struct {
	Code      string
	Retryable bool
	Cause     error
}

// Error 返回稳定分类，不拼接 provider 原始消息。
func (e *ProviderRuntimeError) Error() string {
	if e == nil || e.Code == "" {
		return "provider runtime failure"
	}
	return e.Code
}

// Unwrap 保留 context 和底层错误的判定能力，但调用方不应展示 Cause 文本。
func (e *ProviderRuntimeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

type chatRequest struct {
	Model          string          `json:"model"`
	Temperature    float64         `json:"temperature,omitempty"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
	Messages       []chatMessage   `json:"messages"`
	Tools          []chatTool      `json:"tools,omitempty"`
}

type responseFormat struct {
	Type string `json:"type"`
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
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
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
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
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
	PromptTokens        int64  `json:"prompt_tokens"`
	CompletionTokens    int64  `json:"completion_tokens"`
	TotalTokens         int64  `json:"total_tokens"`
	PromptCacheHit      *int64 `json:"prompt_cache_hit_tokens"`
	PromptCacheMiss     *int64 `json:"prompt_cache_miss_tokens"`
	PromptTokensDetails struct {
		CachedTokens *int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

type providerToolNames struct {
	logicalToProvider map[string]string
	providerToLogical map[string]string
	logicalToVersion  map[string]string
	providerToVersion map[string]string
}

func newProviderToolNames() providerToolNames {
	return providerToolNames{
		logicalToProvider: make(map[string]string),
		providerToLogical: make(map[string]string),
		logicalToVersion:  make(map[string]string),
		providerToVersion: make(map[string]string),
	}
}

func (names *providerToolNames) add(logical string) (string, error) {
	if existing, ok := names.logicalToProvider[logical]; ok {
		return existing, nil
	}
	base := providerToolNameBase(logical)
	if base == "" {
		return "", errors.New("logical tool name is empty")
	}
	for suffix := 0; ; suffix++ {
		candidate := base
		if suffix > 0 {
			tail := "_" + strconv.Itoa(suffix+1)
			prefixBytes := maxProviderNameBytes - len(tail)
			if prefixBytes <= 0 {
				return "", errors.New("provider tool name is too long")
			}
			candidate = truncateASCII(base, prefixBytes) + tail
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

func (names providerToolNames) logicalName(provider string) (string, bool) {
	value, ok := names.providerToLogical[provider]
	return value, ok
}

func providerToolNameBase(logical string) string {
	var builder strings.Builder
	for _, r := range logical {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			builder.WriteRune(r)
		} else {
			builder.WriteByte('_')
		}
	}
	return truncateASCII(builder.String(), maxProviderNameBytes)
}

func validVersion(value string) bool {
	if len(value) < 2 || value[0] != 'v' || value[1] == '0' {
		return false
	}
	for _, runeValue := range value[1:] {
		if runeValue < '0' || runeValue > '9' {
			return false
		}
	}
	return true
}

func validProviderName(value string) bool {
	if value == "" || len(value) > maxProviderNameBytes {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// Complete 把 ModelTurn 映射为一次有界 Chat Completions 逻辑轮次。
func (c *Client) Complete(ctx context.Context, turn domain.ModelTurn) (domain.ModelResult, error) {
	if c == nil || c.bindings == nil {
		return domain.ModelResult{}, errors.New("openai client is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if !finiteNumber(turn.Temperature) || turn.Temperature < 0 || turn.Temperature > 2 {
		return domain.ModelResult{}, errors.New("model temperature is outside the supported bound")
	}
	turnContext, cancel := context.WithTimeoutCause(ctx, c.timeout, ErrModelTurnTimeout)
	defer cancel()

	binding, err := c.bindings.LoadBinding(turnContext, turn.Binding)
	if err != nil {
		return domain.ModelResult{}, fmt.Errorf("load openai binding: %w", err)
	}
	binding, err = normalizeBinding(binding)
	if err != nil {
		return domain.ModelResult{}, fmt.Errorf("validate openai binding: %w", err)
	}
	defer clearBinding(&binding)

	tools, names, schemaBytes, err := buildTools(turn.Tools)
	base := domain.ModelResult{Provider: binding.provider(), Model: binding.Model, ToolCount: len(tools), ToolSchemaBytes: schemaBytes}
	if err != nil {
		return base, fmt.Errorf("encode openai tools: %w", err)
	}
	messages, err := buildMessages(turn, names)
	if err != nil {
		return base, fmt.Errorf("encode openai messages: %w", err)
	}
	maxTokens := turn.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}
	if maxTokens > outputRetryMaxTokens {
		return base, errors.New("model output token bound is too large")
	}
	var aggregate domain.ModelResult
	for outputAttempt := 0; outputAttempt < 2; outputAttempt++ {
		attemptResult, attemptErr := c.completeAttempt(turnContext, binding, turn, maxTokens, tools, names, schemaBytes, messages)
		aggregate = mergeResults(aggregate, attemptResult)
		if attemptErr == nil {
			return aggregate, nil
		}
		if !errors.Is(attemptErr, ErrModelOutputExhausted) || outputAttempt == 1 {
			return aggregate, attemptErr
		}
		maxTokens = outputRetryMaxTokens
	}
	return aggregate, ErrModelOutputExhausted
}

func (c *Client) completeAttempt(ctx context.Context, binding Binding, turn domain.ModelTurn, maxTokens int, tools []chatTool, names providerToolNames, schemaBytes int64, messages []chatMessage) (domain.ModelResult, error) {
	result := domain.ModelResult{Provider: binding.provider(), Model: binding.Model, ModelCalls: 1, ToolCount: len(tools), ToolSchemaBytes: schemaBytes}
	request := chatRequest{Model: binding.Model, Temperature: turn.Temperature, MaxTokens: maxTokens, Messages: messages, Tools: tools}
	if binding.ResponseFormat == ResponseFormatJSONObject {
		request.ResponseFormat = &responseFormat{Type: string(ResponseFormatJSONObject)}
	}
	var requestPayload any = request
	if binding.APIMode == APIModeResponses {
		responses, buildErr := buildResponsesRequest(binding.Model, maxTokens, messages, tools, binding.ResponseFormat)
		if buildErr != nil {
			return result, buildErr
		}
		requestPayload = responses
	}
	if binding.APIMode == APIModeMessages {
		anthropic, buildErr := buildMessagesRequest(binding.Model, maxTokens, messages, tools, binding.ResponseFormat)
		if buildErr != nil {
			return result, buildErr
		}
		requestPayload = anthropic
	}
	payload, err := json.Marshal(requestPayload)
	if err != nil {
		return result, fmt.Errorf("encode openai request: %w", err)
	}
	result.RequestBytes = int64(len(payload))
	body, status, elapsed, err := c.doRequest(ctx, binding, payload, result)
	if err != nil {
		return result, err
	}
	var decoded chatResponse
	switch binding.APIMode {
	case APIModeResponses:
		decoded, err = decodeResponsesResponse(body)
	case APIModeMessages:
		decoded, err = decodeMessagesResponse(body, binding.ResponseFormat)
	default:
		err = json.Unmarshal(body, &decoded)
	}
	if err != nil {
		wrapped := fmt.Errorf("decode openai response: invalid JSON")
		c.logRequest(ctx, binding, payload, status, elapsed, body, result, wrapped)
		return result, wrapped
	}
	applyUsage(&result, decoded.Usage)
	if len(decoded.Choices) == 0 {
		wrapped := errors.New("openai response contained no choices")
		c.logRequest(ctx, binding, payload, status, elapsed, body, result, wrapped)
		return result, wrapped
	}
	choice := decoded.Choices[0]
	result.FinishReason = choice.FinishReason
	calls, err := decodeToolCalls(choice.Message.ToolCalls, names)
	if err != nil {
		c.logRequest(ctx, binding, payload, status, elapsed, body, result, err)
		return result, err
	}
	if strings.TrimSpace(choice.Message.Content) != "" && len(calls) > 0 {
		wrapped := errors.New("openai response contained tool calls and content together")
		c.logRequest(ctx, binding, payload, status, elapsed, body, result, wrapped)
		return result, wrapped
	}
	if strings.TrimSpace(choice.Message.Content) == "" && len(calls) == 0 {
		var wrapped error
		if choice.FinishReason == "length" {
			wrapped = fmt.Errorf("openai response exhausted output token budget: %w", ErrModelOutputExhausted)
		} else {
			wrapped = errors.New("openai response contained no content or tool calls")
		}
		c.logRequest(ctx, binding, payload, status, elapsed, body, result, wrapped)
		return result, wrapped
	}
	result.Content = choice.Message.Content
	result.ToolCalls = calls
	result.OutputBytes = int64(len(result.Content))
	for _, call := range calls {
		arguments, _ := json.Marshal(call.Arguments)
		result.OutputBytes += int64(len(call.ID) + len(call.Name) + len(call.Version) + len(arguments))
	}
	c.logRequest(ctx, binding, payload, status, elapsed, body, result, nil)
	return result, nil
}

func (c *Client) doRequest(ctx context.Context, binding Binding, payload []byte, metrics domain.ModelResult) ([]byte, int, time.Duration, error) {
	endpoint := binding.endpoint()
	var lastDuration time.Duration
	for attempt := 1; attempt <= maxRetryAttempts; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return nil, 0, 0, fmt.Errorf("create openai request: %w", err)
		}
		for name, value := range binding.Headers {
			request.Header.Set(name, value)
		}
		binding.setAuth(request)
		request.Header.Set("Content-Type", "application/json")
		started := time.Now()
		response, err := c.http.Do(request)
		lastDuration = time.Since(started)
		if err != nil {
			classified := classifyTransport(ctx, err)
			c.logRequest(ctx, binding, payload, 0, lastDuration, nil, metrics, classified)
			if attempt == maxRetryAttempts || !retryableTransport(ctx, err) {
				return nil, 0, lastDuration, classified
			}
			if err := waitRetry(ctx, attempt, ""); err != nil {
				return nil, 0, lastDuration, classifyTransport(ctx, err)
			}
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, c.maxResponseBytes+1))
		_ = response.Body.Close()
		if readErr != nil {
			classified := classifyTransport(ctx, readErr)
			c.logRequest(ctx, binding, payload, response.StatusCode, lastDuration, body, metrics, classified)
			if attempt == maxRetryAttempts || !retryableTransport(ctx, readErr) {
				return nil, response.StatusCode, lastDuration, classified
			}
			if err := waitRetry(ctx, attempt, response.Header.Get("Retry-After")); err != nil {
				return nil, response.StatusCode, lastDuration, classifyTransport(ctx, err)
			}
			continue
		}
		if int64(len(body)) > c.maxResponseBytes {
			err := errors.New("openai response exceeded the configured bound")
			c.logRequest(ctx, binding, payload, response.StatusCode, lastDuration, body[:c.maxResponseBytes], metrics, err)
			return nil, response.StatusCode, lastDuration, err
		}
		if response.StatusCode != http.StatusOK {
			err := &ProviderRuntimeError{Code: providerStatusCode(response.StatusCode), Retryable: retryableStatus(response.StatusCode)}
			c.logRequest(ctx, binding, payload, response.StatusCode, lastDuration, body, metrics, err)
			if attempt == maxRetryAttempts || !retryableStatus(response.StatusCode) {
				return nil, response.StatusCode, lastDuration, err
			}
			if err := waitRetry(ctx, attempt, response.Header.Get("Retry-After")); err != nil {
				return nil, response.StatusCode, lastDuration, classifyTransport(ctx, err)
			}
			continue
		}
		return body, response.StatusCode, lastDuration, nil
	}
	return nil, 0, lastDuration, errors.New("openai request attempts exhausted")
}

func buildResponsesRequest(model string, maxTokens int, messages []chatMessage, tools []chatTool, format ResponseFormat) (responsesRequest, error) {
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
			return responsesRequest{}, errors.New("encode openai responses input: invalid role")
		}
	}
	responseTools := make([]responsesTool, 0, len(tools))
	for _, tool := range tools {
		responseTools = append(responseTools, responsesTool{
			Type: "function", Name: tool.Function.Name, Description: tool.Function.Description,
			Parameters: tool.Function.Parameters,
		})
	}
	request := responsesRequest{Model: model, MaxOutputTokens: maxTokens, Input: input, Tools: responseTools}
	if format == ResponseFormatJSONObject {
		request.Text = &responsesText{Format: responseFormat{Type: string(ResponseFormatJSONObject)}}
	}
	return request, nil
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

func buildMessages(turn domain.ModelTurn, names providerToolNames) ([]chatMessage, error) {
	history := coreapplication.SelectPairedHistory(turn.Messages, coreapplication.HistoryBounds{
		MaxItems: maxHistoryItems, MaxBytes: maxHistoryBytes, MaxMessageBytes: maxMessageBytes,
	})
	messages := make([]chatMessage, 0, len(history)+2)
	if strings.TrimSpace(turn.SystemPrompt) != "" {
		messages = append(messages, chatMessage{Role: "system", Content: boundedUTF8(turn.SystemPrompt, maxMessageBytes)})
	}
	for _, message := range history {
		if message.Role != "user" && message.Role != "assistant" && message.Role != "tool" && message.Role != "system" {
			return nil, errors.New("provider history contains an invalid role")
		}
		converted := chatMessage{Role: message.Role, Content: boundedUTF8(message.Content, maxMessageBytes), ToolCallID: boundedUTF8(message.ToolCallID, maxProviderNameBytes)}
		for _, call := range message.ToolCalls {
			providerName, ok := names.logicalToProvider[call.Name]
			if !ok {
				return nil, errors.New("provider history contains an unknown tool name")
			}
			if version := names.logicalToVersion[call.Name]; call.Version != "" && call.Version != version {
				return nil, errors.New("provider history contains a mismatched tool version")
			}
			args, err := json.Marshal(call.Arguments)
			if err != nil || len(args) > maxArgumentBytes {
				return nil, errors.New("provider history tool arguments exceed the bound")
			}
			converted.ToolCalls = append(converted.ToolCalls, chatToolCall{ID: boundedUTF8(call.ID, maxProviderNameBytes), Type: "function", Function: chatFunctionCall{Name: providerName, Arguments: string(args)}})
		}
		if message.Role == "tool" && message.ToolCallID == "" {
			return nil, errors.New("provider history tool result id is required")
		}
		messages = append(messages, converted)
	}
	user := turn.Continuation
	if user == "" {
		user = turn.UserMessage
	}
	if strings.TrimSpace(user) == "" {
		return nil, errors.New("openai user message is required")
	}
	messages = append(messages, chatMessage{Role: "user", Content: boundedUTF8(user, maxMessageBytes)})
	return messages, nil
}

func selectHistory(messages []domain.ModelMessage) []domain.ModelMessage {
	// The core runner normally supplies paired history. This second bound protects
	// callers that use the neutral adapter directly and never emits a partial group.
	groups := make([][]domain.ModelMessage, 0, len(messages)/2+1)
	for _, message := range messages {
		if message.Role == "user" {
			groups = append(groups, nil)
		}
		if len(groups) > 0 {
			groups[len(groups)-1] = append(groups[len(groups)-1], message)
		}
	}
	var selected []domain.ModelMessage
	for index := len(groups) - 1; index >= 0; index-- {
		group := groups[index]
		if !completeHistoryGroup(group) {
			continue
		}
		candidate := append(append([]domain.ModelMessage(nil), group...), selected...)
		if len(candidate) > maxHistoryItems || encodedHistoryBytes(candidate) > maxHistoryBytes {
			break
		}
		selected = candidate
	}
	return selected
}

func completeHistoryGroup(group []domain.ModelMessage) bool {
	if len(group) < 2 || group[0].Role != "user" {
		return false
	}
	pending := make(map[string]struct{})
	for _, message := range group {
		switch message.Role {
		case "assistant":
			for _, call := range message.ToolCalls {
				if call.ID == "" {
					return false
				}
				if _, exists := pending[call.ID]; exists {
					return false
				}
				pending[call.ID] = struct{}{}
			}
		case "tool":
			if _, exists := pending[message.ToolCallID]; !exists {
				return false
			}
			delete(pending, message.ToolCallID)
		}
	}
	return len(pending) == 0
}

func encodedHistoryBytes(messages []domain.ModelMessage) int {
	encoded, err := json.Marshal(messages)
	if err != nil {
		return maxHistoryBytes + 1
	}
	return len(encoded)
}

func buildTools(definitions []domain.ToolDefinition) ([]chatTool, providerToolNames, int64, error) {
	names := newProviderToolNames()
	if len(definitions) > maxToolCount {
		return nil, names, 0, errors.New("too many tools")
	}
	if len(definitions) == 0 {
		return nil, names, 0, nil
	}
	tools := make([]chatTool, 0, len(definitions))
	seen := make(map[string]struct{}, len(definitions))
	var schemaBytes int64
	for _, definition := range definitions {
		if strings.TrimSpace(definition.Name) == "" || len(definition.Name) > maxToolNameBytes || strings.ContainsRune(definition.Name, 0) {
			return nil, names, 0, errors.New("tool name is invalid")
		}
		if !validVersion(definition.Version) || len(definition.Version) > maxProviderNameBytes {
			return nil, names, 0, errors.New("tool version is invalid")
		}
		if _, exists := seen[definition.Name]; exists {
			return nil, names, 0, errors.New("duplicate tool name")
		}
		seen[definition.Name] = struct{}{}
		if len(definition.Description) > maxDescriptionBytes || !utf8.ValidString(definition.Description) || strings.ContainsRune(definition.Description, 0) {
			return nil, names, 0, errors.New("tool description exceeds bound")
		}
		schema, err := normalizeSchema(definition.Parameters)
		if err != nil {
			return nil, names, 0, fmt.Errorf("tool schema is invalid: %w", err)
		}
		encoded, err := json.Marshal(schema)
		if err != nil || len(encoded) > maxSchemaBytes {
			return nil, names, 0, errors.New("tool schema exceeds bound")
		}
		schemaBytes += int64(len(encoded))
		providerName, err := names.add(definition.Name)
		if err != nil {
			return nil, names, 0, err
		}
		names.logicalToVersion[definition.Name] = definition.Version
		names.providerToVersion[providerName] = definition.Version
		tools = append(tools, chatTool{Type: "function", Function: chatFunctionDefinition{Name: providerName, Description: definition.Description, Parameters: schema}})
	}
	return tools, names, schemaBytes, nil
}

func normalizeSchema(schema map[string]any) (map[string]any, error) {
	if schema == nil {
		return nil, errors.New("parameter schema is required")
	}
	copyValue, err := cloneJSON(schema)
	if err != nil {
		return nil, err
	}
	result, ok := copyValue.(map[string]any)
	if !ok || result == nil {
		return nil, errors.New("parameter schema must be an object")
	}
	if result["type"] != "object" {
		return nil, errors.New("parameter schema root must be an object")
	}
	if err := normalizeSchemaTree(result, 0); err != nil {
		return nil, err
	}
	return result, nil
}

func normalizeSchemaTree(schema map[string]any, depth int) error {
	if depth > maxSchemaDepth {
		return errors.New("schema nesting exceeds bound")
	}
	typeName, ok := schema["type"].(string)
	if !ok {
		return errors.New("schema type is required")
	}
	switch typeName {
	case "object":
		if raw, exists := schema["additionalProperties"]; exists && raw != false {
			return errors.New("object schema must be closed")
		}
		schema["additionalProperties"] = false
		properties, exists := schema["properties"]
		if !exists {
			schema["properties"] = map[string]any{}
			properties = schema["properties"]
		}
		propertyMap, ok := properties.(map[string]any)
		if !ok || propertyMap == nil {
			return errors.New("object schema properties must be an object")
		}
		required, err := schemaRequiredNames(schema["required"])
		if err != nil {
			return err
		}
		for _, name := range required {
			if _, declared := propertyMap[name]; !declared {
				return errors.New("required property is not declared")
			}
		}
		for name, raw := range propertyMap {
			if name == "" || len(name) > maxToolNameBytes || !utf8.ValidString(name) || strings.ContainsRune(name, 0) {
				return errors.New("object schema property is invalid")
			}
			child, ok := raw.(map[string]any)
			if !ok || child == nil {
				return errors.New("object schema property is invalid")
			}
			if err := normalizeSchemaTree(child, depth+1); err != nil {
				return err
			}
		}
	case "array":
		child, ok := schema["items"].(map[string]any)
		if !ok || child == nil {
			return errors.New("array schema items are required")
		}
		if err := normalizeSchemaTree(child, depth+1); err != nil {
			return err
		}
	case "string":
		if err := validateSchemaIntegerBounds(schema, "minLength", "maxLength"); err != nil {
			return err
		}
	case "number", "integer":
		if err := validateSchemaNumberBounds(schema); err != nil {
			return err
		}
	case "boolean", "null":
	default:
		return errors.New("schema type is unsupported")
	}
	if typeName == "array" {
		return validateSchemaIntegerBounds(schema, "minItems", "maxItems")
	}
	return nil
}

func schemaRequiredNames(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	values, ok := value.([]any)
	if !ok {
		return nil, errors.New("schema required must be an array")
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		name, ok := raw.(string)
		if !ok || name == "" || len(name) > maxToolNameBytes || !utf8.ValidString(name) {
			return nil, errors.New("schema required property is invalid")
		}
		if _, exists := seen[name]; exists {
			return nil, errors.New("schema required property is duplicated")
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	return result, nil
}

func validateSchemaIntegerBounds(schema map[string]any, minimumName, maximumName string) error {
	minimum, hasMinimum := schema[minimumName]
	maximum, hasMaximum := schema[maximumName]
	if hasMinimum {
		value, ok := schemaInteger(minimum)
		if !ok || value < 0 {
			return errors.New("schema integer bound is invalid")
		}
	}
	if hasMaximum {
		value, ok := schemaInteger(maximum)
		if !ok || value < 0 {
			return errors.New("schema integer bound is invalid")
		}
		if hasMinimum {
			minimumValue, _ := schemaInteger(minimum)
			if value < minimumValue {
				return errors.New("schema integer bounds are inverted")
			}
		}
	}
	return nil
}

func validateSchemaNumberBounds(schema map[string]any) error {
	minimum, hasMinimum := schema["minimum"]
	maximum, hasMaximum := schema["maximum"]
	var minimumValue, maximumValue float64
	if hasMinimum {
		var ok bool
		minimumValue, ok = schemaNumber(minimum)
		if !ok {
			return errors.New("schema numeric bound is invalid")
		}
	}
	if hasMaximum {
		var ok bool
		maximumValue, ok = schemaNumber(maximum)
		if !ok {
			return errors.New("schema numeric bound is invalid")
		}
	}
	if hasMinimum && hasMaximum && maximumValue < minimumValue {
		return errors.New("schema numeric bounds are inverted")
	}
	return nil
}

func schemaInteger(value any) (int64, bool) {
	number, ok := schemaNumber(value)
	return int64(number), ok && number >= 0 && number == float64(int64(number))
}

func schemaNumber(value any) (float64, bool) {
	var number float64
	switch typed := value.(type) {
	case float64:
		number = typed
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		number = parsed
	case int:
		number = float64(typed)
	case int64:
		number = float64(typed)
	default:
		return 0, false
	}
	return number, finiteNumber(number)
}

func decodeToolCalls(raw []chatToolCall, names providerToolNames) ([]domain.ToolCall, error) {
	if len(raw) > maxToolCount {
		return nil, errors.New("too many tool calls")
	}
	calls := make([]domain.ToolCall, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, call := range raw {
		if call.Type != "function" || strings.TrimSpace(call.ID) == "" || len(call.ID) > maxProviderNameBytes || !utf8.ValidString(call.ID) || !validProviderName(call.Function.Name) || len(call.Function.Arguments) > maxArgumentBytes || !utf8.ValidString(call.Function.Arguments) {
			return nil, errors.New("invalid provider tool call")
		}
		if _, exists := seen[call.ID]; exists {
			return nil, errors.New("duplicate provider tool call id")
		}
		seen[call.ID] = struct{}{}
		logical, ok := names.logicalName(call.Function.Name)
		if !ok {
			return nil, errors.New("provider returned an unknown tool")
		}
		var arguments map[string]any
		if err := json.Unmarshal([]byte(call.Function.Arguments), &arguments); err != nil || arguments == nil {
			return nil, errors.New("provider tool arguments must be a JSON object")
		}
		calls = append(calls, domain.ToolCall{ID: call.ID, Name: logical, Version: names.providerToVersion[call.Function.Name], Arguments: arguments})
	}
	return calls, nil
}

func applyUsage(result *domain.ModelResult, usage chatUsage) {
	result.InputTokens = usage.PromptTokens
	result.OutputTokens = usage.CompletionTokens
	result.UsageTokens = usage.TotalTokens
	result.InputTokens = max64(result.InputTokens, 0)
	result.OutputTokens = max64(result.OutputTokens, 0)
	result.UsageTokens = max64(result.UsageTokens, 0)
	if result.UsageTokens == 0 {
		result.UsageTokens = result.InputTokens + result.OutputTokens
	}
	if result.UsageTokens < result.InputTokens+result.OutputTokens {
		result.UsageTokens = result.InputTokens + result.OutputTokens
	}
	if usage.PromptCacheHit != nil || usage.PromptCacheMiss != nil {
		result.CacheTokensReported = true
		result.CacheHitTokens = nonNegative(usage.PromptCacheHit)
		result.CacheMissTokens = nonNegative(usage.PromptCacheMiss)
		return
	}
	if usage.PromptTokensDetails.CachedTokens != nil {
		result.CacheTokensReported = true
		result.CacheHitTokens = nonNegative(usage.PromptTokensDetails.CachedTokens)
		result.CacheMissTokens = max64(usage.PromptTokens-result.CacheHitTokens, 0)
	}
}

func mergeResults(previous, current domain.ModelResult) domain.ModelResult {
	merged := current
	merged.ModelCalls += previous.ModelCalls
	merged.InputTokens += previous.InputTokens
	merged.OutputTokens += previous.OutputTokens
	merged.UsageTokens += previous.UsageTokens
	merged.UsageCostCents += previous.UsageCostCents
	merged.OutputBytes += previous.OutputBytes
	merged.RequestBytes += previous.RequestBytes
	merged.CacheTokensReported = previous.CacheTokensReported || current.CacheTokensReported
	merged.CacheHitTokens += previous.CacheHitTokens
	merged.CacheMissTokens += previous.CacheMissTokens
	if merged.Provider == "" {
		merged.Provider = previous.Provider
	}
	if merged.Model == "" {
		merged.Model = previous.Model
	}
	if merged.ToolCount == 0 {
		merged.ToolCount = previous.ToolCount
	}
	if merged.ToolSchemaBytes == 0 {
		merged.ToolSchemaBytes = previous.ToolSchemaBytes
	}
	return merged
}

func (binding Binding) endpoint() string {
	switch binding.APIMode {
	case APIModeResponses:
		return binding.BaseURL + "/v1/responses"
	case APIModeMessages:
		return binding.BaseURL + "/v1/messages"
	default:
		return binding.BaseURL + "/v1/chat/completions"
	}
}

func (binding Binding) provider() string {
	if binding.APIMode == APIModeMessages {
		return "anthropic"
	}
	return "openai"
}

// setAuth 按协议注入凭据：Anthropic 使用 x-api-key，OpenAI 兼容端点使用 Bearer。
func (binding Binding) setAuth(request *http.Request) {
	if binding.APIMode == APIModeMessages {
		request.Header.Set("x-api-key", string(binding.APIKey))
		request.Header.Set("anthropic-version", anthropicVersion)
		return
	}
	request.Header.Set("Authorization", "Bearer "+string(binding.APIKey))
}

func normalizeBinding(binding Binding) (Binding, error) {
	binding.BaseURL = strings.TrimRight(strings.TrimSpace(binding.BaseURL), "/")
	if binding.BaseURL == "" {
		binding.BaseURL = defaultBaseURL
	}
	binding.Model = strings.TrimSpace(binding.Model)
	if binding.Model == "" {
		binding.Model = ModelID
	}
	if binding.ResponseFormat == "" {
		binding.ResponseFormat = ResponseFormatNone
	}
	if binding.APIMode == "" {
		binding.APIMode = APIModeChatCompletions
	}
	binding.APIKey = append([]byte(nil), binding.APIKey...)
	if len(binding.Headers) > 0 {
		headers := make(map[string]string, len(binding.Headers))
		for name, value := range binding.Headers {
			headers[name] = value
		}
		binding.Headers = headers
	}
	if err := validateBinding(binding); err != nil {
		clearBinding(&binding)
		return Binding{}, err
	}
	return binding, nil
}

func validateBinding(binding Binding) error {
	parsed, err := url.Parse(binding.BaseURL)
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("base URL is invalid")
	}
	if len(binding.BaseURL) > 2048 || strings.ContainsAny(binding.BaseURL, "\r\n") {
		return errors.New("base URL is invalid")
	}
	if strings.TrimSpace(binding.Model) == "" || len(binding.Model) > 256 || strings.ContainsRune(binding.Model, 0) {
		return errors.New("model is invalid")
	}
	if len(binding.APIKey) == 0 || len(binding.APIKey) > 4096 || !utf8.Valid(binding.APIKey) || strings.ContainsRune(string(binding.APIKey), 0) {
		return errors.New("API key is required and valid")
	}
	if binding.ResponseFormat != "" && binding.ResponseFormat != ResponseFormatNone && binding.ResponseFormat != ResponseFormatJSONObject {
		return errors.New("response format is invalid")
	}
	if binding.APIMode != APIModeChatCompletions && binding.APIMode != APIModeResponses && binding.APIMode != APIModeMessages {
		return errors.New("API mode is invalid")
	}
	for name, value := range binding.Headers {
		if strings.TrimSpace(name) == "" || !utf8.ValidString(name) || !utf8.ValidString(value) || strings.ContainsAny(name+value, "\r\n") || len(name) > 128 || len(value) > 4096 {
			return errors.New("header binding is invalid")
		}
		if strings.EqualFold(name, "authorization") || strings.EqualFold(name, "x-api-key") || strings.EqualFold(name, "cookie") {
			return errors.New("credential headers must use APIKey")
		}
	}
	return nil
}

func cloneBinding(binding Binding) Binding {
	binding.APIKey = append([]byte(nil), binding.APIKey...)
	if len(binding.Headers) > 0 {
		headers := make(map[string]string, len(binding.Headers))
		for name, value := range binding.Headers {
			headers[name] = value
		}
		binding.Headers = headers
	}
	return binding
}

func clearBinding(binding *Binding) {
	if binding == nil {
		return
	}
	for index := range binding.APIKey {
		binding.APIKey[index] = 0
	}
	binding.APIKey = nil
	for name := range binding.Headers {
		binding.Headers[name] = ""
	}
	binding.Headers = nil
}

func (c *Client) logRequest(ctx context.Context, binding Binding, request []byte, status int, duration time.Duration, response []byte, metrics domain.ModelResult, err error) {
	host, path := observability.HTTPIdentity(binding.endpoint())
	operation := "chat.completions"
	switch binding.APIMode {
	case APIModeResponses:
		operation = "responses"
	case APIModeMessages:
		operation = "messages"
	}
	observability.LogLLMRequest(ctx, c.logger, observability.LLMRequest{Provider: binding.provider(), Operation: operation, Host: host, Path: path, Model: binding.Model, Status: status, Duration: duration, Request: request, Response: response, RequestBytes: metrics.RequestBytes, ToolCount: metrics.ToolCount, ToolSchemaBytes: metrics.ToolSchemaBytes, CacheTokensReported: metrics.CacheTokensReported, CacheHitTokens: metrics.CacheHitTokens, CacheMissTokens: metrics.CacheMissTokens, Err: err})
}

func classifyTransport(ctx context.Context, err error) error {
	if errors.Is(context.Cause(ctx), ErrModelTurnTimeout) {
		return &ProviderRuntimeError{Code: "provider_timeout", Retryable: true, Cause: ErrModelTurnTimeout}
	}
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return &ProviderRuntimeError{Code: "provider_canceled", Cause: context.Canceled}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &ProviderRuntimeError{Code: "provider_timeout", Retryable: true, Cause: context.DeadlineExceeded}
	}
	return &ProviderRuntimeError{Code: "provider_transport", Retryable: true, Cause: err}
}

func retryableTransport(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError)
}

func retryableStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout, statusOverloaded:
		return true
	default:
		return false
	}
}

func providerStatusCode(status int) string {
	switch {
	case status == http.StatusTooManyRequests:
		return "provider_rate_limit"
	case status == http.StatusRequestTimeout || status == http.StatusGatewayTimeout:
		return "provider_timeout"
	case status >= 500:
		return "provider_http_5xx"
	default:
		return "provider_http_4xx"
	}
}

func waitRetry(ctx context.Context, attempt int, retryAfter string) error {
	delay, ok := parseRetryAfter(retryAfter)
	if !ok {
		delay = initialBackoff
		for index := 1; index < attempt; index++ {
			delay *= 2
			if delay >= maxBackoff {
				delay = maxBackoff
				break
			}
		}
		if delay > time.Millisecond {
			// crypto/rand avoids global math/rand state while retaining bounded jitter.
			if extra, err := rand.Int(rand.Reader, big.NewInt(int64(delay/2)+1)); err == nil {
				delay = delay/2 + time.Duration(extra.Int64())
			}
		}
	}
	if delay > maxBackoff {
		delay = maxBackoff
	}
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func parseRetryAfter(value string) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second, true
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	delay := time.Until(when)
	if delay < 0 {
		delay = 0
	}
	return delay, true
}

func cloneJSON(value any) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var clone any
	if err := json.Unmarshal(encoded, &clone); err != nil {
		return nil, err
	}
	return clone, nil
}

func boundedUTF8(value string, max int) string {
	if len(value) <= max {
		return value
	}
	cut := max
	for cut > 0 && !utf8.ValidString(value[:cut]) {
		cut--
	}
	return value[:cut]
}

func truncateASCII(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}

func nonNegative(value *int64) int64 {
	if value == nil || *value < 0 {
		return 0
	}
	return *value
}

func max64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func finiteNumber(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
