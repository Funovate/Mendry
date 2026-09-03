package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"fixthe/backend/internal/modules/remediation/domain"
)

// maxResilientEvidenceIndex 是进程内 checkpoint evidence index 的条目上限，
// 与 domain 的 maxCheckpointEvidenceIndex 保持一致（该常量未导出，这里本地
// 镜像并在 domain Validate 处再次兜底）。
const (
	maxResilientEvidenceIndex = 256
	maxResilientRecoveries    = 64
)

// maxResilientActionRefs 是进程内 capability-bound action/evidence ref 的
// 条目上限；exhaustion proof 只能引用真实工具动作或已准入证据，不能引用
// recovery challenge 冒充调查动作。
const maxResilientActionRefs = 256

// 以下本地常量镜像 domain checkpoint 的字段级 rune 上限（domain 常量未导出）；
// boundedContinuationText 只做渲染裁剪，持久化边界仍由 domain Validate 兜底。
const (
	maxResilientGoalLength    = 1024
	maxResilientTextLength    = 512
	maxResilientIDLength      = 128
	maxResilientLocatorLength = 256
)

// resilientRunState 是一次 run 在 resilient_v1 模式下的进程内
// checkpoint/recovery 状态。它只存在于一次 drive 的生命周期内（经 context
// 传递），绝不放在 coordinator struct 上，避免并发 run 互相污染；legacy 或
// 未注入 checkpoint store 时该状态为 nil，所有新增路径都是 no-op。这是
// slice 5a 的 feature gate：所有 durable checkpoint / soft-budget /
// reconstruction 行为都从该状态派生。
type resilientRunState struct {
	store           domain.CheckpointStore
	run             domain.Run
	observedVersion int64
	reconstruction  string
	alloc           *phaseBudgetPlan
	lastSoftSignal  softBudgetResult
	// checkpointUnavailable 在一次 required append 失败后锁定。后续 terminal
	// transition 不再递归尝试同一不可靠存储，而是直接持久化
	// persistence_failure 终态。
	checkpointUnavailable bool
	// lastMirroredElapsed 是最近一次镜像消耗时看到的 run 累计秒数。runBudget
	// 的 elapsed 是累计设定（consume 时 set 当前值），而 phaseBudgetPlan 的
	// Consume 是增量累加；镜像时必须先转成增量，否则同一秒数被重复累加，
	// 导致 elapsed 维度提前触发 soft challenge 并污染 budget 投影（D7/AC10）。
	lastMirroredElapsed int64
	evidenceIndex       []domain.CheckpointEvidenceIndexItem
	recoveries          []domain.CheckpointRecovery
	recoveryAttempt     int
	// episodeRecoveryAttempts 是当前 recovery episode 已追加的 recovery
	// checkpoint 数（D8 attempt，F3）。它由 checkpointRun 维护：recovery 触发
	// checkpoint 递增，非 recovery checkpoint（durable 前进/终态）归零；与
	// recoveryEpisodeOpen 同源的 episode 边界语义，随 checkpoint 持久化以便
	// restart/续跑恢复精确 attempt。
	episodeRecoveryAttempts int
	// recoveryEpisodeOpen 表示 run 当前处于一次 active recovery episode：最近
	// 一次 durable checkpoint 是 recovery 触发（或 restart 从 checkpoint 恢复为
	// recovery）且尚未被非 recovery durable 前进结算。episode 打开后，第一个
	// 非 recovery checkpoint 只结算一次 recovery-success 并关闭；放弃型终态在
	// transitionWithReason 先静默关闭，不产生 success。
	recoveryEpisodeOpen bool
	// evidenceCorrectionAttempts 是本次 run 已回喂的 evidence_correction
	// challenge 次数，用作 challenge Attempt 字段的进度计数。
	evidenceCorrectionAttempts int
	// factCheckAttempts 记录 non-metadata failed fact checks；只有相同服务
	// fingerprint 连续无进展达到上限才转为 exhaustion proposal。
	factCheckAttempts        int
	factCheckNoProgress      int
	lastFactCheckFingerprint string
	nextActions              []string
	conversation             *AgentConversation
	analysisOnly             bool
	// lifecycle fields are bounded identities only; raw patch and validation output
	// remain content-addressed artifacts outside the checkpoint payload.
	lifecyclePlanID                  string
	lifecyclePhase                   domain.RunState
	workspace                        *domain.CheckpointWorkspace
	artifacts                        []domain.CheckpointArtifact
	validation                       *domain.CheckpointValidation
	publication                      *domain.CheckpointPublication
	publicationPolicy                *domain.CheckpointPublicationPolicy
	validationCommands               map[string]int64
	planFeedbackAttempts             int
	planFeedbackNoProgress           int
	lastPlanFeedbackFingerprint      string
	lifecycleRecoveryAttempts        int
	lastLifecycleRecoveryFingerprint string
	lastLifecycleRecoveryClass       string
	lifecycleChallengeAttempts       int
	lifecycleStopAttempts            int
	validationNoProgress             int
	lastValidationFingerprint        string
	checkpointNeedsRebuild           bool
	// exhaustionProposalAttempts 是本次 run 已请求的 exhaustion proposal
	// 次数，用作 exhaustion challenge 的 Attempt 字段进度计数。
	exhaustionProposalAttempts int
	// thresholdCheckpointLevelBytes 是下一个自动 byte-threshold checkpoint
	// （D2/R13 T1）的累计模型可见字节级别：从 contextPressureStepBytes（192
	// KiB）开始，每次 T1 命中推进一个步长，每级最多触发一次。进程本地：
	// restart/续跑后 conversation 重建，计数从首级确定性重新开始，无需持久化。
	thresholdCheckpointLevelBytes int64
	// lastAutoCheckpointBytes 是最近一次 durable checkpoint（任何 reason）时
	// conversation 的累计模型可见字节水位。自动 byte-threshold 触发只在累计
	// 字节自该水位增长 ≥ toolOutputPressureBytes 后允许，避免同一内容被多轮
	// 重复评估造成 checkpoint spam。
	lastAutoCheckpointBytes int64
	// consumedToolObservationSequence 是 T2 的 consumed 水位：最近一次 durable
	// checkpoint（任何 reason，含自动触发）落盘时 conversation 最新大工具观察
	// 的 monotone item sequence。T2 只允许出现于该水位之后的新大观察触发；
	// 之后任意 durable checkpoint（phase boundary / recovery / threshold /
	// process shutdown）都会消费/覆盖它——那时该观察的 working memory 已随
	// checkpoint 落盘，后续无关字节增长不得再让同一观察重新触发（T2 stale
	// re-fire 修正）。进程本地：restart/续跑后从 0 开始，配合重建的 fresh
	// conversation，不再对 restart 前已 checkpoint 的内容重复触发。
	consumedToolObservationSequence int64
	// actionRefsByCapability 把真实工具动作及其结果 evidence IDs 绑定到
	// D1 capability class。exhaustion validator 按 capability 校验，禁止用
	// recovery challenge 或另一类工具的 ref 冒充已尝试路径。
	actionRefsByCapability map[string][]string
	// evidenceRefs 是当前 run/series 已准入的证据身份集合，供 untried
	// materiality 原因引用；它与已尝试 action refs 保持不同权威边界。
	evidenceRefs   []string
	actionSequence int
}

