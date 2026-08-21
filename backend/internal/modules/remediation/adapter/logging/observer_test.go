package logging_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"fixthe/backend/internal/modules/remediation/adapter/logging"
	"fixthe/backend/internal/modules/remediation/application"
	"fixthe/backend/internal/modules/remediation/domain"
	"fixthe/backend/internal/platform/observability"
)

var remediationSecretMarkers = []string{
	"sk-test-secret-12345678",
	"plain-password",
	"https://user:password@example.test/private.git",
	"private-key-material",
	"Bearer abcdefghijklmnop",
}

func TestObserverInfoProgressContainsNoPayload(t *testing.T) {
	var output bytes.Buffer
	observer := newTestObserver(t, &output, "info")
	run := application.RunIdentity{RunID: "run-1", SeriesID: "series-1", IncidentID: "incident-1", LifecycleGeneration: 3}
	observer.RunStarted(context.Background(), application.RunStartedObservation{
		Run: run, Phase: domain.RunStateQueued, TriggerReason: "automatic", Priority: "P2",
	})
	observer.ModelTurnCompleted(context.Background(), application.ModelTurnObservation{
		Run: run, Phase: domain.RunStateDiagnosing, Sequence: 1, Duration: time.Millisecond,
		Outcome: "success", EnvelopeKind: "diagnosis",
		Request:  domain.ModelTurn{SystemPrompt: remediationSecretPayload()},
		Response: domain.ModelResult{Content: remediationSecretPayload(), UsageTokensIn: 4, UsageTokensOut: 2},
	})
	records := decodeJSONLines(t, output.Bytes())
	if len(records) != 2 {
		t.Fatalf("INFO records = %d, want 2: %s", len(records), output.String())
	}
	for _, record := range records {
		if _, ok := record[observability.FieldPayload]; ok {
			t.Fatalf("INFO record contains payload: %#v", record)
		}
	}
	assertNoMarkers(t, output.String())
}

func TestObserverDebugPayloadsAreBoundedRedactedAndCorrelated(t *testing.T) {
	var output bytes.Buffer
	observer := newTestObserver(t, &output, "debug")
	run := application.RunIdentity{RunID: "run-1", SeriesID: "series-1", IncidentID: "incident-1", LifecycleGeneration: 3}
	markerText := remediationSecretPayload()
	oversized := strings.Repeat("界", observability.RemediationPayloadMaxBytes/3+100) + markerText
	observer.ContextCompleted(context.Background(), application.ContextObservation{
		Run: run, Phase: domain.RunStatePreparingContext, Operation: "evidence.search",
		Duration: time.Millisecond, Outcome: "success", Bytes: int64(len(oversized)),
		PayloadKind: "evidence_page", Payload: oversized,
	})
	observer.ModelTurnCompleted(context.Background(), application.ModelTurnObservation{
		Run: run, Phase: domain.RunStateDiagnosing, Sequence: 1, Duration: time.Millisecond, Outcome: "success",
		Request:  domain.ModelTurn{SystemPrompt: markerText, UserMessage: "password=plain-password"},
		Response: domain.ModelResult{Content: markerText},
	})
	observer.ToolCompleted(context.Background(), application.ToolObservation{
		Run: run, Phase: domain.RunStateDiagnosing, Sequence: 2, Tool: application.ToolRepoReadFile,
		Duration: time.Millisecond, Outcome: "success",
		Parameters: map[string]interface{}{"authorization": remediationSecretMarkers[4]},
		Result:     map[string]interface{}{"content": markerText},
	})
	records := decodeJSONLines(t, output.Bytes())
	payloads := 0
	for _, record := range records {
		if _, ok := record[observability.FieldPayload]; !ok {
			continue
		}
		payloads++
		if record[observability.FieldRunID] != "run-1" || record[observability.FieldSeriesID] != "series-1" {
			t.Fatalf("payload correlation missing: %#v", record)
		}
		logged := int(record[observability.FieldPayloadLoggedBytes].(float64))
		if logged > observability.RemediationPayloadMaxBytes {
			t.Fatalf("payload logged bytes = %d", logged)
		}
		if len(record[observability.FieldPayloadSHA256].(string)) != 64 {
			t.Fatalf("payload hash = %#v", record[observability.FieldPayloadSHA256])
		}
	}
	if payloads != 5 {
		t.Fatalf("payload records = %d, want 5: %s", payloads, output.String())
	}
	assertNoMarkers(t, output.String())
}

