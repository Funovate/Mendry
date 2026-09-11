package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	nethttp "net/http"
	"net/url"
	"strconv"
	"time"

	authhttp "mendry/backend/internal/modules/auth/adapter/http"
	authdomain "mendry/backend/internal/modules/auth/domain"
	"mendry/backend/internal/modules/observations/application"
	"mendry/backend/internal/modules/observations/domain"
	projectapplication "mendry/backend/internal/modules/projects/application"
	"mendry/backend/internal/platform/httpserver"
)

type service interface {
	List(context.Context, authdomain.User, string, int32) (application.ListResult, error)
	Create(context.Context, authdomain.User, string, application.CreateInput) (domain.Observation, error)
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
		return nil, fmt.Errorf("observation HTTP dependencies are required")
	}
	return &Handler{service: options.Service, authentication: options.Authentication}, nil
}

func (h *Handler) Register(mux *nethttp.ServeMux) {
	mux.Handle("GET /api/v1/projects/{projectKey}/observations", h.authentication.RequireAuthentication(nethttp.HandlerFunc(h.list)))
	mux.Handle("POST /api/v1/projects/{projectKey}/observations", h.authentication.RequireAuthentication(nethttp.HandlerFunc(h.create)))
}

type createRequest struct {
	SourceID    string          `json:"sourceId"`
	Service     *string         `json:"service"`
	OccurredAt  *time.Time      `json:"occurredAt"`
	Level       string          `json:"level"`
	Message     string          `json:"message"`
	Host        *string         `json:"host"`
	RequestID   *string         `json:"requestId"`
	Fingerprint string          `json:"fingerprint"`
	Attributes  json.RawMessage `json:"attributes"`
}

type response struct {
	ID            string          `json:"id"`
	EnvironmentID string          `json:"environmentId"`
	SourceID      string          `json:"sourceId"`
	Service       *string         `json:"service"`
	OccurredAt    time.Time       `json:"occurredAt"`
	Level         string          `json:"level"`
	Message       string          `json:"message"`
	Host          *string         `json:"host"`
	RequestID     *string         `json:"requestId"`
	Fingerprint   string          `json:"fingerprint"`
	Attributes    json.RawMessage `json:"attributes"`
	IngestedAt    time.Time       `json:"ingestedAt"`
}

func (h *Handler) list(writer nethttp.ResponseWriter, request *nethttp.Request) {
	limit, err := listLimit(request)
	if err != nil {
		writeError(writer, request, err)
		return
	}
	principal, ok := authhttp.CurrentUser(request.Context())
	if !ok {
		writeError(writer, request, projectapplication.ErrForbidden)
		return
	}
	items, err := h.service.List(request.Context(), principal, request.PathValue("projectKey"), limit)
	if err != nil {
		writeError(writer, request, err)
		return
	}
	result := make([]response, 0, len(items.Items))
	for _, item := range items.Items {
		result = append(result, mapObservation(item))
	}
	writeListJSON(writer, request, nethttp.StatusOK, result, items.Total)
}

func (h *Handler) create(writer nethttp.ResponseWriter, request *nethttp.Request) {
	var payload createRequest
	if decodeError := httpserver.DecodeJSON(request, &payload); decodeError != nil {
		httpserver.WriteError(writer, request, *decodeError)
		return
	}
	principal, ok := authhttp.CurrentUser(request.Context())
	if !ok {
		writeError(writer, request, projectapplication.ErrForbidden)
		return
	}
	item, err := h.service.Create(request.Context(), principal, request.PathValue("projectKey"), application.CreateInput{
		SourceID: payload.SourceID, Service: payload.Service, OccurredAt: payload.OccurredAt, Level: payload.Level,
		Message: payload.Message, Host: payload.Host, RequestID: payload.RequestID, Fingerprint: payload.Fingerprint, Attributes: payload.Attributes,
	})
	if err != nil {
		writeError(writer, request, err)
		return
	}
	writeJSON(writer, request, nethttp.StatusCreated, mapObservation(item))
}

func mapObservation(item domain.Observation) response {
	return response{ID: item.ID, EnvironmentID: item.EnvironmentID, SourceID: item.SourceID, Service: item.Service,
		OccurredAt: item.OccurredAt, Level: item.Level, Message: item.Message, Host: item.Host, RequestID: item.RequestID,
		Fingerprint: item.Fingerprint, Attributes: item.Attributes, IngestedAt: item.IngestedAt}
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

func writeError(writer nethttp.ResponseWriter, request *nethttp.Request, err error) {
	switch {
	case errors.Is(err, application.ErrInvalidInput):
		httpserver.WriteError(writer, request, httpserver.Error{Status: nethttp.StatusBadRequest, Code: "invalid_request", Message: "Observation request is invalid."})
	case errors.Is(err, application.ErrSourceNotFound):
		httpserver.WriteError(writer, request, httpserver.Error{Status: nethttp.StatusNotFound, Code: "source_not_found", Message: "Observation source was not found."})
	case errors.Is(err, projectapplication.ErrNotFound):
		httpserver.WriteError(writer, request, httpserver.Error{Status: nethttp.StatusNotFound, Code: "project_not_found", Message: "Project was not found."})
	case errors.Is(err, projectapplication.ErrForbidden):
		httpserver.WriteError(writer, request, httpserver.Error{Status: nethttp.StatusForbidden, Code: "forbidden", Message: "You do not have permission to perform this action."})
	default:
		httpserver.WriteInternalError(writer, request, err)
	}
}

func writeJSON(writer nethttp.ResponseWriter, request *nethttp.Request, status int, value any) {
	if err := httpserver.WriteJSON(writer, status, value); err != nil {
		writeError(writer, request, err)
	}
}

func writeListJSON(writer nethttp.ResponseWriter, request *nethttp.Request, status int, value any, total int64) {
	if err := httpserver.WriteListJSON(writer, status, value, total); err != nil {
		writeError(writer, request, err)
	}
}
