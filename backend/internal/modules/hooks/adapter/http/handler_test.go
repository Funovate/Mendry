package http_test

import (
	"context"
	"io"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	hookhttp "fixthe/backend/internal/modules/hooks/adapter/http"
	"fixthe/backend/internal/modules/hooks/application"
	"fixthe/backend/internal/platform/httpserver"
)

type fakeService struct {
	token, raw string
	result     application.Result
	err        error
}

func (f *fakeService) Ingest(_ context.Context, token, raw string) (application.Result, error) {
	f.token, f.raw = token, raw
	return f.result, f.err
}

func TestIngestAcceptsUnauthenticatedPlaintext(t *testing.T) {
	service := &fakeService{result: application.Result{IncidentID: "INC-2049", Created: true}}
	handler := newHandler(t, service)
	request := httptest.NewRequest(nethttp.MethodPost, "/hooks/abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ", strings.NewReader("【告警】测试信息\n触发时间：2026-08-10"))
	request.Header.Set("Content-Type", "text/plain")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusAccepted || !strings.Contains(response.Body.String(), `"incidentId":"INC-2049"`) || !strings.Contains(response.Body.String(), `"created":true`) {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	if service.token != "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ" || !strings.Contains(service.raw, "【告警】测试信息") {
		t.Fatalf("service input token=%q raw=%q", service.token, service.raw)
	}
}

func TestIngestRejectsEmptyBodyAndUnknownToken(t *testing.T) {
	service := &fakeService{err: application.ErrInvalidInput}
	handler := newHandler(t, service)
	request := httptest.NewRequest(nethttp.MethodPost, "/hooks/abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ", strings.NewReader("   "))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusBadRequest || !strings.Contains(response.Body.String(), `"invalid_request"`) {
		t.Fatalf("empty body = %d %q", response.Code, response.Body.String())
	}

	service.err = application.ErrWebhookNotFound
	request = httptest.NewRequest(nethttp.MethodPost, "/hooks/abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ", strings.NewReader("alert"))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusNotFound || !strings.Contains(response.Body.String(), `"webhook_not_found"`) {
		t.Fatalf("unknown token = %d %q", response.Code, response.Body.String())
	}
}

func TestIngestAccessLogUsesRoutePatternNotRawToken(t *testing.T) {
	var output strings.Builder
	service := &fakeService{result: application.Result{IncidentID: "INC-2049", Created: true}}
	handler, err := hookhttp.NewHandler(hookhttp.HandlerOptions{Service: service})
	if err != nil {
		t.Fatal(err)
	}
	mux := nethttp.NewServeMux()
	handler.Register(mux)
	boundary, err := httpserver.Boundary(httpserver.BoundaryOptions{
		Handler: mux, Logger: slog.New(slog.NewJSONHandler(&output, nil)), MaxBodyBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	const token = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ"
	request := httptest.NewRequest(nethttp.MethodPost, "/hooks/"+token, strings.NewReader("alert"))
	request.Header.Set("Content-Type", "text/plain")
	response := httptest.NewRecorder()
	boundary.ServeHTTP(response, request)
	if response.Code != nethttp.StatusAccepted {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	if !strings.Contains(output.String(), `"route":"POST /hooks/{token}"`) || strings.Contains(output.String(), token) {
		t.Fatalf("access log leaked token path: %s", output.String())
	}
}

func newHandler(t *testing.T, service *fakeService) nethttp.Handler {
	t.Helper()
	handler, err := hookhttp.NewHandler(hookhttp.HandlerOptions{Service: service})
	if err != nil {
		t.Fatal(err)
	}
	mux := nethttp.NewServeMux()
	handler.Register(mux)
	boundary, err := httpserver.Boundary(httpserver.BoundaryOptions{Handler: mux, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), MaxBodyBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	return boundary
}
