package application

import (
	"context"
	"fmt"

	"mendry/backend/internal/modules/remediation/domain"
)

// submittedDiagnosisFrom 构建模型提交诊断的 D4 有界审计投影。只复制 envelope
// 中已有的结构化字段（fixability/confidence/reasoning/citations/…），绝不
// 复制原始模型轮次、prompt 或 conversation 文本。RunID 由 append 时填充。
func submittedDiagnosisFrom(diag *DiagnosisOutput) domain.SubmittedDiagnosis {
	return domain.SubmittedDiagnosis{
		Fixability:            diag.Fixability,
		Confidence:            diag.Confidence,
		CausalReasoning:       diag.CausalReasoning,
		Contradictions:        append([]string(nil), diag.Contradictions...),
		MissingEvidence:       append([]string(nil), diag.MissingEvidence...),
		EvidenceCitations:     evidenceCitationIDs(diag.EvidenceCitations),
		RecommendedNextAction: diag.RecommendedNextAction,
	}
}

// submittedCorrectionFromMismatches 把 R7 citation classification 差异转换为
// correction metadata：只含 evidence ID 与持久化权威分类，不含模型文本。
func submittedCorrectionFromMismatches(mismatches []CitationClassificationMismatch) domain.SubmittedDiagnosisCorrection {
	items := make([]domain.SubmittedEvidenceClassification, 0, len(mismatches))
	for _, mismatch := range mismatches {
		items = append(items, domain.SubmittedEvidenceClassification{
			EvidenceID:           mismatch.EvidenceID,
			StoredClassification: mismatch.StoredClassification,
		})
	}
	return domain.SubmittedDiagnosisCorrection{
		Kind:              domain.SubmittedCorrectionEvidence,
		CorrectedEvidence: items,
		CorrectionCount:   len(items),
		Corrected:         true,
	}
}

// submittedGateOutcome 把服务端 gate decision 投影为有界 gate_outcome 文本。
func submittedGateOutcome(decision domain.EvidenceGateDecision) string {
	if decision.PlanningEligible {
		return "planning_eligible"
	}
	return "rejected"
}

// appendSubmittedDiagnosis 持久化 D4 audit projection。store capability、
// latest-decision lookup、validation 或 append 任一失败都返回错误；调用方必须
// 按 persistence/consistency blocker 终止，不能让 accepted decision 与 audit
// 链静默分叉。linkLatestDecision=false 用于尚未接受的 correction challenge。
func (c *RemediationCoordinator) appendSubmittedDiagnosis(
	ctx context.Context,
	runID string,
	submitted domain.SubmittedDiagnosis,
	linkLatestDecision bool,
) error {
	store, ok := c.store.(domain.SubmittedDiagnosisStore)
	if !ok {
		return fmt.Errorf("submitted diagnosis store is unavailable")
	}
	submitted.RunID = runID
	if linkLatestDecision && submitted.DecisionID == "" {
		id, err := store.LatestDecisionID(ctx, runID)
		if err != nil {
			return fmt.Errorf("resolve submitted diagnosis decision: %w", err)
		}
		if id == "" {
			return fmt.Errorf("resolve submitted diagnosis decision: accepted decision is missing")
		}
		submitted.DecisionID = id
	}
	if err := submitted.Validate(); err != nil {
		return fmt.Errorf("validate submitted diagnosis audit: %w", err)
	}
	if err := store.AppendSubmittedDiagnosis(ctx, runID, submitted); err != nil {
		return fmt.Errorf("append submitted diagnosis audit: %w", err)
	}
	return nil
}
