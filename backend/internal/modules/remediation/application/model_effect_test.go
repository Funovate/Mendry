package application

import (
	"testing"

	"mendry/backend/internal/modules/remediation/domain"
)

func TestModelEffectUsesProviderCallCountAndLegacyDefault(t *testing.T) {
	withRetry := modelEffect(domain.ModelResult{ModelCalls: 2, UsageTokensIn: 10, UsageTokensOut: 4})
	if withRetry.ModelCalls != 2 || withRetry.ModelTokensIn != 10 || withRetry.ModelTokensOut != 4 {
		t.Fatalf("retry effect = %#v", withRetry)
	}

	legacy := modelEffect(domain.ModelResult{UsageTokens: 7})
	if legacy.ModelCalls != 1 || legacy.ModelTokensOut != 7 {
		t.Fatalf("legacy effect = %#v", legacy)
	}
}
