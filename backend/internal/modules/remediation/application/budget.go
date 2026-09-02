package application

import (
	"context"
	"errors"
	"strings"
	"time"

	"fixthe/backend/internal/modules/remediation/domain"
)

const (
	defaultMaxElapsed               = 20 * time.Minute
	defaultMaxModelCalls      int64 = 16
	defaultMaxModelCostCents  int64 = 500
	defaultMaxToolCalls       int64 = 64
	defaultMaxEvidenceBytes   int64 = 4 << 20
	defaultMaxRepositoryBytes int64 = 16 << 20
)

var errRunWorkDeadline = errors.New("remediation run work deadline exceeded")

type budgetExhaustionReason string

const (
	budgetReasonElapsed         budgetExhaustionReason = "elapsed"
	budgetReasonModelCalls      budgetExhaustionReason = "model_calls"
	budgetReasonModelCost       budgetExhaustionReason = "model_cost"
	budgetReasonToolCalls       budgetExhaustionReason = "tool_calls"
	budgetReasonEvidenceBytes   budgetExhaustionReason = "evidence_bytes"
	budgetReasonRepositoryBytes budgetExhaustionReason = "repository_bytes"
)

// DefaultBudgetLimits 返回 walking skeleton 在没有项目级策略前使用的保守默认预算。
func DefaultBudgetLimits() domain.BudgetLimits {
	return domain.BudgetLimits{
		MaxElapsed:         defaultMaxElapsed,
		MaxModelCalls:      defaultMaxModelCalls,
		MaxModelCostCents:  defaultMaxModelCostCents,
		MaxToolCalls:       defaultMaxToolCalls,
		MaxEvidenceBytes:   defaultMaxEvidenceBytes,
		MaxRepositoryBytes: defaultMaxRepositoryBytes,
	}
}

type runBudget struct {
	limits    domain.BudgetLimits
	startedAt time.Time
	now       func() time.Time
	used      domain.BudgetCounters
}

func newRunBudget(limits domain.BudgetLimits) *runBudget {
	now := time.Now
	return &runBudget{limits: normalizeBudgetLimits(limits), startedAt: now(), now: now}
}

func normalizeBudgetLimits(limits domain.BudgetLimits) domain.BudgetLimits {
	defaults := DefaultBudgetLimits()
	if limits.MaxElapsed <= 0 {
		limits.MaxElapsed = defaults.MaxElapsed
	}
	if limits.MaxModelCalls <= 0 {
		limits.MaxModelCalls = defaults.MaxModelCalls
	}
	if limits.MaxModelCostCents <= 0 {
		limits.MaxModelCostCents = defaults.MaxModelCostCents
	}
	if limits.MaxToolCalls <= 0 {
		limits.MaxToolCalls = defaults.MaxToolCalls
	}
	if limits.MaxEvidenceBytes <= 0 {
		limits.MaxEvidenceBytes = defaults.MaxEvidenceBytes
	}
	if limits.MaxRepositoryBytes <= 0 {
		limits.MaxRepositoryBytes = defaults.MaxRepositoryBytes
	}
	return limits
}

func (b *runBudget) consume(effect domain.Effect) budgetExhaustionReason {
	b.used.ModelCalls += int64(effect.ModelCalls)
	b.used.ModelTokens += effect.ModelTokensIn + effect.ModelTokensOut
	b.used.ModelCostCents += effect.ModelCostCents
	b.used.ToolCalls += int64(effect.ToolCalls)
	b.used.EvidenceBytes += effect.EvidenceBytes
	b.used.RepositoryBytes += effect.RepositoryBytes
	b.used.ElapsedSeconds = int64(b.elapsed() / time.Second)
	return b.resourceExhaustionReason()
}

func (b *runBudget) admissionExhaustionReason() budgetExhaustionReason {
	if b.elapsed() > b.limits.MaxElapsed {
		return budgetReasonElapsed
	}
	return b.resourceExhaustionReason()
}

// operationContext 把 run 剩余时间变成外部操作的硬上限；调用方保留原 ctx
// 完成终态持久化，避免 deadline 到点后 run 停留在 active state。
func (b *runBudget) operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithDeadlineCause(ctx, b.startedAt.Add(b.limits.MaxElapsed), errRunWorkDeadline)
}

