package application

import (
	"context"

	"fixthe/backend/internal/modules/remediation/domain"
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

// Evaluate resolves citations first and then computes the service-owned cap.
// Without a resolver, no persisted direct evidence is assumed to exist.
func (g *EvidenceGate) Evaluate(ctx context.Context, runID string, diagnosis *DiagnosisOutput) (domain.EvidenceGateDecision, error) {
	resolution := domain.EvidenceResolution{
		Sources:                append([]domain.SourceCoverage(nil), diagnosis.SourceCoverage...),
		Time:                   diagnosis.TimeAssessment,
		Correlation:            diagnosis.Correlation,
		MaterialContradictions: append([]string(nil), diagnosis.MaterialContradictions...),
	}
	if g != nil && g.resolver != nil {
		resolved, err := g.resolver.ResolveEvidence(ctx, runID, diagnosis.EvidenceCitations)
		if err != nil {
			return domain.EvidenceGateDecision{}, err
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
	}), nil
}

func mergeEvidenceResolution(fallback, resolved domain.EvidenceResolution) domain.EvidenceResolution {
	if resolved.Time == nil {
		resolved.Time = fallback.Time
	}
	if resolved.Correlation == nil {
		resolved.Correlation = fallback.Correlation
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

// Apply transforms an unsupported code-fixable claim into a non-actionable
// insufficient-evidence diagnosis while retaining the gate decision.
func (g *EvidenceGate) Apply(ctx context.Context, runID string, diagnosis *DiagnosisOutput) (*DiagnosisOutput, domain.EvidenceGateDecision, error) {
	decision, err := g.Evaluate(ctx, runID, diagnosis)
	if err != nil {
		return nil, domain.EvidenceGateDecision{}, err
	}
	copy := *diagnosis
	copy.EvidenceAssessment = &decision
	if diagnosis.Fixability == domain.FixabilityCodeFixable && !decision.PlanningEligible {
		copy.Fixability = domain.FixabilityInsufficientEvidence
		copy.Confidence = decision.EffectiveConfidence
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
			return nil, domain.EvidenceGateDecision{}, err
		}
	}
	return &copy, decision, nil
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
