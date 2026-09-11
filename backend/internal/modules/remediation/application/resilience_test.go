package application_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"mendry/backend/internal/modules/remediation/application"
	"mendry/backend/internal/modules/remediation/domain"
)

// fakeCheckpointStore 是内存版 domain.CheckpointStore：按 runID 记录 append
// 与 latest snapshot，可按需注入 append/load 错误，供 resilient_v1 的
// coordinator 单测使用。可选 runStore 会校验 observed version，模拟 Postgres
// checkpoint adapter 的 exact-version 合同；hash/sequence 仍由 adapter 测试负责。
type fakeCheckpointStore struct {
	appends     []domain.WorkingMemoryCheckpointV1
	byRun       map[string]domain.CheckpointSnapshot
	appendErr   error
	appendErrAt int
	loadErr     error
	// runStore 启用 production-like observed version 校验；nil 时供纯
	// reconstruction/load 测试使用。
	runStore *fakeRunStore
}

func (f *fakeCheckpointStore) AppendCheckpoint(_ context.Context, runID string, checkpoint domain.WorkingMemoryCheckpointV1) (domain.CheckpointSnapshot, error) {
	if f.runStore != nil && f.runStore.created != nil && checkpoint.ObservedRunVersion != f.runStore.created.Version {
		return domain.CheckpointSnapshot{}, fmt.Errorf("%w: observed run version %d does not match durable version %d",
			domain.ErrCheckpointInvalid, checkpoint.ObservedRunVersion, f.runStore.created.Version)
	}
	if f.appendErr != nil && (f.appendErrAt <= 0 || len(f.appends)+1 == f.appendErrAt) {
		return domain.CheckpointSnapshot{}, f.appendErr
	}
	checkpoint.Sequence = int64(len(f.appends)) + 1
	f.appends = append(f.appends, checkpoint)
	if f.byRun == nil {
		f.byRun = make(map[string]domain.CheckpointSnapshot)
	}
	snapshot := domain.CheckpointSnapshot{
		RunID: runID, SeriesID: checkpoint.SeriesID, ContextVersion: checkpoint.ContextVersion,
		ObservedRunVersion: checkpoint.ObservedRunVersion, Sequence: checkpoint.Sequence,
		Phase: checkpoint.Phase, Checkpoint: checkpoint,
	}
	f.byRun[runID] = snapshot
	return snapshot, nil
}

func (f *fakeCheckpointStore) LoadLatestCheckpoint(_ context.Context, runID string) (domain.CheckpointSnapshot, error) {
	if f.loadErr != nil {
		return domain.CheckpointSnapshot{}, f.loadErr
	}
	snapshot, ok := f.byRun[runID]
	if !ok {
		return domain.CheckpointSnapshot{}, domain.ErrCheckpointNotFound
	}
	return snapshot, nil
}

func (f *fakeCheckpointStore) ListCheckpointEvents(context.Context, string, int) ([]domain.CheckpointEvent, error) {
	return nil, nil
}

func (f *fakeCheckpointStore) countReason(reason string) int {
	count := 0
	for _, checkpoint := range f.appends {
		if checkpoint.Reason == reason {
			count++
		}
	}
	return count
}

// heavyEvidencePort 返回 ~437 字节的 evidence 页面，让两次 evidence.search
// 足以越过 1000 字节 hard ceiling 下的 soft allocation（150）与 unreserved
// pool（650），依次产生 soft_crossed 与 reserve_touched 信号。
type heavyEvidencePort struct {
	fakeEvidencePort
}

func (f *heavyEvidencePort) Search(_ context.Context, scope domain.EvidenceScope, _ domain.LogQuery) (domain.EvidencePage, error) {
	f.calls++
	f.lastScope = scope
	line := domain.EvidenceLine{
		EvidenceID: strings.Repeat("x", 128), Message: strings.Repeat("y", 300),
		Level: "error", Host: "host",
	}
	return domain.EvidencePage{Lines: []domain.EvidenceLine{line}}, nil
}

// heavyRepoPort 返回恰好 153 字节的文件内容：配合 MaxRepositoryBytes=1003
// 的 hard ceiling（soft=150，各 reserve 合计 850，unreserved pool=3），第一次
// read_file 越过 soft 但仍在 pool 内（soft_crossed），第二次越过 pool
// （reserve_touched），两次消耗都不触碰 hard ceiling。repository 工具在
// 任何 source catalog 中都广告，不依赖 source identity。
type heavyRepoPort struct {
	fakeRepoPort
}

func (f *heavyRepoPort) ReadFile(_ context.Context, ref domain.RepoRef, path string, _ domain.ReadOptions) (domain.FileContent, error) {
	f.calls++
	f.lastRef = ref
	return domain.FileContent{Path: path, Content: []byte(strings.Repeat("z", 153))}, nil
}

