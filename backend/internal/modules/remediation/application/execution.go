package application

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"mendry/backend/internal/modules/remediation/domain"
)

var (
	ErrRunExecutionBusy        = errors.New("remediation run is already executing or execution capacity is full")
	ErrRunExecutionInterrupted = errors.New("remediation execution interrupted; durable recovery required")
)

// RunExecutor bounds both normal and recovered work, and holds the same
// cross-process lock throughout model calls and external effects. Configure it
// once at startup, before exposing the coordinator to any caller.
type RunExecutor struct {
	store         domain.RunExecutionStore
	ctx           context.Context
	cancel        context.CancelCauseFunc
	slots         chan struct{}
	checkInterval time.Duration
	checkTimeout  time.Duration
	mu            sync.Mutex
	closed        bool
	active        sync.WaitGroup
	closeOnce     sync.Once
	done          chan struct{}
}

type executionScopeKey struct{}
type executionScope struct {
	executor *RunExecutor
	runID    string
	ctx      context.Context
}

func NewRunExecutor(ctx context.Context, store domain.RunExecutionStore, concurrency int) (*RunExecutor, error) {
	if store == nil || concurrency < 1 || concurrency > 32 {
		return nil, fmt.Errorf("execution store and concurrency between 1 and 32 are required")
	}
	root, cancel := context.WithCancelCause(ctx)
	return &RunExecutor{store: store, ctx: root, cancel: cancel, slots: make(chan struct{}, concurrency),
		checkInterval: 5 * time.Second, checkTimeout: 3 * time.Second, done: make(chan struct{})}, nil
}

func (c *RemediationCoordinator) SetRunExecutor(executor *RunExecutor) { c.executor = executor }

func (e *RunExecutor) owns(ctx context.Context, runID string) bool {
	scope, _ := ctx.Value(executionScopeKey{}).(*executionScope)
	return scope != nil && scope.executor == e && scope.runID == runID
}

// executionInterrupted survives WithoutCancel and operation-specific deadlines.
// Shutdown/session loss must never be converted into a terminal business result.
func executionInterrupted(ctx context.Context) bool {
	scope, _ := ctx.Value(executionScopeKey{}).(*executionScope)
	return scope != nil && (scope.executor.ctx.Err() != nil || errors.Is(context.Cause(scope.ctx), ErrRunExecutionInterrupted))
}

func (e *RunExecutor) Execute(ctx context.Context, runID string, work func(context.Context) (domain.Run, error)) (run domain.Run, result error) {
	if e.owns(ctx, runID) {
		if executionInterrupted(ctx) {
			return domain.Run{}, ErrRunExecutionInterrupted
		}
		return work(ctx)
	}
	e.mu.Lock()
	if e.closed || e.ctx.Err() != nil {
		e.mu.Unlock()
		return domain.Run{}, ErrRunExecutionInterrupted
	}
	select {
	case e.slots <- struct{}{}:
	default:
		e.mu.Unlock()
		return domain.Run{}, ErrRunExecutionBusy
	}
	e.active.Add(1)
	e.mu.Unlock()
	defer func() { <-e.slots; e.active.Done() }()

	ownerCtx, stopOwner := context.WithCancelCause(context.WithoutCancel(ctx))
	execCtx, cancel := context.WithCancelCause(ctx)
	interrupt := func() {
		stopOwner(ErrRunExecutionInterrupted)
		cancel(ErrRunExecutionInterrupted)
	}
	stopRoot := context.AfterFunc(e.ctx, interrupt)
	defer stopRoot()
	defer stopOwner(context.Canceled)
	defer cancel(context.Canceled)
	acquireCtx, acquireCancel := context.WithTimeout(execCtx, e.checkTimeout)
	lease, acquired, err := e.store.TryAcquireRun(acquireCtx, runID)
	acquireCancel()
	if err != nil {
		return domain.Run{}, fmt.Errorf("acquire remediation execution: %w", err)
	}
	if !acquired {
		return domain.Run{}, ErrRunExecutionBusy
	}
	defer func() {
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), e.checkTimeout)
		defer releaseCancel()
		if err := lease.Release(releaseCtx); err != nil {
			result = errors.Join(result, fmt.Errorf("release remediation execution: %w", err))
		}
	}()

	stopCheck, checkDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(checkDone)
		ticker := time.NewTicker(e.checkInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stopCheck:
				return
			case <-ownerCtx.Done():
				return
			case <-ticker.C:
				checkCtx, checkCancel := context.WithTimeout(ownerCtx, e.checkTimeout)
				err := lease.Check(checkCtx)
				checkCancel()
				if err != nil {
					interrupt()
					return
				}
			}
		}
	}()
	defer func() { close(stopCheck); <-checkDone }()
	scope := &executionScope{executor: e, runID: runID, ctx: ownerCtx}
	workCtx := context.WithValue(execCtx, executionScopeKey{}, scope)
	if binder, ok := lease.(domain.ExecutionContextBinder); ok {
		workCtx = binder.BindExecutionContext(workCtx)
	}
	if e.ctx.Err() != nil {
		interrupt()
	}
	if workCtx.Err() != nil {
		return domain.Run{}, context.Cause(workCtx)
	}
	run, result = work(workCtx)
	if executionInterrupted(workCtx) {
		return run, ErrRunExecutionInterrupted
	}
	return run, result
}

// Close stops admission, cancels running operations, and waits for lock release
// before the composition root closes its database clients.
func (e *RunExecutor) Close(ctx context.Context) error {
	e.closeOnce.Do(func() {
		e.mu.Lock()
		e.closed = true
		e.cancel(ErrRunExecutionInterrupted)
		e.mu.Unlock()
		go func() { e.active.Wait(); close(e.done) }()
	})
	select {
	case <-e.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
