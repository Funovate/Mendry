package domain

import (
	"testing"
	"time"
)

// testValidBudgetPlan 返回一个恰好 fitting 的合法计划：soft 10×5、
// later reserves 5×5、recovery 5，模型调用维度总和 80 ≤ ceiling 100。
func testValidBudgetPlan() BudgetPlanV1 {
	ceiling := BudgetLimits{
		MaxElapsed: time.Hour, MaxModelCalls: 100, MaxModelCostCents: 1000,
		MaxToolCalls: 100, MaxEvidenceBytes: 1 << 20, MaxRepositoryBytes: 1 << 20,
	}
	plan := BudgetPlanV1{
		SchemaVersion: BudgetPlanSchemaVersionV1,
		Ceiling:       ceiling,
		Phases:        make([]PhaseBudgetAllocation, 0, MaxBudgetPlanPhases),
		LaterReserves: make([]PhaseReserve, 0, MaxBudgetPlanPhases),
		Recovery:      RecoveryReserve{Min: BudgetAmount{ModelCalls: 5}},
	}
	for _, phase := range BudgetPhaseOrder() {
		plan.Phases = append(plan.Phases, PhaseBudgetAllocation{Phase: phase, Soft: BudgetAmount{ModelCalls: 10}})
		plan.LaterReserves = append(plan.LaterReserves, PhaseReserve{Phase: phase, Min: BudgetAmount{ModelCalls: 5}})
	}
	return plan
}

func TestBudgetPhaseOrderAndIndex(t *testing.T) {
	want := []RunState{
		RunStateDiagnosing, RunStatePlanning, RunStatePatching,
		RunStateValidating, RunStatePublishing,
	}
	got := BudgetPhaseOrder()
	if len(got) != len(want) {
		t.Fatalf("BudgetPhaseOrder length = %d, want %d", len(got), len(want))
	}
	for index, phase := range want {
		if got[index] != phase {
			t.Fatalf("BudgetPhaseOrder[%d] = %q, want %q", index, got[index], phase)
		}
		if BudgetPhaseIndex(phase) != index {
			t.Fatalf("BudgetPhaseIndex(%q) = %d, want %d", phase, BudgetPhaseIndex(phase), index)
		}
	}
	if BudgetPhaseIndex(RunStateQueued) != -1 {
		t.Fatalf("BudgetPhaseIndex(queued) = %d, want -1", BudgetPhaseIndex(RunStateQueued))
	}
	if !IsBudgetPhase(RunStateDiagnosing) || IsBudgetPhase(RunStateQueued) {
		t.Fatal("IsBudgetPhase misclassified a state")
	}
}

func TestBudgetPhaseForNormalizesCollectingMoreContext(t *testing.T) {
	if phase, ok := BudgetPhaseFor(RunStateCollectingMoreContext); !ok || phase != RunStateDiagnosing {
		t.Fatalf("BudgetPhaseFor(collecting_more_context) = %q, %v; want diagnosing, true", phase, ok)
	}
	if phase, ok := BudgetPhaseFor(RunStatePlanning); !ok || phase != RunStatePlanning {
		t.Fatalf("BudgetPhaseFor(planning) = %q, %v; want planning, true", phase, ok)
	}
	if _, ok := BudgetPhaseFor(RunStateQueued); ok {
		t.Fatal("BudgetPhaseFor(queued) reported ok, want false")
	}
}

func TestBudgetAmountValidateRejectsNegativeDimensions(t *testing.T) {
	amounts := []BudgetAmount{
		{ElapsedSeconds: -1}, {ModelCalls: -1}, {ModelCostCents: -1},
		{ToolCalls: -1}, {EvidenceBytes: -1}, {RepositoryBytes: -1},
	}
	for _, amount := range amounts {
		if err := amount.Validate(); err == nil {
			t.Fatalf("Validate(%+v) succeeded, want negative-dimension error", amount)
		}
	}
	if err := (BudgetAmount{}).Validate(); err != nil {
		t.Fatalf("Validate(zero) = %v, want nil", err)
	}
	if !(BudgetAmount{}).IsZero() {
		t.Fatal("zero BudgetAmount is not IsZero")
	}
	if (BudgetAmount{ModelCalls: 1}).IsZero() {
		t.Fatal("non-zero BudgetAmount is IsZero")
	}
}

