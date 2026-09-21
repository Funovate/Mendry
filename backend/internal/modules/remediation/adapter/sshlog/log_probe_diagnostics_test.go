package sshlog

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mendry/backend/internal/modules/projects/application"
)

func TestProbeBootstrapPathFailuresBeforeWrites(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 required")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "app.log")
	if err := os.WriteFile(file, []byte("log"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, path, reason string }{
		{"missing", filepath.Join(dir, "absent"), "log_path_not_found"},
		{"directory", dir, "log_path_not_file"},
		{"unreadable", file, "log_path_not_readable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config, _ := json.Marshal(map[string]string{"logPath": tc.path})
			bootstrap := renderProbeBootstrap(config, nil, nil, "root", "/usr/bin/python3")
			// For the permissions case emulate a denied open under the service account.
			// Any attempted install mutation raises instead of touching the host.
			harness := `import pathlib, subprocess, pwd, types
def run(*args, **kwargs):
    if "capture_output" in kwargs:
        raise TypeError("capture_output requires Python 3.7")
    return types.SimpleNamespace(returncode=1, stderr=b"PermissionError: denied")
pathlib.Path.mkdir = lambda *a, **k: (_ for _ in ()).throw(RuntimeError("unexpected installation write"))
pwd.getpwnam = lambda user: types.SimpleNamespace(pw_uid=0, pw_gid=0)
subprocess.run = run
`
			out, err := exec.Command(python, "-c", harness+bootstrap).CombinedOutput()
			if err == nil {
				t.Fatal("bootstrap unexpectedly succeeded")
			}
			parsed := probeCommandError(context.Background(), err, string(out))
			var failure *application.LogProbePathError
			if !errors.As(parsed, &failure) || failure.Reason != tc.reason || failure.Path != tc.path {
				t.Fatalf("unexpected failure: %v; output=%s", parsed, out)
			}
		})
	}
}

func TestManagedLogProbeScriptsAvoidPostPython36APIs(t *testing.T) {
	config, err := json.Marshal(map[string]string{"logPath": "/var/log/app.log"})
	if err != nil {
		t.Fatal(err)
	}
	combined := renderProbeBootstrap(config, []byte(logProbePython), nil, "root", "/usr/bin/python3") + logProbePython
	for _, incompatible := range []string{"capture_output=", "missing_ok="} {
		if strings.Contains(combined, incompatible) {
			t.Fatalf("managed log probe requires an API newer than Python 3.6: %s", incompatible)
		}
	}
}

func TestProbeCommandErrorSeparatesWarningAndDiagnostic(t *testing.T) {
	for _, reason := range []string{"log_path_not_found", "log_path_not_file", "log_path_not_readable", "unrecognized"} {
		t.Run(reason, func(t *testing.T) {
			payload, _ := json.Marshal(application.LogProbePathError{Reason: reason, Path: "/var/log/app.log", User: "collector"})
			stderr := "WARNING: connection is not using a post-quantum key exchange algorithm.\r\nMENDRY_LOG_PROBE_ERROR=" + string(payload) + "\n"
			err := probeCommandError(context.Background(), errors.New("exit status 1"), stderr)
			var failure *application.LogProbePathError
			if errors.As(err, &failure) != (reason != "unrecognized") {
				t.Fatalf("unexpected classification: %v", err)
			}
			if !strings.Contains(err.Error(), "exit status 1") || !strings.Contains(err.Error(), "WARNING") {
				t.Fatalf("lost diagnostics: %v", err)
			}
			if failure != nil && strings.Contains(failure.Message(), "WARNING") {
				t.Fatal("raw stderr leaked to public message")
			}
		})
	}
}
