package application

import (
	"context"
	"fmt"
	"path"
	"strings"

	"fixthe/backend/internal/modules/remediation/domain"
)

// StaticPlanPolicy 是当前 project policy 的最小只读快照。
// 普通业务源码默认允许；高风险变更需要显式 opt-in 和至少一个 approved
// validation command；CI/CD、部署、IaC、凭据和 binary 路径始终拒绝自动发布。
type StaticPlanPolicy struct {
	AllowHighRisk         bool
	ApprovedValidationIDs []string
}

// NewDefaultPlanPolicy 返回保守的 resilient_v1 默认 plan policy。
func NewDefaultPlanPolicy() StaticPlanPolicy {
	return StaticPlanPolicy{}
}

// EvaluatePlan 对候选计划做确定性的 path/risk/validation 检查，不决定因果结论。
// 推荐候选不合规但存在可用替代项时返回 recoverable feedback，让同一模型循环
// 重新选择；只有已确认的 denied control-plane 才返回 policy_blocked。
func (p StaticPlanPolicy) EvaluatePlan(_ context.Context, input domain.PlanPolicyInput) (domain.PlanPolicyDecision, error) {
	if strings.TrimSpace(input.RunID) == "" || strings.TrimSpace(input.BaselineCommit) == "" {
		return domain.PlanPolicyDecision{}, fmt.Errorf("plan policy run and baseline are required")
	}
	if len(input.Candidates) == 0 || strings.TrimSpace(input.RecommendedID) == "" {
		return domain.PlanPolicyDecision{}, fmt.Errorf("plan policy candidates and recommendation are required")
	}

	decision := domain.PlanPolicyDecision{
		Severity:        domain.RecoverySeverityRecoverable,
		AcceptedPlanIDs: make([]string, 0, len(input.Candidates)),
		RejectedPlanIDs: make([]string, 0, len(input.Candidates)),
	}
	denied := false
	highRiskRejected := false
	for _, candidate := range input.Candidates {
		if strings.TrimSpace(candidate.PlanID) == "" {
			return domain.PlanPolicyDecision{}, fmt.Errorf("plan policy candidate id is required")
		}
		candidateRisk, reason := classifyPlanCandidate(candidate)
		if reason != "" {
			decision.RejectedPlanIDs = append(decision.RejectedPlanIDs, candidate.PlanID)
			if candidateRisk == domain.RiskDeniedControlPlane {
				denied = true
			}
			if candidateRisk == domain.RiskHighRisk {
				highRiskRejected = true
			}
			continue
		}
		if candidateRisk == domain.RiskHighRisk {
			if !p.AllowHighRisk {
				decision.RejectedPlanIDs = append(decision.RejectedPlanIDs, candidate.PlanID)
				highRiskRejected = true
				continue
			}
			if len(p.ApprovedValidationIDs) == 0 {
				decision.RejectedPlanIDs = append(decision.RejectedPlanIDs, candidate.PlanID)
				highRiskRejected = true
				continue
			}
			decision.RequiredValidationIDs = append([]string(nil), p.ApprovedValidationIDs...)
		}
		decision.AcceptedPlanIDs = append(decision.AcceptedPlanIDs, candidate.PlanID)
	}

	if containsPlanID(decision.AcceptedPlanIDs, input.RecommendedID) {
		decision.Accepted = true
		decision.SelectedPlanID = input.RecommendedID
		decision.ReasonCode = "plan_policy_accepted"
		decision.Message = "The recommended plan satisfies the current change policy."
		return decision, decision.Validate()
	}

	decision.Accepted = false
	if denied {
		decision.Severity = domain.RecoverySeverityPolicyBlocked
		decision.ReasonCode = "denied_control_plane_change"
		decision.Message = "The proposed change includes a denied control-plane, deployment, infrastructure, credential, hook, submodule, or binary path. Keep it for manual review; do not apply or publish it automatically."
	} else if highRiskRejected {
		decision.ReasonCode = "high_risk_policy_requires_opt_in"
		decision.Message = "The recommended plan is high risk or lacks the required approved validation policy. Select an allowed plan or provide the configured specialized validation through project policy."
	} else if len(decision.AcceptedPlanIDs) > 0 {
		decision.ReasonCode = "recommended_plan_not_allowed"
		decision.Message = "The recommended plan is not allowed by the current policy. Choose one of the service-approved candidate plan IDs."
	} else {
		decision.ReasonCode = "no_policy_compliant_plan"
		decision.Message = "No candidate plan is currently policy compliant. Revise the affected paths and risk classification, or hand the change to a human reviewer."
	}
	return decision, decision.Validate()
}