func TestBudgetAmountAddSubClamp(t *testing.T) {
	a := BudgetAmount{ModelCalls: 3, EvidenceBytes: 5}
	b := BudgetAmount{ModelCalls: 2}
	if sum := a.Add(b); sum.ModelCalls != 5 || sum.EvidenceBytes != 5 {
		t.Fatalf("Add = %+v, want model calls 5, evidence bytes 5", sum)
	}
	if diff := a.Sub(b); diff.ModelCalls != 1 || diff.EvidenceBytes != 5 {
		t.Fatalf("Sub = %+v, want model calls 1, evidence bytes 5", diff)
	}
	negative := b.Sub(BudgetAmount{ModelCalls: 4})
	if negative.ModelCalls != -2 {
		t.Fatalf("Sub model calls = %d, want -2 (raw negative preserved)", negative.ModelCalls)
	}
	if clamped := negative.ClampNonNegative(); clamped.ModelCalls != 0 {
		t.Fatalf("ClampNonNegative model calls = %d, want 0", clamped.ModelCalls)
	}
}

func TestBudgetAmountAsMap(t *testing.T) {
	amount := BudgetAmount{
		ElapsedSeconds: 1, ModelCalls: 2, ModelCostCents: 3,
		ToolCalls: 4, EvidenceBytes: 5, RepositoryBytes: 6,
	}
	got := amount.AsMap()
	want := map[string]int64{
		"elapsed_seconds": 1, "model_calls": 2, "model_cost_cents": 3,
		"tool_calls": 4, "evidence_bytes": 5, "repository_bytes": 6,
	}
	if len(got) != len(want) {
		t.Fatalf("AsMap length = %d, want %d", len(got), len(want))
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("AsMap[%q] = %d, want %d", key, got[key], value)
		}
	}
}

func TestBudgetLimitsBudgetAmountConvertsElapsedToSeconds(t *testing.T) {
	limits := BudgetLimits{
		MaxElapsed: 90*time.Second + 500*time.Millisecond, MaxModelCalls: 3,
	}
	amount := limits.BudgetAmount()
	if amount.ElapsedSeconds != 90 {
		t.Fatalf("ElapsedSeconds = %d, want 90 (truncated to whole seconds)", amount.ElapsedSeconds)
	}
	if amount.ModelCalls != 3 {
		t.Fatalf("ModelCalls = %d, want 3", amount.ModelCalls)
	}
}

func TestEffectBudgetAmount(t *testing.T) {
	effect := Effect{
		ModelCalls: 2, ModelTokensIn: 100, ModelTokensOut: 50, ModelCostCents: 3,
		ToolCalls: 1, EvidenceBytes: 10, RepositoryBytes: 20,
	}
	got := effect.BudgetAmount()
	want := BudgetAmount{ModelCalls: 2, ModelCostCents: 3, ToolCalls: 1, EvidenceBytes: 10, RepositoryBytes: 20}
	if got != want {
		t.Fatalf("Effect.BudgetAmount() = %+v, want %+v (tokens and elapsed excluded)", got, want)
	}
	if got.ElapsedSeconds != 0 {
		t.Fatalf("ElapsedSeconds = %d, want 0 (caller supplies elapsed)", got.ElapsedSeconds)
	}
}

func TestBudgetPlanValidateRejectsZeroCeiling(t *testing.T) {
	plan := testValidBudgetPlan()
	plan.Ceiling.MaxModelCalls = 0
	if err := plan.Validate(); err == nil {
		t.Fatal("Validate accepted a zero model-calls ceiling")
	}
	plan = testValidBudgetPlan()
	plan.Ceiling.MaxElapsed = 500 * time.Millisecond
	if err := plan.Validate(); err == nil {
		t.Fatal("Validate accepted a sub-second elapsed ceiling")
	}
}

func TestBudgetPlanValidateRejectsNegativeAllocation(t *testing.T) {
	plan := testValidBudgetPlan()
	plan.Phases[0].Soft.ModelCalls = -1
	if err := plan.Validate(); err == nil {
		t.Fatal("Validate accepted a negative soft allocation")
	}
}

func TestBudgetPlanValidateRejectsUnknownPhase(t *testing.T) {
	plan := testValidBudgetPlan()
	plan.Phases[0].Phase = RunStateQueued
	if err := plan.Validate(); err == nil {
		t.Fatal("Validate accepted a non-budget phase")
	}
	plan = testValidBudgetPlan()
	plan.LaterReserves[0].Phase = RunStateQueued
	if err := plan.Validate(); err == nil {
		t.Fatal("Validate accepted a non-budget reserve phase")
	}
}

