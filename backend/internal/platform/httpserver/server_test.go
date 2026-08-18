package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"fixthe/backend/internal/platform/buildinfo"
	"fixthe/backend/internal/platform/errtrace"
	"fixthe/backend/internal/platform/observability"
)

type stackAwareTestError struct {
	message string
	cause   error
	stack   errtrace.Trace
}

func newStackAwareTestError(message string, cause error) stackAwareTestError {
	return stackAwareTestError{message: message, cause: cause, stack: errtrace.Capture(1)}
}

func (e stackAwareTestError) Error() string              { return e.message }
func (e stackAwareTestError) Unwrap() error              { return e.cause }
func (e stackAwareTestError) StackTrace() errtrace.Trace { return e.stack }

func TestAccessLogUsesRoutePatternAndStatus(t *testing.T) {
	var output bytes.Buffer
	logger := testLogger(t, &output)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items/{id}", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodGet, "/items/42", nil)
	response := httptest.NewRecorder()
	AccessLog(logger, 1<<20, mux).ServeHTTP(response, request)

	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; output = %q", err, output.String())
	}
	if record[observability.FieldEvent] != observability.EventHTTPCompleted {
		t.Fatalf("event = %#v", record[observability.FieldEvent])
	}
	if record["route"] != "GET /items/{id}" {
		t.Fatalf("route = %#v", record["route"])
	}
	if record["status"] != float64(http.StatusNoContent) {
		t.Fatalf("status = %#v", record["status"])
	}
}

func TestAccessLogDoesNotEmitFailureSnapshotOnSuccess(t *testing.T) {
	var output bytes.Buffer
	logger := testLogger(t, &output)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /items", func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.ReadAll(request.Body)
		writer.WriteHeader(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodPost, "/items?page=1", strings.NewReader(`{"password":"hunter2"}`))
	AccessLog(logger, 1<<20, mux).ServeHTTP(httptest.NewRecorder(), request)

	records := decodeLogRecords(t, output.Bytes())
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	if records[0][observability.FieldEvent] != observability.EventHTTPCompleted {
		t.Fatalf("event = %#v", records[0][observability.FieldEvent])
	}
}

func TestAccessLogDoesNotEmitFailureSnapshotOn3xx(t *testing.T) {
	var output bytes.Buffer
	logger := testLogger(t, &output)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusFound)
	})

	AccessLog(logger, 1<<20, mux).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/items?page=1", nil))

	records := decodeLogRecords(t, output.Bytes())
	if len(records) != 1 || records[0][observability.FieldEvent] != observability.EventHTTPCompleted {
		t.Fatalf("records = %#v", records)
	}
}

func TestAccessLogEmitsWarnSnapshotOn4xx(t *testing.T) {
	var output bytes.Buffer
	logger := testLogger(t, &output)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /items", func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.ReadAll(request.Body)
		WriteError(writer, request, Error{Status: http.StatusBadRequest, Code: "invalid_request", Message: "The request is invalid."})
	})

	request := httptest.NewRequest(http.MethodPost, "/items?page=2", strings.NewReader(`{"name":"widget"}`))
	AccessLog(logger, 1<<20, mux).ServeHTTP(httptest.NewRecorder(), request)

	failed := findLogRecord(t, output.Bytes(), observability.EventHTTPFailed)
	if failed["level"] != "WARN" {
		t.Fatalf("level = %#v", failed["level"])
	}
	if failed["status"] != float64(http.StatusBadRequest) {
		t.Fatalf("status = %#v", failed["status"])
	}
	if failed[observability.FieldRequestBody] != `{"name":"widget"}` {
		t.Fatalf("request_body = %#v", failed[observability.FieldRequestBody])
	}
	if failed[observability.FieldBodyParseError] != false {
		t.Fatalf("body_parse_error = %#v", failed[observability.FieldBodyParseError])
	}
	if failed[observability.FieldRequestQuery] != `{"page":["2"]}` {
		t.Fatalf("request_query = %#v", failed[observability.FieldRequestQuery])
	}
	for _, record := range decodeLogRecords(t, output.Bytes()) {
		if record[observability.FieldEvent] == observability.EventHTTPError {
			t.Fatalf("4xx emitted server error record: %#v", record)
		}
	}
}

func TestAccessLogEmitsErrorSnapshotOn5xx(t *testing.T) {
	var output bytes.Buffer
	logger := testLogger(t, &output)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items", func(writer http.ResponseWriter, request *http.Request) {
		WriteError(writer, request, Error{Status: http.StatusInternalServerError, Code: "internal_error", Message: "An internal error occurred."})
	})

	AccessLog(logger, 1<<20, mux).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/items", nil))

	failed := findLogRecord(t, output.Bytes(), observability.EventHTTPFailed)
	if failed["level"] != "ERROR" {
		t.Fatalf("level = %#v", failed["level"])
	}
	if failed["status"] != float64(http.StatusInternalServerError) {
		t.Fatalf("status = %#v", failed["status"])
	}
}

