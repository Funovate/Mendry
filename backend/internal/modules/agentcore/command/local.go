package command

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"mendry/backend/internal/modules/agentcore/adapter/file"
	"mendry/backend/internal/modules/agentcore/adapter/mcp"
	"mendry/backend/internal/modules/agentcore/adapter/openai"
	"mendry/backend/internal/modules/agentcore/application"
	"mendry/backend/internal/modules/agentcore/domain"
)

const (
	maxConfigBytes        = 1 << 20
	maxEventBytes         = 1 << 20
	maxOutputBytes        = 16 << 20
	defaultStateDir       = ".agentcore/runs"
	defaultProfile        = "report"
	defaultProfileVersion = "v1"
	defaultRunnerMaxSteps = 32
	maxJSONDepth          = 16
	maxJSONNodes          = 8192
)

// EnvLookup and OutputWriter are injected so command tests never need process-global environment or stdout.
type EnvLookup func(string) (string, bool)
type OutputWriter interface{ Write([]byte) (int, error) }

// Options describes the process boundary. Config and event are trusted only in
// the sense that config is operator-supplied; event still cannot select tools,
// credentials, effects, policy, or evaluator code.
type Options struct {
	ConfigPath string
	EventPath  string
	ConfigJSON []byte
	EventJSON  []byte
	StateDir   string
	// Resume requires an existing durable run; it never creates a new snapshot.
	Resume     bool
	Env        EnvLookup
	Stdout     OutputWriter
	Stderr     OutputWriter
	HTTPClient *http.Client
	Logger     *slog.Logger
	Now        func() time.Time
	NewID      func() string
}

// Config is the strict startup configuration accepted by the local command.
type Config struct {
	SchemaVersion int            `json:"schemaVersion"`
	Profile       ProfileConfig  `json:"profile"`
	Provider      ProviderConfig `json:"provider"`
	Policy        PolicyConfig   `json:"policy"`
	Budgets       BudgetConfig   `json:"budgets"`
	MCP           []MCPConfig    `json:"mcp"`
}

type ProfileConfig struct {
	Name                string                    `json:"name"`
	Version             string                    `json:"version"`
	SystemPrompt        string                    `json:"systemPrompt"`
	Completion          domain.CompletionContract `json:"completion"`
	SummaryArtifactType string                    `json:"summaryArtifactType"`
	MaxTokens           int                       `json:"maxTokens"`
}

type ProviderConfig struct {
	Mode           string                `json:"mode"`
	Binding        string                `json:"binding"`
	BaseURL        string                `json:"baseUrl"`
	Model          string                `json:"model"`
	APIKeyEnv      string                `json:"apiKeyEnv"`
	Headers        map[string]string     `json:"headers"`
	ResponseFormat openai.ResponseFormat `json:"responseFormat"`
	APIMode        openai.APIMode        `json:"apiMode"`
}

type PolicyConfig struct {
	Mode           string              `json:"mode"`
	AllowedTools   []string            `json:"allowedTools"`
	AllowedEffects []domain.ToolEffect `json:"allowedEffects"`
	AllowedRoots   []string            `json:"allowedRoots"`
	PathFields     []string            `json:"pathFields"`
}

type BudgetConfig struct {
	MaxElapsed     time.Duration `json:"maxElapsed"`
	MaxModelCalls  int64         `json:"maxModelCalls"`
	MaxToolCalls   int64         `json:"maxToolCalls"`
	MaxOutputBytes int64         `json:"maxOutputBytes"`
	MaxSteps       int           `json:"maxSteps"`
}

type MCPConfig struct {
	Binding      string            `json:"binding"`
	Version      int64             `json:"version"`
	ServerID     string            `json:"serverId"`
	Transport    string            `json:"transport"`
	Endpoint     string            `json:"endpoint"`
	Headers      map[string]string `json:"headers"`
	Command      string            `json:"command"`
	Args         []string          `json:"args"`
	CWD          string            `json:"cwd"`
	Env          map[string]string `json:"env"`
	EnvRefs      map[string]string `json:"envRefs"`
	AuthEnv      string            `json:"authEnv"`
	ToolBindings []MCPToolConfig   `json:"tools"`
}

type MCPToolConfig struct {
	Name                    string                    `json:"name"`
	RemoteName              string                    `json:"remoteName"`
	Version                 string                    `json:"version"`
	Effect                  domain.ToolEffect         `json:"effect"`
	Description             string                    `json:"description"`
	ArtifactType            string                    `json:"artifactType"`
	ArtifactSchemaVersion   string                    `json:"artifactSchemaVersion"`
	ArtifactProvenance      domain.ArtifactProvenance `json:"artifactProvenance"`
	ExternalIDField         string                    `json:"externalIdField"`
	SubjectExternalIDField  string                    `json:"subjectExternalIdField"`
	SubjectArtifactIDField  string                    `json:"subjectArtifactIdField"`
	VerificationStatusField string                    `json:"verificationStatusField"`
}

// Event is the bounded local input shape. It contains no provider or policy controls.
type Event struct {
	Version    string            `json:"version"`
	Source     string            `json:"source"`
	ExternalID string            `json:"externalId"`
	OccurredAt time.Time         `json:"occurredAt"`
	Goal       string            `json:"goal"`
	Payload    map[string]any    `json:"payload"`
	Context    map[string]string `json:"context"`
}

