package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"mendry/backend/internal/modules/remediation/adapter/postgres"
	"mendry/backend/internal/modules/remediation/application"
	"mendry/backend/internal/modules/remediation/domain"
)

func mustStore(t *testing.T, pool *pgxpool.Pool) *postgres.RunStore {
	t.Helper()
	store, err := postgres.NewRunStore(pool)
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}
	return store
}

func setupTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	ctx := context.Background()
	// 默认使用本地测试库；开发/CI 环境可用 MENDRY_POSTGRES_URL 覆盖（例如指向共享的
	// 测试开发库，fixture 自包含，不依赖 seed 数据）。
	connStr := os.Getenv("MENDRY_POSTGRES_URL")
	if connStr == "" {
		connStr = "postgres://mendry:mendry@localhost:5432/mendry_test?sslmode=disable"
	}

	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		t.Skip("skipping integration test: database not available")
	}

	// Ping to verify connection
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skip("skipping integration test: database not reachable")
	}

	return pool
}

// newRun builds a NewRun input for the given incident key.
func newRun(incidentID uuid.UUID, generation int64, commit string) domain.NewRun {
	return domain.NewRun{
		IncidentID:          incidentID.String(),
		LifecycleGeneration: generation,
		DeployedCommit:      commit,
	}
}

// mustParse converts an opaque run-id string back to a uuid for GetRun.
func mustParse(t *testing.T, id string) uuid.UUID {
	t.Helper()
	u, err := uuid.Parse(id)
	if err != nil {
		t.Fatalf("parse id %q: %v", id, err)
	}
	return u
}

// newV7 生成随机 UUIDv7，失败时直接终止测试。
func newV7(t *testing.T) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid.NewV7: %v", err)
	}
	return id
}

// mustIncident 插入一个满足全部外键/约束的 fixture project + environment +
// source + incident（均为随机 UUIDv7），返回 incident id。每个测试独立调用，
// 保证测试之间互不干扰，也不依赖 seed 数据。
func mustIncident(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	projectID := newV7(t)
	envID := newV7(t)
	sourceID := newV7(t)
	incidentID := newV7(t)

	key := "fixture-" + strings.ReplaceAll(projectID.String(), "-", "")[:20]
	if _, err := pool.Exec(ctx,
		`INSERT INTO projects (id, project_key, name) VALUES ($1, $2, 'Fixture Project')`, projectID, key); err != nil {
		t.Fatalf("fixture project insert failed: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO project_environments (id, project_id, environment_key, name) VALUES ($1, $2, 'prod', 'Production')`, envID, projectID); err != nil {
		t.Fatalf("fixture environment insert failed: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO project_sources (id, project_id, environment_id, kind) VALUES ($1, $2, $3, 'ssh')`, sourceID, projectID, envID); err != nil {
		t.Fatalf("fixture source insert failed: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO incidents (id, title, fingerprint, source, project_id, environment_id, source_id, lifecycle_generation, deployed_commit) VALUES ($1, 'Fixture Incident', $5, 'fixture', $2, $3, $4, 1, '')`, incidentID, projectID, envID, sourceID, "fingerprint-"+incidentID.String()); err != nil {
		t.Fatalf("fixture incident insert failed: %v", err)
	}

	// 测试结束后按外键依赖顺序清理：删 incident 会级联清空 series/run/
	// decisions/plans/invocations/artifacts/evidence，然后再删 source/env/project。
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cleanups := []struct {
			sql  string
			args []any
		}{
			{`DELETE FROM incidents WHERE id = $1`, []any{incidentID}},
			{`DELETE FROM project_sources WHERE id = $1`, []any{sourceID}},
			{`DELETE FROM project_environments WHERE id = $1`, []any{envID}},
			{`DELETE FROM projects WHERE id = $1`, []any{projectID}},
		}
		for _, c := range cleanups {
			if _, err := pool.Exec(cleanupCtx, c.sql, c.args...); err != nil {
				t.Errorf("fixture cleanup failed: %v", err)
			}
		}
	})
	return incidentID
}

func TestRunStore_CreateSeriesAndRun(t *testing.T) {
	pool := setupTestDB(t)
	t.Cleanup(pool.Close)

	store := mustStore(t, pool)
	ctx := context.Background()

	incidentID := mustIncident(t, pool)
	generation := int64(1)
	deployedCommit := "abc123"

	t.Run("creates new series and run in queued state", func(t *testing.T) {
		run, err := store.CreateSeriesAndRun(ctx, newRun(incidentID, generation, deployedCommit))
		if err != nil {
			t.Fatalf("CreateSeriesAndRun failed: %v", err)
		}
		if run.SeriesID == "" {
			t.Error("expected non-empty series ID")
		}
		if run.RunID == "" {
			t.Error("expected non-empty run ID")
		}
		if run.State != domain.RunStateQueued {
			t.Errorf("expected state %s, got %s", domain.RunStateQueued, run.State)
		}
	})

	t.Run("reuses existing series with same key", func(t *testing.T) {
		run1, err := store.CreateSeriesAndRun(ctx, newRun(incidentID, generation, deployedCommit))
		if err != nil {
			t.Fatalf("first CreateSeriesAndRun failed: %v", err)
		}

		run2, err := store.CreateSeriesAndRun(ctx, newRun(incidentID, generation, deployedCommit))
		if err != nil {
			t.Fatalf("second CreateSeriesAndRun failed: %v", err)
		}

		if run1.SeriesID != run2.SeriesID {
			t.Errorf("expected same series ID, got %v and %v", run1.SeriesID, run2.SeriesID)
		}
		if run1.RunID != run2.RunID {
			t.Errorf("expected same root run ID, got %v and %v", run1.RunID, run2.RunID)
		}
	})
}

func makeTerminalRun(t *testing.T, pool *pgxpool.Pool, store *postgres.RunStore, state domain.RunState, contextVersion int64, retryable bool) domain.Run {
	t.Helper()
	incidentID := mustIncident(t, pool)
	input := newRun(incidentID, 1, "abc123")
	input.ContextVersion = contextVersion
	if contextVersion > 0 {
		input.TriggerReason = domain.TriggerReasonAutomatic
	}
	run, err := store.CreateSeriesAndRun(context.Background(), input)
	if err != nil {
		t.Fatalf("CreateSeriesAndRun: %v", err)
	}
	if state != domain.RunStateQueued {
		if err := store.Transition(context.Background(), run.RunID, domain.RunStateQueued, state, domain.Effect{
			TerminalReason: "transient_provider",
			Retryable:      retryable,
		}); err != nil {
			t.Fatalf("Transition to %s: %v", state, err)
		}
	}
	agg, err := store.Get(context.Background(), run.RunID)
	if err != nil {
		t.Fatalf("Get terminal run: %v", err)
	}
	return agg.Run
}

func setIncidentContextVersion(t *testing.T, pool *pgxpool.Pool, incidentID string, version int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `UPDATE incidents SET version = $2 WHERE id = $1`, incidentID, version); err != nil {
		t.Fatalf("set incident context version: %v", err)
	}
}

