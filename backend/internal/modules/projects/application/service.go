package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	authdomain "mendry/backend/internal/modules/auth/domain"
	"mendry/backend/internal/modules/projects/domain"
)

var (
	ErrInvalidInput          = errors.New("invalid project input")
	ErrNotFound              = errors.New("project not found")
	ErrForbidden             = errors.New("project operation forbidden")
	ErrConflict              = errors.New("project resource conflict")
	ErrMemberNotFound        = errors.New("project member user not found")
	ErrMemberChangeRejected  = errors.New("project member change rejected")
	ErrConfigurationNotFound = errors.New("project configuration not found")
	ErrGitUnreachable        = errors.New("git remote is unreachable")
	ErrLLMUnreachable        = errors.New("LLM provider is unreachable")
	ErrDockerUnavailable     = errors.New("Docker inventory is unavailable")
)

const (
	DefaultListLimit int32 = 50
	MaximumListLimit int32 = 100
)

// ListResult carries a stable item slice and the server-side total for a list response.
type ListResult[T any] struct {
	Items []T
	Total int64
}

type Repository interface {
	CreateProject(context.Context, domain.Project, string, string) (domain.Project, error)
	ListProjects(context.Context, string, bool, int32) (ListResult[domain.Project], error)
	ResolveProject(context.Context, string, string, bool) (domain.Project, error)
	ListMembers(context.Context, string) (ListResult[domain.Member], error)
	UpsertMember(context.Context, string, string, domain.Role, string, string) (domain.Member, error)
	DeleteMember(context.Context, string, string, string, string) (domain.Member, error)
	UpdateProjectName(context.Context, domain.Project, string, string) (domain.Project, error)
	CreateSecret(context.Context, domain.EncryptedSecret, string, string) (domain.Secret, error)
	UpdateSecret(context.Context, domain.EncryptedSecret, string, string, bool) (domain.Secret, error)
	ListSecrets(context.Context, string) (ListResult[domain.Secret], error)
	GetEncryptedSecret(context.Context, string, string) (domain.EncryptedSecret, error)
	GetConfiguration(context.Context, string) (domain.Configuration, error)
	GetConfigurationDraft(context.Context, string) (domain.ConfigurationDraft, error)
	UpsertConfiguration(context.Context, string, domain.Configuration, string, string) (domain.Configuration, error)
	UpsertEnvironment(context.Context, string, domain.Environment, string, string) (domain.Environment, error)
	UpsertRepository(context.Context, string, domain.Repository, string, string) (domain.Repository, error)
	UpsertSource(context.Context, string, string, domain.Source, string, string) (domain.Source, error)
	UpsertTrigger(context.Context, string, string, domain.Trigger, string, string) (domain.Trigger, error)
	UpsertLLMProvider(context.Context, string, domain.LLMProvider, string, string) (domain.LLMProvider, error)
	UpsertRemediationPolicy(context.Context, string, domain.RemediationPolicy, string, string) (domain.RemediationPolicy, error)
	LookupWebhookToken(context.Context, []byte) (WebhookIngress, error)
	UpdateWebhookToken(context.Context, string, []byte, []byte, []byte, string, string, bool) error
	ListAuditEvents(context.Context, string, int32) (ListResult[domain.AuditEvent], error)
}

// WebhookIngress 是路径 token 验通后定位到的项目与已启用 source。
type WebhookIngress struct {
	ProjectID string
	SourceID  string
	TriggerID string
	Provider  domain.WebhookProvider
	TopicARN  string
}

type Cipher interface {
	Encrypt(projectID, secretID string, kind domain.SecretKind, plaintext []byte) (ciphertext, nonce []byte, keyVersion int32, err error)
	Decrypt(projectID, secretID string, kind domain.SecretKind, ciphertext, nonce []byte) (plaintext []byte, err error)
	EncryptWebhookToken(projectID, triggerID string, plaintext []byte) (ciphertext, nonce []byte, err error)
	DecryptWebhookToken(projectID, triggerID string, ciphertext, nonce []byte) (plaintext []byte, err error)
}

type GitRefLister interface {
	ListRefs(ctx context.Context, remoteURL, transport string, kind domain.SecretKind, credential []byte) (string, error)
}

// LLMModelLister 用解密后的 bearer 列出 OpenAI 兼容 /v1/models，
// 并用一句固定 hi 探测已选模型是否真能完成对话。
type LLMModelLister interface {
	ListModels(ctx context.Context, baseURL string, apiKey []byte) ([]string, error)
	ProbeChat(ctx context.Context, baseURL string, apiKey []byte, model string) error
}