// Result is the bounded operator-facing output envelope.
type Result struct {
	Run         domain.Run                `json:"run"`
	Invocations []domain.Invocation       `json:"invocations"`
	Results     []domain.InvocationResult `json:"results"`
	Artifacts   []domain.Artifact         `json:"artifacts"`
	Messages    []domain.ModelMessage     `json:"messages"`
}

// Run loads strict config/event and executes one durable local run.
func Run(ctx context.Context, options Options) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	configBytes, err := loadInput(options.ConfigJSON, options.ConfigPath, maxConfigBytes)
	if err != nil {
		return Result{}, fmt.Errorf("load local config: %w", err)
	}
	eventBytes, err := loadInput(options.EventJSON, options.EventPath, maxEventBytes)
	if err != nil {
		return Result{}, fmt.Errorf("load local event: %w", err)
	}
	config, err := decodeConfig(configBytes)
	if err != nil {
		return Result{}, err
	}
	config, err = normalizeConfig(config)
	if err != nil {
		return Result{}, err
	}
	if err := validateConfig(config); err != nil {
		return Result{}, err
	}
	event, err := decodeEvent(eventBytes)
	if err != nil {
		return Result{}, err
	}
	event = normalizeEvent(event)
	if err := validateEvent(event); err != nil {
		return Result{}, err
	}
	if options.Env == nil {
		options.Env = func(key string) (string, bool) { return os.LookupEnv(key) }
	}
	if options.Stdout == nil {
		options.Stdout = io.Discard
	}
	if options.Stderr == nil {
		options.Stderr = io.Discard
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if strings.TrimSpace(options.StateDir) == "" {
		options.StateDir = defaultStateDir
	}
	if err := os.MkdirAll(options.StateDir, 0o700); err != nil {
		return Result{}, fmt.Errorf("create state directory: %w", err)
	}

	configurationHash, err := digestJSON(config)
	if err != nil {
		return Result{}, fmt.Errorf("canonicalize local config: %w", err)
	}
	eventHash, err := digestJSON(event)
	if err != nil {
		return Result{}, fmt.Errorf("canonicalize local event: %w", err)
	}
	runID := stableID(event.Source + "\x00" + event.ExternalID + "\x00" + eventHash)
	eventID := stableID("event\x00" + event.Source + "\x00" + event.ExternalID + "\x00" + eventHash)
	run := domain.Run{ID: runID, EventID: eventID, Goal: event.Goal, Input: cloneAnyMap(event.Payload), Context: cloneStringMap(event.Context), ProfileName: config.Profile.Name, ProfileVersion: config.Profile.Version, PolicyRef: config.Policy.Mode, ConfigurationDigest: configurationHash, State: domain.RunStateQueued, Budget: domain.Budget{Limits: domain.BudgetLimits{MaxElapsed: config.Budgets.MaxElapsed, MaxModelCalls: config.Budgets.MaxModelCalls, MaxToolCalls: config.Budgets.MaxToolCalls, MaxOutputBytes: config.Budgets.MaxOutputBytes}}, Version: 1}
	identity := file.Identity{RunID: runID, EventID: eventID, ConfigurationHash: configurationHash, EventHash: eventHash}
	runDir := filepath.Join(options.StateDir, runID)
	store, err := openOrCreateStore(ctx, runDir, identity, run, options.Resume)
	if err != nil {
		return Result{}, err
	}
	defer store.Close()
	storedRun, err := store.LoadRun(ctx, runID)
	if err != nil {
		return Result{}, fmt.Errorf("load durable run: %w", err)
	}
	// A terminal or waiting snapshot is already an operator-visible boundary.
	// Do not rediscover MCP tools or require provider credentials merely to
	// render/resume that durable state; only queued/running runs compose effects.
	if storedRun.State != domain.RunStateQueued && storedRun.State != domain.RunStateRunning {
		result, resultErr := resultFromStore(ctx, store, storedRun)
		if resultErr != nil {
			return result, resultErr
		}
		if err := writeJSON(options.Stdout, result); err != nil {
			return result, err
		}
		if storedRun.State == domain.RunStateWaiting {
			return result, errors.New("run is waiting for operator resolution")
		}
		return result, nil
	}

	provider, err := buildProvider(config.Provider, options)
	if err != nil {
		return Result{}, err
	}
	registry := application.NewToolRegistry()
	mcpRuntime, err := buildMCPTools(ctx, config.MCP, options.Env, options.HTTPClient, registry)
	if err != nil {
		return Result{}, err
	}
	if mcpRuntime != nil {
		defer mcpRuntime.Close(context.WithoutCancel(ctx))
	}
	policy, err := buildPolicy(config.Policy)
	if err != nil {
		return Result{}, err
	}
	profile, err := application.NewGoalProfile(application.GoalProfileOptions{Name: config.Profile.Name, Version: config.Profile.Version, SystemPrompt: config.Profile.SystemPrompt, Completion: config.Profile.Completion, SummaryArtifactType: config.Profile.SummaryArtifactType, MaxTokens: config.Profile.MaxTokens})
	if err != nil {
		return Result{}, err
	}
	runner, err := application.NewRunner(application.RunnerOptions{Store: store, Provider: provider, Tools: registry, Policy: policy, MaxSteps: config.Budgets.MaxSteps, Now: options.Now, NewID: options.NewID})
	if err != nil {
		return Result{}, err
	}
	completedRun, driveErr := runner.Drive(ctx, runID, profile)
	result, snapshotErr := resultFromStore(ctx, store, completedRun)
	if driveErr == nil && snapshotErr != nil {
		driveErr = snapshotErr
	}
	if driveErr == nil && completedRun.State == domain.RunStateWaiting {
		driveErr = errors.New("run is waiting for operator resolution")
	}
	if err := writeJSON(options.Stdout, result); err != nil && driveErr == nil {
		driveErr = err
	}
	if driveErr != nil {
		return result, driveErr
	}
	return result, nil
}

