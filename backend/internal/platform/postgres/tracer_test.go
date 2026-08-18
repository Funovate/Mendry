package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"fixthe/backend/internal/platform/observability"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestQueryTracerLogsSafeOperationWithoutStatementOrArguments(t *testing.T) {
	var output bytes.Buffer
	tracer, err := NewQueryTracer(testLogger(&output), noop.NewTracerProvider().Tracer("test"), sdkmetric.NewMeterProvider().Meter("test"), 0, false)
	if err != nil {
		t.Fatalf("NewQueryTracer() error = %v", err)
	}
	const statement = "SELECT private_column FROM incidents WHERE token = $1"
	const bindValue = "secret-bind-value"

	ctx := tracer.TraceQueryStart(WithOperation(context.Background(), "incident.get_by_id"), nil, pgx.TraceQueryStartData{
		SQL:  statement,
		Args: []any{bindValue},
	})
	tracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: &pgconn.PgError{
		Code:    "23505",
		Message: "duplicate secret-diagnostic",
	}})

	logOutput := output.String()
	for _, forbidden := range []string{statement, "private_column", bindValue, "secret-diagnostic", observability.FieldDBQueryText, "db.query.args"} {
		if strings.Contains(logOutput, forbidden) {
			t.Fatalf("log exposes %q: %s", forbidden, logOutput)
		}
	}
	if !strings.Contains(logOutput, `"db.operation.name":"incident.get_by_id"`) || !strings.Contains(logOutput, `"error_class":"conflict"`) {
		t.Fatalf("log = %s", logOutput)
	}
}

func TestQueryTracerDebugLogsSQLAndArgumentsWithoutChangingSuccessLevel(t *testing.T) {
	var output bytes.Buffer
	spanRecorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	metricReader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(metricReader))
	tracer, err := NewQueryTracer(testLogger(&output), provider.Tracer("test"), meterProvider.Meter("test"), 0, true)
	if err != nil {
		t.Fatalf("NewQueryTracer() error = %v", err)
	}
	const statement = "SELECT private_column FROM incidents WHERE token = $1 OR extra = $2"
	const bindValue = "secret-bind-value"
	wantSQL := "SELECT private_column FROM incidents WHERE token = 'secret-bind-value' OR extra = NULL"

	ctx := tracer.TraceQueryStart(WithOperation(context.Background(), "incident.get_by_id"), nil, pgx.TraceQueryStartData{
		SQL:  statement,
		Args: []any{bindValue, nil},
	})
	tracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{CommandTag: pgconn.NewCommandTag("SELECT 1")})

	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; output = %q", err, output.String())
	}
	if record["level"] != "DEBUG" {
		t.Fatalf("level = %#v, want DEBUG", record["level"])
	}
	if record[observability.FieldDBQueryText] != wantSQL {
		t.Fatalf("db.query.text = %#v", record[observability.FieldDBQueryText])
	}
	if _, ok := record["db.query.args"]; ok {
		t.Fatalf("db.query.args unexpectedly present: %#v", record["db.query.args"])
	}

	spans := spanRecorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("ended spans = %d", len(spans))
	}
	for _, attr := range spans[0].Attributes() {
		if attr.Key == attribute.Key(observability.FieldDBQueryText) || attr.Key == "db.query.args" ||
			strings.Contains(string(attr.Value.AsString()), statement) || strings.Contains(string(attr.Value.AsString()), bindValue) {
			t.Fatalf("span leaked query debug data: %#v", spans[0].Attributes())
		}
	}

	var collected metricdata.ResourceMetrics
	if err := metricReader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if encoded, _ := json.Marshal(collected); strings.Contains(string(encoded), statement) || strings.Contains(string(encoded), bindValue) {
		t.Fatalf("metric leaked query debug data: %s", encoded)
	}
}

func TestQueryTracerDebugLevelAloneDoesNotLogSQL(t *testing.T) {
	var output bytes.Buffer
	tracer, err := NewQueryTracer(testLogger(&output), noop.NewTracerProvider().Tracer("test"), sdkmetric.NewMeterProvider().Meter("test"), 0, false)
	if err != nil {
		t.Fatalf("NewQueryTracer() error = %v", err)
	}
	const statement = "SELECT private_column FROM incidents WHERE token = $1"
	const bindValue = "secret-bind-value"
	ctx := tracer.TraceQueryStart(WithOperation(context.Background(), "incident.get_by_id"), nil, pgx.TraceQueryStartData{
		SQL:  statement,
		Args: []any{bindValue},
	})
	tracer.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{CommandTag: pgconn.NewCommandTag("SELECT 1")})
	logOutput := output.String()
	if !strings.Contains(logOutput, `"level":"DEBUG"`) {
		t.Fatalf("log = %s", logOutput)
	}
	for _, forbidden := range []string{statement, bindValue, observability.FieldDBQueryText} {
		if strings.Contains(logOutput, forbidden) {
			t.Fatalf("debug-level log exposes %q: %s", forbidden, logOutput)
		}
	}
}

func TestErrorClassification(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{err: context.Canceled, want: errorClassCanceled},
		{err: context.DeadlineExceeded, want: errorClassTimeout},
		{err: &pgconn.PgError{Code: "40001"}, want: errorClassSerialization},
		{err: &pgconn.PgError{Code: "40P01"}, want: errorClassDeadlock},
		{err: errors.New("unknown"), want: errorClassInternal},
	}
	for _, test := range tests {
		if got := classifyError(test.err); got != test.want {
			t.Fatalf("classifyError(%v) = %q, want %q", test.err, got, test.want)
		}
	}
}
