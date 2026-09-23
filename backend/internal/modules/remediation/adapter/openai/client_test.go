package openai_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	projectdomain "mendry/backend/internal/modules/projects/domain"
	"mendry/backend/internal/modules/remediation/adapter/openai"
	"mendry/backend/internal/modules/remediation/domain"
	"mendry/backend/internal/platform/observability"
)

const (
	testProjectID = "019ff544-405c-7d21-9f10-cb3fc579605c"
	testAPIKey    = "sk-test-openai-key"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type retryableTransportError struct{}

func (retryableTransportError) Error() string   { return "transient network failure" }
func (retryableTransportError) Timeout() bool   { return true }
func (retryableTransportError) Temporary() bool { return true }

type staticConfig struct {
	cfg openai.ProviderConfig
	err error
}

func (s staticConfig) LoadLLMProvider(context.Context, string) (openai.ProviderConfig, error) {
	return s.cfg, s.err
}

type namedSecretLookup struct {
	secret projectdomain.EncryptedSecret
	err    error
}

func (l namedSecretLookup) GetEncryptedSecret(context.Context, string, string) (projectdomain.EncryptedSecret, error) {
	return l.secret, l.err
}

type staticCipher struct {
	plaintext []byte
}

func (staticCipher) Encrypt(string, string, projectdomain.SecretKind, []byte) ([]byte, []byte, int32, error) {
	return nil, nil, 0, errors.New("unused")
}
func (c staticCipher) Decrypt(string, string, projectdomain.SecretKind, []byte, []byte) ([]byte, error) {
	return append([]byte(nil), c.plaintext...), nil
}
func (staticCipher) EncryptWebhookToken(string, string, []byte) ([]byte, []byte, error) {
	return nil, nil, errors.New("unused")
}
func (staticCipher) DecryptWebhookToken(string, string, []byte, []byte) ([]byte, error) {
	return nil, errors.New("unused")
}

func TestCompleteMapsChatCompletion(t *testing.T) {
	keyHash := sha256.Sum256([]byte(testAPIKey))
	var sawModel, sawJSON bool
	var authHash string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" {
			http.NotFound(writer, request)
			return
		}
		body, _ := io.ReadAll(request.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(writer, "bad json", http.StatusBadRequest)
			return
		}
		if payload["model"] == openai.ModelID {
			sawModel = true
		}
		if format, _ := payload["response_format"].(map[string]any); format["type"] == "json_object" {
			sawJSON = true
		}
		auth := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
		sum := sha256.Sum256([]byte(auth))
		authHash = hex.EncodeToString(sum[:])
		_, _ = writer.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"{\"schemaVersion\":\"v1\",\"kind\":\"stop\"}"}}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`))
	}))
	defer server.Close()

	client, err := openai.NewClient(openai.Options{
		HTTPClient:   server.Client(),
		BaseURL:      server.URL,
		StaticAPIKey: testAPIKey,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	result, err := client.Complete(context.Background(), domain.ModelTurn{
		ProjectID:    testProjectID,
		SystemPrompt: "sys",
		UserMessage:  "user",
		MaxTokens:    128,
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if !sawModel || !sawJSON {
		t.Fatal("request did not use gpt-5.6 json_object")
	}
	if authHash != hex.EncodeToString(keyHash[:]) {
		t.Fatal("authorization header did not match injected key hash")
	}
	if result.Provider != "openai" || result.Model != openai.ModelID || result.UsageTokensIn != 11 ||
		result.UsageTokensOut != 7 || result.Content == "" || result.FinishReason != "stop" {
		t.Fatalf("result = %#v", result)
	}
}

func TestCompleteSendsToolsAndParsesNativeToolCalls(t *testing.T) {
	var requestBytes, schemaBytes int64
	client, err := openai.NewClient(openai.Options{
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(request.Body)
			if err != nil {
				return nil, err
			}
			requestBytes = int64(len(body))
			var payload map[string]interface{}
			if err := json.Unmarshal(body, &payload); err != nil {
				return nil, err
			}
			tools, ok := payload["tools"].([]interface{})
			if !ok || len(tools) != 1 {
				t.Fatalf("tools = %#v, want one tool", payload["tools"])
			}
			tool := tools[0].(map[string]interface{})
			function := tool["function"].(map[string]interface{})
			if function["name"] != "repository_read_file" {
				t.Fatalf("tool name = %#v", function["name"])
			}
			messages, ok := payload["messages"].([]interface{})
			if !ok || len(messages) < 2 {
				t.Fatalf("messages = %#v, want system, history, and user messages", payload["messages"])
			}
			history, ok := messages[1].(map[string]interface{})
			if !ok {
				t.Fatalf("history message = %#v", messages[1])
			}
			historyCalls, ok := history["tool_calls"].([]interface{})
			if !ok || len(historyCalls) != 1 {
				t.Fatalf("history tool calls = %#v", history["tool_calls"])
			}
			historyCall := historyCalls[0].(map[string]interface{})
			historyFunction := historyCall["function"].(map[string]interface{})
			if historyFunction["name"] != "repository_read_file" {
				t.Fatalf("history tool name = %#v", historyFunction["name"])
			}
			last := messages[len(messages)-1].(map[string]interface{})
			if last["content"] != "incremental user" {
				t.Fatalf("last user message = %#v, want continuation", last["content"])
			}
			parameters := function["parameters"].(map[string]interface{})
			encodedParameters, err := json.Marshal(parameters)
			if err != nil {
				return nil, err
			}
			schemaBytes = int64(len(encodedParameters))
			if parameters["type"] != "object" {
				t.Fatalf("tool parameters = %#v", parameters)
			}
			responseBody := `{"choices":[{"finish_reason":"tool_calls","message":{"content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"repository_read_file","arguments":"{\"path\":\"main.go\"}"}}]}}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5,"prompt_tokens_details":{"cached_tokens":1}}}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(responseBody)),
				Request:    request,
			}, nil
		})},
		StaticAPIKey: testAPIKey,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	result, err := client.Complete(context.Background(), domain.ModelTurn{
		ProjectID:    testProjectID,
		SystemPrompt: "sys",
		UserMessage:  "full fallback user",
		Continuation: "incremental user",
		Messages: []domain.ModelMessage{{
			Role: "assistant",
			ToolCalls: []domain.ToolCall{{
				ID: "call_0", Name: "repository.read_file",
				Arguments: map[string]interface{}{"path": "previous.go"},
			}},
		}},
		Tools: []domain.ToolDefinition{{
			Name: "repository.read_file", Description: "Read a file.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{"type": "string"},
				},
			},
		}},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].ID != "call_1" ||
		result.ToolCalls[0].Name != "repository.read_file" || result.ToolCalls[0].Arguments["path"] != "main.go" {
		t.Fatalf("native tool calls = %#v", result.ToolCalls)
	}
	if result.Content != "" || result.FinishReason != "tool_calls" {
		t.Fatalf("result = %#v", result)
	}
	if result.RequestBytes != requestBytes || result.ToolCount != 1 || result.ToolSchemaBytes != schemaBytes {
		t.Fatalf("request metrics = bytes:%d/%d tools:%d schema:%d/%d", result.RequestBytes, requestBytes, result.ToolCount, result.ToolSchemaBytes, schemaBytes)
	}
	if !result.CacheTokensReported || result.CacheHitTokens != 1 || result.CacheMissTokens != 2 {
		t.Fatalf("cache metrics = reported:%t hit:%d miss:%d", result.CacheTokensReported, result.CacheHitTokens, result.CacheMissTokens)
	}
}

