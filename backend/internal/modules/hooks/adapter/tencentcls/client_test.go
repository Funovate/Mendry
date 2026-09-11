package tencentcls_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"mendry/backend/internal/modules/hooks/adapter/tencentcls"
	hooksapplication "mendry/backend/internal/modules/hooks/application"
	remediationdomain "mendry/backend/internal/modules/remediation/domain"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func httpResponse(request *http.Request, status int, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode:    status,
		Header:        http.Header{"Content-Type": []string{contentType}},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       request,
	}
}

func validCallback(t *testing.T) hooksapplication.TencentCLSCallback {
	t.Helper()
	callback, err := hooksapplication.ParseTencentCLSCallback(`{"TopicId":"topic-callback","DetailUrl":"https://alarm.cls.tencentcs.com/4wOSDecA"}`, "application/json")
	if err != nil {
		t.Fatal(err)
	}
	return callback
}

func TestResolveEvidenceProjectsRawResultsAndSafeDetailProvenance(t *testing.T) {
	callback := validCallback(t)
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.Method == http.MethodGet:
			return httpResponse(request, http.StatusOK, "text/html; charset=utf-8", `<script>window.detail = {"RecordId":"record-raw"}</script>`), nil
		case request.Method == http.MethodPost && request.URL.Path == "/cls_no_login" && request.URL.Query().Get("action") == "GetAlertDetail":
			return httpResponse(request, http.StatusOK, "application/json; charset=utf-8", `{
  "data": {
    "RecordId": "record-raw",
    "TopicId": "topic-callback",
    "ResultsSnapshot": {
      "AnalysisInfo": [{"Name":"original log","Type":"original","Fields":"*","QueryIndex":1,"Limit":1}],
      "RawResults": [{"time":"2026-08-24T07:37:07.956Z","message":"panic: nil pointer from validated CLS detail record"}],
      "QueryParams": {"start":"2026-08-24T07:28:30Z","end":"2026-08-24T07:43:30Z"}
    }
  }
}`), nil
		default:
			return nil, errors.New("unexpected Tencent request")
		}
	})
	client, err := tencentcls.NewClient(tencentcls.Options{HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	detail := client.ResolveEvidence(context.Background(), callback)
	if detail.EvidenceKind != remediationdomain.EvidenceKindProviderDetail || detail.Classification != remediationdomain.EvidenceDirectFault || !detail.Available || detail.Outcome != "success" {
		t.Fatalf("detail projection = %#v", detail)
	}
	payload := string(detail.Payload)
	for _, want := range []string{"RawResults", "panic: nil pointer", "QueryParams", "record-raw"} {
		if !strings.Contains(payload, want) {
			t.Fatalf("payload missing %q: %s", want, payload)
		}
	}
	provenance := string(detail.Provenance)
	for _, want := range []string{"detail_capability_validated", "validated_provider_detail_get_alert_detail", "record-raw"} {
		if !strings.Contains(provenance, want) {
			t.Fatalf("provenance missing %q: %s", want, provenance)
		}
	}
	for _, forbidden := range []string{"DetailUrl", "alarm.cls.tencentcs.com", "console.cloud.tencent.com"} {
		if strings.Contains(payload, forbidden) || strings.Contains(provenance, forbidden) {
			t.Fatalf("projection leaked %q: payload=%s provenance=%s", forbidden, payload, provenance)
		}
	}
}

func TestResolvePreservesOperationalDetailAndIsolatesControlMaterial(t *testing.T) {
	callback := validCallback(t)
	var requestsMu sync.Mutex
	var requests []string
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requestsMu.Lock()
		requests = append(requests, request.Method+" "+request.URL.String())
		requestsMu.Unlock()
		switch {
		case request.Method == http.MethodGet && request.URL.Host == "alarm.cls.tencentcs.com":
			response := httpResponse(request, http.StatusFound, "text/html", "")
			response.Header.Set("Location", "https://eu-frankfurt-monitor.cls.tencentcs.com/cls_no_login?action=GetAlertDetailPage#/alert?RecordId=record-123")
			return response, nil
		case request.Method == http.MethodGet && request.URL.Host == "eu-frankfurt-monitor.cls.tencentcs.com":
			response := httpResponse(request, http.StatusOK, "text/html; charset=utf-8", `<script>window.detail = {"RecordId":"record-123"}</script>`)
			response.Header.Set("Location", "https://eu-frankfurt-monitor.cls.tencentcs.com/cls_no_login?action=GetAlertDetailPage#/alert?RecordId=record-123")
			return response, nil
		case request.Method == http.MethodPost && request.URL.Path == "/cls_no_login" && request.URL.Query().Get("action") == "GetAlertDetail":
			requestBody, err := io.ReadAll(request.Body)
			if err != nil || string(requestBody) != `{"RecordId":"record-123"}` {
				return nil, errors.New("unexpected detail request body")
			}
			return httpResponse(request, http.StatusOK, "application/json; charset=utf-8", `{
  "data": {
    "RecordId": "record-123",
    "AlertId": "alert-123",
    "TopicId": "topic-detail",
    "Topic": "prod-service",
    "LogsetName": "prod-logset",
    "Region": "ap-shanghai",
    "UIN": "100013370924",
    "FireTime": "2026-08-24 07:44:32.007 UTC",
    "QueryInterval": {"start":"2026-08-24T07:28:30Z","end":"2026-08-24T07:43:30Z"},
    "SecretText": "must not cross adapter boundary",
    "ResultsSnapshot": {
      "AnalysisInfo": [{
        "Name": "原始日志",
        "Type": "original",
        "Fields": "*",
        "QueryIndex": 1,
        "Limit": 1,
        "Configuration": {"query":"lv:\"ERROR\" OR \"nil pointer\""},
        "Error": {"Code":"partial", "Message":"context was unavailable"}
      }],
      "AnalysisResultFormat": [{"name":"原始日志","format":"table"}],
      "RawResults": [{"time":"2026-08-24T07:37:07.956Z","message":"triggering intentional nil pointer dereference","token":"operational-log-field"}],
      "ColNames": ["time","message"],
      "Columns": [["time","message"]],
      "QueryParams": {"query":"lv:\"ERROR\" OR \"nil pointer\"","start":"2026-08-24T07:28:30Z","end":"2026-08-24T07:43:30Z"}
    }
  },
  "SecretText": "top-level control material"
}`), nil
		default:
			return nil, errors.New("unexpected Tencent request")
		}
	})
	client, err := tencentcls.NewClient(tencentcls.Options{
		HTTPClient: &http.Client{Transport: transport}, MaxRedirects: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Resolve(context.Background(), callback)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if result.Evidence == nil || result.Evidence.RecordID != "record-123" || result.Evidence.TopicID != "topic-detail" || result.Observation.Outcome != tencentcls.OutcomeSuccess {
		t.Fatalf("result = %#v", result)
	}
	if len(result.Observation.Contradictions) != 1 || result.Observation.Contradictions[0] != "tencent_topic_id_mismatch" {
		t.Fatalf("topic contradiction = %#v", result.Observation.Contradictions)
	}
	if len(result.Evidence.Snapshot.AnalysisInfo) != 1 || result.Evidence.Snapshot.AnalysisInfo[0].Error == nil || result.Evidence.Snapshot.AnalysisInfo[0].Error.Code != "partial" {
		t.Fatalf("analysis info = %#v", result.Evidence.Snapshot.AnalysisInfo)
	}
	if !strings.Contains(string(result.Evidence.Snapshot.RawResults), "operational-log-field") || !strings.Contains(string(result.Evidence.Snapshot.QueryParams), "07:28:30") {
		t.Fatalf("operational evidence was truncated: %#v", result.Evidence.Snapshot)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"SecretText", "top-level control material", "must not cross adapter boundary", "DetailUrl", "alarm.cls.tencentcs.com"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("control material crossed adapter boundary: %s in %s", secret, encoded)
		}
	}
	requestsMu.Lock()
	defer requestsMu.Unlock()
	if len(requests) != 3 || !strings.Contains(requests[2], "/cls_no_login?action=GetAlertDetail") {
		t.Fatalf("requests = %#v", requests)
	}
}

