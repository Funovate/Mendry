package domain

import (
	"fmt"
	"time"
)

// BudgetPlan 常量：schema 标识与 D7 soft-budget phase 集合。
const (
	// BudgetPlanSchemaVersionV1 是 BudgetPlanV1 与 BudgetPlanProjection 的 schema 标识。
	BudgetPlanSchemaVersionV1 = "v1"
	// MaxBudgetPlanPhases 是 D7 soft-budget phase 的数量（五个）。
	MaxBudgetPlanPhases = 5
)

// BudgetPhaseOrder 返回 D7 的固定 phase 顺序：diagnosing 最早，publishing 最晚。
// 每次调用返回新切片，调用方修改不影响后续调用；recovery 与 exhaustion 校验
// 按此顺序判断哪些 phase 是 "later phases"。
func BudgetPhaseOrder() []RunState {
	return []RunState{
		RunStateDiagnosing,
		RunStatePlanning,
		RunStatePatching,
		RunStateValidating,
		RunStatePublishing,
	}
}

// BudgetPhaseIndex 返回 phase 在 BudgetPhaseOrder 中的序号；非 budget phase
// 返回 -1。
func BudgetPhaseIndex(state RunState) int {
	for index, phase := range BudgetPhaseOrder() {
		if phase == state {
			return index
		}
	}
	return -1
}

// IsBudgetPhase 报告 state 是否属于 D7 soft-budget 的五个 phase。
func IsBudgetPhase(state RunState) bool {
	return BudgetPhaseIndex(state) >= 0
}

// BudgetPhaseFor 把任意 RunState 规范化为 budget phase：collecting_more_context
// 属于诊断循环，映射为 diagnosing。非 budget phase 返回 ok=false。
func BudgetPhaseFor(state RunState) (RunState, bool) {
	if IsBudgetPhase(state) {
		return state, true
	}
	if state == RunStateCollectingMoreContext {
		return RunStateDiagnosing, true
	}
	return "", false
}

// BudgetAmount 是 soft allocation / reserve 的按资源维度数量；维度与
// BudgetCounters/BudgetLimits 对齐，但 model tokens 仍只记录、不参与
// soft budget（与 hard ceiling 的语义一致）。
type BudgetAmount struct {
	ElapsedSeconds  int64 `json:"elapsedSeconds"`
	ModelCalls      int64 `json:"modelCalls"`
	ModelCostCents  int64 `json:"modelCostCents"`
	ToolCalls       int64 `json:"toolCalls"`
	EvidenceBytes   int64 `json:"evidenceBytes"`
	RepositoryBytes int64 `json:"repositoryBytes"`
}

// IsZero 报告所有维度是否为零。
func (a BudgetAmount) IsZero() bool {
	return a.ElapsedSeconds == 0 && a.ModelCalls == 0 && a.ModelCostCents == 0 &&
		a.ToolCalls == 0 && a.EvidenceBytes == 0 && a.RepositoryBytes == 0
}

// Validate 校验所有维度非负。
func (a BudgetAmount) Validate() error {
	for _, check := range []struct {
		name  string
		value int64
	}{
		{name: "elapsed seconds", value: a.ElapsedSeconds},
		{name: "model calls", value: a.ModelCalls},
		{name: "model cost cents", value: a.ModelCostCents},
		{name: "tool calls", value: a.ToolCalls},
		{name: "evidence bytes", value: a.EvidenceBytes},
		{name: "repository bytes", value: a.RepositoryBytes},
	} {
		if check.value < 0 {
			return fmt.Errorf("budget amount %s must not be negative", check.name)
		}
	}
	return nil
}

// Add 返回逐维度之和。
func (a BudgetAmount) Add(b BudgetAmount) BudgetAmount {
	return BudgetAmount{
		ElapsedSeconds:  a.ElapsedSeconds + b.ElapsedSeconds,
		ModelCalls:      a.ModelCalls + b.ModelCalls,
		ModelCostCents:  a.ModelCostCents + b.ModelCostCents,
		ToolCalls:       a.ToolCalls + b.ToolCalls,
		EvidenceBytes:   a.EvidenceBytes + b.EvidenceBytes,
		RepositoryBytes: a.RepositoryBytes + b.RepositoryBytes,
	}
}