func TestCompleteSendsResponsesToolsAndParsesFunctionCalls(t *testing.T) {
	client, err := openai.NewClient(openai.Options{
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Path != "/v1/responses" {
				t.Fatalf("path = %q", request.URL.Path)
			}
			var payload map[string]interface{}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				return nil, err
			}
			if payload["max_output_tokens"] != float64(128) {
				t.Fatalf("max_output_tokens = %#v", payload["max_output_tokens"])
			}
			text := payload["text"].(map[string]interface{})
			format := text["format"].(map[string]interface{})
			if format["type"] != "json_object" {
				t.Fatalf("text format = %#v", text)
			}
			tools := payload["tools"].([]interface{})
			tool := tools[0].(map[string]interface{})
			if tool["name"] != "repository_read_file" || tool["type"] != "function" || tool["function"] != nil {
				t.Fatalf("responses tool = %#v", tool)
			}
			input := payload["input"].([]interface{})
			if len(input) != 3 || input[1].(map[string]interface{})["type"] != "function_call" || input[2].(map[string]interface{})["content"] != "incremental user" {
				t.Fatalf("responses input = %#v", input)
			}
			responseBody := `{"status":"completed","output":[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"repository_read_file","arguments":"{\"path\":\"main.go\"}"}],"usage":{"input_tokens":8,"output_tokens":2,"total_tokens":10,"input_tokens_details":{"cached_tokens":3}}}`
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(responseBody)), Request: request}, nil
		})},
		StaticAPIKey: testAPIKey,
		APIMode:      openai.APIModeResponses,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	result, err := client.Complete(context.Background(), domain.ModelTurn{
		ProjectID: testProjectID, SystemPrompt: "sys", UserMessage: "fallback", Continuation: "incremental user", MaxTokens: 128,
		Messages: []domain.ModelMessage{{Role: "assistant", ToolCalls: []domain.ToolCall{{ID: "call_0", Name: "repository.read_file", Arguments: map[string]interface{}{"path": "previous.go"}}}}},
		Tools:    []domain.ToolDefinition{{Name: "repository.read_file", Description: "Read a file.", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"path": map[string]interface{}{"type": "string"}}}}},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].ID != "call_1" || result.ToolCalls[0].Name != "repository.read_file" || result.ToolCalls[0].Arguments["path"] != "main.go" {
		t.Fatalf("responses tool calls = %#v", result.ToolCalls)
	}
	if result.UsageTokensIn != 8 || result.UsageTokensOut != 2 || result.UsageTokens != 10 || result.CacheHitTokens != 3 || result.CacheMissTokens != 5 {
		t.Fatalf("responses usage = %#v", result)
	}
}

