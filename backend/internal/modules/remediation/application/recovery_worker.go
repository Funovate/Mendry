package application

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"mendry/backend/internal/modules/remediation/domain"
)

// RecoveryObservation contains identifiers and fixed reason codes only; model
// output, credentials, and database error text must not enter worker logs.
type RecoveryObservation struct{ RunID, Outcome, Reason string }
type RecoveryObserver func(context.Context, RecoveryObservation)
type RunRecoverer interface {
	Recover(context.Context, string) (domain.Run, error)
}

type RecoveryWorkerOptions struct {
	Store       domain.RunExecutionStore
	Recoverer   RunRecoverer
	Interval    time.Duration
	Concurrency int
	Observer    RecoveryObserver
}

type recoveryRetry struct {
	failures int
	next     time.Time
}

type RecoveryWorker struct {
	options RecoveryWorkerOptions
	mu      sync.Mutex
	retries map[string]recoveryRetry
}

func NewRecoveryWorker(options RecoveryWorkerOptions) (*RecoveryWorker, error) {
	if options.Store == nil || options.Recoverer == nil || options.Interval <= 0 || options.Concurrency < 1 || options.Concurrency > 32 {
		return nil, fmt.Errorf("recovery store, recoverer, positive interval and concurrency between 1 and 32 are required")
	}
	return &RecoveryWorker{options: options, retries: make(map[string]recoveryRetry)}, nil
}

