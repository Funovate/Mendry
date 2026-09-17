package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"mendry/backend/internal/modules/system/application"
	"mendry/backend/internal/platform/config"
	"mendry/backend/internal/platform/observability"
	"mendry/backend/internal/platform/postgres"

	"go.opentelemetry.io/otel/trace"
)

type postgresCloser interface {
	Close(context.Context) error
}

func openPostgreSQL(ctx context.Context, logger *slog.Logger, telemetryRuntime *observability.Telemetry, applicationName string, configuration config.PostgreSQL) (*postgres.Pool, error) {
	pool, err := postgres.Open(ctx, postgres.PoolOptions{
		Configuration: configuration,
		Application:   applicationName,
		Logger:        logger,
		Tracer:        telemetryRuntime.Tracer("mendry/backend/postgres"),
		MeterProvider: telemetryRuntime.MeterProvider(),
	})
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL: %w", err)
	}
	return pool, nil
}

func postgresDependency(pool *postgres.Pool) application.Dependency {
	return application.Dependency{Name: "postgresql", Check: pool.Health}
}

func closePostgreSQL(pool postgresCloser, timeout time.Duration) error {
	// 根 context 在正常 signal shutdown 时已取消；pool closure 使用独立 stage
	// budget，且必须先于 telemetry shutdown，以便关闭事件仍可携带 trace correlation。
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return pool.Close(ctx)
}

func finishWithPostgreSQL(span trace.Span, telemetryRuntime *observability.Telemetry, pool *postgres.Pool, timeout time.Duration, processError error) error {
	postgresError := closePostgreSQL(pool, timeout)
	span.End()
	return errors.Join(processError, postgresError, shutdownTelemetry(telemetryRuntime, timeout))
}
