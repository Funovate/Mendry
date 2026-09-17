package coordinator_test

import (
	"context"
	"testing"

	"mendry/backend/internal/modules/remediation/adapter/coordinator"
	"mendry/backend/internal/modules/remediation/port"
)

func TestStubCoordinator(t *testing.T) {
	stub := coordinator.NewStubCoordinator()
	ctx := context.Background()

	t.Run("success case", func(t *testing.T) {
		resp, err := stub.Remediate(ctx, port.CoordinatorRequest{
			IncidentNumber: 123,
			Generation:     1,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !resp.Success {
			t.Error("expected success=true")
		}
		if resp.Message == "" {
			t.Error("expected non-empty message")
		}
	})

	t.Run("invalid incident number", func(t *testing.T) {
		_, err := stub.Remediate(ctx, port.CoordinatorRequest{
			IncidentNumber: 0,
			Generation:     1,
		})
		if err == nil {
			t.Error("expected error for invalid incident number")
		}
	})

	t.Run("invalid generation", func(t *testing.T) {
		_, err := stub.Remediate(ctx, port.CoordinatorRequest{
			IncidentNumber: 123,
			Generation:     -1,
		})
		if err == nil {
			t.Error("expected error for invalid generation")
		}
	})
}