// ContainerProbeRequest 是 admin-only Docker inventory probe 的无命令请求。
// credential reference 由受信 adapter 解析；它不会携带明文 SSH credential。
type ContainerProbeRequest struct {
	ProjectID          string
	Host               string
	Port               int
	User               string
	CredentialSecretID string
}

// ContainerProbePort 通过固定的只读 SSH/Docker inventory 操作发现容器。
// 实现不得接受模型命令或返回原始 SSH/Docker 输出。
type ContainerProbePort interface {
	ListContainers(context.Context, ContainerProbeRequest) ([]domain.DockerContainer, error)
}

type LLMModels struct {
	Models []string
}

type Options struct {
	Repository Repository
	Cipher     Cipher
	Git        GitRefLister
	LLM        LLMModelLister
	Containers ContainerProbePort
	NewID      func() (string, error)
	// PublicURL 是派生完整入站地址的部署级基址；空值不得静默拼接。
	PublicURL       string
	NewWebhookToken func() (string, error)
}

type Service struct {
	repository      Repository
	cipher          Cipher
	git             GitRefLister
	llm             LLMModelLister
	containers      ContainerProbePort
	idGenerator     func() (string, error)
	publicURL       string
	newWebhookToken func() (string, error)
}

func NewService(options Options) (*Service, error) {
	if options.Repository == nil || options.Cipher == nil || options.NewID == nil {
		return nil, fmt.Errorf("project service dependencies are required")
	}
	if strings.TrimSpace(options.PublicURL) == "" {
		// 入站 URL 由部署级基址派生；空值会静默拼出错误地址，必须在构造时失败。
		return nil, fmt.Errorf("project public URL is required")
	}
	tokenGenerator := options.NewWebhookToken
	if tokenGenerator == nil {
		tokenGenerator = defaultWebhookToken
	}
	return &Service{
		repository: options.Repository, cipher: options.Cipher, git: options.Git, llm: options.LLM, containers: options.Containers,
		idGenerator: options.NewID, publicURL: options.PublicURL, newWebhookToken: tokenGenerator,
	}, nil
}

func (s *Service) CreateProject(ctx context.Context, principal authdomain.User, key, name, description string) (domain.Project, error) {
	if principal.Role != authdomain.RoleAdmin || !principal.Enabled {
		return domain.Project{}, ErrForbidden
	}
	normalizedKey, err := domain.NormalizeProjectKey(key)
	if err != nil {
		return domain.Project{}, ErrInvalidInput
	}
	ids, err := s.newIDs(2)
	if err != nil {
		return domain.Project{}, err
	}
	project := domain.Project{ID: ids[0], Key: normalizedKey, Name: name, Description: description, Role: domain.RoleAdmin}
	if err := domain.ValidateProject(project); err != nil {
		return domain.Project{}, ErrInvalidInput
	}
	return s.repository.CreateProject(ctx, project, principal.ID, ids[1])
}

func (s *Service) ListProjects(ctx context.Context, principal authdomain.User, limit int32) (ListResult[domain.Project], error) {
	if err := validatePrincipal(principal); err != nil {
		return ListResult[domain.Project]{}, err
	}
	if limit < 1 || limit > MaximumListLimit {
		return ListResult[domain.Project]{}, ErrInvalidInput
	}
	result, err := s.repository.ListProjects(ctx, principal.ID, principal.Role == authdomain.RoleAdmin, limit)
	return normalizeListResult(result, err)
}

func (s *Service) GetProject(ctx context.Context, principal authdomain.User, projectKey string) (domain.Project, error) {
	return s.resolve(ctx, principal, projectKey)
}

// UpdateProjectName 仅允许项目管理员改写展示名，并保持稳定 project key 不变。
func (s *Service) UpdateProjectName(ctx context.Context, principal authdomain.User, projectKey, name string) (domain.Project, error) {
	project, err := s.requireAdmin(ctx, principal, projectKey)
	if err != nil {
		return domain.Project{}, err
	}
	project.Name = strings.TrimSpace(name)
	if err := domain.ValidateProject(project); err != nil {
		return domain.Project{}, ErrInvalidInput
	}
	auditID, err := s.newID()
	if err != nil {
		return domain.Project{}, err
	}
	return s.repository.UpdateProjectName(ctx, project, principal.ID, auditID)
}

func (s *Service) ListMembers(ctx context.Context, principal authdomain.User, projectKey string) (ListResult[domain.Member], error) {
	project, err := s.resolve(ctx, principal, projectKey)
	if err != nil {
		return ListResult[domain.Member]{}, err
	}
	result, err := s.repository.ListMembers(ctx, project.ID)
	return normalizeListResult(result, err)
}

