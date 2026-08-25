package mcp

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	modelmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	projectapplication "fixthe/backend/internal/modules/projects/application"
	projectdomain "fixthe/backend/internal/modules/projects/domain"
	"fixthe/backend/internal/modules/remediation/domain"
)

type testSourceLoader struct {
	config SourceConfig
}

func (l *testSourceLoader) LoadMCPSource(context.Context, string, string) (SourceConfig, error) {
	return l.config, nil
}

func newTestRuntime(loader SourceLoader) *Runtime {
	return &Runtime{
		sources: loader, httpClient: &http.Client{Timeout: 2 * time.Second},
		timeout: 2 * time.Second, maxTools: 8, maxResultBytes: 4096,
		sessionTTL: time.Minute, now: time.Now, sessions: make(map[string]*session),
	}
}

func TestParseSourceConfigAcceptsPersistedPrototypeFields(t *testing.T) {
	config, err := ParseSourceConfig("project-1", "source-1", nil, json.RawMessage(`{
		"schemaVersion":1,
		"endpoint":"https://mcp.example.test/tools",
		"transport":"http",
		"headers":{"X-Tenant":"payments"},
		"evidenceProfile":"default",
		"queryScope":"logs"
	}`))
	if err != nil {
		t.Fatalf("ParseSourceConfig() error = %v", err)
	}
	if config.Endpoint == "" || config.Headers["X-Tenant"] != "payments" {
		t.Fatalf("unexpected parsed config: %#v", config)
	}
}

func TestRuntimeRejectsMissingRunIdentity(t *testing.T) {
	runtime := newTestRuntime(&testSourceLoader{})
	_, err := runtime.sessionFor(context.Background(), domain.DynamicToolScope{ProjectID: "project", SourceID: "source"})
	if err == nil || !strings.Contains(err.Error(), "invalid_configuration") {
		t.Fatalf("sessionFor() error = %v, want invalid_configuration", err)
	}
}

func TestRuntimeCleansExpiredSession(t *testing.T) {
	var cleaned atomic.Bool
	now := time.Now()
	runtime := &Runtime{
		sessionTTL: time.Minute, now: func() time.Time { return now },
		sessions: map[string]*session{
			"run:abandoned": {key: "run:abandoned", lastUsed: now.Add(-2 * time.Minute), cleanup: func() { cleaned.Store(true) }},
		},
	}
	runtime.cleanupExpiredSessions()
	if len(runtime.sessions) != 0 || !cleaned.Load() {
		t.Fatalf("expired session was not cleaned: sessions=%d cleaned=%t", len(runtime.sessions), cleaned.Load())
	}
}

func TestRuntimeDoesNotReuseSessionAcrossSourceIdentity(t *testing.T) {
	now := time.Now()
	runtime := newTestRuntime(&testSourceLoader{})
	runtime.sessions["run:run-1"] = &session{
		key: "run:run-1", projectID: "project-1", sourceID: "source-1", version: 4,
		lastUsed: now, tools: map[string]struct{}{},
	}
	runtime.now = func() time.Time { return now }

	for _, scope := range []domain.DynamicToolScope{
		{RunID: "run-1", ProjectID: "project-2", SourceID: "source-1", SourceVersion: 4},
		{RunID: "run-1", ProjectID: "project-1", SourceID: "source-2", SourceVersion: 4},
		{RunID: "run-1", ProjectID: "project-1", SourceID: "source-1", SourceVersion: 5},
	} {
		_, err := runtime.sessionFor(context.Background(), scope)
		if err == nil || !strings.Contains(err.Error(), "invalid_configuration") {
			t.Fatalf("sessionFor(%+v) error = %v, want invalid_configuration", scope, err)
		}
	}
}

func TestRuntimeRejectsChangedSourceVersionBeforeTransport(t *testing.T) {
	runtime := newTestRuntime(&testSourceLoader{config: SourceConfig{
		ProjectID: "project-1", SourceID: "source-1", Version: 5,
		Transport: "streamable_http", Endpoint: "http://127.0.0.1:1",
	}})
	_, err := runtime.sessionFor(context.Background(), domain.DynamicToolScope{
		RunID: "run-1", ProjectID: "project-1", SourceID: "source-1", SourceVersion: 4,
	})
	if err == nil || !strings.Contains(err.Error(), "invalid_configuration") {
		t.Fatalf("sessionFor() error = %v, want invalid_configuration", err)
	}
}