// Run scans immediately at startup, then periodically. Pages and concurrent
// executions are bounded; keyset pagination ensures locked/invalid old runs do
// not starve later IDs. Cancellation stops scans and in-flight recoveries.
func (w *RecoveryWorker) Run(ctx context.Context) {
	for ctx.Err() == nil {
		if err := w.sweep(ctx); err != nil && ctx.Err() == nil {
			w.observe(ctx, RecoveryObservation{Outcome: "failed", Reason: "scan_failed"})
		}
		timer := time.NewTimer(w.options.Interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (w *RecoveryWorker) sweep(ctx context.Context) error {
	const pageSize = 100
	jobs := make(chan string)
	var workers sync.WaitGroup
	for range w.options.Concurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for id := range jobs {
				if ctx.Err() != nil {
					return
				}
				w.recoverRun(ctx, id)
			}
		}()
	}
	defer func() { close(jobs); workers.Wait() }()
	after := ""
	defer func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		// Only expire old backoff entries; a partial/failed scan is not proof a
		// previously failing run has disappeared.
		for id, retry := range w.retries {
			if time.Since(retry.next) > 10*time.Minute {
				delete(w.retries, id)
			}
		}
	}()
	for {
		ids, err := w.options.Store.ListRecoverableRunIDs(ctx, after, pageSize)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if id <= after {
				return fmt.Errorf("recovery candidate cursor did not advance")
			}
			after = id
			w.mu.Lock()
			retry := w.retries[id]
			w.mu.Unlock()
			if time.Now().Before(retry.next) {
				continue
			}
			select {
			case jobs <- id:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if len(ids) < pageSize {
			return nil
		}
	}
}

func (w *RecoveryWorker) recoverRun(ctx context.Context, runID string) {
	run, err := w.options.Recoverer.Recover(ctx, runID)
	if errors.Is(err, ErrRunExecutionBusy) || errors.Is(err, ErrRunExecutionInterrupted) || ctx.Err() != nil {
		return
	}
	w.mu.Lock()
	if err != nil {
		retry := w.retries[runID]
		retry.failures = min(retry.failures+1, 4)
		retry.next = time.Now().Add(min(time.Minute*time.Duration(1<<(retry.failures-1)), 5*time.Minute))
		w.retries[runID] = retry
	} else {
		delete(w.retries, runID)
	}
	w.mu.Unlock()
	if err != nil {
		w.observe(ctx, RecoveryObservation{RunID: runID, Outcome: "failed", Reason: "resume_failed"})
	} else {
		w.observe(ctx, RecoveryObservation{RunID: runID, Outcome: "completed", Reason: string(run.State)})
	}
}

func (w *RecoveryWorker) observe(ctx context.Context, observation RecoveryObservation) {
	if w.options.Observer != nil {
		w.options.Observer(ctx, observation)
	}
}

// Recover reloads candidates under exclusive execution ownership. It never
// creates a new attempt, bypasses human review, or resets the durable budget.
func (c *RemediationCoordinator) Recover(ctx context.Context, runID string) (domain.Run, error) {
	if c.executor == nil {
		return domain.Run{}, fmt.Errorf("recovery requires an execution owner")
	}
	return c.executor.Execute(ctx, runID, func(owned context.Context) (domain.Run, error) {
		aggregate, err := c.store.Get(owned, runID)
		if err != nil {
			return domain.Run{}, err
		}
		run := aggregate.Run
		if c.lookup != nil {
			incident, err := c.lookup.GetByID(owned, run.IncidentID)
			if err != nil {
				return run, fmt.Errorf("reload recovery incident: %w", err)
			}
			if incident.LifecycleGeneration != run.LifecycleGeneration || incident.DeployedCommit != run.DeployedCommit {
				return run, nil
			}
		}
		if run.State == domain.RunStateQueued {
			if run.ContinuationOfRunID != "" {
				var prepared preparedContinuation
				if run.TriggerReason == domain.TriggerOriginManualReconfigure {
					prepared, err = c.rebuildQueuedReconfiguration(owned, run)
				} else {
					prepared, err = c.rebuildQueuedContinuation(owned, run)
				}
				if err != nil {
					return run, err
				}
				return c.runQueued(owned, prepared.child, prepared.brief, prepared.priorEvidence, prepared.reconstruction,
					prepared.resumePhase, run.TriggerReason, "", prepared.priorInvocations)
			}
			return c.runQueued(owned, run, "", "", "", domain.RunStateDiagnosing, run.TriggerReason, "", nil)
		}
		if !recoverableRunState(run.State) {
			return run, nil
		}
		if domain.ParseAgentLoopMode(string(run.AgentLoopMode)) != domain.AgentLoopModeResilientV1 || run.State == domain.RunStateRunning {
			// Legacy runs have no durable working memory. Make the interruption
			// explicit so the existing manual continuation UI becomes available.
			if err := c.transition(owned, runID, run.State, domain.RunStateFailed, domain.Effect{
				TerminalReason: "recovery_checkpoint_unavailable", Retryable: false,
			}); err != nil {
				return run, err
			}
			return c.loadLifecycleRun(owned, runID)
		}
		return c.Resume(owned, runID)
	})
}

func recoverableRunState(state domain.RunState) bool {
	switch state {
	case domain.RunStatePreparingContext, domain.RunStateDiagnosing, domain.RunStateCollectingMoreContext,
		domain.RunStatePlanning, domain.RunStatePatching, domain.RunStateValidating, domain.RunStatePublishing, domain.RunStateRunning:
		return true
	default:
		return false
	}
}

func (c *RemediationCoordinator) rebuildQueuedContinuation(ctx context.Context, run domain.Run) (preparedContinuation, error) {
	predecessor, err := c.store.Get(ctx, run.ContinuationOfRunID)
	if err != nil {
		return preparedContinuation{}, fmt.Errorf("load queued continuation predecessor: %w", err)
	}
	return c.prepareContinuationRun(ctx, domain.NextAttempt{
		ContinuationOfRunID: run.ContinuationOfRunID, SeriesID: run.SeriesID, IncidentID: run.IncidentID,
		LifecycleGeneration: run.LifecycleGeneration, DeployedCommit: run.DeployedCommit, ContextVersion: run.ContextVersion,
		ExpectedPreviousVersion: predecessor.Run.Version, Origin: run.TriggerReason, TriggerReason: run.TriggerReason,
		ContinuationReason: run.ContinuationReason,
	}, &run)
}

func (c *RemediationCoordinator) rebuildQueuedReconfiguration(ctx context.Context, run domain.Run) (preparedContinuation, error) {
	predecessor, err := c.store.Get(ctx, run.ContinuationOfRunID)
	if err != nil {
		return preparedContinuation{}, fmt.Errorf("load queued reconfiguration predecessor: %w", err)
	}
	return c.prepareReconfigured(ctx, domain.NextAttempt{
		ContinuationOfRunID: run.ContinuationOfRunID, SeriesID: run.SeriesID, IncidentID: run.IncidentID,
		LifecycleGeneration: run.LifecycleGeneration, DeployedCommit: run.DeployedCommit, ContextVersion: run.ContextVersion,
		ExpectedPreviousVersion: predecessor.Run.Version, Origin: run.TriggerReason, TriggerReason: run.TriggerReason,
		ContinuationReason: run.ContinuationReason,
	}, &run)
}
