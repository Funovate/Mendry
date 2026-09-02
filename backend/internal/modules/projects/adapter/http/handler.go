package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	nethttp "net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	authhttp "fixthe/backend/internal/modules/auth/adapter/http"
	authdomain "fixthe/backend/internal/modules/auth/domain"
	"fixthe/backend/internal/modules/projects/application"
	"fixthe/backend/internal/modules/projects/domain"
	"fixthe/backend/internal/platform/httpserver"
)

type service interface {
	CreateProject(context.Context, authdomain.User, string, string, string) (domain.Project, error)
	ListProjects(context.Context, authdomain.User, int32) (application.ListResult[domain.Project], error)
	GetProject(context.Context, authdomain.User, string) (domain.Project, error)
	UpdateProjectName(context.Context, authdomain.User, string, string) (domain.Project, error)
	ListMembers(context.Context, authdomain.User, string) (application.ListResult[domain.Member], error)
	UpsertMember(context.Context, authdomain.User, string, string, string) (domain.Member, error)
	DeleteMember(context.Context, authdomain.User, string, string) (domain.Member, error)
	CreateSecret(context.Context, authdomain.User, string, string, string, []byte) (domain.Secret, error)
	UpdateSecret(context.Context, authdomain.User, string, string, string, []byte) (domain.Secret, error)
	ListSecrets(context.Context, authdomain.User, string) (application.ListResult[domain.Secret], error)
	GetConfiguration(context.Context, authdomain.User, string) (domain.Configuration, error)
	GetConfigurationDraft(context.Context, authdomain.User, string) (domain.ConfigurationDraft, error)
	PutConfiguration(context.Context, authdomain.User, string, domain.Configuration) (domain.Configuration, error)
	PutConfigurationEnvironment(context.Context, authdomain.User, string, domain.Environment) (domain.Environment, error)
	PutConfigurationRepository(context.Context, authdomain.User, string, domain.Repository) (domain.Repository, error)
	PutConfigurationSource(context.Context, authdomain.User, string, domain.Source) (domain.Source, error)
	PutConfigurationTrigger(context.Context, authdomain.User, string, domain.Trigger) (domain.Trigger, error)
	PutConfigurationLLMProvider(context.Context, authdomain.User, string, domain.LLMProvider) (domain.LLMProvider, error)
	PutConfigurationRemediationPolicy(context.Context, authdomain.User, string, domain.RemediationPolicy) (domain.RemediationPolicy, error)
	RotateWebhookToken(context.Context, authdomain.User, string) (string, error)
	ProbeRepositoryRefs(context.Context, authdomain.User, string, string, string, string) (application.RepositoryRefs, error)
	ProbeLLMModels(context.Context, authdomain.User, string, string, string) (application.LLMModels, error)
	ProbeLLMChat(context.Context, authdomain.User, string, string, string, string) error
	ListAuditEvents(context.Context, authdomain.User, string, int32) (application.ListResult[domain.AuditEvent], error)
}

type HandlerOptions struct {
	Service        service
	Authentication *authhttp.Handler
}

type Handler struct {
	service        service
	authentication *authhttp.Handler
}

func NewHandler(options HandlerOptions) (*Handler, error) {
	if options.Service == nil || options.Authentication == nil {
		return nil, fmt.Errorf("project HTTP dependencies are required")
	}
	return &Handler{service: options.Service, authentication: options.Authentication}, nil
}

