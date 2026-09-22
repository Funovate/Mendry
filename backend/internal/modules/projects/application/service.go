package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	authdomain "mendry/backend/internal/modules/auth/domain"
	"mendry/backend/internal/modules/projects/domain"
)

var (
	ErrInvalidInput          = errors.New("invalid project input")
	ErrNotFound              = errors.New("project not found")
	ErrForbidden             = errors.New("project operation forbidden")
	ErrConflict              = errors.New("project resource conflict")
	ErrConfigurationNotFound = errors.New("project configuration not found")
	ErrGitUnreachable        = errors.New("git remote is unreachable")
	ErrLLMUnreachable        = errors.New("LLM provider is unreachable")
	ErrDockerUnavailable     = errors.New("Docker inventory is unavailable")
	ErrLogProbeUnavailable   = errors.New("managed log probe is unavailable")
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
	CreateProject(context.Context, domain.Project) (domain.Project, error)
	ListProjects(context.Context, int32) (ListResult[domain.Project], error)
	ResolveProject(context.Context, string) (domain.Project, error)
	UpdateProjectName(context.Context, domain.Project) (domain.Project, error)
	CreateSecret(context.Context, domain.EncryptedSecret) (domain.Secret, error)
	UpdateSecret(context.Context, domain.EncryptedSecret) (domain.Secret, error)
	ListSecrets(context.Context, string) (ListResult[domain.Secret], error)
	GetEncryptedSecret(context.Context, string, string) (domain.EncryptedSecret, error)
	GetConfiguration(context.Context, string) (domain.Configuration, error)
	GetConfigurationDraft(context.Context, string) (domain.ConfigurationDraft, error)
	UpsertConfiguration(context.Context, string, domain.Configuration) (domain.Configuration, error)
	UpsertEnvironment(context.Context, string, domain.Environment) (domain.Environment, error)
	UpsertRepository(context.Context, string, domain.Repository) (domain.Repository, error)
	UpsertSource(context.Context, string, string, domain.Source) (domain.Source, error)
	UpsertTrigger(context.Context, string, string, domain.Trigger) (domain.Trigger, error)
	UpsertLLMProvider(context.Context, string, domain.LLMProvider) (domain.LLMProvider, error)
	UpsertRemediationPolicy(context.Context, string, domain.RemediationPolicy) (domain.RemediationPolicy, error)
	LookupWebhookToken(context.Context, []byte) (WebhookIngress, error)
	UpdateWebhookToken(context.Context, string, []byte, []byte, []byte) error
}

type RepositoryChangeObserver interface {
	RepositoryUpdated(context.Context, string, domain.Repository, domain.Repository, domain.RemediationPolicy)
}

// WebhookIngress 是路径 token 验通后定位到的项目与已启用 source。
type WebhookIngress struct {
	ProjectID      string
	SourceID       string
	TriggerID      string
	TriggerKind    string
	TriggerVersion int64
	Provider       domain.WebhookProvider
	TopicARN       string
	CustomRules    domain.CustomRuleConfig
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

// ContainerProbeRequest 是 authenticated Docker inventory probe 的无命令请求。
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

type LogProbeRequest struct {
	ProjectID          string
	Source             domain.SSHSourceConfig
	CredentialSecretID string
	InboundURL         string
	TriggerVersion     int64
	Rules              domain.CustomRuleConfig
}

type LogProbeStatus struct {
	State         string    `json:"state"`
	Version       string    `json:"version"`
	ConfigVersion int64     `json:"configVersion"`
	CheckedAt     time.Time `json:"checkedAt"`
	Message       string    `json:"message"`
}

type LogProbeManagerPort interface {
	Install(context.Context, LogProbeRequest) (LogProbeStatus, error)
	Status(context.Context, LogProbeRequest) (LogProbeStatus, error)
	Uninstall(context.Context, LogProbeRequest) (LogProbeStatus, error)
}

type LogRuleGenerator interface {
	GenerateLogRule(context.Context, string, []byte, string, string, string) (domain.CustomRule, error)
}

type LogRuleGenerationInput struct {
	Intent string
	Sample string
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
	LogFiles   LogFileBrowserPort
	LogProbes  LogProbeManagerPort
	NewID      func() (string, error)
	// PublicURL 是派生完整入站地址的部署级基址；空值不得静默拼接。
	PublicURL       string
	NewWebhookToken func() (string, error)
}

type Service struct {
	repository               Repository
	cipher                   Cipher
	git                      GitRefLister
	llm                      LLMModelLister
	containers               ContainerProbePort
	logFiles                 LogFileBrowserPort
	logProbes                LogProbeManagerPort
	idGenerator              func() (string, error)
	publicURL                string
	newWebhookToken          func() (string, error)
	repositoryChangeObserver RepositoryChangeObserver
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
		logFiles: options.LogFiles, logProbes: options.LogProbes, idGenerator: options.NewID, publicURL: options.PublicURL, newWebhookToken: tokenGenerator,
	}, nil
}

