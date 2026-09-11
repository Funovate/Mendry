package file

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mendry/backend/internal/modules/agentcore/domain"
)

func testIdentity() Identity {
	return Identity{
		RunID:             "run-1",
		EventID:           "event-1",
		ConfigurationHash: strings.Repeat("a", 64),
		EventHash:         strings.Repeat("b", 64),
	}
}

func testRun(identity Identity) domain.Run {
	now := time.Unix(100, 0).UTC()
	return domain.Run{
		ID: identity.RunID, EventID: identity.EventID, Goal: "produce a report",
		Input:   map[string]any{"nested": map[string]any{"value": "one"}},
		Context: map[string]string{"source": "test"}, ProfileName: "report", ProfileVersion: "v1",
		PolicyRef: "restrict", ConfigurationDigest: identity.ConfigurationHash,
		State: domain.RunStateQueued, Budget: domain.Budget{Limits: domain.BudgetLimits{
			MaxElapsed:     time.Minute,
			MaxModelCalls:  4,
			MaxToolCalls:   4,
			MaxOutputBytes: 1 << 20,
		}}, StartedAt: time.Time{}, UpdatedAt: now, Version: 1,
	}
}

func createTestStore(t *testing.T) (*Store, string, Identity, domain.Run) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "run")
	identity := testIdentity()
	run := testRun(identity)
	store, err := Create(context.Background(), dir, identity, run)
	if err != nil {
		t.Fatal(err)
	}
	return store, dir, identity, run
}

