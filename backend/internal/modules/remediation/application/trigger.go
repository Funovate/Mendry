package application

import (
	"context"
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
}

// FailureReporter 记录 remediation 失败的完整服务端诊断，不接收原始 webhook payload。
type FailureReporter interface {
	Report(context.Context, TriggerRequest, error)
}

// Trigger 是单一同步触发调用点，也是未来 outbox emit 的替换缝。
// 本 slice 在事故写成功后同步调用；后续 slice 把实现换成同一事务内的 outbox insert，
// 不改变 series/run 身份规则（incident_id, lifecycle_generation, deployed_commit）。
type Trigger struct {
	store           domain.RunStore
	coordinator     *RemediationCoordinator
	failureReporter FailureReporter
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
	return &Trigger{store: store, coordinator: coordinator, failureReporter: reporter}, nil
}

// Emit 满足 incidents.RemediationTrigger。不合格请求是 no-op。
func (t *Trigger) Emit(ctx context.Context, req incidentapplication.RemediationRequest) error {
	_, err := t.Start(ctx, TriggerRequest{
		IncidentID: req.IncidentID, LifecycleGeneration: req.LifecycleGeneration,
		DeployedCommit: req.DeployedCommit, Priority: req.Priority, Reason: req.Reason,
	})
	return err
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
