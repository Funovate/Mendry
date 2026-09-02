package application_test

import (
	"strings"
	"testing"

	"fixthe/backend/internal/modules/remediation/application"
	"fixthe/backend/internal/modules/remediation/domain"
)

func TestNewRecoveryChallenge(t *testing.T) {
	t.Run("builds a validated challenge", func(t *testing.T) {
		challenge, err := application.NewRecoveryChallenge(
			domain.RecoveryChallengeKindToolFailure,
			domain.RecoverySeverityRecoverable,
			"connector_timeout",
			"inv-7",
			[]string{"repository", " runtime_logs "},
			[]string{"use_alternative"},
			2,
			map[string]int64{"model_calls": 5},
			"runtime logs timed out; repository inspection remains available.",
		)
		if err != nil {
			t.Fatalf("NewRecoveryChallenge() error = %v", err)
		}
		if challenge.SchemaVersion != domain.RecoverySchemaVersionV1 {
			t.Errorf("schema version = %q, want v1", challenge.SchemaVersion)
		}
		if len(challenge.AvailableCapabilities) != 2 || challenge.AvailableCapabilities[1] != "runtime_logs" {
			t.Errorf("capabilities were not trimmed: %v", challenge.AvailableCapabilities)
		}
		if err := challenge.Validate(); err != nil {
			t.Fatalf("challenge must be valid: %v", err)
		}
	})

	t.Run("sanitizes credential literals from the message", func(t *testing.T) {
		challenge, err := application.NewRecoveryChallenge(
			domain.RecoveryChallengeKindEvidenceCorrection,
			domain.RecoverySeverityRecoverable,
			"evidence_correction",
			"",
			[]string{"repository"},
			nil,
			1,
			nil,
			"token sk-abc123def456ghi7jklmno was rejected; bearer abc123def456ghi7jklmnoqrs expired.",
		)
		if err != nil {
			t.Fatalf("NewRecoveryChallenge() error = %v", err)
		}
		if strings.Contains(challenge.Message, "sk-abc123def456ghi7jklmno") {
			t.Errorf("message still contains the API key literal: %q", challenge.Message)
		}
		if strings.Contains(challenge.Message, "abc123def456ghi7jklmnoqrs") {
			t.Errorf("message still contains the bearer token literal: %q", challenge.Message)
		}
	})

	t.Run("truncates an oversized message to the bound", func(t *testing.T) {
		message := strings.Repeat("x", domain.MaxRecoveryChallengeMessageLength+500)
		challenge, err := application.NewRecoveryChallenge(
			domain.RecoveryChallengeKindProtocolCorrection,
			domain.RecoverySeverityRecoverable,
			"invalid_envelope",
			"",
			nil,
			nil,
			1,
			nil,
			message,
		)
		if err != nil {
			t.Fatalf("NewRecoveryChallenge() error = %v", err)
		}
		if len([]rune(challenge.Message)) != domain.MaxRecoveryChallengeMessageLength {
			t.Fatalf("message length = %d, want %d", len([]rune(challenge.Message)), domain.MaxRecoveryChallengeMessageLength)
		}
	})

	t.Run("does not alias the caller budget map", func(t *testing.T) {
		budget := map[string]int64{"model_calls": 5}
		challenge, err := application.NewRecoveryChallenge(
			domain.RecoveryChallengeKindToolFailure,
			domain.RecoverySeverityRecoverable,
			"connector_timeout",
			"",
			nil, nil, 1, budget, "message",
		)
		if err != nil {
			t.Fatalf("NewRecoveryChallenge() error = %v", err)
		}
		budget["model_calls"] = 999
		if got := challenge.RemainingBudget["model_calls"]; got != 5 {
			t.Fatalf("challenge budget changed after caller mutation: %d, want 5", got)
		}
	})

	t.Run("rejects invalid kind", func(t *testing.T) {
		_, err := application.NewRecoveryChallenge(
			domain.RecoveryChallengeKind("model_stop"),
			domain.RecoverySeverityRecoverable,
			"reason",
			"",
			nil, nil, 1, nil, "message",
		)
		if err == nil {
			t.Fatal("NewRecoveryChallenge() error = nil, want rejection")
		}
	})

	t.Run("rejects invalid severity and attempt", func(t *testing.T) {
		if _, err := application.NewRecoveryChallenge(
			domain.RecoveryChallengeKindToolFailure,
			domain.RecoverySeverity("critical"),
			"reason",
			"",
			nil, nil, 1, nil, "message",
		); err == nil {
			t.Fatal("invalid severity must be rejected")
		}
		if _, err := application.NewRecoveryChallenge(
			domain.RecoveryChallengeKindToolFailure,
			domain.RecoverySeverityRecoverable,
			"reason",
			"",
			nil, nil, 0, nil, "message",
		); err == nil {
			t.Fatal("zero attempt must be rejected")
		}
	})
}
