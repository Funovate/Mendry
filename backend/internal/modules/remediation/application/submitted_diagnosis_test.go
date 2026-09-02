package application_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"fixthe/backend/internal/modules/remediation/application"
	"fixthe/backend/internal/modules/remediation/domain"
)

// hardRejectedCodeFixableEnvelope 返回一个会被 evidence gate 硬拒绝的
// code_fixable 诊断：不注入 resolver 时 gate 没有持久化 direct evidence
// （cap 0.39、不可 planning），causalClosure 为 false 使路由直接进入
// blocked_manual_review（不触发 reassessment 轮次）。
func hardRejectedCodeFixableEnvelope() string {
	return `{"schemaVersion":"v1","kind":"diagnosis","diagnosis":{` +
		`"fixability":"code_fixable","confidence":0.9,"causalReasoning":"root cause",` +
		`"contradictions":[],"missingEvidence":[],"evidenceCitations":["ev-1"],` +
		`"recommendedNextAction":"next","alertQuality":"enriched",` +
		`"causalClosure":{"explainsOriginalSymptom":false,"explanation":"needs runtime correlation"}}}`
}

// TestCoordinator_SubmittedDiagnosisKeepsOriginalWhenGateHardRejects 覆盖
// INC-2270 audit 核心：gate 硬拒绝 code_fixable 时，submitted 行保留模型原始
// fixability/confidence（不被改写为 insufficient_evidence），gate_outcome
// 记录 rejected，而 accepted decision 仍是路由后的 insufficient_evidence；
// 既有路由行为（blocked_manual_review）保持不变。
func TestCoordinator_SubmittedDiagnosisKeepsOriginalWhenGateHardRejects(t *testing.T) {
	model := &scriptedModel{responses: []string{hardRejectedCodeFixableEnvelope()}}
	store := newFakeRunStore()
	// 不注入 evidence resolver：gate 无持久化 direct evidence，硬拒绝 code_fixable。
	coord := application.NewRemediationCoordinatorWithReview(
		store, &fakeRepoPort{}, &fakeEvidencePort{}, model, nil, store, store,
	)
	run, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.State != domain.RunStateBlockedManualReview || store.state != domain.RunStateBlockedManualReview {
		t.Fatalf("final state = %s/%s, want blocked_manual_review", run.State, store.state)
	}
	if len(store.submitted) != 1 {
		t.Fatalf("submitted rows = %d, want 1", len(store.submitted))
	}
	submitted := store.submitted[0]
	// INC-2270 审计核心：submitted 行是模型原始 pre-gate 值。
	if submitted.Fixability != domain.FixabilityCodeFixable || submitted.Confidence != 0.9 {
		t.Fatalf("submitted = %#v, want original code_fixable/0.9", submitted)
	}
	if submitted.GateOutcome != "rejected" {
		t.Fatalf("gate outcome = %q, want rejected", submitted.GateOutcome)
	}
	if submitted.Correction.Corrected {
		t.Fatalf("correction = %#v, want no correction metadata on a hard rejection", submitted.Correction)
	}
	// accepted decision 仍是路由后的 insufficient_evidence，路由行为未变。
	if len(store.decisions) != 1 || store.decisions[0].Fixability != domain.FixabilityInsufficientEvidence {
		t.Fatalf("decisions = %#v, want routed insufficient_evidence", store.decisions)
	}
}

type factCheckSequenceResolver struct {
	calls int
}

func (r *factCheckSequenceResolver) ResolveEvidence(_ context.Context, _ string, citations []domain.EvidenceCitation) (domain.EvidenceResolution, error) {
	r.calls++
	if r.calls == 1 {
		return domain.EvidenceResolution{}, nil
	}
	return (fakeEvidenceResolver{}).ResolveEvidence(context.Background(), "run-1", citations)
}

// TestCoordinator_ResilientFactCheckChallengesWithoutFixabilityRewrite 证明 R6：
// resilient_v1 的 failed fact check 持久化原 submission 并回喂 challenge；修正
// 后只有 code_fixable accepted decision，不产生静默 insufficient_evidence decision。
func TestCoordinator_ResilientFactCheckChallengesWithoutFixabilityRewrite(t *testing.T) {
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	model := &scriptedModel{responses: []string{
		hardRejectedCodeFixableEnvelope(),
		diagnosisEnvelope("code_fixable"),
		planEnvelope(),
	}}
	coord := newCoordinator(store, &fakeRepoPort{}, &fakeEvidencePort{}, model)
	coord.SetCheckpointStore(&fakeCheckpointStore{})
	resolver := &factCheckSequenceResolver{}
	coord.SetEvidenceResolver(resolver)

	run, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if run.State != domain.RunStateDiagnosisReadyForReview {
		t.Fatalf("final state = %s, want diagnosis_ready_for_review", run.State)
	}
	if len(store.decisions) != 1 || store.decisions[0].Fixability != domain.FixabilityCodeFixable {
		t.Fatalf("decisions = %#v, want only corrected code_fixable decision", store.decisions)
	}
	if len(store.submitted) != 2 || store.submitted[0].Fixability != domain.FixabilityCodeFixable ||
		store.submitted[0].GateOutcome != "rejected" || store.submitted[0].DecisionID != "" {
		t.Fatalf("submitted audit = %#v", store.submitted)
	}
	if !strings.Contains(model.turns[1].UserMessage, `"reasonCode":"fact_check_rejected"`) ||
		!strings.Contains(model.turns[1].UserMessage, "fixability and confidence were not rewritten") {
		t.Fatalf("second turn lacks fact-check challenge: %s", model.turns[1].UserMessage)
	}
}

