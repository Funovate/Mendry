package migrate

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestRunnerAppliesPendingMigrationAndReleasesSession(t *testing.T) {
	tx := &fakeTx{}
	connection := &fakeConnection{tx: tx, lockAcquired: true}
	source := &fakeSource{connection: connection}
	runner := &Runner{
		connections: source,
		logger:      testMigrationLogger(&bytes.Buffer{}),
		lockTimeout: time.Second,
		migrations: []Migration{{
			Version:  1,
			Name:     "initialize",
			SQL:      "CREATE TABLE example (id bigint PRIMARY KEY)",
			Checksum: strings.Repeat("a", 64),
		}},
	}

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !source.released || tx.commits != 1 || tx.rollbacks != 0 {
		t.Fatalf("released = %v, commits = %d, rollbacks = %d", source.released, tx.commits, tx.rollbacks)
	}
	if len(tx.execSQL) != 2 || !strings.Contains(tx.execSQL[1], "fixthe_schema_migrations") {
		t.Fatalf("transaction SQL = %#v", tx.execSQL)
	}
	if connection.unlocks != 1 {
		t.Fatalf("unlocks = %d", connection.unlocks)
	}
}

func TestRunnerRejectsAppliedChecksumDrift(t *testing.T) {
	connection := &fakeConnection{
		lockAcquired: true,
		applied:      [][2]any{{int64(1), strings.Repeat("b", 64)}},
	}
	runner := &Runner{
		connections: &fakeSource{connection: connection},
		logger:      testMigrationLogger(&bytes.Buffer{}),
		lockTimeout: time.Second,
		migrations: []Migration{{
			Version: 1, Name: "initialize", SQL: "SELECT 1", Checksum: strings.Repeat("a", 64),
		}},
	}

	err := runner.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("Run() error = %v", err)
	}
	if connection.begins != 0 {
		t.Fatalf("begins = %d", connection.begins)
	}
}

func TestRunnerRollsBackFailedMigration(t *testing.T) {
	tx := &fakeTx{execErrors: []error{errors.New("apply failed")}}
	connection := &fakeConnection{tx: tx, lockAcquired: true}
	runner := &Runner{
		connections: &fakeSource{connection: connection},
		logger:      testMigrationLogger(&bytes.Buffer{}),
		lockTimeout: time.Second,
		migrations:  []Migration{{Version: 1, Name: "broken", SQL: "SELECT invalid", Checksum: strings.Repeat("a", 64)}},
	}

	if err := runner.Run(context.Background()); err == nil {
		t.Fatal("Run() error = nil")
	}
	if tx.rollbacks != 1 || tx.commits != 0 {
		t.Fatalf("commits = %d, rollbacks = %d", tx.commits, tx.rollbacks)
	}
}

func testMigrationLogger(output *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

type fakeSource struct {
	connection connection
	released   bool
}

func (s *fakeSource) Acquire(context.Context) (connection, func(), error) {
	return s.connection, func() { s.released = true }, nil
}

type fakeConnection struct {
	tx           pgx.Tx
	lockAcquired bool
	applied      [][2]any
	beginError   error
	begins       int
	unlocks      int
}

func (c *fakeConnection) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	c.begins++
	return c.tx, c.beginError
}

func (c *fakeConnection) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "pg_advisory_unlock") {
		c.unlocks++
	}
	return pgconn.CommandTag{}, nil
}

func (c *fakeConnection) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return &fakeRows{values: c.applied}, nil
}

func (c *fakeConnection) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	if strings.Contains(sql, "pg_advisory_unlock") {
		c.unlocks++
	}
	return fakeBoolRow{value: c.lockAcquired}
}

type fakeBoolRow struct {
	value bool
}

func (r fakeBoolRow) Scan(destinations ...any) error {
	destination, ok := destinations[0].(*bool)
	if !ok {
		return errors.New("unexpected lock destination")
	}
	*destination = r.value
	return nil
}

type fakeRows struct {
	values [][2]any
	index  int
}

func (*fakeRows) Close()                                       {}
func (*fakeRows) Err() error                                   { return nil }
func (*fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (*fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Next() bool                                 { return r.index < len(r.values) }
func (r *fakeRows) Scan(destinations ...any) error {
	row := r.values[r.index]
	r.index++
	*destinations[0].(*int64) = row[0].(int64)
	*destinations[1].(*string) = row[1].(string)
	return nil
}
func (*fakeRows) Values() ([]any, error) { return nil, errors.New("not implemented") }
func (*fakeRows) RawValues() [][]byte    { return nil }
func (*fakeRows) Conn() *pgx.Conn        { return nil }

type fakeTx struct {
	execSQL     []string
	execErrors  []error
	commits     int
	rollbacks   int
	commitError error
}

func (*fakeTx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("nested transaction is not supported")
}
func (tx *fakeTx) Commit(context.Context) error { tx.commits++; return tx.commitError }
func (tx *fakeTx) Rollback(context.Context) error {
	tx.rollbacks++
	return nil
}
func (*fakeTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, errors.New("not implemented")
}
func (*fakeTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }
func (*fakeTx) LargeObjects() pgx.LargeObjects                         { return pgx.LargeObjects{} }
func (*fakeTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, errors.New("not implemented")
}
func (tx *fakeTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	index := len(tx.execSQL)
	tx.execSQL = append(tx.execSQL, sql)
	if index < len(tx.execErrors) {
		return pgconn.CommandTag{}, tx.execErrors[index]
	}
	return pgconn.CommandTag{}, nil
}
func (*fakeTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("not implemented")
}
func (*fakeTx) QueryRow(context.Context, string, ...any) pgx.Row {
	return fakeBoolRow{}
}
func (*fakeTx) Conn() *pgx.Conn { return nil }
