package domain

import "context"

// RepositoryReadPort 读取项目配置 production branch 的当前代码内容；调用方只传递
// 项目身份和历史基线元数据，不携带凭据或裸客户端。
type RepositoryReadPort interface {
	ListTree(ctx context.Context, ref RepoRef, path string, opts TreeOptions) (TreeListing, error)
	ReadFile(ctx context.Context, ref RepoRef, path string, opts ReadOptions) (FileContent, error)
	Search(ctx context.Context, ref RepoRef, query SearchQuery) (SearchResult, error)
	History(ctx context.Context, ref RepoRef, path string, opts HistoryOptions) (History, error)
}

// EvidenceLogPort 检索有界、脱敏的证据/日志窗口。
// 不要把 SSH inspect 或动态 MCP 能力加到这个接口上；SSH 走 SSHInspectPort。
type EvidenceLogPort interface {
	Search(ctx context.Context, scope EvidenceScope, query LogQuery) (EvidencePage, error)
	GetContext(ctx context.Context, scope EvidenceScope, anchor EvidenceAnchor) (EvidencePage, error)
}

// SSHInspectPort 在远端执行 gateway 已经解析并重写过的 inspect 命令。
// 适配器内部注入凭据；调用者只传递不透明 scope 和重建后的命令字符串。
type SSHInspectPort interface {
	Inspect(ctx context.Context, scope EvidenceScope, req SSHInspectRequest) (SSHInspectResult, error)
}

// SSHInspectRequest 是 gateway 重建后的 quoted argv 命令，绝不是模型原始字符串。
type SSHInspectRequest struct {
	Command string
}

// SSHInspectResult 是有界的原始 inspect 输出。Stdout/Stderr 不做 secret-pattern 脱敏。
type SSHInspectResult struct {
	Command        string
	ExitCode       int
	Stdout         string
	Stderr         string
	Truncated      bool
	BytesRetrieved int64
}

// LLMProviderPort 运行一次有界的模型调用。
// 返回归一化的使用量和待 AgentEngine 校验的原始文本；
// provider SDK 类型和秘密不越过此边界。
type LLMProviderPort interface {
	Complete(ctx context.Context, req ModelTurn) (ModelResult, error)
}

// RunStore 持久化 run/series/decision/plan/artifact/tool-invocation 记录，
// 使用乐观版本控制，在单个事务内操作。
type RunStore interface {
	CreateSeriesAndRun(ctx context.Context, in NewRun) (Run, error)
	AppendDecision(ctx context.Context, runID string, d Decision) error
	RecordToolInvocation(ctx context.Context, runID string, t ToolInvocation) error
	Transition(ctx context.Context, runID string, from, to RunState, effect Effect) error
	Get(ctx context.Context, runID string) (RunAggregate, error)
}

// AttemptStore 负责 continuation 专用读取和 linked next attempt 的原子创建。
// 它独立于 RunStore，以保持既有 coordinator 和 adapter 使用的原始 port 不变。
type AttemptStore interface {
	GetLatestForIncident(ctx context.Context, incidentID string, generation int64, deployedCommit string) (RunAggregate, error)
	CreateNextAttempt(ctx context.Context, in NextAttempt) (Run, error)
}

// ReconfiguredAttemptStore creates a linked attempt from the current project
// policy. It is deliberately separate from CreateNextAttempt: ordinary retry
// keeps the predecessor's immutable policy snapshot.
type ReconfiguredAttemptStore interface {
	CreateReconfiguredAttempt(ctx context.Context, in NextAttempt) (Run, error)
}

// PlanningCheckpointStore 在 bounded review history 之外按 durable series/context
// 查询最近一次通过 evidence gate 的 code_fixable decision。throughAttemptNumber
// 保证 continuation 不会读取 expected predecessor 之后的尝试。
type PlanningCheckpointStore interface {
	GetLatestPlanningCheckpoint(ctx context.Context, seriesID string, contextVersion int64, throughAttemptNumber int32) (RunAggregate, error)
}
