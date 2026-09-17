package http

import (
	"errors"
	nethttp "net/http"

	"context"
	authdomain "mendry/backend/internal/modules/auth/domain"
	"mendry/backend/internal/modules/projects/application"
	"mendry/backend/internal/modules/projects/domain"
	"mendry/backend/internal/platform/httpserver"
)

// RegisterHotfixSetup adds short-lived preflight jobs independently of the
// advanced policy endpoint, which remains available for custom environments.
type HotfixSetupService interface {
	Check(context.Context, authdomain.User, string, string) (application.HotfixCheck, error)
	Get(context.Context, authdomain.User, string) (application.HotfixCheck, error)
	Enable(context.Context, authdomain.User, string, string) (domain.RemediationPolicy, error)
}

func (h *Handler) RegisterHotfixSetup(mux *nethttp.ServeMux, setup HotfixSetupService) {
	mux.Handle("POST /api/v1/projects/{projectKey}/configuration/auto-hotfix/check", h.authentication.RequireAuthentication(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		var input struct {
			Directory string `json:"directory"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		principal, ok := currentUser(r)
		if !ok {
			return
		}
		result, err := setup.Check(r.Context(), principal, r.PathValue("projectKey"), input.Directory)
		if err != nil {
			writeHotfixError(w, r, err)
			return
		}
		writeJSON(w, r, nethttp.StatusAccepted, result)
	})))
	mux.Handle("GET /api/v1/projects/{projectKey}/configuration/auto-hotfix/check", h.authentication.RequireAuthentication(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		principal, ok := currentUser(r)
		if !ok {
			return
		}
		result, err := setup.Get(r.Context(), principal, r.PathValue("projectKey"))
		if err != nil {
			writeHotfixError(w, r, err)
			return
		}
		writeJSON(w, r, nethttp.StatusOK, result)
	})))
	mux.Handle("POST /api/v1/projects/{projectKey}/configuration/auto-hotfix/enable", h.authentication.RequireAuthentication(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		var input struct {
			CheckID string `json:"checkId"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		principal, ok := currentUser(r)
		if !ok {
			return
		}
		result, err := setup.Enable(r.Context(), principal, r.PathValue("projectKey"), input.CheckID)
		if err != nil {
			writeHotfixError(w, r, err)
			return
		}
		writeJSON(w, r, nethttp.StatusOK, mapRemediationPolicy(result))
	})))
}

func writeHotfixError(w nethttp.ResponseWriter, r *nethttp.Request, err error) {
	var problem *application.HotfixSetupProblem
	if errors.As(err, &problem) {
		httpserver.WriteError(w, r, httpserver.Error{Status: nethttp.StatusBadRequest, Code: "hotfix_prerequisite", Message: problem.Message})
		return
	}
	if errors.Is(err, application.ErrHotfixUnavailable) {
		httpserver.WriteError(w, r, httpserver.Error{Status: nethttp.StatusServiceUnavailable, Code: "hotfix_unavailable", Message: "Automatic repair infrastructure is not configured. Ask the platform administrator to enable the validation runner."})
		return
	}
	if errors.Is(err, application.ErrConflict) {
		httpserver.WriteError(w, r, httpserver.Error{Status: nethttp.StatusConflict, Code: "hotfix_check_conflict", Message: "The check expired, configuration changed, or another check is active. Check again before enabling."})
		return
	}
	writeApplicationError(w, r, err)
}
