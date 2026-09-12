package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	authdomain "mendry/backend/internal/modules/auth/domain"
	projectdomain "mendry/backend/internal/modules/projects/domain"
	"mendry/backend/internal/modules/remediation/application"
	"mendry/backend/internal/modules/remediation/domain"
)

// fakeCheckpointReader 记录读取次数并返回固定 snapshot/error，供 review 的
// optional checkpoint projection 测试使用。
type fakeCheckpointReader struct {
	snapshot domain.CheckpointSnapshot
	err      error
	calls    int
}

func (f *fakeCheckpointReader) LoadLatestCheckpoint(_ context.Context, runID string) (domain.CheckpointSnapshot, error) {
	f.calls++
	if f.err != nil {
		return domain.CheckpointSnapshot{}, f.err
	}
	return f.snapshot, nil
}

func recoveryCheckpointSnapshot(runID string) domain.CheckpointSnapshot {
	return domain.CheckpointSnapshot{
		RunID:              runID,
		SeriesID:           "series-1",
		ContextVersion:     2,
		ObservedRunVersion: 7,
		DurableRunVersion:  7,
		Sequence:           5,
		Phase:              "diagnosing",
		ContentHash:        "0123456789abcdef",
		UpdatedAt:          time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC),
		Checkpoint: domain.WorkingMemoryCheckpointV1{
			SchemaVersion:      domain.CheckpointSchemaVersionV1,
			RunID:              runID,
			SeriesID:           "series-1",
			ContextVersion:     2,
			ObservedRunVersion: 7,
			Phase:              "diagnosing",
			Reason:             domain.CheckpointReasonRecovery,
			NextActions:        []string{"inspect the exact deployed source"},
			Recoveries: []domain.CheckpointRecovery{
				{Kind: string(domain.RecoveryChallengeKindToolFailure), Action: "ssh.inspect", OutcomeRef: "challenge:connector_timeout"},
			},
			RecoveryProgress: &domain.CheckpointRecoveryProgress{
				RecoveryAttempts: 1, EpisodeRecoveryAttempts: 1,
			},
		},
	}
}