func TestCompleteNormalizesCacheUsageShapes(t *testing.T) {
	tests := []struct {
		name         string
		usage        string
		wantReported bool
		wantHit      int64
		wantMiss     int64
	}{
		{name: "openai nested", usage: `"prompt_tokens":10,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":4}`, wantReported: true, wantHit: 4, wantMiss: 6},
		{name: "compatible takes precedence", usage: `"prompt_tokens":10,"completion_tokens":1,"prompt_cache_hit_tokens":3,"prompt_cache_miss_tokens":7,"prompt_tokens_details":{"cached_tokens":9}`, wantReported: true, wantHit: 3, wantMiss: 7},
		{name: "reported zero", usage: `"prompt_tokens":0,"completion_tokens":1,"prompt_cache_hit_tokens":0,"prompt_cache_miss_tokens":0`, wantReported: true},
		{name: "unreported", usage: `"prompt_tokens":10,"completion_tokens":1`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			responseBody := `{"choices":[{"finish_reason":"stop","message":{"content":"{\"schemaVersion\":\"v1\",\"kind\":\"stop\"}"}}],"usage":{` + test.usage + `}}`
			client, err := openai.NewClient(openai.Options{
				HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(responseBody)), Request: request}, nil
				})},
				StaticAPIKey: testAPIKey,
			})
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}
			result, err := client.Complete(context.Background(), domain.ModelTurn{ProjectID: testProjectID, UserMessage: "user"})
			if err != nil {
				t.Fatalf("Complete() error = %v", err)
			}
			if result.CacheTokensReported != test.wantReported || result.CacheHitTokens != test.wantHit || result.CacheMissTokens != test.wantMiss {
				t.Fatalf("cache metrics = reported:%t hit:%d miss:%d", result.CacheTokensReported, result.CacheHitTokens, result.CacheMissTokens)
			}
		})
	}
}

