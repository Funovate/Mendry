package application

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"fixthe/backend/internal/modules/remediation/domain"
)

func TestPhaseBudgetPlanSerializeRestartContinueEveryBoundary(t *testing.T) {
	plan := testBudgetPlan(t, testBudgetCeiling(), allPhasesSoft(domain.BudgetAmount{ModelCalls: 10}), map[domain.RunState]domain.BudgetAmount{
		domain.RunStatePlanning: {ModelCalls: 2},
		domain.RunStatePatching: {ModelCalls: 2},
	}, domain.BudgetAmount{ModelCalls: 2})
	allocator, err := newPhaseBudgetPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	for index, phase := range domain.BudgetPhaseOrder() {
		if err := allocator.AdmitPhase(phase); err != nil {
			t.Fatalf("admit %q: %v", phase, err)
		}
		if _, err := allocator.Consume(phase, domain.BudgetAmount{ModelCalls: int64(index + 1)}); err != nil {
			t.Fatalf("consume %q: %v", phase, err)
		}
		assertBudgetRestartEquivalent(t, allocator)
		if err := allocator.ClosePhase(phase); err != nil {
			t.Fatalf("close %q: %v", phase, err)
		}
		assertBudgetRestartEquivalent(t, allocator)
	}
}

func assertBudgetRestartEquivalent(t *testing.T, allocator *phaseBudgetPlan) {
	t.Helper()
	payload, err := json.Marshal(allocator.Recovery())
	if err != nil {
		t.Fatalf("marshal recovery: %v", err)
	}
	var recovery domain.BudgetPlanRecoveryV1
	if err := json.Unmarshal(payload, &recovery); err != nil {
		t.Fatalf("unmarshal recovery: %v", err)
	}
	restored, err := restorePhaseBudgetPlan(recovery)
	if err != nil {
		t.Fatalf("restore recovery: %v", err)
	}
	if !reflect.DeepEqual(restored.Projection(), allocator.Projection()) {
		t.Fatalf("restored projection = %+v, want %+v", restored.Projection(), allocator.Projection())
	}
	if !reflect.DeepEqual(restored.Recovery(), allocator.Recovery()) {
		t.Fatalf("restored recovery = %+v, want %+v", restored.Recovery(), allocator.Recovery())
	}
}

// testBudgetCeiling 返回所有维度都足够大的 ceiling，避免测试被默认值干扰。
func testBudgetCeiling() domain.BudgetLimits {
	return domain.BudgetLimits{
		MaxElapsed:         24 * time.Hour,
		MaxModelCalls:      100,
		MaxModelCostCents:  1000,
		MaxToolCalls:       100,
		MaxEvidenceBytes:   1 << 20,
		MaxRepositoryBytes: 1 << 20,
	}
}

// testBudgetPlan 构造通过 domain Validate 的显式计划：每个 phase 的 soft 与
// reserve 来自 map，缺省为 0；便于确定性断言。
func testBudgetPlan(t *testing.T, ceiling domain.BudgetLimits, soft, reserves map[domain.RunState]domain.BudgetAmount, recovery domain.BudgetAmount) domain.BudgetPlanV1 {
	t.Helper()
	plan := domain.BudgetPlanV1{
		SchemaVersion: domain.BudgetPlanSchemaVersionV1,
		Ceiling:       ceiling,
		Recovery:      domain.RecoveryReserve{Min: recovery},
	}
	for _, phase := range domain.BudgetPhaseOrder() {
		plan.Phases = append(plan.Phases, domain.PhaseBudgetAllocation{Phase: phase, Soft: soft[phase]})
		plan.LaterReserves = append(plan.LaterReserves, domain.PhaseReserve{Phase: phase, Min: reserves[phase]})
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("test budget plan failed validation: %v", err)
	}
	return plan
}

// allPhasesSoft 返回五个 phase 相同 soft allocation 的 map。
func allPhasesSoft(amount domain.BudgetAmount) map[domain.RunState]domain.BudgetAmount {
	soft := make(map[domain.RunState]domain.BudgetAmount, domain.MaxBudgetPlanPhases)
	for _, phase := range domain.BudgetPhaseOrder() {
		soft[phase] = amount
	}
	return soft
}