func classifyPlanCandidate(candidate domain.RepairPlanCandidate) (domain.RiskClassification, string) {
	risk := candidate.Risk
	if risk != domain.RiskOrdinary && risk != domain.RiskHighRisk && risk != domain.RiskDeniedControlPlane {
		return domain.RiskDeniedControlPlane, "unknown_risk"
	}
	inferred := domain.RiskOrdinary
	for _, file := range candidate.AffectedFiles {
		pathRisk, pathReason := classifyPlanPath(file)
		if pathReason != "" {
			return pathRisk, pathReason
		}
		if pathRisk == domain.RiskDeniedControlPlane {
			return pathRisk, "denied_path"
		}
		if pathRisk == domain.RiskHighRisk {
			inferred = domain.RiskHighRisk
		}
	}
	if risk == domain.RiskDeniedControlPlane {
		return risk, "model_declared_denied_risk"
	}
	if risk == domain.RiskHighRisk || inferred == domain.RiskHighRisk {
		return domain.RiskHighRisk, ""
	}
	return domain.RiskOrdinary, ""
}

func classifyPlanPath(value string) (domain.RiskClassification, string) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "..") {
		return domain.RiskDeniedControlPlane, "path_out_of_scope"
	}
	clean := path.Clean(value)
	if clean == "." || clean != value {
		return domain.RiskDeniedControlPlane, "path_out_of_scope"
	}
	lower := strings.ToLower(clean)
	deniedFragments := []string{
		".github/workflows/", ".gitlab-ci", ".circleci/", ".buildkite/",
		"/deploy/", "deploy/", "deployment", "kubernetes/", "k8s/", "helm/", "terraform/", "pulumi/",
		"infrastructure/", "infra/", ".git/hooks/", "secret", "credential",
	}
	for _, fragment := range deniedFragments {
		if strings.Contains(lower, fragment) {
			return domain.RiskDeniedControlPlane, "denied_path"
		}
	}
	if strings.HasSuffix(lower, ".pem") || strings.HasSuffix(lower, ".key") || strings.HasSuffix(lower, ".p12") || strings.HasSuffix(lower, ".pfx") {
		return domain.RiskDeniedControlPlane, "credential_path"
	}
	for _, fragment := range []string{"migrations/", "migration/", "internal/modules/auth/", "/auth/", "go.mod", "go.sum", "package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "requirements.txt", "poetry.lock", "uv.lock", ".csproj", "packages.lock.json", "pom.xml", "build.gradle"} {
		if strings.Contains(lower, fragment) {
			return domain.RiskHighRisk, ""
		}
	}
	for _, extension := range []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".ico", ".so", ".dll", ".exe", ".class", ".jar", ".pyc"} {
		if strings.HasSuffix(lower, extension) {
			return domain.RiskDeniedControlPlane, "binary_path"
		}
	}
	return domain.RiskOrdinary, ""
}