func TestObserverToolFailureIncludesClassificationAndDiagnostic(t *testing.T) {
	var output bytes.Buffer
	observer := newTestObserver(t, &output, "info")
	run := application.RunIdentity{RunID: "run-1", SeriesID: "series-1"}
	observer.ToolCompleted(context.Background(), application.ToolObservation{
		Run: run, Phase: domain.RunStateDiagnosing, Sequence: 2,
		Tool: application.ToolEvidenceSearch, Duration: time.Millisecond,
		Outcome: "failure", FailureClass: "adapter", ErrorCode: "remote_execution", Retryable: true,
		ErrorMessage: "read ssh log: remote command failed: command=tail -n 500 -- '/var/log/app.log'; stderr=Permission denied (publickey)",
	})
	records := decodeJSONLines(t, output.Bytes())
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1: %s", len(records), output.String())
	}
	record := records[0]
	if record[observability.FieldErrorClass] != "adapter" ||
		record[observability.FieldErrorCode] != "remote_execution" ||
		record[observability.FieldRetryable] != true {
		t.Fatalf("tool failure fields = %#v", record)
	}
	if !strings.Contains(record[observability.FieldErrorMessage].(string), "Permission denied (publickey)") {
		t.Fatalf("tool diagnostic = %#v", record[observability.FieldErrorMessage])
	}
}

func TestObserverPolicyRejectionRemainsDistinct(t *testing.T) {
	var output bytes.Buffer
	observer := newTestObserver(t, &output, "info")
	observer.ToolCompleted(context.Background(), application.ToolObservation{
		Run: application.RunIdentity{RunID: "run-1"}, Phase: domain.RunStateDiagnosing,
		Sequence: 1, Tool: "workspace.apply_patch", Outcome: "rejected", FailureClass: "policy",
		RejectionCode: application.RejectUnavailable, ErrorCode: string(application.RejectUnavailable),
		ErrorMessage: "tool_unavailable: tool is not in the run catalog",
	})
	record := decodeJSONLines(t, output.Bytes())[0]
	if record[observability.FieldErrorClass] != "policy" ||
		record[observability.FieldErrorCode] != string(application.RejectUnavailable) ||
		record[observability.FieldToolRejectionCode] != string(application.RejectUnavailable) ||
		record[observability.FieldRetryable] != false {
		t.Fatalf("policy rejection fields = %#v", record)
	}
}

func TestObserverModelFailureIncludesBoundedDiagnostic(t *testing.T) {
	var output bytes.Buffer
	observer := newTestObserver(t, &output, "info")
	message := "decode agent envelope: envelope decode: json: unknown field \"type\""
	observer.ModelTurnCompleted(context.Background(), application.ModelTurnObservation{
		Run: application.RunIdentity{RunID: "run-1"}, Phase: domain.RunStateDiagnosing,
		Sequence: 3, Outcome: "failure", FailureClass: "decode", ErrorMessage: message,
	})
	record := decodeJSONLines(t, output.Bytes())[0]
	if record[observability.FieldErrorClass] != "decode" || record[observability.FieldErrorMessage] != message {
		t.Fatalf("model failure fields = %#v", record)
	}
}

func TestObserverDiagnosticMarksTruncation(t *testing.T) {
	var output bytes.Buffer
	observer := newTestObserver(t, &output, "info")
	observer.ModelTurnCompleted(context.Background(), application.ModelTurnObservation{
		Run: application.RunIdentity{RunID: "run-1"}, Phase: domain.RunStateDiagnosing,
		Sequence: 1, Outcome: "failure", FailureClass: "provider",
		ErrorMessage: strings.Repeat("x", observability.DiagnosticMaxBytes+1),
	})
	record := decodeJSONLines(t, output.Bytes())[0]
	if record[observability.FieldErrorMessageTruncated] != true {
		t.Fatalf("truncation field = %#v", record[observability.FieldErrorMessageTruncated])
	}
	if len(record[observability.FieldErrorMessage].(string)) > observability.DiagnosticMaxBytes {
		t.Fatalf("diagnostic bytes = %d", len(record[observability.FieldErrorMessage].(string)))
	}
}

func remediationSecretPayload() string {
	return strings.Join([]string{
		remediationSecretMarkers[0],
		"password=" + remediationSecretMarkers[1],
		remediationSecretMarkers[2],
		"-----BEGIN PRIVATE KEY-----\n" + remediationSecretMarkers[3] + "\n-----END PRIVATE KEY-----",
		remediationSecretMarkers[4],
	}, "\n")
}

func newTestObserver(t *testing.T, output *bytes.Buffer, level string) *logging.Observer {
	t.Helper()
	logger, err := observability.NewLogger(observability.LoggerOptions{
		Writer: output, Level: level, Format: "json", Service: "test", Environment: "test",
	})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	observer, err := logging.NewObserver(logger)
	if err != nil {
		t.Fatalf("NewObserver() error = %v", err)
	}
	return observer
}

func decodeJSONLines(t *testing.T, raw []byte) []map[string]any {
	t.Helper()
	var records []map[string]any
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	buffer := make([]byte, 1024)
	scanner.Buffer(buffer, 256*1024)
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("decode JSON log: %v; line=%q", err, scanner.Text())
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan JSON logs: %v", err)
	}
	return records
}

func assertNoMarkers(t *testing.T, output string) {
	t.Helper()
	for _, marker := range remediationSecretMarkers {
		if index := strings.Index(output, marker); index >= 0 {
			start := max(0, index-80)
			end := min(len(output), index+len(marker)+80)
			t.Fatalf("secret marker %q leaked near %q", marker, output[start:end])
		}
	}
}
