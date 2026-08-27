package application

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"fixthe/backend/internal/modules/remediation/domain"
)

// EnvelopeVersion 是 agent 协议信封的 schema 版本。
const EnvelopeVersion = "v1"

// ErrInvalidEnvelope 表示模型返回了可纠正的协议信封错误。
// coordinator 把它回喂给下一轮，而不是直接把 run 打成 failed；
// 提供商/基础设施错误不得包装成该哨兵。
var ErrInvalidEnvelope = errors.New("invalid agent envelope")

// errInvalidDiagnosisTimeAssessment 标记可安全映射的 timeAssessment 合同错误；
// 具体模型值只留在 operator 诊断中，不得进入 protocol correction。
var errInvalidDiagnosisTimeAssessment = errors.New("invalid diagnosis time assessment")

// DecodeAgentEnvelope 是 DecodeEnvelope 的别名，用于清晰性。
func DecodeAgentEnvelope(raw []byte) (*AgentEnvelope, error) {
	return DecodeEnvelope(string(raw))
}

// AgentEnvelopeSchema 返回 agent 信封的 JSON schema。
func AgentEnvelopeSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"schemaVersion": map[string]interface{}{"type": "string", "const": EnvelopeVersion},
			"kind": map[string]interface{}{
				"type": "string",
				"enum": []string{"requestTool", "diagnosis", "planCandidates", "stop"},
			},
		},
		"required": []string{"schemaVersion", "kind"},
		"oneOf": []map[string]interface{}{
			{"required": []string{"requestTool"}},
			{"required": []string{"diagnosis"}},
			{"required": []string{"planCandidates"}},
			{"required": []string{"stop"}},
		},
	}
}

// Aliases for easier access
type ToolRequestEnvelope = RequestTool
type DiagnosisEnvelope = DiagnosisOutput
type PlanCandidatesEnvelope = PlanCandidatesOutput
type StopEnvelope = StopOutput

// AgentEnvelope 是 agent 返回的顶层信封，严格验证。
type AgentEnvelope struct {
	SchemaVersion  string                `json:"schemaVersion"`
	Kind           string                `json:"kind"`
	RequestTool    *RequestTool          `json:"requestTool,omitempty"`
	Diagnosis      *DiagnosisOutput      `json:"diagnosis,omitempty"`
	PlanCandidates *PlanCandidatesOutput `json:"planCandidates,omitempty"`
	Stop           *StopOutput           `json:"stop,omitempty"`
	// Reserved for later slices
	PatchComplete        *PatchCompleteOutput        `json:"patchComplete,omitempty"`
	ValidationAssessment *ValidationAssessmentOutput `json:"validationAssessment,omitempty"`
	nativeToolRequests   []RequestTool
}

// RequestTool 是模型请求工具调用的信封。
type RequestTool struct {
	ToolName   string                 `json:"toolName"`
	Parameters map[string]interface{} `json:"parameters"`
	CallID     string                 `json:"-"`
}

// RequestedTools 返回兼容 envelope 或 provider-native tool calls 归一化后的请求。
func (e *AgentEnvelope) RequestedTools() []RequestTool {
	if e == nil {
		return nil
	}
	if len(e.nativeToolRequests) > 0 {
		return append([]RequestTool(nil), e.nativeToolRequests...)
	}
	if e.RequestTool == nil {
		return nil
	}
	return []RequestTool{*e.RequestTool}
}

// DiagnosisOutput 是结构化诊断结果。
type DiagnosisOutput struct {
	Fixability             domain.FixabilityClass           `json:"fixability"`
	Confidence             float64                          `json:"confidence"`
	CausalReasoning        string                           `json:"causalReasoning"`
	Contradictions         []string                         `json:"contradictions"`
	MissingEvidence        []string                         `json:"missingEvidence"`
	EvidenceCitations      []domain.EvidenceCitation        `json:"evidenceCitations"`
	RecommendedNextAction  string                           `json:"recommendedNextAction"`
	CollectMoreContext     *CollectMoreContextRequest       `json:"collectMoreContext,omitempty"`
	AlertQuality           domain.AlertQuality              `json:"alertQuality,omitempty"`
	SourceCoverage         []domain.SourceCoverage          `json:"sourceCoverage,omitempty"`
	TimeAssessment         *domain.TimeAssessment           `json:"timeAssessment,omitempty"`
	Correlation            *domain.CorrelationAssessment    `json:"correlation,omitempty"`
	CausalClosure          *domain.CausalClosure            `json:"causalClosure,omitempty"`
	MaterialContradictions []string                         `json:"materialContradictions,omitempty"`
	TestSuspected          bool                             `json:"testSuspected,omitempty"`
	TestPolicyMatched      bool                             `json:"testPolicyMatched,omitempty"`
	Hypotheses             []domain.NonActionableHypothesis `json:"hypotheses,omitempty"`
	EvidenceAssessment     *domain.EvidenceGateDecision     `json:"-"`
}

