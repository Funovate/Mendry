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
	platformredis "mendry/backend/internal/platform/redis"

	"go.opentelemetry.io/otel/trace"
)

type redisCloser interface {
	Close(context.Context) error
}

func openRedis(ctx context.Context, logger *slog.Logger, telemetryRuntime *observability.Telemetry, applicationName string, configuration config.Redis) (*platformredis.Client, error) {
	if !configuration.Enabled {
		return nil, nil
	}
	client, err := platformredis.Open(ctx, platformredis.ClientOptions{
		Configuration: configuration,
		Application:   applicationName,
		Logger:        logger,
		Tracer:        telemetryRuntime.Tracer("mendry/backend/redis"),
		MeterProvider: telemetryRuntime.MeterProvider(),
	})
	if err != nil {
		return nil, fmt.Errorf("open Redis: %w", err)
	}
	return client, nil
}

func redisDependency(client *platformredis.Client) application.Dependency {
	return application.Dependency{Name: "redis", Check: client.Health}
}

func closeRedis(client redisCloser, timeout time.Duration) error {
	if client == nil {
		return nil
	}
	// 根 context 取消后仍要给 Redis 独立的 close budget；client 必须在
	// telemetry shutdown 前关闭，使最终 pool state event 保持 trace correlation。
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return client.Close(ctx)
}

func finishWithDataClients(span trace.Span, telemetryRuntime *observability.Telemetry, redisClient *platformredis.Client, postgresPool postgresCloser, timeout time.Duration, processError error) error {
	// 具体指针先判空再转成 interface，避免 disabled Redis 的 typed nil 被误判为
	// 已启用 client 并在 shutdown 时触发空指针调用。
	var redisResource redisCloser
	if redisClient != nil {
		redisResource = redisClient
	}
	redisError := closeRedis(redisResource, timeout)
	postgresError := closePostgreSQL(postgresPool, timeout)
	span.End()
	return errors.Join(processError, redisError, postgresError, shutdownTelemetry(telemetryRuntime, timeout))
}
