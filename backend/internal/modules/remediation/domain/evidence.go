package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// AlertQuality describes how much semantic meaning the triggering alert carries.
type AlertQuality string

const (
	AlertQualitySparse     AlertQuality = "sparse"
	AlertQualityAnchorOnly AlertQuality = "anchor_only"
	AlertQualityEnriched   AlertQuality = "enriched"
)

// EvidenceClassification separates directly observed faults from context.
type EvidenceClassification string

const (
	EvidenceDirectFault       EvidenceClassification = "direct_fault"
	EvidenceCorrelatedSupport EvidenceClassification = "correlated_supporting"
	EvidenceContextual        EvidenceClassification = "contextual"
	EvidenceUnrelated         EvidenceClassification = "unrelated"
	EvidenceContradictory     EvidenceClassification = "contradictory"
)

// SourceCoverageStatus is the service-owned state of an expected source.
type SourceCoverageStatus string

const (
	SourceConfigured       SourceCoverageStatus = "configured"
	SourceInspectedSuccess SourceCoverageStatus = "inspected_success"
	SourceInspectedEmpty   SourceCoverageStatus = "inspected_empty"
	SourceUnavailable      SourceCoverageStatus = "unavailable"
	SourceNotApplicable    SourceCoverageStatus = "not_applicable"
	SourceNotInspected     SourceCoverageStatus = "not_inspected"
)

// EvidenceCitation is the typed form of a model evidence reference.
// Unmarshalling a string remains supported for v1 model responses; the service
// resolves the reference and supplies the authoritative classification.
type EvidenceCitation struct {
	EvidenceID     string                 `json:"evidenceId"`
	Classification EvidenceClassification `json:"classification,omitempty"`
}

// EvidenceCitationFieldError 标记严格模型契约中已知但不支持的引用字段，供
// application 层映射为有界的 provider-neutral 修正，同时保留具体错误供操作员诊断。
type EvidenceCitationFieldError struct {
	Field string
}

// Error 返回供 operator log 使用的具体字段诊断；application 层不得把它原文回喂模型。
func (e *EvidenceCitationFieldError) Error() string {
	if e == nil {
		return "evidence citation contains an unsupported field"
	}
	return fmt.Sprintf("evidence citation field %q is unsupported; use \"evidenceId\"", e.Field)
}

