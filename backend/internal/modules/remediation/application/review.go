package application

import (
	"context"
	"regexp"
	"strings"
	"time"

	"fixthe/backend/internal/modules/remediation/domain"
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

// NotificationSink 把终态通知写成 durable audit_events，供既有 Audit 页读取。
type NotificationSink interface {
	Notify(ctx context.Context, n TerminalNotification) error
}

// TerminalNotification 是系统 actor 写入 audit_events 的有界载荷。
// Metadata 只允许 runId/state/fixability/kind，禁止 prompt、diff、凭据和原始日志。
type TerminalNotification struct {
	RunID      string
	IncidentID string
	Kind       string
	Summary    string
	Fixability domain.FixabilityClass
	State      domain.RunState
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
	return review
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

// NotificationMetadata 是写入 audit_events.metadata 的白名单投影。
// 只允许 runId/state/fixability/kind，禁止 prompt、diff、凭据和原始日志。
func NotificationMetadata(n TerminalNotification) map[string]string {
	return map[string]string{
		"runId":      n.RunID,
		"state":      string(n.State),
		"fixability": string(n.Fixability),
		"kind":       n.Kind,
	}
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
