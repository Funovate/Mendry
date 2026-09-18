package http

import (
	"context"
	"errors"
	"net/http"

	authhttp "mendry/backend/internal/modules/auth/adapter/http"
	authdomain "mendry/backend/internal/modules/auth/domain"
	"mendry/backend/internal/modules/notifications/application"
	"mendry/backend/internal/modules/notifications/domain"
	projectapplication "mendry/backend/internal/modules/projects/application"
	projectdomain "mendry/backend/internal/modules/projects/domain"
	"mendry/backend/internal/platform/httpserver"
)

type Projects interface {
	GetProject(context.Context, authdomain.User, string) (projectdomain.Project, error)
}
type Handler struct {
	service  *application.Service
	projects Projects
	auth     *authhttp.Handler
}

func NewHandler(service *application.Service, projects Projects, auth *authhttp.Handler) *Handler {
	return &Handler{service: service, projects: projects, auth: auth}
}
func (h *Handler) Register(mux *http.ServeMux) {
	routes := map[string]http.HandlerFunc{
		"GET /api/v1/projects/{projectKey}/notifications/channels":                       h.listChannels,
		"POST /api/v1/projects/{projectKey}/notifications/channels":                      h.saveChannel,
		"PUT /api/v1/projects/{projectKey}/notifications/channels/{channelId}":           h.saveChannel,
		"DELETE /api/v1/projects/{projectKey}/notifications/channels/{channelId}":        h.deleteChannel,
		"POST /api/v1/projects/{projectKey}/notifications/channels/{channelId}/test":     h.testChannel,
		"GET /api/v1/projects/{projectKey}/notifications/deliveries":                     h.listDeliveries,
		"POST /api/v1/projects/{projectKey}/notifications/deliveries/{deliveryId}/retry": h.retryDelivery,
	}
	for route, handler := range routes {
		mux.Handle(route, h.auth.RequireAuthentication(handler))
	}
}
func (h *Handler) project(w http.ResponseWriter, r *http.Request) (string, bool) {
	user, ok := authhttp.CurrentUser(r.Context())
	if !ok {
		httpserver.WriteError(w, r, httpserver.Error{Status: 401, Code: "authentication_required", Message: "Authentication required."})
		return "", false
	}
	p, err := h.projects.GetProject(r.Context(), user, r.PathValue("projectKey"))
	if err != nil {
		writeError(w, r, err)
		return "", false
	}
	return p.ID, true
}
func (h *Handler) listChannels(w http.ResponseWriter, r *http.Request) {
	id, ok := h.project(w, r)
	if !ok {
		return
	}
	items, err := h.service.ListChannels(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	_ = httpserver.WriteListJSON(w, 200, items, int64(len(items)))
}
func (h *Handler) saveChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := h.project(w, r)
	if !ok {
		return
	}
	var input domain.ChannelInput
	if err := httpserver.DecodeJSON(r, &input); err != nil {
		httpserver.WriteError(w, r, *err)
		return
	}
	c, err := h.service.SaveChannel(r.Context(), id, r.PathValue("channelId"), input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	status := 200
	if r.Method == http.MethodPost {
		status = 201
	}
	_ = httpserver.WriteJSON(w, status, c)
}
func (h *Handler) deleteChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := h.project(w, r)
	if !ok {
		return
	}
	if err := h.service.DeleteChannel(r.Context(), id, r.PathValue("channelId")); err != nil {
		writeError(w, r, err)
		return
	}
	_ = httpserver.WriteJSON(w, 204, nil)
}
func (h *Handler) testChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := h.project(w, r)
	if !ok {
		return
	}
	if err := h.service.TestChannel(r.Context(), id, r.PathValue("channelId")); err != nil {
		if errors.Is(err, domain.ErrInvalidInput) || errors.Is(err, domain.ErrNotFound) {
			writeError(w, r, err)
		} else {
			httpserver.WriteError(w, r, httpserver.Error{Status: 502, Code: "notification_test_failed", Message: "The platform did not accept the test message."})
		}
		return
	}
	_ = httpserver.WriteJSON(w, 200, map[string]bool{"sent": true})
}
func (h *Handler) listDeliveries(w http.ResponseWriter, r *http.Request) {
	id, ok := h.project(w, r)
	if !ok {
		return
	}
	items, err := h.service.ListDeliveries(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	_ = httpserver.WriteListJSON(w, 200, items, int64(len(items)))
}
func (h *Handler) retryDelivery(w http.ResponseWriter, r *http.Request) {
	id, ok := h.project(w, r)
	if !ok {
		return
	}
	if err := h.service.RetryDelivery(r.Context(), id, r.PathValue("deliveryId")); err != nil {
		writeError(w, r, err)
		return
	}
	_ = httpserver.WriteJSON(w, 200, map[string]bool{"queued": true})
}
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	e := httpserver.Error{Status: 500, Code: "internal_error", Message: "An internal error occurred."}
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		e = httpserver.Error{Status: 400, Code: "invalid_input", Message: "Invalid notification configuration."}
	case errors.Is(err, domain.ErrNotFound) || errors.Is(err, projectapplication.ErrNotFound):
		e = httpserver.Error{Status: 404, Code: "not_found", Message: "Resource not found."}
	case errors.Is(err, domain.ErrConflict):
		e = httpserver.Error{Status: 409, Code: "conflict", Message: "Only failed deliveries can be retried."}
	}
	httpserver.WriteError(w, r, e)
}
