package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	incidentapplication "fixthe/backend/internal/modules/incidents/application"
	"fixthe/backend/internal/modules/remediation/domain"
)

const (
	// TriggerReasonAutomatic 是合格新建/reopen 的自动触发原因。
	TriggerReasonAutomatic = incidentapplication.RemediationReasonAutomatic
	// TriggerReasonManual 是 admin/operator 手动启动（含 Info）的原因。
	TriggerReasonManual = incidentapplication.RemediationReasonManual
)

// TriggerRequest 是同步触发 seam 的输入。IncidentID 必须是内部 UUID。
type TriggerRequest struct {
	IncidentID          string
	LifecycleGeneration int64
	DeployedCommit      string
	Priority            string
	Reason              string
	ContextVersion      int64
}

// FailureReporter 记录 remediation 失败的完整服务端诊断，不接收原始 webhook payload。
type FailureReporter interface {
	Report(context.Context, TriggerRequest, error)
}

// GateObservation 描述 automatic continuation gate 的安全决策，不包含 webhook、provider 或 credential payload。
type GateObservation struct {
	Outcome string
	Reason  string
}

// GateReporter 记录 automatic gate 的 continue/skip 决策；旧 reporter 不实现时保持 no-op。
type GateReporter interface {
	ReportGate(context.Context, TriggerRequest, GateObservation)
}

// Trigger 是统一的触发调用点，也是未来 outbox emit 的替换缝。
// 自动 root/continuation 保持现有同步驱动；受保护的 manual continuation 通过
// QueueContinuation 在 queued child 持久化后脱离 HTTP 请求后台驱动。
type Trigger struct {
	store           domain.RunStore
	attempts        domain.AttemptStore
	coordinator     *RemediationCoordinator
	failureReporter FailureReporter
	gateReporter    GateReporter
}

// NewTrigger 组装 store + coordinator。coordinator 可为空，此时只创建 series/root，不 drive。
func NewTrigger(store domain.RunStore, coordinator *RemediationCoordinator) (*Trigger, error) {
	return NewTriggerWithReporter(store, coordinator, nil)
}

// NewTriggerWithReporter 组装带失败诊断 reporter 的 remediation trigger。
func NewTriggerWithReporter(store domain.RunStore, coordinator *RemediationCoordinator, reporter FailureReporter) (*Trigger, error) {
	if store == nil {
		return nil, fmt.Errorf("run store is required")
	}
	attempts, _ := store.(domain.AttemptStore)
	gateReporter, _ := reporter.(GateReporter)
	return &Trigger{store: store, attempts: attempts, coordinator: coordinator, failureReporter: reporter, gateReporter: gateReporter}, nil
}

// Emit 满足 incidents.RemediationTrigger。不合格请求是 no-op；合格自动请求
// 先经过 remediation-owned latest-attempt gate，再决定创建 root 或 linked child。
func (t *Trigger) Emit(ctx context.Context, req incidentapplication.RemediationRequest) error {
	request := TriggerRequest{
		IncidentID: req.IncidentID, LifecycleGeneration: req.LifecycleGeneration,
		DeployedCommit: req.DeployedCommit, Priority: req.Priority, Reason: req.Reason,
		ContextVersion: req.ContextVersion,
	}
	if !qualifies(request) {
		if request.Reason == TriggerReasonAutomatic {
			t.recordGate(ctx, request, "skipped", "non_qualifying_priority")
		}
		return nil
	}
	if request.Reason != TriggerReasonAutomatic || t.attempts == nil {
		_, err := t.Start(ctx, request)
		return err
	}
	_, err := t.emitAutomatic(ctx, request)
	return err
}

// Continue 是 manual/automatic continuation 共用的 next-attempt seam。没有
// coordinator 时仍可只创建 queued child，保持测试和旧 runtime wiring 可用。
func (t *Trigger) Continue(ctx context.Context, in domain.NextAttempt) (domain.Run, error) {
	if err := in.Validate(); err != nil {
		return domain.Run{}, fmt.Errorf("%w: %v", domain.ErrInvalidNextAttempt, err)
	}
	if t.attempts == nil {
		return domain.Run{}, fmt.Errorf("continuation attempt store is required")
	}
	if t.coordinator != nil {
		return t.coordinator.Continue(ctx, in)
	}
	run, err := t.attempts.CreateNextAttempt(ctx, in)
	if err != nil {
		return domain.Run{}, err
	}
	return run, nil
}

