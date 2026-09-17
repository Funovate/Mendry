package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"mendry/backend/internal/modules/remediation/adapter/postgres"
	"mendry/backend/internal/modules/remediation/domain"
)

func mustExecutionStore(t *testing.T, pool *pgxpool.Pool) *postgres.ExecutionStore {
	t.Helper()
	store, err := postgres.NewExecutionStore(pool)
	if err != nil {
		t.Fatalf("NewExecutionStore: %v", err)
	}
	return store
}

func cleanupExecutionLease(t *testing.T, lease domain.RunExecutionLease) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := lease.Release(ctx); err != nil {
			t.Errorf("release execution lease: %v", err)
		}
	})
}

// A one-slot pool makes holding pooled sessions instead of hijacking them fail
// deterministically: both the competing lock request and ordinary SQL need it.
func setupExecutionTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	fixture := setupTestDB(t)
	config := fixture.Config()
	fixture.Close()
	config.MaxConns = 1
	config.MinConns = 0
	config.MinIdleConns = 0
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal("create one-slot execution test pool failed")
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestExecutionStore_MutualExclusionAndRelease(t *testing.T) {
	pool := setupExecutionTestDB(t)
	first := mustExecutionStore(t, pool)
	second := mustExecutionStore(t, pool)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runID := uuid.NewString()

	lease, acquired, err := first.TryAcquireRun(ctx, runID)
	if err != nil || !acquired || lease == nil {
		t.Fatalf("first acquisition: acquired=%v err=%v", acquired, err)
	}
	cleanupExecutionLease(t, lease)
	if err := lease.Check(ctx); err != nil {
		t.Fatalf("check healthy lease: %v", err)
	}
	if pool.Stat().AcquiredConns() != 0 {
		t.Fatal("execution lease still occupies pool capacity")
	}

	// UUID spelling must not change the lock identity.
	contender, acquired, err := second.TryAcquireRun(ctx, strings.ToUpper(runID))
	if contender != nil {
		cleanupExecutionLease(t, contender)
	}
	if err != nil || acquired || contender != nil {
		t.Fatalf("competing acquisition: acquired=%v err=%v", acquired, err)
	}
	if _, err := pool.Exec(ctx, `SELECT 1`); err != nil {
		t.Fatalf("ordinary SQL while lease held: %v", err)
	}
	other, acquired, err := second.TryAcquireRun(ctx, uuid.NewString())
	if err != nil || !acquired || other == nil {
		t.Fatalf("independent run acquisition: acquired=%v err=%v", acquired, err)
	}
	cleanupExecutionLease(t, other)

	// Even cancellation during cleanup must close the dedicated socket.
	releaseCtx, releaseCancel := context.WithCancel(context.Background())
	releaseCancel()
	_ = lease.Release(releaseCtx)
	if err := lease.Check(ctx); err == nil {
		t.Fatal("released lease still passes Check")
	}
	if err := lease.Release(ctx); err != nil {
		t.Fatalf("repeated release: %v", err)
	}
	// Socket closure is local; allow the server to observe EOF and drop locks.
	reacquired := acquireExecutionLeaseEventually(t, ctx, second, runID)
	cleanupExecutionLease(t, reacquired)
}

func acquireExecutionLeaseEventually(t *testing.T, ctx context.Context, store *postgres.ExecutionStore, runID string) domain.RunExecutionLease {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		lease, acquired, err := store.TryAcquireRun(ctx, runID)
		if err != nil {
			t.Fatalf("reacquire execution lease: %v", err)
		}
		if acquired {
			return lease
		}
		select {
		case <-ctx.Done():
			t.Fatal("execution lock was not released before deadline")
		case <-ticker.C:
		}
	}
}

func TestExecutionStore_SessionTerminationReleasesLock(t *testing.T) {
	pool := setupExecutionTestDB(t)
	store := mustExecutionStore(t, pool)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var pid int32
	if err := pool.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatalf("get fixture session pid: %v", err)
	}
	// The sole idle connection is the one acquired and hijacked below.
	runID := uuid.NewString()
	lease, acquired, err := store.TryAcquireRun(ctx, runID)
	if err != nil || !acquired || lease == nil {
		t.Fatalf("acquire execution lease: acquired=%v err=%v", acquired, err)
	}
	cleanupExecutionLease(t, lease)
	var terminated bool
	err = pool.QueryRow(ctx, `SELECT pg_terminate_backend($1)`, pid).Scan(&terminated)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "42501" {
		t.Skip("test database role cannot terminate its fixture session")
	}
	if err != nil || !terminated {
		t.Fatalf("terminate fixture session: terminated=%v err=%v", terminated, err)
	}
	if err := lease.Check(ctx); err == nil {
		t.Fatal("terminated session still passes Check")
	}
	// Reacquire without releasing the original lease: the server must own
	// cleanup, which is also what releases the lock after process death.
	recovered := acquireExecutionLeaseEventually(t, ctx, mustExecutionStore(t, pool), runID)
	cleanupExecutionLease(t, recovered)
}

