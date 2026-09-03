package application_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"fixthe/backend/internal/modules/remediation/application"
	"fixthe/backend/internal/modules/remediation/domain"
)

// recordingResilienceMetrics 记录 coordinator 发出的低基数指标事件。
type recordingResilienceMetrics struct {
	events []application.ResilienceMetric
}

func (r *recordingResilienceMetrics) RecordResilienceMetric(_ context.Context, metric application.ResilienceMetric) {
	r.events = append(r.events, metric)
}

func (r *recordingResilienceMetrics) count(kind string) int {
	count := 0
	for _, event := range r.events {
		if event.Kind == kind {
			count++
		}
	}
	return count
}

func (r *recordingResilienceMetrics) first(kind string) (application.ResilienceMetric, bool) {
	for _, event := range r.events {
		if event.Kind == kind {
			return event, true
		}
	}
	return application.ResilienceMetric{}, false
}

// countChallengeKind 返回指定 D5 challenge kind 的 challenge 事件数。
func (r *recordingResilienceMetrics) countChallengeKind(kind domain.RecoveryChallengeKind) int {
	count := 0
	for _, event := range r.events {
		if event.Kind == application.ResilienceMetricChallenge && event.ChallengeKind == kind {
			count++
		}
	}
	return count
}

// recoverySuccessEvents 返回全部 recovery-success 事件。
func (r *recordingResilienceMetrics) recoverySuccessEvents() []application.ResilienceMetric {
	out := make([]application.ResilienceMetric, 0, 1)
	for _, event := range r.events {
		if event.Kind == application.ResilienceMetricRecoverySuccess {
			out = append(out, event)
		}
	}
	return out
}

// TestResilientMetricsEmitLowCardinalityRecoveryEvents 验证 resilient_v1 的
// evidence-correction 恢复流发出 run_started/state_transitioned/checkpoint/
// run_terminal 指标事件：recovery checkpoint 事件携带 mode/phase/reason 与
// no-progress 标记，事件绝不携带证据内容或模型轮次，且不影响 run 语义。
func TestResilientMetricsEmitLowCardinalityRecoveryEvents(t *testing.T) {
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	checkpoints := &fakeCheckpointStore{}
	metrics := &recordingResilienceMetrics{}
	model := &scriptedModel{responses: []string{
		mismatchedClassificationEnvelope("code_fixable"),
		diagnosisEnvelope("code_fixable"),
		planEnvelope(),
	}}
	coord := newCoordinator(store, &fakeRepoPort{}, &fakeEvidencePort{}, model)
	coord.SetCheckpointStore(checkpoints)
	coord.SetResilienceMetricObserver(metrics)

	run, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.State != domain.RunStateDiagnosisReadyForReview {
		t.Fatalf("final state = %s, want diagnosis_ready_for_review", run.State)
	}
	if metrics.count(application.ResilienceMetricRunStarted) != 1 {
		t.Fatalf("run_started events = %d, want 1", metrics.count(application.ResilienceMetricRunStarted))
	}
	started, _ := metrics.first(application.ResilienceMetricRunStarted)
	if started.Mode != domain.AgentLoopModeResilientV1 || started.Run.RunID == "" {
		t.Fatalf("run_started metric = %+v", started)
	}
	if metrics.count(application.ResilienceMetricStateTransitioned) == 0 {
		t.Fatal("state_transitioned events are missing")
	}
	if metrics.count(application.ResilienceMetricCheckpoint) == 0 {
		t.Fatal("checkpoint events are missing")
	}
	recoveryCount := 0
	phaseCount := 0
	for _, event := range metrics.events {
		if event.Kind != application.ResilienceMetricCheckpoint {
			continue
		}
		if event.Mode != domain.AgentLoopModeResilientV1 || event.Run.RunID == "" {
			t.Fatalf("checkpoint metric identity invalid: %+v", event)
		}
		switch event.Reason {
		case domain.CheckpointReasonRecovery:
			recoveryCount++
		case domain.CheckpointReasonPhaseBoundary:
			phaseCount++
		}
	}
	if recoveryCount != 1 {
		t.Fatalf("recovery checkpoint events = %d, want 1", recoveryCount)
	}
	if phaseCount == 0 {
		t.Fatal("phase-boundary checkpoint events are missing")
	}
	terminal, ok := metrics.first(application.ResilienceMetricRunTerminal)
	if !ok {
		t.Fatal("run_terminal event is missing")
	}
	if terminal.Phase != domain.RunStateDiagnosisReadyForReview || terminal.Mode != domain.AgentLoopModeResilientV1 {
		t.Fatalf("run_terminal metric = %+v", terminal)
	}
}