func TestCompleteRetriesTransientHTTPStatus(t *testing.T) {
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
			if attempts < 3 {
				header := make(http.Header)
				header.Set("Retry-After", "0")
				return &http.Response{
					StatusCode: http.StatusServiceUnavailable,
					Header:     header,
					Body:       io.NopCloser(strings.NewReader(`{"error":"temporary"}`)),
					Request:    request,
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"choices":[{"finish_reason":"stop","message":{"content":"{\"schemaVersion\":\"v1\",\"kind\":\"stop\"}"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)),
				Request:    request,
			}, nil
		})},
		StaticAPIKey: testAPIKey,
		Logger:       logger,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	result, err := client.Complete(context.Background(), domain.ModelTurn{
		ProjectID: testProjectID, SystemPrompt: "sys", UserMessage: "user",
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if attempts != 3 {
		t.Fatalf("HTTP attempts = %d, want 3", attempts)
	}
	if result.Content == "" {
		t.Fatal("successful retry returned empty content")
	}
	lines := bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n"))
	if len(lines) != 3 {
		t.Fatalf("attempt log records = %d, want 3: %s", len(lines), output.String())
	}
	for _, line := range lines {
		var record map[string]interface{}
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("decode attempt log: %v", err)
		}
		if record[observability.FieldRequestBytes] != float64(result.RequestBytes) || record[observability.FieldToolCount] != float64(0) || record[observability.FieldToolSchemaBytes] != float64(0) {
			t.Fatalf("attempt metrics = %#v", record)
		}
	}
}

func TestCompleteRetriesTransientNetworkError(t *testing.T) {
	attempts := 0
	client, err := openai.NewClient(openai.Options{
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			attempts++
			if attempts == 1 {
				return nil, retryableTransportError{}
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"choices":[{"finish_reason":"stop","message":{"content":"{\"schemaVersion\":\"v1\",\"kind\":\"stop\"}"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)),
				Request:    request,
			}, nil
		})},
		StaticAPIKey: testAPIKey,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := client.Complete(context.Background(), domain.ModelTurn{
		ProjectID: testProjectID, SystemPrompt: "sys", UserMessage: "user",
	}); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if attempts != 2 {
		t.Fatalf("HTTP attempts = %d, want 2", attempts)
	}
}

