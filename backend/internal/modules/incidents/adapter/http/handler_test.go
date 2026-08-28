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

	authhttp "fixthe/backend/internal/modules/auth/adapter/http"
	authapplication "fixthe/backend/internal/modules/auth/application"
	authdomain "fixthe/backend/internal/modules/auth/domain"
	incidenthttp "fixthe/backend/internal/modules/incidents/adapter/http"
	"fixthe/backend/internal/modules/incidents/application"
	"fixthe/backend/internal/modules/incidents/domain"
	projectapplication "fixthe/backend/internal/modules/projects/application"
	"fixthe/backend/internal/platform/httpserver"
)

type fakeIncidentService struct {
	incidents   []domain.Incident
	error       error
	limit       int32
	identifier  string
	status      string
	projectKey  string
	createInput application.CreateInput
}

func (f *fakeIncidentService) List(_ context.Context, _ authdomain.User, projectKey string, limit int32) (application.ListResult, error) {
	f.projectKey, f.limit = projectKey, limit
	return application.ListResult{Items: f.incidents, Total: int64(len(f.incidents))}, f.error
}

func (f *fakeIncidentService) Get(_ context.Context, _ authdomain.User, projectKey, identifier string) (domain.Incident, error) {
	f.projectKey, f.identifier = projectKey, identifier
	return firstIncident(f.incidents), f.error
}

func (f *fakeIncidentService) Create(_ context.Context, _ authdomain.User, projectKey string, input application.CreateInput) (domain.Incident, error) {
	f.projectKey, f.createInput = projectKey, input
	return firstIncident(f.incidents), f.error
}

func (f *fakeIncidentService) UpdateStatus(_ context.Context, _ authdomain.User, projectKey, identifier, status string) (domain.Incident, error) {
	f.projectKey, f.identifier, f.status = projectKey, identifier, status
	return firstIncident(f.incidents), f.error
}

type fakeAuthService struct {
	user authdomain.User
}

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

