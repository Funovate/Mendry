package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mendry/backend/internal/platform/observability"
)

func TestListModelsReturnsSortedIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/models" || request.Header.Get("Authorization") != "Bearer sk-test" {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"data": []map[string]string{{"id": "gpt-5.6"}, {"id": "gpt-4.1"}, {"id": "gpt-5.6"}},
		})
	}))
	defer server.Close()

	models, err := NewLister(server.Client(), nil).ListModels(context.Background(), server.URL, []byte("sk-test"))
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if strings.Join(models, ",") != "gpt-4.1,gpt-5.6" {
		t.Fatalf("models = %#v", models)
	}
}

func TestProbeChatAcceptsHiReply(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" || request.Header.Get("Authorization") != "Bearer sk-test" {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			http.Error(writer, "bad json", http.StatusBadRequest)
			return
		}
		if payload["model"] != "gpt-5.6" {
			http.Error(writer, "bad model", http.StatusBadRequest)
			return
		}
		maxTokens, ok := payload["max_tokens"].(float64)
		if !ok || int(maxTokens) != chatProbeMaxTokens || maxTokens < 64 || maxTokens > 256 {
			http.Error(writer, "bad max tokens", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": "hello"}}},
		})
	}))
	defer server.Close()

	if err := NewLister(server.Client(), nil).ProbeChat(context.Background(), server.URL, []byte("sk-test"), "gpt-5.6"); err != nil {
		t.Fatalf("ProbeChat() error = %v", err)
	}
}

func TestProbeChatHidesNonOKBodies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "sk-secret-should-not-leak", http.StatusUnauthorized)
	}))
	defer server.Close()

	err := NewLister(server.Client(), nil).ProbeChat(context.Background(), server.URL, []byte("sk-test"), "gpt-5.6")
	if err == nil || strings.Contains(err.Error(), "sk-secret") || strings.Contains(err.Error(), "sk-test") {
		t.Fatalf("error = %v", err)
	}
}

func TestListModelsHidesNonOKBodies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "sk-secret-should-not-leak", http.StatusUnauthorized)
	}))
	defer server.Close()

	_, err := NewLister(server.Client(), nil).ListModels(context.Background(), server.URL, []byte("sk-test"))
	if err == nil || strings.Contains(err.Error(), "sk-secret") || strings.Contains(err.Error(), "sk-test") {
		t.Fatalf("error = %v", err)
	}
}

func TestProbeChatLogsBoundedFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write([]byte(`{"error":{"message":"Incorrect API key provided: sk-secret-should-not-leak"}}`))
	}))
	defer server.Close()

	var output bytes.Buffer
	logger, err := observability.NewLogger(observability.LoggerOptions{
		Writer: &output, Level: "debug", Format: "json", Service: "mendry-test", Environment: "test",
	})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}

	probeErr := NewLister(server.Client(), logger).ProbeChat(context.Background(), server.URL, []byte("sk-test"), "gpt-5.6")
	if probeErr == nil {
		t.Fatal("expected probe error")
	}

	record := lastJSONRecord(t, output.Bytes())
	if record[observability.FieldEvent] != observability.EventLLMRequestCompleted {
		t.Fatalf("event = %#v", record[observability.FieldEvent])
	}
	if record[observability.FieldComponent] != "openai" {
		t.Fatalf("component = %#v", record[observability.FieldComponent])
	}
	if record[observability.FieldLLMOperation] != opChatProbe {
		t.Fatalf("operation = %#v", record[observability.FieldLLMOperation])
	}
	if record[observability.FieldLLMModel] != "gpt-5.6" {
		t.Fatalf("model = %#v", record[observability.FieldLLMModel])
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
	if int(record[observability.FieldHTTPStatus].(float64)) != http.StatusUnauthorized {
		t.Fatalf("status = %#v", record[observability.FieldHTTPStatus])
	}
	request, _ := record[observability.FieldHTTPRequest].(string)
	if !strings.Contains(request, `"content":"hi"`) || !strings.Contains(request, `"model":"gpt-5.6"`) {
		t.Fatalf("request = %#v", record[observability.FieldHTTPRequest])
	}
	response, _ := record[observability.FieldHTTPResponse].(string)
	if !strings.Contains(response, "Incorrect API key provided") {
		t.Fatalf("response = %#v", record[observability.FieldHTTPResponse])
	}
	dump := output.String()
	if strings.Contains(dump, "sk-test") || strings.Contains(dump, "sk-secret") || strings.Contains(dump, "Authorization") {
		t.Fatalf("log leaked secret material: %s", dump)
	}
}

func TestListModelsLogsBoundedSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"data": []map[string]string{{"id": "gpt-5.6"}}})
	}))
	defer server.Close()

	var output bytes.Buffer
	logger, err := observability.NewLogger(observability.LoggerOptions{
		Writer: &output, Level: "debug", Format: "json", Service: "mendry-test", Environment: "test",
	})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}

	if _, err := NewLister(server.Client(), logger).ListModels(context.Background(), server.URL, []byte("sk-test")); err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}

	record := lastJSONRecord(t, output.Bytes())
	if record[observability.FieldEvent] != observability.EventLLMRequestCompleted {
		t.Fatalf("event = %#v", record[observability.FieldEvent])
	}
	if record[observability.FieldLLMOperation] != opModelsList {
		t.Fatalf("operation = %#v", record[observability.FieldLLMOperation])
	}
	if record[observability.FieldHTTPPath] != "/v1/models" {
		t.Fatalf("path = %#v", record[observability.FieldHTTPPath])
	}
	if record[observability.FieldOutcome] != "success" {
		t.Fatalf("outcome = %#v", record[observability.FieldOutcome])
	}
	response, _ := record[observability.FieldHTTPResponse].(string)
	if !strings.Contains(response, "gpt-5.6") {
		t.Fatalf("success response = %#v", record[observability.FieldHTTPResponse])
	}
	if _, ok := record[observability.FieldLLMModel]; ok {
		t.Fatalf("models.list should not include a model field: %#v", record)
	}
	if strings.Contains(output.String(), "sk-test") {
		t.Fatalf("log leaked api key: %s", output.String())
	}
}

func TestGenerateLogRuleReturnsStructuredRuleWithoutLoggingSample(t *testing.T) {
	const sample = "ERROR card declined customer=redacted"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload struct {
			Messages []map[string]string `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || len(payload.Messages) != 2 || !strings.Contains(payload.Messages[1]["content"], sample) {
			http.Error(writer, "bad prompt", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": `{"id":"payment-errors","name":"Payment errors","matchType":"contains","pattern":"ERROR card declined","excludePattern":"healthcheck","threshold":3,"windowSeconds":60,"cooldownSeconds":300}`}}}})
	}))
	defer server.Close()
	var output bytes.Buffer
	logger, err := observability.NewLogger(observability.LoggerOptions{Writer: &output, Level: "debug", Format: "json", Service: "test", Environment: "test"})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := NewLister(server.Client(), logger).GenerateLogRule(context.Background(), server.URL, []byte("sk-test"), "gpt-5.6", "alert on repeated payment failures", sample)
	if err != nil || rule.ID != "payment-errors" || rule.Threshold != 3 {
		t.Fatalf("GenerateLogRule() = %#v error=%v", rule, err)
	}
	if strings.Contains(output.String(), sample) || strings.Contains(output.String(), "sk-test") {
		t.Fatalf("generation log leaked prompt or credential: %s", output.String())
	}
}

func lastJSONRecord(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	lines := bytes.Split(bytes.TrimSpace(raw), []byte("\n"))
	if len(lines) == 0 || len(lines[0]) == 0 {
		t.Fatalf("no log records: %q", raw)
	}
	var record map[string]any
	if err := json.Unmarshal(lines[len(lines)-1], &record); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; output = %q", err, raw)
	}
	return record
}
