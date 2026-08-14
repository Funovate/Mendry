package application

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	authdomain "fixthe/backend/internal/modules/auth/domain"
	"fixthe/backend/internal/modules/projects/domain"
)

const (
	projectID = "019ff544-405c-7d21-9f10-cb3fc579605c"
	userID    = "019ff544-405c-7d10-8f10-cb3fc579605c"
)

type fakeRepository struct {
	project       domain.Project
	created       domain.Project
	member        domain.Member
	secret        domain.EncryptedSecret
	rotated       bool
	configuration domain.Configuration
	userID        string
	systemAdmin   bool
	projectKey    string
	projectID     string
	role          domain.Role
	error         error
}

func (f *fakeRepository) CreateProject(_ context.Context, project domain.Project, actorID, _ string) (domain.Project, error) {
	f.created, f.userID = project, actorID
	return project, f.error
}
func (f *fakeRepository) ListProjects(_ context.Context, userID string, systemAdmin bool, _ int32) (ListResult[domain.Project], error) {
	f.userID, f.systemAdmin = userID, systemAdmin
	return ListResult[domain.Project]{Items: []domain.Project{f.project}, Total: 1}, f.error
}
func (f *fakeRepository) ResolveProject(_ context.Context, key, userID string, systemAdmin bool) (domain.Project, error) {
	f.projectKey, f.userID, f.systemAdmin = key, userID, systemAdmin
	return f.project, f.error
}
func (*fakeRepository) ListMembers(context.Context, string) (ListResult[domain.Member], error) {
	return ListResult[domain.Member]{Items: []domain.Member{}}, nil
}
func (f *fakeRepository) UpsertMember(_ context.Context, projectID, username string, role domain.Role, _, _ string) (domain.Member, error) {
	f.projectID, f.role = projectID, role
	f.member = domain.Member{Username: username, Role: role}
	return f.member, f.error
}
func (*fakeRepository) DeleteMember(context.Context, string, string, string, string) (domain.Member, error) {
	return domain.Member{}, nil
}
func (f *fakeRepository) UpdateProjectName(_ context.Context, project domain.Project, actorID, _ string) (domain.Project, error) {
	f.created, f.userID = project, actorID
	project.Version = f.project.Version + 1
	f.project = project
	return project, f.error
}
func (f *fakeRepository) CreateSecret(_ context.Context, secret domain.EncryptedSecret, _, _ string) (domain.Secret, error) {
	f.secret = secret
	return secret.Secret, f.error
}
func (f *fakeRepository) UpdateSecret(_ context.Context, secret domain.EncryptedSecret, _, _ string, rotated bool) (domain.Secret, error) {
	f.secret, f.rotated = secret, rotated
	secret.Version++
	return secret.Secret, f.error
}
func (*fakeRepository) ListSecrets(context.Context, string) (ListResult[domain.Secret], error) {
	return ListResult[domain.Secret]{Items: []domain.Secret{}}, nil
}
func (f *fakeRepository) GetEncryptedSecret(context.Context, string, string) (domain.EncryptedSecret, error) {
	if f.secret.ID == "" {
		return domain.EncryptedSecret{}, ErrInvalidInput
	}
	return f.secret, f.error
}
func (*fakeRepository) GetConfiguration(context.Context, string) (domain.Configuration, error) {
	return domain.Configuration{}, nil
}
func (f *fakeRepository) UpsertConfiguration(_ context.Context, projectID string, configuration domain.Configuration, _, _ string) (domain.Configuration, error) {
	f.projectID, f.configuration = projectID, configuration
	return configuration, f.error
}
func (*fakeRepository) ListAuditEvents(context.Context, string, int32) (ListResult[domain.AuditEvent], error) {
	return ListResult[domain.AuditEvent]{Items: []domain.AuditEvent{}}, nil
}

type fakeCipher struct {
	plaintext           []byte
	projectID, secretID string
	kind                domain.SecretKind
}

func (f *fakeCipher) Encrypt(projectID, secretID string, kind domain.SecretKind, plaintext []byte) ([]byte, []byte, int32, error) {
	f.projectID, f.secretID, f.kind = projectID, secretID, kind
	f.plaintext = append([]byte(nil), plaintext...)
	return []byte("encrypted-value-tag"), []byte("123456789012"), 1, nil
}
func (f *fakeCipher) Decrypt(projectID, secretID string, kind domain.SecretKind, _, _ []byte) ([]byte, error) {
	f.projectID, f.secretID, f.kind = projectID, secretID, kind
	return append([]byte(nil), f.plaintext...), nil
}

