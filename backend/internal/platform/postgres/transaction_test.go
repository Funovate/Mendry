package postgres

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestTransactionRunnerCommitsAndMapsOptions(t *testing.T) {
	tx := &fakeTx{}
	beginner := &fakeBeginner{transactions: []pgx.Tx{tx}}
	runner := newTestTransactionRunner(t, beginner, &bytes.Buffer{})

	err := runner.Within(context.Background(), TxOptions{
		Isolation: IsolationSerializable,
		ReadOnly:  true,
	}, func(ctx context.Context, received pgx.Tx) error {
		if received != tx || transactionIDFromContext(ctx) == "" {
			t.Fatalf("transaction callback did not receive scoped dependencies")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Within() error = %v", err)
	}
	if tx.commits != 1 || tx.rollbacks != 0 {
		t.Fatalf("commit = %d, rollback = %d", tx.commits, tx.rollbacks)
	}
	if beginner.options[0].IsoLevel != pgx.Serializable || beginner.options[0].AccessMode != pgx.ReadOnly {
		t.Fatalf("options = %#v", beginner.options[0])
	}
}

func TestTransactionRunnerPreservesCallbackErrorWhenRollbackFails(t *testing.T) {
	callbackError := errors.New("application conflict")
	tx := &fakeTx{rollbackError: errors.New("cleanup failure")}
	var output bytes.Buffer
	runner := newTestTransactionRunner(t, &fakeBeginner{transactions: []pgx.Tx{tx}}, &output)

	err := runner.Within(context.Background(), TxOptions{}, func(context.Context, pgx.Tx) error {
		return callbackError
	})
	if !errors.Is(err, callbackError) {
		t.Fatalf("Within() error = %v", err)
	}
	if tx.rollbacks != 1 || tx.commits != 0 {
		t.Fatalf("commit = %d, rollback = %d", tx.commits, tx.rollbacks)
	}
	if !bytes.Contains(output.Bytes(), []byte(`"cleanup_error_class":"internal"`)) {
		t.Fatalf("log = %s", output.String())
	}
}

func TestTransactionRunnerRollsBackAndRepanics(t *testing.T) {
	tx := &fakeTx{}
	runner := newTestTransactionRunner(t, &fakeBeginner{transactions: []pgx.Tx{tx}}, &bytes.Buffer{})

	defer func() {
		if recovered := recover(); recovered != "boom" {
			t.Fatalf("panic = %#v", recovered)
		}
		if tx.rollbacks != 1 {
			t.Fatalf("rollbacks = %d", tx.rollbacks)
		}
	}()
	_ = runner.Within(context.Background(), TxOptions{}, func(context.Context, pgx.Tx) error {
		panic("boom")
	})
}

func TestTransactionRunnerRetriesOnlyDeclaredSafeSerializationFailure(t *testing.T) {
	first := &fakeTx{}
	second := &fakeTx{}
	beginner := &fakeBeginner{transactions: []pgx.Tx{first, second}}
	runner := newTestTransactionRunner(t, beginner, &bytes.Buffer{})
	callbacks := 0

	err := runner.Within(context.Background(), TxOptions{Retry: RetryPolicy{
		RetrySafe:      true,
		MaxAttempts:    2,
		InitialBackoff: time.Millisecond,
	}}, func(context.Context, pgx.Tx) error {
		callbacks++
		if callbacks == 1 {
			return &pgconn.PgError{Code: "40001"}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Within() error = %v", err)
	}
	if callbacks != 2 || first.rollbacks != 1 || second.commits != 1 {
		t.Fatalf("callbacks = %d, first = %#v, second = %#v", callbacks, first, second)
	}
}

func TestTransactionRunnerRejectsUnsafeRetry(t *testing.T) {
	runner := newTestTransactionRunner(t, &fakeBeginner{}, &bytes.Buffer{})
	err := runner.Within(context.Background(), TxOptions{Retry: RetryPolicy{
		MaxAttempts:    2,
		InitialBackoff: time.Millisecond,
	}}, func(context.Context, pgx.Tx) error { return nil })
	if err == nil {
		t.Fatal("Within() error = nil")
	}
}

func newTestTransactionRunner(t *testing.T, beginner transactionBeginner, output *bytes.Buffer) *TransactionRunner {
	t.Helper()
	runner, err := NewTransactionRunner(beginner, testLogger(output), noop.NewTracerProvider().Tracer("test"), time.Second)
	if err != nil {
		t.Fatalf("NewTransactionRunner() error = %v", err)
	}
	return runner
}

type fakeBeginner struct {
	transactions []pgx.Tx
	errors       []error
	options      []pgx.TxOptions
}

func (b *fakeBeginner) BeginTx(_ context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	b.options = append(b.options, options)
	index := len(b.options) - 1
	if index < len(b.errors) && b.errors[index] != nil {
		return nil, b.errors[index]
	}
	if index >= len(b.transactions) {
		return nil, errors.New("unexpected begin")
	}
	return b.transactions[index], nil
}

type fakeTx struct {
	commits       int
	rollbacks     int
	commitError   error
	rollbackError error
}

func (tx *fakeTx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("nested transaction is not supported")
}
func (tx *fakeTx) Commit(context.Context) error   { tx.commits++; return tx.commitError }
func (tx *fakeTx) Rollback(context.Context) error { tx.rollbacks++; return tx.rollbackError }
func (*fakeTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, errors.New("not implemented")
}
func (*fakeTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }
func (*fakeTx) LargeObjects() pgx.LargeObjects                         { return pgx.LargeObjects{} }
func (*fakeTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, errors.New("not implemented")
}
func (*fakeTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("not implemented")
}
func (*fakeTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("not implemented")
}
func (*fakeTx) QueryRow(context.Context, string, ...any) pgx.Row {
	return errorRow{err: errors.New("not implemented")}
}
func (*fakeTx) Conn() *pgx.Conn { return nil }
