package application

import (
	"fmt"

	"fixthe/backend/internal/modules/remediation/domain"
)

// 默认 soft-budget 分配：每个 phase 的 soft allocation 占 ceiling 的 15%，
// 每个 later phase 的 reserve 占 5%，recovery reserve 占 5%；合计恰为 100%，
// 默认 unreserved pool 为 0（早期 phase 越过 soft allocation 即进入
// recoverable 信号，绝不触碰 reserves）。逐维度向下取整，小 ceiling 下
// reserve 可能为 0，但 validation 仍保证总和不超过 ceiling。
const (
	defaultSoftAllocationPercent  = 15
	defaultLaterReservePercent    = 5
	defaultRecoveryReservePercent = 5
)

// softBudgetResult 是 soft-budget consume 的可恢复信号（D7/D16）：区分
// within_allocation | soft_crossed | reserve_touched。它永远不是 hard
// budgetExhaustionReason：只有 global hard ceiling 的 runBudget 才会产生
// budget_exhausted 终态（AC10）。
type softBudgetResult string

const (
	softWithinAllocation softBudgetResult = "within_allocation"
	softCrossed          softBudgetResult = "soft_crossed"
	softReserveTouched   softBudgetResult = "reserve_touched"
)

// String 返回稳定的字符串值，供 recovery projection 与日志使用。
func (r softBudgetResult) String() string { return string(r) }

// phaseBudgetPlan 是 D7 reserved-budget 模型的确定性 allocator：phase 沿
// BudgetPhaseOrder 前进（forward-only，不允许回到已 close 或已越过的
// phase）；每个 phase 先消耗自己的 soft allocation，再借用 unreserved pool；
// standing later-phase/recovery reserves 永不被早期 phase 借用（AC10）。
// ClosePhase 把未用 soft 与已 close phase 的 reserve 释放回 pool（unused
// capacity flows forward）。本 allocator 只产生 recoverable 信号，不触碰
// hard ceiling 判定；global hard ceiling 由 runBudget 独立、unchanged 地
// 执行。
type phaseBudgetPlan struct {
	plan     domain.BudgetPlanV1
	consumed map[domain.RunState]domain.BudgetAmount
	closed   map[domain.RunState]bool
	current  domain.RunState
	frontier int
}

// newPhaseBudgetPlan 构造 allocator；plan 必须已通过 domain Validate。
func newPhaseBudgetPlan(plan domain.BudgetPlanV1) (*phaseBudgetPlan, error) {
	if err := plan.Validate(); err != nil {
		return nil, fmt.Errorf("invalid budget plan: %w", err)
	}
	return &phaseBudgetPlan{
		plan:     plan,
		consumed: make(map[domain.RunState]domain.BudgetAmount, domain.MaxBudgetPlanPhases),
		closed:   make(map[domain.RunState]bool, domain.MaxBudgetPlanPhases),
		frontier: -1,
	}, nil
}

// restorePhaseBudgetPlan reconstructs the allocator from its stable recovery
// contract. Validation happens before any mutable state is allocated.
func restorePhaseBudgetPlan(recovery domain.BudgetPlanRecoveryV1) (*phaseBudgetPlan, error) {
	if err := recovery.Validate(); err != nil {
		return nil, fmt.Errorf("invalid budget recovery: %w", err)
	}
	plan, err := newPhaseBudgetPlan(recovery.Plan)
	if err != nil {
		return nil, err
	}
	for _, entry := range recovery.Consumed {
		plan.consumed[entry.Phase] = entry.Amount
	}
	for _, phase := range recovery.ClosedPhases {
		plan.closed[phase] = true
	}
	plan.current = recovery.CurrentPhase
	plan.frontier = recovery.Frontier
	return plan, nil
}

// AdmitPhase 进入 phase：只允许 budget phase、未 close、且不早于已到达的
// frontier（forward-only）。重复 admit 当前 phase 是幂等 no-op；close 后
// 只能 admit 更后的 phase。
func (p *phaseBudgetPlan) AdmitPhase(phase domain.RunState) error {
	index := domain.BudgetPhaseIndex(phase)
	if index < 0 {
		return fmt.Errorf("phase %q is not a budget phase", phase)
	}
	if p.closed[phase] {
		return fmt.Errorf("phase %q is already closed", phase)
	}
	if p.current != "" && p.current != phase {
		return fmt.Errorf("current phase %q must be closed before admitting %q", p.current, phase)
	}
	if index < p.frontier {
		return fmt.Errorf("phase %q is earlier than the reached phase frontier", phase)
	}
	p.current = phase
	if index > p.frontier {
		p.frontier = index
	}
	return nil
}

