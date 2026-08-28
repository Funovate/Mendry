package tencentcls_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"fixthe/backend/internal/modules/hooks/adapter/tencentcls"
	hooksapplication "fixthe/backend/internal/modules/hooks/application"
	"fixthe/backend/internal/platform/observability"
)

func TestResolveLogsOriginalDetailResponseAndFailureReason(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	client, err := tencentcls.NewClient(tencentcls.Options{
		Logger: logger,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.Method == http.MethodGet {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/html"}},
					Body:       io.NopCloser(strings.NewReader(`<script>window.detail={"RecordId":"record-log"}</script>`)),
					Request:    request,
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
				Body:       io.NopCloser(strings.NewReader(`<html>upstream detail diagnostic body</html>`)),
				Request:    request,
			}, nil
		})},
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	callback, err := hooksapplication.ParseTencentCLSCallback(
		`{"TopicId":"topic-1","DetailUrl":"https://alarm.cls.tencentcs.com/alert-1"}`,
		"application/json",
	)
	if err != nil {
		t.Fatalf("ParseTencentCLSCallback: %v", err)
	}
	_, err = client.Resolve(context.Background(), callback)
	if !errors.Is(err, tencentcls.ErrDetailInvalid) {
		t.Fatalf("Resolve error = %v, want invalid detail response", err)
	}
	text := output.String()
	for _, want := range []string{
		observability.EventTencentCLSRequestCompleted,
		observability.EventTencentCLSDetailCompleted,
		`"operation":"detail_page"`,
		`"operation":"get_alert_detail"`,
		`"http.method":"POST"`,
		`"http.url":"https://alarm.cls.tencentcs.com/cls_no_login?action=GetAlertDetail"`,
		"get_alert_detail response body omitted: invalid JSON",
		"get_alert_detail.response_media_type",
		"expected response media type",
		`"error_code":"invalid"`,
		`"retryable":false`,
		`"topic_id":"topic-1"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("log missing %q: %s", want, text)
		}
	}
	if strings.Count(text, observability.EventTencentCLSRequestCompleted) != 2 {
		t.Fatalf("request log count = %d, logs: %s", strings.Count(text, observability.EventTencentCLSRequestCompleted), text)
	}
	if strings.Contains(text, "upstream detail diagnostic body") {
		t.Fatalf("log retained malformed response body: %s", text)
	}
	if strings.Contains(text, `"error_message":"invalid"`) {
		t.Fatalf("completion log discarded original error: %s", text)
	}
}

func TestResolveLogsPlaceholderForMalformedDetailText(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	client, err := tencentcls.NewClient(tencentcls.Options{
		Logger: logger,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.Method == http.MethodGet {
				return httpResponse(request, http.StatusOK, "text/html", `<script>window.detail={"RecordId":"record-malformed"}</script>`), nil
			}
			return httpResponse(request, http.StatusOK, "text/plain; charset=utf-8", `provider detail is not JSON: secret-text`), nil
		})},
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	callback, err := hooksapplication.ParseTencentCLSCallback(
		`{"TopicId":"topic-1","DetailUrl":"https://alarm.cls.tencentcs.com/malformed"}`,
		"application/json",
	)
	if err != nil {
		t.Fatalf("ParseTencentCLSCallback: %v", err)
	}
	if _, err := client.Resolve(context.Background(), callback); !errors.Is(err, tencentcls.ErrDetailInvalid) {
		t.Fatalf("Resolve error = %v, want invalid detail response", err)
	}
	text := output.String()
	if !strings.Contains(text, "get_alert_detail response body omitted: invalid JSON") || strings.Contains(text, "provider detail is not JSON") || strings.Contains(text, "secret-text") {
		t.Fatalf("malformed response logging = %s", text)
	}
}

func TestResolveLogsSanitizedDetailPageHTMLControlAssignments(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	client, err := tencentcls.NewClient(tencentcls.Options{
		Logger: logger,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.Method == http.MethodGet {
				return httpResponse(request, http.StatusOK, "text/html; charset=utf-8", `<html><body>operator-visible-html</body><script>
window.detail={"RecordId":"record-html"};
				const ActualCallback = "https://example.invalid/private-webhook";
const H5AlarmShield = {"SecretID":"secret-id","SecretText":"secret-text"};
const SecretID = "secret-id-2";
const SecretText = "secret-text-2";
const callbackUrl = "https://example.invalid/callback/private";
window["SecretText"] = "secret-text-bracket";
const callback_url = "https://example.invalid/callback/underscore";
const h5_alarm_shield = {"SecretText":"secret-text-underscore"};
</script></html>`), nil
			}
			return httpResponse(request, http.StatusOK, "text/plain; charset=utf-8", `{"RecordId":"record-html","ResultsSnapshot":{"AnalysisInfo":[{"Type":"original"}],"RawResults":[{"message":"operator detail"}]}}`), nil
		})},
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	callback, err := hooksapplication.ParseTencentCLSCallback(
		`{"TopicId":"topic-1","DetailUrl":"https://alarm.cls.tencentcs.com/html-capability"}`,
		"application/json",
	)
	if err != nil {
		t.Fatalf("ParseTencentCLSCallback: %v", err)
	}
	if _, err := client.Resolve(context.Background(), callback); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	text := output.String()
	if !strings.Contains(text, "detail_page response body omitted: control marker detected, bytes=") || strings.Contains(text, "operator-visible-html") {
		t.Fatalf("HTML control marker was not fail-closed: %s", text)
	}
	for _, forbidden := range []string{
		"html-capability", "private-webhook", "callback/private", "secret-id", "secret-text", "secret-id-2", "secret-text-2",
		"ActualCallback", "H5AlarmShield", "SecretID", "SecretText",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("HTML log leaked %q: %s", forbidden, text)
		}
	}
}

func TestResolveLogsRedactTencentControlMaterial(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	client, err := tencentcls.NewClient(tencentcls.Options{
		Logger: logger,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.Method == http.MethodGet {
				return httpResponse(request, http.StatusOK, "text/html", `<script src="https://example.invalid/assets/h5-app.js">window.detail={"RecordId":"record-safe-log"}</script>`), nil
			}
			return httpResponse(request, http.StatusOK, "text/plain; charset=utf-8", `{"RecordId":"record-safe-log","TopicId":"topic-1","ResultsSnapshot":{"AnalysisInfo":[{"Type":"original"}],"RawResults":[{"message":"safe operator detail"}],"ActualCallback":[{"URL":"https://example.invalid/private-webhook"}],"H5AlarmShield":{"SecretID":"secret-id","SecretText":"secret-text"}},"diagnostic":"callback_url=https://example.invalid/control" ,"SecretText":"top-level-secret"}`), nil
		})},
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	callback, err := hooksapplication.ParseTencentCLSCallback(
		`{"TopicId":"topic-1","DetailUrl":"https://alarm.cls.tencentcs.com/short-capability"}`,
		"application/json",
	)
	if err != nil {
		t.Fatalf("ParseTencentCLSCallback: %v", err)
	}
	if _, err := client.Resolve(context.Background(), callback); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	text := output.String()
	for _, forbidden := range []string{
		"short-capability",
		"private-webhook",
		"secret-id",
		"secret-text",
		"callback_url",
		"H5AlarmShield",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("log leaked %q: %s", forbidden, text)
		}
	}
	if !strings.Contains(text, "window.detail") || !strings.Contains(text, "https://example.invalid/") || strings.Contains(text, "assets/h5-app.js") {
		t.Fatalf("marker-free SPA HTML was not safely retained: %s", text)
	}
}

func TestResolveEvidenceLogsProjectedPayload(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	client, err := tencentcls.NewClient(tencentcls.Options{
		Logger: logger,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.Method == http.MethodGet {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/html"}},
					Body:       io.NopCloser(strings.NewReader(`<script>window.detail={"RecordId":"record-payload"}</script>`)),
					Request:    request,
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"RecordId":"record-payload","TopicId":"topic-1","ResultsSnapshot":{"AnalysisInfo":[{"Type":"original"}],"RawResults":[{"message":"trigger line sent to remediation"}]}}`)),
				Request:    request,
			}, nil
		})},
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	callback, err := hooksapplication.ParseTencentCLSCallback(
		`{"TopicId":"topic-1","DetailUrl":"https://alarm.cls.tencentcs.com/alert-1"}`,
		"application/json",
	)
	if err != nil {
		t.Fatalf("ParseTencentCLSCallback: %v", err)
	}
	detail := client.ResolveEvidence(context.Background(), callback)
	if !detail.Available {
		t.Fatalf("detail = %#v", detail)
	}
	text := output.String()
	for _, want := range []string{
		observability.EventTencentCLSEvidenceProjected,
		`"available":true`,
		"trigger line sent to remediation",
		`"payload_kind":"provider_detail"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("log missing %q: %s", want, text)
		}
	}
}