// newResilientRunState 在 runQueued 成功 claim（queued→preparing_context）
// 之后构造状态：durable run version 已经比 runQueued 收到的快照多 1，而
// checkpoint append 要求 observedRunVersion 精确等于 durable version，
// 因此从 run.Version+1 开始跟踪。reconstruction 是可选的前驱 durable
// checkpoint 重建块（AC7 种子）。
func newResilientRunState(store domain.CheckpointStore, run domain.Run, reconstruction string) *resilientRunState {
	return &resilientRunState{
		store:                         store,
		run:                           run,
		observedVersion:               run.Version + 1,
		reconstruction:                reconstruction,
		lastSoftSignal:                softWithinAllocation,
		actionRefsByCapability:        make(map[string][]string),
		thresholdCheckpointLevelBytes: contextPressureStepBytes,
	}
}

// appendRecovery 保留最新的 64 条 recovery journal。所有 application append
// 必须经过这里，避免第 65 次可恢复动作把合法 run 转成 persistence_failure。
func (t *resilientRunState) appendRecovery(recovery domain.CheckpointRecovery) {
	if t == nil {
		return
	}
	if len(t.recoveries) >= maxResilientRecoveries {
		t.recoveries = append([]domain.CheckpointRecovery(nil), t.recoveries[len(t.recoveries)-maxResilientRecoveries+1:]...)
	}
	t.recoveries = append(t.recoveries, recovery)
}

// recoveryProgressRef 把模型或 adapter 输入压缩为固定长度 marker；checkpoint
// 只持久化 hash，不保存原始错误、patch、输出或模型内容。
func recoveryProgressRef(code, fingerprint string) string {
	if strings.TrimSpace(fingerprint) == "" {
		return "challenge:" + code
	}
	return "challenge:" + code + ":" + recoveryFingerprint(fingerprint)
}

func recoveryFingerprint(fingerprint string) string {
	if len(fingerprint) == sha256.Size*2 {
		if _, err := hex.DecodeString(fingerprint); err == nil {
			return fingerprint
		}
	}
	sum := sha256.Sum256([]byte(fingerprint))
	return fmt.Sprintf("%x", sum[:])
}

func recoveryProgressHash(ref string) string {
	parts := strings.Split(ref, ":")
	if len(parts) < 3 {
		return ""
	}
	return parts[len(parts)-1]
}

// restoreRecoveryProgress 从 pre-change v1 recovery journal 保守重建 bounded
// no-progress 状态。新 checkpoint 使用 RecoveryProgress 权威快照；journal 仅是
// audit/event history。hashless 旧条目以 action+ref 派生稳定 fingerprint，不能
// 被误当成零进展而重置连续尝试。
func (t *resilientRunState) restoreRecoveryProgress() {
	for _, recovery := range t.recoveries {
		hash := recoveryProgressHash(recovery.OutcomeRef)
		if hash == "" {
			hash = recoveryFingerprint("legacy\x00" + recovery.Action + "\x00" + recovery.OutcomeRef)
		}
		switch {
		case recovery.Action == "correct_citation":
			t.evidenceCorrectionAttempts++
		case recovery.Action == "correct_fact_check":
			t.factCheckAttempts++
			if hash == t.lastFactCheckFingerprint {
				t.factCheckNoProgress++
			} else {
				t.lastFactCheckFingerprint = hash
				t.factCheckNoProgress = 1
			}
		case recovery.Action == "revise_plan":
			t.planFeedbackAttempts++
			if hash == t.lastPlanFeedbackFingerprint {
				t.planFeedbackNoProgress++
			} else {
				t.lastPlanFeedbackFingerprint = hash
				t.planFeedbackNoProgress = 1
			}
		case recovery.Action == "revise_patch":
			if hash == t.lastValidationFingerprint {
				t.validationNoProgress++
			} else {
				t.lastValidationFingerprint = hash
				t.validationNoProgress = 1
			}
		case recovery.Action == "stop":
			t.lifecycleStopAttempts++
			t.lifecycleChallengeAttempts++
		case isLegacyLifecycleRecoveryAction(recovery.Action):
			t.lifecycleChallengeAttempts++
			t.lastLifecycleRecoveryClass = legacyLifecycleRecoveryClass(recovery.Action)
			if hash == t.lastLifecycleRecoveryFingerprint {
				t.lifecycleRecoveryAttempts++
			} else {
				t.lastLifecycleRecoveryFingerprint = hash
				t.lifecycleRecoveryAttempts = 1
			}
		default:
			// pre-change v1 没有独立快照；未分类 challenge 至少消耗一次通用
			// recovery attempt，避免 restart 把旧尝试当成零。
			t.recoveryAttempt++
		}
		if recovery.Kind == string(domain.RecoveryChallengeKindExhaustion) && recovery.Action == "request_exhaustion_proposal" {
			t.exhaustionProposalAttempts++
		}
	}
	// legacy journal 没有 episode 边界标记；以 journal 长度近似当前 episode
	// attempt（保守回退，仅限 pre-change v1 无 progress 快照的 checkpoint）。
	t.episodeRecoveryAttempts = len(t.recoveries)
}

func isLegacyLifecycleRecoveryAction(action string) bool {
	if strings.HasPrefix(action, "lifecycle:") {
		return true
	}
	switch action {
	case "model_turn", "agentEnvelope", ToolWorkspaceApplyPatch, ToolWorkspaceRunValidation,
		ToolWorkspaceReadFile, ToolWorkspaceStatus, "patchComplete", "validationAssessment":
		return true
	default:
		return false
	}
}

func legacyLifecycleRecoveryClass(action string) string {
	action = strings.TrimPrefix(action, "lifecycle:")
	if action == "model_turn" || action == "agentEnvelope" {
		return "protocol"
	}
	return "tool"
}

func (t *resilientRunState) restoreRecoveryProgressSnapshot(progress domain.CheckpointRecoveryProgress) {
	t.recoveryAttempt = progress.RecoveryAttempts
	t.evidenceCorrectionAttempts = progress.EvidenceCorrectionAttempts
	t.factCheckAttempts = progress.FactCheckAttempts
	t.factCheckNoProgress = progress.FactCheckNoProgress
	t.lastFactCheckFingerprint = progress.LastFactCheckFingerprint
	t.planFeedbackAttempts = progress.PlanFeedbackAttempts
	t.planFeedbackNoProgress = progress.PlanFeedbackNoProgress
	t.lastPlanFeedbackFingerprint = progress.LastPlanFeedbackFingerprint
	t.lifecycleRecoveryAttempts = progress.LifecycleRecoveryAttempts
	t.lastLifecycleRecoveryFingerprint = progress.LastLifecycleRecoveryFingerprint
	t.lastLifecycleRecoveryClass = progress.LastLifecycleRecoveryClass
	t.lifecycleChallengeAttempts = progress.LifecycleChallengeAttempts
	t.lifecycleStopAttempts = progress.LifecycleStopAttempts
	t.validationNoProgress = progress.ValidationNoProgress
	t.lastValidationFingerprint = progress.LastValidationFingerprint
	t.exhaustionProposalAttempts = progress.ExhaustionProposalAttempts
	t.episodeRecoveryAttempts = progress.EpisodeRecoveryAttempts
}

