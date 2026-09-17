package application_test

import (
	"context"
	"strings"
	"testing"

	"mendry/backend/internal/modules/remediation/application"
	"mendry/backend/internal/modules/remediation/domain"
)

// thresholdBigRepoPort 返回足以让单条工具观察饱和其 64 KiB 输出上限的仓库
// 文件内容：encoded observation 会被 boundedConversationValue 截断到接近
// maxObservationBytes，从而越过 T2 单次完整工具观察的输出压力线（48 KiB）。
type thresholdBigRepoPort struct {
	fakeRepoPort
}

func (f *thresholdBigRepoPort) ReadFile(_ context.Context, ref domain.RepoRef, path string, _ domain.ReadOptions) (domain.FileContent, error) {
	f.calls++
	f.lastRef = ref
	return domain.FileContent{Path: path, Content: []byte(strings.Repeat("q", 200<<10))}, nil
}

// thresholdSmallRepoPort 返回小型文件内容，单条观察远低于输出压力线。
type thresholdSmallRepoPort struct {
	fakeRepoPort
}

func (f *thresholdSmallRepoPort) ReadFile(_ context.Context, ref domain.RepoRef, path string, _ domain.ReadOptions) (domain.FileContent, error) {
	f.calls++
	f.lastRef = ref
	return domain.FileContent{Path: path, Content: []byte(strings.Repeat("r", 2<<10))}, nil
}

// TestResilientThresholdCheckpoint_OversizedObservationFiresOutputPressure
// 证明 D2/R13 自动 byte-threshold 的 T2 触发：单条 bounded 完整工具观察越过
// 输出压力线后，下一轮 provider 调用前自动追加一次 reason=threshold 的 durable
// checkpoint（complete tool request/response group 已落盘），并发出 reason=
// threshold 的 checkpoint 指标；随后 run 正常终态，无额外 spam。
func TestResilientThresholdCheckpoint_OversizedObservationFiresOutputPressure(t *testing.T) {
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	checkpoints := &fakeCheckpointStore{runStore: store}
	metrics := &recordingResilienceMetrics{}
	model := &scriptedModel{responses: []string{
		requestToolEnvelope("repository.read_file", "main.go"),
		diagnosisEnvelope("unsafe_to_automate"),
	}}
	coord := newCoordinator(store, &thresholdBigRepoPort{}, &fakeEvidencePort{}, model)
	coord.SetCheckpointStore(checkpoints)
	coord.SetResilienceMetricObserver(metrics)

	if _, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if store.state != domain.RunStateBlockedManualReview || model.calls != 2 {
		t.Fatalf("final state/model calls = %s/%d, want blocked_manual_review/2", store.state, model.calls)
	}
	// 边界(#1) + threshold(#2) + 终态前(#3)：恰好一次自动触发，没有 spam。
	if len(checkpoints.appends) != 3 {
		t.Fatalf("checkpoints appended = %d, want 3 (boundary + threshold + terminal)", len(checkpoints.appends))
	}
	if got := checkpoints.countReason(domain.CheckpointReasonThreshold); got != 1 {
		t.Fatalf("threshold checkpoints = %d, want 1", got)
	}
	if got := checkpoints.countReason(domain.CheckpointReasonRecovery); got != 0 {
		t.Fatalf("recovery checkpoints = %d, want 0", got)
	}
	threshold := checkpoints.appends[1]
	if threshold.Reason != domain.CheckpointReasonThreshold || threshold.Phase != string(domain.RunStateDiagnosing) {
		t.Fatalf("threshold checkpoint reason/phase = %q/%q, want threshold/diagnosing", threshold.Reason, threshold.Phase)
	}
	if threshold.RunID != store.created.RunID || threshold.ObservedRunVersion < 1 {
		t.Fatalf("threshold checkpoint identity/version = run %q version %d", threshold.RunID, threshold.ObservedRunVersion)
	}
	// 指标：checkpoint 事件携带 reason=threshold（既有 allowlist 口径）。
	var thresholdMetric *application.ResilienceMetric
	for _, event := range metrics.events {
		if event.Kind == application.ResilienceMetricCheckpoint && event.Reason == domain.CheckpointReasonThreshold {
			thresholdMetric = &event
			break
		}
	}
	if thresholdMetric == nil {
		t.Fatalf("no checkpoint metric with reason threshold: %#v", metrics.events)
	}
	if thresholdMetric.Phase != domain.RunStateDiagnosing {
		t.Fatalf("threshold metric phase = %q, want diagnosing", thresholdMetric.Phase)
	}
}

