package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"fixthe/backend/internal/modules/remediation/adapter/postgres"
	"fixthe/backend/internal/modules/remediation/domain"
)

func checkpointBudgetRecovery() domain.BudgetPlanRecoveryV1 {
	plan := domain.BudgetPlanV1{
		SchemaVersion: domain.BudgetPlanSchemaVersionV1,
		Ceiling:       domain.BudgetLimits{MaxElapsed: time.Second, MaxModelCalls: 1, MaxModelCostCents: 1, MaxToolCalls: 1, MaxEvidenceBytes: 1, MaxRepositoryBytes: 1},
	}
	for _, phase := range domain.BudgetPhaseOrder() {
		plan.Phases = append(plan.Phases, domain.PhaseBudgetAllocation{Phase: phase})
	}
	return domain.BudgetPlanRecoveryV1{SchemaVersion: domain.BudgetPlanSchemaVersionV1, Plan: plan, CurrentPhase: domain.RunStateDiagnosing, Frontier: 0}
}

func mustCheckpointStore(t *testing.T, pool *pgxpool.Pool) *postgres.CheckpointStore {
	t.Helper()
	store, err := postgres.NewCheckpointStore(pool)
	if err != nil {
		t.Fatalf("NewCheckpointStore: %v", err)
	}
	return store
}

// mustCreateRun 用现有 RunStore 创建 queued root run，返回领域 Run。
func mustCreateRun(t *testing.T, pool *pgxpool.Pool, incidentID uuid.UUID, generation int64, commit string) domain.Run {
	t.Helper()
	ctx := context.Background()
	run, err := mustStore(t, pool).CreateSeriesAndRun(ctx, newRun(incidentID, generation, commit))
	if err != nil {
		t.Fatalf("CreateSeriesAndRun failed: %v", err)
	}
	return run
}

func checkpointForRun(run domain.Run) domain.WorkingMemoryCheckpointV1 {
	return domain.WorkingMemoryCheckpointV1{
		SchemaVersion:      domain.CheckpointSchemaVersionV1,
		RunID:              run.RunID,
		SeriesID:           run.SeriesID,
		ContextVersion:     run.ContextVersion,
		ObservedRunVersion: run.Version,
		Phase:              "diagnosing",
		Objective: domain.CheckpointObjective{
			Goal:               "explain the production symptom",
			CompletionCriteria: []string{"causal closure"},
		},
		VerifiedFacts: []domain.CheckpointVerifiedFact{
			{Statement: "the stack names the deployed function", EvidenceIDs: []string{"ev-1"}},
		},
		NextActions: []string{"inspect repository.read_file at the reported line"},
		Budget:      checkpointBudgetRecovery(),
		Reason:      domain.CheckpointReasonThreshold,
	}
}

