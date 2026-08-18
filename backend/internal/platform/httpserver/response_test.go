package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWriteJSONUsesSuccessEnvelopeAndRequestMetadata(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/items", nil)
	request = request.WithContext(context.WithValue(request.Context(), requestIDContextKey{}, "request-safe-123"))
	response := httptest.NewRecorder()
	response.Header().Set(requestIDHeader, "request-safe-123")

	if err := WriteJSON(response, http.StatusOK, map[string]string{"id": "item-1"}); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}

	var body struct {
		Code    string            `json:"code"`
		Message string            `json:"message"`
		Data    map[string]string `json:"data"`
		Meta    struct {
			RequestID  string `json:"requestId"`
			DurationMS int64  `json:"durationMs"`
			Total      *int64 `json:"total"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; body = %q", err, response.Body.String())
	}
	if body.Code != "ok" || body.Message != "OK" || body.Data["id"] != "item-1" {
		t.Fatalf("body = %#v", body)
	}
	if body.Meta.RequestID != "request-safe-123" || body.Meta.DurationMS < 0 || body.Meta.Total != nil {
		t.Fatalf("meta = %#v", body.Meta)
	}
}

func TestWriteListJSONIncludesServerTotalAndEmptyArray(t *testing.T) {
	response := httptest.NewRecorder()
	if err := WriteListJSON(response, http.StatusOK, []string{}, 42); err != nil {
		t.Fatalf("WriteListJSON() error = %v", err)
	}

	var body struct {
		Code string   `json:"code"`
		Data []string `json:"data"`
		Meta struct {
			Total int64 `json:"total"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; body = %q", err, response.Body.String())
	}
	if body.Code != "ok" || body.Data == nil || len(body.Data) != 0 || body.Meta.Total != 42 {
		t.Fatalf("body = %#v", body)
	}
}

func TestWriteJSONLeavesNoBodyFor204(t *testing.T) {
	response := httptest.NewRecorder()
	if err := WriteJSON(response, http.StatusNoContent, map[string]string{"ignored": "body"}); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}
	if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
}

func TestResponseMetadataDurationIsNonNegative(t *testing.T) {
	response := httptest.NewRecorder()
	metadata := &requestMetadataWriter{ResponseWriter: response, requestID: "request-safe-123", started: time.Now().Add(time.Second)}
	if err := WriteJSON(metadata, http.StatusOK, nil); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}
	var body struct {
		Meta struct {
			DurationMS int64 `json:"durationMs"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if body.Meta.DurationMS < 0 {
		t.Fatalf("durationMs = %d", body.Meta.DurationMS)
	}
}

func TestWriteInternalErrorKeepsDependencyCancellationAsInternalFailure(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/items", nil)
	response := httptest.NewRecorder()

	WriteInternalError(response, request, fmt.Errorf("load item: %w", context.Canceled))

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", response.Code)
	}
	body := decodeErrorResponse(t, response)
	if body.Code != "internal_error" || body.Message != "An internal error occurred." {
		t.Fatalf("body = %#v", body)
	}
}

func TestDecodeJSONAcceptsOneStrictObject(t *testing.T) {
	var body struct {
		Name string `json:"name"`
	}
	request := httptest.NewRequest(http.MethodPost, "/items", strings.NewReader(`{"name":"demo"}`))
	request.Header.Set("Content-Type", "application/json; charset=utf-8")

	if apiError := DecodeJSON(request, &body); apiError != nil {
		t.Fatalf("DecodeJSON() error = %#v", apiError)
	}
	if body.Name != "demo" {
		t.Fatalf("Name = %q", body.Name)
	}
}

func TestDecodeJSONRejectsInvalidBodies(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		status      int
		code        string
	}{
		{name: "media type", contentType: "text/plain", body: `{}`, status: http.StatusUnsupportedMediaType, code: "unsupported_media_type"},
		{name: "empty", contentType: "application/json", status: http.StatusBadRequest, code: "invalid_request"},
		{name: "syntax", contentType: "application/json", body: `{`, status: http.StatusBadRequest, code: "invalid_request"},
		{name: "unknown field", contentType: "application/json", body: `{"extra":true}`, status: http.StatusBadRequest, code: "invalid_request"},
		{name: "multiple values", contentType: "application/json", body: `{} {}`, status: http.StatusBadRequest, code: "invalid_request"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var destination struct {
				Name string `json:"name"`
			}
			request := httptest.NewRequest(http.MethodPost, "/items", strings.NewReader(test.body))
			request.Header.Set("Content-Type", test.contentType)
			apiError := DecodeJSON(request, &destination)
			if apiError == nil || apiError.Status != test.status || apiError.Code != test.code {
				t.Fatalf("DecodeJSON() error = %#v", apiError)
			}
		})
	}
}

func decodeErrorResponse(t *testing.T, response *httptest.ResponseRecorder) errorBody {
	t.Helper()
	var envelope errorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; body = %q", err, response.Body.String())
	}
	return envelope.Error
}
