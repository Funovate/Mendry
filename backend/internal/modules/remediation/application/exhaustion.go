package application

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"fixthe/backend/internal/modules/remediation/domain"
)

// exhaustionProposalRequestMessage 是服务端固定的 exhaustion proposal 请求
// 指令：完整 D6 schema、capability class 与 reason code 枚举、以及服务校验
// 规则（覆盖而非工具顺序）。它只含 schema 文本，不携带模型输出、凭据或
// connector 细节。
const exhaustionProposalRequestMessage = "You may not abandon automation directly. Before any manual handoff, " +
	"justify that all viable paths are exhausted by returning exactly one exhaustion proposal envelope: " +
	`{"schemaVersion":"v1","kind":"exhaustion","exhaustion":{"unresolvedGoal":"...",` +
	`"attemptedPaths":[{"capability":"repository","outcomeRefs":["action:repository:1"]}],` +
	`"untriedCapabilities":[{"capability":"runtime_logs","reasonCode":"irrelevant","evidenceRefs":["<owned evidence ref>"]}],` +
	`"bestConclusion":"insufficient_evidence","handoff":"..."}}. ` +
	"capability is one of repository, provider_evidence, runtime_logs, ssh_inspect, workspace, validation, publication. " +
	"reasonCode is one of unavailable, irrelevant, unsafe, budget_prohibited. " +
	"unresolvedGoal must state the unresolved goal. attemptedPaths must list every path already tried using the actionRef " +
	"returned by that capability's tool observation; a recovery challenge ref or another capability's evidence cannot prove an action. " +
	"untriedCapabilities must explain every capability not tried: irrelevant requires owned factual evidence, unavailable or unsafe " +
	"requires matching service policy state, and budget_prohibited requires the complete hard-budget projection to deny that action. " +
	"The service validates coverage, referenced refs, open recoveries, and remaining budget; it rejects incomplete proofs and " +
	"lists the uncovered capability or recovery class, and it never requires a fixed investigation order or that every tool be called."

// ExhaustionValidationContext 是服务端权威的 D6 校验输入。ActionRefs 按
// capability 绑定真实工具动作；EvidenceRefs 仅证明证据归属，不能冒充动作；
// ServiceUntriedReasons 来自 snapshotted policy/capability 状态，模型 reasonCode
// 自身不能创建 unavailable/unsafe 权威；BlockingRecoveryRefs 非空时拒绝交接。
type ExhaustionValidationContext struct {
	CatalogCapabilities   []string
	ActionRefs            map[string][]string
	EvidenceRefs          []string
	ServiceUntriedReasons map[string]domain.ExhaustionReasonCode
	BlockingRecoveryRefs  []string
	RemainingBudget       map[string]int64
}

// ValidateExhaustionProposal 是 D6 的 exhaustion proof 服务校验器（纯函数、
// 确定性、provider-neutral）。它验证结构边界、catalog coverage、真实
// capability/action 绑定、evidence ownership、service policy、未闭合 recovery
// 与完整 hard-budget 投影，不编码固定调查顺序。
func ValidateExhaustionProposal(
	proposal domain.ExhaustionProposalV1,
	validation ExhaustionValidationContext,
) (bool, domain.RecoveryChallengeV1, []string) {
	if err := proposal.Validate(); err != nil {
		challenge, buildErr := NewRecoveryChallenge(
			domain.RecoveryChallengeKindExhaustion,
			domain.RecoverySeverityRecoverable,
			"invalid_exhaustion_proposal",
			"",
			trimNonEmpty(validation.CatalogCapabilities),
			[]string{"correct_request"},
			1,
			copyBudget(validation.RemainingBudget),
			"The exhaustion proposal violates its schema or field bounds: "+boundRecoveryMessage(err.Error()),
		)
		if buildErr != nil {
			return false, domain.RecoveryChallengeV1{}, []string{err.Error()}
		}
		return false, challenge, []string{err.Error()}
	}

	reasons := exhaustionValidationReasons(proposal, validation)
	if len(reasons) > 0 {
		challenge, buildErr := NewRecoveryChallenge(
			domain.RecoveryChallengeKindExhaustion,
			domain.RecoverySeverityRecoverable,
			"exhaustion_proof_incomplete",
			"",
			trimNonEmpty(validation.CatalogCapabilities),
			[]string{"continue_investigation", "rehydrate_evidence", "correct_request"},
			1,
			copyBudget(validation.RemainingBudget),
			exhaustionIncompleteMessage(reasons),
		)
		if buildErr != nil {
			return false, domain.RecoveryChallengeV1{}, reasons
		}
		return false, challenge, reasons
	}
	return true, domain.RecoveryChallengeV1{}, nil
}

