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
	PutConfiguration(context.Context, authdomain.User, string, domain.Configuration) (domain.Configuration, error)
	ProbeRepositoryRefs(context.Context, authdomain.User, string, string, string, string) (application.RepositoryRefs, error)
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
	mux.Handle("PUT /api/v1/projects/{projectKey}/configuration", h.authentication.RequireAuthentication(nethttp.HandlerFunc(h.putConfiguration)))
	mux.Handle("POST /api/v1/projects/{projectKey}/repository/refs", h.authentication.RequireAuthentication(nethttp.HandlerFunc(h.probeRepositoryRefs)))
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
	Name               string          `json:"name"`
	Kind               string          `json:"kind"`
	CredentialSecretID *string         `json:"credentialSecretId"`
	Config             json.RawMessage `json:"config"`
	Capabilities       []string        `json:"capabilities"`
	Enabled            bool            `json:"enabled"`
}

type triggerRequest struct {
	Name            string          `json:"name"`
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
	Environment environmentResponse `json:"environment"`
	Repository  repositoryResponse  `json:"repository"`
	Source      sourceResponse      `json:"source"`
	Trigger     triggerResponse     `json:"trigger"`
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
	Name               string          `json:"name"`
	Kind               string          `json:"kind"`
	CredentialSecretID *string         `json:"credentialSecretId"`
	Config             json.RawMessage `json:"config"`
	Capabilities       []string        `json:"capabilities"`
	Enabled            bool            `json:"enabled"`
	Version            int64           `json:"version"`
}

type triggerResponse struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Kind            string          `json:"kind"`
	SigningSecretID *string         `json:"signingSecretId"`
	Config          json.RawMessage `json:"config"`
	Enabled         bool            `json:"enabled"`
	Version         int64           `json:"version"`
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
		Source: domain.Source{Name: payload.Source.Name, Kind: payload.Source.Kind, CredentialSecretID: payload.Source.CredentialSecretID,
			Config: payload.Source.Config, Capabilities: payload.Source.Capabilities, Enabled: payload.Source.Enabled},
		Trigger: domain.Trigger{Name: payload.Trigger.Name, Kind: payload.Trigger.Kind, SigningSecretID: payload.Trigger.SigningSecretID,
			Config: payload.Trigger.Config, Enabled: payload.Trigger.Enabled},
	})
	if err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	writeJSON(writer, request, nethttp.StatusOK, mapConfiguration(configuration))
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
	return configurationResponse{
		Environment: environmentResponse{ID: configuration.Environment.ID, Key: configuration.Environment.Key,
			Name: configuration.Environment.Name, Service: configuration.Environment.Service, Version: configuration.Environment.Version},
		Repository: repositoryResponse{ID: configuration.Repository.ID, RemoteURL: configuration.Repository.RemoteURL,
			SCMProvider: configuration.Repository.SCMProvider, Transport: configuration.Repository.Transport,
			CredentialSecretID: configuration.Repository.CredentialSecretID, ProductionBranch: configuration.Repository.ProductionBranch,
			DeployedCommit: configuration.Repository.DeployedCommit, Version: configuration.Repository.Version},
		Source: sourceResponse{ID: configuration.Source.ID, Name: configuration.Source.Name, Kind: configuration.Source.Kind,
			CredentialSecretID: configuration.Source.CredentialSecretID, Config: configuration.Source.Config,
			Capabilities: configuration.Source.Capabilities, Enabled: configuration.Source.Enabled, Version: configuration.Source.Version},
		Trigger: triggerResponse{ID: configuration.Trigger.ID, Name: configuration.Trigger.Name, Kind: configuration.Trigger.Kind,
			SigningSecretID: configuration.Trigger.SigningSecretID, Config: configuration.Trigger.Config,
			Enabled: configuration.Trigger.Enabled, Version: configuration.Trigger.Version},
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
	default:
		httpserver.WriteError(writer, request, httpserver.Error{Status: nethttp.StatusInternalServerError, Code: "internal_error", Message: "An internal error occurred."})
	}
}

func writeJSON(writer nethttp.ResponseWriter, request *nethttp.Request, status int, value any) {
	if err := httpserver.WriteJSON(writer, status, value); err != nil {
		httpserver.WriteError(writer, request, httpserver.Error{Status: nethttp.StatusInternalServerError, Code: "internal_error", Message: "An internal error occurred."})
	}
}

func writeListJSON(writer nethttp.ResponseWriter, request *nethttp.Request, status int, value any, total int64) {
	if err := httpserver.WriteListJSON(writer, status, value, total); err != nil {
		httpserver.WriteError(writer, request, httpserver.Error{Status: nethttp.StatusInternalServerError, Code: "internal_error", Message: "An internal error occurred."})
	}
}

func clear(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
