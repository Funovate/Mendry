package domain

import "context"

// ModelProvider 执行一次 provider-neutral 模型调用。
type ModelProvider interface {
	Complete(context.Context, ModelTurn) (ModelResult, error)
}

// ToolExecution 是 trusted executor 返回的结果；Artifacts 可声明 observed/verified provenance。
type ToolExecution struct {
	Output      any
	OutputBytes int64
	Artifacts   []Artifact
}

// ToolExecutor 执行 registry 绑定的具体 capability。
type ToolExecutor interface {
	Execute(context.Context, ToolCall) (ToolExecution, error)
}

// ExecutionError 允许 adapter 明确声明失败结果是否已知；write 的未知结果必须进入 waiting。
type ExecutionError struct {
	Code         string
	OutcomeKnown bool
	Cause        error
}

func (e *ExecutionError) Error() string {
	if e == nil || e.Cause == nil {
		return "tool execution failed"
	}
	return e.Cause.Error()
}

func (e *ExecutionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// RunStore 提供 intent-before-execution、ordered result/artifact 与 run/message durability。
// RecordInvocationResult 必须原子写入 result 和同批 artifacts。
type RunStore interface {
	LoadRun(context.Context, string) (Run, error)
	SaveRun(context.Context, Run) error
	ListInvocations(context.Context, string) ([]Invocation, error)
	RecordInvocationIntent(context.Context, Invocation) error
	RecordInvocationResult(context.Context, InvocationResult, []Artifact) error
	ListArtifacts(context.Context, string) ([]Artifact, error)
	AppendArtifacts(context.Context, string, []Artifact) error
	LoadMessages(context.Context, string) ([]ModelMessage, error)
	AppendMessageGroup(context.Context, string, []ModelMessage) error
}