// UnmarshalJSON 保持证据引用的严格输入契约；已知的 evidenceRef 字段返回 typed
// validation error，不能作为 evidenceId 的兼容别名进入后续 authority 解析。
func (c *EvidenceCitation) UnmarshalJSON(raw []byte) error {
	var id string
	if err := json.Unmarshal(raw, &id); err == nil {
		c.EvidenceID = strings.TrimSpace(id)
		c.Classification = ""
		return nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err == nil && object != nil {
		if _, exists := object["evidenceRef"]; exists {
			return &EvidenceCitationFieldError{Field: "evidenceRef"}
		}
	}
	var value struct {
		EvidenceID     string                 `json:"evidenceId"`
		ID             string                 `json:"id"`
		Classification EvidenceClassification `json:"classification"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if value.EvidenceID == "" {
		value.EvidenceID = value.ID
	}
	c.EvidenceID = strings.TrimSpace(value.EvidenceID)
	c.Classification = value.Classification
	return nil
}

// EvidenceRecord is the trusted, persisted view used by the gate. Model text
// cannot create this record or change its classification.
type EvidenceRecord struct {
	EvidenceID             string
	Classification         EvidenceClassification
	SourceID               string
	Available              bool
	Primary                bool
	TemporalCorrelation    bool
	OperationalCorrelation bool
}

// SourceCoverage records what happened to one expected evidence source.
type SourceCoverage struct {
	SourceID     string               `json:"sourceId"`
	Kind         string               `json:"kind"`
	Primary      bool                 `json:"primary"`
	Status       SourceCoverageStatus `json:"status"`
	Reason       string               `json:"reason,omitempty"`
	DirectBridge bool                 `json:"directBridge,omitempty"`
}

// TimeAssessment preserves raw values while describing the comparison policy.
type TimeAssessment struct {
	OriginalValues  []string `json:"originalValues"`
	NormalizedStart string   `json:"normalizedStart,omitempty"`
	NormalizedEnd   string   `json:"normalizedEnd,omitempty"`
	Basis           string   `json:"basis"`
	Certainty       string   `json:"certainty"`
	Contradictory   bool     `json:"contradictory,omitempty"`
}

// CorrelationAssessment captures temporal and operational bridges to the alert.
type CorrelationAssessment struct {
	Temporal     bool `json:"temporal"`
	Operational  bool `json:"operational"`
	HostIdentity bool `json:"hostIdentity"`
	DirectBridge bool `json:"directBridge"`
}

// CausalClosure asserts that the diagnosis explains the original fault signal.
type CausalClosure struct {
	ExplainsOriginalSymptom bool   `json:"explainsOriginalSymptom"`
	Explanation             string `json:"explanation"`
}

// NonActionableHypothesis is retained for operators without authorizing a fix.
type NonActionableHypothesis struct {
	ID            string   `json:"id"`
	Summary       string   `json:"summary"`
	EvidenceRefs  []string `json:"evidenceRefs,omitempty"`
	NonActionable bool     `json:"nonActionable"`
}

// EvidenceResolution is returned by the trusted evidence persistence port.
type EvidenceResolution struct {
	Records                []EvidenceRecord
	Sources                []SourceCoverage
	Time                   *TimeAssessment
	Correlation            *CorrelationAssessment
	MaterialContradictions []string
}

// CitationClassificationMismatch 是模型声明分类与持久化权威分类的差异（R7）。
// 它由 application 层在 citations+resolved records 已知处收集，绝不作为
// material contradiction 进入 gate 判定：metadata 差异不独立影响 confidence
// cap 或 planning eligibility，只产生可纠正的 evidence_correction challenge。
type CitationClassificationMismatch struct {
	EvidenceID           string
	StoredClassification EvidenceClassification
}

// EvidenceGateInput is the service-owned input to EvaluateEvidenceGate.
type EvidenceGateInput struct {
	Fixability        FixabilityClass
	ModelConfidence   float64
	AlertQuality      AlertQuality
	Citations         []EvidenceCitation
	Resolution        EvidenceResolution
	CausalClosure     *CausalClosure
	TestSuspected     bool
	TestPolicyMatched bool
}

// EvidenceGateDecision is persisted/reviewed separately from model confidence.
type EvidenceGateDecision struct {
	ConfidenceCap       float64
	EffectiveConfidence float64
	PlanningEligible    bool
	Outcome             FixabilityClass
	Reasons             []string
	MissingEvidence     []string
	Contradictions      []string
	DirectEvidenceIDs   []string
}

const (
	ConfidenceCapMissingDirect = 0.39
	ConfidenceCapUnresolved    = 0.69
)

// EvaluateEvidenceGate applies the structural planning threshold independent
// of the model's self-reported confidence.
func EvaluateEvidenceGate(input EvidenceGateInput) EvidenceGateDecision {
	decision := EvidenceGateDecision{
		ConfidenceCap:       1,
		EffectiveConfidence: clampConfidence(input.ModelConfidence),
		Outcome:             input.Fixability,
		Reasons:             []string{},
		MissingEvidence:     []string{},
		Contradictions:      []string{},
		DirectEvidenceIDs:   []string{},
	}

	records := make(map[string]EvidenceRecord, len(input.Resolution.Records))
	for _, record := range input.Resolution.Records {
		if strings.TrimSpace(record.EvidenceID) == "" {
			continue
		}
		records[record.EvidenceID] = record
	}

	hasDirect := false
	for _, citation := range input.Citations {
		id := strings.TrimSpace(citation.EvidenceID)
		if id == "" {
			continue
		}
		record, ok := records[id]
		if !ok || !record.Available {
			decision.MissingEvidence = appendUnique(decision.MissingEvidence, id)
			continue
		}
		// R7：citation classification 与存储分类不一致是可纠正的 metadata，
		// 不是 material contradiction；mismatch 收集在 application 层完成，
		// 这里只使用存储分类做 direct-evidence 判定。
		if record.Classification == EvidenceDirectFault {
			hasDirect = true
			decision.DirectEvidenceIDs = appendUnique(decision.DirectEvidenceIDs, id)
		}
	}

	if !hasDirect {
		decision.ConfidenceCap = ConfidenceCapMissingDirect
		decision.Reasons = append(decision.Reasons, "missing direct fault evidence")
		decision.MissingEvidence = appendUnique(decision.MissingEvidence, "direct_fault")
	}

	// R8/D4：time、host、operational correlation 与 primary source coverage
	// 不是所有 code fix 的全局前置矩阵。它们仍保留在 resolution/audit 中，
	// 但只有模型声明并由服务解析出的 material contradiction 才会阻断；
	// causal closure false 则表示缺口对当前因果链仍然 material。
	if len(input.Resolution.MaterialContradictions) > 0 {
		decision.Contradictions = appendUnique(decision.Contradictions, input.Resolution.MaterialContradictions...)
		decision.ConfidenceCap = minConfidence(decision.ConfidenceCap, ConfidenceCapUnresolved)
		decision.Reasons = append(decision.Reasons, "material contradiction remains unresolved")
	}
	if input.Resolution.Time != nil && input.Resolution.Time.Contradictory {
		decision.Contradictions = appendUnique(decision.Contradictions, "contradictory time evidence")
		decision.ConfidenceCap = minConfidence(decision.ConfidenceCap, ConfidenceCapUnresolved)
		decision.Reasons = appendUnique(decision.Reasons, "material contradiction remains unresolved")
	}
	if input.CausalClosure == nil || !input.CausalClosure.ExplainsOriginalSymptom {
		decision.Reasons = append(decision.Reasons, "causal closure does not explain the original symptom")
	}
	decision.EffectiveConfidence = minConfidence(decision.EffectiveConfidence, decision.ConfidenceCap)
	decision.PlanningEligible = input.Fixability == FixabilityCodeFixable &&
		hasDirect && decision.EffectiveConfidence >= 0.70 &&
		len(decision.Contradictions) == 0 && input.CausalClosure != nil && input.CausalClosure.ExplainsOriginalSymptom
	if input.Fixability == FixabilityCodeFixable && !decision.PlanningEligible {
		// Outcome 描述 gate verdict，不改写 agent diagnosis；application 必须将
		// failed fact check 作为 structured challenge 回到同一 resilient loop。
		decision.Outcome = FixabilityInsufficientEvidence
	}
	return decision
}

func clampConfidence(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func minConfidence(left, right float64) float64 {
	if left < right {
		return left
	}
	return right
}

func appendUnique(values []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(additions))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	for _, value := range additions {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return values
}

// ValidateEvidenceClassification validates a model-provided classification.
// Empty is accepted for the v1 string reference compatibility path.
func ValidateEvidenceClassification(value EvidenceClassification) error {
	if value == "" {
		return nil
	}
	switch value {
	case EvidenceDirectFault, EvidenceCorrelatedSupport, EvidenceContextual, EvidenceUnrelated, EvidenceContradictory:
		return nil
	default:
		return fmt.Errorf("unknown evidence classification %q", value)
	}
}
