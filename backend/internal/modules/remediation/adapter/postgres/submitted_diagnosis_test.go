package postgres_test

import (
	"context"
	"sync"
	"testing"

	"mendry/backend/internal/modules/remediation/domain"
)

// submittedDiagnosisInput 构造一条有界 submitted diagnosis 审计输入；correction
// metadata 与 gate outcome 由用例覆盖。
func submittedDiagnosisInput(fixability domain.FixabilityClass, confidence float64) domain.SubmittedDiagnosis {
	return domain.SubmittedDiagnosis{
		Fixability:            fixability,
		Confidence:            confidence,
		CausalReasoning:       "deployed source panics on nil trigger",
		Contradictions:        []string{"none"},
		MissingEvidence:       []string{},
		EvidenceCitations:     []string{"ev-1"},
		RecommendedNextAction: "apply the suggested patch",
	}
}

// TestRunStore_SubmittedDiagnosisAuditRoundTrip 覆盖 D4 audit 持久化：
// append 分配单调 sequence、correction metadata/gate outcome/decision 链接
// 完整往返；LatestDecisionID 在 appendDecision 之后解析 accepted decision；
// 尚无 decision 时返回空；非法值 fail closed。
func TestRunStore_SubmittedDiagnosisAuditRoundTrip(t *testing.T) {
	pool := setupTestDB(t)
	t.Cleanup(pool.Close)

	store := mustStore(t, pool)
	ctx := context.Background()

	incidentID := mustIncident(t, pool)
	run, err := store.CreateSeriesAndRun(ctx, newRun(incidentID, 1, "abc123"))
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	t.Run("appends with monotonic sequence and no decision link", func(t *testing.T) {
		first := submittedDiagnosisInput(domain.FixabilityCodeFixable, 0.9)
		first.GateOutcome = "planning_eligible"
		if err := store.AppendSubmittedDiagnosis(ctx, run.RunID, first); err != nil {
			t.Fatalf("AppendSubmittedDiagnosis first: %v", err)
		}
		second := submittedDiagnosisInput(domain.FixabilityInsufficientEvidence, 0.2)
		second.Correction = domain.SubmittedDiagnosisCorrection{
			Kind:              domain.SubmittedCorrectionEvidence,
			CorrectedEvidence: []domain.SubmittedEvidenceClassification{{EvidenceID: "ev-1", StoredClassification: domain.EvidenceDirectFault}},
			CorrectionCount:   1,
			Corrected:         true,
		}
		second.GateOutcome = "rejected"
		if err := store.AppendSubmittedDiagnosis(ctx, run.RunID, second); err != nil {
			t.Fatalf("AppendSubmittedDiagnosis second: %v", err)
		}

		rows, err := store.ListSubmittedDiagnoses(ctx, run.RunID)
		if err != nil {
			t.Fatalf("ListSubmittedDiagnoses: %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("rows = %d, want 2", len(rows))
		}
		if rows[0].Sequence != 1 || rows[0].RunID != run.RunID ||
			rows[0].Fixability != domain.FixabilityCodeFixable ||
			rows[0].Confidence != 0.9 || rows[0].GateOutcome != "planning_eligible" ||
			rows[0].Correction.Corrected || rows[0].DecisionID != "" {
			t.Fatalf("row 1 = %#v", rows[0])
		}
		if rows[1].Sequence != 2 || rows[1].Fixability != domain.FixabilityInsufficientEvidence ||
			rows[1].GateOutcome != "rejected" || !rows[1].Correction.Corrected ||
			rows[1].Correction.Kind != domain.SubmittedCorrectionEvidence ||
			rows[1].Correction.CorrectionCount != 1 ||
			len(rows[1].Correction.CorrectedEvidence) != 1 ||
			rows[1].Correction.CorrectedEvidence[0].EvidenceID != "ev-1" ||
			rows[1].Correction.CorrectedEvidence[0].StoredClassification != domain.EvidenceDirectFault {
			t.Fatalf("row 2 = %#v", rows[1])
		}
		// 尚无 accepted decision：audit 链不猜测历史 decision。
		if id, err := store.LatestDecisionID(ctx, run.RunID); err != nil || id != "" {
			t.Fatalf("LatestDecisionID = %q, %v; want empty before any decision", id, err)
		}
	})

	t.Run("links the accepted decision after appendDecision", func(t *testing.T) {
		if err := store.AppendDecision(ctx, run.RunID, domain.Decision{
			Fixability: domain.FixabilityCodeFixable, Confidence: 0.9, CausalReasoning: "root cause",
		}); err != nil {
			t.Fatalf("AppendDecision: %v", err)
		}
		decisionID, err := store.LatestDecisionID(ctx, run.RunID)
		if err != nil {
			t.Fatalf("LatestDecisionID: %v", err)
		}
		if decisionID == "" {
			t.Fatal("LatestDecisionID = empty after appendDecision")
		}
		submitted := submittedDiagnosisInput(domain.FixabilityCodeFixable, 0.9)
		submitted.DecisionID = decisionID
		if err := store.AppendSubmittedDiagnosis(ctx, run.RunID, submitted); err != nil {
			t.Fatalf("AppendSubmittedDiagnosis with decision link: %v", err)
		}
		rows, err := store.ListSubmittedDiagnoses(ctx, run.RunID)
		if err != nil {
			t.Fatalf("ListSubmittedDiagnoses: %v", err)
		}
		last := rows[len(rows)-1]
		if last.DecisionID != decisionID {
			t.Fatalf("decision link = %q, want %q", last.DecisionID, decisionID)
		}
	})

	t.Run("serializes concurrent sequence allocation", func(t *testing.T) {
		const workers = 8
		var wg sync.WaitGroup
		errs := make(chan error, workers)
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs <- store.AppendSubmittedDiagnosis(ctx, run.RunID,
					submittedDiagnosisInput(domain.FixabilityExternalDependency, 0.7))
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("concurrent AppendSubmittedDiagnosis: %v", err)
			}
		}
		rows, err := store.ListSubmittedDiagnoses(ctx, run.RunID)
		if err != nil {
			t.Fatalf("ListSubmittedDiagnoses: %v", err)
		}
		if len(rows) != 3+workers {
			t.Fatalf("rows = %d, want %d", len(rows), 3+workers)
		}
		for index, row := range rows {
			if row.Sequence != int64(index+1) {
				t.Fatalf("row %d sequence = %d, want %d", index, row.Sequence, index+1)
			}
		}
	})

	t.Run("rejects invalid values and unknown runs fail closed", func(t *testing.T) {
		bad := submittedDiagnosisInput(domain.FixabilityCodeFixable, 0.9)
		bad.Fixability = "tcpdump"
		if err := store.AppendSubmittedDiagnosis(ctx, run.RunID, bad); err == nil {
			t.Fatal("AppendSubmittedDiagnosis accepted unknown fixability")
		}
		if err := store.AppendSubmittedDiagnosis(ctx, newV7(t).String(), submittedDiagnosisInput(domain.FixabilityCodeFixable, 0.9)); err == nil {
			t.Fatal("AppendSubmittedDiagnosis accepted an unknown run")
		}
	})
}

