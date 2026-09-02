package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"fixthe/backend/internal/modules/remediation/adapter/postgres/remediationdb"
	"fixthe/backend/internal/modules/remediation/domain"
)

// Compile-time assertion that the adapter satisfies the submitted-diagnosis
// audit port. The methods live on RunStore so the same transaction source
// serves both the frozen RunStore contract and the additive audit companion.
var _ domain.SubmittedDiagnosisStore = (*RunStore)(nil)

// AppendSubmittedDiagnosis 追加一条 D4 提交诊断审计行：先锁定 owning
// remediation series，再按现有行数 +1 分配 sequence；同一 series 的并发
// append 因此串行化，避免两个事务生成相同 sequence。correction metadata、
// gate outcome 与同-run decision ownership 在同一事务内验证并写入。
func (s *RunStore) AppendSubmittedDiagnosis(ctx context.Context, runID string, d domain.SubmittedDiagnosis) error {
	if err := d.Validate(); err != nil {
		return fmt.Errorf("validate submitted diagnosis: %w", err)
	}
	rid, err := parseRunID(runID)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	q := remediationdb.New(tx)

	// 复用 run-creation 的 series row lock：所有属于同一 series 的 audit
	// append 在读取当前 sequence 前串行化，且未知 run fail closed。
	if _, err := q.LockRemediationSeriesForRun(ctx, rid); err != nil {
		return fmt.Errorf("lock remediation series for submitted diagnosis: %w", err)
	}
	rows, err := q.GetRemediationSubmittedDiagnosesByRunID(ctx, rid)
	if err != nil {
		return fmt.Errorf("get submitted diagnoses: %w", err)
	}

	var confidence pgtype.Numeric
	if err := confidence.Scan(strconv.FormatFloat(d.Confidence, 'f', -1, 64)); err != nil {
		return fmt.Errorf("convert submitted diagnosis confidence to numeric: %w", err)
	}
	correctionEvidence, err := json.Marshal(submittedCorrectionEvidence(d.Correction.CorrectedEvidence))
	if err != nil {
		return fmt.Errorf("encode submitted diagnosis correction evidence: %w", err)
	}
	decisionUUID, err := optionalRunUUID(d.DecisionID)
	if err != nil {
		return err
	}
	if decisionUUID.Valid {
		decisions, err := q.GetRemediationDecisionsByRunID(ctx, rid)
		if err != nil {
			return fmt.Errorf("get submitted diagnosis decisions: %w", err)
		}
		owned := false
		for _, decision := range decisions {
			if decision.ID.Valid && decision.ID.Bytes == decisionUUID.Bytes {
				owned = true
				break
			}
		}
		if !owned {
			return fmt.Errorf("submitted diagnosis decision does not belong to run")
		}
	}

	_, err = q.CreateRemediationSubmittedDiagnosis(ctx, remediationdb.CreateRemediationSubmittedDiagnosisParams{
		RunID:                 rid,
		Sequence:              int32(len(rows) + 1),
		FixabilityClass:       string(d.Fixability),
		ConfidenceScore:       confidence,
		Reasoning:             d.CausalReasoning,
		Contradictions:        cloneStrings(d.Contradictions),
		MissingEvidence:       cloneStrings(d.MissingEvidence),
		EvidenceCitations:     cloneStrings(d.EvidenceCitations),
		RecommendedNextAction: d.RecommendedNextAction,
		CorrectionKind:        string(d.Correction.Kind),
		CorrectionEvidence:    correctionEvidence,
		CorrectionCount:       int32(d.Correction.CorrectionCount),
		Corrected:             d.Correction.Corrected,
		GateOutcome:           d.GateOutcome,
		DecisionID:            decisionUUID,
	})
	if err != nil {
		return fmt.Errorf("create submitted diagnosis: %w", err)
	}

	return tx.Commit(ctx)
}

