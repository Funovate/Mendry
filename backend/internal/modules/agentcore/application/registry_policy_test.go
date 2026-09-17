package application_test

import (
	"context"
	"testing"

	"mendry/backend/internal/modules/agentcore/application"
	"mendry/backend/internal/modules/agentcore/domain"
)

type countingExecutor struct{ calls int }

func (e *countingExecutor) Execute(context.Context, domain.ToolCall) (domain.ToolExecution, error) {
	e.calls++
	return domain.ToolExecution{Output: "ok"}, nil
}

func closedSchema(properties map[string]any, required ...string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

func TestToolRegistryAcceptsCustomReadAndWriteWithoutCoreSwitch(t *testing.T) {
	registry := application.NewToolRegistry()
	for _, definition := range []domain.ToolDefinition{
		{Name: "report.generate", Version: "v1", Effect: domain.ToolEffectRead, Parameters: closedSchema(nil)},
		{Name: "workspace.replace", Version: "v3", Effect: domain.ToolEffectWrite, Parameters: closedSchema(map[string]any{"path": map[string]any{"type": "string"}}, "path")},
	} {
		if err := registry.Register(definition, &countingExecutor{}); err != nil {
			t.Fatalf("register %s: %v", definition.Name, err)
		}
	}
	if len(registry.Definitions()) != 2 {
		t.Fatalf("definitions = %d", len(registry.Definitions()))
	}
	definition, _, ok := registry.Resolve("workspace.replace", "v3")
	if !ok {
		t.Fatal("custom write tool not resolved")
	}
	if err := registry.ValidateArguments(definition, map[string]any{"path": "main.go", "extra": true}); err == nil {
		t.Fatal("closed schema accepted an extra argument")
	}
}

func TestToolRegistryRejectsInvalidTrustMetadataAndCollisions(t *testing.T) {
	registry := application.NewToolRegistry()
	executor := &countingExecutor{}
	valid := domain.ToolDefinition{Name: "repo.read", Version: "v1", Effect: domain.ToolEffectRead, Parameters: closedSchema(nil)}
	if err := registry.Register(valid, executor); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(valid, executor); err == nil {
		t.Fatal("collision accepted")
	}
	invalid := valid
	invalid.Name = "read"
	if err := registry.Register(invalid, executor); err == nil {
		t.Fatal("non-namespaced name accepted")
	}
	invalid = valid
	invalid.Version = "latest"
	if err := registry.Register(invalid, executor); err == nil {
		t.Fatal("unversioned tool accepted")
	}
	invalid = valid
	invalid.Name = "repo.open"
	invalid.Parameters = map[string]any{"type": "object", "properties": map[string]any{}}
	if err := registry.Register(invalid, executor); err == nil {
		t.Fatal("open schema accepted")
	}
}

func TestRestrictionPolicyChecksEffectAndPath(t *testing.T) {
	policy := application.NewRestrictionPolicy(application.RestrictionConfig{
		AllowedTools: []string{"workspace.replace"}, AllowedEffects: []domain.ToolEffect{domain.ToolEffectWrite},
		AllowedRoots: []string{"src"}, PathFields: []string{"path"},
	})
	definition := domain.ToolDefinition{Name: "workspace.replace", Effect: domain.ToolEffectWrite}
	advertised, _ := policy.Evaluate(context.Background(), application.PolicyInput{Definition: definition, Stage: "advertisement"})
	if !advertised.Allowed {
		t.Fatalf("advertisement denied: %#v", advertised)
	}
	denied, _ := policy.Evaluate(context.Background(), application.PolicyInput{Definition: definition, Stage: "dispatch", Arguments: map[string]any{"path": "../secret"}})
	if denied.Allowed || denied.Code != "path_denied" {
		t.Fatalf("dispatch decision = %#v", denied)
	}
}