// Sub 返回逐维度差 a-b；允许负值，调用方按语义钳制（如 remaining 投影）。
func (a BudgetAmount) Sub(b BudgetAmount) BudgetAmount {
	return BudgetAmount{
		ElapsedSeconds:  a.ElapsedSeconds - b.ElapsedSeconds,
		ModelCalls:      a.ModelCalls - b.ModelCalls,
		ModelCostCents:  a.ModelCostCents - b.ModelCostCents,
		ToolCalls:       a.ToolCalls - b.ToolCalls,
		EvidenceBytes:   a.EvidenceBytes - b.EvidenceBytes,
		RepositoryBytes: a.RepositoryBytes - b.RepositoryBytes,
	}
}

// ClampNonNegative 把负维度钳制为 0；用于 remaining 类投影，避免超额消耗
// 显示为负余额。
func (a BudgetAmount) ClampNonNegative() BudgetAmount {
	if a.ElapsedSeconds < 0 {
		a.ElapsedSeconds = 0
	}
	if a.ModelCalls < 0 {
		a.ModelCalls = 0
	}
	if a.ModelCostCents < 0 {
		a.ModelCostCents = 0
	}
	if a.ToolCalls < 0 {
		a.ToolCalls = 0
	}
	if a.EvidenceBytes < 0 {
		a.EvidenceBytes = 0
	}
	if a.RepositoryBytes < 0 {
		a.RepositoryBytes = 0
	}
	return a
}

// AsMap 返回有界 key 的按维度投影，供 D5 envelope 的 remainingBudget 与
// checkpoint budget 快照使用；key 长度在 maxRecoveryBudgetKeyLength /
// maxCheckpointBudgetKeyLength 之内。
func (a BudgetAmount) AsMap() map[string]int64 {
	return map[string]int64{
		"elapsed_seconds":  a.ElapsedSeconds,
		"model_calls":      a.ModelCalls,
		"model_cost_cents": a.ModelCostCents,
		"tool_calls":       a.ToolCalls,
		"evidence_bytes":   a.EvidenceBytes,
		"repository_bytes": a.RepositoryBytes,
	}
}

// BudgetAmount 把 BudgetLimits 的 hard ceiling 转换为 BudgetAmount：MaxElapsed
// 按秒取整，与 BudgetCounters.ElapsedSeconds 的单位一致。
func (l BudgetLimits) BudgetAmount() BudgetAmount {
	return BudgetAmount{
		ElapsedSeconds:  int64(l.MaxElapsed / time.Second),
		ModelCalls:      l.MaxModelCalls,
		ModelCostCents:  l.MaxModelCostCents,
		ToolCalls:       l.MaxToolCalls,
		EvidenceBytes:   l.MaxEvidenceBytes,
		RepositoryBytes: l.MaxRepositoryBytes,
	}
}

// BudgetAmount 把 Effect 的资源维度映射为 BudgetAmount：model tokens 只记录、
// 不参与 soft budget，因此被丢弃；ElapsedSeconds 为 0，由调用方按
// runBudget.elapsed() 补充。
func (e Effect) BudgetAmount() BudgetAmount {
	return BudgetAmount{
		ModelCalls:      int64(e.ModelCalls),
		ModelCostCents:  e.ModelCostCents,
		ToolCalls:       int64(e.ToolCalls),
		EvidenceBytes:   e.EvidenceBytes,
		RepositoryBytes: e.RepositoryBytes,
	}
}

// PhaseBudgetAllocation 是单个 phase 的 soft allocation（D7）；Phase 必须属于
// BudgetPhaseOrder 的五个 phase。
type PhaseBudgetAllocation struct {
	Phase RunState     `json:"phase"`
	Soft  BudgetAmount `json:"soft"`
}

// PhaseReserve 是单个 later phase 的最小预留量（D7 "minimum later-phase
// reserves"）：排在它前面的 phase 不能借用该容量；phase 运行并 close 后其
// reserve 释放回 unreserved pool。
type PhaseReserve struct {
	Phase RunState     `json:"phase"`
	Min   BudgetAmount `json:"min"`
}

// RecoveryReserve 是 recovery 的最小预留量（D7 "a recovery reserve"）；
// 它始终 standing，普通 phase 不可借用。
type RecoveryReserve struct {
	Min BudgetAmount `json:"min"`
}