func (s *Service) UpsertMember(ctx context.Context, principal authdomain.User, projectKey, username, roleValue string) (domain.Member, error) {
	project, err := s.requireAdmin(ctx, principal, projectKey)
	if err != nil {
		return domain.Member{}, err
	}
	role, err := domain.ParseRole(roleValue)
	if err != nil {
		return domain.Member{}, ErrInvalidInput
	}
	normalizedUsername, err := authdomain.NormalizeUsername(username)
	if err != nil {
		return domain.Member{}, ErrInvalidInput
	}
	auditID, err := s.newID()
	if err != nil {
		return domain.Member{}, err
	}
	return s.repository.UpsertMember(ctx, project.ID, normalizedUsername, role, principal.ID, auditID)
}

func (s *Service) DeleteMember(ctx context.Context, principal authdomain.User, projectKey, username string) (domain.Member, error) {
	project, err := s.requireAdmin(ctx, principal, projectKey)
	if err != nil {
		return domain.Member{}, err
	}
	auditID, err := s.newID()
	if err != nil {
		return domain.Member{}, err
	}
	normalizedUsername, err := authdomain.NormalizeUsername(username)
	if err != nil {
		return domain.Member{}, ErrInvalidInput
	}
	return s.repository.DeleteMember(ctx, project.ID, normalizedUsername, principal.ID, auditID)
}

func (s *Service) CreateSecret(ctx context.Context, principal authdomain.User, projectKey, name, kindValue string, value []byte) (domain.Secret, error) {
	project, err := s.requireAdmin(ctx, principal, projectKey)
	if err != nil {
		return domain.Secret{}, err
	}
	kind, err := domain.ParseSecretKind(kindValue)
	if err != nil {
		return domain.Secret{}, ErrInvalidInput
	}
	ids, err := s.newIDs(2)
	if err != nil {
		return domain.Secret{}, err
	}
	secret := domain.Secret{ID: ids[0], ProjectID: project.ID, Name: name, Kind: kind}
	if err := domain.ValidateSecret(secret, value); err != nil {
		return domain.Secret{}, ErrInvalidInput
	}
	ciphertext, nonce, keyVersion, err := s.cipher.Encrypt(project.ID, secret.ID, kind, value)
	if err != nil {
		return domain.Secret{}, fmt.Errorf("encrypt project credential: %w", err)
	}
	secret.KeyVersion = keyVersion
	return s.repository.CreateSecret(ctx, domain.EncryptedSecret{Secret: secret, Ciphertext: ciphertext, Nonce: nonce}, principal.ID, ids[1])
}

// UpdateSecret 更新同一凭据的展示名；nil value 保留密文，非 nil value 按原 ID/kind 重新加密。
func (s *Service) UpdateSecret(ctx context.Context, principal authdomain.User, projectKey, secretID, name string, value []byte) (domain.Secret, error) {
	project, err := s.requireAdmin(ctx, principal, projectKey)
	if err != nil {
		return domain.Secret{}, err
	}
	encrypted, err := s.repository.GetEncryptedSecret(ctx, project.ID, secretID)
	if err != nil {
		return domain.Secret{}, err
	}
	encrypted.Name = strings.TrimSpace(name)
	rotate := value != nil
	if rotate {
		if err := domain.ValidateSecret(encrypted.Secret, value); err != nil {
			return domain.Secret{}, ErrInvalidInput
		}
		ciphertext, nonce, keyVersion, err := s.cipher.Encrypt(project.ID, encrypted.ID, encrypted.Kind, value)
		if err != nil {
			return domain.Secret{}, fmt.Errorf("encrypt project credential: %w", err)
		}
		encrypted.Ciphertext = ciphertext
		encrypted.Nonce = nonce
		encrypted.KeyVersion = keyVersion
	} else if err := domain.ValidateSecretMetadata(encrypted.Secret); err != nil {
		return domain.Secret{}, ErrInvalidInput
	}
	auditID, err := s.newID()
	if err != nil {
		return domain.Secret{}, err
	}
	return s.repository.UpdateSecret(ctx, encrypted, principal.ID, auditID, rotate)
}

func (s *Service) ListSecrets(ctx context.Context, principal authdomain.User, projectKey string) (ListResult[domain.Secret], error) {
	project, err := s.resolve(ctx, principal, projectKey)
	if err != nil {
		return ListResult[domain.Secret]{}, err
	}
	result, err := s.repository.ListSecrets(ctx, project.ID)
	return normalizeListResult(result, err)
}

