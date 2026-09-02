package domain_test

import (
	"strings"
	"testing"

	"fixthe/backend/internal/modules/remediation/domain"
)

// validRecoveryChallenge 构造一个满足 v1 边界的 challenge；测试通过修改字段制造违规。
func validRecoveryChallenge() domain.RecoveryChallengeV1 {
	return domain.RecoveryChallengeV1{
		SchemaVersion:            domain.RecoverySchemaVersionV1,
		Kind:                     domain.RecoveryChallengeKindToolFailure,
		Severity:                 domain.RecoverySeverityRecoverable,
		ReasonCode:               "connector_timeout",
		FailedActionRef:          "inv-7",
		AvailableCapabilities:    []string{"repository", "runtime_logs"},
		SuggestedRecoveryClasses: []string{"use_alternative"},
		Attempt:                  2,
		RemainingBudget:          map[string]int64{"model_calls": 5},
		Message:                  "runtime logs timed out; repository inspection remains available.",
	}
}

func TestRecoveryChallengeKindKnown(t *testing.T) {
	known := []domain.RecoveryChallengeKind{
		domain.RecoveryChallengeKindEvidenceCorrection,
		domain.RecoveryChallengeKindProtocolCorrection,
		domain.RecoveryChallengeKindToolFailure,
		domain.RecoveryChallengeKindContextRehydration,
		domain.RecoveryChallengeKindValidationRevision,
		domain.RecoveryChallengeKindPublicationRetry,
		domain.RecoveryChallengeKindBudget,
	}
	for _, kind := range known {
		if !kind.IsKnown() {
			t.Errorf("kind %q must be known", kind)
		}
	}
	if domain.RecoveryChallengeKind("unexpected").IsKnown() {
		t.Fatal("unknown kind must not be known")
	}
	knownSeverities := []domain.RecoverySeverity{
		domain.RecoverySeverityRecoverable,
		domain.RecoverySeverityPolicyBlocked,
		domain.RecoverySeverityHardTerminal,
	}
	for _, severity := range knownSeverities {
		if !severity.IsKnown() {
			t.Errorf("severity %q must be known", severity)
		}
	}
	if domain.RecoverySeverity("fatal").IsKnown() {
		t.Fatal("unknown severity must not be known")
	}
}

func TestRecoveryChallengeV1Validate(t *testing.T) {
	t.Run("valid challenge passes", func(t *testing.T) {
		if err := validRecoveryChallenge().Validate(); err != nil {
			t.Fatalf("Validate() error = %v", err)
		}
	})

	tests := []struct {
		name   string
		mutate func(*domain.RecoveryChallengeV1)
	}{
		{"unknown schema version", func(c *domain.RecoveryChallengeV1) { c.SchemaVersion = "v2" }},
		{"unknown kind", func(c *domain.RecoveryChallengeV1) { c.Kind = "model_stop" }},
		{"unknown severity", func(c *domain.RecoveryChallengeV1) { c.Severity = "critical" }},
		{"empty reason code", func(c *domain.RecoveryChallengeV1) { c.ReasonCode = "" }},
		{"reason code too long", func(c *domain.RecoveryChallengeV1) { c.ReasonCode = strings.Repeat("x", 129) }},
		{"attempt zero", func(c *domain.RecoveryChallengeV1) { c.Attempt = 0 }},
		{"attempt negative", func(c *domain.RecoveryChallengeV1) { c.Attempt = -1 }},
		{"message too long", func(c *domain.RecoveryChallengeV1) {
			c.Message = strings.Repeat("x", domain.MaxRecoveryChallengeMessageLength+1)
		}},
		{"empty capability", func(c *domain.RecoveryChallengeV1) { c.AvailableCapabilities = []string{""} }},
		{"too many capabilities", func(c *domain.RecoveryChallengeV1) {
			c.AvailableCapabilities = make([]string, 33)
			for i := range c.AvailableCapabilities {
				c.AvailableCapabilities[i] = "cap"
			}
		}},
		{"too many suggested classes", func(c *domain.RecoveryChallengeV1) {
			c.SuggestedRecoveryClasses = make([]string, 17)
			for i := range c.SuggestedRecoveryClasses {
				c.SuggestedRecoveryClasses[i] = "class"
			}
		}},
		{"negative budget", func(c *domain.RecoveryChallengeV1) { c.RemainingBudget["model_calls"] = -1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			challenge := validRecoveryChallenge()
			tt.mutate(&challenge)
			if err := challenge.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want error")
			}
		})
	}
}

func TestFailureFingerprintDeterminismAndChangeDetection(t *testing.T) {
	base := domain.FailureFingerprint{
		FailedActionRef: "inv-7",
		Capability:      "runtime_logs",
		ErrorCode:       "connector_timeout",
	}
	same := domain.FailureFingerprint{
		FailedActionRef: "inv-7",
		Capability:      "runtime_logs",
		ErrorCode:       "connector_timeout",
	}
	if base.Key() != same.Key() {
		t.Fatal("identical inputs must produce the same fingerprint key")
	}
	if !base.Equals(same) {
		t.Fatal("identical inputs must compare equal")
	}

	variants := []struct {
		name   string
		mutate func(*domain.FailureFingerprint)
	}{
		{"different action ref", func(f *domain.FailureFingerprint) { f.FailedActionRef = "inv-8" }},
		{"different capability", func(f *domain.FailureFingerprint) { f.Capability = "repository" }},
		{"different error code", func(f *domain.FailureFingerprint) { f.ErrorCode = "connector_rejected" }},
		{"empty action ref", func(f *domain.FailureFingerprint) { f.FailedActionRef = "" }},
	}
	for _, variant := range variants {
		t.Run(variant.name, func(t *testing.T) {
			changed := base
			variant.mutate(&changed)
			if changed.Key() == base.Key() {
				t.Fatal("changed input must produce a different fingerprint key")
			}
			if changed.Equals(base) {
				t.Fatal("changed input must not compare equal")
			}
		})
	}

	// 指纹只捕获检测输入，不规定重试次数（不是 universal one-retry rule）。
	key := base.Key()
	if len(key) != 64 {
		t.Fatalf("fingerprint key length = %d, want 64 hex chars", len(key))
	}
	for _, r := range key {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Fatalf("fingerprint key contains non-hex character %q", r)
		}
	}
}
