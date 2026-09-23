package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mendry/backend/internal/modules/agentcore/domain"
)

func TestClientMapsAnthropicMessagesToolsAndUsage(t *testing.T) {
	var received messagesRequest
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/messages" {
			t.Errorf("request path = %q", request.URL.Path)
		}
		if request.Header.Get("x-api-key") != "test-secret" || request.Header.Get("anthropic-version") != anthropicVersion || request.Header.Get("Authorization") != "" {
			t.Errorf("auth headers = %#v", request.Header)
		}
		if err := json.NewDecoder(request.Body).Decode(&received); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		writeOpenAIJSON(t, writer, map[string]any{
			"stop_reason": "tool_use",
			"content": []map[string]any{
				{"type": "text", "text": "Reading the file."},
				{"type": "tool_use", "id": "toolu-1", "name": "workspace_read", "input": map[string]any{"path": "main.go"}},
			},
			"usage": map[string]any{
				"input_tokens": 6, "output_tokens": 4, "cache_read_input_tokens": 3, "cache_creation_input_tokens": 0,
			},
		})
	}))
	defer server.Close()

	client, err := NewClient(Options{Bindings: StaticBindingLoader{Binding: Binding{
		BaseURL: server.URL, Model: "claude-test", APIKey: []byte("test-secret"),
		ResponseFormat: ResponseFormatJSONObject, APIMode: APIModeMessages,
	}}, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Complete(context.Background(), domain.ModelTurn{
		SystemPrompt: "be careful", UserMessage: "read a file", MaxTokens: 64,
		Tools: []domain.ToolDefinition{{
			Name: "workspace.read", Version: "v1", Description: "read", Effect: domain.ToolEffectRead,
			Parameters: openAIClosedSchema(map[string]any{"path": map[string]any{"type": "string"}}, "path"),
		}},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if received.Model != "claude-test" || received.MaxTokens != 64 || len(received.Messages) != 1 || received.Messages[0].Role != "user" || received.Messages[0].Content[0].Text != "read a file" {
		t.Fatalf("messages request = %+v", received)
	}
	if len(received.System) != 1 || !strings.HasPrefix(received.System[0].Text, "be careful") || !strings.Contains(received.System[0].Text, "single JSON object") {
		t.Fatalf("system = %+v", received.System)
	}
	if len(received.Tools) != 1 || received.Tools[0].Name != "workspace_read" || received.Tools[0].InputSchema["type"] != "object" {
		t.Fatalf("messages tools = %+v", received.Tools)
	}
	if result.Provider != "anthropic" || result.Content != "" || result.FinishReason != "tool_calls" {
		t.Fatalf("result = %+v", result)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].ID != "toolu-1" || result.ToolCalls[0].Name != "workspace.read" || result.ToolCalls[0].Version != "v1" || result.ToolCalls[0].Arguments["path"] != "main.go" {
		t.Fatalf("messages tool mapping = %+v", result.ToolCalls)
	}
	if result.InputTokens != 9 || result.OutputTokens != 4 || result.UsageTokens != 13 || !result.CacheTokensReported || result.CacheHitTokens != 3 || result.CacheMissTokens != 6 {
		t.Fatalf("messages usage = %+v", result)
	}
}

func TestValidateBindingRejectsAPIKeyHeaderOverride(t *testing.T) {
	_, err := normalizeBinding(Binding{
		BaseURL: "https://api.anthropic.com", Model: "claude-test", APIKey: []byte("secret"),
		APIMode: APIModeMessages, Headers: map[string]string{"X-Api-Key": "other"},
	})
	if err == nil {
		t.Fatal("x-api-key header override was accepted")
	}
}