// TestCoordinator_SubmittedDiagnosisFailureIsPersistenceBlocker 证明 D4/R21：
// audit append 失败不会被吞掉；run 进入 persistence_failure，不能继续到业务终态。
func TestCoordinator_SubmittedDiagnosisFailureIsPersistenceBlocker(t *testing.T) {
	store := newFakeRunStore()
	store.submittedErr = errors.New("audit unavailable")
	model := &scriptedModel{responses: []string{diagnosisEnvelope("external_dependency")}}
	coord := newCoordinator(store, &fakeRepoPort{}, &fakeEvidencePort{}, model)

	_, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	if err == nil {
		t.Fatal("Start() error = nil, want persistence failure")
	}
	if store.state != domain.RunStateFailed || len(store.effects) == 0 ||
		store.effects[len(store.effects)-1].TerminalReason != "persistence_failure" {
		t.Fatalf("state/effects = %s/%#v, want failed persistence_failure", store.state, store.effects)
	}
	if len(store.submitted) != 0 || store.countTransitionsTo(domain.RunStateCompletedNonCode) != 0 {
		t.Fatalf("audit failure was ignored: submitted=%#v transitions=%#v", store.submitted, store.transitions)
	}
}

// TestCoordinator_SubmittedDiagnosisRecordsCitationCorrection 覆盖 R7/AC5：
// citation classification 差异在 submitted 行记录 correction metadata
// （kind evidence_correction、权威 stored classification、corrected=true），
// 循环继续；修正后的诊断产生 accepted decision，审计链 join 到该 decision。
func TestCoordinator_SubmittedDiagnosisRecordsCitationCorrection(t *testing.T) {
	model := &scriptedModel{responses: []string{
		mismatchedClassificationEnvelope("code_fixable"),
		diagnosisEnvelope("code_fixable"),
		planEnvelope(),
	}}
	store := newFakeRunStore()
	coord := newCoordinator(store, &fakeRepoPort{}, &fakeEvidencePort{}, model)
	run, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.State != domain.RunStateDiagnosisReadyForReview {
		t.Fatalf("final state = %s, want diagnosis_ready_for_review", run.State)
	}
	if len(store.submitted) != 2 {
		t.Fatalf("submitted rows = %d, want 2 (mismatched then corrected)", len(store.submitted))
	}
	first := store.submitted[0]
	// 被 challenge 的提交：correction metadata 记录权威 stored classification。
	if !first.Correction.Corrected || first.Correction.Kind != domain.SubmittedCorrectionEvidence ||
		first.Correction.CorrectionCount != 1 ||
		len(first.Correction.CorrectedEvidence) != 1 ||
		first.Correction.CorrectedEvidence[0].EvidenceID != "ev-1" ||
		first.Correction.CorrectedEvidence[0].StoredClassification != domain.EvidenceDirectFault {
		t.Fatalf("first correction = %#v, want evidence_correction with stored direct_fault", first.Correction)
	}
	// R7：classification 差异不独立改变 gate 判定；gate verdict 照常记录。
	if first.GateOutcome != "planning_eligible" {
		t.Fatalf("first gate outcome = %q, want planning_eligible", first.GateOutcome)
	}
	// 被 challenge 的轮次没有 accepted decision：audit 链不得误链历史 decision。
	if first.DecisionID != "" {
		t.Fatalf("first decision link = %q, want empty", first.DecisionID)
	}
	// 修正后的提交：无 correction，链接到本轮的 accepted decision。
	second := store.submitted[1]
	if second.Correction.Corrected || second.GateOutcome != "planning_eligible" || second.DecisionID != "decision-1" {
		t.Fatalf("second submitted = %#v, want clean planning_eligible linked to decision-1", second)
	}
	if len(store.decisions) != 1 || store.decisions[0].Fixability != domain.FixabilityCodeFixable {
		t.Fatalf("decisions = %#v, want one accepted code_fixable", store.decisions)
	}
}

