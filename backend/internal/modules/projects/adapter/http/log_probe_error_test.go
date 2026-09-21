package http

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mendry/backend/internal/modules/projects/application"
)

func TestLogProbeSetupErrorResponse(t *testing.T) {
	for _, reason := range []string{"python_unavailable", "python_unsupported", "systemd_unavailable", "probe_user_unavailable", "service_not_ready"} {
		t.Run(reason, func(t *testing.T) {
			err := fmt.Errorf("%w: %w", application.ErrLogProbeUnavailable, &application.LogProbeSetupError{Reason: reason, Cause: fmt.Errorf("private SSH stderr")})
			recorder := httptest.NewRecorder()
			writeApplicationError(recorder, httptest.NewRequest(http.MethodPost, "/", nil), err)
			if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), reason) || strings.Contains(recorder.Body.String(), "private SSH stderr") {
				t.Fatalf("unexpected response: %d %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestLogProbePathErrorResponse(t *testing.T) {
	for _, reason := range []string{"log_path_not_found", "log_path_not_file", "log_path_not_readable"} {
		t.Run(reason, func(t *testing.T) {
			pathError := &application.LogProbePathError{Reason: reason, Path: "/var/log/app.log", User: "collector", Cause: fmt.Errorf("private SSH stderr")}
			err := fmt.Errorf("%w: %w", application.ErrLogProbeUnavailable, pathError)
			recorder := httptest.NewRecorder()
			writeApplicationError(recorder, httptest.NewRequest(http.MethodPost, "/", nil), err)
			if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), reason) || !strings.Contains(recorder.Body.String(), "/var/log/app.log") || strings.Contains(recorder.Body.String(), "private SSH stderr") {
				t.Fatalf("unexpected response: %d %s", recorder.Code, recorder.Body.String())
			}
		})
	}
	recorder := httptest.NewRecorder()
	writeApplicationError(recorder, httptest.NewRequest(http.MethodPost, "/", nil), application.ErrLogProbeUnavailable)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("generic failure status = %d", recorder.Code)
	}
}