func TestAccessLogEmitsOriginalInternalErrorWithoutLeakingResponse(t *testing.T) {
	var output bytes.Buffer
	logger := testLogger(t, &output)
	root := errors.New("postgres scan failed for project configuration")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items/{id}", func(writer http.ResponseWriter, request *http.Request) {
		WriteInternalError(writer, request, fmt.Errorf("load item: %w", root))
	})

	response := httptest.NewRecorder()
	AccessLog(logger, 1<<20, mux).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/items/42", nil))

	record := findLogRecord(t, output.Bytes(), observability.EventHTTPError)
	if record["level"] != "ERROR" || record["route"] != "GET /items/{id}" ||
		record[observability.FieldErrorMessage] != "load item: postgres scan failed for project configuration" {
		t.Fatalf("error record = %#v", record)
	}
	causes, ok := record[observability.FieldErrorCauses].([]any)
	if !ok || len(causes) != 1 {
		t.Fatalf("error_causes = %#v", record[observability.FieldErrorCauses])
	}
	cause, ok := causes[0].(map[string]any)
	if !ok || cause["message"] != root.Error() {
		t.Fatalf("cause = %#v", causes[0])
	}
	stack, ok := record[observability.FieldErrorStack].(string)
	if !ok || !strings.Contains(stack, "TestAccessLogEmitsOriginalInternalErrorWithoutLeakingResponse") ||
		!strings.Contains(stack, "server_test.go:") {
		t.Fatalf("error_stack = %#v", record[observability.FieldErrorStack])
	}
	if record[observability.FieldErrorStackSource] != errorStackSourceHTTPBoundary {
		t.Fatalf("error_stack_source = %#v", record[observability.FieldErrorStackSource])
	}
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "postgres") ||
		strings.Contains(response.Body.String(), "server_test.go") || strings.Contains(response.Body.String(), "error_stack") ||
		!strings.Contains(response.Body.String(), `"code":"internal_error"`) {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}

	recordCount := 0
	for _, candidate := range decodeLogRecords(t, output.Bytes()) {
		if candidate[observability.FieldEvent] == observability.EventHTTPError {
			recordCount++
		}
	}
	if recordCount != 1 {
		t.Fatalf("http.request.error count = %d", recordCount)
	}
}

func TestAccessLogTreatsCanceledRequestAsClientClose(t *testing.T) {
	var output bytes.Buffer
	logger := testLogger(t, &output)
	requestContext, cancel := context.WithCancel(context.Background())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items", func(writer http.ResponseWriter, request *http.Request) {
		cancel()
		WriteInternalError(writer, request, fmt.Errorf("load item: %w", context.Canceled))
	})

	request := httptest.NewRequest(http.MethodGet, "/items?page=1", nil).WithContext(requestContext)
	response := httptest.NewRecorder()
	AccessLog(logger, 1<<20, mux).ServeHTTP(response, request)

	if response.Code != statusClientClosedRequest || response.Body.Len() != 0 {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	records := decodeLogRecords(t, output.Bytes())
	if len(records) != 1 || records[0][observability.FieldEvent] != observability.EventHTTPCompleted ||
		records[0]["level"] != "INFO" || records[0]["status"] != float64(statusClientClosedRequest) {
		t.Fatalf("records = %#v", records)
	}
}

func TestAccessLogPrefersCapturedErrorStack(t *testing.T) {
	var output bytes.Buffer
	logger := testLogger(t, &output)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items", func(writer http.ResponseWriter, request *http.Request) {
		cause := newStackAwareTestError("repository operation failed", errors.New("driver failure"))
		WriteInternalError(writer, request, fmt.Errorf("service operation: %w", cause))
	})

	response := httptest.NewRecorder()
	AccessLog(logger, 1<<20, mux).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/items", nil))

	record := findLogRecord(t, output.Bytes(), observability.EventHTTPError)
	stack, ok := record[observability.FieldErrorStack].(string)
	if !ok || !strings.Contains(stack, "TestAccessLogPrefersCapturedErrorStack") ||
		!strings.Contains(stack, "server_test.go:") {
		t.Fatalf("error_stack = %#v", record[observability.FieldErrorStack])
	}
	if record[observability.FieldErrorStackSource] != errorStackSourceWrappedError {
		t.Fatalf("error_stack_source = %#v", record[observability.FieldErrorStackSource])
	}
	if strings.Contains(response.Body.String(), "repository operation") ||
		strings.Contains(response.Body.String(), "server_test.go") || strings.Contains(response.Body.String(), "error_stack") {
		t.Fatalf("response leaked diagnostics: %q", response.Body.String())
	}
}

