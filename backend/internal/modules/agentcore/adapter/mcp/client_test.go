package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	modelmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"mendry/backend/internal/modules/agentcore/domain"
)

func mcpClosedSchema(properties map[string]any, required ...string) map[string]any {
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

func mcpRuntimeForBinding(t *testing.T, binding Binding) *Runtime {
	t.Helper()
	runtime, err := NewRuntime(Options{
		Bindings:         StaticBindingLoader{Binding: binding},
		Timeout:          2 * time.Second,
		TerminateTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func TestHTTPDiscoveryMappingValidationAuthAndArtifacts(t *testing.T) {
	var toolCalls atomic.Int32
	server := modelmcp.NewServer(&modelmcp.Implementation{Name: "test-server", Version: "v1"}, nil)
	server.AddTool(&modelmcp.Tool{
		Name:        "remote_read",
		Description: "read a file",
		InputSchema: mcpClosedSchema(map[string]any{"path": map[string]any{"type": "string", "maxLength": 32}}, "path"),
	}, func(_ context.Context, request *modelmcp.CallToolRequest) (*modelmcp.CallToolResult, error) {
		toolCalls.Add(1)
		var arguments map[string]any
		if err := json.Unmarshal(request.Params.Arguments, &arguments); err != nil {
			return nil, err
		}
		return &modelmcp.CallToolResult{
			Content: []modelmcp.Content{&modelmcp.TextContent{Text: "read " + arguments["path"].(string)}},
			StructuredContent: map[string]any{
				"id":     "observed-1",
				"path":   arguments["path"],
				"secret": "sk-should-not-be-retained",
			},
		}, nil
	})
	mcpHandler := modelmcp.NewStreamableHTTPHandler(func(*http.Request) *modelmcp.Server { return server }, &modelmcp.StreamableHTTPOptions{JSONResponse: true})
	var headerMu sync.Mutex
	var authHeaders []string
	var customHeaders []string
	httpServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		headerMu.Lock()
		authHeaders = append(authHeaders, request.Header.Get("Authorization"))
		customHeaders = append(customHeaders, request.Header.Get("X-Test"))
		headerMu.Unlock()
		mcpHandler.ServeHTTP(writer, request)
	}))
	defer httpServer.Close()

	binding := Binding{
		ID: "http-test", Version: 1, ServerID: "test-server", Transport: "http", Endpoint: httpServer.URL,
		Headers: map[string]string{"X-Test": "header-value"}, AuthToken: []byte("mcp-secret"),
	}
	runtime := mcpRuntimeForBinding(t, binding)
	defer runtime.Close(context.Background())

	catalog, err := runtime.Discover(context.Background(), binding.ID)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if catalog.ServerID != "test-server" || len(catalog.Tools) != 1 || catalog.Tools[0].Name != "remote_read" {
		t.Fatalf("catalog = %+v", catalog)
	}
	artifact := &ArtifactBinding{Type: "file.observation", SchemaVersion: "v1", Provenance: domain.ProvenanceObserved, ExternalIDField: "id"}
	bound, err := runtime.BindTools(catalog, []ToolBindingConfig{{
		Name: "workspace.read", RemoteName: "remote_read", Version: "v1", Effect: domain.ToolEffectRead,
		Artifact: artifact,
	}})
	if err != nil {
		t.Fatalf("bind tools: %v", err)
	}
	if len(bound) != 1 || bound[0].Definition.Name != "workspace.read" || bound[0].Definition.Version != "v1" {
		t.Fatalf("bound tools = %+v", bound)
	}

	_, err = bound[0].Executor.Execute(context.Background(), domain.ToolCall{Name: "workspace.read", Version: "v1", Arguments: map[string]any{}})
	var invalid *RuntimeError
	if !errors.As(err, &invalid) || invalid.Code != "invalid_arguments" {
		t.Fatalf("missing required argument error = %T %v", err, err)
	}
	if toolCalls.Load() != 0 {
		t.Fatalf("invalid arguments reached remote tool: %d calls", toolCalls.Load())
	}
	_, err = bound[0].Executor.Execute(context.Background(), domain.ToolCall{Name: "workspace.read", Version: "v2", Arguments: map[string]any{"path": "file.txt"}})
	if !errors.As(err, &invalid) || invalid.Code != "tool_unavailable" {
		t.Fatalf("version mismatch error = %T %v", err, err)
	}

	execution, err := bound[0].Executor.Execute(context.Background(), domain.ToolCall{Name: "workspace.read", Version: "v1", Arguments: map[string]any{"path": "file.txt"}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if toolCalls.Load() != 1 || len(execution.Artifacts) != 1 || execution.Artifacts[0].ExternalID != "observed-1" {
		t.Fatalf("execution = %+v calls=%d", execution, toolCalls.Load())
	}
	artifactJSON, _ := json.Marshal(execution.Artifacts[0].Data)
	if strings.Contains(string(artifactJSON), "sk-should-not-be-retained") || !strings.Contains(string(artifactJSON), "[redacted]") {
		t.Fatalf("artifact secret was not sanitized: %s", artifactJSON)
	}

	headerMu.Lock()
	defer headerMu.Unlock()
	if len(authHeaders) == 0 || len(authHeaders) != len(customHeaders) {
		t.Fatalf("captured headers = auth=%v custom=%v", authHeaders, customHeaders)
	}
	for index := range authHeaders {
		if authHeaders[index] != "Bearer mcp-secret" || customHeaders[index] != "header-value" {
			t.Fatalf("request %d headers = auth=%q custom=%q", index, authHeaders[index], customHeaders[index])
		}
	}
}

func TestMCPHTTPTransportDisablesRetries(t *testing.T) {
	transport, cleanup, err := transportFor(Binding{
		ID: "http-test", Version: 1, ServerID: "server", Transport: "http", Endpoint: "http://127.0.0.1:12345",
	}, &http.Client{}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	streamable, ok := transport.(*modelmcp.StreamableClientTransport)
	if !ok {
		t.Fatalf("transport type = %T", transport)
	}
	if streamable.MaxRetries != -1 {
		t.Fatalf("MCP retries = %d, want disabled", streamable.MaxRetries)
	}
}

func TestMCPStdioChild(t *testing.T) {
	if os.Getenv("MCP_STDIO_CHILD") != "1" {
		return
	}
	server := modelmcp.NewServer(&modelmcp.Implementation{Name: "stdio-test-server", Version: "v1"}, nil)
	server.AddTool(&modelmcp.Tool{Name: "echo_env", InputSchema: mcpClosedSchema(nil)}, func(_ context.Context, _ *modelmcp.CallToolRequest) (*modelmcp.CallToolResult, error) {
		_, visible := os.LookupEnv("VISIBLE_VALUE")
		_, pathPresent := os.LookupEnv("PATH")
		_, inherited := os.LookupEnv("UNCONFIGURED_VALUE")
		return &modelmcp.CallToolResult{StructuredContent: map[string]any{
			"visible":     visible,
			"pathPresent": pathPresent,
			"inherited":   inherited,
			"value":       os.Getenv("VISIBLE_VALUE"),
		}}, nil
	})
	if err := server.Run(context.Background(), &modelmcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("stdio child server: %v", err)
	}
}

func TestMCPStdioEnvironmentIsolatedAndSessionCloses(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binding := Binding{
		ID: "stdio-test", Version: 1, ServerID: "stdio-test-server", Transport: "stdio", Command: executable,
		Args: []string{"-test.run=^TestMCPStdioChild$"}, Env: map[string]string{
			"MCP_STDIO_CHILD": "1", "VISIBLE_VALUE": "configured",
			// race runtime 默认退出等待为 1 秒，会与 stdio 终止期限竞争。
			"GORACE": "atexit_sleep_ms=0",
		}, TerminateTimeout: time.Second,
	}
	runtime := mcpRuntimeForBinding(t, binding)
	catalog, err := runtime.Discover(context.Background(), binding.ID)
	if err != nil {
		runtime.Close(context.Background())
		t.Fatalf("stdio discover: %v", err)
	}
	if len(catalog.Tools) != 1 || catalog.Tools[0].Name != "echo_env" {
		runtime.Close(context.Background())
		t.Fatalf("stdio catalog = %+v", catalog)
	}
	bound, err := runtime.BindTools(catalog, []ToolBindingConfig{{Name: "env.inspect", RemoteName: "echo_env", Version: "v1", Effect: domain.ToolEffectRead}})
	if err != nil {
		runtime.Close(context.Background())
		t.Fatalf("stdio bind: %v", err)
	}
	execution, err := bound[0].Executor.Execute(context.Background(), domain.ToolCall{Name: "env.inspect", Version: "v1", Arguments: map[string]any{}})
	if err != nil {
		runtime.Close(context.Background())
		t.Fatalf("stdio execute: %v", err)
	}
	encoded, _ := json.Marshal(execution.Output)
	output := string(encoded)
	for _, expected := range []string{`"visible":true`, `"pathPresent":false`, `"inherited":false`, `"value":"configured"`} {
		if !strings.Contains(output, expected) {
			t.Fatalf("stdio environment output %q missing %s", output, expected)
		}
	}
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("close stdio runtime: %v", err)
	}
}

func TestMCPRejectsInvalidSchemasAndVerifiedArtifactBindings(t *testing.T) {
	if _, err := normalizeJSONSchema(map[string]any{
		"type": "object", "properties": map[string]any{"missing": map[string]any{"type": "string"}},
		"required": []any{"not-declared"},
	}); err == nil {
		t.Fatal("schema with undeclared required property was accepted")
	}
	if _, err := normalizeJSONSchema(map[string]any{"type": "object", "properties": map[string]any{"nested": map[string]any{"type": "object", "properties": map[string]any{}}}}); err != nil {
		t.Fatalf("valid nested schema was rejected: %v", err)
	}
	if err := validateArtifactBinding(&ArtifactBinding{Type: "verification", SchemaVersion: "v1", Provenance: domain.ProvenanceVerified}); err == nil {
		t.Fatal("verified artifact without subject/status was accepted")
	}
}
