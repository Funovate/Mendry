package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Event 是进入 harness 的版本化中立输入；Payload 不能选择凭据或执行策略。
type Event struct {
	ID         string
	Version    string
	Source     string
	ExternalID string
	OccurredAt time.Time
	Payload    map[string]any
	Context    map[string]string
}

// RunState 是通用 run 的 durable 生命周期。
type RunState string

const (
	RunStateQueued    RunState = "queued"
	RunStateRunning   RunState = "running"
	RunStateWaiting   RunState = "waiting"
	RunStateSucceeded RunState = "succeeded"
	RunStateFailed    RunState = "failed"
	RunStateCancelled RunState = "cancelled"
)

// Run 保存 profile/policy 快照、独立预算计数和最终结果。
type Run struct {
	ID                  string
	EventID             string
	Goal                string
	Input               map[string]any
	Context             map[string]string
	ProfileName         string
	ProfileVersion      string
	PolicyRef           string
	ConfigurationDigest string
	State               RunState
	ReasonCode          string
	Budget              Budget
	Result              map[string]any
	StartedAt           time.Time
	UpdatedAt           time.Time
	Version             int64
}

// BudgetLimits 为模型、工具、输出和 wall-clock 各自设置硬上限；零值表示该维度不启用。
type BudgetLimits struct {
	MaxElapsed     time.Duration
	MaxModelCalls  int64
	MaxToolCalls   int64
	MaxOutputBytes int64
}

// BudgetCounters 是必须随 run durable 保存的实际消耗。
type BudgetCounters struct {
	ModelCalls  int64
	ToolCalls   int64
	OutputBytes int64
}

// Budget 将 immutable limits 与 durable counters 绑定在同一个 run 快照中。
type Budget struct {
	Limits   BudgetLimits
	Consumed BudgetCounters
}

// ToolEffect 是 trusted registration 声明的外部效果类别。
type ToolEffect string

const (
	ToolEffectRead  ToolEffect = "read"
	ToolEffectWrite ToolEffect = "write"
)

// ToolDefinition 是发送给模型的版本化 capability，不包含 executor 或凭据。
type ToolDefinition struct {
	Name        string
	Version     string
	Description string
	Parameters  map[string]any
	Effect      ToolEffect
}

// ToolCall 是 provider-neutral 的结构化工具请求。
type ToolCall struct {
	ID             string
	Name           string
	Version        string
	Arguments      map[string]any
	InvocationID   string
	IdempotencyKey string
}

// ModelMessage 是 provider-neutral history item。
type ModelMessage struct {
	Role       string
	Content    string
	ToolCallID string
	ToolCalls  []ToolCall
}

// ModelTurn 是一次模型调用，Messages 只包含完整配对的历史组。
type ModelTurn struct {
	Binding      string
	SystemPrompt string
	UserMessage  string
	Continuation string
	Messages     []ModelMessage
	Tools        []ToolDefinition
	MaxTokens    int
	Temperature  float64
}

// ModelResult 是 provider 返回的中立结果和计费信息。
type ModelResult struct {
	Content             string
	Provider            string
	Model               string
	ToolCalls           []ToolCall
	ModelCalls          int
	InputTokens         int64
	OutputTokens        int64
	UsageTokens         int64
	UsageCostCents      int64
	OutputBytes         int64
	RequestBytes        int64
	ToolCount           int
	ToolSchemaBytes     int64
	CacheTokensReported bool
	CacheHitTokens      int64
	CacheMissTokens     int64
	FinishReason        string
}

// InvocationState 描述 intent 与结果的 durable 状态。
type InvocationState string

const (
	InvocationPending     InvocationState = "pending"
	InvocationSucceeded   InvocationState = "succeeded"
	InvocationFailed      InvocationState = "failed"
	InvocationUnknown     InvocationState = "unknown"
	InvocationRejected    InvocationState = "rejected"
	InvocationInterrupted InvocationState = "interrupted"
)

// Invocation 是外部调用前必须先写入的 intent；ArgumentsDigest 避免 audit 保存原始秘密。
type Invocation struct {
	ID              string
	RunID           string
	ProviderCallID  string
	Sequence        int64
	ToolName        string
	ToolVersion     string
	Effect          ToolEffect
	ArgumentsDigest string
	IdempotencyKey  string
	Authorized      bool
	State           InvocationState
	OutputBytes     int64
	CreatedAt       time.Time
}

// InvocationResult 是按 Invocation.Sequence 排序的 durable 观察结果。
type InvocationResult struct {
	InvocationID string
	Sequence     int64
	State        InvocationState
	Code         string
	Output       any
	OutputBytes  int64
	CompletedAt  time.Time
}