// QueueContinuation 持久化 manual continuation 后立即返回 queued child，避免
// 受保护的 HTTP 请求同步等待 model、Git、SSH 或 MCP 操作。后台驱动失败仍通过
// 已注入的 remediation failure reporter 保留完整服务端诊断。
func (t *Trigger) QueueContinuation(ctx context.Context, in domain.NextAttempt) (domain.Run, error) {
	if err := in.Validate(); err != nil {
		return domain.Run{}, fmt.Errorf("%w: %v", domain.ErrInvalidNextAttempt, err)
	}
	if t.attempts == nil {
		return domain.Run{}, fmt.Errorf("continuation attempt store is required")
	}
	if t.coordinator == nil {
		return t.attempts.CreateNextAttempt(ctx, in)
	}

	child, brief, resumePhase, priorInvocations, err := t.coordinator.prepareContinuation(ctx, in)
	if err != nil {
		return domain.Run{}, err
	}
	background := context.WithoutCancel(ctx)
	go func() {
		if _, driveErr := t.coordinator.runQueued(background, child, brief, resumePhase, child.TriggerReason, "", priorInvocations); driveErr != nil {
			t.reportFailure(background, continuationTriggerRequest(in), driveErr)
		}
	}()
	return child, nil
}

func continuationTriggerRequest(in domain.NextAttempt) TriggerRequest {
	return TriggerRequest{
		IncidentID:          in.IncidentID,
		LifecycleGeneration: in.LifecycleGeneration,
		DeployedCommit:      in.DeployedCommit,
		ContextVersion:      in.ContextVersion,
		Reason:              in.TriggerReason,
	}
}

const maxAutomaticContinuations = 3

func (t *Trigger) emitAutomatic(ctx context.Context, req TriggerRequest) (domain.Run, error) {
	latest, err := t.attempts.GetLatestForIncident(ctx, req.IncidentID, req.LifecycleGeneration, req.DeployedCommit)
	if errors.Is(err, ErrNotFound) {
		// A new/reopened incident may not have a visible series when this seam is
		// used outside the incident creation transaction; root creation remains idempotent.
		t.recordGate(ctx, req, "root", "series_missing")
		return t.Start(ctx, req)
	}
	if err != nil {
		wrapped := fmt.Errorf("load latest remediation attempt: %w", err)
		t.reportFailure(ctx, req, wrapped)
		return domain.Run{}, wrapped
	}
	if latest.Run.IncidentID != req.IncidentID || latest.Run.LifecycleGeneration != req.LifecycleGeneration || latest.Run.DeployedCommit != req.DeployedCommit {
		wrapped := fmt.Errorf("latest remediation attempt does not match the requested series")
		t.reportFailure(ctx, req, wrapped)
		return domain.Run{}, wrapped
	}

	switch latest.Run.State {
	case domain.RunStateQueued:
		if latest.Run.AttemptNumber > 1 {
			// continuation child 已由 Continue 创建并负责 claim；automatic gate
			// 只观察其 active 状态，避免另一个 webhook 驱动同一个 child。
			t.recordGate(ctx, req, "skipped", "active_attempt")
			return latest.Run, nil
		}
		if t.coordinator == nil {
			// store-only wiring 没有驱动器，existing root 必须保持 queued；避免重复读取并
			// 把未执行的工作误认为已完成。
			t.recordGate(ctx, req, "skipped", "active_attempt")
			return latest.Run, nil
		}
		// 新事故事务会先提交 queued root，再从这里进入 Start；root 仍需
		// 通过 coordinator claim，不能把 queued 永久当成 active no-op。
		t.recordGate(ctx, req, "root", "queued_root")
		return t.Start(ctx, req)
	case domain.RunStatePreparingContext, domain.RunStateDiagnosing,
		domain.RunStateCollectingMoreContext, domain.RunStatePlanning, domain.RunStateRunning,
		domain.RunStatePatching, domain.RunStateValidating, domain.RunStatePublishing:
		t.recordGate(ctx, req, "skipped", "active_attempt")
		return latest.Run, nil
	case domain.RunStateDiagnosisReadyForReview, domain.RunStateCompletedNonCode:
		t.recordGate(ctx, req, "skipped", "usable_result")
		return latest.Run, nil
	case domain.RunStateFailed:
		if !latest.Run.Retryable {
			t.recordGate(ctx, req, "skipped", "non_retryable_failure")
			return latest.Run, nil
		}
		if req.ContextVersion <= latest.Run.ContextVersion {
			t.recordGate(ctx, req, "skipped", "unchanged_context")
			return latest.Run, nil
		}
		if automaticContinuationCount(latest) >= maxAutomaticContinuations {
			t.recordGate(ctx, req, "ceiling", "automatic_ceiling")
			return latest.Run, nil
		}
	default:
		t.recordGate(ctx, req, "skipped", "unsupported_state")
		return latest.Run, nil
	}

	run, err := t.Continue(ctx, domain.NextAttempt{
		ContinuationOfRunID:     latest.Run.RunID,
		SeriesID:                latest.Run.SeriesID,
		IncidentID:              req.IncidentID,
		LifecycleGeneration:     req.LifecycleGeneration,
		DeployedCommit:          req.DeployedCommit,
		ContextVersion:          req.ContextVersion,
		ExpectedPreviousVersion: latest.Run.Version,
		Origin:                  domain.TriggerOriginAutomaticContinue,
		TriggerReason:           domain.TriggerOriginAutomaticContinue,
		ContinuationReason:      "new inbound evidence persisted",
	})
	if isExpectedAutomaticGateError(err) {
		t.recordGate(ctx, req, "skipped", automaticGateRaceReason(err))
		return latest.Run, nil
	}
	if err != nil {
		wrapped := fmt.Errorf("continue remediation: %w", err)
		t.reportFailure(ctx, req, wrapped)
		return domain.Run{}, wrapped
	}
	t.recordGate(ctx, req, "continued", "new_inbound_evidence")
	return run, nil
}

