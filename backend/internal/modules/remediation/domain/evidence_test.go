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

func TestEvaluateEvidenceGateDoesNotGloballyRequireCorrelation(t *testing.T) {
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
	if decision.ConfidenceCap != 1 || decision.EffectiveConfidence != 0.99 {
		t.Fatalf("decision = %#v, unresolved non-material correlation must not cap confidence", decision)
	}
	if !decision.PlanningEligible {
		t.Fatal("direct evidence plus causal closure must remain planning eligible without a universal correlation matrix")
	}
}

func TestEvaluateEvidenceGateDoesNotPromoteNonMaterialTimeContradiction(t *testing.T) {
	decision := EvaluateEvidenceGate(EvidenceGateInput{
		Fixability:      FixabilityCodeFixable,
		ModelConfidence: 0.91,
		Citations:       []EvidenceCitation{{EvidenceID: "ev-fault", Classification: EvidenceDirectFault}},
		Resolution: EvidenceResolution{
			Records: []EvidenceRecord{{
				EvidenceID: "ev-fault", Classification: EvidenceDirectFault, Available: true,
			}},
			Time: &TimeAssessment{
				OriginalValues: []string{"2026-08-24T07:16:29Z", "2026-08-24T07:17:05Z"},
				Basis:          "paired_epoch",
				Certainty:      "high",
				Contradictory:  true,
			},
		},
		CausalClosure: &CausalClosure{ExplainsOriginalSymptom: true},
	})
	if decision.ConfidenceCap != 1 || decision.EffectiveConfidence != 0.91 {
		t.Fatalf("decision = %#v, non-material time mismatch must not cap confidence", decision)
	}
	if !decision.PlanningEligible || len(decision.Contradictions) != 0 {
		t.Fatalf("decision = %#v, raw time mismatch must remain audit-only", decision)
	}
}

func TestEvaluateEvidenceGateClassificationMismatchIsNotContradiction(t *testing.T) {
	// R7/AC5：模型声明的分类与存储权威分类不一致是 correctable metadata，
	// 不产生 material contradiction，也不独立 cap confidence 或阻止 planning。
	decision := EvaluateEvidenceGate(EvidenceGateInput{
		Fixability:      FixabilityCodeFixable,
		ModelConfidence: 0.86,
		Citations:       []EvidenceCitation{{EvidenceID: "ev-fault", Classification: EvidenceCorrelatedSupport}},
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
	if len(decision.Contradictions) != 0 {
		t.Fatalf("classification mismatch became a contradiction: %#v", decision.Contradictions)
	}
	if decision.ConfidenceCap != 1 {
		t.Fatalf("classification mismatch capped confidence: %#v", decision)
	}
	if !decision.PlanningEligible || decision.Outcome != FixabilityCodeFixable || decision.EffectiveConfidence != 0.86 {
		t.Fatalf("decision = %#v, want eligible code fix unaffected by metadata mismatch", decision)
	}
}

func TestEvaluateEvidenceGateMaterialContradictionStillBlocks(t *testing.T) {
	// 真矛盾（解析器声明的 material contradiction）仍然 cap 0.69 并阻止
	// planning：只有 classification metadata 差异被解耦，hard gate 语义不变。
	decision := EvaluateEvidenceGate(EvidenceGateInput{
		Fixability:      FixabilityCodeFixable,
		ModelConfidence: 0.9,
		Citations:       []EvidenceCitation{{EvidenceID: "ev-fault", Classification: EvidenceDirectFault}},
		Resolution: EvidenceResolution{
			Records: []EvidenceRecord{{EvidenceID: "ev-fault", Classification: EvidenceDirectFault, Available: true}},
			Sources: []SourceCoverage{{Primary: true, Status: SourceInspectedSuccess}},
			Time:    &TimeAssessment{Basis: "paired_epoch", Certainty: "high"},
			Correlation: &CorrelationAssessment{
				Temporal: true, Operational: true, HostIdentity: true,
			},
			MaterialContradictions: []string{"runtime window conflicts with the alert event time"},
		},
		CausalClosure: &CausalClosure{ExplainsOriginalSymptom: true},
	})
	if decision.ConfidenceCap != ConfidenceCapUnresolved || decision.EffectiveConfidence != ConfidenceCapUnresolved {
		t.Fatalf("decision = %#v, want unresolved cap for a genuine material contradiction", decision)
	}
	if decision.PlanningEligible || len(decision.Contradictions) == 0 {
		t.Fatalf("genuine material contradiction must block planning: %#v", decision)
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
