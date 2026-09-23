package openai

import (
	"encoding/json"
	"fmt"
	"strings"
)

// messagesJSONInstruction 替代 Anthropic 缺失的 json_object 模式；最终回复仍由调用方按 JSON 校验。
const messagesJSONInstruction = "When you reply without calling a tool, respond with a single JSON object only: no markdown fences and no text before or after it."

// messagesRequest 是 Anthropic Messages API 请求。system 为顶层字段，
// 采样参数不发送，因为较新的 Claude 模型拒绝非默认 temperature。
type messagesRequest struct {
	Model     string               `json:"model"`
	MaxTokens int                  `json:"max_tokens"`
	System    []messagesTextBlock  `json:"system,omitempty"`
	Messages  []messagesTurn       `json:"messages"`
	Tools     []messagesToolSchema `json:"tools,omitempty"`
}

type messagesTextBlock struct {
	Type         string                `json:"type"`
	Text         string                `json:"text"`
	CacheControl *messagesCacheControl `json:"cache_control,omitempty"`
}

type messagesCacheControl struct {
	Type string `json:"type"`
}

type messagesTurn struct {
	Role    string                 `json:"role"`
	Content []messagesContentBlock `json:"content"`
}

type messagesContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

type messagesToolSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

type messagesResponse struct {
	StopReason string `json:"stop_reason"`
	Content    []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content"`
	Usage struct {
		InputTokens              int64 `json:"input_tokens"`
		OutputTokens             int64 `json:"output_tokens"`
		CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

// buildMessagesRequest 把内部 chat 消息转换为 Anthropic Messages 请求：
// system 消息并入顶层 system，tool 结果并入 user 轮次的 tool_result 块，
// 相邻同角色轮次合并以满足 user/assistant 交替。
func buildMessagesRequest(model string, maxTokens int, messages []chatMessage, tools []chatTool) (messagesRequest, error) {
	if maxTokens <= 0 {
		maxTokens = messagesDefaultMaxTokens
	}
	systemParts := make([]string, 0, 2)
	turns := make([]messagesTurn, 0, len(messages))
	appendBlocks := func(role string, blocks ...messagesContentBlock) {
		if len(blocks) == 0 {
			return
		}
		if last := len(turns) - 1; last >= 0 && turns[last].Role == role {
			turns[last].Content = append(turns[last].Content, blocks...)
			return
		}
		turns = append(turns, messagesTurn{Role: role, Content: blocks})
	}
	for _, message := range messages {
		switch message.Role {
		case "system":
			if strings.TrimSpace(message.Content) != "" {
				systemParts = append(systemParts, message.Content)
			}
		case "user":
			if strings.TrimSpace(message.Content) != "" {
				appendBlocks("user", messagesContentBlock{Type: "text", Text: message.Content})
			}
		case "tool":
			appendBlocks("user", messagesContentBlock{Type: "tool_result", ToolUseID: message.ToolCallID, Content: message.Content})
		case "assistant":
			blocks := make([]messagesContentBlock, 0, len(message.ToolCalls)+1)
			if strings.TrimSpace(message.Content) != "" {
				blocks = append(blocks, messagesContentBlock{Type: "text", Text: message.Content})
			}
			for _, call := range message.ToolCalls {
				input := json.RawMessage(call.Function.Arguments)
				if !json.Valid(input) {
					return messagesRequest{}, fmt.Errorf("encode anthropic messages input: invalid tool arguments")
				}
				blocks = append(blocks, messagesContentBlock{Type: "tool_use", ID: call.ID, Name: call.Function.Name, Input: input})
			}
			appendBlocks("assistant", blocks...)
		default:
			return messagesRequest{}, fmt.Errorf("encode anthropic messages input: invalid role")
		}
	}
	systemParts = append(systemParts, messagesJSONInstruction)
	// system 在每个轮次中保持不变，标记缓存断点让工具定义与 system 复用 prompt cache。
	system := []messagesTextBlock{{
		Type: "text", Text: strings.Join(systemParts, "\n\n"),
		CacheControl: &messagesCacheControl{Type: "ephemeral"},
	}}
	schemas := make([]messagesToolSchema, 0, len(tools))
	for _, tool := range tools {
		schemas = append(schemas, messagesToolSchema{
			Name: tool.Function.Name, Description: tool.Function.Description,
			InputSchema: tool.Function.Parameters,
		})
	}
	return messagesRequest{Model: model, MaxTokens: maxTokens, System: system, Messages: turns, Tools: schemas}, nil
}

// decodeMessagesResponse 把 Anthropic 回复映射为内部 chatResponse。
// 调用工具时模型常先输出一段说明文字，此时丢弃文字，只保留工具调用。
func decodeMessagesResponse(body []byte) (chatResponse, error) {
	var response messagesResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return chatResponse{}, err
	}
	choice := chatChoice{FinishReason: response.StopReason}
	switch response.StopReason {
	case "max_tokens":
		choice.FinishReason = "length"
	case "tool_use":
		choice.FinishReason = "tool_calls"
	case "end_turn", "stop_sequence":
		choice.FinishReason = "stop"
	}
	var content strings.Builder
	for _, block := range response.Content {
		switch block.Type {
		case "text":
			content.WriteString(block.Text)
		case "tool_use":
			arguments := string(block.Input)
			if strings.TrimSpace(arguments) == "" {
				arguments = "{}"
			}
			choice.Message.ToolCalls = append(choice.Message.ToolCalls, chatToolCall{
				ID: block.ID, Type: "function", Function: chatFunctionCall{Name: block.Name, Arguments: arguments},
			})
		}
	}
	if len(choice.Message.ToolCalls) == 0 {
		choice.Message.Content = extractJSONObject(content.String())
	}
	usage := response.Usage
	hit := usage.CacheReadInputTokens
	miss := usage.InputTokens + usage.CacheCreationInputTokens
	return chatResponse{Choices: []chatChoice{choice}, Usage: chatUsage{
		PromptTokens: hit + miss, CompletionTokens: usage.OutputTokens,
		TotalTokens:          hit + miss + usage.OutputTokens,
		PromptCacheHitTokens: &hit, PromptCacheMissTokens: &miss,
	}}, nil
}

// extractJSONObject 去掉 markdown 代码围栏或前后说明文字；无法取出合法 JSON 对象时原样返回。
func extractJSONObject(text string) string {
	trimmed := strings.TrimSpace(text)
	if json.Valid([]byte(trimmed)) {
		return trimmed
	}
	start, end := strings.Index(trimmed, "{"), strings.LastIndex(trimmed, "}")
	if start < 0 || end <= start {
		return text
	}
	candidate := trimmed[start : end+1]
	if !json.Valid([]byte(candidate)) {
		return text
	}
	return candidate
}
