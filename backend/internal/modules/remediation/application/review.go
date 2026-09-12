package application

import (
	"context"
	"regexp"
	"strings"
	"time"

	"mendry/backend/internal/modules/remediation/domain"
)

const (
	// NotificationNonCodeDiagnosed 是非代码终态的 in-console 通知 action。
	NotificationNonCodeDiagnosed = "remediation.non_code_diagnosed"
	// NotificationDiagnosisReadyForReview 是建议补丁待审的通知 action。
	NotificationDiagnosisReadyForReview = "remediation.diagnosis_ready_for_review"
	// NotificationInsufficientEvidence 是证据不足进入人工审查的通知 action。
	NotificationInsufficientEvidence = "remediation.insufficient_evidence"
	// NotificationManualReviewRequired 是其余 blocked_manual_review 终态的通知 action。
	NotificationManualReviewRequired = "remediation.manual_review_required"
	// NotificationChangeReadyForReview 表示 validated publication 已完成，
	// 但 merge/deploy 仍必须由 human 完成。
	NotificationChangeReadyForReview = "remediation.change_ready_for_review"
)

const maxReviewAttempts = 64

// CheckpointReviewReader 读取 run 最新 durable working-memory checkpoint 的
// 有界摘要。它是 CheckpointStore 的子集 port，供 GET review 在 resilient_v1
// run 上渲染 D8 recovery/checkpoint projection；legacy run 或未注入时跳过。
// 读取失败只降级为不渲染 projection，绝不阻塞 review（R23 保持可用）。
type CheckpointReviewReader interface {
	LoadLatestCheckpoint(ctx context.Context, runID string) (domain.CheckpointSnapshot, error)
}

// ReviewRecorder 把计划候选和建议 diff 写入 run，供 GET review chain 读取。
// 这是冻结 RunStore 之外的 companion port，避免改动已冻结的方法签名。
type ReviewRecorder interface {
	AppendPlans(ctx context.Context, runID string, plans []domain.RepairPlanCandidate, recommendedID string) error
	RecordSuggestedDiff(ctx context.Context, runID string, diff string) error
}

// ReviewQuery 读取单次 run 或事故当前 series key 下的最新 run。
type ReviewQuery interface {
	Get(ctx context.Context, runID string) (domain.RunAggregate, error)
	GetLatestForIncident(ctx context.Context, incidentID string, generation int64, deployedCommit string) (domain.RunAggregate, error)
}

// NotificationSink receives bounded terminal remediation notifications.
type NotificationSink interface {
	Notify(ctx context.Context, n TerminalNotification) error
}

// TerminalNotification 是终态 remediation 的有界通知载荷。
// Metadata 只允许 runId/state/fixability/kind/agentLoopMode，禁止 prompt、
// diff、凭据和原始日志。AgentLoopMode 记录 run 快照的 D9 政策模式。
type TerminalNotification struct {
	RunID      string
	IncidentID string
	Kind       string
	Summary    string
	Fixability domain.FixabilityClass
	State      domain.RunState
	// AgentLoopMode 是 run 创建时快照的项目 remediation 政策模式。
	AgentLoopMode domain.AgentLoopMode
}

// Review 是给 console GET 的无秘密 DTO，不包含 ModelResult.Content、工具载荷或凭据。
type Review struct {
	RunID          string
	SeriesID       string
	Status         domain.RunState
	Generation     int64
	DeployedCommit string
	AttemptNumber  int32
	Version        int64
	Origin         string
	TerminalReason string
	// ManualSuggestion 是 blocked_manual_review 时供值班人执行的安全建议。
	ManualSuggestion      string
	Retryable             bool
	ContinuationAvailable bool
	Attempts              []ReviewAttempt
	Budget                domain.BudgetCounters
	Diagnosis             *ReviewDiagnosis
	Plans                 []ReviewPlan
	SuggestedDiff         string
	Risk                  domain.RiskClassification
	// AgentLoopMode 是 run 创建时快照的项目 remediation 政策模式（D9）；
	// review 页显示它以便区分 legacy 与 resilient_v1 行为。
	AgentLoopMode domain.AgentLoopMode
	// AgentLoopPolicyVersion 是 run 快照的项目政策版本。
	AgentLoopPolicyVersion int64
	// Checkpoint 是可选的最新 durable checkpoint 摘要（D8）：run 处于
	// resilient_v1 且有 checkpoint 时才非 nil；不渲染原始 checkpoint 内容。
	Checkpoint *ReviewCheckpoint
	// Recovery 是可选的活动 recovery projection（D8/R23/R24）：只有 run 仍
	// 处于活动 phase 且 durable 状态显示恢复正在进行时才非 nil。终态 run 与
	// legacy run 保持 nil，人工修复面板仍只由 blocked_manual_review 呈现。
	Recovery *ReviewRecovery
}

