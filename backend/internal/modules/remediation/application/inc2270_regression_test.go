package application_test

// INC-2270 回归矩阵（Phase 2 item 6 / AC1-AC8）。
//
// 本文件是纯增量验证测试：不修改任何生产代码或既有测试，只把 AC1-AC8 的
// 核心可观察行为映射到命名的 coordinator/application 级测试，并接线
// research/inc-2270-replay.json 的 two canonical replay contracts：
//
//   - attempt 1（silent gate rewrite）：classification correction 必须返回
//     structured challenge 且保留 agent 的 causal/fixability proposal 直到
//     重新评估（AC1/AC5）；
//   - attempt 2（provider-detail loss）：same-series provider detail 必须保持
//     被索引并可由 evidence.read 重读，不跨 lifecycle generation / deployed
//     commit（AC2）。
//
// 既有覆盖被直接引用而不是复制：AC1/AC5 的
// TestCoordinator_EvidenceCorrectionChallengeThenPlanning、AC2 的
// TestCoordinatorContinueRendersSameSeriesEvidenceIndex 与 evidence_read_test.go、
// AC3 的 Docker refinement / SSH inspect 测试、AC4 的 connector 失败测试、AC7 的
// TestResilient_ContinuationReconstructsFromDurableCheckpoint、AC8 的
// TestCoordinator_ResilientIncompleteExhaustionProposalChallengesAndRetries。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"mendry/backend/internal/modules/remediation/application"
	"mendry/backend/internal/modules/remediation/domain"
)

// --- INC-2270 replay fixture wiring -----------------------------------------

// inc2270Attempt 是 research/inc-2270-replay.json 中单个 attempt 的有界投影。
// 各节都是指针：不同 attempt 携带不同的 bootstrap/result 结构。
type inc2270Attempt struct {
	Attempt          int `json:"attempt"`
	TrustedBootstrap *struct {
		ProviderDetailPresent bool     `json:"providerDetailPresent"`
		ApproximateBytes      int64    `json:"approximateBytes"`
		Locators              []string `json:"locators"`
	} `json:"trustedBootstrap"`
	ContinuationBootstrap *struct {
		ProviderDetailPresent  bool  `json:"providerDetailPresent"`
		RuntimeEvidencePresent bool  `json:"runtimeEvidencePresent"`
		ApproximateBytes       int64 `json:"approximateBytes"`
	} `json:"continuationBootstrap"`
	AgentResult *struct {
		Fixability      string   `json:"fixability"`
		ConfidenceBand  string   `json:"confidenceBand"`
		CausalClosure   bool     `json:"causalClosure"`
		ReportedMissing []string `json:"reportedMissing"`
	} `json:"agentResult"`
	ServiceResult *struct {
		Fixability      string `json:"fixability"`
		Cause           string `json:"cause"`
		EnteredPlanning *bool  `json:"enteredPlanning"`
	} `json:"serviceResult"`
}

// inc2270ReplayFixture 是 fixture 文件的顶层结构。requiredRegression 是
// pre-change replay 必须最终满足的两个 canonical 契约。
type inc2270ReplayFixture struct {
	SchemaVersion      string           `json:"schemaVersion"`
	ScenarioID         string           `json:"scenarioId"`
	Sanitized          bool             `json:"sanitized"`
	Attempts           []inc2270Attempt `json:"attempts"`
	RequiredRegression struct {
		Attempt1 string `json:"attempt1"`
		Attempt2 string `json:"attempt2"`
	} `json:"requiredRegression"`
}

// inc2270FixtureCandidates 是 fixture 相对测试包工作目录的路径。
var inc2270FixtureCandidates = []string{
	"testdata/inc-2270-replay.json",
}

// loadINC2270ReplayFixture 加载并断言 INC-2270 pre-change replay fixture。
// 文件缺失时 skip 而不是 fail；找到但契约不符时
// 直接 fail，防止 fixture 被误改后静默失效。
func loadINC2270ReplayFixture(t *testing.T) *inc2270ReplayFixture {
	t.Helper()
	for _, candidate := range inc2270FixtureCandidates {
		data, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		var fixture inc2270ReplayFixture
		if err := json.Unmarshal(data, &fixture); err != nil {
			t.Fatalf("parse INC-2270 replay fixture %s: %v", candidate, err)
		}
		assertINC2270CanonicalContracts(t, fixture)
		return &fixture
	}
	t.Skip("INC-2270 replay fixture not found under the test working directory; skipping fixture-wiring regression")
	return nil
}