// TestResilientThresholdCheckpoint_SmallReadsNeverFire 证明低于阈值的小观察
// 不会触发任何自动 byte-threshold checkpoint（T2 观察线未到、累计字节未达
// T1 context generation 级别）。run 只保留既有 forced checkpoint；第 3 次
// diagnosing 模型调用越过的 soft-budget recovery checkpoint 属于既有 D7 语义
// （recovery 触发），不是 byte-threshold。
func TestResilientThresholdCheckpoint_SmallReadsNeverFire(t *testing.T) {
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	checkpoints := &fakeCheckpointStore{runStore: store}
	model := &scriptedModel{responses: []string{
		requestToolEnvelope("repository.read_file", "main.go"),
		requestToolEnvelope("repository.read_file", "other.go"),
		diagnosisEnvelope("unsafe_to_automate"),
	}}
	coord := newCoordinator(store, &thresholdSmallRepoPort{}, &fakeEvidencePort{}, model)
	coord.SetCheckpointStore(checkpoints)

	if _, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if store.state != domain.RunStateBlockedManualReview || model.calls != 3 {
		t.Fatalf("final state/model calls = %s/%d, want blocked_manual_review/3", store.state, model.calls)
	}
	if got := checkpoints.countReason(domain.CheckpointReasonThreshold); got != 0 {
		t.Fatalf("threshold checkpoints = %d, want 0", got)
	}
	// 边界(#1) + 终态前(#2)：3 次 diagnosing 模型调用仍在默认 soft allocation
	// 内，不产生 soft-budget recovery checkpoint，更没有自动 byte-threshold。
	if len(checkpoints.appends) != 2 {
		t.Fatalf("checkpoints appended = %d, want 2 (boundary + terminal)", len(checkpoints.appends))
	}
}

// TestResilientThresholdCheckpoint_NoGrowthNeverSpams 证明 anti-spam 语义：
// 一次大观察触发自动 checkpoint 后，后续不足一个输出压力量子的内容增长
// （小观察 + 每轮 provider 输入）不会重复触发；run 全程只有一次 threshold
// checkpoint。
func TestResilientThresholdCheckpoint_NoGrowthNeverSpams(t *testing.T) {
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	checkpoints := &fakeCheckpointStore{runStore: store}
	model := &scriptedModel{responses: []string{
		requestToolEnvelope("repository.read_file", "big.go"),
		requestToolEnvelope("repository.read_file", "small.go"),
		diagnosisEnvelope("unsafe_to_automate"),
	}}
	coord := newCoordinator(store, &thresholdMixedRepoPort{}, &fakeEvidencePort{}, model)
	coord.SetCheckpointStore(checkpoints)

	if _, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if store.state != domain.RunStateBlockedManualReview || model.calls != 3 {
		t.Fatalf("final state/model calls = %s/%d", store.state, model.calls)
	}
	if got := checkpoints.countReason(domain.CheckpointReasonThreshold); got != 1 {
		t.Fatalf("threshold checkpoints = %d, want exactly 1 (no spam without growth)", got)
	}
}

// thresholdMixedRepoPort 按 path 返回大/小内容，供 no-growth 场景使用。
type thresholdMixedRepoPort struct {
	fakeRepoPort
}

func (f *thresholdMixedRepoPort) ReadFile(_ context.Context, ref domain.RepoRef, path string, _ domain.ReadOptions) (domain.FileContent, error) {
	f.calls++
	f.lastRef = ref
	size := 2 << 10
	if path == "big.go" {
		size = 200 << 10
	}
	return domain.FileContent{Path: path, Content: []byte(strings.Repeat("m", size))}, nil
}

// TestResilientThresholdCheckpoint_LegacyModeIsByteForByteNoOp 证明 legacy
// run（默认 agentLoopMode）即使注入 checkpoint store 且产生超大工具观察，
// 也绝不 append 任何 checkpoint（自动 byte-threshold 是 resilient_v1 专属）。
func TestResilientThresholdCheckpoint_LegacyModeIsByteForByteNoOp(t *testing.T) {
	store := newFakeRunStore()
	checkpoints := &fakeCheckpointStore{runStore: store}
	model := &scriptedModel{responses: []string{
		requestToolEnvelope("repository.read_file", "main.go"),
		diagnosisEnvelope("unsafe_to_automate"),
	}}
	coord := newCoordinator(store, &thresholdBigRepoPort{}, &fakeEvidencePort{}, model)
	coord.SetCheckpointStore(checkpoints)

	if _, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if store.state != domain.RunStateBlockedManualReview || model.calls != 2 {
		t.Fatalf("final state/model calls = %s/%d", store.state, model.calls)
	}
	if len(checkpoints.appends) != 0 {
		t.Fatalf("legacy run appended %d checkpoints, want 0", len(checkpoints.appends))
	}
}

