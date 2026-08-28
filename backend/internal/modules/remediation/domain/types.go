package domain

import (
	"errors"
	"fmt"
	"time"
)

// RepoRef 是 remediation 的项目仓库身份与历史基线元数据；Git adapter
// 使用项目配置的 production branch 最新代码，不用 Commit 选择读取对象。
type RepoRef struct {
	ProjectID string
	RemoteURL string
	Commit    string
}

// EvidenceScope 定义日志证据的查询范围，不包含凭据或裸客户端。
type EvidenceScope struct {
	ProjectID     string
	EnvironmentID string
	SourceID      string
	TimeRange     TimeRange
}

// TimeRange 定义时间窗口。
type TimeRange struct {
	Start time.Time
	End   time.Time
}

// EvidenceAnchor 定义日志上下文的锚点。
type EvidenceAnchor struct {
	EvidenceID  string
	Timestamp   time.Time
	LinesBefore int
	LinesAfter  int
}

// RunState 是 remediation run 的生命周期状态。
type RunState string

const (
	RunStateQueued                  RunState = "queued"
	RunStatePreparingContext        RunState = "preparing_context"
	RunStateDiagnosing              RunState = "diagnosing"
	RunStateCollectingMoreContext   RunState = "collecting_more_context"
	RunStatePlanning                RunState = "planning"
	RunStateDiagnosisReadyForReview RunState = "diagnosis_ready_for_review"
	RunStateCompletedNonCode        RunState = "completed_non_code"
	RunStateBlockedManualReview     RunState = "blocked_manual_review"
	RunStateFailed                  RunState = "failed"
	RunStateBudgetExhausted         RunState = "budget_exhausted"
	// Reserved for later slices
	RunStatePatching            RunState = "patching"
	RunStateValidating          RunState = "validating"
	RunStatePublishing          RunState = "publishing"
	RunStateAwaitingHumanReview RunState = "awaiting_human_review"
	RunStateRunning             RunState = "running" // Alias for active state
)

// ParseRunState parses a string into a RunState.
func ParseRunState(s string) (RunState, error) {
	switch RunState(s) {
	case RunStateQueued, RunStatePreparingContext, RunStateDiagnosing,
		RunStateCollectingMoreContext, RunStatePlanning, RunStateDiagnosisReadyForReview,
		RunStateCompletedNonCode, RunStateBlockedManualReview, RunStateFailed,
		RunStateBudgetExhausted, RunStatePatching, RunStateValidating,
		RunStatePublishing, RunStateAwaitingHumanReview, RunStateRunning:
		return RunState(s), nil
	default:
		return "", fmt.Errorf("unknown run state: %s", s)
	}
}

// TriggerOrigin 标识 remediation attempt 如何进入 series。continuation 使用独立值，
// 使 review 和 audit 能区分 webhook gate 与 operator 操作。
type TriggerOrigin string

const (
	TriggerOriginAutomatic         = "automatic"
	TriggerOriginManual            = "manual"
	TriggerOriginAutomaticContinue = "automatic_continue"
	TriggerOriginManualContinue    = "manual_continue"
	// 使用 persisted column 术语的调用方可继续使用这些兼容名称。
	TriggerReasonAutomatic         = TriggerOriginAutomatic
	TriggerReasonManual            = TriggerOriginManual
	TriggerReasonAutomaticContinue = TriggerOriginAutomaticContinue
	TriggerReasonManualContinue    = TriggerOriginManualContinue
)

// IsKnown 检查值是否属于安全的 persisted trigger origin。
func (o TriggerOrigin) IsKnown() bool {
	switch string(o) {
	case TriggerOriginAutomatic, TriggerOriginManual,
		TriggerOriginAutomaticContinue, TriggerOriginManualContinue:
		return true
	default:
		return false
	}
}

// FixabilityClass 分类事故的可自动修复性。
type FixabilityClass string