func resultFromStore(ctx context.Context, store *file.Store, run domain.Run) (Result, error) {
	if store == nil {
		return Result{}, errors.New("durable run store is required")
	}
	withoutCancel := context.WithoutCancel(ctx)
	result := Result{Run: run}
	var firstErr error
	result.Invocations, firstErr = store.ListInvocations(withoutCancel, run.ID)
	if firstErr != nil {
		return result, firstErr
	}
	snapshot, err := store.Snapshot()
	if err != nil {
		return result, err
	}
	result.Results = append([]domain.InvocationResult(nil), snapshot.Results...)
	result.Artifacts, err = store.ListArtifacts(withoutCancel, run.ID)
	if err != nil {
		return result, err
	}
	result.Messages, err = store.LoadMessages(withoutCancel, run.ID)
	if err != nil {
		return result, err
	}
	return result, nil
}

func openOrCreateStore(ctx context.Context, dir string, identity file.Identity, run domain.Run, resume bool) (*file.Store, error) {
	if resume {
		if _, err := os.Stat(filepath.Join(dir, file.StateFileName)); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, errors.New("durable run does not exist; resume cannot create a run")
			}
			return nil, err
		}
		return file.Open(ctx, dir, identity)
	}
	if _, err := os.Stat(filepath.Join(dir, file.StateFileName)); errors.Is(err, os.ErrNotExist) {
		return file.Create(ctx, dir, identity, run)
	} else if err != nil {
		return nil, err
	}
	return file.Open(ctx, dir, identity)
}

func buildProvider(config ProviderConfig, options Options) (domain.ModelProvider, error) {
	mode := strings.TrimSpace(config.Mode)
	if mode == "fixture" {
		return NewFixtureProvider(), nil
	}
	if mode != "openai" {
		return nil, errors.New("provider mode must be fixture or openai")
	}
	if config.Binding == "" {
		config.Binding = "default"
	}
	key, ok := options.Env(config.APIKeyEnv)
	if !ok || key == "" {
		return nil, errors.New("configured OpenAI API key environment variable is unavailable")
	}
	key = ""
	loader := envOpenAIBindingLoader{baseURL: config.BaseURL, model: config.Model, apiKeyEnv: config.APIKeyEnv, headers: cloneStringMap(config.Headers), responseFormat: config.ResponseFormat, apiMode: config.APIMode, env: options.Env}
	client, err := openai.NewClient(openai.Options{Bindings: loader, HTTPClient: options.HTTPClient, Logger: options.Logger})
	if err != nil {
		return nil, err
	}
	return boundProvider{provider: client, binding: config.Binding}, nil
}

type envOpenAIBindingLoader struct {
	baseURL        string
	model          string
	apiKeyEnv      string
	headers        map[string]string
	responseFormat openai.ResponseFormat
	apiMode        openai.APIMode
	env            EnvLookup
}

func (l envOpenAIBindingLoader) LoadBinding(_ context.Context, _ string) (openai.Binding, error) {
	key, ok := l.env(l.apiKeyEnv)
	if !ok || key == "" {
		return openai.Binding{}, errors.New("configured OpenAI API key environment variable is unavailable")
	}
	return openai.Binding{BaseURL: l.baseURL, Model: l.model, APIKey: []byte(key), Headers: cloneStringMap(l.headers), ResponseFormat: l.responseFormat, APIMode: l.apiMode}, nil
}

type boundProvider struct {
	provider domain.ModelProvider
	binding  string
}

func (p boundProvider) Complete(ctx context.Context, turn domain.ModelTurn) (domain.ModelResult, error) {
	turn.Binding = p.binding
	return p.provider.Complete(ctx, turn)
}

