package application

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"mendry/backend/internal/modules/remediation/domain"
)

type executionTestStore struct {
	mu       sync.Mutex
	owned    map[string]bool
	ids      []string
	checkErr error
	releases int
}
type executionTestLease struct {
	store *executionTestStore
	id    string
}

func (s *executionTestStore) TryAcquireRun(ctx context.Context, id string) (domain.RunExecutionLease, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owned == nil {
		s.owned = make(map[string]bool)
	}
	if s.owned[id] {
		return nil, false, nil
	}
	s.owned[id] = true
	return &executionTestLease{s, id}, true, nil
}
func (s *executionTestStore) ListRecoverableRunIDs(_ context.Context, after string, limit int) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	for _, id := range s.ids {
		if id > after {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids[:min(len(ids), limit)], nil
}
func (l *executionTestLease) Check(context.Context) error {
	l.store.mu.Lock()
	defer l.store.mu.Unlock()
	return l.store.checkErr
}
func (l *executionTestLease) Release(context.Context) error {
	l.store.mu.Lock()
	defer l.store.mu.Unlock()
	delete(l.store.owned, l.id)
	l.store.releases++
	return nil
}
func testExecutor(t *testing.T, store domain.RunExecutionStore, concurrency int) *RunExecutor {
	t.Helper()
	e, err := NewRunExecutor(context.Background(), store, concurrency)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := e.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return e
}
func receiveExecution[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("execution did not finish")
		var zero T
		return zero
	}
}

func TestRunExecutorExcludesOtherProcessAndAllowsNestedLifecycle(t *testing.T) {
	store := &executionTestStore{}
	first, second := testExecutor(t, store, 2), testExecutor(t, store, 2)
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := first.Execute(context.Background(), "run", func(ctx context.Context) (domain.Run, error) {
			close(started)
			_, err := first.Execute(ctx, "run", func(context.Context) (domain.Run, error) { return domain.Run{}, nil })
			<-release
			return domain.Run{}, err
		})
		done <- err
	}()
	receiveExecution(t, started)
	_, err := second.Execute(context.Background(), "run", func(context.Context) (domain.Run, error) { t.Error("duplicate execution"); return domain.Run{}, nil })
	if !errors.Is(err, ErrRunExecutionBusy) {
		t.Fatalf("duplicate error=%v", err)
	}
	close(release)
	if err := receiveExecution(t, done); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Execute(context.Background(), "run", func(context.Context) (domain.Run, error) { return domain.Run{}, nil }); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.releases != 2 {
		t.Fatalf("releases=%d", store.releases)
	}
}

func TestRunExecutorCapacityLeavesOtherRunsUnclaimed(t *testing.T) {
	store := &executionTestStore{}
	e := testExecutor(t, store, 1)
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := e.Execute(context.Background(), "a", func(context.Context) (domain.Run, error) { close(started); <-release; return domain.Run{}, nil })
		done <- err
	}()
	receiveExecution(t, started)
	_, err := e.Execute(context.Background(), "b", func(context.Context) (domain.Run, error) { t.Error("capacity exceeded"); return domain.Run{}, nil })
	if !errors.Is(err, ErrRunExecutionBusy) {
		t.Fatalf("error=%v", err)
	}
	close(release)
	receiveExecution(t, done)
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.releases != 1 {
		t.Fatalf("capacity denial acquired lease: %d", store.releases)
	}
}

type executionTransitionStore struct {
	domain.RunStore
	writes int
}

func (s *executionTransitionStore) Transition(context.Context, string, domain.RunState, domain.RunState, domain.Effect) error {
	s.writes++
	return nil
}