func (s *Service) GetConfiguration(ctx context.Context, principal authdomain.User, projectKey string) (domain.Configuration, error) {
	project, err := s.resolve(ctx, principal, projectKey)
	if err != nil {
		return domain.Configuration{}, err
	}
	configuration, err := s.repository.GetConfiguration(ctx, project.ID)
	if err != nil {
		return domain.Configuration{}, err
	}
	if err := s.attachInboundURL(project, &configuration); err != nil {
		return domain.Configuration{}, err
	}
	return configuration, nil
}

// GetConfigurationDraft 返回编辑器所需的可部分保存配置。
func (s *Service) GetConfigurationDraft(ctx context.Context, principal authdomain.User, projectKey string) (domain.ConfigurationDraft, error) {
	project, err := s.resolve(ctx, principal, projectKey)
	if err != nil {
		return domain.ConfigurationDraft{}, err
	}
	draft, err := s.repository.GetConfigurationDraft(ctx, project.ID)
	if err != nil {
		return domain.ConfigurationDraft{}, err
	}
	if draft.Trigger != nil {
		if err := s.attachInboundURLToTrigger(project, draft.Trigger); err != nil {
			return domain.ConfigurationDraft{}, err
		}
	}
	return draft, nil
}

// PutConfigurationEnvironment 独立保存项目环境配置。
func (s *Service) PutConfigurationEnvironment(ctx context.Context, principal authdomain.User, projectKey string, environment domain.Environment) (domain.Environment, error) {
	project, err := s.requireAdmin(ctx, principal, projectKey)
	if err != nil {
		return domain.Environment{}, err
	}
	if err := domain.ValidateEnvironment(environment); err != nil {
		return domain.Environment{}, ErrInvalidInput
	}
	draft, err := s.repository.GetConfigurationDraft(ctx, project.ID)
	if err != nil {
		return domain.Environment{}, err
	}
	if draft.Environment != nil {
		environment.ID = draft.Environment.ID
	} else if environment.ID == "" {
		environment.ID, err = s.newID()
		if err != nil {
			return domain.Environment{}, err
		}
	}
	auditID, err := s.newID()
	if err != nil {
		return domain.Environment{}, err
	}
	return s.repository.UpsertEnvironment(ctx, project.ID, environment, principal.ID, auditID)
}

// PutConfigurationRepository 独立保存 Git 仓库配置。
func (s *Service) PutConfigurationRepository(ctx context.Context, principal authdomain.User, projectKey string, repository domain.Repository) (domain.Repository, error) {
	project, err := s.requireAdmin(ctx, principal, projectKey)
	if err != nil {
		return domain.Repository{}, err
	}
	if err := domain.ValidateRepository(repository); err != nil {
		return domain.Repository{}, ErrInvalidInput
	}
	draft, err := s.repository.GetConfigurationDraft(ctx, project.ID)
	if err != nil {
		return domain.Repository{}, err
	}
	if draft.Repository != nil {
		repository.ID = draft.Repository.ID
	} else if repository.ID == "" {
		repository.ID, err = s.newID()
		if err != nil {
			return domain.Repository{}, err
		}
	}
	auditID, err := s.newID()
	if err != nil {
		return domain.Repository{}, err
	}
	return s.repository.UpsertRepository(ctx, project.ID, repository, principal.ID, auditID)
}

// PutConfigurationSource 独立保存 collection source 配置。
func (s *Service) PutConfigurationSource(ctx context.Context, principal authdomain.User, projectKey string, source domain.Source) (domain.Source, error) {
	project, err := s.requireAdmin(ctx, principal, projectKey)
	if err != nil {
		return domain.Source{}, err
	}
	if err := domain.ValidateSource(source); err != nil {
		return domain.Source{}, ErrInvalidInput
	}
	draft, err := s.repository.GetConfigurationDraft(ctx, project.ID)
	if err != nil {
		return domain.Source{}, err
	}
	environment, err := s.ensureConfigurationEnvironment(ctx, project, draft, principal.ID)
	if err != nil {
		return domain.Source{}, err
	}
	if draft.Source != nil {
		source.ID = draft.Source.ID
	} else if source.ID == "" {
		source.ID, err = s.newID()
		if err != nil {
			return domain.Source{}, err
		}
	}
	auditID, err := s.newID()
	if err != nil {
		return domain.Source{}, err
	}
	return s.repository.UpsertSource(ctx, project.ID, environment.ID, source, principal.ID, auditID)
}