func buildMCPTools(ctx context.Context, configs []MCPConfig, env EnvLookup, httpClient *http.Client, registry *application.ToolRegistry) (*mcp.Runtime, error) {
	if len(configs) == 0 {
		return nil, nil
	}
	bindings := make(map[string]mcp.Binding, len(configs))
	authEnvs := make(map[string]string, len(configs))
	for _, config := range configs {
		if _, exists := bindings[config.Binding]; exists {
			return nil, errors.New("duplicate MCP binding")
		}
		resolvedEnv, err := resolveMCPEnvironment(config, env)
		if err != nil {
			return nil, err
		}
		bindings[config.Binding] = mcp.Binding{ID: config.Binding, Version: config.Version, ServerID: config.ServerID, Transport: config.Transport, Endpoint: config.Endpoint, Headers: cloneStringMap(config.Headers), Command: config.Command, Args: append([]string(nil), config.Args...), CWD: config.CWD, Env: resolvedEnv}
		authEnvs[config.Binding] = strings.TrimSpace(config.AuthEnv)
	}
	runtime, err := mcp.NewRuntime(mcp.Options{Bindings: mapBindingLoader{bindings: bindings, authEnvs: authEnvs, env: env}, HTTPClient: httpClient})
	if err != nil {
		return nil, err
	}
	for _, config := range configs {
		catalog, err := runtime.Discover(ctx, config.Binding)
		if err != nil {
			runtime.Close(context.WithoutCancel(ctx))
			return nil, err
		}
		toolConfigs := make([]mcp.ToolBindingConfig, 0, len(config.ToolBindings))
		for _, tool := range config.ToolBindings {
			toolConfigs = append(toolConfigs, mcp.ToolBindingConfig{
				Name: tool.Name, RemoteName: tool.RemoteName, Version: tool.Version, Effect: tool.Effect, Description: tool.Description,
				Artifact: localArtifactBinding(tool),
			})
		}
		bound, err := runtime.BindTools(catalog, toolConfigs)
		if err != nil {
			runtime.Close(context.WithoutCancel(ctx))
			return nil, err
		}
		for _, tool := range bound {
			if err := registry.Register(tool.Definition, tool.Executor); err != nil {
				runtime.Close(context.WithoutCancel(ctx))
				return nil, err
			}
		}
	}
	return runtime, nil
}

type mapBindingLoader struct {
	bindings map[string]mcp.Binding
	authEnvs map[string]string
	env      EnvLookup
}

func (l mapBindingLoader) LoadBinding(_ context.Context, id string) (mcp.Binding, error) {
	binding, ok := l.bindings[id]
	if !ok {
		return mcp.Binding{}, errors.New("MCP binding is not configured")
	}
	binding = cloneMCPBinding(binding)
	if authEnv := l.authEnvs[id]; authEnv != "" {
		value, exists := l.env(authEnv)
		if !exists || value == "" {
			return mcp.Binding{}, errors.New("configured MCP authentication environment variable is unavailable")
		}
		binding.AuthToken = []byte(value)
	}
	return binding, nil
}

func resolveMCPEnvironment(config MCPConfig, lookup EnvLookup) (map[string]string, error) {
	values := cloneStringMap(config.Env)
	if len(config.EnvRefs) == 0 {
		return values, nil
	}
	if values == nil {
		values = make(map[string]string, len(config.EnvRefs))
	}
	for name, envName := range config.EnvRefs {
		if _, exists := values[name]; exists {
			return nil, fmt.Errorf("MCP environment %q is configured twice", name)
		}
		if strings.TrimSpace(envName) == "" {
			return nil, fmt.Errorf("MCP environment reference for %q is empty", name)
		}
		value, ok := lookup(envName)
		if !ok {
			return nil, fmt.Errorf("MCP environment variable %q is unavailable", envName)
		}
		values[name] = value
	}
	return values, nil
}

func localArtifactBinding(config MCPToolConfig) *mcp.ArtifactBinding {
	if strings.TrimSpace(config.ArtifactType) == "" {
		return nil
	}
	version := config.ArtifactSchemaVersion
	if version == "" {
		version = "v1"
	}
	provenance := config.ArtifactProvenance
	if provenance == "" {
		provenance = domain.ProvenanceObserved
	}
	return &mcp.ArtifactBinding{
		Type: config.ArtifactType, SchemaVersion: version, Provenance: provenance,
		ExternalIDField: config.ExternalIDField, SubjectExternalIDField: config.SubjectExternalIDField,
		SubjectArtifactIDField: config.SubjectArtifactIDField, VerificationStatusField: config.VerificationStatusField,
	}
}

func buildPolicy(config PolicyConfig) (application.Policy, error) {
	switch strings.TrimSpace(config.Mode) {
	case "allow-all":
		return application.AllowAllPolicy{}, nil
	case "restrict", "":
		effects := append([]domain.ToolEffect(nil), config.AllowedEffects...)
		if len(effects) == 0 {
			effects = []domain.ToolEffect{domain.ToolEffectRead}
		}
		return application.NewRestrictionPolicy(application.RestrictionConfig{AllowedTools: config.AllowedTools, AllowedEffects: effects, AllowedRoots: config.AllowedRoots, PathFields: config.PathFields}), nil
	default:
		return nil, errors.New("policy mode must be restrict or allow-all")
	}
}

func decodeConfig(encoded []byte) (Config, error) {
	var config Config
	if err := strictDecode(encoded, &config); err != nil {
		return Config{}, fmt.Errorf("decode local config: %w", err)
	}
	if config.SchemaVersion != 1 {
		return Config{}, errors.New("local config schemaVersion must be 1")
	}
	return config, nil
}

func decodeEvent(encoded []byte) (Event, error) {
	var event Event
	if err := strictDecode(encoded, &event); err != nil {
		return Event{}, fmt.Errorf("decode local event: %w", err)
	}
	return event, nil
}

