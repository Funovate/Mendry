package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	authdomain "fixthe/backend/internal/modules/auth/domain"
	"fixthe/backend/internal/modules/projects/domain"
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
	UpsertConfiguration(context.Context, string, domain.Configuration, string, string) (domain.Configuration, error)
	ListAuditEvents(context.Context, string, int32) (ListResult[domain.AuditEvent], error)
}

type Cipher interface {
	Encrypt(projectID, secretID string, kind domain.SecretKind, plaintext []byte) (ciphertext, nonce []byte, keyVersion int32, err error)
	Decrypt(projectID, secretID string, kind domain.SecretKind, ciphertext, nonce []byte) (plaintext []byte, err error)
}

type GitRefLister interface {
	ListRefs(ctx context.Context, remoteURL, transport string, kind domain.SecretKind, credential []byte) (string, error)
}

type Options struct {
	Repository Repository
	Cipher     Cipher
	Git        GitRefLister
	NewID      func() (string, error)
}

type Service struct {
	repository  Repository
	cipher      Cipher
	git         GitRefLister
	idGenerator func() (string, error)
}

func NewService(options Options) (*Service, error) {
	if options.Repository == nil || options.Cipher == nil || options.NewID == nil {
		return nil, fmt.Errorf("project service dependencies are required")
	}
	return &Service{repository: options.Repository, cipher: options.Cipher, git: options.Git, idGenerator: options.NewID}, nil
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
	return s.repository.GetConfiguration(ctx, project.ID)
}

func (s *Service) PutConfiguration(ctx context.Context, principal authdomain.User, projectKey string, configuration domain.Configuration) (domain.Configuration, error) {
	project, err := s.requireAdmin(ctx, principal, projectKey)
	if err != nil {
		return domain.Configuration{}, err
	}
	if err := domain.ValidateConfiguration(configuration); err != nil {
		return domain.Configuration{}, ErrInvalidInput
	}
	ids, err := s.newIDs(5)
	if err != nil {
		return domain.Configuration{}, err
	}
	configuration.Environment.ID = ids[0]
	configuration.Repository.ID = ids[1]
	configuration.Source.ID = ids[2]
	configuration.Trigger.ID = ids[3]
	return s.repository.UpsertConfiguration(ctx, project.ID, configuration, principal.ID, ids[4])
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

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
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