// PutConfigurationTrigger 独立保存 trigger 配置，并在首次保存 signed_webhook 时签发 token。
func (s *Service) PutConfigurationTrigger(ctx context.Context, principal authdomain.User, projectKey string, trigger domain.Trigger) (domain.Trigger, error) {
	project, err := s.requireAdmin(ctx, principal, projectKey)
	if err != nil {
		return domain.Trigger{}, err
	}
	if err := domain.ValidateTrigger(trigger); err != nil {
		return domain.Trigger{}, ErrInvalidInput
	}
	draft, err := s.repository.GetConfigurationDraft(ctx, project.ID)
	if err != nil {
		return domain.Trigger{}, err
	}
	environment, err := s.ensureConfigurationEnvironment(ctx, project, draft, principal.ID)
	if err != nil {
		return domain.Trigger{}, err
	}
	var current domain.Trigger
	if draft.Trigger != nil {
		current = *draft.Trigger
		trigger.ID = current.ID
	} else if trigger.ID == "" {
		trigger.ID, err = s.newID()
		if err != nil {
			return domain.Trigger{}, err
		}
	}
	configuration := domain.Configuration{Trigger: trigger}
	if err := s.applyWebhookToken(project.ID, &configuration, current); err != nil {
		return domain.Trigger{}, err
	}
	auditID, err := s.newID()
	if err != nil {
		return domain.Trigger{}, err
	}
	saved, err := s.repository.UpsertTrigger(ctx, project.ID, environment.ID, configuration.Trigger, principal.ID, auditID)
	if err != nil {
		return domain.Trigger{}, err
	}
	if err := s.attachInboundURLToTrigger(project, &saved); err != nil {
		return domain.Trigger{}, err
	}
	return saved, nil
}

// PutConfigurationLLMProvider 独立保存 LLM provider 配置。
func (s *Service) PutConfigurationLLMProvider(ctx context.Context, principal authdomain.User, projectKey string, provider domain.LLMProvider) (domain.LLMProvider, error) {
	project, err := s.requireAdmin(ctx, principal, projectKey)
	if err != nil {
		return domain.LLMProvider{}, err
	}
	if err := domain.ValidateLLMProvider(provider); err != nil {
		return domain.LLMProvider{}, ErrInvalidInput
	}
	draft, err := s.repository.GetConfigurationDraft(ctx, project.ID)
	if err != nil {
		return domain.LLMProvider{}, err
	}
	if draft.LLM != nil {
		provider.ID = draft.LLM.ID
	} else if provider.ID == "" {
		provider.ID, err = s.newID()
		if err != nil {
			return domain.LLMProvider{}, err
		}
	}
	auditID, err := s.newID()
	if err != nil {
		return domain.LLMProvider{}, err
	}
	return s.repository.UpsertLLMProvider(ctx, project.ID, provider, principal.ID, auditID)
}

// PutConfigurationRemediationPolicy is the authorized project-policy source
// used by root remediation run creation. Existing runs are immutable snapshots.
func (s *Service) PutConfigurationRemediationPolicy(ctx context.Context, principal authdomain.User, projectKey string, policy domain.RemediationPolicy) (domain.RemediationPolicy, error) {
	project, err := s.requireAdmin(ctx, principal, projectKey)
	if err != nil {
		return domain.RemediationPolicy{}, err
	}
	if err := domain.ValidateRemediationPolicy(policy); err != nil {
		return domain.RemediationPolicy{}, ErrInvalidInput
	}
	auditID, err := s.newID()
	if err != nil {
		return domain.RemediationPolicy{}, err
	}
	return s.repository.UpsertRemediationPolicy(ctx, project.ID, policy, principal.ID, auditID)
}

func (s *Service) ensureConfigurationEnvironment(ctx context.Context, project domain.Project, draft domain.ConfigurationDraft, actorUserID string) (domain.Environment, error) {
	if draft.Environment != nil {
		return *draft.Environment, nil
	}
	ids, err := s.newIDs(2)
	if err != nil {
		return domain.Environment{}, err
	}
	environment := domain.Environment{ID: ids[0], Key: project.Key, Name: project.Name}
	if err := domain.ValidateEnvironment(environment); err != nil {
		return domain.Environment{}, ErrInvalidInput
	}
	return s.repository.UpsertEnvironment(ctx, project.ID, environment, actorUserID, ids[1])
}