func normalizeConfig(config Config) (Config, error) {
	profile, err := normalizeProfileConfig(config.Profile)
	if err != nil {
		return Config{}, err
	}
	config.Profile = profile

	config.Provider.Mode = strings.TrimSpace(config.Provider.Mode)
	config.Provider.Binding = strings.TrimSpace(config.Provider.Binding)
	if config.Provider.Binding == "" {
		config.Provider.Binding = "default"
	}
	config.Provider.BaseURL = strings.TrimRight(strings.TrimSpace(config.Provider.BaseURL), "/")
	if config.Provider.BaseURL == "" {
		config.Provider.BaseURL = "https://api.openai.com"
	}
	config.Provider.Model = strings.TrimSpace(config.Provider.Model)
	if config.Provider.Model == "" {
		config.Provider.Model = openai.ModelID
	}
	config.Provider.APIKeyEnv = strings.TrimSpace(config.Provider.APIKeyEnv)
	if config.Provider.ResponseFormat == "" {
		config.Provider.ResponseFormat = openai.ResponseFormatNone
	}
	if config.Provider.APIMode == "" {
		config.Provider.APIMode = openai.APIModeChatCompletions
	}
	config.Provider.Headers = cloneStringMap(config.Provider.Headers)

	config.Policy.Mode = strings.TrimSpace(config.Policy.Mode)
	if config.Policy.Mode == "" {
		config.Policy.Mode = "restrict"
	}
	config.Policy.AllowedTools = sortedStrings(config.Policy.AllowedTools)
	config.Policy.AllowedEffects = sortedEffects(config.Policy.AllowedEffects)
	if config.Policy.Mode == "restrict" && len(config.Policy.AllowedEffects) == 0 {
		config.Policy.AllowedEffects = []domain.ToolEffect{domain.ToolEffectRead}
	}
	config.Policy.AllowedRoots = sortedStrings(config.Policy.AllowedRoots)
	config.Policy.PathFields = sortedStrings(config.Policy.PathFields)
	if config.Budgets.MaxSteps == 0 {
		config.Budgets.MaxSteps = defaultRunnerMaxSteps
	}
	if config.MCP == nil {
		config.MCP = []MCPConfig{}
	}
	for index := range config.MCP {
		binding := &config.MCP[index]
		binding.Binding = strings.TrimSpace(binding.Binding)
		binding.ServerID = strings.TrimSpace(binding.ServerID)
		binding.Transport = strings.TrimSpace(binding.Transport)
		binding.Endpoint = strings.TrimRight(strings.TrimSpace(binding.Endpoint), "/")
		binding.Command = strings.TrimSpace(binding.Command)
		binding.CWD = strings.TrimSpace(binding.CWD)
		binding.AuthEnv = strings.TrimSpace(binding.AuthEnv)
		binding.Headers = cloneStringMap(binding.Headers)
		binding.Env = cloneStringMap(binding.Env)
		binding.EnvRefs = cloneStringMap(binding.EnvRefs)
		binding.Args = append([]string(nil), binding.Args...)
		if binding.ToolBindings == nil {
			binding.ToolBindings = []MCPToolConfig{}
		}
		sort.Slice(binding.ToolBindings, func(left, right int) bool {
			if binding.ToolBindings[left].Name != binding.ToolBindings[right].Name {
				return binding.ToolBindings[left].Name < binding.ToolBindings[right].Name
			}
			if binding.ToolBindings[left].Version != binding.ToolBindings[right].Version {
				return binding.ToolBindings[left].Version < binding.ToolBindings[right].Version
			}
			return binding.ToolBindings[left].RemoteName < binding.ToolBindings[right].RemoteName
		})
		for toolIndex := range binding.ToolBindings {
			tool := &binding.ToolBindings[toolIndex]
			tool.Name = strings.TrimSpace(tool.Name)
			tool.RemoteName = strings.TrimSpace(tool.RemoteName)
			tool.Version = strings.TrimSpace(tool.Version)
			tool.Description = strings.TrimSpace(tool.Description)
			tool.ArtifactType = strings.TrimSpace(tool.ArtifactType)
			tool.ArtifactSchemaVersion = strings.TrimSpace(tool.ArtifactSchemaVersion)
			tool.ArtifactProvenance = domain.ArtifactProvenance(strings.TrimSpace(string(tool.ArtifactProvenance)))
			tool.ExternalIDField = strings.TrimSpace(tool.ExternalIDField)
			tool.SubjectExternalIDField = strings.TrimSpace(tool.SubjectExternalIDField)
			tool.SubjectArtifactIDField = strings.TrimSpace(tool.SubjectArtifactIDField)
			tool.VerificationStatusField = strings.TrimSpace(tool.VerificationStatusField)
			if tool.ArtifactType == "" {
				tool.ArtifactSchemaVersion = ""
				tool.ArtifactProvenance = ""
				tool.ExternalIDField = ""
				tool.SubjectExternalIDField = ""
				tool.SubjectArtifactIDField = ""
				tool.VerificationStatusField = ""
			} else {
				if tool.ArtifactSchemaVersion == "" {
					tool.ArtifactSchemaVersion = "v1"
				}
				if tool.ArtifactProvenance == "" {
					tool.ArtifactProvenance = domain.ProvenanceObserved
				}
			}
		}
		sort.Slice(binding.ToolBindings, func(left, right int) bool {
			if binding.ToolBindings[left].Name != binding.ToolBindings[right].Name {
				return binding.ToolBindings[left].Name < binding.ToolBindings[right].Name
			}
			if binding.ToolBindings[left].Version != binding.ToolBindings[right].Version {
				return binding.ToolBindings[left].Version < binding.ToolBindings[right].Version
			}
			return binding.ToolBindings[left].RemoteName < binding.ToolBindings[right].RemoteName
		})
	}
	sort.Slice(config.MCP, func(left, right int) bool { return config.MCP[left].Binding < config.MCP[right].Binding })
	return config, nil
}

