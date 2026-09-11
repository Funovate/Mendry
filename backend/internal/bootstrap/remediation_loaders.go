package bootstrap

import (
	"context"
	"fmt"

	projectpostgres "mendry/backend/internal/modules/projects/adapter/postgres"
	projectdomain "mendry/backend/internal/modules/projects/domain"
	remediationgit "mendry/backend/internal/modules/remediation/adapter/git"
	remediationmcp "mendry/backend/internal/modules/remediation/adapter/mcp"
	remediationopenai "mendry/backend/internal/modules/remediation/adapter/openai"
	remediationsshlog "mendry/backend/internal/modules/remediation/adapter/sshlog"
	remediationdomain "mendry/backend/internal/modules/remediation/domain"
)

// projectRuntimeLoaders 把项目 repository 接到 remediation 适配器，
// 不经过用户面向的 Service 解密路径，也不向 HTTP 暴露明文。
type projectRuntimeLoaders struct {
	repository *projectpostgres.Repository
}

func newProjectRuntimeLoaders(repository *projectpostgres.Repository) (*projectRuntimeLoaders, error) {
	if repository == nil {
		return nil, fmt.Errorf("project repository is required")
	}
	return &projectRuntimeLoaders{repository: repository}, nil
}

// LoadRepository 返回无 userinfo 的仓库配置，供 Git 适配器注入凭据。
func (l *projectRuntimeLoaders) LoadRepository(ctx context.Context, projectID string) (remediationgit.RepositoryConfig, error) {
	configuration, err := l.repository.GetConfiguration(ctx, projectID)
	if err != nil {
		return remediationgit.RepositoryConfig{}, err
	}
	secretID := ""
	if configuration.Repository.CredentialSecretID != nil {
		secretID = *configuration.Repository.CredentialSecretID
	}
	return remediationgit.RepositoryConfig{
		RemoteURL:          configuration.Repository.RemoteURL,
		Transport:          configuration.Repository.Transport,
		CredentialSecretID: secretID,
		ProductionBranch:   configuration.Repository.ProductionBranch,
	}, nil
}

// GetEncryptedSecret 按项目和 secret ID 读取密文，供适配器本地解密。
func (l *projectRuntimeLoaders) GetEncryptedSecret(ctx context.Context, projectID, secretID string) (projectdomain.EncryptedSecret, error) {
	return l.repository.GetEncryptedSecret(ctx, projectID, secretID)
}

// LoadSSHSource 解析项目 SSH 日志源。空 sourceID 回落到项目唯一 SSH source。
func (l *projectRuntimeLoaders) LoadSSHSource(ctx context.Context, projectID, sourceID string) (remediationsshlog.SourceConfig, error) {
	configuration, err := l.repository.GetConfiguration(ctx, projectID)
	if err != nil {
		return remediationsshlog.SourceConfig{}, err
	}
	if configuration.Source.Kind != "ssh" {
		return remediationsshlog.SourceConfig{}, fmt.Errorf("project source is not an SSH log path")
	}
	if sourceID != "" && configuration.Source.ID != sourceID {
		return remediationsshlog.SourceConfig{}, fmt.Errorf("ssh source not found")
	}
	return remediationsshlog.ParseSSHSourceConfig(projectID, configuration.Source.ID, configuration.Source.CredentialSecretID, configuration.Source.Config)
}

// LoadMCPSource parses the persisted MCP union without exposing source config
// or secret material outside the trusted runtime adapter.
func (l *projectRuntimeLoaders) LoadMCPSource(ctx context.Context, projectID, sourceID string) (remediationmcp.SourceConfig, error) {
	configuration, err := l.repository.GetConfiguration(ctx, projectID)
	if err != nil {
		return remediationmcp.SourceConfig{}, err
	}
	if configuration.Source.Kind != "mcp" {
		return remediationmcp.SourceConfig{}, fmt.Errorf("project source is not an MCP source")
	}
	if sourceID == "" || configuration.Source.ID != sourceID {
		return remediationmcp.SourceConfig{}, fmt.Errorf("MCP source not found")
	}
	parsed, err := remediationmcp.ParseSourceConfig(projectID, configuration.Source.ID, configuration.Source.CredentialSecretID, configuration.Source.Config)
	if err != nil {
		return remediationmcp.SourceConfig{}, err
	}
	parsed.Version = configuration.Source.Version
	return parsed, nil
}

