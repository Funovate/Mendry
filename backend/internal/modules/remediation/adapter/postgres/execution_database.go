package postgres

import (
	"context"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type executionSessionKey struct{}

func (l *runExecutionLease) BindExecutionContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, executionSessionKey{}, l)
}

func (l *runExecutionLease) acquire(ctx context.Context) error {
	select {
	case l.gate <- struct{}{}:
		return nil
	default:
		select {
		case l.gate <- struct{}{}:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (l *runExecutionLease) unlock() {
	<-l.gate
}

// executionAwareDatabase routes owned run/checkpoint operations through the
// session holding the advisory lock. A dead session never falls back to the pool.
type executionAwareDatabase struct {
	transactor
}

func wrapExecutionAwareDatabase(database transactor) transactor {
	if database == nil {
		return nil
	}
	if _, ok := database.(executionAwareDatabase); ok {
		return database
	}
	return executionAwareDatabase{transactor: database}
}

func executionSession(ctx context.Context) *runExecutionLease {
	lease, _ := ctx.Value(executionSessionKey{}).(*runExecutionLease)
	return lease
}

func (d executionAwareDatabase) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	l := executionSession(ctx)
	if l == nil {
		return d.transactor.Exec(ctx, sql, args...)
	}
	if err := l.acquire(ctx); err != nil {
		return pgconn.CommandTag{}, err
	}
	defer l.unlock()
	return l.conn.Exec(ctx, sql, args...)
}

func (d executionAwareDatabase) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	l := executionSession(ctx)
	if l == nil {
		return d.transactor.Query(ctx, sql, args...)
	}
	if err := l.acquire(ctx); err != nil {
		return nil, err
	}
	rows, err := l.conn.Query(ctx, sql, args...)
	if err != nil {
		l.unlock()
		return nil, err
	}
	return &executionRows{Rows: rows, release: l.unlock}, nil
}

func (d executionAwareDatabase) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	l := executionSession(ctx)
	if l == nil {
		return d.transactor.QueryRow(ctx, sql, args...)
	}
	if err := l.acquire(ctx); err != nil {
		return executionErrorRow{err: err}
	}
	return &executionRow{row: l.conn.QueryRow(ctx, sql, args...), release: l.unlock}
}

func (d executionAwareDatabase) BeginTx(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	l := executionSession(ctx)
	if l == nil {
		return d.transactor.BeginTx(ctx, options)
	}
	if err := l.acquire(ctx); err != nil {
		return nil, err
	}
	tx, err := l.conn.BeginTx(ctx, options)
	if err != nil {
		l.unlock()
		return nil, err
	}
	return &executionTx{Tx: tx, release: l.unlock}, nil
}

type executionErrorRow struct {
	err error
}

func (r executionErrorRow) Scan(...any) error {
	return r.err
}

type executionRow struct {
	row     pgx.Row
	release func()
	once    sync.Once
}

func (r *executionRow) Scan(dest ...any) error {
	defer r.once.Do(r.release)
	return r.row.Scan(dest...)
}

type executionRows struct {
	pgx.Rows
	release func()
	once    sync.Once
}

func (r *executionRows) Close() {
	r.Rows.Close()
	r.once.Do(r.release)
}

func (r *executionRows) Next() bool {
	next := r.Rows.Next()
	if !next {
		r.once.Do(r.release)
	}
	return next
}

type executionTx struct {
	pgx.Tx
	release func()
	once    sync.Once
}

func (t *executionTx) Commit(ctx context.Context) error {
	defer t.once.Do(t.release)
	return t.Tx.Commit(ctx)
}

func (t *executionTx) Rollback(ctx context.Context) error {
	defer t.once.Do(t.release)
	return t.Tx.Rollback(ctx)
}
