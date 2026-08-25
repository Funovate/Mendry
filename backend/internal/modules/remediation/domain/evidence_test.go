package domain

import "testing"

func TestEvaluateEvidenceGateCapsMissingDirectEvidence(t *testing.T) {
	decision := EvaluateEvidenceGate(EvidenceGateInput{
		Fixability:      FixabilityCodeFixable,
		ModelConfidence: 0.99,
		Citations:       []EvidenceCitation{{EvidenceID: "ev-context"}},
		Resolution: EvidenceResolution{
			Records: []EvidenceRecord{{EvidenceID: "ev-context", Classification: EvidenceContextual, Available: true}},
		},
		CausalClosure: &CausalClosure{ExplainsOriginalSymptom: true},
	})
	if decision.ConfidenceCap != ConfidenceCapMissingDirect || decision.EffectiveConfidence != ConfidenceCapMissingDirect {
		t.Fatalf("decision = %#v, want missing-direct cap", decision)
	}
	if decision.PlanningEligible || decision.Outcome != FixabilityInsufficientEvidence {
		t.Fatalf("decision = %#v, want planning blocked", decision)
	}
}

func TestEvaluateEvidenceGateCapsUnresolvedCorrelation(t *testing.T) {
	decision := EvaluateEvidenceGate(EvidenceGateInput{
		Fixability:      FixabilityCodeFixable,
		ModelConfidence: 0.99,
		Citations:       []EvidenceCitation{{EvidenceID: "ev-fault", Classification: EvidenceDirectFault}},
		Resolution: EvidenceResolution{
			Records:     []EvidenceRecord{{EvidenceID: "ev-fault", Classification: EvidenceDirectFault, Available: true}},
			Sources:     []SourceCoverage{{Primary: true, Status: SourceInspectedSuccess}},
			Time:        &TimeAssessment{Basis: "unresolved", Certainty: "unresolved"},
			Correlation: &CorrelationAssessment{Temporal: false, Operational: true},
		},
		CausalClosure: &CausalClosure{ExplainsOriginalSymptom: true},
	})
	if decision.ConfidenceCap != ConfidenceCapUnresolved || decision.EffectiveConfidence != ConfidenceCapUnresolved {
		t.Fatalf("decision = %#v, want unresolved cap", decision)
	}
	if decision.PlanningEligible {
		t.Fatal("unresolved correlation must not be planning eligible")
	}
}

func TestEvaluateEvidenceGateAllowsCorrelatedDirectFault(t *testing.T) {
	decision := EvaluateEvidenceGate(EvidenceGateInput{
		Fixability:      FixabilityCodeFixable,
		ModelConfidence: 0.86,
		Citations:       []EvidenceCitation{{EvidenceID: "ev-fault", Classification: EvidenceDirectFault}},
		Resolution: EvidenceResolution{
			Records: []EvidenceRecord{{
				EvidenceID: "ev-fault", Classification: EvidenceDirectFault, Available: true,
				TemporalCorrelation: true, OperationalCorrelation: true,
			}},
			Sources:     []SourceCoverage{{Primary: true, Status: SourceInspectedSuccess}},
			Time:        &TimeAssessment{Basis: "paired_epoch", Certainty: "high"},
			Correlation: &CorrelationAssessment{Temporal: true, Operational: true, HostIdentity: true},
		},
		CausalClosure: &CausalClosure{ExplainsOriginalSymptom: true, Explanation: "fault record explains alert"},
	})
	if !decision.PlanningEligible || decision.Outcome != FixabilityCodeFixable || decision.EffectiveConfidence != 0.86 {
		t.Fatalf("decision = %#v, want eligible code fix", decision)
	}
}

func TestEvaluateEvidenceGateAllowsDirectBridgeWithoutPrimaryInspection(t *testing.T) {
	decision := EvaluateEvidenceGate(EvidenceGateInput{
		Fixability:      FixabilityCodeFixable,
		ModelConfidence: 0.8,
		Citations:       []EvidenceCitation{{EvidenceID: "ev-fault"}},
		Resolution: EvidenceResolution{
			Records:     []EvidenceRecord{{EvidenceID: "ev-fault", Classification: EvidenceDirectFault, Available: true}},
			Sources:     []SourceCoverage{{Primary: true, Status: SourceUnavailable, DirectBridge: true}},
			Time:        &TimeAssessment{Basis: "paired_epoch", Certainty: "high"},
			Correlation: &CorrelationAssessment{Temporal: true, Operational: true, DirectBridge: true},
		},
		CausalClosure: &CausalClosure{ExplainsOriginalSymptom: true},
	})
	if !decision.PlanningEligible {
		t.Fatalf("decision = %#v, want direct bridge to close source gap", decision)
	}
}