// TestLegacyMetricsEmitOnlyRunStarted 验证 legacy run 只发出 run_started 指标
// （无 tracker 的状态/checkpoint 事件保持 no-op），且 metrics observer 不改变
// legacy 行为。
func TestLegacyMetricsEmitOnlyRunStarted(t *testing.T) {
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeLegacy
	metrics := &recordingResilienceMetrics{}
	model := &scriptedModel{responses: []string{diagnosisEnvelope("code_fixable"), planEnvelope()}}
	coord := newCoordinator(store, &fakeRepoPort{}, &fakeEvidencePort{}, model)
	coord.SetResilienceMetricObserver(metrics)

	run, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.State != domain.RunStateDiagnosisReadyForReview {
		t.Fatalf("final state = %s, want diagnosis_ready_for_review", run.State)
	}
	if metrics.count(application.ResilienceMetricRunStarted) != 1 {
		t.Fatalf("run_started events = %d, want 1", metrics.count(application.ResilienceMetricRunStarted))
	}
	if metrics.count(application.ResilienceMetricStateTransitioned) != 0 ||
		metrics.count(application.ResilienceMetricCheckpoint) != 0 ||
		metrics.count(application.ResilienceMetricChallenge) != 0 ||
		metrics.count(application.ResilienceMetricRecoverySuccess) != 0 {
		t.Fatalf("legacy run must not emit tracker-scoped metrics: %#v", metrics.events)
	}
}

// TestResilientMetricsEvidenceCorrectionChallengeOnceAndRecoverySuccess 验证
// 共享出口的 per-challenge-kind 指标与 recovery-success 语义：citation
// classification mismatch 只追加一条 evidence_correction challenge（不重复
// 计数），其 recovery checkpoint 打开 episode；随后修正的 code_fixable 通过
// gate 并进入 planning 边界时，第一个非 recovery durable checkpoint 恰好结算
// 一次 recovery-success。
func TestResilientMetricsEvidenceCorrectionChallengeOnceAndRecoverySuccess(t *testing.T) {
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	checkpoints := &fakeCheckpointStore{}
	metrics := &recordingResilienceMetrics{}
	model := &scriptedModel{responses: []string{
		mismatchedClassificationEnvelope("code_fixable"),
		diagnosisEnvelope("code_fixable"),
		planEnvelope(),
	}}
	coord := newCoordinator(store, &fakeRepoPort{}, &fakeEvidencePort{}, model)
	coord.SetCheckpointStore(checkpoints)
	coord.SetResilienceMetricObserver(metrics)

	run, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.State != domain.RunStateDiagnosisReadyForReview {
		t.Fatalf("final state = %s, want diagnosis_ready_for_review", run.State)
	}
	if count := metrics.countChallengeKind(domain.RecoveryChallengeKindEvidenceCorrection); count != 1 {
		t.Fatalf("evidence_correction challenge events = %d, want exactly 1", count)
	}
	if metrics.count(application.ResilienceMetricChallenge) != 1 {
		t.Fatalf("challenge events = %d, want exactly 1 (no duplicate counting)", metrics.count(application.ResilienceMetricChallenge))
	}
	challenge, _ := metrics.first(application.ResilienceMetricChallenge)
	if challenge.Phase != domain.RunStateDiagnosing || challenge.ChallengeKind != domain.RecoveryChallengeKindEvidenceCorrection {
		t.Fatalf("challenge event = %+v, want diagnosing + evidence_correction", challenge)
	}
	successes := metrics.recoverySuccessEvents()
	if len(successes) != 1 {
		t.Fatalf("recovery_success events = %d (%#v), want exactly 1", len(successes), successes)
	}
	if successes[0].Phase != domain.RunStateDiagnosing || successes[0].Reason != domain.CheckpointReasonPhaseBoundary {
		t.Fatalf("recovery_success event = %+v, want diagnosing + phase_boundary", successes[0])
	}
}

