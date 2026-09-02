package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"fixthe/backend/internal/platform/observability"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Isolation 是 application 可选择的 PostgreSQL transaction isolation contract。
type Isolation string

// 支持的 isolation level 必须显式映射到 pgx；READ COMMITTED 是默认值。
const (
	IsolationReadCommitted  Isolation = "read_committed"
	IsolationRepeatableRead Isolation = "repeatable_read"
	IsolationSerializable   Isolation = "serializable"
)

// RetryPolicy 允许 application 明确证明 callback 可安全重放后启用有界重试。
// RetrySafe=false 时 MaxAttempts 必须为 0 或 1，避免基础设施偷偷重复外部副作用。
type RetryPolicy struct {
	RetrySafe      bool
	MaxAttempts    int
	InitialBackoff time.Duration
}

// TxOptions 声明一次 application-owned transaction 的隔离、只读和重试策略。
type TxOptions struct {
	Isolation Isolation
	ReadOnly  bool
	Retry     RetryPolicy
}

type transactionBeginner interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

// TransactionRunner 执行 transaction 状态机，但不拥有 use case scope。
// adapter 用它绑定 transaction-scoped repository，再把不含 pgx 的 repository
// contract 交给 application callback。
type TransactionRunner struct {
	beginner       transactionBeginner
	logger         *slog.Logger
	tracer         trace.Tracer
	cleanupTimeout time.Duration
}

// NewTransactionRunner 创建 transaction runner，并拒绝缺失依赖或无界 cleanup。
func NewTransactionRunner(beginner transactionBeginner, logger *slog.Logger, tracer trace.Tracer, cleanupTimeout time.Duration) (*TransactionRunner, error) {
	if beginner == nil || logger == nil || tracer == nil {
		return nil, fmt.Errorf("PostgreSQL transaction dependencies are required")
	}
	if cleanupTimeout <= 0 {
		return nil, fmt.Errorf("PostgreSQL transaction cleanup timeout must be positive")
	}
	return &TransactionRunner{beginner: beginner, logger: logger, tracer: tracer, cleanupTimeout: cleanupTimeout}, nil
}

// Within 执行完整 transaction callback。
// fn 仅供 outbound adapter 绑定 transaction-scoped repository；application 和
// domain package 不应直接接收 pgx.Tx。
func (r *TransactionRunner) Within(ctx context.Context, options TxOptions, fn func(context.Context, pgx.Tx) error) error {
	if fn == nil {
		return fmt.Errorf("PostgreSQL transaction callback is required")
	}
	pgxOptions, attempts, backoff, err := validateTxOptions(options)
	if err != nil {
		return err
	}

	for attempt := 1; attempt <= attempts; attempt++ {
		err = r.runAttempt(ctx, options, pgxOptions, attempt, fn)
		if err == nil || attempt == attempts || !retryableTransactionError(err) {
			return err
		}
		if err := waitForRetry(ctx, backoff, attempt); err != nil {
			return err
		}
	}
	return err
}

func (r *TransactionRunner) runAttempt(ctx context.Context, options TxOptions, pgxOptions pgx.TxOptions, attempt int, fn func(context.Context, pgx.Tx) error) (result error) {
	transactionID, err := newTransactionID()
	if err != nil {
		return fmt.Errorf("create PostgreSQL transaction correlation ID: %w", err)
	}
	transactionContext := withTransactionID(ctx, transactionID)
	transactionContext, span := r.tracer.Start(transactionContext, "postgres.transaction",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("db.system.name", "postgresql"),
			attribute.String("db.transaction.isolation", string(normalizedIsolation(options.Isolation))),
			attribute.Bool("db.transaction.read_only", options.ReadOnly),
			attribute.Int("db.transaction.attempt", attempt),
		),
	)
	started := time.Now()

	tx, err := r.beginner.BeginTx(WithOperation(transactionContext, "transaction.begin"), pgxOptions)
	if err != nil {
		r.logTransaction(transactionContext, span, options, attempt, started, "begin_failed", err, "")
		return err
	}

	// panic 必须先 rollback 再向外重抛，确保 HTTP recovery boundary
	// 能处理原始 panic，同时 transaction 不会滞留并占满 pool。
	defer func() {
		panicValue := recover()
		if panicValue == nil {
			return
		}
		rollbackError := r.rollback(transactionContext, tx)
		r.logTransaction(transactionContext, span, options, attempt, started, "panic_rollback", fmt.Errorf("transaction callback panicked"), classifyError(rollbackError))
		panic(panicValue)
	}()

	callbackError := fn(transactionContext, tx)
	if callbackError != nil {
		rollbackError := r.rollback(transactionContext, tx)
		// rollback failure 只作为 cleanup classification 进入安全日志，绝不替换
		// application callback 的原始错误。
		r.logTransaction(transactionContext, span, options, attempt, started, "rolled_back", callbackError, classifyError(rollbackError))
		return callbackError
	}

	if commitError := tx.Commit(WithOperation(transactionContext, "transaction.commit")); commitError != nil {
		r.logTransaction(transactionContext, span, options, attempt, started, "commit_failed", commitError, "")
		return commitError
	}
	r.logTransaction(transactionContext, span, options, attempt, started, "committed", nil, "")
	return nil
}