func TestExecutionStore_RecoveryPagination(t *testing.T) {
	pool := setupTestDB(t)
	t.Cleanup(pool.Close)
	store := mustExecutionStore(t, pool)
	runs := mustStore(t, pool)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	expected := make(map[string]bool)
	excluded := make(map[string]bool)
	create := func(state string) domain.Run {
		t.Helper()
		incident := mustIncident(t, pool)
		if _, err := pool.Exec(ctx, `UPDATE incidents SET deployed_commit = 'fixture-commit' WHERE id = $1`, incident); err != nil {
			t.Fatalf("set fixture baseline: %v", err)
		}
		input := newRun(incident, 1, "fixture-commit")
		input.ContextVersion = 1
		run, err := runs.CreateSeriesAndRun(ctx, input)
		if err != nil {
			t.Fatalf("create recovery fixture: %v", err)
		}
		if _, err := pool.Exec(ctx, `UPDATE remediation_run SET state = $2 WHERE id = $1`, run.RunID, state); err != nil {
			t.Fatalf("set fixture state: %v", err)
		}
		agg, err := runs.Get(ctx, run.RunID)
		if err != nil {
			t.Fatalf("load recovery fixture: %v", err)
		}
		return agg.Run
	}
	for _, state := range []string{
		"queued", "preparing_context", "diagnosing", "collecting_more_context",
		"planning", "running", "patching", "validating", "publishing",
	} {
		run := create(state)
		expected[run.RunID] = true
	}
	for _, state := range []string{
		"failed", "budget_exhausted", "completed_non_code", "blocked_manual_review",
		"diagnosis_ready_for_review", "awaiting_human_review",
	} {
		run := create(state)
		excluded[run.RunID] = true
	}
	root := create("failed")
	child, err := runs.CreateNextAttempt(ctx, nextAttemptInput(root, domain.TriggerReasonManualContinue, root.ContextVersion))
	if err != nil {
		t.Fatalf("create queued child: %v", err)
	}
	expected[child.RunID] = true
	excluded[root.RunID] = true

	obsoleteGeneration := create("queued")
	if _, err := pool.Exec(ctx, `UPDATE incidents SET lifecycle_generation = lifecycle_generation + 1 WHERE id = $1`, obsoleteGeneration.IncidentID); err != nil {
		t.Fatalf("advance fixture generation: %v", err)
	}
	excluded[obsoleteGeneration.RunID] = true
	obsoleteCommit := create("queued")
	if _, err := pool.Exec(ctx, `UPDATE incidents SET deployed_commit = 'new-fixture-commit' WHERE id = $1`, obsoleteCommit.IncidentID); err != nil {
		t.Fatalf("advance fixture commit: %v", err)
	}
	excluded[obsoleteCommit.RunID] = true

	// The latest-attempt guard also handles inconsistent old active rows.
	old := create("failed")
	latest, err := runs.CreateNextAttempt(ctx, nextAttemptInput(old, domain.TriggerReasonManualContinue, old.ContextVersion))
	if err != nil {
		t.Fatalf("create superseding attempt: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE remediation_run SET state = CASE WHEN id = $1 THEN 'queued' ELSE 'failed' END WHERE id IN ($1, $2)`, old.RunID, latest.RunID); err != nil {
		t.Fatalf("set superseded fixture states: %v", err)
	}
	excluded[old.RunID] = true
	excluded[latest.RunID] = true

	seen := make(map[string]bool)
	after := ""
	for {
		page, err := store.ListRecoverableRunIDs(ctx, after, 3)
		if err != nil {
			t.Fatalf("list recovery page: %v", err)
		}
		if len(page) > 3 {
			t.Fatalf("page exceeds requested limit: %d", len(page))
		}
		if len(page) == 0 {
			break
		}
		for _, id := range page {
			if id <= after || seen[id] {
				t.Fatalf("recovery cursor did not advance: %q after %q", id, after)
			}
			if excluded[id] {
				t.Errorf("ineligible fixture run returned: %s", id)
			}
			seen[id] = true
			after = id
		}
	}
	for id := range expected {
		if !seen[id] {
			t.Errorf("eligible fixture run missing: %s", id)
		}
	}
}

type executionQueryProbe struct {
	*pgxpool.Pool
	limit int
}

func (p *executionQueryProbe) Query(_ context.Context, _ string, args ...any) (pgx.Rows, error) {
	p.limit = args[1].(int)
	return nil, errors.New("query probe")
}

func TestExecutionStore_ValidatesIDsAndBoundsLimit(t *testing.T) {
	if store, err := postgres.NewExecutionStore(nil); err == nil || store != nil {
		t.Fatal("nil database accepted")
	}
	// An uninitialized pool panics if touched; invalid IDs must fail first.
	store := mustExecutionStore(t, &pgxpool.Pool{})
	for _, id := range []string{"", "not-a-uuid"} {
		if lease, acquired, err := store.TryAcquireRun(context.Background(), id); err == nil || acquired || lease != nil {
			t.Fatalf("invalid ID accepted: %q", id)
		}
	}
	if _, err := store.ListRecoverableRunIDs(context.Background(), "not-a-uuid", 1); err == nil {
		t.Fatal("invalid cursor accepted")
	}
	for _, test := range []struct{ requested, want int }{{-1, 100}, {0, 100}, {1, 1}, {100, 100}, {101, 100}} {
		t.Run(fmt.Sprint(test.requested), func(t *testing.T) {
			probe := &executionQueryProbe{}
			store, err := postgres.NewExecutionStore(probe)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.ListRecoverableRunIDs(context.Background(), "", test.requested); err == nil {
				t.Fatal("query error not propagated")
			}
			if probe.limit != test.want {
				t.Fatalf("query limit = %d, want %d", probe.limit, test.want)
			}
		})
	}
}

func TestExecutionStore_DatabaseSessionIsolation(t *testing.T) {
	pool := setupExecutionTestDB(t)
	execStore := mustExecutionStore(t, pool)
	runStore := mustStore(t, pool)
	checkpointStore, err := postgres.NewCheckpointStore(pool)
	if err != nil {
		t.Fatalf("NewCheckpointStore: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var pid int32
	if err := pool.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatalf("get fixture session pid: %v", err)
	}

	incidentID := mustIncident(t, pool)
	created, err := runStore.CreateSeriesAndRun(ctx, newRun(incidentID, 1, "commit-iso"))
	if err != nil {
		t.Fatalf("create fixture run: %v", err)
	}

	lease, acquired, err := execStore.TryAcquireRun(ctx, created.RunID)
	if err != nil || !acquired || lease == nil {
		t.Fatalf("acquire execution lease: acquired=%v err=%v", acquired, err)
	}
	cleanupExecutionLease(t, lease)

	binder, ok := lease.(domain.ExecutionContextBinder)
	if !ok {
		t.Fatalf("lease does not implement ExecutionContextBinder: %T", lease)
	}
	execCtx := binder.BindExecutionContext(ctx)

	// In the owned execution session, write operations (transitions, checkpoints) succeed.
	if err := runStore.Transition(execCtx, created.RunID, domain.RunStateQueued, domain.RunStateDiagnosing, domain.Effect{}); err != nil {
		t.Fatalf("transition in execution session failed: %v", err)
	}

	// Concurrent query on the 1-slot pool succeeds without deadlock or blocking,
	// verifying the lease session does not consume pool capacity.
	agg, err := runStore.Get(ctx, created.RunID)
	if err != nil {
		t.Fatalf("read from query pool failed: %v", err)
	}
	if agg.Run.State != domain.RunStateDiagnosing {
		t.Fatalf("expected state diagnosing, got %s", agg.Run.State)
	}

	// Checkpoint append inside execution session succeeds.
	checkpoint := checkpointForRun(created)
	checkpoint.ObservedRunVersion = agg.Run.Version
	if _, err := checkpointStore.AppendCheckpoint(execCtx, created.RunID, checkpoint); err != nil {
		t.Fatalf("append checkpoint in execution session failed: %v", err)
	}

	// Terminate the dedicated backend session.
	var terminated bool
	err = pool.QueryRow(ctx, `SELECT pg_terminate_backend($1)`, pid).Scan(&terminated)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "42501" {
		t.Skip("test database role cannot terminate its fixture session")
	}
	if err != nil || !terminated {
		t.Fatalf("terminate fixture session: terminated=%v err=%v", terminated, err)
	}

	// Stale write in the lost session must FAIL and must NOT fall back to the query pool!
	err = runStore.Transition(execCtx, created.RunID, domain.RunStateDiagnosing, domain.RunStatePlanning, domain.Effect{})
	if err == nil {
		t.Fatal("stale transition in dead session succeeded; must not fall back to query pool")
	}

	checkpoint.Phase = "planning"
	if _, err := checkpointStore.AppendCheckpoint(execCtx, created.RunID, checkpoint); err == nil {
		t.Fatal("stale checkpoint in dead session succeeded; must not fall back to query pool")
	}

	// The query pool remains functional for non-session operations.
	aggAfter, err := runStore.Get(ctx, created.RunID)
	if err != nil {
		t.Fatalf("pool read after session loss failed: %v", err)
	}
	if aggAfter.Run.State != domain.RunStateDiagnosing {
		t.Fatalf("run state was corrupted by stale write: %s", aggAfter.Run.State)
	}

	// Released lease also fails writes rather than falling back to pool.
	created2, err := runStore.CreateSeriesAndRun(ctx, newRun(incidentID, 2, "commit-iso2"))
	if err != nil {
		t.Fatalf("create second run: %v", err)
	}
	lease2, acquired, err := execStore.TryAcquireRun(ctx, created2.RunID)
	if err != nil || !acquired || lease2 == nil {
		t.Fatalf("acquire lease2: acquired=%v err=%v", acquired, err)
	}
	execCtx2 := lease2.(domain.ExecutionContextBinder).BindExecutionContext(ctx)
	if err := lease2.Release(ctx); err != nil {
		t.Fatalf("release lease2: %v", err)
	}
	if err := runStore.Transition(execCtx2, created2.RunID, domain.RunStateQueued, domain.RunStateDiagnosing, domain.Effect{}); err == nil {
		t.Fatal("transition on released lease succeeded; must not fall back to query pool")
	}
}

