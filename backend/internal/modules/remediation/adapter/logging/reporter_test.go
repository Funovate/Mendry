package logging_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"mendry/backend/internal/modules/remediation/adapter/logging"
	remediationapplication "mendry/backend/internal/modules/remediation/application"
	"mendry/backend/internal/platform/observability"
)

func TestFailureReporterLogsCauseAndStackWithoutPayload(t *testing.T) {
	var output bytes.Buffer
	logger, err := observability.NewLogger(observability.LoggerOptions{
		Writer: &output, Level: "debug", Format: "json", Service: "mendry-test", Environment: "test",
	})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	reporter, err := logging.NewFailureReporter(logger)
	if err != nil {
		t.Fatalf("NewFailureReporter() error = %v", err)
	}
	cause := fmt.Errorf("coordinator failed: %w", errors.New("provider unavailable"))
	reporter.Report(context.Background(), remediationapplication.TriggerRequest{
		IncidentID: "incident-1", LifecycleGeneration: 1, Priority: "P2", Reason: "automatic",
	}, cause)
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("log JSON error = %v; output = %q", err, output.String())
	}
	if record[observability.FieldEvent] != observability.EventRemediationFailed {
		t.Fatalf("event = %#v", record[observability.FieldEvent])
	}
	if record[observability.FieldErrorMessage] != cause.Error() {
		t.Fatalf("error message = %#v", record[observability.FieldErrorMessage])
	}
	if !strings.Contains(record[observability.FieldErrorStack].(string), "TestFailureReporterLogsCauseAndStackWithoutPayload") {
		t.Fatalf("error stack = %#v", record[observability.FieldErrorStack])
	}
	if strings.Contains(output.String(), "webhook") || strings.Contains(output.String(), "prompt") {
		t.Fatalf("failure log contains payload-like content: %s", output.String())
	}
}

func TestFailureReporterLogsSafeContinuationGateDecision(t *testing.T) {
	var output bytes.Buffer
	logger, err := observability.NewLogger(observability.LoggerOptions{
		Writer: &output, Level: "info", Format: "json", Service: "mendry-test", Environment: "test",
	})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	reporter, err := logging.NewFailureReporter(logger)
	if err != nil {
		t.Fatalf("NewFailureReporter() error = %v", err)
	}
	reporter.ReportGate(context.Background(), remediationapplication.TriggerRequest{
		IncidentID: "incident-1", LifecycleGeneration: 3, Priority: "P2", Reason: "automatic", ContextVersion: 4,
	}, remediationapplication.GateObservation{Outcome: "skipped", Reason: "unchanged_context"})
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("log JSON error = %v; output = %q", err, output.String())
	}
	if record[observability.FieldEvent] != observability.EventRemediationContinuationGate ||
		record[observability.FieldGateOutcome] != "skipped" || record[observability.FieldGateReason] != "unchanged_context" ||
		record[observability.FieldContextVersion] != float64(4) {
		t.Fatalf("gate record = %#v", record)
	}
	if strings.Contains(output.String(), "payload") || strings.Contains(output.String(), "secret") {
		t.Fatalf("gate record contains unsafe metadata: %s", output.String())
	}
}