// TestResilientMetricsAbandonmentExhaustionHasNoRecoverySuccess 验证放弃型终态
// 语义：resilient_v1 下 stop 请求 exhaustion proof（episode 打开），不完整
// proof 拒绝产生第二条 exhaustion challenge，最终接受的完整 proof 进入
// blocked_manual_review。整个流程没有非 recovery durable 前进，因此必须
// 0 次 recovery-success；同时每个追加的 challenge 都恰好计一次（不重复）。
func TestResilientMetricsAbandonmentExhaustionHasNoRecoverySuccess(t *testing.T) {
	// MaxModelCalls=3 让第三个模型轮次的 usage 消耗后剩余为零，使
	// budget_prohibited proof 可被服务接受（与既有 exhaustion 集成测试一致）；
	// soft 分配随消耗增强最多追加一条 kind=budget 的 challenge，不影响本测试
	// 对 exhaustion kind 与 recovery-success 的断言。
	limits := application.DefaultBudgetLimits()
	limits.MaxModelCalls = 3
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	metrics := &recordingResilienceMetrics{}
	model := &scriptedModel{responses: []string{
		stopEnvelope(),
		incompleteExhaustionProposalEnvelope(),
		budgetProhibitedExhaustionProposalEnvelope(),
	}}
	coord := newCoordinatorWithBudget(store, &fakeRepoPort{}, &fakeEvidencePort{}, model, limits)
	coord.SetCheckpointStore(&fakeCheckpointStore{})
	coord.SetResilienceMetricObserver(metrics)

	run, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.State != domain.RunStateBlockedManualReview {
		t.Fatalf("final state = %s, want blocked_manual_review", run.State)
	}
	if count := metrics.countChallengeKind(domain.RecoveryChallengeKindExhaustion); count != 2 {
		t.Fatalf("exhaustion challenge events = %d, want 2 (request + rejection)", count)
	}
	// 每个追加的 challenge 恰好计一次：除 exhaustion(2) 外只允许 soft-budget
	// 增强产生的一条 budget challenge，绝无其它重复 kind。
	if metrics.count(application.ResilienceMetricChallenge) != 3 {
		t.Fatalf("challenge events = %d, want 3 (2 exhaustion + 1 budget)", metrics.count(application.ResilienceMetricChallenge))
	}
	if successes := metrics.recoverySuccessEvents(); len(successes) != 0 {
		t.Fatalf("recovery_success events = %#v, want none for an abandoned run", successes)
	}
	terminal, ok := metrics.first(application.ResilienceMetricRunTerminal)
	if !ok || terminal.Phase != domain.RunStateBlockedManualReview || terminal.TerminalReason != "exhaustion_proof" {
		t.Fatalf("run_terminal event = %+v, want blocked_manual_review + exhaustion_proof", terminal)
	}
}

