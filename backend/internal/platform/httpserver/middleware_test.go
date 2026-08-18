package httpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"fixthe/backend/internal/platform/observability"
)

func TestBoundaryNormalizesMuxErrorsAndRequestID(t *testing.T) {
	logger := testLogger(t, &bytes.Buffer{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	handler, err := Boundary(BoundaryOptions{Handler: mux, Logger: logger, MaxBodyBytes: 1024})
	if err != nil {
		t.Fatalf("Boundary() error = %v", err)
	}

	tests := []struct {
		name   string
		method string
		path   string
		status int
		code   string
	}{
		{name: "not found", method: http.MethodGet, path: "/missing", status: http.StatusNotFound, code: "not_found"},
		{name: "method", method: http.MethodPost, path: "/items", status: http.StatusMethodNotAllowed, code: "method_not_allowed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			request.Header.Set(requestIDHeader, "unsafe request id\nsecret")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code != test.status || response.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("response = %d %q", response.Code, response.Header().Get("Content-Type"))
			}
			requestID := response.Header().Get(requestIDHeader)
			body := decodeErrorResponse(t, response)
			if !validRequestID(requestID) || body.RequestID != requestID || body.Code != test.code {
				t.Fatalf("request ID/code = %q %#v", requestID, body)
			}
			if strings.Contains(response.Body.String(), "secret") {
				t.Fatalf("body leaked request header: %q", response.Body.String())
			}
		})
	}
}

func TestBoundaryPreservesFeatureJSONNotFoundError(t *testing.T) {
	logger := testLogger(t, &bytes.Buffer{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items/{id}", func(writer http.ResponseWriter, request *http.Request) {
		WriteError(writer, request, Error{
			Status: http.StatusNotFound, Code: "item_not_found", Message: "Item was not found.",
		})
	})
	handler, err := Boundary(BoundaryOptions{Handler: mux, Logger: logger, MaxBodyBytes: 1024})
	if err != nil {
		t.Fatalf("Boundary() error = %v", err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/items/42", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d", response.Code)
	}
	if body := decodeErrorResponse(t, response); body.Code != "item_not_found" {
		t.Fatalf("body = %#v", body)
	}
}

