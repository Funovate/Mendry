package application

import (
	"context"
	"errors"
	"net"

	"fixthe/backend/internal/modules/remediation/domain"
)

var (
	errPersistenceFailure   = errors.New("remediation persistence failure")
	errConfigurationFailure = errors.New("remediation configuration failure")
	errAuthorizationFailure = errors.New("remediation authorization failure")
)

type terminalFailureClassification struct {
	reason    string
	retryable bool
}

// classifyTerminalFailure 只根据 typed provider/runtime 或 control-plane category
// 生成安全持久化 metadata，绝不通过自由文本判断 automatic retry eligibility。
func classifyTerminalFailure(cause error) terminalFailureClassification {
	if cause == nil {
		return terminalFailureClassification{reason: "unknown_failure"}
	}
	if errors.Is(cause, domain.ErrModelOutputExhausted) {
		return terminalFailureClassification{reason: "model_output_exhausted", retryable: true}
	}

	var providerFailure *domain.ProviderRuntimeError
	if errors.As(cause, &providerFailure) {
		code := safeProviderFailureCode(providerFailure.Code)
		return terminalFailureClassification{reason: code, retryable: providerFailure.Retryable && isRetryableProviderFailureCode(code)}
	}

	var toolFailure *domain.ToolRuntimeError
	if errors.As(cause, &toolFailure) {
		code := safeRuntimeCode(toolFailure.Code)
		return terminalFailureClassification{reason: code, retryable: safeRuntimeRetryable(code, toolFailure.Retryable)}
	}

	if code, ok := RejectionCode(cause); ok {
		if code == RejectUnavailable || code == RejectOutOfPhase || code == RejectArguments || code == RejectPathScope || code == RejectBudget {
			return terminalFailureClassification{reason: "policy_rejection"}
		}
	}

	// caller 或 run budget 拥有 context deadline；provider 自有 logical deadline
	// 必须先通过 ProviderRuntimeError 明确标记。
	if errors.Is(cause, context.DeadlineExceeded) {
		return terminalFailureClassification{reason: "elapsed"}
	}
	if errors.Is(cause, context.Canceled) {
		return terminalFailureClassification{reason: "canceled"}
	}
	if errors.Is(cause, ErrInvalidEnvelope) {
		return terminalFailureClassification{reason: "invalid_envelope"}
	}
	if errors.Is(cause, errPersistenceFailure) {
		return terminalFailureClassification{reason: "persistence_failure"}
	}
	if errors.Is(cause, errConfigurationFailure) {
		return terminalFailureClassification{reason: "configuration_failure"}
	}
	if errors.Is(cause, errAuthorizationFailure) {
		return terminalFailureClassification{reason: "authorization_failure"}
	}

	var networkFailure net.Error
	if errors.As(cause, &networkFailure) {
		if networkFailure.Timeout() {
			return terminalFailureClassification{reason: "provider_timeout", retryable: true}
		}
		return terminalFailureClassification{reason: "provider_transport", retryable: true}
	}

	return terminalFailureClassification{reason: "unknown_failure"}
}

func markPersistenceFailure(cause error) error {
	if cause == nil {
		return errPersistenceFailure
	}
	return errors.Join(errPersistenceFailure, cause)
}

func markConfigurationFailure(cause error) error {
	if cause == nil {
		return errConfigurationFailure
	}
	return errors.Join(errConfigurationFailure, cause)
}

func safeProviderFailureCode(code string) string {
	switch code {
	case "model_output_exhausted", "provider_timeout", "provider_transport", "provider_rate_limit",
		"provider_http_5xx", "provider_http_4xx", "provider_decode", "provider_failure",
		"provider_configuration", "provider_authentication", "provider_response_too_large",
		"transient_provider":
		return code
	default:
		return "provider_failure"
	}
}

func isRetryableProviderFailureCode(code string) bool {
	switch code {
	case "model_output_exhausted", "provider_timeout", "provider_transport", "provider_rate_limit", "provider_http_5xx", "transient_provider":
		return true
	default:
		return false
	}
}

func terminalEffectForState(to domain.RunState, effect domain.Effect, reason budgetExhaustionReason) domain.Effect {
	if to == domain.RunStateBudgetExhausted {
		effect.TerminalReason = string(reason)
		if effect.TerminalReason == "" {
			effect.TerminalReason = "budget_exhausted"
		}
		effect.Retryable = false
		return effect
	}
	if !isTerminalStateForApplication(to) {
		return effect
	}
	if effect.TerminalReason == "" {
		switch to {
		case domain.RunStateDiagnosisReadyForReview:
			effect.TerminalReason = "diagnosis_ready_for_review"
		case domain.RunStateCompletedNonCode:
			effect.TerminalReason = "completed_non_code"
		case domain.RunStateBlockedManualReview:
			effect.TerminalReason = "blocked_manual_review"
		case domain.RunStateFailed:
			effect.TerminalReason = "unknown_failure"
		default:
			effect.TerminalReason = "unknown_failure"
		}
	}
	if to != domain.RunStateFailed {
		effect.Retryable = false
	}
	return effect
}

func isTerminalStateForApplication(state domain.RunState) bool {
	switch state {
	case domain.RunStateFailed, domain.RunStateBudgetExhausted,
		domain.RunStateCompletedNonCode, domain.RunStateBlockedManualReview,
		domain.RunStateDiagnosisReadyForReview:
		return true
	default:
		return false
	}
}
