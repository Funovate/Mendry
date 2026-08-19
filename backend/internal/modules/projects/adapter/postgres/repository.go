package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"fixthe/backend/internal/modules/projects/adapter/postgres/projectdb"
	"fixthe/backend/internal/modules/projects/application"
	"fixthe/backend/internal/modules/projects/domain"
	"fixthe/backend/internal/platform/errtrace"
	platformpostgres "fixthe/backend/internal/platform/postgres"

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

func (r *Repository) CreateProject(ctx context.Context, project domain.Project, actorUserID, auditID string) (domain.Project, error) {
	projectID, err := uuidParameter(project.ID, "project")
	if err != nil {
		return domain.Project{}, err
	}
	actorID, err := uuidParameter(actorUserID, "actor user")
	if err != nil {
		return domain.Project{}, err
	}
	auditUUID, err := uuidParameter(auditID, "audit event")
	if err != nil {
		return domain.Project{}, err
	}
	row, err := r.queries.CreateProject(platformpostgres.WithOperation(ctx, "project.create"), projectdb.CreateProjectParams{
		ProjectID: projectID, ProjectKey: project.Key, Name: project.Name, Description: project.Description,
		ActorUserID: actorID, AuditID: auditUUID,
	})
	if projectConflict(err) {
		return domain.Project{}, application.ErrConflict
	}
	if err != nil {
		return domain.Project{}, newRepositoryError("insert project", err)
	}
	return mapProject(row.ID, row.ProjectKey, row.Name, row.Description, row.Role, row.Version, row.CreatedAt, row.UpdatedAt)
}

func (r *Repository) ListProjects(ctx context.Context, userID string, systemAdmin bool, limit int32) (application.ListResult[domain.Project], error) {
	userUUID, err := uuidParameter(userID, "user")
	if err != nil {
		return application.ListResult[domain.Project]{}, err
	}
	rows, err := r.queries.ListProjectsForUser(platformpostgres.WithOperation(ctx, "project.list"), projectdb.ListProjectsForUserParams{
		SystemAdmin: systemAdmin, UserID: userUUID, ResultLimit: limit,
	})
	if err != nil {
		return application.ListResult[domain.Project]{}, newRepositoryError("list projects", err)
	}
	projects := make([]domain.Project, 0, len(rows))
	var total int64
	for _, row := range rows {
		project, err := mapProject(row.ID, row.ProjectKey, row.Name, row.Description, row.Role, row.Version, row.CreatedAt, row.UpdatedAt)
		if err != nil {
			return application.ListResult[domain.Project]{}, fmt.Errorf("map listed project: %w", err)
		}
		projects = append(projects, project)
		total = row.TotalCount
	}
	return application.ListResult[domain.Project]{Items: projects, Total: total}, nil
}

