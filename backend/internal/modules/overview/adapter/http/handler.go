package http

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	authhttp "mendry/backend/internal/modules/auth/adapter/http"
	authdomain "mendry/backend/internal/modules/auth/domain"
	"mendry/backend/internal/modules/overview/application"
	projectapplication "mendry/backend/internal/modules/projects/application"
	"mendry/backend/internal/platform/httpserver"
)

type Service interface {
	GetSnapshot(context.Context, authdomain.User, string, application.Window) (application.Snapshot, error)
	GetTasks(context.Context, authdomain.User, string, application.TaskFilter) (application.TaskPage, error)
}
type Authentication interface {
	RequireAuthentication(http.Handler) http.Handler
}
type Handler struct {
	service        Service
	authentication Authentication
}

func NewHandler(service Service, authentication Authentication) (*Handler, error) {
	if service == nil || authentication == nil {
		return nil, errors.New("overview HTTP dependencies are required")
	}
	return &Handler{service: service, authentication: authentication}, nil
}
func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/projects/{projectKey}/overview", h.authentication.RequireAuthentication(http.HandlerFunc(h.snapshot)))
	mux.Handle("GET /api/v1/projects/{projectKey}/overview/tasks", h.authentication.RequireAuthentication(http.HandlerFunc(h.tasks)))
}
func (h *Handler) snapshot(w http.ResponseWriter, r *http.Request) {
	user, ok := authhttp.CurrentUser(r.Context())
	if !ok {
		writeError(w, r, projectapplication.ErrForbidden)
		return
	}
	q := r.URL.Query()
	window, err := application.NewWindow(time.Now(), q.Get("timezone"), q.Get("range"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	result, err := h.service.GetSnapshot(r.Context(), user, r.PathValue("projectKey"), window)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	if err := httpserver.WriteJSON(w, http.StatusOK, result); err != nil {
		httpserver.WriteInternalError(w, r, err)
	}
}
func (h *Handler) tasks(w http.ResponseWriter, r *http.Request) {
	user, ok := authhttp.CurrentUser(r.Context())
	if !ok {
		writeError(w, r, projectapplication.ErrForbidden)
		return
	}
	q := r.URL.Query()
	page, size := 1, 20
	var err error
	if q.Has("page") {
		page, err = strconv.Atoi(q.Get("page"))
		if err != nil {
			writeError(w, r, application.ErrInvalidInput)
			return
		}
	}
	if q.Has("pageSize") {
		size, err = strconv.Atoi(q.Get("pageSize"))
		if err != nil {
			writeError(w, r, application.ErrInvalidInput)
			return
		}
	}
	sort, scope := q.Get("sort"), q.Get("scope")
	if sort == "" {
		sort = "recent"
	}
	if scope == "" {
		scope = "all"
	}
	filter := application.TaskFilter{State: q.Get("state"), Scope: scope, Sort: sort, Page: page, PageSize: size}
	if err := filter.Validate(); err != nil {
		writeError(w, r, err)
		return
	}
	result, err := h.service.GetTasks(r.Context(), user, r.PathValue("projectKey"), filter)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	if err := httpserver.WriteListJSON(w, http.StatusOK, result.Items, result.Total); err != nil {
		httpserver.WriteInternalError(w, r, err)
	}
}
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, application.ErrInvalidInput):
		httpserver.WriteError(w, r, httpserver.Error{Status: 400, Code: "invalid_input", Message: "Invalid overview timezone, range or task filter."})
	case errors.Is(err, projectapplication.ErrForbidden):
		httpserver.WriteError(w, r, httpserver.Error{Status: 403, Code: "forbidden", Message: "Project access is required."})
	case errors.Is(err, projectapplication.ErrNotFound):
		httpserver.WriteError(w, r, httpserver.Error{Status: 404, Code: "not_found", Message: "Project not found."})
	default:
		httpserver.WriteInternalError(w, r, err)
	}
}
