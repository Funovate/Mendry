package application_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"mendry/backend/internal/modules/remediation/application"
	"mendry/backend/internal/modules/remediation/domain"
)

// exhaustionProposalEnvelope 构造 D6 exhaustion envelope；capabilityRefs 是
// attemptedPaths 的 JSON（能力→outcomeRefs 引用），untried 是
// untriedCapabilities 的 JSON。bestConclusion 与 handoff 固定为
// insufficient_evidence / 有界人工建议，goal 固定为有界文本。
func exhaustionProposalEnvelope(attempted, untried string) string {
	return fmt.Sprintf(`{"schemaVersion":"v1","kind":"exhaustion","exhaustion":{`+
		`"unresolvedGoal":"no collectible evidence can close the causal gap",`+
		`"attemptedPaths":%s,"untriedCapabilities":%s,`+
		`"bestConclusion":"insufficient_evidence",`+
		`"handoff":"ask an operator to collect the missing runtime evidence"}}`, attempted, untried)
}

// completeExhaustionProposalEnvelope 覆盖默认 diagnosing catalog（repository）
// 并引用真实 repository tool observation 的 actionRef。
func completeExhaustionProposalEnvelope(outcomeRef string) string {
	return exhaustionProposalEnvelope(
		fmt.Sprintf(`[{"capability":"repository","outcomeRefs":[%q]}]`, outcomeRef),
		`[]`,
	)
}

// budgetProhibitedExhaustionProposalEnvelope 用完整 hard-budget 投影可验证的
// budget_prohibited 原因覆盖尚未尝试的 repository capability。
func budgetProhibitedExhaustionProposalEnvelope() string {
	return exhaustionProposalEnvelope(
		`[]`,
		`[{"capability":"repository","reasonCode":"budget_prohibited","evidenceRefs":[]}]`,
	)
}

// incompleteExhaustionProposalEnvelope 不尝试也不解释任何 capability：默认
// catalog 的 repository 未被覆盖，服务必须拒绝并列出它。
func incompleteExhaustionProposalEnvelope() string {
	return exhaustionProposalEnvelope(`[]`, `[]`)
}

// malformedExhaustionProposalEnvelope 违反 schema（unresolvedGoal 为空），
// 必须在解码边界作为协议错误回喂，不能静默放行。
const malformedExhaustionProposalEnvelope = `{"schemaVersion":"v1","kind":"exhaustion","exhaustion":{` +
	`"unresolvedGoal":"","attemptedPaths":[],"untriedCapabilities":[],` +
	`"bestConclusion":"insufficient_evidence","handoff":"x"}}`