func TestResolveRejectsRedirectAndSchemaFailuresWithoutLeakingDetails(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(*http.Request) (*http.Response, error)
		outcome tencentcls.ConnectorOutcome
		wantErr error
	}{
		{
			name: "redirect host",
			setup: func(request *http.Request) (*http.Response, error) {
				return httpResponse(request, http.StatusFound, "text/html", ""), nil
			},
			outcome: tencentcls.OutcomeRedirectRejected,
			wantErr: tencentcls.ErrRedirectRejected,
		},
		{
			name: "wrong page content type",
			setup: func(request *http.Request) (*http.Response, error) {
				return httpResponse(request, http.StatusOK, "application/json", `{}`), nil
			},
			outcome: tencentcls.OutcomeInvalid,
			wantErr: tencentcls.ErrDetailInvalid,
		},
		{
			name: "missing record id",
			setup: func(request *http.Request) (*http.Response, error) {
				return httpResponse(request, http.StatusOK, "text/html", `<html>no record here</html>`), nil
			},
			outcome: tencentcls.OutcomeInvalid,
			wantErr: tencentcls.ErrDetailInvalid,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if tc.name == "redirect host" {
					response, _ := tc.setup(request)
					response.Header.Set("Location", "https://evil.example/redirect")
					return response, nil
				}
				return tc.setup(request)
			})
			client, err := tencentcls.NewClient(tencentcls.Options{HTTPClient: &http.Client{Transport: transport}})
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.Resolve(context.Background(), validCallback(t))
			if !errors.Is(err, tc.wantErr) || result.Observation.Outcome != tc.outcome || result.Evidence != nil {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			if strings.Contains(err.Error(), "evil.example") || strings.Contains(err.Error(), "no record") {
				t.Fatalf("unsafe connector error = %v", err)
			}
		})
	}
}