// Consume 把 amount 记入当前 phase 的消耗并返回 recoverable soft-budget
// 信号：先消耗 soft allocation（within_allocation），越过 soft 后借用
// unreserved pool（soft_crossed），借用越过 pool 即触碰 standing reserves
// （reserve_touched）。phase 必须等于当前 phase；ElapsedSeconds 由调用方按
// runBudget.elapsed() 补充。hard ceiling 的判定不在此方法内。
func (p *phaseBudgetPlan) Consume(phase domain.RunState, amount domain.BudgetAmount) (softBudgetResult, error) {
	if phase != p.current {
		return "", fmt.Errorf("consume phase %q is not the current phase %q", phase, p.current)
	}
	if err := amount.Validate(); err != nil {
		return "", err
	}
	p.consumed[phase] = p.consumed[phase].Add(amount)
	return p.signalFor(phase), nil
}

// ClosePhase 结束当前 phase（phase boundary checkpoint）：标记 closed 并把
// 未用 soft 与其 reserve 释放回 unreserved pool；调用后当前 phase 置空，
// 只能 admit 更后的 phase。
func (p *phaseBudgetPlan) ClosePhase(phase domain.RunState) error {
	if phase != p.current {
		return fmt.Errorf("close phase %q is not the current phase %q", phase, p.current)
	}
	p.closed[phase] = true
	p.current = ""
	return nil
}

// Available 返回当前 phase 在不触发 reserve_touched 的前提下还能消耗的量：
// 剩余 soft allocation（钳制非负）+ unreserved pool。phase 必须为当前 phase。
func (p *phaseBudgetPlan) Available(phase domain.RunState) (domain.BudgetAmount, error) {
	if phase != p.current {
		return domain.BudgetAmount{}, fmt.Errorf("available phase %q is not the current phase %q", phase, p.current)
	}
	remaining := p.softFor(phase).Sub(p.consumed[phase]).ClampNonNegative()
	return remaining.Add(p.unreservedPool()), nil
}

// Projection 返回当前时间点的软预算投影（D8/D23）：consumed、相对 hard
// ceiling 的 remaining、per-phase 剩余 soft、standing later-phase reserves
// 与 recovery reserve、unreserved pool 与不变的 ceiling。只含数值与 phase
// 标识，string-safe。
func (p *phaseBudgetPlan) Projection() domain.BudgetPlanProjection {
	var consumed domain.BudgetAmount
	for _, phase := range domain.BudgetPhaseOrder() {
		consumed = consumed.Add(p.consumed[phase])
	}
	ceiling := p.plan.Ceiling.BudgetAmount()
	softRemaining := make([]domain.PhaseSoftRemaining, 0, domain.MaxBudgetPlanPhases)
	for _, phase := range domain.BudgetPhaseOrder() {
		softRemaining = append(softRemaining, domain.PhaseSoftRemaining{
			Phase:     phase,
			Remaining: p.softFor(phase).Sub(p.consumed[phase]).ClampNonNegative(),
		})
	}
	reserves := make([]domain.PhaseReserve, 0, len(p.plan.LaterReserves))
	for _, phase := range domain.BudgetPhaseOrder() {
		if !p.isStanding(phase) {
			continue
		}
		if reserve := p.reserveFor(phase); !reserve.IsZero() {
			reserves = append(reserves, domain.PhaseReserve{Phase: phase, Min: reserve})
		}
	}
	return domain.BudgetPlanProjection{
		SchemaVersion: domain.BudgetPlanSchemaVersionV1,
		Phase:         p.current,
		Consumed:      consumed,
		Remaining:     ceiling.Sub(consumed).ClampNonNegative(),
		SoftRemaining: softRemaining,
		Reserves:      reserves,
		Recovery:      p.plan.Recovery,
		Unreserved:    p.unreservedPool(),
		Ceiling:       p.plan.Ceiling,
	}
}

// Recovery returns the complete stable state required to serialize/restart and
// continue without changing allocator decisions.
func (p *phaseBudgetPlan) Recovery() domain.BudgetPlanRecoveryV1 {
	recovery := domain.BudgetPlanRecoveryV1{
		SchemaVersion: domain.BudgetPlanSchemaVersionV1,
		Plan:          p.plan,
		CurrentPhase:  p.current,
		Frontier:      p.frontier,
	}
	for _, phase := range domain.BudgetPhaseOrder() {
		if amount, ok := p.consumed[phase]; ok && !amount.IsZero() {
			recovery.Consumed = append(recovery.Consumed, domain.PhaseBudgetConsumption{Phase: phase, Amount: amount})
		}
		if p.closed[phase] {
			recovery.ClosedPhases = append(recovery.ClosedPhases, phase)
		}
	}
	return recovery
}

