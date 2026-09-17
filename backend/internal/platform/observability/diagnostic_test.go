package observability

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestSnapshotDiagnosticBoundsUTF8WithoutChangingContent(t *testing.T) {
	raw := "command=tail -- /var/log/app.log; stderr=" + strings.Repeat("界", DiagnosticMaxBytes)
	message, truncated := SnapshotDiagnostic(raw)
	if !truncated {
		t.Fatal("SnapshotDiagnostic() truncated = false, want true")
	}
	if len(message) > DiagnosticMaxBytes {
		t.Fatalf("diagnostic bytes = %d, want <= %d", len(message), DiagnosticMaxBytes)
	}
	if !utf8.ValidString(message) {
		t.Fatal("diagnostic is not valid UTF-8")
	}
	if !strings.HasPrefix(message, "command=tail -- /var/log/app.log; stderr=") {
		t.Fatalf("diagnostic prefix changed: %q", message)
	}
}

func TestLogSSHEvidenceRequestConsoleIncludesCommandAndReason(t *testing.T) {
	var output bytes.Buffer
	logger, err := NewLogger(LoggerOptions{
		Writer: &output, Level: "debug", Format: "console", Service: "test", Environment: "test",
	})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	LogSSHEvidenceRequest(context.Background(), logger, SSHEvidenceRequest{
		Operation: "search", Command: "tail -n 500 -- '/var/log/app.log'", Host: "logs.example.invalid",
		Port: 22, Duration: time.Millisecond, Err: errors.New("read ssh log: remote command failed: stderr=Permission denied"),
	})
	formatted := output.String()
	for _, expected := range []string{
		"ssh.command=\"tail -n 500 -- '/var/log/app.log'\"",
		"error=command",
		"error_message=\"read ssh log: remote command failed: stderr=Permission denied\"",
	} {
		if !strings.Contains(formatted, expected) {
			t.Fatalf("console output %q does not contain %q", formatted, expected)
		}
	}
}
