package observability

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"fixthe/backend/internal/platform/buildinfo"
)

func TestHTTPIdentityStripsUserinfoAndQuery(t *testing.T) {
	host, path := HTTPIdentity("https://user:secret@api.example.com:8443/v1/chat/completions?api_key=sk-test")
	if host != "api.example.com:8443" || path != "/v1/chat/completions" {
		t.Fatalf("identity = %q %q", host, path)
	}
}

func TestHTTPIdentityRejectsUnparseableURL(t *testing.T) {
	host, path := HTTPIdentity("://bad")
	if host != "invalid" || path != "invalid" {
		t.Fatalf("identity = %q %q", host, path)
	}
}

func TestClassifyOutboundUsesStableClasses(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		want   string
	}{
		{name: "canceled", err: context.Canceled, want: OutboundCanceled},
		{name: "deadline", err: context.DeadlineExceeded, want: OutboundTimeout},
		{name: "dns", err: &net.DNSError{Err: "no such host", Name: "api.example.com", IsNotFound: true}, want: OutboundDNS},
		{name: "tls", err: tls.RecordHeaderError{Msg: "bad record"}, want: OutboundTLS},
		{name: "timeout", err: timeoutError{}, want: OutboundTimeout},
		{name: "network", err: networkError{}, want: OutboundNetwork},
		{name: "http4xx", err: errors.New("openai returned status 401"), status: http.StatusUnauthorized, want: OutboundHTTP4xx},
		{name: "http5xx", err: errors.New("openai returned status 502"), status: http.StatusBadGateway, want: OutboundHTTP5xx},
		{name: "decode", err: errors.New("decode openai response"), status: http.StatusOK, want: OutboundDecode},
		{name: "internal", err: errors.New("unknown"), want: OutboundInternal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyOutbound(tc.err, tc.status); got != tc.want {
				t.Fatalf("ClassifyOutbound() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLogLLMRequestWritesJSONRecord(t *testing.T) {
	var output bytes.Buffer
	logger, err := NewLogger(LoggerOptions{
		Writer: &output, Level: "debug", Format: "json", Service: "fixthe-test", Environment: "test", Build: buildinfo.Current(),
	})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}

	LogLLMRequest(context.Background(), logger, LLMRequest{
		Operation: "chat.completions",
		Host:      "api.openai.com",
		Path:      "/v1/chat/completions",
		Model:     "gpt-5.6",
		Status:    http.StatusUnauthorized,
		Duration:  12 * time.Millisecond,
		Request:   []byte(`{"model":"gpt-5.6","messages":[{"role":"user","content":"hi"}]}`),
		Response:  []byte(`{"error":{"message":"Incorrect API key provided: sk-test-openai-key","type":"invalid_request_error"},"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`),
		Err:       errors.New("openai returned status 401"),
	})

	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; output = %q", err, output.String())
	}
	assertField(t, record, FieldEvent, EventLLMRequestCompleted)
	assertField(t, record, FieldComponent, "openai")
	assertField(t, record, FieldLLMOperation, "chat.completions")
	assertField(t, record, FieldHTTPHost, "api.openai.com")
	assertField(t, record, FieldHTTPPath, "/v1/chat/completions")
	assertField(t, record, FieldLLMModel, "gpt-5.6")
	assertField(t, record, FieldOutcome, "failure")
	assertField(t, record, FieldErrorClass, OutboundHTTP4xx)
	assertField(t, record, FieldHTTPStatus, float64(http.StatusUnauthorized))
	assertField(t, record, FieldDurationMS, float64(12))
	request, _ := record[FieldHTTPRequest].(string)
	if !strings.Contains(request, `"content":"hi"`) || !strings.Contains(request, `"model":"gpt-5.6"`) {
		t.Fatalf("request = %#v", request)
	}
	response, _ := record[FieldHTTPResponse].(string)
	if !strings.Contains(response, "Incorrect API key provided") || !strings.Contains(response, "invalid_request_error") {
		t.Fatalf("response = %#v", response)
	}
	if !strings.Contains(response, `"prompt_tokens":11`) {
		t.Fatalf("usage tokens should remain visible: %#v", response)
	}
	if strings.Contains(output.String(), "sk-") {
		t.Fatalf("record leaked secret material: %s", output.String())
	}
}

func TestSnapshotHTTPPayloadRedactsSecretsAndTruncates(t *testing.T) {
	body, truncated := SnapshotHTTPPayload([]byte(`{"error":{"message":"Incorrect API key provided: sk-test-openai-key","api_key":"keep-field-hidden"}}`))
	if truncated {
		t.Fatal("small JSON should not truncate")
	}
	if strings.Contains(body, "sk-test") || strings.Contains(body, "keep-field-hidden") {
		t.Fatalf("snapshot leaked secret: %s", body)
	}
	if !strings.Contains(body, "Incorrect API key provided") || !strings.Contains(body, redactedPlaceholder) {
		t.Fatalf("snapshot = %s", body)
	}
	usage, _ := SnapshotHTTPPayload([]byte(`{"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`))
	if !strings.Contains(usage, `"prompt_tokens":11`) || strings.Contains(usage, redactedPlaceholder) {
		t.Fatalf("usage snapshot = %s", usage)
	}

	long := strings.Repeat("ok", outboundResponseMaxBytes)
	_, truncated = SnapshotHTTPPayload([]byte(long))
	if !truncated {
		t.Fatal("expected truncation")
	}
}

func TestLogLLMRequestSkipsNilLogger(t *testing.T) {
	LogLLMRequest(context.Background(), nil, LLMRequest{Operation: "chat.completions", Err: errors.New("boom")})
}

func TestConsoleHandlerFormatsLLMRequest(t *testing.T) {
	var output bytes.Buffer
	handler := newConsoleHandler(&output, slog.LevelDebug, false)
	record := slog.NewRecord(time.Date(2026, time.August, 18, 9, 52, 40, 0, time.UTC), slog.LevelWarn, "request completed", 0)
	record.AddAttrs(
		slog.String(FieldComponent, "openai"),
		slog.String(FieldLLMOperation, "chat.completions"),
		slog.String(FieldHTTPHost, "api.openai.com"),
		slog.String(FieldHTTPPath, "/v1/chat/completions"),
		slog.String(FieldLLMModel, "gpt-5.6"),
		slog.Int(FieldHTTPStatus, http.StatusUnauthorized),
		slog.Int64(FieldDurationMS, 12),
		slog.String(FieldOutcome, "failure"),
		slog.String(FieldErrorClass, OutboundHTTP4xx),
		slog.String(FieldHTTPRequest, `{"model":"gpt-5.6","messages":[{"role":"user","content":"hi"}]}`),
		slog.String(FieldHTTPResponse, `{"error":{"message":"Incorrect API key provided: [redacted]"}}`),
	)
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	want := "2026-08-18 09:52:40 WRN [openai] request completed operation=chat.completions host=api.openai.com path=/v1/chat/completions model=gpt-5.6 status=401 took=12ms outcome=failure error=http_4xx request=\"{\\\"model\\\":\\\"gpt-5.6\\\",\\\"messages\\\":[{\\\"role\\\":\\\"user\\\",\\\"content\\\":\\\"hi\\\"}]}\" response=\"{\\\"error\\\":{\\\"message\\\":\\\"Incorrect API key provided: [redacted]\\\"}}\"\n"
	if output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

type networkError struct{}

func (networkError) Error() string   { return "connection refused" }
func (networkError) Timeout() bool   { return false }
func (networkError) Temporary() bool { return true }

var (
	_ net.Error = timeoutError{}
	_ net.Error = networkError{}
)

func TestLogGitRequestWritesJSONRecord(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		var output bytes.Buffer
		logger, err := NewLogger(LoggerOptions{
			Writer: &output, Level: "debug", Format: "json", Service: "fixthe-test", Environment: "test", Build: buildinfo.Current(),
		})
		if err != nil {
			t.Fatalf("NewLogger() error = %v", err)
		}

		LogGitRequest(context.Background(), logger, GitRequest{
			Operation: "ls-remote",
			Host:      "github.com",
			Path:      "/org/repo.git",
			Duration:  312 * time.Millisecond,
			Request:   []byte("git ls-remote --symref --heads https://github.com/org/repo.git"),
			Response:  []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\trefs/heads/main\n"),
		})

		var record map[string]any
		if err := json.Unmarshal(output.Bytes(), &record); err != nil {
			t.Fatalf("json.Unmarshal() error = %v; output = %q", err, output.String())
		}
		assertField(t, record, FieldEvent, EventGitRequestCompleted)
		assertField(t, record, FieldComponent, "git")
		assertField(t, record, FieldGitOperation, "ls-remote")
		assertField(t, record, FieldHTTPHost, "github.com")
		assertField(t, record, FieldHTTPPath, "/org/repo.git")
		assertField(t, record, FieldOutcome, "success")
		assertField(t, record, FieldDurationMS, float64(312))
		request, _ := record[FieldHTTPRequest].(string)
		if !strings.Contains(request, "ls-remote") || !strings.Contains(request, "https://github.com/org/repo.git") {
			t.Fatalf("request = %#v", request)
		}
		response, _ := record[FieldHTTPResponse].(string)
		if !strings.Contains(response, "refs/heads/main") {
			t.Fatalf("response = %#v", response)
		}
		if _, ok := record[FieldHTTPStatus]; ok {
			t.Fatalf("git record must omit http.status: %#v", record[FieldHTTPStatus])
		}
		if _, ok := record[FieldLLMModel]; ok {
			t.Fatalf("git record must omit llm.model: %#v", record[FieldLLMModel])
		}
		if _, ok := record[FieldErrorClass]; ok {
			t.Fatalf("success record must omit error_class: %#v", record[FieldErrorClass])
		}
	})

	t.Run("failure", func(t *testing.T) {
		var output bytes.Buffer
		logger, err := NewLogger(LoggerOptions{
			Writer: &output, Level: "debug", Format: "json", Service: "fixthe-test", Environment: "test", Build: buildinfo.Current(),
		})
		if err != nil {
			t.Fatalf("NewLogger() error = %v", err)
		}

		LogGitRequest(context.Background(), logger, GitRequest{
			Operation: "ls-remote",
			Host:      "git.example.com",
			Path:      "/app.git",
			Duration:  40 * time.Millisecond,
			Request:   []byte("git ls-remote --symref --heads https://git.example.com/app.git"),
			Response:  []byte("fatal: Authentication failed for 'https://git:super-secret-token@git.example.com/app.git/'"),
			Err:       errors.New("exit status 128"),
		})

		var record map[string]any
		if err := json.Unmarshal(output.Bytes(), &record); err != nil {
			t.Fatalf("json.Unmarshal() error = %v; output = %q", err, output.String())
		}
		assertField(t, record, FieldEvent, EventGitRequestCompleted)
		assertField(t, record, FieldComponent, "git")
		assertField(t, record, FieldGitOperation, "ls-remote")
		assertField(t, record, FieldHTTPHost, "git.example.com")
		assertField(t, record, FieldHTTPPath, "/app.git")
		assertField(t, record, FieldOutcome, "failure")
		assertField(t, record, FieldErrorClass, OutboundCommand)
		if _, ok := record[FieldHTTPStatus]; ok {
			t.Fatalf("git record must omit http.status: %#v", record[FieldHTTPStatus])
		}
		request, _ := record[FieldHTTPRequest].(string)
		if !strings.Contains(request, "ls-remote") || !strings.Contains(request, "https://git.example.com/app.git") {
			t.Fatalf("request = %#v", request)
		}
		response, _ := record[FieldHTTPResponse].(string)
		if !strings.Contains(response, "Authentication failed") || !strings.Contains(response, redactedPlaceholder) {
			t.Fatalf("response = %#v", response)
		}
		if strings.Contains(output.String(), "super-secret-token") {
			t.Fatalf("record leaked git userinfo: %s", output.String())
		}
	})
}

