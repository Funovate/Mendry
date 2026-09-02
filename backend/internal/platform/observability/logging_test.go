package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"fixthe/backend/internal/platform/buildinfo"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestLoggerWritesStableJSONEnvelope(t *testing.T) {
	var output bytes.Buffer
	logger, err := NewLogger(LoggerOptions{
		Writer:      &output,
		Level:       "info",
		Format:      "json",
		Service:     "fixthe-test",
		Environment: "test",
		Build: buildinfo.Info{
			Version: "1.2.3",
			Commit:  "abc123",
		},
	})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}

	Log(context.Background(), logger, slog.LevelInfo, EventProcessStarted, "started",
		slog.String(FieldComponent, "test"),
	)

	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; output = %q", err, output.String())
	}

	assertField(t, record, FieldEvent, EventProcessStarted)
	assertField(t, record, FieldService, "fixthe-test")
	assertField(t, record, FieldVersion, "1.2.3")
	assertField(t, record, FieldCommit, "abc123")
	assertField(t, record, FieldEnvironment, "test")
	assertField(t, record, FieldComponent, "test")
	if _, ok := record["timestamp"]; !ok {
		t.Fatal("record has no timestamp")
	}
}

func TestLoggerWritesQueryDebugJSONFields(t *testing.T) {
	var output bytes.Buffer
	logger, err := NewLogger(LoggerOptions{
		Writer:      &output,
		Level:       "debug",
		Format:      "json",
		Service:     "fixthe-test",
		Environment: "test",
		Build:       buildinfo.Current(),
	})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}

	Log(context.Background(), logger, slog.LevelDebug, EventDBQueryCompleted, "query completed",
		slog.String(FieldComponent, "postgres"),
		slog.String(FieldDBOperation, "incident.get_by_id"),
		slog.String(FieldDBQueryText, "SELECT private_column FROM incidents WHERE token = 'secret-bind-value'"),
	)
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; output = %q", err, output.String())
	}
	assertField(t, record, FieldEvent, EventDBQueryCompleted)
	assertField(t, record, FieldDBQueryText, "SELECT private_column FROM incidents WHERE token = 'secret-bind-value'")
}

func TestConsoleRendersTencentCLSResponseAndEvidenceAsPhysicalBlocks(t *testing.T) {
	var output bytes.Buffer
	logger, err := NewLogger(LoggerOptions{
		Writer: &output, Level: "info", Format: "console",
		Service: "fixthe-test", Environment: "test", Build: buildinfo.Current(),
	})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}

	Log(context.Background(), logger, slog.LevelInfo, EventTencentCLSRequestCompleted, "Tencent CLS request completed",
		slog.String(FieldComponent, "hooks"),
		slog.String(FieldHTTPRequest, `{"RecordId":"record-1"}`),
		slog.String(FieldHTTPResponse, "first line\nsecond line"),
	)
	Log(context.Background(), logger, slog.LevelInfo, EventTencentCLSEvidenceProjected, "Tencent CLS evidence projected for remediation",
		slog.String(FieldComponent, "hooks"),
		slog.String(FieldPayloadKind, "provider_detail"),
		slog.String(FieldPayload, `{"RawResults":[{"message":"trigger"}]}`),
	)

	text := output.String()
	for _, want := range []string{
		"request:\n{\"RecordId\":\"record-1\"}",
		"response:\nfirst line\nsecond line",
		"tencent_cls_evidence[provider_detail]:\n{\"RawResults\":[{\"message\":\"trigger\"}]}",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("console output missing %q: %s", want, text)
		}
	}
	for _, quoted := range []string{`request="`, `response="`, `payload="`} {
		if strings.Contains(text, quoted) {
			t.Fatalf("console output kept quoted block %q: %s", quoted, text)
		}
	}
}