func (r *Repository) ResolveProject(ctx context.Context, projectKey, userID string, systemAdmin bool) (domain.Project, error) {
	userUUID, err := uuidParameter(userID, "user")
	if err != nil {
		return domain.Project{}, err
	}
	row, err := r.queries.GetProjectAccess(platformpostgres.WithOperation(ctx, "project.resolve_access"), projectdb.GetProjectAccessParams{
		SystemAdmin: systemAdmin, UserID: userUUID, ProjectKey: projectKey,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Project{}, application.ErrNotFound
	}
	if err != nil {
		return domain.Project{}, newRepositoryError("resolve project access", err)
	}
	return mapProject(row.ID, row.ProjectKey, row.Name, row.Description, row.Role, row.Version, row.CreatedAt, row.UpdatedAt)
}

func (r *Repository) ListMembers(ctx context.Context, projectID string) (application.ListResult[domain.Member], error) {
	projectUUID, err := uuidParameter(projectID, "project")
	if err != nil {
		return application.ListResult[domain.Member]{}, err
	}
	rows, err := r.queries.ListProjectMembers(platformpostgres.WithOperation(ctx, "project.member.list"), projectUUID)
	if err != nil {
		return application.ListResult[domain.Member]{}, newRepositoryError("list project members", err)
	}
	members := make([]domain.Member, 0, len(rows))
	var total int64
	for _, row := range rows {
		member, err := mapMember(row.UserID, row.Username, row.Role, row.Version, row.CreatedAt, row.UpdatedAt)
		if err != nil {
			return application.ListResult[domain.Member]{}, fmt.Errorf("map listed project member: %w", err)
		}
		members = append(members, member)
		total = row.TotalCount
	}
	return application.ListResult[domain.Member]{Items: members, Total: total}, nil
}

func (r *Repository) UpsertMember(ctx context.Context, projectID, username string, role domain.Role, actorUserID, auditID string) (domain.Member, error) {
	params, err := r.memberMutationParameters(projectID, actorUserID, auditID)
	if err != nil {
		return domain.Member{}, err
	}
	if _, err := r.queries.GetEnabledProjectUser(platformpostgres.WithOperation(ctx, "project.member.get_enabled_user"), username); errors.Is(err, pgx.ErrNoRows) {
		return domain.Member{}, application.ErrMemberNotFound
	} else if err != nil {
		return domain.Member{}, newRepositoryError("query project member user", err)
	}
	row, err := r.queries.UpsertProjectMember(platformpostgres.WithOperation(ctx, "project.member.upsert"), projectdb.UpsertProjectMemberParams{
		Username: username, ProjectID: params.projectID, Role: string(role), AuditID: params.auditID, ActorUserID: params.actorID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Member{}, application.ErrMemberChangeRejected
	}
	if err != nil {
		return domain.Member{}, newRepositoryError("upsert project member", err)
	}
	return mapMember(row.UserID, row.Username, row.Role, row.Version, row.CreatedAt, row.UpdatedAt)
}

func (r *Repository) DeleteMember(ctx context.Context, projectID, username, actorUserID, auditID string) (domain.Member, error) {
	params, err := r.memberMutationParameters(projectID, actorUserID, auditID)
	if err != nil {
		return domain.Member{}, err
	}
	existing, err := r.queries.GetProjectMemberByUsername(platformpostgres.WithOperation(ctx, "project.member.get"), projectdb.GetProjectMemberByUsernameParams{
		ProjectID: params.projectID, Username: username,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Member{}, application.ErrMemberNotFound
	}
	if err != nil {
		return domain.Member{}, newRepositoryError("query project member", err)
	}
	row, err := r.queries.DeleteProjectMember(platformpostgres.WithOperation(ctx, "project.member.delete"), projectdb.DeleteProjectMemberParams{
		ProjectID: params.projectID, Username: username, AuditID: params.auditID, ActorUserID: params.actorID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Member{}, application.ErrMemberChangeRejected
	}
	if err != nil {
		return domain.Member{}, newRepositoryError("delete project member", err)
	}
	role, err := domain.ParseRole(row.Role)
	if err != nil {
		return domain.Member{}, fmt.Errorf("map deleted project member role: %w", err)
	}
	return domain.Member{UserID: uuidString(row.UserID), Username: row.Username, Role: role, Version: existing.Version,
		CreatedAt: existing.CreatedAt.Time.UTC(), UpdatedAt: existing.UpdatedAt.Time.UTC()}, nil
}

// UpdateProjectName 原子更新项目名；仅当环境名仍等于旧项目名时同步 environment。
func (r *Repository) UpdateProjectName(ctx context.Context, project domain.Project, actorUserID, auditID string) (domain.Project, error) {
	projectID, err := uuidParameter(project.ID, "project")
	if err != nil {
		return domain.Project{}, err
	}
	actorID, err := uuidParameter(actorUserID, "actor user")
	if err != nil {
		return domain.Project{}, err
	}
	auditUUID, err := uuidParameter(auditID, "audit event")
	if err != nil {
		return domain.Project{}, err
	}
	row, err := r.queries.UpdateProjectName(platformpostgres.WithOperation(ctx, "project.rename"), projectdb.UpdateProjectNameParams{
		Role: string(project.Role), ProjectID: projectID, Name: project.Name, AuditID: auditUUID, ActorUserID: actorID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Project{}, application.ErrNotFound
	}
	if err != nil {
		return domain.Project{}, newRepositoryError("update project name", err)
	}
	return mapProject(row.ID, row.ProjectKey, row.Name, row.Description, row.Role, row.Version, row.CreatedAt, row.UpdatedAt)
}

func (r *Repository) CreateSecret(ctx context.Context, encrypted domain.EncryptedSecret, actorUserID, auditID string) (domain.Secret, error) {
	secretID, err := uuidParameter(encrypted.ID, "project secret")
	if err != nil {
		return domain.Secret{}, err
	}
	projectID, err := uuidParameter(encrypted.ProjectID, "project")
	if err != nil {
		return domain.Secret{}, err
	}
	actorID, err := uuidParameter(actorUserID, "actor user")
	if err != nil {
		return domain.Secret{}, err
	}
	auditUUID, err := uuidParameter(auditID, "audit event")
	if err != nil {
		return domain.Secret{}, err
	}
	row, err := r.queries.CreateProjectSecret(platformpostgres.WithOperation(ctx, "project.secret.create"), projectdb.CreateProjectSecretParams{
		SecretID: secretID, ProjectID: projectID, Name: encrypted.Name, Kind: string(encrypted.Kind),
		Ciphertext: encrypted.Ciphertext, Nonce: encrypted.Nonce, KeyVersion: encrypted.KeyVersion,
		AuditID: auditUUID, ActorUserID: actorID,
	})
	if secretConflict(err) {
		return domain.Secret{}, application.ErrConflict
	}
	if err != nil {
		return domain.Secret{}, newRepositoryError("insert project credential", err)
	}
	return mapSecret(row.ID, row.ProjectID, row.Name, row.Kind, row.KeyVersion, row.Version, row.CreatedAt, row.UpdatedAt)
}

// UpdateSecret 原地更新凭据 metadata，并写入仅含 name/kind/rotated 的审计事件。
func (r *Repository) UpdateSecret(ctx context.Context, encrypted domain.EncryptedSecret, actorUserID, auditID string, rotated bool) (domain.Secret, error) {
	secretID, err := uuidParameter(encrypted.ID, "project secret")
	if err != nil {
		return domain.Secret{}, err
	}
	projectID, err := uuidParameter(encrypted.ProjectID, "project")
	if err != nil {
		return domain.Secret{}, err
	}
	actorID, err := uuidParameter(actorUserID, "actor user")
	if err != nil {
		return domain.Secret{}, err
	}
	auditUUID, err := uuidParameter(auditID, "audit event")
	if err != nil {
		return domain.Secret{}, err
	}
	row, err := r.queries.UpdateProjectSecret(platformpostgres.WithOperation(ctx, "project.secret.update"), projectdb.UpdateProjectSecretParams{
		Name: encrypted.Name, Ciphertext: encrypted.Ciphertext, Nonce: encrypted.Nonce, KeyVersion: encrypted.KeyVersion,
		ProjectID: projectID, SecretID: secretID, AuditID: auditUUID, ActorUserID: actorID, Rotated: rotated,
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
	return mapConfiguration(configurationRow{
		environmentID: row.EnvironmentID, environmentKey: row.EnvironmentKey, environmentName: row.EnvironmentName,
		service: row.Service, environmentVersion: row.EnvironmentVersion, repositoryID: row.RepositoryID,
		remoteURL: row.RemoteUrl, scmProvider: row.ScmProvider, transport: row.Transport,
		repositorySecretID: row.RepositoryCredentialSecretID, productionBranch: row.ProductionBranch,
		deployedCommit: row.DeployedCommit, repositoryVersion: row.RepositoryVersion, sourceID: row.SourceID,
		sourceName: row.SourceName, sourceKind: row.SourceKind, sourceSecretID: row.SourceCredentialSecretID,
		sourceConfig: row.SourceConfig, sourceCapabilities: row.SourceCapabilities, sourceEnabled: row.SourceEnabled,
		sourceVersion: row.SourceVersion, triggerID: row.TriggerID, triggerName: row.TriggerName,
		triggerKind: row.TriggerKind, signingSecretID: row.SigningSecretID, triggerConfig: row.TriggerConfig,
		triggerEnabled: row.TriggerEnabled, triggerVersion: row.TriggerVersion,
		ingressTokenHash: row.IngressTokenHash, ingressTokenCiphertext: row.IngressTokenCiphertext,
		ingressTokenNonce: row.IngressTokenNonce,
		llmID:             row.LlmID, llmProvider: row.LlmProvider, llmBaseURL: row.LlmBaseUrl,
		llmSecretID: row.LlmCredentialSecretID, llmModel: row.LlmModel, llmVersion: row.LlmVersion,
	})
}

func (r *Repository) UpsertConfiguration(ctx context.Context, projectID string, configuration domain.Configuration, actorUserID, auditID string) (domain.Configuration, error) {
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
	actorID, err := uuidParameter(actorUserID, "actor user")
	if err != nil {
		return domain.Configuration{}, err
	}
	auditUUID, err := uuidParameter(auditID, "audit event")
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
		SourceID: sourceID, SourceName: configuration.Source.Name, SourceKind: configuration.Source.Kind,
		SourceCredentialSecretID: optionalUUID(configuration.Source.CredentialSecretID), SourceConfig: configuration.Source.Config,
		SourceCapabilities: configuration.Source.Capabilities, SourceEnabled: configuration.Source.Enabled,
		TriggerID: triggerID, TriggerName: configuration.Trigger.Name, TriggerKind: configuration.Trigger.Kind,
		SigningSecretID: optionalUUID(configuration.Trigger.SigningSecretID), TriggerConfig: configuration.Trigger.Config,
		TriggerEnabled:   configuration.Trigger.Enabled,
		IngressTokenHash: configuration.Trigger.IngressTokenHash, IngressTokenCiphertext: configuration.Trigger.IngressTokenCiphertext,
		IngressTokenNonce: configuration.Trigger.IngressTokenNonce,
		AuditID:           auditUUID, ActorUserID: actorID,
		LlmID: llmID, LlmProvider: configuration.LLM.Provider, LlmBaseUrl: configuration.LLM.BaseURL,
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
	return mapConfiguration(configurationRow{
		environmentID: row.EnvironmentID, environmentKey: row.EnvironmentKey, environmentName: row.EnvironmentName,
		service: row.Service, environmentVersion: row.EnvironmentVersion, repositoryID: row.RepositoryID,
		remoteURL: row.RemoteUrl, scmProvider: row.ScmProvider, transport: row.Transport,
		repositorySecretID: row.RepositoryCredentialSecretID, productionBranch: row.ProductionBranch,
		deployedCommit: row.DeployedCommit, repositoryVersion: row.RepositoryVersion, sourceID: row.SourceID,
		sourceName: row.SourceName, sourceKind: row.SourceKind, sourceSecretID: row.SourceCredentialSecretID,
		sourceConfig: row.SourceConfig, sourceCapabilities: row.SourceCapabilities, sourceEnabled: row.SourceEnabled,
		sourceVersion: row.SourceVersion, triggerID: row.TriggerID, triggerName: row.TriggerName,
		triggerKind: row.TriggerKind, signingSecretID: row.SigningSecretID, triggerConfig: row.TriggerConfig,
		triggerEnabled: row.TriggerEnabled, triggerVersion: row.TriggerVersion,
		ingressTokenHash: row.IngressTokenHash, ingressTokenCiphertext: row.IngressTokenCiphertext,
		ingressTokenNonce: row.IngressTokenNonce,
		llmID:             row.LlmID, llmProvider: stringPointer(row.LlmProvider), llmBaseURL: stringPointer(row.LlmBaseUrl),
		llmSecretID: row.LlmCredentialSecretID, llmModel: stringPointer(row.LlmModel), llmVersion: int64Pointer(row.LlmVersion),
	})
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
	if !row.ProjectID.Valid || !row.SourceID.Valid {
		return application.WebhookIngress{}, application.ErrNotFound
	}
	return application.WebhookIngress{ProjectID: uuidString(row.ProjectID), SourceID: uuidString(row.SourceID)}, nil
}

func (r *Repository) UpdateWebhookToken(ctx context.Context, projectID string, hash, ciphertext, nonce []byte, actorUserID, auditID string, rotated bool) error {
	if err := domain.ValidateWebhookTokenColumns(hash, ciphertext, nonce); err != nil {
		return application.ErrInvalidInput
	}
	projectUUID, err := uuidParameter(projectID, "project")
	if err != nil {
		return err
	}
	actorID, err := uuidParameter(actorUserID, "actor user")
	if err != nil {
		return err
	}
	auditUUID, err := uuidParameter(auditID, "audit event")
	if err != nil {
		return err
	}
	if _, err := r.queries.UpdateWebhookToken(platformpostgres.WithOperation(ctx, "project.webhook.update_token"), projectdb.UpdateWebhookTokenParams{
		IngressTokenHash: hash, IngressTokenCiphertext: ciphertext, IngressTokenNonce: nonce,
		ProjectID: projectUUID, AuditID: auditUUID, ActorUserID: actorID, Rotated: rotated,
	}); errors.Is(err, pgx.ErrNoRows) {
		return application.ErrConfigurationNotFound
	} else if err != nil {
		return newRepositoryError("update webhook token", err)
	}
	return nil
}

func (r *Repository) ListAuditEvents(ctx context.Context, projectID string, limit int32) (application.ListResult[domain.AuditEvent], error) {
	projectUUID, err := uuidParameter(projectID, "project")
	if err != nil {
		return application.ListResult[domain.AuditEvent]{}, err
	}
	rows, err := r.queries.ListProjectAuditEvents(platformpostgres.WithOperation(ctx, "project.audit.list"), projectdb.ListProjectAuditEventsParams{
		ProjectID: projectUUID, ResultLimit: limit,
	})
	if err != nil {
		return application.ListResult[domain.AuditEvent]{}, newRepositoryError("list project audit events", err)
	}
	events := make([]domain.AuditEvent, 0, len(rows))
	var total int64
	for _, row := range rows {
		if !row.ID.Valid || !row.OccurredAt.Valid || !json.Valid(row.Metadata) {
			return application.ListResult[domain.AuditEvent]{}, fmt.Errorf("project audit row has invalid generated values")
		}
		events = append(events, domain.AuditEvent{ID: uuidString(row.ID), ActorUserID: optionalUUIDString(row.ActorUserID),
			Action: row.Action, TargetType: row.TargetType, TargetID: optionalUUIDString(row.TargetID),
			Summary: row.Summary, Metadata: row.Metadata, OccurredAt: row.OccurredAt.Time.UTC()})
		total = row.TotalCount
	}
	return application.ListResult[domain.AuditEvent]{Items: events, Total: total}, nil
}

type memberMutationParams struct{ projectID, actorID, auditID pgtype.UUID }

func (r *Repository) memberMutationParameters(projectID, actorUserID, auditID string) (memberMutationParams, error) {
	projectUUID, err := uuidParameter(projectID, "project")
	if err != nil {
		return memberMutationParams{}, err
	}
	actorID, err := uuidParameter(actorUserID, "actor user")
	if err != nil {
		return memberMutationParams{}, err
	}
	auditUUID, err := uuidParameter(auditID, "audit event")
	if err != nil {
		return memberMutationParams{}, err
	}
	return memberMutationParams{projectID: projectUUID, actorID: actorID, auditID: auditUUID}, nil
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
	sourceName, sourceKind                                      string
	sourceSecretID                                              pgtype.UUID
	sourceConfig                                                []byte
	sourceCapabilities                                          []string
	sourceEnabled                                               bool
	sourceVersion                                               int64
	triggerID                                                   pgtype.UUID
	triggerName, triggerKind                                    string
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
		Source: domain.Source{ID: uuidString(row.sourceID), Name: row.sourceName, Kind: row.sourceKind,
			CredentialSecretID: optionalUUIDString(row.sourceSecretID), Config: row.sourceConfig,
			Capabilities: row.sourceCapabilities, Enabled: row.sourceEnabled, Version: row.sourceVersion},
		Trigger: domain.Trigger{ID: uuidString(row.triggerID), Name: row.triggerName, Kind: row.triggerKind,
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

func mapProject(id pgtype.UUID, key, name, description, roleValue string, version int64, createdAt, updatedAt pgtype.Timestamptz) (domain.Project, error) {
	if !id.Valid || !createdAt.Valid || !updatedAt.Valid || version <= 0 {
		return domain.Project{}, fmt.Errorf("project row has invalid generated values")
	}
	role, err := domain.ParseRole(roleValue)
	if err != nil {
		return domain.Project{}, fmt.Errorf("map project role: %w", err)
	}
	project := domain.Project{ID: uuidString(id), Key: key, Name: name, Description: description, Role: role, Version: version,
		CreatedAt: createdAt.Time.UTC(), UpdatedAt: updatedAt.Time.UTC()}
	if err := domain.ValidateProject(project); err != nil {
		return domain.Project{}, fmt.Errorf("validate project row: %w", err)
	}
	return project, nil
}

func mapMember(id pgtype.UUID, username, roleValue string, version int64, createdAt, updatedAt pgtype.Timestamptz) (domain.Member, error) {
	if !id.Valid || !createdAt.Valid || !updatedAt.Valid || version <= 0 {
		return domain.Member{}, fmt.Errorf("project member row has invalid generated values")
	}
	role, err := domain.ParseRole(roleValue)
	if err != nil {
		return domain.Member{}, fmt.Errorf("map project member role: %w", err)
	}
	return domain.Member{UserID: uuidString(id), Username: username, Role: role, Version: version,
		CreatedAt: createdAt.Time.UTC(), UpdatedAt: updatedAt.Time.UTC()}, nil
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