// BudgetPlanV1 是 D7 的 provider-neutral soft-budget 计划快照：Ceiling 是
// 不变的 global hard ceiling（BudgetLimits）；Phases 固定为五个 phase 的
// soft allocation；LaterReserves 是 later-phase 的最小预留；Recovery 是
// recovery 预留。每维度 soft+reserves+recovery 之和不得超过 ceiling，
// 差值即 unreserved pool（早期 phase 可借用的容量）。
type BudgetPlanV1 struct {
	SchemaVersion string                  `json:"schemaVersion"`
	Ceiling       BudgetLimits            `json:"ceiling"`
	Phases        []PhaseBudgetAllocation `json:"phases"`
	LaterReserves []PhaseReserve          `json:"laterReserves"`
	Recovery      RecoveryReserve         `json:"recovery"`
}

// PhaseBudgetConsumption is the stable per-phase usage representation used by
// restart recovery. Entries are stored in BudgetPhaseOrder, never map order.
type PhaseBudgetConsumption struct {
	Phase  RunState     `json:"phase"`
	Amount BudgetAmount `json:"amount"`
}

// BudgetPlanRecoveryV1 is the complete, provider-neutral allocator state. It
// intentionally describes public invariants instead of serializing private
// phaseBudgetPlan fields.
type BudgetPlanRecoveryV1 struct {
	SchemaVersion string                   `json:"schemaVersion"`
	Plan          BudgetPlanV1             `json:"plan"`
	Consumed      []PhaseBudgetConsumption `json:"consumed"`
	ClosedPhases  []RunState               `json:"closedPhases"`
	CurrentPhase  RunState                 `json:"currentPhase"`
	Frontier      int                      `json:"frontier"`
}

// Validate ensures recovery data can reconstruct one unambiguous allocator.
func (r BudgetPlanRecoveryV1) Validate() error {
	if r.SchemaVersion != BudgetPlanSchemaVersionV1 {
		return fmt.Errorf("unknown budget recovery schema version %q", r.SchemaVersion)
	}
	if err := r.Plan.Validate(); err != nil {
		return fmt.Errorf("budget recovery plan: %w", err)
	}
	if r.Frontier < -1 || r.Frontier >= MaxBudgetPlanPhases {
		return fmt.Errorf("budget recovery frontier %d is invalid", r.Frontier)
	}
	seenConsumed := make(map[RunState]bool, len(r.Consumed))
	lastIndex := -1
	for _, entry := range r.Consumed {
		index := BudgetPhaseIndex(entry.Phase)
		if index < 0 || index <= lastIndex || index > r.Frontier || seenConsumed[entry.Phase] {
			return fmt.Errorf("budget recovery consumption order is invalid at phase %q", entry.Phase)
		}
		if err := entry.Amount.Validate(); err != nil {
			return fmt.Errorf("budget recovery phase %q: %w", entry.Phase, err)
		}
		seenConsumed[entry.Phase] = true
		lastIndex = index
	}
	closed := make(map[RunState]bool, len(r.ClosedPhases))
	lastIndex = -1
	for _, phase := range r.ClosedPhases {
		index := BudgetPhaseIndex(phase)
		if index < 0 || index <= lastIndex || index > r.Frontier || closed[phase] {
			return fmt.Errorf("budget recovery closed phase order is invalid at phase %q", phase)
		}
		closed[phase] = true
		lastIndex = index
	}
	if r.CurrentPhase == "" {
		if r.Frontier >= 0 && !closed[BudgetPhaseOrder()[r.Frontier]] {
			return fmt.Errorf("budget recovery frontier phase must be current or closed")
		}
		return nil
	}
	currentIndex := BudgetPhaseIndex(r.CurrentPhase)
	if currentIndex < 0 || currentIndex != r.Frontier || closed[r.CurrentPhase] {
		return fmt.Errorf("budget recovery current phase %q conflicts with frontier", r.CurrentPhase)
	}
	return nil
}

