package domain

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Attempt represents a single remediation run attempt within a series.
type Attempt struct {
	ID              uuid.UUID
	SeriesID        uuid.UUID
	AttemptNumber   int
	State           RunState
	StartedAt       time.Time
	EndedAt         *time.Time
	ElapsedMS       *int64
	ModelCalls      int
	ModelTokensIn   int64
	ModelTokensOut  int64
	ModelCostCents  int64
	ModelProvider   string
	ModelName       string
	ToolCalls       int
	EvidenceBytes   int64
	RepositoryBytes int64
	Version         int64
}

// Validate checks if the attempt has valid field values.
func (a *Attempt) Validate() error {
	if a.ID == uuid.Nil {
		return fmt.Errorf("attempt ID cannot be nil")
	}
	if a.SeriesID == uuid.Nil {
		return fmt.Errorf("series ID cannot be nil")
	}
	if a.AttemptNumber < 0 {
		return fmt.Errorf("attempt number cannot be negative")
	}
	if _, err := ParseRunState(string(a.State)); err != nil {
		return fmt.Errorf("invalid state: %w", err)
	}
	if a.EndedAt != nil && a.EndedAt.Before(a.StartedAt) {
		return fmt.Errorf("ended_at cannot be before started_at")
	}
	if a.Version < 0 {
		return fmt.Errorf("version cannot be negative")
	}
	return nil
}

// Effect represents the side effects of a state transition.
type Effect struct {
	ModelCalls      int
	ModelTokensIn   int64
	ModelTokensOut  int64
	ModelCostCents  int64
	ModelProvider   string
	ModelName       string
	ToolCalls       int
	EvidenceBytes   int64
	RepositoryBytes int64
}