// ReviewCheckpoint 是最新 durable checkpoint 的安全摘要（D8）。
type ReviewCheckpoint struct {
	Sequence           int64
	Phase              string
	Reason             string
	ObservedRunVersion int64
	UpdatedAt          time.Time
}

// ReviewRecovery 是活动 recovery 的安全摘要（D8/R23）：只含 kind、reason
// code、attempt 数、恢复/capability class 与预算数值投影，绝不包含原始模型
// 轮次、connector 错误文本、凭据或未截断的工具输出。
//
// Attempt 是 D8 的当前 recovery episode attempt（F3）：由 checkpoint 的
// RecoveryProgress.EpisodeRecoveryAttempts 持久化（每次 recovery 触发
// checkpoint 递增、episode 关闭后重新从 1 计数），不是跨 episode 累计的
// journal 长度；旧格式 checkpoint（无 progress 快照）才回退到 journal 长度。
//
// RemainingBudget 复用 domain.BudgetPlanProjection（D8/D23 专门为 review/API
// 安全渲染设计的纯数值投影），由 checkpoint 的 BudgetPlanRecoveryV1 经纯
// validated 构造器恢复后产生，不暴露 allocator 私有字段。
type ReviewRecovery struct {
	Active               bool
	Kind                 string
	Reason               string
	Attempt              int
	AttemptedPathClasses []string
	NextAction           string
	RemainingBudget      *domain.BudgetPlanProjection
}

