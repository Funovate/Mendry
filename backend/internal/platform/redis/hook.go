package redis

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"regexp"
	"strings"
	"time"

	"mendry/backend/internal/platform/observability"

	redisclient "github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

var commandNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// CommandHook 为 Redis command 和 pipeline 记录低基数日志、trace 与 latency metric。
// Hook 永不读取 `Args()` 或 `String()`，因此 key、value 和 credential 不会进入观测数据。
type CommandHook struct {
	logger        *slog.Logger
	tracer        trace.Tracer
	duration      metric.Float64Histogram
	slowThreshold time.Duration
}

// NewCommandHook 创建安全 Redis instrumentation，并拒绝缺失依赖或无效阈值。
func NewCommandHook(logger *slog.Logger, tracer trace.Tracer, meter metric.Meter, slowThreshold time.Duration) (*CommandHook, error) {
	if logger == nil || tracer == nil || meter == nil {
		return nil, fmt.Errorf("Redis hook dependencies are required")
	}
	if slowThreshold <= 0 {
		return nil, fmt.Errorf("Redis slow command threshold must be positive")
	}
	duration, err := meter.Float64Histogram("mendry.redis.command.duration",
		metric.WithUnit("ms"),
		metric.WithDescription("Redis command latency by safe command and outcome"),
	)
	if err != nil {
		return nil, fmt.Errorf("create Redis command duration metric: %w", err)
	}
	return &CommandHook{logger: logger, tracer: tracer, duration: duration, slowThreshold: slowThreshold}, nil
}

// DialHook 保持 go-redis 的 dial 行为；endpoint 不会被 instrumentation 捕获。
func (h *CommandHook) DialHook(next redisclient.DialHook) redisclient.DialHook {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		return next(ctx, network, address)
	}
}

// ProcessHook 包裹单条 command，只使用经过约束的 protocol name 作为 operation。
func (h *CommandHook) ProcessHook(next redisclient.ProcessHook) redisclient.ProcessHook {
	return func(ctx context.Context, command redisclient.Cmder) error {
		return h.observe(ctx, commandName(command), 1, func(commandContext context.Context) error {
			return next(commandContext, command)
		})
	}
}

// ProcessPipelineHook 将整批 pipeline 记录为一个 operation，避免逐条重复 span 和日志。
func (h *CommandHook) ProcessPipelineHook(next redisclient.ProcessPipelineHook) redisclient.ProcessPipelineHook {
	return func(ctx context.Context, commands []redisclient.Cmder) error {
		return h.observe(ctx, "pipeline", len(commands), func(commandContext context.Context) error {
			return next(commandContext, commands)
		})
	}
}

func (h *CommandHook) observe(ctx context.Context, command string, count int, run func(context.Context) error) error {
	ctx, span := h.tracer.Start(ctx, "redis.command",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("db.system.name", "redis"),
			attribute.String("db.operation.name", command),
		),
	)
	started := time.Now()
	err := run(ctx)
	elapsed := time.Since(started)
	outcome := "success"
	level := slog.LevelDebug
	attrs := []slog.Attr{
		slog.String(observability.FieldComponent, "redis"),
		slog.String(observability.FieldRedisCommand, command),
		slog.Int(observability.FieldCommandCount, count),
		slog.Int64(observability.FieldDurationMS, elapsed.Milliseconds()),
	}
	if err != nil {
		classification := classifyError(err)
		outcome = "failure"
		attrs = append(attrs, slog.String(observability.FieldErrorClass, classification))
		span.SetStatus(codes.Error, classification)
		level = redisErrorLevel(classification)
	} else if elapsed >= h.slowThreshold {
		level = slog.LevelWarn
	}
	attrs = append(attrs, slog.String(observability.FieldOutcome, outcome))
	span.SetAttributes(attribute.String("db.operation.outcome", outcome))
	span.End()
	h.duration.Record(ctx, float64(elapsed)/float64(time.Millisecond), metric.WithAttributes(
		attribute.String("command", command),
		attribute.String("outcome", outcome),
	))
	observability.Log(ctx, h.logger, level, observability.EventRedisCommandCompleted, "command completed", attrs...)
	return err
}

func commandName(command redisclient.Cmder) string {
	if command == nil {
		return "unknown"
	}
	name := strings.ToLower(command.Name())
	if !commandNamePattern.MatchString(name) {
		return "unknown"
	}
	return name
}

func redisErrorLevel(classification string) slog.Level {
	switch classification {
	case errorClassCanceled, errorClassNotFound:
		return slog.LevelDebug
	case errorClassTimeout, errorClassUnavailable:
		return slog.LevelWarn
	default:
		return slog.LevelError
	}
}