func (t *Trigger) recordGate(ctx context.Context, req TriggerRequest, outcome, reason string) {
	if t.gateReporter == nil {
		return
	}
	t.gateReporter.ReportGate(ctx, req, GateObservation{Outcome: outcome, Reason: reason})
}

func automaticGateRaceReason(err error) string {
	switch {
	case errors.Is(err, domain.ErrStalePredecessor):
		return "stale_predecessor"
	case errors.Is(err, domain.ErrActiveAttempt):
		return "active_attempt"
	case errors.Is(err, domain.ErrAutomaticCeiling):
		return "automatic_ceiling"
	case errors.Is(err, domain.ErrAutomaticGateRejected):
		return "gate_rejected"
	default:
		return "gate_race"
	}
}

func isExpectedAutomaticGateError(err error) bool {
	return errors.Is(err, domain.ErrStalePredecessor) || errors.Is(err, domain.ErrActiveAttempt) ||
		errors.Is(err, domain.ErrAutomaticGateRejected) || errors.Is(err, domain.ErrAutomaticCeiling)
}

func automaticContinuationCount(aggregate domain.RunAggregate) int {
	count := 0
	for _, summary := range aggregate.AttemptSummaries {
		if summary.Origin == domain.TriggerOriginAutomaticContinue {
			count++
		}
	}
	return count
}

// Start 按唯一 key 创建或复用 series 的根 run。
// 返回 queued 时同步推进；root run 的唯一性由 RunStore 的 series key 保证。
func (t *Trigger) Start(ctx context.Context, req TriggerRequest) (domain.Run, error) {
	if !qualifies(req) {
		return domain.Run{}, nil
	}
	if strings.TrimSpace(req.IncidentID) == "" {
		return domain.Run{}, fmt.Errorf("incident id is required")
	}
	if req.LifecycleGeneration < 1 {
		return domain.Run{}, fmt.Errorf("invalid lifecycle generation")
	}
	in := domain.NewRun{
		IncidentID: req.IncidentID, LifecycleGeneration: req.LifecycleGeneration,
		DeployedCommit: req.DeployedCommit, Priority: req.Priority, TriggerReason: req.Reason,
		ContextVersion: req.ContextVersion,
	}
	if t.coordinator != nil {
		run, err := t.coordinator.Start(ctx, in)
		if err != nil {
			t.reportFailure(ctx, req, err)
		}
		return run, err
	}
	run, err := t.store.CreateSeriesAndRun(ctx, in)
	if err != nil {
		err = fmt.Errorf("create series and run: %w", err)
		t.reportFailure(ctx, req, err)
		return domain.Run{}, err
	}
	return run, nil
}

func (t *Trigger) reportFailure(ctx context.Context, req TriggerRequest, err error) {
	if t.failureReporter != nil && err != nil {
		t.failureReporter.Report(ctx, req, err)
	}
}

func qualifies(req TriggerRequest) bool {
	switch req.Reason {
	case TriggerReasonManual:
		return true
	case TriggerReasonAutomatic:
		return req.Priority == "P1" || req.Priority == "P2"
	default:
		return false
	}
}
