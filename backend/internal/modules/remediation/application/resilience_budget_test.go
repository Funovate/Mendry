package application

import (
	"context"
	"testing"
	"time"

	"mendry/backend/internal/modules/remediation/domain"
)

// TestSoftBudgetRecoveryMirrorsElapsedAsDelta 证明 soft-budget 镜像把 runBudget
// 的累计 elapsed 转成增量再计入 allocator：同一秒内的多次消耗不会把同一秒数
// 重复累加。runBudget 的 elapsed 是累计设定（consume 时 set 当前值），而
// phaseBudgetPlan 的 Consume 是增量累加；本回归固定 budget 时钟在 2s，两次
// 消耗后 allocator 的 elapsed 消耗必须仍是 2s（修复前会累加为 4s，提前触发
// soft challenge 并污染 budget 投影，D7/AC10）。
func TestSoftBudgetRecoveryMirrorsElapsedAsDelta(t *testing.T) {
	coord := &RemediationCoordinator{}
	tracker := newResilientRunState(nil, domain.Run{}, "")
	tracker.admitSoftBudget(DefaultBudgetLimits(), domain.RunStateDiagnosing)
	tracker.conversation = NewAgentConversation("bootstrap")
	ctx := withResilientRunState(context.Background(), tracker)

	budget := newRunBudget(DefaultBudgetLimits())
	budget.startedAt = time.Now().Add(-2 * time.Second)

	if err := coord.softBudgetRecovery(ctx, budget, domain.RunStateDiagnosing, domain.Effect{ModelCalls: 1}, true); err != nil {
		t.Fatal(err)
	}
	if err := coord.softBudgetRecovery(ctx, budget, domain.RunStateDiagnosing, domain.Effect{ModelCalls: 1}, true); err != nil {
		t.Fatal(err)
	}

	var elapsed, modelCalls int64
	for _, consumed := range tracker.alloc.Recovery().Consumed {
		if consumed.Phase != domain.RunStateDiagnosing {
			continue
		}
		elapsed += consumed.Amount.ElapsedSeconds
		modelCalls += consumed.Amount.ModelCalls
	}
	if elapsed != 2 {
		t.Fatalf("mirrored elapsed = %d, want 2 (cumulative second must not be re-added per consume)", elapsed)
	}
	if modelCalls != 2 {
		t.Fatalf("mirrored model calls = %d, want 2", modelCalls)
	}
}
