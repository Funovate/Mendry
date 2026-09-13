package http

import (
	"context"
	"errors"
	authhttp "mendry/backend/internal/modules/auth/adapter/http"
	authapplication "mendry/backend/internal/modules/auth/application"
	authdomain "mendry/backend/internal/modules/auth/domain"
	"mendry/backend/internal/modules/overview/application"
	projectapplication "mendry/backend/internal/modules/projects/application"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type testAuth struct{ authenticated bool }

func (a testAuth) Login(context.Context, string, []byte, string) (authapplication.LoginResult, error) {
	return authapplication.LoginResult{}, nil
}
func (a testAuth) Logout(context.Context, string) error { return nil }
func (a testAuth) Authenticate(context.Context, string) (authdomain.User, error) {
	if !a.authenticated {
		return authdomain.User{}, authapplication.ErrUnauthenticated
	}
	return authdomain.User{ID: "user", Enabled: true}, nil
}

type testService struct {
	err    error
	calls  int
	key    string
	filter application.TaskFilter
}

func (s *testService) GetSnapshot(_ context.Context, _ authdomain.User, key string, _ application.Window) (application.Snapshot, error) {
	s.calls++
	s.key = key
	return application.Snapshot{}, s.err
}
func (s *testService) GetTasks(_ context.Context, _ authdomain.User, key string, f application.TaskFilter) (application.TaskPage, error) {
	s.calls++
	s.key = key
	s.filter = f
	return application.TaskPage{Items: []application.Task{}, Total: 42}, s.err
}
func TestOverviewHTTPBoundary(t *testing.T) {
	for _, tt := range []struct {
		name, path    string
		authenticated bool
		err           error
		status        int
		calls         int
	}{
		{"unauthenticated", "/overview", false, nil, 401, 0},
		{"invalid zone", "/overview?timezone=Nope", true, nil, 400, 0},
		{"invalid period", "/overview?range=year", true, nil, 400, 0},
		{"invalid page", "/overview/tasks?page=-1", true, nil, 400, 0},
		{"invalid size", "/overview/tasks?pageSize=1000", true, nil, 400, 0},
		{"invalid sort", "/overview/tasks?sort=secret", true, nil, 400, 0},
		{"invalid state", "/overview/tasks?state=a%27", true, nil, 400, 0},
		{"forbidden", "/overview", true, projectapplication.ErrForbidden, 403, 1},
		{"not found", "/overview", true, projectapplication.ErrNotFound, 404, 1},
		{"internal", "/overview", true, errors.New("sensitive internal detail"), 500, 1},
		{"snapshot", "/overview?timezone=UTC&range=7d", true, nil, 200, 1},
		{"tasks", "/overview/tasks?scope=flow&state=future_phase&page=2&sort=tokens", true, nil, 200, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			auth, _ := authhttp.NewHandler(authhttp.HandlerOptions{Service: testAuth{tt.authenticated}, Now: time.Now})
			service := &testService{err: tt.err}
			handler, _ := NewHandler(service, auth)
			mux := http.NewServeMux()
			handler.Register(mux)
			request := httptest.NewRequest("GET", "/api/v1/projects/example"+tt.path, nil)
			request.AddCookie(&http.Cookie{Name: authhttp.SessionCookieName, Value: "test"})
			writer := httptest.NewRecorder()
			mux.ServeHTTP(writer, request)
			if writer.Code != tt.status || service.calls != tt.calls {
				t.Fatalf("status=%d calls=%d body=%s", writer.Code, service.calls, writer.Body.String())
			}
			if strings.Contains(writer.Body.String(), "sensitive internal detail") {
				t.Fatal("leaked cause")
			}
			if tt.status == 200 && service.key != "example" {
				t.Fatal("missing project key")
			}
			if tt.name == "tasks" && (!strings.Contains(writer.Body.String(), `"total":42`) || !strings.Contains(writer.Body.String(), `"data":[]`) || service.filter.Page != 2) {
				t.Fatalf("list contract: %s", writer.Body.String())
			}
		})
	}
}