// CollectMoreContextRequest 是请求更多上下文的子结构。
type CollectMoreContextRequest struct {
	Reason    string        `json:"reason"`
	ToolCalls []RequestTool `json:"toolCalls"`
}

// PlanCandidatesOutput 是修复计划候选列表。
type PlanCandidatesOutput struct {
	Candidates    []PlanCandidate `json:"candidates"`
	RecommendedID string          `json:"recommendedId"`
	Rationale     string          `json:"rationale"`
	SuggestedDiff string          `json:"suggestedDiff"`
}

// PlanCandidate 是一个修复计划候选。
type PlanCandidate struct {
	PlanID           string   `json:"planId"`
	EvidenceRefs     []string `json:"evidenceRefs"`
	AffectedFiles    []string `json:"affectedFiles"`
	IntendedBehavior string   `json:"intendedBehavior"`
	Risk             string   `json:"risk"`
	RollbackStrategy string   `json:"rollbackStrategy"`
}

// StopOutput 表示模型主动停止。
type StopOutput struct {
	Reason string `json:"reason"`
}

// PatchCompleteOutput 是补丁完成的信封（本 slice 未使用）。
type PatchCompleteOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// ValidationAssessmentOutput 是验证评估的信封（本 slice 未使用）。
type ValidationAssessmentOutput struct {
	Passed  bool   `json:"passed"`
	Summary string `json:"summary"`
}

// DecodeEnvelope 解析并严格验证 agent 信封。
func DecodeEnvelope(raw string) (*AgentEnvelope, error) {
	var env AgentEnvelope
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&env); err != nil {
		return nil, fmt.Errorf("envelope decode: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("envelope decode: multiple JSON values")
	}

	if env.SchemaVersion != EnvelopeVersion {
		return nil, fmt.Errorf("envelope schema version %q not supported", env.SchemaVersion)
	}

	// 严格校验：恰好一个 kind 字段非空
	kinds := []string{}
	if env.RequestTool != nil {
		kinds = append(kinds, "requestTool")
	}
	if env.Diagnosis != nil {
		kinds = append(kinds, "diagnosis")
	}
	if env.PlanCandidates != nil {
		kinds = append(kinds, "planCandidates")
	}
	if env.Stop != nil {
		kinds = append(kinds, "stop")
	}
	if env.PatchComplete != nil {
		kinds = append(kinds, "patchComplete")
	}
	if env.ValidationAssessment != nil {
		kinds = append(kinds, "validationAssessment")
	}

	if len(kinds) == 0 {
		return nil, fmt.Errorf("envelope must contain exactly one kind")
	}
	if len(kinds) > 1 {
		return nil, fmt.Errorf("envelope contains multiple kinds: %s", strings.Join(kinds, ", "))
	}

	if env.Kind != kinds[0] {
		return nil, fmt.Errorf("envelope kind field %q does not match payload %q", env.Kind, kinds[0])
	}

	// 验证内容
	switch env.Kind {
	case "requestTool":
		if err := validateRequestTool(env.RequestTool); err != nil {
			return nil, err
		}
	case "diagnosis":
		if err := validateDiagnosis(env.Diagnosis); err != nil {
			return nil, err
		}
	case "planCandidates":
		if err := validatePlanCandidates(env.PlanCandidates); err != nil {
			return nil, err
		}
	case "stop":
		if err := validateStop(env.Stop); err != nil {
			return nil, err
		}
	case "patchComplete", "validationAssessment":
		return nil, fmt.Errorf("envelope kind %q is reserved for later slices", env.Kind)
	default:
		return nil, fmt.Errorf("unknown envelope kind %q", env.Kind)
	}

	return &env, nil
}

func validateRequestTool(rt *RequestTool) error {
	if rt.ToolName == "" {
		return fmt.Errorf("requestTool: toolName is required")
	}
	if rt.Parameters == nil {
		return fmt.Errorf("requestTool: parameters must be present")
	}
	return nil
}

