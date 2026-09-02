package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"fixthe/backend/internal/platform/buildinfo"

	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	apiTrace "go.opentelemetry.io/otel/trace"
)

func TestHTTPHandlerContinuesW3CTraceContext(t *testing.T) {
	traceExporter := tracetest.NewInMemoryExporter()
	runtime, err := NewTelemetry(context.Background(), testTelemetryOptions())
	if err != nil {
		t.Fatalf("newTelemetry() error = %v", err)
	}
	runtime.tracerProvider.RegisterSpanProcessor(trace.NewSimpleSpanProcessor(traceExporter))

	var handlerSpan apiTrace.SpanContext
	handler := runtime.HTTPHandler(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		handlerSpan = apiTrace.SpanContextFromContext(request.Context())
		writer.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/livez", nil)
	request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	handler.ServeHTTP(httptest.NewRecorder(), request)

	if handlerSpan.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("TraceID = %s", handlerSpan.TraceID())
	}
	if handlerSpan.SpanID().String() == "00f067aa0ba902b7" {
		t.Fatal("server span reused upstream SpanID")
	}
}

func TestTelemetryCreatesAndShutsDownLocalProviders(t *testing.T) {
	runtime, err := NewTelemetry(context.Background(), testTelemetryOptions())
	if err != nil {
		t.Fatalf("NewTelemetry() error = %v", err)
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestBoundedParentSamplerDoesNotTrustRemoteSampledBit(t *testing.T) {
	traceID, err := apiTrace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatalf("TraceIDFromHex() error = %v", err)
	}
	spanID, err := apiTrace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatalf("SpanIDFromHex() error = %v", err)
	}
	remote := apiTrace.NewSpanContext(apiTrace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: apiTrace.FlagsSampled,
		Remote:     true,
	})
	parentContext := apiTrace.ContextWithRemoteSpanContext(context.Background(), remote)
	sampler := boundedParentSampler{
		root:   trace.NeverSample(),
		parent: trace.ParentBased(trace.AlwaysSample()),
	}

	result := sampler.ShouldSample(trace.SamplingParameters{ParentContext: parentContext, TraceID: traceID})
	if result.Decision != trace.Drop {
		t.Fatalf("Decision = %v, want Drop", result.Decision)
	}
}

func testTelemetryOptions() TelemetryOptions {
	return TelemetryOptions{
		Service:     "fixthe-test",
		Environment: "test",
		Build:       buildinfo.Info{Version: "test"},
	}
}

func TestHTTPHandlerDoesNotRecordRequestPayloads(t *testing.T) {
	traceExporter := tracetest.NewInMemoryExporter()
	runtime, err := NewTelemetry(context.Background(), testTelemetryOptions())
	if err != nil {
		t.Fatalf("NewTelemetry() error = %v", err)
	}
	t.Cleanup(func() {
		if err := runtime.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown() error = %v", err)
		}
	})
	// 生产采样是 bounded ratio；这个测试需要确定导出一个 HTTP span，
	// 才能断言 payload 没有被写成 span attribute。
	sampled := trace.NewTracerProvider(
		trace.WithSampler(trace.AlwaysSample()),
		trace.WithSyncer(traceExporter),
	)
	t.Cleanup(func() {
		if err := sampled.Shutdown(context.Background()); err != nil {
			t.Errorf("sampled.Shutdown() error = %v", err)
		}
	})
	runtime.tracerProvider = sampled

	handler := runtime.HTTPHandler(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = request.Body.Read(make([]byte, 64))
		writer.Header().Set("Set-Cookie", "fixthe_session=new-session")
		_, _ = writer.Write([]byte(`{"ok":true}`))
	}))
	request := httptest.NewRequest(http.MethodPost, "/items?token=query-secret", strings.NewReader(`{"password":"hunter2"}`))
	request.Header.Set("Cookie", "fixthe_session=super-secret-session")
	request.Header.Set("Authorization", "Bearer super-secret-token")
	handler.ServeHTTP(httptest.NewRecorder(), request)

	spans := traceExporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("expected at least one HTTP span")
	}
	for _, span := range spans {
		for _, attribute := range span.Attributes {
			value := attribute.Value.AsString()
			for _, leaked := range []string{"hunter2", "query-secret", "super-secret-session", "super-secret-token", "new-session"} {
				if strings.Contains(value, leaked) {
					t.Fatalf("span %q attribute %s leaked %q: %q", span.Name, attribute.Key, leaked, value)
				}
			}
		}
	}
}