func (t *resilientRunState) recoveryProgressSnapshot() *domain.CheckpointRecoveryProgress {
	return &domain.CheckpointRecoveryProgress{
		RecoveryAttempts: t.recoveryAttempt, EvidenceCorrectionAttempts: t.evidenceCorrectionAttempts,
		FactCheckAttempts: t.factCheckAttempts, FactCheckNoProgress: t.factCheckNoProgress,
		LastFactCheckFingerprint: t.lastFactCheckFingerprint,
		PlanFeedbackAttempts:     t.planFeedbackAttempts, PlanFeedbackNoProgress: t.planFeedbackNoProgress,
		LastPlanFeedbackFingerprint:      t.lastPlanFeedbackFingerprint,
		LifecycleRecoveryAttempts:        t.lifecycleRecoveryAttempts,
		LastLifecycleRecoveryFingerprint: t.lastLifecycleRecoveryFingerprint,
		LastLifecycleRecoveryClass:       t.lastLifecycleRecoveryClass,
		LifecycleChallengeAttempts:       t.lifecycleChallengeAttempts, LifecycleStopAttempts: t.lifecycleStopAttempts,
		ValidationNoProgress: t.validationNoProgress, LastValidationFingerprint: t.lastValidationFingerprint,
		ExhaustionProposalAttempts: t.exhaustionProposalAttempts,
		EpisodeRecoveryAttempts:    t.episodeRecoveryAttempts,
	}
}

func (t *resilientRunState) recordLifecycleFailure(fingerprint string) {
	class := "tool"
	if strings.HasPrefix(fingerprint, "model_turn\x00") || strings.HasPrefix(fingerprint, "agentEnvelope\x00") {
		class = "protocol"
	}
	fingerprint = recoveryFingerprint(fingerprint)
	if fingerprint == t.lastLifecycleRecoveryFingerprint {
		t.lifecycleRecoveryAttempts++
		return
	}
	t.lastLifecycleRecoveryClass = class
	t.lastLifecycleRecoveryFingerprint = fingerprint
	t.lifecycleRecoveryAttempts = 1
}

func (t *resilientRunState) resetLifecycleProtocolNoProgress() {
	if t.lastLifecycleRecoveryClass == "protocol" {
		t.lifecycleRecoveryAttempts = 0
		t.lastLifecycleRecoveryFingerprint = ""
		t.lastLifecycleRecoveryClass = ""
	}
	t.lifecycleStopAttempts = 0
}

func (t *resilientRunState) resetLifecycleNoProgress() {
	t.lifecycleRecoveryAttempts = 0
	t.lastLifecycleRecoveryFingerprint = ""
	t.lastLifecycleRecoveryClass = ""
	t.lifecycleStopAttempts = 0
}

// resetDiagnosisNoProgress 在 gate 准入的 code_fixable 诊断收敛时清空
// diagnosing 的 no-progress 连续计数与 exhaustion 请求计数（F1）。这些计数
// 只在同一连续无进展 loop 内有意义：一次成功的 durable 前进（gate 准入、
// 进入 planning）后，后续 checkpoint 不应继续携带已结算 loop 的标记
// （lifetime stickiness）。绝不重置 validation/lifecycle 计数（那些由各自的
// 收敛点与修复边界复位方法管理）。
func (t *resilientRunState) resetDiagnosisNoProgress() {
	if t == nil {
		return
	}
	t.factCheckNoProgress = 0
	t.lastFactCheckFingerprint = ""
	t.exhaustionProposalAttempts = 0
}

// advanceRecoveryEpisode 在每次 durable checkpoint 追加前更新当前 recovery
// episode 的 attempt 计数（D8 attempt，F3）：recovery 触发 checkpoint 递增；
// 非 recovery checkpoint（phase boundary / threshold / process shutdown，即
// durable 前进或终态）关闭 episode 并从 1 重新计数。计数随 checkpoint 持久化，
// restart/续跑经 restoreRecoveryProgressSnapshot 精确恢复。
func (t *resilientRunState) advanceRecoveryEpisode(reason string) {
	if t == nil {
		return
	}
	if reason == domain.CheckpointReasonRecovery {
		t.episodeRecoveryAttempts++
	} else {
		t.episodeRecoveryAttempts = 0
	}
}

func (t *resilientRunState) resetValidationNoProgress() {
	t.validationNoProgress = 0
	t.lastValidationFingerprint = ""
}

type resilientRunStateKey struct{}

// withResilientRunState 把 per-run resilient 状态挂到 context，让 coordinator
// 的既有方法签名保持不变；legacy run 不挂载，读取返回 nil。
func withResilientRunState(ctx context.Context, state *resilientRunState) context.Context {
	return context.WithValue(ctx, resilientRunStateKey{}, state)
}

// resilientStateFrom 返回当前 run 的 resilient 状态；legacy / 未注入 store
// 时为 nil，调用方必须把它当作 no-op。
func resilientStateFrom(ctx context.Context) *resilientRunState {
	state, _ := ctx.Value(resilientRunStateKey{}).(*resilientRunState)
	return state
}

// admitSoftBudget 在 drive 解析出 resumePhase 后为 run 构造并 admit D7
// soft-budget allocator。计划使用 NewBudgetPlan 的保守默认分配（15% soft /
// 5% later-phase reserve / 5% recovery reserve）。失败（如预算维度非法）时
// 保持 alloc 为 nil：soft-budget 是可选的 recoverable 增强，绝不能阻断 run。
func (t *resilientRunState) admitSoftBudget(limits domain.BudgetLimits, phase domain.RunState) {
	budgetPhase, ok := domain.BudgetPhaseFor(phase)
	if !ok {
		return
	}
	plan, err := NewBudgetPlan(limits, nil, nil, domain.RecoveryReserve{})
	if err != nil {
		return
	}
	alloc, err := newPhaseBudgetPlan(plan)
	if err != nil {
		return
	}
	if err := alloc.AdmitPhase(budgetPhase); err != nil {
		return
	}
	t.alloc = alloc
}

