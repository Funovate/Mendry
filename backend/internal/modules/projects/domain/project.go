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
	Name               string
	Kind               string
	CredentialSecretID *string
	Config             json.RawMessage
	Capabilities       []string
	Enabled            bool
	Version            int64
}

type Trigger struct {
	ID              string
	Name            string
	Kind            string
	SigningSecretID *string
	Config          json.RawMessage
	Enabled         bool
	Version         int64
}

type Configuration struct {
	Environment Environment
	Repository  Repository
	Source      Source
	Trigger     Trigger
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
)

func IsCommitSHA(value string) bool {
	return commitPattern.MatchString(value)
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

func ValidateRepositoryProbe(remoteURL, transport string) error {
	remote, err := url.Parse(remoteURL)
	if err != nil || remote.User != nil || remote.Host == "" || !bounded(remoteURL, 1, 2048) ||
		!oneOf(remote.Scheme, "https", "ssh") || remote.Scheme != transport || !oneOf(transport, "https", "ssh") {
		return fmt.Errorf("repository remote URL is invalid")
	}
	return nil
}

func ValidateConfiguration(configuration Configuration) error {
	environment := configuration.Environment
	if !environmentKeyPattern.MatchString(environment.Key) || !bounded(environment.Name, 1, 120) ||
		(environment.Service != nil && !bounded(*environment.Service, 1, 120)) {
		return fmt.Errorf("environment configuration is invalid")
	}

	repository := configuration.Repository
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
	for _, reference := range []*string{repository.CredentialSecretID, configuration.Source.CredentialSecretID, configuration.Trigger.SigningSecretID} {
		if err := validateOptionalUUIDv7(reference); err != nil {
			return err
		}
	}

	if !bounded(configuration.Source.Name, 1, 120) || !oneOf(configuration.Source.Kind, "ssh", "cloud", "mcp") {
		return fmt.Errorf("source configuration is invalid")
	}
	if err := validateCapabilities(configuration.Source.Capabilities); err != nil {
		return err
	}
	if err := validateSourceConfig(configuration.Source.Kind, configuration.Source.Config); err != nil {
		return err
	}

	if !bounded(configuration.Trigger.Name, 1, 120) || !oneOf(configuration.Trigger.Kind, "signed_webhook", "custom_rule") {
		return fmt.Errorf("trigger configuration is invalid")
	}
	if err := validateTriggerConfig(configuration.Trigger.Kind, configuration.Trigger.Config); err != nil {
		return err
	}
	if configuration.Trigger.Kind == "signed_webhook" && configuration.Trigger.SigningSecretID == nil {
		return fmt.Errorf("signed webhook requires a signing secret reference")
	}
	return nil
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
	parsed, err := uuid.Parse(*value)
	if err != nil || parsed.Version() != 7 || parsed.String() != *value {
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
