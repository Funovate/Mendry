package openai_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"mendry/backend/internal/modules/remediation/adapter/openai"
	"mendry/backend/internal/modules/remediation/domain"
	"mendry/backend/internal/platform/observability"
)

func anthropicResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}
}

func TestCompleteSendsAnthropicMessagesAndParsesToolUse(t *testing.T) {
	client, err := openai.NewClient(openai.Options{
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Path != "/v1/messages" {
				t.Fatalf("path = %q", request.URL.Path)
			}
			if request.Header.Get("x-api-key") != testAPIKey || request.Header.Get("anthropic-version") != "2023-06-01" || request.Header.Get("Authorization") != "" {
				t.Fatalf("auth headers = %#v", request.Header)
			}
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				return nil, err
			}
			if payload["max_tokens"] != float64(128) || payload["temperature"] != nil || payload["response_format"] != nil {
				t.Fatalf("sampling fields = %#v", payload)
			}
			system := payload["system"].([]any)[0].(map[string]any)
			if !strings.HasPrefix(system["text"].(string), "sys\n\n") || !strings.Contains(system["text"].(string), "single JSON object") || system["cache_control"] == nil {
				t.Fatalf("system = %#v", system)
			}
			tool := payload["tools"].([]any)[0].(map[string]any)
			if tool["name"] != "repository_read_file" || tool["input_schema"] == nil || tool["type"] != nil {
				t.Fatalf("tool = %#v", tool)
			}
			messages := payload["messages"].([]any)
			if len(messages) != 2 {
				t.Fatalf("messages = %#v", messages)
			}
			assistant := messages[0].(map[string]any)
			toolUse := assistant["content"].([]any)[0].(map[string]any)
			if assistant["role"] != "assistant" || toolUse["type"] != "tool_use" || toolUse["id"] != "call_0" || toolUse["input"].(map[string]any)["path"] != "previous.go" {
				t.Fatalf("assistant turn = %#v", assistant)
			}
			user := messages[1].(map[string]any)
			blocks := user["content"].([]any)
			if user["role"] != "user" || len(blocks) != 2 ||
				blocks[0].(map[string]any)["type"] != "tool_result" || blocks[0].(map[string]any)["tool_use_id"] != "call_0" ||
				blocks[1].(map[string]any)["text"] != "incremental user" {
				t.Fatalf("user turn = %#v", user)
			}
			return anthropicResponse(request, http.StatusOK, `{"stop_reason":"tool_use","content":[{"type":"text","text":"Let me read it."},{"type":"tool_use","id":"toolu_1","name":"repository_read_file","input":{"path":"main.go"}}],"usage":{"input_tokens":5,"output_tokens":2,"cache_read_input_tokens":3,"cache_creation_input_tokens":1}}`), nil
		})},
		StaticAPIKey: testAPIKey,
		APIMode:      openai.APIModeMessages,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	result, err := client.Complete(context.Background(), domain.ModelTurn{
		ProjectID: testProjectID, SystemPrompt: "sys", UserMessage: "fallback", Continuation: "incremental user", MaxTokens: 128,
		Messages: []domain.ModelMessage{
			{Role: "assistant", ToolCalls: []domain.ToolCall{{ID: "call_0", Name: "repository.read_file", Arguments: map[string]any{"path": "previous.go"}}}},
			{Role: "tool", ToolCallID: "call_0", Content: "package main"},
		},
		Tools: []domain.ToolDefinition{{Name: "repository.read_file", Description: "Read a file.", Parameters: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}}},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if result.Provider != "anthropic" || result.Content != "" || result.FinishReason != "tool_calls" {
		t.Fatalf("result = %#v", result)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].ID != "toolu_1" || result.ToolCalls[0].Name != "repository.read_file" || result.ToolCalls[0].Arguments["path"] != "main.go" {
		t.Fatalf("tool calls = %#v", result.ToolCalls)
	}
	if result.UsageTokensIn != 9 || result.UsageTokensOut != 2 || result.UsageTokens != 11 || !result.CacheTokensReported || result.CacheHitTokens != 3 || result.CacheMissTokens != 6 {
		t.Fatalf("usage = %#v", result)
	}
}

func TestCompleteExtractsAnthropicJSONFromFencedText(t *testing.T) {
	client, err := openai.NewClient(openai.Options{
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return anthropicResponse(request, http.StatusOK, "{\"stop_reason\":\"end_turn\",\"content\":[{\"type\":\"text\",\"text\":\"```json\\n{\\\"schemaVersion\\\":\\\"v1\\\",\\\"kind\\\":\\\"stop\\\"}\\n```\"}],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}"), nil
		})},
		StaticAPIKey: testAPIKey,
		APIMode:      openai.APIModeMessages,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	result, err := client.Complete(context.Background(), domain.ModelTurn{ProjectID: testProjectID, UserMessage: "user"})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if result.Content != `{"schemaVersion":"v1","kind":"stop"}` || result.FinishReason != "stop" {
		t.Fatalf("content = %q finish = %q", result.Content, result.FinishReason)
	}
}

func TestCompleteMapsAnthropicMaxTokensToOutputExhausted(t *testing.T) {
	var budgets []float64
	client, err := openai.NewClient(openai.Options{
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				return nil, err
			}
			budgets = append(budgets, payload["max_tokens"].(float64))
			return anthropicResponse(request, http.StatusOK, `{"stop_reason":"max_tokens","content":[],"usage":{"input_tokens":1,"output_tokens":1}}`), nil
		})},
		StaticAPIKey: testAPIKey,
		APIMode:      openai.APIModeMessages,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	_, err = client.Complete(context.Background(), domain.ModelTurn{ProjectID: testProjectID, UserMessage: "user"})
	if !errors.Is(err, domain.ErrModelOutputExhausted) {
		t.Fatalf("Complete() error = %v, want ErrModelOutputExhausted", err)
	}
	if len(budgets) != 2 || budgets[0] != 8192 || budgets[1] != 16384 {
		t.Fatalf("max_tokens budgets = %v", budgets)
	}
}

func TestCompleteRetriesAnthropicOverloadedWithoutLeakingKey(t *testing.T) {
	attempts := 0
	var output bytes.Buffer
	logger, err := observability.NewLogger(observability.LoggerOptions{
		Writer: &output, Level: "debug", Format: "json", Service: "mendry-test", Environment: "test",
	})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	client, err := openai.NewClient(openai.Options{
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			attempts++
			if attempts == 1 {
				response := anthropicResponse(request, 529, `{"type":"error","error":{"type":"overloaded_error"}}`)
				response.Header.Set("Retry-After", "0")
				return response, nil
			}
			return anthropicResponse(request, http.StatusOK, `{"stop_reason":"end_turn","content":[{"type":"text","text":"{\"kind\":\"stop\"}"}],"usage":{"input_tokens":1,"output_tokens":1}}`), nil
		})},
		StaticAPIKey: testAPIKey,
		APIMode:      openai.APIModeMessages,
		Logger:       logger,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := client.Complete(context.Background(), domain.ModelTurn{ProjectID: testProjectID, UserMessage: "user"}); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if attempts != 2 {
		t.Fatalf("HTTP attempts = %d, want 2", attempts)
	}
	logs := output.String()
	if strings.Contains(logs, testAPIKey) {
		t.Fatal("logs leaked the API key")
	}
	if !strings.Contains(logs, `"component":"anthropic"`) || !strings.Contains(logs, `"messages"`) {
		t.Fatalf("logs missing anthropic identity: %s", logs)
	}
}
