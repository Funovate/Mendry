package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mendry/backend/internal/modules/remediation/domain"
)

type mountStub struct {
	path  string
	calls int
}

func (m *mountStub) ResolveWorkspaceMount(_ context.Context, runID, workspaceID, treeHash string) (string, error) {
	m.calls++
	if runID != "run-1" || workspaceID != "workspace-1" || treeHash != strings.Repeat("a", 40) {
		return "", os.ErrInvalid
	}
	return m.path, nil
}

type artifactStub struct {
	values map[string][]byte
}

func (a artifactStub) Put(_ context.Context, content []byte) (string, string, error) {
	digest := sha256.Sum256(content)
	hash := hex.EncodeToString(digest[:])
	a.values["sha256:"+hash] = append([]byte(nil), content...)
	return "sha256:" + hash, hash, nil
}

func TestDockerRunnerUsesRestrictedReadOnlyContainerAndRedactsOutput(t *testing.T) {
	root := t.TempDir()
	mount := filepath.Join(root, "repo")
	if err := os.Mkdir(mount, 0o700); err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(root, "args.txt")
	fakeDocker := filepath.Join(root, "docker")
	script := "#!/bin/sh\nif [ \"$1\" = run ]; then\n  printf '%s\\n' \"$@\" > \"$MENDRY_DOCKER_ARGS_FILE\"\n  printf 'password=secret-output-value\\n'\n  printf 'checks complete\\n' >&2\nfi\nexit 0\n"
	if err := os.WriteFile(fakeDocker, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MENDRY_DOCKER_ARGS_FILE", argsFile)
	artifacts := artifactStub{values: map[string][]byte{}}
	mounts := &mountStub{path: mount}
	runner, err := NewDockerRunner(DockerOptions{Command: fakeDocker, Workspaces: mounts, Artifacts: artifacts})
	if err != nil {
		t.Fatal(err)
	}
	request := validValidationRequest()
	result, err := runner.Run(context.Background(), request)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.Passed || result.ExitCode != 0 || result.TreeHash != request.ExpectedTreeHash || mounts.calls != 2 {
		t.Fatalf("validation result = %+v, mount calls=%d", result, mounts.calls)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"--network=none", "--read-only", "--cap-drop=ALL", "--cpus=2", "--memory=4096m", "--memory-swap=4096m", "--pids-limit=128", "--volume", mount + ":/workspace:ro", "go", "test", "./..."} {
		if !strings.Contains(string(args), required) {
			t.Fatalf("Docker invocation missing %q: %s", required, args)
		}
	}
	for _, forbidden := range []string{"--network=host", "--privileged", "secret-output-value"} {
		if strings.Contains(string(args), forbidden) {
			t.Fatalf("unexpected value in Docker invocation %q", forbidden)
		}
	}
	stored := artifacts.values[result.OutputArtifactRef]
	if strings.Contains(string(stored), "secret-output-value") || !strings.Contains(strings.ToLower(string(stored)), "[redacted]") {
		t.Fatalf("validation artifact was not redacted: %q", stored)
	}
}

func TestDockerRunnerTimesOutAndRemovesContainer(t *testing.T) {
	root := t.TempDir()
	mount := filepath.Join(root, "repo")
	if err := os.Mkdir(mount, 0o700); err != nil {
		t.Fatal(err)
	}
	callsFile := filepath.Join(root, "calls.txt")
	fakeDocker := filepath.Join(root, "docker")
	script := "#!/bin/sh\nprintf '%s\\n' \"$1\" >> \"$MENDRY_DOCKER_CALLS_FILE\"\nif [ \"$1\" = run ]; then exec sleep 3; fi\nexit 0\n"
	if err := os.WriteFile(fakeDocker, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MENDRY_DOCKER_CALLS_FILE", callsFile)
	runner, err := NewDockerRunner(DockerOptions{
		Command: fakeDocker, Workspaces: &mountStub{path: mount}, Artifacts: artifactStub{values: map[string][]byte{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := validValidationRequest()
	request.Profile.RequiredCommands[0].TimeoutSeconds = 1
	result, err := runner.Run(context.Background(), request)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Passed || result.ExitCode != -1 || result.Summary != "validation command timed out" {
		t.Fatalf("timeout result = %+v", result)
	}
	calls, err := os.ReadFile(callsFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(calls), "rm\n") < 2 || !strings.Contains(string(calls), "run\n") {
		t.Fatalf("container cleanup calls = %q", calls)
	}
}

func TestDockerRunnerRecordsNonzeroCommandExit(t *testing.T) {
	root := t.TempDir()
	mount := filepath.Join(root, "repo")
	if err := os.Mkdir(mount, 0o700); err != nil {
		t.Fatal(err)
	}
	fakeDocker := filepath.Join(root, "docker")
	script := "#!/bin/sh\nif [ \"$1\" = run ]; then exit 7; fi\nexit 0\n"
	if err := os.WriteFile(fakeDocker, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	runner, err := NewDockerRunner(DockerOptions{
		Command: fakeDocker, Workspaces: &mountStub{path: mount}, Artifacts: artifactStub{values: map[string][]byte{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(context.Background(), validValidationRequest())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Passed || result.ExitCode != 7 {
		t.Fatalf("nonzero exit result = %+v", result)
	}
}

func validValidationRequest() domain.ValidationRequest {
	return domain.ValidationRequest{
		RunID: "run-1", WorkspaceID: "workspace-1", CommandID: "unit", CommandVersion: 3,
		ExpectedTreeHash: strings.Repeat("a", 40), IdempotencyKey: "validation:run-1:unit:3",
		Profile: domain.ExecutionProfileSnapshot{
			ImageDigest: "sha256:" + strings.Repeat("b", 64), WorkingDirectory: "backend",
			RequiredCommands: []domain.ValidationCommandSnapshot{{ID: "unit", Version: 3, Argv: []string{"go", "test", "./..."}, TimeoutSeconds: 10}},
			CPULimit:         2, MemoryLimitMiB: 4096, WorkspaceLimitMiB: 1024,
		},
	}
}
