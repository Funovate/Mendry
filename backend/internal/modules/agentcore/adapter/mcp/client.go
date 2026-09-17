package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	modelmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"mendry/backend/internal/modules/agentcore/domain"
)

const (
	defaultTimeout       = 15 * time.Second
	defaultMaxTools      = 128
	defaultMaxPages      = 16
	defaultMaxResultSize = 64 << 10
	defaultTerminateTime = 5 * time.Second
	maxEndpointLength    = 2048
	maxCommandLength     = 2048
	maxArgumentLength    = 4096
	maxArgumentBytes     = 64 << 10
	maxSchemaBytes       = 64 << 10
	maxSchemaDepth       = 8
	maxIdentityLength    = 256
	maxEnvironment       = 64
	maxEnvironmentValue  = 4096
	maxDescriptionBytes  = 4096
)

var environmentNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Binding 是 composition 注入的完整、可信 MCP 连接配置。
// Env 的值是最终传给 stdio 子进程的值；本包不调用 os.Getenv，也不继承当前进程环境。
type Binding struct {
	ID        string
	Version   int64
	ServerID  string
	Transport string
	Endpoint  string
	Headers   map[string]string
	// AuthToken is a trusted bearer token injected by composition for HTTP transports.
	// It is never read from MCP arguments or event data.
	AuthToken          []byte
	Command            string
	Args               []string
	CWD                string
	Env                map[string]string
	Timeout            time.Duration
	MaxTools           int
	MaxResultBytes     int64
	TerminateTimeout   time.Duration
	DisableHTTPRetries bool
}

// ToolBinding 是把 MCP server tool 映射到 trusted Agent Core capability 的配置。
// MCP annotations 只能作为描述输入，不能改变 Effect。
type ToolBinding struct {
	ServerName  string
	Version     string
	Name        string
	Description string
	Parameters  map[string]any
	Effect      domain.ToolEffect
}

// ToolBindingConfig 用于根据 discovery 结果决定暴露哪些工具。
type ToolBindingConfig struct {
	// Name is the trusted logical Agent Core capability name.
	Name string
	// RemoteName is the discovered MCP name. When empty, Name is used.
	RemoteName  string
	Version     string
	Effect      domain.ToolEffect
	Description string
	Artifact    *ArtifactBinding
}

// ArtifactBinding describes a trusted projection of a successful structured MCP result.
// It is composition metadata; the remote server and model cannot choose it.
type ArtifactBinding struct {
	Type                    string
	SchemaVersion           string
	Provenance              domain.ArtifactProvenance
	ExternalIDField         string
	SubjectExternalIDField  string
	SubjectArtifactIDField  string
	VerificationStatusField string
}

// BindingLoader 在调用前返回 trusted 的完整 binding；binding ID 来自 composition，而非模型参数。
type BindingLoader interface {
	LoadBinding(context.Context, string) (Binding, error)
}

// StaticBindingLoader 为 local composition 提供 copy-safe 固定 binding。
type StaticBindingLoader struct{ Binding Binding }

// LoadBinding 返回固定 binding 的深拷贝。
func (l StaticBindingLoader) LoadBinding(_ context.Context, _ string) (Binding, error) {
	return cloneBinding(l.Binding), nil
}

// Options 构造 account-free MCP runtime。
type Options struct {
	Bindings         BindingLoader
	HTTPClient       *http.Client
	Timeout          time.Duration
	MaxTools         int
	MaxResultBytes   int64
	TerminateTimeout time.Duration
}

// Runtime 管理 trusted MCP sessions；一个 run 只绑定一个 source configuration。
type Runtime struct {
	bindings         BindingLoader
	httpClient       *http.Client
	timeout          time.Duration
	maxTools         int
	maxResultBytes   int64
	terminateTimeout time.Duration

	mu        sync.Mutex
	connectMu sync.Mutex
	sessions  map[string]*session
	closed    bool
}

type session struct {
	client  *modelmcp.Client
	conn    *modelmcp.ClientSession
	binding Binding
	cleanup func()
	server  string
	tools   map[string]modelmcp.Tool
	mu      sync.RWMutex
	close   sync.Once
	err     error
}

// Catalog 是 bounded discovery 结果；Tools 的顺序由 server discovery 排序后稳定化。
type Catalog struct {
	BindingID string
	ServerID  string
	Version   string
	Tools     []ToolBinding
	Truncated bool
}

