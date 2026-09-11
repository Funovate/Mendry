package application

import (
	"strings"
	"testing"
	"time"

	"mendry/backend/internal/modules/remediation/domain"
)

// sampleReviewCheckpoint 构造供 review projection 测试使用的 checkpoint
// snapshot。budget 可选：nil 表示跳过（projection 中 RemainingBudget 为空）。
func sampleReviewCheckpoint(runID string, active bool, reason string, withJournal bool) domain.CheckpointSnapshot {
	checkpoint := domain.WorkingMemoryCheckpointV1{
		SchemaVersion:      domain.CheckpointSchemaVersionV1,
		RunID:              runID,
		SeriesID:           "series-1",
		ContextVersion:     2,
		ObservedRunVersion: 7,
		Phase:              "diagnosing",
		Reason:             reason,
		NextActions:        []string{"inspect repository locator", "re-read evidence"},
	}
	if withJournal {
		checkpoint.Recoveries = []domain.CheckpointRecovery{
			{Kind: string(domain.RecoveryChallengeKindProtocolCorrection), Action: "correct_envelope", OutcomeRef: "challenge:invalid_envelope"},
			{Kind: string(domain.RecoveryChallengeKindToolFailure), Action: "evidence.search", OutcomeRef: "challenge:connector_unavailable"},
			{Kind: string(domain.RecoveryChallengeKindToolFailure), Action: "repository.read_file", OutcomeRef: "challenge:git_unreachable"},
		}
	}
	return domain.CheckpointSnapshot{
		RunID:              runID,
		SeriesID:           "series-1",
		ContextVersion:     2,
		ObservedRunVersion: 7,
		DurableRunVersion:  7,
		Sequence:           4,
		Phase:              "diagnosing",
		ContentHash:        "0123456789abcdef",
		UpdatedAt:          time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC),
		Checkpoint:         checkpoint,
	}
}

// recoveryCheckpointWithEpisodeProgress 给 snapshot 附加新格式的 durable
// recovery-progress 快照（episodeRecoveryAttempts 为当前 episode attempt），
// 供 F3 断言 Attempt 取 episode 而非累计 journal 长度。
func recoveryCheckpointWithEpisodeProgress(snapshot domain.CheckpointSnapshot, episodeAttempts int) domain.CheckpointSnapshot {
	snapshot.Checkpoint.RecoveryProgress = &domain.CheckpointRecoveryProgress{
		RecoveryAttempts:        episodeAttempts,
		EpisodeRecoveryAttempts: episodeAttempts,
	}
	return snapshot
}

// reviewCheckpointWithBudget 返回带有效 soft-budget recovery 的 checkpoint，
// 用于断言 RemainingBudget 投影存在。
func reviewCheckpointWithBudget(t *testing.T, snapshot domain.CheckpointSnapshot) domain.CheckpointSnapshot {
	t.Helper()
	plan := testBudgetPlan(
		t,
		testBudgetCeiling(),
		allPhasesSoft(domain.BudgetAmount{ModelCalls: 10}),
		map[domain.RunState]domain.BudgetAmount{domain.RunStatePlanning: {ModelCalls: 5}},
		domain.BudgetAmount{ModelCalls: 5},
	)
	allocator, err := newPhaseBudgetPlan(plan)
	if err != nil {
		t.Fatalf("newPhaseBudgetPlan: %v", err)
	}
	if err := allocator.AdmitPhase(domain.RunStateDiagnosing); err != nil {
		t.Fatalf("admit diagnosing: %v", err)
	}
	if _, err := allocator.Consume(domain.RunStateDiagnosing, domain.BudgetAmount{ModelCalls: 2}); err != nil {
		t.Fatalf("consume diagnosing: %v", err)
	}
	snapshot.Checkpoint.Budget = allocator.Recovery()
	return snapshot
}

// TestBuildReviewSnapshotsAgentLoopMode 验证 run 创建时快照的 agentLoopMode 与
// policy version 进入 review（D9 审计投影；legacy 缺省仍稳定）。
func TestBuildReviewSnapshotsAgentLoopMode(t *testing.T) {
	agg := domain.RunAggregate{
		Run: domain.Run{
			RunID: "run-1", State: domain.RunStateDiagnosing,
			AgentLoopMode: domain.AgentLoopModeResilientV1, AgentLoopPolicyVersion: 3,
		},
		AttemptSummaries: []domain.AttemptSummary{{ID: "run-1", AttemptNumber: 1}},
	}
	review := buildReview(agg)
	if review.AgentLoopMode != domain.AgentLoopModeResilientV1 || review.AgentLoopPolicyVersion != 3 {
		t.Fatalf("mode snapshot = %q v%d", review.AgentLoopMode, review.AgentLoopPolicyVersion)
	}
	legacy := buildReview(domain.RunAggregate{Run: domain.Run{RunID: "run-2", State: domain.RunStateQueued}})
	if legacy.AgentLoopMode != domain.AgentLoopModeLegacy || legacy.AgentLoopPolicyVersion != 0 {
		t.Fatalf("legacy default = %q v%d", legacy.AgentLoopMode, legacy.AgentLoopPolicyVersion)
	}
}