const (
	FixabilityCodeFixable          FixabilityClass = "code_fixable"
	FixabilityExternalDependency   FixabilityClass = "external_dependency"
	FixabilityConfiguration        FixabilityClass = "configuration"
	FixabilityData                 FixabilityClass = "data"
	FixabilityInfrastructure       FixabilityClass = "infrastructure"
	FixabilityInsufficientEvidence FixabilityClass = "insufficient_evidence"
	FixabilityUnsafeToAutomate     FixabilityClass = "unsafe_to_automate"
)

// RiskClassification 分类变更风险。
type RiskClassification string

const (
	RiskOrdinary           RiskClassification = "ordinary"
	RiskHighRisk           RiskClassification = "high_risk"
	RiskDeniedControlPlane RiskClassification = "denied_control_plane"
)

// Decision 是模型在 run 中做出的结构化决策。
type Decision struct {
	DecisionID            string
	Fixability            FixabilityClass
	Confidence            float64
	CausalReasoning       string
	Contradictions        []string
	MissingEvidence       []string
	EvidenceCitations     []string
	RecommendedNextAction string
	EvidenceAssessment    *EvidenceGateDecision
	RecordedAt            time.Time
}

// RepairPlanCandidate 是一个候选修复计划。
type RepairPlanCandidate struct {
	PlanID           string
	EvidenceRefs     []string
	AffectedFiles    []string
	IntendedBehavior string
	Risk             RiskClassification
	RollbackStrategy string
	Rationale        string
	Recommended      bool
}

// ToolInvocation 记录一次工具调用。
type ToolInvocation struct {
	InvocationID      string
	ToolName          string
	Phase             RunState
	ParametersSummary string // 不包含秘密
	ResultSummary     string
	EvidenceIDs       []string
	BytesRetrieved    int64
	InvokedAt         time.Time
	CompletedAt       time.Time
	Error             string
}

// StateTransitionEffect 记录状态转换的副作用。
type StateTransitionEffect struct {
	ArtifactRefs   []string
	BudgetConsumed BudgetCounters
	Message        string
}

// BudgetCounters 记录资源消耗。
type BudgetCounters struct {
	ElapsedSeconds  int64
	ModelCalls      int64
	ModelTokens     int64
	ModelCostCents  int64
	ToolCalls       int64
	EvidenceBytes   int64
	RepositoryBytes int64
}

// BudgetLimits 定义单次 remediation run 的硬预算上限；模型 token 只记录，
// 不作为终止条件。零值由 application 层替换为当前 slice 的保守默认值。
type BudgetLimits struct {
	MaxElapsed         time.Duration
	MaxModelCalls      int64
	MaxModelCostCents  int64
	MaxToolCalls       int64
	MaxEvidenceBytes   int64
	MaxRepositoryBytes int64
}

// TreeOptions 定义 list_tree 的选项。
type TreeOptions struct {
	MaxDepth   int
	MaxEntries int
}

// TreeListing 是目录树列表结果。
type TreeListing struct {
	Entries   []TreeEntry
	Truncated bool
}

// TreeEntry 是目录树中的一个条目。
type TreeEntry struct {
	Path string
	Type string // "file" | "dir"
	Size int64
	Mode string
}

// ReadOptions 定义 read_file 的选项。
type ReadOptions struct {
	MaxBytes int64
}

// FileContent 是文件读取结果。
type FileContent struct {
	Path      string
	Content   []byte
	Truncated bool
	Reason    string
}

// SearchQuery 定义代码搜索查询。
type SearchQuery struct {
	Pattern    string
	PathGlob   string
	MaxResults int
}

// SearchResult 是搜索结果。
type SearchResult struct {
	Matches   []SearchMatch
	Truncated bool
}

// SearchMatch 是一个搜索匹配。
type SearchMatch struct {
	Path       string
	LineNumber int
	Line       string
}

// HistoryOptions 定义 history 查询选项。
type HistoryOptions struct {
	MaxCommits int
	Since      time.Time
}

// History 是提交历史结果。
type History struct {
	Commits   []CommitInfo
	Truncated bool
}

// CommitInfo 是一个 commit 的信息。
type CommitInfo struct {
	Hash      string
	Author    string
	Timestamp time.Time
	Message   string
}

