package application

import (
	"context"

	"mendry/backend/internal/modules/remediation/domain"
)

// Resilience metric kinds：low-cardinality 事件分类，全部不含 evidence/model 内容。
const (
	// ResilienceMetricRunStarted 是 run 启动（含 claim 后的状态推进）事件。
	ResilienceMetricRunStarted = "run_started"
	// ResilienceMetricStateTransitioned 是 durable state transition 事件。
	ResilienceMetricStateTransitioned = "state_transitioned"
	// ResilienceMetricRunTerminal 是进入终态的事件，TerminalReason 携带 durable
	// terminal reason。
	ResilienceMetricRunTerminal = "run_terminal"
	// ResilienceMetricCheckpoint 是 durable checkpoint append 事件；Reason 是
	// threshold|phase_boundary|recovery|process_shutdown 触发分类。
	ResilienceMetricCheckpoint = "checkpoint"
	// ResilienceMetricBudgetSignal 是 soft-budget 信号增强事件（soft_crossed /
	// reserve_touched）；Reason 是稳定 signal code。
	ResilienceMetricBudgetSignal = "budget_signal"
	// ResilienceMetricExhaustion 是 exhaustion proposal 决策事件；Reason 为
	// accepted|rejected。
	ResilienceMetricExhaustion = "exhaustion"
	// ResilienceMetricReconstruction 是 continuation 从 predecessor durable
	// checkpoint 重建工作记忆的事件。
	ResilienceMetricReconstruction = "reconstruction"
	// ResilienceMetricChallenge 是 resilient 模式向 conversation 追加一条 D5
	// recovery challenge 的事件；ChallengeKind 携带 challenge kind，Reason 留空。
	// 追加与计数在同一出口完成，保证覆盖诊断/规划/生命周期全部 kind 且不重复。
	ResilienceMetricChallenge = "challenge"
	// ResilienceMetricRecoverySuccess 是 recovery episode 收敛事件：run 处于
	// active recovery（最近一次 durable checkpoint 为 recovery 触发）时，第一个
	// 非 recovery durable checkpoint（phase boundary / threshold / process
	// shutdown，即 durable 前进）恰好结算一次 success。放弃型终态（failed /
	// budget_exhausted / blocked_manual_review）先静默关闭 episode，不产生
	// success。
	ResilienceMetricRecoverySuccess = "recovery_success"
)

// ResilienceMetric 是 coordinator 发出的有界、低基数恢复/生命周期指标事件。
// 只允许枚举 kind、run 身份、mode、phase/reason code 与布尔/计数标记；绝不含
// evidence 内容、模型轮次、凭据或工具输出。
type ResilienceMetric struct {
	Run            RunIdentity
	Mode           domain.AgentLoopMode
	Kind           string
	Phase          domain.RunState
	Reason         string
	From           domain.RunState
	To             domain.RunState
	TerminalReason string
	// ChallengeKind 只在 Kind=ResilienceMetricChallenge 时携带被追加 challenge
	// 的 D5 kind；其它事件保持空值。
	ChallengeKind domain.RecoveryChallengeKind
	NoProgress    bool
	Attempts      int
}

// ResilienceMetricObserver 接收低基数恢复/生命周期指标事件。实现负责把事件投影
// 到具体遥测传输（计数器）；observer 为可选注入，nil/legacy run 走 no-op。
type ResilienceMetricObserver interface {
	RecordResilienceMetric(context.Context, ResilienceMetric)
}

type noopResilienceMetricObserver struct{}

func (noopResilienceMetricObserver) RecordResilienceMetric(context.Context, ResilienceMetric) {}

func normalizeResilienceMetricObserver(observer ResilienceMetricObserver) ResilienceMetricObserver {
	if observer == nil {
		return noopResilienceMetricObserver{}
	}
	return observer
}

// emitResilienceMetric 把指标事件投递给注入的 observer（无 observer 时 no-op）。
func (c *RemediationCoordinator) emitResilienceMetric(ctx context.Context, metric ResilienceMetric) {
	if c == nil || c.resilienceMetrics == nil {
		return
	}
	c.resilienceMetrics.RecordResilienceMetric(ctx, metric)
}

// metricMode 从 tracker 或 fallback run 解析已快照的 agentLoopMode。
func metricMode(mode domain.AgentLoopMode) domain.AgentLoopMode {
	return domain.ParseAgentLoopMode(string(mode))
}

// checkpointNoProgress 判断 durable recovery-progress 快照是否处于一条
// 已达到/超过 escalation threshold 的 no-progress loop（F1）。逐族判定使用
// 与 escalation 决策相同的阈值常量，而不是任何非零计数：单次重复（计数
// 1-2）是有界 retry，只有连续无进展计数达到该族 escalation 上限才代表
// loop（fact check 3 / plan feedback 3 / validation 3 / lifecycle tool/stop
// 3）；exhaustion 循环由第 2 次起重新请求 exhaustion proof 表示（连续拒绝
// 或协议 loop 第二次请求即进入重复循环）。计数经收敛点复位（见
// resetDiagnosisNoProgress 与 lifecycle/validation 各自的复位方法），因此该
// 标记不会在收敛后对后续 checkpoint 长期粘滞。
func checkpointNoProgress(progress *domain.CheckpointRecoveryProgress) bool {
	if progress == nil {
		return false
	}
	return progress.FactCheckNoProgress >= maxConsecutiveProtocolFailures ||
		progress.PlanFeedbackNoProgress >= maxPlanPolicyFeedbackAttempts ||
		progress.ValidationNoProgress >= maxLifecycleRecoveryAttempts ||
		progress.LifecycleRecoveryAttempts >= maxLifecycleRecoveryAttempts ||
		progress.LifecycleStopAttempts >= maxLifecycleRecoveryAttempts ||
		progress.ExhaustionProposalAttempts >= minExhaustionReRequestAttempts
}