// TestAttachReviewCheckpointActiveRecoveryProjection 验证活动 resilient run 上
// recovery checkpoint 同时渲染 checkpoint 与 recovery 安全摘要（AC12/R23）：
// reason code 提取时丢弃 challenge hash，attemptedPathClasses 从工具与
// recovery action 派生，nextAction 取 bounded 首条。
func TestAttachReviewCheckpointActiveRecoveryProjection(t *testing.T) {
	review := &Review{
		RunID: "run-1", SeriesID: "series-1", Status: domain.RunStateDiagnosing,
		AgentLoopMode: domain.AgentLoopModeResilientV1,
	}
	snapshot := reviewCheckpointWithBudget(t, sampleReviewCheckpoint("run-1", true, domain.CheckpointReasonRecovery, true))
	attachReviewCheckpoint(review, snapshot)
	if review.Checkpoint == nil {
		t.Fatal("checkpoint projection is missing")
	}
	if review.Checkpoint.Sequence != 4 || review.Checkpoint.Reason != domain.CheckpointReasonRecovery ||
		review.Checkpoint.ObservedRunVersion != 7 || review.Checkpoint.UpdatedAt.IsZero() {
		t.Fatalf("checkpoint projection = %+v", review.Checkpoint)
	}
	if review.Recovery == nil || !review.Recovery.Active {
		t.Fatal("active recovery projection is missing")
	}
	recovery := review.Recovery
	if recovery.Kind != string(domain.RecoveryChallengeKindToolFailure) {
		t.Fatalf("kind = %q", recovery.Kind)
	}
	// 无 progress 快照（旧格式 checkpoint）时 Attempt 回退到 journal 长度。
	if recovery.Attempt != 3 {
		t.Fatalf("attempt = %d", recovery.Attempt)
	}
	// 最新 journal 条目 reason code 是 git_unreachable（不含 hash）；文本保持
	// bounded 且不包含任何 connector 原始错误。
	if recovery.Reason != "git_unreachable" {
		t.Fatalf("reason = %q", recovery.Reason)
	}
	// evidence.search/repository.read_file → provider_evidence/repository；
	// correct_envelope → protocol_correction。按 journal 时间顺序去重，最新在最后。
	got := recovery.AttemptedPathClasses
	if len(got) != 3 || got[0] != "protocol_correction" || got[1] != "provider_evidence" || got[2] != "repository" {
		t.Fatalf("attempted path classes = %v", got)
	}
	if recovery.NextAction != "inspect repository locator" {
		t.Fatalf("next action = %q", recovery.NextAction)
	}
	if recovery.RemainingBudget == nil {
		t.Fatal("remaining budget projection is missing")
	}
	if recovery.RemainingBudget.Phase != domain.RunStateDiagnosing {
		t.Fatalf("budget phase = %q", recovery.RemainingBudget.Phase)
	}
	// 预算纯数值投影：consumed 2 次模型调用、remaining = ceiling - consumed。
	if recovery.RemainingBudget.Consumed.ModelCalls != 2 || recovery.RemainingBudget.Remaining.ModelCalls != 98 {
		t.Fatalf("budget projection consumed/remaining = %d/%d",
			recovery.RemainingBudget.Consumed.ModelCalls, recovery.RemainingBudget.Remaining.ModelCalls)
	}
}

// TestAttachReviewCheckpointTerminalRunHasNoRecovery 验证终态 run 仍可显示
// checkpoint 摘要，但绝不出现在恢复中（R24/AC12）：只有 blocked_manual_review
// 人工修复面板语义存在。
func TestAttachReviewCheckpointTerminalRunHasNoRecovery(t *testing.T) {
	review := &Review{RunID: "run-1", Status: domain.RunStateBlockedManualReview, TerminalReason: "exhaustion_proof"}
	snapshot := sampleReviewCheckpoint("run-1", false, domain.CheckpointReasonRecovery, true)
	attachReviewCheckpoint(review, snapshot)
	if review.Checkpoint == nil {
		t.Fatal("checkpoint projection should remain visible for terminal runs")
	}
	if review.Recovery != nil {
		t.Fatalf("terminal run must not expose an active recovery projection: %+v", review.Recovery)
	}
}

