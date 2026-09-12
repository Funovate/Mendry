package application

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	authdomain "mendry/backend/internal/modules/auth/domain"
	"mendry/backend/internal/modules/projects/domain"
)

const (
	projectID = "019ff544-405c-7d21-9f10-cb3fc579605c"
	userID    = "019ff544-405c-7d10-8f10-cb3fc579605c"
)

type fakeRepository struct {
	project       domain.Project
	created       domain.Project
	secret        domain.EncryptedSecret
	rotated       bool
	configuration domain.Configuration
	ingress       WebhookIngress
	lookupHash    []byte
	projectKey    string
	projectID     string
	error         error
}

func (f *fakeRepository) CreateProject(_ context.Context, project domain.Project) (domain.Project, error) {
	f.created = project
	return project, f.error
}
func (f *fakeRepository) ListProjects(_ context.Context, _ int32) (ListResult[domain.Project], error) {
	return ListResult[domain.Project]{Items: []domain.Project{f.project}, Total: 1}, f.error
}
func (f *fakeRepository) ResolveProject(_ context.Context, key string) (domain.Project, error) {
	f.projectKey = key
	return f.project, f.error
}
func (f *fakeRepository) UpdateProjectName(_ context.Context, project domain.Project) (domain.Project, error) {
	f.created = project
	project.Version = f.project.Version + 1
	f.project = project
	return project, f.error
}
func (f *fakeRepository) CreateSecret(_ context.Context, secret domain.EncryptedSecret) (domain.Secret, error) {
	f.secret = secret
	return secret.Secret, f.error
}
func (f *fakeRepository) UpdateSecret(_ context.Context, secret domain.EncryptedSecret) (domain.Secret, error) {
	f.rotated = string(f.secret.Ciphertext) != string(secret.Ciphertext)
	f.secret = secret
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
func (f *fakeRepository) GetConfiguration(context.Context, string) (domain.Configuration, error) {
	if f.configuration.Trigger.ID == "" && f.configuration.Environment.ID == "" {
		return domain.Configuration{}, ErrConfigurationNotFound
	}
	return f.configuration, f.error
}

func (f *fakeRepository) GetConfigurationDraft(context.Context, string) (domain.ConfigurationDraft, error) {
	draft := domain.ConfigurationDraft{}
	if f.configuration.Environment.ID != "" {
		environment := f.configuration.Environment
		draft.Environment = &environment
	}
	if f.configuration.Repository.ID != "" {
		repository := f.configuration.Repository
		draft.Repository = &repository
	}
	if f.configuration.Source.ID != "" {
		source := f.configuration.Source
		draft.Source = &source
	}
	if f.configuration.Trigger.ID != "" {
		trigger := f.configuration.Trigger
		draft.Trigger = &trigger
	}
	if f.configuration.LLM != nil {
		provider := *f.configuration.LLM
		draft.LLM = &provider
	}
	return draft, f.error
}

func (f *fakeRepository) UpsertEnvironment(_ context.Context, projectID string, environment domain.Environment) (domain.Environment, error) {
	f.projectID = projectID
	environment.Version++
	f.configuration.Environment = environment
	return environment, f.error
}

func (f *fakeRepository) UpsertRepository(_ context.Context, projectID string, repository domain.Repository) (domain.Repository, error) {
	f.projectID = projectID
	repository.Version++
	f.configuration.Repository = repository
	return repository, f.error
}

func (f *fakeRepository) UpsertSource(_ context.Context, projectID, _ string, source domain.Source) (domain.Source, error) {
	f.projectID = projectID
	source.Version++
	f.configuration.Source = source
	return source, f.error
}

func (f *fakeRepository) UpsertTrigger(_ context.Context, projectID, _ string, trigger domain.Trigger) (domain.Trigger, error) {
	f.projectID = projectID
	trigger.Version++
	f.configuration.Trigger = trigger
	return trigger, f.error
}

func (f *fakeRepository) UpsertLLMProvider(_ context.Context, projectID string, provider domain.LLMProvider) (domain.LLMProvider, error) {
	f.projectID = projectID
	provider.Version++
	f.configuration.LLM = &provider
	return provider, f.error
}

func (f *fakeRepository) UpsertRemediationPolicy(_ context.Context, projectID string, policy domain.RemediationPolicy) (domain.RemediationPolicy, error) {
	f.projectID = projectID
	policy.Version++
	f.configuration.Remediation = policy
	return policy, f.error
}
func (f *fakeRepository) UpsertConfiguration(_ context.Context, projectID string, configuration domain.Configuration) (domain.Configuration, error) {
	f.projectID, f.configuration = projectID, configuration
	return configuration, f.error
}
func (f *fakeRepository) LookupWebhookToken(_ context.Context, hash []byte) (WebhookIngress, error) {
	f.lookupHash = append([]byte(nil), hash...)
	if f.ingress.ProjectID == "" {
		return WebhookIngress{}, ErrNotFound
	}
	return f.ingress, f.error
}
func (f *fakeRepository) UpdateWebhookToken(_ context.Context, projectID string, hash, ciphertext, nonce []byte) error {
	f.projectID, f.rotated = projectID, true
	f.configuration.Trigger.IngressTokenHash = append([]byte(nil), hash...)
	f.configuration.Trigger.IngressTokenCiphertext = append([]byte(nil), ciphertext...)
	f.configuration.Trigger.IngressTokenNonce = append([]byte(nil), nonce...)
	return f.error
}

type fakeCipher struct {
	plaintext           []byte
	projectID, secretID string
	kind                domain.SecretKind
	token               []byte
	triggerID           string
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
func (f *fakeCipher) EncryptWebhookToken(projectID, triggerID string, plaintext []byte) ([]byte, []byte, error) {
	f.projectID, f.triggerID = projectID, triggerID
	f.token = append([]byte(nil), plaintext...)
	return []byte("encrypted-webhook-token"), []byte("webhooknonce"), nil
}
func (f *fakeCipher) DecryptWebhookToken(projectID, triggerID string, _, _ []byte) ([]byte, error) {
	f.projectID, f.triggerID = projectID, triggerID
	return append([]byte(nil), f.token...), nil
}

func TestAuthenticatedUserCreatesAndListsProjects(t *testing.T) {
	repository := &fakeRepository{project: domain.Project{ID: projectID, Key: "payments"}}
	service := newService(t, repository, &fakeCipher{})
	user := authdomain.User{ID: userID, Enabled: true}
	created, err := service.CreateProject(context.Background(), user, " Payments-API ", "Payments API", "Production service")
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	if created.Key != "payments-api" {
		t.Fatalf("created = %#v", created)
	}
	listed, err := service.ListProjects(context.Background(), user, 10)
	if err != nil || len(listed.Items) != 1 || listed.Items[0].ID != projectID || listed.Total != 1 {
		t.Fatalf("ListProjects() = %#v, error = %v", listed, err)
	}
}

func TestUpdateProjectNamePreservesKey(t *testing.T) {
	repository := &fakeRepository{project: domain.Project{ID: projectID, Key: "payments", Name: "Payments", Version: 1}}
	service := newService(t, repository, &fakeCipher{})
	principal := authdomain.User{ID: userID, Enabled: true}
	updated, err := service.UpdateProjectName(context.Background(), principal, "payments", " Payments Platform ")
	if err != nil || updated.Key != "payments" || updated.Name != "Payments Platform" || repository.created.Key != "payments" {
		t.Fatalf("UpdateProjectName() = %#v, repository = %#v, error = %v", updated, repository, err)
	}
	if _, err := service.UpdateProjectName(context.Background(), principal, "payments", ""); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty UpdateProjectName() error = %v", err)
	}
}

func TestUpdateSecretNameOnlyPreservesEncryptedMaterial(t *testing.T) {
	secretID := "019ff544-405c-7d24-9f10-cb3fc579605c"
	repository := &fakeRepository{
		project: domain.Project{ID: projectID, Key: "payments"},
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
		project: domain.Project{ID: projectID, Key: "payments"},
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
}

func TestCreateSecretEncryptsBeforePersistence(t *testing.T) {
	repository := &fakeRepository{project: domain.Project{ID: projectID, Key: "payments"}}
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

func TestConfigurationWriteAssignsOwnedIDs(t *testing.T) {
	repository := &fakeRepository{project: domain.Project{ID: projectID, Key: "payments"}}
	service := newService(t, repository, &fakeCipher{})
	configuration := validConfiguration()
	result, err := service.PutConfiguration(context.Background(), authdomain.User{ID: userID, Enabled: true}, "payments", configuration)
	if err != nil {
		t.Fatalf("PutConfiguration() error = %v", err)
	}
	if repository.projectID != projectID || result.Environment.ID == "" || result.Repository.ID == "" || result.Source.ID == "" || result.Trigger.ID == "" || result.LLM == nil || result.LLM.ID == "" {
		t.Fatalf("configuration = %#v", result)
	}
}

func TestPutConfigurationRemediationPolicy(t *testing.T) {
	repository := &fakeRepository{project: domain.Project{ID: projectID, Key: "payments"}}
	service := newService(t, repository, &fakeCipher{})
	saved, err := service.PutConfigurationRemediationPolicy(context.Background(), authdomain.User{ID: userID, Enabled: true}, "payments", domain.RemediationPolicy{AgentLoopMode: domain.AgentLoopModeResilientV1})
	if err != nil {
		t.Fatalf("PutConfigurationRemediationPolicy() error = %v", err)
	}
	if saved.AgentLoopMode != domain.AgentLoopModeResilientV1 || saved.Version != 1 || repository.projectID != projectID {
		t.Fatalf("saved policy = %+v project=%q", saved, repository.projectID)
	}
}

func TestPutConfigurationTriggerSavesWithoutLLM(t *testing.T) {
	repository := &fakeRepository{project: domain.Project{ID: projectID, Key: "payments", Name: "Payments"}}
	cipher := &fakeCipher{}
	service := newService(t, repository, cipher)
	service.newWebhookToken = func() (string, error) { return "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ", nil }
	trigger := validConfiguration().Trigger
	trigger.SigningSecretID = nil

	saved, err := service.PutConfigurationTrigger(context.Background(), authdomain.User{ID: userID, Enabled: true}, "payments", trigger)
	if err != nil {
		t.Fatalf("PutConfigurationTrigger() error = %v", err)
	}
	if saved.Kind != "signed_webhook" || saved.InboundURL == "" || repository.configuration.LLM != nil {
		t.Fatalf("saved trigger = %#v configuration = %#v", saved, repository.configuration)
	}
}

func newService(t *testing.T, repository Repository, cipher Cipher) *Service {
	t.Helper()
	ids := []string{"019ff544-405c-7d31-9f10-cb3fc579605c", "019ff544-405c-7d32-9f10-cb3fc579605c", "019ff544-405c-7d33-9f10-cb3fc579605c", "019ff544-405c-7d34-9f10-cb3fc579605c", "019ff544-405c-7d35-9f10-cb3fc579605c", "019ff544-405c-7d36-9f10-cb3fc579605c"}
	index := 0
	service, err := NewService(Options{
		Repository: repository, Cipher: cipher, PublicURL: "http://127.0.0.1:8080",
		NewID: func() (string, error) { id := ids[index%len(ids)]; index++; return id, nil },
	})
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
		Source: domain.Source{Kind: "mcp", Config: json.RawMessage(`{"schemaVersion":1,"endpoint":"https://mcp.example.com","transport":"http","headers":{},"evidenceProfile":"default","queryScope":"logs"}`),
			Capabilities: []string{"pull_collection"}, Enabled: true},
		Trigger: domain.Trigger{Kind: "signed_webhook", SigningSecretID: &secretID,
			Config: json.RawMessage(`{"schemaVersion":1,"eventTypes":["error"],"deduplicationKey":"fingerprint"}`), Enabled: true},
		LLM: &domain.LLMProvider{Provider: "openai", BaseURL: "https://api.openai.com", CredentialSecretID: secretID, Model: "gpt-5.6"},
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
		project: domain.Project{ID: projectID, Key: "payments"},
		secret:  domain.EncryptedSecret{Secret: domain.Secret{ID: secretID, ProjectID: projectID, Name: "git-http", Kind: domain.SecretGitCredential}, Ciphertext: []byte("cipher"), Nonce: []byte("nonce12bytes")},
	}
	cipher := &fakeCipher{plaintext: []byte("deploy:token")}
	git := &fakeGit{output: "ref: refs/heads/main\tHEAD\n0123456789abcdef0123456789abcdef01234567\trefs/heads/main\n"}
	service, err := NewService(Options{Repository: repository, Cipher: cipher, Git: git, PublicURL: "http://127.0.0.1:8080", NewID: func() (string, error) { return secretID, nil }})
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
	service := newService(t, &fakeRepository{project: domain.Project{ID: projectID, Key: "payments"}}, &fakeCipher{})
	service.git = git
	if _, err := service.ProbeRepositoryRefs(context.Background(), authdomain.User{ID: userID, Enabled: true}, "payments", "https://git.example.internal/app.git", "ssh", "019ff544-405c-7d24-9f10-cb3fc579605c"); err != ErrInvalidInput {
		t.Fatalf("error = %v", err)
	}
	if git.remoteURL != "" {
		t.Fatal("git lister was called")
	}
}

func TestPutConfigurationGeneratesWebhookTokenOnFirstSave(t *testing.T) {
	repository := &fakeRepository{project: domain.Project{ID: projectID, Key: "payments"}}
	cipher := &fakeCipher{}
	service := newService(t, repository, cipher)
	service.newWebhookToken = func() (string, error) { return "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ", nil }
	result, err := service.PutConfiguration(context.Background(), authdomain.User{ID: userID, Enabled: true}, "payments", validConfiguration())
	if err != nil {
		t.Fatalf("PutConfiguration() error = %v", err)
	}
	if len(repository.configuration.Trigger.IngressTokenHash) != 32 || string(cipher.token) != "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ" {
		t.Fatalf("token material = %#v cipher=%#v", repository.configuration.Trigger, cipher)
	}
	if result.Trigger.InboundURL != "http://127.0.0.1:8080/hooks/abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ" {
		t.Fatalf("inbound URL = %q", result.Trigger.InboundURL)
	}
}

func TestPutConfigurationKeepsExistingWebhookToken(t *testing.T) {
	existing := validConfiguration()
	existing.Trigger.ID = "019ff544-405c-7d34-9f10-cb3fc579605c"
	existing.Trigger.IngressTokenHash = []byte("existing-hash-32-bytes-aaaaaaaa")
	existing.Trigger.IngressTokenCiphertext = []byte("existing-cipher")
	existing.Trigger.IngressTokenNonce = []byte("existingnonce")
	repository := &fakeRepository{
		project:       domain.Project{ID: projectID, Key: "payments"},
		configuration: existing,
	}
	cipher := &fakeCipher{token: []byte("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ")}
	service := newService(t, repository, cipher)
	service.newWebhookToken = func() (string, error) { return "SHOULD-NOT-GENERATE-TOKEN-VALUE-AAAAAAAAAAA", nil }
	result, err := service.PutConfiguration(context.Background(), authdomain.User{ID: userID, Enabled: true}, "payments", validConfiguration())
	if err != nil {
		t.Fatalf("PutConfiguration() error = %v", err)
	}
	if string(repository.configuration.Trigger.IngressTokenHash) != "existing-hash-32-bytes-aaaaaaaa" || result.Trigger.ID != existing.Trigger.ID {
		t.Fatalf("token rotated on save: %#v", repository.configuration.Trigger)
	}
}

func TestGetConfigurationRevealsInboundURL(t *testing.T) {
	configuration := validConfiguration()
	configuration.Trigger.ID = "019ff544-405c-7d34-9f10-cb3fc579605c"
	configuration.Trigger.IngressTokenHash = []byte("existing-hash-32-bytes-aaaaaaaa")
	configuration.Trigger.IngressTokenCiphertext = []byte("existing-cipher")
	configuration.Trigger.IngressTokenNonce = []byte("existingnonce")
	repository := &fakeRepository{
		project:       domain.Project{ID: projectID, Key: "payments"},
		configuration: configuration,
	}
	cipher := &fakeCipher{token: []byte("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ")}
	service := newService(t, repository, cipher)
	configuration, err := service.GetConfiguration(context.Background(), authdomain.User{ID: userID, Enabled: true}, "payments")
	if err != nil || configuration.Trigger.InboundURL != "http://127.0.0.1:8080/hooks/abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ" {
		t.Fatalf("GetConfiguration() = %#v, %v", configuration.Trigger, err)
	}
}

func TestRotateWebhookTokenRejectsNonSignedWebhook(t *testing.T) {
	configuration := validConfiguration()
	configuration.Trigger.ID = "019ff544-405c-7d34-9f10-cb3fc579605c"
	configuration.Trigger.Kind = "custom_rule"
	configuration.Trigger.Config = json.RawMessage(`{"schemaVersion":1,"groupingWindowSeconds":60,"matchExpression":"level=ERROR"}`)
	repository := &fakeRepository{
		project:       domain.Project{ID: projectID, Key: "payments"},
		configuration: configuration,
	}
	service := newService(t, repository, &fakeCipher{})
	if _, err := service.RotateWebhookToken(context.Background(), authdomain.User{ID: userID, Enabled: true}, "payments"); err != ErrInvalidInput {
		t.Fatalf("error = %v", err)
	}
	if repository.rotated {
		t.Fatal("token rotated for custom_rule")
	}
}

func TestRotateWebhookTokenReplacesHash(t *testing.T) {
	configuration := validConfiguration()
	configuration.Trigger.ID = "019ff544-405c-7d34-9f10-cb3fc579605c"
	configuration.Trigger.IngressTokenHash = []byte("existing-hash-32-bytes-aaaaaaaa")
	configuration.Trigger.IngressTokenCiphertext = []byte("old-cipher")
	configuration.Trigger.IngressTokenNonce = []byte("old-nonce-12")
	repository := &fakeRepository{
		project:       domain.Project{ID: projectID, Key: "payments"},
		configuration: configuration,
	}
	cipher := &fakeCipher{}
	service := newService(t, repository, cipher)
	service.newWebhookToken = func() (string, error) { return "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ", nil }
	url, err := service.RotateWebhookToken(context.Background(), authdomain.User{ID: userID, Enabled: true}, "payments")
	if err != nil || url != "http://127.0.0.1:8080/hooks/abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ" || !repository.rotated {
		t.Fatalf("RotateWebhookToken() = %q rotated=%t err=%v", url, repository.rotated, err)
	}
	if len(repository.configuration.Trigger.IngressTokenHash) != 32 || string(repository.configuration.Trigger.IngressTokenCiphertext) == "old-cipher" {
		t.Fatalf("token columns = %#v", repository.configuration.Trigger)
	}
}

func TestPutConfigurationClearsWebhookTokenWhenKindChanges(t *testing.T) {
	existing := validConfiguration()
	existing.Trigger.ID = "019ff544-405c-7d34-9f10-cb3fc579605c"
	existing.Trigger.IngressTokenHash = []byte("existing-hash-32-bytes-aaaaaaaa")
	existing.Trigger.IngressTokenCiphertext = []byte("existing-cipher")
	existing.Trigger.IngressTokenNonce = []byte("existingnonce")
	repository := &fakeRepository{
		project:       domain.Project{ID: projectID, Key: "payments"},
		configuration: existing,
	}
	service := newService(t, repository, &fakeCipher{})
	next := validConfiguration()
	next.Trigger.Kind = "custom_rule"
	next.Trigger.Config = json.RawMessage(`{"schemaVersion":1,"groupingWindowSeconds":60,"matchExpression":"level=ERROR"}`)
	result, err := service.PutConfiguration(context.Background(), authdomain.User{ID: userID, Enabled: true}, "payments", next)
	if err != nil {
		t.Fatalf("PutConfiguration() error = %v", err)
	}
	if len(repository.configuration.Trigger.IngressTokenHash) != 0 || result.Trigger.InboundURL != "" {
		t.Fatalf("token columns survived kind change: %#v", repository.configuration.Trigger)
	}
}

func TestLookupWebhookTokenHashesBeforeRepository(t *testing.T) {
	repository := &fakeRepository{ingress: WebhookIngress{ProjectID: projectID, SourceID: "019ff544-405c-7d23-9f10-cb3fc579605c"}}
	service := newService(t, repository, &fakeCipher{})
	ingress, err := service.LookupWebhookToken(context.Background(), "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ")
	if err != nil || ingress.ProjectID != projectID || len(repository.lookupHash) != 32 {
		t.Fatalf("LookupWebhookToken() = %#v hash=%x err=%v", ingress, repository.lookupHash, err)
	}
	if _, err := service.LookupWebhookToken(context.Background(), "short"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("malformed token error = %v", err)
	}
}