func (h *Handler) Register(mux *nethttp.ServeMux) {
	mux.Handle("GET /api/v1/projects", h.authentication.RequireAuthentication(nethttp.HandlerFunc(h.listProjects)))
	mux.Handle("POST /api/v1/projects", h.authentication.RequireAuthentication(nethttp.HandlerFunc(h.createProject)))
	mux.Handle("GET /api/v1/projects/{projectKey}", h.authentication.RequireAuthentication(nethttp.HandlerFunc(h.getProject)))
	mux.Handle("PATCH /api/v1/projects/{projectKey}", h.authentication.RequireAuthentication(nethttp.HandlerFunc(h.updateProject)))
	mux.Handle("GET /api/v1/projects/{projectKey}/members", h.authentication.RequireAuthentication(nethttp.HandlerFunc(h.listMembers)))
	mux.Handle("PUT /api/v1/projects/{projectKey}/members/{username}", h.authentication.RequireAuthentication(nethttp.HandlerFunc(h.putMember)))
	mux.Handle("DELETE /api/v1/projects/{projectKey}/members/{username}", h.authentication.RequireAuthentication(nethttp.HandlerFunc(h.deleteMember)))
	mux.Handle("GET /api/v1/projects/{projectKey}/secrets", h.authentication.RequireAuthentication(nethttp.HandlerFunc(h.listSecrets)))
	mux.Handle("POST /api/v1/projects/{projectKey}/secrets", h.authentication.RequireAuthentication(nethttp.HandlerFunc(h.createSecret)))
	mux.Handle("PATCH /api/v1/projects/{projectKey}/secrets/{secretId}", h.authentication.RequireAuthentication(nethttp.HandlerFunc(h.updateSecret)))
	mux.Handle("GET /api/v1/projects/{projectKey}/configuration", h.authentication.RequireAuthentication(nethttp.HandlerFunc(h.getConfiguration)))
	mux.Handle("GET /api/v1/projects/{projectKey}/configuration/draft", h.authentication.RequireAuthentication(nethttp.HandlerFunc(h.getConfigurationDraft)))
	mux.Handle("PUT /api/v1/projects/{projectKey}/configuration", h.authentication.RequireAuthentication(nethttp.HandlerFunc(h.putConfiguration)))
	mux.Handle("PUT /api/v1/projects/{projectKey}/configuration/{component}", h.authentication.RequireAuthentication(nethttp.HandlerFunc(h.putConfigurationComponent)))
	mux.Handle("POST /api/v1/projects/{projectKey}/configuration/webhook-token", h.authentication.RequireAuthentication(nethttp.HandlerFunc(h.rotateWebhookToken)))
	mux.Handle("POST /api/v1/projects/{projectKey}/repository/refs", h.authentication.RequireAuthentication(nethttp.HandlerFunc(h.probeRepositoryRefs)))
	mux.Handle("POST /api/v1/projects/{projectKey}/llm/models", h.authentication.RequireAuthentication(nethttp.HandlerFunc(h.probeLLMModels)))
	mux.Handle("POST /api/v1/projects/{projectKey}/llm/chat", h.authentication.RequireAuthentication(nethttp.HandlerFunc(h.probeLLMChat)))
	mux.Handle("GET /api/v1/projects/{projectKey}/audit-events", h.authentication.RequireAuthentication(nethttp.HandlerFunc(h.listAuditEvents)))
}

type createProjectRequest struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type updateProjectRequest struct {
	Name string `json:"name"`
}

type memberRequest struct {
	Role string `json:"role"`
}

type secretRequest struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type updateSecretRequest struct {
	Name  string  `json:"name"`
	Value *string `json:"value"`
}

type repositoryRefsRequest struct {
	RemoteURL          string `json:"remoteUrl"`
	Transport          string `json:"transport"`
	CredentialSecretID string `json:"credentialSecretId"`
}

type configurationRequest struct {
	Environment environmentRequest `json:"environment"`
	Repository  repositoryRequest  `json:"repository"`
	Source      sourceRequest      `json:"source"`
	Trigger     triggerRequest     `json:"trigger"`
	LLM         *llmRequest        `json:"llm"`
}

type remediationPolicyRequest struct {
	AgentLoopMode domain.AgentLoopMode `json:"agentLoopMode"`
}

type llmRequest struct {
	Provider           string `json:"provider"`
	BaseURL            string `json:"baseUrl"`
	CredentialSecretID string `json:"credentialSecretId"`
	Model              string `json:"model"`
}

type llmModelsRequest struct {
	BaseURL            string `json:"baseUrl"`
	CredentialSecretID string `json:"credentialSecretId"`
}

type llmChatRequest struct {
	BaseURL            string `json:"baseUrl"`
	CredentialSecretID string `json:"credentialSecretId"`
	Model              string `json:"model"`
}

type environmentRequest struct {
	Key     string  `json:"key"`
	Name    string  `json:"name"`
	Service *string `json:"service"`
}

type repositoryRequest struct {
	RemoteURL          string  `json:"remoteUrl"`
	SCMProvider        string  `json:"scmProvider"`
	Transport          string  `json:"transport"`
	CredentialSecretID *string `json:"credentialSecretId"`
	ProductionBranch   string  `json:"productionBranch"`
	DeployedCommit     string  `json:"deployedCommit"`
}

type repositoryRefsResponse struct {
	DefaultBranch  string              `json:"defaultBranch"`
	DeployedCommit string              `json:"deployedCommit"`
	Branches       []gitBranchResponse `json:"branches"`
}

type gitBranchResponse struct {
	Name   string `json:"name"`
	Commit string `json:"commit"`
}

type sourceRequest struct {
	Kind               string          `json:"kind"`
	CredentialSecretID *string         `json:"credentialSecretId"`
	Config             json.RawMessage `json:"config"`
	Capabilities       []string        `json:"capabilities"`
	Enabled            bool            `json:"enabled"`
}