func nextAttemptInput(run domain.Run, origin string, contextVersion int64) domain.NextAttempt {
	return domain.NextAttempt{
		ContinuationOfRunID:     run.RunID,
		SeriesID:                run.SeriesID,
		IncidentID:              run.IncidentID,
		LifecycleGeneration:     run.LifecycleGeneration,
		DeployedCommit:          run.DeployedCommit,
		ContextVersion:          contextVersion,
		ExpectedPreviousVersion: run.Version,
		TriggerReason:           origin,
		ContinuationReason:      "bounded continuation requested",
	}
}

func TestRunStore_CreateNextAttemptRules(t *testing.T) {
	pool := setupTestDB(t)
	t.Cleanup(pool.Close)

	store := mustStore(t, pool)
	ctx := context.Background()

	t.Run("manual continuation links the next attempt and preserves history", func(t *testing.T) {
		root := makeTerminalRun(t, pool, store, domain.RunStateFailed, 1, false)
		child, err := store.CreateNextAttempt(ctx, nextAttemptInput(root, domain.TriggerReasonManualContinue, root.ContextVersion))
		if err != nil {
			t.Fatalf("CreateNextAttempt: %v", err)
		}
		if child.AttemptNumber != root.AttemptNumber+1 || child.ContinuationOfRunID != root.RunID ||
			child.TriggerReason != domain.TriggerReasonManualContinue || child.ContextVersion != root.ContextVersion {
			t.Fatalf("child = %+v", child)
		}
		if child.State != domain.RunStateQueued {
			t.Fatalf("child state = %s, want queued", child.State)
		}
		agg, err := store.Get(ctx, root.RunID)
		if err != nil {
			t.Fatalf("Get root: %v", err)
		}
		if len(agg.AttemptSummaries) != 2 || agg.AttemptSummaries[0].RunID != root.RunID ||
			agg.AttemptSummaries[1].RunID != child.RunID || agg.AttemptSummaries[1].Origin != domain.TriggerReasonManualContinue {
			t.Fatalf("attempt summaries = %#v", agg.AttemptSummaries)
		}
		if agg.Run.State != domain.RunStateFailed {
			t.Fatalf("root state changed to %s", agg.Run.State)
		}
	})

	t.Run("active and unsupported predecessors are rejected", func(t *testing.T) {
		active := makeTerminalRun(t, pool, store, domain.RunStateQueued, 1, false)
		if _, err := store.CreateNextAttempt(ctx, nextAttemptInput(active, domain.TriggerReasonManualContinue, 1)); !errors.Is(err, domain.ErrActiveAttempt) {
			t.Fatalf("active error = %v, want ErrActiveAttempt", err)
		}

		ready := makeTerminalRun(t, pool, store, domain.RunStateDiagnosisReadyForReview, 1, false)
		if _, err := store.CreateNextAttempt(ctx, nextAttemptInput(ready, domain.TriggerReasonManualContinue, 1)); !errors.Is(err, domain.ErrUnsupportedState) {
			t.Fatalf("ready error = %v, want ErrUnsupportedState", err)
		}
	})

	t.Run("automatic continuation requires fresh context and retryability", func(t *testing.T) {
		nonRetryable := makeTerminalRun(t, pool, store, domain.RunStateFailed, 1, false)
		setIncidentContextVersion(t, pool, nonRetryable.IncidentID, 2)
		if _, err := store.CreateNextAttempt(ctx, nextAttemptInput(nonRetryable, domain.TriggerReasonAutomaticContinue, 2)); !errors.Is(err, domain.ErrAutomaticGateRejected) {
			t.Fatalf("non-retryable error = %v, want ErrAutomaticGateRejected", err)
		}

		unchanged := makeTerminalRun(t, pool, store, domain.RunStateFailed, 1, true)
		if _, err := store.CreateNextAttempt(ctx, nextAttemptInput(unchanged, domain.TriggerReasonAutomaticContinue, 1)); !errors.Is(err, domain.ErrAutomaticGateRejected) {
			t.Fatalf("unchanged-context error = %v, want ErrAutomaticGateRejected", err)
		}
	})

	t.Run("automatic continuation stops at the bounded ceiling", func(t *testing.T) {
		latest := makeTerminalRun(t, pool, store, domain.RunStateFailed, 1, true)
		for i := 0; i < 3; i++ {
			setIncidentContextVersion(t, pool, latest.IncidentID, latest.ContextVersion+1)
			child, err := store.CreateNextAttempt(ctx, nextAttemptInput(latest, domain.TriggerReasonAutomaticContinue, latest.ContextVersion+1))
			if err != nil {
				t.Fatalf("CreateNextAttempt %d: %v", i+1, err)
			}
			if err := store.Transition(ctx, child.RunID, domain.RunStateQueued, domain.RunStateFailed, domain.Effect{
				TerminalReason: "transient_provider",
				Retryable:      true,
			}); err != nil {
				t.Fatalf("finish automatic continuation %d: %v", i+1, err)
			}
			agg, err := store.Get(ctx, child.RunID)
			if err != nil {
				t.Fatalf("Get continuation %d: %v", i+1, err)
			}
			latest = agg.Run
		}
		setIncidentContextVersion(t, pool, latest.IncidentID, latest.ContextVersion+1)
		if _, err := store.CreateNextAttempt(ctx, nextAttemptInput(latest, domain.TriggerReasonAutomaticContinue, latest.ContextVersion+1)); !errors.Is(err, domain.ErrAutomaticCeiling) {
			t.Fatalf("ceiling error = %v, want ErrAutomaticCeiling", err)
		}
	})

	t.Run("manual continuation snapshots current incident context", func(t *testing.T) {
		root := makeTerminalRun(t, pool, store, domain.RunStateFailed, 1, false)
		setIncidentContextVersion(t, pool, root.IncidentID, 2)
		if _, err := store.CreateNextAttempt(ctx, nextAttemptInput(root, domain.TriggerReasonManualContinue, 1)); !errors.Is(err, domain.ErrStalePredecessor) {
			t.Fatalf("stale context error = %v, want ErrStalePredecessor", err)
		}
		child, err := store.CreateNextAttempt(ctx, nextAttemptInput(root, domain.TriggerReasonManualContinue, 2))
		if err != nil {
			t.Fatalf("CreateNextAttempt with current context: %v", err)
		}
		if child.ContextVersion != 2 {
			t.Fatalf("child context version = %d, want 2", child.ContextVersion)
		}
	})

	t.Run("cross-incident predecessor is rejected without creating a child", func(t *testing.T) {
		root := makeTerminalRun(t, pool, store, domain.RunStateFailed, 1, false)
		otherIncidentID := mustIncident(t, pool)
		input := nextAttemptInput(root, domain.TriggerReasonManualContinue, root.ContextVersion)
		input.IncidentID = otherIncidentID.String()

		if _, err := store.CreateNextAttempt(ctx, input); !errors.Is(err, domain.ErrStalePredecessor) {
			t.Fatalf("cross-incident error = %v, want ErrStalePredecessor", err)
		}
		aggregate, err := store.Get(ctx, root.RunID)
		if err != nil {
			t.Fatalf("Get root after rejected continuation: %v", err)
		}
		if len(aggregate.AttemptSummaries) != 1 || aggregate.AttemptSummaries[0].RunID != root.RunID {
			t.Fatalf("rejected continuation changed attempt history: %#v", aggregate.AttemptSummaries)
		}
	})

	t.Run("stale concurrent creators converge on one child", func(t *testing.T) {
		root := makeTerminalRun(t, pool, store, domain.RunStateFailed, 1, false)
		input := nextAttemptInput(root, domain.TriggerReasonManualContinue, root.ContextVersion)
		results := make(chan error, 2)
		var wait sync.WaitGroup
		wait.Add(2)
		for i := 0; i < 2; i++ {
			go func() {
				defer wait.Done()
				_, err := store.CreateNextAttempt(ctx, input)
				results <- err
			}()
		}
		wait.Wait()
		close(results)

		successes := 0
		stale := 0
		for err := range results {
			if err == nil {
				successes++
			} else if errors.Is(err, domain.ErrStalePredecessor) {
				stale++
			} else {
				t.Fatalf("concurrent error = %v", err)
			}
		}
		if successes != 1 || stale != 1 {
			t.Fatalf("successes/stale = %d/%d, want 1/1", successes, stale)
		}
	})

	t.Run("legacy root metadata remains conservative", func(t *testing.T) {
		root := makeTerminalRun(t, pool, store, domain.RunStateQueued, 0, false)
		if root.ContextVersion != 0 || root.TriggerReason != "" || root.Origin != "" || root.Retryable {
			t.Fatalf("legacy root metadata = %+v", root)
		}
	})
}

