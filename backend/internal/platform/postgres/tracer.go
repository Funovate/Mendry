package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"fixthe/backend/internal/platform/observability"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type queryTraceState struct {
	started   time.Time
	operation string
	span      trace.Span
	statement string
}

type queryTraceContextKey struct{}

// QueryTracer 为 pgx query 生成安全日志和 OpenTelemetry span。
// 默认忽略 pgx 提供的 Args，并且不把原始 SQL 写入任何观测信号。
// 仅当 QueryDebug 打开时，已发出的 db.query.completed 记录才会带上 SQL 和参数。
type QueryTracer struct {
	logger        *slog.Logger
	tracer        trace.Tracer
	duration      metric.Float64Histogram
	slowThreshold time.Duration
	queryDebug    bool
}

// NewQueryTracer 创建数据库 query tracer；slowThreshold 只改变日志级别，
// queryDebug 只决定日志是否附带 SQL/参数，两者都不会把 statement 写入 span 或 metric。
func NewQueryTracer(logger *slog.Logger, tracer trace.Tracer, meter metric.Meter, slowThreshold time.Duration, queryDebug bool) (*QueryTracer, error) {
	duration, err := meter.Float64Histogram("fixthe.postgres.query.duration",
		metric.WithUnit("ms"),
		metric.WithDescription("PostgreSQL query duration by safe operation and outcome"),
	)
	if err != nil {
		return nil, fmt.Errorf("create PostgreSQL query duration metric: %w", err)
	}
	return &QueryTracer{
		logger:        logger,
		tracer:        tracer,
		duration:      duration,
		slowThreshold: slowThreshold,
		queryDebug:    queryDebug,
	}, nil
}

// TraceQueryStart 提取安全 operation，并创建不含 statement/args 的 client span。
func (t *QueryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	operation := operationFromContext(ctx, data.SQL)
	ctx, span := t.tracer.Start(ctx, "postgres."+operation,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("db.system.name", "postgresql"),
			attribute.String("db.operation.name", operation),
		),
	)
	state := queryTraceState{
		started:   time.Now(),
		operation: operation,
		span:      span,
	}
	if t.queryDebug {
		// 在 query start 展开 SQL：pgx 可能在结束后复用调用方的 Args。
		state.statement = interpolateQuery(data.SQL, data.Args)
	}
	return context.WithValue(ctx, queryTraceContextKey{}, state)
}

// TraceQueryEnd 完成 query 的观测记录；错误仅转为稳定 class，不记录数据库消息。
func (t *QueryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	state, ok := ctx.Value(queryTraceContextKey{}).(queryTraceState)
	if !ok {
		return
	}
	duration := time.Since(state.started)
	outcome := "success"
	level := slog.LevelDebug
	attrs := []slog.Attr{
		slog.String(observability.FieldComponent, "postgres"),
		slog.String(observability.FieldDBOperation, state.operation),
		slog.Int64(observability.FieldDurationMS, duration.Milliseconds()),
		slog.Int64(observability.FieldRowsAffected, data.CommandTag.RowsAffected()),
	}
	if transactionID := transactionIDFromContext(ctx); transactionID != "" {
		attrs = append(attrs, slog.String(observability.FieldTransactionID, transactionID))
	}

	if data.Err != nil {
		outcome = "failure"
		classification := classifyError(data.Err)
		attrs = append(attrs, slog.String(observability.FieldErrorClass, classification))
		level = queryErrorLevel(classification)
		state.span.SetStatus(codes.Error, classification)
	} else if t.slowThreshold > 0 && duration >= t.slowThreshold {
		level = slog.LevelWarn
	}
	if t.queryDebug {
		attrs = append(attrs, slog.String(observability.FieldDBQueryText, state.statement))
	}
	attrs = append(attrs, slog.String(observability.FieldOutcome, outcome))
	state.span.SetAttributes(attribute.String("db.operation.outcome", outcome))
	t.duration.Record(ctx, float64(duration.Microseconds())/1000,
		metric.WithAttributes(
			attribute.String("db.operation.name", state.operation),
			attribute.String("outcome", outcome),
		),
	)
	state.span.End()

	observability.Log(ctx, t.logger, level, observability.EventDBQueryCompleted, "query completed", attrs...)
}

func queryErrorLevel(classification string) slog.Level {
	switch classification {
	case errorClassCanceled, errorClassConflict:
		return slog.LevelInfo
	case errorClassTimeout, errorClassConstraint, errorClassSerialization, errorClassDeadlock:
		return slog.LevelWarn
	default:
		return slog.LevelError
	}
}