func TestPhaseBudgetPlanDiagnosisBorrowsOnlyUnreservedPool(t *testing.T) {
	// (a) AC10：诊断只能借用 unreserved pool，永不触碰 planning/recovery
	// reserves。ceiling 100 次模型调用；soft 每 phase 10（合计 50），
	// planning reserve 5，recovery reserve 5 → pool = 40。
	ceiling := testBudgetCeiling()
	plan := testBudgetPlan(
		t,
		ceiling,
		allPhasesSoft(domain.BudgetAmount{ModelCalls: 10}),
		map[domain.RunState]domain.BudgetAmount{domain.RunStatePlanning: {ModelCalls: 5}},
		domain.BudgetAmount{ModelCalls: 5},
	)
	allocator, err := newPhaseBudgetPlan(plan)
	if err != nil {
		t.Fatalf("newPhaseBudgetPlan: %v", err)
	}
	if err := allocator.AdmitPhase(domain.RunStateDiagnosing); err != nil {
		t.Fatalf("AdmitPhase: %v", err)
	}
	// 消耗 soft allocation：within。
	result, err := allocator.Consume(domain.RunStateDiagnosing, domain.BudgetAmount{ModelCalls: 10})
	if err != nil || result != softWithinAllocation {
		t.Fatalf("Consume(soft) = %q, %v; want within_allocation", result, err)
	}
	// 借用 pool 内：soft_crossed（可恢复信号，不是 budget_exhausted）。
	result, err = allocator.Consume(domain.RunStateDiagnosing, domain.BudgetAmount{ModelCalls: 5})
	if err != nil || result != softCrossed {
		t.Fatalf("Consume(pool) = %q, %v; want soft_crossed", result, err)
	}
	// 恰好借满 pool（over = 40）：仍是 soft_crossed。
	result, err = allocator.Consume(domain.RunStateDiagnosing, domain.BudgetAmount{ModelCalls: 35})
	if err != nil || result != softCrossed {
		t.Fatalf("Consume(pool exhausted) = %q, %v; want soft_crossed", result, err)
	}
	// 越过 pool：reserve_touched。
	result, err = allocator.Consume(domain.RunStateDiagnosing, domain.BudgetAmount{ModelCalls: 1})
	if err != nil || result != softReserveTouched {
		t.Fatalf("Consume(beyond pool) = %q, %v; want reserve_touched", result, err)
	}
	// 投影证明 planning/recovery reserves 仍然 standing，未被消耗。
	projection := allocator.Projection()
	if len(projection.Reserves) != 1 || projection.Reserves[0].Phase != domain.RunStatePlanning ||
		projection.Reserves[0].Min.ModelCalls != 5 {
		t.Fatalf("standing reserves = %+v, want planning reserve 5", projection.Reserves)
	}
	if projection.Recovery.Min.ModelCalls != 5 {
		t.Fatalf("recovery reserve = %d, want 5", projection.Recovery.Min.ModelCalls)
	}
	if projection.Consumed.ModelCalls != 51 {
		t.Fatalf("consumed model calls = %d, want 51", projection.Consumed.ModelCalls)
	}
	if err := projection.Validate(); err != nil {
		t.Fatalf("projection failed validation: %v", err)
	}
}

func TestPhaseBudgetPlanSoftCrossingIsRecoverableNotHard(t *testing.T) {
	// (b) 越过 soft allocation 产生 recoverable soft_crossed；相同总消耗在
	// runBudget 下不产生 hard reason；softBudgetResult 的词汇与
	// budgetExhaustionReason 完全不相交。
	ceiling := testBudgetCeiling()
	plan := testBudgetPlan(t, ceiling, allPhasesSoft(domain.BudgetAmount{ModelCalls: 10}), nil, domain.BudgetAmount{})
	allocator, err := newPhaseBudgetPlan(plan)
	if err != nil {
		t.Fatalf("newPhaseBudgetPlan: %v", err)
	}
	if err := allocator.AdmitPhase(domain.RunStateDiagnosing); err != nil {
		t.Fatalf("AdmitPhase: %v", err)
	}
	result, err := allocator.Consume(domain.RunStateDiagnosing, domain.BudgetAmount{ModelCalls: 15})
	if err != nil || result != softCrossed {
		t.Fatalf("Consume(15) = %q, %v; want soft_crossed", result, err)
	}
	// 同一个 15 次模型调用消耗对 runBudget（hard ceiling 100）不产生任何
	// hard reason。
	budget := newRunBudget(ceiling)
	if reason := budget.consume(domain.Effect{ModelCalls: 15}); reason != "" {
		t.Fatalf("runBudget.consume(15) = %q, want no hard reason", reason)
	}
	// 可恢复信号与 hard 词汇完全分离。
	hardReasons := []string{
		string(budgetReasonElapsed), string(budgetReasonModelCalls),
		string(budgetReasonModelCost), string(budgetReasonToolCalls),
		string(budgetReasonEvidenceBytes), string(budgetReasonRepositoryBytes),
	}
	for _, signal := range []string{softWithinAllocation.String(), softCrossed.String(), softReserveTouched.String()} {
		for _, reason := range hardReasons {
			if signal == reason {
				t.Fatalf("soft signal %q collides with hard reason %q", signal, reason)
			}
		}
	}
}