func TestRunExecutorShutdownAndLostSessionPreserveActiveState(t *testing.T) {
	for _, reason := range []string{"shutdown", "session_loss"} {
		t.Run(reason, func(t *testing.T) {
			locks := &executionTestStore{}
			e := testExecutor(t, locks, 1)
			e.checkInterval = time.Millisecond
			store := &executionTransitionStore{}
			c := &RemediationCoordinator{store: store}
			started, done := make(chan struct{}), make(chan error, 1)
			go func() {
				_, err := e.Execute(context.Background(), "run", func(ctx context.Context) (domain.Run, error) {
					close(started)
					<-ctx.Done()
					// Even terminal persistence that deliberately detaches cancellation
					// must recognize that this execution no longer owns its run.
					err := c.transitionWithReason(context.WithoutCancel(ctx), "run", domain.RunStateDiagnosing, domain.RunStateFailed, domain.Effect{}, "")
					if !errors.Is(err, ErrRunExecutionInterrupted) {
						return domain.Run{}, fmt.Errorf("terminal transition=%v", err)
					}
					return domain.Run{}, err
				})
				done <- err
			}()
			receiveExecution(t, started)
			if reason == "shutdown" {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := e.Close(ctx); err != nil {
					t.Fatal(err)
				}
			} else {
				locks.mu.Lock()
				locks.checkErr = errors.New("disconnected")
				locks.mu.Unlock()
			}
			if err := receiveExecution(t, done); !errors.Is(err, ErrRunExecutionInterrupted) {
				t.Fatalf("error=%v", err)
			}
			if store.writes != 0 {
				t.Fatalf("terminal writes=%d", store.writes)
			}
			locks.mu.Lock()
			defer locks.mu.Unlock()
			if len(locks.owned) != 0 {
				t.Fatal("execution lock leaked")
			}
		})
	}
}

type workerTestRecoverer struct {
	mu    sync.Mutex
	calls map[string]int
	work  func(context.Context, string) (domain.Run, error)
}

func (r *workerTestRecoverer) Recover(ctx context.Context, id string) (domain.Run, error) {
	r.mu.Lock()
	if r.calls == nil {
		r.calls = make(map[string]int)
	}
	r.calls[id]++
	r.mu.Unlock()
	if r.work != nil {
		return r.work(ctx, id)
	}
	return domain.Run{State: domain.RunStateDiagnosisReadyForReview}, nil
}
func TestRecoveryWorkerStartupAndShutdown(t *testing.T) {
	started := make(chan struct{})
	recoverer := &workerTestRecoverer{work: func(ctx context.Context, _ string) (domain.Run, error) {
		close(started)
		<-ctx.Done()
		return domain.Run{}, ctx.Err()
	}}
	worker, err := NewRecoveryWorker(RecoveryWorkerOptions{Store: &executionTestStore{ids: []string{"a"}}, Recoverer: recoverer, Interval: time.Hour, Concurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { worker.Run(ctx); close(done) }()
	receiveExecution(t, started) // No need to wait for the one-hour interval.
	cancel()
	receiveExecution(t, done)
}
func TestRecoveryWorkerPaginationBusyRunsAndBackoff(t *testing.T) {
	store := &executionTestStore{}
	for i := 0; i < 205; i++ {
		store.ids = append(store.ids, fmt.Sprintf("%03d", i))
	}
	recoverer := &workerTestRecoverer{work: func(_ context.Context, id string) (domain.Run, error) {
		if id == "000" {
			return domain.Run{}, ErrRunExecutionBusy
		}
		if id == "001" {
			return domain.Run{}, errors.New("unavailable checkpoint")
		}
		return domain.Run{State: domain.RunStateDiagnosisReadyForReview}, nil
	}}
	worker, err := NewRecoveryWorker(RecoveryWorkerOptions{Store: store, Recoverer: recoverer, Interval: time.Second, Concurrency: 4})
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(recoverer.calls) != 205 {
		t.Fatalf("visited %d candidates", len(recoverer.calls))
	}
	if err := worker.sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if recoverer.calls["001"] != 1 || recoverer.calls["000"] != 2 || recoverer.calls["204"] != 2 {
		t.Fatalf("backoff/pagination calls=%v", recoverer.calls)
	}
	worker.retries["001"] = recoveryRetry{failures: 1, next: time.Now().Add(-time.Second)}
	if err := worker.sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if recoverer.calls["001"] != 2 {
		t.Fatal("failed run did not retry after backoff")
	}
}