func TestValidateExhaustionProposal(t *testing.T) {
	baseProposal := func() domain.ExhaustionProposalV1 {
		return domain.ExhaustionProposalV1{
			UnresolvedGoal: "no collectible evidence can close the causal gap",
			AttemptedPaths: []domain.ExhaustionAttemptedPath{{
				Capability:  "repository",
				OutcomeRefs: []string{"action:repository:1"},
			}},
			UntriedCapabilities: []domain.ExhaustionUntriedCapability{{
				Capability: "runtime_logs", ReasonCode: string(domain.ExhaustionReasonIrrelevant),
				EvidenceRefs: []string{"ev-1"},
			}},
			BestConclusion: domain.FixabilityInsufficientEvidence,
			Handoff:        "ask an operator to collect the missing runtime evidence",
		}
	}
	baseContext := func() application.ExhaustionValidationContext {
		return application.ExhaustionValidationContext{
			CatalogCapabilities: []string{"repository", "runtime_logs"},
			ActionRefs: map[string][]string{
				"repository": {"action:repository:1"},
			},
			EvidenceRefs: []string{"ev-1"},
			RemainingBudget: map[string]int64{
				"model_calls": 0, "model_cost_cents": 0, "tool_calls": 0,
				"elapsed_seconds": 0, "evidence_bytes": 0, "repository_bytes": 0,
			},
		}
	}

	t.Run("accepts capability-bound actions and owned materiality evidence", func(t *testing.T) {
		accepted, challenge, reasons := application.ValidateExhaustionProposal(baseProposal(), baseContext())
		if !accepted {
			t.Fatalf("accepted = false, want true; reasons = %v", reasons)
		}
		if challenge.SchemaVersion != "" {
			t.Fatalf("accepted proof must return zero challenge, got %#v", challenge)
		}
	})

	t.Run("rejects uncovered capability with the capability listed", func(t *testing.T) {
		validation := baseContext()
		validation.CatalogCapabilities = append(validation.CatalogCapabilities, "ssh_inspect")
		accepted, challenge, reasons := application.ValidateExhaustionProposal(baseProposal(), validation)
		if accepted || challenge.ReasonCode != "exhaustion_proof_incomplete" ||
			!strings.Contains(strings.Join(reasons, ";"), "uncovered capability: ssh_inspect") {
			t.Fatalf("accepted/challenge/reasons = %t/%#v/%v", accepted, challenge, reasons)
		}
	})

	t.Run("rejects recovery challenge ref masquerading as repository action", func(t *testing.T) {
		proposal := baseProposal()
		proposal.AttemptedPaths[0].OutcomeRefs = []string{"challenge:exhaustion:stop"}
		validation := baseContext()
		validation.EvidenceRefs = append(validation.EvidenceRefs, "challenge:exhaustion:stop")
		accepted, _, reasons := application.ValidateExhaustionProposal(proposal, validation)
		if accepted || !strings.Contains(strings.Join(reasons, ";"), "is not a repository action") {
			t.Fatalf("accepted/reasons = %t/%v", accepted, reasons)
		}
	})

	t.Run("rejects action ref bound to another capability", func(t *testing.T) {
		validation := baseContext()
		validation.ActionRefs = map[string][]string{"runtime_logs": {"action:repository:1"}}
		accepted, _, reasons := application.ValidateExhaustionProposal(baseProposal(), validation)
		if accepted || !strings.Contains(strings.Join(reasons, ";"), "is not a repository action") {
			t.Fatalf("accepted/reasons = %t/%v", accepted, reasons)
		}
	})

	t.Run("rejects unowned materiality evidence", func(t *testing.T) {
		validation := baseContext()
		validation.EvidenceRefs = nil
		accepted, _, reasons := application.ValidateExhaustionProposal(baseProposal(), validation)
		if accepted || !strings.Contains(strings.Join(reasons, ";"), "does not belong to this run or series") {
			t.Fatalf("accepted/reasons = %t/%v", accepted, reasons)
		}
	})

	t.Run("rejects unavailable reason without matching service policy", func(t *testing.T) {
		proposal := baseProposal()
		proposal.UntriedCapabilities[0].ReasonCode = string(domain.ExhaustionReasonUnavailable)
		accepted, _, reasons := application.ValidateExhaustionProposal(proposal, baseContext())
		if accepted || !strings.Contains(strings.Join(reasons, ";"), "lacks matching service policy evidence") {
			t.Fatalf("accepted/reasons = %t/%v", accepted, reasons)
		}
	})

	t.Run("accepts unavailable reason only from service policy snapshot", func(t *testing.T) {
		proposal := baseProposal()
		proposal.UntriedCapabilities[0].ReasonCode = string(domain.ExhaustionReasonUnavailable)
		validation := baseContext()
		validation.ServiceUntriedReasons = map[string]domain.ExhaustionReasonCode{
			"runtime_logs": domain.ExhaustionReasonUnavailable,
		}
		accepted, _, reasons := application.ValidateExhaustionProposal(proposal, validation)
		if !accepted {
			t.Fatalf("accepted = false, reasons = %v", reasons)
		}
	})

	t.Run("budget prohibited checks cost and capability byte dimensions", func(t *testing.T) {
		proposal := baseProposal()
		proposal.UntriedCapabilities[0] = domain.ExhaustionUntriedCapability{
			Capability: "runtime_logs", ReasonCode: string(domain.ExhaustionReasonBudgetProhibited),
		}
		validation := baseContext()
		validation.RemainingBudget = map[string]int64{
			"model_calls": 1, "model_cost_cents": 1, "tool_calls": 1,
			"elapsed_seconds": 1, "evidence_bytes": 1, "repository_bytes": 1,
		}
		accepted, _, reasons := application.ValidateExhaustionProposal(proposal, validation)
		if accepted || !strings.Contains(strings.Join(reasons, ";"), "is not budget prohibited") {
			t.Fatalf("accepted/reasons = %t/%v", accepted, reasons)
		}
		validation.RemainingBudget["model_cost_cents"] = 0
		accepted, _, reasons = application.ValidateExhaustionProposal(proposal, validation)
		if !accepted {
			t.Fatalf("zero model cost capacity should prove budget prohibition: %v", reasons)
		}
	})

	t.Run("rejects blocking recovery", func(t *testing.T) {
		validation := baseContext()
		validation.BlockingRecoveryRefs = []string{"challenge:context_rehydration"}
		accepted, _, reasons := application.ValidateExhaustionProposal(baseProposal(), validation)
		if accepted || !strings.Contains(strings.Join(reasons, ";"), "unresolved recovery") {
			t.Fatalf("accepted/reasons = %t/%v", accepted, reasons)
		}
	})

	t.Run("rejects structurally invalid proposal", func(t *testing.T) {
		proposal := baseProposal()
		proposal.AttemptedPaths[0].Capability = "tcpdump"
		accepted, challenge, reasons := application.ValidateExhaustionProposal(proposal, baseContext())
		if accepted || challenge.ReasonCode != "invalid_exhaustion_proposal" || len(reasons) == 0 {
			t.Fatalf("accepted/challenge/reasons = %t/%#v/%v", accepted, challenge, reasons)
		}
	})
}

