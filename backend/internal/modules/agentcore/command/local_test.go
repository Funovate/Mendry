package command

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mendry/backend/internal/modules/agentcore/domain"
)

func testCompletion(mode domain.CompletionMode, parameters map[string]any) domain.CompletionContract {
	return domain.CompletionContract{Mode: mode, Version: "v1", Parameters: parameters}
}

func testFixtureConfig(workspaceBinary string, policy PolicyConfig) Config {
	return Config{
		SchemaVersion: 1,
		Profile: ProfileConfig{
			Name: "workspace", Version: "v1", SystemPrompt: "Use the configured workspace tools.",
			Completion:          testCompletion(domain.CompletionActionWithVerification, map[string]any{"actionType": "workspace.action", "verificationType": "workspace.verification"}),
			SummaryArtifactType: "report.summary", MaxTokens: 128,
		},
		Provider: ProviderConfig{Mode: "fixture", Binding: "fixture"},
		Policy:   policy,
		Budgets:  BudgetConfig{MaxElapsed: time.Minute, MaxModelCalls: 8, MaxToolCalls: 8, MaxOutputBytes: 1 << 20, MaxSteps: 16},
		MCP: []MCPConfig{{
			Binding: "workspace", Version: 1, ServerID: "mendry-mcp-workspace", Transport: "stdio", Command: workspaceBinary,
			Args: []string{"--root", "PLACEHOLDER"},
			ToolBindings: []MCPToolConfig{
				{Name: "workspace.read", RemoteName: "read_file", Version: "v1", Effect: domain.ToolEffectRead},
				{Name: "workspace.write", RemoteName: "write_file", Version: "v1", Effect: domain.ToolEffectWrite, ArtifactType: "workspace.action", ArtifactSchemaVersion: "v1", ArtifactProvenance: domain.ProvenanceObserved, ExternalIDField: "actionId"},
				{Name: "workspace.verify", RemoteName: "verify_file", Version: "v1", Effect: domain.ToolEffectRead, ArtifactType: "workspace.verification", ArtifactSchemaVersion: "v1", ArtifactProvenance: domain.ProvenanceVerified, ExternalIDField: "actionId", SubjectExternalIDField: "actionId", VerificationStatusField: "status"},
			},
		}},
	}
}

func withWorkspaceRoot(config Config, root string) Config {
	config.MCP[0].Args = []string{"--root", root}
	return config
}

func testEvent(goal string) Event {
	return Event{Version: "v1", Source: "test", ExternalID: "event-1", OccurredAt: time.Unix(100, 0).UTC(), Goal: goal, Payload: map[string]any{"message": "hello"}, Context: map[string]string{"environment": "test"}}
}

func runOptions(t *testing.T, config Config, event Event, stateDir string) (Result, error) {
	t.Helper()
	configJSON, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	eventJSON, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	identifier := 0
	clock := time.Now().UTC()
	return Run(context.Background(), Options{
		ConfigJSON: configJSON, EventJSON: eventJSON, StateDir: stateDir,
		Env: func(key string) (string, bool) {
			if key == "VISIBLE_ENV" {
				return "visible", true
			}
			return "", false
		},
		Stdout: &stdout, Stderr: &bytes.Buffer{}, Now: func() time.Time { return clock },
		NewID: func() string {
			identifier++
			return fmt.Sprintf("test-id-%d", identifier)
		},
	})
}

func buildWorkspaceBinary(t *testing.T) string {
	t.Helper()
	backendRoot := filepath.Join("..", "..", "..", "..")
	binary := filepath.Join(t.TempDir(), "mcp-workspace")
	command := exec.Command("go", "build", "-o", binary, "./examples/mcp-workspace")
	command.Dir = backendRoot
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build workspace example: %v\n%s", err, output)
	}
	return binary
}

func TestRunRealMCPStdioFilesystemAndResume(t *testing.T) {
	workspaceBinary := buildWorkspaceBinary(t)
	root := t.TempDir()
	config := withWorkspaceRoot(testFixtureConfig(workspaceBinary, PolicyConfig{Mode: "allow-all", AllowedEffects: []domain.ToolEffect{domain.ToolEffectRead, domain.ToolEffectWrite}}), root)
	event := testEvent("perform a workspace write and verify it")
	stateDir := filepath.Join(t.TempDir(), "runs")

	result, err := runOptions(t, config, event, stateDir)
	if err != nil {
		t.Fatalf("real local run: %v\nresult=%+v", err, result)
	}
	if result.Run.State != domain.RunStateSucceeded || result.Run.ReasonCode != "action_verified" {
		t.Fatalf("run lifecycle = %#v err=%v invocations=%#v results=%#v messages=%#v artifacts=%#v", result.Run, err, result.Invocations, result.Results, result.Messages, result.Artifacts)
	}
	if result.Run.Budget.Consumed.ModelCalls != 3 || result.Run.Budget.Consumed.ToolCalls != 2 {
		t.Fatalf("run budget = %#v", result.Run.Budget)
	}
	if len(result.Invocations) != 2 || result.Invocations[0].State != domain.InvocationSucceeded || result.Invocations[1].State != domain.InvocationSucceeded {
		t.Fatalf("invocations = %#v", result.Invocations)
	}
	if len(result.Artifacts) < 2 {
		t.Fatalf("artifacts = %#v", result.Artifacts)
	}
	content, err := os.ReadFile(filepath.Join(root, "fixture", "reversible.txt"))
	if err != nil || string(content) != "reversible local example\n" {
		t.Fatalf("workspace content=%q err=%v", content, err)
	}

	resumed := config
	configJSON, _ := json.Marshal(resumed)
	eventJSON, _ := json.Marshal(event)
	resumedResult, err := Run(context.Background(), Options{ConfigJSON: configJSON, EventJSON: eventJSON, StateDir: stateDir, Resume: true, Env: func(string) (string, bool) { return "", false }, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}})
	if err != nil || resumedResult.Run.ID != result.Run.ID || resumedResult.Run.State != domain.RunStateSucceeded {
		t.Fatalf("resume result=%+v err=%v", resumedResult, err)
	}
}

