package domain_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"fixthe/backend/internal/modules/remediation/domain"
)

func validBudgetRecovery() domain.BudgetPlanRecoveryV1 {
	plan := domain.BudgetPlanV1{
		SchemaVersion: domain.BudgetPlanSchemaVersionV1,
		Ceiling:       domain.BudgetLimits{MaxElapsed: time.Second, MaxModelCalls: 1, MaxModelCostCents: 1, MaxToolCalls: 1, MaxEvidenceBytes: 1, MaxRepositoryBytes: 1},
	}
	for _, phase := range domain.BudgetPhaseOrder() {
		plan.Phases = append(plan.Phases, domain.PhaseBudgetAllocation{Phase: phase})
	}
	return domain.BudgetPlanRecoveryV1{SchemaVersion: domain.BudgetPlanSchemaVersionV1, Plan: plan, CurrentPhase: domain.RunStateDiagnosing, Frontier: 0}
}

// validCheckpoint 构造一个满足 v1 边界的 checkpoint；测试通过修改字段制造违规。
func validCheckpoint() domain.WorkingMemoryCheckpointV1 {
	return domain.WorkingMemoryCheckpointV1{
		SchemaVersion:      domain.CheckpointSchemaVersionV1,
		Sequence:           1,
		RunID:              "run-1",
		SeriesID:           "series-1",
		ContextVersion:     2,
		ObservedRunVersion: 1,
		Phase:              "diagnosing",
		Objective: domain.CheckpointObjective{
			Goal:               "explain the production symptom",
			CompletionCriteria: []string{"causal closure to the original symptom"},
		},
		VerifiedFacts: []domain.CheckpointVerifiedFact{
			{Statement: "the stack names the deployed function", EvidenceIDs: []string{"ev-1"}},
		},
		ActiveHypotheses: []domain.CheckpointHypothesis{
			{ID: "h1", Summary: "bad argument handling", EvidenceIDs: []string{"ev-1"}},
		},
		RejectedHypotheses: []domain.CheckpointHypothesis{
			{ID: "h0", Summary: "network loss", Reason: "logs show no transport error", EvidenceIDs: []string{"ev-2"}},
		},
		EvidenceIndex: []domain.CheckpointEvidenceIndexItem{
			{EvidenceID: "ev-1", Kind: "provider_detail", Locator: "source/attempt 1", ContentHash: strings.Repeat("a", 64)},
		},
		UnresolvedQuestions: []domain.CheckpointQuestion{
			{Question: "was the deploy atomic?", Material: true},
		},
		PhaseProgress: domain.CheckpointPhaseProgress{
			Completed: []string{"collect provider detail"},
			Remaining: []string{"map path to repository file"},
		},
		Recoveries: []domain.CheckpointRecovery{
			{Kind: "tool_failure", Action: "refine log query", OutcomeRef: "obs-1"},
		},
		NextActions: []string{"inspect repository.read_file at the reported line"},
		Budget:      validBudgetRecovery(),
		Reason:      domain.CheckpointReasonThreshold,
	}
}