func TestRuntimeDiscoverCallAndCloseRedactsResult(t *testing.T) {
	server := modelmcp.NewServer(&modelmcp.Implementation{Name: "fixture-server", Version: "1"}, nil)
	server.AddTool(&modelmcp.Tool{
		Name:        "query_errors",
		Description: "query bounded errors",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(context.Context, *modelmcp.CallToolRequest) (*modelmcp.CallToolResult, error) {
		return &modelmcp.CallToolResult{
			Content:           []modelmcp.Content{&modelmcp.TextContent{Text: "authorization: Bearer super-secret-token-12345678"}},
			StructuredContent: map[string]any{"message": "ok", "api_key": "sk-fixture-secret-12345678"},
		}, nil
	})
	handler := modelmcp.NewStreamableHTTPHandler(func(*http.Request) *modelmcp.Server { return server }, &modelmcp.StreamableHTTPOptions{JSONResponse: true})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("sandbox does not permit a local TCP fixture: %v", err)
	}
	httpServer := &http.Server{Handler: handler}
	go func() { _ = httpServer.Serve(listener) }()
	defer httpServer.Shutdown(context.Background())

	loader := &testSourceLoader{config: SourceConfig{
		ProjectID: "project-1", SourceID: "source-1", Version: 4, Endpoint: "http://" + listener.Addr().String(), Transport: "streamable_http",
	}}
	runtime := newTestRuntime(loader)
	scope := domain.DynamicToolScope{RunID: "run-1", ProjectID: "project-1", SourceID: "source-1", SourceVersion: 4}
	catalog, err := runtime.Discover(context.Background(), scope)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(catalog.Tools) != 1 || catalog.Tools[0].Name != "query_errors" {
		t.Fatalf("unexpected catalog: %#v", catalog)
	}
	result, err := runtime.Call(context.Background(), scope, domain.DynamicToolCall{
		SourceID: scope.SourceID, ServerID: catalog.ServerID,
		Name: "query_errors", Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	encoded, _ := json.Marshal(result.Payload)
	text := string(encoded)
	for _, secret := range []string{"super-secret-token-12345678", "sk-fixture-secret-12345678"} {
		if strings.Contains(text, secret) {
			t.Fatalf("MCP result leaked secret %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, "[redacted]") {
		t.Fatalf("MCP result was not redacted: %s", text)
	}
	if err := runtime.CloseRun(context.Background(), scope.RunID); err != nil {
		t.Fatalf("CloseRun() error = %v", err)
	}
	if len(runtime.sessions) != 0 {
		t.Fatalf("session remains after CloseRun: %d", len(runtime.sessions))
	}
}

func TestRuntimeDiscoversPaginatedToolsAndHonorsToolBound(t *testing.T) {
	server := modelmcp.NewServer(&modelmcp.Implementation{Name: "pagination-server", Version: "1"}, &modelmcp.ServerOptions{PageSize: 1})
	for _, name := range []string{"query_errors", "query_traces"} {
		toolName := name
		server.AddTool(&modelmcp.Tool{
			Name: toolName, Description: "bounded read",
			InputSchema: map[string]any{"type": "object", "additionalProperties": false},
		}, func(context.Context, *modelmcp.CallToolRequest) (*modelmcp.CallToolResult, error) {
			return &modelmcp.CallToolResult{Content: []modelmcp.Content{&modelmcp.TextContent{Text: toolName}}}, nil
		})
	}
	handler := modelmcp.NewStreamableHTTPHandler(func(*http.Request) *modelmcp.Server { return server }, &modelmcp.StreamableHTTPOptions{JSONResponse: true})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("sandbox does not permit a local TCP fixture: %v", err)
	}
	httpServer := &http.Server{Handler: handler}
	go func() { _ = httpServer.Serve(listener) }()
	defer httpServer.Shutdown(context.Background())

	loader := &testSourceLoader{config: SourceConfig{
		ProjectID: "project-1", SourceID: "source-1", Version: 7,
		Endpoint: "http://" + listener.Addr().String(), Transport: "streamable_http",
	}}
	runtime := newTestRuntime(loader)
	scope := domain.DynamicToolScope{RunID: "run-pagination", ProjectID: "project-1", SourceID: "source-1", SourceVersion: 7}
	catalog, err := runtime.Discover(context.Background(), scope)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(catalog.Tools) != 2 || catalog.Truncated || catalog.Hash == "" || catalog.Version != catalog.Hash {
		t.Fatalf("unexpected paginated discovery: %#v", catalog)
	}
	if err := runtime.CloseRun(context.Background(), scope.RunID); err != nil {
		t.Fatalf("CloseRun() error = %v", err)
	}

	bounded := newTestRuntime(loader)
	bounded.maxTools = 1
	limited, err := bounded.Discover(context.Background(), domain.DynamicToolScope{
		RunID: "run-limited", ProjectID: "project-1", SourceID: "source-1", SourceVersion: 7,
	})
	if err != nil {
		t.Fatalf("bounded Discover() error = %v", err)
	}
	if len(limited.Tools) != 1 || !limited.Truncated {
		t.Fatalf("bounded discovery = %#v, want one truncated tool", limited)
	}
}

func TestClassifyRuntimeErrorDoesNotExposeConnectorDetails(t *testing.T) {
	err := classifyRuntimeError(context.Background(), &testError{message: "HTTP 401 body contains bearer secret-token"}, "fallback")
	if err.Code != "authentication" || err.Retryable || strings.Contains(err.Message, "secret-token") {
		t.Fatalf("unsafe runtime error classification: %#v", err)
	}
}

type testError struct{ message string }

func (e *testError) Error() string { return e.message }

type ownershipSecretLoader struct {
	secret projectdomain.EncryptedSecret
}

func (l ownershipSecretLoader) GetEncryptedSecret(context.Context, string, string) (projectdomain.EncryptedSecret, error) {
	return l.secret, nil
}

type ownershipCipher struct{}

func (ownershipCipher) Encrypt(string, string, projectdomain.SecretKind, []byte) ([]byte, []byte, int32, error) {
	return nil, nil, 0, nil
}

func (ownershipCipher) Decrypt(string, string, projectdomain.SecretKind, []byte, []byte) ([]byte, error) {
	return []byte("plaintext"), nil
}

func (ownershipCipher) EncryptWebhookToken(string, string, []byte) ([]byte, []byte, error) {
	return nil, nil, nil
}

func (ownershipCipher) DecryptWebhookToken(string, string, []byte, []byte) ([]byte, error) {
	return nil, nil
}

func TestRuntimeRejectsCredentialOwnershipMismatch(t *testing.T) {
	runtime := &Runtime{
		secrets: ownershipSecretLoader{secret: projectdomain.EncryptedSecret{Secret: projectdomain.Secret{
			ID: "secret-elsewhere", ProjectID: "project-elsewhere", Kind: projectdomain.SecretHTTPBearer,
		}}},
		cipher: ownershipCipher{},
	}
	config := SourceConfig{ProjectID: "project-1", SourceID: "source-1", CredentialSecretID: "secret-1"}
	if _, err := runtime.resolveHeaders(context.Background(), config); err == nil || !strings.Contains(err.Error(), "invalid_configuration") {
		t.Fatalf("resolveHeaders() error = %v, want invalid_configuration", err)
	}
	config.Transport = "stdio"
	config.Command = "fixture"
	config.SecretEnv = map[string]string{"MCP_TOKEN": "secret-1"}
	if _, _, err := runtime.resolveEnvironment(context.Background(), config); err == nil || !strings.Contains(err.Error(), "invalid_configuration") {
		t.Fatalf("resolveEnvironment() error = %v, want invalid_configuration", err)
	}
}

var _ projectapplication.Cipher = ownershipCipher{}