// assertINC2270CanonicalContracts 断言 fixture 编码了 pre-change 的两个失败
// 形态，且 requiredRegression 明确要求修复后的两个 canonical 契约：
//   - attempt 1：silent gate rewrite（serviceResult 把 code_fixable 改成
//     insufficient_evidence 且未进入 planning）→ 修复要求 structured
//     challenge + 保留 proposal（AC1/AC5）；
//   - attempt 2：provider-detail loss（continuation 只有 runtime evidence，
//     无 provider detail）→ 修复要求 same-series evidence.read 重读（AC2）。
func assertINC2270CanonicalContracts(t *testing.T, fixture inc2270ReplayFixture) {
	t.Helper()
	if fixture.SchemaVersion != "v1" || fixture.ScenarioID != "INC-2270" || !fixture.Sanitized {
		t.Fatalf("fixture identity = schema %q scenario %q sanitized %t, want sanitized v1 INC-2270 replay",
			fixture.SchemaVersion, fixture.ScenarioID, fixture.Sanitized)
	}
	if len(fixture.Attempts) != 2 {
		t.Fatalf("fixture attempts = %d, want 2", len(fixture.Attempts))
	}

	// attempt 1：documented silent gate rewrite。
	attempt1 := fixture.Attempts[0]
	if attempt1.ServiceResult == nil || attempt1.ServiceResult.Fixability != "insufficient_evidence" {
		t.Fatalf("attempt 1 serviceResult = %#v, want the documented silent rewrite to insufficient_evidence", attempt1.ServiceResult)
	}
	if attempt1.ServiceResult.EnteredPlanning == nil || *attempt1.ServiceResult.EnteredPlanning {
		t.Fatalf("attempt 1 must document that the silent rewrite never entered planning")
	}
	if !strings.Contains(strings.ToLower(attempt1.ServiceResult.Cause), "citation classification mismatch") {
		t.Fatalf("attempt 1 cause = %q, want the citation classification mismatch root cause", attempt1.ServiceResult.Cause)
	}
	// 修复契约 1：structured challenge + 保留 causal/fixability proposal。
	for _, want := range []string{"structured challenge", "preserves"} {
		if !strings.Contains(strings.ToLower(fixture.RequiredRegression.Attempt1), want) {
			t.Fatalf("requiredRegression.attempt1 missing %q: %q", want, fixture.RequiredRegression.Attempt1)
		}
	}

	// attempt 2：documented provider-detail loss on continuation。
	attempt2 := fixture.Attempts[1]
	if attempt2.ContinuationBootstrap == nil || attempt2.ContinuationBootstrap.ProviderDetailPresent {
		t.Fatalf("attempt 2 continuationBootstrap = %#v, want providerDetailPresent=false (provider-detail loss)", attempt2.ContinuationBootstrap)
	}
	if attempt2.AgentResult == nil || len(attempt2.AgentResult.ReportedMissing) == 0 {
		t.Fatalf("attempt 2 agentResult = %#v, want the reported rawError/stack/timeEvidence gap", attempt2.AgentResult)
	}
	// 修复契约 2：same-series evidence.read 重读，不跨 generation/commit。
	for _, want := range []string{"evidence.read", "same-series"} {
		if !strings.Contains(strings.ToLower(fixture.RequiredRegression.Attempt2), want) {
			t.Fatalf("requiredRegression.attempt2 missing %q: %q", want, fixture.RequiredRegression.Attempt2)
		}
	}
}

// TestINC2270Regression_FixtureLoaded 加载 pre-change replay fixture 并把两个
// canonical 契约显式映射到 AC1/AC5（attempt 1）与 AC2（attempt 2）。fixture
// 缺失时 skip；契约不符时 fail。
func TestINC2270Regression_FixtureLoaded(t *testing.T) {
	fixture := loadINC2270ReplayFixture(t)
	if fixture == nil {
		return
	}
	// attempt 1 的 trusted locators 必须包含 stack/sourcePath/line/rawTime，
	// 即 AC1 首轮必须携带的 bounded 原始定位信息。
	locators := fixture.Attempts[0].TrustedBootstrap
	if locators == nil || !locators.ProviderDetailPresent {
		t.Fatalf("attempt 1 trustedBootstrap = %#v, want provider detail present with locators", locators)
	}
	for _, want := range []string{"error", "stack", "sourcePath", "line", "rawTime"} {
		found := false
		for _, locator := range locators.Locators {
			if locator == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("attempt 1 trusted locators %v missing %q", locators.Locators, want)
		}
	}
	// attempt 2 的缺口必须与修复契约一致：provider detail 缺失导致模型报告
	// rawError/stack/timeEvidence 不可用，修复后由 evidence.read 按 ID 重读。
	for _, want := range []string{"rawError", "stack", "timeEvidence"} {
		found := false
		for _, missing := range fixture.Attempts[1].AgentResult.ReportedMissing {
			if missing == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("attempt 2 reportedMissing %v missing %q", fixture.Attempts[1].AgentResult.ReportedMissing, want)
		}
	}
}

// --- AC1 ---------------------------------------------------------------------

// TestINC2270Regression_AC1_TrustedLocatorsLeadCodeFirstAndCorrectionEntersPlanning
// 证明 AC1/R1：首轮诊断必须携带 bounded 原始可信 provider detail（error/stack/
// sourcePath/line/rawTime + evidence ID）；agent 可以自由选择先检查精确部署代码
// （无强制 Docker-first 顺序）；classification challenge 被结构化回喂后，模型
// 修正 metadata 并保留 code_fixable，随后进入 planning，全程无人工介入。
func TestINC2270Regression_AC1_TrustedLocatorsLeadCodeFirstAndCorrectionEntersPlanning(t *testing.T) {
	store := newFakeRunStore()
	repo := &fakeRepoPort{}
	model := &scriptedModel{responses: []string{
		requestToolEnvelope(application.ToolRepoReadFile, "internal/handler.go"),
		mismatchedClassificationEnvelope("code_fixable"),
		diagnosisEnvelope("code_fixable"),
		planEnvelope(),
	}}
	coord := newCoordinator(store, repo, &fakeEvidencePort{}, model)
	coord.SetBootstrapEvidenceLoader(&bootstrapEvidenceLoader{value: domain.BootstrapEvidence{
		Records: []domain.StoredEvidence{{
			EvidenceID:     "ev-inc2270-provider-detail",
			Provider:       "tencent_cls",
			EvidenceKind:   domain.EvidenceKindProviderDetail,
			Classification: domain.EvidenceDirectFault,
			Outcome:        "success",
			Available:      true,
			Primary:        true,
			Payload: json.RawMessage(`{"error":"TriggerNilPointerFault","stack":"handler.go:42",` +
				`"sourcePath":"internal/handler.go","line":42,"rawTime":"2026-08-24T07:37:07.956Z"}`),
		}},
	}})

	run, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if run.State != domain.RunStateDiagnosisReadyForReview || store.state != domain.RunStateDiagnosisReadyForReview {
		t.Fatalf("final state = %s/%s, want diagnosis_ready_for_review", run.State, store.state)
	}
	if store.countTransitionsTo(domain.RunStateBlockedManualReview) != 0 {
		t.Fatalf("manual review was entered: %#v", store.transitions)
	}

	// R1：首轮携带 trusted provider detail 的 locators 与证据 ID。
	first := model.turns[0].UserMessage
	for _, want := range []string{
		"ev-inc2270-provider-detail", "TriggerNilPointerFault", "handler.go:42",
		"internal/handler.go", `"line":42`, "rawTime",
		"no required first tool and no mandated sequence", "strong code-localization hint",
	} {
		if !strings.Contains(first, want) {
			t.Errorf("first turn missing %q", want)
		}
	}
	// R2/R3：agent 首选精确部署代码（code-first），harness 不强制 Docker-first。
	if len(store.invocations) != 1 || store.invocations[0].ToolName != application.ToolRepoReadFile ||
		store.invocations[0].Error != "" {
		t.Fatalf("first tool invocation = %#v, want repository.read_file without forced Docker/SSH order", store.invocations)
	}
	if repo.calls != 1 {
		t.Fatalf("repository adapter calls = %d, want 1", repo.calls)
	}

	// R6/R7：classification mismatch 以结构化 challenge 回喂，携带存储权威分类。
	// challenge 出现在 code-first 工具轮之后的第二轮（turns[2]）。
	second := model.turns[2].UserMessage
	for _, want := range []string{
		`"kind":"evidence_correction"`, `"severity":"recoverable"`,
		`"reasonCode":"citation_classification_mismatch"`,
		"ev-1 is stored as direct_fault",
		"does not by itself change fixability or confidence",
	} {
		if !strings.Contains(second, want) {
			t.Errorf("second turn missing %q", want)
		}
	}

	// AC1：修正后只持久化一条 code_fixable decision 并进入 planning。
	if len(store.decisions) != 1 {
		t.Fatalf("decisions = %d, want 1 (only the corrected diagnosis)", len(store.decisions))
	}
	if store.decisions[0].Fixability != domain.FixabilityCodeFixable {
		t.Fatalf("decision fixability = %q, want code_fixable preserved", store.decisions[0].Fixability)
	}
	if store.countTransitionsTo(domain.RunStatePlanning) != 1 {
		t.Fatalf("planning transitions = %d, want 1", store.countTransitionsTo(domain.RunStatePlanning))
	}
	if model.calls != 4 {
		t.Fatalf("model calls = %d, want 4 (code-first read, mismatched, corrected, plan)", model.calls)
	}
}