func TestLoggerWritesHTTPCompletedDebugJSONRecord(t *testing.T) {
	var output bytes.Buffer
	logger, err := NewLogger(LoggerOptions{
		Writer:      &output,
		Level:       "info",
		Format:      "json",
		Service:     "fixthe-test",
		Environment: "test",
		Build:       buildinfo.Current(),
	})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}

	Log(context.Background(), logger, slog.LevelInfo, EventHTTPCompleted, "request completed",
		slog.String(FieldComponent, "httpserver"),
		slog.String(FieldHTTPRequestHeaders, "Authorization: Bearer secret-token\nCookie: fixthe_session=secret-cookie"),
		slog.String(FieldHTTPRequestQuery, "token=query-secret"),
		slog.String(FieldHTTPRequest, `{"password":"hunter2"}`),
		slog.Bool(FieldHTTPRequestTruncated, true),
		slog.String(FieldHTTPResponseHeaders, "Set-Cookie: fixthe_session=new-session"),
		slog.String(FieldHTTPResponse, `{"ok":true}`),
		slog.Bool(FieldHTTPResponseTruncated, false),
	)

	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; output = %q", err, output.String())
	}
	assertField(t, record, FieldEvent, EventHTTPCompleted)
	assertField(t, record, FieldHTTPRequestHeaders, "Authorization: Bearer secret-token\nCookie: fixthe_session=secret-cookie")
	assertField(t, record, FieldHTTPRequestQuery, "token=query-secret")
	assertField(t, record, FieldHTTPRequest, `{"password":"hunter2"}`)
	assertField(t, record, FieldHTTPRequestTruncated, true)
	assertField(t, record, FieldHTTPResponseHeaders, "Set-Cookie: fixthe_session=new-session")
	assertField(t, record, FieldHTTPResponse, `{"ok":true}`)
	assertField(t, record, FieldHTTPResponseTruncated, false)
}

func TestConsoleHandlerFormatsInboundHTTPDebugBlocks(t *testing.T) {
	var output bytes.Buffer
	handler := newConsoleHandler(&output, slog.LevelInfo, false)
	record := slog.NewRecord(time.Date(2026, time.August, 18, 10, 43, 18, 0, time.UTC), slog.LevelInfo, "request completed", 0)
	record.AddAttrs(
		slog.String(FieldEvent, EventHTTPCompleted),
		slog.String(FieldComponent, "httpserver"),
		slog.String(FieldTraceID, "cbad87f9abcd1234"),
		slog.String(FieldRequestID, "b8dc3be5abcd1234"),
		slog.String("method", "POST"),
		slog.String("route", "POST /login"),
		slog.Int("status", 200),
		slog.Int64(FieldDurationMS, 12),
		slog.String(FieldHTTPRequestHeaders, "Authorization: Bearer secret-token\nCookie: fixthe_session=secret-cookie"),
		slog.String(FieldHTTPRequestQuery, "env=prod"),
		slog.String(FieldHTTPRequest, `{"password":"hunter2"}`),
		slog.String(FieldHTTPResponseHeaders, "Set-Cookie: fixthe_session=new-session"),
		slog.String(FieldHTTPResponse, `{"ok":true}`),
	)
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	formatted := output.String()
	if !strings.HasPrefix(formatted, "2026-08-18 10:43:18 INF [httpserver] request completed") {
		t.Fatalf("output = %q", formatted)
	}
	for _, expected := range []string{
		"trace=cbad87f9",
		"req=b8dc3be5",
		"method=POST",
		`route="POST /login"`,
		"status=200",
		"took=12ms",
		"request_headers:\nAuthorization: Bearer secret-token\nCookie: fixthe_session=secret-cookie\n",
		"request_query:\nenv=prod\n",
		"request:\n{\"password\":\"hunter2\"}\n",
		"response_headers:\nSet-Cookie: fixthe_session=new-session\n",
		"response:\n{\"ok\":true}\n",
	} {
		if !strings.Contains(formatted, expected) {
			t.Fatalf("output %q does not contain %q", formatted, expected)
		}
	}
	for _, quoted := range []string{`request="`, `response="`, `request_headers="`, `response_headers="`, `request_query="`, `\nAuthorization`} {
		if strings.Contains(formatted, quoted) {
			t.Fatalf("output %q contains quoted debug fragment %q", formatted, quoted)
		}
	}
}