func (s *Service) PutConfiguration(ctx context.Context, principal authdomain.User, projectKey string, configuration domain.Configuration) (domain.Configuration, error) {
	project, err := s.requireAdmin(ctx, principal, projectKey)
	if err != nil {
		return domain.Configuration{}, err
	}
	if err := domain.ValidateConfiguration(configuration); err != nil {
		return domain.Configuration{}, ErrInvalidInput
	}
	if configuration.LLM == nil {
		return domain.Configuration{}, ErrInvalidInput
	}
	current, err := s.repository.GetConfiguration(ctx, project.ID)
	if err != nil && !errors.Is(err, ErrConfigurationNotFound) {
		return domain.Configuration{}, err
	}
	ids, err := s.newIDs(6)
	if err != nil {
		return domain.Configuration{}, err
	}
	configuration.Environment.ID = ids[0]
	configuration.Repository.ID = ids[1]
	configuration.Source.ID = ids[2]
	configuration.Trigger.ID = ids[3]
	configuration.LLM.ID = ids[4]
	if current.Trigger.ID != "" {
		configuration.Trigger.ID = current.Trigger.ID
	}
	if err := s.applyWebhookToken(project.ID, &configuration, current.Trigger); err != nil {
		return domain.Configuration{}, err
	}
	saved, err := s.repository.UpsertConfiguration(ctx, project.ID, configuration, principal.ID, ids[5])
	if err != nil {
		return domain.Configuration{}, err
	}
	if err := s.attachInboundURL(project, &saved); err != nil {
		return domain.Configuration{}, err
	}
	return saved, nil
}

// RotateWebhookToken 在 signed_webhook 上创建或轮换入站 token，并返回完整公开地址。
func (s *Service) RotateWebhookToken(ctx context.Context, principal authdomain.User, projectKey string) (string, error) {
	project, err := s.requireAdmin(ctx, principal, projectKey)
	if err != nil {
		return "", err
	}
	configuration, err := s.repository.GetConfiguration(ctx, project.ID)
	if err != nil {
		return "", err
	}
	// 只认已落库的 trigger kind。向导草稿切到 signed_webhook 并不等于可以签发 token。
	if configuration.Trigger.Kind != "signed_webhook" {
		return "", ErrInvalidInput
	}
	rotated := len(configuration.Trigger.IngressTokenHash) > 0
	plaintext, hash, ciphertext, nonce, err := s.issueWebhookToken(project.ID, configuration.Trigger.ID)
	if err != nil {
		return "", err
	}
	defer clearBytes(plaintext)
	auditID, err := s.newID()
	if err != nil {
		return "", err
	}
	if err := s.repository.UpdateWebhookToken(ctx, project.ID, hash, ciphertext, nonce, principal.ID, auditID, rotated); err != nil {
		return "", err
	}
	return domain.InboundWebhookURL(s.publicURL, string(plaintext))
}

// LookupWebhookToken 用路径 token 定位已启用的 signed_webhook 与同项目 source。
func (s *Service) LookupWebhookToken(ctx context.Context, token string) (WebhookIngress, error) {
	normalized, err := domain.ParseWebhookToken(token)
	if err != nil {
		// 形状错误在查找前拒绝，避免把任意路径字符串送进哈希查询。
		return WebhookIngress{}, ErrInvalidInput
	}
	sum := sha256.Sum256([]byte(normalized))
	return s.repository.LookupWebhookToken(ctx, sum[:])
}

func (s *Service) ListAuditEvents(ctx context.Context, principal authdomain.User, projectKey string, limit int32) (ListResult[domain.AuditEvent], error) {
	project, err := s.resolve(ctx, principal, projectKey)
	if err != nil {
		return ListResult[domain.AuditEvent]{}, err
	}
	if limit < 1 || limit > MaximumListLimit {
		return ListResult[domain.AuditEvent]{}, ErrInvalidInput
	}
	result, err := s.repository.ListAuditEvents(ctx, project.ID, limit)
	return normalizeListResult(result, err)
}

func (s *Service) ResolveAccess(ctx context.Context, principal authdomain.User, projectKey string) (domain.Project, error) {
	return s.resolve(ctx, principal, projectKey)
}

func (s *Service) RequireIncidentWrite(ctx context.Context, principal authdomain.User, projectKey string) (domain.Project, error) {
	project, err := s.resolve(ctx, principal, projectKey)
	if err != nil {
		return domain.Project{}, err
	}
	if !project.CanWriteIncidents() {
		return domain.Project{}, ErrForbidden
	}
	return project, nil
}

func (s *Service) resolve(ctx context.Context, principal authdomain.User, projectKey string) (domain.Project, error) {
	if err := validatePrincipal(principal); err != nil {
		return domain.Project{}, err
	}
	normalizedKey, err := domain.NormalizeProjectKey(projectKey)
	if err != nil {
		return domain.Project{}, ErrNotFound
	}
	return s.repository.ResolveProject(ctx, normalizedKey, principal.ID, principal.Role == authdomain.RoleAdmin)
}

