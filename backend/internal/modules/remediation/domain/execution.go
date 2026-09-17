package domain

import "context"

// RunExecutionLease owns one run's execution until Release. The implementation
// must release ownership if the process dies; Check detects a lost session.
type RunExecutionLease interface {
	Check(context.Context) error
	Release(context.Context) error
}

// ExecutionContextBinder lets a persistence adapter bind owned operations to
// the session holding its lock. Application code never receives a DB client.
type ExecutionContextBinder interface {
	BindExecutionContext(context.Context) context.Context
}

// RunExecutionStore is shared by ordinary execution and restart recovery.
// Candidate IDs are paginated lexicographically so busy/invalid old runs cannot
// hide later work. A candidate is advisory: state must be reloaded under its lock.
type RunExecutionStore interface {
	TryAcquireRun(context.Context, string) (RunExecutionLease, bool, error)
	ListRecoverableRunIDs(context.Context, string, int) ([]string, error)
}