func TestWorkingMemoryCheckpointV1Validate(t *testing.T) {
	t.Run("valid checkpoint passes", func(t *testing.T) {
		if err := validCheckpoint().Validate(); err != nil {
			t.Fatalf("Validate() error = %v", err)
		}
	})

	tests := []struct {
		name   string
		mutate func(*domain.WorkingMemoryCheckpointV1)
	}{
		{"unknown schema version", func(c *domain.WorkingMemoryCheckpointV1) { c.SchemaVersion = "v2" }},
		{"empty run id", func(c *domain.WorkingMemoryCheckpointV1) { c.RunID = "" }},
		{"empty series id", func(c *domain.WorkingMemoryCheckpointV1) { c.SeriesID = "  " }},
		{"negative sequence", func(c *domain.WorkingMemoryCheckpointV1) { c.Sequence = -1 }},
		{"negative context version", func(c *domain.WorkingMemoryCheckpointV1) { c.ContextVersion = -1 }},
		{"empty phase", func(c *domain.WorkingMemoryCheckpointV1) { c.Phase = "" }},
		{"unknown reason", func(c *domain.WorkingMemoryCheckpointV1) { c.Reason = "model_stop" }},
		{"empty goal", func(c *domain.WorkingMemoryCheckpointV1) { c.Objective.Goal = "" }},
		{"fact without evidence ids", func(c *domain.WorkingMemoryCheckpointV1) { c.VerifiedFacts[0].EvidenceIDs = nil }},
		{"fact statement empty", func(c *domain.WorkingMemoryCheckpointV1) { c.VerifiedFacts[0].Statement = "" }},
		{"active hypothesis without summary", func(c *domain.WorkingMemoryCheckpointV1) { c.ActiveHypotheses[0].Summary = "" }},
		{"index item without hash", func(c *domain.WorkingMemoryCheckpointV1) { c.EvidenceIndex[0].ContentHash = "" }},
		{"question empty", func(c *domain.WorkingMemoryCheckpointV1) { c.UnresolvedQuestions[0].Question = "" }},
		{"next action empty", func(c *domain.WorkingMemoryCheckpointV1) { c.NextActions = []string{""} }},
		{"invalid budget frontier", func(c *domain.WorkingMemoryCheckpointV1) { c.Budget.Frontier = 99 }},
		{"negative recovery progress", func(c *domain.WorkingMemoryCheckpointV1) {
			c.RecoveryProgress = &domain.CheckpointRecoveryProgress{LifecycleRecoveryAttempts: -1}
		}},
		{"invalid recovery fingerprint", func(c *domain.WorkingMemoryCheckpointV1) {
			c.RecoveryProgress = &domain.CheckpointRecoveryProgress{LastLifecycleRecoveryFingerprint: "not-a-hash"}
		}},
		{"recovery no progress exceeds attempts", func(c *domain.WorkingMemoryCheckpointV1) {
			c.RecoveryProgress = &domain.CheckpointRecoveryProgress{FactCheckAttempts: 1, FactCheckNoProgress: 2}
		}},
		{"phase too long", func(c *domain.WorkingMemoryCheckpointV1) { c.Phase = strings.Repeat("x", 65) }},
		{"fact statement too long", func(c *domain.WorkingMemoryCheckpointV1) { c.VerifiedFacts[0].Statement = strings.Repeat("x", 513) }},
		{"too many verified facts", func(c *domain.WorkingMemoryCheckpointV1) {
			facts := make([]domain.CheckpointVerifiedFact, 65)
			for i := range facts {
				facts[i] = domain.CheckpointVerifiedFact{Statement: "s", EvidenceIDs: []string{"e"}}
			}
			c.VerifiedFacts = facts
		}},
		{"too many evidence ids", func(c *domain.WorkingMemoryCheckpointV1) {
			ids := make([]string, 65)
			for i := range ids {
				ids[i] = "e"
			}
			c.VerifiedFacts[0].EvidenceIDs = ids
		}},
		{"too many evidence index entries", func(c *domain.WorkingMemoryCheckpointV1) {
			items := make([]domain.CheckpointEvidenceIndexItem, 257)
			for i := range items {
				items[i] = domain.CheckpointEvidenceIndexItem{EvidenceID: "e", Kind: "k", Locator: "l", ContentHash: strings.Repeat("a", 64)}
			}
			c.EvidenceIndex = items
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkpoint := validCheckpoint()
			tt.mutate(&checkpoint)
			if err := checkpoint.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want error")
			}
		})
	}

	// rejected hypothesis 的 reason 是可选的（仅 rejected 语义字段），空值必须放行。
	t.Run("rejected hypothesis without reason is allowed", func(t *testing.T) {
		checkpoint := validCheckpoint()
		checkpoint.RejectedHypotheses[0].Reason = ""
		if err := checkpoint.Validate(); err != nil {
			t.Fatalf("Validate() error = %v, want nil", err)
		}
	})
}