// exhaustionValidationReasons 收集 proof 的所有不完整原因；空表示接受。
// 顺序固定（coverage → action/evidence authority → policy/budget → recovery），
// 保证确定性。
func exhaustionValidationReasons(
	proposal domain.ExhaustionProposalV1,
	validation ExhaustionValidationContext,
) []string {
	attempted := make(map[string]int, len(proposal.AttemptedPaths))
	for _, path := range proposal.AttemptedPaths {
		attempted[path.Capability]++
	}
	explained := make(map[string]int, len(proposal.UntriedCapabilities))
	for _, untried := range proposal.UntriedCapabilities {
		explained[untried.Capability]++
	}

	var reasons []string
	for capability, count := range attempted {
		if count > 1 {
			reasons = append(reasons, "capability "+capability+" has duplicate attempted paths")
		}
		if explained[capability] > 0 {
			reasons = append(reasons, "capability "+capability+" is both attempted and untried")
		}
	}
	for capability, count := range explained {
		if count > 1 {
			reasons = append(reasons, "capability "+capability+" has duplicate untried explanations")
		}
	}
	for _, capability := range trimNonEmpty(validation.CatalogCapabilities) {
		if attempted[capability] == 0 && explained[capability] == 0 {
			reasons = append(reasons, "uncovered capability: "+capability)
		}
	}

	for _, path := range proposal.AttemptedPaths {
		owned := validation.ActionRefs[path.Capability]
		for _, ref := range path.OutcomeRefs {
			if !containsExhaustionRef(owned, ref) {
				reasons = append(reasons, "outcome ref "+ref+" is not a "+path.Capability+" action in this run")
			}
		}
	}
	for _, untried := range proposal.UntriedCapabilities {
		for _, ref := range untried.EvidenceRefs {
			if !containsExhaustionRef(validation.EvidenceRefs, ref) {
				reasons = append(reasons, "evidence ref "+ref+" does not belong to this run or series")
			}
		}
		reasonCode := domain.ExhaustionReasonCode(untried.ReasonCode)
		switch reasonCode {
		case domain.ExhaustionReasonBudgetProhibited:
			if capabilityBudgetAvailable(untried.Capability, validation.RemainingBudget) {
				reasons = append(reasons, "capability "+untried.Capability+" is not budget prohibited")
			}
		case domain.ExhaustionReasonUnavailable, domain.ExhaustionReasonUnsafe:
			if validation.ServiceUntriedReasons[untried.Capability] != reasonCode {
				reasons = append(reasons, "capability "+untried.Capability+" lacks matching service policy evidence for "+untried.ReasonCode)
			}
		case domain.ExhaustionReasonIrrelevant:
			// Domain validation requires factual evidence refs; ownership is checked above.
		}
	}
	for _, recovery := range validation.BlockingRecoveryRefs {
		reasons = append(reasons, "unresolved recovery: "+recovery)
	}
	return reasons
}

// capabilityBudgetAvailable 使用完整 hard-ceiling 投影判断一个 capability 是否
// 仍可执行一次模型决策加一次工具读取。不同 capability 使用各自的 byte 预算；
// cost/time/model/tool 任一维度为零都不能声称仍有可行执行容量。
func capabilityBudgetAvailable(capability string, remaining map[string]int64) bool {
	if remaining["model_calls"] <= 0 || remaining["model_cost_cents"] <= 0 ||
		remaining["tool_calls"] <= 0 || remaining["elapsed_seconds"] <= 0 {
		return false
	}
	switch capability {
	case "repository", "workspace":
		return remaining["repository_bytes"] > 0
	case "provider_evidence", "runtime_logs", "ssh_inspect":
		return remaining["evidence_bytes"] > 0
	case "validation":
		return remaining["repository_bytes"] > 0 || remaining["evidence_bytes"] > 0
	case "publication":
		return true
	default:
		return false
	}
}

