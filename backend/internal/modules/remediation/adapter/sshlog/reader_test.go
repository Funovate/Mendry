package sshlog_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	projectdomain "fixthe/backend/internal/modules/projects/domain"
	"fixthe/backend/internal/modules/remediation/adapter/sshlog"
	"fixthe/backend/internal/modules/remediation/domain"
	"fixthe/backend/internal/platform/observability"
)

const (
	testProjectID = "019ff544-405c-7d21-9f10-cb3fc579605c"
	testSourceID  = "019ff544-405c-7d23-9f10-cb3fc579605c"
	testSecretID  = "019ff544-405c-7d24-9f10-cb3fc579605c"
)

type staticSource struct {
	cfg sshlog.SourceConfig
}

func TestReaderLogsMetadataWithoutSSHCredential(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "app.log")
	if err := os.WriteFile(fixture, []byte("INFO ready\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	logger, err := observability.NewLogger(observability.LoggerOptions{
		Writer: &output, Level: "debug", Format: "json", Service: "test", Environment: "test",
	})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	reader, err := sshlog.NewReader(sshlog.Options{
		Sources: staticSource{cfg: sshlog.SourceConfig{
			ProjectID: testProjectID, SourceID: testSourceID, Host: "logs.example.invalid",
			Port: 22, User: "app", LogPath: "/var/log/app.log", Mode: "tail", CredentialSecretID: testSecretID,
		}},
		Secrets: staticSecrets{}, Cipher: staticCipher{}, SSHCommand: writeFakeSSH(t, fixture), Logger: logger,
	})
	if err != nil {
		t.Fatalf("NewReader() error = %v", err)
	}
	if _, err := reader.Search(context.Background(), domain.EvidenceScope{
		ProjectID: testProjectID, SourceID: testSourceID,
	}, domain.LogQuery{}); err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("decode log: %v; output=%s", err, output.String())
	}
	if record[observability.FieldEvent] != observability.EventSSHEvidenceCompleted ||
		record[observability.FieldCredentialSecretID] != testSecretID ||
		record[observability.FieldCredentialKind] != string(projectdomain.SecretSSHPrivateKey) {
		t.Fatalf("SSH metadata record = %#v", record)
	}
	if record[observability.FieldSSHCommand] != "tail -n 500 -- '/var/log/app.log'" {
		t.Fatalf("SSH command = %#v", record[observability.FieldSSHCommand])
	}
	for _, forbidden := range []string{"PRIVATE KEY", "test\n", "-i"} {
		if strings.Contains(output.String(), forbidden) {
			t.Fatalf("SSH log leaked %q: %s", forbidden, output.String())
		}
	}
}

func (s staticSource) LoadSSHSource(context.Context, string, string) (sshlog.SourceConfig, error) {
	return s.cfg, nil
}

type staticSecrets struct{}

func (staticSecrets) GetEncryptedSecret(context.Context, string, string) (projectdomain.EncryptedSecret, error) {
	return projectdomain.EncryptedSecret{
		Secret: projectdomain.Secret{ID: testSecretID, ProjectID: testProjectID, Kind: projectdomain.SecretSSHPrivateKey},
	}, nil
}

type staticCipher struct{}

func (staticCipher) Encrypt(string, string, projectdomain.SecretKind, []byte) ([]byte, []byte, int32, error) {
	return nil, nil, 0, errors.New("unused")
}
func (staticCipher) Decrypt(string, string, projectdomain.SecretKind, []byte, []byte) ([]byte, error) {
	return []byte("-----BEGIN OPENSSH PRIVATE KEY-----\ntest\n-----END OPENSSH PRIVATE KEY-----\n"), nil
}
func (staticCipher) EncryptWebhookToken(string, string, []byte) ([]byte, []byte, error) {
	return nil, nil, errors.New("unused")
}
func (staticCipher) DecryptWebhookToken(string, string, []byte, []byte) ([]byte, error) {
	return nil, errors.New("unused")
}

func writeFakeSSH(t *testing.T, fixture string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-ssh")
	if runtime.GOOS == "windows" {
		t.Skip("fake ssh script uses a POSIX shell")
	}
	script := "#!/bin/sh\ncat \"$FIXTURE_LOG\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FIXTURE_LOG", fixture)
	return path
}