func (s *Service) CreateProject(ctx context.Context, principal authdomain.User, key, name, description string) (domain.Project, error) {
	if err := validatePrincipal(principal); err != nil {
		return domain.Project{}, err
	}
	normalizedKey, err := domain.NormalizeProjectKey(key)
	if err != nil {
		return domain.Project{}, ErrInvalidInput
	}
	id, err := s.newID()
	if err != nil {
		return domain.Project{}, err
	}
	project := domain.Project{ID: id, Key: normalizedKey, Name: name, Description: description}
	if err := domain.ValidateProject(project); err != nil {
		return domain.Project{}, ErrInvalidInput
	}
	return s.repository.CreateProject(ctx, project)
}

func (s *Service) ListProjects(ctx context.Context, principal authdomain.User, limit int32) (ListResult[domain.Project], error) {
	if err := validatePrincipal(principal); err != nil {
		return ListResult[domain.Project]{}, err
	}
	if limit < 1 || limit > MaximumListLimit {
		return ListResult[domain.Project]{}, ErrInvalidInput
	}
	result, err := s.repository.ListProjects(ctx, limit)
	return normalizeListResult(result, err)
}

func (s *Service) GetProject(ctx context.Context, principal authdomain.User, projectKey string) (domain.Project, error) {
	return s.resolve(ctx, principal, projectKey)
}

// UpdateProjectName 更新项目展示名，并保持稳定 project key 不变。
func (s *Service) UpdateProjectName(ctx context.Context, principal authdomain.User, projectKey, name string) (domain.Project, error) {
	project, err := s.resolveProject(ctx, principal, projectKey)
	if err != nil {
		return domain.Project{}, err
	}
	project.Name = strings.TrimSpace(name)
	if err := domain.ValidateProject(project); err != nil {
		return domain.Project{}, ErrInvalidInput
	}
	return s.repository.UpdateProjectName(ctx, project)
}

func (s *Service) CreateSecret(ctx context.Context, principal authdomain.User, projectKey, name, kindValue string, value []byte) (domain.Secret, error) {
	project, err := s.resolveProject(ctx, principal, projectKey)
	if err != nil {
		return domain.Secret{}, err
	}
	kind, err := domain.ParseSecretKind(kindValue)
	if err != nil {
		return domain.Secret{}, ErrInvalidInput
	}
	id, err := s.newID()
	if err != nil {
		return domain.Secret{}, err
	}
	secret := domain.Secret{ID: id, ProjectID: project.ID, Name: name, Kind: kind}
	if err := domain.ValidateSecret(secret, value); err != nil {
		return domain.Secret{}, ErrInvalidInput
	}
	ciphertext, nonce, keyVersion, err := s.cipher.Encrypt(project.ID, secret.ID, kind, value)
	if err != nil {
		return domain.Secret{}, fmt.Errorf("encrypt project credential: %w", err)
	}
	secret.KeyVersion = keyVersion
	return s.repository.CreateSecret(ctx, domain.EncryptedSecret{Secret: secret, Ciphertext: ciphertext, Nonce: nonce})
}