type triggerRequest struct {
	Kind            string          `json:"kind"`
	SigningSecretID *string         `json:"signingSecretId"`
	Config          json.RawMessage `json:"config"`
	Enabled         bool            `json:"enabled"`
}

type projectResponse struct {
	ID           string               `json:"id"`
	Key          string               `json:"key"`
	Name         string               `json:"name"`
	Description  string               `json:"description"`
	Role         domain.Role          `json:"role"`
	Capabilities capabilitiesResponse `json:"capabilities"`
	Version      int64                `json:"version"`
	CreatedAt    time.Time            `json:"createdAt"`
	UpdatedAt    time.Time            `json:"updatedAt"`
}

type capabilitiesResponse struct {
	Read           bool `json:"read"`
	WriteIncidents bool `json:"writeIncidents"`
	ManageMembers  bool `json:"manageMembers"`
	ManageConfig   bool `json:"manageConfiguration"`
}

type memberResponse struct {
	UserID    string      `json:"userId"`
	Username  string      `json:"username"`
	Role      domain.Role `json:"role"`
	Version   int64       `json:"version"`
	CreatedAt time.Time   `json:"createdAt"`
	UpdatedAt time.Time   `json:"updatedAt"`
}

type secretResponse struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Kind       domain.SecretKind `json:"kind"`
	KeyVersion int32             `json:"keyVersion"`
	Version    int64             `json:"version"`
	CreatedAt  time.Time         `json:"createdAt"`
	UpdatedAt  time.Time         `json:"updatedAt"`
}

type configurationResponse struct {
	Environment environmentResponse       `json:"environment"`
	Repository  repositoryResponse        `json:"repository"`
	Source      sourceResponse            `json:"source"`
	Trigger     triggerResponse           `json:"trigger"`
	LLM         *llmResponse              `json:"llm"`
	Remediation remediationPolicyResponse `json:"remediation"`
}

type configurationDraftResponse struct {
	Environment *environmentResponse       `json:"environment"`
	Repository  *repositoryResponse        `json:"repository"`
	Source      *sourceResponse            `json:"source"`
	Trigger     *triggerResponse           `json:"trigger"`
	LLM         *llmResponse               `json:"llm"`
	Remediation *remediationPolicyResponse `json:"remediation"`
}

type remediationPolicyResponse struct {
	AgentLoopMode domain.AgentLoopMode `json:"agentLoopMode"`
	Version       int64                `json:"version"`
}

type llmResponse struct {
	ID                 string `json:"id"`
	Provider           string `json:"provider"`
	BaseURL            string `json:"baseUrl"`
	CredentialSecretID string `json:"credentialSecretId"`
	Model              string `json:"model"`
	Version            int64  `json:"version"`
}

type llmModelsResponse struct {
	Models []string `json:"models"`
}

type environmentResponse struct {
	ID      string  `json:"id"`
	Key     string  `json:"key"`
	Name    string  `json:"name"`
	Service *string `json:"service"`
	Version int64   `json:"version"`
}

type repositoryResponse struct {
	ID                 string  `json:"id"`
	RemoteURL          string  `json:"remoteUrl"`
	SCMProvider        string  `json:"scmProvider"`
	Transport          string  `json:"transport"`
	CredentialSecretID *string `json:"credentialSecretId"`
	ProductionBranch   string  `json:"productionBranch"`
	DeployedCommit     string  `json:"deployedCommit"`
	Version            int64   `json:"version"`
}

type sourceResponse struct {
	ID                 string          `json:"id"`
	Kind               string          `json:"kind"`
	CredentialSecretID *string         `json:"credentialSecretId"`
	Config             json.RawMessage `json:"config"`
	Capabilities       []string        `json:"capabilities"`
	Enabled            bool            `json:"enabled"`
	Version            int64           `json:"version"`
}

type triggerResponse struct {
	ID              string          `json:"id"`
	Kind            string          `json:"kind"`
	SigningSecretID *string         `json:"signingSecretId"`
	InboundURL      *string         `json:"inboundUrl"`
	Config          json.RawMessage `json:"config"`
	Enabled         bool            `json:"enabled"`
	Version         int64           `json:"version"`
}

type webhookTokenResponse struct {
	InboundURL string `json:"inboundUrl"`
}

type auditEventResponse struct {
	ID          string          `json:"id"`
	ActorUserID *string         `json:"actorUserId"`
	Action      string          `json:"action"`
	TargetType  string          `json:"targetType"`
	TargetID    *string         `json:"targetId"`
	Summary     string          `json:"summary"`
	Metadata    json.RawMessage `json:"metadata"`
	OccurredAt  time.Time       `json:"occurredAt"`
}