// TestResilientMetricsPlanPolicyChallengeIsValidationRevisionAtPlanning 验证
// planning 阶段的 recoverable plan-policy 反馈走同一共享出口：kind 为
// validation_revision、phase 为 planning，并且修正后的 accepted plan 进入
// diagnosis_ready_for_review（productive terminal）时结算一次 recovery-success。
func TestResilientMetricsPlanPolicyChallengeIsValidationRevisionAtPlanning(t *testing.T) {
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	metrics := &recordingResilienceMetrics{}
	model := &scriptedModel{responses: []string{
		diagnosisEnvelope("code_fixable"),
		`{"schemaVersion":"v1","kind":"planCandidates","planCandidates":{"candidates":[` +
			`{"planId":"high","evidenceRefs":["ev-1"],"affectedFiles":["go.mod"],"intendedBehavior":"change dependency","risk":"high_risk","rollbackStrategy":"revert"},` +
			`{"planId":"ordinary","evidenceRefs":["ev-1"],"affectedFiles":["main.go"],"intendedBehavior":"fix handler","risk":"ordinary","rollbackStrategy":"revert"}],` +
			`"recommendedId":"high","rationale":"first choice","suggestedDiff":"diff --git a/go.mod"}}`,
		planEnvelope(),
	}}
	coord := newCoordinator(store, &fakeRepoPort{}, &fakeEvidencePort{}, model)
	coord.SetCheckpointStore(&fakeCheckpointStore{})
	coord.SetResilienceMetricObserver(metrics)

	run, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.State != domain.RunStateDiagnosisReadyForReview {
		t.Fatalf("final state = %s, want diagnosis_ready_for_review", run.State)
	}
	if count := metrics.countChallengeKind(domain.RecoveryChallengeKindValidationRevision); count != 1 {
		t.Fatalf("validation_revision challenge events = %d, want exactly 1", count)
	}
	challenge, _ := metrics.first(application.ResilienceMetricChallenge)
	if challenge.Phase != domain.RunStatePlanning || challenge.ChallengeKind != domain.RecoveryChallengeKindValidationRevision {
		t.Fatalf("challenge event = %+v, want planning + validation_revision", challenge)
	}
	successes := metrics.recoverySuccessEvents()
	if len(successes) != 1 {
		t.Fatalf("recovery_success events = %d (%#v), want exactly 1", len(successes), successes)
	}
	if successes[0].Phase != domain.RunStatePlanning {
		t.Fatalf("recovery_success event = %+v, want planning phase", successes[0])
	}
}

// TestResilientMetricsLifecyclePublicationRetryCountsChallengeAndConvergence
// 验证 lifecycle 阶段走同一共享出口：一次 transient publication failure 追加
// 一条 kind=publication_retry 的 challenge（publishing 阶段），run 保持 active
// 且此时不产生 recovery-success；进程 restart 后 ResumeLifecycle 从 durable
// recovery checkpoint 重建（episode 保持打开），第一次 durable 前进的
// phase_boundary checkpoint 恰好结算一次 recovery-success，最终进入
// awaiting_human_review。
func TestResilientMetricsLifecyclePublicationRetryCountsChallengeAndConvergence(t *testing.T) {
	metrics := &recordingResilienceMetrics{}
	coord, store, _, _, _, _, publication := setupLifecycleCoordinator(t, []string{
		diagnosisEnvelope("code_fixable"), planEnvelope(),
		patchRequestEnvelope("diff --git a/main.go b/main.go\n"), patchCompleteEnvelope(),
		validationRequestEnvelope("unit"), validationAssessmentEnvelope(true),
	})
	coord.SetResilienceMetricObserver(metrics)
	publication.errors = []error{&domain.LifecycleRuntimeError{Code: "transport", Retryable: true}}
	startLifecyclePlan(t, coord, store)

	run, err := coord.ApplyPlan(context.Background(), "run-1", "p1")
	if err != nil {
		t.Fatalf("ApplyPlan() transient error = %v", err)
	}
	if run.State != domain.RunStatePublishing || store.state != domain.RunStatePublishing {
		t.Fatalf("after transient publication state = %s/%s, want publishing", run.State, store.state)
	}
	if count := metrics.countChallengeKind(domain.RecoveryChallengeKindPublicationRetry); count != 1 {
		t.Fatalf("publication_retry challenge events = %d, want exactly 1", count)
	}
	challenge, _ := metrics.first(application.ResilienceMetricChallenge)
	if challenge.Phase != domain.RunStatePublishing || challenge.ChallengeKind != domain.RecoveryChallengeKindPublicationRetry {
		t.Fatalf("challenge event = %+v, want publishing + publication_retry", challenge)
	}
	if successes := metrics.recoverySuccessEvents(); len(successes) != 0 {
		t.Fatalf("recovery_success events before convergence = %#v, want none", successes)
	}

	run, err = coord.ResumeLifecycle(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("ResumeLifecycle() error = %v", err)
	}
	if run.State != domain.RunStateAwaitingHumanReview || store.state != domain.RunStateAwaitingHumanReview {
		t.Fatalf("final state = %s/%s, want awaiting_human_review", run.State, store.state)
	}
	if count := metrics.countChallengeKind(domain.RecoveryChallengeKindPublicationRetry); count != 1 {
		t.Fatalf("publication_retry challenge events after resume = %d, want still exactly 1", count)
	}
	successes := metrics.recoverySuccessEvents()
	if len(successes) != 1 {
		t.Fatalf("recovery_success events = %d (%#v), want exactly 1 after resume convergence", len(successes), successes)
	}
	if successes[0].Phase != domain.RunStatePublishing || successes[0].Reason != domain.CheckpointReasonPhaseBoundary {
		t.Fatalf("recovery_success event = %+v, want publishing + phase_boundary", successes[0])
	}
	var terminal application.ResilienceMetric
	found := false
	for _, event := range metrics.events {
		if event.Kind == application.ResilienceMetricRunTerminal {
			terminal = event
			found = true
		}
	}
	if !found || terminal.Phase != domain.RunStateAwaitingHumanReview || terminal.TerminalReason != "awaiting_human_review" {
		t.Fatalf("final run_terminal event = %+v, want awaiting_human_review", terminal)
	}
}