func TestLoggerWritesHTTPFailedJSONRecord(t *testing.T) {
	var output bytes.Buffer
	logger, err := NewLogger(LoggerOptions{
		Writer:      &output,
		Level:       "info",
		Format:      "json",
		Service:     "fixthe-test",
		Environment: "test",
		Build:       buildinfo.Current(),
	})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}

	Log(context.Background(), logger, slog.LevelWarn, EventHTTPFailed, "request failed",
		slog.String(FieldComponent, "httpserver"),
		slog.String(FieldRequestBody, `{"username":"alice","password":"[redacted]"}`),
		slog.String(FieldRequestQuery, `{"page":["2"]}`),
		slog.Bool(FieldBodyTruncated, false),
		slog.Bool(FieldQueryTruncated, false),
		slog.Bool(FieldBodyParseError, false),
	)

	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; output = %q", err, output.String())
	}
	assertField(t, record, FieldEvent, EventHTTPFailed)
	assertField(t, record, FieldComponent, "httpserver")
	assertField(t, record, FieldRequestBody, `{"username":"alice","password":"[redacted]"}`)
	assertField(t, record, FieldRequestQuery, `{"page":["2"]}`)
	assertField(t, record, FieldBodyTruncated, false)
	assertField(t, record, FieldQueryTruncated, false)
	assertField(t, record, FieldBodyParseError, false)
}

func TestLoggerWritesHTTPErrorAndPanicDiagnosticFields(t *testing.T) {
	var output bytes.Buffer
	logger, err := NewLogger(LoggerOptions{
		Writer: &output, Level: "info", Format: "json", Service: "fixthe-test", Environment: "test", Build: buildinfo.Current(),
	})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}

	Log(context.Background(), logger, slog.LevelError, EventHTTPError, "request error",
		slog.String(FieldErrorType, "*fmt.wrapError"),
		slog.String(FieldErrorMessage, "load configuration: database failure"),
		slog.Any(FieldErrorCauses, []map[string]string{{"type": "*errors.errorString", "message": "database failure"}}),
		slog.String(FieldErrorStack, "function\n\t/source/file.go:42"),
		slog.String(FieldErrorStackSource, "wrapped_error"),
		slog.String(FieldPanicType, "string"),
		slog.String(FieldPanicValue, "panic detail"),
		slog.String(FieldStack, "stack detail"),
	)

	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; output = %q", err, output.String())
	}
	assertField(t, record, FieldEvent, EventHTTPError)
	assertField(t, record, FieldErrorType, "*fmt.wrapError")
	assertField(t, record, FieldErrorMessage, "load configuration: database failure")
	assertField(t, record, FieldErrorStack, "function\n\t/source/file.go:42")
	assertField(t, record, FieldErrorStackSource, "wrapped_error")
	assertField(t, record, FieldPanicType, "string")
	assertField(t, record, FieldPanicValue, "panic detail")
	assertField(t, record, FieldStack, "stack detail")
	if causes, ok := record[FieldErrorCauses].([]any); !ok || len(causes) != 1 {
		t.Fatalf("error_causes = %#v", record[FieldErrorCauses])
	}
}

func TestLoggerFiltersBelowConfiguredLevel(t *testing.T) {
	var output bytes.Buffer
	logger, err := NewLogger(LoggerOptions{
		Writer:      &output,
		Level:       "warn",
		Format:      "json",
		Service:     "fixthe-test",
		Environment: "test",
		Build:       buildinfo.Current(),
	})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}

	Log(context.Background(), logger, slog.LevelInfo, EventProcessStarted, "started")
	if output.Len() != 0 {
		t.Fatalf("output = %q", output.String())
	}
}

func TestLogAddsTraceCorrelationFromContext(t *testing.T) {
	var output bytes.Buffer
	logger, err := NewLogger(LoggerOptions{
		Writer:      &output,
		Level:       "info",
		Format:      "json",
		Service:     "fixthe-test",
		Environment: "test",
		Build:       buildinfo.Current(),
	})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}

	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	ctx, span := provider.Tracer("test").Start(context.Background(), "correlated")
	defer span.End()
	Log(ctx, logger, slog.LevelInfo, EventProcessStarted, "started")

	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	spanContext := span.SpanContext()
	assertField(t, record, FieldTraceID, spanContext.TraceID().String())
	assertField(t, record, FieldSpanID, spanContext.SpanID().String())
}

