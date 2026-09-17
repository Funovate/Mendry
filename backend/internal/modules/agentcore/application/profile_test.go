package application_test

import (
	"context"
	"testing"

	"mendry/backend/internal/modules/agentcore/application"
	"mendry/backend/internal/modules/agentcore/domain"
)

func TestProfileRegistryResolvesExactGoalProfileVersion(t *testing.T) {
	profile, err := application.NewGoalProfile(application.GoalProfileOptions{
		Name: "report", Version: "v1", SystemPrompt: "Produce a bounded report.",
		Completion:          domain.CompletionContract{Mode: domain.CompletionSolutionDelivered, Version: "v1", Parameters: map[string]any{"artifactType": "report.summary"}},
		SummaryArtifactType: "report.summary",
	})
	if err != nil {
		t.Fatal(err)
	}
	registry := application.NewProfileRegistry()
	if err := registry.Register(profile); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(profile); err == nil {
		t.Fatal("duplicate profile accepted")
	}
	resolved, err := registry.Resolve("report", "v1")
	if err != nil || resolved.Definition().Completion.Mode != domain.CompletionSolutionDelivered {
		t.Fatalf("resolved=%v err=%v", resolved, err)
	}
	if _, err := registry.Resolve("report", "v2"); err == nil {
		t.Fatal("unregistered profile version resolved")
	}
}

func TestGoalProfileProducesOnlyModelAuthoredSummary(t *testing.T) {
	profile, err := application.NewGoalProfile(application.GoalProfileOptions{
		Name: "report", Version: "v1", SystemPrompt: "Report.",
		Completion: domain.CompletionContract{Mode: domain.CompletionSolutionDelivered, Version: "v1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := profile.Interpret(context.Background(), domain.ModelResult{Content: "findings"})
	if err != nil || len(result.Artifacts) != 1 || result.Artifacts[0].Provenance != domain.ProvenanceModel || !result.Stop {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
