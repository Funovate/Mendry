package application

import (
	"context"
	"sync/atomic"
	"time"

	"fixthe/backend/internal/modules/remediation/domain"
)

// RunIdentity 是所有 remediation observation 共享的关联身份，不含项目凭据。
type RunIdentity struct {
	RunID               string
	SeriesID            string
	IncidentID          string
	LifecycleGeneration int64
}

// RunStartedObservation 描述已持久化 queued run 的启动边界。
type RunStartedObservation struct {
	Run           RunIdentity
	Phase         domain.RunState
	TriggerReason string
	Priority      string
}

// StateTransitionObservation 描述一次成功提交的状态迁移及本次预算 effect。
type StateTransitionObservation struct {
	Run    RunIdentity
	From   domain.RunState
	To     domain.RunState
	Effect domain.Effect
}

// ContextObservation 描述一次 repository/evidence 上下文操作及可选安全 payload。
type ContextObservation struct {
	Run          RunIdentity
	Phase        domain.RunState
	Operation    string
	Duration     time.Duration
	Outcome      string
	FailureClass string
	Bytes        int64
	PayloadKind  string
	Payload      any
}

// ModelTurnObservation 描述一次有序模型轮次及逻辑 request/response。
type ModelTurnObservation struct {
	Run          RunIdentity
	Phase        domain.RunState
	Sequence     int64
	Duration     time.Duration
	Outcome      string
	FailureClass string
	ErrorMessage string
	EnvelopeKind string
	Request      domain.ModelTurn
	Response     domain.ModelResult
}

// ToolObservation 描述一次有序 tool request/result，包括 policy rejection。
type ToolObservation struct {
	Run           RunIdentity
	Phase         domain.RunState
	Sequence      int64
	Tool          string
	Duration      time.Duration
	Outcome       string
	FailureClass  string
	RejectionCode ToolRejectionCode
	ErrorCode     string
	Retryable     bool
	ErrorMessage  string
	Bytes         int64
	Parameters    map[string]interface{}
	Result        any
}

// RunCompletedObservation 描述 run 的终态和最终累计计数。
type RunCompletedObservation struct {
	Run           RunIdentity
	TerminalState domain.RunState
	Duration      time.Duration
	Outcome       string
	Aggregate     domain.RunAggregate
}

// RunObserver 接收 credential-free application observation；实现负责投影到具体传输。
type RunObserver interface {
	RunStarted(context.Context, RunStartedObservation)
	StateTransitioned(context.Context, StateTransitionObservation)
	ContextCompleted(context.Context, ContextObservation)
	ModelTurnCompleted(context.Context, ModelTurnObservation)
	ToolCompleted(context.Context, ToolObservation)
	RunCompleted(context.Context, RunCompletedObservation)
}

type noopRunObserver struct{}

func (noopRunObserver) RunStarted(context.Context, RunStartedObservation)             {}
func (noopRunObserver) StateTransitioned(context.Context, StateTransitionObservation) {}
func (noopRunObserver) ContextCompleted(context.Context, ContextObservation)          {}
func (noopRunObserver) ModelTurnCompleted(context.Context, ModelTurnObservation)      {}
func (noopRunObserver) ToolCompleted(context.Context, ToolObservation)                {}
func (noopRunObserver) RunCompleted(context.Context, RunCompletedObservation)         {}

func normalizeRunObserver(observer RunObserver) RunObserver {
	if observer == nil {
		return noopRunObserver{}
	}
	return observer
}

func runIdentity(run domain.Run) RunIdentity {
	return RunIdentity{
		RunID: run.RunID, SeriesID: run.SeriesID, IncidentID: run.IncidentID,
		LifecycleGeneration: run.LifecycleGeneration,
	}
}

type runObservationContext struct {
	identity RunIdentity
	sequence atomic.Int64
}

type runObservationContextKey struct{}

func withRunObservationContext(ctx context.Context, run domain.Run) context.Context {
	return context.WithValue(ctx, runObservationContextKey{}, &runObservationContext{identity: runIdentity(run)})
}

func observationRun(ctx context.Context) RunIdentity {
	trace, _ := ctx.Value(runObservationContextKey{}).(*runObservationContext)
	if trace == nil {
		return RunIdentity{}
	}
	return trace.identity
}

func nextObservationSequence(ctx context.Context) int64 {
	trace, _ := ctx.Value(runObservationContextKey{}).(*runObservationContext)
	if trace == nil {
		return 0
	}
	return trace.sequence.Add(1)
}