// Validate 校验 BudgetPlanV1 的 schema、phase 集合与维度边界：
// ceiling 各维度必须为正（elapsed 至少 1 秒，0 表示未配置，调用方应先归一化）；
// Phases 必须恰好覆盖五个 budget phase 且无重复；reserves 的 phase 合法且无
// 重复；所有数量非负；每维度 soft+reserves+recovery ≤ ceiling。
func (p BudgetPlanV1) Validate() error {
	if p.SchemaVersion != BudgetPlanSchemaVersionV1 {
		return fmt.Errorf("unknown budget plan schema version %q", p.SchemaVersion)
	}
	if err := validateBudgetCeiling(p.Ceiling); err != nil {
		return err
	}
	if len(p.Phases) != MaxBudgetPlanPhases {
		return fmt.Errorf("budget plan requires exactly %d phase allocations", MaxBudgetPlanPhases)
	}
	seen := make(map[RunState]bool, len(p.Phases))
	for _, allocation := range p.Phases {
		if !IsBudgetPhase(allocation.Phase) {
			return fmt.Errorf("budget plan phase %q is not a budget phase", allocation.Phase)
		}
		if seen[allocation.Phase] {
			return fmt.Errorf("budget plan phase %q is duplicated", allocation.Phase)
		}
		seen[allocation.Phase] = true
		if err := allocation.Soft.Validate(); err != nil {
			return fmt.Errorf("budget plan phase %q: %v", allocation.Phase, err)
		}
	}
	reserveSeen := make(map[RunState]bool, len(p.LaterReserves))
	for _, reserve := range p.LaterReserves {
		if !IsBudgetPhase(reserve.Phase) {
			return fmt.Errorf("budget plan reserve phase %q is not a budget phase", reserve.Phase)
		}
		if reserveSeen[reserve.Phase] {
			return fmt.Errorf("budget plan reserve phase %q is duplicated", reserve.Phase)
		}
		reserveSeen[reserve.Phase] = true
		if err := reserve.Min.Validate(); err != nil {
			return fmt.Errorf("budget plan reserve phase %q: %v", reserve.Phase, err)
		}
	}
	if err := p.Recovery.Min.Validate(); err != nil {
		return fmt.Errorf("budget plan recovery reserve: %v", err)
	}
	if err := p.validateWithinCeiling(); err != nil {
		return err
	}
	return nil
}

// validateBudgetCeiling 要求 ceiling 每维度为正；0 表示未配置，domain 计划
// 只接受已归一化的具体 ceiling（application 构造器用 DefaultBudgetLimits 填充）。
func validateBudgetCeiling(ceiling BudgetLimits) error {
	if ceiling.MaxElapsed < time.Second {
		return fmt.Errorf("budget plan ceiling elapsed must be at least one second")
	}
	if ceiling.MaxModelCalls < 1 || ceiling.MaxModelCostCents < 1 ||
		ceiling.MaxToolCalls < 1 || ceiling.MaxEvidenceBytes < 1 || ceiling.MaxRepositoryBytes < 1 {
		return fmt.Errorf("budget plan ceiling dimensions must be positive")
	}
	return nil
}

// validateWithinCeiling 逐维度校验 soft+later reserves+recovery 不超过
// ceiling；这是 D7 的硬性计划约束，防止早期 phase 通过超量 soft allocation
// 间接消耗 later phase / recovery 的容量。
func (p BudgetPlanV1) validateWithinCeiling() error {
	var soft BudgetAmount
	for _, allocation := range p.Phases {
		soft = soft.Add(allocation.Soft)
	}
	reserved := p.Recovery.Min
	for _, reserve := range p.LaterReserves {
		reserved = reserved.Add(reserve.Min)
	}
	ceiling := p.Ceiling.BudgetAmount()
	checks := []struct {
		name  string
		total int64
		max   int64
	}{
		{name: "elapsed", total: soft.ElapsedSeconds + reserved.ElapsedSeconds, max: ceiling.ElapsedSeconds},
		{name: "model calls", total: soft.ModelCalls + reserved.ModelCalls, max: ceiling.ModelCalls},
		{name: "model cost", total: soft.ModelCostCents + reserved.ModelCostCents, max: ceiling.ModelCostCents},
		{name: "tool calls", total: soft.ToolCalls + reserved.ToolCalls, max: ceiling.ToolCalls},
		{name: "evidence bytes", total: soft.EvidenceBytes + reserved.EvidenceBytes, max: ceiling.EvidenceBytes},
		{name: "repository bytes", total: soft.RepositoryBytes + reserved.RepositoryBytes, max: ceiling.RepositoryBytes},
	}
	for _, check := range checks {
		if check.total > check.max {
			return fmt.Errorf("budget plan soft allocations and reserves (%d) exceed the %s ceiling (%d)", check.total, check.name, check.max)
		}
	}
	return nil
}

