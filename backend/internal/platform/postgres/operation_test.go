package postgres

import (
	"context"
	"testing"
)

func TestOperationUsesDeclaredNameOrSafeVerb(t *testing.T) {
	tests := []struct {
		name      string
		ctx       context.Context
		statement string
		want      string
	}{
		{name: "declared", ctx: WithOperation(context.Background(), "incident.get_by_id"), statement: "SELECT secret", want: "incident.get_by_id"},
		{name: "sqlc-comment", ctx: context.Background(), statement: "-- name: GetIncident :one\nSELECT secret FROM incidents", want: "select"},
		{name: "block-comment", ctx: context.Background(), statement: "/* generated */ UPDATE incidents SET secret = $1", want: "update"},
		{name: "unknown", ctx: WithOperation(context.Background(), "User Supplied"), statement: "VACUUM private_table", want: "unknown"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := operationFromContext(test.ctx, test.statement); got != test.want {
				t.Fatalf("operation = %q, want %q", got, test.want)
			}
		})
	}
}