func TestWorkingMemoryCheckpointV1CanonicalRoundTrip(t *testing.T) {
	checkpoint := validCheckpoint()
	checkpoint.RecoveryProgress = &domain.CheckpointRecoveryProgress{
		RecoveryAttempts: 2, LifecycleRecoveryAttempts: 1,
		LastLifecycleRecoveryFingerprint: strings.Repeat("a", 64),
		LastLifecycleRecoveryClass:       "tool",
	}
	payload, err := checkpoint.CanonicalEncode()
	if err != nil {
		t.Fatalf("CanonicalEncode() error = %v", err)
	}
	if len(payload) == 0 || !json.Valid(payload) {
		t.Fatal("canonical payload is empty or invalid JSON")
	}

	// 同一 checkpoint 两次编码必须字节一致（确定性）。
	again, err := checkpoint.CanonicalEncode()
	if err != nil {
		t.Fatalf("second CanonicalEncode() error = %v", err)
	}
	if string(payload) != string(again) {
		t.Fatal("canonical encoding is not deterministic")
	}

	// 解码后重新编码必须字节一致（hash 校验依赖该不变量）。
	var decoded domain.WorkingMemoryCheckpointV1
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	reencoded, err := decoded.CanonicalEncode()
	if err != nil {
		t.Fatalf("re-encode decoded checkpoint: %v", err)
	}
	if string(payload) != string(reencoded) {
		t.Fatal("decode/re-encode changed the canonical payload")
	}

	hash, err := checkpoint.ContentHash()
	if err != nil {
		t.Fatalf("ContentHash() error = %v", err)
	}
	if len(hash) != 64 {
		t.Fatalf("ContentHash() length = %d, want 64", len(hash))
	}
	decodedHash, err := decoded.ContentHash()
	if err != nil {
		t.Fatalf("decoded ContentHash() error = %v", err)
	}
	if hash != decodedHash {
		t.Fatal("decoded checkpoint hash differs from original")
	}
}

func TestWorkingMemoryCheckpointV1OmitsEmptyLifecycleFieldsForLegacyHashes(t *testing.T) {
	payload, err := validCheckpoint().CanonicalEncode()
	if err != nil {
		t.Fatalf("CanonicalEncode() error = %v", err)
	}
	for _, field := range []string{"workspace", "artifacts", "validation", "publication", "publicationPolicy", "validationCommands", "recoveryProgress"} {
		if strings.Contains(string(payload), `"`+field+`"`) {
			t.Fatalf("legacy-compatible checkpoint unexpectedly encoded empty field %q: %s", field, payload)
		}
	}
}
func TestWorkingMemoryCheckpointV1OversizeRejected(t *testing.T) {
	checkpoint := validCheckpoint()
	// 把所有字段推到边界上限：64 个 verified facts，每个携带 64 个 128-rune
	// evidence ids。逐字段校验全部通过，但 canonical payload 超过 1 MiB 上限。
	facts := make([]domain.CheckpointVerifiedFact, 64)
	for i := range facts {
		ids := make([]string, 64)
		for j := range ids {
			ids[j] = strings.Repeat("界", 128)
		}
		facts[i] = domain.CheckpointVerifiedFact{Statement: strings.Repeat("界", 512), EvidenceIDs: ids}
	}
	checkpoint.VerifiedFacts = facts
	if err := checkpoint.Validate(); err != nil {
		t.Fatalf("Validate() should accept bounded fields: %v", err)
	}
	if _, err := checkpoint.CanonicalEncode(); err == nil {
		t.Fatal("CanonicalEncode() error = nil, want oversize rejection")
	}
}

func TestWorkingMemoryCheckpointV1WithSequence(t *testing.T) {
	checkpoint := validCheckpoint()
	stamped, err := checkpoint.WithSequence(7)
	if err != nil {
		t.Fatalf("WithSequence(7) error = %v", err)
	}
	if stamped.Sequence != 7 {
		t.Fatalf("stamped sequence = %d, want 7", stamped.Sequence)
	}
	if checkpoint.Sequence != 1 {
		t.Fatal("WithSequence mutated the receiver")
	}
	if _, err := checkpoint.WithSequence(0); err == nil {
		t.Fatal("WithSequence(0) error = nil, want rejection")
	}
	if _, err := checkpoint.WithSequence(-1); err == nil {
		t.Fatal("WithSequence(-1) error = nil, want rejection")
	}
}

func TestParseAgentLoopMode(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  domain.AgentLoopMode
	}{
		{"legacy", "legacy", domain.AgentLoopModeLegacy},
		{"resilient_v1", "resilient_v1", domain.AgentLoopModeResilientV1},
		{"empty defaults to legacy", "", domain.AgentLoopModeLegacy},
		{"unknown defaults to legacy", "resilient_v2", domain.AgentLoopModeLegacy},
		{"garbage defaults to legacy", "drop tables", domain.AgentLoopModeLegacy},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := domain.ParseAgentLoopMode(tt.value); got != tt.want {
				t.Errorf("ParseAgentLoopMode(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
	if !domain.AgentLoopModeLegacy.IsKnown() || !domain.AgentLoopModeResilientV1.IsKnown() {
		t.Fatal("known modes must report IsKnown() = true")
	}
	if domain.AgentLoopMode("future_mode").IsKnown() {
		t.Fatal("unknown mode must report IsKnown() = false")
	}
}