func validateDiagnosis(d *DiagnosisOutput) error {
	validFixability := map[domain.FixabilityClass]bool{
		domain.FixabilityCodeFixable:          true,
		domain.FixabilityExternalDependency:   true,
		domain.FixabilityConfiguration:        true,
		domain.FixabilityData:                 true,
		domain.FixabilityInfrastructure:       true,
		domain.FixabilityInsufficientEvidence: true,
		domain.FixabilityUnsafeToAutomate:     true,
	}
	if !validFixability[d.Fixability] {
		return fmt.Errorf("diagnosis: unknown fixability %q", d.Fixability)
	}
	if d.Confidence < 0 || d.Confidence > 1 {
		return fmt.Errorf("diagnosis: confidence must be between 0 and 1")
	}
	if d.CausalReasoning == "" {
		return fmt.Errorf("diagnosis: causalReasoning is required")
	}
	if len(d.EvidenceCitations) > 64 || len(d.Contradictions) > 32 || len(d.MissingEvidence) > 32 || len(d.MaterialContradictions) > 32 {
		return fmt.Errorf("diagnosis: evidence fields exceed bounds")
	}
	for _, citation := range d.EvidenceCitations {
		if citation.EvidenceID == "" || len(citation.EvidenceID) > 255 {
			return fmt.Errorf("diagnosis: evidence citation id is invalid")
		}
		if err := domain.ValidateEvidenceClassification(citation.Classification); err != nil {
			return fmt.Errorf("diagnosis: %w", err)
		}
	}
	if len(d.SourceCoverage) > 32 {
		return fmt.Errorf("diagnosis: source coverage exceeds bounds")
	}
	for _, source := range d.SourceCoverage {
		if len(source.SourceID) > 255 || len(source.Kind) > 80 || len(source.Reason) > 1000 {
			return fmt.Errorf("diagnosis: source coverage is invalid")
		}
		switch source.Status {
		case "", domain.SourceConfigured, domain.SourceInspectedSuccess, domain.SourceInspectedEmpty,
			domain.SourceUnavailable, domain.SourceNotApplicable, domain.SourceNotInspected:
		default:
			return fmt.Errorf("diagnosis: unknown source coverage status %q", source.Status)
		}
	}
	if d.TimeAssessment != nil {
		if len(d.TimeAssessment.OriginalValues) > 32 || len(d.TimeAssessment.Basis) > 40 || len(d.TimeAssessment.Certainty) > 40 {
			return fmt.Errorf("diagnosis: %w", errInvalidDiagnosisTimeAssessment)
		}
		switch d.TimeAssessment.Basis {
		case "", "paired_epoch", "explicit_offset", "contextual_zone", "unresolved":
		default:
			return fmt.Errorf("diagnosis: %w: unknown basis %q", errInvalidDiagnosisTimeAssessment, d.TimeAssessment.Basis)
		}
	}
	if len(d.Hypotheses) > 16 {
		return fmt.Errorf("diagnosis: hypotheses exceed bounds")
	}
	for _, hypothesis := range d.Hypotheses {
		if hypothesis.ID == "" || hypothesis.Summary == "" || !hypothesis.NonActionable {
			return fmt.Errorf("diagnosis: hypotheses must be bounded and non-actionable")
		}
		if len(hypothesis.EvidenceRefs) > 16 {
			return fmt.Errorf("diagnosis: hypothesis evidence refs exceed bounds")
		}
	}
	return nil
}

func validatePlanCandidates(pc *PlanCandidatesOutput) error {
	if len(pc.Candidates) == 0 {
		return fmt.Errorf("planCandidates: at least one candidate required")
	}
	if pc.RecommendedID == "" {
		return fmt.Errorf("planCandidates: recommendedId is required")
	}
	if pc.SuggestedDiff == "" {
		return fmt.Errorf("planCandidates: suggestedDiff is required")
	}
	if strings.TrimSpace(pc.Rationale) == "" {
		return fmt.Errorf("planCandidates: rationale is required")
	}

	found := false
	for _, c := range pc.Candidates {
		if c.PlanID == "" {
			return fmt.Errorf("planCandidates: candidate planId is required")
		}
		if c.PlanID == pc.RecommendedID {
			found = true
		}
		validRisk := map[string]bool{
			string(domain.RiskOrdinary):           true,
			string(domain.RiskHighRisk):           true,
			string(domain.RiskDeniedControlPlane): true,
		}
		if !validRisk[c.Risk] {
			return fmt.Errorf("planCandidates: unknown risk %q", c.Risk)
		}
		if len(c.EvidenceRefs) == 0 {
			return fmt.Errorf("planCandidates: candidate evidenceRefs is required")
		}
		if len(c.AffectedFiles) == 0 {
			return fmt.Errorf("planCandidates: candidate affectedFiles is required")
		}
		if strings.TrimSpace(c.IntendedBehavior) == "" {
			return fmt.Errorf("planCandidates: candidate intendedBehavior is required")
		}
		if strings.TrimSpace(c.RollbackStrategy) == "" {
			return fmt.Errorf("planCandidates: candidate rollbackStrategy is required")
		}
	}
	if !found {
		return fmt.Errorf("planCandidates: recommendedId %q not found in candidates", pc.RecommendedID)
	}
	return nil
}

func validateStop(s *StopOutput) error {
	if s.Reason == "" {
		return fmt.Errorf("stop: reason is required")
	}
	return nil
}