func TestReaderSearchBoundsAndRedaction(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "app.log")
	content := strings.Join([]string{
		"INFO boot",
		"ERROR password=hunter2 Authorization: Bearer sk-abc123456789",
		"WARN token=abcd",
		"INFO still going",
		"DEBUG extra",
	}, "\n") + "\n"
	if err := os.WriteFile(fixture, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	reader, err := sshlog.NewReader(sshlog.Options{
		Sources: staticSource{cfg: sshlog.SourceConfig{
			ProjectID: testProjectID, SourceID: testSourceID, Host: "logs.example.invalid",
			Port: 22, User: "app", LogPath: "/var/log/app.log", Mode: "tail",
			CredentialSecretID: testSecretID,
		}},
		Secrets:    staticSecrets{},
		Cipher:     staticCipher{},
		SSHCommand: writeFakeSSH(t, fixture),
	})
	if err != nil {
		t.Fatalf("NewReader() error = %v", err)
	}
	page, err := reader.Search(context.Background(), domain.EvidenceScope{
		ProjectID: testProjectID, SourceID: testSourceID,
	}, domain.LogQuery{MaxLines: 2, MaxBytes: 1024})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if !page.Truncated || len(page.Lines) != 2 {
		t.Fatalf("Search page = %#v", page)
	}
	joined := page.Lines[0].Message + page.Lines[1].Message
	if strings.Contains(joined, "hunter2") || strings.Contains(joined, "sk-abc") || strings.Contains(joined, "Bearer sk-") {
		t.Fatalf("unredacted log: %#v", page.Lines)
	}

	full, err := reader.Search(context.Background(), domain.EvidenceScope{
		ProjectID: testProjectID, SourceID: testSourceID,
	}, domain.LogQuery{MaxLines: 50, MaxBytes: 4096})
	if err != nil || len(full.Lines) < 3 {
		t.Fatalf("full Search = %#v err=%v", full, err)
	}
	anchor := full.Lines[1].EvidenceID
	window, err := reader.GetContext(context.Background(), domain.EvidenceScope{
		ProjectID: testProjectID, SourceID: testSourceID,
	}, domain.EvidenceAnchor{EvidenceID: anchor, LinesBefore: 1, LinesAfter: 1})
	if err != nil || len(window.Lines) != 3 || window.Lines[1].EvidenceID != anchor {
		t.Fatalf("GetContext = %#v err=%v", window, err)
	}
	unknown, err := reader.GetContext(context.Background(), domain.EvidenceScope{
		ProjectID: testProjectID, SourceID: testSourceID,
	}, domain.EvidenceAnchor{EvidenceID: "ev-missing"})
	if err != nil || len(unknown.Lines) != 0 {
		t.Fatalf("unknown GetContext = %#v err=%v", unknown, err)
	}
}

func TestParseSSHSourceConfigAcceptsProjectFolder(t *testing.T) {
	secretID := testSecretID
	cfg, err := sshlog.ParseSSHSourceConfig(testProjectID, testSourceID, &secretID, []byte(`{
		"schemaVersion": 1,
		"host": "logs.example.invalid",
		"port": 22,
		"user": "app",
		"projectFolder": "/srv/app",
		"logPath": "/var/log/app.log",
		"mode": "tail"
	}`))
	if err != nil {
		t.Fatalf("ParseSSHSourceConfig() error = %v", err)
	}
	if cfg.Host != "logs.example.invalid" || cfg.LogPath != "/var/log/app.log" || cfg.CredentialSecretID != secretID {
		t.Fatalf("config = %#v", cfg)
	}
}

func TestReaderPreservesRemoteFailureDiagnostics(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake ssh script uses a POSIX shell")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-ssh")
	script := "#!/bin/sh\necho 'Permission denied (publickey) sk-abc123456789' >&2\nexit 1\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	reader, err := sshlog.NewReader(sshlog.Options{
		Sources: staticSource{cfg: sshlog.SourceConfig{
			ProjectID: testProjectID, SourceID: testSourceID, Host: "logs.example.invalid",
			Port: 22, User: "app", LogPath: "/var/log/app.log", Mode: "tail",
			CredentialSecretID: testSecretID,
		}},
		Secrets:    staticSecrets{},
		Cipher:     staticCipher{},
		SSHCommand: path,
	})
	if err != nil {
		t.Fatalf("NewReader() error = %v", err)
	}
	var output bytes.Buffer
	logger, err := observability.NewLogger(observability.LoggerOptions{
		Writer: &output, Level: "debug", Format: "json", Service: "test", Environment: "test",
	})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	reader, err = sshlog.NewReader(sshlog.Options{
		Sources: staticSource{cfg: sshlog.SourceConfig{
			ProjectID: testProjectID, SourceID: testSourceID, Host: "logs.example.invalid",
			Port: 22, User: "app", LogPath: "/var/log/app.log", Mode: "tail",
			CredentialSecretID: testSecretID,
		}},
		Secrets: staticSecrets{}, Cipher: staticCipher{}, SSHCommand: path, Logger: logger,
	})
	if err != nil {
		t.Fatalf("NewReader() with logger error = %v", err)
	}
	_, err = reader.Search(context.Background(), domain.EvidenceScope{
		ProjectID: testProjectID, SourceID: testSourceID,
	}, domain.LogQuery{MaxLines: 2})
	if err == nil {
		t.Fatal("expected remote failure")
	}
	command := "tail -n 2 -- '/var/log/app.log'"
	for _, expected := range []string{"Permission denied (publickey)", "sk-abc123456789", "command=" + command} {
		if !strings.Contains(err.Error(), expected) {
			t.Fatalf("error %q does not contain %q", err, expected)
		}
	}
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("decode SSH failure log: %v; output=%s", err, output.String())
	}
	if record[observability.FieldErrorClass] != observability.OutboundCommand {
		t.Fatalf("error class = %#v, want %q", record[observability.FieldErrorClass], observability.OutboundCommand)
	}
	if record[observability.FieldSSHCommand] != command {
		t.Fatalf("SSH command = %#v, want %q", record[observability.FieldSSHCommand], command)
	}
	if !strings.Contains(record[observability.FieldErrorMessage].(string), "Permission denied (publickey)") {
		t.Fatalf("error message = %#v", record[observability.FieldErrorMessage])
	}
}