func TestLoggerWritesReadableConsoleRecordWithoutColorForBuffer(t *testing.T) {
	var output bytes.Buffer
	logger, err := NewLogger(LoggerOptions{
		Writer:      &output,
		Level:       "info",
		Format:      "console",
		Service:     "fixthe-test",
		Environment: "development",
		Build:       buildinfo.Current(),
	})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}

	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	ctx, span := provider.Tracer("test").Start(context.Background(), "console")
	defer span.End()
	Log(ctx, logger, slog.LevelWarn, EventProcessStarted, "command completed",
		slog.String(FieldComponent, "test"),
		slog.String(FieldRequestID, "request-123456789"),
		slog.String(FieldTransactionID, "transaction-123456789"),
		slog.String(FieldRedisCommand, "hello"),
		slog.Int(FieldCommandCount, 1),
		slog.Int64(FieldDurationMS, 13),
	)

	record := output.String()
	traceID := span.SpanContext().TraceID().String()[:8]
	for _, expected := range []string{"WRN", "[test] command completed", "trace=" + traceID, "req=request-", "tx=transact", "command=hello", "count=1", "took=13ms"} {
		if !strings.Contains(record, expected) {
			t.Fatalf("record %q does not contain %q", record, expected)
		}
	}
	for _, omitted := range []string{"\x1b[", "event=", "trace_id=", "span_id=", "request_id=", "transaction_id=", "component=", "service=", "version=", "commit=", "environment="} {
		if strings.Contains(record, omitted) {
			t.Fatalf("record %q contains %q", record, omitted)
		}
	}
}

func TestConsoleHandlerUsesComponentFromDerivedLogger(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(newConsoleHandler(&output, slog.LevelInfo, false)).WithGroup("").With(FieldComponent, "postgres")
	logger.Info("query completed")

	if record := output.String(); !strings.Contains(record, "[postgres] query completed") || strings.Contains(record, "component=") {
		t.Fatalf("output = %q", record)
	}
}

func TestConsoleHandlerFormatsDiagnosticRecord(t *testing.T) {
	var output bytes.Buffer
	handler := newConsoleHandler(&output, slog.LevelDebug, false)
	record := slog.NewRecord(time.Date(2026, time.August, 14, 15, 19, 57, 0, time.UTC), slog.LevelDebug, "query completed", 0)
	record.AddAttrs(
		slog.String(FieldComponent, "postgres"),
		slog.String(FieldTraceID, "312f6084f0a1f872196de5d306bcd196"),
		slog.String(FieldSpanID, "ead25ef144df8c73"),
		slog.String(FieldDBOperation, "GetProject"),
		slog.Int64(FieldRowsAffected, 1),
		slog.Int64(FieldDurationMS, 8),
		slog.String(FieldDBQueryText, "SELECT id FROM projects\nWHERE key = 'demo'"),
		slog.String(FieldOutcome, "success"),
	)
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	want := "2026-08-14 15:19:57 DBG [postgres] query completed trace=312f6084 operation=GetProject rows=1 took=8ms outcome=success\nSELECT id FROM projects\nWHERE key = 'demo'\n"
	if output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
}