// TestCoordinator_ResilientStopRequestsExhaustionProposalThenTerminalizes 证明
// R18/D6：resilient_v1 下模型 stop 不直接终态化——先向同一循环追加 D6
// exhaustion proposal 请求，模型提交完整 proof 后服务校验通过，才以
// exhaustion_proof 进入 blocked_manual_review。legacy 的
// TestCoordinator_StopReachesBlockedManualReview 保持字节不变。
func TestCoordinator_ResilientStopRequestsExhaustionProposalThenTerminalizes(t *testing.T) {
	limits := application.DefaultBudgetLimits()
	limits.MaxModelCalls = 3
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	checkpoints := &fakeCheckpointStore{}
	model := &scriptedModel{responses: []string{
		requestToolEnvelope(application.ToolRepoReadFile, "main.go"),
		stopEnvelope(),
		completeExhaustionProposalEnvelope("action:repository:1"),
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
	if model.calls != 3 {
		t.Fatalf("model calls = %d, want 3 (repository action, stop, proposal)", model.calls)
	}
	if len(store.invocations) != 1 || store.invocations[0].InvocationID != "action:repository:1" ||
		store.invocations[0].ToolName != application.ToolRepoReadFile {
		t.Fatalf("durable action authority = %#v", store.invocations)
	}
	if len(store.effects) == 0 || store.effects[len(store.effects)-1].TerminalReason != "exhaustion_proof" {
		t.Fatalf("terminal effect = %#v, want exhaustion_proof", store.effects)
	}
	// stop 不再直接持久化 stop decision；终态 decision 来自有界 proposal 元数据。
	if len(store.decisions) != 1 || store.decisions[0].Fixability != domain.FixabilityInsufficientEvidence ||
		store.decisions[0].RecommendedNextAction != "ask an operator to collect the missing runtime evidence" {
		t.Fatalf("decisions = %#v", store.decisions)
	}
	// stop 后的第三轮上下文包含 exhaustion request；第二轮工具观察公开的
	// actionRef 是 proof 唯一可用于 repository attempted path 的引用。
	if !strings.Contains(model.turns[1].UserMessage, `"actionRef":"action:repository:1"`) {
		t.Fatalf("second turn lacks capability-bound action ref: %s", model.turns[1].UserMessage)
	}
	if !strings.Contains(model.turns[2].UserMessage, `"kind":"exhaustion"`) ||
		!strings.Contains(model.turns[2].UserMessage, "exhaustion_proof_required") {
		t.Fatalf("third turn lacks exhaustion proposal request: %s", model.turns[2].UserMessage)
	}
	assertExhaustionRecoveryRecorded(t, checkpoints)
}

// TestCoordinator_ResilientIncompleteExhaustionProposalChallengesAndRetries
// 证明 AC8：不完整 proof（repository 未覆盖）被服务拒绝并回喂 recoverable
// exhaustion challenge（列出未覆盖 capability），循环继续；模型随后提交完整
// proof 被接受并终态化。
func TestCoordinator_ResilientToolAuditFailureBlocksActionAuthority(t *testing.T) {
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	store.invocationErr = errors.New("tool audit unavailable")
	model := &scriptedModel{responses: []string{
		requestToolEnvelope(application.ToolRepoReadFile, "main.go"),
	}}
	coord := newCoordinator(store, &fakeRepoPort{}, &fakeEvidencePort{}, model)
	coord.SetCheckpointStore(&fakeCheckpointStore{})

	_, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	if err == nil {
		t.Fatal("Start() error = nil, want persistence blocker")
	}
	if store.state != domain.RunStateFailed || len(store.effects) == 0 ||
		store.effects[len(store.effects)-1].TerminalReason != "persistence_failure" {
		t.Fatalf("state/effects = %s/%#v, want failed persistence_failure", store.state, store.effects)
	}
	if len(store.invocations) != 0 || model.calls != 1 {
		t.Fatalf("failed audit exposed action to another turn: invocations=%#v calls=%d", store.invocations, model.calls)
	}
}

func TestCoordinator_ResilientIncompleteExhaustionProposalChallengesAndRetries(t *testing.T) {
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
	// 不完整 proof 的 challenge 出现在第三轮上下文：kind=exhaustion、
	// reasonCode=exhaustion_proof_incomplete、列出未覆盖 capability。
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
}

// TestCoordinator_ResilientInsufficientEvidenceRequestsExhaustionProposal
// 证明 R18：resilient_v1 下无 tool request 的 terminal insufficient_evidence
// 不再直接 blockForInsufficientEvidence，而是先要求 exhaustion proposal；
// 完整 proof 被接受后以 exhaustion_proof 终态化。
func TestCoordinator_ResilientInsufficientEvidenceRequestsExhaustionProposal(t *testing.T) {
	limits := application.DefaultBudgetLimits()
	limits.MaxModelCalls = 2
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	checkpoints := &fakeCheckpointStore{}
	model := &scriptedModel{responses: []string{
		insufficientWithoutCollectionEnvelope(),
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
	if model.calls != 2 {
		t.Fatalf("model calls = %d, want 2", model.calls)
	}
	if len(store.effects) == 0 || store.effects[len(store.effects)-1].TerminalReason != "exhaustion_proof" {
		t.Fatalf("terminal effect = %#v, want exhaustion_proof", store.effects)
	}
	// 提交的 diagnosis 与接受后的有界 proposal 决策都持久化（D4）。
	if len(store.decisions) != 2 {
		t.Fatalf("decisions = %d, want submitted diagnosis + accepted proposal", len(store.decisions))
	}
}

// TestCoordinator_ResilientCollectLoopExhaustionRequestsExhaustionProposal
// 证明 R18：collect-loop 达到上限后 resilient_v1 不直接终态化，而是要求
// exhaustion proposal；完整 proof 被接受后以 exhaustion_proof 终态化。
func TestCoordinator_ResilientCollectLoopExhaustionRequestsExhaustionProposal(t *testing.T) {
	limits := application.DefaultBudgetLimits()
	limits.MaxModelCalls = 5
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	checkpoints := &fakeCheckpointStore{}
	model := &scriptedModel{responses: []string{
		insufficientWithCollectEnvelope(),
		insufficientWithCollectEnvelope(),
		insufficientWithCollectEnvelope(),
		insufficientWithCollectEnvelope(),
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
	if store.countTransitionsTo(domain.RunStateCollectingMoreContext) != 3 {
		t.Fatalf("collect loops = %d, want 3", store.countTransitionsTo(domain.RunStateCollectingMoreContext))
	}
	if len(store.effects) == 0 || store.effects[len(store.effects)-1].TerminalReason != "exhaustion_proof" {
		t.Fatalf("terminal effect = %#v, want exhaustion_proof", store.effects)
	}
	if model.calls != 5 {
		t.Fatalf("model calls = %d, want 5 (3 collects + over-limit + proposal)", model.calls)
	}
}

// TestCoordinator_ResilientDockerRefinementExhaustionStaysActiveAndRecovers
// 证明 AC4/R24：Docker refinement 被忽略达到上限时，resilient_v1 不进入
// blocked_manual_review（legacy 的
// TestCoordinator_BlocksRepeatedDiagnosisWithPendingDockerRefinement 保持
// 字节不变），而是要求 exhaustion proposal 并保持 run active；模型随后用
// 更窄的 docker.logs 查询恢复并正常完成诊断。
func TestCoordinator_ResilientDockerRefinementExhaustionStaysActiveAndRecovers(t *testing.T) {
	start := time.Date(2026, 8, 28, 8, 6, 0, 0, time.UTC)
	model := &scriptedModel{responses: []string{
		dockerLogRequestEnvelope(start.Add(-time.Minute), start.Add(time.Minute), 2, ""),
		diagnosisEnvelope("external_dependency"),
		diagnosisEnvelope("external_dependency"),
		dockerLogRequestEnvelope(start, start.Add(time.Second), 20, "TriggerNilPointerFault"),
		diagnosisEnvelope("external_dependency"),
	}}
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	checkpoints := &fakeCheckpointStore{}
	port := &sequenceDockerEvidencePort{results: []domain.DockerLogResult{
		{
			Container: domain.DockerContainerIdentity{Name: "real-estate-api", ID: strings.Repeat("d", 64)},
			Stdout:    "tail-only\n", CoverageLimited: true, RefinementRequired: true, CoverageReason: "tail_limit", WindowLines: -1,
		},
		{
			Container: domain.DockerContainerIdentity{Name: "real-estate-api", ID: strings.Repeat("d", 64)},
			Stderr:    "panic TriggerNilPointerFault\ngoroutine 42 [running]:\n", WindowLines: -1, FilteredLines: 1,
		},
	}}
	coord := newDockerFlowCoordinator(t, store, model, port, start)
	coord.SetCheckpointStore(checkpoints)

	run, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.State != domain.RunStateCompletedNonCode || store.state != domain.RunStateCompletedNonCode {
		t.Fatalf("final state = %s/%s, want completed_non_code after recovery", run.State, store.state)
	}
	if store.countTransitionsTo(domain.RunStateBlockedManualReview) != 0 {
		t.Fatalf("docker refinement exhaustion terminalized the run: %#v", store.transitions)
	}
	if model.calls != 5 || len(port.queries) != 2 {
		t.Fatalf("model/docker calls = %d/%d, want 5/2", model.calls, len(port.queries))
	}
	// 第三次模型轮次之后追加了 exhaustion proposal 请求（第四次轮次可见）。
	if !strings.Contains(model.turns[3].UserMessage, "exhaustion_proof_required") {
		t.Fatalf("fourth turn lacks exhaustion proposal request: %s", model.turns[3].UserMessage)
	}
	assertExhaustionRecoveryRecorded(t, checkpoints)
}

// TestCoordinator_ResilientRepeatedMalformedEnvelopesRequestExhaustion 证明
// R16/R18/R21：连续 protocol failures 达到 no-progress 上限后不会直接进入
// blocked_manual_review；coordinator 请求 exhaustion proof，agent 仍可修正为
// 正常 diagnosis 并完成自动化。
func TestCoordinator_ResilientRepeatedMalformedEnvelopesRequestExhaustion(t *testing.T) {
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	checkpoints := &fakeCheckpointStore{}
	model := &scriptedModel{responses: []string{
		`not json`,
		`still not json`,
		`{"schemaVersion":"v1","kind":"diagnosis"}`,
		diagnosisEnvelope("external_dependency"),
	}}
	coord := newCoordinator(store, &fakeRepoPort{}, &fakeEvidencePort{}, model)
	coord.SetCheckpointStore(checkpoints)

	run, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if run.State != domain.RunStateCompletedNonCode || store.state != domain.RunStateCompletedNonCode {
		t.Fatalf("final state = %s/%s, want completed_non_code", run.State, store.state)
	}
	if store.countTransitionsTo(domain.RunStateBlockedManualReview) != 0 {
		t.Fatalf("protocol failures directly terminalized the run: %#v", store.transitions)
	}
	if !strings.Contains(model.turns[3].UserMessage, "exhaustion_proof_required") ||
		!strings.Contains(model.turns[1].UserMessage, `"kind":"protocol_correction"`) {
		t.Fatalf("recovery observations missing: turn2=%s turn4=%s", model.turns[1].UserMessage, model.turns[3].UserMessage)
	}
	assertExhaustionRecoveryRecorded(t, checkpoints)
}

// TestCoordinator_ResilientMalformedExhaustionProposalIsProtocolCorrection
// 证明 malformed exhaustion proposal 走严格解码与协议修正路径，而不是被静默
// 放行或直接终态化；模型随后返回有效 diagnosis 并正常完成。
func TestCoordinator_ResilientMalformedExhaustionProposalIsProtocolCorrection(t *testing.T) {
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	checkpoints := &fakeCheckpointStore{}
	model := &scriptedModel{responses: []string{
		stopEnvelope(),
		malformedExhaustionProposalEnvelope,
		diagnosisEnvelope("external_dependency"),
	}}
	coord := newCoordinator(store, &fakeRepoPort{}, &fakeEvidencePort{}, model)
	coord.SetCheckpointStore(checkpoints)

	run, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.State != domain.RunStateCompletedNonCode || store.state != domain.RunStateCompletedNonCode {
		t.Fatalf("final state = %s/%s, want completed_non_code", run.State, store.state)
	}
	if model.calls != 3 {
		t.Fatalf("model calls = %d, want 3 (stop, malformed proposal, diagnosis)", model.calls)
	}
	if !strings.Contains(model.turns[2].UserMessage, "protocol_observation") {
		t.Fatalf("third turn lacks the protocol correction observation: %s", model.turns[2].UserMessage)
	}
}

// TestCoordinator_ResilientExhaustionProofAcceptsBootstrapEvidenceRef 证明
// R1/R10/R19：exhaustion proof 可以引用首轮 bootstrap 预存证据（可信
// provider detail）的证据 ID——它们是 incident/series 已准入、在首轮渲染为
// evidence_ref 的 run-owned refs，引用归属校验不能误拒绝。与 validator 的
// “不属于 run 的 ref 被拒绝”测试互补：只有真正不属于 run 的 ref 才被拒。
func TestCoordinator_ResilientExhaustionProofAcceptsBootstrapEvidenceRef(t *testing.T) {
	limits := application.DefaultBudgetLimits()
	limits.MaxModelCalls = 2
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	checkpoints := &fakeCheckpointStore{}
	model := &scriptedModel{responses: []string{
		stopEnvelope(),
		exhaustionProposalEnvelope(
			`[]`,
			`[{"capability":"repository","reasonCode":"irrelevant","evidenceRefs":["ev-bootstrap-provider-detail"]}]`,
		),
	}}
	coord := newCoordinatorWithBudget(store, &fakeRepoPort{}, &fakeEvidencePort{}, model, limits)
	coord.SetCheckpointStore(checkpoints)
	coord.SetBootstrapEvidenceLoader(&bootstrapEvidenceLoader{value: domain.BootstrapEvidence{
		Observation: &domain.TriggeringObservation{
			ID: "obs-1", ProjectID: "p1", EnvironmentID: "e1", SourceID: "s1",
		},
		Records: []domain.StoredEvidence{{
			EvidenceID:     "ev-bootstrap-provider-detail",
			Provider:       "tencent_cls",
			EvidenceKind:   domain.EvidenceKindProviderDetail,
			Classification: domain.EvidenceDirectFault,
			Outcome:        "success",
			Available:      true,
			Primary:        true,
		}},
	}})

	run, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.State != domain.RunStateBlockedManualReview || store.state != domain.RunStateBlockedManualReview {
		t.Fatalf("final state = %s/%s, want blocked_manual_review", run.State, store.state)
	}
	if model.calls != 2 {
		t.Fatalf("model calls = %d, want 2 (stop then accepted proposal)", model.calls)
	}
	if len(store.effects) == 0 || store.effects[len(store.effects)-1].TerminalReason != "exhaustion_proof" {
		t.Fatalf("terminal effect = %#v, want exhaustion_proof", store.effects)
	}
	assertExhaustionRecoveryRecorded(t, checkpoints)
}

// TestCoordinator_ResilientExhaustionProofCannotBypassTencentDetailGate 证明
// R20：resilient_v1 下 Tencent detail 强制证据门关闭（尚未尝试 detail）时，
// exhaustion proof 与 diagnosis/stop 一样被 required_direct_evidence 拒绝；
// 模型必须先调用 evidence.tencent_cls_detail 打开目录，之后才能正常完成。
// exhaustion proof 不能绕过强制证据门直接交接人工。
func TestCoordinator_ResilientExhaustionProofCannotBypassTencentDetailGate(t *testing.T) {
	model := &scriptedModel{responses: []string{
		budgetProhibitedExhaustionProposalEnvelope(),
		fmt.Sprintf(`{"schemaVersion":"v1","kind":"requestTool","requestTool":{"toolName":%q,"parameters":{}}}`, application.ToolTencentCLSDetail),
		diagnosisEnvelope("external_dependency"),
	}}
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	checkpoints := &fakeCheckpointStore{}
	coord := application.NewRemediationCoordinatorWithReview(store, &fakeRepoPort{}, &fakeEvidencePort{}, model, nil, store, store)
	coord.SetCheckpointStore(checkpoints)
	coord.SetBootstrapEvidenceLoader(&bootstrapEvidenceLoader{value: tencentBootstrapEvidence()})
	detail := &fakeTencentDetailPort{result: tencentDetailResult()}
	coord.SetTencentCLSDetailPort(detail)

	run, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.State != domain.RunStateCompletedNonCode || store.state != domain.RunStateCompletedNonCode {
		t.Fatalf("final state = %s/%s, want completed_non_code", run.State, store.state)
	}
	if model.calls != 3 || detail.calls != 1 {
		t.Fatalf("model/detail calls = %d/%d, want 3/1", model.calls, detail.calls)
	}
	// 第一轮 exhaustion proof 被 gate 拒绝：第二轮上下文含 required_direct_evidence。
	if !strings.Contains(model.turns[1].UserMessage, "required_direct_evidence") {
		t.Fatalf("second turn missing mandatory detail correction: %s", model.turns[1].UserMessage)
	}
	if store.countTransitionsTo(domain.RunStateBlockedManualReview) != 0 {
		t.Fatalf("exhaustion proof bypassed the detail gate: %#v", store.transitions)
	}
}

// assertExhaustionRecoveryRecorded 断言 resilient 状态在 recovery checkpoint
// 里记录了 exhaustion kind 的 recovery 条目（D2 recoveries 定位记录）。
func assertExhaustionRecoveryRecorded(t *testing.T, checkpoints *fakeCheckpointStore) {
	t.Helper()
	for _, checkpoint := range checkpoints.appends {
		if checkpoint.Reason != domain.CheckpointReasonRecovery {
			continue
		}
		for _, recovery := range checkpoint.Recoveries {
			if recovery.Kind == string(domain.RecoveryChallengeKindExhaustion) {
				return
			}
		}
	}
	t.Fatalf("no recovery checkpoint records an exhaustion recovery: %#v", checkpoints.appends)
}
