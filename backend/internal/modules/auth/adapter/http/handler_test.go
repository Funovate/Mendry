package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	authhttp "fixthe/backend/internal/modules/auth/adapter/http"
	"fixthe/backend/internal/modules/auth/application"
	"fixthe/backend/internal/modules/auth/domain"
	"fixthe/backend/internal/platform/httpserver"
	"fixthe/backend/internal/platform/observability"
)

type fakeService struct {
	loginResult application.LoginResult
	loginError  error
	authUser    domain.User
	authError   error
	logoutError error
	oldToken    string
	loggedOut   string
	createdUser domain.User
	createError error
	password    []byte
}

func (f *fakeService) Login(_ context.Context, _ string, _ []byte, oldToken string) (application.LoginResult, error) {
	f.oldToken = oldToken
	return f.loginResult, f.loginError
}

func (f *fakeService) Authenticate(_ context.Context, _ string) (domain.User, error) {
	return f.authUser, f.authError
}

func (f *fakeService) Logout(_ context.Context, token string) error {
	f.loggedOut = token
	return f.logoutError
}

func (f *fakeService) CreateUser(_ context.Context, _ domain.User, username string, password []byte) (domain.User, error) {
	f.password = append([]byte(nil), password...)
	if f.createError != nil {
		return domain.User{}, f.createError
	}
	f.createdUser = domain.User{ID: "user-2", Username: username, Role: domain.RoleViewer, Enabled: true}
	return f.createdUser, nil
}

func TestLoginSetsSecureSessionCookieAndReturnsSafeUser(t *testing.T) {
	now := time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC)
	expiresAt := now.Add(time.Hour)
	service := &fakeService{loginResult: application.LoginResult{
		User:  domain.User{ID: "user-1", Username: "admin", Role: domain.RoleAdmin, Enabled: true},
		Token: "new-session-token", ExpiresAt: expiresAt,
	}}
	handler := newHandlerAt(t, service, true, func() time.Time { return now })
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"secret-password"}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: authhttp.SessionCookieName, Value: "old-session-token"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || service.oldToken != "old-session-token" {
		t.Fatalf("status = %d, old token = %q", response.Code, service.oldToken)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != authhttp.SessionCookieName || cookies[0].Value != "new-session-token" ||
		!cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteLaxMode || cookies[0].Path != "/" || cookies[0].MaxAge != 3600 {
		t.Fatalf("cookies = %#v", cookies)
	}
	if strings.Contains(response.Body.String(), "secret-password") || strings.Contains(response.Body.String(), "new-session-token") ||
		!strings.Contains(response.Body.String(), `"role":"admin"`) {
		t.Fatalf("body = %q", response.Body.String())
	}
}

