package bootstrap

import (
	"context"
	"fmt"

	migratecommand "fixthe/backend/internal/commands/migrate"
	"fixthe/backend/internal/platform/config"

	"go.opentelemetry.io/otel/trace"
)

// RunMigrate 执行唯一可修改 PostgreSQL schema 的显式 migration 入口。
// API 不导入 migration runner，也不会在 startup 时隐式修改 schema。
func RunMigrate(ctx context.Context, options Options) (result error) {
	migrateConfig, err := config.LoadMigrate(options.Lookup)
	if err != nil {
		return fmt.Errorf("load migrate configuration: %w", err)
	}

	logger, logSink, err := logger(options, "fixthe-migrate", migrateConfig.Common)
	if err != nil {
		return err
	}
	defer closeLogSink(&result, logSink)
	telemetryRuntime, err := telemetry(ctx, options, "fixthe-migrate", migrateConfig.Common.Environment)
	if err != nil {
		return err
	}
	ctx, processSpan := telemetryRuntime.Tracer("fixthe/backend/bootstrap").Start(ctx, "process.migrate",
		trace.WithSpanKind(trace.SpanKindInternal),
	)
	logStart(ctx, logger, "migrate")
	postgresPool, err := openPostgreSQL(ctx, logger, telemetryRuntime, "fixthe-migrate", migrateConfig.PostgreSQL)
	if err != nil {
		return finishProcess(ctx, processSpan, telemetryRuntime, migrateConfig.Common.ShutdownTimeout, err)
	}
	logStarted(ctx, logger, "migrate")

	if err := ctx.Err(); err != nil {
		return finishWithPostgreSQL(processSpan, telemetryRuntime, postgresPool, migrateConfig.Common.ShutdownTimeout, err)
	}
	runner, err := migratecommand.NewRunner(migratecommand.RunnerOptions{
		Connections: migratecommand.PoolSource{Pool: postgresPool},
		Logger:      logger,
		LockTimeout: migrateConfig.MigrationLockTimeout,
	})
	if err != nil {
		return finishWithPostgreSQL(processSpan, telemetryRuntime, postgresPool, migrateConfig.Common.ShutdownTimeout,
			fmt.Errorf("create migration runner: %w", err),
		)
	}
	if err := runner.Run(ctx); err != nil {
		return finishWithPostgreSQL(processSpan, telemetryRuntime, postgresPool, migrateConfig.Common.ShutdownTimeout,
			fmt.Errorf("run migrations: %w", err),
		)
	}
	logStopped(ctx, logger, "migrate")
	return finishWithPostgreSQL(processSpan, telemetryRuntime, postgresPool, migrateConfig.Common.ShutdownTimeout, nil)
}