func (h *Handler) listProjects(writer nethttp.ResponseWriter, request *nethttp.Request) {
	limit, err := listLimit(request)
	if err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	principal, ok := currentUser(request)
	if !ok {
		return
	}
	projects, err := h.service.ListProjects(request.Context(), principal, limit)
	if err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	response := make([]projectResponse, 0, len(projects.Items))
	for _, project := range projects.Items {
		response = append(response, mapProject(project))
	}
	writeListJSON(writer, request, nethttp.StatusOK, response, projects.Total)
}

func (h *Handler) createProject(writer nethttp.ResponseWriter, request *nethttp.Request) {
	var payload createProjectRequest
	if !decodeJSON(writer, request, &payload) {
		return
	}
	principal, ok := currentUser(request)
	if !ok {
		return
	}
	project, err := h.service.CreateProject(request.Context(), principal, payload.Key, payload.Name, payload.Description)
	if err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	writer.Header().Set("Location", "/api/v1/projects/"+project.Key)
	writeJSON(writer, request, nethttp.StatusCreated, mapProject(project))
}

func (h *Handler) getProject(writer nethttp.ResponseWriter, request *nethttp.Request) {
	principal, ok := currentUser(request)
	if !ok {
		return
	}
	project, err := h.service.GetProject(request.Context(), principal, request.PathValue("projectKey"))
	if err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	writeJSON(writer, request, nethttp.StatusOK, mapProject(project))
}

func (h *Handler) updateProject(writer nethttp.ResponseWriter, request *nethttp.Request) {
	var payload updateProjectRequest
	if !decodeJSON(writer, request, &payload) {
		return
	}
	principal, ok := currentUser(request)
	if !ok {
		return
	}
	project, err := h.service.UpdateProjectName(request.Context(), principal, request.PathValue("projectKey"), payload.Name)
	if err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	writeJSON(writer, request, nethttp.StatusOK, mapProject(project))
}

func (h *Handler) listMembers(writer nethttp.ResponseWriter, request *nethttp.Request) {
	principal, ok := currentUser(request)
	if !ok {
		return
	}
	members, err := h.service.ListMembers(request.Context(), principal, request.PathValue("projectKey"))
	if err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	response := make([]memberResponse, 0, len(members.Items))
	for _, member := range members.Items {
		response = append(response, mapMember(member))
	}
	writeListJSON(writer, request, nethttp.StatusOK, response, members.Total)
}

func (h *Handler) putMember(writer nethttp.ResponseWriter, request *nethttp.Request) {
	var payload memberRequest
	if !decodeJSON(writer, request, &payload) {
		return
	}
	principal, ok := currentUser(request)
	if !ok {
		return
	}
	member, err := h.service.UpsertMember(request.Context(), principal, request.PathValue("projectKey"), request.PathValue("username"), payload.Role)
	if err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	writeJSON(writer, request, nethttp.StatusOK, mapMember(member))
}

func (h *Handler) deleteMember(writer nethttp.ResponseWriter, request *nethttp.Request) {
	principal, ok := currentUser(request)
	if !ok {
		return
	}
	if _, err := h.service.DeleteMember(request.Context(), principal, request.PathValue("projectKey"), request.PathValue("username")); err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	writer.WriteHeader(nethttp.StatusNoContent)
}

func (h *Handler) listSecrets(writer nethttp.ResponseWriter, request *nethttp.Request) {
	principal, ok := currentUser(request)
	if !ok {
		return
	}
	secrets, err := h.service.ListSecrets(request.Context(), principal, request.PathValue("projectKey"))
	if err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	response := make([]secretResponse, 0, len(secrets.Items))
	for _, secret := range secrets.Items {
		response = append(response, mapSecret(secret))
	}
	writeListJSON(writer, request, nethttp.StatusOK, response, secrets.Total)
}

func (h *Handler) createSecret(writer nethttp.ResponseWriter, request *nethttp.Request) {
	var payload secretRequest
	if !decodeJSON(writer, request, &payload) {
		return
	}
	principal, ok := currentUser(request)
	if !ok {
		return
	}
	value := []byte(payload.Value)
	payload.Value = ""
	defer clear(value)
	secret, err := h.service.CreateSecret(request.Context(), principal, request.PathValue("projectKey"), payload.Name, payload.Kind, value)
	if err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	writer.Header().Set("Location", "/api/v1/projects/"+request.PathValue("projectKey")+"/secrets/"+secret.ID)
	writeJSON(writer, request, nethttp.StatusCreated, mapSecret(secret))
}