func (s *Service) ProbeRepositoryRefs(ctx context.Context, principal authdomain.User, projectKey, remoteURL, transport, secretID string) (RepositoryRefs, error) {
	if s.git == nil {
		return RepositoryRefs{}, fmt.Errorf("git ref lister is required")
	}
	project, err := s.requireAdmin(ctx, principal, projectKey)
	if err != nil {
		return RepositoryRefs{}, err
	}
	if err := domain.ValidateRepositoryProbe(remoteURL, transport); err != nil {
		return RepositoryRefs{}, ErrInvalidInput
	}
	encrypted, err := s.repository.GetEncryptedSecret(ctx, project.ID, secretID)
	if err != nil {
		return RepositoryRefs{}, err
	}
	plaintext, err := s.cipher.Decrypt(project.ID, encrypted.ID, encrypted.Kind, encrypted.Ciphertext, encrypted.Nonce)
	if err != nil {
		return RepositoryRefs{}, fmt.Errorf("decrypt project credential: %w", err)
	}
	defer clearBytes(plaintext)
	output, err := s.git.ListRefs(ctx, remoteURL, transport, encrypted.Kind, plaintext)
	if err != nil {
		return RepositoryRefs{}, ErrGitUnreachable
	}
	return ParseGitLsRemote(output)
}

// ProbeSSHContainers 返回有界的 Docker inventory；容器名是唯一可持久化的选择器。
// 项目服务只负责成员/credential 所有权校验，SSH/Docker 命令由受信 adapter 执行。
func (s *Service) ProbeSSHContainers(ctx context.Context, principal authdomain.User, projectKey, host string, port int, user, secretID string) ([]domain.DockerContainer, error) {
	if s.containers == nil {
		return nil, ErrDockerUnavailable
	}
	project, err := s.requireAdmin(ctx, principal, projectKey)
	if err != nil {
		return nil, err
	}
	if err := domain.ValidateSSHContainerProbe(host, port, user, secretID); err != nil {
		return nil, ErrInvalidInput
	}
	encrypted, err := s.repository.GetEncryptedSecret(ctx, project.ID, secretID)
	if err != nil {
		return nil, ErrDockerUnavailable
	}
	if encrypted.Kind != domain.SecretSSHPrivateKey {
		return nil, ErrInvalidInput
	}
	containers, err := s.containers.ListContainers(ctx, ContainerProbeRequest{
		ProjectID: project.ID, Host: strings.TrimSpace(host), Port: port,
		User: strings.TrimSpace(user), CredentialSecretID: secretID,
	})
	if err != nil {
		return nil, ErrDockerUnavailable
	}
	if len(containers) > 100 {
		containers = containers[:100]
	}
	return append([]domain.DockerContainer(nil), containers...), nil
}

func (s *Service) ProbeLLMModels(ctx context.Context, principal authdomain.User, projectKey, baseURL, secretID string) (LLMModels, error) {
	if s.llm == nil {
		return LLMModels{}, fmt.Errorf("LLM model lister is required")
	}
	project, err := s.requireAdmin(ctx, principal, projectKey)
	if err != nil {
		return LLMModels{}, err
	}
	if err := domain.ValidateLLMModelsProbe(baseURL, secretID); err != nil {
		return LLMModels{}, ErrInvalidInput
	}
	encrypted, err := s.repository.GetEncryptedSecret(ctx, project.ID, secretID)
	if err != nil {
		return LLMModels{}, err
	}
	if encrypted.Kind != domain.SecretHTTPBearer {
		return LLMModels{}, ErrInvalidInput
	}
	plaintext, err := s.cipher.Decrypt(project.ID, encrypted.ID, encrypted.Kind, encrypted.Ciphertext, encrypted.Nonce)
	if err != nil {
		return LLMModels{}, fmt.Errorf("decrypt project credential: %w", err)
	}
	defer clearBytes(plaintext)
	models, err := s.llm.ListModels(ctx, baseURL, plaintext)
	if err != nil {
		return LLMModels{}, ErrLLMUnreachable
	}
	return LLMModels{Models: models}, nil
}