// TestAttachReviewCheckpointNoRecoveryEvidence 验证无 recovery 触发的 checkpoint
// 不渲染 recovery projection；正常活动的 phase checkpoint 不是“恢复中”。
func TestAttachReviewCheckpointNoRecoveryEvidence(t *testing.T) {
	review := &Review{RunID: "run-1", Status: domain.RunStateDiagnosing}
	snapshot := sampleReviewCheckpoint("run-1", true, domain.CheckpointReasonPhaseBoundary, false)
	attachReviewCheckpoint(review, snapshot)
	if review.Checkpoint == nil {
		t.Fatal("checkpoint projection is missing")
	}
	if review.Recovery != nil {
		t.Fatalf("phase-boundary checkpoint must not render recovery: %+v", review.Recovery)
	}
}

// TestAttachReviewCheckpointProgressedPastRecovery 验证 R24 语义：run 已经越过
// recovery 写出后续 phase_boundary checkpoint（如进入 planning）时，即使
// recovery journal 与 no-progress 计数仍在 audit 中也不再渲染“恢复中”。
func TestAttachReviewCheckpointProgressedPastRecovery(t *testing.T) {
	review := &Review{RunID: "run-1", Status: domain.RunStatePlanning}
	snapshot := sampleReviewCheckpoint("run-1", true, domain.CheckpointReasonPhaseBoundary, true)
	progress := domain.CheckpointRecoveryProgress{
		FactCheckAttempts: 2, FactCheckNoProgress: 1, LastFactCheckFingerprint: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	snapshot.Checkpoint.RecoveryProgress = &progress
	attachReviewCheckpoint(review, snapshot)
	if review.Checkpoint == nil {
		t.Fatal("checkpoint projection is missing")
	}
	if review.Recovery != nil {
		t.Fatalf("run progressed past recovery must not render active recovery: %+v", review.Recovery)
	}
}

// TestReviewRecoveryReasonCodeHidesHash 验证 challenge:<code>:<hash> 形式的
// outcome ref 只提取 code，绝不到达 review。
func TestReviewRecoveryReasonCodeHidesHash(t *testing.T) {
	if got := recoveryReasonCode("challenge:validation_failed:abcd1234", "revise_patch"); got != "validation_failed" {
		t.Fatalf("reason code = %q", got)
	}
	if got := recoveryReasonCode("challenge:connector_unavailable", ""); got != "connector_unavailable" {
		t.Fatalf("reason code = %q", got)
	}
	if got := recoveryReasonCode("", "stop"); got != "stop" {
		t.Fatalf("fallback action reason = %q", got)
	}
	if got := recoveryReasonCode("", "model_turn"); got != "" {
		t.Fatalf("model_turn must not become a review reason: %q", got)
	}
}

// TestReviewRecoveryAttemptUsesCurrentEpisodeNotJournalLength 覆盖 F3：D8
// attempt 必须是当前 recovery episode 的持久化 attempt（journal 尾部属于同一
// episode 的 recovery checkpoint 数），而不是跨 episode 累计的 journal 长度。
// 旧格式 checkpoint（无 RecoveryProgress 快照）才回退 journal 长度。
func TestReviewRecoveryAttemptUsesCurrentEpisodeNotJournalLength(t *testing.T) {
	// 三次 recovery 中前两次属于上一个 episode（已由 phase_boundary checkpoint
	// 关闭），最新一次 recovery checkpoint 只记录当前 episode 的第 1 次尝试。
	snapshot := recoveryCheckpointWithEpisodeProgress(
		sampleReviewCheckpoint("run-1", true, domain.CheckpointReasonRecovery, true),
		1, // current episode attempts
	)
	recovery := reviewRecoveryProjection(snapshot)
	if recovery == nil || !recovery.Active {
		t.Fatal("active recovery projection missing")
	}
	// journal 长度 3（跨 episode），但当前 episode attempt 是 1。
	if recovery.Attempt != 1 {
		t.Fatalf("attempt = %d, want current episode attempt 1, not cumulative journal length", recovery.Attempt)
	}

	// episode 内第二次 recovery：journal 与 durable episode 计数同步到 2。
	snapshot2 := recoveryCheckpointWithEpisodeProgress(
		sampleReviewCheckpoint("run-1", true, domain.CheckpointReasonRecovery, true),
		2,
	)
	recovery2 := reviewRecoveryProjection(snapshot2)
	if recovery2 == nil || recovery2.Attempt != 2 {
		t.Fatalf("second episode attempt = %d, want 2", recovery2.Attempt)
	}
}

// TestReviewRecoveryAttemptLegacyCheckpointFallsBackToJournalLength 记录旧格式
// checkpoint（无 RecoveryProgress 快照，pre-change v1）的保守回退：没有 episode
// 边界数据时 review 只能使用 journal 长度近似 attempt。
func TestReviewRecoveryAttemptLegacyCheckpointFallsBackToJournalLength(t *testing.T) {
	snapshot := sampleReviewCheckpoint("run-1", true, domain.CheckpointReasonRecovery, true)
	if snapshot.Checkpoint.RecoveryProgress != nil {
		t.Fatal("sample must not carry a recovery progress snapshot")
	}
	recovery := reviewRecoveryProjection(snapshot)
	if recovery == nil || recovery.Attempt != 3 {
		t.Fatalf("legacy fallback attempt = %d, want journal length 3", recovery.Attempt)
	}
}

// TestAttachReviewCheckpointRequiresMatchingRunID 覆盖 F4：snapshot 必须属于被
// 渲染的 run（snapshot.RunID == review.RunID）；reader 返回其它 run 的快照时
// checkpoint/recovery projection 一律不附加，避免把另一 run 的恢复状态渲染到
// 当前 review。
func TestAttachReviewCheckpointRequiresMatchingRunID(t *testing.T) {
	review := &Review{
		RunID: "run-1", SeriesID: "series-1", Status: domain.RunStateDiagnosing,
		AgentLoopMode: domain.AgentLoopModeResilientV1,
	}
	foreign := recoveryCheckpointWithEpisodeProgress(
		sampleReviewCheckpoint("run-2", true, domain.CheckpointReasonRecovery, true), 1,
	)
	attachReviewCheckpoint(review, foreign)
	if review.Checkpoint != nil || review.Recovery != nil {
		t.Fatalf("foreign snapshot attached projections: checkpoint=%+v recovery=%+v", review.Checkpoint, review.Recovery)
	}

	owned := recoveryCheckpointWithEpisodeProgress(
		sampleReviewCheckpoint("run-1", true, domain.CheckpointReasonRecovery, true), 1,
	)
	attachReviewCheckpoint(review, owned)
	if review.Checkpoint == nil || review.Recovery == nil || review.Recovery.Attempt != 1 {
		t.Fatalf("owned snapshot must attach: checkpoint=%+v recovery=%+v", review.Checkpoint, review.Recovery)
	}
}

// TestCheckpointNoProgressOnlyAtEscalationThreshold 覆盖 F1 的纯谓词矩阵：
// no-progress 标记只在任一连续无进展计数达到/超过该族 escalation threshold
// 时为 true；单次/两次重复（有界 retry）与跨 episode 残留的非当前计数不得
// 触发；lifecycle tool/stop 与 exhaustion 再请求循环同样按各自阈值覆盖。
func TestCheckpointNoProgressOnlyAtEscalationThreshold(t *testing.T) {
	cases := []struct {
		name     string
		progress *domain.CheckpointRecoveryProgress
		want     bool
	}{
		{name: "nil progress", progress: nil, want: false},
		{name: "zero counters", progress: &domain.CheckpointRecoveryProgress{}, want: false},
		{name: "fact check single repeat below threshold", progress: &domain.CheckpointRecoveryProgress{FactCheckNoProgress: 1}, want: false},
		{name: "fact check two repeats below threshold", progress: &domain.CheckpointRecoveryProgress{FactCheckNoProgress: 2}, want: false},
		{name: "fact check at escalation threshold", progress: &domain.CheckpointRecoveryProgress{FactCheckNoProgress: maxConsecutiveProtocolFailures}, want: true},
		{name: "plan feedback below threshold", progress: &domain.CheckpointRecoveryProgress{PlanFeedbackNoProgress: 1}, want: false},
		{name: "plan feedback at escalation threshold", progress: &domain.CheckpointRecoveryProgress{PlanFeedbackNoProgress: maxPlanPolicyFeedbackAttempts}, want: true},
		{name: "validation below threshold", progress: &domain.CheckpointRecoveryProgress{ValidationNoProgress: 2}, want: false},
		{name: "validation at escalation threshold", progress: &domain.CheckpointRecoveryProgress{ValidationNoProgress: maxLifecycleRecoveryAttempts}, want: true},
		{name: "lifecycle tool at escalation threshold", progress: &domain.CheckpointRecoveryProgress{LifecycleRecoveryAttempts: maxLifecycleRecoveryAttempts}, want: true},
		{name: "lifecycle stop at escalation threshold", progress: &domain.CheckpointRecoveryProgress{LifecycleStopAttempts: maxLifecycleRecoveryAttempts}, want: true},
		{name: "single exhaustion request is not a loop", progress: &domain.CheckpointRecoveryProgress{ExhaustionProposalAttempts: 1}, want: false},
		{name: "re-requested exhaustion is a loop", progress: &domain.CheckpointRecoveryProgress{ExhaustionProposalAttempts: 2}, want: true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := checkpointNoProgress(tt.progress); got != tt.want {
				t.Fatalf("checkpointNoProgress(%+v) = %v, want %v", tt.progress, got, tt.want)
			}
		})
	}
}