// LogQuery 定义日志查询。
type LogQuery struct {
	Keywords []string
	Level    string
	MaxLines int
	MaxBytes int64
}

// EvidencePage 是一页证据结果。
type EvidencePage struct {
	Lines     []EvidenceLine
	Truncated bool
}

// EvidenceLine 是一行日志证据。
type EvidenceLine struct {
	EvidenceID string
	Timestamp  time.Time
	Level      string
	Message    string
	Host       string
	Redacted   bool
}

// ErrModelOutputExhausted 表示 provider 在生成最终内容或 tool call 前耗尽输出预算。
var ErrModelOutputExhausted = errors.New("model output token budget exhausted")

// ModelTurn 是一次模型调用请求。
type ModelTurn struct {
	// ProjectID 供 provider 适配器解析同项目的约定凭据；不得携带秘密。
	ProjectID    string
	SystemPrompt string
	UserMessage  string
	// Continuation 是 provider-native 历史之后仅追加一次的本轮输入。
	// 旧 provider 忽略它并继续使用完整 UserMessage。
	Continuation string
	// Messages 保存当前轮之前的有序对话消息；旧 provider 可继续只使用
	// SystemPrompt / UserMessage。
	Messages    []ModelMessage
	Tools       []ToolDefinition
	MaxTokens   int
	Temperature float64
}

// ModelMessage 是跨 provider 的最小消息表示，不暴露任何 SDK 类型。
type ModelMessage struct {
	Role       string
	Content    string
	ToolCallID string
	ToolCalls  []ToolCall
}

// ToolCall 是 provider 返回的结构化工具调用。Arguments 已经解析为 JSON
// object，可信 gateway 仍负责最终的工具、阶段、参数和预算校验。
type ToolCall struct {
	ID        string
	Name      string
	Arguments map[string]interface{}
}

// ToolDefinition 定义一个可用工具。
type ToolDefinition struct {
	Name        string
	Description string
	Parameters  map[string]interface{}
}

// ModelResult 是模型调用结果。
type ModelResult struct {
	Content             string
	Provider            string
	Model               string
	ToolCalls           []ToolCall
	ModelCalls          int
	UsageTokensIn       int64
	UsageTokensOut      int64
	UsageTokens         int64
	UsageCostCents      int64
	RequestBytes        int64
	ToolCount           int
	ToolSchemaBytes     int64
	CacheTokensReported bool
	CacheHitTokens      int64
	CacheMissTokens     int64
	FinishReason        string
}

// NewRun 是创建 series 和 run 的输入。
type NewRun struct {
	IncidentID          string
	LifecycleGeneration int64
	DeployedCommit      string
	Priority            string
	TriggerReason       string
	ContextVersion      int64
}

// AttemptSummary 是随 requested run 暴露的有界安全 history projection；不含 prompt、
// provider payload、credential 或无界 connector output。
type AttemptSummary struct {
	ID                  string
	RunID               string
	AttemptNumber       int32
	Status              RunState
	Origin              string
	ContinuationOfRunID string
	ContinuationReason  string
	ContextVersion      int64
	TerminalReason      string
	Retryable           bool
	Version             int64
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// Run 是一个 remediation run 的聚合。
type Run struct {
	RunID               string
	SeriesID            string
	IncidentID          string
	LifecycleGeneration int64
	DeployedCommit      string
	AttemptNumber       int32
	State               RunState
	Origin              string
	TriggerReason       string
	ContinuationOfRunID string
	ContinuationReason  string
	ContextVersion      int64
	TerminalReason      string
	Retryable           bool
	Budget              BudgetCounters
	ModelProvider       string
	ModelName           string
	Version             int64
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// RunAggregate 是完整的 run 聚合，包括决策、计划和工具调用。
type RunAggregate struct {
	Run                Run
	Decisions          []Decision
	Plans              []RepairPlanCandidate
	ToolInvocations    []ToolInvocation
	ArtifactReferences []string
	SuggestedDiff      string
	RecommendedPlanID  string
	AttemptSummaries   []AttemptSummary
}
