package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"mendry/backend/internal/modules/remediation/adapter/postgres/remediationdb"
	"mendry/backend/internal/modules/remediation/domain"
)

type executionDatabase interface {
	remediationdb.DBTX
	Acquire(context.Context) (*pgxpool.Conn, error)
}

// ExecutionStore coordinates execution across processes using session advisory
// locks. The caller must bound active leases: each owns a dedicated connection
// outside the pool, leaving pool capacity available for ordinary run writes.
type ExecutionStore struct {
	db executionDatabase
}

var _ domain.RunExecutionStore = (*ExecutionStore)(nil)

// NewExecutionStore constructs the shared execution and recovery adapter.
func NewExecutionStore(database executionDatabase) (*ExecutionStore, error) {
	if database == nil {
		return nil, errors.New("remediation execution database is required")
	}
	return &ExecutionStore{db: database}, nil
}

// TryAcquireRun acquires a nonblocking session lock for a canonical run UUID.
// Hijack removes the session from the pool before any lock is acquired. Every
// unsuccessful path closes it; a locked session can never return to the pool.
func (s *ExecutionStore) TryAcquireRun(ctx context.Context, runID string) (domain.RunExecutionLease, bool, error) {
	id, err := uuid.Parse(runID)
	if err != nil {
		return nil, false, errors.New("invalid remediation execution run ID")
	}
	pooled, err := s.db.Acquire(ctx)
	if err != nil {
		// Connection errors can contain credentials or a DSN. Do not propagate
		// them to execution/recovery logs; retain caller cancellation instead.
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		return nil, false, errors.New("acquire remediation execution session failed")
	}
	conn := pooled.Hijack()
	sum := sha256.Sum256([]byte("mendry:remediation:run-execution:v1:" + id.String()))
	key := int64(binary.BigEndian.Uint64(sum[:8]))
	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1::bigint)`, key).Scan(&acquired); err != nil {
		_ = conn.Close(ctx)
		return nil, false, fmt.Errorf("acquire remediation execution lock: %w", err)
	}
	if !acquired {
		if err := conn.Close(ctx); err != nil {
			return nil, false, fmt.Errorf("close unowned remediation execution session: %w", err)
		}
		return nil, false, nil
	}
	return &runExecutionLease{conn: conn, gate: make(chan struct{}, 1)}, true, nil
}

type runExecutionLease struct {
	gate chan struct{}
	conn *pgx.Conn
}

// Check detects loss of the dedicated session (and therefore its lock).
func (l *runExecutionLease) Check(ctx context.Context) error {
	// An active query/transaction already uses the session. Do not classify
	// waiting for that query as session loss.
	select {
	case l.gate <- struct{}{}:
		defer l.unlock()
	default:
		return nil
	}
	if err := l.conn.Ping(ctx); err != nil {
		return fmt.Errorf("check remediation execution session: %w", err)
	}
	return nil
}

// Release closes the dedicated connection, releasing all session locks even
// when the caller's context has expired. pgx Close closes its socket on errors.
// Serialize with Check because pgx connections do not support concurrent use.
func (l *runExecutionLease) Release(ctx context.Context) error {
	var acquired bool
	select {
	case l.gate <- struct{}{}:
		acquired = true
	default:
		select {
		case l.gate <- struct{}{}:
			acquired = true
		case <-ctx.Done():
		}
	}
	if acquired {
		defer l.unlock()
	}
	if err := l.conn.Close(context.Background()); err != nil {
		return fmt.Errorf("close remediation execution session: %w", err)
	}
	return nil
}

// ListRecoverableRunIDs lists the latest active attempt of each series whose
// generation and deployed commit still match its incident. Context version is
// deliberately not filtered: a run may be collecting newly arrived context.
// Candidates remain advisory; the executor must reload state under its lease.
func (s *ExecutionStore) ListRecoverableRunIDs(ctx context.Context, afterID string, limit int) ([]string, error) {
	var after pgtype.UUID
	if afterID != "" {
		id, err := uuid.Parse(afterID)
		if err != nil {
			return nil, errors.New("invalid remediation recovery cursor")
		}
		after = pgtype.UUID{Bytes: id, Valid: true}
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
SELECT r.id::text
FROM remediation_run AS r
JOIN remediation_series AS s ON s.id = r.series_id
JOIN incidents AS i ON i.id = s.incident_id
WHERE ($1::uuid IS NULL OR r.id > $1::uuid)
  AND s.lifecycle_generation = i.lifecycle_generation
  AND s.deployed_commit = i.deployed_commit
  AND r.state IN (
      'queued', 'preparing_context', 'diagnosing', 'collecting_more_context',
      'planning', 'running', 'patching', 'validating', 'publishing'
  )
  AND NOT EXISTS (
      SELECT 1 FROM remediation_run AS newer
      WHERE newer.series_id = r.series_id
        AND newer.attempt_number > r.attempt_number
  )
ORDER BY r.id
LIMIT $2`, after, limit)
	if err != nil {
		return nil, fmt.Errorf("list recoverable remediation runs: %w", err)
	}
	defer rows.Close()
	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan recoverable remediation run: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read recoverable remediation runs: %w", err)
	}
	return ids, nil
}
