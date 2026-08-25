package domain_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"fixthe/backend/internal/modules/remediation/domain"
)

func TestParseRunState(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    domain.RunState
		wantErr bool
	}{
		{"queued", "queued", domain.RunStateQueued, false},
		{"running", "running", domain.RunStateRunning, false},
		{"failed", "failed", domain.RunStateFailed, false},
		{"budget_exhausted", "budget_exhausted", domain.RunStateBudgetExhausted, false},
		{"completed_non_code", "completed_non_code", domain.RunStateCompletedNonCode, false},
		{"blocked_manual_review", "blocked_manual_review", domain.RunStateBlockedManualReview, false},
		{"invalid", "invalid", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.ParseRunState(tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseRunState() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseRunState() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAttemptValidate(t *testing.T) {
	validAttempt := domain.Attempt{
		ID:            uuid.New(),
		SeriesID:      uuid.New(),
		AttemptNumber: 1,
		State:         domain.RunStateQueued,
		StartedAt:     time.Now().Add(-time.Hour),
		Version:       1,
	}
	if err := validAttempt.Validate(); err != nil {
		t.Errorf("valid attempt should not error, got: %v", err)
	}

	tests := []struct {
		name    string
		modify  func(domain.Attempt) domain.Attempt
		wantErr bool
	}{
		{"zero ID", func(a domain.Attempt) domain.Attempt { a.ID = uuid.Nil; return a }, true},
		{"zero series ID", func(a domain.Attempt) domain.Attempt { a.SeriesID = uuid.Nil; return a }, true},
		{"negative attempt number", func(a domain.Attempt) domain.Attempt { a.AttemptNumber = -1; return a }, true},
		{"invalid state", func(a domain.Attempt) domain.Attempt { a.State = "invalid"; return a }, true},
		{"negative version", func(a domain.Attempt) domain.Attempt { a.Version = -1; return a }, true},
		{"ended before started", func(a domain.Attempt) domain.Attempt {
			now := time.Now()
			a.StartedAt = now
			endedAt := now.Add(-time.Hour)
			a.EndedAt = &endedAt
			return a
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attempt := tt.modify(validAttempt)
			err := attempt.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