// ListSubmittedDiagnoses 按 sequence 升序返回 run 的提交诊断审计行，供 audit
// 查询证明 submitted→accepted 的 join 与纠正元数据；不含任何模型轮次文本。
func (s *RunStore) ListSubmittedDiagnoses(ctx context.Context, runID string) ([]domain.SubmittedDiagnosis, error) {
	rid, err := parseRunID(runID)
	if err != nil {
		return nil, err
	}
	rows, err := remediationdb.New(s.db).GetRemediationSubmittedDiagnosesByRunID(ctx, rid)
	if err != nil {
		return nil, fmt.Errorf("list submitted diagnoses: %w", err)
	}
	out := make([]domain.SubmittedDiagnosis, 0, len(rows))
	for _, row := range rows {
		mapped, err := mapSubmittedDiagnosis(row)
		if err != nil {
			return nil, err
		}
		out = append(out, mapped)
	}
	return out, nil
}

// LatestDecisionID 返回 run 最近一次 accepted decision 的 ID。冻结的
// RunStore.AppendDecision 不返回创建的 decision ID，审计链在 append 之后用
// 本方法解析 submitted→accepted join；尚无 decision 时返回空字符串。
func (s *RunStore) LatestDecisionID(ctx context.Context, runID string) (string, error) {
	rid, err := parseRunID(runID)
	if err != nil {
		return "", err
	}
	id, err := remediationdb.New(s.db).GetLatestRemediationDecisionID(ctx, rid)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get latest decision id: %w", err)
	}
	if !id.Valid {
		return "", fmt.Errorf("remediation decision id is invalid")
	}
	return uuidString(id), nil
}

// submittedCorrectionEvidence 把 domain 的 corrected citation 条目编码为有界
// jsonb 数组 [{evidenceId, storedClassification}]；分类来自持久化权威记录。
func submittedCorrectionEvidence(items []domain.SubmittedEvidenceClassification) []map[string]string {
	out := make([]map[string]string, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]string{
			"evidenceId":           item.EvidenceID,
			"storedClassification": string(item.StoredClassification),
		})
	}
	return out
}

// mapSubmittedDiagnosis 把生成的 audit 行映射回 domain 值；任何非法生成值
// fail closed，保证 List 不会把损坏行静默投影为有效审计记录。
func mapSubmittedDiagnosis(row remediationdb.RemediationSubmittedDiagnosis) (domain.SubmittedDiagnosis, error) {
	if !row.ID.Valid || !row.RunID.Valid || !row.SubmittedAt.Valid {
		return domain.SubmittedDiagnosis{}, fmt.Errorf("remediation submitted diagnosis row has invalid generated values")
	}
	var correctedEvidence []domain.SubmittedEvidenceClassification
	if err := json.Unmarshal(row.CorrectionEvidence, &correctedEvidence); err != nil {
		return domain.SubmittedDiagnosis{}, fmt.Errorf("remediation submitted diagnosis correction evidence is invalid")
	}
	confidence := 0.0
	if f, err := row.ConfidenceScore.Float64Value(); err == nil && f.Valid {
		confidence = f.Float64
	}
	mapped := domain.SubmittedDiagnosis{
		RunID:                 uuidString(row.RunID),
		Sequence:              int64(row.Sequence),
		Fixability:            domain.FixabilityClass(row.FixabilityClass),
		Confidence:            confidence,
		CausalReasoning:       row.Reasoning,
		Contradictions:        cloneStrings(row.Contradictions),
		MissingEvidence:       cloneStrings(row.MissingEvidence),
		EvidenceCitations:     cloneStrings(row.EvidenceCitations),
		RecommendedNextAction: row.RecommendedNextAction,
		Correction: domain.SubmittedDiagnosisCorrection{
			Kind:              domain.SubmittedDiagnosisCorrectionKind(row.CorrectionKind),
			CorrectedEvidence: correctedEvidence,
			CorrectionCount:   int(row.CorrectionCount),
			Corrected:         row.Corrected,
		},
		GateOutcome: row.GateOutcome,
		SubmittedAt: row.SubmittedAt.Time.UTC(),
	}
	if row.DecisionID.Valid {
		mapped.DecisionID = uuidString(row.DecisionID)
	}
	if err := mapped.Validate(); err != nil {
		return domain.SubmittedDiagnosis{}, fmt.Errorf("validate remediation submitted diagnosis row: %w", err)
	}
	return mapped, nil
}
