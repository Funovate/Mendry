package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"mendry/backend/internal/modules/agentcore/adapter/file"
	"mendry/backend/internal/modules/agentcore/domain"
)

func buildLocalCommand(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	backendRoot := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	binary := filepath.Join(t.TempDir(), "agentcore-local")
	command := exec.Command("go", "build", "-o", binary, "./cmd/agentcore-local")
	command.Dir = backendRoot
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build agentcore-local: %v\n%s", err, output)
	}
	return binary
}

func writeCLIJSON(t *testing.T, path string, value any) {
	t.Helper()
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runCLI(t *testing.T, binary string, args ...string) (stdout, stderr []byte, err error) {
	t.Helper()
	command := exec.Command(binary, args...)
	var out, diagnostic bytes.Buffer
	command.Stdout = &out
	command.Stderr = &diagnostic
	err = command.Run()
	return out.Bytes(), diagnostic.Bytes(), err
}

func TestCLIRunInspectAndLegacyInvocation(t *testing.T) {
	binary := buildLocalCommand(t)
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	eventPath := filepath.Join(root, "event.json")
	stateDir := filepath.Join(root, "state")
	config := map[string]any{
		"schemaVersion": 1,
		"profile": map[string]any{
			"name": "report", "version": "v1", "completion": map[string]any{
				"mode": "solution_delivered", "version": "v1", "parameters": map[string]any{"artifactType": "report.summary"},
			}, "maxTokens": 32,
		},
		"provider": map[string]any{"mode": "fixture"},
		"budgets":  map[string]any{"maxSteps": 4},
	}
	event := map[string]any{"version": "v1", "source": "cli-test", "externalId": "event-1", "goal": "write a report", "payload": map[string]any{"key": "value"}}
	writeCLIJSON(t, configPath, config)
	writeCLIJSON(t, eventPath, event)

	stdout, stderr, err := runCLI(t, binary, "run", "--config", configPath, "--event", eventPath, "--state-dir", stateDir)
	if err != nil {
		t.Fatalf("CLI run: %v\nstderr=%s\nstdout=%s", err, stderr, stdout)
	}
	var result struct {
		Run domain.Run `json:"run"`
	}
	if err := json.Unmarshal(stdout, &result); err != nil {
		t.Fatalf("decode CLI result: %v\n%s", err, stdout)
	}
	if result.Run.State != domain.RunStateSucceeded || len(result.Run.ID) != 32 {
		t.Fatalf("CLI result run = %#v", result.Run)
	}

	inspected, stderr, err := runCLI(t, binary, "inspect", "--state-dir", stateDir, "--run-id", result.Run.ID)
	if err != nil {
		t.Fatalf("CLI inspect: %v\nstderr=%s", err, stderr)
	}
	var snapshot file.Snapshot
	if err := json.Unmarshal(inspected, &snapshot); err != nil {
		t.Fatalf("decode inspect: %v", err)
	}
	if snapshot.Identity.RunID != result.Run.ID || snapshot.Run.State != domain.RunStateSucceeded {
		t.Fatalf("inspect snapshot = %#v", snapshot)
	}

	legacyState := filepath.Join(root, "legacy-state")
	legacy, stderr, err := runCLI(t, binary, "--config", configPath, "--event", eventPath, "--state-dir", legacyState)
	if err != nil {
		t.Fatalf("legacy CLI run: %v\nstderr=%s\nstdout=%s", err, stderr, legacy)
	}
	var legacyResult struct {
		Run domain.Run `json:"run"`
	}
	if err := json.Unmarshal(legacy, &legacyResult); err != nil {
		t.Fatalf("decode legacy result: %v", err)
	}
	if legacyResult.Run.State != domain.RunStateSucceeded || legacyResult.Run.ID != result.Run.ID {
		t.Fatalf("legacy run = %#v", legacyResult.Run)
	}
}

func TestCLIResolveRejectsTrailingJSONAndInvalidOutcome(t *testing.T) {
	binary := buildLocalCommand(t)
	stateDir := t.TempDir()
	_, stderr, err := runCLI(t, binary, "resolve", "--state-dir", stateDir, "--run-id", strings.Repeat("a", 32), "--invocation-id", "inv-1", "--state", "unknown", "--output-json", `{"ok":true}`)
	if err == nil || !strings.Contains(string(stderr), "succeeded or failed") {
		t.Fatalf("invalid outcome err=%v stderr=%s", err, stderr)
	}
	_, stderr, err = runCLI(t, binary, "resolve", "--state-dir", stateDir, "--run-id", strings.Repeat("a", 32), "--invocation-id", "inv-1", "--output-json", `{"ok":true} {"extra":true}`)
	if err == nil || !strings.Contains(string(stderr), "trailing") {
		t.Fatalf("trailing outcome err=%v stderr=%s", err, stderr)
	}
}

func TestCLIResolveOperatorOutcomeThroughDurableSnapshot(t *testing.T) {
	binary := buildLocalCommand(t)
	root := t.TempDir()
	runDir := filepath.Join(root, "state", strings.Repeat("a", 32))
	identity := file.Identity{RunID: strings.Repeat("a", 32), EventID: strings.Repeat("b", 32), ConfigurationHash: strings.Repeat("c", 64), EventHash: strings.Repeat("d", 64)}
	run := domain.Run{ID: identity.RunID, EventID: identity.EventID, Goal: "manual recovery", ProfileName: "report", ProfileVersion: "v1", PolicyRef: "restrict", ConfigurationDigest: identity.ConfigurationHash, State: domain.RunStateWaiting, ReasonCode: "unknown_write_outcome", Version: 1, UpdatedAt: time.Unix(1, 0).UTC()}
	store, err := file.Create(context.Background(), runDir, identity, run)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessageGroup(context.Background(), run.ID, []domain.ModelMessage{{Role: "user", Content: "write"}, {Role: "assistant", ToolCalls: []domain.ToolCall{{ID: "call-1", Name: "workspace.write", Version: "v1", Arguments: map[string]any{"path": "file"}}}}}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	invocation := domain.Invocation{ID: "inv-1", RunID: run.ID, Sequence: 1, ToolName: "workspace.write", ToolVersion: "v1", Effect: domain.ToolEffectWrite, ArgumentsDigest: strings.Repeat("e", 64), Authorized: true, State: domain.InvocationPending, CreatedAt: time.Unix(2, 0).UTC()}
	if err := store.RecordInvocationIntent(context.Background(), invocation); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := runCLI(t, binary, "resolve", "--state-dir", filepath.Join(root, "state"), "--run-id", run.ID, "--invocation-id", invocation.ID, "--state", "succeeded", "--code", "operator_confirmed", "--output-json", `{"status":"confirmed"}`, "--completed-at", "2026-09-10T00:00:00Z")
	if err != nil {
		t.Fatalf("CLI resolve: %v\nstderr=%s\nstdout=%s", err, stderr, stdout)
	}
	var snapshot file.Snapshot
	if err := json.Unmarshal(stdout, &snapshot); err != nil {
		t.Fatalf("decode resolution: %v", err)
	}
	if snapshot.Run.State != domain.RunStateRunning || snapshot.Invocations[0].State != domain.InvocationSucceeded || len(snapshot.Results) != 1 || len(snapshot.MessageGroups) != 2 {
		t.Fatalf("resolved snapshot = %#v", snapshot)
	}
}
