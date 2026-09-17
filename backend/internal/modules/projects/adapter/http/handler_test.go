package http_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	authhttp "mendry/backend/internal/modules/auth/adapter/http"
	authapplication "mendry/backend/internal/modules/auth/application"
	authdomain "mendry/backend/internal/modules/auth/domain"
	projecthttp "mendry/backend/internal/modules/projects/adapter/http"
	projectapplication "mendry/backend/internal/modules/projects/application"
	"mendry/backend/internal/modules/projects/domain"
	"mendry/backend/internal/platform/httpserver"
)

type fakeService struct {
	project       domain.Project
	secret        domain.Secret
	projectKey    string
	secretID      string
	secretName    string
	secretValue   []byte
	inboundURL    string
	configuration domain.Configuration
	updateErr     error
}

func (f *fakeService) CreateProject(context.Context, authdomain.User, string, string, string) (domain.Project, error) {
	return f.project, nil
}
func (f *fakeService) ListProjects(context.Context, authdomain.User, int32) (projectapplication.ListResult[domain.Project], error) {
	return projectapplication.ListResult[domain.Project]{Items: []domain.Project{f.project}, Total: 1}, nil
}
func (f *fakeService) GetProject(context.Context, authdomain.User, string) (domain.Project, error) {
	return f.project, nil
}
func (f *fakeService) UpdateProjectName(_ context.Context, _ authdomain.User, projectKey, name string) (domain.Project, error) {
	if f.updateErr != nil {
		return domain.Project{}, f.updateErr
	}
	f.projectKey = projectKey
	updated := f.project
	updated.Name = name
	updated.Version++
	f.project = updated
	return updated, nil
}
func (f *fakeService) CreateSecret(_ context.Context, _ authdomain.User, projectKey, _, _ string, value []byte) (domain.Secret, error) {
	f.projectKey, f.secretValue = projectKey, append([]byte(nil), value...)
	return f.secret, nil
}
func (f *fakeService) UpdateSecret(_ context.Context, _ authdomain.User, projectKey, secretID, name string, value []byte) (domain.Secret, error) {
	if f.updateErr != nil {
		return domain.Secret{}, f.updateErr
	}
	f.projectKey, f.secretID, f.secretName = projectKey, secretID, name
	if value != nil {
		f.secretValue = append([]byte(nil), value...)
	} else {
		f.secretValue = nil
	}
	updated := f.secret
	updated.Name = name
	updated.Version++
	f.secret = updated
	return updated, nil
}
func (f *fakeService) ListSecrets(context.Context, authdomain.User, string) (projectapplication.ListResult[domain.Secret], error) {
	return projectapplication.ListResult[domain.Secret]{Items: []domain.Secret{f.secret}, Total: 1}, nil
}
func (f *fakeService) GetConfiguration(context.Context, authdomain.User, string) (domain.Configuration, error) {
	return domain.Configuration{Trigger: domain.Trigger{Kind: "signed_webhook", InboundURL: f.inboundURL}}, nil
}
func (f *fakeService) PutConfiguration(_ context.Context, _ authdomain.User, projectKey string, configuration domain.Configuration) (domain.Configuration, error) {
	f.projectKey = projectKey
	f.configuration = configuration
	configuration.Environment.ID = "env-id"
	configuration.Repository.ID = "repo-id"
	configuration.Source.ID = "source-id"
	configuration.Trigger.ID = "trigger-id"
	configuration.Environment.Version = 1
	configuration.Repository.Version = 1
	configuration.Source.Version = 1
	configuration.Trigger.Version = 1
	f.configuration = configuration
	return configuration, nil
}

func (f *fakeService) GetConfigurationDraft(context.Context, authdomain.User, string) (domain.ConfigurationDraft, error) {
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
	return draft, nil
}

func (f *fakeService) PutConfigurationEnvironment(_ context.Context, _ authdomain.User, projectKey string, environment domain.Environment) (domain.Environment, error) {
	f.projectKey = projectKey
	environment.ID = "env-id"
	environment.Version = 1
	f.configuration.Environment = environment
	return environment, f.updateErr
}