func TestResolveRejectsEmptyOrNullAnalysisInfoWithoutRetry(t *testing.T) {
	for _, analysisInfo := range []string{"null", "[]", "{}"} {
		t.Run(analysisInfo, func(t *testing.T) {
			postCalls := 0
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.Method == http.MethodGet {
					return httpResponse(request, http.StatusOK, "text/html", `<script>window.detail={"RecordId":"record-empty-analysis"}</script>`), nil
				}
				postCalls++
				return httpResponse(request, http.StatusOK, "text/plain; charset=utf-8", `{"Response":{"Record":{"RecordId":"record-empty-analysis","ResultsSnapshot":{"AnalysisInfo":`+analysisInfo+`}}}}`), nil
			})
			client, err := tencentcls.NewClient(tencentcls.Options{HTTPClient: &http.Client{Transport: transport}, RetryDelays: []time.Duration{0}})
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.Resolve(context.Background(), validCallback(t))
			if !errors.Is(err, tencentcls.ErrDetailInvalid) || result.Observation.Outcome != tencentcls.OutcomeInvalid || postCalls != 1 {
				t.Fatalf("analysisInfo=%s result=%#v err=%v post calls=%d", analysisInfo, result, err, postCalls)
			}
		})
	}
}

func TestResolveBoundsResponseAndTimeout(t *testing.T) {
	t.Run("oversized", func(t *testing.T) {
		transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return httpResponse(request, http.StatusOK, "text/html", strings.Repeat("x", 128)), nil
		})
		client, err := tencentcls.NewClient(tencentcls.Options{HTTPClient: &http.Client{Transport: transport}, MaxPageBytes: 32})
		if err != nil {
			t.Fatal(err)
		}
		result, err := client.Resolve(context.Background(), validCallback(t))
		if !errors.Is(err, tencentcls.ErrDetailTooLarge) || result.Observation.Outcome != tencentcls.OutcomeOversized {
			t.Fatalf("result=%#v err=%v", result, err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
			<-request.Context().Done()
			return nil, request.Context().Err()
		})
		client, err := tencentcls.NewClient(tencentcls.Options{HTTPClient: &http.Client{Transport: transport}, Timeout: 5 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		result, err := client.Resolve(context.Background(), validCallback(t))
		if !errors.Is(err, tencentcls.ErrDetailTimeout) || result.Observation.Outcome != tencentcls.OutcomeTimeout {
			t.Fatalf("result=%#v err=%v", result, err)
		}
	})
}

