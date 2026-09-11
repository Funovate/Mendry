package http

import (
	"context"
	"errors"
	nethttp "net/http"
	"net/url"
	"strconv"
	"time"

	authhttp "mendry/backend/internal/modules/auth/adapter/http"
	authdomain "mendry/backend/internal/modules/auth/domain"
	"mendry/backend/internal/modules/incidents/application"
	"mendry/backend/internal/modules/incidents/domain"
	projectapplication "mendry/backend/internal/modules/projects/application"
	"mendry/backend/internal/platform/httpserver"
)

type service interface {
	List(context.Context, authdomain.User, string, int32) (application.ListResult, error)
	Get(context.Context, authdomain.User, string, string) (domain.Incident, error)
	Create(context.Context, authdomain.User, string, application.CreateInput) (domain.Incident, error)
	UpdateStatus(context.Context, authdomain.User, string, string, string) (domain.Incident, error)
}

type authentication interface {
	RequireAuthentication(nethttp.Handler) nethttp.Handler
}

// HandlerOptions 声明事故 HTTP adapter 的用例和认证 middleware 依赖。
type HandlerOptions struct {
	Service        service
	Authentication authentication
}

// Handler 负责事故 REST DTO、路径解析和安全错误映射，不实现业务规则。
type Handler struct {
	service        service
	authentication authentication
}

// NewHandler 在注册 route 前验证事故 HTTP 依赖。
func NewHandler(options HandlerOptions) (*Handler, error) {
	if options.Service == nil || options.Authentication == nil {
		return nil, errors.New("incident HTTP dependencies are required")
	}
	return &Handler{service: options.Service, authentication: options.Authentication}, nil
}

// Register 注册受 Session 和角色保护的事故 endpoint。
func (h *Handler) Register(mux *nethttp.ServeMux) {
	mux.Handle("GET /api/v1/projects/{projectKey}/incidents", h.authentication.RequireAuthentication(nethttp.HandlerFunc(h.list)))
	mux.Handle("POST /api/v1/projects/{projectKey}/incidents", h.authentication.RequireAuthentication(nethttp.HandlerFunc(h.create)))
	mux.Handle("GET /api/v1/projects/{projectKey}/incidents/{id}", h.authentication.RequireAuthentication(nethttp.HandlerFunc(h.get)))
	mux.Handle("PATCH /api/v1/projects/{projectKey}/incidents/{id}/status", h.authentication.RequireAuthentication(nethttp.HandlerFunc(h.updateStatus)))
}

type createRequest struct {
	Title               string     `json:"title"`
	Fingerprint         string     `json:"fingerprint"`
	Priority            string     `json:"priority"`
	SourceID            string     `json:"sourceId"`
	FirstSeen           *time.Time `json:"firstSeen"`
	LastSeen            *time.Time `json:"lastSeen"`
	OccurrenceCount     *int64     `json:"occurrenceCount"`
	HostCount           *int64     `json:"hostCount"`
	Muted               *bool      `json:"muted"`
	NotificationSummary *string    `json:"notificationSummary"`
}

type updateStatusRequest struct {
	Status string `json:"status"`
}

type incidentResponse struct {
	ID                  string          `json:"id"`
	Title               string          `json:"title"`
	Fingerprint         string          `json:"fingerprint"`
	Status              domain.Status   `json:"status"`
	Priority            domain.Priority `json:"priority"`
	Source              string          `json:"source"`
	SourceID            string          `json:"sourceId"`
	EnvironmentID       string          `json:"environmentId"`
	FirstSeen           time.Time       `json:"firstSeen"`
	LastSeen            time.Time       `json:"lastSeen"`
	OccurrenceCount     int64           `json:"occurrenceCount"`
	HostCount           int64           `json:"hostCount"`
	Muted               bool            `json:"muted"`
	NotificationSummary string          `json:"notificationSummary"`
	Version             int64           `json:"version"`
	LifecycleGeneration int64           `json:"lifecycleGeneration"`
	CreatedAt           time.Time       `json:"createdAt"`
	UpdatedAt           time.Time       `json:"updatedAt"`
}

func (h *Handler) list(writer nethttp.ResponseWriter, request *nethttp.Request) {
	limit, err := listLimit(request)
	if err != nil {
		writeApplicationError(writer, request, application.ErrInvalidInput)
		return
	}
	principal, ok := authhttp.CurrentUser(request.Context())
	if !ok {
		writeApplicationError(writer, request, projectapplication.ErrForbidden)
		return
	}
	incidents, err := h.service.List(request.Context(), principal, request.PathValue("projectKey"), limit)
	if err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	response := make([]incidentResponse, 0, len(incidents.Items))
	for _, incident := range incidents.Items {
		mapped, err := mapIncident(incident)
		if err != nil {
			writeApplicationError(writer, request, err)
			return
		}
		response = append(response, mapped)
	}
	writeListJSON(writer, request, nethttp.StatusOK, response, incidents.Total)
}

func (h *Handler) get(writer nethttp.ResponseWriter, request *nethttp.Request) {
	principal, ok := authhttp.CurrentUser(request.Context())
	if !ok {
		writeApplicationError(writer, request, projectapplication.ErrForbidden)
		return
	}
	incident, err := h.service.Get(request.Context(), principal, request.PathValue("projectKey"), request.PathValue("id"))
	if err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	writeIncident(writer, request, nethttp.StatusOK, incident)
}

