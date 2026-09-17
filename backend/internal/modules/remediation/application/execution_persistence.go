package application

import "context"

// executionPersistenceContext detaches request/model deadlines while retaining
// cancellation from the execution owner. The lock monitor remains alive during
// terminal persistence, even after the original request has been cancelled.
func executionPersistenceContext(ctx context.Context) (context.Context, context.CancelFunc) {
	writeCtx, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	scope, _ := ctx.Value(executionScopeKey{}).(*executionScope)
	if scope == nil {
		return writeCtx, func() { cancel(context.Canceled) }
	}
	interrupt := func() { cancel(ErrRunExecutionInterrupted) }
	stopOwner := context.AfterFunc(scope.ctx, interrupt)
	stopRoot := context.AfterFunc(scope.executor.ctx, interrupt)
	if executionInterrupted(ctx) {
		interrupt()
	}
	return writeCtx, func() {
		stopOwner()
		stopRoot()
		cancel(context.Canceled)
	}
}
