package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"sync"
	"time"

	"mendry/backend/internal/platform/config"
	"mendry/backend/internal/platform/observability"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

var applicationNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// PoolOptions 声明 pgxpool 的已验证配置、进程身份和观测依赖。
type PoolOptions struct {
	Configuration config.PostgreSQL
	Application   string
	Logger        *slog.Logger
	Tracer        trace.Tracer
	MeterProvider metric.MeterProvider
	SlowThreshold time.Duration
}

// Pool 持有一个进程独占的有界 pgxpool，并统一 acquire、health 和关闭行为。
type Pool struct {
	pool           *pgxpool.Pool
	logger         *slog.Logger
	acquireTimeout time.Duration
	healthTimeout  time.Duration
	metrics        *poolMetrics
	closeOnce      sync.Once
	closeDone      chan struct{}
	closeError     error
}

// Open 解析安全配置、创建 pool，并在返回前完成一次 startup ping。
// 任何 parse/connect failure 都使用固定错误文本包装原因，避免 connection string
// 通过进程最终错误进入 stderr 或日志。
func Open(ctx context.Context, options PoolOptions) (*Pool, error) {
	poolConfig, err := buildPoolConfig(options)
	if err != nil {
		return nil, err
	}

	rawPool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, newSafeError("create PostgreSQL pool", err)
	}
	pool := &Pool{
		pool:           rawPool,
		logger:         options.Logger,
		acquireTimeout: options.Configuration.AcquireTimeout,
		healthTimeout:  options.Configuration.HealthTimeout,
		closeDone:      make(chan struct{}),
	}
	pool.metrics, err = newPoolMetrics(options.MeterProvider.Meter("mendry/backend/postgres"), rawPool)
	if err != nil {
		rawPool.Close()
		return nil, err
	}
	if err := pool.Health(ctx); err != nil {
		_ = pool.metrics.close()
		rawPool.Close()
		return nil, newSafeError("verify PostgreSQL startup health", err)
	}
	pool.logState(context.WithoutCancel(ctx), "ready")
	return pool, nil
}

func buildPoolConfig(options PoolOptions) (*pgxpool.Config, error) {
	if options.Logger == nil {
		return nil, fmt.Errorf("PostgreSQL logger is required")
	}
	if options.Tracer == nil {
		return nil, fmt.Errorf("PostgreSQL tracer is required")
	}
	if !applicationNamePattern.MatchString(options.Application) {
		return nil, fmt.Errorf("PostgreSQL application name is invalid")
	}
	configuration := options.Configuration
	if configuration.URL == "" || configuration.ConnectTimeout <= 0 || configuration.AcquireTimeout <= 0 ||
		configuration.StatementTimeout <= 0 || configuration.HealthTimeout <= 0 || configuration.MaxConnections <= 0 ||
		configuration.MinConnections < 0 || configuration.MinConnections > configuration.MaxConnections ||
		configuration.MaxConnLifetime <= 0 || configuration.MaxConnIdleTime <= 0 || configuration.HealthCheckPeriod <= 0 ||
		configuration.SlowQueryThreshold <= 0 {
		return nil, fmt.Errorf("PostgreSQL configuration is invalid")
	}

	poolConfig, err := pgxpool.ParseConfig(configuration.URL)
	if err != nil {
		return nil, newSafeError("parse PostgreSQL configuration", err)
	}
	if options.MeterProvider == nil {
		return nil, fmt.Errorf("PostgreSQL meter provider is required")
	}
	slowThreshold := configuration.SlowQueryThreshold
	if options.SlowThreshold > 0 {
		slowThreshold = options.SlowThreshold
	}
	queryTracer, err := NewQueryTracer(options.Logger, options.Tracer,
		options.MeterProvider.Meter("mendry/backend/postgres"), slowThreshold, configuration.QueryDebug)
	if err != nil {
		return nil, err
	}
	poolConfig.ConnConfig.ConnectTimeout = configuration.ConnectTimeout
	poolConfig.ConnConfig.Tracer = queryTracer
	poolConfig.ConnConfig.RuntimeParams["application_name"] = options.Application
	poolConfig.ConnConfig.RuntimeParams["statement_timeout"] = strconv.FormatInt(configuration.StatementTimeout.Milliseconds(), 10)
	poolConfig.MinConns = configuration.MinConnections
	poolConfig.MaxConns = configuration.MaxConnections
	poolConfig.MaxConnLifetime = configuration.MaxConnLifetime
	poolConfig.MaxConnIdleTime = configuration.MaxConnIdleTime
	poolConfig.HealthCheckPeriod = configuration.HealthCheckPeriod
	poolConfig.PingTimeout = configuration.HealthTimeout
	return poolConfig, nil
}

// Health 使用独立的 health timeout 获取连接并执行 PostgreSQL ping。
func (p *Pool) Health(ctx context.Context) error {
	healthContext, cancel := boundedContext(ctx, p.healthTimeout)
	defer cancel()

	acquired, err := p.acquire(healthContext)
	if err != nil {
		return newSafeError("acquire PostgreSQL health connection", err)
	}
	defer acquired.Release()
	if err := acquired.Ping(WithOperation(healthContext, "postgres.health")); err != nil {
		return newSafeError("ping PostgreSQL", err)
	}
	return nil
}

