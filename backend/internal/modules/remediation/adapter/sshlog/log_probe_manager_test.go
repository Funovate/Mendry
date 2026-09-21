package sshlog_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	projectapplication "mendry/backend/internal/modules/projects/application"
	projectdomain "mendry/backend/internal/modules/projects/domain"
	"mendry/backend/internal/modules/remediation/adapter/sshlog"
)

func TestLogProbeManagerInstallsWithFixedSSHCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake ssh script uses a POSIX shell")
	}
	dir := t.TempDir()
	argsPath, stdinPath := filepath.Join(dir, "args"), filepath.Join(dir, "stdin")
	commandPath := filepath.Join(dir, "fake-ssh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$ARGS_PATH\"\ncat >> \"$STDIN_PATH\"\ncase \"$*\" in\n  *'sudo -n sh -s'*) printf 'MENDRY_PROBE_PYTHON=/opt/python/bin/python3\\n' ;;\n  *'log-probe.service'*) printf 'active\\n{\"version\":\"1\",\"configVersion\":7,\"checkedAt\":\"%s\",\"message\":\"monitoring\"}\\n' \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\" ;;\nesac\n"
	if err := os.WriteFile(commandPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ARGS_PATH", argsPath)
	t.Setenv("STDIN_PATH", stdinPath)
	manager, err := sshlog.NewLogProbeManager(sshlog.LogProbeManagerOptions{
		Secrets: staticSecrets{}, Cipher: staticCipher{}, SSHCommand: commandPath,
	})
	if err != nil {
		t.Fatalf("NewLogProbeManager() error = %v", err)
	}
	request := validLogProbeRequest()
	status, err := manager.Install(context.Background(), request)
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if status.State != "starting" || status.ConfigVersion != 7 || status.Message != "" {
		t.Fatalf("Install() status = %#v", status)
	}
	arguments, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(arguments), "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ") || !strings.Contains(string(arguments), "sudo -n /opt/python/bin/python3 -") || !strings.Contains(string(arguments), "sudo -n sh -s") {
		t.Fatalf("SSH arguments leaked token or omitted fixed installer command: %s", arguments)
	}
	bootstrap, err := os.ReadFile(stdinPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bootstrap), `"/opt/python/bin/python3"`) || strings.Contains(string(bootstrap), "PRIVATE KEY") {
		t.Fatalf("installer input is invalid or leaked credential")
	}
	clearStatus := strings.Index(string(bootstrap), `pathlib.Path("/var/lib/mendry-log-probe/status.json").unlink()`)
	restart := strings.Index(string(bootstrap), `subprocess.run(["systemctl", "restart", "mendry-log-probe.service"], check=True)`)
	if clearStatus < 0 || restart < clearStatus {
		t.Fatal("installer must clear the previous status before restarting")
	}
}

func TestLogProbeManagerRejectsUnsafeRuntimeConfiguration(t *testing.T) {
	manager, err := sshlog.NewLogProbeManager(sshlog.LogProbeManagerOptions{Secrets: staticSecrets{}, Cipher: staticCipher{}})
	if err != nil {
		t.Fatal(err)
	}
	request := projectapplication.LogProbeRequest{
		ProjectID: testProjectID, CredentialSecretID: testSecretID, InboundURL: "http://untrusted.example/hooks/token", TriggerVersion: 1,
		Source: projectdomain.SSHSourceConfig{Host: "host", Port: 22, User: "root;bad", LogPath: "/var/log/app log", Mode: "tail", Deployment: projectdomain.SSHDeployment{Kind: projectdomain.SSHDeploymentHost}},
		Rules:  projectdomain.CustomRuleConfig{Rules: []projectdomain.CustomRule{{ID: "errors"}}},
	}
	if _, err := manager.Install(context.Background(), request); err == nil {
		t.Fatal("Install() accepted unsafe configuration")
	}
}

func validLogProbeRequest() projectapplication.LogProbeRequest {
	return projectapplication.LogProbeRequest{
		ProjectID: testProjectID, CredentialSecretID: testSecretID,
		Source: projectdomain.SSHSourceConfig{
			Host: "logs.example.invalid", Port: 22, User: "collector", LogPath: "/var/log/app.log", Mode: "tail",
			Deployment: projectdomain.SSHDeployment{Kind: projectdomain.SSHDeploymentHost},
		},
		InboundURL: "https://mendry.example/hooks/abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ", TriggerVersion: 7,
		Rules: projectdomain.CustomRuleConfig{SchemaVersion: 2, GroupingWindowSeconds: 300, Rules: []projectdomain.CustomRule{{
			ID: "errors", Name: "Errors", MatchType: "contains", Pattern: "ERROR", Threshold: 1, WindowSeconds: 60, CooldownSeconds: 300,
		}}},
	}
}

