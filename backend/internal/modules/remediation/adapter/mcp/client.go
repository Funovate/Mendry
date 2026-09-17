package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	modelmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	projectapplication "mendry/backend/internal/modules/projects/application"
	projectdomain "mendry/backend/internal/modules/projects/domain"
	"mendry/backend/internal/modules/remediation/domain"
)

const (
	defaultTimeout        = 15 * time.Second
	defaultMaxTools       = 128
	defaultMaxPages       = 16
	defaultMaxResultSize  = 64 << 10
	defaultSessionTTL     = 10 * time.Minute
	maxSessionTTL         = 24 * time.Hour
	maxTransportRetries   = 2
	maxEndpointLength     = 2048
	maxCommandLength      = 2048
	maxArgumentLength     = 4096
	maxArgumentBytes      = 64 << 10
	maxSchemaBytes        = 64 << 10
	maxSchemaDepth        = 8
	maxIdentityLength     = 256
	maxEnvironmentEntries = 64
	maxEnvironmentValue   = 4096
)

var environmentNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var (
	mcpSecretTokenPattern      = regexp.MustCompile(`(?i)(sk-[A-Za-z0-9_-]{8,}|Bearer\s+[A-Za-z0-9._~+/=-]{8,})`)
	mcpSecretAssignmentPattern = regexp.MustCompile(`(?im)(\b(?:password|token|secret|authorization|api[-_]?key|credential|value)\b\s*[:=]\s*)(?:"[^"]*"|'[^']*'|[^\s,;]+)`)
)

// SourceConfig is the credential-free configuration needed to open one MCP
// source. SecretEnv values are credential IDs, never plaintext values.
type SourceConfig struct {
	ProjectID          string
	SourceID           string
	Version            int64
	Endpoint           string
	Transport          string
	Headers            map[string]string
	CredentialSecretID string
	Command            string
	Args               []string
	CWD                string
	Env                map[string]string
	SecretEnv          map[string]string
}

type SourceLoader interface {
	LoadMCPSource(context.Context, string, string) (SourceConfig, error)
}

type SecretLoader interface {
	GetEncryptedSecret(context.Context, string, string) (projectdomain.EncryptedSecret, error)
}

type Options struct {
	Sources          SourceLoader
	Secrets          SecretLoader
	Cipher           projectapplication.Cipher
	HTTPClient       *http.Client
	Timeout          time.Duration
	MaxTools         int
	MaxResultBytes   int64
	TerminateTimeout time.Duration
	SessionTTL       time.Duration
	Now              func() time.Time
	Logger           *slog.Logger
}

type Runtime struct {
	sources          SourceLoader
	secrets          SecretLoader
	cipher           projectapplication.Cipher
	httpClient       *http.Client
	timeout          time.Duration
	maxTools         int
	maxResultBytes   int64
	terminateTimeout time.Duration
	sessionTTL       time.Duration
	now              func() time.Time
	logger           *slog.Logger

	mu       sync.Mutex
	sessions map[string]*session
}

type session struct {
	client    *modelmcp.Client
	conn      *modelmcp.ClientSession
	key       string
	projectID string
	sourceID  string
	version   int64
	lastUsed  time.Time
	cleanup   func()
	mu        sync.RWMutex
	serverID  string
	tools     map[string]struct{}
	closeOnce sync.Once
	closeErr  error
}

