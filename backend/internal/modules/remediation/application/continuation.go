package application

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"fixthe/backend/internal/modules/remediation/domain"
)

const (
	maxContinuationBriefBytes = 32 << 10
	maxContinuationTextBytes  = 2048
	maxContinuationListItems  = 32
	maxContinuationEvidence   = 16
	// maxContinuationEvidenceIndex 是同 series 证据索引的条目上限。索引条目远小于
	// runtime 全量记录，且与 runtime 记录共享 maxContinuationBriefBytes 总量预算。
	maxContinuationEvidenceIndex = 32
)

const continuationHypothesisInstruction = "Prior conclusions are hypotheses only. Verify, overturn, or extend them with current bootstrap evidence before acting; current bootstrap evidence is authoritative for this attempt."

type continuationBriefEnvelope struct {
	Kind               string                    `json:"kind"`
	PreviousAttempt    continuationAttempt       `json:"previousAttempt"`
	PlanningCheckpoint *continuationAttempt      `json:"planningCheckpoint,omitempty"`
	PriorDiagnoses     []continuationDiagnosis   `json:"priorDiagnoses"`
	PriorPlans         []continuationPlan        `json:"priorPlans"`
	SuggestedDiff      continuationDiffSummary   `json:"suggestedDiff"`
	ArtifactRefs       []string                  `json:"artifactRefs"`
	ToolOutcomes       []continuationToolOutcome `json:"toolOutcomes"`
	Continuation       continuationDetails       `json:"continuation"`
}

type continuationAttempt struct {
	RunID               string `json:"runId"`
	AttemptNumber       int32  `json:"attemptNumber"`
	State               string `json:"state"`
	Origin              string `json:"origin"`
	ContinuationOfRunID string `json:"continuationOfRunId,omitempty"`
	TerminalReason      string `json:"terminalReason"`
	Retryable           bool   `json:"retryable"`
	Version             int64  `json:"version"`
	ContextVersion      int64  `json:"contextVersion"`
}

type continuationDiagnosis struct {
	Fixability            string   `json:"fixability"`
	Confidence            float64  `json:"confidence"`
	CausalReasoning       string   `json:"causalReasoning"`
	EvidenceIDs           []string `json:"evidenceIds"`
	MissingEvidence       []string `json:"missingEvidence"`
	Contradictions        []string `json:"contradictions"`
	RecommendedNextAction string   `json:"recommendedNextAction"`
}

type continuationPlan struct {
	PlanID           string   `json:"planId"`
	AffectedFiles    []string `json:"affectedFiles"`
	Risk             string   `json:"risk"`
	RollbackStrategy string   `json:"rollbackStrategy"`
	Rationale        string   `json:"rationale"`
	EvidenceRefs     []string `json:"evidenceRefs"`
}

type continuationDiffSummary struct {
	Available bool `json:"available"`
	Bytes     int  `json:"bytes"`
}

type continuationToolOutcome struct {
	Name       string `json:"name"`
	Phase      string `json:"phase"`
	Outcome    string `json:"outcome"`
	OutcomeRef string `json:"outcomeRef,omitempty"`
	ErrorCode  string `json:"errorCode,omitempty"`
	Bytes      int64  `json:"bytes"`
}

type continuationDetails struct {
	Reason                string `json:"reason"`
	CurrentContextVersion int64  `json:"currentContextVersion"`
	Instruction           string `json:"instruction"`
}

// renderContinuationRuntimeEvidence 把同 series 早期 attempt 的 sanitized runtime
// evidence 渲染进 diagnosis continuation 上下文，保留原始证据 ID 供引用。payload
// 来自 canonical 投影（持久化前已脱敏裁剪），这里只做总量裁剪；跨 series/未来
// attempt 的行由 store 查询排除，不会到达此处。
func renderContinuationRuntimeEvidence(records []domain.StoredEvidence) string {
	if len(records) == 0 {
		return ""
	}
	if len(records) > maxContinuationEvidence {
		records = records[:maxContinuationEvidence]
	}
	type priorEvidenceRecord struct {
		EvidenceID     string          `json:"evidenceId"`
		Provider       string          `json:"provider"`
		Kind           string          `json:"kind"`
		Classification string          `json:"classification"`
		Outcome        string          `json:"outcome"`
		Payload        json.RawMessage `json:"payload"`
	}
	items := make([]priorEvidenceRecord, 0, len(records))
	total := 0
	for _, record := range records {
		if strings.TrimSpace(record.EvidenceID) == "" || len(record.Payload) == 0 {
			continue
		}
		item := priorEvidenceRecord{
			EvidenceID:     boundedContinuationText(record.EvidenceID, 128),
			Provider:       boundedContinuationText(record.Provider, 64),
			Kind:           boundedContinuationText(record.EvidenceKind, 96),
			Classification: string(record.Classification),
			Outcome:        boundedContinuationText(record.Outcome, 64),
			Payload:        record.Payload,
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			continue
		}
		if total+len(encoded) > maxContinuationBriefBytes {
			break
		}
		items = append(items, item)
		total += len(encoded)
	}
	if len(items) == 0 {
		return ""
	}
	encoded, err := json.Marshal(map[string]interface{}{
		"kind":        "prior_runtime_evidence",
		"instruction": "Prior runtime evidence is persisted and citable; cite its evidenceId when reasoning uses it. Evidence resolution and persisted classification remain authoritative.",
		"records":     items,
	})
	if err != nil {
		return ""
	}
	return "## Prior runtime evidence (same series; persisted and citable)\n" + string(encoded)
}