func (f *fakeService) PutConfigurationRepository(_ context.Context, _ authdomain.User, projectKey string, repository domain.Repository) (domain.Repository, error) {
	f.projectKey = projectKey
	repository.ID = "repo-id"
	repository.Version = 1
	f.configuration.Repository = repository
	return repository, f.updateErr
}

func (f *fakeService) PutConfigurationSource(_ context.Context, _ authdomain.User, projectKey string, source domain.Source) (domain.Source, error) {
	f.projectKey = projectKey
	source.ID = "source-id"
	source.Version = 1
	f.configuration.Source = source
	return source, f.updateErr
}

func (f *fakeService) PutConfigurationTrigger(_ context.Context, _ authdomain.User, projectKey string, trigger domain.Trigger) (domain.Trigger, error) {
	f.projectKey = projectKey
	trigger.ID = "trigger-id"
	trigger.Version = 1
	trigger.InboundURL = f.inboundURL
	f.configuration.Trigger = trigger
	return trigger, f.updateErr
}

func (f *fakeService) PutConfigurationLLMProvider(_ context.Context, _ authdomain.User, projectKey string, provider domain.LLMProvider) (domain.LLMProvider, error) {
	f.projectKey = projectKey
	provider.ID = "llm-id"
	provider.Version = 1
	f.configuration.LLM = &provider
	return provider, f.updateErr
}

func (f *fakeService) PutConfigurationRemediationPolicy(_ context.Context, _ authdomain.User, projectKey string, policy domain.RemediationPolicy) (domain.RemediationPolicy, error) {
	f.projectKey = projectKey
	policy.Version++
	f.configuration.Remediation = policy
	return policy, nil
}
func (f *fakeService) RotateWebhookToken(_ context.Context, _ authdomain.User, projectKey string) (string, error) {
	if f.updateErr != nil {
		return "", f.updateErr
	}
	f.projectKey = projectKey
	return f.inboundURL, nil
}
func (*fakeService) ProbeRepositoryRefs(context.Context, authdomain.User, string, string, string, string) (projectapplication.RepositoryRefs, error) {
	return projectapplication.RepositoryRefs{DefaultBranch: "main", DeployedCommit: "0123456789abcdef0123456789abcdef01234567", Branches: []projectapplication.GitRef{{Name: "main", Commit: "0123456789abcdef0123456789abcdef01234567"}}}, nil
}
func (*fakeService) ProbeSSHContainers(context.Context, authdomain.User, string, string, int, string, string) ([]domain.DockerContainer, error) {
	return []domain.DockerContainer{{Name: "app", ID: "0123456789abcdef", Image: "example/app:latest", State: "running", Status: "Up 1 minute"}}, nil
}
func (*fakeService) ProbeLLMModels(context.Context, authdomain.User, string, string, string) (projectapplication.LLMModels, error) {
	return projectapplication.LLMModels{Models: []string{"gpt-4.1", "gpt-5.6"}}, nil
}
func (*fakeService) ProbeLLMChat(context.Context, authdomain.User, string, string, string, string) error {
	return nil
}

type fakeAuthService struct{ user authdomain.User }

func (*fakeAuthService) Login(context.Context, string, []byte, string) (authapplication.LoginResult, error) {
	return authapplication.LoginResult{}, nil
}
func (f *fakeAuthService) Authenticate(context.Context, string) (authdomain.User, error) {
	if !f.user.Enabled {
		return authdomain.User{}, authapplication.ErrUnauthenticated
	}
	return f.user, nil
}
func (*fakeAuthService) Logout(context.Context, string) error { return nil }

func TestProjectResponseOmitsAuthorizationMetadata(t *testing.T) {
	now := time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC)
	service := &fakeService{project: domain.Project{ID: "project", Key: "payments", Name: "Payments", Version: 1, CreatedAt: now, UpdatedAt: now}}
	handler := newHandler(t, service)
	request := httptest.NewRequest(nethttp.MethodGet, "/api/v1/projects/payments", nil)
	request.AddCookie(sessionCookie())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusOK || !strings.Contains(response.Body.String(), `"key":"payments"`) || strings.Contains(response.Body.String(), `"role"`) || strings.Contains(response.Body.String(), `"capabilities"`) {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
}