func TestResolveMirrorsTencentCLSDetailPageResponse(t *testing.T) {
	const recordID = "8f7991f9-96b8-416e-a4c0-ae9136845945"
	const topicID = "df6e99ce-44f2-495d-848d-5099c3503556"
	callback, err := hooksapplication.ParseTencentCLSCallback(
		`{"TopicId":"`+topicID+`","DetailUrl":"https://alarm.cls.tencentcs.com/MColyiGd"}`,
		"application/json",
	)
	if err != nil {
		t.Fatal(err)
	}
	var requests []string
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests = append(requests, request.Method+" "+request.URL.String())
		switch {
		case request.Method == http.MethodGet && request.URL.Host == "alarm.cls.tencentcs.com":
			response := httpResponse(request, http.StatusFound, "text/html", "")
			response.Header.Set("Location", "https://eu-frankfurt-monitor.cls.tencentcs.com/cls_no_login?action=GetAlertDetailPage#/alert?RecordId="+recordID+"&JumpDomainID=notice-6c2fca2f-1182-4acc-8770-7ad83de27b53")
			return response, nil
		case request.Method == http.MethodGet && request.URL.Host == "eu-frankfurt-monitor.cls.tencentcs.com":
			return httpResponse(request, http.StatusOK, "text/html; charset=utf-8", `<html><body><div id="app"></div><script src="h5-app.js"></script></body></html>`), nil
		case request.Method == http.MethodPost && request.URL.Path == "/cls_no_login" && request.URL.Query().Get("action") == "GetAlertDetail":
			body, readErr := io.ReadAll(request.Body)
			if readErr != nil || string(body) != `{"RecordId":"`+recordID+`"}` {
				return nil, errors.New("unexpected Tencent CLS detail request body")
			}
			return httpResponse(request, http.StatusOK, "text/plain; charset=utf-8", `{
  "Response": {
    "Record": {
      "RecordId": "8f7991f9-96b8-416e-a4c0-ae9136845945",
      "AlertId": "alarm-b9b9ddc8-c46a-48c1-be75-5834fe9830e4",
      "AlertName": "mendry",
      "TopicId": "df6e99ce-44f2-495d-848d-5099c3503556",
      "TopicName": "prod-海外房产-service-cvm",
      "Status": 1,
      "ResultsSnapshot": {
        "Region": "法兰克福",
        "LogsetName": "生产环境-海外房产-日志集",
        "Level": "Warn",
        "Alarm": "mendry",
        "Query": "lv:\"ERROR\" OR \"nil pointer\"",
        "QueryCount": [1],
        "StartTime": "2026-08-26 10:37:32",
        "StartTimeUnix": 1787711852008,
        "FireTime": 1787711852008,
        "NotifyTime": "2026-08-26 10:37:32",
        "AnalysisInfo": [{
          "Name": "原始日志",
          "Type": "original",
          "ConfigInfo": [
            {"Key":"Fields","Value":"*"},
            {"Key":"QueryIndex","Value":"1"},
            {"Key":"Format","Value":"1"},
            {"Key":"Limit","Value":"1"}
          ],
          "AnalysisOriginal": [{
            "__FILENAME__": "/app/run/real-estate/backend/api/logs/server.log",
            "__HOSTNAME__": "VM-6-17-tencentos",
            "__SOURCE__": "10.16.6.17",
            "__TIMESTAMP__": "1787711785644",
            "lv": "INFO ",
            "msg": "triggering intentional nil pointer dereference",
            "path": "/workspace/backend/api/internal/service/common/common.go:59",
            "time": "2026-08-26 02:36:25.644"
          }]
        }],
        "AnalysisResultFormat": "原始日志",
        "RawResults": [[{"__QUERYCOUNT__":1}]],
        "ColNames": [["__QUERYCOUNT__"]],
        "Columns": [[{"Name":"__QUERYCOUNT__","Type":"bigint"}]],
        "QueryParams": [{
          "StartTime": 1787710890000,
          "EndTime": 1787711790000,
          "TopicId": "df6e99ce-44f2-495d-848d-5099c3503556",
          "TopicName": "prod-海外房产-service-cvm",
          "Query": "lv:\"ERROR\" OR \"nil pointer\"",
          "QueryUrlPath": "/cls/search?topic_id=df6e99ce-44f2-495d-848d-5099c3503556",
          "grammarVersion": "lucene"
        }],
        "ActualCallback": [{"URL":"https://example.invalid/private-webhook"}],
        "H5AlarmShield": {"SecretID":"secret-id","SecretText":"secret-text"}
      },
      "SecretText": "top-level-secret"
    }
  },
  "RequestId": "request-1"
}`), nil
		default:
			return nil, errors.New("unexpected Tencent CLS request")
		}
	})
	client, err := tencentcls.NewClient(tencentcls.Options{HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Resolve(context.Background(), callback)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if result.Evidence == nil || result.Evidence.RecordID != recordID || result.Evidence.AlertID == "" ||
		result.Evidence.TopicID != topicID || result.Evidence.Topic != "prod-海外房产-service-cvm" ||
		result.Evidence.Region != "法兰克福" || result.Evidence.Logset != "生产环境-海外房产-日志集" ||
		result.Evidence.Snapshot.AnalysisInfo[0].Fields != "*" ||
		result.Evidence.Snapshot.AnalysisInfo[0].QueryIndex != 1 ||
		result.Evidence.Snapshot.AnalysisInfo[0].Limit != 1 {
		t.Fatalf("production-shaped evidence = %#v", result.Evidence)
	}
	if !strings.Contains(string(result.Evidence.Snapshot.AnalysisInfo[0].RawResult), "triggering intentional nil pointer dereference") ||
		!strings.Contains(string(result.Evidence.Snapshot.QueryParams), "1787710890000") {
		t.Fatalf("original CLS evidence was not retained: %#v", result.Evidence.Snapshot)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"private-webhook", "secret-id", "secret-text", "top-level-secret", "H5AlarmShield", "ActualCallback"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("control material crossed evidence boundary: %q in %s", forbidden, encoded)
		}
	}
	if len(requests) != 3 || !strings.Contains(requests[0], "MColyiGd") || !strings.Contains(requests[1], "RecordId="+recordID) ||
		!strings.Contains(requests[2], "/cls_no_login?action=GetAlertDetail") {
		t.Fatalf("requests = %#v", requests)
	}
}