func TestRunStore_GetLatestPlanningCheckpoint(t *testing.T) {
	pool := setupTestDB(t)
	t.Cleanup(pool.Close)

	store := mustStore(t, pool)
	ctx := context.Background()
	incidentID := mustIncident(t, pool)
	input := newRun(incidentID, 1, "abc123")
	input.ContextVersion = 1
	root, err := store.CreateSeriesAndRun(ctx, input)
	if err != nil {
		t.Fatalf("CreateSeriesAndRun: %v", err)
	}
	if err := store.AppendDecision(ctx, root.RunID, domain.Decision{
		Fixability: domain.FixabilityCodeFixable, Confidence: 0.9,
		CausalReasoning: "durable evidence-gated checkpoint",
	}); err != nil {
		t.Fatalf("AppendDecision root: %v", err)
	}
	if err := store.Transition(ctx, root.RunID, domain.RunStateQueued, domain.RunStateBlockedManualReview, domain.Effect{
		TerminalReason: "invalid_envelope",
	}); err != nil {
		t.Fatalf("finish root: %v", err)
	}
	rootAggregate, err := store.Get(ctx, root.RunID)
	if err != nil {
		t.Fatalf("Get root: %v", err)
	}
	child, err := store.CreateNextAttempt(ctx, nextAttemptInput(rootAggregate.Run, domain.TriggerReasonManualContinue, 1))
	if err != nil {
		t.Fatalf("CreateNextAttempt: %v", err)
	}
	if err := store.AppendDecision(ctx, child.RunID, domain.Decision{
		Fixability: domain.FixabilityInsufficientEvidence, Confidence: 0.2,
		CausalReasoning: "later polluted diagnosis",
	}); err != nil {
		t.Fatalf("AppendDecision child: %v", err)
	}
	if err := store.Transition(ctx, child.RunID, domain.RunStateQueued, domain.RunStateBlockedManualReview, domain.Effect{
		TerminalReason: "insufficient_evidence",
	}); err != nil {
		t.Fatalf("finish child: %v", err)
	}

	checkpoint, err := store.GetLatestPlanningCheckpoint(ctx, root.SeriesID, 1, child.AttemptNumber)
	if err != nil {
		t.Fatalf("GetLatestPlanningCheckpoint: %v", err)
	}
	if checkpoint.Run.RunID != root.RunID || len(checkpoint.Decisions) != 1 ||
		checkpoint.Decisions[0].Fixability != domain.FixabilityCodeFixable {
		t.Fatalf("checkpoint = %#v", checkpoint)
	}
	if _, err := store.GetLatestPlanningCheckpoint(ctx, root.SeriesID, 2, child.AttemptNumber); !errors.Is(err, domain.ErrPlanningCheckpointNotFound) {
		t.Fatalf("new-context checkpoint error = %v, want ErrPlanningCheckpointNotFound", err)
	}
}

func TestRunStore_Get(t *testing.T) {
	pool := setupTestDB(t)
	t.Cleanup(pool.Close)

	store := mustStore(t, pool)
	ctx := context.Background()

	incidentID := mustIncident(t, pool)
	run, err := store.CreateSeriesAndRun(ctx, newRun(incidentID, 1, "abc123"))
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	t.Run("retrieves existing run aggregate", func(t *testing.T) {
		agg, err := store.Get(ctx, run.RunID)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if agg.Run.RunID != run.RunID {
			t.Errorf("expected run ID %v, got %v", run.RunID, agg.Run.RunID)
		}
		if agg.Run.State != domain.RunStateQueued {
			t.Errorf("expected state %s, got %s", domain.RunStateQueued, agg.Run.State)
		}
	})

	t.Run("returns error for non-existent run", func(t *testing.T) {
		_, err := store.Get(ctx, uuid.New().String())
		if err == nil {
			t.Error("expected error for non-existent run")
		}
	})
}