func TestSecretCreateAndListNeverDiscloseSensitiveMaterial(t *testing.T) {
	now := time.Now().UTC()
	service := &fakeService{secret: domain.Secret{ID: "secret-id", Name: "git-token", Kind: domain.SecretGitCredential, KeyVersion: 1, Version: 1, CreatedAt: now, UpdatedAt: now}}
	handler := newHandler(t, service)
	const plaintext = "unique-plaintext-secret"
	request := httptest.NewRequest(nethttp.MethodPost, "/api/v1/projects/payments/secrets", strings.NewReader(`{"name":"git-token","kind":"git_credential","value":"`+plaintext+`"}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(sessionCookie())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusCreated || service.projectKey != "payments" || string(service.secretValue) != plaintext {
		t.Fatalf("create response = %d %q, input = %q", response.Code, response.Body.String(), service.secretValue)
	}
	for _, forbidden := range []string{plaintext, "ciphertext", "nonce", `"value"`} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("create response leaked %q: %s", forbidden, response.Body.String())
		}
	}

	request = httptest.NewRequest(nethttp.MethodGet, "/api/v1/projects/payments/secrets", nil)
	request.AddCookie(sessionCookie())
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusOK {
		t.Fatalf("list response = %d %q", response.Code, response.Body.String())
	}
	var body struct {
		Data []map[string]any `json:"data"`
		Meta struct {
			Total int64 `json:"total"`
		} `json:"meta"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil || len(body.Data) != 1 || body.Meta.Total != 1 {
		t.Fatalf("body = %#v, error = %v", body, err)
	}
	for _, field := range []string{"value", "ciphertext", "nonce", "projectId"} {
		if _, ok := body.Data[0][field]; ok {
			t.Fatalf("list response leaked field %q: %#v", field, body.Data[0])
		}
	}
}

func TestUpdateProjectReturnsSafeDTOAndPreservesKey(t *testing.T) {
	now := time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC)
	service := &fakeService{project: domain.Project{ID: "project", Key: "payments", Name: "Payments", Version: 1, CreatedAt: now, UpdatedAt: now}}
	handler := newHandler(t, service)
	request := httptest.NewRequest(nethttp.MethodPatch, "/api/v1/projects/payments", strings.NewReader(`{"name":"Payments Platform"}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(sessionCookie())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusOK || service.projectKey != "payments" || service.project.Name != "Payments Platform" {
		t.Fatalf("response = %d %q, project = %#v", response.Code, response.Body.String(), service.project)
	}
	body := response.Body.String()
	if !strings.Contains(body, `"key":"payments"`) || !strings.Contains(body, `"name":"Payments Platform"`) || strings.Contains(body, `"capabilities"`) {
		t.Fatalf("body = %s", body)
	}
}

func TestUpdateProjectMapsForbiddenAndInvalidErrors(t *testing.T) {
	now := time.Now().UTC()
	service := &fakeService{project: domain.Project{ID: "project", Key: "payments", Name: "Payments", Version: 1, CreatedAt: now, UpdatedAt: now}, updateErr: projectapplication.ErrForbidden}
	handler := newHandler(t, service)
	request := httptest.NewRequest(nethttp.MethodPatch, "/api/v1/projects/payments", strings.NewReader(`{"name":"Payments Platform"}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(sessionCookie())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusForbidden || !strings.Contains(response.Body.String(), `"code":"forbidden"`) {
		t.Fatalf("forbidden response = %d %q", response.Code, response.Body.String())
	}
	service.updateErr = projectapplication.ErrInvalidInput
	request = httptest.NewRequest(nethttp.MethodPatch, "/api/v1/projects/payments", strings.NewReader(`{"name":""}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(sessionCookie())
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("invalid response = %d %q", response.Code, response.Body.String())
	}
}

func TestUpdateSecretNameOnlyOmitsValueAndNeverDisclosesMaterial(t *testing.T) {
	now := time.Now().UTC()
	service := &fakeService{secret: domain.Secret{ID: "secret-id", Name: "git-token", Kind: domain.SecretGitCredential, KeyVersion: 1, Version: 1, CreatedAt: now, UpdatedAt: now}}
	handler := newHandler(t, service)
	request := httptest.NewRequest(nethttp.MethodPatch, "/api/v1/projects/payments/secrets/secret-id", strings.NewReader(`{"name":"git-token-renamed"}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(sessionCookie())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusOK || service.secretID != "secret-id" || service.secretName != "git-token-renamed" || service.secretValue != nil {
		t.Fatalf("name-only update = %d %q service=%#v", response.Code, response.Body.String(), service)
	}
	body := response.Body.String()
	for _, forbidden := range []string{"ciphertext", "nonce", `"value"`, "projectId"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("update response leaked %q: %s", forbidden, body)
		}
	}
}

func TestUpdateSecretRotationClearsPlaintextAndRejectsEmptyValue(t *testing.T) {
	now := time.Now().UTC()
	service := &fakeService{secret: domain.Secret{ID: "secret-id", Name: "git-token", Kind: domain.SecretGitCredential, KeyVersion: 1, Version: 1, CreatedAt: now, UpdatedAt: now}}
	handler := newHandler(t, service)
	const plaintext = "unique-replacement-secret"
	request := httptest.NewRequest(nethttp.MethodPatch, "/api/v1/projects/payments/secrets/secret-id", strings.NewReader(`{"name":"git-token","value":"`+plaintext+`"}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(sessionCookie())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusOK || string(service.secretValue) != plaintext {
		t.Fatalf("rotation response = %d %q value=%q", response.Code, response.Body.String(), service.secretValue)
	}
	if strings.Contains(response.Body.String(), plaintext) || strings.Contains(response.Body.String(), `"value"`) {
		t.Fatalf("rotation response leaked secret material: %s", response.Body.String())
	}

	request = httptest.NewRequest(nethttp.MethodPatch, "/api/v1/projects/payments/secrets/secret-id", strings.NewReader(`{"name":"git-token","value":""}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(sessionCookie())
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("empty value response = %d %q", response.Code, response.Body.String())
	}

	service.updateErr = projectapplication.ErrConflict
	request = httptest.NewRequest(nethttp.MethodPatch, "/api/v1/projects/payments/secrets/secret-id", strings.NewReader(`{"name":"duplicate"}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(sessionCookie())
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusConflict || !strings.Contains(response.Body.String(), `"code":"project_conflict"`) {
		t.Fatalf("conflict response = %d %q", response.Code, response.Body.String())
	}
}

func TestProbeRepositoryRefsReturnsBranchesWithoutSecretMaterial(t *testing.T) {
	handler := newHandler(t, &fakeService{})
	request := httptest.NewRequest(nethttp.MethodPost, "/api/v1/projects/payments/repository/refs", strings.NewReader(`{"remoteUrl":"https://git.example.internal/app.git","transport":"https","credentialSecretId":"019ff544-405c-7d24-9f10-cb3fc579605c"}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(sessionCookie())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusOK {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, `"defaultBranch":"main"`) || !strings.Contains(body, `"deployedCommit":"0123456789abcdef0123456789abcdef01234567"`) {
		t.Fatalf("body = %s", body)
	}
	for _, forbidden := range []string{"value", "ciphertext", "nonce", "deploy:token"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("probe response leaked %q: %s", forbidden, body)
		}
	}
}

func TestConfigurationPUTOmitsSourceAndTriggerAliases(t *testing.T) {
	service := &fakeService{}
	handler := newHandler(t, service)
	payload := `{"environment":{"key":"production","name":"Production","service":"checkout"},"repository":{"remoteUrl":"https://git.example.internal/checkout.git","scmProvider":"github","transport":"https","credentialSecretId":null,"productionBranch":"main","deployedCommit":"0123456789abcdef0123456789abcdef01234567"},"source":{"kind":"cloud","credentialSecretId":null,"config":{"schemaVersion":1,"provider":"tencent-cls","region":"ap-shanghai","resource":"checkout-logset"},"capabilities":["pull_collection"],"enabled":true},"trigger":{"kind":"custom_rule","signingSecretId":null,"config":{"schemaVersion":1,"groupingWindowSeconds":900,"matchExpression":"level=ERROR"},"enabled":true},"llm":null}`
	request := httptest.NewRequest(nethttp.MethodPut, "/api/v1/projects/payments/configuration", strings.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(sessionCookie())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusOK || service.projectKey != "payments" || service.configuration.Source.Kind != "cloud" || service.configuration.Trigger.Kind != "custom_rule" {
		t.Fatalf("configuration PUT = %d %q service=%#v", response.Code, response.Body.String(), service)
	}
	body := response.Body.String()
	if strings.Contains(body, `"source":{"id":"source-id","name"`) || strings.Contains(body, `"trigger":{"id":"trigger-id","name"`) {
		t.Fatalf("configuration response exposed source/trigger name: %s", body)
	}
}

func TestConfigurationPUTRejectsStaleSourceAndTriggerAliases(t *testing.T) {
	handler := newHandler(t, &fakeService{})
	payload := `{"environment":{"key":"production","name":"Production","service":null},"repository":{"remoteUrl":"https://git.example.internal/checkout.git","scmProvider":"github","transport":"https","credentialSecretId":null,"productionBranch":"main","deployedCommit":"0123456789abcdef0123456789abcdef01234567"},"source":{"name":"legacy-source-alias","kind":"cloud","credentialSecretId":null,"config":{"schemaVersion":1,"provider":"tencent-cls","region":"ap-shanghai","resource":"checkout-logset"},"capabilities":["pull_collection"],"enabled":true},"trigger":{"name":"legacy-trigger-alias","kind":"custom_rule","signingSecretId":null,"config":{"schemaVersion":1,"groupingWindowSeconds":900,"matchExpression":"level=ERROR"},"enabled":true},"llm":null}`
	request := httptest.NewRequest(nethttp.MethodPut, "/api/v1/projects/payments/configuration", strings.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(sessionCookie())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("stale name response = %d %q", response.Code, response.Body.String())
	}
}

func TestConfigurationTriggerPUTIsComponentScoped(t *testing.T) {
	service := &fakeService{inboundURL: "http://127.0.0.1:8080/hooks/abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ"}
	handler := newHandler(t, service)
	payload := `{"kind":"signed_webhook","signingSecretId":null,"config":{"schemaVersion":1,"eventTypes":["alarm"],"deduplicationKey":"title"},"enabled":true}`
	request := httptest.NewRequest(nethttp.MethodPut, "/api/v1/projects/payments/configuration/trigger", strings.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(sessionCookie())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusOK || service.projectKey != "payments" || service.configuration.Trigger.Kind != "signed_webhook" {
		t.Fatalf("trigger PUT = %d %q service=%#v", response.Code, response.Body.String(), service)
	}
	if !strings.Contains(response.Body.String(), `"inboundUrl":"http://127.0.0.1:8080/hooks/abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ"`) || strings.Contains(response.Body.String(), `"llm"`) {
		t.Fatalf("trigger response = %s", response.Body.String())
	}
	if strings.Contains(string(service.configuration.Trigger.Config), "name") {
		t.Fatalf("trigger payload retained alias: %s", service.configuration.Trigger.Config)
	}
}

func TestConfigurationRemediationPolicyPUTIsComponentScoped(t *testing.T) {
	service := &fakeService{}
	handler := newHandler(t, service)
	request := httptest.NewRequest(nethttp.MethodPut, "/api/v1/projects/payments/configuration/remediation-policy", strings.NewReader(`{"agentLoopMode":"resilient_v1"}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(sessionCookie())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusOK || service.projectKey != "payments" {
		t.Fatalf("policy PUT = %d %q project=%q", response.Code, response.Body.String(), service.projectKey)
	}
	if !strings.Contains(response.Body.String(), `"agentLoopMode":"resilient_v1"`) ||
		!strings.Contains(response.Body.String(), `"executionMode":"analysis_only"`) ||
		!strings.Contains(response.Body.String(), `"branchPrefix":"hotfix/remediation"`) ||
		!strings.Contains(response.Body.String(), `"maxChangedFiles":10`) {
		t.Fatalf("policy response = %s", response.Body.String())
	}
}

func TestConfigurationRemediationPolicyPUTAcceptsVersion(t *testing.T) {
	service := &fakeService{}
	handler := newHandler(t, service)
	request := httptest.NewRequest(nethttp.MethodPut, "/api/v1/projects/payments/configuration/remediation-policy", strings.NewReader(`{"agentLoopMode":"resilient_v1","version":1}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(sessionCookie())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusOK || service.projectKey != "payments" {
		t.Fatalf("policy PUT with version = %d %q project=%q", response.Code, response.Body.String(), service.projectKey)
	}
}

func TestConfigurationRemediationPolicyPUTWithCustomLimits(t *testing.T) {
	service := &fakeService{}
	handler := newHandler(t, service)
	payload := `{"agentLoopMode":"resilient_v1","executionMode":"auto_hotfix","validationProfile":{"enabled":false},"publication":{"branchPrefix":"hotfix/remediation","gitCredentialSecretId":"cred-1","apiCredentialSecretId":"","apiBaseUrl":"https://git.example.com/api/v4"},"changePolicy":{"allowedPaths":["**"],"deniedPaths":[],"maxChangedFiles":20,"maxChangedLines":4000}}`
	request := httptest.NewRequest(nethttp.MethodPut, "/api/v1/projects/payments/configuration/remediation-policy", strings.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(sessionCookie())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusOK || service.projectKey != "payments" {
		t.Fatalf("policy PUT custom limits = %d %q project=%q", response.Code, response.Body.String(), service.projectKey)
	}
	if service.configuration.Remediation.ChangePolicy.MaxChangedFiles != 20 || service.configuration.Remediation.ChangePolicy.MaxChangedLines != 4000 {
		t.Fatalf("service policy limits = %+v", service.configuration.Remediation.ChangePolicy)
	}
}

func newHandler(t *testing.T, service *fakeService) nethttp.Handler {
	t.Helper()
	authHandler, err := authhttp.NewHandler(authhttp.HandlerOptions{Service: &fakeAuthService{user: authdomain.User{ID: "user", Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := projecthttp.NewHandler(projecthttp.HandlerOptions{Service: service, Authentication: authHandler})
	if err != nil {
		t.Fatal(err)
	}
	mux := nethttp.NewServeMux()
	handler.Register(mux)
	boundary, err := httpserver.Boundary(httpserver.BoundaryOptions{Handler: mux, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), MaxBodyBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	return boundary
}

func sessionCookie() *nethttp.Cookie {
	return &nethttp.Cookie{Name: authhttp.SessionCookieName, Value: "valid"}
}

func TestRotateWebhookTokenReturnsInboundURL(t *testing.T) {
	service := &fakeService{inboundURL: "http://127.0.0.1:8080/hooks/abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ"}
	handler := newHandler(t, service)
	request := httptest.NewRequest(nethttp.MethodPost, "/api/v1/projects/payments/configuration/webhook-token", strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(sessionCookie())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusOK || service.projectKey != "payments" {
		t.Fatalf("response = %d %q project=%q", response.Code, response.Body.String(), service.projectKey)
	}
	if !strings.Contains(response.Body.String(), `"inboundUrl":"http://127.0.0.1:8080/hooks/abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ"`) {
		t.Fatalf("body = %s", response.Body.String())
	}
	if strings.Contains(response.Body.String(), `"token"`) {
		t.Fatalf("response leaked token field: %s", response.Body.String())
	}
}

func TestRotateWebhookTokenMapsForbidden(t *testing.T) {
	service := &fakeService{updateErr: projectapplication.ErrForbidden}
	handler := newHandler(t, service)
	request := httptest.NewRequest(nethttp.MethodPost, "/api/v1/projects/payments/configuration/webhook-token", strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(sessionCookie())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusForbidden || !strings.Contains(response.Body.String(), `"code":"forbidden"`) {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
}