func validateConfig(config Config) error {
	if !validBoundedText(config.Profile.Name, 256) || !validVersionN(config.Profile.Version) {
		return errors.New("profile name and vN version are required")
	}
	if !validBoundedText(config.Profile.SystemPrompt, 64<<10) || !validVersionN(config.Profile.Completion.Version) || !validCompletionMode(config.Profile.Completion.Mode) {
		return errors.New("profile prompt and completion contract are invalid")
	}
	if !validBoundedText(config.Profile.SummaryArtifactType, 256) {
		return errors.New("profile summary artifact type is invalid")
	}
	if err := boundedJSONTree(config.Profile.Completion.Parameters, maxConfigBytes); err != nil {
		return fmt.Errorf("completion parameters are invalid: %w", err)
	}
	if config.Profile.MaxTokens <= 0 || config.Profile.MaxTokens > 16384 {
		return errors.New("profile maxTokens is outside the bound")
	}
	if config.Provider.Mode != "fixture" && config.Provider.Mode != "openai" {
		return errors.New("provider mode must be fixture or openai")
	}
	if !validBoundedText(config.Provider.Binding, 256) || !validBoundedText(config.Provider.BaseURL, 2048) || !validBoundedText(config.Provider.Model, 256) {
		return errors.New("provider binding is invalid")
	}
	if config.Provider.Mode == "openai" && !validEnvironmentName(config.Provider.APIKeyEnv) {
		return errors.New("OpenAI apiKeyEnv is required and must be a valid environment name")
	}
	if config.Provider.ResponseFormat != openai.ResponseFormatNone && config.Provider.ResponseFormat != openai.ResponseFormatJSONObject {
		return errors.New("provider response format is invalid")
	}
	if config.Provider.APIMode != openai.APIModeChatCompletions && config.Provider.APIMode != openai.APIModeResponses {
		return errors.New("provider API mode is invalid")
	}
	if len(config.Provider.Headers) > 64 {
		return errors.New("provider headers are too large")
	}
	for name, value := range config.Provider.Headers {
		if !validBoundedText(name, 128) || !validBoundedText(value, 4096) || strings.ContainsAny(name+value, "\r\n") {
			return errors.New("provider header is invalid")
		}
	}
	if config.Budgets.MaxElapsed < 0 || config.Budgets.MaxModelCalls < 0 || config.Budgets.MaxToolCalls < 0 || config.Budgets.MaxOutputBytes < 0 || config.Budgets.MaxSteps <= 0 || config.Budgets.MaxSteps > 4096 {
		return errors.New("budget values are invalid or outside the bound")
	}
	if config.Policy.Mode != "restrict" && config.Policy.Mode != "allow-all" {
		return errors.New("policy mode must be restrict or allow-all")
	}
	if len(config.Policy.AllowedTools) > 256 || len(config.Policy.AllowedRoots) > 256 || len(config.Policy.PathFields) > 256 || len(config.Policy.AllowedEffects) > 2 {
		return errors.New("policy configuration is too large")
	}
	for _, value := range append(append(append([]string{}, config.Policy.AllowedTools...), config.Policy.AllowedRoots...), config.Policy.PathFields...) {
		if !validBoundedText(value, 1024) {
			return errors.New("policy value is invalid")
		}
	}
	for _, effect := range config.Policy.AllowedEffects {
		if effect != domain.ToolEffectRead && effect != domain.ToolEffectWrite {
			return errors.New("policy effect is invalid")
		}
	}

	seenBindings := make(map[string]struct{}, len(config.MCP))
	for _, binding := range config.MCP {
		if !validBoundedText(binding.Binding, 256) {
			return errors.New("MCP binding is required and invalid")
		}
		if _, exists := seenBindings[binding.Binding]; exists {
			return errors.New("duplicate MCP binding")
		}
		seenBindings[binding.Binding] = struct{}{}
		if binding.Version <= 0 || binding.ServerID != "" && !validBoundedText(binding.ServerID, 256) {
			return errors.New("MCP binding identity is invalid")
		}
		if binding.Transport != "stdio" && binding.Transport != "http" && binding.Transport != "streamable_http" {
			return errors.New("MCP transport is unsupported")
		}
		if binding.AuthEnv != "" && (!validEnvironmentName(binding.AuthEnv) || binding.Transport == "stdio") {
			return errors.New("MCP authEnv is invalid for this transport")
		}
		if len(binding.Env)+len(binding.EnvRefs) > 64 || len(binding.Args) > 128 || len(binding.ToolBindings) > 128 {
			return errors.New("MCP binding exceeds a configured bound")
		}
		for name, value := range binding.Env {
			if !validEnvironmentName(name) || !validBoundedText(value, 4096) {
				return errors.New("MCP environment entry is invalid")
			}
		}
		for name, envName := range binding.EnvRefs {
			if !validEnvironmentName(name) || !validEnvironmentName(envName) {
				return errors.New("MCP environment reference is invalid")
			}
		}
		for _, arg := range binding.Args {
			if !validBoundedText(arg, 4096) {
				return errors.New("MCP argument is invalid")
			}
		}
		seenTools := make(map[string]struct{}, len(binding.ToolBindings))
		for _, tool := range binding.ToolBindings {
			if !validToolName(tool.Name) || !validVersionN(tool.Version) || tool.RemoteName != "" && !validBoundedText(tool.RemoteName, 256) || !validBoundedText(tool.Description, 4096) || !validEffect(tool.Effect) {
				return errors.New("MCP tool identity or effect is invalid")
			}
			if _, exists := seenTools[tool.Name]; exists {
				return errors.New("duplicate MCP tool binding")
			}
			seenTools[tool.Name] = struct{}{}
			if tool.ArtifactProvenance != "" && tool.ArtifactProvenance != domain.ProvenanceObserved && tool.ArtifactProvenance != domain.ProvenanceVerified {
				return errors.New("MCP artifact provenance is invalid")
			}
			for _, field := range []string{tool.ArtifactType, tool.ArtifactSchemaVersion, tool.ExternalIDField, tool.SubjectExternalIDField, tool.SubjectArtifactIDField, tool.VerificationStatusField} {
				if !validBoundedText(field, 256) {
					return errors.New("MCP artifact field is invalid")
				}
			}
			if tool.ArtifactProvenance == domain.ProvenanceVerified && (tool.VerificationStatusField == "" || tool.SubjectExternalIDField == "" && tool.SubjectArtifactIDField == "") {
				return errors.New("verified MCP artifacts require status and subject fields")
			}
		}
	}
	return nil
}

