package application

import (
	"context"
	"testing"

	"fixthe/backend/internal/modules/remediation/domain"
)

func TestEvidenceGateApplyDoesNotTrustModelCitationWithoutResolver(t *testing.T) {
	diagnosis := &DiagnosisOutput{
		Fixability:        domain.FixabilityCodeFixable,
		Confidence:        0.99,
		CausalReasoning:   "the model says the code is faulty",
		EvidenceCitations: []domain.EvidenceCitation{{EvidenceID: "ev-1", Classification: domain.EvidenceDirectFault}},
		CausalClosure:     &domain.CausalClosure{ExplainsOriginalSymptom: true},
	}
	gated, decision, err := NewEvidenceGate(nil).Apply(context.Background(), "run-1", diagnosis)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if gated.Fixability != domain.FixabilityInsufficientEvidence || decision.PlanningEligible {
		t.Fatalf("gated=%#v decision=%#v, want insufficient evidence", gated, decision)
	}
	if decision.ConfidenceCap != 0.39 || decision.EffectiveConfidence != 0.39 {
		t.Fatalf("decision=%#v, want 0.39 cap", decision)
	}
}

type evidenceResolverWithoutTime struct{}

func (evidenceResolverWithoutTime) ResolveEvidence(_ context.Context, _ string, citations []domain.EvidenceCitation) (domain.EvidenceResolution, error) {
	records := make([]domain.EvidenceRecord, 0, len(citations))
	for _, citation := range citations {
		records = append(records, domain.EvidenceRecord{
			EvidenceID: citation.EvidenceID, Classification: domain.EvidenceDirectFault, Available: true,
		})
	}
	return domain.EvidenceResolution{
		Records:     records,
		Sources:     []domain.SourceCoverage{{SourceID: "source-1", Primary: true, Status: domain.SourceInspectedSuccess}},
		Correlation: &domain.CorrelationAssessment{Temporal: true, Operational: true},
	}, nil
}

func TestEvidenceGatePreservesDiagnosisTimeWhenResolverHasNoPersistedAssessment(t *testing.T) {
	diagnosis := &DiagnosisOutput{
		Fixability:        domain.FixabilityCodeFixable,
		Confidence:        0.9,
		CausalReasoning:   "the direct runtime fault explains the alert",
		EvidenceCitations: []domain.EvidenceCitation{{EvidenceID: "ev-fault", Classification: domain.EvidenceDirectFault}},
		TimeAssessment:    &domain.TimeAssessment{Basis: "paired_epoch", Certainty: "high"},
		CausalClosure:     &domain.CausalClosure{ExplainsOriginalSymptom: true},
	}

	gated, decision, err := NewEvidenceGate(evidenceResolverWithoutTime{}).Apply(context.Background(), "run-1", diagnosis)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if !decision.PlanningEligible || gated.Fixability != domain.FixabilityCodeFixable {
		t.Fatalf("gated=%#v decision=%#v, want planning eligible", gated, decision)
	}
	if decision.EffectiveConfidence != 0.9 {
		t.Fatalf("decision=%#v, want preserved high-confidence time assessment", decision)
	}
}
