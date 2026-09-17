package application

import (
	"context"
	"testing"

	"mendry/backend/internal/modules/remediation/domain"
)

func TestEvidenceGateApplyDoesNotTrustModelCitationWithoutResolver(t *testing.T) {
	diagnosis := &DiagnosisOutput{
		Fixability:        domain.FixabilityCodeFixable,
		Confidence:        0.99,
		CausalReasoning:   "the model says the code is faulty",
		EvidenceCitations: []domain.EvidenceCitation{{EvidenceID: "ev-1", Classification: domain.EvidenceDirectFault}},
		CausalClosure:     &domain.CausalClosure{ExplainsOriginalSymptom: true},
	}
	gated, decision, mismatches, err := NewEvidenceGate(nil).Apply(context.Background(), "run-1", diagnosis)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	// INC-2270 core：gate 不再静默改写结论（D4/R6）——缺少持久化直接证据时
	// 仍然 cap 0.39 且不可 planning，但 code_fixable 结论与置信度原样保留。
	if gated.Fixability != domain.FixabilityCodeFixable || gated.Confidence != 0.99 {
		t.Fatalf("gated=%#v, want preserved code_fixable conclusion", gated)
	}
	if decision.PlanningEligible {
		t.Fatalf("decision=%#v, want planning blocked without persisted direct evidence", decision)
	}
	if decision.ConfidenceCap != 0.39 || decision.EffectiveConfidence != 0.39 {
		t.Fatalf("decision=%#v, want 0.39 cap", decision)
	}
	if len(mismatches) != 0 {
		t.Fatalf("mismatches=%#v, want none without resolved records", mismatches)
	}
	if gated.EvidenceAssessment == nil || gated.EvidenceAssessment.ConfidenceCap != 0.39 {
		t.Fatalf("assessment not attached: %#v", gated.EvidenceAssessment)
	}
}

// recordingAssessmentWriter 记录 PersistEvidenceAssessment 调用，供 gate 测试
// 证明 assessment 仍被持久化而结论不被改写。
type recordingAssessmentWriter struct {
	assessments []domain.EvidenceAssessment
}

func (w *recordingAssessmentWriter) PersistEvidenceAssessment(_ context.Context, assessment domain.EvidenceAssessment) error {
	w.assessments = append(w.assessments, assessment)
	return nil
}

// resolverWithWriter 把既有 resolver 与 assessment writer 组合成单个
// EvidenceResolver（NewEvidenceGate 从 resolver 上探测 writer）。
type resolverWithWriter struct {
	resolver EvidenceResolver
	writer   *recordingAssessmentWriter
}

func (r resolverWithWriter) ResolveEvidence(ctx context.Context, runID string, citations []domain.EvidenceCitation) (domain.EvidenceResolution, error) {
	return r.resolver.ResolveEvidence(ctx, runID, citations)
}

func (r resolverWithWriter) PersistEvidenceAssessment(ctx context.Context, assessment domain.EvidenceAssessment) error {
	return r.writer.PersistEvidenceAssessment(ctx, assessment)
}

// TestEvidenceGateApplyPreservesConclusionOnClassificationMismatch 证明 R7/AC5：
// 模型声明分类与存储权威分类不一致时，Apply 返回可纠正的 mismatch（携带存储
// 分类），既不改写结论/置信度，也不产生 contradiction，同时照常持久化 assessment。
func TestEvidenceGateApplyPreservesConclusionOnClassificationMismatch(t *testing.T) {
	diagnosis := &DiagnosisOutput{
		Fixability:        domain.FixabilityCodeFixable,
		Confidence:        0.9,
		CausalReasoning:   "the direct runtime fault explains the alert",
		EvidenceCitations: []domain.EvidenceCitation{{EvidenceID: "ev-fault", Classification: domain.EvidenceCorrelatedSupport}},
		TimeAssessment:    &domain.TimeAssessment{Basis: "paired_epoch", Certainty: "high"},
		CausalClosure:     &domain.CausalClosure{ExplainsOriginalSymptom: true},
	}
	writer := &recordingAssessmentWriter{}
	gate := NewEvidenceGate(resolverWithWriter{resolver: evidenceResolverWithoutTime{}, writer: writer})
	gated, decision, mismatches, err := gate.Apply(context.Background(), "run-1", diagnosis)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if gated.Fixability != domain.FixabilityCodeFixable || gated.Confidence != 0.9 {
		t.Fatalf("conclusion was silently rewritten: %#v", gated)
	}
	if !decision.PlanningEligible || decision.EffectiveConfidence != 0.9 || len(decision.Contradictions) != 0 {
		t.Fatalf("decision=%#v, want eligible with no contradiction", decision)
	}
	if len(mismatches) != 1 || mismatches[0].EvidenceID != "ev-fault" ||
		mismatches[0].StoredClassification != domain.EvidenceDirectFault {
		t.Fatalf("mismatches=%#v, want stored authoritative classification direct_fault", mismatches)
	}
	if gated.EvidenceAssessment == nil || !gated.EvidenceAssessment.PlanningEligible {
		t.Fatalf("assessment not attached: %#v", gated.EvidenceAssessment)
	}
	if len(writer.assessments) != 1 || writer.assessments[0].ConfidenceCap != 1 {
		t.Fatalf("assessment was not persisted: %#v", writer.assessments)
	}
}

