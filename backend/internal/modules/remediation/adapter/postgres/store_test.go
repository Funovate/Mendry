package postgres_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"fixthe/backend/internal/modules/remediation/adapter/postgres"
	"fixthe/backend/internal/modules/remediation/application"
	"fixthe/backend/internal/modules/remediation/domain"
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
	connStr := "postgres://fixthe:fixthe@localhost:5432/fixthe_test?sslmode=disable"

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

func TestRunStore_CreateSeriesAndRun(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	store := mustStore(t, pool)
	ctx := context.Background()

	incidentID := uuid.New()
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

func TestRunStore_Get(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	store := mustStore(t, pool)
	ctx := context.Background()

	incidentID := uuid.New()
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
	defer pool.Close()

	store := mustStore(t, pool)
	ctx := context.Background()

	incidentID := uuid.New()
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

	t.Run("terminal state sets ended_at", func(t *testing.T) {
		if err := store.Transition(ctx, run.RunID, domain.RunStateRunning, domain.RunStateFailed, domain.Effect{}); err != nil {
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
	})
}

func TestRunStore_ModelMetadataOmitsSecretLikeValues(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	store := mustStore(t, pool)
	ctx := context.Background()

	incidentID := uuid.New()
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
	defer pool.Close()

	store := mustStore(t, pool)
	ctx := context.Background()

	incidentID := uuid.New()
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
	defer pool.Close()

	store := mustStore(t, pool)
	ctx := context.Background()

	incidentID := uuid.New()
	run, err := store.CreateSeriesAndRun(ctx, newRun(incidentID, 1, "abc123"))
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	t.Run("records invocations with correct sequence", func(t *testing.T) {
		err := store.RecordToolInvocation(ctx, run.RunID, domain.ToolInvocation{
			ToolName: "repository.read_file",
			Phase:    domain.RunStateDiagnosing,
		})
		if err != nil {
			t.Fatalf("RecordToolInvocation failed: %v", err)
		}

		err = store.RecordToolInvocation(ctx, run.RunID, domain.ToolInvocation{
			ToolName: "evidence.search",
			Phase:    domain.RunStateDiagnosing,
		})
		if err != nil {
			t.Fatalf("second RecordToolInvocation failed: %v", err)
		}

		agg, err := store.Get(ctx, run.RunID)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if len(agg.ToolInvocations) != 2 {
			t.Errorf("expected 2 invocations, got %d", len(agg.ToolInvocations))
		}
	})
}

func TestRunStore_RecordToolInvocationOmitsRawPayloads(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	store := mustStore(t, pool)
	ctx := context.Background()

	incidentID := uuid.New()
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
	if got := agg.ToolInvocations[0]; got.ToolName != "repository.read_file" || got.Phase != domain.RunStateDiagnosing || got.ResultSummary != "error" {
		t.Fatalf("sanitized invocation = %#v", got)
	}
}

func TestRunStore_OptimisticVersioning(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	store := mustStore(t, pool)
	ctx := context.Background()

	incidentID := uuid.New()
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
	defer pool.Close()

	store := mustStore(t, pool)
	ctx := context.Background()

	t.Run("series and run created atomically", func(t *testing.T) {
		incidentID := uuid.New()
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
	defer pool.Close()

	store := mustStore(t, pool)
	ctx := context.Background()

	incidentID := uuid.New()
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
	defer pool.Close()

	store := mustStore(t, pool)
	ctx := context.Background()
	incidentID := uuid.New()
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
	defer pool.Close()

	store := mustStore(t, pool)
	ctx := context.Background()
	incidentID := uuid.New()
	run, err := store.CreateSeriesAndRun(ctx, newRun(incidentID, 1, "abc123"))
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	notification := application.TerminalNotification{
		RunID: run.RunID, Kind: application.NotificationDiagnosisReadyForReview,
		Summary:    "Remediation diagnosis is ready for review.",
		Fixability: domain.FixabilityCodeFixable, State: domain.RunStateDiagnosisReadyForReview,
	}
	err = store.Notify(ctx, notification)
	if err == nil {
		t.Fatal("expected notify to fail closed without an owning incident")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("notify error = %v", err)
	}
	meta := application.NotificationMetadata(notification)
	if len(meta) != 4 || meta["runId"] != run.RunID || meta["kind"] != application.NotificationDiagnosisReadyForReview {
		t.Fatalf("metadata = %#v", meta)
	}
	for key := range meta {
		switch key {
		case "runId", "state", "fixability", "kind":
		default:
			t.Fatalf("unexpected metadata key %q", key)
		}
	}
}