func TestBoundarySuccessEnvelopeMatchesRequestIDHeader(t *testing.T) {
	logger := testLogger(t, &bytes.Buffer{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items", func(writer http.ResponseWriter, _ *http.Request) {
		if err := WriteJSON(writer, http.StatusOK, map[string]string{"id": "item-1"}); err != nil {
			t.Errorf("WriteJSON() error = %v", err)
		}
	})
	handler, err := Boundary(BoundaryOptions{Handler: mux, Logger: logger, MaxBodyBytes: 1024})
	if err != nil {
		t.Fatalf("Boundary() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/items", nil)
	request.Header.Set(requestIDHeader, "request-safe-123")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	var body struct {
		Code string `json:"code"`
		Meta struct {
			RequestID  string `json:"requestId"`
			DurationMS int64  `json:"durationMs"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; body = %q", err, response.Body.String())
	}
	if response.Code != http.StatusOK || body.Code != "ok" || body.Meta.RequestID != response.Header().Get(requestIDHeader) || body.Meta.DurationMS < 0 {
		t.Fatalf("status/header/body = %d %q %#v", response.Code, response.Header().Get(requestIDHeader), body)
	}
}

func TestBoundaryLogsPanicDiagnosticsWithoutReturningThem(t *testing.T) {
	var logs bytes.Buffer
	logger := testLogger(t, &logs)
	handler, err := Boundary(BoundaryOptions{
		Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic("sensitive panic value")
		}),
		Logger: logger, MaxBodyBytes: 1024,
	})
	if err != nil {
		t.Fatalf("Boundary() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/panic", nil)
	request.Header.Set(requestIDHeader, "request-safe-123")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", response.Code)
	}
	body := decodeErrorResponse(t, response)
	if body.Code != "internal_error" || body.RequestID != "request-safe-123" {
		t.Fatalf("body = %#v", body)
	}
	if strings.Contains(response.Body.String(), "sensitive") {
		t.Fatal("panic value leaked to response")
	}
	panicRecord := findLogRecord(t, logs.Bytes(), observability.EventHTTPPanicRecovered)
	if panicRecord[observability.FieldPanicType] != "string" || panicRecord[observability.FieldPanicValue] != "sensitive panic value" {
		t.Fatalf("panic record = %#v", panicRecord)
	}
	stack, ok := panicRecord[observability.FieldStack].(string)
	if !ok || !strings.Contains(stack, "TestBoundaryLogsPanicDiagnosticsWithoutReturningThem") {
		t.Fatalf("stack = %#v", panicRecord[observability.FieldStack])
	}
	failed := findLogRecord(t, logs.Bytes(), observability.EventHTTPFailed)
	if failed["level"] != "ERROR" || failed["status"] != float64(http.StatusInternalServerError) {
		t.Fatalf("failed record = %#v", failed)
	}
}

func TestBoundaryLimitsStrictJSONBody(t *testing.T) {
	logger := testLogger(t, &bytes.Buffer{})
	handler, err := Boundary(BoundaryOptions{
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			var input struct {
				Name string `json:"name"`
			}
			if apiError := DecodeJSON(request, &input); apiError != nil {
				WriteError(writer, request, *apiError)
				return
			}
			_ = WriteJSON(writer, http.StatusOK, input)
		}),
		Logger: logger, MaxBodyBytes: 16,
	})
	if err != nil {
		t.Fatalf("Boundary() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/items", strings.NewReader(`{"name":"value-that-is-too-long"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusRequestEntityTooLarge || decodeErrorResponse(t, response).Code != "request_too_large" {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
}

func TestCORSAllowsOnlyConfiguredOrigin(t *testing.T) {
	logger := testLogger(t, &bytes.Buffer{})
	handler, err := Boundary(BoundaryOptions{
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusNoContent)
		}),
		Logger: logger, MaxBodyBytes: 1024, CORSAllowedOrigin: "https://console.example.com",
	})
	if err != nil {
		t.Fatalf("Boundary() error = %v", err)
	}

	allowed := httptest.NewRequest(http.MethodOptions, "/items", nil)
	allowed.Header.Set("Origin", "https://console.example.com")
	allowed.Header.Set("Access-Control-Request-Method", http.MethodPost)
	allowedResponse := httptest.NewRecorder()
	handler.ServeHTTP(allowedResponse, allowed)
	if allowedResponse.Code != http.StatusNoContent ||
		allowedResponse.Header().Get("Access-Control-Allow-Origin") != "https://console.example.com" ||
		allowedResponse.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("allowed response = %d %#v", allowedResponse.Code, allowedResponse.Header())
	}

	denied := httptest.NewRequest(http.MethodGet, "/items", nil)
	denied.Header.Set("Origin", "https://attacker.example.com")
	deniedResponse := httptest.NewRecorder()
	handler.ServeHTTP(deniedResponse, denied)
	if deniedResponse.Code != http.StatusForbidden || decodeErrorResponse(t, deniedResponse).Code != "origin_not_allowed" {
		t.Fatalf("denied response = %d %q", deniedResponse.Code, deniedResponse.Body.String())
	}
}

func TestCORSWithoutConfigurationAllowsSameHostOriginOnly(t *testing.T) {
	logger := testLogger(t, &bytes.Buffer{})
	handler, err := Boundary(BoundaryOptions{
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusNoContent)
		}),
		Logger: logger, MaxBodyBytes: 1024,
	})
	if err != nil {
		t.Fatalf("Boundary() error = %v", err)
	}

	sameHost := httptest.NewRequest(http.MethodPost, "http://api.example.com/items", nil)
	sameHost.Header.Set("Origin", "http://api.example.com")
	sameHostResponse := httptest.NewRecorder()
	handler.ServeHTTP(sameHostResponse, sameHost)
	if sameHostResponse.Code != http.StatusNoContent {
		t.Fatalf("same-host status = %d", sameHostResponse.Code)
	}

	otherHost := httptest.NewRequest(http.MethodPost, "http://api.example.com/items", nil)
	otherHost.Header.Set("Origin", "https://console.example.com")
	otherHostResponse := httptest.NewRecorder()
	handler.ServeHTTP(otherHostResponse, otherHost)
	if otherHostResponse.Code != http.StatusForbidden {
		t.Fatalf("other-host status = %d", otherHostResponse.Code)
	}
}

func TestBoundaryIncludesRequestIDInAccessLog(t *testing.T) {
	var output bytes.Buffer
	logger := testLogger(t, &output)
	handler, err := Boundary(BoundaryOptions{
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusNoContent)
		}),
		Logger: logger, MaxBodyBytes: 1024,
	})
	if err != nil {
		t.Fatalf("Boundary() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/livez", nil)
	request.Header.Set(requestIDHeader, "request-safe-123")
	handler.ServeHTTP(httptest.NewRecorder(), request)

	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if record["request_id"] != "request-safe-123" {
		t.Fatalf("request_id = %#v", record["request_id"])
	}
}