// ReviewAttempt 是 review 页显示的有界 attempt history；不含 prompt、provider
// payload、credential 或 unrestricted tool output。
type ReviewAttempt struct {
	ID             string
	AttemptNumber  int32
	Status         domain.RunState
	Origin         string
	ContextVersion int64
	TerminalReason string
	Retryable      bool
	Version        int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ReviewDiagnosis 是最新一条结构化诊断的安全投影。
type ReviewDiagnosis struct {
	Fixability            domain.FixabilityClass
	Confidence            float64
	CausalReasoning       string
	EvidenceRefs          []string
	Contradictions        []string
	MissingEvidence       []string
	RecommendedNextAction string
	EvidenceAssessment    *domain.EvidenceGateDecision
}

// ReviewPlan 是候选修复计划的安全投影。
type ReviewPlan struct {
	PlanID           string
	IntendedBehavior string
	Risk             domain.RiskClassification
	Rationale        string
	EvidenceRefs     []string
	AffectedFiles    []string
	RollbackStrategy string
	Recommended      bool
}

// NotificationKindForTerminal 按 PRD 名称选择终态通知；failed/budget_exhausted 返回空。
func NotificationKindForTerminal(state domain.RunState, fixability domain.FixabilityClass) string {
	switch state {
	case domain.RunStateCompletedNonCode:
		return NotificationNonCodeDiagnosed
	case domain.RunStateDiagnosisReadyForReview:
		return NotificationDiagnosisReadyForReview
	case domain.RunStateAwaitingHumanReview:
		return NotificationChangeReadyForReview
	case domain.RunStateBlockedManualReview:
		if fixability == domain.FixabilityInsufficientEvidence {
			return NotificationInsufficientEvidence
		}
		return NotificationManualReviewRequired
	default:
		return ""
	}
}

func notificationSummary(kind string) string {
	switch kind {
	case NotificationNonCodeDiagnosed:
		return "Remediation diagnosed a non-code cause."
	case NotificationDiagnosisReadyForReview:
		return "Remediation diagnosis is ready for review."
	case NotificationChangeReadyForReview:
		return "Remediation change is ready for human review."
	case NotificationInsufficientEvidence:
		return "Remediation needs more evidence."
	case NotificationManualReviewRequired:
		return "Remediation requires manual review."
	default:
		return "Remediation reached a reviewable outcome."
	}
}

func buildReview(agg domain.RunAggregate) Review {
	review := Review{
		RunID:          agg.Run.RunID,
		SeriesID:       agg.Run.SeriesID,
		Status:         agg.Run.State,
		Generation:     agg.Run.LifecycleGeneration,
		DeployedCommit: sanitizeReviewText(agg.Run.DeployedCommit),
		AttemptNumber:  agg.Run.AttemptNumber,
		Version:        agg.Run.Version,
		Origin:         safeReviewOrigin(agg.Run.Origin, agg.Run.TriggerReason),
		TerminalReason: safeReviewTerminalReason(agg.Run.TerminalReason),
		Retryable:      agg.Run.Retryable,
		Attempts:       make([]ReviewAttempt, 0, min(len(agg.AttemptSummaries), maxReviewAttempts)),
		Budget:         agg.Run.Budget,
		Plans:          make([]ReviewPlan, 0, len(agg.Plans)),
		SuggestedDiff:  sanitizeSuggestedDiff(agg.SuggestedDiff),
	}
	summaries := agg.AttemptSummaries
	if len(summaries) > maxReviewAttempts {
		summaries = summaries[len(summaries)-maxReviewAttempts:]
	}
	for _, summary := range summaries {
		review.Attempts = append(review.Attempts, mapReviewAttempt(summary))
	}
	if len(review.Attempts) == 0 && review.RunID != "" {
		review.Attempts = append(review.Attempts, reviewAttemptFromRun(agg.Run))
	}
	if len(agg.Decisions) > 0 {
		latest := agg.Decisions[len(agg.Decisions)-1]
		review.ManualSuggestion = sanitizeReviewText(latest.RecommendedNextAction)
		review.Diagnosis = &ReviewDiagnosis{
			Fixability:            latest.Fixability,
			Confidence:            latest.Confidence,
			CausalReasoning:       sanitizeReviewText(latest.CausalReasoning),
			EvidenceRefs:          sanitizeReviewList(latest.EvidenceCitations),
			Contradictions:        sanitizeReviewList(latest.Contradictions),
			MissingEvidence:       sanitizeReviewList(latest.MissingEvidence),
			RecommendedNextAction: sanitizeReviewText(latest.RecommendedNextAction),
			EvidenceAssessment:    sanitizeEvidenceAssessment(latest.EvidenceAssessment),
		}
	}
	for _, plan := range agg.Plans {
		mapped := ReviewPlan{
			PlanID:           sanitizeReviewText(plan.PlanID),
			IntendedBehavior: sanitizeReviewText(plan.IntendedBehavior),
			Risk:             plan.Risk,
			Rationale:        sanitizeReviewText(plan.Rationale),
			EvidenceRefs:     sanitizeReviewList(plan.EvidenceRefs),
			AffectedFiles:    sanitizeReviewList(plan.AffectedFiles),
			RollbackStrategy: sanitizeReviewText(plan.RollbackStrategy),
			Recommended:      plan.Recommended,
		}
		review.Plans = append(review.Plans, mapped)
		if mapped.Recommended {
			review.Risk = mapped.Risk
		}
	}
	if review.Risk == "" && agg.RecommendedPlanID != "" {
		for _, plan := range review.Plans {
			if plan.PlanID == agg.RecommendedPlanID {
				review.Risk = plan.Risk
				break
			}
		}
	}
	review.AgentLoopMode = domain.ParseAgentLoopMode(string(agg.Run.AgentLoopMode))
	review.AgentLoopPolicyVersion = max(agg.Run.AgentLoopPolicyVersion, 0)
	return review
}

// attachReviewCheckpoint 把最新 durable checkpoint 的安全摘要附加到 review。
// Checkpoint 与 Recovery 都是可选的 additive projection：任何校验失败都只
// 跳过该 projection，绝不让 review GET 因为 checkpoint 内容异常而失败
// （D8/R23 review 保持可用；corrupt checkpoint 由 coordinator 在 durable
// 边界 fail closed）。
func attachReviewCheckpoint(review *Review, snapshot domain.CheckpointSnapshot) {
	// F4：只有属于该 run 的 durable checkpoint 才允许附加 projection。加载路径
	// 以 agg.Run.RunID 读取，身份不匹配说明 reader/存储返回了错误快照，必须
	// 拒绝而不是渲染另一 run 的恢复状态。
	if review == nil || review.RunID == "" || snapshot.RunID == "" || snapshot.RunID != review.RunID {
		return
	}
	checkpoint := &ReviewCheckpoint{
		Sequence:           max(snapshot.Sequence, 0),
		Phase:              sanitizeReviewText(snapshot.Phase),
		Reason:             sanitizeCheckpointReason(snapshot.Checkpoint.Reason),
		ObservedRunVersion: max(snapshot.ObservedRunVersion, 0),
		UpdatedAt:          snapshot.UpdatedAt,
	}
	review.Checkpoint = checkpoint
	if !isActiveRunState(review.Status) {
		// R24：只有活动 run 才可能处于 recovery；终态只走手动修复/人工评审面板。
		return
	}
	recovery := reviewRecoveryProjection(snapshot)
	if recovery == nil {
		return
	}
	review.Recovery = recovery
}

// reviewRecoveryProjection 从 durable checkpoint 构建 bounded recovery 摘要。
// “活动 recovery”定义为：run 处于活动 phase（调用方已保证）且最新 checkpoint
// 由 recovery 触发（reason=recovery）。recovery journal/no-progress 计数只是
// audit 历史：run 一旦写出后续 phase_boundary/threshold checkpoint（如进入
// planning 或终态前），即便 journal 仍在也不再渲染“恢复中”（R24）。所有文本都
// 经 sanitize；数字只含 low-cardinality 计数与预算维度。
func reviewRecoveryProjection(snapshot domain.CheckpointSnapshot) *ReviewRecovery {
	checkpoint := snapshot.Checkpoint
	if checkpoint.Reason != domain.CheckpointReasonRecovery {
		return nil
	}
	projection := &ReviewRecovery{Active: true, AttemptedPathClasses: []string{}}
	if latest, ok := latestCheckpointRecovery(checkpoint.Recoveries); ok {
		projection.Kind = sanitizeReviewText(latest.Kind)
		projection.Reason = sanitizeReviewText(recoveryReasonCode(latest.OutcomeRef, latest.Action))
	}
	// D8 attempt = 当前 recovery episode 的持久化 attempt（F3）。progress 快照
	// 缺失（pre-change v1 旧格式）时才回退到 journal 长度作为保守近似。
	projection.Attempt = len(checkpoint.Recoveries)
	if progress := checkpoint.RecoveryProgress; progress != nil {
		projection.Attempt = max(progress.EpisodeRecoveryAttempts, 0)
	}
	projection.AttemptedPathClasses = reviewAttemptedPathClasses(checkpoint.Recoveries)
	projection.NextAction = firstCheckpointNextAction(checkpoint.NextActions)
	if alloc, err := restorePhaseBudgetPlan(checkpoint.Budget); err == nil {
		if budget := alloc.Projection(); budget.Validate() == nil {
			projection.RemainingBudget = &budget
		}
	}
	return projection
}

// latestCheckpointRecovery 返回 journal 最后一条 recovery 记录；journal 有界。
func latestCheckpointRecovery(recoveries []domain.CheckpointRecovery) (domain.CheckpointRecovery, bool) {
	if len(recoveries) == 0 {
		return domain.CheckpointRecovery{}, false
	}
	return recoveries[len(recoveries)-1], true
}

// recoveryReasonCode 从 recovery outcome ref 或 action 提取有界 reason code；
// ref 形如 challenge:<code>[:<sha256>]，hash 部分绝不展示。
func recoveryReasonCode(outcomeRef, action string) string {
	if strings.HasPrefix(outcomeRef, "challenge:") {
		parts := strings.Split(outcomeRef, ":")
		if len(parts) >= 2 && strings.TrimSpace(parts[1]) != "" {
			return parts[1]
		}
	}
	if strings.TrimSpace(action) != "" && strings.TrimSpace(action) != "model_turn" {
		return action
	}
	return ""
}

// reviewAttemptedPathClasses 从 recovery journal 派生去重的有界 path class 集合：
// 工具 action 映射到 D1 capability class；非工具 action 按 recovery class 保留。
func reviewAttemptedPathClasses(recoveries []domain.CheckpointRecovery) []string {
	seen := make(map[string]bool, 8)
	var out []string
	for _, recovery := range recoveries {
		class := toolCapabilityClass(recovery.Action)
		if class == "" {
			class = reviewRecoveryClass(recovery.Kind, recovery.Action)
		}
		if class == "" || seen[class] {
			continue
		}
		seen[class] = true
		out = append(out, class)
	}
	return out
}

// reviewRecoveryClass 为非工具 action 的 journal 条目派生稳定 label：lifecycle
// 前缀/发布/验证修订归入 lifecycle，证据/协议/预算修正按 recovery kind 保留，
// 未知 kind 保守忽略。
func reviewRecoveryClass(kind, action string) string {
	switch {
	case strings.HasPrefix(action, "lifecycle:") || action == "revise_patch" ||
		action == "stop" || action == "run_validation" || action == "retry_publication" ||
		action == "inspect_workspace" || action == "apply_changed_patch":
		return "lifecycle"
	case action == "correct_citation" || action == "correct_fact_check":
		return string(domain.RecoveryChallengeKindEvidenceCorrection)
	case action == "correct_envelope":
		return string(domain.RecoveryChallengeKindProtocolCorrection)
	case action == "reject_exhaustion_proposal":
		return string(domain.RecoveryChallengeKindExhaustion)
	default:
		if strings.TrimSpace(kind) == "" {
			return ""
		}
		return kind
	}
}

// firstCheckpointNextAction 返回 checkpoint 的首条 next action；只取有界单条。
func firstCheckpointNextAction(actions []string) string {
	for _, action := range actions {
		if cleaned := sanitizeReviewText(action); cleaned != "" {
			return cleaned
		}
	}
	return ""
}

// sanitizeCheckpointReason 只透传已知 checkpoint reason，其余一律清空。
func sanitizeCheckpointReason(reason string) string {
	switch reason {
	case domain.CheckpointReasonThreshold, domain.CheckpointReasonPhaseBoundary,
		domain.CheckpointReasonRecovery, domain.CheckpointReasonProcessShutdown:
		return reason
	default:
		return ""
	}
}

func mapReviewAttempt(summary domain.AttemptSummary) ReviewAttempt {
	id := strings.TrimSpace(summary.ID)
	if id == "" {
		id = strings.TrimSpace(summary.RunID)
	}
	return ReviewAttempt{
		ID:             sanitizeReviewText(id),
		AttemptNumber:  summary.AttemptNumber,
		Status:         domain.RunState(safeContinuationState(summary.Status)),
		Origin:         safeReviewOrigin(summary.Origin),
		ContextVersion: max(summary.ContextVersion, 0),
		TerminalReason: safeReviewTerminalReason(summary.TerminalReason),
		Retryable:      summary.Retryable,
		Version:        max(summary.Version, 0),
		CreatedAt:      summary.CreatedAt,
		UpdatedAt:      summary.UpdatedAt,
	}
}

func reviewAttemptFromRun(run domain.Run) ReviewAttempt {
	return ReviewAttempt{
		ID:             sanitizeReviewText(run.RunID),
		AttemptNumber:  run.AttemptNumber,
		Status:         run.State,
		Origin:         safeReviewOrigin(run.Origin, run.TriggerReason),
		ContextVersion: max(run.ContextVersion, 0),
		TerminalReason: safeReviewTerminalReason(run.TerminalReason),
		Retryable:      run.Retryable,
		Version:        max(run.Version, 0),
		CreatedAt:      run.CreatedAt,
		UpdatedAt:      run.UpdatedAt,
	}
}

func safeReviewOrigin(values ...string) string {
	return safeContinuationOrigin(values...)
}

func safeReviewTerminalReason(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return safeContinuationReasonCode(value)
}

func aggregateHasActiveAttempt(aggregate domain.RunAggregate) bool {
	if isActiveRunState(aggregate.Run.State) {
		return true
	}
	for _, summary := range aggregate.AttemptSummaries {
		if isActiveRunState(summary.Status) {
			return true
		}
	}
	return false
}

func isActiveRunState(state domain.RunState) bool {
	switch state {
	case domain.RunStateQueued, domain.RunStatePreparingContext, domain.RunStateDiagnosing,
		domain.RunStateCollectingMoreContext, domain.RunStatePlanning, domain.RunStateRunning,
		domain.RunStatePatching, domain.RunStateValidating, domain.RunStatePublishing:
		return true
	default:
		return false
	}
}

func isManualContinuationState(state domain.RunState) bool {
	switch state {
	case domain.RunStateFailed, domain.RunStateBudgetExhausted, domain.RunStateBlockedManualReview:
		return true
	default:
		return false
	}
}

func sanitizeEvidenceAssessment(value *domain.EvidenceGateDecision) *domain.EvidenceGateDecision {
	if value == nil {
		return nil
	}
	return &domain.EvidenceGateDecision{
		ConfidenceCap:       value.ConfidenceCap,
		EffectiveConfidence: value.EffectiveConfidence,
		PlanningEligible:    value.PlanningEligible,
		Outcome:             value.Outcome,
		Reasons:             sanitizeReviewList(value.Reasons),
		MissingEvidence:     sanitizeReviewList(value.MissingEvidence),
		Contradictions:      sanitizeReviewList(value.Contradictions),
		DirectEvidenceIDs:   sanitizeReviewList(value.DirectEvidenceIDs),
	}
}

func sanitizeReviewList(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || isCredentialLiteral(value) {
			continue
		}
		if cleaned := sanitizeReviewText(value); cleaned != "" {
			out = append(out, cleaned)
		}
	}
	return out
}