// TestCoordinator_SubmittedDiagnosisRecordsExhaustionAcceptance 覆盖 D6：已接受
// 的 exhaustion proof 记录一条 submitted 审计行（correction kind exhaustion，
// decisionID 指向 accepted decision），不改变 exhaustion 终态路径。
func TestCoordinator_SubmittedDiagnosisRecordsExhaustionAcceptance(t *testing.T) {
	limits := application.DefaultBudgetLimits()
	limits.MaxModelCalls = 2
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	checkpoints := &fakeCheckpointStore{}
	model := &scriptedModel{responses: []string{
		stopEnvelope(),
		budgetProhibitedExhaustionProposalEnvelope(),
	}}
	coord := newCoordinatorWithBudget(store, &fakeRepoPort{}, &fakeEvidencePort{}, model, limits)
	coord.SetCheckpointStore(checkpoints)

	run, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.State != domain.RunStateBlockedManualReview || store.state != domain.RunStateBlockedManualReview {
		t.Fatalf("final state = %s/%s, want blocked_manual_review", run.State, store.state)
	}
	if len(store.submitted) != 1 {
		t.Fatalf("submitted rows = %d, want 1", len(store.submitted))
	}
	submitted := store.submitted[0]
	if submitted.Fixability != domain.FixabilityInsufficientEvidence {
		t.Fatalf("submitted fixability = %s, want proposal best conclusion", submitted.Fixability)
	}
	if submitted.Correction.Kind != domain.SubmittedCorrectionExhaustion ||
		!submitted.Correction.Corrected || submitted.Correction.CorrectionCount != 1 {
		t.Fatalf("correction = %#v, want exhaustion correction metadata", submitted.Correction)
	}
	if submitted.GateOutcome != "" {
		t.Fatalf("gate outcome = %q, want empty (no evidence gate ran)", submitted.GateOutcome)
	}
	// decisionID 指向本次 exhaustion appendDecision 创建的 accepted decision。
	if submitted.DecisionID != "decision-1" {
		t.Fatalf("decision link = %q, want decision-1", submitted.DecisionID)
	}
	if len(store.decisions) != 1 || store.decisions[0].Fixability != domain.FixabilityInsufficientEvidence {
		t.Fatalf("decisions = %#v, want one accepted proposal decision", store.decisions)
	}
}

// TestCoordinator_SubmittedDiagnosisOmitsRawConversationText 证明 D4：persisted
// 审计投影只含 envelope 的有界结构化字段，绝不包含模型轮次的完整
// conversation 文本或原始 envelope JSON。
func TestCoordinator_SubmittedDiagnosisOmitsRawConversationText(t *testing.T) {
	rawEnvelope := diagnosisEnvelope("external_dependency")
	model := &scriptedModel{responses: []string{rawEnvelope}}
	store, _, err := runStart(t, model)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(store.submitted) != 1 {
		t.Fatalf("submitted rows = %d, want 1", len(store.submitted))
	}
	submitted := store.submitted[0]
	// 有界结构化字段原样保留：fixability/confidence/reasoning/citations。
	if submitted.Fixability != domain.FixabilityExternalDependency ||
		submitted.Confidence != 0.9 ||
		submitted.CausalReasoning != "root cause" ||
		len(submitted.EvidenceCitations) != 1 || submitted.EvidenceCitations[0] != "ev-1" ||
		submitted.RecommendedNextAction != "next" {
		t.Fatalf("submitted projection = %#v", submitted)
	}
	// 完整 conversation 文本与原始 envelope JSON 绝不进入审计行。
	rendered := fmt.Sprintf("%#v", submitted)
	if strings.Contains(rendered, model.turns[0].UserMessage) {
		t.Fatal("submitted diagnosis persisted the full conversation text")
	}
	if strings.Contains(rendered, rawEnvelope) {
		t.Fatal("submitted diagnosis persisted the raw model envelope")
	}
}

// TestCoordinator_LegacyModeRecordsSubmittedAuditWithUnchangedRouting 证明
// audit 持久化按 store capability 生效（非按模式）：legacy run 同样记录
// submitted 行，同时 routing/decision 行为与既有 legacy 测试完全一致。
func TestCoordinator_LegacyModeRecordsSubmittedAuditWithUnchangedRouting(t *testing.T) {
	model := &scriptedModel{responses: []string{diagnosisEnvelope("external_dependency")}}
	store, run, err := runStart(t, model)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.State != domain.RunStateCompletedNonCode || store.state != domain.RunStateCompletedNonCode {
		t.Fatalf("final state = %s/%s, want completed_non_code", run.State, store.state)
	}
	if store.countTransitionsTo(domain.RunStateCompletedNonCode) != 1 {
		t.Fatalf("transitions to completed_non_code = %d, want 1 (routing unchanged)", store.countTransitionsTo(domain.RunStateCompletedNonCode))
	}
	if len(store.decisions) != 1 || store.decisions[0].Fixability != domain.FixabilityExternalDependency {
		t.Fatalf("decisions = %#v, want one external_dependency", store.decisions)
	}
	if len(store.submitted) != 1 {
		t.Fatalf("submitted rows = %d, want 1 in legacy mode", len(store.submitted))
	}
	submitted := store.submitted[0]
	if submitted.Fixability != domain.FixabilityExternalDependency || submitted.GateOutcome != "" ||
		submitted.DecisionID != "decision-1" {
		t.Fatalf("legacy submitted = %#v", submitted)
	}
}
