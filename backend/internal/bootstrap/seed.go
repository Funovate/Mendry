package bootstrap

import (
	"context"
	"fmt"

	seedcommand "fixthe/backend/internal/commands/seed"
	"fixthe/backend/internal/platform/config"

	"go.opentelemetry.io/otel/trace"
)

// RunSeed 只连接 PostgreSQL，并原子写入显式开发数据。API 启动不会调用它。
func RunSeed(ctx context.Context, options Options) (result error) {
	configuration, err := config.LoadSeed(options.Lookup)
	if err != nil {
		return fmt.Errorf("load development seed configuration: %w", err)
	}
	logger, logSink, err := logger(options, "fixthe-seed", configuration.Common)
	if err != nil {
		return err
	}
	defer closeLogSink(&result, logSink)
	telemetryRuntime, err := telemetry(ctx, options, "fixthe-seed", configuration.Common.Environment)
	if err != nil {
		return err
	}
	ctx, processSpan := telemetryRuntime.Tracer("fixthe/backend/bootstrap").Start(ctx, "process.seed",
		trace.WithSpanKind(trace.SpanKindInternal),
	)
	logStart(ctx, logger, "seed")
	postgresPool, err := openPostgreSQL(ctx, logger, telemetryRuntime, "fixthe-seed", configuration.PostgreSQL)
	if err != nil {
		return finishProcess(ctx, processSpan, telemetryRuntime, configuration.Common.ShutdownTimeout, err)
	}
	runner, err := seedcommand.NewRunner(postgresPool)
	if err != nil {
		return finishWithPostgreSQL(processSpan, telemetryRuntime, postgresPool, configuration.Common.ShutdownTimeout,
			fmt.Errorf("create development seed runner: %w", err),
		)
	}
	if err := runner.Run(ctx); err != nil {
		return finishWithPostgreSQL(processSpan, telemetryRuntime, postgresPool, configuration.Common.ShutdownTimeout,
			fmt.Errorf("run development seed: %w", err),
		)
	}
	logStopped(ctx, logger, "seed")
	return finishWithPostgreSQL(processSpan, telemetryRuntime, postgresPool, configuration.Common.ShutdownTimeout, nil)
}