func TestPhaseBudgetPlanUnusedSoftTransfersForward(t *testing.T) {
	// (c) 未用 soft allocation 在 phase close 后转入 unreserved pool：原始
	// pool 50，诊断只消耗 4 后 close，释放 6 → pool 56；planning 借用 55
	// 仍为 soft_crossed（超过原始 pool）。
	ceiling := testBudgetCeiling()
	plan := testBudgetPlan(t, ceiling, allPhasesSoft(domain.BudgetAmount{ModelCalls: 10}), nil, domain.BudgetAmount{})
	allocator, err := newPhaseBudgetPlan(plan)
	if err != nil {
		t.Fatalf("newPhaseBudgetPlan: %v", err)
	}
	if err := allocator.AdmitPhase(domain.RunStateDiagnosing); err != nil {
		t.Fatalf("AdmitPhase: %v", err)
	}
	if _, err := allocator.Consume(domain.RunStateDiagnosing, domain.BudgetAmount{ModelCalls: 4}); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if err := allocator.ClosePhase(domain.RunStateDiagnosing); err != nil {
		t.Fatalf("ClosePhase: %v", err)
	}
	if err := allocator.AdmitPhase(domain.RunStatePlanning); err != nil {
		t.Fatalf("AdmitPhase(planning): %v", err)
	}
	if _, err := allocator.Consume(domain.RunStatePlanning, domain.BudgetAmount{ModelCalls: 10}); err != nil {
		t.Fatalf("Consume(soft): %v", err)
	}
	result, err := allocator.Consume(domain.RunStatePlanning, domain.BudgetAmount{ModelCalls: 55})
	if err != nil || result != softCrossed {
		t.Fatalf("Consume(55) = %q, %v; want soft_crossed (transferred capacity)", result, err)
	}
	result, err = allocator.Consume(domain.RunStatePlanning, domain.BudgetAmount{ModelCalls: 2})
	if err != nil || result != softReserveTouched {
		t.Fatalf("Consume(2) = %q, %v; want reserve_touched", result, err)
	}
	if pool := allocator.Projection().Unreserved; pool.ModelCalls != 0 {
		t.Fatalf("unreserved pool = %d, want 0 after planning exhausted transferred capacity", pool.ModelCalls)
	}
}

func TestPhaseBudgetPlanCannotBorrowUnreservedPoolTwiceAcrossPhases(t *testing.T) {
	// base pool=40。diagnosing 消耗 soft 10 + pool 40 后关闭；planning 只能
	// 使用自己的 soft 10，再多 1 必须立即报告 reserve_touched，不能重新看到
	// 已被 diagnosing 借完的 40。
	plan := testBudgetPlan(
		t,
		testBudgetCeiling(),
		allPhasesSoft(domain.BudgetAmount{ModelCalls: 10}),
		map[domain.RunState]domain.BudgetAmount{domain.RunStatePlanning: {ModelCalls: 5}},
		domain.BudgetAmount{ModelCalls: 5},
	)
	allocator, err := newPhaseBudgetPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := allocator.AdmitPhase(domain.RunStateDiagnosing); err != nil {
		t.Fatal(err)
	}
	if result, err := allocator.Consume(domain.RunStateDiagnosing, domain.BudgetAmount{ModelCalls: 50}); err != nil || result != softCrossed {
		t.Fatalf("diagnosing consume = %q, %v; want soft_crossed", result, err)
	}
	if err := allocator.ClosePhase(domain.RunStateDiagnosing); err != nil {
		t.Fatal(err)
	}
	if err := allocator.AdmitPhase(domain.RunStatePlanning); err != nil {
		t.Fatal(err)
	}
	available, err := allocator.Available(domain.RunStatePlanning)
	if err != nil || available.ModelCalls != 10 {
		t.Fatalf("planning available = %+v, %v; want only its soft 10", available, err)
	}
	if result, err := allocator.Consume(domain.RunStatePlanning, domain.BudgetAmount{ModelCalls: 11}); err != nil || result != softReserveTouched {
		t.Fatalf("planning consume = %q, %v; want reserve_touched", result, err)
	}
	assertBudgetRestartEquivalent(t, allocator)
}

