// Package metrics 把 remediation coordinator 的低基数恢复/生命周期指标事件
// 投影为 OpenTelemetry counters。该 adapter 只接收 application.ResilienceMetric
// 的枚举 kind、run 身份、mode 与 reason code；绝不接收 evidence/model 内容或
// 凭据，因此 counter 属性保持低基数。
package metrics

import (
	"context"
	"fmt"

	"mendry/backend/internal/modules/remediation/application"
	"mendry/backend/internal/modules/remediation/domain"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// metricUnknownReason 是 reason/kind 属性允许值之外的统一 fallback。metric
// 属性是低基数合同：任何不在 canonical 允许表中的值都折叠到这里，绝不截断
// 任意文本（截断无法控制基数）。
const metricUnknownReason = "unknown"

// Options 声明 remediation metrics observer 的 meter 依赖。
type Options struct {
	Meter metric.Meter
}

// Observer 把低基数 ResilienceMetric 事件计数到稳定 counter；未知 kind 是
// no-op，避免未来新事件在旧进程产生意外计数膨胀。
type Observer struct {
	runStarted      metric.Int64Counter
	stateTransition metric.Int64Counter
	runTerminal     metric.Int64Counter
	checkpoint      metric.Int64Counter
	noProgress      metric.Int64Counter
	budgetSignal    metric.Int64Counter
	exhaustion      metric.Int64Counter
	reconstruction  metric.Int64Counter
	challenge       metric.Int64Counter
	recoverySuccess metric.Int64Counter
}

var _ application.ResilienceMetricObserver = (*Observer)(nil)

// NewObserver 验证 meter 并注册全部 counter；构造失败时直接返回错误，不允许
// 半初始化 observer 静默丢失指标。
func NewObserver(options Options) (*Observer, error) {
	if options.Meter == nil {
		return nil, fmt.Errorf("remediation metrics meter is required")
	}
	newCounter := func(name, description string) (metric.Int64Counter, error) {
		counter, err := options.Meter.Int64Counter(name, metric.WithDescription(description))
		if err != nil {
			return nil, fmt.Errorf("create %s counter: %w", name, err)
		}
		return counter, nil
	}
	observer := &Observer{}
	var err error
	if observer.runStarted, err = newCounter("mendry.remediation.run.started", "Remediation runs started by snapshotted agent loop mode"); err != nil {
		return nil, err
	}
	if observer.stateTransition, err = newCounter("mendry.remediation.state.transitioned", "Durable remediation state transitions"); err != nil {
		return nil, err
	}
	if observer.runTerminal, err = newCounter("mendry.remediation.run.terminal", "Terminal remediation transitions by terminal reason"); err != nil {
		return nil, err
	}
	if observer.checkpoint, err = newCounter("mendry.remediation.checkpoint.persisted", "Durable working-memory checkpoints by trigger reason and phase"); err != nil {
		return nil, err
	}
	if observer.noProgress, err = newCounter("mendry.remediation.recovery.no_progress", "Checkpoints persisted while a no-progress recovery loop is detected"); err != nil {
		return nil, err
	}
	if observer.budgetSignal, err = newCounter("mendry.remediation.budget.signal", "Recoverable soft-budget signal crossings by signal code"); err != nil {
		return nil, err
	}
	if observer.exhaustion, err = newCounter("mendry.remediation.exhaustion.decided", "Exhaustion proposal decisions by acceptance"); err != nil {
		return nil, err
	}
	if observer.reconstruction, err = newCounter("mendry.remediation.continuation.reconstructed", "Continuations reconstructed from a durable predecessor checkpoint"); err != nil {
		return nil, err
	}
	if observer.challenge, err = newCounter("mendry.remediation.recovery.challenge", "D5 recovery challenges appended to a resilient conversation by challenge kind"); err != nil {
		return nil, err
	}
	if observer.recoverySuccess, err = newCounter("mendry.remediation.recovery.success", "Recovery episodes that durably converged to forward progress"); err != nil {
		return nil, err
	}
	return observer, nil
}

// RecordResilienceMetric 把事件投影为对应 counter。属性是低基数契约：mode /
// phase / state / from / to 是 domain 枚举，reason / kind 属性通过严格允许表
// 归一化，任何非允许值折叠为 metricUnknownReason，绝不做任意文本截断。
func (o *Observer) RecordResilienceMetric(ctx context.Context, event application.ResilienceMetric) {
	if o == nil {
		return
	}
	mode := metricModeValue(event.Mode)
	switch event.Kind {
	case application.ResilienceMetricRunStarted:
		o.runStarted.Add(ctx, 1, metric.WithAttributes(attribute.String("mode", mode)))
	case application.ResilienceMetricStateTransitioned:
		o.stateTransition.Add(ctx, 1, metric.WithAttributes(
			attribute.String("mode", mode),
			attribute.String("from", metricStateValue(event.From)),
			attribute.String("to", metricStateValue(event.To)),
		))
	case application.ResilienceMetricRunTerminal:
		o.runTerminal.Add(ctx, 1, metric.WithAttributes(
			attribute.String("mode", mode),
			attribute.String("state", metricStateValue(event.Phase)),
			attribute.String("terminal_reason", metricTerminalReason(event.TerminalReason)),
		))
	case application.ResilienceMetricCheckpoint:
		o.checkpoint.Add(ctx, 1, metric.WithAttributes(
			attribute.String("mode", mode),
			attribute.String("reason", metricCheckpointTrigger(event.Reason)),
			attribute.String("phase", metricStateValue(event.Phase)),
		))
		if event.NoProgress {
			o.noProgress.Add(ctx, 1, metric.WithAttributes(
				attribute.String("mode", mode),
				attribute.String("phase", metricStateValue(event.Phase)),
			))
		}
	case application.ResilienceMetricBudgetSignal:
		o.budgetSignal.Add(ctx, 1, metric.WithAttributes(
			attribute.String("mode", mode),
			attribute.String("signal", metricBudgetSignal(event.Reason)),
		))
	case application.ResilienceMetricExhaustion:
		o.exhaustion.Add(ctx, 1, metric.WithAttributes(
			attribute.String("mode", mode),
			attribute.String("accepted", metricExhaustionDecision(event.Reason)),
		))
	case application.ResilienceMetricReconstruction:
		o.reconstruction.Add(ctx, 1, metric.WithAttributes(attribute.String("mode", mode)))
	case application.ResilienceMetricChallenge:
		o.challenge.Add(ctx, 1, metric.WithAttributes(
			attribute.String("mode", mode),
			attribute.String("phase", metricStateValue(event.Phase)),
			attribute.String("kind", metricChallengeKind(event.ChallengeKind)),
		))
	case application.ResilienceMetricRecoverySuccess:
		o.recoverySuccess.Add(ctx, 1, metric.WithAttributes(
			attribute.String("mode", mode),
			attribute.String("phase", metricStateValue(event.Phase)),
			attribute.String("reason", metricCheckpointTrigger(event.Reason)),
		))
	default:
		// 未知 kind 是 no-op：未来新增事件不应在旧版本上产生未命名计数。
	}
}

// metricModeValue 只保留 domain 的 AgentLoopMode 枚举值。
func metricModeValue(mode domain.AgentLoopMode) string {
	switch mode {
	case domain.AgentLoopModeLegacy, domain.AgentLoopModeResilientV1:
		return string(mode)
	default:
		return metricUnknownReason
	}
}

// metricStateValue 只保留 domain 的 RunState 枚举值。
func metricStateValue(state domain.RunState) string {
	if _, err := domain.ParseRunState(string(state)); err == nil {
		return string(state)
	}
	return metricUnknownReason
}

// metricCheckpointTrigger 只保留 D2 checkpoint 触发分类。
func metricCheckpointTrigger(reason string) string {
	switch reason {
	case domain.CheckpointReasonThreshold, domain.CheckpointReasonPhaseBoundary,
		domain.CheckpointReasonRecovery, domain.CheckpointReasonProcessShutdown:
		return reason
	default:
		return metricUnknownReason
	}
}

// metricBudgetSignal 只保留 soft-budget allocator 的 recoverable 信号代码
// （softBudgetReasonCode 的 soft_crossed / reserve_touched 输出）。
func metricBudgetSignal(reason string) string {
	switch reason {
	case "soft_budget_crossed", "soft_budget_reserve_touched":
		return reason
	default:
		return metricUnknownReason
	}
}

// metricExhaustionDecision 只保留 exhaustion proposal 决策。
func metricExhaustionDecision(reason string) string {
	switch reason {
	case "accepted", "rejected":
		return reason
	default:
		return metricUnknownReason
	}
}

// metricChallengeKind 只保留 domain 的 D5 RecoveryChallengeKind 枚举。
func metricChallengeKind(kind domain.RecoveryChallengeKind) string {
	if kind.IsKnown() {
		return string(kind)
	}
	return metricUnknownReason
}

// metricTerminalReason 只保留 coordinator 持久化的 durable terminal reason
// 词汇表（与 application 的 classifyTerminalFailure / terminalEffectForState /
// safeLifecycleTerminalReason / safePlanPolicyReason / exhaustion 分类一致）。
// 任何其它输入折叠为 metricUnknownReason，保证 terminal_reason 属性严格
// low-cardinality，即使未来新增 reason 也自动落入 fallback。
func metricTerminalReason(reason string) string {
	switch reason {
	case "diagnosis_ready_for_review", "completed_non_code", "blocked_manual_review",
		"awaiting_human_review",
		"model_output_exhausted", "provider_timeout", "provider_transport", "provider_rate_limit",
		"provider_http_5xx", "provider_http_4xx", "provider_decode", "provider_failure",
		"provider_configuration", "provider_authentication", "provider_response_too_large",
		"transient_provider",
		"canceled", "connector_timeout", "transport", "rate_limit", "remote_execution",
		"authentication", "authorization", "not_found", "invalid_response",
		"invalid_configuration", "capability_unavailable", "policy_unconfigured",
		"invalid_arguments", "tool_unavailable", "connector_authorization",
		"connector_not_found", "connector_failure",
		"provider_detail_unavailable", "provider_detail_invalid", "provider_detail_redirect_rejected",
		"provider_detail_oversized", "provider_detail_timeout", "provider_detail_persistence",
		"runtime_evidence_persistence", "prior_detail_failure",
		"invalid_envelope", "insufficient_evidence", "budget_exhausted", "elapsed", "model_calls",
		"model_cost", "tool_calls", "evidence_bytes", "repository_bytes", "configuration_failure",
		"authorization_failure", "persistence_failure", "policy_rejection", "unknown_failure",
		"exhaustion_proof",
		"denied_control_plane_change", "high_risk_policy_requires_opt_in",
		"plan_policy_no_progress", "no_policy_compliant_plan", "plan_policy_blocked",
		"publication_retry_exhausted", "workspace_retry_exhausted", "patch_retry_exhausted",
		"validation_retry_exhausted", "publication_validation_required",
		"publication_patch_artifact_required", "patch_protocol_no_progress",
		"validation_protocol_no_progress", "validation_no_progress",
		"lifecycle_tool_no_progress", "lifecycle_model_no_progress", "agent_stop_no_progress",
		"lifecycle_failure", "lifecycle_policy_blocked", "effect_retry_exhausted",
		"workspace_unavailable", "workspace_invalid", "workspace_invalid_response",
		"workspace_baseline_mismatch", "patch_invalid_response", "validation_unavailable",
		"validation_invalid_response", "validation_command_unapproved",
		"publication_invalid_response", "publication_baseline_mismatch":
		return reason
	default:
		return metricUnknownReason
	}
}
