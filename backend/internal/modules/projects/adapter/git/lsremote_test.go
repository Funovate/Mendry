package git

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fixthe/backend/internal/modules/projects/application"
	"fixthe/backend/internal/modules/projects/domain"
	"fixthe/backend/internal/platform/observability"
)

func TestPrivateKeyBytesKeepsOnlyPEMBlock(t *testing.T) {
	key := []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nsecret-key\n-----END OPENSSH PRIVATE KEY-----\npassphrase\n")
	got := privateKeyBytes(key)
	if !bytes.Equal(got, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nsecret-key\n-----END OPENSSH PRIVATE KEY-----\n")) {
		t.Fatalf("privateKeyBytes() = %q", got)
	}
}

func TestListRefsLogsBoundedSuccess(t *testing.T) {
	const stdout = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\trefs/heads/main\n"
	var output bytes.Buffer
	lister := NewLister(testLogger(t, &output))
	lister.command = writeStubCommand(t, "#!/bin/sh\nprintf '%s' '"+stdout+"'\n")

	got, err := lister.ListRefs(context.Background(), "https://git.example.com/app.git", "https", domain.SecretGitCredential, []byte("deploy:token-value"))
	if err != nil {
		t.Fatalf("ListRefs() error = %v", err)
	}
	if got != stdout {
		t.Fatalf("ListRefs() = %q", got)
	}

	record := onlyJSONRecord(t, output.Bytes())
	if record[observability.FieldEvent] != observability.EventGitRequestCompleted {
		t.Fatalf("event = %#v", record[observability.FieldEvent])
	}
	if record[observability.FieldComponent] != "git" {
		t.Fatalf("component = %#v", record[observability.FieldComponent])
	}
	if record[observability.FieldGitOperation] != opLSRemote {
		t.Fatalf("operation = %#v", record[observability.FieldGitOperation])
	}
	if record[observability.FieldHTTPHost] != "git.example.com" {
		t.Fatalf("host = %#v", record[observability.FieldHTTPHost])
	}
	if record[observability.FieldHTTPPath] != "/app.git" {
		t.Fatalf("path = %#v", record[observability.FieldHTTPPath])
	}
	if record[observability.FieldOutcome] != "success" {
		t.Fatalf("outcome = %#v", record[observability.FieldOutcome])
	}
	request, _ := record[observability.FieldHTTPRequest].(string)
	if !strings.Contains(request, "ls-remote") || !strings.Contains(request, "https://git.example.com/app.git") {
		t.Fatalf("request = %#v", request)
	}
	if strings.Contains(request, "token-value") || strings.Contains(request, "deploy:") {
		t.Fatalf("request leaked userinfo: %#v", request)
	}
	response, _ := record[observability.FieldHTTPResponse].(string)
	if !strings.Contains(response, "refs/heads/main") {
		t.Fatalf("response = %#v", response)
	}
	if strings.Contains(output.String(), "token-value") {
		t.Fatalf("log leaked credential: %s", output.String())
	}
}

func TestListRefsRedactsFailureSnapshot(t *testing.T) {
	var output bytes.Buffer
	lister := NewLister(testLogger(t, &output))
	// $4 是认证后的 HTTPS remote；日志必须脱敏 userinfo 和 PEM，不能原样写出。
	lister.command = writeStubCommand(t, "#!/bin/sh\nprintf \"fatal: Authentication failed for '%s'\\n\" \"$4\" >&2\nprintf '%s\\n' '-----BEGIN OPENSSH PRIVATE KEY-----' 'secret-key-material-must-not-leak' '-----END OPENSSH PRIVATE KEY-----' >&2\nexit 1\n")

	_, err := lister.ListRefs(context.Background(), "https://git.example.com/app.git", "https", domain.SecretGitCredential, []byte("git:super-secret-token"))
	if !errors.Is(err, application.ErrGitUnreachable) {
		t.Fatalf("error = %v", err)
	}

	record := onlyJSONRecord(t, output.Bytes())
	if record[observability.FieldOutcome] != "failure" {
		t.Fatalf("outcome = %#v", record[observability.FieldOutcome])
	}
	if record[observability.FieldErrorClass] != observability.OutboundCommand {
		t.Fatalf("error_class = %#v", record[observability.FieldErrorClass])
	}
	if record[observability.FieldHTTPHost] != "git.example.com" || record[observability.FieldHTTPPath] != "/app.git" {
		t.Fatalf("identity = %#v %#v", record[observability.FieldHTTPHost], record[observability.FieldHTTPPath])
	}
	host, _ := record[observability.FieldHTTPHost].(string)
	path, _ := record[observability.FieldHTTPPath].(string)
	if strings.Contains(host, "super-secret-token") || strings.Contains(path, "super-secret-token") || strings.Contains(host, "@") {
		t.Fatalf("host/path leaked userinfo: host=%#v path=%#v", host, path)
	}
	request, _ := record[observability.FieldHTTPRequest].(string)
	if !strings.Contains(request, "ls-remote") || !strings.Contains(request, "https://git.example.com/app.git") {
		t.Fatalf("request = %#v", request)
	}
	if strings.Contains(request, "super-secret-token") || strings.Contains(request, "git:") {
		t.Fatalf("request leaked userinfo: %#v", request)
	}
	response, _ := record[observability.FieldHTTPResponse].(string)
	if !strings.Contains(response, "Authentication failed") || !strings.Contains(response, "[redacted]") {
		t.Fatalf("response = %#v", response)
	}
	if strings.Contains(response, "super-secret-token") || strings.Contains(response, "secret-key-material-must-not-leak") {
		t.Fatalf("response leaked secret: %#v", response)
	}
	dump := output.String()
	if strings.Contains(dump, "super-secret-token") || strings.Contains(dump, "secret-key-material-must-not-leak") {
		t.Fatalf("log leaked secret material: %s", dump)
	}
}

func TestListRefsNilLoggerSucceedsWithoutRecords(t *testing.T) {
	lister := NewLister(nil)
	lister.command = writeStubCommand(t, "#!/bin/sh\nprintf 'ok-stdout\n'\n")
	got, err := lister.ListRefs(context.Background(), "https://git.example.com/app.git", "https", domain.SecretGitCredential, []byte("deploy:token-value"))
	if err != nil {
		t.Fatalf("ListRefs() error = %v", err)
	}
	if got != "ok-stdout\n" {
		t.Fatalf("ListRefs() = %q", got)
	}
}

func testLogger(t *testing.T, output *bytes.Buffer) *slog.Logger {
	t.Helper()
	logger, err := observability.NewLogger(observability.LoggerOptions{
		Writer: output, Level: "debug", Format: "json", Service: "fixthe-test", Environment: "test",
	})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	return logger
}

func writeStubCommand(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "git-stub")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return path
}

func onlyJSONRecord(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	lines := bytes.Split(bytes.TrimSpace(raw), []byte("\n"))
	if len(lines) != 1 || len(lines[0]) == 0 {
		t.Fatalf("want one log record, got %d: %q", len(lines), raw)
	}
	var record map[string]any
	if err := json.Unmarshal(lines[0], &record); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; output = %q", err, raw)
	}
	return record
}
