package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"mendry/backend/internal/modules/projects/adapter/postgres/projectdb"
	"mendry/backend/internal/modules/projects/application"
	"mendry/backend/internal/modules/projects/domain"
	"mendry/backend/internal/platform/errtrace"
	platformpostgres "mendry/backend/internal/platform/postgres"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type Repository struct {
	queries *projectdb.Queries
}

func NewRepository(database projectdb.DBTX) (*Repository, error) {
	if database == nil {
		return nil, fmt.Errorf("project PostgreSQL database is required")
	}
	return &Repository{queries: projectdb.New(database)}, nil
}

func (r *Repository) CreateProject(ctx context.Context, project domain.Project) (domain.Project, error) {
	projectID, err := uuidParameter(project.ID, "project")
	if err != nil {
		return domain.Project{}, err
	}
	row, err := r.queries.CreateProject(platformpostgres.WithOperation(ctx, "project.create"), projectdb.CreateProjectParams{
		ProjectID: projectID, ProjectKey: project.Key, Name: project.Name, Description: project.Description,
	})
	if projectConflict(err) {
		return domain.Project{}, application.ErrConflict
	}
	if err != nil {
		return domain.Project{}, newRepositoryError("insert project", err)
	}
	return mapProject(row.ID, row.ProjectKey, row.Name, row.Description, row.Version, row.CreatedAt, row.UpdatedAt)
}

func (r *Repository) ListProjects(ctx context.Context, limit int32) (application.ListResult[domain.Project], error) {
	rows, err := r.queries.ListProjects(platformpostgres.WithOperation(ctx, "project.list"), limit)
	if err != nil {
		return application.ListResult[domain.Project]{}, newRepositoryError("list projects", err)
	}
	projects := make([]domain.Project, 0, len(rows))
	var total int64
	for _, row := range rows {
		project, err := mapProject(row.ID, row.ProjectKey, row.Name, row.Description, row.Version, row.CreatedAt, row.UpdatedAt)
		if err != nil {
			return application.ListResult[domain.Project]{}, fmt.Errorf("map listed project: %w", err)
		}
		projects = append(projects, project)
		total = row.TotalCount
	}
	return application.ListResult[domain.Project]{Items: projects, Total: total}, nil
}

func (r *Repository) ResolveProject(ctx context.Context, projectKey string) (domain.Project, error) {
	row, err := r.queries.GetProject(platformpostgres.WithOperation(ctx, "project.resolve"), projectKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Project{}, application.ErrNotFound
	}
	if err != nil {
		return domain.Project{}, newRepositoryError("resolve project", err)
	}
	return mapProject(row.ID, row.ProjectKey, row.Name, row.Description, row.Version, row.CreatedAt, row.UpdatedAt)
}