// --- AC2 ---------------------------------------------------------------------

// TestINC2270Regression_AC2_ContinuationRehydratesEarlierProviderDetailByID
// 证明 AC2/R10：后续 attempt 的 continuity 不降级为 runtime-only 上下文——
// 首轮渲染同 series provider detail 的紧凑索引，模型凭 evidence ID 通过
// evidence.read 重读 earlier trusted provider detail；且模型可见的 evidence ID /
// content hash 不能伪造 cursor offset/expiry（D3 server-authoritative cursor）。
func TestINC2270Regression_AC2_ContinuationRehydratesEarlierProviderDetailByID(t *testing.T) {
	t.Run("later attempt re-reads earlier provider detail via evidence.read", func(t *testing.T) {
		predecessor := eligibleAutomaticAggregate(domain.RunStateFailed, 5, true)
		predecessor.Run.TerminalReason = "provider_transport"
		predecessor.Run.Retryable = true
		store := &continuationStore{
			fakeRunStore: newFakeRunStore(),
			predecessor:  predecessor,
			continuationEvidenceIndex: []domain.EvidenceIndexEntry{{
				EvidenceID:     "ev-inc2270-provider-detail",
				Kind:           domain.EvidenceKindProviderDetail,
				Provider:       "tencent_cls",
				Classification: domain.EvidenceDirectFault,
				SourceAttempt:  1,
				ContentHash:    strings.Repeat("a", 64),
			}},
		}
		readPort := &fakeEvidenceReadPort{result: domain.EvidenceReadPage{
			EvidenceID:           "ev-inc2270-provider-detail",
			Kind:                 domain.EvidenceKindProviderDetail,
			StoredClassification: domain.EvidenceDirectFault,
			Provenance:           domain.EvidenceReadProvenance{SourceID: "source-1", SourceAttempt: 1},
			ContentHash:          strings.Repeat("a", 64),
			Content: `{"error":"TriggerNilPointerFault","stack":"handler.go:42",` +
				`"sourcePath":"internal/handler.go","line":42,"rawTime":"2026-08-24T07:37:07.956Z"}`,
		}}
		model := &scriptedModel{responses: []string{
			requestEvidenceReadEnvelope("ev-inc2270-provider-detail"),
			diagnosisEnvelope("external_dependency"),
		}}
		// 使用带 source identity 的 runtime wiring，使 continuation 的诊断
		// catalog 启用 evidence.read（evidence 工具要求 source enabled）。
		coord := application.NewRemediationCoordinatorWithRuntime(
			store, &fakeRepoPort{}, &fakeEvidencePort{}, model,
			wiringLookup{identity: application.IncidentIdentity{
				ID: testIncidentUUID, ProjectID: "project-1", EnvironmentID: "environment-1", SourceID: "source-1",
				Priority: "P2", DeployedCommit: "abc123", LifecycleGeneration: 1,
			}},
			staticRemote("https://git.example.invalid/app.git"), store, store,
		)
		coord.SetEvidenceReadPort(readPort)

		run, err := coord.Continue(context.Background(), domain.NextAttempt{
			ContinuationOfRunID:     predecessor.Run.RunID,
			SeriesID:                predecessor.Run.SeriesID,
			IncidentID:              predecessor.Run.IncidentID,
			LifecycleGeneration:     predecessor.Run.LifecycleGeneration,
			DeployedCommit:          predecessor.Run.DeployedCommit,
			ContextVersion:          6,
			ExpectedPreviousVersion: predecessor.Run.Version,
			Origin:                  domain.TriggerOriginAutomaticContinue,
			TriggerReason:           domain.TriggerOriginAutomaticContinue,
			ContinuationReason:      "new inbound evidence persisted",
		})
		if err != nil {
			t.Fatalf("Continue() error = %v", err)
		}
		if run.State != domain.RunStateCompletedNonCode || store.state != domain.RunStateCompletedNonCode {
			t.Fatalf("continued run = %#v, want completed_non_code", run)
		}
		if store.continuationIndexCalls != 1 {
			t.Fatalf("continuation evidence index calls = %d, want 1", store.continuationIndexCalls)
		}
		// 不降级为 runtime-only：provider detail 以紧凑索引出现在首轮。
		first := model.turns[0].UserMessage
		for _, want := range []string{"prior_evidence_index", "ev-inc2270-provider-detail", "provider_detail", "evidence.read"} {
			if !contains(first, want) {
				t.Fatalf("diagnosis continuation missing %q: %s", want, first)
			}
		}
		if contains(first, "prior_runtime_evidence") {
			t.Fatalf("continuation degraded to runtime-only context: %s", first)
		}
		// evidence.read 以 child run 身份解析同 series 证据，并返回原 payload。
		if readPort.calls != 1 || readPort.lastReq.RunID != "run-next" ||
			readPort.lastReq.EvidenceID != "ev-inc2270-provider-detail" {
			t.Fatalf("evidence.read request = %#v, want run-next identity and the provider detail id", readPort.lastReq)
		}
		second := model.turns[1].UserMessage
		for _, want := range []string{"TriggerNilPointerFault", "handler.go:42", "internal/handler.go"} {
			if !strings.Contains(second, want) {
				t.Fatalf("rehydrated provider detail missing %q in the following turn: %s", want, second)
			}
		}
	})

	t.Run("tampered cursor cannot forge an offset or expiry", func(t *testing.T) {
		// D3：cursor 是 server-state-backed random capability；模型可见的 evidence
		// ID / content hash / 源码都不能 mint 或修改 offset/expiry。伪造的 cursor
		// 在 port 边界被拒绝为 invalid_arguments。
		readPort := &fakeEvidenceReadPort{err: domain.ErrEvidenceReadCursorInvalid}
		gateway := mustEvidenceReadGateway(readPort)
		catalog := mustEvidenceReadCatalog(t, gateway, "run-1", domain.RunStateDiagnosing)
		_, err := gateway.ExecuteToolWithCatalog(context.Background(), domain.RunStateDiagnosing,
			domain.RepoRef{}, domain.EvidenceScope{ProjectID: "project-1", SourceID: "source-1"}, catalog,
			application.ToolEvidenceRead,
			map[string]interface{}{"evidenceId": "ev-inc2270-provider-detail", "cursor": "forged-offset"})
		if code, _ := application.RejectionCode(err); code != application.RejectArguments {
			t.Fatalf("tampered cursor rejection code = %s, want %s", code, application.RejectArguments)
		}
	})
}