func TestResolveRetriesTemporaryProviderDetailResponse(t *testing.T) {
	const recordID = "8f7991f9-96b8-416e-a4c0-ae9136845945"
	var postCalls int
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodGet {
			return httpResponse(request, http.StatusOK, "text/html", `<script>window.detail={"RecordId":"`+recordID+`"}</script>`), nil
		}
		postCalls++
		requestBody, err := io.ReadAll(request.Body)
		if err != nil || string(requestBody) != `{"RecordId":"`+recordID+`"}` {
			return nil, errors.New("unexpected detail request body")
		}
		if postCalls == 1 {
			return httpResponse(request, http.StatusOK, "text/plain; charset=utf-8", `{"Response":{"Error":{"Code":-1001,"Message":"record is not ready"}}}`), nil
		}
		return httpResponse(request, http.StatusOK, "text/plain; charset=utf-8", `{"Response":{"Record":{"RecordId":"`+recordID+`","ResultsSnapshot":{"AnalysisInfo":[{"Type":"original","AnalysisOriginal":[{"msg":"original log"}]}],"RawResults":[{"message":"original log"}]}}}}`), nil
	})
	client, err := tencentcls.NewClient(tencentcls.Options{
		HTTPClient:  &http.Client{Transport: transport},
		RetryDelays: []time.Duration{0},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Resolve(context.Background(), validCallback(t))
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if postCalls != 2 || result.Evidence == nil || result.Evidence.RecordID != recordID ||
		!strings.Contains(string(result.Evidence.Snapshot.AnalysisInfo[0].RawResult), "original log") {
		t.Fatalf("post calls/result = %d/%#v", postCalls, result)
	}
}

func TestResolveExhaustedTemporaryProviderDetailIsRetryableUnavailable(t *testing.T) {
	var postCalls int
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodGet {
			return httpResponse(request, http.StatusOK, "text/html", `<script>window.detail={"RecordId":"record-temporary"}</script>`), nil
		}
		postCalls++
		return httpResponse(request, http.StatusOK, "text/plain; charset=utf-8", `{"Response":{"Error":{"Code":"-1001"}}}`), nil
	})
	client, err := tencentcls.NewClient(tencentcls.Options{
		HTTPClient:  &http.Client{Transport: transport},
		RetryDelays: []time.Duration{0},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Resolve(context.Background(), validCallback(t))
	if !errors.Is(err, tencentcls.ErrDetailUnavailable) || result.Observation.Outcome != tencentcls.OutcomeUnavailable || result.Evidence != nil || postCalls != 2 {
		t.Fatalf("result = %#v, err = %v, post calls = %d", result, err, postCalls)
	}
	var connectorErr *tencentcls.ConnectorError
	if !errors.As(err, &connectorErr) || !connectorErr.Retryable {
		t.Fatalf("connector error = %#v, want retryable", connectorErr)
	}
}

func TestResolveCancellationStopsTemporaryDetailRetryWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	posted := make(chan struct{}, 1)
	var postCalls int
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodGet {
			return httpResponse(request, http.StatusOK, "text/html", `<script>window.detail={"RecordId":"record-cancel"}</script>`), nil
		}
		postCalls++
		posted <- struct{}{}
		return httpResponse(request, http.StatusOK, "text/plain; charset=utf-8", `{"Response":{"Error":{"Code":-1001}}}`), nil
	})
	client, err := tencentcls.NewClient(tencentcls.Options{
		HTTPClient:  &http.Client{Transport: transport},
		RetryDelays: []time.Duration{time.Minute},
	})
	if err != nil {
		t.Fatal(err)
	}
	type resolveResult struct {
		result tencentcls.FetchResult
		err    error
	}
	done := make(chan resolveResult, 1)
	go func() {
		result, err := client.Resolve(ctx, validCallback(t))
		done <- resolveResult{result: result, err: err}
	}()
	select {
	case <-posted:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("detail request was not issued")
	}
	select {
	case resolved := <-done:
		if !errors.Is(resolved.err, tencentcls.ErrDetailUnavailable) || resolved.result.Observation.Outcome != tencentcls.OutcomeUnavailable || postCalls != 1 {
			t.Fatalf("canceled result = %#v, err = %v, post calls = %d", resolved.result, resolved.err, postCalls)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not stop retry wait")
	}
}