func TestCheckpointStore_AppendAndLoadAtomicity(t *testing.T) {
	pool := setupTestDB(t)
	t.Cleanup(pool.Close)

	store := mustCheckpointStore(t, pool)
	ctx := context.Background()
	incidentID := mustIncident(t, pool)
	run := mustCreateRun(t, pool, incidentID, 1, "abc123")

	t.Run("append persists event and snapshot atomically", func(t *testing.T) {
		snapshot, err := store.AppendCheckpoint(ctx, run.RunID, checkpointForRun(run))
		if err != nil {
			t.Fatalf("AppendCheckpoint failed: %v", err)
		}
		if snapshot.Sequence != 1 {
			t.Errorf("first snapshot sequence = %d, want 1", snapshot.Sequence)
		}
		if snapshot.Checkpoint.Sequence != 1 {
			t.Errorf("stamped checkpoint sequence = %d, want 1", snapshot.Checkpoint.Sequence)
		}
		if snapshot.ContentHash == "" || len(snapshot.ContentHash) != 64 {
			t.Errorf("content hash = %q, want 64 hex chars", snapshot.ContentHash)
		}
		if snapshot.RunID != run.RunID || snapshot.SeriesID != run.SeriesID {
			t.Errorf("snapshot identity = %q/%q, want run %q", snapshot.RunID, snapshot.SeriesID, run.RunID)
		}

		loaded, err := store.LoadLatestCheckpoint(ctx, run.RunID)
		if err != nil {
			t.Fatalf("LoadLatestCheckpoint failed: %v", err)
		}
		if loaded.Sequence != 1 || loaded.ContentHash != snapshot.ContentHash {
			t.Errorf("loaded snapshot = %+v, want sequence 1 with hash %q", loaded, snapshot.ContentHash)
		}
		if loaded.Checkpoint.VerifiedFacts[0].EvidenceIDs[0] != "ev-1" {
			t.Errorf("loaded verified fact lost evidence ids: %+v", loaded.Checkpoint.VerifiedFacts)
		}
	})

	t.Run("second append bumps the optimistic sequence", func(t *testing.T) {
		snapshot, err := store.AppendCheckpoint(ctx, run.RunID, checkpointForRun(run))
		if err != nil {
			t.Fatalf("second AppendCheckpoint failed: %v", err)
		}
		if snapshot.Sequence != 2 {
			t.Errorf("second snapshot sequence = %d, want 2", snapshot.Sequence)
		}
		loaded, err := store.LoadLatestCheckpoint(ctx, run.RunID)
		if err != nil {
			t.Fatalf("LoadLatestCheckpoint failed: %v", err)
		}
		if loaded.Sequence != 2 || loaded.Phase != "diagnosing" {
			t.Errorf("loaded latest = sequence %d phase %q, want sequence 2", loaded.Sequence, loaded.Phase)
		}
	})

	t.Run("list events returns bounded audit history", func(t *testing.T) {
		events, err := store.ListCheckpointEvents(ctx, run.RunID, 10)
		if err != nil {
			t.Fatalf("ListCheckpointEvents failed: %v", err)
		}
		if len(events) != 2 {
			t.Fatalf("events = %d, want 2", len(events))
		}
		if events[0].Sequence != 2 || events[1].Sequence != 1 {
			t.Errorf("events not ordered by descending sequence: %d, %d", events[0].Sequence, events[1].Sequence)
		}
		if events[0].TriggerReason != domain.CheckpointReasonThreshold || events[0].Checkpoint.Sequence != 2 {
			t.Errorf("event 2 projection is incomplete: %+v", events[0])
		}
	})
}

func TestCheckpointStore_AppendRejectsWrongIdentityAndContext(t *testing.T) {
	pool := setupTestDB(t)
	t.Cleanup(pool.Close)

	store := mustCheckpointStore(t, pool)
	ctx := context.Background()
	runA := mustCreateRun(t, pool, mustIncident(t, pool), 1, "abc123")
	runB := mustCreateRun(t, pool, mustIncident(t, pool), 1, "def456")

	t.Run("wrong series identity is rejected", func(t *testing.T) {
		checkpoint := checkpointForRun(runA)
		checkpoint.SeriesID = runB.SeriesID
		if _, err := store.AppendCheckpoint(ctx, runA.RunID, checkpoint); !errors.Is(err, domain.ErrCheckpointInvalid) {
			t.Fatalf("AppendCheckpoint error = %v, want ErrCheckpointInvalid", err)
		}
	})

	t.Run("wrong run identity is rejected", func(t *testing.T) {
		checkpoint := checkpointForRun(runA)
		checkpoint.RunID = runB.RunID
		if _, err := store.AppendCheckpoint(ctx, runA.RunID, checkpoint); !errors.Is(err, domain.ErrCheckpointInvalid) {
			t.Fatalf("AppendCheckpoint error = %v, want ErrCheckpointInvalid", err)
		}
	})

	t.Run("wrong context version is rejected", func(t *testing.T) {
		checkpoint := checkpointForRun(runA)
		checkpoint.ContextVersion = runA.ContextVersion + 1
		if _, err := store.AppendCheckpoint(ctx, runA.RunID, checkpoint); !errors.Is(err, domain.ErrCheckpointInvalid) {
			t.Fatalf("AppendCheckpoint error = %v, want ErrCheckpointInvalid", err)
		}
	})

	t.Run("rejected appends leave no partial state", func(t *testing.T) {
		var events, snapshots int
		if err := pool.QueryRow(ctx,
			`SELECT (SELECT count(*) FROM remediation_checkpoint_event WHERE run_id = $1),
			        (SELECT count(*) FROM remediation_working_memory WHERE run_id = $1)`,
			runA.RunID).Scan(&events, &snapshots); err != nil {
			t.Fatalf("count checkpoint rows: %v", err)
		}
		if events != 0 || snapshots != 0 {
			t.Fatalf("partial state after rejected appends: events=%d snapshots=%d", events, snapshots)
		}
	})

	t.Run("unknown run is rejected", func(t *testing.T) {
		checkpoint := checkpointForRun(runB)
		if _, err := store.AppendCheckpoint(ctx, "00000000-0000-7000-8000-000000000000", checkpoint); err == nil {
			t.Fatal("AppendCheckpoint error = nil, want rejection")
		}
	})
}

