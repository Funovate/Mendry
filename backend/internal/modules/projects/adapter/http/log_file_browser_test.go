package http_test

import (
	"context"
	"io"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	authhttp "mendry/backend/internal/modules/auth/adapter/http"
	authdomain "mendry/backend/internal/modules/auth/domain"
	projecthttp "mendry/backend/internal/modules/projects/adapter/http"
	"mendry/backend/internal/modules/projects/application"
	"mendry/backend/internal/platform/httpserver"
)

type logFileBrowserHTTPService struct {
	*fakeService
	calls   int
	request application.LogFileBrowseRequest
	err     error
}

func (s *logFileBrowserHTTPService) ProbeSSHLogFiles(_ context.Context, _ authdomain.User, key string, request application.LogFileBrowseRequest) (application.LogFileListing, error) {
	s.calls++
	s.projectKey = key
	s.request = request
	return application.LogFileListing{Directory: "/var/log", Entries: []application.RemoteLogEntry{{Name: "app.log", Path: "/var/log/app.log", Kind: "file", Readable: true}}}, s.err
}

func TestLogFileBrowserHTTPAuthenticationAndResponses(t *testing.T) {
	for _, tc := range []struct {
		name, body    string
		authenticated bool
		err           error
		status        int
		code          string
	}{
		{"list", `{"host":"logs.example.com","port":22,"user":"collector","credentialSecretId":"secret","path":"/var/log"}`, true, nil, 200, "app.log"},
		{"unauthenticated", `{}`, false, nil, 401, ""},
		{"unknown field", `{"command":"ls"}`, true, nil, 400, ""},
		{"missing", `{}`, true, application.ErrLogDirectoryNotFound, 422, "log_directory_not_found"},
		{"permissions", `{}`, true, application.ErrLogDirectoryNotReadable, 422, "log_directory_not_readable"},
		{"unavailable", `{}`, true, application.ErrLogFileBrowseUnavailable, 502, "log_file_browse_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &logFileBrowserHTTPService{fakeService: &fakeService{}, err: tc.err}
			auth, err := authhttp.NewHandler(authhttp.HandlerOptions{Service: &fakeAuthService{user: authdomain.User{ID: "user", Enabled: tc.authenticated}}})
			if err != nil {
				t.Fatal(err)
			}
			handler, err := projecthttp.NewHandler(projecthttp.HandlerOptions{Service: service, Authentication: auth})
			if err != nil {
				t.Fatal(err)
			}
			mux := nethttp.NewServeMux()
			handler.Register(mux)
			boundary, err := httpserver.Boundary(httpserver.BoundaryOptions{Handler: mux, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), MaxBodyBytes: 4096})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(nethttp.MethodPost, "/api/v1/projects/demo/configuration/source/ssh/log-files", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			if tc.authenticated {
				req.AddCookie(sessionCookie())
			}
			rec := httptest.NewRecorder()
			boundary.ServeHTTP(rec, req)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.code) {
				t.Fatalf("response=%d %s", rec.Code, rec.Body.String())
			}
			if tc.status == 400 || tc.status == 401 {
				if service.calls != 0 {
					t.Fatal("invalid request reached service")
				}
			}
			if tc.status == 200 && (service.projectKey != "demo" || service.request.Path != "/var/log" || service.request.CredentialSecretID != "secret") {
				t.Fatalf("invalid request mapping: %+v", service)
			}
		})
	}
}