func (h *Handler) updateSecret(writer nethttp.ResponseWriter, request *nethttp.Request) {
	var payload updateSecretRequest
	if !decodeJSON(writer, request, &payload) {
		return
	}
	principal, ok := currentUser(request)
	if !ok {
		return
	}
	// 省略 value 才是 name-only；显式空字符串不是轮换材料，必须在进入
	// application 之前拒绝，避免 []byte("") 被当成非 nil 的 rotate。
	var value []byte
	if payload.Value != nil {
		replacement := *payload.Value
		payload.Value = nil
		if strings.TrimSpace(replacement) == "" {
			writeApplicationError(writer, request, application.ErrInvalidInput)
			return
		}
		value = []byte(replacement)
		defer clear(value)
	}
	secret, err := h.service.UpdateSecret(request.Context(), principal, request.PathValue("projectKey"), request.PathValue("secretId"), payload.Name, value)
	if err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	writeJSON(writer, request, nethttp.StatusOK, mapSecret(secret))
}

func (h *Handler) probeRepositoryRefs(writer nethttp.ResponseWriter, request *nethttp.Request) {
	var payload repositoryRefsRequest
	if !decodeJSON(writer, request, &payload) {
		return
	}
	principal, ok := currentUser(request)
	if !ok {
		return
	}
	refs, err := h.service.ProbeRepositoryRefs(request.Context(), principal, request.PathValue("projectKey"), payload.RemoteURL, payload.Transport, payload.CredentialSecretID)
	if err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	writeJSON(writer, request, nethttp.StatusOK, mapRepositoryRefs(refs))
}

func (h *Handler) probeLLMModels(writer nethttp.ResponseWriter, request *nethttp.Request) {
	var payload llmModelsRequest
	if !decodeJSON(writer, request, &payload) {
		return
	}
	principal, ok := currentUser(request)
	if !ok {
		return
	}
	models, err := h.service.ProbeLLMModels(request.Context(), principal, request.PathValue("projectKey"), payload.BaseURL, payload.CredentialSecretID)
	if err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	writeJSON(writer, request, nethttp.StatusOK, llmModelsResponse{Models: models.Models})
}