func NewRuntime(options Options) (*Runtime, error) {
	if options.Sources == nil || options.Secrets == nil || options.Cipher == nil {
		return nil, fmt.Errorf("MCP runtime dependencies are required")
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	maxTools := options.MaxTools
	if maxTools <= 0 || maxTools > defaultMaxTools {
		maxTools = defaultMaxTools
	}
	maxResultBytes := options.MaxResultBytes
	if maxResultBytes <= 0 || maxResultBytes > defaultMaxResultSize {
		maxResultBytes = defaultMaxResultSize
	}
	terminate := options.TerminateTimeout
	if terminate <= 0 {
		terminate = 5 * time.Second
	}
	sessionTTL := options.SessionTTL
	if sessionTTL <= 0 || sessionTTL > maxSessionTTL {
		sessionTTL = defaultSessionTTL
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	return &Runtime{
		sources: options.Sources, secrets: options.Secrets, cipher: options.Cipher,
		httpClient: client, timeout: timeout, maxTools: maxTools,
		maxResultBytes: maxResultBytes, terminateTimeout: terminate,
		sessionTTL: sessionTTL, now: now,
		logger: options.Logger, sessions: make(map[string]*session),
	}, nil
}

var _ domain.DynamicToolRuntimePort = (*Runtime)(nil)

// ParseSourceConfig decodes the project source union without exposing the raw
// config beyond the trusted adapter boundary. The project domain validates the
// same union before persistence; this parser fails closed for stale/corrupt
// rows loaded directly by the runtime.
func ParseSourceConfig(projectID, sourceID string, credentialSecretID *string, raw json.RawMessage) (SourceConfig, error) {
	var value struct {
		SchemaVersion int               `json:"schemaVersion"`
		Endpoint      string            `json:"endpoint"`
		Transport     string            `json:"transport"`
		Headers       map[string]string `json:"headers"`
		// These fields belong to the persisted MCP prototype contract. They are
		// accepted for compatibility but are not used by the runtime.
		EvidenceProfile string            `json:"evidenceProfile"`
		QueryScope      string            `json:"queryScope"`
		Command         string            `json:"command"`
		Args            []string          `json:"args"`
		CWD             string            `json:"cwd"`
		Env             map[string]string `json:"env"`
		SecretEnv       map[string]string `json:"secretEnv"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return SourceConfig{}, fmt.Errorf("decode MCP source config: %w", err)
	}
	if value.SchemaVersion != 1 || strings.TrimSpace(projectID) == "" || strings.TrimSpace(sourceID) == "" {
		return SourceConfig{}, fmt.Errorf("MCP source config is invalid")
	}
	config := SourceConfig{
		ProjectID: projectID, SourceID: sourceID, Endpoint: strings.TrimSpace(value.Endpoint),
		Transport: strings.TrimSpace(value.Transport), Headers: cloneMap(value.Headers),
		Command: value.Command, Args: append([]string(nil), value.Args...), CWD: value.CWD,
		Env: cloneMap(value.Env), SecretEnv: cloneMap(value.SecretEnv),
	}
	if credentialSecretID != nil {
		config.CredentialSecretID = strings.TrimSpace(*credentialSecretID)
	}
	if err := validateSourceConfig(config); err != nil {
		return SourceConfig{}, err
	}
	return config, nil
}

func validateSourceConfig(config SourceConfig) error {
	switch config.Transport {
	case "http", "streamable_http", "sse":
		parsed, err := url.Parse(config.Endpoint)
		if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" || len(config.Endpoint) > maxEndpointLength {
			return fmt.Errorf("MCP endpoint is invalid")
		}
		if config.Command != "" || len(config.Args) > 0 || config.CWD != "" || len(config.Env) > 0 || len(config.SecretEnv) > 0 {
			return fmt.Errorf("remote MCP config contains stdio fields")
		}
	case "stdio":
		if config.Command == "" || len(config.Command) > maxCommandLength || strings.ContainsRune(config.Command, 0) {
			return fmt.Errorf("MCP stdio command is invalid")
		}
		if len(config.Args) > 128 || len(config.CWD) > maxCommandLength || strings.ContainsRune(config.CWD, 0) {
			return fmt.Errorf("MCP stdio process bounds are invalid")
		}
		for _, arg := range config.Args {
			if len(arg) > maxArgumentLength || strings.ContainsRune(arg, 0) {
				return fmt.Errorf("MCP stdio argument is invalid")
			}
		}
		if config.Endpoint != "" || len(config.Headers) > 0 || config.CredentialSecretID != "" {
			return fmt.Errorf("stdio MCP config contains remote fields")
		}
	default:
		return fmt.Errorf("MCP transport is unsupported")
	}
	if len(config.Env)+len(config.SecretEnv) > maxEnvironmentEntries {
		return fmt.Errorf("MCP environment is too large")
	}
	for name, value := range config.Env {
		if !environmentNamePattern.MatchString(name) || len(value) > maxEnvironmentValue || strings.ContainsRune(value, 0) {
			return fmt.Errorf("MCP environment entry is invalid")
		}
		if _, ok := config.SecretEnv[name]; ok {
			return fmt.Errorf("MCP environment entry is duplicated")
		}
	}
	for name, secretID := range config.SecretEnv {
		if !environmentNamePattern.MatchString(name) || strings.TrimSpace(secretID) == "" {
			return fmt.Errorf("MCP secret environment reference is invalid")
		}
	}
	for name, value := range config.Headers {
		if len(name) == 0 || len(name) > 80 || len(value) > 1000 || strings.ContainsAny(name+value, "\r\n") {
			return fmt.Errorf("MCP header is invalid")
		}
		if strings.EqualFold(name, "authorization") || strings.EqualFold(name, "cookie") {
			return fmt.Errorf("MCP credential header must use a secret reference")
		}
	}
	return nil
}

func (r *Runtime) Discover(ctx context.Context, scope domain.DynamicToolScope) (domain.DynamicToolCatalog, error) {
	operationContext, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	session, err := r.sessionFor(operationContext, scope)
	if err != nil {
		return domain.DynamicToolCatalog{}, err
	}
	defer r.touchSession(session.key)
	session.setDiscovered("", nil)
	result := session.conn.InitializeResult()
	if result == nil || result.ServerInfo == nil || !validMCPIdentity(result.ServerInfo.Name) {
		return domain.DynamicToolCatalog{}, &domain.ToolRuntimeError{Code: "invalid_response", Message: "MCP initialization returned no server identity"}
	}
	serverID := result.ServerInfo.Name
	tools := make([]domain.DynamicToolDefinition, 0)
	seen := make(map[string]struct{})
	var cursor string
	truncated := false
	for page := 0; page < defaultMaxPages && len(tools) < r.maxTools; page++ {
		pageResult, err := session.conn.ListTools(operationContext, &modelmcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return domain.DynamicToolCatalog{}, classifyRuntimeError(operationContext, err, "MCP tool discovery failed")
		}
		if pageResult == nil {
			return domain.DynamicToolCatalog{}, &domain.ToolRuntimeError{Code: "invalid_response", Message: "MCP tool discovery returned no page"}
		}
		for _, tool := range pageResult.Tools {
			if len(tools) >= r.maxTools {
				truncated = true
				break
			}
			if tool == nil {
				return domain.DynamicToolCatalog{}, &domain.ToolRuntimeError{Code: "invalid_response", Message: "MCP tool discovery returned an invalid tool"}
			}
			if !validMCPIdentity(tool.Name) {
				return domain.DynamicToolCatalog{}, &domain.ToolRuntimeError{Code: "invalid_response", Message: "MCP tool discovery returned an invalid tool name"}
			}
			if _, exists := seen[tool.Name]; exists {
				return domain.DynamicToolCatalog{}, &domain.ToolRuntimeError{Code: "invalid_response", Message: "MCP tool discovery returned a duplicate tool"}
			}
			seen[tool.Name] = struct{}{}
			schema, err := mapBoundedJSON(tool.InputSchema, maxSchemaBytes, maxSchemaDepth)
			if err != nil {
				return domain.DynamicToolCatalog{}, &domain.ToolRuntimeError{Code: "invalid_response", Message: "MCP tool discovery returned an invalid schema"}
			}
			var annotations map[string]interface{}
			if tool.Annotations != nil {
				annotations, err = mapBoundedJSON(tool.Annotations, maxSchemaBytes, maxSchemaDepth)
				if err != nil {
					return domain.DynamicToolCatalog{}, &domain.ToolRuntimeError{Code: "invalid_response", Message: "MCP tool discovery returned invalid annotations"}
				}
			}
			tools = append(tools, domain.DynamicToolDefinition{
				SourceID: scope.SourceID, ServerID: serverID, Name: tool.Name,
				Description: tool.Description, InputSchema: schema, Annotations: annotations,
			})
		}
		cursor = pageResult.NextCursor
		if len(tools) >= r.maxTools {
			truncated = cursor != ""
			cursor = ""
		}
		if cursor == "" {
			break
		}
	}
	if cursor != "" {
		return domain.DynamicToolCatalog{}, &domain.ToolRuntimeError{Code: "invalid_response", Message: "MCP tool discovery exceeded its page bound"}
	}
	allowed := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		allowed[tool.Name] = struct{}{}
	}
	session.setDiscovered(serverID, allowed)
	hash := catalogVersion(serverID, tools, truncated)
	return domain.DynamicToolCatalog{
		SourceID: scope.SourceID, ServerID: serverID,
		Version: hash, Hash: hash,
		Tools: tools, Truncated: truncated,
	}, nil
}

func (r *Runtime) Call(ctx context.Context, scope domain.DynamicToolScope, call domain.DynamicToolCall) (domain.DynamicToolResult, error) {
	if strings.TrimSpace(call.SourceID) != strings.TrimSpace(scope.SourceID) ||
		!validMCPIdentity(call.ServerID) || !validMCPIdentity(call.Name) {
		return domain.DynamicToolResult{}, &domain.ToolRuntimeError{Code: "tool_unavailable", Message: "MCP tool is not available for this source"}
	}
	if err := validateCallArguments(call.Arguments); err != nil {
		return domain.DynamicToolResult{}, err
	}
	operationContext, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	session, err := r.sessionFor(operationContext, scope)
	if err != nil {
		return domain.DynamicToolResult{}, err
	}
	defer r.touchSession(session.key)
	if !session.allows(call.ServerID, call.Name) {
		return domain.DynamicToolResult{}, &domain.ToolRuntimeError{Code: "tool_unavailable", Message: "MCP tool was not returned by discovery"}
	}
	result, err := session.conn.CallTool(operationContext, &modelmcp.CallToolParams{Name: call.Name, Arguments: call.Arguments})
	if err != nil {
		return domain.DynamicToolResult{}, classifyRuntimeError(operationContext, err, "MCP tool call failed")
	}
	if result == nil {
		return domain.DynamicToolResult{}, &domain.ToolRuntimeError{Code: "invalid_response", Message: "MCP tool returned no result"}
	}
	payload := make([]interface{}, 0, len(result.Content))
	for _, content := range result.Content {
		switch value := content.(type) {
		case *modelmcp.TextContent:
			payload = append(payload, map[string]interface{}{"type": "text", "text": value.Text})
		default:
			// Binary, audio, image, resources, and links are not forwarded in
			// this slice. The marker lets the model choose another read path.
			payload = append(payload, map[string]interface{}{"type": "unsupported_content"})
		}
	}
	if result.StructuredContent != nil {
		structured, err := normalizeStructuredContent(result.StructuredContent)
		if err != nil {
			return domain.DynamicToolResult{}, &domain.ToolRuntimeError{Code: "invalid_response", Message: "MCP structured result could not be decoded"}
		}
		payload = append(payload, map[string]interface{}{
			"type": "structured", "value": structured,
		})
	}
	value := sanitizeValue(payload)
	encoded, err := json.Marshal(value)
	if err != nil {
		return domain.DynamicToolResult{}, &domain.ToolRuntimeError{Code: "invalid_response", Message: "MCP tool result could not be encoded"}
	}
	truncated := false
	if int64(len(encoded)) > r.maxResultBytes {
		truncated = true
		value = map[string]interface{}{
			"truncated": true,
			"bytes":     len(encoded),
			"preview":   boundedString(string(encoded), r.maxResultBytes),
		}
		encoded, _ = json.Marshal(value)
	}
	if result.IsError {
		return domain.DynamicToolResult{Payload: value, BytesRetrieved: int64(len(encoded)), Truncated: truncated},
			&domain.ToolRuntimeError{Code: "remote_execution", Message: "MCP tool reported an execution error"}
	}
	return domain.DynamicToolResult{Payload: value, BytesRetrieved: int64(len(encoded)), Truncated: truncated}, nil
}

func (r *Runtime) CloseRun(_ context.Context, runID string) error {
	if runID == "" {
		return nil
	}
	r.mu.Lock()
	current := r.sessions[sessionKeyForRun(runID)]
	delete(r.sessions, sessionKeyForRun(runID))
	r.mu.Unlock()
	if current == nil {
		return nil
	}
	if err := current.close(); err != nil {
		return &domain.ToolRuntimeError{Code: "transport", Retryable: true, Message: "MCP session close failed"}
	}
	return nil
}

func (r *Runtime) sessionFor(ctx context.Context, scope domain.DynamicToolScope) (*session, error) {
	if !validMCPIdentity(scope.RunID) || !validMCPIdentity(scope.ProjectID) || !validMCPIdentity(scope.SourceID) || scope.SourceVersion <= 0 {
		return nil, &domain.ToolRuntimeError{Code: "invalid_configuration", Message: "MCP run and source identity are required"}
	}
	r.cleanupExpiredSessions()
	key := sessionKeyForRun(scope.RunID)
	r.mu.Lock()
	current := r.sessions[key]
	if current != nil {
		if !current.matches(scope) {
			r.mu.Unlock()
			return nil, &domain.ToolRuntimeError{Code: "invalid_configuration", Message: "MCP run is already bound to another source configuration"}
		}
		current.lastUsed = r.now()
		r.mu.Unlock()
		return current, nil
	}
	r.mu.Unlock()

	config, err := r.sources.LoadMCPSource(ctx, scope.ProjectID, scope.SourceID)
	if err != nil {
		return nil, &domain.ToolRuntimeError{Code: "capability_unavailable", Message: "MCP source configuration is unavailable"}
	}
	if err := validateSourceConfig(config); err != nil {
		return nil, &domain.ToolRuntimeError{Code: "invalid_configuration", Message: "MCP source configuration is invalid"}
	}
	if config.ProjectID != scope.ProjectID || config.SourceID != scope.SourceID ||
		config.Version <= 0 || config.Version != scope.SourceVersion {
		return nil, &domain.ToolRuntimeError{Code: "invalid_configuration", Message: "MCP source identity or version changed during the run"}
	}
	transport, cleanup, err := r.transportFor(ctx, config)
	if err != nil {
		return nil, err
	}
	client := modelmcp.NewClient(&modelmcp.Implementation{Name: "mendry-remediation", Version: "1"}, nil)
	connectCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	conn, err := client.Connect(connectCtx, transport, nil)
	if err != nil {
		cleanup()
		return nil, classifyRuntimeError(ctx, err, "MCP initialization failed")
	}
	created := &session{
		client: client, conn: conn, key: key, projectID: scope.ProjectID, sourceID: scope.SourceID,
		version: scope.SourceVersion, lastUsed: r.now(), cleanup: cleanup, tools: make(map[string]struct{}),
	}
	r.mu.Lock()
	if existing := r.sessions[key]; existing != nil {
		if !existing.matches(scope) {
			r.mu.Unlock()
			_ = created.close()
			return nil, &domain.ToolRuntimeError{Code: "invalid_configuration", Message: "MCP run is already bound to another source configuration"}
		}
		r.mu.Unlock()
		_ = created.close()
		r.touchSession(key)
		return existing, nil
	}
	r.sessions[key] = created
	r.mu.Unlock()
	return created, nil
}

func (s *session) matches(scope domain.DynamicToolScope) bool {
	return s != nil && s.projectID == scope.ProjectID && s.sourceID == scope.SourceID && s.version == scope.SourceVersion
}

func (r *Runtime) transportFor(ctx context.Context, config SourceConfig) (modelmcp.Transport, func(), error) {
	cleanup := func() {}
	switch config.Transport {
	case "http", "streamable_http", "sse":
		headers, err := r.resolveHeaders(ctx, config)
		if err != nil {
			return nil, cleanup, err
		}
		client := cloneHTTPClient(r.httpClient, r.timeout, headers)
		cleanup = func() { clearStringMap(headers) }
		if config.Transport == "sse" {
			return &modelmcp.SSEClientTransport{Endpoint: config.Endpoint, HTTPClient: client}, cleanup, nil
		}
		return &modelmcp.StreamableClientTransport{
			Endpoint: config.Endpoint, HTTPClient: client,
			MaxRetries: maxTransportRetries, DisableStandaloneSSE: true,
		}, cleanup, nil
	case "stdio":
		env, clear, err := r.resolveEnvironment(ctx, config)
		if err != nil {
			return nil, cleanup, err
		}
		command := exec.Command(config.Command, config.Args...)
		command.Dir = config.CWD
		// Do not inherit the worker environment: it may contain unrelated
		// provider or deployment credentials. The source configuration is the
		// complete environment contract for the child process.
		command.Env = append([]string(nil), env...)
		commandEnv := command.Env
		cleanup = func() {
			clearStringSlice(commandEnv)
			command.Env = nil
			for i := range env {
				env[i] = ""
			}
			clear()
		}
		return &modelmcp.CommandTransport{Command: command, TerminateDuration: r.terminateTimeout}, cleanup, nil
	default:
		return nil, cleanup, &domain.ToolRuntimeError{Code: "capability_unavailable", Message: "MCP transport is unavailable"}
	}
}

func (r *Runtime) resolveHeaders(ctx context.Context, config SourceConfig) (map[string]string, error) {
	headers := cloneMap(config.Headers)
	if config.CredentialSecretID == "" {
		return headers, nil
	}
	encrypted, err := r.secrets.GetEncryptedSecret(ctx, config.ProjectID, config.CredentialSecretID)
	if err != nil {
		return nil, &domain.ToolRuntimeError{Code: "authentication", Message: "MCP credential is unavailable"}
	}
	if err := validateSecretOwnership(encrypted, config.ProjectID, config.CredentialSecretID); err != nil {
		return nil, err
	}
	if encrypted.Kind != projectdomain.SecretHTTPBearer {
		return nil, &domain.ToolRuntimeError{Code: "invalid_configuration", Message: "MCP credential kind is unsupported"}
	}
	plaintext, err := r.cipher.Decrypt(encrypted.ProjectID, encrypted.ID, encrypted.Kind, encrypted.Ciphertext, encrypted.Nonce)
	if err != nil {
		return nil, &domain.ToolRuntimeError{Code: "authentication", Message: "MCP credential could not be decrypted"}
	}
	defer clearBytes(plaintext)
	headers["Authorization"] = "Bearer " + string(plaintext)
	return headers, nil
}

func (r *Runtime) resolveEnvironment(ctx context.Context, config SourceConfig) ([]string, func(), error) {
	entries := make([]string, 0, len(config.Env)+len(config.SecretEnv))
	for name, value := range config.Env {
		entries = append(entries, name+"="+value)
	}
	plaintexts := make([][]byte, 0, len(config.SecretEnv))
	for name, secretID := range config.SecretEnv {
		encrypted, err := r.secrets.GetEncryptedSecret(ctx, config.ProjectID, secretID)
		if err != nil {
			clearSecretBytes(plaintexts)
			return nil, func() {}, &domain.ToolRuntimeError{Code: "authentication", Message: "MCP stdio credential is unavailable"}
		}
		if err := validateSecretOwnership(encrypted, config.ProjectID, secretID); err != nil {
			clearSecretBytes(plaintexts)
			return nil, func() {}, err
		}
		if encrypted.Kind != projectdomain.SecretHTTPHeader && encrypted.Kind != projectdomain.SecretHTTPBearer {
			clearSecretBytes(plaintexts)
			return nil, func() {}, &domain.ToolRuntimeError{Code: "invalid_configuration", Message: "MCP stdio credential kind is unsupported"}
		}
		plaintext, err := r.cipher.Decrypt(encrypted.ProjectID, encrypted.ID, encrypted.Kind, encrypted.Ciphertext, encrypted.Nonce)
		if err != nil {
			clearSecretBytes(plaintexts)
			return nil, func() {}, &domain.ToolRuntimeError{Code: "authentication", Message: "MCP stdio credential could not be decrypted"}
		}
		plaintexts = append(plaintexts, plaintext)
		entries = append(entries, name+"="+string(plaintext))
	}
	return entries, func() { clearSecretBytes(plaintexts) }, nil
}

func validateSecretOwnership(secret projectdomain.EncryptedSecret, projectID, secretID string) error {
	if secret.ProjectID != projectID || secret.ID != secretID {
		return &domain.ToolRuntimeError{Code: "invalid_configuration", Message: "MCP credential ownership is invalid"}
	}
	return nil
}

func (r *Runtime) cleanupExpiredSessions() {
	now := r.now()
	r.mu.Lock()
	expired := make([]*session, 0)
	for key, current := range r.sessions {
		if now.Sub(current.lastUsed) < r.sessionTTL {
			continue
		}
		delete(r.sessions, key)
		expired = append(expired, current)
	}
	r.mu.Unlock()
	for _, current := range expired {
		_ = current.close()
	}
}

func (r *Runtime) touchSession(key string) {
	r.mu.Lock()
	if current := r.sessions[key]; current != nil {
		current.lastUsed = r.now()
	}
	r.mu.Unlock()
}

func (s *session) setDiscovered(serverID string, tools map[string]struct{}) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.serverID = serverID
	s.tools = tools
	s.mu.Unlock()
}

func (s *session) allows(serverID, toolName string) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.serverID != serverID {
		return false
	}
	_, ok := s.tools[toolName]
	return ok
}

func sessionKeyForRun(runID string) string {
	return "run:" + strings.TrimSpace(runID)
}

func (s *session) close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		if s.conn != nil {
			s.closeErr = s.conn.Close()
		}
		if s.cleanup != nil {
			s.cleanup()
		}
	})
	return s.closeErr
}

type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t headerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	for name, value := range t.headers {
		clone.Header.Set(name, value)
	}
	return t.base.RoundTrip(clone)
}

func cloneHTTPClient(source *http.Client, timeout time.Duration, headers map[string]string) *http.Client {
	client := *source
	if client.Timeout <= 0 || client.Timeout > timeout {
		client.Timeout = timeout
	}
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client.Transport = headerTransport{base: base, headers: headers}
	return &client
}

func mapBoundedJSON(value any, maxBytes, maxDepth int) (map[string]interface{}, error) {
	if value == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(encoded) > maxBytes {
		return nil, fmt.Errorf("JSON value exceeds bound")
	}
	var result map[string]interface{}
	if err := json.Unmarshal(encoded, &result); err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("JSON value must be an object")
	}
	if err := validateJSONTree(result, 0, maxDepth); err != nil {
		return nil, err
	}
	return result, nil
}

func validateCallArguments(arguments map[string]interface{}) error {
	if arguments == nil {
		return &domain.ToolRuntimeError{Code: "invalid_arguments", Message: "MCP tool arguments must be an object"}
	}
	if _, err := mapBoundedJSON(arguments, maxArgumentBytes, maxSchemaDepth); err != nil {
		return &domain.ToolRuntimeError{Code: "invalid_arguments", Message: "MCP tool arguments exceed the runtime bound"}
	}
	return nil
}

func validateJSONTree(value any, depth, maxDepth int) error {
	if depth > maxDepth {
		return fmt.Errorf("JSON value nesting exceeds bound")
	}
	switch current := value.(type) {
	case map[string]interface{}:
		for key, child := range current {
			if len(key) > maxIdentityLength || strings.ContainsRune(key, 0) {
				return fmt.Errorf("JSON object key exceeds bound")
			}
			if err := validateJSONTree(child, depth+1, maxDepth); err != nil {
				return err
			}
		}
	case []interface{}:
		for _, child := range current {
			if err := validateJSONTree(child, depth+1, maxDepth); err != nil {
				return err
			}
		}
	case string, bool, float64, nil:
	default:
		return fmt.Errorf("JSON value contains unsupported data")
	}
	return nil
}

func validMCPIdentity(value string) bool {
	return strings.TrimSpace(value) != "" && len(value) <= maxIdentityLength &&
		!strings.ContainsRune(value, 0) && utf8.ValidString(value)
}

func catalogVersion(serverID string, tools []domain.DynamicToolDefinition, truncated bool) string {
	payload, _ := json.Marshal(struct {
		Server    string                         `json:"server"`
		Tools     []domain.DynamicToolDefinition `json:"tools"`
		Truncated bool                           `json:"truncated"`
	}{serverID, tools, truncated})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func sanitizeValue(value any) any {
	switch current := value.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{}, len(current))
		for key, item := range current {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "secret") || strings.Contains(lower, "token") || strings.Contains(lower, "password") || strings.Contains(lower, "authorization") || strings.Contains(lower, "api_key") || strings.Contains(lower, "apikey") {
				result[key] = "[redacted]"
				continue
			}
			result[key] = sanitizeValue(item)
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(current))
		for index, item := range current {
			result[index] = sanitizeValue(item)
		}
		return result
	case string:
		return boundedString(redactMCPText(current), defaultMaxResultSize)
	default:
		return value
	}
}

func classifyRuntimeError(ctx context.Context, err error, fallback string) *domain.ToolRuntimeError {
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return &domain.ToolRuntimeError{Code: "canceled", Message: "MCP operation was canceled"}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &domain.ToolRuntimeError{Code: "connector_timeout", Retryable: true, Message: "MCP operation timed out"}
	}
	if errors.Is(err, modelmcp.ErrConnectionClosed) || errors.Is(err, modelmcp.ErrSessionMissing) {
		return &domain.ToolRuntimeError{Code: "transport", Retryable: true, Message: "MCP session connection was interrupted"}
	}
	lower := strings.ToLower(errString(err))
	switch {
	case strings.Contains(lower, "401"), strings.Contains(lower, "unauthorized"), strings.Contains(lower, "authentication"):
		return &domain.ToolRuntimeError{Code: "authentication", Message: "MCP authentication failed"}
	case strings.Contains(lower, "403"), strings.Contains(lower, "forbidden"), strings.Contains(lower, "permission"):
		return &domain.ToolRuntimeError{Code: "authorization", Message: "MCP authorization failed"}
	case strings.Contains(lower, "429"), strings.Contains(lower, "rate limit"), strings.Contains(lower, "too many requests"):
		return &domain.ToolRuntimeError{Code: "rate_limit", Retryable: true, Message: "MCP server rate limit reached"}
	case strings.Contains(lower, "404"), strings.Contains(lower, "not found"):
		return &domain.ToolRuntimeError{Code: "not_found", Message: "MCP endpoint or resource was not found"}
	case strings.Contains(lower, "400"), strings.Contains(lower, "invalid") || strings.Contains(lower, "malformed"):
		return &domain.ToolRuntimeError{Code: "invalid_response", Message: "MCP server returned an invalid response"}
	}
	return &domain.ToolRuntimeError{Code: "transport", Retryable: true, Message: fallback}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func cloneMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func clearSecretBytes(values [][]byte) {
	for _, value := range values {
		clearBytes(value)
	}
}

func boundedString(value string, limit int64) string {
	if limit <= 0 {
		return ""
	}
	if int64(len(value)) <= limit {
		return value
	}
	end := int(limit)
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
}

func normalizeStructuredContent(value any) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var normalized any
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		return nil, err
	}
	return sanitizeValue(normalized), nil
}

func redactMCPText(value string) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	value = mcpSecretTokenPattern.ReplaceAllString(value, "[redacted]")
	return mcpSecretAssignmentPattern.ReplaceAllString(value, "${1}[redacted]")
}

func clearStringSlice(values []string) {
	for index := range values {
		values[index] = ""
	}
}

func clearStringMap(values map[string]string) {
	for key := range values {
		values[key] = ""
	}
}