// sourceWiredCoordinator 构造带 source identity 的 runtime coordinator，使
// evidence.search/context/read 在 diagnosing catalog 中广告。
func sourceWiredCoordinator(
	store *fakeRunStore,
	repo domain.RepositoryReadPort,
	ev domain.EvidenceLogPort,
	model domain.LLMProviderPort,
) *application.RemediationCoordinator {
	return application.NewRemediationCoordinatorWithRuntime(
		store, repo, ev, model,
		wiringLookup{identity: application.IncidentIdentity{
			ID: testIncidentUUID, ProjectID: "project-1", EnvironmentID: "environment-1", SourceID: "source-1",
			Priority: "P2", DeployedCommit: "abc123", LifecycleGeneration: 1,
		}},
		staticRemote("https://git.example.invalid/app.git"), store, store,
	)
}

func requestEvidenceReadEnvelope(evidenceID string) string {
	return fmt.Sprintf(`{"schemaVersion":"v1","kind":"requestTool","requestTool":{`+
		`"toolName":"evidence.read","parameters":{"evidenceId":%q}}}`, evidenceID)
}

// TestResilient_ForcedCheckpointsAtPhaseBoundaryAndBeforeTerminal 证明
// resilient_v1 run 在 preparing_context→diagnosing 边界与终态 transition 前
// 各强制 append 一次 checkpoint，且 checkpoint 携带 run/series/context 身份、
// objective 与 budget recovery 快照。
func TestResilient_ForcedCheckpointsAtPhaseBoundaryAndBeforeTerminal(t *testing.T) {
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	checkpoints := &fakeCheckpointStore{runStore: store}
	// 终态诊断用 unsafe_to_automate：它是 D6 明确的 policy blocker，保留直接
	// 终态语义；insufficient_evidence 在 resilient_v1 下会先要求 exhaustion
	// proof，不属于本测试要验证的 checkpoint 触发集。
	model := &scriptedModel{responses: []string{diagnosisEnvelope("unsafe_to_automate")}}
	coord := newCoordinator(store, &fakeRepoPort{}, &fakeEvidencePort{}, model)
	coord.SetCheckpointStore(checkpoints)

	run, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123", ContextVersion: 1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.state != domain.RunStateBlockedManualReview {
		t.Fatalf("final state = %s, want blocked_manual_review", store.state)
	}
	if len(checkpoints.appends) != 2 {
		t.Fatalf("checkpoints appended = %d, want 2 (phase boundary + before terminal)", len(checkpoints.appends))
	}
	first := checkpoints.appends[0]
	if first.RunID != run.RunID || first.SeriesID != run.SeriesID || first.ContextVersion != 1 {
		t.Errorf("checkpoint identity = run %q series %q context %d, want run %q series %q context 1",
			first.RunID, first.SeriesID, first.ContextVersion, run.RunID, run.SeriesID)
	}
	if first.Phase != string(domain.RunStateDiagnosing) || first.Reason != domain.CheckpointReasonPhaseBoundary {
		t.Errorf("first checkpoint phase/reason = %q/%q, want diagnosing/phase_boundary", first.Phase, first.Reason)
	}
	if first.ObservedRunVersion < 1 {
		t.Errorf("first checkpoint observed run version = %d, want positive", first.ObservedRunVersion)
	}
	if strings.TrimSpace(first.Objective.Goal) == "" || len(first.Objective.CompletionCriteria) == 0 {
		t.Errorf("checkpoint objective is incomplete: %#v", first.Objective)
	}
	last := checkpoints.appends[len(checkpoints.appends)-1]
	if last.Phase != string(domain.RunStateDiagnosing) || last.Reason != domain.CheckpointReasonPhaseBoundary {
		t.Errorf("terminal checkpoint phase/reason = %q/%q, want diagnosing/phase_boundary", last.Phase, last.Reason)
	}
	// 两次 checkpoint 之间没有其他 transition：终态前的 checkpoint 看到与边界
	// checkpoint 相同的 durable run version。
	if last.ObservedRunVersion != first.ObservedRunVersion {
		t.Errorf("terminal checkpoint observed version = %d, want %d (no transition between checkpoints)",
			last.ObservedRunVersion, first.ObservedRunVersion)
	}
	if err := last.Budget.Validate(); err != nil {
		t.Errorf("checkpoint budget recovery snapshot is invalid: %v", err)
	}
	if last.Budget.CurrentPhase != domain.RunStateDiagnosing {
		t.Errorf("checkpoint budget current phase = %q, want diagnosing", last.Budget.CurrentPhase)
	}
}