func runWorkDeadlineExceeded(ctx context.Context) bool {
	return errors.Is(context.Cause(ctx), errRunWorkDeadline)
}

func (b *runBudget) resourceExhaustionReason() budgetExhaustionReason {
	switch {
	case b.used.ModelCalls > b.limits.MaxModelCalls:
		return budgetReasonModelCalls
	case b.used.ModelCostCents > b.limits.MaxModelCostCents:
		return budgetReasonModelCost
	case b.used.ToolCalls > b.limits.MaxToolCalls:
		return budgetReasonToolCalls
	case b.used.EvidenceBytes > b.limits.MaxEvidenceBytes:
		return budgetReasonEvidenceBytes
	case b.used.RepositoryBytes > b.limits.MaxRepositoryBytes:
		return budgetReasonRepositoryBytes
	default:
		return ""
	}
}

func hasBudgetEffect(effect domain.Effect) bool {
	return effect.ModelCalls != 0 || effect.ModelTokensIn != 0 ||
		effect.ModelTokensOut != 0 || effect.ModelCostCents != 0 ||
		effect.ModelProvider != "" || effect.ModelName != "" || effect.ToolCalls != 0 ||
		effect.EvidenceBytes != 0 || effect.RepositoryBytes != 0
}

func (b *runBudget) elapsed() time.Duration {
	return b.now().Sub(b.startedAt)
}

// remaining 返回相对 hard ceiling 的剩余预算投影（D5 remainingBudget），
// key 与 domain.BudgetAmount.AsMap 一致；负值维度的语义是不再可消耗，
// 钳制为 0 避免 recovery challenge 的 Validate 拒绝。只含数值，供 challenge
// 与 checkpoint 使用；hard ceiling 判定不受影响。
func (b *runBudget) remaining() map[string]int64 {
	clamp := func(value int64) int64 {
		if value < 0 {
			return 0
		}
		return value
	}
	return map[string]int64{
		"elapsed_seconds":  clamp(int64(b.limits.MaxElapsed/time.Second) - b.used.ElapsedSeconds),
		"model_calls":      clamp(b.limits.MaxModelCalls - b.used.ModelCalls),
		"model_cost_cents": clamp(b.limits.MaxModelCostCents - b.used.ModelCostCents),
		"tool_calls":       clamp(b.limits.MaxToolCalls - b.used.ToolCalls),
		"evidence_bytes":   clamp(b.limits.MaxEvidenceBytes - b.used.EvidenceBytes),
		"repository_bytes": clamp(b.limits.MaxRepositoryBytes - b.used.RepositoryBytes),
	}
}

func toolResultEffect(result ToolResult) domain.Effect {
	effect := domain.Effect{ToolCalls: 1}
	switch {
	case strings.HasPrefix(result.Tool, "repository."):
		effect.RepositoryBytes = result.BytesRetrieved
	case strings.HasPrefix(result.Tool, "evidence."), result.Tool == ToolSSHInspect, result.Tool == ToolDockerLogs:
		// inspect/Docker 输出计入 evidence 预算，避免 SSH/远端读取绕过共享 run bound。
		effect.EvidenceBytes = result.BytesRetrieved
	}
	return effect
}

func repositoryListingBytes(listing domain.TreeListing) int64 {
	var bytes int64
	for _, entry := range listing.Entries {
		bytes += int64(len(entry.Path) + len(entry.Type) + len(entry.Mode))
	}
	return bytes
}

func repositorySearchBytes(result domain.SearchResult) int64 {
	var bytes int64
	for _, match := range result.Matches {
		bytes += int64(len(match.Path) + len(match.Line))
	}
	return bytes
}

func repositoryHistoryBytes(history domain.History) int64 {
	var bytes int64
	for _, commit := range history.Commits {
		bytes += int64(len(commit.Hash) + len(commit.Author) + len(commit.Message))
	}
	return bytes
}

func evidencePageBytes(page domain.EvidencePage) int64 {
	var bytes int64
	for _, line := range page.Lines {
		bytes += int64(len(line.EvidenceID) + len(line.Level) + len(line.Message) + len(line.Host))
	}
	return bytes
}