func TestLogGitRequestSkipsNilLogger(t *testing.T) {
	LogGitRequest(context.Background(), nil, GitRequest{Operation: "ls-remote", Err: errors.New("boom")})
}

func TestClassifyGitUsesContextDeadline(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Millisecond))
	defer cancel()
	if got := ClassifyGit(ctx, errors.New("signal: killed")); got != OutboundTimeout {
		t.Fatalf("ClassifyGit() = %q, want %q", got, OutboundTimeout)
	}
	if got := ClassifyGit(context.Background(), errors.New("exit status 128")); got != OutboundCommand {
		t.Fatalf("ClassifyGit() = %q, want %q", got, OutboundCommand)
	}
	if got := ClassifyGit(context.Background(), nil); got != "" {
		t.Fatalf("ClassifyGit(nil) = %q", got)
	}
}

func TestSnapshotHTTPPayloadRedactsGitUserinfoAndPEM(t *testing.T) {
	body, truncated := SnapshotHTTPPayload([]byte("fatal: Authentication failed for 'https://git:super-secret-token@git.example.com/app.git/'\n-----BEGIN OPENSSH PRIVATE KEY-----\nsecret-key-material-must-not-leak\n-----END OPENSSH PRIVATE KEY-----\n"))
	if truncated {
		t.Fatal("small git snapshot should not truncate")
	}
	if strings.Contains(body, "super-secret-token") || strings.Contains(body, "git:super-secret-token") || strings.Contains(body, "secret-key-material-must-not-leak") {
		t.Fatalf("snapshot leaked secret: %s", body)
	}
	if !strings.Contains(body, redactedPlaceholder) {
		t.Fatalf("snapshot = %s", body)
	}
}