func TestStoreRestartPreservesStateAndCopiesValues(t *testing.T) {
	store, dir, identity, run := createTestStore(t)
	defer store.Close()

	if err := store.SaveRun(context.Background(), func() domain.Run {
		updated := run
		updated.State = domain.RunStateRunning
		updated.StartedAt = time.Unix(200, 0).UTC()
		updated.UpdatedAt = time.Unix(201, 0).UTC()
		updated.Version = 2
		return updated
	}()); err != nil {
		t.Fatal(err)
	}
	invocation := domain.Invocation{
		ID: "inv-1", RunID: identity.RunID, Sequence: 1, ToolName: "workspace.read",
		ToolVersion: "v1", Effect: domain.ToolEffectRead, ArgumentsDigest: strings.Repeat("c", 64),
		IdempotencyKey: "idem-1", Authorized: true, State: domain.InvocationPending, CreatedAt: time.Unix(202, 0).UTC(),
	}
	if err := store.RecordInvocationIntent(context.Background(), invocation); err != nil {
		t.Fatal(err)
	}
	artifact := domain.Artifact{ID: "artifact-1", RunID: identity.RunID, InvocationID: invocation.ID,
		Type: "observation.file", SchemaVersion: "v1", Data: map[string]any{"value": "observed"},
		Provenance: domain.ProvenanceObserved, CreatedAt: time.Unix(203, 0).UTC()}
	result := domain.InvocationResult{InvocationID: invocation.ID, Sequence: 1, State: domain.InvocationSucceeded,
		Code: "ok", Output: map[string]any{"status": "ok"}, OutputBytes: 15, CompletedAt: time.Unix(204, 0).UTC()}
	if err := store.RecordInvocationResult(context.Background(), result, []domain.Artifact{artifact}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessageGroup(context.Background(), identity.RunID, []domain.ModelMessage{
		{Role: "user", Content: "read"},
		{Role: "assistant", ToolCalls: []domain.ToolCall{{ID: "call-1", Name: "workspace.read", Version: "v1", Arguments: map[string]any{}}}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessageGroup(context.Background(), identity.RunID, []domain.ModelMessage{{Role: "tool", ToolCallID: "call-1", Content: "ok"}}); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.LoadRun(context.Background(), identity.RunID)
	if err != nil {
		t.Fatal(err)
	}
	loaded.Input["nested"].(map[string]any)["value"] = "mutated"
	loaded.Context["source"] = "mutated"
	if loadedInput := loaded.Input["nested"].(map[string]any)["value"]; loadedInput != "mutated" {
		t.Fatalf("unexpected loaded input: %#v", loadedInput)
	}
	fresh, err := store.LoadRun(context.Background(), identity.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Input["nested"].(map[string]any)["value"] != "one" || fresh.Context["source"] != "test" {
		t.Fatalf("store returned aliased run values: %#v", fresh)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := Open(context.Background(), dir, identity)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	loaded, err = restarted.LoadRun(context.Background(), identity.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != domain.RunStateRunning || loaded.Version != 2 || loaded.StartedAt != time.Unix(200, 0).UTC() {
		t.Fatalf("restart changed run lifecycle: %#v", loaded)
	}
	invocations, err := restarted.ListInvocations(context.Background(), identity.RunID)
	if err != nil || len(invocations) != 1 || invocations[0].State != domain.InvocationSucceeded {
		t.Fatalf("restart invocations = %#v, err=%v", invocations, err)
	}
	artifacts, err := restarted.ListArtifacts(context.Background(), identity.RunID)
	if err != nil || len(artifacts) != 1 || artifacts[0].InvocationID != "inv-1" {
		t.Fatalf("restart artifacts = %#v, err=%v", artifacts, err)
	}
	messages, err := restarted.LoadMessages(context.Background(), identity.RunID)
	if err != nil || len(messages) != 3 || messages[2].ToolCallID != "call-1" {
		t.Fatalf("restart messages = %#v, err=%v", messages, err)
	}
}

func TestStoreRejectsLockContentionAndIdentityDrift(t *testing.T) {
	store, dir, identity, _ := createTestStore(t)
	defer store.Close()
	if _, err := Open(context.Background(), dir, identity); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("second open error = %v, want lock error", err)
	}
	wrong := identity
	wrong.EventHash = strings.Repeat("d", 64)
	if _, err := Open(context.Background(), dir, wrong); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("locked wrong identity error = %v, want lock error", err)
	}
}

func TestStoreRejectsCorruptAndUnknownState(t *testing.T) {
	store, dir, identity, _ := createTestStore(t)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(dir, StateFileName)
	if err := os.WriteFile(statePath, []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), dir, identity); err == nil {
		t.Fatal("corrupt state was accepted")
	}
	encoded, err := json.Marshal(map[string]any{"schemaVersion": SchemaVersion, "revision": 1, "identity": identity})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(context.Background(), dir); err == nil {
		t.Fatal("incomplete state was accepted")
	}
}

func TestStoreReconcilesTempWithoutReplayingIt(t *testing.T) {
	store, dir, identity, run := createTestStore(t)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(dir, StateFileName)
	original, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	candidate := run
	candidate.State = domain.RunStateSucceeded
	candidate.Version = 2
	candidate.Result = map[string]any{"candidate": true}
	candidateJSON, err := json.Marshal(Snapshot{SchemaVersion: SchemaVersion, Revision: 2, Identity: identity, Run: candidate})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, tempFileName), candidateJSON, 0o600); err != nil {
		t.Fatal(err)
	}

	restarted, err := Open(context.Background(), dir, identity)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	loaded, err := restarted.LoadRun(context.Background(), identity.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != run.State || loaded.Version != run.Version || loaded.Result != nil {
		t.Fatalf("temporary candidate was replayed: %#v", loaded)
	}
	if _, err := os.Stat(filepath.Join(dir, tempFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary state was not removed: %v", err)
	}
	current, err := os.ReadFile(statePath)
	if err != nil || string(current) != string(original) {
		t.Fatalf("state changed during temp reconciliation: err=%v", err)
	}
}

func TestStoreRejectsImmutableRunChangesAndInvalidResultPairs(t *testing.T) {
	store, _, identity, run := createTestStore(t)
	defer store.Close()
	changed := run
	changed.Version = 2
	changed.Input = map[string]any{"different": true}
	if err := store.SaveRun(context.Background(), changed); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("immutable change error = %v", err)
	}
	invocation := domain.Invocation{ID: "inv-1", RunID: identity.RunID, Sequence: 1, ToolName: "workspace.read", ToolVersion: "v1", Effect: domain.ToolEffectRead, ArgumentsDigest: strings.Repeat("c", 64), State: domain.InvocationPending, CreatedAt: time.Unix(1, 0)}
	if err := store.RecordInvocationIntent(context.Background(), invocation); err != nil {
		t.Fatal(err)
	}
	bad := domain.InvocationResult{InvocationID: invocation.ID, Sequence: 2, State: domain.InvocationSucceeded}
	if err := store.RecordInvocationResult(context.Background(), bad, nil); err == nil {
		t.Fatal("mismatched invocation result was accepted")
	}
	if err := store.RecordInvocationResult(context.Background(), domain.InvocationResult{InvocationID: invocation.ID, Sequence: 1, State: domain.InvocationPending}, nil); err == nil {
		t.Fatal("pending invocation result was accepted")
	}
}

func TestStoreRejectsOrphanOrInterleavedMessageGroups(t *testing.T) {
	store, _, identity, _ := createTestStore(t)
	defer store.Close()
	if err := store.AppendMessageGroup(context.Background(), identity.RunID, []domain.ModelMessage{{Role: "tool", ToolCallID: "missing", Content: "orphan"}}); err == nil {
		t.Fatal("orphan tool result was accepted")
	}
	group := []domain.ModelMessage{{Role: "user", Content: "write"}, {Role: "assistant", ToolCalls: []domain.ToolCall{{ID: "call-1", Name: "workspace.write", Version: "v1", Arguments: map[string]any{}}}}}
	if err := store.AppendMessageGroup(context.Background(), identity.RunID, group); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessageGroup(context.Background(), identity.RunID, []domain.ModelMessage{{Role: "user", Content: "interleaved"}, {Role: "assistant", Content: "no"}}); err == nil {
		t.Fatal("interleaved model group was accepted")
	}
	if err := store.AppendMessageGroup(context.Background(), identity.RunID, []domain.ModelMessage{{Role: "tool", ToolCallID: "call-1", Content: "ok"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessageGroup(context.Background(), identity.RunID, []domain.ModelMessage{{Role: "tool", ToolCallID: "call-1", Content: "duplicate"}}); err == nil {
		t.Fatal("duplicate tool result was accepted")
	}
}

func TestStoreRequiresArtifactReferenceOrDataAndKnownInvocation(t *testing.T) {
	store, _, identity, _ := createTestStore(t)
	defer store.Close()
	missing := domain.Artifact{ID: "artifact-1", RunID: identity.RunID, Type: "observation.test", SchemaVersion: "v1", Provenance: domain.ProvenanceObserved}
	if err := store.AppendArtifacts(context.Background(), identity.RunID, []domain.Artifact{missing}); err == nil {
		t.Fatal("empty artifact was accepted")
	}
	unknown := missing
	unknown.Data = map[string]any{"ok": true}
	unknown.InvocationID = "missing"
	if err := store.AppendArtifacts(context.Background(), identity.RunID, []domain.Artifact{unknown}); err == nil {
		t.Fatal("artifact for unknown invocation was accepted")
	}
}

func TestStoreResolveInvocationRecordsOperatorOutcomeWithoutExecuting(t *testing.T) {
	store, _, identity, run := createTestStore(t)
	defer store.Close()
	run.State = domain.RunStateWaiting
	run.ReasonCode = "unknown_write_outcome"
	run.Version = 2
	if err := store.SaveRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessageGroup(context.Background(), identity.RunID, []domain.ModelMessage{
		{Role: "user", Content: "write"},
		{Role: "assistant", ToolCalls: []domain.ToolCall{{ID: "call-write", Name: "workspace.write", Version: "v1", Arguments: map[string]any{"path": "x"}}}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordInvocationIntent(context.Background(), domain.Invocation{
		ID: "inv-write", RunID: identity.RunID, Sequence: 1, ToolName: "workspace.write", ToolVersion: "v1", Effect: domain.ToolEffectWrite,
		ArgumentsDigest: strings.Repeat("e", 64), Authorized: true, State: domain.InvocationPending, CreatedAt: time.Unix(5, 0),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.ResolveInvocation(context.Background(), "inv-write", domain.InvocationSucceeded, "operator_confirmed", map[string]any{"status": "confirmed"}, time.Unix(6, 0)); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadRun(context.Background(), identity.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != domain.RunStateRunning || loaded.ReasonCode != "operator_resolved" {
		t.Fatalf("resolved run lifecycle = %#v", loaded)
	}
	invocations, _ := store.ListInvocations(context.Background(), identity.RunID)
	if len(invocations) != 1 || invocations[0].State != domain.InvocationSucceeded {
		t.Fatalf("resolved invocation = %#v", invocations)
	}
	messages, _ := store.LoadMessages(context.Background(), identity.RunID)
	if len(messages) != 3 || messages[2].ToolCallID != "call-write" || !strings.Contains(messages[2].Content, "operator_confirmed") {
		t.Fatalf("resolved history = %#v", messages)
	}
	if err := store.ResolveInvocation(context.Background(), "inv-write", domain.InvocationSucceeded, "again", nil, time.Now()); err == nil {
		t.Fatal("resolved invocation was accepted twice")
	}
}