// TestResilient_CodeFixableCheckpointsAroundPlanning 证明 resilient_v1 的
// code_fixable 路径在进入 planning 边界前（phase diagnosing）与
// diagnosis_ready_for_review 终态前（phase planning）各强制 checkpoint，
// 覆盖跨 phase 的 AC7 重启种子。
func TestResilient_CodeFixableCheckpointsAroundPlanning(t *testing.T) {
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	checkpoints := &fakeCheckpointStore{}
	model := &scriptedModel{responses: []string{
		diagnosisEnvelope("code_fixable"),
		planEnvelope(),
	}}
	coord := newCoordinator(store, &fakeRepoPort{}, &fakeEvidencePort{}, model)
	coord.SetCheckpointStore(checkpoints)

	if _, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.state != domain.RunStateDiagnosisReadyForReview {
		t.Fatalf("final state = %s, want diagnosis_ready_for_review", store.state)
	}
	if len(checkpoints.appends) != 4 {
		t.Fatalf("checkpoints appended = %d, want 4 (diagnosing entry + before/after planning + before terminal)", len(checkpoints.appends))
	}
	if checkpoints.appends[1].Phase != string(domain.RunStateDiagnosing) ||
		checkpoints.appends[1].Reason != domain.CheckpointReasonPhaseBoundary {
		t.Errorf("before-plan checkpoint = phase %q reason %q, want diagnosing/phase_boundary",
			checkpoints.appends[1].Phase, checkpoints.appends[1].Reason)
	}
	last := checkpoints.appends[len(checkpoints.appends)-1]
	if last.Phase != string(domain.RunStatePlanning) || last.Reason != domain.CheckpointReasonPhaseBoundary {
		t.Errorf("terminal checkpoint = phase %q reason %q, want planning/phase_boundary", last.Phase, last.Reason)
	}
	if last.Budget.CurrentPhase != domain.RunStatePlanning {
		t.Errorf("terminal checkpoint budget current phase = %q, want planning", last.Budget.CurrentPhase)
	}
}

// TestResilient_EvidenceCorrectionRecordsRecoveryCheckpoint 证明 resilient_v1
// 下，evidence_correction challenge 除回喂模型外还会强制一次 recovery
// checkpoint，并在 checkpoint 的 recoveries 里留下 kind/action/outcomeRef
// 定位记录（D2/D4）；run 修正后正常到达 diagnosis_ready_for_review。
func TestResilient_EvidenceCorrectionRecordsRecoveryCheckpoint(t *testing.T) {
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	checkpoints := &fakeCheckpointStore{}
	model := &scriptedModel{responses: []string{
		mismatchedClassificationEnvelope("code_fixable"),
		diagnosisEnvelope("code_fixable"),
		planEnvelope(),
	}}
	coord := newCoordinator(store, &fakeRepoPort{}, &fakeEvidencePort{}, model)
	coord.SetCheckpointStore(checkpoints)

	run, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.State != domain.RunStateDiagnosisReadyForReview || store.state != domain.RunStateDiagnosisReadyForReview {
		t.Fatalf("final state = %s/%s, want diagnosis_ready_for_review", run.State, store.state)
	}
	if checkpoints.countReason(domain.CheckpointReasonRecovery) != 1 {
		t.Fatalf("recovery checkpoints = %d, want 1", checkpoints.countReason(domain.CheckpointReasonRecovery))
	}
	var recorded domain.CheckpointRecovery
	for _, checkpoint := range checkpoints.appends {
		if checkpoint.Reason != domain.CheckpointReasonRecovery {
			continue
		}
		if checkpoint.Phase != string(domain.RunStateDiagnosing) || checkpoint.RunID != run.RunID {
			t.Fatalf("recovery checkpoint identity = phase %q run %q", checkpoint.Phase, checkpoint.RunID)
		}
		for _, recovery := range checkpoint.Recoveries {
			if recovery.Kind == string(domain.RecoveryChallengeKindEvidenceCorrection) {
				recorded = recovery
			}
		}
	}
	if recorded.Kind != string(domain.RecoveryChallengeKindEvidenceCorrection) ||
		recorded.Action != "correct_citation" || !strings.Contains(recorded.OutcomeRef, "citation_classification_mismatch") {
		t.Fatalf("checkpoint recovery record = %#v", recorded)
	}
	if !strings.Contains(model.turns[1].UserMessage, `"kind":"evidence_correction"`) {
		t.Fatalf("second turn lacks evidence correction challenge: %s", model.turns[1].UserMessage)
	}
	if len(store.decisions) != 1 {
		t.Fatalf("decisions = %d, want 1 (only the corrected diagnosis)", len(store.decisions))
	}
}

