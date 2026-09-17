package http_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	authhttp "mendry/backend/internal/modules/auth/adapter/http"
	authapplication "mendry/backend/internal/modules/auth/application"
	authdomain "mendry/backend/internal/modules/auth/domain"
	projecthttp "mendry/backend/internal/modules/projects/adapter/http"
	"mendry/backend/internal/modules/projects/application"
	"mendry/backend/internal/modules/projects/domain"
	"mendry/backend/internal/platform/httpserver"
)

type hotfixAuthFake struct{ fakeAuthService }

func (f *hotfixAuthFake) Authenticate(ctx context.Context, token string) (authdomain.User, error) {
	if token != "valid" {
		return authdomain.User{}, authapplication.ErrUnauthenticated
	}
	return f.fakeAuthService.Authenticate(ctx, token)
}

type hotfixHTTPFake struct {
	calls                   int
	key, directory, checkID string
	err                     error
}

func (f *hotfixHTTPFake) Check(_ context.Context, _ authdomain.User, key, directory string) (application.HotfixCheck, error) {
	f.calls++
	f.key = key
	f.directory = directory
	return application.HotfixCheck{ID: "check-1", Status: "checking", Candidates: []application.HotfixCandidate{}}, f.err
}
func (f *hotfixHTTPFake) Get(_ context.Context, _ authdomain.User, key string) (application.HotfixCheck, error) {
	f.calls++
	f.key = key
	return application.HotfixCheck{ID: "check-1", Status: "ready", Candidates: []application.HotfixCandidate{}}, f.err
}
func (f *hotfixHTTPFake) Enable(_ context.Context, _ authdomain.User, key, id string) (domain.RemediationPolicy, error) {
	f.calls++
	f.key = key
	f.checkID = id
	return domain.DefaultRemediationPolicy(), f.err
}
func hotfixHTTPHandler(t *testing.T, f *hotfixHTTPFake) http.Handler {
	t.Helper()
	auth, err := authhttp.NewHandler(authhttp.HandlerOptions{Service: &hotfixAuthFake{fakeAuthService{user: authdomain.User{ID: "user", Enabled: true}}}})
	if err != nil {
		t.Fatal(err)
	}
	h, err := projecthttp.NewHandler(projecthttp.HandlerOptions{Service: &fakeService{}, Authentication: auth})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.RegisterHotfixSetup(mux, f)
	boundary, err := httpserver.Boundary(httpserver.BoundaryOptions{Handler: mux, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), MaxBodyBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	return boundary
}
func TestHotfixHTTPAuthAndScopedPayloads(t *testing.T) {
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{{"POST", "check", `{"directory":"backend"}`, 202}, {"GET", "check", "", 200}, {"POST", "enable", `{"checkId":"check-1"}`, 200}} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			f := &hotfixHTTPFake{}
			h := hotfixHTTPHandler(t, f)
			request := func(auth bool) *httptest.ResponseRecorder {
				r := httptest.NewRequest(tc.method, "/api/v1/projects/demo/configuration/auto-hotfix/"+tc.path, strings.NewReader(tc.body))
				if tc.body != "" {
					r.Header.Set("Content-Type", "application/json")
				}
				if auth {
					r.AddCookie(sessionCookie())
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			if w := request(false); w.Code != 401 || f.calls != 0 {
				t.Fatalf("anonymous=%d calls=%d", w.Code, f.calls)
			}
			w := request(true)
			if w.Code != tc.status || f.key != "demo" || !strings.Contains(w.Body.String(), `"data":`) {
				t.Fatalf("response=%d %s", w.Code, w.Body.String())
			}
			if tc.path == "enable" && f.checkID != "check-1" {
				t.Fatal("lost check identity")
			}
			if tc.method == "POST" && tc.path == "check" && f.directory != "backend" {
				t.Fatal("lost service selection")
			}
		})
	}
}
func TestHotfixHTTPErrorsAreActionableAndDoNotLeak(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{{application.ErrHotfixUnavailable, 503, "hotfix_unavailable"}, {application.ErrConflict, 409, "hotfix_check_conflict"}, {&application.HotfixSetupProblem{Message: "Connect a Git credential first."}, 400, "hotfix_prerequisite"}, {errors.New("private-token-value"), 500, "internal_error"}} {
		t.Run(tc.code, func(t *testing.T) {
			f := &hotfixHTTPFake{err: tc.err}
			h := hotfixHTTPHandler(t, f)
			r := httptest.NewRequest("POST", "/api/v1/projects/demo/configuration/auto-hotfix/check", strings.NewReader("{}"))
			r.Header.Set("Content-Type", "application/json")
			r.AddCookie(sessionCookie())
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) || strings.Contains(w.Body.String(), "private-token-value") {
				t.Fatalf("response=%d %s", w.Code, w.Body.String())
			}
		})
	}
}