// UpdateSecret 更新同一凭据的展示名；nil value 保留密文，非 nil value 按原 ID/kind 重新加密。
func (s *Service) UpdateSecret(ctx context.Context, principal authdomain.User, projectKey, secretID, name string, value []byte) (domain.Secret, error) {
	project, err := s.resolveProject(ctx, principal, projectKey)
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
	return s.repository.UpdateSecret(ctx, encrypted)
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
	project, err := s.resolveProject(ctx, principal, projectKey)
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
	return s.repository.UpsertEnvironment(ctx, project.ID, environment)
}

func (s *Service) SetRepositoryChangeObserver(observer RepositoryChangeObserver) {
	if s != nil {
		s.repositoryChangeObserver = observer
	}
}

func (s *Service) PutConfigurationRepository(ctx context.Context, principal authdomain.User, projectKey string, repository domain.Repository) (domain.Repository, error) {
	project, err := s.resolveProject(ctx, principal, projectKey)
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
	saved, err := s.repository.UpsertRepository(ctx, project.ID, repository)
	if err != nil {
		return domain.Repository{}, err
	}
	if s.repositoryChangeObserver != nil && draft.Repository != nil && draft.Remediation != nil &&
		draft.Remediation.ExecutionMode == domain.RemediationExecutionAutoHotfix &&
		draft.Repository.DeployedCommit != saved.DeployedCommit {
		s.repositoryChangeObserver.RepositoryUpdated(context.WithoutCancel(ctx), project.ID, *draft.Repository, saved, *draft.Remediation)
	}
	return saved, nil
}

// PutConfigurationSource 独立保存 collection source 配置。
func (s *Service) PutConfigurationSource(ctx context.Context, principal authdomain.User, projectKey string, source domain.Source) (domain.Source, error) {
	project, err := s.resolveProject(ctx, principal, projectKey)
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
	environment, err := s.ensureConfigurationEnvironment(ctx, project, draft)
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
	return s.repository.UpsertSource(ctx, project.ID, environment.ID, source)
}

// PutConfigurationTrigger 独立保存 trigger 配置，并在首次保存 webhook 或 custom rule 时签发 token。
func (s *Service) PutConfigurationTrigger(ctx context.Context, principal authdomain.User, projectKey string, trigger domain.Trigger) (domain.Trigger, error) {
	project, err := s.resolveProject(ctx, principal, projectKey)
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
	environment, err := s.ensureConfigurationEnvironment(ctx, project, draft)
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
	saved, err := s.repository.UpsertTrigger(ctx, project.ID, environment.ID, configuration.Trigger)
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
	project, err := s.resolveProject(ctx, principal, projectKey)
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
	return s.repository.UpsertLLMProvider(ctx, project.ID, provider)
}

// PutConfigurationRemediationPolicy is the authorized project-policy source
// used by root remediation run creation. Existing runs are immutable snapshots.
func (s *Service) PutConfigurationRemediationPolicy(ctx context.Context, principal authdomain.User, projectKey string, policy domain.RemediationPolicy) (domain.RemediationPolicy, error) {
	project, err := s.resolveProject(ctx, principal, projectKey)
	if err != nil {
		return domain.RemediationPolicy{}, err
	}
	policy = domain.NormalizeRemediationPolicy(policy)
	if err := domain.ValidateRemediationPolicy(policy); err != nil {
		return domain.RemediationPolicy{}, ErrInvalidInput
	}
	if policy.ExecutionMode == domain.RemediationExecutionAutoHotfix && (policy.ValidationProfile.Enabled == nil || *policy.ValidationProfile.Enabled) {
		// Enhanced profiles contain platform-owned image and command identities.
		// Only the checked preparation flow may persist them.
		return domain.RemediationPolicy{}, ErrInvalidInput
	}
	return s.repository.UpsertRemediationPolicy(ctx, project.ID, policy)
}