func (r *TransactionRunner) rollback(ctx context.Context, tx pgx.Tx) error {
	cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.cleanupTimeout)
	defer cancel()
	return tx.Rollback(WithOperation(cleanupContext, "transaction.rollback"))
}

func (r *TransactionRunner) logTransaction(ctx context.Context, span trace.Span, options TxOptions, attempt int, started time.Time, outcome string, err error, cleanupClass string) {
	attrs := []slog.Attr{
		slog.String(observability.FieldComponent, "postgres"),
		slog.String(observability.FieldTransactionID, transactionIDFromContext(ctx)),
		slog.String(observability.FieldIsolation, string(normalizedIsolation(options.Isolation))),
		slog.Bool(observability.FieldReadOnly, options.ReadOnly),
		slog.Int(observability.FieldAttempt, attempt),
		slog.Int64(observability.FieldDurationMS, time.Since(started).Milliseconds()),
		slog.String(observability.FieldOutcome, outcome),
	}
	level := slog.LevelDebug
	if err != nil {
		classification := classifyError(err)
		attrs = append(attrs, slog.String(observability.FieldErrorClass, classification))
		level = queryErrorLevel(classification)
		span.SetStatus(codes.Error, classification)
	}
	if cleanupClass != "" {
		attrs = append(attrs, slog.String(observability.FieldCleanupErrorClass, cleanupClass))
		if level < slog.LevelWarn {
			level = slog.LevelWarn
		}
	}
	span.SetAttributes(attribute.String("db.transaction.outcome", outcome))
	span.End()
	observability.Log(ctx, r.logger, level, observability.EventDBTransactionCompleted, "transaction completed", attrs...)
}

func validateTxOptions(options TxOptions) (pgx.TxOptions, int, time.Duration, error) {
	isolation := normalizedIsolation(options.Isolation)
	var pgxIsolation pgx.TxIsoLevel
	switch isolation {
	case IsolationReadCommitted:
		pgxIsolation = pgx.ReadCommitted
	case IsolationRepeatableRead:
		pgxIsolation = pgx.RepeatableRead
	case IsolationSerializable:
		pgxIsolation = pgx.Serializable
	default:
		return pgx.TxOptions{}, 0, 0, fmt.Errorf("unsupported PostgreSQL transaction isolation")
	}

	attempts := options.Retry.MaxAttempts
	if attempts == 0 {
		attempts = 1
	}
	if attempts < 1 || attempts > 5 {
		return pgx.TxOptions{}, 0, 0, fmt.Errorf("PostgreSQL transaction attempts must be between 1 and 5")
	}
	if attempts > 1 && !options.Retry.RetrySafe {
		return pgx.TxOptions{}, 0, 0, fmt.Errorf("PostgreSQL transaction retry requires an explicitly retry-safe callback")
	}
	backoff := options.Retry.InitialBackoff
	if attempts > 1 && (backoff < time.Millisecond || backoff > time.Second) {
		return pgx.TxOptions{}, 0, 0, fmt.Errorf("PostgreSQL transaction retry backoff must be between 1ms and 1s")
	}

	accessMode := pgx.ReadWrite
	if options.ReadOnly {
		accessMode = pgx.ReadOnly
	}
	return pgx.TxOptions{IsoLevel: pgxIsolation, AccessMode: accessMode}, attempts, backoff, nil
}

func normalizedIsolation(isolation Isolation) Isolation {
	if isolation == "" {
		return IsolationReadCommitted
	}
	return isolation
}

func waitForRetry(ctx context.Context, initial time.Duration, attempt int) error {
	delay := initial << (attempt - 1)
	if delay > time.Second {
		delay = time.Second
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func newTransactionID() (string, error) {
	identifier := make([]byte, 16)
	if _, err := rand.Read(identifier); err != nil {
		return "", err
	}
	return hex.EncodeToString(identifier), nil
}