func TestPhaseBudgetPlanOnlyHardCeilingIsTerminal(t *testing.T) {
	// (d) 只有 global hard ceiling 产生 hard reason（coordinator 由此进入
	// budget_exhausted）；allocator 无论消耗多大都只返回 recoverable 信号。
	tiny := domain.BudgetLimits{
		MaxElapsed: time.Hour, MaxModelCalls: 1, MaxModelCostCents: 1,
		MaxToolCalls: 1, MaxEvidenceBytes: 1, MaxRepositoryBytes: 1,
	}
	budget := newRunBudget(tiny)
	if reason := budget.consume(domain.Effect{ModelCalls: 2}); reason != budgetReasonModelCalls {
		t.Fatalf("runBudget.consume(2) = %q, want model_calls hard reason", reason)
	}
	// 同样的超量消耗在 allocator 上只能是 reserve_touched：soft 0、pool 1，
	// over 2 > 1。
	plan := testBudgetPlan(t, tiny, allPhasesSoft(domain.BudgetAmount{}), nil, domain.BudgetAmount{})
	allocator, err := newPhaseBudgetPlan(plan)
	if err != nil {
		t.Fatalf("newPhaseBudgetPlan: %v", err)
	}
	if err := allocator.AdmitPhase(domain.RunStateDiagnosing); err != nil {
		t.Fatalf("AdmitPhase: %v", err)
	}
	result, err := allocator.Consume(domain.RunStateDiagnosing, domain.BudgetAmount{ModelCalls: 2})
	if err != nil || result != softReserveTouched {
		t.Fatalf("Consume(2) = %q, %v; want reserve_touched, never a hard reason", result, err)
	}
}

func TestNewBudgetPlanFillsConservativeDefaults(t *testing.T) {
	// (f) 默认保守分配：soft 每 phase 15%、later-phase reserve 每 phase 5%
	// （planning..publishing）、recovery 5%，总和恰为 ceiling 的 100%；
	// ceiling 零值按 DefaultBudgetLimits 归一化。
	plan, err := NewBudgetPlan(domain.BudgetLimits{}, nil, nil, domain.RecoveryReserve{})
	if err != nil {
		t.Fatalf("NewBudgetPlan(defaults): %v", err)
	}
	defaults := DefaultBudgetLimits()
	if plan.Ceiling != defaults {
		t.Fatalf("ceiling = %+v, want normalized defaults %+v", plan.Ceiling, defaults)
	}
	expectedSoft := percentOf(defaults.BudgetAmount(), defaultSoftAllocationPercent)
	expectedReserve := percentOf(defaults.BudgetAmount(), defaultLaterReservePercent)
	expectedRecovery := percentOf(defaults.BudgetAmount(), defaultRecoveryReservePercent)
	if len(plan.Phases) != domain.MaxBudgetPlanPhases {
		t.Fatalf("phases = %d, want %d", len(plan.Phases), domain.MaxBudgetPlanPhases)
	}
	for _, allocation := range plan.Phases {
		if allocation.Soft != expectedSoft {
			t.Fatalf("phase %q soft = %+v, want default %+v", allocation.Phase, allocation.Soft, expectedSoft)
		}
	}
	if len(plan.LaterReserves) != domain.MaxBudgetPlanPhases-1 {
		t.Fatalf("later reserves = %d, want 4 (no diagnosing reserve default)", len(plan.LaterReserves))
	}
	for _, reserve := range plan.LaterReserves {
		if reserve.Phase == domain.RunStateDiagnosing {
			t.Fatal("default plan contains a diagnosing reserve")
		}
		if reserve.Min != expectedReserve {
			t.Fatalf("reserve %q min = %+v, want default %+v", reserve.Phase, reserve.Min, expectedReserve)
		}
	}
	if plan.Recovery.Min != expectedRecovery {
		t.Fatalf("recovery reserve = %+v, want default %+v", plan.Recovery.Min, expectedRecovery)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("default plan failed validation: %v", err)
	}
}

