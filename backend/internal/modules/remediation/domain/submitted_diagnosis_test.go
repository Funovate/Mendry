package domain_test

import (
	"strings"
	"testing"

	"fixthe/backend/internal/modules/remediation/domain"
)

// validSubmittedDiagnosis 返回一个满足全部边界的 submitted diagnosis。
func validSubmittedDiagnosis() domain.SubmittedDiagnosis {
	return domain.SubmittedDiagnosis{
		RunID:                 "run-1",
		Fixability:            domain.FixabilityCodeFixable,
		Confidence:            0.9,
		CausalReasoning:       "deployed source panics on nil trigger",
		Contradictions:        []string{"none"},
		MissingEvidence:       []string{},
		EvidenceCitations:     []string{"ev-1"},
		RecommendedNextAction: "apply the suggested patch",
		GateOutcome:           "planning_eligible",
		DecisionID:            "019ff544-405c-7d11-9f10-cb3fc579605c",
	}
}

func TestSubmittedDiagnosisValidateAcceptsBoundedValue(t *testing.T) {
	if err := validSubmittedDiagnosis().Validate(); err != nil {
		t.Fatalf("valid submitted diagnosis rejected: %v", err)
	}
}

func TestSubmittedDiagnosisValidateRejectsBounds(t *testing.T) {
	tooLongReasoning := strings.Repeat("r", 131073)
	cases := []struct {
		name   string
		mutate func(*domain.SubmittedDiagnosis)
		want   string
	}{
		{"unknown fixability", func(d *domain.SubmittedDiagnosis) { d.Fixability = "tcpdump" }, "fixability"},
		{"confidence above one", func(d *domain.SubmittedDiagnosis) { d.Confidence = 1.1 }, "confidence"},
		{"confidence below zero", func(d *domain.SubmittedDiagnosis) { d.Confidence = -0.1 }, "confidence"},
		{"reasoning too long", func(d *domain.SubmittedDiagnosis) { d.CausalReasoning = tooLongReasoning }, "reasoning"},
		{"contradictions too many", func(d *domain.SubmittedDiagnosis) {
			d.Contradictions = make([]string, 33)
		}, "contradictions"},
		{"missing evidence too many", func(d *domain.SubmittedDiagnosis) {
			d.MissingEvidence = make([]string, 33)
		}, "missing evidence"},
		{"citations too many", func(d *domain.SubmittedDiagnosis) {
			d.EvidenceCitations = make([]string, 65)
		}, "evidence citations"},
		{"next action too long", func(d *domain.SubmittedDiagnosis) {
			d.RecommendedNextAction = strings.Repeat("n", 2001)
		}, "next action"},
		{"unknown correction kind", func(d *domain.SubmittedDiagnosis) {
			d.Correction.Kind = "rewrite"
		}, "correction kind"},
		{"correction evidence too many", func(d *domain.SubmittedDiagnosis) {
			d.Correction.CorrectedEvidence = make([]domain.SubmittedEvidenceClassification, 33)
		}, "correction evidence"},
		{"correction evidence empty id", func(d *domain.SubmittedDiagnosis) {
			d.Correction.CorrectedEvidence = []domain.SubmittedEvidenceClassification{{EvidenceID: ""}}
		}, "correction evidence id"},
		{"correction evidence unknown classification", func(d *domain.SubmittedDiagnosis) {
			d.Correction.CorrectedEvidence = []domain.SubmittedEvidenceClassification{{
				EvidenceID: "ev-1", StoredClassification: "forged",
			}}
		}, "classification"},
		{"correction count negative", func(d *domain.SubmittedDiagnosis) {
			d.Correction.CorrectionCount = -1
		}, "correction count"},
		{"corrected without kind", func(d *domain.SubmittedDiagnosis) {
			d.Correction.Corrected = true
		}, "without correction kind"},
		{"unknown gate outcome", func(d *domain.SubmittedDiagnosis) { d.GateOutcome = "maybe" }, "gate outcome"},
		{"decision id too long", func(d *domain.SubmittedDiagnosis) {
			d.DecisionID = strings.Repeat("x", 65)
		}, "decision id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value := validSubmittedDiagnosis()
			tc.mutate(&value)
			err := value.Validate()
			if err == nil {
				t.Fatal("Validate() = nil, want error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() error = %q, want substring %q", err, tc.want)
			}
		})
	}
}

func TestSubmittedDiagnosisValidateCorrectionConsistency(t *testing.T) {
	value := validSubmittedDiagnosis()
	value.Correction = domain.SubmittedDiagnosisCorrection{
		Kind:              domain.SubmittedCorrectionEvidence,
		CorrectedEvidence: []domain.SubmittedEvidenceClassification{{EvidenceID: "ev-1", StoredClassification: domain.EvidenceDirectFault}},
		CorrectionCount:   1,
		Corrected:         true,
	}
	if err := value.Validate(); err != nil {
		t.Fatalf("evidence_correction metadata rejected: %v", err)
	}
	value.Correction.Kind = domain.SubmittedCorrectionExhaustion
	value.Correction.CorrectedEvidence = nil
	value.Correction.CorrectionCount = 1
	if err := value.Validate(); err != nil {
		t.Fatalf("exhaustion metadata rejected: %v", err)
	}

	invalid := []domain.SubmittedDiagnosisCorrection{
		{Kind: domain.SubmittedCorrectionEvidence, Corrected: true, CorrectionCount: 1},
		{Kind: domain.SubmittedCorrectionEvidence, Corrected: true, CorrectionCount: 2,
			CorrectedEvidence: []domain.SubmittedEvidenceClassification{{EvidenceID: "ev-1", StoredClassification: domain.EvidenceDirectFault}}},
		{Kind: domain.SubmittedCorrectionExhaustion, Corrected: true, CorrectionCount: 1,
			CorrectedEvidence: []domain.SubmittedEvidenceClassification{{EvidenceID: "ev-1", StoredClassification: domain.EvidenceDirectFault}}},
	}
	for _, correction := range invalid {
		value := validSubmittedDiagnosis()
		value.Correction = correction
		if err := value.Validate(); err == nil {
			t.Fatalf("inconsistent correction accepted: %#v", correction)
		}
	}
}

func TestSubmittedDiagnosisCorrectionKindIsKnown(t *testing.T) {
	for _, kind := range []domain.SubmittedDiagnosisCorrectionKind{
		domain.SubmittedCorrectionNone,
		domain.SubmittedCorrectionEvidence,
		domain.SubmittedCorrectionExhaustion,
	} {
		if !kind.IsKnown() {
			t.Fatalf("kind %q must be known", kind)
		}
	}
	if domain.SubmittedDiagnosisCorrectionKind("unknown").IsKnown() {
		t.Fatal("unknown correction kind reported known")
	}
}