func TestOnlySystemAdminCreatesProjectAndListsAllProjects(t *testing.T) {
	repository := &fakeRepository{project: domain.Project{ID: projectID, Key: "payments", Role: domain.RoleAdmin}}
	service := newService(t, repository, &fakeCipher{})
	admin := authdomain.User{ID: userID, Enabled: true, Role: authdomain.RoleAdmin}
	created, err := service.CreateProject(context.Background(), admin, " Payments-API ", "Payments API", "Production service")
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	if created.Key != "payments-api" || repository.userID != userID {
		t.Fatalf("created = %#v, actor = %q", created, repository.userID)
	}
	viewer := authdomain.User{ID: userID, Enabled: true, Role: authdomain.RoleViewer}
	if _, err := service.CreateProject(context.Background(), viewer, "viewer-project", "Viewer", ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer CreateProject() error = %v", err)
	}
	if _, err := service.ListProjects(context.Background(), admin, 10); err != nil || !repository.systemAdmin {
		t.Fatalf("admin ListProjects() system admin = %t, error = %v", repository.systemAdmin, err)
	}
	if _, err := service.ListProjects(context.Background(), viewer, 10); err != nil || repository.systemAdmin {
		t.Fatalf("viewer ListProjects() system admin = %t, error = %v", repository.systemAdmin, err)
	}
}

func TestProjectAdminManagesMembersButViewerCannot(t *testing.T) {
	repository := &fakeRepository{project: domain.Project{ID: projectID, Key: "payments", Role: domain.RoleAdmin}}
	service := newService(t, repository, &fakeCipher{})
	principal := authdomain.User{ID: userID, Enabled: true, Role: authdomain.RoleViewer}
	member, err := service.UpsertMember(context.Background(), principal, "payments", " Operator.User ", "operator")
	if err != nil || member.Username != "operator.user" || repository.projectID != projectID || repository.role != domain.RoleOperator {
		t.Fatalf("UpsertMember() = %#v, repository = %#v, error = %v", member, repository, err)
	}
	repository.project.Role = domain.RoleViewer
	if _, err := service.UpsertMember(context.Background(), principal, "payments", "operator.user", "viewer"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("project viewer UpsertMember() error = %v", err)
	}
}

func TestUpdateProjectNameRequiresAdminAndPreservesKey(t *testing.T) {
	repository := &fakeRepository{project: domain.Project{ID: projectID, Key: "payments", Name: "Payments", Role: domain.RoleAdmin, Version: 1}}
	service := newService(t, repository, &fakeCipher{})
	principal := authdomain.User{ID: userID, Enabled: true}
	updated, err := service.UpdateProjectName(context.Background(), principal, "payments", " Payments Platform ")
	if err != nil || updated.Key != "payments" || updated.Name != "Payments Platform" || repository.created.Key != "payments" {
		t.Fatalf("UpdateProjectName() = %#v, repository = %#v, error = %v", updated, repository, err)
	}
	repository.project.Role = domain.RoleViewer
	if _, err := service.UpdateProjectName(context.Background(), principal, "payments", "Viewer rename"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer UpdateProjectName() error = %v", err)
	}
	repository.project.Role = domain.RoleAdmin
	if _, err := service.UpdateProjectName(context.Background(), principal, "payments", ""); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty UpdateProjectName() error = %v", err)
	}
}

func TestUpdateSecretNameOnlyPreservesEncryptedMaterial(t *testing.T) {
	secretID := "019ff544-405c-7d24-9f10-cb3fc579605c"
	repository := &fakeRepository{
		project: domain.Project{ID: projectID, Key: "payments", Role: domain.RoleAdmin},
		secret: domain.EncryptedSecret{
			Secret:     domain.Secret{ID: secretID, ProjectID: projectID, Name: "git-token", Kind: domain.SecretGitCredential, KeyVersion: 1},
			Ciphertext: []byte("encrypted-value-tag"),
			Nonce:      []byte("123456789012"),
		},
	}
	cipher := &fakeCipher{}
	service := newService(t, repository, cipher)
	updated, err := service.UpdateSecret(context.Background(), authdomain.User{ID: userID, Enabled: true}, "payments", secretID, "git-token-renamed", nil)
	if err != nil {
		t.Fatalf("UpdateSecret() error = %v", err)
	}
	if updated.ID != secretID || updated.Kind != domain.SecretGitCredential || updated.Name != "git-token-renamed" || updated.KeyVersion != 1 {
		t.Fatalf("updated = %#v", updated)
	}
	if cipher.plaintext != nil || repository.rotated || string(repository.secret.Ciphertext) != "encrypted-value-tag" {
		t.Fatalf("name-only update encrypted or rotated: cipher=%#v repository=%#v", cipher, repository)
	}
}