// PhaseSoftRemaining 是投影中单个 phase 的剩余 soft allocation（已钳制非负）。
type PhaseSoftRemaining struct {
	Phase     RunState     `json:"phase"`
	Remaining BudgetAmount `json:"remaining"`
}

// BudgetPlanProjection 是某个时间点的 soft-budget 快照投影（D8/D23）：
// per-phase 剩余 soft、standing later-phase/recovery reserves、consumed、
// unreserved pool 与不变的 global hard ceiling。只含数值与 phase 标识，
// 供 review/API 安全渲染，不暴露模型 trace 或凭据。
type BudgetPlanProjection struct {
	SchemaVersion string               `json:"schemaVersion"`
	Phase         RunState             `json:"phase"`
	Consumed      BudgetAmount         `json:"consumed"`
	Remaining     BudgetAmount         `json:"remaining"`
	SoftRemaining []PhaseSoftRemaining `json:"softRemaining"`
	Reserves      []PhaseReserve       `json:"reserves"`
	Recovery      RecoveryReserve      `json:"recovery"`
	Unreserved    BudgetAmount         `json:"unreserved"`
	Ceiling       BudgetLimits         `json:"ceiling"`
}

// Validate 校验投影的 schema、phase 与数值边界；投影由 allocator 产生，
// 本校验是 JSON/API 边界前的防御性检查，不校验与 allocator 状态的一致性。
func (p BudgetPlanProjection) Validate() error {
	if p.SchemaVersion != BudgetPlanSchemaVersionV1 {
		return fmt.Errorf("unknown budget plan projection schema version %q", p.SchemaVersion)
	}
	if p.Phase != "" && !IsBudgetPhase(p.Phase) {
		return fmt.Errorf("budget plan projection phase %q is not a budget phase", p.Phase)
	}
	for _, check := range []struct {
		label  string
		amount BudgetAmount
	}{
		{label: "consumed", amount: p.Consumed},
		{label: "remaining", amount: p.Remaining},
		{label: "unreserved", amount: p.Unreserved},
	} {
		if err := check.amount.Validate(); err != nil {
			return fmt.Errorf("budget plan projection %s: %v", check.label, err)
		}
	}
	if err := p.Recovery.Min.Validate(); err != nil {
		return fmt.Errorf("budget plan projection recovery: %v", err)
	}
	if len(p.SoftRemaining) != MaxBudgetPlanPhases {
		return fmt.Errorf("budget plan projection requires exactly %d soft remaining entries", MaxBudgetPlanPhases)
	}
	seen := make(map[RunState]bool, len(p.SoftRemaining))
	for _, entry := range p.SoftRemaining {
		if !IsBudgetPhase(entry.Phase) {
			return fmt.Errorf("budget plan projection soft remaining phase %q is not a budget phase", entry.Phase)
		}
		if seen[entry.Phase] {
			return fmt.Errorf("budget plan projection soft remaining phase %q is duplicated", entry.Phase)
		}
		seen[entry.Phase] = true
		if err := entry.Remaining.Validate(); err != nil {
			return fmt.Errorf("budget plan projection soft remaining phase %q: %v", entry.Phase, err)
		}
	}
	reserveSeen := make(map[RunState]bool, len(p.Reserves))
	for _, reserve := range p.Reserves {
		if !IsBudgetPhase(reserve.Phase) {
			return fmt.Errorf("budget plan projection reserve phase %q is not a budget phase", reserve.Phase)
		}
		if reserveSeen[reserve.Phase] {
			return fmt.Errorf("budget plan projection reserve phase %q is duplicated", reserve.Phase)
		}
		reserveSeen[reserve.Phase] = true
		if err := reserve.Min.Validate(); err != nil {
			return fmt.Errorf("budget plan projection reserve phase %q: %v", reserve.Phase, err)
		}
	}
	return nil
}