// checkpointRun 在 resilient 模式下强制追加一次 working-memory checkpoint
// （D2 forced set：phase boundary / recovery / 终态前）。required checkpoint 的
// 构建或持久化失败会返回错误并锁定 checkpointUnavailable；调用方必须把 run
// 转为 persistence_failure，不能继续模型决策路径。
func (c *RemediationCoordinator) checkpointRun(ctx context.Context, tracker *resilientRunState, phase domain.RunState, reason string) error {
	if tracker == nil || tracker.store == nil {
		return nil
	}
	if tracker.checkpointUnavailable {
		return fmt.Errorf("checkpoint store is unavailable after an earlier required append")
	}
	// RunStore 的 version 增量属于 adapter 持久化合同：Postgres 对带 budget
	// effect 的 transition 会分别更新 state 与 counters，因此一次调用可能推进
	// 两个版本。required checkpoint 构建前必须从 store 刷新权威版本，不能按
	// transition 调用次数猜测，否则真实 store 会拒绝 stale checkpoint。
	aggregate, err := c.store.Get(ctx, tracker.run.RunID)
	if err != nil {
		tracker.checkpointUnavailable = true
		return fmt.Errorf("refresh remediation run version for checkpoint: %w", err)
	}
	if aggregate.Run.Version < 1 {
		tracker.checkpointUnavailable = true
		return fmt.Errorf("refresh remediation run version for checkpoint: invalid version %d", aggregate.Run.Version)
	}
	tracker.observedVersion = aggregate.Run.Version

	// queued/preparing_context 尚未进入模型阶段，但 Postgres checkpoint store
	// 将两者的 durable working memory 归到 diagnosing；与 adapter 的
	// checkpointPhaseMatchesRun 合同保持一致，避免 pre-drive typed failure 被
	// 错误的 phase rejection 覆盖为 persistence_failure。
	if phase == domain.RunStateQueued || phase == domain.RunStatePreparingContext {
		phase = domain.RunStateDiagnosing
	} else if budgetPhase, ok := domain.BudgetPhaseFor(phase); ok {
		phase = budgetPhase
	}
	// D8 attempt 的 episode 边界（F3）：recovery 触发的 checkpoint 递增当前
	// episode attempt，非 recovery checkpoint 关闭 episode 归零。必须在构造
	// checkpoint payload 之前更新，使持久化的 RecoveryProgress 携带最新值。
	tracker.advanceRecoveryEpisode(reason)
	checkpoint, err := tracker.buildCheckpoint(phase, reason)
	if err != nil {
		tracker.checkpointUnavailable = true
		return fmt.Errorf("build remediation checkpoint: %w", err)
	}
	if _, err := tracker.store.AppendCheckpoint(ctx, tracker.run.RunID, checkpoint); err != nil {
		tracker.checkpointUnavailable = true
		return fmt.Errorf("append remediation checkpoint: %w", err)
	}
	// Phase 4 指标：每次 durable checkpoint 都只发出 reason/phase/no-progress
	// 标记与 recovery journal 长度；recovery 触发(checkpoint reason=recovery)可
	// 在 phase_boundary/threshold 事件旁推导 recovery 收敛率。
	c.emitResilienceMetric(ctx, ResilienceMetric{
		Run: observationRun(ctx), Mode: metricMode(tracker.run.AgentLoopMode),
		Kind: ResilienceMetricCheckpoint, Phase: phase, Reason: reason,
		NoProgress: checkpointNoProgress(checkpoint.RecoveryProgress), Attempts: len(checkpoint.Recoveries),
	})
	// Recovery episode 结算（durable tracker state）：recovery 触发 checkpoint
	// 打开/保持 episode；之后第一个非 recovery checkpoint 表示 durable 前进，
	// 恰好结算一次 recovery-success 并关闭 episode。被放弃型终态静默关闭的
	// episode 不会走到这里，因此不会误报 success。
	if reason == domain.CheckpointReasonRecovery {
		tracker.recoveryEpisodeOpen = true
	} else if tracker.recoveryEpisodeOpen {
		tracker.recoveryEpisodeOpen = false
		c.emitResilienceMetric(ctx, ResilienceMetric{
			Run: observationRun(ctx), Mode: metricMode(tracker.run.AgentLoopMode),
			Kind: ResilienceMetricRecoverySuccess, Phase: phase, Reason: reason,
		})
	}
	// 自动 byte-threshold 触发的 anti-spam/consumed 水位（D2/R13）：任意 durable
	// checkpoint 已代表 working memory 落盘，消费当时 conversation 的字节水位
	// 与大工具观察身份；自动触发要求在该水位之上再有 ≥ toolOutputPressureBytes
	// 的新增内容，且新大观察必须出现在 consumed 水位之后。queued/preparing
	// 早期尚无 conversation 时跳过。
	tracker.consumeConversationWatermarks(tracker.conversation)
	return nil
}

// buildCheckpoint 从进程内状态构造 bounded WorkingMemoryCheckpointV1。
// Sequence 为 0，由存储分配；ObservedRunVersion 取最后一次成功 transition
// 后的 durable version，保证 append 校验通过。Phase 必须是 durable run
// state 对应的 budget phase。verified facts 在本 slice 为空：任何已验证事实
// 都必须携带 evidence IDs（R15），事实提取属于后续 slice，这里只保留
// evidence index 与 recovery 记录。
func (t *resilientRunState) buildCheckpoint(phase domain.RunState, reason string) (domain.WorkingMemoryCheckpointV1, error) {
	checkpoint := domain.WorkingMemoryCheckpointV1{
		SchemaVersion:      domain.CheckpointSchemaVersionV1,
		RunID:              t.run.RunID,
		SeriesID:           t.run.SeriesID,
		ContextVersion:     t.run.ContextVersion,
		ObservedRunVersion: t.observedVersion,
		Phase:              string(phase),
		Objective:          checkpointObjective(phase),
		EvidenceIndex:      append([]domain.CheckpointEvidenceIndexItem(nil), t.evidenceIndex...),
		Recoveries:         append([]domain.CheckpointRecovery(nil), t.recoveries...),
		RecoveryProgress:   t.recoveryProgressSnapshot(),
		NextActions:        append([]string(nil), t.nextActions...),
		Workspace:          cloneCheckpointWorkspace(t.workspace),
		Artifacts:          append([]domain.CheckpointArtifact(nil), t.artifacts...),
		Validation:         cloneCheckpointValidation(t.validation),
		Publication:        cloneCheckpointPublication(t.publication),
		PublicationPolicy:  cloneCheckpointPublicationPolicy(t.publicationPolicy),
		ValidationCommands: cloneValidationCommands(t.validationCommands),
		Reason:             reason,
	}
	if t.alloc != nil {
		checkpoint.Budget = t.alloc.Recovery()
	}
	if err := checkpoint.Validate(); err != nil {
		return domain.WorkingMemoryCheckpointV1{}, err
	}
	return checkpoint, nil
}

// recordEvidenceRead 把一次成功的 evidence.read 页面投影为 evidence index
// 条目并去重；只保留身份/kind/locator/hash，不解码 payload 或凭据（R15）。
// 条目随下一次强制 AppendCheckpoint 持久化（D3）。
func (t *resilientRunState) recordEvidenceRead(page domain.EvidenceReadPage) {
	if strings.TrimSpace(page.EvidenceID) == "" {
		return
	}
	for _, item := range t.evidenceIndex {
		if item.EvidenceID == page.EvidenceID {
			return
		}
	}
	locator := page.Provenance.SourceID
	if locator == "" {
		locator = page.Kind
	}
	if len(t.evidenceIndex) >= maxResilientEvidenceIndex {
		t.evidenceIndex = t.evidenceIndex[len(t.evidenceIndex)-maxResilientEvidenceIndex+1:]
	}
	t.evidenceIndex = append(t.evidenceIndex, domain.CheckpointEvidenceIndexItem{
		EvidenceID:  page.EvidenceID,
		Kind:        page.Kind,
		Locator:     locator,
		ContentHash: page.ContentHash,
	})
}