func TestRunRestrictivePolicyRecordsRefusalWithoutExecutingWrite(t *testing.T) {
	workspaceBinary := buildWorkspaceBinary(t)
	root := t.TempDir()
	config := withWorkspaceRoot(testFixtureConfig(workspaceBinary, PolicyConfig{
		Mode: "restrict", AllowedTools: []string{"workspace.write"}, AllowedEffects: []domain.ToolEffect{domain.ToolEffectWrite},
		AllowedRoots: []string{"allowed"}, PathFields: []string{"path"},
	}), root)
	config.Profile.Completion = testCompletion(domain.CompletionSolutionDelivered, map[string]any{"artifactType": "report.summary"})
	result, err := runOptions(t, config, testEvent("perform a workspace write"), filepath.Join(t.TempDir(), "runs"))
	if err != nil {
		t.Fatalf("restrictive run: %v", err)
	}
	if result.Run.State != domain.RunStateSucceeded || len(result.Invocations) != 1 || result.Invocations[0].State != domain.InvocationRejected {
		t.Fatalf("restrictive result run=%#v err=%v invocations=%#v results=%#v messages=%#v artifacts=%#v", result.Run, err, result.Invocations, result.Results, result.Messages, result.Artifacts)
	}
	if result.Invocations[0].Authorized || len(result.Results) != 0 || len(result.Messages) < 3 || result.Messages[2].ToolCallID != "fixture-write-1" || !strings.Contains(result.Messages[2].Content, "rejected") {
		t.Fatalf("refusal was not durable: invocations=%#v results=%#v messages=%#v", result.Invocations, result.Results, result.Messages)
	}
	if _, err := os.Stat(filepath.Join(root, "fixture", "reversible.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("restricted write reached workspace: %v", err)
	}
}

func TestNormalizeConfigAndEventProduceStableIdentities(t *testing.T) {
	first := Config{SchemaVersion: 1, Provider: ProviderConfig{Mode: "fixture"}, Policy: PolicyConfig{}, MCP: []MCPConfig{{Binding: "b", Version: 1, ServerID: "server", Transport: "stdio", Command: "tool", ToolBindings: []MCPToolConfig{{Name: "z.tool", Version: "v1", Effect: domain.ToolEffectRead}, {Name: "a.tool", Version: "v1", Effect: domain.ToolEffectRead}}}, {Binding: "a", Version: 1, ServerID: "server", Transport: "stdio", Command: "tool"}}}
	second := Config{SchemaVersion: 1, Profile: ProfileConfig{Name: "report", Version: "v1", SystemPrompt: "Return a concise structured report for the configured goal.", Completion: testCompletion(domain.CompletionSolutionDelivered, map[string]any{}), SummaryArtifactType: "report.summary", MaxTokens: 4096}, Provider: ProviderConfig{Mode: "fixture", Binding: "default", BaseURL: "https://api.openai.com", Model: "gpt-5.6", ResponseFormat: "none"}, Policy: PolicyConfig{Mode: "restrict", AllowedEffects: []domain.ToolEffect{domain.ToolEffectRead}}, Budgets: BudgetConfig{MaxSteps: 32}, MCP: []MCPConfig{{Binding: "a", Version: 1, ServerID: "server", Transport: "stdio", Command: "tool", ToolBindings: []MCPToolConfig{}}, {Binding: "b", Version: 1, ServerID: "server", Transport: "stdio", Command: "tool", ToolBindings: []MCPToolConfig{{Name: "a.tool", Version: "v1", Effect: domain.ToolEffectRead}, {Name: "z.tool", Version: "v1", Effect: domain.ToolEffectRead}}}}}
	// The second value uses the same effective defaults as the first after normalization.
	firstNormalized, err := normalizeConfig(first)
	if err != nil {
		t.Fatal(err)
	}
	secondNormalized, err := normalizeConfig(second)
	if err != nil {
		t.Fatal(err)
	}
	firstHash, err := digestJSON(firstNormalized)
	if err != nil {
		t.Fatal(err)
	}
	secondHash, err := digestJSON(secondNormalized)
	if err != nil {
		t.Fatal(err)
	}
	if firstHash != secondHash {
		t.Fatalf("equivalent configs have different hashes: %s != %s\nfirst=%+v\nsecond=%+v", firstHash, secondHash, firstNormalized, secondNormalized)
	}
	firstEvent := normalizeEvent(Event{Version: "v1", Source: "source", ExternalID: "id", Goal: "goal"})
	secondEvent := normalizeEvent(Event{Version: "v1", Source: "source", ExternalID: "id", Goal: "goal", Payload: map[string]any{}, Context: map[string]string{}, OccurredAt: time.Time{}})
	firstEventHash, _ := digestJSON(firstEvent)
	secondEventHash, _ := digestJSON(secondEvent)
	if firstEventHash != secondEventHash {
		t.Fatalf("equivalent events have different hashes: %s != %s", firstEventHash, secondEventHash)
	}
}

func TestRunRejectsConfigurationDriftAndMissingResume(t *testing.T) {
	config := Config{SchemaVersion: 1, Profile: ProfileConfig{Name: "report", Version: "v1", Completion: testCompletion(domain.CompletionSolutionDelivered, map[string]any{"artifactType": "report.summary"}), MaxTokens: 32}, Provider: ProviderConfig{Mode: "fixture"}, Budgets: BudgetConfig{MaxSteps: 4}}
	event := testEvent("report")
	stateDir := filepath.Join(t.TempDir(), "runs")
	first, err := runOptions(t, config, event, stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if first.Run.State != domain.RunStateSucceeded {
		t.Fatalf("initial report = %#v", first.Run)
	}
	changed := config
	changed.Profile.SystemPrompt = "different trusted prompt"
	_, err = runOptions(t, changed, event, stateDir)
	if err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("configuration drift error = %v", err)
	}
	missingState := filepath.Join(t.TempDir(), "missing")
	configJSON, _ := json.Marshal(config)
	eventJSON, _ := json.Marshal(event)
	_, err = Run(context.Background(), Options{ConfigJSON: configJSON, EventJSON: eventJSON, StateDir: missingState, Resume: true, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "resume cannot create") {
		t.Fatalf("missing resume error = %v", err)
	}
}

func TestResolveMCPEnvironmentUsesInjectedLookupOnly(t *testing.T) {
	config := MCPConfig{Env: map[string]string{"STATIC_VALUE": "static"}, EnvRefs: map[string]string{"DYNAMIC_VALUE": "LOOKUP_VALUE"}}
	values, err := resolveMCPEnvironment(config, func(name string) (string, bool) {
		if name == "LOOKUP_VALUE" {
			return "resolved", true
		}
		return "", false
	})
	if err != nil || values["STATIC_VALUE"] != "static" || values["DYNAMIC_VALUE"] != "resolved" {
		t.Fatalf("resolved MCP environment = %#v err=%v", values, err)
	}
	if _, err := resolveMCPEnvironment(config, func(string) (string, bool) { return "", false }); err == nil {
		t.Fatal("missing environment reference was accepted")
	}
	if _, err := resolveMCPEnvironment(MCPConfig{Env: map[string]string{"DYNAMIC_VALUE": "static"}, EnvRefs: map[string]string{"DYNAMIC_VALUE": "LOOKUP_VALUE"}}, func(string) (string, bool) { return "resolved", true }); err == nil {
		t.Fatal("duplicate environment reference was accepted")
	}
}

func TestDecodeAndBoundedInputRejectUnknownAndTrailingValues(t *testing.T) {
	if _, err := decodeConfig([]byte(`{"schemaVersion":1,"unknown":true}`)); err == nil {
		t.Fatal("unknown config field was accepted")
	}
	if err := strictDecode([]byte(`{"x":1}{"y":2}`), &map[string]any{}); err == nil {
		t.Fatal("trailing JSON was accepted")
	}
	deep := any(map[string]any{})
	for index := 0; index < maxJSONDepth+2; index++ {
		deep = map[string]any{"next": deep}
	}
	if err := boundedJSONTree(deep, maxEventBytes); err == nil {
		t.Fatal("over-deep JSON was accepted")
	}
}

func TestResolveInvocationRequiresDurableTarget(t *testing.T) {
	_, err := ResolveInvocation(context.Background(), t.TempDir(), strings.Repeat("a", 32), "inv-1", domain.InvocationSucceeded, "confirmed", map[string]any{"ok": true}, time.Now())
	if err == nil {
		t.Fatal("resolve accepted a nonexistent durable run")
	}
	if !strings.Contains(err.Error(), "run state") && !strings.Contains(err.Error(), "lock") {
		t.Logf("nonexistent run error: %v", err)
	}
}

func ExampleFixtureProvider() {
	result, _ := NewFixtureProvider().Complete(context.Background(), domain.ModelTurn{UserMessage: "report"})
	fmt.Println(result.Provider, result.Content != "")
	// Output: fixture true
}