// TestAdvanceRecoveryEpisodeTracksDurableEpisodeAttempts 覆盖 F3 的持久化边界：
// recovery checkpoint 递增当前 episode attempt；phase boundary/threshold/
// process shutdown checkpoint 关闭 episode 并从 1 重新开始；snapshot/restore
// 往返保留精确计数（restart 恢复依据）。
func TestAdvanceRecoveryEpisodeTracksDurableEpisodeAttempts(t *testing.T) {
	tracker := &resilientRunState{}
	tracker.advanceRecoveryEpisode(domain.CheckpointReasonRecovery)
	tracker.advanceRecoveryEpisode(domain.CheckpointReasonRecovery)
	if tracker.episodeRecoveryAttempts != 2 {
		t.Fatalf("episode attempts after two recoveries = %d, want 2", tracker.episodeRecoveryAttempts)
	}
	// durable 前进关闭 episode。
	tracker.advanceRecoveryEpisode(domain.CheckpointReasonPhaseBoundary)
	if tracker.episodeRecoveryAttempts != 0 {
		t.Fatalf("episode attempts after phase boundary = %d, want 0", tracker.episodeRecoveryAttempts)
	}
	// 新 episode 从 1 重新计数。
	tracker.advanceRecoveryEpisode(domain.CheckpointReasonRecovery)
	if tracker.episodeRecoveryAttempts != 1 {
		t.Fatalf("episode attempts after new recovery = %d, want 1", tracker.episodeRecoveryAttempts)
	}
	snapshot := tracker.recoveryProgressSnapshot()
	if snapshot.EpisodeRecoveryAttempts != 1 {
		t.Fatalf("snapshot episode attempts = %d, want 1", snapshot.EpisodeRecoveryAttempts)
	}
	restored := &resilientRunState{}
	restored.restoreRecoveryProgressSnapshot(*snapshot)
	if restored.episodeRecoveryAttempts != 1 {
		t.Fatalf("restored episode attempts = %d, want 1", restored.episodeRecoveryAttempts)
	}
}