func reviewServiceWithCheckpoints(t *testing.T, reviews application.ReviewQuery, checkpoints application.CheckpointReviewReader) *application.Service {
	t.Helper()
	service, err := application.NewService(application.ServiceOptions{
		Projects: &fakeProjectAccess{project: projectdomain.Project{ID: testProjectID}},
		Incidents: &fakeIncidentLookup{identity: application.IncidentIdentity{
			ID: testIncidentUUID, ProjectID: testProjectID, LifecycleGeneration: 1, DeployedCommit: "abc123", ContextVersion: 9,
		}},
		Trigger:     &fakeTriggerStarter{},
		Reviews:     reviews,
		Checkpoints: checkpoints,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

// TestGetRemediationAttachesRecoveryProjectionForResilientRun 覆盖 D8/AC12：活动
// resilient_v1 run 的 GET review 附加 checkpoint 摘要与 active recovery 摘要
// （kind/attempt/attemptedPathClasses/nextAction），且不渲染原始模型内容。
func TestGetRemediationAttachesRecoveryProjectionForResilientRun(t *testing.T) {
	reviews := &fakeReviewQuery{agg: domain.RunAggregate{
		Run: domain.Run{
			RunID: "run-1", SeriesID: "series-1", IncidentID: testIncidentUUID,
			State: domain.RunStateDiagnosing, LifecycleGeneration: 1, DeployedCommit: "abc123",
			AttemptNumber: 1, Version: 7, AgentLoopMode: domain.AgentLoopModeResilientV1,
		},
	}}
	checkpoints := &fakeCheckpointReader{snapshot: recoveryCheckpointSnapshot("run-1")}
	service := reviewServiceWithCheckpoints(t, reviews, checkpoints)
	review, err := service.GetRemediation(context.Background(), authdomain.User{ID: "operator"}, "payments", "INC-2049")
	if err != nil {
		t.Fatalf("GetRemediation() error = %v", err)
	}
	if review.Checkpoint == nil || review.Checkpoint.Sequence != 5 {
		t.Fatalf("checkpoint projection = %+v", review.Checkpoint)
	}
	if review.Recovery == nil || !review.Recovery.Active {
		t.Fatal("active recovery projection missing")
	}
	if review.Recovery.Kind != string(domain.RecoveryChallengeKindToolFailure) || review.Recovery.Attempt != 1 {
		t.Fatalf("recovery = %+v", review.Recovery)
	}
	if len(review.Recovery.AttemptedPathClasses) != 1 || review.Recovery.AttemptedPathClasses[0] != "ssh_inspect" {
		t.Fatalf("attempted path classes = %v", review.Recovery.AttemptedPathClasses)
	}
	if review.Recovery.NextAction != "inspect the exact deployed source" {
		t.Fatalf("next action = %q", review.Recovery.NextAction)
	}
	if review.AgentLoopMode != domain.AgentLoopModeResilientV1 {
		t.Fatalf("mode = %q", review.AgentLoopMode)
	}
}

// TestGetRemediationTerminalResilientRunHasCheckpointWithoutRecovery 覆盖
// R24/AC12：resilient run 进入诊断就绪/人工评审终态后只显示 checkpoint 摘要，
// 不显示“recovering”（人工修复面板语义保留给 blocked_manual_review）。
func TestGetRemediationTerminalResilientRunHasCheckpointWithoutRecovery(t *testing.T) {
	reviews := &fakeReviewQuery{agg: domain.RunAggregate{
		Run: domain.Run{
			RunID: "run-1", SeriesID: "series-1", IncidentID: testIncidentUUID,
			State: domain.RunStateBlockedManualReview, LifecycleGeneration: 1, DeployedCommit: "abc123",
			AttemptNumber: 1, Version: 7, AgentLoopMode: domain.AgentLoopModeResilientV1,
			TerminalReason: "exhaustion_proof",
		},
	}}
	checkpoints := &fakeCheckpointReader{snapshot: recoveryCheckpointSnapshot("run-1")}
	service := reviewServiceWithCheckpoints(t, reviews, checkpoints)
	review, err := service.GetRemediation(context.Background(), authdomain.User{ID: "operator"}, "payments", "INC-2049")
	if err != nil {
		t.Fatalf("GetRemediation() error = %v", err)
	}
	if review.Checkpoint == nil {
		t.Fatal("checkpoint projection missing on terminal run")
	}
	if review.Recovery != nil {
		t.Fatalf("terminal run must not report active recovery: %+v", review.Recovery)
	}
}

// TestGetRemediationLegacyRunSkipsCheckpointReader 覆盖 D9/AC13 兼容性：legacy
// run 即使注入了 checkpoint reader 也不读取，projection 保持 nil。
func TestGetRemediationLegacyRunSkipsCheckpointReader(t *testing.T) {
	reviews := &fakeReviewQuery{agg: domain.RunAggregate{
		Run: domain.Run{
			RunID: "run-1", SeriesID: "series-1", IncidentID: testIncidentUUID,
			State: domain.RunStateDiagnosing, LifecycleGeneration: 1, DeployedCommit: "abc123",
			AttemptNumber: 1, Version: 3, AgentLoopMode: domain.AgentLoopModeLegacy,
		},
	}}
	checkpoints := &fakeCheckpointReader{snapshot: recoveryCheckpointSnapshot("run-1")}
	service := reviewServiceWithCheckpoints(t, reviews, checkpoints)
	review, err := service.GetRemediation(context.Background(), authdomain.User{ID: "operator"}, "payments", "INC-2049")
	if err != nil {
		t.Fatalf("GetRemediation() error = %v", err)
	}
	if checkpoints.calls != 0 {
		t.Fatalf("legacy run triggered %d checkpoint reads", checkpoints.calls)
	}
	if review.Checkpoint != nil || review.Recovery != nil {
		t.Fatalf("legacy run must omit projections: checkpoint=%+v recovery=%+v", review.Checkpoint, review.Recovery)
	}
}

// TestGetRemediationCheckpointLoadErrorDegradesToNoProjection 覆盖 R23：checkpoint
// 读取失败只降级为不渲染 projection，review GET 仍然成功（可用性优先）。
func TestGetRemediationCheckpointLoadErrorDegradesToNoProjection(t *testing.T) {
	reviews := &fakeReviewQuery{agg: domain.RunAggregate{
		Run: domain.Run{
			RunID: "run-1", SeriesID: "series-1", IncidentID: testIncidentUUID,
			State: domain.RunStateDiagnosing, LifecycleGeneration: 1, DeployedCommit: "abc123",
			AttemptNumber: 1, Version: 7, AgentLoopMode: domain.AgentLoopModeResilientV1,
		},
	}}
	checkpoints := &fakeCheckpointReader{err: errors.New("checkpoint store unavailable")}
	service := reviewServiceWithCheckpoints(t, reviews, checkpoints)
	review, err := service.GetRemediation(context.Background(), authdomain.User{ID: "operator"}, "payments", "INC-2049")
	if err != nil {
		t.Fatalf("GetRemediation() error = %v", err)
	}
	if review.Checkpoint != nil || review.Recovery != nil {
		t.Fatalf("failed checkpoint load must omit projections: %+v %+v", review.Checkpoint, review.Recovery)
	}
	if review.RunID != "run-1" {
		t.Fatalf("review = %+v", review)
	}
}

// TestGetRemediationRecoveryAttemptReflectsCurrentEpisode 覆盖 F3 的服务层投影：
// 即使 checkpoint journal 保留了先前 episode 的条目，Attempt 也只反映当前
// recovery episode 的持久化 attempt 计数（而非累计 journal 长度）。
func TestGetRemediationRecoveryAttemptReflectsCurrentEpisode(t *testing.T) {
	snapshot := recoveryCheckpointSnapshot("run-1")
	snapshot.Checkpoint.Recoveries = []domain.CheckpointRecovery{
		{Kind: string(domain.RecoveryChallengeKindToolFailure), Action: "ssh.inspect", OutcomeRef: "challenge:connector_timeout"},
		{Kind: string(domain.RecoveryChallengeKindToolFailure), Action: "docker.logs", OutcomeRef: "challenge:connector_timeout"},
		{Kind: string(domain.RecoveryChallengeKindToolFailure), Action: "repository.read_file", OutcomeRef: "challenge:git_unreachable"},
	}
	snapshot.Checkpoint.RecoveryProgress = &domain.CheckpointRecoveryProgress{
		RecoveryAttempts: 3, EpisodeRecoveryAttempts: 1,
	}
	reviews := &fakeReviewQuery{agg: domain.RunAggregate{
		Run: domain.Run{
			RunID: "run-1", SeriesID: "series-1", IncidentID: testIncidentUUID,
			State: domain.RunStateDiagnosing, LifecycleGeneration: 1, DeployedCommit: "abc123",
			AttemptNumber: 1, Version: 7, AgentLoopMode: domain.AgentLoopModeResilientV1,
		},
	}}
	service := reviewServiceWithCheckpoints(t, reviews, &fakeCheckpointReader{snapshot: snapshot})
	review, err := service.GetRemediation(context.Background(), authdomain.User{ID: "operator"}, "payments", "INC-2049")
	if err != nil {
		t.Fatalf("GetRemediation() error = %v", err)
	}
	if review.Recovery == nil || !review.Recovery.Active {
		t.Fatal("active recovery projection missing")
	}
	// journal 长度 3（含先前 episode 条目），当前 episode attempt 只有 1。
	if review.Recovery.Attempt != 1 {
		t.Fatalf("recovery attempt = %d, want current episode attempt 1", review.Recovery.Attempt)
	}
}

// TestGetRemediationRejectsForeignRunCheckpointSnapshot 覆盖 F4 服务层：checkpoint
// reader 返回不属于该 run 的 snapshot 时，review 不附加 checkpoint/recovery
// projection，且 GET 仍然成功（可用性优先，不渲染其它 run 的恢复状态）。
func TestGetRemediationRejectsForeignRunCheckpointSnapshot(t *testing.T) {
	reviews := &fakeReviewQuery{agg: domain.RunAggregate{
		Run: domain.Run{
			RunID: "run-1", SeriesID: "series-1", IncidentID: testIncidentUUID,
			State: domain.RunStateDiagnosing, LifecycleGeneration: 1, DeployedCommit: "abc123",
			AttemptNumber: 1, Version: 7, AgentLoopMode: domain.AgentLoopModeResilientV1,
		},
	}}
	checkpoints := &fakeCheckpointReader{snapshot: recoveryCheckpointSnapshot("other-run")}
	service := reviewServiceWithCheckpoints(t, reviews, checkpoints)
	review, err := service.GetRemediation(context.Background(), authdomain.User{ID: "operator"}, "payments", "INC-2049")
	if err != nil {
		t.Fatalf("GetRemediation() error = %v", err)
	}
	if review.Checkpoint != nil || review.Recovery != nil {
		t.Fatalf("foreign snapshot must not attach projections: checkpoint=%+v recovery=%+v", review.Checkpoint, review.Recovery)
	}
	if review.RunID != "run-1" || review.Status != domain.RunStateDiagnosing {
		t.Fatalf("review degraded: %+v", review)
	}
}