// --- AC3 ---------------------------------------------------------------------

// sequenceInspectPort 是 canned SSHInspectPort：按序消费 errs，之后按序消费
// results，最后回落默认成功结果。供 AC3 的“第一次 inspect 失败、精化查询后
// 成功”脚本使用。
type sequenceInspectPort struct {
	fakeInspectPort
	errs    []error
	results []domain.SSHInspectResult
}

func (f *sequenceInspectPort) Inspect(ctx context.Context, scope domain.EvidenceScope, req domain.SSHInspectRequest) (domain.SSHInspectResult, error) {
	f.calls++
	f.lastScope = scope
	f.lastCommand = req.Command
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		if err != nil {
			return domain.SSHInspectResult{}, err
		}
	}
	if len(f.results) > 0 {
		result := f.results[0]
		f.results = f.results[1:]
		return result, nil
	}
	return domain.SSHInspectResult{Command: req.Command, ExitCode: 0, Stdout: "ok"}, nil
}

// TestINC2270Regression_AC3_AmbiguousCodeFirstSwitchesToRuntimeThenBackToRepository
// 证明 AC3/R3：code-first 检查无法建立因果时，agent 自主切换到 SSH/runtime
// 工具，第一次查询失败后精化查询，再返回精确部署代码分析；全程不创建新
// attempt、不进入人工 review，run 保持 active 并正常完成。
func TestINC2270Regression_AC3_AmbiguousCodeFirstSwitchesToRuntimeThenBackToRepository(t *testing.T) {
	const (
		projectID     = "019ff544-405c-7d21-9f10-cb3fc579605c"
		environmentID = "019ff544-405c-7d22-9f10-cb3fc579605c"
		sourceID      = "019ff544-405c-7d23-9f10-cb3fc579605c"
	)
	store := newFakeRunStore()
	repo := &fakeRepoPort{}
	inspect := &sequenceInspectPort{
		errs: []error{fmt.Errorf("remote command failed: connection reset")},
		results: []domain.SSHInspectResult{{
			Command:  "'grep' '-E' 'TriggerNilPointerFault|nil pointer' '/var/log/app.log' | 'head' '-20'",
			ExitCode: 0, Stdout: "2026-08-24T07:37:07Z app[42]: panic: TriggerNilPointerFault\n",
		}},
	}
	runtimeWriter := &fakeRuntimeEvidenceWriter{}
	model := &scriptedModel{responses: []string{
		requestToolEnvelope(application.ToolRepoReadFile, "internal/handler.go"),
		requestSSHInspectEnvelope("grep 'TriggerNilPointerFault' /var/log/app.log"),
		requestSSHInspectEnvelope("grep -E 'TriggerNilPointerFault|nil pointer' /var/log/app.log | head -20"),
		requestToolEnvelope(application.ToolRepoReadFile, "internal/handler.go"),
		diagnosisEnvelope("code_fixable"),
		planEnvelope(),
	}}
	coord := application.NewRemediationCoordinatorWithDynamicRuntime(
		store, repo, &fakeEvidencePort{}, inspect, model,
		wiringLookup{identity: application.IncidentIdentity{
			ID: testIncidentUUID, ProjectID: projectID, EnvironmentID: environmentID, SourceID: sourceID,
			Priority: "P2", DeployedCommit: "abc123", LifecycleGeneration: 1,
		}},
		staticRemote("https://git.example.internal/app.git"),
		store, store, nil,
		staticSourceCaps{snapshot: domain.SourceCapabilitySnapshot{
			ProjectID: projectID, SourceID: sourceID, Kind: "ssh", Enabled: true, Supported: true,
			Declared: []string{"pull_collection"}, Version: 4,
			SSHHost: "logs.example.invalid", SSHUser: "app", SSHProjectFolder: "/srv/app", SSHLogPath: "/var/log",
		}},
		nil, nil,
	)
	coord.SetEvidenceResolver(fakeEvidenceResolver{})
	coord.SetRuntimeEvidenceWriter(runtimeWriter)

	run, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if run.State != domain.RunStateDiagnosisReadyForReview || store.state != domain.RunStateDiagnosisReadyForReview {
		t.Fatalf("final state = %s/%s, want diagnosis_ready_for_review", run.State, store.state)
	}
	if store.countTransitionsTo(domain.RunStateBlockedManualReview) != 0 {
		t.Fatalf("manual review was entered despite recovery: %#v", store.transitions)
	}
	// 工具序列：repository → ssh(inspect) → ssh(精化) → repository（返回代码分析）。
	wantTools := []string{
		application.ToolRepoReadFile,
		application.ToolSSHInspect,
		application.ToolSSHInspect,
		application.ToolRepoReadFile,
	}
	if len(store.invocations) != len(wantTools) {
		t.Fatalf("invocations = %d, want %d", len(store.invocations), len(wantTools))
	}
	for index, want := range wantTools {
		if store.invocations[index].ToolName != want {
			t.Fatalf("invocation %d tool = %q, want %q", index, store.invocations[index].ToolName, want)
		}
	}
	// 第一次 inspect 失败：稳定 retryable 错误回喂，不终态化。
	if store.invocations[1].Error != "remote_execution" {
		t.Fatalf("failed inspect invocation = %#v, want remote_execution", store.invocations[1])
	}
	failedTurn := model.turns[2].UserMessage
	for _, want := range []string{`"code":"remote_execution"`, `"retryable":true`, `"recoveryAction":"retry_transient"`} {
		if !strings.Contains(failedTurn, want) {
			t.Fatalf("turn after failed inspect missing %q: %s", want, failedTurn)
		}
	}
	// 精化后的成功 inspect 持久化为 run 归属的 canonical runtime evidence，
	// 其证据 ID 进入下一轮观察供引用。
	if len(runtimeWriter.evidence) != 1 || runtimeWriter.evidence[0].Provider != "ssh" ||
		runtimeWriter.evidence[0].RunID != "run-1" {
		t.Fatalf("runtime evidence = %#v", runtimeWriter.evidence)
	}
	runtimeEvidenceID := runtimeWriter.evidence[0].EvidenceID
	if runtimeEvidenceID == "" {
		t.Fatal("runtime evidence missing id")
	}
	if !strings.Contains(model.turns[3].UserMessage, runtimeEvidenceID) {
		t.Fatalf("turn after refined inspect omitted evidence id %s: %s", runtimeEvidenceID, model.turns[3].UserMessage)
	}
	if model.calls != 6 {
		t.Fatalf("model calls = %d, want 6 (repo, inspect, refine, repo, diagnosis, plan)", model.calls)
	}
}