func TestRunStore_Transition(t *testing.T) {
	pool := setupTestDB(t)
	t.Cleanup(pool.Close)

	store := mustStore(t, pool)
	ctx := context.Background()

	incidentID := mustIncident(t, pool)
	run, err := store.CreateSeriesAndRun(ctx, newRun(incidentID, 1, "abc123"))
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	t.Run("successful state transition applies effect counters", func(t *testing.T) {
		effect := domain.Effect{
			ModelCalls:     1,
			ModelTokensIn:  100,
			ModelTokensOut: 40,
			ModelCostCents: 7,
			ModelProvider:  "openai",
			ModelName:      "gpt-5.6",
			ToolCalls:      2,
		}

		if err := store.Transition(ctx, run.RunID, domain.RunStateQueued, domain.RunStateRunning, effect); err != nil {
			t.Fatalf("Transition failed: %v", err)
		}

		attempt, err := store.GetRun(ctx, mustParse(t, run.RunID))
		if err != nil {
			t.Fatalf("GetRun failed: %v", err)
		}
		if attempt.State != domain.RunStateRunning {
			t.Errorf("expected state %s, got %s", domain.RunStateRunning, attempt.State)
		}
		if attempt.ModelCalls != 1 {
			t.Errorf("expected 1 model call, got %d", attempt.ModelCalls)
		}
		if attempt.ModelTokensIn != 100 {
			t.Errorf("expected 100 tokens in, got %d", attempt.ModelTokensIn)
		}
		if attempt.ModelTokensOut != 40 {
			t.Errorf("expected 40 tokens out, got %d", attempt.ModelTokensOut)
		}
		if attempt.ModelCostCents != 7 {
			t.Errorf("expected 7 cost cents, got %d", attempt.ModelCostCents)
		}
		if attempt.ModelProvider != "openai" || attempt.ModelName != "gpt-5.6" {
			t.Errorf("expected model metadata openai/gpt-5.6, got %q/%q", attempt.ModelProvider, attempt.ModelName)
		}
	})

	t.Run("fails on state mismatch", func(t *testing.T) {
		err := store.Transition(ctx, run.RunID, domain.RunStateQueued, domain.RunStateFailed, domain.Effect{})
		if err == nil {
			t.Error("expected error on state mismatch")
		}
	})

	t.Run("terminal state sets ended_at and safe retry metadata", func(t *testing.T) {
		if err := store.Transition(ctx, run.RunID, domain.RunStateRunning, domain.RunStateFailed, domain.Effect{
			TerminalReason: "transient_provider",
			Retryable:      true,
		}); err != nil {
			t.Fatalf("Transition failed: %v", err)
		}

		attempt, err := store.GetRun(ctx, mustParse(t, run.RunID))
		if err != nil {
			t.Fatalf("GetRun failed: %v", err)
		}
		if attempt.EndedAt == nil {
			t.Error("expected ended_at to be set for terminal state")
		}
		if attempt.ElapsedMS == nil {
			t.Error("expected elapsed_ms to be set for terminal state")
		}
		if attempt.TerminalReason != "transient_provider" || !attempt.Retryable {
			t.Errorf("terminal metadata = %q/%t, want transient_provider/true", attempt.TerminalReason, attempt.Retryable)
		}
	})
}

func TestRunStore_ModelMetadataOmitsSecretLikeValues(t *testing.T) {
	pool := setupTestDB(t)
	t.Cleanup(pool.Close)

	store := mustStore(t, pool)
	ctx := context.Background()

	incidentID := mustIncident(t, pool)
	run, err := store.CreateSeriesAndRun(ctx, newRun(incidentID, 1, "abc123"))
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	secretLike := "sk-test-secret-value"
	if err := store.Transition(ctx, run.RunID, domain.RunStateQueued, domain.RunStateRunning, domain.Effect{
		ModelCalls:    1,
		ModelProvider: secretLike,
		ModelName:     "gpt-5.6-" + secretLike,
	}); err != nil {
		t.Fatalf("Transition failed: %v", err)
	}

	attempt, err := store.GetRun(ctx, mustParse(t, run.RunID))
	if err != nil {
		t.Fatalf("GetRun failed: %v", err)
	}
	if strings.Contains(fmt.Sprintf("%#v", attempt), secretLike) {
		t.Fatalf("attempt contains secret-like model metadata: %#v", attempt)
	}
	if attempt.ModelProvider != "" || attempt.ModelName != "" {
		t.Fatalf("model metadata = %q/%q, want empty after sanitization", attempt.ModelProvider, attempt.ModelName)
	}
}

func TestRunStore_AppendDecision(t *testing.T) {
	pool := setupTestDB(t)
	t.Cleanup(pool.Close)

	store := mustStore(t, pool)
	ctx := context.Background()

	incidentID := mustIncident(t, pool)
	run, err := store.CreateSeriesAndRun(ctx, newRun(incidentID, 1, "abc123"))
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	t.Run("appends decisions with correct sequence", func(t *testing.T) {
		err := store.AppendDecision(ctx, run.RunID, domain.Decision{
			Fixability:      domain.FixabilityCodeFixable,
			Confidence:      0.85,
			CausalReasoning: "looks fixable",
		})
		if err != nil {
			t.Fatalf("AppendDecision failed: %v", err)
		}

		err = store.AppendDecision(ctx, run.RunID, domain.Decision{
			Fixability:      domain.FixabilityInsufficientEvidence,
			Confidence:      0.50,
			CausalReasoning: "need more data",
		})
		if err != nil {
			t.Fatalf("second AppendDecision failed: %v", err)
		}

		agg, err := store.Get(ctx, run.RunID)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if len(agg.Decisions) != 2 {
			t.Fatalf("expected 2 decisions, got %d", len(agg.Decisions))
		}
		if agg.Decisions[0].Fixability != domain.FixabilityCodeFixable {
			t.Errorf("expected first decision code_fixable, got %s", agg.Decisions[0].Fixability)
		}
	})
}

func TestRunStore_RecordToolInvocation(t *testing.T) {
	pool := setupTestDB(t)
	t.Cleanup(pool.Close)

	store := mustStore(t, pool)
	ctx := context.Background()

	incidentID := mustIncident(t, pool)
	run, err := store.CreateSeriesAndRun(ctx, newRun(incidentID, 1, "abc123"))
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	t.Run("records invocations with correct sequence", func(t *testing.T) {
		err := store.RecordToolInvocation(ctx, run.RunID, domain.ToolInvocation{
			InvocationID: "action:repository:1",
			ToolName:     "repository.read_file",
			Phase:        domain.RunStateDiagnosing,
			EvidenceIDs:  []string{"ev-repo-1"},
		})
		if err != nil {
			t.Fatalf("RecordToolInvocation failed: %v", err)
		}

		err = store.RecordToolInvocation(ctx, run.RunID, domain.ToolInvocation{
			InvocationID: "action:provider_evidence:1",
			ToolName:     "evidence.search",
			Phase:        domain.RunStateDiagnosing,
			EvidenceIDs:  []string{"ev-provider-1"},
			Error:        "connector_timeout",
		})
		if err != nil {
			t.Fatalf("second RecordToolInvocation failed: %v", err)
		}

		agg, err := store.Get(ctx, run.RunID)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if len(agg.ToolInvocations) != 2 {
			t.Fatalf("expected 2 invocations, got %d", len(agg.ToolInvocations))
		}
		first, second := agg.ToolInvocations[0], agg.ToolInvocations[1]
		if first.InvocationID != "action:repository:1" || !reflect.DeepEqual(first.EvidenceIDs, []string{"ev-repo-1"}) {
			t.Fatalf("first invocation authority = %#v", first)
		}
		if second.InvocationID != "action:provider_evidence:1" || second.Error != "connector_timeout" ||
			!reflect.DeepEqual(second.EvidenceIDs, []string{"ev-provider-1"}) {
			t.Fatalf("second invocation authority = %#v", second)
		}
	})
}

