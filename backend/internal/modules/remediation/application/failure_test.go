package application

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"

	"fixthe/backend/internal/modules/remediation/domain"
)

type timeoutFailure struct{}

func (timeoutFailure) Error() string   { return "transport timeout" }
func (timeoutFailure) Timeout() bool   { return true }
func (timeoutFailure) Temporary() bool { return true }

func TestClassifyTerminalFailureMatrix(t *testing.T) {
	tests := []struct {
		name      string
		cause     error
		wantCode  string
		wantRetry bool
	}{
		{name: "output exhausted", cause: domain.ErrModelOutputExhausted, wantCode: "model_output_exhausted", wantRetry: true},
		{name: "typed provider transport", cause: &domain.ProviderRuntimeError{Code: "provider_transport", Retryable: true}, wantCode: "provider_transport", wantRetry: true},
		{name: "typed provider status", cause: &domain.ProviderRuntimeError{Code: "provider_http_5xx", Retryable: true}, wantCode: "provider_http_5xx", wantRetry: true},
		{name: "typed non retryable provider", cause: &domain.ProviderRuntimeError{Code: "provider_http_4xx", Retryable: true}, wantCode: "provider_http_4xx", wantRetry: false},
		{name: "deadline", cause: context.DeadlineExceeded, wantCode: "elapsed", wantRetry: false},
		{name: "canceled", cause: context.Canceled, wantCode: "canceled", wantRetry: false},
		{name: "invalid envelope", cause: fmt.Errorf("decode failed: %w", ErrInvalidEnvelope), wantCode: "invalid_envelope", wantRetry: false},
		{name: "persistence", cause: markPersistenceFailure(errors.New("database detail")), wantCode: "persistence_failure", wantRetry: false},
		{name: "policy", cause: &ToolRejection{Code: RejectArguments, Tool: "repository.read_file"}, wantCode: "policy_rejection", wantRetry: false},
		{name: "typed connector", cause: &domain.ToolRuntimeError{Code: "transport", Retryable: true}, wantCode: "transport", wantRetry: true},
		{name: "network timeout", cause: timeoutFailure{}, wantCode: "provider_timeout", wantRetry: true},
		{name: "unknown", cause: errors.New("provider output contains a transient-looking phrase"), wantCode: "unknown_failure", wantRetry: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := classifyTerminalFailure(test.cause)
			if got.reason != test.wantCode || got.retryable != test.wantRetry {
				t.Fatalf("classification = %#v, want code=%q retryable=%t", got, test.wantCode, test.wantRetry)
			}
		})
	}
}

func TestClassifyTerminalFailureDoesNotUseProviderErrorText(t *testing.T) {
	cause := &domain.ProviderRuntimeError{
		Code:      "unrecognized-provider-code",
		Retryable: true,
		Cause:     errors.New("temporary outage; retry this request"),
	}
	got := classifyTerminalFailure(cause)
	if got.reason != "provider_failure" || got.retryable {
		t.Fatalf("classification = %#v, want provider_failure/non-retryable", got)
	}

	var _ net.Error = timeoutFailure{}
}

func TestTerminalEffectForStateAddsSafeDefaults(t *testing.T) {
	failed := terminalEffectForState(domain.RunStateFailed, domain.Effect{}, "")
	if failed.TerminalReason != "unknown_failure" || failed.Retryable {
		t.Fatalf("failed effect = %#v", failed)
	}
	blocked := terminalEffectForState(domain.RunStateBlockedManualReview, domain.Effect{}, "")
	if blocked.TerminalReason != "blocked_manual_review" || blocked.Retryable {
		t.Fatalf("blocked effect = %#v", blocked)
	}
	budget := terminalEffectForState(domain.RunStateBudgetExhausted, domain.Effect{Retryable: true}, budgetReasonElapsed)
	if budget.TerminalReason != string(budgetReasonElapsed) || budget.Retryable {
		t.Fatalf("budget effect = %#v", budget)
	}
}