// recordEvidenceRefs 把当前 series 已准入的 evidence IDs 记入独立集合。
// 这些 refs 可以支持 untried capability 的 factual/materiality 说明，但不能
// 证明对应 capability 已执行。
func (t *resilientRunState) recordEvidenceRefs(refs []string) {
	if t == nil {
		return
	}
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" || containsExhaustionRef(t.evidenceRefs, ref) {
			continue
		}
		if len(t.evidenceRefs) >= maxResilientActionRefs {
			return
		}
		t.evidenceRefs = append(t.evidenceRefs, ref)
	}
}

// recordToolAction 为一次真实工具调用生成模型可见 action ref，并把该 ref 与
// 工具结果 evidence IDs 绑定到工具对应的 D1 capability。失败或拒绝的调用也
// 是真实尝试，因此仍有 action ref；未知工具 class 不参与 exhaustion coverage。
func (t *resilientRunState) recordToolAction(toolName string, evidenceIDs []string) string {
	if t == nil {
		return ""
	}
	capability := toolCapabilityClass(toolName)
	if capability == "" {
		return ""
	}
	t.actionSequence++
	actionRef := fmt.Sprintf("action:%s:%d", capability, t.actionSequence)
	t.bindCapabilityRefs(capability, append([]string{actionRef}, evidenceIDs...))
	t.recordEvidenceRefs(evidenceIDs)
	return actionRef
}

// recordPriorToolAction 恢复 predecessor attempt 的持久化工具身份。continuation
// brief 会显示 invocation ID；已有 evidence IDs 同时保持可重读证据身份。
func (t *resilientRunState) recordPriorToolAction(invocation domain.ToolInvocation) {
	if t == nil {
		return
	}
	capability := toolCapabilityClass(invocation.ToolName)
	if capability == "" {
		return
	}
	refs := append([]string(nil), invocation.EvidenceIDs...)
	if strings.TrimSpace(invocation.InvocationID) != "" {
		refs = append([]string{invocation.InvocationID}, refs...)
	}
	t.bindCapabilityRefs(capability, refs)
	t.recordEvidenceRefs(invocation.EvidenceIDs)
}

func (t *resilientRunState) bindCapabilityRefs(capability string, refs []string) {
	if t.actionRefsByCapability == nil {
		t.actionRefsByCapability = make(map[string][]string)
	}
	current := t.actionRefsByCapability[capability]
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" || containsExhaustionRef(current, ref) {
			continue
		}
		if len(current) >= maxResilientActionRefs {
			break
		}
		current = append(current, ref)
	}
	t.actionRefsByCapability[capability] = current
}

// exhaustionValidationContext 返回服务权威的 capability/action、evidence、
// recovery 与预算快照。返回值复制内部集合，validator 不能修改 tracker。
func (t *resilientRunState) exhaustionValidationContext(catalog *ToolCatalog, remaining map[string]int64) ExhaustionValidationContext {
	context := ExhaustionValidationContext{
		CatalogCapabilities: catalogCapabilityClasses(catalog),
		ActionRefs:          make(map[string][]string, len(t.actionRefsByCapability)),
		EvidenceRefs:        append([]string(nil), t.evidenceRefs...),
		RemainingBudget:     copyBudget(remaining),
	}
	for capability, refs := range t.actionRefsByCapability {
		context.ActionRefs[capability] = append([]string(nil), refs...)
	}
	return context
}

// softBudgetRecovery 把一次已持久化的模型/工具消耗镜像记入 soft-budget
// allocator（D7）；信号增强（within→soft_crossed→reserve_touched）时向同一
// conversation 回喂 bounded recoverable budget challenge（D5）并强制 recovery
// checkpoint。它只产生 recoverable 信号，hard ceiling 的 budget_exhausted
// 终态仍完全由 runBudget/admitOperation/transitionBudgeted 独立判定（AC10）。
// legacy 或 allocator/conversation 未就绪时是 no-op。
func (c *RemediationCoordinator) softBudgetRecovery(ctx context.Context, budget *runBudget, state domain.RunState, effect domain.Effect, persistRecovery bool) error {
	tracker := resilientStateFrom(ctx)
	if tracker == nil || tracker.alloc == nil || tracker.conversation == nil {
		return nil
	}
	phase, ok := domain.BudgetPhaseFor(state)
	if !ok {
		return nil
	}
	if phase != tracker.alloc.current {
		// validation 失败可以安全地回到 patching；allocator 保持在已接纳的
		// validation frontier，revision 消耗继续计入当前 phase，避免重开
		// 已关闭 phase 或重复借用 unreserved pool。
		if tracker.lifecyclePhase == domain.RunStatePatching && tracker.alloc.current != "" {
			phase = tracker.alloc.current
		} else {
			return nil
		}
	}
	// elapsed 按 runBudget 的累计语义取值，再减去上次镜像时的值得到本次增量；
	// 与 hard ceiling 同源（budget.elapsed()），保证镜像投影与真实消耗一致。
	elapsed := int64(budget.elapsed() / time.Second)
	amount := effect.BudgetAmount()
	amount.ElapsedSeconds = elapsed - tracker.lastMirroredElapsed
	if amount.ElapsedSeconds < 0 {
		amount.ElapsedSeconds = 0
	}
	tracker.lastMirroredElapsed = elapsed
	signal, err := tracker.alloc.Consume(phase, amount)
	if err != nil {
		return fmt.Errorf("consume remediation soft budget: %w", err)
	}
	// 只在信号严格增强时回喂一次 challenge：同一信号的重复消耗不需要重复告警，
	// 避免对话被无进展的同一 challenge 刷屏（progress-aware recovery）。
	if !softSignalStrengthened(signal, tracker.lastSoftSignal) {
		return nil
	}
	tracker.lastSoftSignal = signal
	// 跨 phase/terminal transition 的 effect 仍要进入 allocator，但 durable
	// state 已离开旧 phase，不能再为旧 phase 追加 recovery checkpoint。
	if !persistRecovery {
		return nil
	}
	tracker.recoveryAttempt++
	challenge, err := NewRecoveryChallenge(
		domain.RecoveryChallengeKindBudget,
		domain.RecoverySeverityRecoverable,
		softBudgetReasonCode(signal),
		"",
		tracker.recoveryCapabilities(),
		[]string{"reduce_scope", "finalize_phase"},
		tracker.recoveryAttempt,
		budget.remaining(),
		softBudgetMessage(signal),
	)
	if err != nil {
		return fmt.Errorf("build soft budget recovery challenge: %w", err)
	}
	// Phase 4 指标：soft-budget 信号增强（soft_crossed/reserve_touched）发出
	// low-cardinality budget signal；只有增强时发一次，避免重复告警刷屏。
	c.emitResilienceMetric(ctx, ResilienceMetric{
		Run: observationRun(ctx), Mode: metricMode(tracker.run.AgentLoopMode),
		Kind: ResilienceMetricBudgetSignal, Phase: phase, Reason: softBudgetReasonCode(signal),
	})
	tracker.appendRecovery(domain.CheckpointRecovery{
		Kind:       string(challenge.Kind),
		Action:     string(signal),
		OutcomeRef: "challenge:" + challenge.ReasonCode,
	})
	// D5 challenge 追加与 per-kind 指标在同一共享出口完成。
	c.appendRecoveryChallenge(ctx, phase, tracker.conversation, challenge)
	if err := c.checkpointRun(ctx, tracker, phase, domain.CheckpointReasonRecovery); err != nil {
		return err
	}
	return nil
}