// TestResilient_SoftBudgetSignalsFeedChallengeAndCheckpoint 证明 allocator 的
// soft_crossed 与 reserve_touched 信号各自向 conversation 回喂一次 recoverable
// budget challenge 并强制 recovery checkpoint，而 run 不会进入 budget_exhausted
// （只有 hard ceiling 才会；AC10）。
func TestResilient_SoftBudgetSignalsFeedChallengeAndCheckpoint(t *testing.T) {
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	checkpoints := &fakeCheckpointStore{}
	limits := domain.BudgetLimits{MaxRepositoryBytes: 1003}
	model := &scriptedModel{responses: []string{
		requestToolEnvelope(application.ToolRepoReadFile, "main.go"),
		requestToolEnvelope(application.ToolRepoReadFile, "main.go"),
		// 终态诊断用 unsafe_to_automate（policy blocker，保留直接终态语义）；
		// resilient_v1 的 insufficient_evidence 会先要求 exhaustion proof，
		// 不属于本测试要验证的 soft-budget 信号集。
		diagnosisEnvelope("unsafe_to_automate"),
	}}
	coord := newCoordinatorWithBudget(store, &heavyRepoPort{}, &fakeEvidencePort{}, model, limits)
	coord.SetCheckpointStore(checkpoints)

	_, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.state != domain.RunStateBlockedManualReview {
		t.Fatalf("final state = %s, want blocked_manual_review (soft budget must not hard-terminate)", store.state)
	}
	if len(model.turns) != 3 {
		t.Fatalf("model turns = %d, want 3", len(model.turns))
	}
	if !strings.Contains(model.turns[1].UserMessage, "soft_budget_crossed") {
		t.Errorf("turn 2 lacks soft_budget_crossed challenge: %s", model.turns[1].UserMessage)
	}
	if !strings.Contains(model.turns[1].UserMessage, `"kind":"budget"`) || !strings.Contains(model.turns[1].UserMessage, `"severity":"recoverable"`) {
		t.Errorf("turn 2 challenge is not a bounded recoverable budget envelope: %s", model.turns[1].UserMessage)
	}
	if !strings.Contains(model.turns[2].UserMessage, "soft_budget_reserve_touched") {
		t.Errorf("turn 3 lacks soft_budget_reserve_touched challenge: %s", model.turns[2].UserMessage)
	}
	if checkpoints.countReason(domain.CheckpointReasonRecovery) != 2 {
		t.Errorf("recovery checkpoints = %d, want 2 (one per strengthened signal)", checkpoints.countReason(domain.CheckpointReasonRecovery))
	}
	if checkpoints.countReason(domain.CheckpointReasonPhaseBoundary) != 2 {
		t.Errorf("phase boundary checkpoints = %d, want 2 (diagnosing entry + before terminal)", checkpoints.countReason(domain.CheckpointReasonPhaseBoundary))
	}
	last := checkpoints.appends[len(checkpoints.appends)-1]
	if len(last.Recoveries) != 2 {
		t.Errorf("checkpoint recoveries = %d, want 2", len(last.Recoveries))
	}
}