func (s *Service) ensureConfigurationEnvironment(ctx context.Context, project domain.Project, draft domain.ConfigurationDraft) (domain.Environment, error) {
	if draft.Environment != nil {
		return *draft.Environment, nil
	}
	id, err := s.newID()
	if err != nil {
		return domain.Environment{}, err
	}
	environment := domain.Environment{ID: id, Key: project.Key, Name: project.Name}
	if err := domain.ValidateEnvironment(environment); err != nil {
		return domain.Environment{}, ErrInvalidInput
	}
	return s.repository.UpsertEnvironment(ctx, project.ID, environment)
}

func (s *Service) PutConfiguration(ctx context.Context, principal authdomain.User, projectKey string, configuration domain.Configuration) (domain.Configuration, error) {
	project, err := s.resolveProject(ctx, principal, projectKey)
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
	ids, err := s.newIDs(5)
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
	saved, err := s.repository.UpsertConfiguration(ctx, project.ID, configuration)
	if err != nil {
		return domain.Configuration{}, err
	}
	if err := s.attachInboundURL(project, &saved); err != nil {
		return domain.Configuration{}, err
	}
	return saved, nil
}

// RotateWebhookToken 在公开入站 trigger 上创建或轮换 token，并返回完整公开地址。
func (s *Service) RotateWebhookToken(ctx context.Context, principal authdomain.User, projectKey string) (string, error) {
	project, err := s.resolveProject(ctx, principal, projectKey)
	if err != nil {
		return "", err
	}
	configuration, err := s.repository.GetConfiguration(ctx, project.ID)
	if err != nil {
		return "", err
	}
	// 只认已落库的公开入站 trigger kind。向导草稿切换并不等于可以签发 token。
	if configuration.Trigger.Kind != "signed_webhook" && configuration.Trigger.Kind != "custom_rule" {
		return "", ErrInvalidInput
	}
	plaintext, hash, ciphertext, nonce, err := s.issueWebhookToken(project.ID, configuration.Trigger.ID)
	if err != nil {
		return "", err
	}
	defer clearBytes(plaintext)
	if err := s.repository.UpdateWebhookToken(ctx, project.ID, hash, ciphertext, nonce); err != nil {
		return "", err
	}
	return domain.InboundWebhookURL(s.publicURL, string(plaintext))
}

// LookupWebhookToken 用路径 token 定位已启用的公开入站 trigger 与同项目 source。
func (s *Service) LookupWebhookToken(ctx context.Context, token string) (WebhookIngress, error) {
	normalized, err := domain.ParseWebhookToken(token)
	if err != nil {
		// 形状错误在查找前拒绝，避免把任意路径字符串送进哈希查询。
		return WebhookIngress{}, ErrInvalidInput
	}
	sum := sha256.Sum256([]byte(normalized))
	return s.repository.LookupWebhookToken(ctx, sum[:])
}

func (s *Service) ResolveAccess(ctx context.Context, principal authdomain.User, projectKey string) (domain.Project, error) {
	return s.resolve(ctx, principal, projectKey)
}

func (s *Service) RequireIncidentWrite(ctx context.Context, principal authdomain.User, projectKey string) (domain.Project, error) {
	return s.resolve(ctx, principal, projectKey)
}

func (s *Service) resolve(ctx context.Context, principal authdomain.User, projectKey string) (domain.Project, error) {
	if err := validatePrincipal(principal); err != nil {
		return domain.Project{}, err
	}
	normalizedKey, err := domain.NormalizeProjectKey(projectKey)
	if err != nil {
		return domain.Project{}, ErrNotFound
	}
	return s.repository.ResolveProject(ctx, normalizedKey)
}

