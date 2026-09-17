package application

import (
	"context"
	"testing"
	"time"

	"mendry/backend/internal/modules/remediation/domain"
)

func TestDefaultRepositoryByteBudget(t *testing.T) {
	const ceiling int64 = 256 << 20
	if got := DefaultBudgetLimits().MaxRepositoryBytes; got != ceiling {
		t.Fatalf("MaxRepositoryBytes = %d, want %d", got, ceiling)
	}
	budget := newRunBudget(DefaultBudgetLimits())
	if reason := budget.consume(domain.Effect{RepositoryBytes: 213731471}); reason != "" {
		t.Fatalf("reported repository usage exhausted default budget: %s", reason)
	}
	if reason := budget.consume(domain.Effect{RepositoryBytes: ceiling - 213731471}); reason != "" {
		t.Fatalf("exact ceiling exhausted default budget: %s", reason)
	}
	if reason := budget.consume(domain.Effect{RepositoryBytes: 1}); reason != budgetReasonRepositoryBytes {
		t.Fatalf("consume reason = %q, want repository_bytes", reason)
	}
}

func TestRunBudgetDoesNotExhaustOnModelTokens(t *testing.T) {
	budget := newRunBudget(DefaultBudgetLimits())

	if reason := budget.consume(domain.Effect{ModelTokensIn: 1_000_000}); reason != "" {
		t.Fatalf("model tokens exhausted the run budget: %s", reason)
	}
	if budget.used.ModelTokens != 1_000_000 {
		t.Fatalf("model tokens = %d, want 1000000", budget.used.ModelTokens)
	}
}

func TestDefaultRunElapsedBudgetIsTwentyMinutes(t *testing.T) {
	limits := DefaultBudgetLimits()
	if got := limits.MaxElapsed; got != 20*time.Minute {
		t.Fatalf("MaxElapsed = %s, want 20m", got)
	}
	if got := limits.MaxModelCalls; got != 50 {
		t.Fatalf("MaxModelCalls = %d, want 50", got)
	}
}

func TestRunBudgetOperationContextUsesRemainingDeadline(t *testing.T) {
	limits := DefaultBudgetLimits()
	limits.MaxElapsed = 50 * time.Millisecond
	budget := newRunBudget(limits)
	operationCtx, cancel := budget.operationContext(context.Background())
	defer cancel()

	select {
	case <-operationCtx.Done():
		if !runWorkDeadlineExceeded(operationCtx) {
			t.Fatalf("context cause = %v, want run work deadline", context.Cause(operationCtx))
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("operation context did not enforce run deadline")
	}
}

func TestRunBudgetReturnsDeterministicResourceReason(t *testing.T) {
	tests := []struct {
		name   string
		effect domain.Effect
		want   budgetExhaustionReason
	}{
		{name: "model calls", effect: domain.Effect{ModelCalls: 2}, want: budgetReasonModelCalls},
		{name: "model cost", effect: domain.Effect{ModelCostCents: 2}, want: budgetReasonModelCost},
		{name: "tool calls", effect: domain.Effect{ToolCalls: 2}, want: budgetReasonToolCalls},
		{name: "evidence bytes", effect: domain.Effect{EvidenceBytes: 2}, want: budgetReasonEvidenceBytes},
		{name: "repository bytes", effect: domain.Effect{RepositoryBytes: 2}, want: budgetReasonRepositoryBytes},
		{name: "priority", effect: domain.Effect{ModelCalls: 2, ModelCostCents: 2, ToolCalls: 2}, want: budgetReasonModelCalls},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			limits := domain.BudgetLimits{
				MaxElapsed: time.Hour, MaxModelCalls: 1, MaxModelCostCents: 1,
				MaxToolCalls: 1, MaxEvidenceBytes: 1, MaxRepositoryBytes: 1,
			}
			budget := newRunBudget(limits)
			if reason := budget.consume(test.effect); reason != test.want {
				t.Fatalf("consume reason = %q, want %q", reason, test.want)
			}
		})
	}
}

func TestRunBudgetChecksElapsedOnlyAtAdmission(t *testing.T) {
	limits := DefaultBudgetLimits()
	limits.MaxElapsed = time.Second
	budget := newRunBudget(limits)
	budget.now = func() time.Time { return budget.startedAt.Add(2 * time.Second) }

	if reason := budget.consume(domain.Effect{ModelCalls: 1}); reason != "" {
		t.Fatalf("post-operation consume reason = %q, want none", reason)
	}
	if reason := budget.admissionExhaustionReason(); reason != budgetReasonElapsed {
		t.Fatalf("admission reason = %q, want elapsed", reason)
	}
}