// TestResilient_ContinuationReconstructsFromDurableCheckpoint 证明 resilient_v1
// diagnosis continuation 从 predecessor 的 durable checkpoint 重建 bounded
// working memory（objective、evidence IDs、budget），并用它取代 runtime-only
// evidence 块，同时保留主 brief；run 正常继续（AC7 种子）。
func TestResilient_ContinuationReconstructsFromDurableCheckpoint(t *testing.T) {
	predecessor := eligibleAutomaticAggregate(domain.RunStateFailed, 1, true)
	predecessor.Decisions = []domain.Decision{{Fixability: domain.FixabilityInsufficientEvidence}}
	store := &continuationStore{
		fakeRunStore: newFakeRunStore(),
		predecessor:  predecessor,
		childMode:    domain.AgentLoopModeResilientV1,
		continuationEvidence: []domain.StoredEvidence{{
			EvidenceID: "ev-runtime-1", Provider: "ssh", EvidenceKind: "runtime",
			Classification: domain.EvidenceCorrelatedSupport, Payload: []byte(`{"stdout":"prior runtime"}`),
		}},
		continuationEvidenceIndex: []domain.EvidenceIndexEntry{{
			EvidenceID: "ev-provider-1", Kind: "provider_detail", Provider: "tencent_cls",
			Classification: domain.EvidenceDirectFault, SourceAttempt: 1, ContentHash: strings.Repeat("c", 64),
		}},
	}
	checkpoints := &fakeCheckpointStore{}
	plan, planErr := application.NewBudgetPlan(application.DefaultBudgetLimits(), nil, nil, domain.RecoveryReserve{})
	if planErr != nil {
		t.Fatalf("NewBudgetPlan: %v", planErr)
	}
	checkpoints.byRun = map[string]domain.CheckpointSnapshot{
		"run-previous": {
			RunID: "run-previous", SeriesID: "series-1", ContextVersion: 1,
			ObservedRunVersion: 7, Sequence: 3, Phase: string(domain.RunStateDiagnosing),
			Checkpoint: domain.WorkingMemoryCheckpointV1{
				SchemaVersion: domain.CheckpointSchemaVersionV1, Sequence: 3,
				RunID: "run-previous", SeriesID: "series-1", ContextVersion: 1, ObservedRunVersion: 7,
				Phase: string(domain.RunStateDiagnosing),
				Objective: domain.CheckpointObjective{
					Goal:               "Diagnose the incident from trusted evidence and repository inspection.",
					CompletionCriteria: []string{"diagnosis envelope accepted"},
				},
				NextActions: []string{"inspect repository locator"},
				Budget: domain.BudgetPlanRecoveryV1{
					SchemaVersion: domain.BudgetPlanSchemaVersionV1,
					Plan:          plan,
					CurrentPhase:  domain.RunStateDiagnosing,
					Frontier:      0,
				},
				Reason: domain.CheckpointReasonPhaseBoundary,
			},
		},
	}
	model := &scriptedModel{responses: []string{diagnosisEnvelope("unsafe_to_automate")}}
	coord := application.NewRemediationCoordinatorWithReview(store, &fakeRepoPort{}, &fakeEvidencePort{}, model, nil, store, store)
	coord.SetCheckpointStore(checkpoints)

	run, err := coord.Continue(context.Background(), domain.NextAttempt{
		ContinuationOfRunID:     "run-previous",
		SeriesID:                "series-1",
		IncidentID:              testIncidentUUID,
		LifecycleGeneration:     1,
		DeployedCommit:          "abc123",
		ContextVersion:          1,
		ExpectedPreviousVersion: 7,
		Origin:                  domain.TriggerOriginManualContinue,
		TriggerReason:           domain.TriggerOriginManualContinue,
		ContinuationReason:      "operator continue",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.state != domain.RunStateBlockedManualReview {
		t.Fatalf("final state = %s, want blocked_manual_review", store.state)
	}
	if run.RunID != "run-next" || store.continuationIndexCalls != 1 {
		t.Fatalf("child = %q, continuation index calls = %d", run.RunID, store.continuationIndexCalls)
	}
	firstTurn := model.turns[0].UserMessage
	if !strings.Contains(firstTurn, "working_memory_reconstruction") {
		t.Errorf("first turn lacks reconstruction block: %s", firstTurn)
	}
	if !strings.Contains(firstTurn, `"evidenceId":"ev-provider-1"`) {
		t.Errorf("first turn lacks store-authorized provider evidence index: %s", firstTurn)
	}
	if !strings.Contains(firstTurn, `"evidenceId":"ev-runtime-1"`) {
		t.Errorf("first turn lacks store-authorized runtime evidence index: %s", firstTurn)
	}
	if !strings.Contains(firstTurn, "inspect repository locator") {
		t.Errorf("first turn lacks checkpoint next actions: %s", firstTurn)
	}
	if !strings.Contains(firstTurn, `"unreserved"`) {
		t.Errorf("first turn lacks budget projection from the checkpoint: %s", firstTurn)
	}
	// reconstruction 保留 store 在 continuation 时重新授权的 provider/runtime
	// evidence identities，即使 predecessor checkpoint 自身没有这些条目；原始
	// prior evidence payload/index block 不重复内联。
	if strings.Contains(firstTurn, "prior_runtime_evidence") || strings.Contains(firstTurn, "prior_evidence_index") {
		t.Errorf("prior evidence blocks must be merged as reconstruction locators: %s", firstTurn)
	}
	// 主 brief（predecessor 元数据）仍然保留。
	if !strings.Contains(firstTurn, "remediation_continuation_brief") {
		t.Errorf("main continuation brief must be retained next to reconstruction: %s", firstTurn)
	}
	// child run 在边界与终态前各 append 一次 checkpoint。
	if len(checkpoints.appends) != 2 || checkpoints.appends[0].RunID != "run-next" {
		t.Errorf("child checkpoints = %d (run %q), want 2 for run-next", len(checkpoints.appends), checkpoints.appends[0].RunID)
	}
}

// TestResilient_ReconstructionFallsBackToRuntimeBrief 证明 checkpoint 身份不
// 匹配（不同 series）时重建块被丢弃，continuation 保持既有 runtime-only
// brief 路径。
func TestResilient_ReconstructionFallsBackToRuntimeBrief(t *testing.T) {
	predecessor := eligibleAutomaticAggregate(domain.RunStateFailed, 1, true)
	predecessor.Decisions = []domain.Decision{{Fixability: domain.FixabilityInsufficientEvidence}}
	store := &continuationStore{
		fakeRunStore: newFakeRunStore(),
		predecessor:  predecessor,
		childMode:    domain.AgentLoopModeResilientV1,
		continuationEvidenceIndex: []domain.EvidenceIndexEntry{{
			EvidenceID: "ev-provider-1", Kind: "provider_detail", Provider: "tencent_cls",
			Classification: domain.EvidenceDirectFault, SourceAttempt: 1, ContentHash: strings.Repeat("c", 64),
		}},
	}
	checkpoints := &fakeCheckpointStore{}
	// 身份不匹配：checkpoint 属于另一个 series。
	checkpoints.byRun = map[string]domain.CheckpointSnapshot{
		"run-previous": {
			RunID: "run-previous", SeriesID: "other-series", ContextVersion: 1,
			Checkpoint: domain.WorkingMemoryCheckpointV1{
				SchemaVersion: domain.CheckpointSchemaVersionV1, RunID: "run-previous",
				SeriesID: "other-series", ContextVersion: 1, ObservedRunVersion: 7,
				Phase: string(domain.RunStateDiagnosing),
				Objective: domain.CheckpointObjective{
					Goal: "Diagnose the incident from trusted evidence.", CompletionCriteria: []string{"accepted"},
				},
				Budget: domain.BudgetPlanRecoveryV1{SchemaVersion: domain.BudgetPlanSchemaVersionV1, Frontier: -1},
				Reason: domain.CheckpointReasonPhaseBoundary,
			},
		},
	}
	model := &scriptedModel{responses: []string{diagnosisEnvelope("unsafe_to_automate")}}
	coord := application.NewRemediationCoordinatorWithReview(store, &fakeRepoPort{}, &fakeEvidencePort{}, model, nil, store, store)
	coord.SetCheckpointStore(checkpoints)

	_, err := coord.Continue(context.Background(), domain.NextAttempt{
		ContinuationOfRunID:     "run-previous",
		SeriesID:                "series-1",
		IncidentID:              testIncidentUUID,
		LifecycleGeneration:     1,
		DeployedCommit:          "abc123",
		ContextVersion:          1,
		ExpectedPreviousVersion: 7,
		Origin:                  domain.TriggerOriginManualContinue,
		TriggerReason:           domain.TriggerOriginManualContinue,
		ContinuationReason:      "operator continue",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	firstTurn := model.turns[0].UserMessage
	if strings.Contains(firstTurn, "working_memory_reconstruction") {
		t.Errorf("mismatched checkpoint must not be reconstructed: %s", firstTurn)
	}
	if !strings.Contains(firstTurn, "prior_evidence_index") {
		t.Errorf("runtime-only brief must be kept when reconstruction is invalid: %s", firstTurn)
	}
}

// TestResilient_LegacyAndNilStoreTakeExactLegacyPath 证明 feature gate 的两条
// no-op 路径：legacy 模式即使注入 checkpoint store 也不 append/挑战；resilient
// 模式没有 store 时与 legacy 完全相同。resilient required checkpoint 失败则
// 必须进入 persistence_failure，而不是继续模型路径。
func TestResilient_LegacyAndNilStoreTakeExactLegacyPath(t *testing.T) {
	t.Run("legacy mode with store injected", func(t *testing.T) {
		store := newFakeRunStore() // mode 默认 legacy
		checkpoints := &fakeCheckpointStore{}
		model := &scriptedModel{responses: []string{insufficientWithoutCollectionEnvelope()}}
		coord := newCoordinator(store, &fakeRepoPort{}, &fakeEvidencePort{}, model)
		coord.SetCheckpointStore(checkpoints)

		if _, err := coord.Start(context.Background(), domain.NewRun{
			IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
		}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if store.state != domain.RunStateBlockedManualReview {
			t.Fatalf("final state = %s, want blocked_manual_review", store.state)
		}
		if len(checkpoints.appends) != 0 {
			t.Fatalf("legacy mode appended %d checkpoints, want 0", len(checkpoints.appends))
		}
		if strings.Contains(model.turns[0].UserMessage, "challenge") {
			t.Errorf("legacy mode must not inflate a recovery challenge: %s", model.turns[0].UserMessage)
		}
	})

	t.Run("resilient mode without checkpoint store", func(t *testing.T) {
		store := newFakeRunStore()
		store.mode = domain.AgentLoopModeResilientV1
		model := &scriptedModel{responses: []string{insufficientWithoutCollectionEnvelope()}}
		coord := newCoordinator(store, &fakeRepoPort{}, &fakeEvidencePort{}, model)
		// 不调用 SetCheckpointStore：nil store 必须保持 legacy 行为。

		if _, err := coord.Start(context.Background(), domain.NewRun{
			IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
		}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if store.state != domain.RunStateBlockedManualReview {
			t.Fatalf("final state = %s, want blocked_manual_review", store.state)
		}
		if strings.Contains(model.turns[0].UserMessage, "challenge") {
			t.Errorf("nil store must not inflate a recovery challenge: %s", model.turns[0].UserMessage)
		}
	})

	t.Run("legacy mode soft budget signals are no-ops", func(t *testing.T) {
		store := newFakeRunStore()
		checkpoints := &fakeCheckpointStore{}
		limits := domain.BudgetLimits{MaxRepositoryBytes: 1003}
		model := &scriptedModel{responses: []string{
			requestToolEnvelope(application.ToolRepoReadFile, "main.go"),
			requestToolEnvelope(application.ToolRepoReadFile, "main.go"),
			insufficientWithoutCollectionEnvelope(),
		}}
		coord := newCoordinatorWithBudget(store, &heavyRepoPort{}, &fakeEvidencePort{}, model, limits)
		coord.SetCheckpointStore(checkpoints)

		if _, err := coord.Start(context.Background(), domain.NewRun{
			IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
		}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if store.state != domain.RunStateBlockedManualReview {
			t.Fatalf("final state = %s, want blocked_manual_review", store.state)
		}
		if len(checkpoints.appends) != 0 {
			t.Fatalf("legacy mode appended %d checkpoints, want 0", len(checkpoints.appends))
		}
		for index, turn := range model.turns {
			if strings.Contains(turn.UserMessage, "soft_budget") || strings.Contains(turn.UserMessage, "recoverable") {
				t.Errorf("legacy turn %d contains a soft-budget challenge: %s", index, turn.UserMessage)
			}
		}
	})

	t.Run("pre-drive configuration failure preserves classification", func(t *testing.T) {
		store := newFakeRunStore()
		store.mode = domain.AgentLoopModeResilientV1
		checkpoints := &fakeCheckpointStore{runStore: store}
		model := &scriptedModel{responses: []string{insufficientWithoutCollectionEnvelope()}}
		coord := application.NewRemediationCoordinatorWithRuntime(
			store, &fakeRepoPort{}, &fakeEvidencePort{}, model,
			wiringLookup{identity: application.IncidentIdentity{
				ID: testIncidentUUID, ProjectID: testProjectID, LifecycleGeneration: 1,
			}},
			staticRemote(""), store, store,
		)
		coord.SetCheckpointStore(checkpoints)

		_, err := coord.Start(context.Background(), domain.NewRun{
			IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
		})
		if err == nil || !strings.Contains(err.Error(), "remediation configuration failure") {
			t.Fatalf("pre-drive failure error = %v, want configuration failure", err)
		}
		if store.state != domain.RunStateFailed {
			t.Fatalf("final state = %s, want failed", store.state)
		}
		if len(store.effects) == 0 || store.effects[len(store.effects)-1].TerminalReason != "configuration_failure" {
			t.Fatalf("terminal effect = %#v, want configuration_failure", store.effects)
		}
		if len(checkpoints.appends) != 1 {
			t.Fatalf("terminal checkpoints appended = %d, want 1", len(checkpoints.appends))
		}
		if checkpoint := checkpoints.appends[0]; checkpoint.Phase != string(domain.RunStateDiagnosing) ||
			checkpoint.Budget.CurrentPhase != domain.RunStateDiagnosing {
			t.Fatalf("checkpoint phase/budget phase = %q/%q, want diagnosing/diagnosing",
				checkpoint.Phase, checkpoint.Budget.CurrentPhase)
		}
		if model.calls != 0 {
			t.Fatalf("model calls = %d, want 0 before drive", model.calls)
		}
	})

	t.Run("diagnosing boundary checkpoint failure terminates resilient run", func(t *testing.T) {
		store := newFakeRunStore()
		store.mode = domain.AgentLoopModeResilientV1
		checkpoints := &fakeCheckpointStore{appendErr: fmt.Errorf("checkpoint store unavailable")}
		model := &scriptedModel{responses: []string{insufficientWithoutCollectionEnvelope()}}
		coord := newCoordinator(store, &fakeRepoPort{}, &fakeEvidencePort{}, model)
		coord.SetCheckpointStore(checkpoints)

		_, err := coord.Start(context.Background(), domain.NewRun{
			IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
		})
		if err == nil || !strings.Contains(err.Error(), "remediation persistence failure") {
			t.Fatalf("checkpoint failure error = %v, want persistence failure", err)
		}
		if store.state != domain.RunStateFailed {
			t.Fatalf("final state = %s, want failed", store.state)
		}
		if len(store.effects) == 0 || store.effects[len(store.effects)-1].TerminalReason != "persistence_failure" {
			t.Fatalf("terminal effect = %#v, want persistence_failure", store.effects)
		}
		if model.calls != 0 {
			t.Fatalf("model calls = %d, want 0 after diagnosing boundary checkpoint failure", model.calls)
		}
	})

	t.Run("terminal checkpoint failure overrides requested terminal", func(t *testing.T) {
		store := newFakeRunStore()
		store.mode = domain.AgentLoopModeResilientV1
		checkpoints := &fakeCheckpointStore{appendErr: fmt.Errorf("checkpoint store unavailable"), appendErrAt: 2}
		// 终态诊断用 unsafe_to_automate：它是 D6 明确的 policy blocker，终态前
		// 仍强制 terminal checkpoint；insufficient_evidence 在 resilient_v1 下
		// 会先要求 exhaustion proof，不再直接走到终态 checkpoint。
		model := &scriptedModel{responses: []string{diagnosisEnvelope("unsafe_to_automate")}}
		coord := newCoordinator(store, &fakeRepoPort{}, &fakeEvidencePort{}, model)
		coord.SetCheckpointStore(checkpoints)

		_, err := coord.Start(context.Background(), domain.NewRun{
			IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
		})
		if err == nil || !strings.Contains(err.Error(), "persist required checkpoint before terminal transition") {
			t.Fatalf("terminal checkpoint failure error = %v", err)
		}
		if store.state != domain.RunStateFailed || len(checkpoints.appends) != 1 {
			t.Fatalf("state/appends = %s/%d, want failed/1", store.state, len(checkpoints.appends))
		}
		if len(store.effects) == 0 || store.effects[len(store.effects)-1].TerminalReason != "persistence_failure" {
			t.Fatalf("terminal effect = %#v, want persistence_failure", store.effects)
		}
		if model.calls != 1 {
			t.Fatalf("model calls = %d, want 1 before terminal checkpoint failure", model.calls)
		}
	})
}

// TestResilient_EvidenceReadUpdatesCheckpointEvidenceIndex 证明成功的
// evidence.read 把证据 ID/kind/hash 记入进程内 checkpoint evidence index，
// 并在下一次强制 checkpoint 时持久化；payload 不进入索引。
func TestResilient_EvidenceReadUpdatesCheckpointEvidenceIndex(t *testing.T) {
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	checkpoints := &fakeCheckpointStore{}
	readPort := &fakeEvidenceReadPort{result: mustEvidenceReadPage()}
	model := &scriptedModel{responses: []string{
		requestEvidenceReadEnvelope("0190-0000-0000-7000-0000000000aa"),
		// 终态诊断用 unsafe_to_automate（policy blocker，保留直接终态语义）；
		// resilient_v1 的 insufficient_evidence 会先要求 exhaustion proof。
		diagnosisEnvelope("unsafe_to_automate"),
	}}
	coord := sourceWiredCoordinator(store, &fakeRepoPort{}, &fakeEvidencePort{}, model)
	coord.SetCheckpointStore(checkpoints)
	coord.SetEvidenceReadPort(readPort)

	if _, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.state != domain.RunStateBlockedManualReview {
		t.Fatalf("final state = %s, want blocked_manual_review", store.state)
	}
	if readPort.calls != 1 {
		t.Fatalf("evidence.read calls = %d, want 1", readPort.calls)
	}
	if len(checkpoints.appends) != 2 {
		t.Fatalf("checkpoints appended = %d, want 2", len(checkpoints.appends))
	}
	if len(checkpoints.appends[0].EvidenceIndex) != 0 {
		t.Errorf("phase boundary checkpoint must precede any evidence.read")
	}
	last := checkpoints.appends[len(checkpoints.appends)-1]
	if len(last.EvidenceIndex) != 1 {
		t.Fatalf("terminal checkpoint evidence index = %d entries, want 1", len(last.EvidenceIndex))
	}
	item := last.EvidenceIndex[0]
	if item.EvidenceID != "0190-0000-0000-7000-0000000000aa" || item.Kind != domain.EvidenceKindProviderDetail {
		t.Errorf("evidence index item = %#v, want provider detail page identity", item)
	}
	if item.ContentHash != strings.Repeat("a", 64) {
		t.Errorf("evidence index content hash = %q, want page hash", item.ContentHash)
	}
}

// TestResilient_EvidenceIndexDeduplicatesRepeatedReads 证明同一 run 内重复
// evidence.read 同一证据只保留一条索引条目（第二页/重复读取不重复记账）。
func TestResilient_EvidenceIndexDeduplicatesRepeatedReads(t *testing.T) {
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	checkpoints := &fakeCheckpointStore{}
	readPort := &fakeEvidenceReadPort{result: mustEvidenceReadPage()}
	model := &scriptedModel{responses: []string{
		requestEvidenceReadEnvelope("0190-0000-0000-7000-0000000000aa"),
		requestEvidenceReadEnvelope("0190-0000-0000-7000-0000000000aa"),
		// 终态诊断用 unsafe_to_automate（policy blocker，保留直接终态语义）；
		// resilient_v1 的 insufficient_evidence 会先要求 exhaustion proof。
		diagnosisEnvelope("unsafe_to_automate"),
	}}
	coord := sourceWiredCoordinator(store, &fakeRepoPort{}, &fakeEvidencePort{}, model)
	coord.SetCheckpointStore(checkpoints)
	coord.SetEvidenceReadPort(readPort)

	if _, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if readPort.calls != 2 {
		t.Fatalf("evidence.read calls = %d, want 2", readPort.calls)
	}
	last := checkpoints.appends[len(checkpoints.appends)-1]
	if len(last.EvidenceIndex) != 1 {
		t.Fatalf("evidence index entries = %d, want 1 (deduplicated)", len(last.EvidenceIndex))
	}
}
