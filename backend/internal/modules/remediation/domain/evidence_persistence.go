package domain

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	EvidenceKindNormalizedAlert      = "normalized_alert"
	EvidenceKindProviderDetail       = "provider_detail"
	EvidenceKindConnectorObservation = "connector_observation"
	EvidenceKindRuntime              = "runtime"
)

// StoredEvidence is the adapter-neutral persisted evidence record. Payload is
// operational evidence only; credential/control material must be absent before
// this port is called.
type StoredEvidence struct {
	EvidenceID             string
	ProjectID              string
	EnvironmentID          string
	SourceID               string
	IncidentID             string
	RunID                  string
	ObservationID          string
	Provider               string
	EvidenceKind           string
	DeduplicationKey       string
	Classification         EvidenceClassification
	Outcome                string
	Available              bool
	Primary                bool
	TemporalCorrelation    bool
	OperationalCorrelation bool
	OccurredAt             *time.Time
	IngestedAt             time.Time
	ContentHash            string
	ByteCount              int64
	Provenance             json.RawMessage
	Payload                json.RawMessage
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

// TriggeringObservation is the bounded, credential-free reference that links
// a remediation run back to the accepted inbound event. The raw webhook
// message is intentionally absent: provider adapters persist the safe
// normalized alert and operational evidence separately.
type TriggeringObservation struct {
	ID            string
	ProjectID     string
	EnvironmentID string
	SourceID      string
	Service       *string
	OccurredAt    time.Time
	Level         string
	Host          *string
	RequestID     *string
	Fingerprint   string
	Attributes    json.RawMessage
	IngestedAt    time.Time
}

// CallbackEvidenceSnapshot 是受信任 adapter 重试 provider detail 所需的入站快照。
// Payload 只在 server-side callback loader 与 provider adapter 之间流转，不进入
// AgentConversation 或模型工具参数。
type CallbackEvidenceSnapshot struct {
	IncidentID    string
	ProjectID     string
	EnvironmentID string
	SourceID      string
	ObservationID string
	Payload       string
	OccurredAt    time.Time
	IngestedAt    time.Time
}

// TencentCLSDetailRequest 标识一次由当前 remediation run 发起的详情重试。
// 请求不携带 URL；trusted adapter 根据 IncidentID 找回已接收的 callback。
type TencentCLSDetailRequest struct {
	RunID         string
	IncidentID    string
	ProjectID     string
	EnvironmentID string
	SourceID      string
}

// TencentCLSDetailResult 是成功详情读取后的有界证据结果。
type TencentCLSDetailResult struct {
	Evidence       StoredEvidence
	BytesRetrieved int64
}

// CallbackEvidenceLoader 只向 trusted provider adapter 提供受范围约束的 callback
// 快照；实现不得把 Payload 传入模型上下文。
type CallbackEvidenceLoader interface {
	LoadTencentCLSCallbackSnapshot(context.Context, string) (CallbackEvidenceSnapshot, error)
}

// TencentCLSDetailPort 是 remediation 的受信任 Tencent CLS detail 读取边界。
// 模型只能通过无参数逻辑工具间接调用它，不能提交 URL 或 HTTP 选项。
type TencentCLSDetailPort interface {
	ResolveTencentCLSDetail(context.Context, TencentCLSDetailRequest) (TencentCLSDetailResult, error)
}

// BootstrapEvidence is the pre-run evidence snapshot used to build the first
// diagnosis turn. Records are limited to the triggering Observation, while
// runtime evidence remains run-scoped and is collected through tools.
type BootstrapEvidence struct {
	Observation        *TriggeringObservation
	Records            []StoredEvidence
	SourceCoverage     []SourceCoverage
	AlertQuality       AlertQuality
	TimeRange          TimeRange
	OriginalTimeValues []string
	TimeBasis          string
	TimeCertainty      string
	MissingEvidence    []string
	Contradictions     []string
}

func (e StoredEvidence) Validate() error {
	for name, value := range map[string]string{
		"project": e.ProjectID, "environment": e.EnvironmentID, "source": e.SourceID,
		"incident": e.IncidentID, "provider": e.Provider, "evidence kind": e.EvidenceKind,
		"deduplication key": e.DeduplicationKey, "content hash": e.ContentHash,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if err := ValidateEvidenceClassification(e.Classification); err != nil {
		return err
	}
	if len(e.Provider) > 64 || len(e.EvidenceKind) > 96 || len(e.DeduplicationKey) > 255 || len(e.ContentHash) != 64 || e.ByteCount < 0 || e.ByteCount > 8<<20 {
		return fmt.Errorf("stored evidence bounds are invalid")
	}
	if len(e.Payload) == 0 || !json.Valid(e.Payload) || len(e.Payload) > 8<<20 {
		return fmt.Errorf("stored evidence payload is invalid")
	}
	if len(e.Provenance) == 0 || !json.Valid(e.Provenance) || len(e.Provenance) > 64<<10 {
		return fmt.Errorf("stored evidence provenance is invalid")
	}
	return nil
}

// EvidenceAssessment is the durable service-owned decision applied after a
// model diagnosis. It is intentionally independent from Decision.Confidence.
type EvidenceAssessment struct {
	RunID               string
	ProjectID           string
	IncidentID          string
	ConfidenceCap       float64
	EffectiveConfidence float64
	PlanningEligible    bool
	Outcome             FixabilityClass
	Reasons             []string
	MissingEvidence     []string
	Contradictions      []string
	DirectEvidenceIDs   []string
	AssessedAt          time.Time
}

// EvidencePersistencePort owns append-only evidence and run-scoped gate
// assessment persistence. It does not expose database or HTTP types.
type EvidencePersistencePort interface {
	AppendEvidence(context.Context, StoredEvidence) (StoredEvidence, error)
	ResolveEvidence(context.Context, string, []EvidenceCitation) (EvidenceResolution, error)
	PersistEvidenceAssessment(context.Context, EvidenceAssessment) error
}

// ContinuationEvidenceQuery 限定同 series、attempt_number <= through 的证据
// 读取。查询必须保持 attempt 单调：不能读到未来 attempt 或其他
// series/incident 的证据行。
type ContinuationEvidenceQuery struct {
	SeriesID             string
	ThroughAttemptNumber int32
	Limit                int
}

// EvidenceIndexEntry 是同 series 证据索引的紧凑条目（D3/R10）：只含标识与
// 元数据，绝不内联 payload。模型需要内容时必须通过 evidence.read 按
// EvidenceID 重新读取原始持久化证据，因此索引条目不会把摘要升级成证据（R15）。
type EvidenceIndexEntry struct {
	EvidenceID     string
	Kind           string
	Provider       string
	Classification EvidenceClassification
	SourceAttempt  int32
	ContentHash    string
}

// ContinuationEvidenceStore 是同 series 早期 attempt 的证据读取边界，供
// diagnosis continuation 引用。ListContinuationRuntimeEvidence 返回带 payload
// 的 runtime 记录（canonical 投影，用于既有全量渲染）；
// ListContinuationEvidenceIndex 返回其余证据种类的紧凑索引，供模型按 ID 通过
// evidence.read 重新读取。它独立于冻结的 RunStore；实现不得把 evidence 行复制
// 或重新归属到 child run。
type ContinuationEvidenceStore interface {
	ListContinuationRuntimeEvidence(context.Context, ContinuationEvidenceQuery) ([]StoredEvidence, error)
	ListContinuationEvidenceIndex(context.Context, ContinuationEvidenceQuery) ([]EvidenceIndexEntry, error)
}

// BootstrapEvidenceLoader loads only evidence that belongs to the triggering
// inbound Observation. It is separate from citation resolution so the model
// cannot choose the initial context or use an evidence ID as an authority.
type BootstrapEvidenceLoader interface {
	LoadBootstrapEvidence(context.Context, string) (BootstrapEvidence, error)
}