func TestNewBudgetPlanKeepsExplicitValues(t *testing.T) {
	ceiling := testBudgetCeiling()
	plan, err := NewBudgetPlan(
		ceiling,
		[]domain.PhaseBudgetAllocation{{Phase: domain.RunStateDiagnosing, Soft: domain.BudgetAmount{ModelCalls: 7}}},
		[]domain.PhaseReserve{{Phase: domain.RunStatePlanning, Min: domain.BudgetAmount{ModelCalls: 3}}},
		domain.RecoveryReserve{Min: domain.BudgetAmount{ModelCalls: 2}},
	)
	if err != nil {
		t.Fatalf("NewBudgetPlan(explicit): %v", err)
	}
	for _, allocation := range plan.Phases {
		want := domain.BudgetAmount{ModelCalls: 7}
		if allocation.Phase != domain.RunStateDiagnosing {
			want = percentOf(ceiling.BudgetAmount(), defaultSoftAllocationPercent)
		}
		if allocation.Soft != want {
			t.Fatalf("phase %q soft = %+v, want %+v", allocation.Phase, allocation.Soft, want)
		}
	}
	for _, reserve := range plan.LaterReserves {
		if reserve.Phase == domain.RunStatePlanning {
			if reserve.Min.ModelCalls != 3 {
				t.Fatalf("planning reserve = %+v, want explicit 3", reserve.Min)
			}
			continue
		}
		if reserve.Min != percentOf(ceiling.BudgetAmount(), defaultLaterReservePercent) {
			t.Fatalf("reserve %q = %+v, want default", reserve.Phase, reserve.Min)
		}
	}
	if plan.Recovery.Min.ModelCalls != 2 {
		t.Fatalf("recovery reserve = %+v, want explicit 2", plan.Recovery.Min)
	}
}

func TestPhaseBudgetPlanAdmitPhaseRejectsBackwardOrClosed(t *testing.T) {
	plan := testBudgetPlan(t, testBudgetCeiling(), allPhasesSoft(domain.BudgetAmount{}), nil, domain.BudgetAmount{})
	allocator, err := newPhaseBudgetPlan(plan)
	if err != nil {
		t.Fatalf("newPhaseBudgetPlan: %v", err)
	}
	if err := allocator.AdmitPhase(domain.RunStateQueued); err == nil {
		t.Fatal("AdmitPhase accepted a non-budget phase")
	}
	if err := allocator.AdmitPhase(domain.RunStateDiagnosing); err != nil {
		t.Fatalf("AdmitPhase(diagnosing): %v", err)
	}
	if err := allocator.AdmitPhase(domain.RunStatePlanning); err == nil {
		t.Fatal("AdmitPhase advanced before closing the current phase")
	}
	// 重复 admit 当前 phase 是幂等 no-op。
	if err := allocator.AdmitPhase(domain.RunStateDiagnosing); err != nil {
		t.Fatalf("re-AdmitPhase(diagnosing): %v", err)
	}
	if err := allocator.ClosePhase(domain.RunStateDiagnosing); err != nil {
		t.Fatalf("ClosePhase: %v", err)
	}
	if err := allocator.AdmitPhase(domain.RunStatePlanning); err != nil {
		t.Fatalf("AdmitPhase(planning): %v", err)
	}
	if err := allocator.AdmitPhase(domain.RunStateDiagnosing); err == nil {
		t.Fatal("AdmitPhase accepted a backward phase move")
	}
	if err := allocator.ClosePhase(domain.RunStatePlanning); err != nil {
		t.Fatalf("ClosePhase(planning): %v", err)
	}
	if err := allocator.AdmitPhase(domain.RunStatePlanning); err == nil {
		t.Fatal("AdmitPhase accepted a closed phase")
	}
}