func TestLoginMapsInvalidCredentialsWithoutAccountDisclosure(t *testing.T) {
	service := &fakeService{loginError: application.ErrInvalidCredentials}
	handler := newHandler(t, service, false)
	for _, username := range []string{"known", "unknown"} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"`+username+`","password":"wrong-password"}`))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), `"code":"invalid_credentials"`) {
			t.Fatalf("%s response = %d %q", username, response.Code, response.Body.String())
		}
	}
}

func TestCurrentUserRequiresSessionAndReturnsPrincipal(t *testing.T) {
	service := &fakeService{authError: application.ErrUnauthenticated}
	handler := newHandler(t, service, false)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil))
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), `"authentication_required"`) {
		t.Fatalf("unauthenticated response = %d %q", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}

	service.authError = nil
	service.authUser = domain.User{ID: "user-2", Username: "viewer", Role: domain.RoleViewer, Enabled: true}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	request.AddCookie(&http.Cookie{Name: authhttp.SessionCookieName, Value: "valid-token"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("body = %#v, error = %v", body, err)
	}
	data, ok := body["data"].(map[string]any)
	if !ok || data["username"] != "viewer" || body["code"] != "ok" || body["message"] != "OK" {
		t.Fatalf("body = %#v", body)
	}
}

func TestAuthenticationUnknownErrorIsLoggedButNotReturned(t *testing.T) {
	var logs bytes.Buffer
	root := errors.New("Redis session JSON is corrupted")
	service := &fakeService{authError: fmt.Errorf("load login session: %w", root)}
	authHandler, err := authhttp.NewHandler(authhttp.HandlerOptions{Service: service})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	mux := http.NewServeMux()
	authHandler.Register(mux)
	boundary, err := httpserver.Boundary(httpserver.BoundaryOptions{
		Handler: mux, Logger: slog.New(slog.NewJSONHandler(&logs, nil)), MaxBodyBytes: 1024,
	})
	if err != nil {
		t.Fatalf("Boundary() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	request.AddCookie(&http.Cookie{Name: authhttp.SessionCookieName, Value: "valid-token"})
	response := httptest.NewRecorder()
	boundary.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), `"code":"internal_error"`) {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	for _, forbidden := range []string{"load login session", root.Error()} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, response.Body.String())
		}
	}
	if !strings.Contains(logs.String(), observability.EventHTTPError) || !strings.Contains(logs.String(), root.Error()) ||
		!strings.Contains(logs.String(), "load login session") {
		t.Fatalf("logs do not contain original cause: %s", logs.String())
	}
}

func TestLogoutIsIdempotentAndClearsCookie(t *testing.T) {
	service := &fakeService{}
	handler := newHandler(t, service, true)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: authhttp.SessionCookieName, Value: "valid-token"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || service.loggedOut != "valid-token" {
		t.Fatalf("response = %d, logged out = %q", response.Code, service.loggedOut)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].MaxAge != -1 || !cookies[0].HttpOnly || !cookies[0].Secure {
		t.Fatalf("cookies = %#v", cookies)
	}
}

func TestRoleMiddlewareDeniesViewerAndAllowsOperator(t *testing.T) {
	service := &fakeService{authUser: domain.User{ID: "user-1", Username: "viewer", Role: domain.RoleViewer, Enabled: true}}
	authHandler, err := authhttp.NewHandler(authhttp.HandlerOptions{Service: service})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	protected := authHandler.RequireRoles([]domain.Role{domain.RoleAdmin, domain.RoleOperator}, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodPost, "/protected", nil)
	request.AddCookie(&http.Cookie{Name: authhttp.SessionCookieName, Value: "valid-token"})
	response := httptest.NewRecorder()
	protected.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("viewer status = %d", response.Code)
	}

	service.authUser.Role = domain.RoleOperator
	response = httptest.NewRecorder()
	protected.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("operator status = %d", response.Code)
	}
}

func TestSystemAdminCreatesLocalUserWithoutReturningPassword(t *testing.T) {
	service := &fakeService{authUser: domain.User{ID: "admin-1", Username: "admin", Role: domain.RoleAdmin, Enabled: true}}
	handler := newHandler(t, service, false)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/users", strings.NewReader(`{"username":"operator.user","password":"long-enough-password"}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: authhttp.SessionCookieName, Value: "valid-token"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated || service.createdUser.Username != "operator.user" || string(service.password) != "long-enough-password" {
		t.Fatalf("response = %d %q, created = %#v", response.Code, response.Body.String(), service.createdUser)
	}
	if strings.Contains(response.Body.String(), "long-enough-password") || !strings.Contains(response.Body.String(), `"role":"viewer"`) {
		t.Fatalf("body = %q", response.Body.String())
	}

	service.authUser.Role = domain.RoleViewer
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("viewer status = %d, body = %q", response.Code, response.Body.String())
	}
}

func TestCreateLocalUserMapsValidationAndConflict(t *testing.T) {
	service := &fakeService{authUser: domain.User{ID: "admin-1", Role: domain.RoleAdmin, Enabled: true}}
	handler := newHandler(t, service, false)
	for _, test := range []struct {
		err    error
		status int
		code   string
	}{
		{err: application.ErrInvalidInput, status: http.StatusBadRequest, code: "invalid_request"},
		{err: application.ErrUserConflict, status: http.StatusConflict, code: "user_conflict"},
	} {
		service.createError = test.err
		request := httptest.NewRequest(http.MethodPost, "/api/v1/users", strings.NewReader(`{"username":"operator.user","password":"long-enough-password"}`))
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(&http.Cookie{Name: authhttp.SessionCookieName, Value: "valid-token"})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status || !strings.Contains(response.Body.String(), `"code":"`+test.code+`"`) {
			t.Fatalf("error %v response = %d %q", test.err, response.Code, response.Body.String())
		}
	}
}

func newHandler(t *testing.T, service *fakeService, secure bool) http.Handler {
	return newHandlerAt(t, service, secure, time.Now)
}

func newHandlerAt(t *testing.T, service *fakeService, secure bool, now func() time.Time) http.Handler {
	t.Helper()
	authHandler, err := authhttp.NewHandler(authhttp.HandlerOptions{Service: service, UserCreator: service, SecureCookie: secure, Now: now})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	mux := http.NewServeMux()
	authHandler.Register(mux)
	boundary, err := httpserver.Boundary(httpserver.BoundaryOptions{
		Handler: mux, Logger: testLogger(), MaxBodyBytes: 1024,
	})
	if err != nil {
		t.Fatalf("Boundary() error = %v", err)
	}
	return boundary
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