// exhaustionIncompleteMessage 生成服务端固定的不完整 proof 文案：只含
// 服务端生成的原因（覆盖/recovery/预算），不携带模型输出或凭据。
func exhaustionIncompleteMessage(reasons []string) string {
	return "The exhaustion proof is incomplete: " + strings.Join(reasons, "; ") +
		". Provide the unresolved goal, every attempted path with its observation or evidence references, " +
		"and a factual, policy-backed reason for every untried capability. " +
		"The service validates coverage, not a fixed investigation order, and does not require every tool to be called."
}

// catalogCapabilityClasses 从 run 的 ToolCatalog 派生当前广告的 D1 capability
// class 集合（去重、排序）。exhaustion coverage 校验以它为准，而不是
// recoveryCapabilities 的固定提示列表。
func catalogCapabilityClasses(catalog *ToolCatalog) []string {
	if catalog == nil {
		return nil
	}
	seen := make(map[string]bool, 8)
	var out []string
	for _, def := range catalog.definitions {
		if class := toolCapabilityClass(def.Name); class != "" && !seen[class] {
			seen[class] = true
			out = append(out, class)
		}
	}
	sort.Strings(out)
	return out
}

// toolCapabilityClass 把 gateway 工具名映射到 D1 稳定 capability class；
// 未映射的动态/未知工具不参与 coverage 校验（模型上下文也只见有界描述）。
func toolCapabilityClass(name string) string {
	switch {
	case strings.HasPrefix(name, "repository."):
		return "repository"
	case name == ToolDockerLogs:
		return "runtime_logs"
	case name == ToolSSHInspect:
		return "ssh_inspect"
	case strings.HasPrefix(name, "evidence."):
		return "provider_evidence"
	case name == ToolSourceSearchTools || name == ToolSourceRefreshTools:
		return "provider_evidence"
	case name == ToolWorkspaceRunValidation:
		return "validation"
	case strings.HasPrefix(name, "workspace."):
		return "workspace"
	case strings.HasPrefix(name, "mcp_"):
		return "provider_evidence"
	default:
		return ""
	}
}

