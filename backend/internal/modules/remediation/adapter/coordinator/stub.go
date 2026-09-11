package coordinator

import (
	"context"
	"fmt"

	"mendry/backend/internal/modules/remediation/port"
)

// StubCoordinator is a minimal coordinator that returns canned responses.
// R5: This proves the port interface is sufficient without building the real coordinator.
type StubCoordinator struct{}

// NewStubCoordinator creates a new stub coordinator.
func NewStubCoordinator() *StubCoordinator {
	return &StubCoordinator{}
}

// Remediate returns a canned success response.
func (c *StubCoordinator) Remediate(ctx context.Context, req port.CoordinatorRequest) (port.CoordinatorResponse, error) {
	if req.IncidentNumber <= 0 {
		return port.CoordinatorResponse{}, fmt.Errorf("invalid incident number")
	}
	if req.Generation < 0 {
		return port.CoordinatorResponse{}, fmt.Errorf("invalid generation")
	}
	return port.CoordinatorResponse{
		Success: true,
		Message: fmt.Sprintf("stub remediation for incident %d generation %d", req.IncidentNumber, req.Generation),
	}, nil
}