// advanceSoftBudget 在 durable phase transition 成功且 from phase 的 effect 已
// 镜像后关闭旧 phase、接纳新 phase。collecting_more_context 与 diagnosing 映射
// 到同一 budget phase，因此不会产生虚假的 close/re-admit。
func (t *resilientRunState) advanceSoftBudget(from, to domain.RunState) error {
	if t == nil || t.alloc == nil {
		return nil
	}
	fromPhase, fromOK := domain.BudgetPhaseFor(from)
	if from == domain.RunStateDiagnosisReadyForReview {
		fromPhase, fromOK = domain.RunStatePlanning, true
	}
	toPhase, toOK := domain.BudgetPhaseFor(to)
	if !toOK || (fromOK && fromPhase == toPhase) {
		return nil
	}
	if fromOK && toOK && domain.BudgetPhaseIndex(toPhase) < domain.BudgetPhaseIndex(fromPhase) {
		// Validation repair is a bounded backward state transition. The allocator
		// stays at the validation frontier; recovery work is charged there.
		return nil
	}
	if fromOK && t.alloc.current != "" && fromPhase != t.alloc.current {
		// validation revision may temporarily drive patching tools while the
		// allocator remains on the forward validation frontier. Keep one budget
		// projection and charge the recovery work there instead of reopening a
		// closed phase.
		if toOK && toPhase == t.alloc.current {
			return nil
		}
		return nil
	}
	if fromOK {
		if err := t.alloc.ClosePhase(fromPhase); err != nil {
			return fmt.Errorf("close remediation soft budget phase %q: %w", fromPhase, err)
		}
	}
	if err := t.alloc.AdmitPhase(toPhase); err != nil {
		return fmt.Errorf("admit remediation soft budget phase %q: %w", toPhase, err)
	}
	t.lastSoftSignal = softWithinAllocation
	return nil
}

// softSignalStrengthened 报告 signal 是否严格强于 last（within_allocation →
// soft_crossed → reserve_touched），用于只在信号增强时回喂一次 challenge。
func softSignalStrengthened(signal, last softBudgetResult) bool {
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
	return rank(signal) > rank(last)
}

// softBudgetReasonCode 是 challenge 的稳定 reason code。
func softBudgetReasonCode(signal softBudgetResult) string {
	switch signal {
	case softReserveTouched:
		return "soft_budget_reserve_touched"
	case softCrossed:
		return "soft_budget_crossed"
	default:
		return "soft_budget_within_allocation"
	}
}

// softBudgetMessage 是服务端固定、已 sanitize 的 challenge 文案；不携带
// provider/connector 细节或模型输出。
func softBudgetMessage(signal softBudgetResult) string {
	switch signal {
	case softReserveTouched:
		return "The current phase has consumed its soft budget allocation and the unreserved pool; further consumption would touch standing later-phase or recovery reserves. Finalize the current phase now or declare exhaustion. The soft budget never terminates the run; only the global hard ceiling does."
	case softCrossed:
		return "The current phase has consumed its soft budget allocation and is borrowing from the unreserved pool. Complete the remaining investigation with the highest-value reads and finalize the phase; later-phase and recovery reserves remain protected."
	default:
		return ""
	}
}

// recoveryCapabilities 返回 challenge 的 available capability classes（D1
// 稳定 class 名）；只作有界提示，不编码诊断决策树。
func (t *resilientRunState) recoveryCapabilities() []string {
	if t.analysisOnly {
		return []string{"repository"}
	}
	if t.alloc != nil && t.alloc.current == domain.RunStatePlanning {
		return []string{"repository", "validation", "publication"}
	}
	return []string{"repository", "provider_evidence", "runtime_logs", "ssh_inspect"}
}

// checkpointObjective 返回 phase 的服务端固定目标与完成标准；只描述阶段
// 契约，不含凭据或证据内容。
func checkpointObjective(phase domain.RunState) domain.CheckpointObjective {
	switch phase {
	case domain.RunStatePlanning:
		return domain.CheckpointObjective{
			Goal: "Produce a repair plan for the diagnosed fault from repository evidence and validated candidates.",
			CompletionCriteria: []string{
				"planCandidates envelope accepted",
				"every candidate carries evidence refs and affected files",
				"recommended candidate is justified by the diagnosis",
			},
		}
	case domain.RunStatePatching, domain.RunStateValidating, domain.RunStatePublishing:
		return domain.CheckpointObjective{
			Goal:               "Complete the current lifecycle phase without duplicating external effects.",
			CompletionCriteria: []string{"phase completed", "recovery contract preserved"},
		}
	default:
		return domain.CheckpointObjective{
			Goal: "Diagnose the incident from trusted evidence and repository inspection and classify fixability with evidence-backed causal closure.",
			CompletionCriteria: []string{
				"diagnosis envelope accepted by the fact gate",
				"fixability classified with causal closure",
				"no material contradiction remains",
			},
		}
	}
}

// shouldAutoCheckpoint 是自动 byte-threshold 触发（D2/R13 automatic hybrid
// trigger）的纯判定，供 autoThresholdCheckpoint 与单测使用：
//   - T1 context pressure：累计模型可见字节达到下一个 contextPressureStepBytes
//     级别（192 KiB，一个完整 context generation ≈ 现有 256 KiB 上限的 3/4），
//     在下一轮序列化可能开始丢弃最旧内容前持久化 working memory；
//   - T2 tool-output pressure：存在一条块字节达到 toolOutputPressureBytes 且
//     item sequence 出现在 consumed 水位之后（尚未被任何 durable checkpoint
//     覆盖）的完整工具观察——该有界观察本身构成 configured output pressure，
//     且每条大观察至多触发一次（见 consumedToolObservationSequence）。
//
// anti-spam：两个触发共用 lastAutoCheckpointBytes 字节水位——只有累计字节自
// 上次 durable checkpoint 增长 ≥ toolOutputPressureBytes 才允许新的自动
// checkpoint，没有新增内容的重复评估不会触发。返回 (fire, contextPressure)；
// T1 命中后由 noteAutoCheckpoint 推进 level。legacy / 无 store / 无
// conversation 状态一律不触发。
func (t *resilientRunState) shouldAutoCheckpoint(conversation *AgentConversation) (fire, contextPressure bool) {
	if t == nil || t.store == nil || conversation == nil {
		return false, false
	}
	cumulative := int64(conversation.CumulativeModelVisibleBytes())
	if cumulative < t.lastAutoCheckpointBytes+toolOutputPressureBytes {
		return false, false
	}
	contextPressure = t.thresholdCheckpointLevelBytes > 0 && cumulative >= t.thresholdCheckpointLevelBytes
	// 大观察出现在 consumed 水位之后即满足字节线（记录时已 ≥ threshold），
	// 且它自身为本次评估贡献了 ≥ 一个输出压力量子的新增内容；这里的字节线
	// 检查只是防御性兜底。
	sequence, observationBytes := conversation.LargeToolObservation()
	outputPressure := sequence > t.consumedToolObservationSequence && observationBytes >= toolOutputPressureBytes
	return contextPressure || outputPressure, contextPressure
}

