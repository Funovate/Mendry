package application

import (
	"strings"
	"unicode/utf8"

	"mendry/backend/internal/modules/remediation/domain"
)

// NewRecoveryChallenge 构造经过 sanitize 与边界检查的 provider-neutral recovery
// challenge（D5 envelope）。message 复用 review 的 sanitize 规则并截断到上限，
// 保证不携带凭据字面量；kind/severity/attempt 非法时返回错误，不静默改写。
// coordinator 已用它回喂可恢复挑战（evidence_correction、soft-budget 信号）；
// exhaustion proposal 接入前的 terminal 路由仍不使用该 envelope。
func NewRecoveryChallenge(
	kind domain.RecoveryChallengeKind,
	severity domain.RecoverySeverity,
	reasonCode, failedActionRef string,
	availableCapabilities, suggestedRecoveryClasses []string,
	attempt int,
	remainingBudget map[string]int64,
	message string,
) (domain.RecoveryChallengeV1, error) {
	challenge := domain.RecoveryChallengeV1{
		SchemaVersion:            domain.RecoverySchemaVersionV1,
		Kind:                     kind,
		Severity:                 severity,
		ReasonCode:               reasonCode,
		FailedActionRef:          failedActionRef,
		AvailableCapabilities:    trimNonEmpty(availableCapabilities),
		SuggestedRecoveryClasses: trimNonEmpty(suggestedRecoveryClasses),
		Attempt:                  attempt,
		RemainingBudget:          copyBudget(remainingBudget),
		Message:                  boundRecoveryMessage(message),
	}
	if err := challenge.Validate(); err != nil {
		return domain.RecoveryChallengeV1{}, err
	}
	return challenge, nil
}

// boundRecoveryMessage 先走 review 的 sanitize 规则再按 rune 截断，
// 保证 message 是 bounded safe guidance，不携带 provider/connector 细节。
func boundRecoveryMessage(value string) string {
	value = sanitizeReviewText(value)
	if utf8.RuneCountInString(value) <= domain.MaxRecoveryChallengeMessageLength {
		return value
	}
	return string([]rune(value)[:domain.MaxRecoveryChallengeMessageLength])
}

// trimNonEmpty 去掉空白并丢弃空条目；数量与长度上限由 domain Validate 兜底。
func trimNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

// copyBudget 复制 remaining budget map，避免调用方后续修改改变已构造的
// challenge；nil 保持 nil。
func copyBudget(budget map[string]int64) map[string]int64 {
	if budget == nil {
		return nil
	}
	out := make(map[string]int64, len(budget))
	for key, value := range budget {
		out[key] = value
	}
	return out
}