// signalFor 计算当前 phase 消耗后的最强信号：reserve_touched > soft_crossed
// > within_allocation。任一维度越过当前 phase soft 即为 soft_crossed；所有
// phase 累计借用超过可释放的 pool capacity 时升级为 reserve_touched。借用量
// 必须跨 phase 汇总，防止每个 phase 重复使用同一份 unreserved capacity。
func (p *phaseBudgetPlan) signalFor(phase domain.RunState) softBudgetResult {
	soft := p.softFor(phase)
	consumed := p.consumed[phase]
	result := softWithinAllocation
	for _, axis := range []struct {
		consumed, soft int64
	}{
		{consumed.ElapsedSeconds, soft.ElapsedSeconds},
		{consumed.ModelCalls, soft.ModelCalls},
		{consumed.ModelCostCents, soft.ModelCostCents},
		{consumed.ToolCalls, soft.ToolCalls},
		{consumed.EvidenceBytes, soft.EvidenceBytes},
		{consumed.RepositoryBytes, soft.RepositoryBytes},
	} {
		if axis.consumed > axis.soft {
			result = softCrossed
			break
		}
	}
	capacity := p.unreservedCapacity()
	borrowed := p.borrowedUnreserved()
	for _, axis := range []struct {
		borrowed, capacity int64
	}{
		{borrowed.ElapsedSeconds, capacity.ElapsedSeconds},
		{borrowed.ModelCalls, capacity.ModelCalls},
		{borrowed.ModelCostCents, capacity.ModelCostCents},
		{borrowed.ToolCalls, capacity.ToolCalls},
		{borrowed.EvidenceBytes, capacity.EvidenceBytes},
		{borrowed.RepositoryBytes, capacity.RepositoryBytes},
	} {
		if axis.borrowed > axis.capacity {
			return softReserveTouched
		}
	}
	return result
}

// strongerSoftResult 返回更强的信号：reserve_touched > soft_crossed >
// within_allocation。
func strongerSoftResult(a, b softBudgetResult) softBudgetResult {
	rank := func(result softBudgetResult) int {
		switch result {
		case softReserveTouched:
			return 2
		case softCrossed:
			return 1
		default:
			return 0
		}
	}
	if rank(b) > rank(a) {
		return b
	}
	return a
}

// unreservedPool 返回扣除所有 phase 累计借用后的剩余 pool。capacity 来自
// 初始未分配容量以及已 close phase 释放的未用 soft/reserve；borrowed 必须
// 全局扣除，不能在后续 phase 重新出现。
func (p *phaseBudgetPlan) unreservedPool() domain.BudgetAmount {
	return p.unreservedCapacity().Sub(p.borrowedUnreserved()).ClampNonNegative()
}

// unreservedCapacity 返回当前时点可供所有 phase 共享的 pool 总容量。
func (p *phaseBudgetPlan) unreservedCapacity() domain.BudgetAmount {
	capacity := p.plan.Ceiling.BudgetAmount().Sub(p.totalAllocated())
	for _, phase := range domain.BudgetPhaseOrder() {
		if !p.closed[phase] {
			continue
		}
		capacity = capacity.Add(p.softFor(phase).Sub(p.consumed[phase]).ClampNonNegative())
		capacity = capacity.Add(p.reserveFor(phase))
	}
	return capacity
}

// borrowedUnreserved 汇总所有 phase 超过自身 soft allocation 的消耗。
func (p *phaseBudgetPlan) borrowedUnreserved() domain.BudgetAmount {
	var borrowed domain.BudgetAmount
	for _, phase := range domain.BudgetPhaseOrder() {
		borrowed = borrowed.Add(p.consumed[phase].Sub(p.softFor(phase)).ClampNonNegative())
	}
	return borrowed
}

// totalAllocated 是全部 soft allocation + later-phase reserves + recovery
// reserve 之和；domain Validate 保证其不超过 ceiling。
func (p *phaseBudgetPlan) totalAllocated() domain.BudgetAmount {
	total := p.plan.Recovery.Min
	for _, allocation := range p.plan.Phases {
		total = total.Add(allocation.Soft)
	}
	for _, reserve := range p.plan.LaterReserves {
		total = total.Add(reserve.Min)
	}
	return total
}

// softFor 返回 phase 的 soft allocation；plan 保证五个 phase 都存在。
func (p *phaseBudgetPlan) softFor(phase domain.RunState) domain.BudgetAmount {
	for _, allocation := range p.plan.Phases {
		if allocation.Phase == phase {
			return allocation.Soft
		}
	}
	return domain.BudgetAmount{}
}