func TestRunStore_RecordToolInvocationOmitsRawPayloads(t *testing.T) {
	pool := setupTestDB(t)
	t.Cleanup(pool.Close)

	store := mustStore(t, pool)
	ctx := context.Background()

	incidentID := mustIncident(t, pool)
	run, err := store.CreateSeriesAndRun(ctx, newRun(incidentID, 1, "abc123"))
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	secretLike := "sk-test-secret-value"
	if err := store.RecordToolInvocation(ctx, run.RunID, domain.ToolInvocation{
		ToolName:          "repository.read_file",
		Phase:             domain.RunStateDiagnosing,
		ParametersSummary: "path=main.go authorization=" + secretLike,
		ResultSummary:     "raw file content containing " + secretLike,
		Error:             "connector returned token " + secretLike,
	}); err != nil {
		t.Fatalf("RecordToolInvocation failed: %v", err)
	}

	agg, err := store.Get(ctx, run.RunID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	rendered := fmt.Sprintf("%#v", agg)
	if strings.Contains(rendered, secretLike) {
		t.Fatalf("aggregate contains raw tool payload or credential-like value: %s", rendered)
	}
	if len(agg.ToolInvocations) != 1 {
		t.Fatalf("tool invocations = %d, want 1", len(agg.ToolInvocations))
	}
	if got := agg.ToolInvocations[0]; got.ToolName != "repository.read_file" || got.Phase != domain.RunStateDiagnosing ||
		got.ResultSummary != "error" || got.Error != "error" {
		t.Fatalf("sanitized invocation = %#v", got)
	}
}

func TestRunStore_OptimisticVersioning(t *testing.T) {
	pool := setupTestDB(t)
	t.Cleanup(pool.Close)

	store := mustStore(t, pool)
	ctx := context.Background()

	incidentID := mustIncident(t, pool)
	run, err := store.CreateSeriesAndRun(ctx, newRun(incidentID, 1, "abc123"))
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	if err := store.Transition(ctx, run.RunID, domain.RunStateQueued, domain.RunStateRunning, domain.Effect{}); err != nil {
		t.Fatalf("first transition failed: %v", err)
	}

	agg1, err := store.Get(ctx, run.RunID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	initialVersion := agg1.Run.Version

	if err := store.Transition(ctx, run.RunID, domain.RunStateRunning, domain.RunStateFailed, domain.Effect{}); err != nil {
		t.Fatalf("second transition failed: %v", err)
	}

	agg2, err := store.Get(ctx, run.RunID)
	if err != nil {
		t.Fatalf("Get after transition failed: %v", err)
	}
	if agg2.Run.Version <= initialVersion {
		t.Errorf("expected version to increment, got %d -> %d", initialVersion, agg2.Run.Version)
	}
}

func TestRunStore_TransactionalIntegrity(t *testing.T) {
	pool := setupTestDB(t)
	t.Cleanup(pool.Close)

	store := mustStore(t, pool)
	ctx := context.Background()

	t.Run("series and run created atomically", func(t *testing.T) {
		incidentID := mustIncident(t, pool)
		run, err := store.CreateSeriesAndRun(ctx, newRun(incidentID, 1, "abc123"))
		if err != nil {
			t.Fatalf("CreateSeriesAndRun failed: %v", err)
		}

		agg, err := store.Get(ctx, run.RunID)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if agg.Run.SeriesID != run.SeriesID {
			t.Errorf("expected series ID %v, got %v", run.SeriesID, agg.Run.SeriesID)
		}
	})
}

// TestRunStore_SeriesKeyUniqueness verifies the series key reuses the same
// series for an identical key and creates a new series for a different one.
func TestRunStore_SeriesKeyUniqueness(t *testing.T) {
	pool := setupTestDB(t)
	t.Cleanup(pool.Close)

	store := mustStore(t, pool)
	ctx := context.Background()

	incidentID := mustIncident(t, pool)
	generation := int64(1)
	deployedCommit := "abc123"

	run1, err := store.CreateSeriesAndRun(ctx, newRun(incidentID, generation, deployedCommit))
	if err != nil {
		t.Fatalf("first CreateSeriesAndRun failed: %v", err)
	}

	run2, err := store.CreateSeriesAndRun(ctx, newRun(incidentID, generation, deployedCommit))
	if err != nil {
		t.Fatalf("second CreateSeriesAndRun failed: %v", err)
	}
	if run1.SeriesID != run2.SeriesID {
		t.Errorf("expected same series for identical key, got %v and %v", run1.SeriesID, run2.SeriesID)
	}

	run3, err := store.CreateSeriesAndRun(ctx, newRun(incidentID, generation+1, deployedCommit))
	if err != nil {
		t.Fatalf("third CreateSeriesAndRun failed: %v", err)
	}
	if run1.SeriesID == run3.SeriesID {
		t.Error("expected different series for different generation")
	}
}

func TestRunStore_ReviewSurfaceRoundTrip(t *testing.T) {
	pool := setupTestDB(t)
	t.Cleanup(pool.Close)

	store := mustStore(t, pool)
	ctx := context.Background()
	incidentID := mustIncident(t, pool)
	run, err := store.CreateSeriesAndRun(ctx, newRun(incidentID, 1, "abc123"))
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	if err := store.AppendDecision(ctx, run.RunID, domain.Decision{
		Fixability: domain.FixabilityCodeFixable, Confidence: 0.91, CausalReasoning: "nil deref",
		Contradictions: []string{"none"}, MissingEvidence: []string{}, EvidenceCitations: []string{"ev-1"},
		RecommendedNextAction: "apply suggested patch",
	}); err != nil {
		t.Fatalf("AppendDecision: %v", err)
	}
	if err := store.AppendPlans(ctx, run.RunID, []domain.RepairPlanCandidate{{
		PlanID: "p1", IntendedBehavior: "add nil check", Risk: domain.RiskOrdinary,
		Rationale: "simplest", EvidenceRefs: []string{"ev-1"}, AffectedFiles: []string{"main.go"},
		RollbackStrategy: "revert", Recommended: true,
	}}, "p1"); err != nil {
		t.Fatalf("AppendPlans: %v", err)
	}
	if err := store.RecordSuggestedDiff(ctx, run.RunID, "diff --git a/main.go"); err != nil {
		t.Fatalf("RecordSuggestedDiff: %v", err)
	}
	agg, err := store.GetLatestForIncident(ctx, incidentID.String(), 1, "abc123")
	if err != nil {
		t.Fatalf("GetLatestForIncident: %v", err)
	}
	if len(agg.Decisions) != 1 || agg.Decisions[0].RecommendedNextAction != "apply suggested patch" {
		t.Fatalf("decisions = %#v", agg.Decisions)
	}
	if len(agg.Plans) != 1 || !agg.Plans[0].Recommended || agg.SuggestedDiff != "diff --git a/main.go" {
		t.Fatalf("review aggregate = %#v", agg)
	}
}

func TestRunStore_NotifyWritesAllowlistedAuditMetadata(t *testing.T) {
	pool := setupTestDB(t)
	t.Cleanup(pool.Close)

	store := mustStore(t, pool)
	ctx := context.Background()
	incidentID := mustIncident(t, pool)
	run, err := store.CreateSeriesAndRun(ctx, newRun(incidentID, 1, "abc123"))
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	notification := application.TerminalNotification{
		RunID: run.RunID, Kind: application.NotificationDiagnosisReadyForReview,
		Summary:    "Remediation diagnosis is ready for review.",
		Fixability: domain.FixabilityCodeFixable, State: domain.RunStateDiagnosisReadyForReview,
	}
	// 白名单元数据：允许的键仅 runId/state/fixability/kind/agentLoopMode。
	meta := application.NotificationMetadata(notification)
	if len(meta) != 5 || meta["runId"] != run.RunID || meta["kind"] != application.NotificationDiagnosisReadyForReview {
		t.Fatalf("metadata = %#v", meta)
	}
	for key := range meta {
		switch key {
		case "runId", "state", "fixability", "kind", "agentLoopMode":
		default:
			t.Fatalf("unexpected metadata key %q", key)
		}
	}
	// 有 owning incident 时 Notify 成功写入审计。
	if err := store.Notify(ctx, notification); err != nil {
		t.Fatalf("notify with owning incident failed: %v", err)
	}
	// 无对应 run 时 fail closed，保持安全诊断路径。
	orphan := application.TerminalNotification{
		RunID: newV7(t).String(), Kind: application.NotificationDiagnosisReadyForReview,
		Summary: "orphan", Fixability: domain.FixabilityCodeFixable, State: domain.RunStateDiagnosisReadyForReview,
	}
	if err := store.Notify(ctx, orphan); err == nil {
		t.Fatal("expected notify to fail closed for an unknown run")
	} else if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("notify error = %v", err)
	}
}

// fixtureScopeIDs 从 incident 行读取 project/environment/source 的 UUID，供证据写入。
func fixtureScopeIDs(t *testing.T, pool *pgxpool.Pool, incidentID string) (projectID, environmentID, sourceID string) {
	t.Helper()
	var project, environment, source uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`SELECT project_id, environment_id, source_id FROM incidents WHERE id = $1`, incidentID,
	).Scan(&project, &environment, &source); err != nil {
		t.Fatalf("load incident scope ids: %v", err)
	}
	return project.String(), environment.String(), source.String()
}