func validateEvent(event Event) error {
	if strings.TrimSpace(event.Version) == "" || strings.TrimSpace(event.Source) == "" || strings.TrimSpace(event.ExternalID) == "" || strings.TrimSpace(event.Goal) == "" {
		return errors.New("event version, source, externalId, and goal are required")
	}
	if len(event.Version) > 64 || len(event.Source) > 256 || len(event.ExternalID) > 256 || len(event.Goal) > 64<<10 {
		return errors.New("event identity or goal exceeds the bound")
	}
	if len(event.Context) > 64 {
		return errors.New("event context exceeds the bound")
	}
	for key, value := range event.Context {
		if strings.TrimSpace(key) == "" || len(key) > 256 || len(value) > 4096 || strings.ContainsRune(key, 0) || strings.ContainsRune(value, 0) {
			return errors.New("event context entry is invalid")
		}
	}
	if err := boundedEventJSON(event.Payload); err != nil {
		return err
	}
	return nil
}

func normalizeProfileConfig(config ProfileConfig) (ProfileConfig, error) {
	config.Name = strings.TrimSpace(config.Name)
	config.Version = strings.TrimSpace(config.Version)
	config.Completion.Mode = domain.CompletionMode(strings.TrimSpace(string(config.Completion.Mode)))
	config.Completion.Version = strings.TrimSpace(config.Completion.Version)
	config.SummaryArtifactType = strings.TrimSpace(config.SummaryArtifactType)
	if config.Name == "" {
		config.Name = defaultProfile
	}
	if config.Version == "" {
		config.Version = defaultProfileVersion
	}
	if config.SystemPrompt == "" {
		config.SystemPrompt = "Return a concise structured report for the configured goal."
	}
	if config.SummaryArtifactType == "" {
		config.SummaryArtifactType = "report.summary"
	}
	if config.Completion.Mode == "" {
		config.Completion.Mode = domain.CompletionSolutionDelivered
	}
	if config.Completion.Version == "" {
		config.Completion.Version = "v1"
	}
	if config.Completion.Parameters == nil {
		config.Completion.Parameters = map[string]any{}
	}
	if config.MaxTokens == 0 {
		config.MaxTokens = 4096
	}
	if config.MaxTokens < 0 || config.MaxTokens > 16384 {
		return ProfileConfig{}, errors.New("profile maxTokens is outside the bound")
	}
	return config, nil
}

func normalizeEvent(event Event) Event {
	if !event.OccurredAt.IsZero() {
		event.OccurredAt = event.OccurredAt.UTC()
	}
	if event.Payload == nil {
		event.Payload = map[string]any{}
	}
	if event.Context == nil {
		event.Context = map[string]string{}
	}
	return event
}

func validCompletionMode(mode domain.CompletionMode) bool {
	switch mode {
	case domain.CompletionSolutionDelivered, domain.CompletionActionWithVerification, domain.CompletionObservedRemoteBranch, domain.CompletionPipelineVerification:
		return true
	default:
		return false
	}
}

func validVersionN(value string) bool {
	if len(value) < 2 || value[0] != 'v' || value[1] == '0' {
		return false
	}
	for _, runeValue := range value[1:] {
		if runeValue < '0' || runeValue > '9' {
			return false
		}
	}
	return true
}

func validEnvironmentName(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	for index, runeValue := range value {
		if index == 0 {
			if !(runeValue == '_' || runeValue >= 'A' && runeValue <= 'Z' || runeValue >= 'a' && runeValue <= 'z') {
				return false
			}
			continue
		}
		if !(runeValue == '_' || runeValue >= 'A' && runeValue <= 'Z' || runeValue >= 'a' && runeValue <= 'z' || runeValue >= '0' && runeValue <= '9') {
			return false
		}
	}
	return true
}

func validToolName(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if part == "" || part[0] < 'a' || part[0] > 'z' {
			return false
		}
		for _, runeValue := range part[1:] {
			if !(runeValue >= 'a' && runeValue <= 'z' || runeValue >= '0' && runeValue <= '9' || runeValue == '_') {
				return false
			}
		}
	}
	return len(value) <= 256
}

func validEffect(value domain.ToolEffect) bool {
	return value == domain.ToolEffectRead || value == domain.ToolEffectWrite
}