func TestLogProbeManagerReturnsBeforeStatusIsHealthy(t *testing.T) {
	commandPath := filepath.Join(t.TempDir(), "fake-ssh")
	script := "#!/bin/sh\ncat > /dev/null\ncase \"$*\" in\n  *'sudo -n sh -s'*) printf 'MENDRY_PROBE_PYTHON=/usr/bin/python3\\n' ;;\n  *'log-probe.service'*) exit 91 ;;\nesac\n"
	if err := os.WriteFile(commandPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	manager, err := sshlog.NewLogProbeManager(sshlog.LogProbeManagerOptions{Secrets: staticSecrets{}, Cipher: staticCipher{}, SSHCommand: commandPath})
	if err != nil {
		t.Fatal(err)
	}
	status, err := manager.Install(context.Background(), validLogProbeRequest())
	if err != nil || status.State != "starting" || status.CheckedAt.IsZero() {
		t.Fatalf("Install() = %#v, %v; want starting without a status request", status, err)
	}
}

func TestProbePreflightClassifiesUnsupportedHosts(t *testing.T) {
	for _, reason := range []string{"python_unavailable", "python_unsupported", "systemd_unavailable", "probe_user_unavailable"} {
		t.Run(reason, func(t *testing.T) {
			commandPath := filepath.Join(t.TempDir(), "fake-ssh")
			script := "#!/bin/sh\ncat > /dev/null\nprintf 'MENDRY_PROBE_PREFLIGHT=%s\\n' \"$FAIL_REASON\" >&2\nexit 1\n"
			if err := os.WriteFile(commandPath, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("FAIL_REASON", reason)
			manager, err := sshlog.NewLogProbeManager(sshlog.LogProbeManagerOptions{Secrets: staticSecrets{}, Cipher: staticCipher{}, SSHCommand: commandPath})
			if err != nil {
				t.Fatal(err)
			}
			_, err = manager.Install(context.Background(), validLogProbeRequest())
			var setup *projectapplication.LogProbeSetupError
			if !errors.As(err, &setup) || setup.Reason != reason {
				t.Fatalf("Install() error = %v, want %s", err, reason)
			}
		})
	}
}

func TestProbePreflightRejectsUnsafeInterpreterPath(t *testing.T) {
	commandPath := filepath.Join(t.TempDir(), "fake-ssh")
	script := "#!/bin/sh\ncat > /dev/null\nprintf 'MENDRY_PROBE_PYTHON=/tmp/python3;touch /tmp/unexpected\\n'\n"
	if err := os.WriteFile(commandPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	manager, err := sshlog.NewLogProbeManager(sshlog.LogProbeManagerOptions{Secrets: staticSecrets{}, Cipher: staticCipher{}, SSHCommand: commandPath})
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.Install(context.Background(), validLogProbeRequest())
	var setup *projectapplication.LogProbeSetupError
	if !errors.As(err, &setup) || setup.Reason != "python_unavailable" {
		t.Fatalf("Install() error = %v, want python_unavailable", err)
	}
}

// 远端 bootstrap 的退出原因是区分 SSH 不可达、sudo 被拒和配置错误的唯一证据，
// 必须穿过 adapter 边界而不是被折叠成一句通用失败。
func TestLogProbeManagerInstallKeepsRemoteFailureCause(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake ssh script uses a POSIX shell")
	}
	dir := t.TempDir()
	commandPath := filepath.Join(dir, "fake-ssh")
	script := "#!/bin/sh\ncat > /dev/null\ncase \"$*\" in\n  *'sudo -n sh -s'*) printf 'MENDRY_PROBE_PYTHON=/usr/bin/python3\\n';;\n  *) printf 'configured log file is not readable\\n' >&2; exit 1;;\nesac\n"
	if err := os.WriteFile(commandPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	manager, err := sshlog.NewLogProbeManager(sshlog.LogProbeManagerOptions{
		Secrets: staticSecrets{}, Cipher: staticCipher{}, SSHCommand: commandPath,
	})
	if err != nil {
		t.Fatalf("NewLogProbeManager() error = %v", err)
	}
	if _, err := manager.Install(context.Background(), validLogProbeRequest()); err == nil {
		t.Fatal("Install() accepted a failing remote bootstrap")
	} else if !strings.Contains(err.Error(), "configured log file is not readable") || !strings.Contains(err.Error(), "exit status 1") {
		t.Fatalf("Install() dropped the remote failure cause: %v", err)
	}
}
