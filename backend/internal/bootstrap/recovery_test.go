package bootstrap

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type recoveryServerFunc func(context.Context) error

func (f recoveryServerFunc) Run(ctx context.Context) error { return f(ctx) }

type recoveryWorkerFunc func(context.Context)

func (f recoveryWorkerFunc) Run(ctx context.Context) { f(ctx) }

type recoveryExecutorFunc func(context.Context) error

func (f recoveryExecutorFunc) Close(ctx context.Context) error { return f(ctx) }

func TestRunWithRecoveryDrainsOnListenerFailure(t *testing.T) {
	started := make(chan struct{})
	var stopped, closed atomic.Bool
	failure := errors.New("listener unavailable")
	worker := recoveryWorkerFunc(func(ctx context.Context) { close(started); <-ctx.Done(); stopped.Store(true) })
	server := recoveryServerFunc(func(context.Context) error { <-started; return failure })
	executor := recoveryExecutorFunc(func(ctx context.Context) error {
		if ctx.Err() != nil {
			t.Error("cleanup context already canceled")
		}
		closed.Store(true)
		return nil
	})
	err := runWithRecovery(context.Background(), server, worker, executor, time.Second)
	if !errors.Is(err, failure) || !stopped.Load() || !closed.Load() {
		t.Fatalf("error=%v stopped=%v closed=%v", err, stopped.Load(), closed.Load())
	}
}

func TestRunWithRecoveryBoundsShutdown(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	worker := recoveryWorkerFunc(func(context.Context) { <-release })
	server := recoveryServerFunc(func(context.Context) error { return nil })
	executor := recoveryExecutorFunc(func(context.Context) error { return nil })
	err := runWithRecovery(context.Background(), server, worker, executor, time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error=%v", err)
	}
}