// NewRuntime 构造不读取业务项目或环境的 MCP runtime。
func NewRuntime(options Options) (*Runtime, error) {
	if options.Bindings == nil {
		return nil, errors.New("MCP binding loader is required")
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	if timeout > 10*time.Minute {
		return nil, errors.New("MCP timeout exceeds the maximum")
	}
	maxTools := options.MaxTools
	if maxTools <= 0 {
		maxTools = defaultMaxTools
	}
	if maxTools > defaultMaxTools {
		return nil, errors.New("MCP tool bound exceeds the maximum")
	}
	maxResult := options.MaxResultBytes
	if maxResult <= 0 {
		maxResult = defaultMaxResultSize
	}
	if maxResult > defaultMaxResultSize {
		return nil, errors.New("MCP result bound exceeds the maximum")
	}
	terminate := options.TerminateTimeout
	if terminate <= 0 {
		terminate = defaultTerminateTime
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{}
	} else {
		clone := *client
		clone.Timeout = 0
		client = &clone
	}
	return &Runtime{bindings: options.Bindings, httpClient: client, timeout: timeout, maxTools: maxTools, maxResultBytes: maxResult, terminateTimeout: terminate, sessions: make(map[string]*session)}, nil
}

// Discover initializes the trusted binding and returns the server's bounded tool catalog.
// Discovery itself is safe to repeat by explicitly closing and reopening a session; calls are never retried here.
func (r *Runtime) Discover(ctx context.Context, bindingID string) (Catalog, error) {
	if r == nil || r.bindings == nil {
		return Catalog{}, errors.New("MCP runtime is not configured")
	}
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return Catalog{}, errors.New("MCP runtime is closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	operation, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	binding, err := r.loadBinding(operation, bindingID)
	if err != nil {
		return Catalog{}, err
	}
	defer clearBinding(&binding)
	key := bindingKey(binding)
	s, err := r.sessionFor(operation, binding, key)
	if err != nil {
		return Catalog{}, err
	}
	info := s.conn.InitializeResult()
	if info == nil || info.ServerInfo == nil || !validIdentity(info.ServerInfo.Name) {
		return Catalog{}, &RuntimeError{Code: "invalid_response", Message: "MCP initialization returned no server identity"}
	}
	if binding.ServerID != "" && info.ServerInfo.Name != binding.ServerID {
		return Catalog{}, &RuntimeError{Code: "identity_mismatch", Message: "MCP server identity did not match the trusted binding"}
	}
	tools := make([]modelmcp.Tool, 0, r.maxTools)
	var cursor string
	truncated := false
	for page := 0; page < defaultMaxPages && len(tools) < r.maxTools; page++ {
		result, callErr := s.conn.ListTools(operation, &modelmcp.ListToolsParams{Cursor: cursor})
		if callErr != nil {
			return Catalog{}, classifyError(operation, callErr)
		}
		if result == nil {
			return Catalog{}, &RuntimeError{Code: "invalid_response", Message: "MCP tool discovery returned no page"}
		}
		seenTools := make(map[string]struct{}, len(tools))
		for _, existing := range tools {
			seenTools[existing.Name] = struct{}{}
		}
		for _, tool := range result.Tools {
			if len(tools) >= r.maxTools {
				truncated = true
				break
			}
			if tool == nil || !validIdentity(tool.Name) || len(tool.Description) > maxDescriptionBytes {
				return Catalog{}, &RuntimeError{Code: "invalid_response", Message: "MCP tool discovery returned an invalid tool"}
			}
			if _, exists := seenTools[tool.Name]; exists {
				return Catalog{}, &RuntimeError{Code: "invalid_response", Message: "MCP tool discovery returned a duplicate tool"}
			}
			normalizedSchema, schemaErr := normalizeJSONSchema(tool.InputSchema)
			if schemaErr != nil {
				return Catalog{}, &RuntimeError{Code: "invalid_response", Message: "MCP tool discovery returned an invalid schema"}
			}
			tool.InputSchema = normalizedSchema
			seenTools[tool.Name] = struct{}{}
			tools = append(tools, *tool)
		}
		cursor = result.NextCursor
		if len(tools) >= r.maxTools {
			truncated = truncated || cursor != ""
			cursor = ""
		}
		if cursor == "" {
			break
		}
	}
	if cursor != "" {
		return Catalog{}, &RuntimeError{Code: "invalid_response", Message: "MCP tool discovery exceeded its page bound"}
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	s.setTools(info.ServerInfo.Name, tools)
	catalog := Catalog{BindingID: binding.ID, ServerID: info.ServerInfo.Name, Truncated: truncated}
	for _, tool := range tools {
		catalog.Tools = append(catalog.Tools, ToolBinding{ServerName: info.ServerInfo.Name, Name: tool.Name, Description: tool.Description, Parameters: cloneObject(tool.InputSchema)})
	}
	catalog.Version = catalogHash(catalog)
	return catalog, nil
}

// BindTools validates trusted mappings against a discovered catalog and returns executors.
func (r *Runtime) BindTools(catalog Catalog, configs []ToolBindingConfig) ([]BoundTool, error) {
	if r == nil {
		return nil, errors.New("MCP runtime is not configured")
	}
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return nil, errors.New("MCP runtime is closed")
	}
	if len(configs) > r.maxTools {
		return nil, errors.New("too many MCP tool bindings")
	}
	seen := make(map[string]struct{}, len(configs))
	bound := make([]BoundTool, 0, len(configs))
	for _, config := range configs {
		if !validIdentity(config.Name) || !validVersion(config.Version) || !validEffect(config.Effect) {
			return nil, errors.New("MCP tool binding identity or effect is invalid")
		}
		if _, exists := seen[config.Name]; exists {
			return nil, errors.New("MCP tool binding is duplicated")
		}
		seen[config.Name] = struct{}{}
		remoteName := config.RemoteName
		if remoteName == "" {
			remoteName = config.Name
		}
		if !validIdentity(remoteName) {
			return nil, errors.New("MCP remote tool name is invalid")
		}
		var discovered ToolBinding
		found := false
		for _, tool := range catalog.Tools {
			if tool.Name == remoteName && tool.ServerName == catalog.ServerID {
				discovered = tool
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("MCP tool %q was not discovered", config.Name)
		}
		description := config.Description
		if description == "" {
			description = discovered.Description
		}
		if len(description) > maxDescriptionBytes || !utf8.ValidString(description) || strings.ContainsRune(description, 0) {
			return nil, errors.New("MCP tool description is invalid")
		}
		parameters, schemaErr := normalizeJSONSchema(discovered.Parameters)
		if schemaErr != nil {
			return nil, fmt.Errorf("MCP schema for %q is invalid: %w", config.Name, schemaErr)
		}
		definition := domain.ToolDefinition{Name: config.Name, Version: config.Version, Description: description, Parameters: parameters, Effect: config.Effect}
		artifact := cloneArtifactBinding(config.Artifact)
		if err := validateArtifactBinding(artifact); err != nil {
			return nil, fmt.Errorf("MCP artifact binding for %q is invalid: %w", config.Name, err)
		}
		bound = append(bound, BoundTool{Definition: definition, Executor: &executor{runtime: r, bindingID: catalog.BindingID, serverID: catalog.ServerID, logicalName: config.Name, logicalVersion: config.Version, remoteName: remoteName, parameters: cloneObject(parameters), artifact: artifact}})
	}
	return bound, nil
}

// BoundTool is a trusted registry-ready definition and its MCP executor.
type BoundTool struct {
	Definition domain.ToolDefinition
	Executor   domain.ToolExecutor
}

// Execute performs exactly one MCP tools/call operation. No retry is performed for either read or write.
type executor struct {
	runtime        *Runtime
	bindingID      string
	serverID       string
	logicalName    string
	logicalVersion string
	remoteName     string
	parameters     map[string]any
	artifact       *ArtifactBinding
}

func (e *executor) Execute(ctx context.Context, call domain.ToolCall) (domain.ToolExecution, error) {
	if e == nil || e.runtime == nil {
		return domain.ToolExecution{}, errors.New("MCP executor is not configured")
	}
	if call.Name != e.logicalName || call.Version != e.logicalVersion {
		return domain.ToolExecution{}, &RuntimeError{Code: "tool_unavailable", Message: "MCP tool binding mismatch"}
	}
	if err := validateArguments(e.parameters, call.Arguments); err != nil {
		return domain.ToolExecution{}, &RuntimeError{Code: "invalid_arguments", Message: "MCP tool arguments are invalid"}
	}
	arguments, err := json.Marshal(call.Arguments)
	if err != nil || len(arguments) > maxArgumentBytes {
		return domain.ToolExecution{}, &RuntimeError{Code: "invalid_arguments", Message: "MCP tool arguments exceed the bound"}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	operation, cancel := context.WithTimeout(ctx, e.runtime.timeout)
	defer cancel()
	binding, err := e.runtime.loadBinding(operation, e.bindingID)
	if err != nil {
		return domain.ToolExecution{}, err
	}
	defer clearBinding(&binding)
	s, err := e.runtime.sessionFor(operation, binding, bindingKey(binding))
	if err != nil {
		return domain.ToolExecution{}, err
	}
	if !s.allows(e.serverID, e.remoteName) {
		return domain.ToolExecution{}, &RuntimeError{Code: "tool_unavailable", Message: "MCP tool was not returned by discovery"}
	}
	result, err := s.conn.CallTool(operation, &modelmcp.CallToolParams{Name: e.remoteName, Arguments: call.Arguments})
	if err != nil {
		return domain.ToolExecution{}, classifyError(operation, err)
	}
	if result == nil {
		return domain.ToolExecution{}, &RuntimeError{Code: "invalid_response", Message: "MCP tool returned no result"}
	}
	var structuredResult any
	value := make([]any, 0, len(result.Content)+1)
	for _, content := range result.Content {
		switch item := content.(type) {
		case *modelmcp.TextContent:
			value = append(value, map[string]any{"type": "text", "text": sanitizeText(item.Text)})
		default:
			value = append(value, map[string]any{"type": "unsupported_content"})
		}
	}
	if result.StructuredContent != nil {
		structured, structuredErr := boundedJSONValue(result.StructuredContent)
		if structuredErr != nil {
			return domain.ToolExecution{}, &RuntimeError{Code: "invalid_response", Message: "MCP structured result is invalid"}
		}
		structuredResult = structured
		value = append(value, map[string]any{"type": "structured", "value": sanitizeValue(structured)})
	}
	payload := any(value)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return domain.ToolExecution{}, &RuntimeError{Code: "invalid_response", Message: "MCP result could not be encoded"}
	}
	if int64(len(encoded)) > e.runtime.maxResultBytes {
		previewLimit := e.runtime.maxResultBytes / 4
		if previewLimit < 64 {
			previewLimit = 64
		}
		payload = map[string]any{"truncated": true, "bytes": len(encoded), "preview": boundedString(string(encoded), previewLimit)}
		encoded, _ = json.Marshal(payload)
		if int64(len(encoded)) > e.runtime.maxResultBytes {
			payload = map[string]any{"truncated": true, "bytes": len(encoded)}
			encoded, _ = json.Marshal(payload)
		}
	}
	execution := domain.ToolExecution{Output: payload, OutputBytes: int64(len(encoded))}
	if result.IsError {
		return execution, &RuntimeError{Code: "remote_execution", Message: "MCP tool reported an execution error"}
	}
	if e.artifact != nil {
		artifact, artifactErr := artifactFromResult(*e.artifact, structuredResult)
		if artifactErr != nil {
			return domain.ToolExecution{}, &RuntimeError{Code: "invalid_response", Message: "MCP structured result did not satisfy artifact binding"}
		}
		execution.Artifacts = []domain.Artifact{artifact}
	}
	return execution, nil
}

// Close closes all sessions and releases child processes/resources.
func (r *Runtime) Close(_ context.Context) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	sessions := make([]*session, 0, len(r.sessions))
	for key, current := range r.sessions {
		delete(r.sessions, key)
		sessions = append(sessions, current)
	}
	r.mu.Unlock()
	var first error
	for _, current := range sessions {
		if err := current.closeSession(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// CloseBinding closes the session belonging to one binding, useful for explicit restart/retry of discovery only.
func (r *Runtime) CloseBinding(_ context.Context, bindingID string) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	key := strings.TrimSpace(bindingID)
	current := r.sessions[key]
	delete(r.sessions, key)
	r.mu.Unlock()
	if current == nil {
		return nil
	}
	return current.closeSession()
}

func (r *Runtime) loadBinding(ctx context.Context, id string) (Binding, error) {
	if strings.TrimSpace(id) == "" || len(id) > maxIdentityLength {
		return Binding{}, errors.New("MCP binding ID is invalid")
	}
	binding, err := r.bindings.LoadBinding(ctx, id)
	if err != nil {
		return Binding{}, fmt.Errorf("load MCP binding: %w", err)
	}
	binding, err = normalizeBinding(binding)
	if err != nil {
		return Binding{}, fmt.Errorf("validate MCP binding: %w", err)
	}
	if binding.ID == "" {
		binding.ID = id
	}
	if binding.ID != id {
		return Binding{}, errors.New("MCP binding identity mismatch")
	}
	return binding, nil
}

func (r *Runtime) sessionFor(ctx context.Context, binding Binding, key string) (*session, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, errors.New("MCP runtime is closed")
	}
	if current := r.sessions[key]; current != nil {
		if !sameBinding(current.binding, binding) {
			r.mu.Unlock()
			return nil, errors.New("MCP binding changed during a session")
		}
		r.mu.Unlock()
		return current, nil
	}
	r.mu.Unlock()

	// Serialize connection creation. Without this second lock, concurrent
	// discovery/callers can start two child processes or HTTP sessions for the
	// same trusted binding before either one is published in sessions.
	r.connectMu.Lock()
	defer r.connectMu.Unlock()
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, errors.New("MCP runtime is closed")
	}
	if current := r.sessions[key]; current != nil {
		if !sameBinding(current.binding, binding) {
			r.mu.Unlock()
			return nil, errors.New("MCP binding changed during a session")
		}
		r.mu.Unlock()
		return current, nil
	}
	r.mu.Unlock()

	transport, cleanup, err := transportFor(binding, r.httpClient, r.terminateTimeout)
	if err != nil {
		return nil, err
	}
	client := modelmcp.NewClient(&modelmcp.Implementation{Name: "mendry-agentcore-local", Version: "v1"}, nil)
	connection, err := client.Connect(ctx, transport, nil)
	if err != nil {
		cleanup()
		return nil, classifyError(ctx, err)
	}
	created := &session{client: client, conn: connection, binding: cloneBinding(binding), cleanup: cleanup, tools: make(map[string]modelmcp.Tool)}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		_ = created.closeSession()
		return nil, errors.New("MCP runtime is closed")
	}
	r.sessions[key] = created
	r.mu.Unlock()
	return created, nil
}

type headerRoundTripper struct {
	base      http.RoundTripper
	headers   map[string]string
	authToken []byte
}

func (t headerRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	for name, value := range t.headers {
		clone.Header.Set(name, value)
	}
	if len(t.authToken) > 0 {
		clone.Header.Set("Authorization", "Bearer "+string(t.authToken))
	}
	return t.base.RoundTrip(clone)
}

func cloneHTTPClient(source *http.Client, timeout time.Duration, headers map[string]string, authToken []byte) (*http.Client, func()) {
	client := *source
	if timeout > 0 && (client.Timeout <= 0 || client.Timeout > timeout) {
		client.Timeout = timeout
	}
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	authCopy := append([]byte(nil), authToken...)
	client.Transport = headerRoundTripper{base: base, headers: cloneStringMap(headers), authToken: authCopy}
	return &client, func() {
		for index := range authCopy {
			authCopy[index] = 0
		}
	}
}

func transportFor(binding Binding, httpClient *http.Client, terminate time.Duration) (modelmcp.Transport, func(), error) {
	cleanup := func() {}
	switch binding.Transport {
	case "stdio":
		command := exec.Command(binding.Command, binding.Args...)
		command.Dir = binding.CWD
		command.Env = environmentEntries(binding.Env)
		childEnv := command.Env
		cleanup = func() {
			for i := range childEnv {
				childEnv[i] = ""
			}
			command.Env = nil
		}
		return &modelmcp.CommandTransport{Command: command, TerminateDuration: terminate}, cleanup, nil
	case "http", "streamable_http":
		client, cleanup := cloneHTTPClient(httpClient, binding.Timeout, binding.Headers, binding.AuthToken)
		return &modelmcp.StreamableClientTransport{Endpoint: binding.Endpoint, HTTPClient: client, MaxRetries: -1, DisableStandaloneSSE: true}, cleanup, nil
	default:
		return nil, cleanup, errors.New("MCP transport is unsupported")
	}
}

func normalizeBinding(binding Binding) (Binding, error) {
	binding.ID = strings.TrimSpace(binding.ID)
	binding.ServerID = strings.TrimSpace(binding.ServerID)
	binding.Transport = strings.TrimSpace(binding.Transport)
	if binding.Version <= 0 {
		return Binding{}, errors.New("MCP binding version is required")
	}
	if binding.Timeout <= 0 {
		binding.Timeout = defaultTimeout
	}
	if binding.MaxTools <= 0 {
		binding.MaxTools = defaultMaxTools
	}
	if binding.MaxResultBytes <= 0 {
		binding.MaxResultBytes = defaultMaxResultSize
	}
	if binding.TerminateTimeout <= 0 {
		binding.TerminateTimeout = defaultTerminateTime
	}
	binding.Endpoint = strings.TrimRight(strings.TrimSpace(binding.Endpoint), "/")
	binding.Command = strings.TrimSpace(binding.Command)
	binding.Headers = cloneStringMap(binding.Headers)
	binding.AuthToken = append([]byte(nil), binding.AuthToken...)
	binding.Env = cloneStringMap(binding.Env)
	binding.Args = append([]string(nil), binding.Args...)
	if err := validateBinding(binding); err != nil {
		return Binding{}, err
	}
	return binding, nil
}

func validateBinding(binding Binding) error {
	if binding.ID != "" && !validIdentity(binding.ID) || binding.ServerID != "" && !validIdentity(binding.ServerID) {
		return errors.New("MCP binding identity is invalid")
	}
	if binding.Timeout <= 0 || binding.Timeout > 10*time.Minute || binding.MaxTools <= 0 || binding.MaxTools > defaultMaxTools || binding.MaxResultBytes <= 0 || binding.MaxResultBytes > defaultMaxResultSize {
		return errors.New("MCP binding bounds are invalid")
	}
	switch binding.Transport {
	case "http", "streamable_http":
		parsed, err := url.Parse(binding.Endpoint)
		if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || len(binding.Endpoint) > maxEndpointLength || parsed.Scheme == "http" && !loopbackHost(parsed.Hostname()) {
			return errors.New("MCP endpoint is invalid")
		}
		if binding.Command != "" || len(binding.Args) > 0 || binding.CWD != "" || len(binding.Env) > 0 {
			return errors.New("remote MCP binding contains stdio fields")
		}
	case "stdio":
		if binding.Command == "" || len(binding.Command) > maxCommandLength || strings.ContainsRune(binding.Command, 0) || len(binding.Args) > 128 || len(binding.CWD) > maxCommandLength || strings.ContainsRune(binding.CWD, 0) {
			return errors.New("MCP stdio process bounds are invalid")
		}
		for _, arg := range binding.Args {
			if len(arg) > maxArgumentLength || strings.ContainsRune(arg, 0) {
				return errors.New("MCP stdio argument is invalid")
			}
		}
		if binding.Endpoint != "" || len(binding.Headers) > 0 || len(binding.AuthToken) > 0 {
			return errors.New("stdio MCP binding contains remote fields")
		}
	default:
		return errors.New("MCP transport is unsupported")
	}
	if len(binding.Env) > maxEnvironment {
		return errors.New("MCP environment is too large")
	}
	for name, value := range binding.Env {
		if !environmentNamePattern.MatchString(name) || len(value) > maxEnvironmentValue || strings.ContainsRune(value, 0) {
			return errors.New("MCP environment entry is invalid")
		}
	}
	if len(binding.AuthToken) > 4096 || strings.ContainsAny(string(binding.AuthToken), "\x00\r\n") {
		return errors.New("MCP authentication token is invalid")
	}
	for name, value := range binding.Headers {
		if name == "" || !utf8.ValidString(name) || !utf8.ValidString(value) || len(name) > 80 || len(value) > 1000 || strings.ContainsAny(name+value, "\r\n") || strings.EqualFold(name, "authorization") || strings.EqualFold(name, "cookie") {
			return errors.New("MCP header is invalid")
		}
	}
	return nil
}

func (s *session) setTools(server string, tools []modelmcp.Tool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.server = server
	clear(s.tools)
	for _, tool := range tools {
		s.tools[tool.Name] = tool
	}
}

func (s *session) tool(name string) (modelmcp.Tool, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tool, ok := s.tools[name]
	return tool, ok
}

func (s *session) allows(server, name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.tools[name]
	return ok && s.server == server
}

func (s *session) closeSession() error {
	if s == nil {
		return nil
	}
	s.close.Do(func() {
		if s.conn != nil {
			s.err = s.conn.Close()
		}
		if s.cleanup != nil {
			s.cleanup()
		}
		clearBinding(&s.binding)
	})
	return s.err
}

// RuntimeError is a bounded MCP adapter error. It never includes stderr, response bodies, or credentials.
type RuntimeError struct {
	Code      string
	Retryable bool
	Message   string
}

func (e *RuntimeError) Error() string {
	if e == nil {
		return "MCP runtime failure"
	}
	return e.Code + ": " + e.Message
}

func classifyError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return &RuntimeError{Code: "canceled", Message: "MCP operation was canceled"}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &RuntimeError{Code: "timeout", Retryable: true, Message: "MCP operation timed out"}
	}
	if errors.Is(err, modelmcp.ErrConnectionClosed) || errors.Is(err, modelmcp.ErrSessionMissing) {
		return &RuntimeError{Code: "transport", Message: "MCP session connection was interrupted"}
	}
	return &RuntimeError{Code: "transport", Message: "MCP operation failed"}
}