// containsExhaustionRef 报告 refs 是否包含目标引用；供 exhaustion proof 的
// 引用归属校验使用（与测试文件的 containsString 名字区分）。
func containsExhaustionRef(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// requestExhaustionProposal 在 resilient_v1 下把一个 terminalize 点改造成
// exhaustion proposal 请求：计入本次模型轮次预算、记录 D2 recovery、强制
// recovery checkpoint，然后向同一 conversation 追加 D6 请求观察并继续循环。
// legacy（tracker nil）下是 no-op，调用方必须已经用 resilientStateFrom 判断。
// 只有 global hard ceiling 才会产生 budget_exhausted；本方法只产生可恢复的
// proposal 请求。
func (c *RemediationCoordinator) requestExhaustionProposal(
	ctx context.Context,
	budget *runBudget,
	runID string,
	usage domain.ModelResult,
	conversation *AgentConversation,
	trigger string,
) error {
	tracker := resilientStateFrom(ctx)
	if tracker == nil {
		return nil
	}
	exhausted, err := c.recordSameStateBudget(ctx, budget, runID, domain.RunStateDiagnosing, modelEffect(usage))
	if err != nil || exhausted {
		return err
	}
	return c.requestExhaustionProposalAfterRecorded(ctx, budget, runID, conversation, trigger)
}

// requestExhaustionProposalAfterRecorded 在调用方已经持久化当前模型用量后追加
// exhaustion request，避免 protocol/fact recovery 转入 D6 时重复计费。
func (c *RemediationCoordinator) requestExhaustionProposalAfterRecorded(
	ctx context.Context,
	budget *runBudget,
	runID string,
	conversation *AgentConversation,
	trigger string,
) error {
	tracker := resilientStateFrom(ctx)
	if tracker == nil {
		return nil
	}
	tracker.exhaustionProposalAttempts++
	outcomeRef := "challenge:exhaustion:" + trigger
	// D2 recovery-triggered checkpoint：策略变化（请求 exhaustion proof）后
	// 强制持久化 working memory，供重启/续跑重建。
	tracker.recoveries = append(tracker.recoveries, domain.CheckpointRecovery{
		Kind:       string(domain.RecoveryChallengeKindExhaustion),
		Action:     "request_exhaustion_proposal",
		OutcomeRef: outcomeRef,
	})
	if err := c.checkpointRun(ctx, tracker, domain.RunStateDiagnosing, domain.CheckpointReasonRecovery); err != nil {
		return c.fail(ctx, runID, domain.RunStateDiagnosing, markPersistenceFailure(err))
	}
	conversation.AppendExhaustionProposalRequest(
		tracker.exhaustionProposalAttempts, tracker.recoveryCapabilities(), budget.remaining(), outcomeRef,
	)
	return nil
}

// handleExhaustionEnvelope 处理 resilient_v1 下的 exhaustion envelope：
// 计入本次模型轮次预算，用服务 validator 校验 proof；接受时持久化有界
// proposal 决策并进入 blocked_manual_review（terminal reason
// exhaustion_proof），拒绝时把不完整原因作为 recoverable exhaustion challenge
// 回喂同一循环并强制 recovery checkpoint。返回 done=true 表示已终态化。
func (c *RemediationCoordinator) handleExhaustionEnvelope(
	ctx context.Context,
	budget *runBudget,
	runID string,
	usage domain.ModelResult,
	proposal *domain.ExhaustionProposalV1,
	catalog *ToolCatalog,
	conversation *AgentConversation,
) (bool, error) {
	tracker := resilientStateFrom(ctx)
	if tracker == nil || proposal == nil {
		return false, fmt.Errorf("exhaustion envelope is only valid in a resilient run with a payload")
	}
	exhausted, err := c.recordSameStateBudget(ctx, budget, runID, domain.RunStateDiagnosing, modelEffect(usage))
	if err != nil || exhausted {
		return false, err
	}
	accepted, challenge, _ := ValidateExhaustionProposal(
		*proposal,
		tracker.exhaustionValidationContext(catalog, budget.remaining()),
	)
	if !accepted {
		attempt := tracker.exhaustionProposalAttempts
		if attempt < 1 {
			attempt = 1
		}
		challenge.Attempt = attempt
		tracker.recoveries = append(tracker.recoveries, domain.CheckpointRecovery{
			Kind: string(challenge.Kind), Action: "reject_exhaustion_proposal", OutcomeRef: "challenge:" + challenge.ReasonCode,
		})
		if err := c.checkpointRun(ctx, tracker, domain.RunStateDiagnosing, domain.CheckpointReasonRecovery); err != nil {
			return false, c.fail(ctx, runID, domain.RunStateDiagnosing, markPersistenceFailure(err))
		}
		conversation.AppendRecoveryChallenge(challenge)
		return false, nil
	}
	// 接受：持久化有界 proposal 元数据（不含原始模型轮次），再进入人工交接。
	decision := &DiagnosisOutput{
		Fixability:            proposal.BestConclusion,
		Confidence:            0,
		CausalReasoning:       proposal.UnresolvedGoal,
		RecommendedNextAction: proposal.Handoff,
	}
	if err := c.appendDecision(ctx, runID, decision); err != nil {
		return false, c.fail(ctx, runID, domain.RunStateDiagnosing, markPersistenceFailure(err))
	}
	// D4：已接受的 exhaustion proof 也记录一条 submitted-diagnosis 审计行
	// （correction kind exhaustion，decisionID 指向刚创建的 accepted decision）；
	// 只含 proposal 的有界元数据，不含原始模型轮次。
	if err := c.appendSubmittedDiagnosis(ctx, runID, domain.SubmittedDiagnosis{
		Fixability:            decision.Fixability,
		Confidence:            decision.Confidence,
		CausalReasoning:       decision.CausalReasoning,
		RecommendedNextAction: decision.RecommendedNextAction,
		Correction: domain.SubmittedDiagnosisCorrection{
			Kind:            domain.SubmittedCorrectionExhaustion,
			CorrectionCount: 1,
			Corrected:       true,
		},
	}, true); err != nil {
		return false, c.fail(ctx, runID, domain.RunStateDiagnosing, markPersistenceFailure(err))
	}
	effect := domain.Effect{TerminalReason: "exhaustion_proof"}
	exhausted, err = c.transitionBudgeted(ctx, budget, runID, domain.RunStateDiagnosing, domain.RunStateBlockedManualReview, effect)
	if err != nil || exhausted {
		return false, err
	}
	return true, c.notifyTerminal(ctx, runID, domain.RunStateBlockedManualReview, proposal.BestConclusion)
}