// TestEvidenceGateApplyPreservesHardGateCaps 证明 genuinely hard gate 原因
// （缺直接证据 / 服务解析出的 material contradiction）仍保持既有 cap 与不可
// planning 语义，且没有 mismatch 时不会产生 challenge。
func TestEvidenceGateApplyPreservesHardGateCaps(t *testing.T) {
	// 缺少持久化直接证据：cap 0.39、不可 planning，结论保留。
	noDirect := &DiagnosisOutput{
		Fixability:        domain.FixabilityCodeFixable,
		Confidence:        0.9,
		CausalReasoning:   "no persisted direct evidence",
		EvidenceCitations: []domain.EvidenceCitation{{EvidenceID: "ev-context"}},
		CausalClosure:     &domain.CausalClosure{ExplainsOriginalSymptom: true},
	}
	gated, decision, mismatches, err := NewEvidenceGate(nil).Apply(context.Background(), "run-1", noDirect)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if gated.Fixability != domain.FixabilityCodeFixable || decision.ConfidenceCap != 0.39 || decision.PlanningEligible {
		t.Fatalf("missing-direct handling changed: gated=%#v decision=%#v", gated, decision)
	}
	if len(mismatches) != 0 {
		t.Fatalf("mismatches=%#v, want none for missing direct evidence", mismatches)
	}
	if len(gated.MissingEvidence) == 0 || gated.RecommendedNextAction == "" {
		t.Fatalf("gate metadata not merged for review: %#v", gated)
	}

	// 真 material contradiction：cap 0.69、不可 planning，且进入 contradictions。
	contradicted := &DiagnosisOutput{
		Fixability:             domain.FixabilityCodeFixable,
		Confidence:             0.9,
		CausalReasoning:        "direct fault",
		EvidenceCitations:      []domain.EvidenceCitation{{EvidenceID: "ev-fault", Classification: domain.EvidenceDirectFault}},
		TimeAssessment:         &domain.TimeAssessment{Basis: "paired_epoch", Certainty: "high"},
		MaterialContradictions: []string{"runtime window conflicts with the alert event time"},
		CausalClosure:          &domain.CausalClosure{ExplainsOriginalSymptom: true},
	}
	writer := &recordingAssessmentWriter{}
	gated, decision, _, err = NewEvidenceGate(resolverWithWriter{resolver: contradictionResolver{}, writer: writer}).Apply(context.Background(), "run-1", contradicted)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if decision.ConfidenceCap != 0.69 || decision.PlanningEligible || len(decision.Contradictions) == 0 {
		t.Fatalf("material contradiction handling changed: %#v", decision)
	}
	if gated.Fixability != domain.FixabilityCodeFixable {
		t.Fatalf("conclusion was silently rewritten on a hard block: %#v", gated)
	}
}

// contradictionResolver 返回有 direct fault record 与未决 correlation 的解析
// 结果，让输入中的 material contradiction 成为唯一的 unresolved 原因。
type contradictionResolver struct{}