// ArtifactProvenance 区分模型陈述、外部观察和独立验证。
type ArtifactProvenance string

const (
	ProvenanceModel    ArtifactProvenance = "model"
	ProvenanceObserved ArtifactProvenance = "observed"
	ProvenanceVerified ArtifactProvenance = "verified"
)

// VerificationSubject 将 verification 明确关联到被验证 action/artifact。
type VerificationSubject struct {
	ArtifactID string
	ExternalID string
}

// VerificationStatus 是 verified artifact 的可信检查结论。
type VerificationStatus string

const (
	VerificationPassed VerificationStatus = "passed"
	VerificationFailed VerificationStatus = "failed"
)

// Artifact 是自由类型、版本化且有 provenance 的 run 输出。
type Artifact struct {
	ID                 string
	RunID              string
	InvocationID       string
	Type               string
	SchemaVersion      string
	Data               any
	Reference          string
	ExternalID         string
	Provenance         ArtifactProvenance
	Subject            *VerificationSubject
	VerificationStatus VerificationStatus
	CreatedAt          time.Time
}

// Validate 检查 artifact 的内容边界、provenance 和 verification subject 不变量。
func (a Artifact) Validate() error {
	if strings.TrimSpace(a.Type) == "" || len(a.Type) > 256 || strings.TrimSpace(a.SchemaVersion) == "" || len(a.SchemaVersion) > 64 {
		return errors.New("artifact type and schema version are required and bounded")
	}
	if a.Data == nil && strings.TrimSpace(a.Reference) == "" {
		return errors.New("artifact data or reference is required")
	}
	if len(a.Reference) > 4096 {
		return errors.New("artifact reference exceeds bound")
	}
	if a.Data != nil {
		encoded, err := json.Marshal(a.Data)
		if err != nil || len(encoded) > 1<<20 || emptyJSONValue(encoded) {
			return errors.New("artifact data must be non-empty bounded JSON")
		}
	}
	switch a.Provenance {
	case ProvenanceModel, ProvenanceObserved:
		if a.Subject != nil || a.VerificationStatus != "" {
			return errors.New("only verified artifacts may carry verification metadata")
		}
	case ProvenanceVerified:
		if a.Subject == nil || (strings.TrimSpace(a.Subject.ArtifactID) == "" && strings.TrimSpace(a.Subject.ExternalID) == "") {
			return errors.New("verified artifact requires a subject")
		}
		if a.VerificationStatus != VerificationPassed && a.VerificationStatus != VerificationFailed {
			return errors.New("verified artifact requires an explicit passed or failed status")
		}
	default:
		return fmt.Errorf("unsupported artifact provenance %q", a.Provenance)
	}
	return nil
}

func emptyJSONValue(encoded []byte) bool {
	value := strings.TrimSpace(string(encoded))
	return value == "" || value == "null" || value == `""` || value == "{}" || value == "[]"
}

// CompletionMode 选择 composition 注册的 completion evaluator。
type CompletionMode string

const (
	CompletionSolutionDelivered      CompletionMode = "solution_delivered"
	CompletionActionWithVerification CompletionMode = "action_with_verification"
	CompletionObservedRemoteBranch   CompletionMode = "observed_remote_branch"
	CompletionPipelineVerification   CompletionMode = "pipeline_verification"
)

// CompletionContract 是 profile 固定的 evaluator 名称、版本和可信参数。
type CompletionContract struct {
	Mode       CompletionMode
	Version    string
	Parameters map[string]any
}

// ProfileDefinition 固定 workflow 行为与 completion contract 的版本。
type ProfileDefinition struct {
	Name       string
	Version    string
	Completion CompletionContract
}

// ProfileContext 是 profile 准备下一轮时可读取的有界 durable snapshot。
type ProfileContext struct {
	Run         Run
	Input       map[string]any
	Context     map[string]string
	History     []ModelMessage
	Tools       []ToolDefinition
	Invocations []Invocation
	Artifacts   []Artifact
}

// ProfileResult 是 profile 对非工具模型输出的解释；Artifacts 必须保持 model provenance。
type ProfileResult struct {
	Artifacts []Artifact
	Result    map[string]any
	Stop      bool
}

// Profile 保留业务协议解释权，同时让 runner 拥有模型/工具序列。
type Profile interface {
	Definition() ProfileDefinition
	Prepare(context.Context, ProfileContext) (ModelTurn, error)
	Interpret(context.Context, ModelResult) (ProfileResult, error)
}