func validBoundedText(value string, max int) bool {
	return len(value) <= max && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

func sortedStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

func sortedEffects(values []domain.ToolEffect) []domain.ToolEffect {
	result := append([]domain.ToolEffect(nil), values...)
	sort.Slice(result, func(left, right int) bool { return result[left] < result[right] })
	return result
}

func strictDecode(encoded []byte, destination any) error {
	if len(bytes.TrimSpace(encoded)) == 0 {
		return errors.New("JSON input is empty")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("JSON input has trailing data")
		}
		return err
	}
	return nil
}

func loadInput(inline []byte, path string, max int64) ([]byte, error) {
	if len(inline) > 0 {
		if int64(len(inline)) > max {
			return nil, errors.New("inline input exceeds bound")
		}
		return append([]byte(nil), inline...), nil
	}
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("input path or inline JSON is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, errors.New("input file exceeds bound")
	}
	return data, nil
}

func boundedEventJSON(value any) error {
	if err := boundedJSONTree(value, maxEventBytes); err != nil {
		return fmt.Errorf("event payload exceeds the bound: %w", err)
	}
	return nil
}

func boundedJSONTree(value any, limit int) error {
	if value == nil {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return errors.New("value is not JSON")
	}
	if len(encoded) > limit {
		return errors.New("encoded value is too large")
	}
	var normalized any
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		return errors.New("value is not valid JSON")
	}
	nodes := 0
	if err := validateJSONTree(normalized, 0, &nodes); err != nil {
		return err
	}
	return nil
}

func validateJSONTree(value any, depth int, nodes *int) error {
	if depth > maxJSONDepth {
		return errors.New("JSON nesting exceeds the bound")
	}
	*nodes++
	if *nodes > maxJSONNodes {
		return errors.New("JSON node count exceeds the bound")
	}
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			if !utf8.ValidString(key) || strings.ContainsRune(key, 0) || len(key) > 4096 {
				return errors.New("JSON object key is invalid")
			}
			if err := validateJSONTree(child, depth+1, nodes); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range current {
			if err := validateJSONTree(child, depth+1, nodes); err != nil {
				return err
			}
		}
	case string:
		if !utf8.ValidString(current) || strings.ContainsRune(current, 0) {
			return errors.New("JSON string is invalid")
		}
	}
	return nil
}

func digestJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return digest(encoded), nil
}

func digest(value []byte) string { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }
func stableID(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:16])
}
func cloneAnyMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	encoded, _ := json.Marshal(value)
	var clone map[string]any
	_ = json.Unmarshal(encoded, &clone)
	return clone
}
func cloneStringMap(value map[string]string) map[string]string {
	if len(value) == 0 {
		return nil
	}
	clone := make(map[string]string, len(value))
	for key, item := range value {
		clone[key] = item
	}
	return clone
}
func cloneMCPBinding(value mcp.Binding) mcp.Binding {
	value.Headers = cloneStringMap(value.Headers)
	value.Env = cloneStringMap(value.Env)
	value.AuthToken = append([]byte(nil), value.AuthToken...)
	value.Args = append([]string(nil), value.Args...)
	return value
}

// Inspect returns a validated durable snapshot without opening the run for execution.
func Inspect(ctx context.Context, stateDir, runID string) (file.Snapshot, error) {
	if strings.TrimSpace(stateDir) == "" || !validRunID(runID) {
		return file.Snapshot{}, errors.New("state directory and run ID are required")
	}
	return file.Inspect(ctx, filepath.Join(stateDir, runID))
}

// ResolveInvocation records an operator-confirmed result without executing the
// configured tool. The returned snapshot is ready for inspection or resume.
func ResolveInvocation(ctx context.Context, stateDir, runID, invocationID string, state domain.InvocationState, code string, output any, completedAt time.Time) (file.Snapshot, error) {
	if strings.TrimSpace(stateDir) == "" || !validRunID(runID) || strings.TrimSpace(invocationID) == "" {
		return file.Snapshot{}, errors.New("state directory, run ID, and invocation ID are required")
	}
	store, err := file.Open(ctx, filepath.Join(stateDir, runID), file.Identity{})
	if err != nil {
		return file.Snapshot{}, err
	}
	defer store.Close()
	current, err := store.Snapshot()
	if err != nil {
		return file.Snapshot{}, err
	}
	if current.Identity.RunID != runID {
		return file.Snapshot{}, errors.New("durable run path and snapshot identity do not match")
	}
	if err := store.ResolveInvocation(ctx, invocationID, state, code, output, completedAt); err != nil {
		return file.Snapshot{}, err
	}
	return store.Snapshot()
}

func validRunID(value string) bool {
	if len(value) != sha256.Size || strings.TrimSpace(value) != value {
		return false
	}
	for _, runeValue := range value {
		if !(runeValue >= '0' && runeValue <= '9' || runeValue >= 'a' && runeValue <= 'f') {
			return false
		}
	}
	return true
}

func writeJSON(writer OutputWriter, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if len(encoded) > maxOutputBytes {
		return errors.New("local output exceeds the bound")
	}
	encoded = append(encoded, '\n')
	_, err = writer.Write(encoded)
	return err
}

var _ openai.BindingLoader = openai.StaticBindingLoader{}
var _ mcp.BindingLoader = mapBindingLoader{}