func (contradictionResolver) ResolveEvidence(_ context.Context, _ string, citations []domain.EvidenceCitation) (domain.EvidenceResolution, error) {
	records := make([]domain.EvidenceRecord, 0, len(citations))
	for _, citation := range citations {
		records = append(records, domain.EvidenceRecord{
			EvidenceID: citation.EvidenceID, Classification: domain.EvidenceDirectFault, Available: true,
		})
	}
	return domain.EvidenceResolution{
		Records: records,
		Sources: []domain.SourceCoverage{{SourceID: "source-1", Primary: true, Status: domain.SourceInspectedSuccess}},
		Time:    &domain.TimeAssessment{Basis: "paired_epoch", Certainty: "high"},
		Correlation: &domain.CorrelationAssessment{
			Temporal: true, Operational: true, HostIdentity: true,
		},
		MaterialContradictions: []string{"runtime window conflicts with the alert event time"},
	}, nil
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

	gated, decision, _, err := NewEvidenceGate(evidenceResolverWithoutTime{}).Apply(context.Background(), "run-1", diagnosis)
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

func TestEvidenceGateKeepsNonMaterialTimeContradictionAuditOnly(t *testing.T) {
	diagnosis := &DiagnosisOutput{
		Fixability:        domain.FixabilityCodeFixable,
		Confidence:        0.9,
		CausalReasoning:   "persisted direct evidence and source explain the fault",
		EvidenceCitations: []domain.EvidenceCitation{{EvidenceID: "ev-fault", Classification: domain.EvidenceDirectFault}},
		TimeAssessment: &domain.TimeAssessment{
			OriginalValues: []string{"2026-08-24T07:16:29Z", "2026-08-24T07:17:05Z"},
			Basis:          "paired_epoch",
			Certainty:      "high",
			Contradictory:  true,
		},
		CausalClosure: &domain.CausalClosure{ExplainsOriginalSymptom: true},
	}

	gated, decision, _, err := NewEvidenceGate(evidenceResolverWithoutTime{}).Apply(context.Background(), "run-1", diagnosis)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if !decision.PlanningEligible || decision.ConfidenceCap != 1 || len(decision.Contradictions) != 0 {
		t.Fatalf("decision=%#v, want non-material time mismatch to remain audit-only", decision)
	}
	if gated.TimeAssessment == nil || !gated.TimeAssessment.Contradictory {
		t.Fatalf("gated=%#v, want original time assessment preserved for audit", gated)
	}
}

// persistedHostIdentityResolver 返回 SSH inspect 持久化投影 + Docker/Tencent 直接
// 故障证据：temporal/operational/direct-bridge 布尔为真，但 hostIdentity 是数据库
// 布尔投影所不表示的字段，恒为 false。
type persistedHostIdentityResolver struct{}

func (persistedHostIdentityResolver) ResolveEvidence(_ context.Context, _ string, citations []domain.EvidenceCitation) (domain.EvidenceResolution, error) {
	records := make([]domain.EvidenceRecord, 0, len(citations))
	for _, citation := range citations {
		classification := citation.Classification
		if classification == "" {
			classification = domain.EvidenceCorrelatedSupport
		}
		records = append(records, domain.EvidenceRecord{
			EvidenceID: citation.EvidenceID, Classification: classification, Available: true,
			TemporalCorrelation: true, OperationalCorrelation: true, Primary: true,
		})
	}
	return domain.EvidenceResolution{
		Records: records,
		Sources: []domain.SourceCoverage{{SourceID: "source-1", Primary: true, Status: domain.SourceInspectedSuccess}},
		Time:    &domain.TimeAssessment{Basis: "paired_epoch", Certainty: "high"},
		// hostIdentity 不在持久化 schema 中；必须保留模型基于 SSH inspect 证据的归并。
		Correlation: &domain.CorrelationAssessment{Temporal: true, Operational: true, HostIdentity: false},
	}, nil
}

// TestEvidenceGatePreservesHostIdentityFromPersistedSSHInspect 覆盖设计第 5 步：
// 持久化 correlation 的布尔投影不表示 hostIdentity，合并时保留模型对主机身份
// 的归并，避免空布尔擦除该语义。
func TestEvidenceGatePreservesHostIdentityFromPersistedSSHInspect(t *testing.T) {
	// 模型通过持久化 SSH inspect 证据（hostname -I / ip addr show）归并了
	// INC-2267 的 VM-6-17-tencentos / 10.16.6.17 与 SSH 源公网地址；Docker/Tencent
	// 记录提供直接故障证据，SSH inspect 是相关性支持证据。
	diagnosis := &DiagnosisOutput{
		Fixability:      domain.FixabilityCodeFixable,
		Confidence:      0.9,
		CausalReasoning: "host identity reconciled from persisted ssh.inspect",
		EvidenceCitations: []domain.EvidenceCitation{
			{EvidenceID: "ev-docker", Classification: domain.EvidenceDirectFault},
			{EvidenceID: "ev-ssh-inspect", Classification: domain.EvidenceCorrelatedSupport},
		},
		TimeAssessment: &domain.TimeAssessment{Basis: "paired_epoch", Certainty: "high"},
		Correlation: &domain.CorrelationAssessment{
			Temporal: true, Operational: true, HostIdentity: true, DirectBridge: true,
		},
		CausalClosure: &domain.CausalClosure{ExplainsOriginalSymptom: true},
	}

	gated, decision, _, err := NewEvidenceGate(persistedHostIdentityResolver{}).Apply(context.Background(), "run-1", diagnosis)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if !decision.PlanningEligible || gated.Fixability != domain.FixabilityCodeFixable {
		t.Fatalf("gated=%#v decision=%#v, want planning eligible after host identity preservation", gated, decision)
	}

	// 反向：模型没有做主机身份归并时，持久化投影的 false 保持权威，不伪造 hostIdentity。
	diagnosisWithout := &DiagnosisOutput{
		Fixability:      domain.FixabilityCodeFixable,
		Confidence:      0.9,
		CausalReasoning: "no host identity claim",
		EvidenceCitations: []domain.EvidenceCitation{
			{EvidenceID: "ev-docker", Classification: domain.EvidenceDirectFault},
			{EvidenceID: "ev-ssh-inspect", Classification: domain.EvidenceCorrelatedSupport},
		},
		TimeAssessment: &domain.TimeAssessment{Basis: "paired_epoch", Certainty: "high"},
		Correlation:    &domain.CorrelationAssessment{Temporal: true, Operational: true, HostIdentity: false},
		CausalClosure:  &domain.CausalClosure{ExplainsOriginalSymptom: true},
	}
	if _, _, _, err := NewEvidenceGate(persistedHostIdentityResolver{}).Apply(context.Background(), "run-1", diagnosisWithout); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
}
