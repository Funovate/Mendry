package migrate

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"fixthe/backend/internal/commands/migrate/migratedb"
	"fixthe/backend/internal/platform/observability"
	"fixthe/backend/internal/platform/postgres"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const migrationLockID int64 = 751698837750598474

type connection interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type connectionSource interface {
	Acquire(context.Context) (connection, func(), error)
}

// RunnerOptions 声明 migration 所需的 connection ownership、lock timeout 和 logger。
type RunnerOptions struct {
	Connections connectionSource
	Logger      *slog.Logger
	LockTimeout time.Duration
}

// Runner 按版本应用 embedded migration，并验证 immutable checksum。
type Runner struct {
	connections connectionSource
	logger      *slog.Logger
	lockTimeout time.Duration
	migrations  []Migration
}

// NewRunner 验证 migration source 和运行依赖，创建尚未访问数据库的 Runner。
func NewRunner(options RunnerOptions) (*Runner, error) {
	if options.Connections == nil || options.Logger == nil {
		return nil, fmt.Errorf("migration dependencies are required")
	}
	if options.LockTimeout <= 0 {
		return nil, fmt.Errorf("migration lock timeout must be positive")
	}
	migrations, err := loadMigrations(embeddedMigrations)
	if err != nil {
		return nil, err
	}
	return &Runner{
		connections: options.Connections,
		logger:      options.Logger,
		lockTimeout: options.LockTimeout,
		migrations:  migrations,
	}, nil
}

// Run 在一条独占 connection 上持有 session advisory lock，验证历史并顺序应用。
// 单个 migration 与其 metadata row 在同一 transaction 中提交；失败不会留下
// 已执行 SQL 却未记录版本的半完成状态。
func (r *Runner) Run(ctx context.Context) error {
	connection, release, err := r.connections.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer release()

	if err := r.acquireLock(ctx, connection); err != nil {
		return err
	}
	defer func() {
		// session 释放也会释放 advisory lock；显式 unlock 主要缩短正常退出时的
		// lock lifetime。主流程错误优先，因此 cleanup failure 只记录稳定 class。
		unlockContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		queries := migratedb.New(connection)
		if _, unlockError := queries.ReleaseMigrationLock(
			postgres.WithOperation(unlockContext, "migration.unlock"), migrationLockID,
		); unlockError != nil {
			observability.Log(unlockContext, r.logger, slog.LevelWarn, observability.EventMigrationLockCleanupFailed, "migration lock cleanup failed",
				slog.String(observability.FieldComponent, "migrate"),
				slog.String(observability.FieldOutcome, "failure"),
			)
		}
	}()

	applied, err := readApplied(ctx, connection)
	if err != nil {
		return err
	}
	for _, migration := range r.migrations {
		if checksum, ok := applied[migration.Version]; ok {
			if checksum != migration.Checksum {
				return fmt.Errorf("migration %06d checksum does not match immutable history", migration.Version)
			}
			continue
		}
		if err := r.apply(ctx, connection, migration); err != nil {
			return err
		}
	}
	observability.Log(ctx, r.logger, slog.LevelInfo, observability.EventMigrationsCompleted, "migrations completed",
		slog.String(observability.FieldComponent, "migrate"),
		slog.String(observability.FieldOutcome, "success"),
		slog.Int("migration_count", len(r.migrations)),
	)
	return nil
}

func (r *Runner) acquireLock(ctx context.Context, connection connection) error {
	lockContext, cancel := context.WithTimeout(ctx, r.lockTimeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	queries := migratedb.New(connection)

	for {
		acquired, err := queries.TryAcquireMigrationLock(
			postgres.WithOperation(lockContext, "migration.try_lock"), migrationLockID,
		)
		if err != nil {
			return fmt.Errorf("acquire PostgreSQL migration lock: %w", err)
		}
		if acquired {
			return nil
		}
		select {
		case <-lockContext.Done():
			return fmt.Errorf("acquire PostgreSQL migration lock: %w", lockContext.Err())
		case <-ticker.C:
		}
	}
}

func readApplied(ctx context.Context, connection connection) (map[int64]string, error) {
	// runner metadata table 的 bootstrap SQL 必须与第一份 migration 保持幂等，
	// 以便空数据库先获得历史表，再由 migration transaction 验证完整定义。
	queries := migratedb.New(connection)
	if err := queries.EnsureMigrationHistory(postgres.WithOperation(ctx, "migration.ensure_history")); err != nil {
		return nil, fmt.Errorf("ensure PostgreSQL migration history: %w", err)
	}

	rows, err := queries.ListAppliedMigrations(postgres.WithOperation(ctx, "migration.list_applied"))
	if err != nil {
		return nil, fmt.Errorf("list applied PostgreSQL migrations: %w", err)
	}
	applied := make(map[int64]string, len(rows))
	for _, row := range rows {
		applied[row.Version] = row.Checksum
	}
	return applied, nil
}

func (r *Runner) apply(ctx context.Context, connection connection, migration Migration) error {
	tx, err := connection.BeginTx(postgres.WithOperation(ctx, "migration.begin"), pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin PostgreSQL migration %06d: %w", migration.Version, err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		rollbackContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = tx.Rollback(postgres.WithOperation(rollbackContext, "migration.rollback"))
	}()

	if _, err := tx.Exec(postgres.WithOperation(ctx, "migration.apply"), migration.SQL); err != nil {
		return fmt.Errorf("apply PostgreSQL migration %06d: %w", migration.Version, err)
	}
	queries := migratedb.New(tx)
	if err := queries.RecordAppliedMigration(postgres.WithOperation(ctx, "migration.record"), migratedb.RecordAppliedMigrationParams{
		Version:  migration.Version,
		Name:     migration.Name,
		Checksum: migration.Checksum,
	}); err != nil {
		return fmt.Errorf("record PostgreSQL migration %06d: %w", migration.Version, err)
	}
	if err := tx.Commit(postgres.WithOperation(ctx, "migration.commit")); err != nil {
		return fmt.Errorf("commit PostgreSQL migration %06d: %w", migration.Version, err)
	}
	committed = true
	observability.Log(ctx, r.logger, slog.LevelInfo, observability.EventMigrationApplied, "migration applied",
		slog.String(observability.FieldComponent, "migrate"),
		slog.Int64("migration_version", migration.Version),
		slog.String("migration_name", migration.Name),
		slog.String(observability.FieldOutcome, "success"),
	)
	return nil
}

// PoolSource 将 platform Pool 适配为 runner 的 session-ownership contract。
type PoolSource struct {
	Pool *postgres.Pool
}

// Acquire 获取 migration 独占 connection，并返回幂等 release callback。
func (s PoolSource) Acquire(ctx context.Context) (connection, func(), error) {
	if s.Pool == nil {
		return nil, nil, fmt.Errorf("migration PostgreSQL pool is required")
	}
	acquired, err := s.Pool.Acquire(ctx)
	if err != nil {
		return nil, nil, err
	}
	return acquired, acquired.Release, nil
}