// factCheckLoopResolver 前三次返回空 resolution（gate 以相同原因硬拒绝三次，
// 形成连续 identical fact-check no-progress），之后委托 fakeEvidenceResolver
// 让同一诊断准入（收敛）。
type factCheckLoopResolver struct {
	calls int
}

func (r *factCheckLoopResolver) ResolveEvidence(_ context.Context, _ string, citations []domain.EvidenceCitation) (domain.EvidenceResolution, error) {
	r.calls++
	if r.calls <= 3 {
		return domain.EvidenceResolution{}, nil
	}
	return (fakeEvidenceResolver{}).ResolveEvidence(context.Background(), "run-1", citations)
}

// noProgressCheckpointEvents 返回全部 checkpoint 指标事件中 NoProgress=true 的
// 子集，便于断言 no-progress 标记只在 escalation checkpoint 上出现。
func (r *recordingResilienceMetrics) noProgressCheckpointEvents() []application.ResilienceMetric {
	out := make([]application.ResilienceMetric, 0, 1)
	for _, event := range r.events {
		if event.Kind == application.ResilienceMetricCheckpoint && event.NoProgress {
			out = append(out, event)
		}
	}
	return out
}

// TestResilientMetricsNoProgressFlagsOnlyEscalationAndClearsOnConvergence 覆盖
// F1：recovery.no_progress 只在连续 identical fact-check 拒绝达到 escalation
// threshold（第 3 次，转为 exhaustion 请求）的 recovery checkpoint 上出现；
// 前两次有界 retry 的 checkpoint 不标记，gate 准入后的 planning phase-boundary
// checkpoint（收敛）也不再携带已结算 loop 的标记（lifetime stickiness 已消除）。
func TestResilientMetricsNoProgressFlagsOnlyEscalationAndClearsOnConvergence(t *testing.T) {
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	checkpoints := &fakeCheckpointStore{}
	metrics := &recordingResilienceMetrics{}
	model := &scriptedModel{responses: []string{
		hardRejectedCodeFixableEnvelope(), // gate reject #1
		hardRejectedCodeFixableEnvelope(), // gate reject #2 (identical)
		hardRejectedCodeFixableEnvelope(), // gate reject #3 → exhaustion request
		diagnosisEnvelope("code_fixable"), // converged, gate admits
		planEnvelope(),
	}}
	coord := newCoordinator(store, &fakeRepoPort{}, &fakeEvidencePort{}, model)
	coord.SetCheckpointStore(checkpoints)
	coord.SetResilienceMetricObserver(metrics)
	coord.SetEvidenceResolver(&factCheckLoopResolver{})

	run, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if run.State != domain.RunStateDiagnosisReadyForReview {
		t.Fatalf("final state = %s, want diagnosis_ready_for_review", run.State)
	}
	flagged := metrics.noProgressCheckpointEvents()
	if len(flagged) != 1 {
		t.Fatalf("no-progress checkpoint events = %d (%#v), want exactly 1 (the exhaustion-request escalation checkpoint)", len(flagged), flagged)
	}
	if flagged[0].Reason != domain.CheckpointReasonRecovery || flagged[0].Phase != domain.RunStateDiagnosing {
		t.Fatalf("flagged checkpoint = %+v, want diagnosing + recovery trigger", flagged[0])
	}
	// 收敛后的 phase-boundary checkpoint（planning 边界/终态前）不得再携带
	// 已结算 loop 的 no-progress 标记。
	for _, event := range metrics.events {
		if event.Kind == application.ResilienceMetricCheckpoint && event.Reason == domain.CheckpointReasonPhaseBoundary && event.NoProgress {
			t.Fatalf("phase-boundary checkpoint after convergence is still flagged: %+v", event)
		}
	}
	// 前两次有界 retry 的 recovery checkpoint 不标记（阈值语义）：至少存在两个
	// 未标记的 recovery checkpoint（rejection #1、#2）。
	unflagged := 0
	for _, event := range metrics.events {
		if event.Kind == application.ResilienceMetricCheckpoint && event.Reason == domain.CheckpointReasonRecovery && !event.NoProgress {
			unflagged++
		}
	}
	if unflagged < 2 {
		t.Fatalf("unflagged recovery checkpoints = %d, want at least 2 (bounded retries before escalation)", unflagged)
	}
}