func (h *Handler) probeLLMChat(writer nethttp.ResponseWriter, request *nethttp.Request) {
	var payload llmChatRequest
	if !decodeJSON(writer, request, &payload) {
		return
	}
	principal, ok := currentUser(request)
	if !ok {
		return
	}
	if err := h.service.ProbeLLMChat(request.Context(), principal, request.PathValue("projectKey"), payload.BaseURL, payload.CredentialSecretID, payload.Model); err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	writeJSON(writer, request, nethttp.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) getConfiguration(writer nethttp.ResponseWriter, request *nethttp.Request) {
	principal, ok := currentUser(request)
	if !ok {
		return
	}
	configuration, err := h.service.GetConfiguration(request.Context(), principal, request.PathValue("projectKey"))
	if err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	writeJSON(writer, request, nethttp.StatusOK, mapConfiguration(configuration))
}

func (h *Handler) getConfigurationDraft(writer nethttp.ResponseWriter, request *nethttp.Request) {
	principal, ok := currentUser(request)
	if !ok {
		return
	}
	draft, err := h.service.GetConfigurationDraft(request.Context(), principal, request.PathValue("projectKey"))
	if err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	writeJSON(writer, request, nethttp.StatusOK, mapConfigurationDraft(draft))
}

func (h *Handler) putConfigurationComponent(writer nethttp.ResponseWriter, request *nethttp.Request) {
	principal, ok := currentUser(request)
	if !ok {
		return
	}
	projectKey := request.PathValue("projectKey")
	var response any
	var err error

	switch request.PathValue("component") {
	case "environment":
		var payload environmentRequest
		if !decodeJSON(writer, request, &payload) {
			return
		}
		var saved domain.Environment
		saved, err = h.service.PutConfigurationEnvironment(request.Context(), principal, projectKey, domain.Environment{Key: payload.Key, Name: payload.Name, Service: payload.Service})
		response = mapEnvironment(saved)
	case "repository":
		var payload repositoryRequest
		if !decodeJSON(writer, request, &payload) {
			return
		}
		var saved domain.Repository
		saved, err = h.service.PutConfigurationRepository(request.Context(), principal, projectKey, domain.Repository{RemoteURL: payload.RemoteURL, SCMProvider: payload.SCMProvider, Transport: payload.Transport, CredentialSecretID: payload.CredentialSecretID, ProductionBranch: payload.ProductionBranch, DeployedCommit: payload.DeployedCommit})
		response = mapRepository(saved)
	case "source":
		var payload sourceRequest
		if !decodeJSON(writer, request, &payload) {
			return
		}
		var saved domain.Source
		saved, err = h.service.PutConfigurationSource(request.Context(), principal, projectKey, domain.Source{Kind: payload.Kind, CredentialSecretID: payload.CredentialSecretID, Config: payload.Config, Capabilities: payload.Capabilities, Enabled: payload.Enabled})
		response = mapSource(saved)
	case "trigger":
		var payload triggerRequest
		if !decodeJSON(writer, request, &payload) {
			return
		}
		var saved domain.Trigger
		saved, err = h.service.PutConfigurationTrigger(request.Context(), principal, projectKey, domain.Trigger{Kind: payload.Kind, SigningSecretID: payload.SigningSecretID, Config: payload.Config, Enabled: payload.Enabled})
		response = mapTrigger(saved)
	case "llm":
		var payload llmRequest
		if !decodeJSON(writer, request, &payload) {
			return
		}
		provider := mapLLMRequest(&payload)
		if provider == nil {
			writeApplicationError(writer, request, application.ErrInvalidInput)
			return
		}
		var saved domain.LLMProvider
		saved, err = h.service.PutConfigurationLLMProvider(request.Context(), principal, projectKey, *provider)
		response = mapLLM(saved)
	case "remediation-policy":
		var payload remediationPolicyRequest
		if !decodeJSON(writer, request, &payload) {
			return
		}
		var saved domain.RemediationPolicy
		saved, err = h.service.PutConfigurationRemediationPolicy(request.Context(), principal, projectKey, domain.RemediationPolicy{AgentLoopMode: payload.AgentLoopMode})
		response = mapRemediationPolicy(saved)
	default:
		writeApplicationError(writer, request, application.ErrInvalidInput)
		return
	}
	if err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	writeJSON(writer, request, nethttp.StatusOK, response)
}

func (h *Handler) putConfiguration(writer nethttp.ResponseWriter, request *nethttp.Request) {
	var payload configurationRequest
	if !decodeJSON(writer, request, &payload) {
		return
	}
	principal, ok := currentUser(request)
	if !ok {
		return
	}
	configuration, err := h.service.PutConfiguration(request.Context(), principal, request.PathValue("projectKey"), domain.Configuration{
		Environment: domain.Environment{Key: payload.Environment.Key, Name: payload.Environment.Name, Service: payload.Environment.Service},
		Repository: domain.Repository{RemoteURL: payload.Repository.RemoteURL, SCMProvider: payload.Repository.SCMProvider,
			Transport: payload.Repository.Transport, CredentialSecretID: payload.Repository.CredentialSecretID,
			ProductionBranch: payload.Repository.ProductionBranch, DeployedCommit: payload.Repository.DeployedCommit},
		Source: domain.Source{Kind: payload.Source.Kind, CredentialSecretID: payload.Source.CredentialSecretID,
			Config: payload.Source.Config, Capabilities: payload.Source.Capabilities, Enabled: payload.Source.Enabled},
		Trigger: domain.Trigger{Kind: payload.Trigger.Kind, SigningSecretID: payload.Trigger.SigningSecretID,
			Config: payload.Trigger.Config, Enabled: payload.Trigger.Enabled},
		LLM: mapLLMRequest(payload.LLM),
	})
	if err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	writeJSON(writer, request, nethttp.StatusOK, mapConfiguration(configuration))
}

func (h *Handler) rotateWebhookToken(writer nethttp.ResponseWriter, request *nethttp.Request) {
	// 空对象保持与其它 JSON POST 一致的 DecodeJSON 边界，避免无 body 的 rotate 绕过 media type 检查。
	if decodeError := httpserver.DecodeJSON(request, &struct{}{}); decodeError != nil {
		httpserver.WriteError(writer, request, *decodeError)
		return
	}
	principal, ok := currentUser(request)
	if !ok {
		return
	}
	inboundURL, err := h.service.RotateWebhookToken(request.Context(), principal, request.PathValue("projectKey"))
	if err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	writeJSON(writer, request, nethttp.StatusOK, webhookTokenResponse{InboundURL: inboundURL})
}

func (h *Handler) listAuditEvents(writer nethttp.ResponseWriter, request *nethttp.Request) {
	limit, err := listLimit(request)
	if err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	principal, ok := currentUser(request)
	if !ok {
		return
	}
	events, err := h.service.ListAuditEvents(request.Context(), principal, request.PathValue("projectKey"), limit)
	if err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	response := make([]auditEventResponse, 0, len(events.Items))
	for _, event := range events.Items {
		response = append(response, auditEventResponse{ID: event.ID, ActorUserID: event.ActorUserID, Action: event.Action,
			TargetType: event.TargetType, TargetID: event.TargetID, Summary: event.Summary,
			Metadata: event.Metadata, OccurredAt: event.OccurredAt})
	}
	writeListJSON(writer, request, nethttp.StatusOK, response, events.Total)
}

func mapProject(project domain.Project) projectResponse {
	return projectResponse{ID: project.ID, Key: project.Key, Name: project.Name, Description: project.Description,
		Role: project.Role, Capabilities: capabilitiesResponse{Read: true, WriteIncidents: project.CanWriteIncidents(),
			ManageMembers: project.CanAdminister(), ManageConfig: project.CanAdminister()},
		Version: project.Version, CreatedAt: project.CreatedAt, UpdatedAt: project.UpdatedAt}
}

func mapMember(member domain.Member) memberResponse {
	return memberResponse{UserID: member.UserID, Username: member.Username, Role: member.Role, Version: member.Version,
		CreatedAt: member.CreatedAt, UpdatedAt: member.UpdatedAt}
}

func mapRepositoryRefs(refs application.RepositoryRefs) repositoryRefsResponse {
	branches := make([]gitBranchResponse, 0, len(refs.Branches))
	for _, branch := range refs.Branches {
		branches = append(branches, gitBranchResponse{Name: branch.Name, Commit: branch.Commit})
	}
	return repositoryRefsResponse{DefaultBranch: refs.DefaultBranch, DeployedCommit: refs.DeployedCommit, Branches: branches}
}

func mapSecret(secret domain.Secret) secretResponse {
	return secretResponse{ID: secret.ID, Name: secret.Name, Kind: secret.Kind, KeyVersion: secret.KeyVersion,
		Version: secret.Version, CreatedAt: secret.CreatedAt, UpdatedAt: secret.UpdatedAt}
}

func mapConfiguration(configuration domain.Configuration) configurationResponse {
	return configurationResponse{Environment: mapEnvironment(configuration.Environment), Repository: mapRepository(configuration.Repository), Source: mapSource(configuration.Source), Trigger: mapTrigger(configuration.Trigger), LLM: mapLLMResponse(configuration.LLM), Remediation: mapRemediationPolicy(configuration.Remediation)}
}

func mapConfigurationDraft(draft domain.ConfigurationDraft) configurationDraftResponse {
	response := configurationDraftResponse{LLM: mapLLMResponse(draft.LLM)}
	if draft.Remediation != nil {
		value := mapRemediationPolicy(*draft.Remediation)
		response.Remediation = &value
	}
	if draft.Environment != nil {
		value := mapEnvironment(*draft.Environment)
		response.Environment = &value
	}
	if draft.Repository != nil {
		value := mapRepository(*draft.Repository)
		response.Repository = &value
	}
	if draft.Source != nil {
		value := mapSource(*draft.Source)
		response.Source = &value
	}
	if draft.Trigger != nil {
		value := mapTrigger(*draft.Trigger)
		response.Trigger = &value
	}
	return response
}

func mapRemediationPolicy(policy domain.RemediationPolicy) remediationPolicyResponse {
	return remediationPolicyResponse{AgentLoopMode: policy.AgentLoopMode, Version: policy.Version}
}

func mapEnvironment(environment domain.Environment) environmentResponse {
	return environmentResponse{ID: environment.ID, Key: environment.Key, Name: environment.Name, Service: environment.Service, Version: environment.Version}
}

func mapRepository(repository domain.Repository) repositoryResponse {
	return repositoryResponse{ID: repository.ID, RemoteURL: repository.RemoteURL, SCMProvider: repository.SCMProvider, Transport: repository.Transport, CredentialSecretID: repository.CredentialSecretID, ProductionBranch: repository.ProductionBranch, DeployedCommit: repository.DeployedCommit, Version: repository.Version}
}

func mapSource(source domain.Source) sourceResponse {
	return sourceResponse{ID: source.ID, Kind: source.Kind, CredentialSecretID: source.CredentialSecretID, Config: source.Config, Capabilities: source.Capabilities, Enabled: source.Enabled, Version: source.Version}
}

func mapTrigger(trigger domain.Trigger) triggerResponse {
	return triggerResponse{ID: trigger.ID, Kind: trigger.Kind, SigningSecretID: trigger.SigningSecretID, InboundURL: optionalString(trigger.InboundURL), Config: trigger.Config, Enabled: trigger.Enabled, Version: trigger.Version}
}

func mapLLM(provider domain.LLMProvider) llmResponse {
	return llmResponse{ID: provider.ID, Provider: provider.Provider, BaseURL: provider.BaseURL, CredentialSecretID: provider.CredentialSecretID, Model: provider.Model, Version: provider.Version}
}

func mapLLMResponse(provider *domain.LLMProvider) *llmResponse {
	if provider == nil {
		return nil
	}
	response := mapLLM(*provider)
	return &response
}

func mapLLMRequest(payload *llmRequest) *domain.LLMProvider {
	if payload == nil {
		return nil
	}
	return &domain.LLMProvider{
		Provider: payload.Provider, BaseURL: payload.BaseURL,
		CredentialSecretID: payload.CredentialSecretID, Model: payload.Model,
	}
}

func currentUser(request *nethttp.Request) (authdomain.User, bool) {
	principal, ok := authhttp.CurrentUser(request.Context())
	return principal, ok
}

func decodeJSON(writer nethttp.ResponseWriter, request *nethttp.Request, target any) bool {
	if decodeError := httpserver.DecodeJSON(request, target); decodeError != nil {
		httpserver.WriteError(writer, request, *decodeError)
		return false
	}
	return true
}

func listLimit(request *nethttp.Request) (int32, error) {
	query, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		return 0, application.ErrInvalidInput
	}
	for key, values := range query {
		if key != "limit" || len(values) != 1 {
			return 0, application.ErrInvalidInput
		}
	}
	values, exists := query["limit"]
	if !exists {
		return application.DefaultListLimit, nil
	}
	limit, err := strconv.ParseInt(values[0], 10, 32)
	if err != nil || limit < 1 || limit > int64(application.MaximumListLimit) {
		return 0, application.ErrInvalidInput
	}
	return int32(limit), nil
}