// --- AC4 ---------------------------------------------------------------------

// TestINC2270Regression_AC4_ConnectorFailureKeepsAlternativeToolAndRunActive
// 证明 AC4/R16/R17：log-API（evidence.search）connector 失败产生可恢复的
// 安全观察（retryable + recoveryAction），替代 capability（repository /
// evidence.read）在下一轮目录中仍然广告且可用；run 保持 active，不进入
// 人工 review，并经由替代工具正常完成。
func TestINC2270Regression_AC4_ConnectorFailureKeepsAlternativeToolAndRunActive(t *testing.T) {
	model := &scriptedModel{responses: []string{
		requestEvidenceSearchEnvelope(),
		requestToolEnvelope(application.ToolRepoReadFile, "main.go"),
		diagnosisEnvelope("external_dependency"),
	}}
	store := newFakeRunStore()
	evidence := &failingSearchEvidencePort{err: fmt.Errorf("log connector unavailable")}
	// sourceWiredCoordinator 带 source identity：evidence.search/context/read 在
	// diagnosing catalog 中广告且可用（evidence 工具要求 source enabled）。
	coord := sourceWiredCoordinator(store, &fakeRepoPort{}, evidence, model)

	run, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if run.State != domain.RunStateCompletedNonCode || store.state != domain.RunStateCompletedNonCode {
		t.Fatalf("final state = %s/%s, want completed_non_code via the alternative tool", run.State, store.state)
	}
	if store.countTransitionsTo(domain.RunStateBlockedManualReview) != 0 {
		t.Fatalf("connector failure terminalized the run: %#v", store.transitions)
	}
	// 失败观察安全回喂（D5 可恢复类：retryable + recoveryAction）。
	second := model.turns[1].UserMessage
	for _, want := range []string{`"status":"error"`, `"code":"connector_failure"`, `"retryable":true`, `"recoveryAction":"retry_transient"`} {
		if !strings.Contains(second, want) {
			t.Fatalf("second turn missing %q: %s", want, second)
		}
	}
	if strings.Contains(second, "log connector unavailable") {
		t.Fatalf("second turn leaked raw connector error: %s", second)
	}
	// 替代工具仍被广告：repository.read_file 与 evidence.read 保留在下一轮目录。
	var sawRepo, sawRead, sawSearch bool
	for _, tool := range model.turns[1].Tools {
		switch tool.Name {
		case application.ToolRepoReadFile:
			sawRepo = true
		case application.ToolEvidenceRead:
			sawRead = true
		case application.ToolEvidenceSearch:
			sawSearch = true
		}
	}
	if !sawRepo || !sawRead || !sawSearch {
		t.Fatalf("alternative capabilities not advertised after failure: repo=%t read=%t search=%t", sawRepo, sawRead, sawSearch)
	}
	// run 保持 active：失败调用记账后，替代工具执行成功并完成诊断。
	if len(store.invocations) != 2 || store.invocations[0].Error != "connector_failure" || store.invocations[1].Error != "" {
		t.Fatalf("invocations = %#v, want failed search then successful repository read", store.invocations)
	}
}