// TestResilientMetricsExhaustionReRequestFlagsNoProgress 覆盖 F1 的
// protocol/exhaustion loop 覆盖：第一次 stop → exhaustion 请求是正常 give-up
// 转换（不标记）；模型再次 stop（重复循环）触发第二次 exhaustion 请求后，
// checkpoint 达到 exhaustion re-request threshold 才标记 no-progress。
func TestResilientMetricsExhaustionReRequestFlagsNoProgress(t *testing.T) {
	limits := application.DefaultBudgetLimits()
	limits.MaxModelCalls = 3
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	metrics := &recordingResilienceMetrics{}
	model := &scriptedModel{responses: []string{
		stopEnvelope(), // stop #1 → exhaustion request (attempt 1)
		stopEnvelope(), // stop #2 → exhaustion request (attempt 2, loop)
		budgetProhibitedExhaustionProposalEnvelope(), // accepted → manual review
	}}
	coord := newCoordinatorWithBudget(store, &fakeRepoPort{}, &fakeEvidencePort{}, model, limits)
	coord.SetCheckpointStore(&fakeCheckpointStore{})
	coord.SetResilienceMetricObserver(metrics)

	run, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if run.State != domain.RunStateBlockedManualReview {
		t.Fatalf("final state = %s, want blocked_manual_review", run.State)
	}
	flagged := metrics.noProgressCheckpointEvents()
	// 第一次请求（attempt 1）不标记；第二次请求与随后的终态前 checkpoint
	// （attempt 2）标记。
	if len(flagged) != 2 {
		t.Fatalf("no-progress checkpoint events = %d (%#v), want 2 (2nd request + pre-terminal)", len(flagged), flagged)
	}
	for _, event := range flagged {
		if event.Phase != domain.RunStateDiagnosing {
			t.Fatalf("flagged checkpoint phase = %s, want diagnosing", event.Phase)
		}
	}
}

