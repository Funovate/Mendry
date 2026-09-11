package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	systemhttp "mendry/backend/internal/modules/system/adapter/http"
	"mendry/backend/internal/modules/system/application"
)

func TestSystemEndpoints(t *testing.T) {
	mux := http.NewServeMux()
	service, err := application.NewService(time.Second)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	systemhttp.NewHandler(service).Register(mux)

	tests := []struct {
		path   string
		status string
	}{
		{path: "/livez", status: "alive"},
		{path: "/readyz", status: "ready"},
		{path: "/api/v1/system/status", status: "ready"},
	}

	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			response := httptest.NewRecorder()

			mux.ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("status = %d", response.Code)
			}
			if contentType := response.Header().Get("Content-Type"); contentType != "application/json" {
				t.Fatalf("Content-Type = %q", contentType)
			}

			var envelope struct {
				Data application.Report `json:"data"`
			}
			if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if envelope.Data.Status != test.status {
				t.Fatalf("Status = %q, want %q", envelope.Data.Status, test.status)
			}
		})
	}
}

func TestReadinessReportsDependencyFailureWithoutErrorDetails(t *testing.T) {
	mux := http.NewServeMux()
	service, err := application.NewService(time.Second, application.Dependency{
		Name: "postgresql",
		Check: func(context.Context) error {
			return errors.New("secret database diagnostic")
		},
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	systemhttp.NewHandler(service).Register(mux)

	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", response.Code)
	}
	if strings.Contains(response.Body.String(), "secret") || !strings.Contains(response.Body.String(), `"postgresql":"unavailable"`) {
		t.Fatalf("body = %q", response.Body.String())
	}
}

func TestSystemEndpointsRejectUnsupportedMethod(t *testing.T) {
	mux := http.NewServeMux()
	service, err := application.NewService(time.Second)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	systemhttp.NewHandler(service).Register(mux)

	request := httptest.NewRequest(http.MethodPost, "/livez", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", response.Code)
	}
}