// reserveFor 返回 phase 的 later-phase reserve；未配置时为 0。
func (p *phaseBudgetPlan) reserveFor(phase domain.RunState) domain.BudgetAmount {
	for _, reserve := range p.plan.LaterReserves {
		if reserve.Phase == phase {
			return reserve.Min
		}
	}
	return domain.BudgetAmount{}
}

// isStanding 报告 phase 的 reserve 是否仍 standing：phase 未 close 且排在
// 当前 phase 之后（尚未运行）；无当前 phase 时，未 close 的 phase 全部
// standing（已 close 的 reserve 已释放进 unreserved pool）。
func (p *phaseBudgetPlan) isStanding(phase domain.RunState) bool {
	if p.closed[phase] {
		return false
	}
	if p.current == "" {
		return true
	}
	return domain.BudgetPhaseIndex(phase) > domain.BudgetPhaseIndex(p.current)
}

// NewBudgetPlan 构造规范化的 BudgetPlanV1：ceiling 先用 DefaultBudgetLimits
// 按维度填充零值；缺失（或全零）的 phase soft allocation 填 ceiling 固定
// 百分比的保守默认值，later-phase reserve 与 recovery reserve 同理；已显式
// 提供的非零值保持不变。结果保证通过 domain Validate。
func NewBudgetPlan(
	ceiling domain.BudgetLimits,
	phases []domain.PhaseBudgetAllocation,
	laterReserves []domain.PhaseReserve,
	recovery domain.RecoveryReserve,
) (domain.BudgetPlanV1, error) {
	ceiling = normalizeBudgetLimits(ceiling)
	ceilingAmount := ceiling.BudgetAmount()
	plan := domain.BudgetPlanV1{
		SchemaVersion: domain.BudgetPlanSchemaVersionV1,
		Ceiling:       ceiling,
		Phases:        make([]domain.PhaseBudgetAllocation, 0, domain.MaxBudgetPlanPhases),
		LaterReserves: make([]domain.PhaseReserve, 0, domain.MaxBudgetPlanPhases),
		Recovery:      recovery,
	}
	providedSoft := make(map[domain.RunState]domain.BudgetAmount, len(phases))
	for _, allocation := range phases {
		providedSoft[allocation.Phase] = allocation.Soft
	}
	for _, phase := range domain.BudgetPhaseOrder() {
		soft, ok := providedSoft[phase]
		if !ok || soft.IsZero() {
			soft = percentOf(ceilingAmount, defaultSoftAllocationPercent)
		}
		plan.Phases = append(plan.Phases, domain.PhaseBudgetAllocation{Phase: phase, Soft: soft})
	}
	providedReserves := make(map[domain.RunState]domain.BudgetAmount, len(laterReserves))
	for _, reserve := range laterReserves {
		providedReserves[reserve.Phase] = reserve.Min
	}
	// diagnosing 是第一个 phase，不存在 "更早的 phase"，其 reserve 没有保护
	// 对象，因此默认不为它配置 reserve；显式传入的值仍然保留。
	for _, phase := range domain.BudgetPhaseOrder() {
		if phase == domain.RunStateDiagnosing {
			continue
		}
		min, ok := providedReserves[phase]
		if !ok || min.IsZero() {
			min = percentOf(ceilingAmount, defaultLaterReservePercent)
		}
		plan.LaterReserves = append(plan.LaterReserves, domain.PhaseReserve{Phase: phase, Min: min})
	}
	if plan.Recovery.Min.IsZero() {
		plan.Recovery.Min = percentOf(ceilingAmount, defaultRecoveryReservePercent)
	}
	if err := plan.Validate(); err != nil {
		return domain.BudgetPlanV1{}, err
	}
	return plan, nil
}

// percentOf 返回 ceiling 每个维度固定百分比（向下取整）的默认分配。
func percentOf(ceiling domain.BudgetAmount, percent int) domain.BudgetAmount {
	return domain.BudgetAmount{
		ElapsedSeconds:  ceiling.ElapsedSeconds * int64(percent) / 100,
		ModelCalls:      ceiling.ModelCalls * int64(percent) / 100,
		ModelCostCents:  ceiling.ModelCostCents * int64(percent) / 100,
		ToolCalls:       ceiling.ToolCalls * int64(percent) / 100,
		EvidenceBytes:   ceiling.EvidenceBytes * int64(percent) / 100,
		RepositoryBytes: ceiling.RepositoryBytes * int64(percent) / 100,
	}
}