// renderContinuationEvidenceIndex 把同 series 早期 attempt 的非 runtime 证据渲染
// 为紧凑索引（D3/R10）：只含 evidenceId/kind/provider/classification/
// sourceAttempt/contentHash，绝不内联 payload。模型需要内容时必须用
// evidence.read 按 evidenceId 重新读取原始持久化证据；索引条目因此保留完整
// 证据身份，不会把摘要升级成证据（R15）。跨 series/未来 attempt 的行由 store
// 查询排除，不会到达此处。
func renderContinuationEvidenceIndex(entries []domain.EvidenceIndexEntry) string {
	if len(entries) == 0 {
		return ""
	}
	if len(entries) > maxContinuationEvidenceIndex {
		entries = entries[:maxContinuationEvidenceIndex]
	}
	type priorEvidenceIndexEntry struct {
		EvidenceID     string `json:"evidenceId"`
		Kind           string `json:"kind"`
		Provider       string `json:"provider"`
		Classification string `json:"classification"`
		SourceAttempt  int32  `json:"sourceAttempt"`
		ContentHash    string `json:"contentHash"`
	}
	items := make([]priorEvidenceIndexEntry, 0, len(entries))
	total := 0
	for _, entry := range entries {
		if strings.TrimSpace(entry.EvidenceID) == "" {
			continue
		}
		item := priorEvidenceIndexEntry{
			EvidenceID:     boundedContinuationText(entry.EvidenceID, 128),
			Kind:           boundedContinuationText(entry.Kind, 96),
			Provider:       boundedContinuationText(entry.Provider, 64),
			Classification: safeContinuationClassification(entry.Classification),
			SourceAttempt:  max(entry.SourceAttempt, 0),
			ContentHash:    boundedContinuationText(entry.ContentHash, 64),
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			continue
		}
		if total+len(encoded) > maxContinuationBriefBytes {
			break
		}
		items = append(items, item)
		total += len(encoded)
	}
	if len(items) == 0 {
		return ""
	}
	encoded, err := json.Marshal(map[string]interface{}{
		"kind":        "prior_evidence_index",
		"instruction": "Indexed evidence is persisted and re-readable; call evidence.read with its evidenceId to page the original content. Stored classification and provenance remain authoritative.",
		"entries":     items,
	})
	if err != nil {
		return ""
	}
	return "## Prior evidence index (same series; persisted and re-readable by evidenceId)\n" + string(encoded)
}