// ResolveSourceCapability returns credential-free source metadata used while
// preparing a run. It does not contact Git, SSH, HTTP, or MCP.
func (l *projectRuntimeLoaders) ResolveSourceCapability(ctx context.Context, projectID, sourceID string) (remediationdomain.SourceCapabilitySnapshot, error) {
	configuration, err := l.repository.GetConfiguration(ctx, projectID)
	if err != nil {
		return remediationdomain.SourceCapabilitySnapshot{}, err
	}
	source := configuration.Source
	if source.ID != sourceID {
		return remediationdomain.SourceCapabilitySnapshot{}, fmt.Errorf("project source not found")
	}
	snapshot := remediationdomain.SourceCapabilitySnapshot{
		ProjectID: projectID, SourceID: source.ID, Kind: source.Kind,
		Enabled: source.Enabled, Declared: append([]string(nil), source.Capabilities...), Version: source.Version,
	}
	switch source.Kind {
	case "ssh":
		cfg, err := remediationsshlog.ParseSSHSourceConfig(projectID, source.ID, source.CredentialSecretID, source.Config)
		if err != nil {
			return remediationdomain.SourceCapabilitySnapshot{}, fmt.Errorf("validate SSH source capability: %w", err)
		}
		snapshot.Supported = true
		snapshot.SSHHost = cfg.Host
		snapshot.SSHUser = cfg.User
		snapshot.SSHProjectFolder = cfg.ProjectFolder
		snapshot.SSHLogPath = cfg.LogPath
		snapshot.SSHDeploymentKind = string(cfg.Deployment.Kind)
		snapshot.SSHContainerName = cfg.Deployment.ContainerName
	case "mcp":
		if _, err := remediationmcp.ParseSourceConfig(projectID, source.ID, source.CredentialSecretID, source.Config); err != nil {
			return remediationdomain.SourceCapabilitySnapshot{}, fmt.Errorf("validate MCP source capability: %w", err)
		}
		snapshot.Supported = true
	case "cloud":
		snapshot.UnavailableReason = "cloud log connector is unavailable"
	default:
		return remediationdomain.SourceCapabilitySnapshot{}, fmt.Errorf("unsupported project source kind")
	}
	return snapshot, nil
}

// LoadLLMProvider 返回已保存的 OpenAI 兼容接入点，不含明文 API key。
func (l *projectRuntimeLoaders) LoadLLMProvider(ctx context.Context, projectID string) (remediationopenai.ProviderConfig, error) {
	configuration, err := l.repository.GetConfiguration(ctx, projectID)
	if err != nil {
		return remediationopenai.ProviderConfig{}, err
	}
	if configuration.LLM == nil {
		return remediationopenai.ProviderConfig{}, fmt.Errorf("project LLM provider is not configured")
	}
	return remediationopenai.ProviderConfig{
		BaseURL:            configuration.LLM.BaseURL,
		Model:              configuration.LLM.Model,
		CredentialSecretID: configuration.LLM.CredentialSecretID,
	}, nil
}

var (
	_ remediationgit.ConfigLoader                = (*projectRuntimeLoaders)(nil)
	_ remediationgit.SecretLoader                = (*projectRuntimeLoaders)(nil)
	_ remediationsshlog.SourceLoader             = (*projectRuntimeLoaders)(nil)
	_ remediationsshlog.SecretLoader             = (*projectRuntimeLoaders)(nil)
	_ remediationmcp.SourceLoader                = (*projectRuntimeLoaders)(nil)
	_ remediationmcp.SecretLoader                = (*projectRuntimeLoaders)(nil)
	_ remediationdomain.SourceCapabilityResolver = (*projectRuntimeLoaders)(nil)
	_ remediationopenai.ConfigLoader             = (*projectRuntimeLoaders)(nil)
	_ remediationopenai.SecretLoader             = (*projectRuntimeLoaders)(nil)
)
