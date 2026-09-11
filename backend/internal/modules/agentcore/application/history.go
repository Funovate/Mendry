package application

import (
	"encoding/json"
	"errors"

	"mendry/backend/internal/modules/agentcore/domain"
)

// HistoryBounds 限制 provider-native history 的条目数、总编码字节和单条文本。
type HistoryBounds struct {
	MaxItems        int
	MaxBytes        int
	MaxMessageBytes int
}

// DefaultHistoryBounds 返回适合通用 runner 的保守 history 边界。
func DefaultHistoryBounds() HistoryBounds {
	return HistoryBounds{MaxItems: 32, MaxBytes: 256 << 10, MaxMessageBytes: 64 << 10}
}

// SelectPairedHistory 深拷贝并保留最新完整 user-led groups；不返回孤立 tool result 或未完成 tool call。
func SelectPairedHistory(messages []domain.ModelMessage, bounds HistoryBounds) []domain.ModelMessage {
	bounds = normalizeHistoryBounds(bounds)
	groups := make([][]domain.ModelMessage, 0, len(messages)/2+1)
	for _, message := range messages {
		if message.Role == "user" {
			groups = append(groups, nil)
		}
		if len(groups) == 0 {
			continue
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], cloneMessage(message, bounds.MaxMessageBytes))
	}

	var selected []domain.ModelMessage
	for index := len(groups) - 1; index >= 0; index-- {
		group := groups[index]
		if !completeGroup(group) {
			continue
		}
		candidate := make([]domain.ModelMessage, 0, len(group)+len(selected))
		candidate = append(candidate, group...)
		candidate = append(candidate, selected...)
		if len(candidate) > bounds.MaxItems || encodedBytes(candidate) > bounds.MaxBytes {
			break
		}
		selected = candidate
	}
	return selected
}

// PairedHistory 在内存中维护只允许完整 tool pairing 的 provider-neutral history。
type PairedHistory struct {
	messages []domain.ModelMessage
	bounds   HistoryBounds
}

// NewPairedHistory 从 durable messages 建立 copy-safe history。
func NewPairedHistory(messages []domain.ModelMessage, bounds HistoryBounds) *PairedHistory {
	bounds = normalizeHistoryBounds(bounds)
	return &PairedHistory{messages: SelectPairedHistory(messages, bounds), bounds: bounds}
}

// Snapshot 返回不会共享 map/slice backing storage 的有界完整历史。
func (h *PairedHistory) Snapshot() []domain.ModelMessage {
	if h == nil {
		return nil
	}
	return SelectPairedHistory(h.messages, h.bounds)
}

// CommitModelTurn 仅在 profile 接受 provider 结果后追加 user/assistant 组。
func (h *PairedHistory) CommitModelTurn(user string, result domain.ModelResult) error {
	if h == nil {
		return errors.New("history is nil")
	}
	if result.Content != "" && len(result.ToolCalls) > 0 {
		return errors.New("model content and tool calls cannot be mixed")
	}
	group := []domain.ModelMessage{{Role: "user", Content: boundedString(user, h.bounds.MaxMessageBytes)}}
	if len(result.ToolCalls) > 0 {
		seen := make(map[string]struct{}, len(result.ToolCalls))
		calls := make([]domain.ToolCall, len(result.ToolCalls))
		for index, call := range result.ToolCalls {
			if call.ID == "" {
				return errors.New("tool call id is required")
			}
			if _, exists := seen[call.ID]; exists {
				return errors.New("duplicate tool call id")
			}
			seen[call.ID] = struct{}{}
			calls[index] = cloneCall(call)
		}
		group = append(group, domain.ModelMessage{Role: "assistant", ToolCalls: calls})
	} else {
		group = append(group, domain.ModelMessage{Role: "assistant", Content: boundedString(result.Content, h.bounds.MaxMessageBytes)})
	}
	h.messages = append(h.messages, group...)
	return nil
}

// AppendToolResult 只接受当前最后 assistant group 中尚未配对的 call ID。
func (h *PairedHistory) AppendToolResult(callID, content string) error {
	if h == nil || callID == "" {
		return errors.New("tool call id is required")
	}
	pending := pendingCallIDs(h.messages)
	if _, ok := pending[callID]; !ok {
		return errors.New("tool result has no pending call")
	}
	h.messages = append(h.messages, domain.ModelMessage{Role: "tool", ToolCallID: callID, Content: boundedString(content, h.bounds.MaxMessageBytes)})
	return nil
}

func normalizeHistoryBounds(bounds HistoryBounds) HistoryBounds {
	defaults := DefaultHistoryBounds()
	if bounds.MaxItems <= 0 {
		bounds.MaxItems = defaults.MaxItems
	}
	if bounds.MaxBytes <= 0 {
		bounds.MaxBytes = defaults.MaxBytes
	}
	if bounds.MaxMessageBytes <= 0 {
		bounds.MaxMessageBytes = defaults.MaxMessageBytes
	}
	return bounds
}

func completeGroup(group []domain.ModelMessage) bool {
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

func pendingCallIDs(messages []domain.ModelMessage) map[string]struct{} {
	pending := make(map[string]struct{})
	for _, message := range messages {
		if message.Role == "user" {
			clear(pending)
		}
		if message.Role == "assistant" {
			for _, call := range message.ToolCalls {
				pending[call.ID] = struct{}{}
			}
		}
		if message.Role == "tool" {
			delete(pending, message.ToolCallID)
		}
	}
	return pending
}

func cloneMessage(message domain.ModelMessage, maxBytes int) domain.ModelMessage {
	cloned := domain.ModelMessage{Role: message.Role, Content: boundedString(message.Content, maxBytes), ToolCallID: message.ToolCallID}
	if len(message.ToolCalls) > 0 {
		cloned.ToolCalls = make([]domain.ToolCall, len(message.ToolCalls))
		for index, call := range message.ToolCalls {
			cloned.ToolCalls[index] = cloneCall(call)
		}
	}
	return cloned
}

func cloneCall(call domain.ToolCall) domain.ToolCall {
	return domain.ToolCall{ID: call.ID, Name: call.Name, Version: call.Version, Arguments: cloneMap(call.Arguments)}
}

func cloneMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return map[string]any{}
	}
	var cloned map[string]any
	if json.Unmarshal(encoded, &cloned) != nil {
		return map[string]any{}
	}
	return cloned
}

// EncodedHistoryBytes 返回 provider-neutral messages 的 JSON 编码字节；编码失败返回最大 int。
func EncodedHistoryBytes(messages []domain.ModelMessage) int {
	return encodedBytes(messages)
}

func encodedBytes(messages []domain.ModelMessage) int {
	encoded, err := json.Marshal(messages)
	if err != nil {
		return int(^uint(0) >> 1)
	}
	return len(encoded)
}

func boundedString(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit]
}