func (s *Service) ProbeRepositoryRefs(ctx context.Context, principal authdomain.User, projectKey, remoteURL, transport, secretID string) (RepositoryRefs, error) {
	if s.git == nil {
		return RepositoryRefs{}, fmt.Errorf("git ref lister is required")
	}
	project, err := s.resolveProject(ctx, principal, projectKey)
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
	project, err := s.resolveProject(ctx, principal, projectKey)
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
	project, err := s.resolveProject(ctx, principal, projectKey)
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
	project, err := s.resolveProject(ctx, principal, projectKey)
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

func (s *Service) GenerateLogRule(ctx context.Context, principal authdomain.User, projectKey string, input LogRuleGenerationInput) (domain.CustomRule, error) {
	generator, ok := s.llm.(LogRuleGenerator)
	if !ok {
		return domain.CustomRule{}, ErrLLMUnreachable
	}
	intent, sample := strings.TrimSpace(input.Intent), strings.TrimSpace(input.Sample)
	if len(intent) < 3 || len(intent) > 2000 || len(sample) < 1 || len(sample) > 16000 {
		return domain.CustomRule{}, ErrInvalidInput
	}
	project, err := s.resolveProject(ctx, principal, projectKey)
	if err != nil {
		return domain.CustomRule{}, err
	}
	draft, err := s.repository.GetConfigurationDraft(ctx, project.ID)
	if err != nil || draft.LLM == nil {
		return domain.CustomRule{}, ErrInvalidInput
	}
	encrypted, err := s.repository.GetEncryptedSecret(ctx, project.ID, draft.LLM.CredentialSecretID)
	if err != nil || encrypted.Kind != domain.SecretHTTPBearer {
		return domain.CustomRule{}, ErrInvalidInput
	}
	plaintext, err := s.cipher.Decrypt(project.ID, encrypted.ID, encrypted.Kind, encrypted.Ciphertext, encrypted.Nonce)
	if err != nil {
		return domain.CustomRule{}, fmt.Errorf("decrypt project credential: %w", err)
	}
	defer clearBytes(plaintext)
	rule, err := generator.GenerateLogRule(ctx, draft.LLM.BaseURL, plaintext, draft.LLM.Model, intent, sample)
	if err != nil {
		return domain.CustomRule{}, ErrLLMUnreachable
	}
	encoded, err := json.Marshal(domain.CustomRuleConfig{SchemaVersion: 2, GroupingWindowSeconds: 300, Rules: []domain.CustomRule{rule}})
	if err != nil {
		return domain.CustomRule{}, ErrLLMUnreachable
	}
	validated, err := domain.ParseCustomRuleConfig(encoded)
	if err != nil || len(validated.Rules) != 1 {
		return domain.CustomRule{}, ErrLLMUnreachable
	}
	return validated.Rules[0], nil
}

func (s *Service) InstallLogProbe(ctx context.Context, principal authdomain.User, projectKey string) (LogProbeStatus, error) {
	request, err := s.logProbeRequest(ctx, principal, projectKey)
	if err != nil {
		return LogProbeStatus{}, err
	}
	status, err := s.logProbes.Install(ctx, request)
	if err != nil {
		return LogProbeStatus{}, fmt.Errorf("%w: %w", ErrLogProbeUnavailable, err)
	}
	return status, nil
}

func (s *Service) GetLogProbeStatus(ctx context.Context, principal authdomain.User, projectKey string) (LogProbeStatus, error) {
	request, err := s.logProbeTarget(ctx, principal, projectKey)
	if err != nil {
		return LogProbeStatus{}, err
	}
	status, err := s.logProbes.Status(ctx, request)
	if err != nil {
		return LogProbeStatus{}, fmt.Errorf("%w: %v", ErrLogProbeUnavailable, err)
	}
	return status, nil
}

func (s *Service) UninstallLogProbe(ctx context.Context, principal authdomain.User, projectKey string) (LogProbeStatus, error) {
	request, err := s.logProbeTarget(ctx, principal, projectKey)
	if err != nil {
		return LogProbeStatus{}, err
	}
	status, err := s.logProbes.Uninstall(ctx, request)
	if err != nil {
		return LogProbeStatus{}, fmt.Errorf("%w: %v", ErrLogProbeUnavailable, err)
	}
	return status, nil
}

func (s *Service) logProbeTarget(ctx context.Context, principal authdomain.User, projectKey string) (LogProbeRequest, error) {
	if s.logProbes == nil {
		return LogProbeRequest{}, ErrLogProbeUnavailable
	}
	project, err := s.resolveProject(ctx, principal, projectKey)
	if err != nil {
		return LogProbeRequest{}, err
	}
	draft, err := s.repository.GetConfigurationDraft(ctx, project.ID)
	if err != nil {
		return LogProbeRequest{}, err
	}
	if draft.Source == nil || draft.Source.Kind != "ssh" || draft.Source.CredentialSecretID == nil {
		return LogProbeRequest{}, ErrInvalidInput
	}
	source, err := domain.ParseSSHSourceConfig(draft.Source.Config)
	if err != nil || source.Deployment.Kind != domain.SSHDeploymentHost {
		return LogProbeRequest{}, ErrInvalidInput
	}
	version := int64(1)
	if draft.Trigger != nil {
		version = draft.Trigger.Version
	}
	return LogProbeRequest{ProjectID: project.ID, Source: source, CredentialSecretID: *draft.Source.CredentialSecretID, TriggerVersion: version}, nil
}

func (s *Service) logProbeRequest(ctx context.Context, principal authdomain.User, projectKey string) (LogProbeRequest, error) {
	if s.logProbes == nil {
		return LogProbeRequest{}, ErrLogProbeUnavailable
	}
	project, err := s.resolveProject(ctx, principal, projectKey)
	if err != nil {
		return LogProbeRequest{}, err
	}
	draft, err := s.repository.GetConfigurationDraft(ctx, project.ID)
	if err != nil {
		return LogProbeRequest{}, err
	}
	if draft.Source == nil || draft.Trigger == nil || !draft.Source.Enabled || !draft.Trigger.Enabled ||
		draft.Source.Kind != "ssh" || draft.Source.CredentialSecretID == nil || draft.Trigger.Kind != "custom_rule" {
		return LogProbeRequest{}, ErrInvalidInput
	}
	source, err := domain.ParseSSHSourceConfig(draft.Source.Config)
	if err != nil || source.Deployment.Kind != domain.SSHDeploymentHost || source.Mode != "tail" {
		return LogProbeRequest{}, ErrInvalidInput
	}
	rules, err := domain.ParseCustomRuleConfig(draft.Trigger.Config)
	if err != nil {
		return LogProbeRequest{}, ErrInvalidInput
	}
	if err := s.attachInboundURLToTrigger(project, draft.Trigger); err != nil || draft.Trigger.InboundURL == "" {
		return LogProbeRequest{}, ErrLogProbeUnavailable
	}
	return LogProbeRequest{
		ProjectID: project.ID, Source: source, CredentialSecretID: *draft.Source.CredentialSecretID,
		InboundURL: draft.Trigger.InboundURL, TriggerVersion: draft.Trigger.Version, Rules: rules,
	}, nil
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
	if configuration.Trigger.Kind != "signed_webhook" && configuration.Trigger.Kind != "custom_rule" {
		// 离开公开入站 trigger 必须清掉三列，旧 URL 立刻失效且不得残留可解密密文。
		configuration.Trigger.IngressTokenHash = nil
		configuration.Trigger.IngressTokenCiphertext = nil
		configuration.Trigger.IngressTokenNonce = nil
		return nil
	}
	if current.Kind == configuration.Trigger.Kind && len(current.IngressTokenHash) > 0 {
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
	if trigger.Kind != "signed_webhook" && trigger.Kind != "custom_rule" {
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

func (s *Service) resolveProject(ctx context.Context, principal authdomain.User, projectKey string) (domain.Project, error) {
	return s.resolve(ctx, principal, projectKey)
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