func TestIncidentRoutesRequireAuthenticationAndRoles(t *testing.T) {
	service := &fakeIncidentService{incidents: []domain.Incident{testIncident()}}
	authService := &fakeAuthService{}
	handler := newHandler(t, service, authService)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(nethttp.MethodGet, "/api/v1/projects/payments/incidents", nil))
	if response.Code != nethttp.StatusUnauthorized || !strings.Contains(response.Body.String(), `"authentication_required"`) {
		t.Fatalf("unauthenticated response = %d %q", response.Code, response.Body.String())
	}

	authService.user = authdomain.User{Enabled: true, Role: authdomain.RoleViewer}
	request := jsonRequest(nethttp.MethodPost, "/api/v1/projects/payments/incidents", `{"title":"title"}`)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusCreated || service.projectKey != "payments" {
		t.Fatalf("authenticated route response = %d, project = %q", response.Code, service.projectKey)
	}

	request = httptest.NewRequest(nethttp.MethodGet, "/api/v1/incidents", nil)
	request.AddCookie(&nethttp.Cookie{Name: authhttp.SessionCookieName, Value: "valid"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusNotFound {
		t.Fatalf("legacy global route status = %d", response.Code)
	}
}

func TestListReturnsEnvelopeWithTotalAndValidatesLimit(t *testing.T) {
	service := &fakeIncidentService{incidents: []domain.Incident{testIncident()}}
	handler := newHandler(t, service, &fakeAuthService{user: authdomain.User{Enabled: true, Role: authdomain.RoleViewer}})
	request := httptest.NewRequest(nethttp.MethodGet, "/api/v1/projects/payments/incidents?limit=25", nil)
	request.AddCookie(&nethttp.Cookie{Name: authhttp.SessionCookieName, Value: "valid"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusOK || service.limit != 25 || service.projectKey != "payments" {
		t.Fatalf("response = %d %q, limit = %d", response.Code, response.Body.String(), service.limit)
	}
	var body struct {
		Code    string           `json:"code"`
		Message string           `json:"message"`
		Data    []map[string]any `json:"data"`
		Meta    struct {
			Total int64 `json:"total"`
		} `json:"meta"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil || body.Code != "ok" || body.Message != "OK" || len(body.Data) != 1 || body.Data[0]["id"] != "INC-2049" || body.Meta.Total != 1 {
		t.Fatalf("body = %#v, error = %v", body, err)
	}
	if _, exists := body.Data[0]["internalId"]; exists {
		t.Fatalf("response leaked internal ID: %#v", body.Data[0])
	}

	for _, query := range []string{"limit=101", "limit=", "limit=10&limit=20", "cursor=1", "limit=%ZZ"} {
		request = httptest.NewRequest(nethttp.MethodGet, "/api/v1/projects/payments/incidents?"+query, nil)
		request.AddCookie(&nethttp.Cookie{Name: authhttp.SessionCookieName, Value: "valid"})
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != nethttp.StatusBadRequest || !strings.Contains(response.Body.String(), `"invalid_request"`) {
			t.Errorf("query %q response = %d %q", query, response.Code, response.Body.String())
		}
	}
}

func TestCreateUsesStrictJSONAndReturnsLocation(t *testing.T) {
	service := &fakeIncidentService{incidents: []domain.Incident{testIncident()}}
	handler := newHandler(t, service, &fakeAuthService{user: authdomain.User{Enabled: true, Role: authdomain.RoleOperator}})
	request := jsonRequest(nethttp.MethodPost, "/api/v1/projects/payments/incidents", `{"title":"Database latency","fingerprint":"pg:latency","sourceId":"019ff544-405c-7d23-9f10-cb3fc579605c","unknown":true}`)
	request.AddCookie(&nethttp.Cookie{Name: authhttp.SessionCookieName, Value: "valid"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusBadRequest {
		t.Fatalf("unknown field response = %d %q", response.Code, response.Body.String())
	}

	request = jsonRequest(nethttp.MethodPost, "/api/v1/projects/payments/incidents", `{"title":"Database latency","fingerprint":"pg:latency","priority":"P2","sourceId":"019ff544-405c-7d23-9f10-cb3fc579605c"}`)
	request.AddCookie(&nethttp.Cookie{Name: authhttp.SessionCookieName, Value: "valid"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusCreated || response.Header().Get("Location") != "/api/v1/projects/payments/incidents/INC-2049" || service.createInput.Priority != "P2" || service.createInput.SourceID == "" {
		t.Fatalf("create response = %d %q, location = %q, input = %#v", response.Code, response.Body.String(), response.Header().Get("Location"), service.createInput)
	}
}

func TestGetUpdateAndApplicationErrorMapping(t *testing.T) {
	service := &fakeIncidentService{incidents: []domain.Incident{testIncident()}}
	handler := newHandler(t, service, &fakeAuthService{user: authdomain.User{Enabled: true, Role: authdomain.RoleAdmin}})
	request := httptest.NewRequest(nethttp.MethodGet, "/api/v1/projects/payments/incidents/INC-2049", nil)
	request.AddCookie(&nethttp.Cookie{Name: authhttp.SessionCookieName, Value: "valid"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusOK || service.identifier != "INC-2049" {
		t.Fatalf("get response = %d %q, identifier = %q", response.Code, response.Body.String(), service.identifier)
	}

	request = jsonRequest(nethttp.MethodPatch, "/api/v1/projects/payments/incidents/INC-2049/status", `{"status":"Closed"}`)
	request.AddCookie(&nethttp.Cookie{Name: authhttp.SessionCookieName, Value: "valid"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusOK || service.status != "Closed" {
		t.Fatalf("update response = %d %q, status = %q", response.Code, response.Body.String(), service.status)
	}

	for _, test := range []struct {
		error error
		code  int
		body  string
	}{
		{error: application.ErrInvalidInput, code: nethttp.StatusBadRequest, body: "invalid_request"},
		{error: application.ErrNotFound, code: nethttp.StatusNotFound, body: "incident_not_found"},
		{error: application.ErrConflict, code: nethttp.StatusConflict, body: "incident_conflict"},
		{error: projectapplication.ErrNotFound, code: nethttp.StatusNotFound, body: "project_not_found"},
		{error: io.ErrUnexpectedEOF, code: nethttp.StatusInternalServerError, body: "internal_error"},
	} {
		service.error = test.error
		request = httptest.NewRequest(nethttp.MethodGet, "/api/v1/projects/payments/incidents/INC-9999", nil)
		request.AddCookie(&nethttp.Cookie{Name: authhttp.SessionCookieName, Value: "valid"})
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.code || !strings.Contains(response.Body.String(), test.body) || strings.Contains(response.Body.String(), test.error.Error()) {
			t.Errorf("error %v response = %d %q", test.error, response.Code, response.Body.String())
		}
	}
}

func newHandler(t *testing.T, service *fakeIncidentService, authService *fakeAuthService) nethttp.Handler {
	t.Helper()
	authHandler, err := authhttp.NewHandler(authhttp.HandlerOptions{Service: authService})
	if err != nil {
		t.Fatalf("auth NewHandler() error = %v", err)
	}
	incidentHandler, err := incidenthttp.NewHandler(incidenthttp.HandlerOptions{Service: service, Authentication: authHandler})
	if err != nil {
		t.Fatalf("incident NewHandler() error = %v", err)
	}
	mux := nethttp.NewServeMux()
	incidentHandler.Register(mux)
	boundary, err := httpserver.Boundary(httpserver.BoundaryOptions{
		Handler: mux, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), MaxBodyBytes: 1024,
	})
	if err != nil {
		t.Fatalf("Boundary() error = %v", err)
	}
	return boundary
}

func jsonRequest(method, target, body string) *nethttp.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	return request
}

func firstIncident(incidents []domain.Incident) domain.Incident {
	if len(incidents) == 0 {
		return domain.Incident{}
	}
	return incidents[0]
}

func testIncident() domain.Incident {
	now := time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC)
	return domain.Incident{
		InternalID: "019ff544-405c-7d11-9f10-cb3fc579605c", ProjectID: "019ff544-405c-7d21-9f10-cb3fc579605c",
		EnvironmentID: "019ff544-405c-7d22-9f10-cb3fc579605c", SourceID: "019ff544-405c-7d23-9f10-cb3fc579605c", Number: 2049,
		Title: "Database latency", Fingerprint: "pg:latency", Status: domain.StatusOpen,
		Priority: domain.PriorityP2, Source: "monitor", FirstSeen: now, LastSeen: now,
		OccurrenceCount: 2, HostCount: 1, NotificationSummary: "Lifecycle default", LifecycleGeneration: 1, Version: 1,
		CreatedAt: now, UpdatedAt: now,
	}
}