func (s *Service) ProbeLLMChat(ctx context.Context, principal authdomain.User, projectKey, baseURL, secretID, model string) error {
	if s.llm == nil {
		return fmt.Errorf("LLM model lister is required")
	}
	project, err := s.requireAdmin(ctx, principal, projectKey)
	if err != nil {
		return err
	}
	if err := domain.ValidateLLMChatProbe(baseURL, secretID, model); err != nil {
		return ErrInvalidInput
	}
	encrypted, err := s.repository.GetEncryptedSecret(ctx, project.ID, secretID)
	if err != nil {
		return err
	}
	if encrypted.Kind != domain.SecretHTTPBearer {
		return ErrInvalidInput
	}
	plaintext, err := s.cipher.Decrypt(project.ID, encrypted.ID, encrypted.Kind, encrypted.Ciphertext, encrypted.Nonce)
	if err != nil {
		return fmt.Errorf("decrypt project credential: %w", err)
	}
	defer clearBytes(plaintext)
	if err := s.llm.ProbeChat(ctx, baseURL, plaintext, model); err != nil {
		return ErrLLMUnreachable
	}
	return nil
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func defaultWebhookToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate webhook token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (s *Service) applyWebhookToken(projectID string, configuration *domain.Configuration, current domain.Trigger) error {
	configuration.Trigger.InboundURL = ""
	if configuration.Trigger.Kind != "signed_webhook" {
		// 离开 signed_webhook 必须清掉三列，旧 URL 立刻失效且不得残留可解密密文。
		configuration.Trigger.IngressTokenHash = nil
		configuration.Trigger.IngressTokenCiphertext = nil
		configuration.Trigger.IngressTokenNonce = nil
		return nil
	}
	if len(current.IngressTokenHash) > 0 {
		configuration.Trigger.IngressTokenHash = current.IngressTokenHash
		configuration.Trigger.IngressTokenCiphertext = current.IngressTokenCiphertext
		configuration.Trigger.IngressTokenNonce = current.IngressTokenNonce
		return nil
	}
	plaintext, hash, ciphertext, nonce, err := s.issueWebhookToken(projectID, configuration.Trigger.ID)
	if err != nil {
		return err
	}
	clearBytes(plaintext)
	configuration.Trigger.IngressTokenHash = hash
	configuration.Trigger.IngressTokenCiphertext = ciphertext
	configuration.Trigger.IngressTokenNonce = nonce
	return nil
}

func (s *Service) issueWebhookToken(projectID, triggerID string) ([]byte, []byte, []byte, []byte, error) {
	token, err := s.newWebhookToken()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if _, err := domain.ParseWebhookToken(token); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("generated webhook token is invalid: %w", err)
	}
	plaintext := []byte(token)
	ciphertext, nonce, err := s.cipher.EncryptWebhookToken(projectID, triggerID, plaintext)
	if err != nil {
		clearBytes(plaintext)
		return nil, nil, nil, nil, fmt.Errorf("encrypt webhook token: %w", err)
	}
	sum := sha256.Sum256(plaintext)
	return plaintext, sum[:], ciphertext, nonce, nil
}

func (s *Service) attachInboundURL(project domain.Project, configuration *domain.Configuration) error {
	return s.attachInboundURLToTrigger(project, &configuration.Trigger)
}

func (s *Service) attachInboundURLToTrigger(project domain.Project, trigger *domain.Trigger) error {
	trigger.InboundURL = ""
	if !project.CanAdminister() || trigger.Kind != "signed_webhook" {
		return nil
	}
	if len(trigger.IngressTokenCiphertext) == 0 || len(trigger.IngressTokenNonce) == 0 {
		return nil
	}
	plaintext, err := s.cipher.DecryptWebhookToken(project.ID, trigger.ID, trigger.IngressTokenCiphertext, trigger.IngressTokenNonce)
	if err != nil {
		return fmt.Errorf("decrypt webhook token: %w", err)
	}
	defer clearBytes(plaintext)
	token, err := domain.ParseWebhookToken(string(plaintext))
	if err != nil {
		return fmt.Errorf("stored webhook token is invalid: %w", err)
	}
	inbound, err := domain.InboundWebhookURL(s.publicURL, token)
	if err != nil {
		return err
	}
	trigger.InboundURL = inbound
	return nil
}

func (s *Service) requireAdmin(ctx context.Context, principal authdomain.User, projectKey string) (domain.Project, error) {
	project, err := s.resolve(ctx, principal, projectKey)
	if err != nil {
		return domain.Project{}, err
	}
	if !project.CanAdminister() {
		return domain.Project{}, ErrForbidden
	}
	return project, nil
}

func (s *Service) newID() (string, error) {
	id, err := s.idGenerator()
	if err != nil {
		return "", fmt.Errorf("generate project resource ID: %w", err)
	}
	return id, nil
}

func (s *Service) newIDs(count int) ([]string, error) {
	ids := make([]string, count)
	for index := range ids {
		id, err := s.idGenerator()
		if err != nil {
			return nil, fmt.Errorf("generate project resource ID: %w", err)
		}
		ids[index] = id
	}
	return ids, nil
}

func validatePrincipal(principal authdomain.User) error {
	if principal.ID == "" || !principal.Enabled {
		return ErrForbidden
	}
	return nil
}

func normalizeListResult[T any](result ListResult[T], err error) (ListResult[T], error) {
	if err != nil {
		return ListResult[T]{}, err
	}
	if result.Items == nil {
		result.Items = []T{}
	}
	return result, nil
}