// --- AC5 ---------------------------------------------------------------------

// TestINC2270Regression_AC5_CitationClassificationMismatchIsCorrectedNotContradicted
// 证明 AC5/R7：citation classification mismatch 通过 agent loop 修正——它既不
// 独立降低置信度，也不产生 material contradiction；修正后的 code_fixable 以
// 原始 0.9 置信度、零 contradiction 通过 gate 并进入 planning。
func TestINC2270Regression_AC5_CitationClassificationMismatchIsCorrectedNotContradicted(t *testing.T) {
	model := &scriptedModel{responses: []string{
		mismatchedClassificationEnvelope("code_fixable"),
		diagnosisEnvelope("code_fixable"),
		planEnvelope(),
	}}
	store, run, err := runStart(t, model)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.State != domain.RunStateDiagnosisReadyForReview || store.state != domain.RunStateDiagnosisReadyForReview {
		t.Fatalf("final state = %s/%s, want diagnosis_ready_for_review", run.State, store.state)
	}
	if store.countTransitionsTo(domain.RunStateBlockedManualReview) != 0 {
		t.Fatalf("correctable mismatch terminalized the run: %#v", store.transitions)
	}
	// challenge 明确声明 mismatch 不改变 fixability/confidence（R7）。
	second := model.turns[1].UserMessage
	for _, want := range []string{
		`"kind":"evidence_correction"`, `"severity":"recoverable"`,
		"ev-1 is stored as direct_fault", "does not by itself change fixability or confidence",
	} {
		if !strings.Contains(second, want) {
			t.Fatalf("second turn missing %q: %s", want, second)
		}
	}
	// 修正后的 diagnosis 保留 0.9 置信度且 gate 无 contradiction。
	if len(store.decisions) != 1 {
		t.Fatalf("decisions = %d, want 1 (only the corrected diagnosis)", len(store.decisions))
	}
	decision := store.decisions[0]
	if decision.Fixability != domain.FixabilityCodeFixable || decision.Confidence != 0.9 {
		t.Fatalf("corrected decision = fixability %q confidence %v, want code_fixable / 0.9 unchanged", decision.Fixability, decision.Confidence)
	}
	if decision.EvidenceAssessment == nil || !decision.EvidenceAssessment.PlanningEligible ||
		decision.EvidenceAssessment.EffectiveConfidence != 0.9 {
		t.Fatalf("gate assessment = %#v, want planning eligible with effective confidence 0.9", decision.EvidenceAssessment)
	}
	if len(decision.EvidenceAssessment.Contradictions) != 0 || len(decision.Contradictions) != 0 {
		t.Fatalf("mismatch created a material contradiction: assessment=%v decision=%v",
			decision.EvidenceAssessment.Contradictions, decision.Contradictions)
	}
}

// --- AC6 ---------------------------------------------------------------------

// TestINC2270Regression_AC6_RecoverableLoopFailuresPreserveDiagnosisClass
// 证明 AC6：malformed envelope、oversized tool observation、provider-native
// continuation 丢失都走可恢复路径，最终诊断仍是 code_fixable 而非被降级为
// insufficient_evidence；run 不进入人工 review。
func TestINC2270Regression_AC6_RecoverableLoopFailuresPreserveDiagnosisClass(t *testing.T) {
	t.Run("malformed envelope recovers with code_fixable preserved", func(t *testing.T) {
		model := &scriptedModel{responses: []string{
			`not json`,
			diagnosisEnvelope("code_fixable"),
			planEnvelope(),
		}}
		store, run, err := runStart(t, model)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if run.State != domain.RunStateDiagnosisReadyForReview {
			t.Fatalf("final state = %s, want diagnosis_ready_for_review", run.State)
		}
		if len(store.decisions) != 1 || store.decisions[0].Fixability != domain.FixabilityCodeFixable {
			t.Fatalf("decisions = %#v, want a single code_fixable decision (not insufficient_evidence)", store.decisions)
		}
		second := model.turns[1].UserMessage
		if !strings.Contains(second, "protocol_observation") || !strings.Contains(second, "invalid_envelope") {
			t.Fatalf("second turn missing protocol correction: %s", second)
		}
		if store.countTransitionsTo(domain.RunStateBlockedManualReview) != 0 {
			t.Fatalf("malformed envelope terminalized the run: %#v", store.transitions)
		}
	})

	t.Run("oversized tool observation stays bounded and diagnosis class preserved", func(t *testing.T) {
		page := mustEvidenceReadPage()
		page.Content = strings.Repeat("c", domain.MaxEvidenceReadPageBytes)
		page.Truncated = true
		page.ByteCount = domain.MaxEvidenceReadPageBytes
		readPort := &fakeEvidenceReadPort{result: page}
		model := &scriptedModel{responses: []string{
			requestEvidenceReadEnvelope("0190-0000-0000-7000-0000000000aa"),
			diagnosisEnvelope("code_fixable"),
			planEnvelope(),
		}}
		store := newFakeRunStore()
		// sourceWiredCoordinator 带 source identity：evidence.read 在 diagnosing
		// catalog 中广告且可用，oversized page 才会真正走端口执行。resolver 由
		// coordinator 路径需要（code_fixable 走 evidence gate）。
		coord := sourceWiredCoordinator(store, &fakeRepoPort{}, &fakeEvidencePort{}, model)
		coord.SetEvidenceResolver(fakeEvidenceResolver{})
		coord.SetEvidenceReadPort(readPort)

		if _, err := coord.Start(context.Background(), domain.NewRun{
			IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
		}); err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		if store.state != domain.RunStateDiagnosisReadyForReview {
			t.Fatalf("final state = %s, want diagnosis_ready_for_review", store.state)
		}
		if len(store.decisions) != 1 || store.decisions[0].Fixability != domain.FixabilityCodeFixable {
			t.Fatalf("decisions = %#v, want code_fixable preserved after oversized observation", store.decisions)
		}
		second := model.turns[1].UserMessage
		if !strings.Contains(second, "evidence.read") || !strings.Contains(second, `"bytes":65536`) {
			t.Fatalf("second turn lost the oversized tool observation: %s", second)
		}
		// 观察被 bounded：64 KiB 页面加上 envelope 元数据后总长仍远低于两倍
		// 页面上限，未被无限内联。
		if len(second) >= 2*domain.MaxEvidenceReadPageBytes {
			t.Fatalf("oversized observation was not bounded: %d bytes", len(second))
		}
	})

	t.Run("provider-native continuation loss keeps accepted context", func(t *testing.T) {
		model := &nativeScriptedModel{results: []domain.ModelResult{
			{
				Provider: "openai", Model: "gpt-5.6", UsageTokensOut: 2,
				ToolCalls: []domain.ToolCall{{ID: "call-1", Name: application.ToolRepoReadFile, Arguments: map[string]interface{}{"path": "main.go"}}},
			},
			{Content: `not json`, Provider: "openai", Model: "gpt-5.6", UsageTokensOut: 2},
			{Content: diagnosisEnvelope("code_fixable"), Provider: "openai", Model: "gpt-5.6", UsageTokensOut: 2},
			{Content: planEnvelope(), Provider: "openai", Model: "gpt-5.6", UsageTokensOut: 2},
		}}
		store := newFakeRunStore()
		coord := newCoordinator(store, &fakeRepoPort{}, &fakeEvidencePort{}, model)

		if _, err := coord.Start(context.Background(), domain.NewRun{
			IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
		}); err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		if store.state != domain.RunStateDiagnosisReadyForReview {
			t.Fatalf("final state = %s, want diagnosis_ready_for_review", store.state)
		}
		if len(store.decisions) != 1 || store.decisions[0].Fixability != domain.FixabilityCodeFixable {
			t.Fatalf("decisions = %#v, want code_fixable preserved after native continuation loss", store.decisions)
		}
		// 被拒绝的 native 输出没有进入 accepted history；既有工具结果保留，
		// 下一次 provider 轮次从 durable 上下文重建，而不是丢失。
		if len(model.turns[2].Messages) != len(model.turns[1].Messages) {
			t.Fatalf("rejected native output grew accepted history: before=%d after=%d",
				len(model.turns[1].Messages), len(model.turns[2].Messages))
		}
		last := model.turns[2].Messages[len(model.turns[2].Messages)-1]
		if last.Role != "tool" || last.ToolCallID != "call-1" {
			t.Fatalf("accepted tool context was lost after native continuation loss: %#v", model.turns[2].Messages)
		}
	})
}