func containsPlanID(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

const maxPlanPolicyFeedbackAttempts = 3

func repairPlanCandidates(output *PlanCandidatesOutput) []domain.RepairPlanCandidate {
	if output == nil {
		return nil
	}
	candidates := make([]domain.RepairPlanCandidate, 0, len(output.Candidates))
	for _, candidate := range output.Candidates {
		candidates = append(candidates, domain.RepairPlanCandidate{
			PlanID: candidate.PlanID, EvidenceRefs: append([]string(nil), candidate.EvidenceRefs...),
			AffectedFiles: append([]string(nil), candidate.AffectedFiles...), IntendedBehavior: candidate.IntendedBehavior,
			Risk: domain.RiskClassification(candidate.Risk), RollbackStrategy: candidate.RollbackStrategy,
			Rationale: output.Rationale, Recommended: candidate.PlanID == output.RecommendedID,
		})
	}
	return candidates
}

// handlePlanPolicyDecision 把 policy/risk feedback 回喂 planning conversation。
// recoverable feedback 不改变 run state；重复相同拒绝达到上限后才作为已验证
// policy blocker 交给人工审查，避免无限模型循环。
func (c *RemediationCoordinator) handlePlanPolicyDecision(
	ctx context.Context,
	budget *runBudget,
	runID string,
	usage domain.ModelResult,
	decision domain.PlanPolicyDecision,
	conversation *AgentConversation,
) (bool, error) {
	tracker := resilientStateFrom(ctx)
	if tracker == nil || decision.Accepted {
		return false, nil
	}
	if err := decision.Validate(); err != nil {
		return false, c.fail(ctx, runID, domain.RunStatePlanning, markPersistenceFailure(fmt.Errorf("validate plan policy decision: %w", err)))
	}
	fingerprint := recoveryFingerprint(domain.FailureFingerprint{
		FailedActionRef: decision.ReasonCode + "\x00" + strings.Join(decision.RejectedPlanIDs, "\x00"),
		Capability:      "workspace",
		ErrorCode:       "plan_policy",
	}.Key())
	tracker.planFeedbackAttempts++
	if fingerprint == tracker.lastPlanFeedbackFingerprint {
		tracker.planFeedbackNoProgress++
	} else {
		tracker.lastPlanFeedbackFingerprint = fingerprint
		tracker.planFeedbackNoProgress = 1
	}
	severity := decision.Severity
	if severity == "" {
		severity = domain.RecoverySeverityRecoverable
	}
	if tracker.planFeedbackNoProgress >= maxPlanPolicyFeedbackAttempts {
		severity = domain.RecoverySeverityPolicyBlocked
		decision.ReasonCode = "plan_policy_no_progress"
		decision.Message = "The agent repeated an unchanged policy-incompatible plan. The service closed this path; a human must review the proposed change."
	}

	exhausted, err := c.recordSameStateBudget(ctx, budget, runID, domain.RunStatePlanning, modelEffect(usage))
	if err != nil || exhausted {
		return true, err
	}
	if severity == domain.RecoverySeverityPolicyBlocked || severity == domain.RecoverySeverityHardTerminal {
		effect := domain.Effect{TerminalReason: safePlanPolicyReason(decision.ReasonCode)}
		if _, transitionErr := c.transitionBudgeted(ctx, budget, runID, domain.RunStatePlanning, domain.RunStateBlockedManualReview, effect); transitionErr != nil {
			return true, transitionErr
		}
		return true, c.notifyTerminal(ctx, runID, domain.RunStateBlockedManualReview, domain.FixabilityUnsafeToAutomate)
	}

	message := decision.Message
	if len(decision.AcceptedPlanIDs) > 0 {
		message += " Service-approved alternatives: " + strings.Join(decision.AcceptedPlanIDs, ", ") + "."
	}
	challenge, err := NewRecoveryChallenge(
		domain.RecoveryChallengeKindValidationRevision,
		domain.RecoverySeverityRecoverable,
		decision.ReasonCode,
		"planCandidates",
		[]string{"repository", "workspace", "validation"},
		[]string{"revise_plan", "select_alternative"},
		tracker.planFeedbackAttempts,
		budget.remaining(),
		message,
	)
	if err != nil {
		return true, c.fail(ctx, runID, domain.RunStatePlanning, markPersistenceFailure(fmt.Errorf("build plan policy challenge: %w", err)))
	}
	tracker.appendRecovery(domain.CheckpointRecovery{
		Kind: string(challenge.Kind), Action: "revise_plan", OutcomeRef: recoveryProgressRef(challenge.ReasonCode, fingerprint),
	})
	target := conversation
	if tracker.conversation != nil {
		target = tracker.conversation
	}
	c.appendRecoveryChallenge(ctx, domain.RunStatePlanning, target, challenge)
	if err := c.checkpointRun(ctx, tracker, domain.RunStatePlanning, domain.CheckpointReasonRecovery); err != nil {
		return true, c.fail(ctx, runID, domain.RunStatePlanning, markPersistenceFailure(err))
	}
	return false, nil
}

func safePlanPolicyReason(reason string) string {
	switch reason {
	case "denied_control_plane_change", "high_risk_policy_requires_opt_in", "plan_policy_no_progress", "no_policy_compliant_plan":
		return reason
	default:
		return "plan_policy_blocked"
	}
}