func writeApplicationError(writer nethttp.ResponseWriter, request *nethttp.Request, err error) {
	switch {
	case errors.Is(err, application.ErrInvalidInput):
		httpserver.WriteError(writer, request, httpserver.Error{Status: nethttp.StatusBadRequest, Code: "invalid_request", Message: "Project request is invalid."})
	case errors.Is(err, application.ErrNotFound):
		httpserver.WriteError(writer, request, httpserver.Error{Status: nethttp.StatusNotFound, Code: "project_not_found", Message: "Project was not found."})
	case errors.Is(err, application.ErrMemberNotFound):
		httpserver.WriteError(writer, request, httpserver.Error{Status: nethttp.StatusNotFound, Code: "member_not_found", Message: "Project member was not found."})
	case errors.Is(err, application.ErrConfigurationNotFound):
		httpserver.WriteError(writer, request, httpserver.Error{Status: nethttp.StatusNotFound, Code: "configuration_not_found", Message: "Project configuration was not found."})
	case errors.Is(err, application.ErrForbidden):
		httpserver.WriteError(writer, request, httpserver.Error{Status: nethttp.StatusForbidden, Code: "forbidden", Message: "You do not have permission to perform this action."})
	case errors.Is(err, application.ErrConflict):
		httpserver.WriteError(writer, request, httpserver.Error{Status: nethttp.StatusConflict, Code: "project_conflict", Message: "Project resource already exists."})
	case errors.Is(err, application.ErrMemberChangeRejected):
		httpserver.WriteError(writer, request, httpserver.Error{Status: nethttp.StatusConflict, Code: "member_change_rejected", Message: "The project must retain an administrator."})
	case errors.Is(err, application.ErrGitUnreachable):
		httpserver.WriteError(writer, request, httpserver.Error{Status: nethttp.StatusBadGateway, Code: "git_unreachable", Message: "The Git remote could not be read."})
	case errors.Is(err, application.ErrLLMUnreachable):
		httpserver.WriteError(writer, request, httpserver.Error{Status: nethttp.StatusBadGateway, Code: "llm_unreachable", Message: "The LLM provider could not be reached."})
	default:
		httpserver.WriteInternalError(writer, request, err)
	}
}

func writeJSON(writer nethttp.ResponseWriter, request *nethttp.Request, status int, value any) {
	if err := httpserver.WriteJSON(writer, status, value); err != nil {
		httpserver.WriteInternalError(writer, request, err)
	}
}

func writeListJSON(writer nethttp.ResponseWriter, request *nethttp.Request, status int, value any, total int64) {
	if err := httpserver.WriteListJSON(writer, status, value, total); err != nil {
		httpserver.WriteInternalError(writer, request, err)
	}
}

func clear(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