func TestPhaseBudgetPlanConsumeAndCloseRequireCurrent(t *testing.T) {
	plan := testBudgetPlan(t, testBudgetCeiling(), allPhasesSoft(domain.BudgetAmount{ModelCalls: 10}), nil, domain.BudgetAmount{})
	allocator, err := newPhaseBudgetPlan(plan)
	if err != nil {
		t.Fatalf("newPhaseBudgetPlan: %v", err)
	}
	if _, err := allocator.Consume(domain.RunStateDiagnosing, domain.BudgetAmount{ModelCalls: 1}); err == nil {
		t.Fatal("Consume succeeded without an admitted phase")
	}
	if err := allocator.ClosePhase(domain.RunStateDiagnosing); err == nil {
		t.Fatal("ClosePhase succeeded without an admitted phase")
	}
	if err := allocator.AdmitPhase(domain.RunStateDiagnosing); err != nil {
		t.Fatalf("AdmitPhase: %v", err)
	}
	if _, err := allocator.Consume(domain.RunStatePlanning, domain.BudgetAmount{ModelCalls: 1}); err == nil {
		t.Fatal("Consume accepted a non-current phase")
	}
	if _, err := allocator.Consume(domain.RunStateDiagnosing, domain.BudgetAmount{ModelCalls: -1}); err == nil {
		t.Fatal("Consume accepted a negative amount")
	}
	if _, err := allocator.Available(domain.RunStatePlanning); err == nil {
		t.Fatal("Available accepted a non-current phase")
	}
}

func TestPhaseBudgetPlanAvailableAndProjection(t *testing.T) {
	ceiling := testBudgetCeiling()
	plan := testBudgetPlan(
		t,
		ceiling,
		allPhasesSoft(domain.BudgetAmount{ModelCalls: 10}),
		map[domain.RunState]domain.BudgetAmount{domain.RunStatePlanning: {ModelCalls: 5}},
		domain.BudgetAmount{ModelCalls: 5},
	)
	allocator, err := newPhaseBudgetPlan(plan)
	if err != nil {
		t.Fatalf("newPhaseBudgetPlan: %v", err)
	}
	if err := allocator.AdmitPhase(domain.RunStateDiagnosing); err != nil {
		t.Fatalf("AdmitPhase: %v", err)
	}
	// Available = 剩余 soft (10) + pool (40) = 50。
	available, err := allocator.Available(domain.RunStateDiagnosing)
	if err != nil || available.ModelCalls != 50 {
		t.Fatalf("Available = %+v, %v; want 50 model calls", available, err)
	}
	if _, err := allocator.Consume(domain.RunStateDiagnosing, domain.BudgetAmount{ModelCalls: 4}); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	projection := allocator.Projection()
	if projection.Phase != domain.RunStateDiagnosing {
		t.Fatalf("projection phase = %q, want diagnosing", projection.Phase)
	}
	if projection.Consumed.ModelCalls != 4 || projection.Remaining.ModelCalls != 96 {
		t.Fatalf("consumed/remaining = %d/%d, want 4/96", projection.Consumed.ModelCalls, projection.Remaining.ModelCalls)
	}
	if len(projection.SoftRemaining) != domain.MaxBudgetPlanPhases {
		t.Fatalf("soft remaining entries = %d, want %d", len(projection.SoftRemaining), domain.MaxBudgetPlanPhases)
	}
	if projection.SoftRemaining[0].Phase != domain.RunStateDiagnosing || projection.SoftRemaining[0].Remaining.ModelCalls != 6 {
		t.Fatalf("diagnosing soft remaining = %+v, want 6", projection.SoftRemaining[0])
	}
	if projection.Unreserved.ModelCalls != 40 {
		t.Fatalf("unreserved = %d, want 40", projection.Unreserved.ModelCalls)
	}
	if projection.Ceiling != ceiling {
		t.Fatalf("projection ceiling = %+v, want unchanged %+v", projection.Ceiling, ceiling)
	}
	if err := projection.Validate(); err != nil {
		t.Fatalf("projection failed validation: %v", err)
	}
}