func TestConsoleHandlerKeepsOriginalErrorDiagnostics(t *testing.T) {
	var output bytes.Buffer
	handler := newConsoleHandler(&output, slog.LevelDebug, false)
	record := slog.NewRecord(time.Date(2026, time.August, 14, 15, 37, 37, 0, time.UTC), slog.LevelError, "request error", 0)
	record.AddAttrs(
		slog.String(FieldComponent, "httpserver"),
		slog.String(FieldErrorType, "*fmt.wrapError"),
		slog.String(FieldErrorMessage, "load configuration: database failure"),
		slog.Any(FieldErrorCauses, []map[string]string{{"type": "*errors.errorString", "message": "database failure"}}),
		slog.String(FieldErrorStack, "function\n\t/source/file.go:42"),
		slog.String(FieldErrorStackSource, "wrapped_error"),
		slog.String(FieldPanicValue, "panic detail"),
		slog.String(FieldStack, "goroutine 1 [running]:\nmain.main()\n\t/source/panic.go:7 +0x1\n"),
	)
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	formatted := output.String()
	for _, expected := range []string{
		"ERR [httpserver] request error",
		"error_type=*fmt.wrapError",
		`error_message="load configuration: database failure"`,
		"database failure",
		"error_stack_source=wrapped_error",
		`panic_value="panic detail"`,
		"\nfunction\n\t/source/file.go:42\n",
		"goroutine 1 [running]:\nmain.main()\n\t/source/panic.go:7 +0x1\n",
	} {
		if !strings.Contains(formatted, expected) {
			t.Fatalf("output %q does not contain %q", formatted, expected)
		}
	}
	for _, escaped := range []string{`error_stack=`, `stack=`, `\n\t/source/file.go:42`, `\n\t/source/panic.go:7`} {
		if strings.Contains(formatted, escaped) {
			t.Fatalf("output %q contains escaped stack fragment %q", formatted, escaped)
		}
	}
}

type blockingConsoleWriter struct {
	mu           sync.Mutex
	writes       []string
	firstWrite   chan struct{}
	releaseFirst chan struct{}
	once         sync.Once
}

func newBlockingConsoleWriter() *blockingConsoleWriter {
	return &blockingConsoleWriter{firstWrite: make(chan struct{}), releaseFirst: make(chan struct{})}
}

func (w *blockingConsoleWriter) Write(body []byte) (int, error) {
	first := false
	w.once.Do(func() {
		first = true
		close(w.firstWrite)
	})
	if first {
		<-w.releaseFirst
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writes = append(w.writes, string(body))
	return len(body), nil
}

func (w *blockingConsoleWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.Join(w.writes, "")
}

func TestConsoleHandlerKeepsConcurrentEventAndStackTogether(t *testing.T) {
	output := newBlockingConsoleWriter()
	handler := newConsoleHandler(output, slog.LevelDebug, false)
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)

	first := slog.NewRecord(time.Time{}, slog.LevelError, "first error", 0)
	first.AddAttrs(slog.String(FieldErrorStack, "first.function\n\t/source/first.go:11"))
	second := slog.NewRecord(time.Time{}, slog.LevelError, "second error", 0)
	second.AddAttrs(slog.String(FieldErrorStack, "second.function\n\t/source/second.go:22"))

	go func() { firstDone <- handler.Handle(context.Background(), first) }()
	<-output.firstWrite
	go func() { secondDone <- handler.Handle(context.Background(), second) }()
	close(output.releaseFirst)

	if err := <-firstDone; err != nil {
		t.Fatalf("first Handle() error = %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second Handle() error = %v", err)
	}

	formatted := output.String()
	firstEvent := strings.Index(formatted, "first error")
	firstStack := strings.Index(formatted, "/source/first.go:11")
	secondEvent := strings.Index(formatted, "second error")
	secondStack := strings.Index(formatted, "/source/second.go:22")
	if firstEvent < 0 || firstStack < firstEvent || secondEvent < firstStack || secondStack < secondEvent {
		t.Fatalf("concurrent output interleaved: %q", formatted)
	}
}

func TestConsoleHandlerUsesColorWhenEnabled(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(newConsoleHandler(&output, slog.LevelInfo, true))
	logger.Warn("warning")
	if !strings.Contains(output.String(), "\x1b[") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestLoggerRejectsUnsupportedFormat(t *testing.T) {
	_, err := NewLogger(LoggerOptions{
		Writer:      &bytes.Buffer{},
		Level:       "info",
		Format:      "pretty-secret-marker",
		Service:     "fixthe-test",
		Environment: "test",
		Build:       buildinfo.Current(),
	})
	if err == nil || strings.Contains(err.Error(), "pretty-secret-marker") {
		t.Fatalf("NewLogger() error = %v", err)
	}
}

func assertField(t *testing.T, record map[string]any, field string, expected any) {
	t.Helper()
	if actual := record[field]; actual != expected {
		t.Fatalf("%s = %#v, want %#v", field, actual, expected)
	}
}
