package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Role string

const (
	RoleAdmin    Role = "admin"
	RoleOperator Role = "operator"
	RoleViewer   Role = "viewer"
)

type Project struct {
	ID          string
	Key         string
	Name        string
	Description string
	Role        Role
	Version     int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Member struct {
	UserID    string
	Username  string
	Role      Role
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

type SecretKind string

const (
	SecretSSHPassword   SecretKind = "ssh_password"
	SecretSSHPrivateKey SecretKind = "ssh_private_key"
	SecretHTTPBearer    SecretKind = "http_bearer"
	SecretHTTPHeader    SecretKind = "http_header"
	SecretWebhookHMAC   SecretKind = "webhook_hmac"
	SecretGitCredential SecretKind = "git_credential"
)

type Secret struct {
	ID         string
	ProjectID  string
	Name       string
	Kind       SecretKind
	KeyVersion int32
	Version    int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type EncryptedSecret struct {
	Secret
	Ciphertext []byte
	Nonce      []byte
}

type Environment struct {
	ID      string
	Key     string
	Name    string
	Service *string
	Version int64
}

type Repository struct {
	ID                 string
	RemoteURL          string
	SCMProvider        string
	Transport          string
	CredentialSecretID *string
	ProductionBranch   string
	DeployedCommit     string
	Version            int64
}

type Source struct {
	ID                 string
	Kind               string
	CredentialSecretID *string
	Config             json.RawMessage
	Capabilities       []string
	Enabled            bool
	Version            int64
}

type Trigger struct {
	ID              string
	Kind            string
	SigningSecretID *string
	Config          json.RawMessage
	Enabled         bool
	Version         int64
	// InboundURL 仅项目管理员读取 signed_webhook 时由 application 填入完整公开地址。
	InboundURL string
	// IngressTokenHash / Ciphertext / Nonce 是 trigger 行上的入站 token 材料，
	// 不得进入 HTTP JSON；仅 persistence 与 application 揭示路径使用。
	IngressTokenHash       []byte
	IngressTokenCiphertext []byte
	IngressTokenNonce      []byte
}

// LLMProvider 是项目唯一的 OpenAI 兼容模型接入点。
// API key 只以同项目 http_bearer 凭据引用存在，不进入本结构。
type LLMProvider struct {
	ID                 string
	Provider           string
	BaseURL            string
	CredentialSecretID string
	Model              string
	Version            int64
}

type Configuration struct {
	Environment Environment
	Repository  Repository
	Source      Source
	Trigger     Trigger
	LLM         *LLMProvider
}

// ConfigurationDraft 表示配置编辑器读取到的可部分保存配置。
// 每个组件都可以尚未落库，完整运行配置仍由 Configuration 表示。
type ConfigurationDraft struct {
	Environment *Environment
	Repository  *Repository
	Source      *Source
	Trigger     *Trigger
	LLM         *LLMProvider
}

type AuditEvent struct {
	ID          string
	ActorUserID *string
	Action      string
	TargetType  string
	TargetID    *string
	Summary     string
	Metadata    json.RawMessage
	OccurredAt  time.Time
}

var (
	projectKeyPattern     = regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}[a-z0-9]$`)
	environmentKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	commitPattern         = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)
	webhookTokenPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
)

func IsCommitSHA(value string) bool {
	return commitPattern.MatchString(value)
}

// ParseWebhookToken 拒绝路径中形状错误的入站 token，避免无意义的哈希查找。
func ParseWebhookToken(value string) (string, error) {
	if !webhookTokenPattern.MatchString(value) {
		return "", fmt.Errorf("webhook token is invalid")
	}
	return value, nil
}

// InboundWebhookURL 用部署级公共基址和 token 派生完整入站地址。
func InboundWebhookURL(publicURL, token string) (string, error) {
	if _, err := ParseWebhookToken(token); err != nil {
		return "", err
	}
	return publicURL + "/hooks/" + token, nil
}

func NormalizeProjectKey(value string) (string, error) {
	key := strings.ToLower(strings.TrimSpace(value))
	if !projectKeyPattern.MatchString(key) {
		return "", fmt.Errorf("project key must contain 3 to 64 lowercase URL-safe characters")
	}
	return key, nil
}

func ParseRole(value string) (Role, error) {
	role := Role(value)
	switch role {
	case RoleAdmin, RoleOperator, RoleViewer:
		return role, nil
	default:
		return "", fmt.Errorf("unknown project role")
	}
}

func (p Project) CanWriteIncidents() bool { return p.Role == RoleAdmin || p.Role == RoleOperator }
func (p Project) CanAdminister() bool     { return p.Role == RoleAdmin }

func ValidateProject(project Project) error {
	if _, err := NormalizeProjectKey(project.Key); err != nil {
		return err
	}
	if !bounded(project.Name, 1, 120) || len(project.Description) > 1000 {
		return fmt.Errorf("project name or description is invalid")
	}
	if _, err := ParseRole(string(project.Role)); err != nil {
		return err
	}
	return nil
}

func ParseSecretKind(value string) (SecretKind, error) {
	kind := SecretKind(value)
	switch kind {
	case SecretSSHPassword, SecretSSHPrivateKey, SecretHTTPBearer, SecretHTTPHeader, SecretWebhookHMAC, SecretGitCredential:
		return kind, nil
	default:
		return "", fmt.Errorf("unknown secret kind")
	}
}

// ValidateSecretMetadata 校验凭据展示名和 kind，供 name-only 更新在不提供明文时复用。
func ValidateSecretMetadata(secret Secret) error {
	if !bounded(secret.Name, 1, 120) {
		return fmt.Errorf("secret name is invalid")
	}
	if _, err := ParseSecretKind(string(secret.Kind)); err != nil {
		return err
	}
	return nil
}

func ValidateSecret(secret Secret, value []byte) error {
	if err := ValidateSecretMetadata(secret); err != nil {
		return err
	}
	if len(value) < 1 || len(value) > 65519 {
		return fmt.Errorf("secret value size is invalid")
	}
	return nil
}

func ValidateLLMProvider(provider LLMProvider) error {
	if !oneOf(provider.Provider, "openai") || !validHTTPURL(provider.BaseURL) || !bounded(provider.BaseURL, 1, 2048) ||
		!bounded(provider.Model, 1, 200) || strings.ContainsAny(provider.Model, " \t\r\n") {
		return fmt.Errorf("LLM provider configuration is invalid")
	}
	if err := validateRequiredUUIDv7(provider.CredentialSecretID); err != nil {
		return err
	}
	return nil
}

func ValidateLLMModelsProbe(baseURL, secretID string) error {
	if !validHTTPURL(baseURL) || !bounded(baseURL, 1, 2048) {
		return fmt.Errorf("LLM base URL is invalid")
	}
	return validateRequiredUUIDv7(secretID)
}

func ValidateLLMChatProbe(baseURL, secretID, model string) error {
	if err := ValidateLLMModelsProbe(baseURL, secretID); err != nil {
		return err
	}
	if !bounded(model, 1, 200) || strings.ContainsAny(model, " \t\r\n") {
		return fmt.Errorf("LLM model is invalid")
	}
	return nil
}

func ValidateRepositoryProbe(remoteURL, transport string) error {
	remote, err := url.Parse(remoteURL)
	if err != nil || remote.User != nil || remote.Host == "" || !bounded(remoteURL, 1, 2048) ||
		!oneOf(remote.Scheme, "https", "ssh") || remote.Scheme != transport || !oneOf(transport, "https", "ssh") {
		return fmt.Errorf("repository remote URL is invalid")
	}
	return nil
}

// ValidateEnvironment 校验项目环境的独立配置。
func ValidateEnvironment(environment Environment) error {
	if !environmentKeyPattern.MatchString(environment.Key) || !bounded(environment.Name, 1, 120) ||
		(environment.Service != nil && !bounded(*environment.Service, 1, 120)) {
		return fmt.Errorf("environment configuration is invalid")
	}
	return nil
}

// ValidateRepository 校验 Git 仓库的独立配置。
func ValidateRepository(repository Repository) error {
	remote, err := url.Parse(repository.RemoteURL)
	if err != nil || remote.User != nil || remote.Host == "" || !bounded(repository.RemoteURL, 1, 2048) ||
		!oneOf(remote.Scheme, "https", "ssh") || remote.Scheme != repository.Transport {
		return fmt.Errorf("repository remote URL is invalid")
	}
	if !oneOf(repository.SCMProvider, "github", "gitlab", "yunxiao", "gitee", "generic") ||
		!oneOf(repository.Transport, "https", "ssh") || !bounded(repository.ProductionBranch, 1, 255) ||
		!commitPattern.MatchString(repository.DeployedCommit) {
		return fmt.Errorf("repository configuration is invalid")
	}
	return validateOptionalUUIDv7(repository.CredentialSecretID)
}

// ValidateSource 校验 collection source 的独立配置。
func ValidateSource(source Source) error {
	if err := validateOptionalUUIDv7(source.CredentialSecretID); err != nil {
		return err
	}
	if !oneOf(source.Kind, "ssh", "cloud", "mcp") {
		return fmt.Errorf("source configuration is invalid")
	}
	if err := validateCapabilities(source.Capabilities); err != nil {
		return err
	}
	return validateSourceConfig(source.Kind, source.Config)
}

// ValidateTrigger 校验 trigger 的独立配置。
func ValidateTrigger(trigger Trigger) error {
	if err := validateOptionalUUIDv7(trigger.SigningSecretID); err != nil {
		return err
	}
	if !oneOf(trigger.Kind, "signed_webhook", "custom_rule") {
		return fmt.Errorf("trigger configuration is invalid")
	}
	return validateTriggerConfig(trigger.Kind, trigger.Config)
}

func ValidateConfiguration(configuration Configuration) error {
	if err := ValidateEnvironment(configuration.Environment); err != nil {
		return err
	}
	if err := ValidateRepository(configuration.Repository); err != nil {
		return err
	}
	if err := ValidateSource(configuration.Source); err != nil {
		return err
	}
	if err := ValidateTrigger(configuration.Trigger); err != nil {
		return err
	}
	if configuration.LLM != nil {
		if err := ValidateLLMProvider(*configuration.LLM); err != nil {
			return err
		}
	}
	return nil
}

// ValidateWebhookTokenColumns 要求入站 token 的 hash/ciphertext/nonce 同时为空或同时完整。
func ValidateWebhookTokenColumns(hash, ciphertext, nonce []byte) error {
	if len(hash) == 0 && len(ciphertext) == 0 && len(nonce) == 0 {
		return nil
	}
	if len(hash) == 32 && len(ciphertext) > 0 && len(nonce) == 12 {
		return nil
	}
	return fmt.Errorf("webhook token columns must be all present or all empty")
}

type sshConfig struct {
	SchemaVersion int    `json:"schemaVersion"`
	Host          string `json:"host"`
	Port          int    `json:"port"`
	User          string `json:"user"`
	ProjectFolder string `json:"projectFolder"`
	LogPath       string `json:"logPath"`
	Mode          string `json:"mode"`
}

type cloudConfig struct {
	SchemaVersion int    `json:"schemaVersion"`
	Provider      string `json:"provider"`
	Region        string `json:"region"`
	Resource      string `json:"resource"`
}

type mcpConfig struct {
	SchemaVersion   int               `json:"schemaVersion"`
	Endpoint        string            `json:"endpoint"`
	Transport       string            `json:"transport"`
	Headers         map[string]string `json:"headers"`
	EvidenceProfile string            `json:"evidenceProfile"`
	QueryScope      string            `json:"queryScope"`
}

type webhookConfig struct {
	SchemaVersion    int      `json:"schemaVersion"`
	EventTypes       []string `json:"eventTypes"`
	DeduplicationKey string   `json:"deduplicationKey"`
}

type ruleConfig struct {
	SchemaVersion         int    `json:"schemaVersion"`
	GroupingWindowSeconds int    `json:"groupingWindowSeconds"`
	MatchExpression       string `json:"matchExpression"`
}

func validateSourceConfig(kind string, raw json.RawMessage) error {
	switch kind {
	case "ssh":
		var value sshConfig
		if err := decodeStrict(raw, &value); err != nil || value.SchemaVersion != 1 || !bounded(value.Host, 1, 255) ||
			value.Port < 1 || value.Port > 65535 || !bounded(value.User, 1, 128) || !bounded(value.ProjectFolder, 1, 2048) ||
			!bounded(value.LogPath, 1, 2048) || !oneOf(value.Mode, "tail", "snapshot") {
			return fmt.Errorf("SSH source config is invalid")
		}
	case "cloud":
		var value cloudConfig
		if err := decodeStrict(raw, &value); err != nil || value.SchemaVersion != 1 || !bounded(value.Provider, 1, 64) ||
			!bounded(value.Region, 1, 128) || !bounded(value.Resource, 1, 255) {
			return fmt.Errorf("cloud source config is invalid")
		}
	case "mcp":
		var value mcpConfig
		if err := decodeStrict(raw, &value); err != nil || value.SchemaVersion != 1 || !validHTTPURL(value.Endpoint) ||
			!oneOf(value.Transport, "http", "sse", "streamable_http") || !bounded(value.EvidenceProfile, 1, 120) ||
			!bounded(value.QueryScope, 1, 1000) || len(value.Headers) > 20 {
			return fmt.Errorf("MCP source config is invalid")
		}
		for name, headerValue := range value.Headers {
			lowerName := strings.ToLower(strings.TrimSpace(name))
			if !bounded(name, 1, 80) || !bounded(headerValue, 1, 1000) || strings.ContainsAny(name+headerValue, "\r\n") ||
				oneOf(lowerName, "authorization", "proxy-authorization", "cookie", "set-cookie", "x-api-key", "api-key") {
				return fmt.Errorf("MCP source headers are invalid")
			}
		}
	default:
		return fmt.Errorf("unknown source kind")
	}
	return nil
}

func validateTriggerConfig(kind string, raw json.RawMessage) error {
	switch kind {
	case "signed_webhook":
		var value webhookConfig
		if err := decodeStrict(raw, &value); err != nil || value.SchemaVersion != 1 || len(value.EventTypes) == 0 ||
			len(value.EventTypes) > 32 || !bounded(value.DeduplicationKey, 1, 255) {
			return fmt.Errorf("signed webhook config is invalid")
		}
		for _, eventType := range value.EventTypes {
			if !bounded(eventType, 1, 80) {
				return fmt.Errorf("signed webhook event type is invalid")
			}
		}
	case "custom_rule":
		var value ruleConfig
		if err := decodeStrict(raw, &value); err != nil || value.SchemaVersion != 1 || value.GroupingWindowSeconds < 1 ||
			value.GroupingWindowSeconds > 86400 || !bounded(value.MatchExpression, 1, 4000) {
			return fmt.Errorf("custom rule config is invalid")
		}
	default:
		return fmt.Errorf("unknown trigger kind")
	}
	return nil
}

func validateCapabilities(values []string) error {
	if len(values) == 0 || len(values) > 4 {
		return fmt.Errorf("source capabilities are invalid")
	}
	known := []string{"push_ingestion", "pull_collection", "context_collection", "metric_collection"}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !slices.Contains(known, value) {
			return fmt.Errorf("source capability is invalid")
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("source capabilities contain duplicates")
		}
		seen[value] = struct{}{}
	}
	return nil
}

func decodeStrict(raw []byte, target any) error {
	if len(raw) == 0 || len(raw) > 65536 {
		return fmt.Errorf("configuration JSON size is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("configuration JSON must contain one object")
	}
	return nil
}

func validateOptionalUUIDv7(value *string) error {
	if value == nil {
		return nil
	}
	return validateRequiredUUIDv7(*value)
}

func validateRequiredUUIDv7(value string) error {
	parsed, err := uuid.Parse(value)
	if err != nil || parsed.Version() != 7 || parsed.String() != value {
		return fmt.Errorf("credential reference must be a canonical UUIDv7")
	}
	return nil
}

func validHTTPURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.User == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func bounded(value string, minimum, maximum int) bool {
	length := len(strings.TrimSpace(value))
	return length >= minimum && length <= maximum
}

func oneOf(value string, values ...string) bool { return slices.Contains(values, value) }