// --- AC7 ---------------------------------------------------------------------

// countingCheckpointStore 包装 fakeCheckpointStore，记录 LoadLatestCheckpoint
// 的调用目标，供 AC7 证明重启进程从 predecessor 的 durable checkpoint 重建。
type countingCheckpointStore struct {
	*fakeCheckpointStore
	loads []string
}

func (c *countingCheckpointStore) LoadLatestCheckpoint(ctx context.Context, runID string) (domain.CheckpointSnapshot, error) {
	c.loads = append(c.loads, runID)
	return c.fakeCheckpointStore.LoadLatestCheckpoint(ctx, runID)
}

// TestINC2270Regression_AC7_RestartReconstructsFromDurableCheckpoint
// 证明 AC7/R12/R14：进程重启（模拟为全新 coordinator + 全新 model，唯一
// durable 输入是 checkpoint store）在 diagnosing phase boundary 从 predecessor
// 的最新 durable checkpoint 重建 bounded working memory（objective、next
// actions、evidence index、budget），不依赖 opaque provider session；重建块
// 取代 runtime-only evidence 块但保留主 brief。
func TestINC2270Regression_AC7_RestartReconstructsFromDurableCheckpoint(t *testing.T) {
	predecessor := eligibleAutomaticAggregate(domain.RunStateFailed, 1, true)
	predecessor.Decisions = []domain.Decision{{Fixability: domain.FixabilityInsufficientEvidence}}
	store := &continuationStore{
		fakeRunStore: newFakeRunStore(),
		predecessor:  predecessor,
		childMode:    domain.AgentLoopModeResilientV1,
		continuationEvidenceIndex: []domain.EvidenceIndexEntry{{
			EvidenceID:     "ev-inc2270-provider-detail",
			Kind:           "provider_detail",
			Provider:       "tencent_cls",
			Classification: domain.EvidenceDirectFault,
			SourceAttempt:  1,
			ContentHash:    strings.Repeat("c", 64),
		}},
	}
	plan, planErr := application.NewBudgetPlan(application.DefaultBudgetLimits(), nil, nil, domain.RecoveryReserve{})
	if planErr != nil {
		t.Fatalf("NewBudgetPlan: %v", planErr)
	}
	base := &fakeCheckpointStore{}
	base.byRun = map[string]domain.CheckpointSnapshot{
		"run-previous": {
			RunID: "run-previous", SeriesID: "series-1", ContextVersion: 1,
			ObservedRunVersion: 7, Sequence: 3, Phase: string(domain.RunStateDiagnosing),
			Checkpoint: domain.WorkingMemoryCheckpointV1{
				SchemaVersion: domain.CheckpointSchemaVersionV1, Sequence: 3,
				RunID: "run-previous", SeriesID: "series-1", ContextVersion: 1, ObservedRunVersion: 7,
				Phase: string(domain.RunStateDiagnosing),
				Objective: domain.CheckpointObjective{
					Goal:               "Diagnose the incident from trusted evidence and repository inspection.",
					CompletionCriteria: []string{"diagnosis envelope accepted"},
				},
				NextActions: []string{"inspect repository locator"},
				Budget: domain.BudgetPlanRecoveryV1{
					SchemaVersion: domain.BudgetPlanSchemaVersionV1,
					Plan:          plan,
					CurrentPhase:  domain.RunStateDiagnosing,
					Frontier:      0,
				},
				Reason: domain.CheckpointReasonPhaseBoundary,
			},
		},
	}
	checkpoints := &countingCheckpointStore{fakeCheckpointStore: base}
	model := &scriptedModel{responses: []string{diagnosisEnvelope("unsafe_to_automate")}}
	coord := application.NewRemediationCoordinatorWithReview(store, &fakeRepoPort{}, &fakeEvidencePort{}, model, nil, store, store)
	coord.SetCheckpointStore(checkpoints)

	run, err := coord.Continue(context.Background(), domain.NextAttempt{
		ContinuationOfRunID:     "run-previous",
		SeriesID:                "series-1",
		IncidentID:              testIncidentUUID,
		LifecycleGeneration:     1,
		DeployedCommit:          "abc123",
		ContextVersion:          1,
		ExpectedPreviousVersion: 7,
		Origin:                  domain.TriggerOriginManualContinue,
		TriggerReason:           domain.TriggerOriginManualContinue,
		ContinuationReason:      "operator continue",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.RunID != "run-next" || store.state != domain.RunStateBlockedManualReview {
		t.Fatalf("restarted run = %#v state = %s, want run-next blocked_manual_review", run, store.state)
	}
	// 重启进程唯一 durable 输入是 checkpoint：LoadLatestCheckpoint 被查询
	// predecessor 的最新快照（无 opaque provider session）。
	if len(checkpoints.loads) != 1 || checkpoints.loads[0] != "run-previous" {
		t.Fatalf("checkpoint loads = %v, want exactly one load of run-previous", checkpoints.loads)
	}
	first := model.turns[0].UserMessage
	for _, want := range []string{
		"working_memory_reconstruction",
		"Diagnose the incident from trusted evidence and repository inspection.",
		"inspect repository locator",
		`"evidenceId":"ev-inc2270-provider-detail"`,
		"remediation_continuation_brief",
	} {
		if !contains(first, want) {
			t.Fatalf("first turn missing %q: %s", want, first)
		}
	}
	// 重建块取代 runtime-only evidence 块（不重复内联原 payload/index block）。
	if contains(first, "prior_runtime_evidence") || contains(first, "prior_evidence_index") {
		t.Fatalf("reconstruction must merge prior evidence as locators, not inline blocks: %s", first)
	}
	// 子 run 在 phase boundary 与终态前各持久化一次 checkpoint（AC7 重启种子）。
	if len(checkpoints.appends) != 2 || checkpoints.appends[0].RunID != "run-next" {
		t.Fatalf("child checkpoints = %d (run %q), want 2 for run-next", len(checkpoints.appends), checkpoints.appends[0].RunID)
	}
}

// --- AC8 ---------------------------------------------------------------------

// TestINC2270Regression_AC8_IncompleteExhaustionProposalRejectedWithChallenge
// 证明 AC8/R20：服务拒绝 early/incomplete exhaustion proposal，并把 listing
// 每个未覆盖 capability/recovery class 的 recoverable challenge 回喂同一循环；
// 完整 proof 才被接受并以 exhaustion_proof 进入 blocked_manual_review。
func TestINC2270Regression_AC8_IncompleteExhaustionProposalRejectedWithChallenge(t *testing.T) {
	t.Run("coordinator rejects an early proposal and lists the uncovered capability", func(t *testing.T) {
		limits := application.DefaultBudgetLimits()
		limits.MaxModelCalls = 3
		store := newFakeRunStore()
		store.mode = domain.AgentLoopModeResilientV1
		checkpoints := &fakeCheckpointStore{}
		model := &scriptedModel{responses: []string{
			stopEnvelope(),
			incompleteExhaustionProposalEnvelope(),
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
			t.Fatalf("final state = %s/%s, want blocked_manual_review after the corrected proof", run.State, store.state)
		}
		if model.calls != 3 {
			t.Fatalf("model calls = %d, want 3 (stop, incomplete, complete)", model.calls)
		}
		if len(store.effects) == 0 || store.effects[len(store.effects)-1].TerminalReason != "exhaustion_proof" {
			t.Fatalf("terminal effect = %#v, want exhaustion_proof", store.effects)
		}
		// 不完整 proof 的 challenge 列出未覆盖 capability（默认 diagnosing catalog
		// 只广告 repository）。
		third := model.turns[2].UserMessage
		for _, want := range []string{
			`"kind":"exhaustion"`, `"severity":"recoverable"`,
			`"reasonCode":"exhaustion_proof_incomplete"`,
			"uncovered capability: repository",
		} {
			if !strings.Contains(third, want) {
				t.Fatalf("third turn missing %q: %s", want, third)
			}
		}
		assertExhaustionRecoveryRecorded(t, checkpoints)
	})

	t.Run("early proposal with no attempted paths lists every uncovered class", func(t *testing.T) {
		accepted, challenge, reasons := application.ValidateExhaustionProposal(
			domain.ExhaustionProposalV1{
				UnresolvedGoal:      "no collectible evidence can close the causal gap",
				AttemptedPaths:      nil,
				UntriedCapabilities: nil,
				BestConclusion:      domain.FixabilityInsufficientEvidence,
				Handoff:             "ask an operator to collect the missing runtime evidence",
			},
			application.ExhaustionValidationContext{
				CatalogCapabilities: []string{"repository", "runtime_logs", "ssh_inspect"},
				ActionRefs:          map[string][]string{},
				RemainingBudget: map[string]int64{
					"model_calls": 0, "model_cost_cents": 0, "tool_calls": 0,
					"elapsed_seconds": 0, "evidence_bytes": 0, "repository_bytes": 0,
				},
			},
		)
		if accepted {
			t.Fatal("early proposal was accepted, want rejection")
		}
		if challenge.Kind != domain.RecoveryChallengeKindExhaustion ||
			challenge.ReasonCode != "exhaustion_proof_incomplete" {
			t.Fatalf("challenge = %#v, want recoverable exhaustion challenge", challenge)
		}
		joined := strings.Join(reasons, ";")
		for _, want := range []string{
			"uncovered capability: repository",
			"uncovered capability: runtime_logs",
			"uncovered capability: ssh_inspect",
		} {
			if !strings.Contains(joined, want) {
				t.Fatalf("reasons = %v, want %q listed", reasons, want)
			}
		}
	})
}