func TestCollectErrorCausesExpandsJoinedErrors(t *testing.T) {
	first := fmt.Errorf("first wrapper: %w", errors.New("first root"))
	second := errors.New("second root")
	causes := collectErrorCauses(errors.Join(first, second))

	if len(causes) != 3 || causes[0].Message != first.Error() || causes[1].Message != second.Error() ||
		causes[2].Message != "first root" {
		t.Fatalf("causes = %#v", causes)
	}
}

func TestAccessLogRedactsPasswordAndOmitsHeaders(t *testing.T) {
	var output bytes.Buffer
	logger := testLogger(t, &output)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login", func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.ReadAll(request.Body)
		WriteError(writer, request, Error{Status: http.StatusUnauthorized, Code: "invalid_credentials", Message: "Username or password is invalid."})
	})

	request := httptest.NewRequest(http.MethodPost, "/login?token=query-secret", strings.NewReader(`{"username":"alice","password":"hunter2"}`))
	request.Header.Set("Cookie", "fixthe_session=super-secret-session")
	request.Header.Set("Authorization", "Bearer super-secret-token")
	AccessLog(logger, 1<<20, mux).ServeHTTP(httptest.NewRecorder(), request)

	failed := findLogRecord(t, output.Bytes(), observability.EventHTTPFailed)
	body := failed[observability.FieldRequestBody]
	if body != `{"password":"[redacted]","username":"alice"}` {
		t.Fatalf("request_body = %#v", body)
	}
	if failed[observability.FieldRequestQuery] != `{"token":["[redacted]"]}` {
		t.Fatalf("request_query = %#v", failed[observability.FieldRequestQuery])
	}
	raw := output.String()
	for _, leaked := range []string{"hunter2", "query-secret", "super-secret-session", "super-secret-token"} {
		if strings.Contains(raw, leaked) {
			t.Fatalf("logs leaked %q: %s", leaked, raw)
		}
	}
}

func TestAccessLogSnapshotsDecodeJSONFailure(t *testing.T) {
	var output bytes.Buffer
	logger := testLogger(t, &output)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /items", func(writer http.ResponseWriter, request *http.Request) {
		var payload struct {
			Name string `json:"name"`
		}
		if apiError := DecodeJSON(request, &payload); apiError != nil {
			WriteError(writer, request, *apiError)
		}
	})

	request := httptest.NewRequest(http.MethodPost, "/items?page=3", strings.NewReader(`{"name":`))
	request.Header.Set("Content-Type", "application/json")
	AccessLog(logger, 1<<20, mux).ServeHTTP(httptest.NewRecorder(), request)

	failed := findLogRecord(t, output.Bytes(), observability.EventHTTPFailed)
	if failed["level"] != "WARN" || failed["status"] != float64(http.StatusBadRequest) {
		t.Fatalf("failed record = %#v", failed)
	}
	if failed[observability.FieldBodyParseError] != true {
		t.Fatalf("body_parse_error = %#v", failed[observability.FieldBodyParseError])
	}
	if _, ok := failed[observability.FieldRequestBody]; ok {
		t.Fatalf("request_body = %#v, want omitted", failed[observability.FieldRequestBody])
	}
}

func TestAccessLogFlagsParseErrorWithoutRawBody(t *testing.T) {
	var output bytes.Buffer
	logger := testLogger(t, &output)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /items", func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.ReadAll(request.Body)
		WriteError(writer, request, Error{Status: http.StatusBadRequest, Code: "invalid_json", Message: "Request body is not valid JSON."})
	})

	request := httptest.NewRequest(http.MethodPost, "/items", strings.NewReader("not-json password=hunter2"))
	AccessLog(logger, 1<<20, mux).ServeHTTP(httptest.NewRecorder(), request)

	failed := findLogRecord(t, output.Bytes(), observability.EventHTTPFailed)
	if failed[observability.FieldBodyParseError] != true {
		t.Fatalf("body_parse_error = %#v", failed[observability.FieldBodyParseError])
	}
	if _, ok := failed[observability.FieldRequestBody]; ok {
		t.Fatalf("request_body = %#v, want omitted", failed[observability.FieldRequestBody])
	}
	if strings.Contains(output.String(), "hunter2") || strings.Contains(output.String(), "not-json") {
		t.Fatalf("raw body leaked: %s", output.String())
	}
}