// buildContinuationBrief 只把 direct predecessor 和可选 planning checkpoint 中
// 已经持久化的有界 metadata 投影到下一轮 model。raw provider message、payload、
// credential 和无界 tool output 均不进入 projection。
func buildContinuationBrief(predecessor domain.RunAggregate, planningCheckpoint *domain.RunAggregate, next domain.NextAttempt) string {
	previous := predecessor.Run
	prior := predecessor
	if planningCheckpoint != nil {
		prior = *planningCheckpoint
	}
	brief := continuationBriefEnvelope{
		Kind:            "remediation_continuation_brief",
		PreviousAttempt: continuationAttemptProjection(previous),
		PriorDiagnoses:  make([]continuationDiagnosis, 0, min(len(prior.Decisions), maxContinuationListItems)),
		PriorPlans:      make([]continuationPlan, 0, min(len(prior.Plans), maxContinuationListItems)),
		ArtifactRefs:    boundedContinuationList(prior.ArtifactReferences),
		ToolOutcomes:    make([]continuationToolOutcome, 0, min(len(prior.ToolInvocations), maxContinuationListItems)),
		Continuation: continuationDetails{
			Reason:                boundedContinuationText(next.ContinuationReason, maxContinuationTextBytes),
			CurrentContextVersion: max(next.ContextVersion, 0),
			Instruction:           continuationHypothesisInstruction,
		},
	}
	if planningCheckpoint != nil {
		checkpoint := continuationAttemptProjection(planningCheckpoint.Run)
		brief.PlanningCheckpoint = &checkpoint
	}
	if prior.SuggestedDiff != "" {
		brief.SuggestedDiff = continuationDiffSummary{
			Available: true,
			Bytes:     len(sanitizeSuggestedDiff(prior.SuggestedDiff)),
		}
	}

	for _, decision := range prior.Decisions {
		if len(brief.PriorDiagnoses) >= maxContinuationListItems {
			break
		}
		brief.PriorDiagnoses = append(brief.PriorDiagnoses, continuationDiagnosis{
			Fixability:            safeContinuationFixability(decision.Fixability),
			Confidence:            clampConfidence(decision.Confidence),
			CausalReasoning:       boundedContinuationText(decision.CausalReasoning, maxContinuationTextBytes),
			EvidenceIDs:           boundedContinuationList(decision.EvidenceCitations),
			MissingEvidence:       boundedContinuationList(decision.MissingEvidence),
			Contradictions:        boundedContinuationList(decision.Contradictions),
			RecommendedNextAction: boundedContinuationText(decision.RecommendedNextAction, maxContinuationTextBytes),
		})
	}
	for _, plan := range prior.Plans {
		if len(brief.PriorPlans) >= maxContinuationListItems {
			break
		}
		brief.PriorPlans = append(brief.PriorPlans, continuationPlan{
			PlanID:           boundedContinuationText(plan.PlanID, 128),
			AffectedFiles:    boundedContinuationList(plan.AffectedFiles),
			Risk:             safeContinuationRisk(plan.Risk),
			RollbackStrategy: boundedContinuationText(plan.RollbackStrategy, maxContinuationTextBytes),
			Rationale:        boundedContinuationText(plan.Rationale, maxContinuationTextBytes),
			EvidenceRefs:     boundedContinuationList(plan.EvidenceRefs),
		})
	}
	for _, invocation := range prior.ToolInvocations {
		if len(brief.ToolOutcomes) >= maxContinuationListItems {
			break
		}
		brief.ToolOutcomes = append(brief.ToolOutcomes, continuationToolOutcome{
			Name:       safeContinuationToolName(invocation.ToolName),
			Phase:      safeContinuationState(invocation.Phase),
			Outcome:    safeContinuationToolOutcome(invocation),
			OutcomeRef: boundedContinuationText(invocation.InvocationID, 128),
			ErrorCode:  safeContinuationErrorCode(invocation.Error),
			Bytes:      max(invocation.BytesRetrieved, 0),
		})
	}

	encoded := encodeContinuationBrief(brief)
	return "## Continuation brief (bounded; prior conclusions are hypotheses)\n" + string(encoded)
}

func continuationAttemptProjection(run domain.Run) continuationAttempt {
	return continuationAttempt{
		RunID:               boundedContinuationText(run.RunID, 128),
		AttemptNumber:       run.AttemptNumber,
		State:               safeContinuationState(run.State),
		Origin:              safeContinuationOrigin(run.Origin, run.TriggerReason),
		ContinuationOfRunID: boundedContinuationText(run.ContinuationOfRunID, 128),
		TerminalReason:      safeContinuationReasonCode(run.TerminalReason),
		Retryable:           run.Retryable,
		Version:             run.Version,
		ContextVersion:      max(run.ContextVersion, 0),
	}
}

func encodeContinuationBrief(brief continuationBriefEnvelope) []byte {
	encoded, err := json.Marshal(brief)
	if err == nil && len(encoded) <= maxContinuationBriefBytes {
		return encoded
	}

	// 即使持久化 aggregate 异常偏大，也要保留 predecessor identity、continuation
	// reason 和 verification instruction。
	brief.PriorDiagnoses = trimContinuationDiagnoses(brief.PriorDiagnoses, 8)
	brief.PriorPlans = trimContinuationPlans(brief.PriorPlans, 8)
	brief.ArtifactRefs = trimContinuationStrings(brief.ArtifactRefs, 8)
	brief.ToolOutcomes = trimContinuationTools(brief.ToolOutcomes, 16)
	encoded, err = json.Marshal(brief)
	if err == nil && len(encoded) <= maxContinuationBriefBytes {
		return encoded
	}

	brief.PriorDiagnoses = nil
	brief.PriorPlans = nil
	brief.ArtifactRefs = nil
	brief.ToolOutcomes = nil
	encoded, err = json.Marshal(brief)
	if err == nil {
		return encoded
	}
	return []byte(`{"kind":"remediation_continuation_brief","continuation":{"reason":"bounded continuation","instruction":"Verify current bootstrap evidence before acting."}}`)
}

