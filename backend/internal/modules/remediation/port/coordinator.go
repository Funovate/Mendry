// Package port defines interfaces for external dependencies.
package port

import "context"

// CoordinatorRequest represents a remediation request sent to the coordinator.
type CoordinatorRequest struct {
	IncidentNumber int64
	Generation     int64
}

// CoordinatorResponse represents the coordinator's response.
type CoordinatorResponse struct {
	Success bool
	Message string
}

// Coordinator orchestrates agentic remediation workflows.
// R1: This interface accepts only incident number and generation.
// The coordinator receives NO credentials, NO git clients, NO database clients.
type Coordinator interface {
	Remediate(ctx context.Context, req CoordinatorRequest) (CoordinatorResponse, error)
}