func TestUpdateSecretRotationReencryptsSameIdentity(t *testing.T) {
	secretID := "019ff544-405c-7d24-9f10-cb3fc579605c"
	repository := &fakeRepository{
		project: domain.Project{ID: projectID, Key: "payments", Role: domain.RoleAdmin},
		secret: domain.EncryptedSecret{
			Secret:     domain.Secret{ID: secretID, ProjectID: projectID, Name: "git-token", Kind: domain.SecretGitCredential, KeyVersion: 1},
			Ciphertext: []byte("old-ciphertext"),
			Nonce:      []byte("old-nonce-12"),
		},
	}
	cipher := &fakeCipher{}
	service := newService(t, repository, cipher)
	plaintext := []byte("replacement-token")
	updated, err := service.UpdateSecret(context.Background(), authdomain.User{ID: userID, Enabled: true}, "payments", secretID, "git-token", plaintext)
	if err != nil {
		t.Fatalf("UpdateSecret() error = %v", err)
	}
	if updated.ID != secretID || updated.Kind != domain.SecretGitCredential || cipher.secretID != secretID || cipher.kind != domain.SecretGitCredential {
		t.Fatalf("updated = %#v cipher = %#v", updated, cipher)
	}
	if string(cipher.plaintext) != string(plaintext) || !repository.rotated || string(repository.secret.Ciphertext) == string(plaintext) {
		t.Fatalf("rotation did not re-encrypt: cipher=%#v repository=%#v", cipher, repository)
	}
	repository.project.Role = domain.RoleOperator
	if _, err := service.UpdateSecret(context.Background(), authdomain.User{ID: userID, Enabled: true}, "payments", secretID, "git-token", nil); !errors.Is(err, ErrForbidden) {
		t.Fatalf("operator UpdateSecret() error = %v", err)
	}
}

func TestCreateSecretEncryptsBeforePersistence(t *testing.T) {
	repository := &fakeRepository{project: domain.Project{ID: projectID, Key: "payments", Role: domain.RoleAdmin}}
	cipher := &fakeCipher{}
	service := newService(t, repository, cipher)
	plaintext := []byte("top-secret-token")
	secret, err := service.CreateSecret(context.Background(), authdomain.User{ID: userID, Enabled: true}, "payments", "git-token", "git_credential", plaintext)
	if err != nil {
		t.Fatalf("CreateSecret() error = %v", err)
	}
	if string(cipher.plaintext) != string(plaintext) || cipher.projectID != projectID || cipher.secretID != secret.ID || cipher.kind != domain.SecretGitCredential {
		t.Fatalf("cipher input = %#v", cipher)
	}
	if string(repository.secret.Ciphertext) == string(plaintext) || string(repository.secret.Ciphertext) != "encrypted-value-tag" || len(repository.secret.Nonce) != 12 {
		t.Fatalf("persisted secret = %#v", repository.secret)
	}
}

func TestConfigurationWriteRequiresProjectAdminAndAssignsOwnedIDs(t *testing.T) {
	repository := &fakeRepository{project: domain.Project{ID: projectID, Key: "payments", Role: domain.RoleAdmin}}
	service := newService(t, repository, &fakeCipher{})
	configuration := validConfiguration()
	result, err := service.PutConfiguration(context.Background(), authdomain.User{ID: userID, Enabled: true}, "payments", configuration)
	if err != nil {
		t.Fatalf("PutConfiguration() error = %v", err)
	}
	if repository.projectID != projectID || result.Environment.ID == "" || result.Repository.ID == "" || result.Source.ID == "" || result.Trigger.ID == "" {
		t.Fatalf("configuration = %#v", result)
	}
	repository.project.Role = domain.RoleOperator
	if _, err := service.PutConfiguration(context.Background(), authdomain.User{ID: userID, Enabled: true}, "payments", configuration); !errors.Is(err, ErrForbidden) {
		t.Fatalf("operator PutConfiguration() error = %v", err)
	}
}