func catalogHash(catalog Catalog) string {
	encoded, _ := json.Marshal(catalog)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func bindingKey(binding Binding) string { return binding.ID }

func sameBinding(left, right Binding) bool {
	return left.ID == right.ID && left.Version == right.Version && left.ServerID == right.ServerID && left.Transport == right.Transport && left.Endpoint == right.Endpoint && left.Command == right.Command && left.CWD == right.CWD && left.Timeout == right.Timeout && left.MaxTools == right.MaxTools && left.MaxResultBytes == right.MaxResultBytes && left.TerminateTimeout == right.TerminateTimeout && left.DisableHTTPRetries == right.DisableHTTPRetries && equalStringMap(left.Headers, right.Headers) && equalBytes(left.AuthToken, right.AuthToken) && equalStringMap(left.Env, right.Env) && equalStrings(left.Args, right.Args)
}

func cloneBinding(binding Binding) Binding {
	binding.Headers = cloneStringMap(binding.Headers)
	binding.AuthToken = append([]byte(nil), binding.AuthToken...)
	binding.Env = cloneStringMap(binding.Env)
	binding.Args = append([]string(nil), binding.Args...)
	return binding
}

func clearBinding(binding *Binding) {
	if binding == nil {
		return
	}
	for index := range binding.AuthToken {
		binding.AuthToken[index] = 0
	}
	binding.AuthToken = nil
	for key, value := range binding.Env {
		binding.Env[key] = ""
		_ = value
	}
	binding.Env = nil
	for key := range binding.Headers {
		binding.Headers[key] = ""
	}
	binding.Headers = nil
	for index := range binding.Args {
		binding.Args[index] = ""
	}
	binding.Args = nil
}

func environmentEntries(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	entries := make([]string, 0, len(keys))
	for _, key := range keys {
		entries = append(entries, key+"="+values[key])
	}
	return entries
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func equalStringMap(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func cloneObject(value any) map[string]any {
	if value == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var result map[string]any
	if json.Unmarshal(encoded, &result) != nil || result == nil {
		return nil
	}
	return result
}

func validateJSONSchema(value any) error {
	_, err := normalizeJSONSchema(value)
	return err
}

func normalizeJSONSchema(value any) (map[string]any, error) {
	object := cloneObject(value)
	if object == nil {
		return nil, errors.New("MCP input schema must be an object")
	}
	if object["type"] != "object" {
		return nil, errors.New("MCP input schema root must be an object")
	}
	if err := normalizeSchemaTree(object, 0); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(object)
	if err != nil || len(encoded) > maxSchemaBytes {
		return nil, errors.New("MCP input schema exceeds the bound")
	}
	return object, nil
}

func validateArguments(schema map[string]any, arguments map[string]any) error {
	if schema == nil {
		return errors.New("MCP argument schema is missing")
	}
	if arguments == nil {
		arguments = map[string]any{}
	}
	return validateArgumentValue(schema, arguments, "arguments")
}

func validateArgumentValue(schema map[string]any, value any, path string) error {
	typeName, _ := schema["type"].(string)
	if !argumentMatchesType(typeName, value) {
		return fmt.Errorf("%s has invalid type", path)
	}
	if rawEnum, ok := schema["enum"].([]any); ok && len(rawEnum) > 0 {
		matched := false
		encodedValue, _ := json.Marshal(value)
		for _, candidate := range rawEnum {
			encodedCandidate, _ := json.Marshal(candidate)
			if string(encodedCandidate) == string(encodedValue) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s is not an allowed value", path)
		}
	}
	switch typed := value.(type) {
	case map[string]any:
		properties, _ := schema["properties"].(map[string]any)
		required, requiredErr := schemaRequired(schema["required"])
		if requiredErr != nil {
			return requiredErr
		}
		for _, name := range required {
			if _, exists := typed[name]; !exists {
				return fmt.Errorf("%s.%s is required", path, name)
			}
		}
		closed, _ := schema["additionalProperties"].(bool)
		for name, child := range typed {
			raw, exists := properties[name]
			if !exists {
				if closed {
					return fmt.Errorf("%s.%s is not allowed", path, name)
				}
				continue
			}
			childSchema, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("%s.%s schema is invalid", path, name)
			}
			if err := validateArgumentValue(childSchema, child, path+"."+name); err != nil {
				return err
			}
		}
	case []any:
		if maximum, ok := schemaInteger(schema["maxItems"]); ok && int64(len(typed)) > maximum {
			return fmt.Errorf("%s contains too many items", path)
		}
		if minimum, ok := schemaInteger(schema["minItems"]); ok && int64(len(typed)) < minimum {
			return fmt.Errorf("%s does not contain enough items", path)
		}
		items, _ := schema["items"].(map[string]any)
		for index, item := range typed {
			if err := validateArgumentValue(items, item, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	case string:
		if minimum, ok := schemaInteger(schema["minLength"]); ok && int64(len(typed)) < minimum {
			return fmt.Errorf("%s is too short", path)
		}
		if maximum, ok := schemaInteger(schema["maxLength"]); ok && int64(len(typed)) > maximum {
			return fmt.Errorf("%s is too long", path)
		}
	}
	if number, ok := argumentNumber(value); ok {
		if minimum, ok := argumentNumber(schema["minimum"]); ok && number < minimum {
			return fmt.Errorf("%s is below minimum", path)
		}
		if maximum, ok := argumentNumber(schema["maximum"]); ok && number > maximum {
			return fmt.Errorf("%s exceeds maximum", path)
		}
	}
	return nil
}

func argumentMatchesType(typeName string, value any) bool {
	switch typeName {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		_, ok := argumentNumber(value)
		return ok
	case "integer":
		number, ok := argumentNumber(value)
		return ok && number == float64(int64(number))
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	default:
		return false
	}
}

func argumentNumber(value any) (float64, bool) {
	var number float64
	switch typed := value.(type) {
	case int:
		number = float64(typed)
	case int64:
		number = float64(typed)
	case float64:
		number = typed
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		number = parsed
	default:
		return 0, false
	}
	return number, !math.IsNaN(number) && !math.IsInf(number, 0)
}

func schemaInteger(value any) (int64, bool) {
	number, ok := argumentNumber(value)
	return int64(number), ok && number >= 0 && number == float64(int64(number))
}

func schemaRequired(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	result := make([]string, 0)
	seen := make(map[string]struct{})
	appendName := func(raw any) error {
		name, ok := raw.(string)
		if !ok || name == "" || len(name) > maxIdentityLength || !utf8.ValidString(name) || strings.ContainsRune(name, 0) {
			return errors.New("MCP required property name is invalid")
		}
		if _, exists := seen[name]; exists {
			return errors.New("MCP required property is duplicated")
		}
		seen[name] = struct{}{}
		result = append(result, name)
		return nil
	}
	switch values := value.(type) {
	case []any:
		for _, raw := range values {
			if err := appendName(raw); err != nil {
				return nil, err
			}
		}
	case []string:
		for _, raw := range values {
			if err := appendName(raw); err != nil {
				return nil, err
			}
		}
	default:
		return nil, errors.New("MCP schema required must be an array")
	}
	return result, nil
}

func normalizeSchemaTree(object map[string]any, depth int) error {
	if depth > maxSchemaDepth {
		return errors.New("MCP schema nesting exceeds the bound")
	}
	typeName, ok := object["type"].(string)
	if !ok {
		return errors.New("MCP schema type is required")
	}
	switch typeName {
	case "object":
		if raw, exists := object["additionalProperties"]; exists && raw != false {
			return errors.New("MCP object schema must be closed")
		}
		object["additionalProperties"] = false
		properties, exists := object["properties"]
		if !exists {
			object["properties"] = map[string]any{}
			properties = object["properties"]
		}
		propertyMap, ok := properties.(map[string]any)
		if !ok {
			return errors.New("MCP object properties are invalid")
		}
		required, requiredErr := schemaRequired(object["required"])
		if requiredErr != nil {
			return requiredErr
		}
		for _, name := range required {
			if _, declared := propertyMap[name]; !declared {
				return errors.New("MCP required property is not declared")
			}
		}
		for name, child := range propertyMap {
			if name == "" || len(name) > maxIdentityLength || !utf8.ValidString(name) || strings.ContainsRune(name, 0) {
				return errors.New("MCP schema property name is invalid")
			}
			childObject, ok := child.(map[string]any)
			if !ok {
				return errors.New("MCP schema property is invalid")
			}
			if err := normalizeSchemaTree(childObject, depth+1); err != nil {
				return err
			}
		}
	case "array":
		items, ok := object["items"].(map[string]any)
		if !ok {
			return errors.New("MCP array items are required")
		}
		return normalizeSchemaTree(items, depth+1)
	case "string", "number", "integer", "boolean", "null":
	default:
		return errors.New("MCP schema type is unsupported")
	}
	return nil
}

func boundedJSONValue(value any) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > maxResultBytesGlobal {
		return nil, errors.New("JSON value exceeds bound")
	}
	var normalized any
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

const maxResultBytesGlobal = defaultMaxResultSize

func sanitizeValue(value any) any {
	switch current := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(current))
		for key, item := range current {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "secret") || strings.Contains(lower, "token") || strings.Contains(lower, "password") || strings.Contains(lower, "authorization") || strings.Contains(lower, "api_key") || strings.Contains(lower, "apikey") {
				result[key] = "[redacted]"
				continue
			}
			result[key] = sanitizeValue(item)
		}
		return result
	case []any:
		result := make([]any, len(current))
		for index, item := range current {
			result[index] = sanitizeValue(item)
		}
		return result
	case string:
		return sanitizeText(current)
	default:
		return value
	}
}

func sanitizeText(value string) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	value = strings.NewReplacer("sk-", "[redacted]", "Bearer ", "[redacted] ").Replace(value)
	return boundedString(value, maxResultBytesGlobal)
}