// BeginTx 在独立 acquire timeout 内开始 transaction；返回的 pgx transaction
// 仍由 caller 使用原始 context 执行并负责 commit/rollback。
func (p *Pool) BeginTx(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	acquireContext, cancel := boundedContext(ctx, p.acquireTimeout)
	defer cancel()
	return p.pool.BeginTx(acquireContext, options)
}

// Exec 实现 sqlc 生成代码所需的 DBTX contract，并在执行前单独限制 acquire。
func (p *Pool) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	acquired, err := p.acquire(ctx)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	defer acquired.Release()
	return acquired.Exec(ctx, sql, arguments...)
}

// Query 实现 sqlc DBTX contract；connection 会在 rows 关闭或耗尽时释放。
func (p *Pool) Query(ctx context.Context, sql string, arguments ...any) (pgx.Rows, error) {
	acquired, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := acquired.Query(ctx, sql, arguments...)
	if err != nil {
		acquired.Release()
		return nil, err
	}
	return &releasingRows{Rows: rows, release: acquired.Release}, nil
}

// QueryRow 实现 sqlc DBTX contract；connection 会在 Scan 完成后释放。
func (p *Pool) QueryRow(ctx context.Context, sql string, arguments ...any) pgx.Row {
	acquired, err := p.acquire(ctx)
	if err != nil {
		return errorRow{err: err}
	}
	return &releasingRow{row: acquired.QueryRow(ctx, sql, arguments...), release: acquired.Release}
}

// Acquire 为 migration 和少数需要 session ownership 的 adapter 获取连接。
// 普通 feature query 应优先使用 sqlc 对 Pool 的 DBTX contract。
func (p *Pool) Acquire(ctx context.Context) (*pgxpool.Conn, error) {
	return p.acquire(ctx)
}

func (p *Pool) acquire(ctx context.Context) (*pgxpool.Conn, error) {
	acquireContext, cancel := boundedContext(ctx, p.acquireTimeout)
	defer cancel()
	connection, err := p.pool.Acquire(acquireContext)
	if err != nil {
		return nil, newSafeError("acquire PostgreSQL connection", err)
	}
	return connection, nil
}

// Close 等待已借出的 connection 归还，但受 caller 的 shutdown stage context 限制。
// pgxpool.Close 本身没有 context，因此超时只允许进程继续后续清理；后台关闭会在
// 最后一个 connection 归还后自行完成。
func (p *Pool) Close(ctx context.Context) error {
	p.closeOnce.Do(func() {
		go func() {
			p.pool.Close()
			p.closeError = p.metrics.close()
			close(p.closeDone)
		}()
	})

	select {
	case <-p.closeDone:
		if p.closeError != nil {
			return fmt.Errorf("unregister PostgreSQL pool metrics: %w", p.closeError)
		}
		p.logState(context.WithoutCancel(ctx), "closed")
		return nil
	case <-ctx.Done():
		return newSafeError("close PostgreSQL pool", ctx.Err())
	}
}

func (p *Pool) logState(ctx context.Context, health string) {
	statistics := p.pool.Stat()
	observability.Log(ctx, p.logger, slog.LevelInfo, observability.EventDBPoolState, "pool state changed",
		slog.String(observability.FieldComponent, "postgres"),
		slog.String(observability.FieldHealth, health),
		slog.Int64(observability.FieldPoolAcquired, int64(statistics.AcquiredConns())),
		slog.Int64(observability.FieldPoolIdle, int64(statistics.IdleConns())),
		slog.Int64(observability.FieldPoolTotal, int64(statistics.TotalConns())),
	)
}

func boundedContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if deadline, ok := parent.Deadline(); ok && time.Until(deadline) <= timeout {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, timeout)
}

type releasingRows struct {
	pgx.Rows
	release func()
	once    sync.Once
}

func (r *releasingRows) Close() {
	r.Rows.Close()
	r.once.Do(r.release)
}

func (r *releasingRows) Next() bool {
	next := r.Rows.Next()
	if !next {
		r.once.Do(r.release)
	}
	return next
}

func (r *releasingRows) Scan(destinations ...any) error {
	err := r.Rows.Scan(destinations...)
	if err != nil {
		r.once.Do(r.release)
	}
	return err
}

func (r *releasingRows) Values() ([]any, error) {
	values, err := r.Rows.Values()
	if err != nil {
		r.once.Do(r.release)
	}
	return values, err
}

type releasingRow struct {
	row     pgx.Row
	release func()
	once    sync.Once
}

func (r *releasingRow) Scan(destinations ...any) error {
	defer r.once.Do(r.release)
	return r.row.Scan(destinations...)
}

type errorRow struct {
	err error
}

func (r errorRow) Scan(...any) error {
	return r.err
}

var _ interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
} = (*Pool)(nil)