// noteAutoCheckpoint 在自动 byte-threshold checkpoint 成功后推进 T1 level 并
// 消费字节/观察水位（consumeConversationWatermarks），保证每级和每条大观察
// 最多触发一次。调用方必须确认 checkpoint 已成功持久化。
func (t *resilientRunState) noteAutoCheckpoint(conversation *AgentConversation, contextPressure bool) {
	if t == nil || conversation == nil {
		return
	}
	t.consumeConversationWatermarks(conversation)
	if contextPressure && t.thresholdCheckpointLevelBytes > 0 {
		t.thresholdCheckpointLevelBytes += contextPressureStepBytes
	}
}

// consumeConversationWatermarks 把 conversation 当前的累计字节与最新大工具
// 观察身份消费到 tracker 的 anti-spam/consumed 水位。任何 durable checkpoint
// 成功落盘（checkpointRun，含 phase boundary / recovery / threshold / process
// shutdown）或成功触发的自动 checkpoint（noteAutoCheckpoint）之后调用；调用方
// 必须保证该 checkpoint 已持久化，消费语义才安全：已被覆盖的大观察不再因后续
// 无关字节增长重复触发 T2。
func (t *resilientRunState) consumeConversationWatermarks(conversation *AgentConversation) {
	if t == nil || conversation == nil {
		return
	}
	cumulative := int64(conversation.CumulativeModelVisibleBytes())
	if cumulative > t.lastAutoCheckpointBytes {
		t.lastAutoCheckpointBytes = cumulative
	}
	if sequence, _ := conversation.LargeToolObservation(); sequence > t.consumedToolObservationSequence {
		t.consumedToolObservationSequence = sequence
	}
}

// autoThresholdCheckpoint 在 resilient_v1 下于每轮模型调用前评估并执行自动
// byte-threshold checkpoint（D2/R13；判定见 shouldAutoCheckpoint）。评估点
// 在模型轮次之前，此时 native assistant tool_call + tool result 组必然完整
// （组在 RecordModelTurn / AppendToolResult 同步完成），因此 checkpoint 永不
// 截断 provider-native group。checkpoint 失败按既有 contract 是
// persistence/consistency blocker：直接 transition 到 failed
// （persistence_failure），不继续模型决策路径；checkpointRun 的 phase/version
// 刷新与 metric（reason=threshold）语义保持不变。legacy / 未注入 store 时是
// no-op，不影响既有字节路径。
func (c *RemediationCoordinator) autoThresholdCheckpoint(
	ctx context.Context,
	tracker *resilientRunState,
	phase domain.RunState,
	conversation *AgentConversation,
	runID string,
) error {
	if tracker == nil || tracker.store == nil || conversation == nil {
		return nil
	}
	fire, contextPressure := tracker.shouldAutoCheckpoint(conversation)
	if !fire {
		return nil
	}
	if err := c.checkpointRun(ctx, tracker, phase, domain.CheckpointReasonThreshold); err != nil {
		return c.fail(ctx, runID, phase, markPersistenceFailure(err))
	}
	tracker.noteAutoCheckpoint(conversation, contextPressure)
	return nil
}

// checkpointReconstructionEnvelope 是 reconstruction 块的 JSON 形状；所有
// 字段都来自已校验的 durable checkpoint，经 boundedContinuationText 消毒。
type checkpointReconstructionEnvelope struct {
	Kind             string                               `json:"kind"`
	SourceRunID      string                               `json:"sourceRunId"`
	Sequence         int64                                `json:"sequence"`
	Phase            string                               `json:"phase"`
	ContextVersion   int64                                `json:"contextVersion"`
	Objective        domain.CheckpointObjective           `json:"objective"`
	VerifiedFacts    []domain.CheckpointVerifiedFact      `json:"verifiedFacts"`
	ActiveHypotheses []domain.CheckpointHypothesis        `json:"activeHypotheses"`
	NextActions      []string                             `json:"nextActions"`
	EvidenceIndex    []domain.CheckpointEvidenceIndexItem `json:"evidenceIndex"`
	CompletedActions []checkpointCompletedAction          `json:"completedActions,omitempty"`
	Budget           domain.BudgetPlanProjection          `json:"budget"`
}

type checkpointCompletedAction struct {
	ActionRef   string   `json:"actionRef"`
	Capability  string   `json:"capability"`
	Outcome     string   `json:"outcome"`
	EvidenceIDs []string `json:"evidenceIds,omitempty"`
}

// renderCheckpointReconstruction 把 predecessor 的 durable working-memory
// checkpoint 渲染为 bounded、可安全进入模型上下文的 reconstruction 块（D2）：
// objective、verified facts（保留 evidence IDs）、active hypotheses、next
// actions、evidence index、completed action identity 与 soft-budget projection。
// completed actions 只含 invocation ref/capability/outcome/evidence IDs，不含参数、
// result summary 或 raw output；模型需要原文时必须用 evidence.read 或显式重读
// （R15）。budget 通过纯构造函数 restore 后再投影，保证与 allocator 决策
// 一致（D7）。
func renderCheckpointReconstruction(
	snapshot domain.CheckpointSnapshot,
	priorRuntime []domain.StoredEvidence,
	priorIndex []domain.EvidenceIndexEntry,
	priorInvocations []domain.ToolInvocation,
) string {
	checkpoint := snapshot.Checkpoint
	evidenceIndex := reconstructionEvidenceIndex(checkpoint.EvidenceIndex, priorRuntime, priorIndex)
	envelope := checkpointReconstructionEnvelope{
		Kind:             "working_memory_reconstruction",
		SourceRunID:      boundedContinuationText(checkpoint.RunID, 128),
		Sequence:         checkpoint.Sequence,
		Phase:            boundedContinuationText(checkpoint.Phase, 64),
		ContextVersion:   checkpoint.ContextVersion,
		Objective:        boundedCheckpointObjective(checkpoint.Objective),
		VerifiedFacts:    boundedCheckpointFacts(checkpoint.VerifiedFacts, 16),
		ActiveHypotheses: boundedCheckpointHypotheses(checkpoint.ActiveHypotheses, 16),
		NextActions:      boundedContinuationList(checkpoint.NextActions),
		EvidenceIndex:    boundedCheckpointIndex(evidenceIndex, 32),
		CompletedActions: boundedCheckpointCompletedActions(priorInvocations, 32),
	}
	if restored, err := restorePhaseBudgetPlan(checkpoint.Budget); err == nil {
		envelope.Budget = restored.Projection()
	}
	encoded := encodeCheckpointReconstruction(envelope)
	return "## Reconstructed working memory (durable checkpoint; re-read evidence by evidenceId)\n" + string(encoded)
}