func boundedString(value string, limit int64) string {
	if limit <= 0 || int64(len(value)) <= limit {
		return value
	}
	cut := int(limit)
	for cut > 0 && !utf8.ValidString(value[:cut]) {
		cut--
	}
	return value[:cut]
}

func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validIdentity(value string) bool {
	return strings.TrimSpace(value) == value && strings.TrimSpace(value) != "" && len(value) <= maxIdentityLength && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

func validVersion(value string) bool {
	if len(value) < 2 || value[0] != 'v' || value[1] == '0' {
		return false
	}
	for _, r := range value[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validEffect(value domain.ToolEffect) bool {
	return value == domain.ToolEffectRead || value == domain.ToolEffectWrite
}

func cloneArtifactBinding(value *ArtifactBinding) *ArtifactBinding {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func validateArtifactBinding(value *ArtifactBinding) error {
	if value == nil {
		return nil
	}
	if strings.TrimSpace(value.Type) == "" || len(value.Type) > 256 || strings.TrimSpace(value.SchemaVersion) == "" || len(value.SchemaVersion) > 64 {
		return errors.New("artifact type and schema version are required")
	}
	if value.Provenance != domain.ProvenanceObserved && value.Provenance != domain.ProvenanceVerified {
		return errors.New("artifact provenance must be observed or verified")
	}
	fields := []string{value.ExternalIDField, value.SubjectExternalIDField, value.SubjectArtifactIDField, value.VerificationStatusField}
	for _, field := range fields {
		if len(field) > maxIdentityLength || strings.ContainsRune(field, 0) {
			return errors.New("artifact field path is invalid")
		}
	}
	if value.Provenance == domain.ProvenanceObserved && (value.SubjectExternalIDField != "" || value.SubjectArtifactIDField != "" || value.VerificationStatusField != "") {
		return errors.New("observed artifact cannot carry verification fields")
	}
	if value.Provenance == domain.ProvenanceVerified && (value.SubjectExternalIDField == "" && value.SubjectArtifactIDField == "" || value.VerificationStatusField == "") {
		return errors.New("verified artifact requires subject and status fields")
	}
	return nil
}

func artifactFromResult(binding ArtifactBinding, structured any) (domain.Artifact, error) {
	value, ok := structured.(map[string]any)
	if !ok || value == nil {
		return domain.Artifact{}, errors.New("structured result must be an object")
	}
	artifact := domain.Artifact{Type: binding.Type, SchemaVersion: binding.SchemaVersion, Data: sanitizeValue(value), Provenance: binding.Provenance}
	if binding.ExternalIDField != "" {
		externalID, ok := fieldString(value, binding.ExternalIDField)
		if !ok || !validIdentity(externalID) {
			return domain.Artifact{}, errors.New("artifact external ID is missing")
		}
		artifact.ExternalID = externalID
	}
	if binding.Provenance == domain.ProvenanceVerified {
		subject := &domain.VerificationSubject{}
		if binding.SubjectExternalIDField != "" {
			externalID, ok := fieldString(value, binding.SubjectExternalIDField)
			if !ok || !validIdentity(externalID) {
				return domain.Artifact{}, errors.New("verification subject external ID is missing")
			}
			subject.ExternalID = externalID
		}
		if binding.SubjectArtifactIDField != "" {
			artifactID, ok := fieldString(value, binding.SubjectArtifactIDField)
			if !ok || !validIdentity(artifactID) {
				return domain.Artifact{}, errors.New("verification subject artifact ID is missing")
			}
			subject.ArtifactID = artifactID
		}
		status := domain.VerificationPassed
		if binding.VerificationStatusField != "" {
			raw, ok := fieldString(value, binding.VerificationStatusField)
			if !ok {
				return domain.Artifact{}, errors.New("verification status is missing")
			}
			status = domain.VerificationStatus(raw)
		}
		if status != domain.VerificationPassed && status != domain.VerificationFailed {
			return domain.Artifact{}, errors.New("verification status is invalid")
		}
		artifact.Subject = subject
		artifact.VerificationStatus = status
	}
	if err := artifact.Validate(); err != nil {
		return domain.Artifact{}, err
	}
	return artifact, nil
}

func fieldString(value map[string]any, path string) (string, bool) {
	current := any(value)
	for _, part := range strings.Split(path, ".") {
		if part == "" {
			return "", false
		}
		object, ok := current.(map[string]any)
		if !ok {
			return "", false
		}
		current, ok = object[part]
		if !ok {
			return "", false
		}
	}
	text, ok := current.(string)
	return strings.TrimSpace(text), ok
}

var _ domain.ToolExecutor = (*executor)(nil)
