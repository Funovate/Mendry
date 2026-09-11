package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"unicode/utf8"

	"mendry/backend/internal/platform/buildinfo"
)

func TestSnapshotRemediationPayloadRedactsBeforeHashAndTruncation(t *testing.T) {
	secretMarkers := []string{
		"sk-test-secret-12345678",
		"Bearer abcdefghijklmnop",
		"https://user:password@example.test/repo.git",
		"-----BEGIN PRIVATE KEY-----\nprivate-material\n-----END PRIVATE KEY-----",
		"password=plain-password",
	}
	input := strings.Repeat("界", RemediationPayloadMaxBytes/3) + strings.Join(secretMarkers, "\n")
	snapshot, err := SnapshotRemediationPayload(input)
	if err != nil {
		t.Fatalf("SnapshotRemediationPayload() error = %v", err)
	}
	if !snapshot.Truncated || snapshot.LoggedBytes > RemediationPayloadMaxBytes {
		t.Fatalf("snapshot bounds = %+v", snapshot)
	}
	if !utf8.ValidString(snapshot.Text) {
		t.Fatal("snapshot text is not valid UTF-8")
	}
	if snapshot.Bytes <= snapshot.LoggedBytes || len(snapshot.SHA256) != 64 {
		t.Fatalf("snapshot metadata = %+v", snapshot)
	}
	full, err := SnapshotRemediationPayload(strings.Join(secretMarkers, "\n"))
	if err != nil {
		t.Fatalf("SnapshotRemediationPayload(markers) error = %v", err)
	}
	for _, marker := range secretMarkers {
		if strings.Contains(full.Text, marker) {
			t.Fatalf("secret marker leaked: %q in %q", marker, full.Text)
		}
	}
}

func TestSnapshotRemediationPayloadRedactsStructuredSensitiveKeys(t *testing.T) {
	snapshot, err := SnapshotRemediationPayload(map[string]any{
		"prompt":       "safe",
		"apiKey":       "key-material",
		"value":        "plain-value-material",
		"nested":       map[string]any{"credentialValue": "credential-material"},
		"usage_tokens": 12,
	})
	if err != nil {
		t.Fatalf("SnapshotRemediationPayload() error = %v", err)
	}
	if strings.Contains(snapshot.Text, "key-material") || strings.Contains(snapshot.Text, "credential-material") ||
		strings.Contains(snapshot.Text, "plain-value-material") {
		t.Fatalf("structured secret leaked: %s", snapshot.Text)
	}
	if !strings.Contains(snapshot.Text, `"usage_tokens":12`) {
		t.Fatalf("usage counter was redacted: %s", snapshot.Text)
	}
}

func TestConsoleHandlerProjectsRemediationPayloadAsAtomicBlock(t *testing.T) {
	var output bytes.Buffer
	logger, err := NewLogger(LoggerOptions{
		Writer: &output, Level: "debug", Format: "console", Service: "test",
		Environment: "test", Build: buildinfo.Info{Version: "dev", Commit: "test"},
	})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	Log(context.Background(), logger, slog.LevelDebug, EventRemediationModelPayload, "model payload",
		slog.String(FieldComponent, "remediation"),
		slog.String(FieldRunID, "run-1"),
		slog.String(FieldPayloadKind, "model_response"),
		slog.String(FieldPayload, "{\n  \"kind\": \"diagnosis\"\n}"),
	)
	formatted := output.String()
	if strings.Contains(formatted, `payload="`) || !strings.Contains(formatted, "remediation_payload[model_response]:\n{\n") {
		t.Fatalf("unexpected console payload projection: %q", formatted)
	}
}

func TestJSONLoggerKeepsRemediationPayloadContract(t *testing.T) {
	var output bytes.Buffer
	logger, err := NewLogger(LoggerOptions{
		Writer: &output, Level: "debug", Format: "json", Service: "test",
		Environment: "test", Build: buildinfo.Info{Version: "dev", Commit: "test"},
	})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	Log(context.Background(), logger, slog.LevelDebug, EventRemediationToolPayload, "tool payload",
		slog.String(FieldRunID, "run-1"), slog.String(FieldPayloadKind, "tool_result"),
		slog.String(FieldPayload, `{"ok":true}`), slog.Int(FieldPayloadBytes, 11),
		slog.Int(FieldPayloadLoggedBytes, 11), slog.Bool(FieldPayloadTruncated, false),
		slog.String(FieldPayloadSHA256, strings.Repeat("a", 64)),
	)
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("decode JSON log: %v", err)
	}
	for key := range map[string]struct{}{
		FieldEvent: {}, FieldRunID: {}, FieldPayloadKind: {}, FieldPayload: {},
		FieldPayloadBytes: {}, FieldPayloadLoggedBytes: {}, FieldPayloadTruncated: {}, FieldPayloadSHA256: {},
	} {
		if _, ok := record[key]; !ok {
			t.Fatalf("JSON record missing %s: %#v", key, record)
		}
	}
}