func TestCheckpointStore_LoadRejectsCorruptState(t *testing.T) {
	pool := setupTestDB(t)
	t.Cleanup(pool.Close)

	store := mustCheckpointStore(t, pool)
	ctx := context.Background()
	incidentID := mustIncident(t, pool)
	run := mustCreateRun(t, pool, incidentID, 1, "abc123")

	t.Run("missing snapshot returns ErrCheckpointNotFound", func(t *testing.T) {
		if _, err := store.LoadLatestCheckpoint(ctx, run.RunID); !errors.Is(err, domain.ErrCheckpointNotFound) {
			t.Fatalf("LoadLatestCheckpoint error = %v, want ErrCheckpointNotFound", err)
		}
	})

	t.Run("hash mismatch is rejected", func(t *testing.T) {
		if _, err := store.AppendCheckpoint(ctx, run.RunID, checkpointForRun(run)); err != nil {
			t.Fatalf("AppendCheckpoint failed: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`UPDATE remediation_working_memory SET content_hash = $1 WHERE run_id = $2`,
			strings.Repeat("0", 64), run.RunID); err != nil {
			t.Fatalf("tamper snapshot hash: %v", err)
		}
		if _, err := store.LoadLatestCheckpoint(ctx, run.RunID); !errors.Is(err, domain.ErrCheckpointCorrupt) {
			t.Fatalf("LoadLatestCheckpoint error = %v, want ErrCheckpointCorrupt", err)
		}
	})

	t.Run("payload run identity mismatch is rejected", func(t *testing.T) {
		other := mustCreateRun(t, pool, mustIncident(t, pool), 1, "other")
		forged := checkpointForRun(other)
		forged.Sequence = 3
		payload, err := forged.CanonicalEncode()
		if err != nil {
			t.Fatalf("forged CanonicalEncode: %v", err)
		}
		sum := sha256.Sum256(payload)
		if _, err := pool.Exec(ctx,
			`INSERT INTO remediation_checkpoint_event (run_id, sequence, trigger_reason, payload, content_hash)
			 VALUES ($1, 3, 'recovery', $2, $3)`,
			run.RunID, payload, hex.EncodeToString(sum[:])); err != nil {
			t.Fatalf("insert forged event: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO remediation_working_memory (run_id, sequence, context_version, phase, content_hash)
			 VALUES ($1, 3, $2, 'diagnosing', $3)`,
			run.RunID, run.ContextVersion, hex.EncodeToString(sum[:])); err != nil {
			t.Fatalf("insert forged snapshot: %v", err)
		}
		if _, err := store.LoadLatestCheckpoint(ctx, run.RunID); !errors.Is(err, domain.ErrCheckpointCorrupt) {
			t.Fatalf("LoadLatestCheckpoint error = %v, want ErrCheckpointCorrupt", err)
		}
		// audit 列表也必须对 payload 身份不一致 fail closed。
		if _, err := store.ListCheckpointEvents(ctx, run.RunID, 10); !errors.Is(err, domain.ErrCheckpointCorrupt) {
			t.Fatalf("ListCheckpointEvents error = %v, want ErrCheckpointCorrupt", err)
		}
	})
}

func TestCheckpointStore_DBConstraints(t *testing.T) {
	pool := setupTestDB(t)
	t.Cleanup(pool.Close)

	store := mustCheckpointStore(t, pool)
	ctx := context.Background()
	incidentID := mustIncident(t, pool)
	run := mustCreateRun(t, pool, incidentID, 1, "abc123")

	t.Run("snapshot sequence without a backing event is rejected", func(t *testing.T) {
		_, err := pool.Exec(ctx,
			`INSERT INTO remediation_working_memory (run_id, sequence, context_version, observed_run_version, phase, content_hash)
			 VALUES ($1, 99, 0, $2, 'diagnosing', $3)`,
			run.RunID, run.Version, strings.Repeat("1", 64))
		if err == nil {
			t.Fatal("unbacked snapshot insert error = nil, want foreign key violation")
		}
		if !strings.Contains(err.Error(), "remediation_working_memory_event_backing") {
			t.Errorf("unbacked snapshot error = %v, want event_backing constraint", err)
		}
	})

	t.Run("checkpoint events are immutable", func(t *testing.T) {
		if _, err := store.AppendCheckpoint(ctx, run.RunID, checkpointForRun(run)); err != nil {
			t.Fatalf("AppendCheckpoint failed: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`UPDATE remediation_checkpoint_event SET content_hash = $1 WHERE run_id = $2`,
			strings.Repeat("0", 64), run.RunID); err == nil {
			t.Fatal("event update error = nil, want immutability rejection")
		}
		// DELETE 仍允许：run 级联清理依赖它，immutability 只禁止编辑。
		if _, err := pool.Exec(ctx,
			`DELETE FROM remediation_checkpoint_event WHERE run_id = $1`, run.RunID); err != nil {
			t.Fatalf("event delete failed: %v", err)
		}
		if _, err := store.LoadLatestCheckpoint(ctx, run.RunID); !errors.Is(err, domain.ErrCheckpointNotFound) {
			t.Fatalf("LoadLatestCheckpoint after delete error = %v, want ErrCheckpointNotFound", err)
		}
	})

	t.Run("duplicate event sequence is rejected", func(t *testing.T) {
		checkpoint := checkpointForRun(run)
		payload, err := checkpoint.WithSequence(7)
		if err != nil {
			t.Fatalf("WithSequence: %v", err)
		}
		encoded, err := payload.CanonicalEncode()
		if err != nil {
			t.Fatalf("CanonicalEncode: %v", err)
		}
		sum := sha256.Sum256(encoded)
		for i := 0; i < 2; i++ {
			_, err := pool.Exec(ctx,
				`INSERT INTO remediation_checkpoint_event (run_id, sequence, trigger_reason, payload, content_hash)
				 VALUES ($1, 7, 'threshold', $2, $3)`,
				run.RunID, encoded, hex.EncodeToString(sum[:]))
			if i == 0 && err != nil {
				t.Fatalf("first duplicate-sequence insert failed: %v", err)
			}
			if i == 1 && err == nil {
				t.Fatal("duplicate sequence insert error = nil, want unique violation")
			}
		}
	})
}

func TestCheckpointStore_LegacyRunModeDefault(t *testing.T) {
	pool := setupTestDB(t)
	t.Cleanup(pool.Close)

	store := mustStore(t, pool)
	ctx := context.Background()
	incidentID := mustIncident(t, pool)
	run := mustCreateRun(t, pool, incidentID, 1, "abc123")

	if run.AgentLoopMode != domain.AgentLoopModeLegacy {
		t.Errorf("new run mode = %q, want legacy default", run.AgentLoopMode)
	}

	var mode string
	if err := pool.QueryRow(ctx,
		`SELECT agent_loop_mode FROM remediation_run WHERE id = $1`, run.RunID).Scan(&mode); err != nil {
		t.Fatalf("read agent_loop_mode: %v", err)
	}
	if mode != "legacy" {
		t.Errorf("stored agent_loop_mode = %q, want legacy", mode)
	}

	// 既有读取路径（Get 聚合）在新增列后仍然工作，且旧行保持可读。
	aggregate, err := store.Get(ctx, run.RunID)
	if err != nil {
		t.Fatalf("Get after additive migration failed: %v", err)
	}
	if aggregate.Run.AgentLoopMode != domain.AgentLoopModeLegacy {
		t.Errorf("aggregate mode = %q, want legacy", aggregate.Run.AgentLoopMode)
	}

	// 手动把行切到 resilient_v1 后，映射必须反映快照模式。
	if _, err := pool.Exec(ctx,
		`UPDATE remediation_run SET agent_loop_mode = 'resilient_v1' WHERE id = $1`, run.RunID); err != nil {
		t.Fatalf("update agent_loop_mode: %v", err)
	}
	aggregate, err = store.Get(ctx, run.RunID)
	if err != nil {
		t.Fatalf("Get after mode update failed: %v", err)
	}
	if aggregate.Run.AgentLoopMode != domain.AgentLoopModeResilientV1 {
		t.Errorf("aggregate mode = %q, want resilient_v1", aggregate.Run.AgentLoopMode)
	}
}

func TestRunModeSnapshotsProjectPolicyAndContinuationInherits(t *testing.T) {
	pool := setupTestDB(t)
	t.Cleanup(pool.Close)
	ctx := context.Background()
	store := mustStore(t, pool)
	incidentID := mustIncident(t, pool)

	var projectID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT project_id FROM incidents WHERE id = $1`, incidentID).Scan(&projectID); err != nil {
		t.Fatalf("read project: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE projects SET agent_loop_mode = 'resilient_v1', agent_loop_policy_version = agent_loop_policy_version + 1 WHERE id = $1`, projectID); err != nil {
		t.Fatalf("enable resilient policy: %v", err)
	}
	root := mustCreateRun(t, pool, incidentID, 1, "abc123")
	if root.AgentLoopMode != domain.AgentLoopModeResilientV1 || root.AgentLoopPolicyVersion != 2 {
		t.Fatalf("root policy snapshot = %q/v%d, want resilient_v1/v2", root.AgentLoopMode, root.AgentLoopPolicyVersion)
	}
	if err := store.Transition(ctx, root.RunID, domain.RunStateQueued, domain.RunStateFailed, domain.Effect{TerminalReason: "retry", Retryable: true}); err != nil {
		t.Fatalf("finish root: %v", err)
	}
	rootAggregate, err := store.Get(ctx, root.RunID)
	if err != nil {
		t.Fatal(err)
	}
	root = rootAggregate.Run
	if _, err := pool.Exec(ctx, `UPDATE projects SET agent_loop_mode = 'legacy', agent_loop_policy_version = agent_loop_policy_version + 1 WHERE id = $1`, projectID); err != nil {
		t.Fatalf("rollback project policy: %v", err)
	}
	setIncidentContextVersion(t, pool, root.IncidentID, 2)
	child, err := store.CreateNextAttempt(ctx, nextAttemptInput(root, domain.TriggerReasonManualContinue, 2))
	if err != nil {
		t.Fatalf("create continuation: %v", err)
	}
	if child.AgentLoopMode != domain.AgentLoopModeResilientV1 || child.AgentLoopPolicyVersion != 2 {
		t.Fatalf("continuation policy = %q/v%d, want inherited resilient_v1/v2", child.AgentLoopMode, child.AgentLoopPolicyVersion)
	}

	newRoot := mustCreateRun(t, pool, incidentID, 2, "def456")
	if newRoot.AgentLoopMode != domain.AgentLoopModeLegacy || newRoot.AgentLoopPolicyVersion != 3 {
		t.Fatalf("new root policy = %q/v%d, want current legacy/v3", newRoot.AgentLoopMode, newRoot.AgentLoopPolicyVersion)
	}
}

func TestCheckpointStore_ReconcilesBothTransitionCrashWindows(t *testing.T) {
	pool := setupTestDB(t)
	t.Cleanup(pool.Close)
	ctx := context.Background()
	store := mustStore(t, pool)
	checkpointStore := mustCheckpointStore(t, pool)
	incidentID := mustIncident(t, pool)
	run := mustCreateRun(t, pool, incidentID, 1, "abc123")
	if _, err := pool.Exec(ctx, `UPDATE remediation_run SET state = 'diagnosing', version = version + 1 WHERE id = $1`, run.RunID); err != nil {
		t.Fatal(err)
	}
	aggregate, err := store.Get(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	run = aggregate.Run
	if _, err := checkpointStore.AppendCheckpoint(ctx, run.RunID, checkpointForRun(run)); err != nil {
		t.Fatalf("append before-transition checkpoint: %v", err)
	}
	before, err := checkpointStore.LoadLatestCheckpoint(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if before.NeedsRebuild || before.DurableRunVersion != before.ObservedRunVersion {
		t.Fatalf("before-transition reconciliation = %+v, want exact resume", before)
	}

	if _, err := pool.Exec(ctx, `UPDATE remediation_run SET state = 'planning', version = version + 1 WHERE id = $1`, run.RunID); err != nil {
		t.Fatal(err)
	}
	after, err := checkpointStore.LoadLatestCheckpoint(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.NeedsRebuild || after.DurableRunState != domain.RunStatePlanning || after.DurableRunVersion != after.ObservedRunVersion+1 {
		t.Fatalf("after-transition reconciliation = %+v, want stale projection rebuild", after)
	}

	future := checkpointForRun(run)
	future.ObservedRunVersion = after.DurableRunVersion + 1
	if _, err := checkpointStore.AppendCheckpoint(ctx, run.RunID, future); !errors.Is(err, domain.ErrCheckpointInvalid) {
		t.Fatalf("future checkpoint append error = %v, want ErrCheckpointInvalid", err)
	}
}

// 确保 JSON 载荷确实以 jsonb 规范形式落库并可读回（hash 校验覆盖 canonical 编码）。
func TestCheckpointStore_PayloadJSONValid(t *testing.T) {
	pool := setupTestDB(t)
	t.Cleanup(pool.Close)

	store := mustCheckpointStore(t, pool)
	ctx := context.Background()
	incidentID := mustIncident(t, pool)
	run := mustCreateRun(t, pool, incidentID, 1, "abc123")

	if _, err := store.AppendCheckpoint(ctx, run.RunID, checkpointForRun(run)); err != nil {
		t.Fatalf("AppendCheckpoint failed: %v", err)
	}
	var payload []byte
	if err := pool.QueryRow(ctx,
		`SELECT payload FROM remediation_checkpoint_event WHERE run_id = $1`, run.RunID).Scan(&payload); err != nil {
		t.Fatalf("read event payload: %v", err)
	}
	if !json.Valid(payload) {
		t.Fatal("stored payload is not valid JSON")
	}
	var object map[string]any
	if err := json.Unmarshal(payload, &object); err != nil {
		t.Fatalf("decode stored payload: %v", err)
	}
	if object["schemaVersion"] != "v1" || object["runId"] != run.RunID {
		t.Errorf("stored payload identity = %v, want schema v1 run %q", object["runId"], run.RunID)
	}
}