func TestBudgetPlanValidateRejectsDuplicatePhase(t *testing.T) {
	plan := testValidBudgetPlan()
	plan.Phases[1].Phase = RunStateDiagnosing
	if err := plan.Validate(); err == nil {
		t.Fatal("Validate accepted duplicated phase allocations")
	}
	plan = testValidBudgetPlan()
	plan.LaterReserves[1].Phase = RunStateDiagnosing
	if err := plan.Validate(); err == nil {
		t.Fatal("Validate accepted duplicated reserve phases")
	}
}

func TestBudgetPlanValidateRejectsWrongPhaseCount(t *testing.T) {
	plan := testValidBudgetPlan()
	plan.Phases = plan.Phases[:MaxBudgetPlanPhases-1]
	if err := plan.Validate(); err == nil {
		t.Fatal("Validate accepted fewer than five phase allocations")
	}
}

func TestBudgetPlanValidateRejectsReserveOversubscription(t *testing.T) {
	// 模型调用维度：soft 10×5 + reserves 5×5 + recovery 5 = 80 ≤ 100 合法；
	// 把 recovery 抬到 26 后总和 101 > 100，必须拒绝（reserves 计入超额）。
	plan := testValidBudgetPlan()
	plan.Recovery.Min.ModelCalls = 26
	if err := plan.Validate(); err == nil {
		t.Fatal("Validate accepted reserves exceeding the ceiling")
	}
	// 只抬 soft allocation 同样必须拒绝。
	plan = testValidBudgetPlan()
	for index := range plan.Phases {
		plan.Phases[index].Soft.ModelCalls = 20
	}
	if err := plan.Validate(); err == nil {
		t.Fatal("Validate accepted soft allocations exceeding the ceiling")
	}
}

func TestBudgetPlanValidateAcceptsExactFit(t *testing.T) {
	plan := testValidBudgetPlan()
	if err := plan.Validate(); err != nil {
		t.Fatalf("Validate(exact-fit plan) = %v, want nil", err)
	}
}

func TestBudgetPlanProjectionValidate(t *testing.T) {
	projection := BudgetPlanProjection{
		SchemaVersion: BudgetPlanSchemaVersionV1,
		Phase:         RunStateDiagnosing,
		Consumed:      BudgetAmount{ModelCalls: 4},
		Remaining:     BudgetAmount{ModelCalls: 96},
		SoftRemaining: []PhaseSoftRemaining{
			{Phase: RunStateDiagnosing, Remaining: BudgetAmount{ModelCalls: 6}},
			{Phase: RunStatePlanning, Remaining: BudgetAmount{ModelCalls: 10}},
			{Phase: RunStatePatching, Remaining: BudgetAmount{ModelCalls: 10}},
			{Phase: RunStateValidating, Remaining: BudgetAmount{ModelCalls: 10}},
			{Phase: RunStatePublishing, Remaining: BudgetAmount{ModelCalls: 10}},
		},
		Reserves:   []PhaseReserve{{Phase: RunStatePlanning, Min: BudgetAmount{ModelCalls: 5}}},
		Recovery:   RecoveryReserve{Min: BudgetAmount{ModelCalls: 5}},
		Unreserved: BudgetAmount{ModelCalls: 40},
		Ceiling:    testValidBudgetPlan().Ceiling,
	}
	if err := projection.Validate(); err != nil {
		t.Fatalf("Validate(valid projection) = %v, want nil", err)
	}
	projection.Phase = RunStateQueued
	if err := projection.Validate(); err == nil {
		t.Fatal("Validate accepted an unknown projection phase")
	}
	projection = BudgetPlanProjection{
		SchemaVersion: BudgetPlanSchemaVersionV1,
		Phase:         RunStateDiagnosing,
		Consumed:      BudgetAmount{ModelCalls: -1},
		SoftRemaining: []PhaseSoftRemaining{{Phase: RunStateDiagnosing}},
	}
	if err := projection.Validate(); err == nil {
		t.Fatal("Validate accepted negative consumed amounts")
	}
	projection = BudgetPlanProjection{
		SchemaVersion: BudgetPlanSchemaVersionV1,
		Phase:         RunStateDiagnosing,
		SoftRemaining: []PhaseSoftRemaining{{Phase: RunStateDiagnosing}},
	}
	if err := projection.Validate(); err == nil {
		t.Fatal("Validate accepted fewer than five soft remaining entries")
	}
}
