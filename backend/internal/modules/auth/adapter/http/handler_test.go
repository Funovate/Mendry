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

	authhttp "mendry/backend/internal/modules/auth/adapter/http"
	"mendry/backend/internal/modules/auth/application"
	"mendry/backend/internal/modules/auth/domain"
	"mendry/backend/internal/platform/httpserver"
	"mendry/backend/internal/platform/observability"
)

type fakeService struct {
	loginResult application.LoginResult
	loginError  error
	authUser    domain.User
	authError   error
	logoutError error
	oldToken    string
	authToken   string
	loggedOut   string
	createdUser domain.User
	createError error
	password    []byte
}

func (f *fakeService) Login(_ context.Context, _ string, _ []byte, oldToken string) (application.LoginResult, error) {
	f.oldToken = oldToken
	return f.loginResult, f.loginError
}

func (f *fakeService) Authenticate(_ context.Context, token string) (domain.User, error) {
	f.authToken = token
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
	f.createdUser = domain.User{ID: "user-2", Username: username, Enabled: true}
	return f.createdUser, nil
}

func TestLoginSetsSecureSessionCookieAndReturnsSafeUser(t *testing.T) {
	now := time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC)
	expiresAt := now.Add(time.Hour)
	service := &fakeService{loginResult: application.LoginResult{
		User:  domain.User{ID: "user-1", Username: "admin", Enabled: true},
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
	if len(cookies) != 2 || cookies[0].Name != authhttp.SessionCookieName || cookies[0].Value != "new-session-token" ||
		!cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteLaxMode || cookies[0].Path != "/" || cookies[0].MaxAge != 3600 ||
		cookies[1].Name != "fixthe_session" || cookies[1].MaxAge != -1 {
		t.Fatalf("cookies = %#v", cookies)
	}
	if strings.Contains(response.Body.String(), "secret-password") || strings.Contains(response.Body.String(), "new-session-token") ||
		!strings.Contains(response.Body.String(), `"username":"admin"`) || strings.Contains(response.Body.String(), `"role"`) {
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
	service.authUser = domain.User{ID: "user-2", Username: "viewer", Enabled: true}
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
	if !ok || data["username"] != "viewer" || body["code"] != "ok" || body["message"] != "OK" || service.authToken != "valid-token" {
		t.Fatalf("body = %#v, auth token = %q", body, service.authToken)
	}

	legacyRequest := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	legacyRequest.AddCookie(&http.Cookie{Name: "fixthe_session", Value: "legacy-token"})
	legacyResponse := httptest.NewRecorder()
	handler.ServeHTTP(legacyResponse, legacyRequest)
	if legacyResponse.Code != http.StatusOK || service.authToken != "legacy-token" {
		t.Fatalf("legacy response = %d, auth token = %q", legacyResponse.Code, service.authToken)
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
	if len(cookies) != 2 || cookies[0].MaxAge != -1 || cookies[1].Name != "fixthe_session" || cookies[1].MaxAge != -1 ||
		!cookies[0].HttpOnly || !cookies[0].Secure || !cookies[1].HttpOnly || !cookies[1].Secure {
		t.Fatalf("cookies = %#v", cookies)
	}
}

func newHandler(t *testing.T, service *fakeService, secure bool) http.Handler {
	return newHandlerAt(t, service, secure, time.Now)
}

func newHandlerAt(t *testing.T, service *fakeService, secure bool, now func() time.Time) http.Handler {
	t.Helper()
	authHandler, err := authhttp.NewHandler(authhttp.HandlerOptions{Service: service, SecureCookie: secure, Now: now})
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
