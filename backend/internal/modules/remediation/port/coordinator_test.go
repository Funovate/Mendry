package port_test

import (
	"context"
	"reflect"
	"testing"

	"mendry/backend/internal/modules/remediation/port"
)

// TestCoordinatorInterfaceDoesNotAcceptCredentials verifies R1 contract:
// the Remediate method accepts only (context.Context, CoordinatorRequest).
// No credential types, no git client types, no database client types.
func TestCoordinatorInterfaceDoesNotAcceptCredentials(t *testing.T) {
	coordType := reflect.TypeOf((*port.Coordinator)(nil)).Elem()
	method, ok := coordType.MethodByName("Remediate")
	if !ok {
		t.Fatal("Coordinator.Remediate method not found")
	}

	// Method signature should be: Remediate(context.Context, CoordinatorRequest) (CoordinatorResponse, error)
	// That's 3 params total: receiver (0), ctx (1), req (2)
	// and 2 results: response (0), error (1)
	if method.Type.NumIn() != 2 {
		t.Errorf("Remediate should have 2 input params (ctx, req), got %d", method.Type.NumIn())
	}
	if method.Type.NumOut() != 2 {
		t.Errorf("Remediate should have 2 output params (response, error), got %d", method.Type.NumOut())
	}

	// Verify first param is context.Context
	param0 := method.Type.In(0)
	ctxType := reflect.TypeOf((*context.Context)(nil)).Elem()
	if param0 != ctxType {
		t.Errorf("Remediate first param should be context.Context, got %v", param0)
	}

	// Verify second param is CoordinatorRequest
	param1 := method.Type.In(1)
	reqType := reflect.TypeOf(port.CoordinatorRequest{})
	if param1 != reqType {
		t.Errorf("Remediate second param should be CoordinatorRequest, got %v", param1)
	}
}

// TestCoordinatorRequestStructure verifies CoordinatorRequest contains only
// incident number and generation, no credentials or clients.
func TestCoordinatorRequestStructure(t *testing.T) {
	reqType := reflect.TypeOf(port.CoordinatorRequest{})

	// Should have exactly 2 fields: IncidentNumber and Generation
	if reqType.NumField() != 2 {
		t.Errorf("CoordinatorRequest should have exactly 2 fields, got %d", reqType.NumField())
	}

	// Verify field names and types
	incidentField, ok := reqType.FieldByName("IncidentNumber")
	if !ok {
		t.Error("CoordinatorRequest missing IncidentNumber field")
	} else if incidentField.Type.Kind() != reflect.Int64 {
		t.Errorf("IncidentNumber should be int64, got %v", incidentField.Type)
	}

	genField, ok := reqType.FieldByName("Generation")
	if !ok {
		t.Error("CoordinatorRequest missing Generation field")
	} else if genField.Type.Kind() != reflect.Int64 {
		t.Errorf("Generation should be int64, got %v", genField.Type)
	}
}