func newService(t *testing.T, repository Repository, cipher Cipher) *Service {
	t.Helper()
	ids := []string{"019ff544-405c-7d31-9f10-cb3fc579605c", "019ff544-405c-7d32-9f10-cb3fc579605c", "019ff544-405c-7d33-9f10-cb3fc579605c", "019ff544-405c-7d34-9f10-cb3fc579605c", "019ff544-405c-7d35-9f10-cb3fc579605c"}
	index := 0
	service, err := NewService(Options{Repository: repository, Cipher: cipher, NewID: func() (string, error) { id := ids[index%len(ids)]; index++; return id, nil }})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

func validConfiguration() domain.Configuration {
	secretID := "019ff544-405c-7d24-9f10-cb3fc579605c"
	return domain.Configuration{
		Environment: domain.Environment{Key: "production", Name: "Production"},
		Repository: domain.Repository{RemoteURL: "https://github.com/example/service.git", SCMProvider: "github", Transport: "https",
			ProductionBranch: "main", DeployedCommit: "0123456789abcdef0123456789abcdef01234567"},
		Source: domain.Source{Name: "production-mcp", Kind: "mcp", Config: json.RawMessage(`{"schemaVersion":1,"endpoint":"https://mcp.example.com","transport":"http","headers":{},"evidenceProfile":"default","queryScope":"logs"}`),
			Capabilities: []string{"pull_collection"}, Enabled: true},
		Trigger: domain.Trigger{Name: "error-events", Kind: "signed_webhook", SigningSecretID: &secretID,
			Config: json.RawMessage(`{"schemaVersion":1,"eventTypes":["error"],"deduplicationKey":"fingerprint"}`), Enabled: true},
	}
}

type fakeGit struct {
	remoteURL, transport string
	kind                 domain.SecretKind
	credential           []byte
	output               string
	err                  error
}

func (f *fakeGit) ListRefs(_ context.Context, remoteURL, transport string, kind domain.SecretKind, credential []byte) (string, error) {
	f.remoteURL, f.transport, f.kind = remoteURL, transport, kind
	f.credential = append([]byte(nil), credential...)
	return f.output, f.err
}

func TestProbeRepositoryRefsDecryptsOwnedSecret(t *testing.T) {
	secretID := "019ff544-405c-7d24-9f10-cb3fc579605c"
	repository := &fakeRepository{
		project: domain.Project{ID: projectID, Key: "payments", Role: domain.RoleAdmin},
		secret:  domain.EncryptedSecret{Secret: domain.Secret{ID: secretID, ProjectID: projectID, Name: "git-http", Kind: domain.SecretGitCredential}, Ciphertext: []byte("cipher"), Nonce: []byte("nonce12bytes")},
	}
	cipher := &fakeCipher{plaintext: []byte("deploy:token")}
	git := &fakeGit{output: "ref: refs/heads/main\tHEAD\n0123456789abcdef0123456789abcdef01234567\trefs/heads/main\n"}
	service, err := NewService(Options{Repository: repository, Cipher: cipher, Git: git, NewID: func() (string, error) { return secretID, nil }})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	refs, err := service.ProbeRepositoryRefs(context.Background(), authdomain.User{ID: userID, Enabled: true}, "payments", "https://git.example.internal/app.git", "https", secretID)
	if err != nil {
		t.Fatalf("ProbeRepositoryRefs() error = %v", err)
	}
	if refs.DefaultBranch != "main" || string(git.credential) != "deploy:token" || git.remoteURL != "https://git.example.internal/app.git" {
		t.Fatalf("refs = %#v git = %#v", refs, git)
	}
}

func TestProbeRepositoryRefsRejectsInvalidRemoteWithoutCallingGit(t *testing.T) {
	git := &fakeGit{output: "should-not-run"}
	service := newService(t, &fakeRepository{project: domain.Project{ID: projectID, Key: "payments", Role: domain.RoleAdmin}}, &fakeCipher{})
	service.git = git
	if _, err := service.ProbeRepositoryRefs(context.Background(), authdomain.User{ID: userID, Enabled: true}, "payments", "https://git.example.internal/app.git", "ssh", "019ff544-405c-7d24-9f10-cb3fc579605c"); err != ErrInvalidInput {
		t.Fatalf("error = %v", err)
	}
	if git.remoteURL != "" {
		t.Fatal("git lister was called")
	}
}
