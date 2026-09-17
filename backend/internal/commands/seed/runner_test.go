package seed

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestRunnerAppliesSeedAtomically(t *testing.T) {
	tx := &fakeTx{}
	runner, err := NewRunner(fakeDatabase{tx: tx})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if tx.commits != 1 || tx.rollbacks != 0 || !strings.Contains(tx.sql, "INSERT INTO projects") ||
		!strings.Contains(tx.sql, "INSERT INTO incidents") {
		t.Fatalf("commits = %d, rollbacks = %d, sql = %q", tx.commits, tx.rollbacks, tx.sql)
	}
}

func TestRunnerRollsBackFailedSeed(t *testing.T) {
	tx := &fakeTx{execError: errors.New("apply failed")}
	runner, err := NewRunner(fakeDatabase{tx: tx})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	if err := runner.Run(context.Background()); err == nil {
		t.Fatal("Run() error = nil")
	}
	if tx.commits != 0 || tx.rollbacks != 1 {
		t.Fatalf("commits = %d, rollbacks = %d", tx.commits, tx.rollbacks)
	}
}

type fakeDatabase struct{ tx pgx.Tx }

func (d fakeDatabase) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) { return d.tx, nil }

type fakeTx struct {
	sql       string
	execError error
	commits   int
	rollbacks int
}

func (*fakeTx) Begin(context.Context) (pgx.Tx, error) { return nil, errors.New("not implemented") }
func (t *fakeTx) Commit(context.Context) error        { t.commits++; return nil }
func (t *fakeTx) Rollback(context.Context) error      { t.rollbacks++; return nil }
func (*fakeTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, errors.New("not implemented")
}
func (*fakeTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }
func (*fakeTx) LargeObjects() pgx.LargeObjects                         { return pgx.LargeObjects{} }
func (*fakeTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, errors.New("not implemented")
}
func (t *fakeTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	t.sql = sql
	return pgconn.CommandTag{}, t.execError
}
func (*fakeTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("not implemented")
}
func (*fakeTx) QueryRow(context.Context, string, ...any) pgx.Row { return nil }
func (*fakeTx) Conn() *pgx.Conn                                  { return nil }