// TestResetDiagnosisNoProgressClearsEscalatedStreaksAfterConvergence 覆盖 F1 的
// 收敛复位：只有 diagnosing 的 fact-check/exhaustion 连续计数被清空，其它族
// （validation/lifecycle/plan）不受影响，避免一次已结算 loop 长期粘滞到后续
// checkpoint。
func TestResetDiagnosisNoProgressClearsEscalatedStreaksAfterConvergence(t *testing.T) {
	tracker := &resilientRunState{
		factCheckAttempts: 3, factCheckNoProgress: 3, lastFactCheckFingerprint: strings.Repeat("a", 64),
		exhaustionProposalAttempts: 2,
		planFeedbackNoProgress:     1, planFeedbackAttempts: 2,
		validationNoProgress: 3, lastValidationFingerprint: strings.Repeat("b", 64),
		lifecycleRecoveryAttempts: 3, lastLifecycleRecoveryFingerprint: strings.Repeat("c", 64), lastLifecycleRecoveryClass: "tool",
	}
	tracker.resetDiagnosisNoProgress()
	if tracker.factCheckNoProgress != 0 || tracker.lastFactCheckFingerprint != "" || tracker.exhaustionProposalAttempts != 0 {
		t.Fatalf("diagnosis no-progress was not cleared: %#v", tracker)
	}
	if tracker.factCheckAttempts != 3 || tracker.planFeedbackNoProgress != 1 || tracker.validationNoProgress != 3 ||
		tracker.lifecycleRecoveryAttempts != 3 || tracker.planFeedbackAttempts != 2 {
		t.Fatalf("unrelated recovery progress changed: %#v", tracker)
	}
}