// TestRunStore_SubmittedDiagnosisOmitsRawModelText 证明 D4 的审计行只含
// envelope 中已有的有界结构化字段：把疑似 raw turn 文本放入 reasoning /
// recommendedNextAction 后往返，字段值原样保留（这些本来就是 envelope 结构化
// 字段），而 prompt / conversation 等 raw 文本从不作为额外字段进入投影。
func TestRunStore_SubmittedDiagnosisOmitsRawModelText(t *testing.T) {
	pool := setupTestDB(t)
	t.Cleanup(pool.Close)

	store := mustStore(t, pool)
	ctx := context.Background()

	incidentID := mustIncident(t, pool)
	run, err := store.CreateSeriesAndRun(ctx, newRun(incidentID, 1, "abc123"))
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	submitted := domain.SubmittedDiagnosis{
		Fixability:            domain.FixabilityInsufficientEvidence,
		Confidence:            0.3,
		CausalReasoning:       "no available evidence can close the gap",
		RecommendedNextAction: "manual review",
	}
	if err := store.AppendSubmittedDiagnosis(ctx, run.RunID, submitted); err != nil {
		t.Fatalf("AppendSubmittedDiagnosis: %v", err)
	}
	rows, err := store.ListSubmittedDiagnoses(ctx, run.RunID)
	if err != nil {
		t.Fatalf("ListSubmittedDiagnoses: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	got := rows[0]
	// 投影只包含 envelope 的结构化字段：没有 conversation/prompt 文本字段，
	// 也没有 correction 内容之外的任何自由文本载体。
	if got.CausalReasoning != "no available evidence can close the gap" ||
		got.RecommendedNextAction != "manual review" ||
		got.Fixability != domain.FixabilityInsufficientEvidence || got.Confidence != 0.3 {
		t.Fatalf("projection = %#v", got)
	}
}
