package postgres

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"fixthe/backend/internal/platform/config"
	"fixthe/backend/internal/platform/errtrace"

	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace/noop"
)

func safeErrorForStackTest() error {
	return newSafeError("test PostgreSQL operation", errors.New("root cause"))
}

func TestSafeErrorCapturesWrappingLocation(t *testing.T) {
	trace, ok := errtrace.FromError(safeErrorForStackTest())
	if !ok {
		t.Fatal("safeError did not expose a captured stack")
	}
	formatted := trace.String()
	if !strings.Contains(formatted, "postgres.safeErrorForStackTest") || !strings.Contains(formatted, "pool_test.go:") {
		t.Fatalf("trace = %q", formatted)
	}
}

func TestBuildPoolConfigAppliesExplicitBounds(t *testing.T) {
	configuration := testPoolConfiguration()
	poolConfig, err := buildPoolConfig(PoolOptions{
		Configuration: configuration,
		Application:   "fixthe-api",
		Logger:        testLogger(&bytes.Buffer{}),
		Tracer:        noop.NewTracerProvider().Tracer("test"),
		MeterProvider: metricnoop.NewMeterProvider(),
		SlowThreshold: 250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("buildPoolConfig() error = %v", err)
	}

	if poolConfig.MinConns != configuration.MinConnections || poolConfig.MaxConns != configuration.MaxConnections {
		t.Fatalf("pool size = %d..%d", poolConfig.MinConns, poolConfig.MaxConns)
	}
	if poolConfig.ConnConfig.ConnectTimeout != configuration.ConnectTimeout || poolConfig.PingTimeout != configuration.HealthTimeout {
		t.Fatalf("timeouts = connect %s, ping %s", poolConfig.ConnConfig.ConnectTimeout, poolConfig.PingTimeout)
	}
	if got := poolConfig.ConnConfig.RuntimeParams["statement_timeout"]; got != "30000" {
		t.Fatalf("statement_timeout = %q", got)
	}
	if got := poolConfig.ConnConfig.RuntimeParams["application_name"]; got != "fixthe-api" {
		t.Fatalf("application_name = %q", got)
	}
	if _, ok := poolConfig.ConnConfig.Tracer.(*QueryTracer); !ok {
		t.Fatalf("tracer = %T", poolConfig.ConnConfig.Tracer)
	}
}

func TestBuildPoolConfigDoesNotExposeConnectionString(t *testing.T) {
	const secret = "postgres://secret-user:secret-password@%gh&%ij/secret-db"
	configuration := testPoolConfiguration()
	configuration.URL = secret
	_, err := buildPoolConfig(PoolOptions{
		Configuration: configuration,
		Application:   "fixthe-api",
		Logger:        testLogger(&bytes.Buffer{}),
		Tracer:        noop.NewTracerProvider().Tracer("test"),
		MeterProvider: metricnoop.NewMeterProvider(),
	})
	if err == nil {
		t.Fatal("buildPoolConfig() error = nil")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "secret-password") {
		t.Fatalf("error exposes connection string: %q", err)
	}
}

func TestBoundedContextPreservesEarlierParentDeadline(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	bounded, boundedCancel := boundedContext(parent, time.Second)
	defer boundedCancel()

	parentDeadline, _ := parent.Deadline()
	boundedDeadline, _ := bounded.Deadline()
	if !parentDeadline.Equal(boundedDeadline) {
		t.Fatalf("deadline = %s, want %s", boundedDeadline, parentDeadline)
	}
}

func testPoolConfiguration() config.PostgreSQL {
	return config.PostgreSQL{
		URL:                "postgres://test:test@localhost:5432/fixthe_test?sslmode=disable",
		ConnectTimeout:     5 * time.Second,
		AcquireTimeout:     2 * time.Second,
		StatementTimeout:   30 * time.Second,
		HealthTimeout:      2 * time.Second,
		MinConnections:     1,
		MaxConnections:     10,
		MaxConnLifetime:    30 * time.Minute,
		MaxConnIdleTime:    5 * time.Minute,
		HealthCheckPeriod:  30 * time.Second,
		SlowQueryThreshold: 500 * time.Millisecond,
	}
}

func testLogger(output *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