func (h *Handler) create(writer nethttp.ResponseWriter, request *nethttp.Request) {
	var payload createRequest
	if decodeError := httpserver.DecodeJSON(request, &payload); decodeError != nil {
		httpserver.WriteError(writer, request, *decodeError)
		return
	}
	principal, ok := authhttp.CurrentUser(request.Context())
	if !ok {
		writeApplicationError(writer, request, projectapplication.ErrForbidden)
		return
	}
	incident, err := h.service.Create(request.Context(), principal, request.PathValue("projectKey"), application.CreateInput{
		Title: payload.Title, Fingerprint: payload.Fingerprint, Priority: payload.Priority,
		SourceID: payload.SourceID, FirstSeen: payload.FirstSeen, LastSeen: payload.LastSeen,
		OccurrenceCount: payload.OccurrenceCount, HostCount: payload.HostCount,
		Muted: payload.Muted, NotificationSummary: payload.NotificationSummary,
	})
	if err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	identifier, err := domain.FormatID(incident.Number)
	if err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	writer.Header().Set("Location", "/api/v1/projects/"+request.PathValue("projectKey")+"/incidents/"+identifier)
	writeIncident(writer, request, nethttp.StatusCreated, incident)
}

func (h *Handler) updateStatus(writer nethttp.ResponseWriter, request *nethttp.Request) {
	var payload updateStatusRequest
	if decodeError := httpserver.DecodeJSON(request, &payload); decodeError != nil {
		httpserver.WriteError(writer, request, *decodeError)
		return
	}
	principal, ok := authhttp.CurrentUser(request.Context())
	if !ok {
		writeApplicationError(writer, request, projectapplication.ErrForbidden)
		return
	}
	incident, err := h.service.UpdateStatus(request.Context(), principal, request.PathValue("projectKey"), request.PathValue("id"), payload.Status)
	if err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	writeIncident(writer, request, nethttp.StatusOK, incident)
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
	value := values[0]
	if value == "" {
		return 0, application.ErrInvalidInput
	}
	limit, err := strconv.ParseInt(value, 10, 32)
	if err != nil || limit < 1 || limit > int64(application.MaximumListLimit) {
		return 0, application.ErrInvalidInput
	}
	return int32(limit), nil
}

func mapIncident(incident domain.Incident) (incidentResponse, error) {
	identifier, err := domain.FormatID(incident.Number)
	if err != nil {
		return incidentResponse{}, err
	}
	return incidentResponse{
		ID: identifier, Title: incident.Title, Fingerprint: incident.Fingerprint,
		Status: incident.Status, Priority: incident.Priority, Source: incident.Source,
		SourceID: incident.SourceID, EnvironmentID: incident.EnvironmentID,
		FirstSeen: incident.FirstSeen, LastSeen: incident.LastSeen,
		OccurrenceCount: incident.OccurrenceCount, HostCount: incident.HostCount,
		Muted: incident.Muted, NotificationSummary: incident.NotificationSummary,
		Version: incident.Version, LifecycleGeneration: incident.LifecycleGeneration,
		CreatedAt: incident.CreatedAt, UpdatedAt: incident.UpdatedAt,
	}, nil
}

func writeIncident(writer nethttp.ResponseWriter, request *nethttp.Request, status int, incident domain.Incident) {
	response, err := mapIncident(incident)
	if err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	writeJSON(writer, request, status, response)
}

func writeJSON(writer nethttp.ResponseWriter, request *nethttp.Request, status int, response any) {
	if err := httpserver.WriteJSON(writer, status, response); err != nil {
		httpserver.WriteInternalError(writer, request, err)
	}
}

func writeListJSON(writer nethttp.ResponseWriter, request *nethttp.Request, status int, response any, total int64) {
	if err := httpserver.WriteListJSON(writer, status, response, total); err != nil {
		httpserver.WriteInternalError(writer, request, err)
	}
}

func writeApplicationError(writer nethttp.ResponseWriter, request *nethttp.Request, err error) {
	switch {
	case errors.Is(err, application.ErrInvalidInput):
		httpserver.WriteError(writer, request, httpserver.Error{
			Status: nethttp.StatusBadRequest, Code: "invalid_request", Message: "Incident request is invalid.",
		})
	case errors.Is(err, application.ErrNotFound):
		httpserver.WriteError(writer, request, httpserver.Error{
			Status: nethttp.StatusNotFound, Code: "incident_not_found", Message: "Incident was not found.",
		})
	case errors.Is(err, application.ErrConflict):
		httpserver.WriteError(writer, request, httpserver.Error{
			Status: nethttp.StatusConflict, Code: "incident_conflict", Message: "An incident with this fingerprint already exists.",
		})
	case errors.Is(err, projectapplication.ErrNotFound):
		httpserver.WriteError(writer, request, httpserver.Error{
			Status: nethttp.StatusNotFound, Code: "project_not_found", Message: "Project was not found.",
		})
	case errors.Is(err, projectapplication.ErrForbidden):
		httpserver.WriteError(writer, request, httpserver.Error{
			Status: nethttp.StatusForbidden, Code: "forbidden", Message: "You do not have permission to perform this action.",
		})
	default:
		httpserver.WriteInternalError(writer, request, err)
	}
}