// runtimeEvidenceInput 构造一条 run 归属的 canonical runtime evidence。
func runtimeEvidenceInput(run domain.Run, projectID, environmentID, sourceID string, output string) domain.StoredEvidence {
	payload, _ := json.Marshal(map[string]interface{}{
		"command": "'hostname' '-I'", "stdout": output, "stderr": "", "truncated": false,
	})
	sum := sha256.Sum256(payload)
	return domain.StoredEvidence{
		ProjectID: projectID, EnvironmentID: environmentID, SourceID: sourceID,
		IncidentID: run.IncidentID, RunID: run.RunID,
		Provider: "ssh", EvidenceKind: domain.EvidenceKindRuntime,
		DeduplicationKey: "ssh.inspect:" + hex.EncodeToString(sum[:]),
		Classification:   domain.EvidenceCorrelatedSupport,
		Outcome:          "success", Available: true, Primary: true,
		OperationalCorrelation: true,
		ContentHash:            hex.EncodeToString(sum[:]),
		ByteCount:              int64(len(payload)),
		Provenance:             json.RawMessage(`{"tool":"ssh.inspect","phase":"diagnosing","projectionVersion":1}`),
		Payload:                payload,
	}
}

// TestRunStore_ContinuationEvidenceSameSeriesReuse 覆盖 implement.md 第 6 步：
// child attempt 可引用同 series 早期 attempt 的 runtime evidence，证据 ID 不变、
// 行不重新归属；未来 attempt、跨 series/incident 的证据不可解析。
func TestRunStore_ContinuationEvidenceSameSeriesReuse(t *testing.T) {
	pool := setupTestDB(t)
	t.Cleanup(pool.Close)

	store := mustStore(t, pool)
	ctx := context.Background()

	// 根 attempt 收集 SSH inspect runtime evidence 后失败。
	root := makeTerminalRun(t, pool, store, domain.RunStateFailed, 1, true)
	projectID, environmentID, sourceID := fixtureScopeIDs(t, pool, root.IncidentID)
	evidence, err := store.AppendEvidence(ctx, runtimeEvidenceInput(root, projectID, environmentID, sourceID, "10.16.6.17 43.131.29.186"))
	if err != nil {
		t.Fatalf("AppendEvidence: %v", err)
	}
	if evidence.RunID != root.RunID {
		t.Fatalf("evidence run ownership = %s, want %s", evidence.RunID, root.RunID)
	}

	// child attempt 在同一 series 中创建；请求 context version 2 前先推进
	// incident 的权威版本，避免用不存在的上下文创建 continuation。
	setIncidentContextVersion(t, pool, root.IncidentID, 2)
	child, err := store.CreateNextAttempt(ctx, nextAttemptInput(root, domain.TriggerReasonManualContinue, 2))
	if err != nil {
		t.Fatalf("CreateNextAttempt: %v", err)
	}
	if child.AttemptNumber != root.AttemptNumber+1 {
		t.Fatalf("child attempt = %d, want %d", child.AttemptNumber, root.AttemptNumber+1)
	}

	// child 可以解析早期 attempt 的证据（GetRemediationEvidenceForRun 放宽到同 series）。
	resolution, err := store.ResolveEvidence(ctx, child.RunID, []domain.EvidenceCitation{{EvidenceID: evidence.EvidenceID}})
	if err != nil {
		t.Fatalf("ResolveEvidence: %v", err)
	}
	if len(resolution.Records) != 1 || resolution.Records[0].EvidenceID != evidence.EvidenceID ||
		resolution.Records[0].Classification != domain.EvidenceCorrelatedSupport {
		t.Fatalf("child resolution = %#v", resolution.Records)
	}

	// 同 series 早期证据通过 continuation loader 读到，且证据 ID 不变、行未重新归属。
	records, err := store.ListContinuationRuntimeEvidence(ctx, domain.ContinuationEvidenceQuery{
		SeriesID: root.SeriesID, ThroughAttemptNumber: root.AttemptNumber, Limit: 16,
	})
	if err != nil {
		t.Fatalf("ListContinuationRuntimeEvidence: %v", err)
	}
	if len(records) != 1 || records[0].EvidenceID != evidence.EvidenceID || records[0].RunID != root.RunID {
		t.Fatalf("continuation records = %#v", records)
	}
	after, err := store.Get(ctx, root.RunID)
	if err != nil {
		t.Fatalf("Get root after child: %v", err)
	}
	if len(after.AttemptSummaries) != 2 {
		t.Fatalf("attempt summaries = %#v", after.AttemptSummaries)
	}

	// 另一 incident（不同 series）的证据不能被本 series 解析，也不会被 loader 返回。
	otherIncident := mustIncident(t, pool)
	otherProject, otherEnv, otherSource := fixtureScopeIDs(t, pool, otherIncident.String())
	otherRun := makeTerminalRunForIncident(t, pool, store, otherIncident, domain.RunStateFailed, 1, false)
	otherEvidence, err := store.AppendEvidence(ctx, runtimeEvidenceInput(otherRun, otherProject, otherEnv, otherSource, "other-host"))
	if err != nil {
		t.Fatalf("AppendEvidence other: %v", err)
	}
	otherResolution, err := store.ResolveEvidence(ctx, child.RunID, []domain.EvidenceCitation{{EvidenceID: otherEvidence.EvidenceID}})
	if err != nil {
		t.Fatalf("ResolveEvidence other: %v", err)
	}
	if len(otherResolution.Records) != 0 {
		t.Fatalf("cross-incident evidence must be unresolved: %#v", otherResolution.Records)
	}
	records, err = store.ListContinuationRuntimeEvidence(ctx, domain.ContinuationEvidenceQuery{
		SeriesID: root.SeriesID, ThroughAttemptNumber: root.AttemptNumber, Limit: 16,
	})
	if err != nil {
		t.Fatalf("ListContinuationRuntimeEvidence after other incident: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("cross-incident evidence leaked into loader: %#v", records)
	}

	// 未来 attempt 的证据不能被 loader 读回（attempt 单调）。
	futureEvidence, err := store.AppendEvidence(ctx, runtimeEvidenceInput(child, projectID, environmentID, sourceID, "future-output"))
	if err != nil {
		t.Fatalf("AppendEvidence future: %v", err)
	}
	records, err = store.ListContinuationRuntimeEvidence(ctx, domain.ContinuationEvidenceQuery{
		SeriesID: root.SeriesID, ThroughAttemptNumber: root.AttemptNumber, Limit: 16,
	})
	if err != nil {
		t.Fatalf("ListContinuationRuntimeEvidence future: %v", err)
	}
	for _, record := range records {
		if record.EvidenceID == futureEvidence.EvidenceID {
			t.Fatalf("future-attempt evidence must not be readable through attempt %d", root.AttemptNumber)
		}
	}
}

// providerDetailEvidenceInput 构造一条 run 归属的 provider_detail 证据（供索引测试）。
func providerDetailEvidenceInput(run domain.Run, projectID, environmentID, sourceID string) domain.StoredEvidence {
	payload, _ := json.Marshal(map[string]interface{}{
		"recordId": "record-uuid", "message": "trusted provider detail", "sourcePath": "internal/app/main.go", "line": 42,
	})
	sum := sha256.Sum256(payload)
	return domain.StoredEvidence{
		ProjectID: projectID, EnvironmentID: environmentID, SourceID: sourceID,
		IncidentID: run.IncidentID, RunID: run.RunID,
		Provider: "tencent_cls", EvidenceKind: domain.EvidenceKindProviderDetail,
		DeduplicationKey: "tencent_cls.detail:" + hex.EncodeToString(sum[:]),
		Classification:   domain.EvidenceDirectFault,
		Outcome:          "success", Available: true, Primary: true,
		TemporalCorrelation: true, OperationalCorrelation: true,
		ContentHash: hex.EncodeToString(sum[:]),
		ByteCount:   int64(len(payload)),
		Provenance:  json.RawMessage(`{"adapter":"tencent_cls","detail_resolution":"validated_provider_detail_get_alert_detail","contradictions":[]}`),
		Payload:     payload,
	}
}

// TestRunStore_ContinuationEvidenceIndexSameSeries 覆盖 implement.md slice 3：
// child attempt 可读取同 series 早期 attempt 的非 runtime 证据紧凑索引
// （provider_detail 等），pre-run 证据（run_id IS NULL）始终可见且
// sourceAttempt=0；runtime 行不进入索引（由全量 payload 查询提供）；未来
// attempt 与跨 series/incident 的证据不可见。索引条目不含 payload。
func TestRunStore_ContinuationEvidenceIndexSameSeries(t *testing.T) {
	pool := setupTestDB(t)
	t.Cleanup(pool.Close)

	store := mustStore(t, pool)
	ctx := context.Background()

	// 根 attempt 收集 provider_detail 与 runtime evidence 后失败。
	root := makeTerminalRun(t, pool, store, domain.RunStateFailed, 1, true)
	projectID, environmentID, sourceID := fixtureScopeIDs(t, pool, root.IncidentID)
	detail, err := store.AppendEvidence(ctx, providerDetailEvidenceInput(root, projectID, environmentID, sourceID))
	if err != nil {
		t.Fatalf("AppendEvidence provider detail: %v", err)
	}
	if detail.RunID != root.RunID {
		t.Fatalf("provider detail run ownership = %s, want %s", detail.RunID, root.RunID)
	}
	runtimeEvidence, err := store.AppendEvidence(ctx, runtimeEvidenceInput(root, projectID, environmentID, sourceID, "10.16.6.17"))
	if err != nil {
		t.Fatalf("AppendEvidence runtime: %v", err)
	}

	// pre-run normalized_alert 证据（run_id IS NULL）同样进入索引。它从
	// incident 快照 baseline，因此先把 incident 对齐到 series 并推进 child
	// 将使用的 context version。
	if _, err := pool.Exec(ctx,
		`UPDATE incidents SET version = 2, lifecycle_generation = $2, deployed_commit = $3 WHERE id = $1`,
		root.IncidentID, root.LifecycleGeneration, root.DeployedCommit); err != nil {
		t.Fatalf("align continuation incident baseline: %v", err)
	}
	preRun := runtimeEvidenceInput(root, projectID, environmentID, sourceID, "pre-run alert")
	preRun.RunID = ""
	preRun.Provider = "webhook"
	preRun.EvidenceKind = domain.EvidenceKindNormalizedAlert
	preRun.Classification = domain.EvidenceContextual
	preRun.DeduplicationKey = "webhook.normalized_alert:pre-run-fixed"
	preRun, err = store.AppendEvidence(ctx, preRun)
	if err != nil {
		t.Fatalf("AppendEvidence pre-run: %v", err)
	}
	if preRun.RunID != "" {
		t.Fatalf("pre-run evidence run ownership = %s, want empty", preRun.RunID)
	}

	// child attempt 在同一 series 中创建。
	child, err := store.CreateNextAttempt(ctx, nextAttemptInput(root, domain.TriggerReasonManualContinue, 2))
	if err != nil {
		t.Fatalf("CreateNextAttempt: %v", err)
	}

	entries, err := store.ListContinuationEvidenceIndex(ctx, domain.ContinuationEvidenceQuery{
		SeriesID: root.SeriesID, ThroughAttemptNumber: root.AttemptNumber, Limit: 32,
	})
	if err != nil {
		t.Fatalf("ListContinuationEvidenceIndex: %v", err)
	}
	byID := make(map[string]domain.EvidenceIndexEntry, len(entries))
	for _, entry := range entries {
		byID[entry.EvidenceID] = entry
	}
	// provider_detail 与 pre-run 证据都进入索引；runtime 行被排除。
	detailEntry, ok := byID[detail.EvidenceID]
	if !ok || detailEntry.Kind != domain.EvidenceKindProviderDetail ||
		detailEntry.Provider != "tencent_cls" || detailEntry.Classification != domain.EvidenceDirectFault ||
		detailEntry.SourceAttempt != 1 || detailEntry.ContentHash != detail.ContentHash {
		t.Fatalf("index detail entry = %#v, want provider detail with source attempt 1", detailEntry)
	}
	preRunEntry, ok := byID[preRun.EvidenceID]
	if !ok || preRunEntry.Kind != domain.EvidenceKindNormalizedAlert || preRunEntry.SourceAttempt != 0 {
		t.Fatalf("index pre-run entry = %#v, want normalized alert with source attempt 0", preRunEntry)
	}
	if _, ok := byID[runtimeEvidence.EvidenceID]; ok {
		t.Fatalf("runtime evidence leaked into the compact index: %#v", byID[runtimeEvidence.EvidenceID])
	}

	// 另一 incident（不同 series）的证据不能进入本 series 索引。
	otherIncident := mustIncident(t, pool)
	otherProject, otherEnv, otherSource := fixtureScopeIDs(t, pool, otherIncident.String())
	otherRun := makeTerminalRunForIncident(t, pool, store, otherIncident, domain.RunStateFailed, 1, false)
	otherDetail, err := store.AppendEvidence(ctx, providerDetailEvidenceInput(otherRun, otherProject, otherEnv, otherSource))
	if err != nil {
		t.Fatalf("AppendEvidence other: %v", err)
	}
	entries, err = store.ListContinuationEvidenceIndex(ctx, domain.ContinuationEvidenceQuery{
		SeriesID: root.SeriesID, ThroughAttemptNumber: root.AttemptNumber, Limit: 32,
	})
	if err != nil {
		t.Fatalf("ListContinuationEvidenceIndex after other incident: %v", err)
	}
	for _, entry := range entries {
		if entry.EvidenceID == otherDetail.EvidenceID {
			t.Fatalf("cross-incident evidence leaked into the index: %#v", entry)
		}
	}

	// 未来 attempt 的证据不能通过 attempt 单调边界读回。
	futureDetail, err := store.AppendEvidence(ctx, providerDetailEvidenceInput(child, projectID, environmentID, sourceID))
	if err != nil {
		t.Fatalf("AppendEvidence future: %v", err)
	}
	entries, err = store.ListContinuationEvidenceIndex(ctx, domain.ContinuationEvidenceQuery{
		SeriesID: root.SeriesID, ThroughAttemptNumber: root.AttemptNumber, Limit: 32,
	})
	if err != nil {
		t.Fatalf("ListContinuationEvidenceIndex future: %v", err)
	}
	for _, entry := range entries {
		if entry.EvidenceID == futureDetail.EvidenceID {
			t.Fatalf("future-attempt evidence must not be readable through attempt %d", root.AttemptNumber)
		}
	}
}

func TestLifecycleEffectsRoundTripAndSuccessfulProjectionIsImmutable(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()
	incidentID := mustIncident(t, pool)
	store := mustStore(t, pool)
	root, err := store.CreateSeriesAndRun(context.Background(), newRun(incidentID, 1, "abc123"))
	if err != nil {
		t.Fatalf("CreateSeriesAndRun: %v", err)
	}
	started := domain.LifecycleEffect{
		RunID: root.RunID, Kind: domain.LifecycleEffectWorkspace, IdempotencyKey: "workspace/test",
		State: domain.LifecycleEffectStarted, Attempt: 1, BaselineCommit: root.DeployedCommit,
		Summary: "workspace started",
	}
	if _, err := store.UpsertLifecycleEffect(context.Background(), started); err != nil {
		t.Fatalf("UpsertLifecycleEffect started: %v", err)
	}
	succeeded := started
	succeeded.State = domain.LifecycleEffectSucceeded
	succeeded.WorkspaceID = "workspace-1"
	succeeded.BaseTreeHash = "base-tree"
	succeeded.ResultTreeHash = "base-tree"
	succeeded.Summary = "workspace ready"
	if _, err := store.UpsertLifecycleEffect(context.Background(), succeeded); err != nil {
		t.Fatalf("UpsertLifecycleEffect succeeded: %v", err)
	}
	failed := succeeded
	failed.State = domain.LifecycleEffectFailed
	failed.ErrorCode = "transport"
	failed.Summary = "must not replace success"
	loaded, err := store.UpsertLifecycleEffect(context.Background(), failed)
	if err != nil {
		t.Fatalf("UpsertLifecycleEffect after success: %v", err)
	}
	if loaded.State != domain.LifecycleEffectSucceeded || loaded.WorkspaceID != succeeded.WorkspaceID {
		t.Fatalf("successful lifecycle effect was overwritten: %#v", loaded)
	}
	listed, err := store.ListLifecycleEffects(context.Background(), root.RunID)
	if err != nil || len(listed) != 1 || listed[0].State != domain.LifecycleEffectSucceeded {
		t.Fatalf("ListLifecycleEffects = %#v, err=%v", listed, err)
	}
}

// makeTerminalRunForIncident 在指定 incident 上创建 terminal run（供跨 incident 隔离断言）。
func makeTerminalRunForIncident(t *testing.T, pool *pgxpool.Pool, store *postgres.RunStore, incidentID uuid.UUID, state domain.RunState, contextVersion int64, retryable bool) domain.Run {
	t.Helper()
	input := newRun(incidentID, 1, "abc123")
	input.ContextVersion = contextVersion
	if contextVersion > 0 {
		input.TriggerReason = domain.TriggerReasonAutomatic
	}
	run, err := store.CreateSeriesAndRun(context.Background(), input)
	if err != nil {
		t.Fatalf("CreateSeriesAndRun: %v", err)
	}
	if state != domain.RunStateQueued {
		if err := store.Transition(context.Background(), run.RunID, domain.RunStateQueued, state, domain.Effect{
			TerminalReason: "transient_provider",
			Retryable:      retryable,
		}); err != nil {
			t.Fatalf("Transition to %s: %v", state, err)
		}
	}
	return run
}