func TestConsoleHandlerFormatsGitRequest(t *testing.T) {
	var output bytes.Buffer
	handler := newConsoleHandler(&output, slog.LevelDebug, false)
	record := slog.NewRecord(time.Date(2026, time.August, 18, 9, 52, 40, 0, time.UTC), slog.LevelDebug, "request completed", 0)
	record.AddAttrs(
		slog.String(FieldComponent, "git"),
		slog.String(FieldGitOperation, "ls-remote"),
		slog.String(FieldHTTPHost, "github.com"),
		slog.String(FieldHTTPPath, "/org/repo.git"),
		slog.Int64(FieldDurationMS, 312),
		slog.String(FieldOutcome, "success"),
		slog.String(FieldHTTPRequest, "git ls-remote --symref --heads https://github.com/org/repo.git"),
		slog.String(FieldHTTPResponse, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\trefs/heads/main"),
	)
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	got := output.String()
	if !strings.Contains(got, "[git] request completed") {
		t.Fatalf("output = %q", got)
	}
	if !strings.Contains(got, "operation=ls-remote") || !strings.Contains(got, "host=github.com") || !strings.Contains(got, "path=/org/repo.git") {
		t.Fatalf("output = %q", got)
	}
	if !strings.Contains(got, "took=312ms") || !strings.Contains(got, "outcome=success") {
		t.Fatalf("output = %q", got)
	}
	if strings.Contains(got, "git.operation.name") || strings.Contains(got, "http.host") || strings.Contains(got, "http.path") {
		t.Fatalf("console leaked machine keys: %q", got)
	}
}