// UpdateProjectName 原子更新项目名；仅当环境名仍等于旧项目名时同步 environment。
func (r *Repository) UpdateProjectName(ctx context.Context, project domain.Project) (domain.Project, error) {
	projectID, err := uuidParameter(project.ID, "project")
	if err != nil {
		return domain.Project{}, err
	}
	row, err := r.queries.UpdateProjectName(platformpostgres.WithOperation(ctx, "project.rename"), projectdb.UpdateProjectNameParams{
		ProjectID: projectID, Name: project.Name,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Project{}, application.ErrNotFound
	}
	if err != nil {
		return domain.Project{}, newRepositoryError("update project name", err)
	}
	return mapProject(row.ID, row.ProjectKey, row.Name, row.Description, row.Version, row.CreatedAt, row.UpdatedAt)
}

func (r *Repository) CreateSecret(ctx context.Context, encrypted domain.EncryptedSecret) (domain.Secret, error) {
	secretID, err := uuidParameter(encrypted.ID, "project secret")
	if err != nil {
		return domain.Secret{}, err
	}
	projectID, err := uuidParameter(encrypted.ProjectID, "project")
	if err != nil {
		return domain.Secret{}, err
	}
	row, err := r.queries.CreateProjectSecret(platformpostgres.WithOperation(ctx, "project.secret.create"), projectdb.CreateProjectSecretParams{
		SecretID: secretID, ProjectID: projectID, Name: encrypted.Name, Kind: string(encrypted.Kind),
		Ciphertext: encrypted.Ciphertext, Nonce: encrypted.Nonce, KeyVersion: encrypted.KeyVersion,
	})
	if secretConflict(err) {
		return domain.Secret{}, application.ErrConflict
	}
	if err != nil {
		return domain.Secret{}, newRepositoryError("insert project credential", err)
	}
	return mapSecret(row.ID, row.ProjectID, row.Name, row.Kind, row.KeyVersion, row.Version, row.CreatedAt, row.UpdatedAt)
}

// UpdateSecret 原地更新凭据 metadata 和密文。
func (r *Repository) UpdateSecret(ctx context.Context, encrypted domain.EncryptedSecret) (domain.Secret, error) {
	secretID, err := uuidParameter(encrypted.ID, "project secret")
	if err != nil {
		return domain.Secret{}, err
	}
	projectID, err := uuidParameter(encrypted.ProjectID, "project")
	if err != nil {
		return domain.Secret{}, err
	}
	row, err := r.queries.UpdateProjectSecret(platformpostgres.WithOperation(ctx, "project.secret.update"), projectdb.UpdateProjectSecretParams{
		Name: encrypted.Name, Ciphertext: encrypted.Ciphertext, Nonce: encrypted.Nonce, KeyVersion: encrypted.KeyVersion,
		ProjectID: projectID, SecretID: secretID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Secret{}, application.ErrInvalidInput
	}
	if secretConflict(err) {
		return domain.Secret{}, application.ErrConflict
	}
	if err != nil {
		return domain.Secret{}, newRepositoryError("update project credential", err)
	}
	return mapSecret(row.ID, row.ProjectID, row.Name, row.Kind, row.KeyVersion, row.Version, row.CreatedAt, row.UpdatedAt)
}

func (r *Repository) ListSecrets(ctx context.Context, projectID string) (application.ListResult[domain.Secret], error) {
	projectUUID, err := uuidParameter(projectID, "project")
	if err != nil {
		return application.ListResult[domain.Secret]{}, err
	}
	rows, err := r.queries.ListProjectSecrets(platformpostgres.WithOperation(ctx, "project.secret.list"), projectUUID)
	if err != nil {
		return application.ListResult[domain.Secret]{}, newRepositoryError("list project credentials", err)
	}
	secrets := make([]domain.Secret, 0, len(rows))
	var total int64
	for _, row := range rows {
		secret, err := mapSecret(row.ID, row.ProjectID, row.Name, row.Kind, row.KeyVersion, row.Version, row.CreatedAt, row.UpdatedAt)
		if err != nil {
			return application.ListResult[domain.Secret]{}, fmt.Errorf("map listed project credential: %w", err)
		}
		secrets = append(secrets, secret)
		total = row.TotalCount
	}
	return application.ListResult[domain.Secret]{Items: secrets, Total: total}, nil
}

func (r *Repository) GetEncryptedSecret(ctx context.Context, projectID, secretID string) (domain.EncryptedSecret, error) {
	projectUUID, err := uuidParameter(projectID, "project")
	if err != nil {
		return domain.EncryptedSecret{}, err
	}
	secretUUID, err := uuidParameter(secretID, "project secret")
	if err != nil {
		return domain.EncryptedSecret{}, err
	}
	row, err := r.queries.GetProjectSecret(platformpostgres.WithOperation(ctx, "project.secret.get"), projectdb.GetProjectSecretParams{
		ProjectID: projectUUID, SecretID: secretUUID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.EncryptedSecret{}, application.ErrInvalidInput
	}
	if err != nil {
		return domain.EncryptedSecret{}, newRepositoryError("get project credential", err)
	}
	secret, err := mapSecret(row.ID, row.ProjectID, row.Name, row.Kind, row.KeyVersion, row.Version, row.CreatedAt, row.UpdatedAt)
	if err != nil {
		return domain.EncryptedSecret{}, fmt.Errorf("map project credential: %w", err)
	}
	if len(row.Ciphertext) == 0 || len(row.Nonce) == 0 {
		return domain.EncryptedSecret{}, fmt.Errorf("project credential ciphertext is incomplete")
	}
	return domain.EncryptedSecret{Secret: secret, Ciphertext: row.Ciphertext, Nonce: row.Nonce}, nil
}

func (r *Repository) GetConfiguration(ctx context.Context, projectID string) (domain.Configuration, error) {
	projectUUID, err := uuidParameter(projectID, "project")
	if err != nil {
		return domain.Configuration{}, err
	}
	row, err := r.queries.GetProjectConfiguration(platformpostgres.WithOperation(ctx, "project.configuration.get"), projectUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Configuration{}, application.ErrConfigurationNotFound
	}
	if err != nil {
		return domain.Configuration{}, newRepositoryError("query project configuration", err)
	}
	configuration, err := mapConfiguration(configurationRow{
		environmentID: row.EnvironmentID, environmentKey: row.EnvironmentKey, environmentName: row.EnvironmentName,
		service: row.Service, environmentVersion: row.EnvironmentVersion, repositoryID: row.RepositoryID,
		remoteURL: row.RemoteUrl, scmProvider: row.ScmProvider, transport: row.Transport,
		repositorySecretID: row.RepositoryCredentialSecretID, productionBranch: row.ProductionBranch,
		deployedCommit: row.DeployedCommit, repositoryVersion: row.RepositoryVersion, sourceID: row.SourceID,
		sourceKind: row.SourceKind, sourceSecretID: row.SourceCredentialSecretID,
		sourceConfig: row.SourceConfig, sourceCapabilities: row.SourceCapabilities, sourceEnabled: row.SourceEnabled,
		sourceVersion: row.SourceVersion, triggerID: row.TriggerID,
		triggerKind: row.TriggerKind, signingSecretID: row.SigningSecretID, triggerConfig: row.TriggerConfig,
		triggerEnabled: row.TriggerEnabled, triggerVersion: row.TriggerVersion,
		ingressTokenHash: row.IngressTokenHash, ingressTokenCiphertext: row.IngressTokenCiphertext,
		ingressTokenNonce: row.IngressTokenNonce,
		llmID:             row.LlmID, llmProvider: row.LlmProvider, llmBaseURL: row.LlmBaseUrl,
		llmSecretID: row.LlmCredentialSecretID, llmModel: row.LlmModel, llmVersion: row.LlmVersion,
	})
	if err != nil {
		return domain.Configuration{}, err
	}
	policy, err := r.getRemediationPolicy(ctx, projectUUID)
	if err != nil {
		return domain.Configuration{}, err
	}
	configuration.Remediation = policy
	return configuration, nil
}

func (r *Repository) GetConfigurationDraft(ctx context.Context, projectID string) (domain.ConfigurationDraft, error) {
	projectUUID, err := uuidParameter(projectID, "project")
	if err != nil {
		return domain.ConfigurationDraft{}, err
	}
	draft := domain.ConfigurationDraft{}
	policy, err := r.getRemediationPolicy(ctx, projectUUID)
	if err != nil {
		return domain.ConfigurationDraft{}, err
	}
	draft.Remediation = &policy

	environmentRow, err := r.queries.GetProjectEnvironment(platformpostgres.WithOperation(ctx, "project.configuration.environment.get"), projectUUID)
	if err == nil {
		environment, mapErr := mapEnvironmentRow(environmentRow)
		if mapErr != nil {
			return domain.ConfigurationDraft{}, mapErr
		}
		draft.Environment = &environment
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return domain.ConfigurationDraft{}, newRepositoryError("query project environment", err)
	}

	repositoryRow, err := r.queries.GetProjectRepository(platformpostgres.WithOperation(ctx, "project.configuration.repository.get"), projectUUID)
	if err == nil {
		repository, mapErr := mapRepositoryRow(repositoryRow)
		if mapErr != nil {
			return domain.ConfigurationDraft{}, mapErr
		}
		draft.Repository = &repository
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return domain.ConfigurationDraft{}, newRepositoryError("query project repository", err)
	}

	sourceRow, err := r.queries.GetProjectSource(platformpostgres.WithOperation(ctx, "project.configuration.source.get"), projectUUID)
	if err == nil {
		source, mapErr := mapSourceRow(sourceRow)
		if mapErr != nil {
			return domain.ConfigurationDraft{}, mapErr
		}
		draft.Source = &source
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return domain.ConfigurationDraft{}, newRepositoryError("query project source", err)
	}

	triggerRow, err := r.queries.GetProjectTrigger(platformpostgres.WithOperation(ctx, "project.configuration.trigger.get"), projectUUID)
	if err == nil {
		trigger, mapErr := mapTriggerRow(triggerRow)
		if mapErr != nil {
			return domain.ConfigurationDraft{}, mapErr
		}
		draft.Trigger = &trigger
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return domain.ConfigurationDraft{}, newRepositoryError("query project trigger", err)
	}

	llmRow, err := r.queries.GetProjectLLMProvider(platformpostgres.WithOperation(ctx, "project.configuration.llm.get"), projectUUID)
	if err == nil {
		provider, mapErr := mapLLMRow(llmRow)
		if mapErr != nil {
			return domain.ConfigurationDraft{}, mapErr
		}
		draft.LLM = &provider
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return domain.ConfigurationDraft{}, newRepositoryError("query project LLM provider", err)
	}

	return draft, nil
}

func (r *Repository) getRemediationPolicy(ctx context.Context, projectID pgtype.UUID) (domain.RemediationPolicy, error) {
	row, err := r.queries.GetProjectRemediationPolicy(platformpostgres.WithOperation(ctx, "project.configuration.remediation_policy.get"), projectID)
	if err != nil {
		return domain.RemediationPolicy{}, newRepositoryError("query project remediation policy", err)
	}
	policy := domain.RemediationPolicy{AgentLoopMode: domain.AgentLoopMode(row.AgentLoopMode), Version: row.AgentLoopPolicyVersion}
	if err := domain.ValidateRemediationPolicy(policy); err != nil {
		return domain.RemediationPolicy{}, fmt.Errorf("validate project remediation policy row: %w", err)
	}
	return policy, nil
}

func (r *Repository) UpsertRemediationPolicy(ctx context.Context, projectID string, policy domain.RemediationPolicy) (domain.RemediationPolicy, error) {
	params, err := r.projectParameters(projectID)
	if err != nil {
		return domain.RemediationPolicy{}, err
	}
	row, err := r.queries.UpsertProjectRemediationPolicy(platformpostgres.WithOperation(ctx, "project.configuration.remediation_policy.upsert"), projectdb.UpsertProjectRemediationPolicyParams{
		ProjectID: params.projectID, AgentLoopMode: string(policy.AgentLoopMode),
	})
	if err != nil {
		return domain.RemediationPolicy{}, newRepositoryError("upsert project remediation policy", err)
	}
	return domain.RemediationPolicy{AgentLoopMode: domain.AgentLoopMode(row.AgentLoopMode), Version: row.AgentLoopPolicyVersion}, nil
}

func (r *Repository) UpsertEnvironment(ctx context.Context, projectID string, environment domain.Environment) (domain.Environment, error) {
	params, err := r.projectParameters(projectID)
	if err != nil {
		return domain.Environment{}, err
	}
	environmentID, err := uuidParameter(environment.ID, "project environment")
	if err != nil {
		return domain.Environment{}, err
	}
	row, err := r.queries.UpsertProjectEnvironment(platformpostgres.WithOperation(ctx, "project.configuration.environment.upsert"), projectdb.UpsertProjectEnvironmentParams{
		EnvironmentID: environmentID, ProjectID: params.projectID, EnvironmentKey: environment.Key, EnvironmentName: environment.Name,
		Service: environment.Service,
	})
	if configurationConflict(err) {
		return domain.Environment{}, application.ErrConflict
	}
	if err != nil {
		return domain.Environment{}, newRepositoryError("upsert project environment", err)
	}
	return mapEnvironmentRow(projectdb.GetProjectEnvironmentRow{ID: row.ID, EnvironmentKey: row.EnvironmentKey, Name: row.Name, Service: row.Service, Version: row.Version})
}

func (r *Repository) UpsertRepository(ctx context.Context, projectID string, repository domain.Repository) (domain.Repository, error) {
	params, err := r.projectParameters(projectID)
	if err != nil {
		return domain.Repository{}, err
	}
	repositoryID, err := uuidParameter(repository.ID, "project repository")
	if err != nil {
		return domain.Repository{}, err
	}
	row, err := r.queries.UpsertProjectRepository(platformpostgres.WithOperation(ctx, "project.configuration.repository.upsert"), projectdb.UpsertProjectRepositoryParams{
		RepositoryID: repositoryID, ProjectID: params.projectID, RemoteUrl: repository.RemoteURL, ScmProvider: repository.SCMProvider,
		RepositoryTransport: repository.Transport, CredentialSecretID: optionalUUID(repository.CredentialSecretID),
		ProductionBranch: repository.ProductionBranch, DeployedCommit: repository.DeployedCommit,
	})
	if configurationReferenceConflict(err) {
		return domain.Repository{}, application.ErrInvalidInput
	}
	if configurationConflict(err) {
		return domain.Repository{}, application.ErrConflict
	}
	if err != nil {
		return domain.Repository{}, newRepositoryError("upsert project repository", err)
	}
	return mapRepositoryRow(projectdb.GetProjectRepositoryRow{ID: row.ID, RemoteUrl: row.RemoteUrl, ScmProvider: row.ScmProvider, Transport: row.Transport, CredentialSecretID: row.CredentialSecretID, ProductionBranch: row.ProductionBranch, DeployedCommit: row.DeployedCommit, Version: row.Version})
}

func (r *Repository) UpsertSource(ctx context.Context, projectID, environmentID string, source domain.Source) (domain.Source, error) {
	params, err := r.projectParameters(projectID)
	if err != nil {
		return domain.Source{}, err
	}
	sourceID, err := uuidParameter(source.ID, "project source")
	if err != nil {
		return domain.Source{}, err
	}
	environmentUUID, err := uuidParameter(environmentID, "project environment")
	if err != nil {
		return domain.Source{}, err
	}
	row, err := r.queries.UpsertProjectSource(platformpostgres.WithOperation(ctx, "project.configuration.source.upsert"), projectdb.UpsertProjectSourceParams{
		SourceID: sourceID, ProjectID: params.projectID, EnvironmentID: environmentUUID, SourceKind: source.Kind,
		CredentialSecretID: optionalUUID(source.CredentialSecretID), SourceConfig: source.Config, SourceCapabilities: source.Capabilities,
		SourceEnabled: source.Enabled,
	})
	if configurationReferenceConflict(err) {
		return domain.Source{}, application.ErrInvalidInput
	}
	if configurationConflict(err) {
		return domain.Source{}, application.ErrConflict
	}
	if err != nil {
		return domain.Source{}, newRepositoryError("upsert project source", err)
	}
	return mapSourceRow(projectdb.GetProjectSourceRow{ID: row.ID, Kind: row.Kind, CredentialSecretID: row.CredentialSecretID, Config: row.Config, Capabilities: row.Capabilities, Enabled: row.Enabled, Version: row.Version})
}

func (r *Repository) UpsertTrigger(ctx context.Context, projectID, environmentID string, trigger domain.Trigger) (domain.Trigger, error) {
	params, err := r.projectParameters(projectID)
	if err != nil {
		return domain.Trigger{}, err
	}
	triggerID, err := uuidParameter(trigger.ID, "project trigger")
	if err != nil {
		return domain.Trigger{}, err
	}
	environmentUUID, err := uuidParameter(environmentID, "project environment")
	if err != nil {
		return domain.Trigger{}, err
	}
	if err := domain.ValidateWebhookTokenColumns(trigger.IngressTokenHash, trigger.IngressTokenCiphertext, trigger.IngressTokenNonce); err != nil {
		return domain.Trigger{}, application.ErrInvalidInput
	}
	row, err := r.queries.UpsertProjectTrigger(platformpostgres.WithOperation(ctx, "project.configuration.trigger.upsert"), projectdb.UpsertProjectTriggerParams{
		TriggerID: triggerID, ProjectID: params.projectID, EnvironmentID: environmentUUID, TriggerKind: trigger.Kind,
		SigningSecretID: optionalUUID(trigger.SigningSecretID), TriggerConfig: trigger.Config, TriggerEnabled: trigger.Enabled,
		IngressTokenHash: trigger.IngressTokenHash, IngressTokenCiphertext: trigger.IngressTokenCiphertext, IngressTokenNonce: trigger.IngressTokenNonce,
	})
	if configurationReferenceConflict(err) {
		return domain.Trigger{}, application.ErrInvalidInput
	}
	if configurationConflict(err) {
		return domain.Trigger{}, application.ErrConflict
	}
	if err != nil {
		return domain.Trigger{}, newRepositoryError("upsert project trigger", err)
	}
	return mapTriggerRow(projectdb.GetProjectTriggerRow{ID: row.ID, Kind: row.Kind, SigningSecretID: row.SigningSecretID, Config: row.Config, Enabled: row.Enabled, Version: row.Version, IngressTokenHash: row.IngressTokenHash, IngressTokenCiphertext: row.IngressTokenCiphertext, IngressTokenNonce: row.IngressTokenNonce})
}

func (r *Repository) UpsertLLMProvider(ctx context.Context, projectID string, provider domain.LLMProvider) (domain.LLMProvider, error) {
	params, err := r.projectParameters(projectID)
	if err != nil {
		return domain.LLMProvider{}, err
	}
	llmID, err := uuidParameter(provider.ID, "project LLM provider")
	if err != nil {
		return domain.LLMProvider{}, err
	}
	credentialID, err := uuidParameter(provider.CredentialSecretID, "LLM credential")
	if err != nil {
		return domain.LLMProvider{}, err
	}
	row, err := r.queries.UpsertProjectLLMProvider(platformpostgres.WithOperation(ctx, "project.configuration.llm.upsert"), projectdb.UpsertProjectLLMProviderParams{
		LlmID: llmID, ProjectID: params.projectID, LlmProvider: provider.Provider, LlmBaseUrl: provider.BaseURL,
		LlmCredentialSecretID: credentialID, LlmModel: provider.Model,
	})
	if configurationReferenceConflict(err) {
		return domain.LLMProvider{}, application.ErrInvalidInput
	}
	if configurationConflict(err) {
		return domain.LLMProvider{}, application.ErrConflict
	}
	if err != nil {
		return domain.LLMProvider{}, newRepositoryError("upsert project LLM provider", err)
	}
	return mapLLMRow(projectdb.GetProjectLLMProviderRow{ID: row.ID, Provider: row.Provider, BaseUrl: row.BaseUrl, CredentialSecretID: row.CredentialSecretID, Model: row.Model, Version: row.Version})
}

func (r *Repository) UpsertConfiguration(ctx context.Context, projectID string, configuration domain.Configuration) (domain.Configuration, error) {
	projectUUID, err := uuidParameter(projectID, "project")
	if err != nil {
		return domain.Configuration{}, err
	}
	environmentID, err := uuidParameter(configuration.Environment.ID, "project environment")
	if err != nil {
		return domain.Configuration{}, err
	}
	repositoryID, err := uuidParameter(configuration.Repository.ID, "project repository")
	if err != nil {
		return domain.Configuration{}, err
	}
	sourceID, err := uuidParameter(configuration.Source.ID, "project source")
	if err != nil {
		return domain.Configuration{}, err
	}
	triggerID, err := uuidParameter(configuration.Trigger.ID, "project trigger")
	if err != nil {
		return domain.Configuration{}, err
	}
	if configuration.LLM == nil {
		return domain.Configuration{}, application.ErrInvalidInput
	}
	llmID, err := uuidParameter(configuration.LLM.ID, "project LLM provider")
	if err != nil {
		return domain.Configuration{}, err
	}
	llmSecretID, err := uuidParameter(configuration.LLM.CredentialSecretID, "LLM credential")
	if err != nil {
		return domain.Configuration{}, err
	}
	row, err := r.queries.UpsertProjectConfiguration(platformpostgres.WithOperation(ctx, "project.configuration.upsert"), projectdb.UpsertProjectConfigurationParams{
		EnvironmentID: environmentID, ProjectID: projectUUID, EnvironmentKey: configuration.Environment.Key,
		EnvironmentName: configuration.Environment.Name, Service: configuration.Environment.Service,
		RepositoryID: repositoryID, RemoteUrl: configuration.Repository.RemoteURL,
		ScmProvider: configuration.Repository.SCMProvider, RepositoryTransport: configuration.Repository.Transport,
		RepositoryCredentialSecretID: optionalUUID(configuration.Repository.CredentialSecretID),
		ProductionBranch:             configuration.Repository.ProductionBranch, DeployedCommit: configuration.Repository.DeployedCommit,
		SourceID: sourceID, SourceKind: configuration.Source.Kind,
		SourceCredentialSecretID: optionalUUID(configuration.Source.CredentialSecretID), SourceConfig: configuration.Source.Config,
		SourceCapabilities: configuration.Source.Capabilities, SourceEnabled: configuration.Source.Enabled,
		TriggerID: triggerID, TriggerKind: configuration.Trigger.Kind,
		SigningSecretID: optionalUUID(configuration.Trigger.SigningSecretID), TriggerConfig: configuration.Trigger.Config,
		TriggerEnabled:   configuration.Trigger.Enabled,
		IngressTokenHash: configuration.Trigger.IngressTokenHash, IngressTokenCiphertext: configuration.Trigger.IngressTokenCiphertext,
		IngressTokenNonce: configuration.Trigger.IngressTokenNonce,
		LlmID:             llmID, LlmProvider: configuration.LLM.Provider, LlmBaseUrl: configuration.LLM.BaseURL,
		LlmCredentialSecretID: llmSecretID, LlmModel: configuration.LLM.Model,
	})
	if configurationReferenceConflict(err) {
		return domain.Configuration{}, application.ErrInvalidInput
	}
	if configurationConflict(err) {
		return domain.Configuration{}, application.ErrConflict
	}
	if err != nil {
		return domain.Configuration{}, newRepositoryError("upsert project configuration", err)
	}
	saved, err := mapConfiguration(configurationRow{
		environmentID: row.EnvironmentID, environmentKey: row.EnvironmentKey, environmentName: row.EnvironmentName,
		service: row.Service, environmentVersion: row.EnvironmentVersion, repositoryID: row.RepositoryID,
		remoteURL: row.RemoteUrl, scmProvider: row.ScmProvider, transport: row.Transport,
		repositorySecretID: row.RepositoryCredentialSecretID, productionBranch: row.ProductionBranch,
		deployedCommit: row.DeployedCommit, repositoryVersion: row.RepositoryVersion, sourceID: row.SourceID,
		sourceKind: row.SourceKind, sourceSecretID: row.SourceCredentialSecretID,
		sourceConfig: row.SourceConfig, sourceCapabilities: row.SourceCapabilities, sourceEnabled: row.SourceEnabled,
		sourceVersion: row.SourceVersion, triggerID: row.TriggerID,
		triggerKind: row.TriggerKind, signingSecretID: row.SigningSecretID, triggerConfig: row.TriggerConfig,
		triggerEnabled: row.TriggerEnabled, triggerVersion: row.TriggerVersion,
		ingressTokenHash: row.IngressTokenHash, ingressTokenCiphertext: row.IngressTokenCiphertext,
		ingressTokenNonce: row.IngressTokenNonce,
		llmID:             row.LlmID, llmProvider: stringPointer(row.LlmProvider), llmBaseURL: stringPointer(row.LlmBaseUrl),
		llmSecretID: row.LlmCredentialSecretID, llmModel: stringPointer(row.LlmModel), llmVersion: int64Pointer(row.LlmVersion),
	})
	if err != nil {
		return domain.Configuration{}, err
	}
	policy, err := r.getRemediationPolicy(ctx, projectUUID)
	if err != nil {
		return domain.Configuration{}, err
	}
	saved.Remediation = policy
	return saved, nil
}

func (r *Repository) LookupWebhookToken(ctx context.Context, hash []byte) (application.WebhookIngress, error) {
	if len(hash) != 32 {
		return application.WebhookIngress{}, application.ErrNotFound
	}
	row, err := r.queries.LookupWebhookToken(platformpostgres.WithOperation(ctx, "project.webhook.lookup"), hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.WebhookIngress{}, application.ErrNotFound
	}
	if err != nil {
		return application.WebhookIngress{}, newRepositoryError("lookup webhook token", err)
	}
	return webhookIngressFromRow(row)
}

func webhookIngressFromRow(row projectdb.LookupWebhookTokenRow) (application.WebhookIngress, error) {
	if !row.ProjectID.Valid || !row.SourceID.Valid || !row.TriggerID.Valid {
		return application.WebhookIngress{}, application.ErrNotFound
	}
	config, err := domain.ParseSignedWebhookConfig(row.TriggerConfig)
	if err != nil {
		// Treat incomplete or corrupt stored configuration like an unknown token at
		// the public ingress boundary; do not infer a generic provider.
		return application.WebhookIngress{}, application.ErrNotFound
	}
	ingress := application.WebhookIngress{
		ProjectID: uuidString(row.ProjectID), SourceID: uuidString(row.SourceID), TriggerID: uuidString(row.TriggerID),
		Provider: config.Provider,
	}
	if config.AWSCloudWatch != nil {
		ingress.TopicARN = config.AWSCloudWatch.TopicARN
	}
	return ingress, nil
}

func (r *Repository) UpdateWebhookToken(ctx context.Context, projectID string, hash, ciphertext, nonce []byte) error {
	if err := domain.ValidateWebhookTokenColumns(hash, ciphertext, nonce); err != nil {
		return application.ErrInvalidInput
	}
	projectUUID, err := uuidParameter(projectID, "project")
	if err != nil {
		return err
	}
	if _, err := r.queries.UpdateWebhookToken(platformpostgres.WithOperation(ctx, "project.webhook.update_token"), projectdb.UpdateWebhookTokenParams{
		IngressTokenHash: hash, IngressTokenCiphertext: ciphertext, IngressTokenNonce: nonce,
		ProjectID: projectUUID,
	}); errors.Is(err, pgx.ErrNoRows) {
		return application.ErrConfigurationNotFound
	} else if err != nil {
		return newRepositoryError("update webhook token", err)
	}
	return nil
}

type projectParams struct{ projectID pgtype.UUID }

func (r *Repository) projectParameters(projectID string) (projectParams, error) {
	projectUUID, err := uuidParameter(projectID, "project")
	if err != nil {
		return projectParams{}, err
	}
	return projectParams{projectID: projectUUID}, nil
}

type configurationRow struct {
	environmentID                                               pgtype.UUID
	environmentKey, environmentName                             string
	service                                                     *string
	environmentVersion                                          int64
	repositoryID                                                pgtype.UUID
	remoteURL, scmProvider, transport                           string
	repositorySecretID                                          pgtype.UUID
	productionBranch, deployedCommit                            string
	repositoryVersion                                           int64
	sourceID                                                    pgtype.UUID
	sourceKind                                                  string
	sourceSecretID                                              pgtype.UUID
	sourceConfig                                                []byte
	sourceCapabilities                                          []string
	sourceEnabled                                               bool
	sourceVersion                                               int64
	triggerID                                                   pgtype.UUID
	triggerKind                                                 string
	signingSecretID                                             pgtype.UUID
	triggerConfig                                               []byte
	triggerEnabled                                              bool
	triggerVersion                                              int64
	ingressTokenHash, ingressTokenCiphertext, ingressTokenNonce []byte
	llmID                                                       pgtype.UUID
	llmProvider, llmBaseURL, llmModel                           *string
	llmSecretID                                                 pgtype.UUID
	llmVersion                                                  *int64
}

func mapConfiguration(row configurationRow) (domain.Configuration, error) {
	if !row.environmentID.Valid || !row.repositoryID.Valid || !row.sourceID.Valid || !row.triggerID.Valid ||
		!json.Valid(row.sourceConfig) || !json.Valid(row.triggerConfig) {
		return domain.Configuration{}, fmt.Errorf("project configuration row has invalid generated values")
	}
	configuration := domain.Configuration{
		Environment: domain.Environment{ID: uuidString(row.environmentID), Key: row.environmentKey, Name: row.environmentName,
			Service: row.service, Version: row.environmentVersion},
		Repository: domain.Repository{ID: uuidString(row.repositoryID), RemoteURL: row.remoteURL, SCMProvider: row.scmProvider,
			Transport: row.transport, CredentialSecretID: optionalUUIDString(row.repositorySecretID), ProductionBranch: row.productionBranch,
			DeployedCommit: row.deployedCommit, Version: row.repositoryVersion},
		Source: domain.Source{ID: uuidString(row.sourceID), Kind: row.sourceKind,
			CredentialSecretID: optionalUUIDString(row.sourceSecretID), Config: row.sourceConfig,
			Capabilities: row.sourceCapabilities, Enabled: row.sourceEnabled, Version: row.sourceVersion},
		Trigger: domain.Trigger{ID: uuidString(row.triggerID), Kind: row.triggerKind,
			SigningSecretID: optionalUUIDString(row.signingSecretID), Config: row.triggerConfig,
			Enabled: row.triggerEnabled, Version: row.triggerVersion,
			IngressTokenHash: row.ingressTokenHash, IngressTokenCiphertext: row.ingressTokenCiphertext,
			IngressTokenNonce: row.ingressTokenNonce},
		LLM: mapLLMProvider(row),
	}
	if err := domain.ValidateConfiguration(configuration); err != nil {
		return domain.Configuration{}, fmt.Errorf("validate project configuration row: %w", err)
	}
	return configuration, nil
}

func mapEnvironmentRow(row projectdb.GetProjectEnvironmentRow) (domain.Environment, error) {
	if !row.ID.Valid || row.Version <= 0 {
		return domain.Environment{}, fmt.Errorf("project environment row has invalid generated values")
	}
	environment := domain.Environment{ID: uuidString(row.ID), Key: row.EnvironmentKey, Name: row.Name, Service: row.Service, Version: row.Version}
	if err := domain.ValidateEnvironment(environment); err != nil {
		return domain.Environment{}, fmt.Errorf("validate project environment row: %w", err)
	}
	return environment, nil
}

func mapRepositoryRow(row projectdb.GetProjectRepositoryRow) (domain.Repository, error) {
	if !row.ID.Valid || row.Version <= 0 {
		return domain.Repository{}, fmt.Errorf("project repository row has invalid generated values")
	}
	repository := domain.Repository{ID: uuidString(row.ID), RemoteURL: row.RemoteUrl, SCMProvider: row.ScmProvider, Transport: row.Transport,
		CredentialSecretID: optionalUUIDString(row.CredentialSecretID), ProductionBranch: row.ProductionBranch, DeployedCommit: row.DeployedCommit, Version: row.Version}
	if err := domain.ValidateRepository(repository); err != nil {
		return domain.Repository{}, fmt.Errorf("validate project repository row: %w", err)
	}
	return repository, nil
}

func mapSourceRow(row projectdb.GetProjectSourceRow) (domain.Source, error) {
	if !row.ID.Valid || row.Version <= 0 || !json.Valid(row.Config) {
		return domain.Source{}, fmt.Errorf("project source row has invalid generated values")
	}
	source := domain.Source{ID: uuidString(row.ID), Kind: row.Kind, CredentialSecretID: optionalUUIDString(row.CredentialSecretID),
		Config: row.Config, Capabilities: row.Capabilities, Enabled: row.Enabled, Version: row.Version}
	if err := domain.ValidateSource(source); err != nil {
		return domain.Source{}, fmt.Errorf("validate project source row: %w", err)
	}
	return source, nil
}

func mapTriggerRow(row projectdb.GetProjectTriggerRow) (domain.Trigger, error) {
	if !row.ID.Valid || row.Version <= 0 || !json.Valid(row.Config) {
		return domain.Trigger{}, fmt.Errorf("project trigger row has invalid generated values")
	}
	if err := domain.ValidateWebhookTokenColumns(row.IngressTokenHash, row.IngressTokenCiphertext, row.IngressTokenNonce); err != nil {
		return domain.Trigger{}, fmt.Errorf("validate project trigger token row: %w", err)
	}
	trigger := domain.Trigger{ID: uuidString(row.ID), Kind: row.Kind, SigningSecretID: optionalUUIDString(row.SigningSecretID), Config: row.Config,
		Enabled: row.Enabled, Version: row.Version, IngressTokenHash: row.IngressTokenHash, IngressTokenCiphertext: row.IngressTokenCiphertext, IngressTokenNonce: row.IngressTokenNonce}
	if err := domain.ValidateTrigger(trigger); err != nil {
		return domain.Trigger{}, fmt.Errorf("validate project trigger row: %w", err)
	}
	return trigger, nil
}

func mapLLMRow(row projectdb.GetProjectLLMProviderRow) (domain.LLMProvider, error) {
	if !row.ID.Valid || !row.CredentialSecretID.Valid || row.Version <= 0 {
		return domain.LLMProvider{}, fmt.Errorf("project LLM provider row has invalid generated values")
	}
	provider := domain.LLMProvider{ID: uuidString(row.ID), Provider: row.Provider, BaseURL: row.BaseUrl, CredentialSecretID: uuidString(row.CredentialSecretID), Model: row.Model, Version: row.Version}
	if err := domain.ValidateLLMProvider(provider); err != nil {
		return domain.LLMProvider{}, fmt.Errorf("validate project LLM provider row: %w", err)
	}
	return provider, nil
}

func mapProject(id pgtype.UUID, key, name, description string, version int64, createdAt, updatedAt pgtype.Timestamptz) (domain.Project, error) {
	if !id.Valid || !createdAt.Valid || !updatedAt.Valid || version <= 0 {
		return domain.Project{}, fmt.Errorf("project row has invalid generated values")
	}
	project := domain.Project{ID: uuidString(id), Key: key, Name: name, Description: description, Version: version,
		CreatedAt: createdAt.Time.UTC(), UpdatedAt: updatedAt.Time.UTC()}
	if err := domain.ValidateProject(project); err != nil {
		return domain.Project{}, fmt.Errorf("validate project row: %w", err)
	}
	return project, nil
}

func mapSecret(id, projectID pgtype.UUID, name, kindValue string, keyVersion int32, version int64, createdAt, updatedAt pgtype.Timestamptz) (domain.Secret, error) {
	if !id.Valid || !projectID.Valid || !createdAt.Valid || !updatedAt.Valid || keyVersion <= 0 || version <= 0 {
		return domain.Secret{}, fmt.Errorf("project credential row has invalid generated values")
	}
	kind, err := domain.ParseSecretKind(kindValue)
	if err != nil {
		return domain.Secret{}, fmt.Errorf("map project credential kind: %w", err)
	}
	return domain.Secret{ID: uuidString(id), ProjectID: uuidString(projectID), Name: name, Kind: kind, KeyVersion: keyVersion,
		Version: version, CreatedAt: createdAt.Time.UTC(), UpdatedAt: updatedAt.Time.UTC()}, nil
}

func mapLLMProvider(row configurationRow) *domain.LLMProvider {
	if !row.llmID.Valid || row.llmProvider == nil || row.llmBaseURL == nil || row.llmModel == nil || row.llmVersion == nil {
		return nil
	}
	secretID := optionalUUIDString(row.llmSecretID)
	if secretID == nil {
		return nil
	}
	return &domain.LLMProvider{
		ID: uuidString(row.llmID), Provider: *row.llmProvider, BaseURL: *row.llmBaseURL,
		CredentialSecretID: *secretID, Model: *row.llmModel, Version: *row.llmVersion,
	}
}

func stringPointer(value string) *string { return &value }

func int64Pointer(value int64) *int64 { return &value }

func uuidParameter(value, label string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(value)
	if err != nil || parsed.Version() != 7 {
		return pgtype.UUID{}, fmt.Errorf("%s ID must be UUIDv7", label)
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}

func optionalUUID(value *string) pgtype.UUID {
	if value == nil {
		return pgtype.UUID{}
	}
	parsed, err := uuid.Parse(*value)
	if err != nil || parsed.Version() != 7 {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}
}

func uuidString(value pgtype.UUID) string { return uuid.UUID(value.Bytes).String() }
func optionalUUIDString(value pgtype.UUID) *string {
	if !value.Valid {
		return nil
	}
	result := uuidString(value)
	return &result
}

func projectConflict(err error) bool { return constraintError(err, "projects_key_unique_idx") }
func secretConflict(err error) bool {
	return constraintError(err, "project_secrets_project_name_unique")
}
func configurationConflict(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505"
}
func configurationReferenceConflict(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23503"
}
func constraintError(err error, name string) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.ConstraintName == name
}

type repositoryError struct {
	operation string
	cause     error
	stack     errtrace.Trace
}

func newRepositoryError(operation string, cause error) repositoryError {
	return repositoryError{operation: operation, cause: cause, stack: errtrace.Capture(1)}
}

func (e repositoryError) Error() string              { return e.operation + " failed" }
func (e repositoryError) Unwrap() error              { return e.cause }
func (e repositoryError) StackTrace() errtrace.Trace { return e.stack }
