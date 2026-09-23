package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mendry/backend/internal/modules/agentcore/domain"
)

func openAIClosedSchema(properties map[string]any, required ...string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}

func openAIResponse(content string, finish string, calls []chatToolCall, usage chatUsage) map[string]any {
	message := map[string]any{"content": content}
	if calls != nil {
		message["tool_calls"] = calls
	}
	return map[string]any{
		"choices": []any{map[string]any{"finish_reason": finish, "message": message}},
		"usage": map[string]any{
			"prompt_tokens":     usage.PromptTokens,
			"completion_tokens": usage.CompletionTokens,
			"total_tokens":      usage.TotalTokens,
		},
	}
}

func writeOpenAIJSON(t *testing.T, writer http.ResponseWriter, value any) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		t.Fatalf("encode test response: %v", err)
	}
}

func TestClientMapsLogicalToolsAndPreservesUsageAndCredentials(t *testing.T) {
	var received chatRequest
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" {
			t.Errorf("request path = %q", request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer test-secret" {
			t.Errorf("authorization = %q", got)
		}
		if got := request.Header.Get("X-Test"); got != "header-value" {
			t.Errorf("custom header = %q", got)
		}
		if err := json.NewDecoder(request.Body).Decode(&received); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		writeOpenAIJSON(t, writer, openAIResponse("", "tool_calls", []chatToolCall{{
			ID: "call-1", Type: "function", Function: chatFunctionCall{Name: "workspace_read_2", Arguments: `{"path":"main.go"}`},
		}}, chatUsage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10}))
	}))
	defer server.Close()

	loader := StaticBindingLoader{Binding: Binding{
		BaseURL: server.URL, Model: "test-model", APIKey: []byte("test-secret"),
		Headers: map[string]string{"X-Test": "header-value"}, ResponseFormat: ResponseFormatJSONObject,
	}}
	client, err := NewClient(Options{Bindings: loader, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Complete(context.Background(), domain.ModelTurn{
		Binding: "trusted-binding", SystemPrompt: "be concise", UserMessage: "read a file",
		MaxTokens: 32,
		Tools: []domain.ToolDefinition{
			{Name: "workspace.read", Version: "v1", Description: "read", Effect: domain.ToolEffectRead, Parameters: openAIClosedSchema(map[string]any{"path": map[string]any{"type": "string"}}, "path")},
			{Name: "workspace_read", Version: "v2", Description: "other", Effect: domain.ToolEffectRead, Parameters: openAIClosedSchema(nil)},
		},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if result.Provider != "openai" || result.Model != "test-model" || result.ModelCalls != 1 || result.InputTokens != 7 || result.OutputTokens != 3 || result.UsageTokens != 10 {
		t.Fatalf("unexpected model result: %+v", result)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Name != "workspace_read" || result.ToolCalls[0].Version != "v2" || result.ToolCalls[0].Arguments["path"] != "main.go" {
		t.Fatalf("logical tool mapping = %+v", result.ToolCalls)
	}
	if result.OutputBytes <= 0 || result.RequestBytes <= 0 || result.ToolCount != 2 || result.ToolSchemaBytes <= 0 {
		t.Fatalf("missing bounded accounting: %+v", result)
	}
	if received.Model != "test-model" || received.MaxTokens != 32 || received.ResponseFormat == nil || received.ResponseFormat.Type != string(ResponseFormatJSONObject) {
		t.Fatalf("request binding = %+v", received)
	}
	if len(received.Tools) != 2 || received.Tools[0].Function.Name != "workspace_read" || received.Tools[1].Function.Name != "workspace_read_2" {
		t.Fatalf("provider tool names = %+v", received.Tools)
	}
	if len(loader.Binding.APIKey) == 0 || string(loader.Binding.APIKey) != "test-secret" {
		t.Fatal("static loader credential was mutated by adapter")
	}
}

func TestClientMapsResponsesToolsAndUsage(t *testing.T) {
	var received responsesRequest
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/responses" {
			t.Errorf("request path = %q", request.URL.Path)
		}
		if err := json.NewDecoder(request.Body).Decode(&received); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		writeOpenAIJSON(t, writer, map[string]any{
			"status": "completed",
			"output": []map[string]any{{
				"type": "function_call", "id": "fc-1", "call_id": "call-1",
				"name": "workspace_read", "arguments": `{"path":"main.go"}`,
			}},
			"usage": map[string]any{
				"input_tokens": 9, "output_tokens": 4, "total_tokens": 13,
				"input_tokens_details": map[string]any{"cached_tokens": 3},
			},
		})
	}))
	defer server.Close()

	client, err := NewClient(Options{Bindings: StaticBindingLoader{Binding: Binding{
		BaseURL: server.URL, Model: "test-model", APIKey: []byte("test-secret"),
		ResponseFormat: ResponseFormatJSONObject, APIMode: APIModeResponses,
	}}, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Complete(context.Background(), domain.ModelTurn{
		UserMessage: "read a file", MaxTokens: 64,
		Tools: []domain.ToolDefinition{{
			Name: "workspace.read", Version: "v1", Description: "read", Effect: domain.ToolEffectRead,
			Parameters: openAIClosedSchema(map[string]any{"path": map[string]any{"type": "string"}}, "path"),
		}},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if received.Model != "test-model" || received.MaxOutputTokens != 64 || len(received.Input) != 1 || received.Input[0].Content != "read a file" {
		t.Fatalf("responses request = %+v", received)
	}
	if len(received.Tools) != 1 || received.Tools[0].Name != "workspace_read" || received.Text == nil || received.Text.Format.Type != "json_object" {
		t.Fatalf("responses tools = %+v text=%+v", received.Tools, received.Text)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].ID != "call-1" || result.ToolCalls[0].Name != "workspace.read" || result.ToolCalls[0].Version != "v1" {
		t.Fatalf("responses tool mapping = %+v", result.ToolCalls)
	}
	if result.InputTokens != 9 || result.OutputTokens != 4 || result.UsageTokens != 13 || !result.CacheTokensReported || result.CacheHitTokens != 3 || result.CacheMissTokens != 6 {
		t.Fatalf("responses usage = %+v", result)
	}
}

func TestClientRetriesTemporaryHTTPFailureWithinOneTurn(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			writer.Header().Set("Retry-After", "0")
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = writer.Write([]byte(`{"error":{"message":"temporary provider detail"}}`))
			return
		}
		writeOpenAIJSON(t, writer, openAIResponse(`{"ok":true}`, "stop", nil, chatUsage{PromptTokens: 2, CompletionTokens: 1, TotalTokens: 3}))
	}))
	defer server.Close()
	client, err := NewClient(Options{Bindings: StaticBindingLoader{Binding: Binding{BaseURL: server.URL, Model: "model", APIKey: []byte("key")}}, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Complete(context.Background(), domain.ModelTurn{UserMessage: "report"})
	if err != nil || requests.Load() != 2 || result.Content == "" || result.ModelCalls != 1 {
		t.Fatalf("retry result=%+v requests=%d err=%v", result, requests.Load(), err)
	}
}

func TestClientRetriesOutputExhaustionOnceWithLargerBudget(t *testing.T) {
	var requests atomic.Int32
	var maxTokens []int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var decoded chatRequest
		if err := json.NewDecoder(request.Body).Decode(&decoded); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		maxTokens = append(maxTokens, decoded.MaxTokens)
		if requests.Add(1) == 1 {
			writeOpenAIJSON(t, writer, openAIResponse("", "length", nil, chatUsage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3}))
			return
		}
		writeOpenAIJSON(t, writer, openAIResponse("finished", "stop", nil, chatUsage{PromptTokens: 2, CompletionTokens: 4, TotalTokens: 6}))
	}))
	defer server.Close()
	client, err := NewClient(Options{Bindings: StaticBindingLoader{Binding: Binding{BaseURL: server.URL, Model: "model", APIKey: []byte("key")}}, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Complete(context.Background(), domain.ModelTurn{UserMessage: "long report", MaxTokens: 4})
	if err != nil || result.Content != "finished" || result.ModelCalls != 2 || len(maxTokens) != 2 || maxTokens[0] != 4 || maxTokens[1] != outputRetryMaxTokens {
		t.Fatalf("output retry result=%+v maxTokens=%v err=%v", result, maxTokens, err)
	}
	if result.UsageTokens != 9 {
		t.Fatalf("usage was not aggregated: %+v", result)
	}
}

func TestClientBoundsResponsesAndSanitizesProviderFailures(t *testing.T) {
	secret := "sk-very-secret-value"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write([]byte(`{"error":{"message":"` + secret + `"}}`))
	}))
	defer server.Close()
	client, err := NewClient(Options{Bindings: StaticBindingLoader{Binding: Binding{BaseURL: server.URL, Model: "model", APIKey: []byte("key")}}, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Complete(context.Background(), domain.ModelTurn{UserMessage: "report"})
	if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "temporary") {
		t.Fatalf("provider error leaked response details: %v", err)
	}
	var runtimeErr *ProviderRuntimeError
	if !errors.As(err, &runtimeErr) || runtimeErr.Code != "provider_http_4xx" {
		t.Fatalf("provider error classification = %T %v", err, err)
	}

	large := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(strings.Repeat("x", 100)))
	}))
	defer large.Close()
	bounded, err := NewClient(Options{Bindings: StaticBindingLoader{Binding: Binding{BaseURL: large.URL, Model: "model", APIKey: []byte("key")}}, MaxResponseBytes: 32, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bounded.Complete(context.Background(), domain.ModelTurn{UserMessage: "report"}); err == nil || !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("oversized response error = %v", err)
	}
}

func TestClientAppliesOneSharedTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = writer.Write([]byte(`{"choices":[]}`))
	}))
	defer server.Close()
	client, err := NewClient(Options{Bindings: StaticBindingLoader{Binding: Binding{BaseURL: server.URL, Model: "model", APIKey: []byte("key")}}, Timeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err = client.Complete(context.Background(), domain.ModelTurn{UserMessage: "report"})
	if err == nil || time.Since(started) > time.Second {
		t.Fatalf("timeout result err=%v elapsed=%s", err, time.Since(started))
	}
	var runtimeErr *ProviderRuntimeError
	if !errors.As(err, &runtimeErr) || runtimeErr.Code != "provider_timeout" {
		t.Fatalf("timeout classification = %T %v", err, err)
	}
}

func TestUsageNeverReportsNegativeCounters(t *testing.T) {
	result := domain.ModelResult{}
	hit, miss := int64(-3), int64(-4)
	applyUsage(&result, chatUsage{PromptTokens: -1, CompletionTokens: -2, TotalTokens: -3, PromptCacheHit: &hit, PromptCacheMiss: &miss})
	if result.InputTokens != 0 || result.OutputTokens != 0 || result.UsageTokens != 0 || result.CacheHitTokens != 0 || result.CacheMissTokens != 0 || !result.CacheTokensReported {
		t.Fatalf("negative usage was retained: %+v", result)
	}
}

func TestClientRejectsInvalidToolVersionsAndOpenSchemas(t *testing.T) {
	if _, _, _, err := buildTools([]domain.ToolDefinition{{Name: "workspace.read", Version: "v0", Effect: domain.ToolEffectRead, Parameters: openAIClosedSchema(nil)}}); err == nil {
		t.Fatal("v0 tool version was accepted")
	}
	openNested := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"nested": map[string]any{
				"type": "object", "properties": map[string]any{}, "additionalProperties": true,
			},
		},
		"additionalProperties": false,
	}
	if _, _, _, err := buildTools([]domain.ToolDefinition{{Name: "workspace.read", Version: "v1", Effect: domain.ToolEffectRead, Parameters: openNested}}); err == nil {
		t.Fatal("open nested schema was accepted")
	}
	closedNested := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"nested": map[string]any{"type": "object", "properties": map[string]any{}},
		},
		"additionalProperties": false,
	}
	if _, _, _, err := buildTools([]domain.ToolDefinition{{Name: "workspace.read", Version: "v1", Effect: domain.ToolEffectRead, Parameters: closedNested}}); err != nil {
		t.Fatalf("closed nested schema was rejected: %v", err)
	}
}