func sanitizeReviewText(value string) string {
	return redactConversationText(redactSensitiveValues(strings.TrimSpace(value)))
}

// sanitizeSuggestedDiff 只遮蔽凭据字面量和 authenticated URL，绝不因 diff 里出现 token/secret
// 这类普通英文就丢弃整份建议补丁。
func sanitizeSuggestedDiff(value string) string {
	return redactConversationText(redactSensitiveValues(value))
}

// NotificationMetadata 返回终态通知的白名单投影。
// 只允许 runId/state/fixability/kind/agentLoopMode，禁止 prompt、diff、凭据
// 和原始日志。agentLoopMode 空值（旧路径）时省略，保持既有审计行不变。
func NotificationMetadata(n TerminalNotification) map[string]string {
	metadata := map[string]string{
		"runId":      n.RunID,
		"state":      string(n.State),
		"fixability": string(n.Fixability),
		"kind":       n.Kind,
	}
	if mode := domain.ParseAgentLoopMode(string(n.AgentLoopMode)); mode.IsKnown() {
		metadata["agentLoopMode"] = string(mode)
	}
	return metadata
}

var (
	openAIKeyPattern = regexp.MustCompile(`(?i)sk-[A-Za-z0-9_-]{8,}`)
	bearerPattern    = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._\-+/=]{8,}`)
	assignedSecret   = regexp.MustCompile(`(?i)\b(password|token|secret|authorization|api[_-]?key|credential|value)\s*[:=]\s*(?:"[^"]*"|'[^']*'|[^\s,;]+)`)
	pemBlockPattern  = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`)
)

// redactSensitiveValues 就地替换凭据字面量。讨论 token/secret 的普通诊断文本必须保留。
func redactSensitiveValues(value string) string {
	if value == "" {
		return ""
	}
	value = pemBlockPattern.ReplaceAllString(value, "[redacted]")
	value = openAIKeyPattern.ReplaceAllString(value, "[redacted]")
	value = bearerPattern.ReplaceAllString(value, "bearer [redacted]")
	value = assignedSecret.ReplaceAllString(value, "[redacted]")
	return value
}

func isCredentialLiteral(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	if openAIKeyPattern.MatchString(trimmed) && openAIKeyPattern.FindString(trimmed) == trimmed {
		return true
	}
	if pemBlockPattern.MatchString(trimmed) {
		return true
	}
	lower := strings.ToLower(trimmed)
	return strings.Contains(lower, "provider_payload") || strings.Contains(lower, "providerpayload")
}