func boundedCheckpointCompletedActions(invocations []domain.ToolInvocation, limit int) []checkpointCompletedAction {
	if len(invocations) > limit {
		invocations = invocations[len(invocations)-limit:]
	}
	actions := make([]checkpointCompletedAction, 0, len(invocations))
	for _, invocation := range invocations {
		capability := toolCapabilityClass(invocation.ToolName)
		actionRef := boundedContinuationText(invocation.InvocationID, 128)
		if capability == "" || actionRef == "" {
			continue
		}
		outcome := "succeeded"
		if invocation.Error != "" {
			outcome = "failed"
		}
		actions = append(actions, checkpointCompletedAction{
			ActionRef: actionRef, Capability: capability, Outcome: outcome,
			EvidenceIDs: boundedContinuationList(invocation.EvidenceIDs),
		})
	}
	return actions
}

// encodeCheckpointReconstruction 有界编码 reconstruction 块；超限时先丢弃
// hypotheses/evidence index，再丢弃 facts/next actions/completed actions；仍超限
// 时返回最小安全 envelope。
func encodeCheckpointReconstruction(envelope checkpointReconstructionEnvelope) []byte {
	encoded, err := json.Marshal(envelope)
	if err == nil && len(encoded) <= maxContinuationBriefBytes {
		return encoded
	}
	envelope.ActiveHypotheses = nil
	envelope.EvidenceIndex = nil
	encoded, err = json.Marshal(envelope)
	if err == nil && len(encoded) <= maxContinuationBriefBytes {
		return encoded
	}
	envelope.VerifiedFacts = nil
	envelope.NextActions = nil
	envelope.CompletedActions = nil
	encoded, err = json.Marshal(envelope)
	if err == nil && len(encoded) <= maxContinuationBriefBytes {
		return encoded
	}
	return []byte(`{"kind":"working_memory_reconstruction","objective":{"goal":"Diagnose the incident from trusted evidence and repository inspection."},"budget":{}}`)
}

func boundedCheckpointObjective(objective domain.CheckpointObjective) domain.CheckpointObjective {
	return domain.CheckpointObjective{
		Goal:               boundedContinuationText(objective.Goal, maxResilientGoalLength),
		CompletionCriteria: boundedContinuationList(objective.CompletionCriteria),
	}
}

func boundedCheckpointFacts(facts []domain.CheckpointVerifiedFact, limit int) []domain.CheckpointVerifiedFact {
	if len(facts) > limit {
		facts = facts[:limit]
	}
	out := make([]domain.CheckpointVerifiedFact, 0, len(facts))
	for _, fact := range facts {
		out = append(out, domain.CheckpointVerifiedFact{
			Statement:   boundedContinuationText(fact.Statement, maxResilientTextLength),
			EvidenceIDs: boundedContinuationList(fact.EvidenceIDs),
		})
	}
	return out
}

func boundedCheckpointHypotheses(hypotheses []domain.CheckpointHypothesis, limit int) []domain.CheckpointHypothesis {
	if len(hypotheses) > limit {
		hypotheses = hypotheses[:limit]
	}
	out := make([]domain.CheckpointHypothesis, 0, len(hypotheses))
	for _, hypothesis := range hypotheses {
		out = append(out, domain.CheckpointHypothesis{
			ID:          boundedContinuationText(hypothesis.ID, maxResilientIDLength),
			Summary:     boundedContinuationText(hypothesis.Summary, maxResilientTextLength),
			EvidenceIDs: boundedContinuationList(hypothesis.EvidenceIDs),
			Reason:      boundedContinuationText(hypothesis.Reason, maxResilientTextLength),
		})
	}
	return out
}

func boundedCheckpointIndex(index []domain.CheckpointEvidenceIndexItem, limit int) []domain.CheckpointEvidenceIndexItem {
	if len(index) > limit {
		index = index[:limit]
	}
	out := make([]domain.CheckpointEvidenceIndexItem, 0, len(index))
	for _, item := range index {
		out = append(out, domain.CheckpointEvidenceIndexItem{
			EvidenceID:  boundedContinuationText(item.EvidenceID, maxResilientIDLength),
			Kind:        boundedContinuationText(item.Kind, 96),
			Locator:     boundedContinuationText(item.Locator, maxResilientLocatorLength),
			ContentHash: boundedContinuationText(item.ContentHash, 64),
		})
	}
	return out
}

// reconstructionEvidenceIndex 合并 store 在 continuation 时重新授权的证据索引
// 与 predecessor checkpoint 索引。provider/non-runtime 证据优先，随后是 runtime
// 身份，最后补 checkpoint-only 条目；不内联 payload，模型统一通过 evidence.read
// 重读。这样 reconstruction 不需要携带 priorEvidenceBlock 也不会丢证据身份。
func reconstructionEvidenceIndex(
	checkpointIndex []domain.CheckpointEvidenceIndexItem,
	priorRuntime []domain.StoredEvidence,
	priorIndex []domain.EvidenceIndexEntry,
) []domain.CheckpointEvidenceIndexItem {
	out := make([]domain.CheckpointEvidenceIndexItem, 0, min(maxResilientEvidenceIndex, len(checkpointIndex)+len(priorRuntime)+len(priorIndex)))
	seen := make(map[string]struct{}, cap(out))
	appendItem := func(item domain.CheckpointEvidenceIndexItem) {
		if strings.TrimSpace(item.EvidenceID) == "" || len(out) >= maxResilientEvidenceIndex {
			return
		}
		if _, exists := seen[item.EvidenceID]; exists {
			return
		}
		seen[item.EvidenceID] = struct{}{}
		out = append(out, item)
	}
	for _, entry := range priorIndex {
		locator := entry.Provider
		if strings.TrimSpace(locator) == "" {
			locator = entry.Kind
		}
		appendItem(domain.CheckpointEvidenceIndexItem{
			EvidenceID: entry.EvidenceID, Kind: entry.Kind, Locator: locator, ContentHash: entry.ContentHash,
		})
	}
	for _, evidence := range priorRuntime {
		locator := evidence.Provider
		if strings.TrimSpace(locator) == "" {
			locator = evidence.EvidenceKind
		}
		appendItem(domain.CheckpointEvidenceIndexItem{
			EvidenceID: evidence.EvidenceID, Kind: evidence.EvidenceKind, Locator: locator, ContentHash: evidence.ContentHash,
		})
	}
	for _, item := range checkpointIndex {
		appendItem(item)
	}
	return out
}