// TestResilientMetricsTencentDetailGateCorrectionCountsProtocolChallengeOnce 覆盖
// F2：resilient_v1 下 mandatory Tencent detail 门关闭导致的
// required_direct_evidence recoverable 修正走共享 per-kind challenge 指标出口，
// 恰好计一次 protocol_correction；模型可见观察与 legacy 一致（对话内容含
// required_direct_evidence code），legacy run 不产生 challenge 指标。
func TestResilientMetricsTencentDetailGateCorrectionCountsProtocolChallengeOnce(t *testing.T) {
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	checkpoints := &fakeCheckpointStore{}
	metrics := &recordingResilienceMetrics{}
	model := &scriptedModel{responses: []string{
		budgetProhibitedExhaustionProposalEnvelope(),
		fmt.Sprintf(`{"schemaVersion":"v1","kind":"requestTool","requestTool":{"toolName":%q,"parameters":{}}}`, application.ToolTencentCLSDetail),
		diagnosisEnvelope("external_dependency"),
	}}
	coord := application.NewRemediationCoordinatorWithReview(store, &fakeRepoPort{}, &fakeEvidencePort{}, model, nil, store, store)
	coord.SetCheckpointStore(checkpoints)
	coord.SetResilienceMetricObserver(metrics)
	coord.SetBootstrapEvidenceLoader(&bootstrapEvidenceLoader{value: tencentBootstrapEvidence()})
	coord.SetTencentCLSDetailPort(&fakeTencentDetailPort{result: tencentDetailResult()})

	run, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if run.State != domain.RunStateCompletedNonCode {
		t.Fatalf("final state = %s, want completed_non_code", run.State)
	}
	// 模型可见观察与 legacy 一致：第二轮上下文含 required_direct_evidence。
	if !strings.Contains(model.turns[1].UserMessage, "required_direct_evidence") {
		t.Fatalf("second turn missing mandatory detail correction: %s", model.turns[1].UserMessage)
	}
	// 恰好一次 protocol_correction challenge 指标（exhaustion 被 gate 拒绝的那次
	// 修正），detail 工具成功后不再重复。
	if count := metrics.countChallengeKind(domain.RecoveryChallengeKindProtocolCorrection); count != 1 {
		t.Fatalf("protocol_correction challenge events = %d, want exactly 1", count)
	}
	if metrics.count(application.ResilienceMetricChallenge) != 1 {
		t.Fatalf("challenge events = %d, want exactly 1", metrics.count(application.ResilienceMetricChallenge))
	}
	challenge, _ := metrics.first(application.ResilienceMetricChallenge)
	if challenge.Phase != domain.RunStateDiagnosing || challenge.ChallengeKind != domain.RecoveryChallengeKindProtocolCorrection {
		t.Fatalf("challenge event = %+v, want diagnosing + protocol_correction", challenge)
	}

	// legacy 对照：同一 gate 流程不产生任何 challenge 指标（metrics 只记录
	// run_started），模型可见修正保持不变。
	legacyStore := newFakeRunStore()
	legacyStore.mode = domain.AgentLoopModeLegacy
	legacyMetrics := &recordingResilienceMetrics{}
	legacyModel := &scriptedModel{responses: []string{
		diagnosisEnvelope("external_dependency"),
		fmt.Sprintf(`{"schemaVersion":"v1","kind":"requestTool","requestTool":{"toolName":%q,"parameters":{}}}`, application.ToolTencentCLSDetail),
		diagnosisEnvelope("external_dependency"),
	}}
	legacyCoord := application.NewRemediationCoordinatorWithReview(legacyStore, &fakeRepoPort{}, &fakeEvidencePort{}, legacyModel, nil, legacyStore, legacyStore)
	legacyCoord.SetResilienceMetricObserver(legacyMetrics)
	legacyCoord.SetBootstrapEvidenceLoader(&bootstrapEvidenceLoader{value: tencentBootstrapEvidence()})
	legacyCoord.SetTencentCLSDetailPort(&fakeTencentDetailPort{result: tencentDetailResult()})
	legacyRun, err := legacyCoord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("legacy Start() error = %v", err)
	}
	if legacyRun.State != domain.RunStateCompletedNonCode {
		t.Fatalf("legacy final state = %s, want completed_non_code", legacyRun.State)
	}
	if !strings.Contains(legacyModel.turns[1].UserMessage, "required_direct_evidence") {
		t.Fatalf("legacy second turn missing mandatory detail correction: %s", legacyModel.turns[1].UserMessage)
	}
	if legacyMetrics.count(application.ResilienceMetricChallenge) != 0 {
		t.Fatalf("legacy run must not emit challenge metrics: %#v", legacyMetrics.events)
	}
}
