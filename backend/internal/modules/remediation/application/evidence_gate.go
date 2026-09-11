package application

import (
	"context"
	"strings"

	"mendry/backend/internal/modules/remediation/domain"
)

// EvidenceResolver resolves model references against project/run-owned
// persisted evidence. It never accepts raw URLs, credentials, or model text as
// authority.
type EvidenceResolver interface {
	ResolveEvidence(context.Context, string, []domain.EvidenceCitation) (domain.EvidenceResolution, error)
}

type evidenceAssessmentWriter interface {
	PersistEvidenceAssessment(context.Context, domain.EvidenceAssessment) error
}

// EvidenceGate is the application-owned adapter around the pure domain gate.
// It splits the gate work into two outputs (D4): the service-owned
// EvidenceGateDecision (caps/eligibility over genuinely hard reasons) and the
// set of correctable citation-classification mismatches, which are metadata
// corrections fed back to the same agent loop (R7) instead of becoming
// contradictions or silently rewriting the diagnosis conclusion (R6).
type EvidenceGate struct {
	resolver EvidenceResolver
	writer   evidenceAssessmentWriter
}

func NewEvidenceGate(resolver EvidenceResolver) *EvidenceGate {
	gate := &EvidenceGate{resolver: resolver}
	if writer, ok := resolver.(evidenceAssessmentWriter); ok {
		gate.writer = writer
	}
	return gate
}

// CitationClassificationMismatch 是一次可纠正的 citation classification 元数据
// 差异：模型声明的分类与持久化记录的权威分类不一致（R7）。它绝不构成 material
// contradiction，也不独立影响 confidence cap 或 planning eligibility；gate 返回
// 它只是为了向同一循环回喂 evidence_correction challenge。
type CitationClassificationMismatch struct {
	EvidenceID           string
	StoredClassification domain.EvidenceClassification
}

// Evaluate resolves citations first and then computes the service-owned cap.
// It returns the correctable citation-classification mismatches alongside the
// decision; without a resolver, no persisted direct evidence is assumed to
// exist and no mismatch can be established.
func (g *EvidenceGate) Evaluate(ctx context.Context, runID string, diagnosis *DiagnosisOutput) (domain.EvidenceGateDecision, []CitationClassificationMismatch, error) {
	resolution := domain.EvidenceResolution{
		Sources:                append([]domain.SourceCoverage(nil), diagnosis.SourceCoverage...),
		Time:                   diagnosis.TimeAssessment,
		Correlation:            diagnosis.Correlation,
		MaterialContradictions: append([]string(nil), diagnosis.MaterialContradictions...),
	}
	if g != nil && g.resolver != nil {
		resolved, err := g.resolver.ResolveEvidence(ctx, runID, diagnosis.EvidenceCitations)
		if err != nil {
			return domain.EvidenceGateDecision{}, nil, err
		}
		resolution = mergeEvidenceResolution(resolution, resolved)
	}
	return domain.EvaluateEvidenceGate(domain.EvidenceGateInput{
		Fixability:        diagnosis.Fixability,
		ModelConfidence:   diagnosis.Confidence,
		AlertQuality:      diagnosis.AlertQuality,
		Citations:         diagnosis.EvidenceCitations,
		Resolution:        resolution,
		CausalClosure:     diagnosis.CausalClosure,
		TestSuspected:     diagnosis.TestSuspected,
		TestPolicyMatched: diagnosis.TestPolicyMatched,
	}), collectClassificationMismatches(diagnosis.EvidenceCitations, resolution.Records), nil
}

// collectClassificationMismatches 在 citations 与 resolved records 都已知的
// application 层收集可纠正的 classification 差异（R7）。只有持久化记录可用、
// 模型声明了分类且与存储分类不一致时才产生条目；按 evidence ID 去重，绝不
// 把差异升级为 contradiction。
func collectClassificationMismatches(citations []domain.EvidenceCitation, records []domain.EvidenceRecord) []CitationClassificationMismatch {
	stored := make(map[string]domain.EvidenceClassification, len(records))
	for _, record := range records {
		if strings.TrimSpace(record.EvidenceID) == "" {
			continue
		}
		stored[record.EvidenceID] = record.Classification
	}
	var mismatches []CitationClassificationMismatch
	seen := make(map[string]struct{}, len(citations))
	for _, citation := range citations {
		id := strings.TrimSpace(citation.EvidenceID)
		if id == "" {
			continue
		}
		classification, ok := stored[id]
		if !ok || citation.Classification == "" || citation.Classification == classification {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		mismatches = append(mismatches, CitationClassificationMismatch{
			EvidenceID:           id,
			StoredClassification: classification,
		})
	}
	return mismatches
}

func mergeEvidenceResolution(fallback, resolved domain.EvidenceResolution) domain.EvidenceResolution {
	if resolved.Time == nil {
		resolved.Time = fallback.Time
	}
	if resolved.Correlation == nil {
		resolved.Correlation = fallback.Correlation
	} else if fallback.Correlation != nil && fallback.Correlation.HostIdentity {
		// 持久化 correlation 不表示 hostIdentity（数据库布尔投影恒为 false）；保留
		// 模型基于持久化 SSH inspect 证据做出的主机身份归并，避免空布尔擦除该语义。
		resolved.Correlation.HostIdentity = true
	}
	if len(resolved.Sources) == 0 {
		resolved.Sources = fallback.Sources
	}
	resolved.MaterialContradictions = appendUniqueStrings(
		resolved.MaterialContradictions,
		fallback.MaterialContradictions...,
	)
	return resolved
}

// Apply attaches the service-owned assessment and persists it, but never
// mutates the diagnosis conclusion: Fixability and Confidence are preserved
// even when the gate is not planning-eligible (D4/R6 — the INC-2270 silent
// rewrite is removed). Gate metadata (missing evidence, contradictions, a
// bounded fallback recommendation) is still merged for review, and the
// correctable classification mismatches are returned for challenge feedback.
func (g *EvidenceGate) Apply(ctx context.Context, runID string, diagnosis *DiagnosisOutput) (*DiagnosisOutput, domain.EvidenceGateDecision, []CitationClassificationMismatch, error) {
	decision, mismatches, err := g.Evaluate(ctx, runID, diagnosis)
	if err != nil {
		return nil, domain.EvidenceGateDecision{}, nil, err
	}
	copy := *diagnosis
	copy.EvidenceAssessment = &decision
	if diagnosis.Fixability == domain.FixabilityCodeFixable && !decision.PlanningEligible {
		copy.MissingEvidence = appendUniqueStrings(copy.MissingEvidence, decision.MissingEvidence...)
		copy.Contradictions = appendUniqueStrings(copy.Contradictions, decision.Contradictions...)
		if copy.RecommendedNextAction == "" {
			copy.RecommendedNextAction = "collect bounded evidence and keep the hypothesis non-actionable"
		}
	}
	if g != nil && g.writer != nil {
		if err := g.writer.PersistEvidenceAssessment(ctx, domain.EvidenceAssessment{
			RunID: runID, ConfidenceCap: decision.ConfidenceCap, EffectiveConfidence: decision.EffectiveConfidence,
			PlanningEligible: decision.PlanningEligible, Outcome: decision.Outcome, Reasons: decision.Reasons,
			MissingEvidence: decision.MissingEvidence, Contradictions: decision.Contradictions,
			DirectEvidenceIDs: decision.DirectEvidenceIDs,
		}); err != nil {
			return nil, domain.EvidenceGateDecision{}, nil, err
		}
	}
	return &copy, decision, mismatches, nil
}

func appendUniqueStrings(values []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(additions))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	for _, value := range additions {
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