// minExhaustionReRequestAttempts 是 exhaustion 循环的 escalation 阈值：第一次
// exhaustion 请求是正常 give-up/策略转换，只有再次请求（重复循环）才被标记为
// no-progress。
const minExhaustionReRequestAttempts = 2

// emitChallengeMetric 是 per-kind challenge 指标的唯一 emit 出口：任何向
// conversation 追加 D5 recovery 观察的 writer（appendRecoveryChallenge /
// appendExhaustionProposalRequest / appendRequiredDetailCorrection）都通过这里
// 计数，保证每条追加观察恰好产生一个 challenge 指标且不重复。legacy run 没有
// resilient state，调用方在 tracker nil 时跳过 emit（行为与直接追加一致）。
func (c *RemediationCoordinator) emitChallengeMetric(ctx context.Context, phase domain.RunState, kind domain.RecoveryChallengeKind) {
	if tracker := resilientStateFrom(ctx); tracker != nil {
		c.emitResilienceMetric(ctx, ResilienceMetric{
			Run: observationRun(ctx), Mode: metricMode(tracker.run.AgentLoopMode),
			Kind: ResilienceMetricChallenge, Phase: phase, ChallengeKind: kind,
		})
	}
}

// appendRequiredDetailCorrection 把 mandatory Tencent CLS detail gate 关闭时
// 的 required_direct_evidence 协议修正回喂 diagnosing loop（F2）。模型可见的
// 观察与 legacy 完全一致（conversation.AppendProtocolError 同一形状）；只有
// resilient_v1 run 额外通过共享 per-kind challenge 指标出口恰好计数一次
// protocol_correction，便于统一观测所有 recoverable 修正的分布。
func (c *RemediationCoordinator) appendRequiredDetailCorrection(ctx context.Context, conversation *AgentConversation) {
	if conversation == nil {
		return
	}
	conversation.AppendProtocolError(domain.RunStateDiagnosing, requiredTencentDetailCorrection())
	if tracker := resilientStateFrom(ctx); tracker != nil {
		c.emitChallengeMetric(ctx, domain.RunStateDiagnosing, domain.RecoveryChallengeKindProtocolCorrection)
	}
}

// appendRecoveryChallenge 是 resilient 模式把 D5 recovery challenge 追加到
// conversation 的唯一出口（shared choke point）。追加与 per-kind 指标计数绑定
// 在同一处，保证每条 challenge 恰好计数一次，覆盖诊断/规划/生命周期全部
// kind（evidence_correction / protocol_correction / tool_failure /
// context_rehydration / validation_revision / publication_retry / budget /
// exhaustion），且不重复计数。legacy run 没有 resilient state，只追加不计数，
// 行为与直接调用 conversation.AppendRecoveryChallenge 完全一致。
func (c *RemediationCoordinator) appendRecoveryChallenge(
	ctx context.Context,
	phase domain.RunState,
	conversation *AgentConversation,
	challenge domain.RecoveryChallengeV1,
) {
	if conversation == nil {
		return
	}
	conversation.AppendRecoveryChallenge(challenge)
	c.emitChallengeMetric(ctx, phase, challenge.Kind)
}

// appendExhaustionProposalRequest 是 D6 exhaustion proposal 请求追加到
// conversation 的唯一出口。它与 appendRecoveryChallenge 一起构成全部 D5
// recovery 观察的写入 choke point，保证 kind=exhaustion 的请求与拒绝 challenge
// 一样按 per-kind 指标计数且不重复。仅 resilient run 可达（调用方已持有
// tracker）。
func (c *RemediationCoordinator) appendExhaustionProposalRequest(
	ctx context.Context,
	conversation *AgentConversation,
	attempt int,
	capabilities []string,
	remaining map[string]int64,
	outcomeRef string,
) {
	if conversation == nil {
		return
	}
	c.emitChallengeMetric(ctx, domain.RunStateDiagnosing, domain.RecoveryChallengeKindExhaustion)
	conversation.AppendExhaustionProposalRequest(attempt, capabilities, remaining, outcomeRef)
}

// metricAbandonmentTerminal 报告 durable terminal state 是否属于放弃型终态：
// 这类终态不产生 recovery-success，因为 run 没有从 recovery 收敛回前进路径。
// 相反，diagnosis_ready_for_review / completed_non_code / awaiting_human_review
// 是业务结论，recovery 收敛到这些终态仍计一次 success。
func metricAbandonmentTerminal(state domain.RunState) bool {
	switch state {
	case domain.RunStateFailed, domain.RunStateBudgetExhausted, domain.RunStateBlockedManualReview:
		return true
	default:
		return false
	}
}