func TestCompleteDoesNotRetryBadRequest(t *testing.T) {
	attempts := 0
	client, err := openai.NewClient(openai.Options{
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			attempts++
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"error":"invalid request"}`)),
				Request:    request,
			}, nil
		})},
		StaticAPIKey: testAPIKey,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := client.Complete(context.Background(), domain.ModelTurn{
		ProjectID: testProjectID, SystemPrompt: "sys", UserMessage: "user",
	}); err == nil || !strings.Contains(err.Error(), "status 400") {
		t.Fatalf("Complete() error = %v, want status 400", err)
	}
	if attempts != 1 {
		t.Fatalf("HTTP attempts = %d, want 1", attempts)
	}
}

func TestCompleteFailsClosed(t *testing.T) {
	t.Run("missing secret", func(t *testing.T) {
		client, err := openai.NewClient(openai.Options{
			Configs: staticConfig{err: errors.New("not found")},
			Secrets: namedSecretLookup{},
			Cipher:  staticCipher{},
		})
		if err != nil {
			t.Fatalf("NewClient() error = %v", err)
		}
		_, err = client.Complete(context.Background(), domain.ModelTurn{ProjectID: testProjectID})
		if err == nil || !strings.Contains(err.Error(), "llm") {
			t.Fatalf("missing secret error = %v", err)
		}
	})
	t.Run("non-200", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusBadGateway)
			_, _ = writer.Write([]byte(`{"error":"nope"}`))
		}))
		defer server.Close()
		client, err := openai.NewClient(openai.Options{HTTPClient: server.Client(), BaseURL: server.URL, StaticAPIKey: testAPIKey})
		if err != nil {
			t.Fatalf("NewClient() error = %v", err)
		}
		if _, err := client.Complete(context.Background(), domain.ModelTurn{ProjectID: testProjectID}); err == nil {
			t.Fatal("non-200 error = nil")
		}
	})
	t.Run("invalid json", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = writer.Write([]byte("not-json"))
		}))
		defer server.Close()
		client, err := openai.NewClient(openai.Options{HTTPClient: server.Client(), BaseURL: server.URL, StaticAPIKey: testAPIKey})
		if err != nil {
			t.Fatalf("NewClient() error = %v", err)
		}
		if _, err := client.Complete(context.Background(), domain.ModelTurn{ProjectID: testProjectID}); err == nil {
			t.Fatal("invalid json error = nil")
		}
	})
}

func TestAdapterDoesNotExportSDKTypes(t *testing.T) {
	// 包内只导出 Client / Options / SecretLookup / ModelID，避免 SDK 类型越过边界。
	var _ domain.LLMProviderPort = (*openai.Client)(nil)
}

func TestCompleteDoesNotLeakAPIKeyInErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write([]byte(`{"error":{"message":"Incorrect API key provided: sk-test-openai-key"}}`))
	}))
	defer server.Close()
	client, err := openai.NewClient(openai.Options{HTTPClient: server.Client(), BaseURL: server.URL, StaticAPIKey: testAPIKey})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	_, err = client.Complete(context.Background(), domain.ModelTurn{ProjectID: testProjectID})
	if err == nil {
		t.Fatal("expected status error")
	}
	if strings.Contains(err.Error(), testAPIKey) || strings.Contains(err.Error(), "Incorrect API key") {
		t.Fatalf("error leaked provider payload: %v", err)
	}
}

func TestCompleteLogsBoundedFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write([]byte(`{"error":{"message":"Incorrect API key provided: sk-test-openai-key"}}`))
	}))
	defer server.Close()

	var output bytes.Buffer
	logger, err := observability.NewLogger(observability.LoggerOptions{
		Writer: &output, Level: "debug", Format: "json", Service: "mendry-test", Environment: "test",
	})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	client, err := openai.NewClient(openai.Options{
		HTTPClient: server.Client(), BaseURL: server.URL, StaticAPIKey: testAPIKey, Logger: logger,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := client.Complete(context.Background(), domain.ModelTurn{ProjectID: testProjectID}); err == nil {
		t.Fatal("expected status error")
	}

	lines := bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n"))
	if len(lines) == 0 || len(lines[0]) == 0 {
		t.Fatalf("no log records: %q", output.Bytes())
	}
	var record map[string]any
	if err := json.Unmarshal(lines[len(lines)-1], &record); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; output = %q", err, output.String())
	}
	if record[observability.FieldEvent] != observability.EventLLMRequestCompleted {
		t.Fatalf("event = %#v", record[observability.FieldEvent])
	}
	if record[observability.FieldLLMOperation] != "chat.completions" {
		t.Fatalf("operation = %#v", record[observability.FieldLLMOperation])
	}
	if record[observability.FieldHTTPPath] != "/v1/chat/completions" {
		t.Fatalf("path = %#v", record[observability.FieldHTTPPath])
	}
	if record[observability.FieldOutcome] != "failure" {
		t.Fatalf("outcome = %#v", record[observability.FieldOutcome])
	}
	if record[observability.FieldErrorClass] != observability.OutboundHTTP4xx {
		t.Fatalf("error_class = %#v", record[observability.FieldErrorClass])
	}
	if record[observability.FieldRequestBytes].(float64) <= 0 || record[observability.FieldToolCount] != float64(0) || record[observability.FieldToolSchemaBytes] != float64(0) {
		t.Fatalf("request metrics = %#v", record)
	}
	if _, exists := record[observability.FieldModelCacheHitTokens]; exists {
		t.Fatalf("unreported cache fields must be omitted: %#v", record)
	}
	request, _ := record[observability.FieldHTTPRequest].(string)
	if !strings.Contains(request, `"role":"user"`) {
		t.Fatalf("request = %#v", record[observability.FieldHTTPRequest])
	}
	response, _ := record[observability.FieldHTTPResponse].(string)
	if !strings.Contains(response, "Incorrect API key provided") {
		t.Fatalf("response = %#v", record[observability.FieldHTTPResponse])
	}
	dump := output.String()
	if strings.Contains(dump, testAPIKey) || strings.Contains(dump, "Authorization") {
		t.Fatalf("log leaked secret material: %s", dump)
	}
}

func TestCompleteRejectsCredentialOwnershipMismatch(t *testing.T) {
	client, err := openai.NewClient(openai.Options{
		Configs: staticConfig{cfg: openai.ProviderConfig{
			BaseURL: "https://llm.example.test", Model: openai.ModelID, CredentialSecretID: "secret-1",
		}},
		Secrets: namedSecretLookup{secret: projectdomain.EncryptedSecret{Secret: projectdomain.Secret{
			ID: "secret-elsewhere", ProjectID: "project-elsewhere", Kind: projectdomain.SecretHTTPBearer,
		}}},
		Cipher: staticCipher{plaintext: []byte(testAPIKey)},
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	_, err = client.Complete(context.Background(), domain.ModelTurn{ProjectID: testProjectID})
	if err == nil || !strings.Contains(err.Error(), "ownership") {
		t.Fatalf("credential ownership error = %v", err)
	}
}

func TestCompleteRejectsDuplicateToolDefinitionsBeforeRequest(t *testing.T) {
	client, err := openai.NewClient(openai.Options{StaticAPIKey: testAPIKey})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	definition := domain.ToolDefinition{
		Name: "repository.read_file", Parameters: map[string]interface{}{"type": "object"},
	}
	_, err = client.Complete(context.Background(), domain.ModelTurn{
		ProjectID: testProjectID, Tools: []domain.ToolDefinition{definition, definition},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate tool name") {
		t.Fatalf("duplicate definition error = %v", err)
	}
}

func TestCompleteUsesOneLogicalTurnDeadlineAndClearsHTTPClientTimeout(t *testing.T) {
	injected := &http.Client{
		Timeout: time.Millisecond,
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			select {
			case <-time.After(30 * time.Millisecond):
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body: io.NopCloser(strings.NewReader(
						`{"choices":[{"finish_reason":"stop","message":{"content":"{\"schemaVersion\":\"v1\",\"kind\":\"stop\"}"}}],"usage":{"total_tokens":1}}`,
					)),
					Request: request,
				}, nil
			case <-request.Context().Done():
				return nil, request.Context().Err()
			}
		}),
	}
	client, err := openai.NewClient(openai.Options{
		HTTPClient: injected, Timeout: 200 * time.Millisecond, StaticAPIKey: testAPIKey,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	if _, err := client.Complete(context.Background(), domain.ModelTurn{
		ProjectID: testProjectID, SystemPrompt: "sys", UserMessage: "user",
	}); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if injected.Timeout != time.Millisecond {
		t.Fatalf("injected HTTP client timeout mutated to %s", injected.Timeout)
	}
}

func TestCompleteRetryBackoffSharesLogicalTurnDeadline(t *testing.T) {
	attempts := 0
	client, err := openai.NewClient(openai.Options{
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			attempts++
			return nil, retryableTransportError{}
		})},
		Timeout: 20 * time.Millisecond, StaticAPIKey: testAPIKey,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	_, err = client.Complete(context.Background(), domain.ModelTurn{ProjectID: testProjectID})
	if !errors.Is(err, openai.ErrModelTurnTimeout) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Complete() error = %v, want logical turn timeout", err)
	}
	if attempts != 1 {
		t.Fatalf("HTTP attempts = %d, want shared deadline to expire during first backoff", attempts)
	}
}

func TestCompleteEarlierParentDeadlineWins(t *testing.T) {
	attempts := 0
	client, err := openai.NewClient(openai.Options{
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			attempts++
			<-request.Context().Done()
			return nil, request.Context().Err()
		})},
		Timeout: 200 * time.Millisecond, StaticAPIKey: testAPIKey,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	parent, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err = client.Complete(parent, domain.ModelTurn{ProjectID: testProjectID})
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, openai.ErrModelTurnTimeout) {
		t.Fatalf("Complete() error = %v, want parent deadline", err)
	}
	if attempts != 1 {
		t.Fatalf("HTTP attempts = %d, want 1", attempts)
	}
}

func TestCompleteRetriesOutputExhaustionOnceAndAggregatesUsage(t *testing.T) {
	var payloads []map[string]interface{}
	client, err := openai.NewClient(openai.Options{
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			var payload map[string]interface{}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				return nil, err
			}
			payloads = append(payloads, payload)
			body := `{"choices":[{"finish_reason":"length","message":{"content":""}}],"usage":{"prompt_tokens":10,"completion_tokens":8192,"total_tokens":8202,"prompt_cache_hit_tokens":3,"prompt_cache_miss_tokens":7}}`
			if len(payloads) == 2 {
				body = `{"choices":[{"finish_reason":"stop","message":{"content":"{\"schemaVersion\":\"v1\",\"kind\":\"stop\"}"}}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18,"prompt_tokens_details":{"cached_tokens":4}}}`
			}
			return &http.Response{
				StatusCode: http.StatusOK, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(body)), Request: request,
			}, nil
		})},
		StaticAPIKey: testAPIKey,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	result, err := client.Complete(context.Background(), domain.ModelTurn{
		ProjectID: testProjectID, SystemPrompt: "sys", UserMessage: "user", MaxTokens: 8192,
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if len(payloads) != 2 || payloads[0]["max_tokens"] != float64(8192) || payloads[1]["max_tokens"] != float64(16384) {
		t.Fatalf("output retry payloads = %#v", payloads)
	}
	first := mapsWithoutKey(payloads[0], "max_tokens")
	second := mapsWithoutKey(payloads[1], "max_tokens")
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("retry changed request fields: first=%#v second=%#v", first, second)
	}
	if result.ModelCalls != 2 || result.UsageTokensIn != 21 || result.UsageTokensOut != 8199 || result.UsageTokens != 8220 {
		t.Fatalf("aggregated usage = %#v", result)
	}
	if !result.CacheTokensReported || result.CacheHitTokens != 7 || result.CacheMissTokens != 14 {
		t.Fatalf("aggregated cache usage = %#v", result)
	}
	if result.FinishReason != "stop" || result.Content == "" {
		t.Fatalf("final response = %#v", result)
	}
}

func TestCompleteReturnsAggregatedOutputExhaustionAfterOneRetry(t *testing.T) {
	attempts := 0
	client, err := openai.NewClient(openai.Options{
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			attempts++
			return &http.Response{
				StatusCode: http.StatusOK, Header: make(http.Header), Request: request,
				Body: io.NopCloser(strings.NewReader(
					`{"choices":[{"finish_reason":"length","message":{"content":""}}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`,
				)),
			}, nil
		})},
		StaticAPIKey: testAPIKey,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	result, err := client.Complete(context.Background(), domain.ModelTurn{ProjectID: testProjectID, MaxTokens: 8192})
	if !errors.Is(err, domain.ErrModelOutputExhausted) {
		t.Fatalf("Complete() error = %v, want output exhaustion", err)
	}
	if attempts != 2 || result.ModelCalls != 2 || result.UsageTokensIn != 4 || result.UsageTokensOut != 6 || result.UsageTokens != 10 || result.FinishReason != "length" {
		t.Fatalf("terminal exhaustion result = %#v attempts=%d", result, attempts)
	}
}

func TestCompleteDoesNotRetryGenericEmptyResponse(t *testing.T) {
	attempts := 0
	client, err := openai.NewClient(openai.Options{
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			attempts++
			return &http.Response{
				StatusCode: http.StatusOK, Header: make(http.Header), Request: request,
				Body: io.NopCloser(strings.NewReader(
					`{"choices":[{"finish_reason":"stop","message":{"content":""}}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`,
				)),
			}, nil
		})},
		StaticAPIKey: testAPIKey,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	result, err := client.Complete(context.Background(), domain.ModelTurn{ProjectID: testProjectID, MaxTokens: 8192})
	if err == nil || errors.Is(err, domain.ErrModelOutputExhausted) || !strings.Contains(err.Error(), "no content or tool calls") {
		t.Fatalf("Complete() error = %v, want generic empty response", err)
	}
	if attempts != 1 || result.ModelCalls != 1 || result.UsageTokens != 3 || result.FinishReason != "stop" {
		t.Fatalf("generic empty result = %#v attempts=%d", result, attempts)
	}
}

func TestCompleteOutputRetrySharesLogicalTurnDeadline(t *testing.T) {
	attempts := 0
	client, err := openai.NewClient(openai.Options{
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			attempts++
			if attempts == 1 {
				return &http.Response{
					StatusCode: http.StatusOK, Header: make(http.Header), Request: request,
					Body: io.NopCloser(strings.NewReader(
						`{"choices":[{"finish_reason":"length","message":{"content":""}}],"usage":{"total_tokens":1}}`,
					)),
				}, nil
			}
			<-request.Context().Done()
			return nil, request.Context().Err()
		})},
		Timeout: 20 * time.Millisecond, StaticAPIKey: testAPIKey,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	result, err := client.Complete(context.Background(), domain.ModelTurn{ProjectID: testProjectID, MaxTokens: 8192})
	if !errors.Is(err, openai.ErrModelTurnTimeout) {
		t.Fatalf("Complete() error = %v, want shared logical timeout", err)
	}
	if attempts != 2 || result.ModelCalls != 2 || result.UsageTokens != 1 {
		t.Fatalf("shared deadline result = %#v attempts=%d", result, attempts)
	}
}

func TestCompleteFinalTransientStatusCarriesProviderClassification(t *testing.T) {
	client, err := openai.NewClient(openai.Options{
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			header := make(http.Header)
			header.Set("Retry-After", "0")
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Header:     header,
				Body:       io.NopCloser(strings.NewReader(`{"error":"temporary"}`)),
				Request:    request,
			}, nil
		})},
		StaticAPIKey: testAPIKey,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	_, err = client.Complete(context.Background(), domain.ModelTurn{ProjectID: testProjectID})
	var providerFailure *domain.ProviderRuntimeError
	if err == nil || !errors.As(err, &providerFailure) || providerFailure.Code != "provider_http_5xx" || !providerFailure.Retryable {
		t.Fatalf("error = %v, provider failure = %#v", err, providerFailure)
	}
}

func TestCompleteFinalNonTransientStatusIsNonRetryableProviderClassification(t *testing.T) {
	client, err := openai.NewClient(openai.Options{
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"error":"invalid"}`)),
				Request:    request,
			}, nil
		})},
		StaticAPIKey: testAPIKey,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	_, err = client.Complete(context.Background(), domain.ModelTurn{ProjectID: testProjectID})
	var providerFailure *domain.ProviderRuntimeError
	if err == nil || !errors.As(err, &providerFailure) || providerFailure.Code != "provider_http_4xx" || providerFailure.Retryable {
		t.Fatalf("error = %v, provider failure = %#v", err, providerFailure)
	}
}

func mapsWithoutKey(source map[string]interface{}, key string) map[string]interface{} {
	result := make(map[string]interface{}, len(source)-1)
	for name, value := range source {
		if name != key {
			result[name] = value
		}
	}
	return result
}