// TestResilientThresholdCheckpoint_AppendFailureIsPersistenceTerminal 证明自动
// threshold checkpoint 失败按既有 contract 是 persistence blocker：run 以
// failed/persistence_failure 终态收场，不会继续模型决策路径。
func TestResilientThresholdCheckpoint_AppendFailureIsPersistenceTerminal(t *testing.T) {
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	checkpoints := &fakeCheckpointStore{
		runStore:    store,
		appendErr:   errThresholdAppend,
		appendErrAt: 2, // #1 = diagnosing 边界；#2 = 大观察后的自动 threshold append。
	}
	model := &scriptedModel{responses: []string{
		requestToolEnvelope("repository.read_file", "main.go"),
		diagnosisEnvelope("unsafe_to_automate"),
	}}
	coord := newCoordinator(store, &thresholdBigRepoPort{}, &fakeEvidencePort{}, model)
	coord.SetCheckpointStore(checkpoints)

	if _, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	}); err == nil {
		t.Fatal("Start() succeeded despite threshold checkpoint append failure")
	}
	if store.state != domain.RunStateFailed {
		t.Fatalf("final state = %s, want failed", store.state)
	}
	if len(store.effects) == 0 || store.effects[len(store.effects)-1].TerminalReason != "persistence_failure" {
		t.Fatalf("terminal effect = %#v, want persistence_failure", store.effects[len(store.effects)-1])
	}
	if model.calls != 1 {
		t.Fatalf("model calls = %d, want 1 (no re-prompt after persistence blocker)", model.calls)
	}
	if len(checkpoints.appends) != 1 {
		t.Fatalf("checkpoints appended = %d, want only the pre-failure boundary append", len(checkpoints.appends))
	}
}

// TestResilientThresholdCheckpoint_TwoDistinctLargeObservationsFireTwice 证明
// Defect-2 修正的 coordinator 层语义：两条各自独立的大工具观察（separate
// turns）在每条都被前一次自动 checkpoint 消费后仍各触发一次 T2（“two fires
// when growth allows”）；已覆盖的观察不会因后续增长再次触发，因此全程恰好两次
// reason=threshold。
func TestResilientThresholdCheckpoint_TwoDistinctLargeObservationsFireTwice(t *testing.T) {
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	checkpoints := &fakeCheckpointStore{runStore: store}
	model := &scriptedModel{responses: []string{
		requestToolEnvelope("repository.read_file", "big1.go"),
		requestToolEnvelope("repository.read_file", "big2.go"),
		diagnosisEnvelope("unsafe_to_automate"),
	}}
	coord := newCoordinator(store, &thresholdBigRepoPort{}, &fakeEvidencePort{}, model)
	coord.SetCheckpointStore(checkpoints)

	if _, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if store.state != domain.RunStateBlockedManualReview || model.calls != 3 {
		t.Fatalf("final state/model calls = %s/%d, want blocked_manual_review/3", store.state, model.calls)
	}
	// 边界(#1) + 两条大观察各自触发一次 threshold(#2/#3) + 终态前(#4)。
	if got := checkpoints.countReason(domain.CheckpointReasonThreshold); got != 2 {
		t.Fatalf("threshold checkpoints = %d, want 2 (one per distinct large observation)", got)
	}
	if got := checkpoints.countReason(domain.CheckpointReasonRecovery); got != 0 {
		t.Fatalf("recovery checkpoints = %d, want 0", got)
	}
	if len(checkpoints.appends) != 4 {
		t.Fatalf("checkpoints appended = %d, want 4 (boundary + two thresholds + terminal)", len(checkpoints.appends))
	}
}

// errThresholdAppend 是自动 threshold checkpoint 持久化失败的注入错误。
var errThresholdAppend = contextErrorString("threshold append failed")

type contextErrorString string

func (e contextErrorString) Error() string { return string(e) }