func boundedContinuationText(value string, limit int) string {
	value = strings.ToValidUTF8(redactConversationText(sanitizeReviewText(value)), "\uFFFD")
	if limit <= 0 || len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}

func boundedContinuationList(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	sanitized := sanitizeReviewList(values)
	if len(sanitized) > maxContinuationListItems {
		sanitized = sanitized[:maxContinuationListItems]
	}
	out := make([]string, 0, len(sanitized))
	for _, value := range sanitized {
		out = append(out, boundedContinuationText(value, maxContinuationTextBytes))
	}
	return out
}

func safeContinuationState(state domain.RunState) string {
	if _, err := domain.ParseRunState(string(state)); err != nil {
		return "unknown"
	}
	return string(state)
}

func safeContinuationOrigin(values ...string) string {
	for _, value := range values {
		if domain.TriggerOrigin(value).IsKnown() {
			return value
		}
	}
	return ""
}

func safeContinuationReasonCode(value string) string {
	value = strings.TrimSpace(value)
	switch value {
	case "model_output_exhausted", "provider_timeout", "provider_transport", "provider_rate_limit",
		"provider_http_5xx", "provider_http_4xx", "provider_decode", "provider_failure",
		"provider_configuration", "provider_authentication", "provider_response_too_large",
		"transient_provider", "connector_timeout", "transport", "rate_limit", "remote_execution", "connector_failure",
		"connector_authorization", "connector_not_found", "invalid_response", "invalid_configuration",
		"capability_unavailable", "policy_unconfigured", "invalid_arguments", "tool_unavailable",
		"provider_detail_unavailable", "provider_detail_invalid", "provider_detail_redirect_rejected",
		"provider_detail_oversized", "provider_detail_timeout", "provider_detail_persistence",
		"runtime_evidence_persistence", "prior_detail_failure", "policy_rejection",
		"invalid_envelope", "blocked_manual_review", "insufficient_evidence", "budget_exhausted",
		"elapsed", "model_calls", "model_cost", "tool_calls", "evidence_bytes", "repository_bytes",
		"configuration_failure", "authorization_failure", "persistence_failure", "canceled", "unknown_failure",
		"awaiting_human_review":
		return value
	default:
		return "unknown"
	}
}

func safeContinuationErrorCode(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return safeContinuationReasonCode(value)
}

func safeContinuationFixability(value domain.FixabilityClass) string {
	switch value {
	case domain.FixabilityCodeFixable, domain.FixabilityExternalDependency, domain.FixabilityConfiguration,
		domain.FixabilityData, domain.FixabilityInfrastructure, domain.FixabilityInsufficientEvidence,
		domain.FixabilityUnsafeToAutomate:
		return string(value)
	default:
		return "unknown"
	}
}

func safeContinuationClassification(value domain.EvidenceClassification) string {
	if err := domain.ValidateEvidenceClassification(value); err != nil {
		return "unknown"
	}
	return string(value)
}

func safeContinuationRisk(value domain.RiskClassification) string {
	switch value {
	case domain.RiskOrdinary, domain.RiskHighRisk, domain.RiskDeniedControlPlane:
		return string(value)
	default:
		return "unknown"
	}
}

func safeContinuationToolName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return "unknown"
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			continue
		}
		switch r {
		case '.', '-', '_':
		default:
			return "unknown"
		}
	}
	return value
}

func safeContinuationToolOutcome(invocation domain.ToolInvocation) string {
	switch invocation.ResultSummary {
	case "success", "empty", "error", "rejected", "unavailable", "failure":
		return invocation.ResultSummary
	default:
		if invocation.Error != "" {
			return "error"
		}
		return "unknown"
	}
}

func clampConfidence(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func trimContinuationStrings(values []string, limit int) []string {
	if len(values) > limit {
		values = values[:limit]
	}
	return values
}

func trimContinuationDiagnoses(values []continuationDiagnosis, limit int) []continuationDiagnosis {
	if len(values) > limit {
		values = values[:limit]
	}
	return values
}

func trimContinuationPlans(values []continuationPlan, limit int) []continuationPlan {
	if len(values) > limit {
		values = values[:limit]
	}
	return values
}

func trimContinuationTools(values []continuationToolOutcome, limit int) []continuationToolOutcome {
	if len(values) > limit {
		values = values[:limit]
	}
	return values
}