func decodeLogRecords(t *testing.T, output []byte) []map[string]any {
	t.Helper()
	lines := bytes.Split(bytes.TrimSpace(output), []byte("\n"))
	records := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("json.Unmarshal() error = %v; line = %q", err, line)
		}
		records = append(records, record)
	}
	return records
}

func findLogRecord(t *testing.T, output []byte, event string) map[string]any {
	t.Helper()
	for _, record := range decodeLogRecords(t, output) {
		if record[observability.FieldEvent] == event {
			return record
		}
	}
	t.Fatalf("no %s record in %q", event, output)
	return nil
}

func TestAccessLogIncludesHTTPSpanCorrelation(t *testing.T) {
	var output bytes.Buffer
	logger := testLogger(t, &output)
	telemetry, err := observability.NewTelemetry(context.Background(), observability.TelemetryOptions{
		Service:     "fixthe-test",
		Environment: "test",
		Build:       buildinfo.Current(),
	})
	if err != nil {
		t.Fatalf("NewTelemetry() error = %v", err)
	}
	t.Cleanup(func() {
		if err := telemetry.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown() error = %v", err)
		}
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/livez", nil)
	request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	telemetry.HTTPHandler(AccessLog(logger, 1<<20, mux)).ServeHTTP(httptest.NewRecorder(), request)

	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; output = %q", err, output.String())
	}
	if record[observability.FieldTraceID] != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("trace_id = %#v", record[observability.FieldTraceID])
	}
	if record[observability.FieldSpanID] == "" || record[observability.FieldSpanID] == "00f067aa0ba902b7" {
		t.Fatalf("span_id = %#v", record[observability.FieldSpanID])
	}
}

func TestServerStopsAfterContextCancellation(t *testing.T) {
	logger := testLogger(t, io.Discard)
	listener := newBlockingListener()
	server, err := New(Options{
		Address:           "127.0.0.1:0",
		ReadHeaderTimeout: time.Second,
		ReadTimeout:       time.Second,
		WriteTimeout:      time.Second,
		IdleTimeout:       time.Second,
		ShutdownTimeout:   time.Second,
		Handler:           http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		Logger:            logger,
		Component:         "api",
		Listen: func(string, string) (net.Listener, error) {
			return listener, nil
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- server.Run(ctx)
	}()
	<-listener.accepting
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not stop")
	}
}

func TestServerDoesNotReportStartedWhenListenFails(t *testing.T) {
	var output bytes.Buffer
	logger := testLogger(t, &output)
	server, err := New(Options{
		Address:           "127.0.0.1:8080",
		ReadHeaderTimeout: time.Second,
		ReadTimeout:       time.Second,
		WriteTimeout:      time.Second,
		IdleTimeout:       time.Second,
		ShutdownTimeout:   time.Second,
		Handler:           http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		Logger:            logger,
		Component:         "api",
		Listen: func(string, string) (net.Listener, error) {
			return nil, errors.New("listen failed")
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = server.Run(context.Background())
	if err == nil {
		t.Fatal("Run() error = nil")
	}
	if strings.Contains(output.String(), observability.EventProcessStarted) {
		t.Fatalf("output = %q", output.String())
	}
}

// blockingListener 不绑定真实 socket，并用 channel 精确同步 Serve 已进入 Accept 的时刻。
// 这样取消测试不会依赖调度速度，也不会误连开发机上的其他服务。
type blockingListener struct {
	accepting  chan struct{}
	closed     chan struct{}
	acceptOnce sync.Once
	closeOnce  sync.Once
}

func newBlockingListener() *blockingListener {
	return &blockingListener{
		accepting: make(chan struct{}),
		closed:    make(chan struct{}),
	}
}

func (l *blockingListener) Accept() (net.Conn, error) {
	l.acceptOnce.Do(func() { close(l.accepting) })
	<-l.closed
	return nil, net.ErrClosed
}

func (l *blockingListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (l *blockingListener) Addr() net.Addr {
	return testAddress("127.0.0.1:0")
}

type testAddress string

func (a testAddress) Network() string { return "tcp" }
func (a testAddress) String() string  { return string(a) }

func TestNewRejectsMissingDependencies(t *testing.T) {
	_, err := New(Options{})
	if err == nil {
		t.Fatal("New() error = nil")
	}
}

func testLogger(t *testing.T, writer io.Writer) *slog.Logger {
	t.Helper()
	logger, err := observability.NewLogger(observability.LoggerOptions{
		Writer:      writer,
		Level:       "info",
		Format:      "json",
		Service:     "fixthe-test",
		Environment: "test",
		Build:       buildinfo.Current(),
	})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	return logger
}
