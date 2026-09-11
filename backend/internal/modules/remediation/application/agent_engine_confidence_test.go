package application

import (
	"context"
	"strings"
	"testing"

	"mendry/backend/internal/modules/remediation/domain"
)

func TestDiagnosisPromptRequiresNumericConfidence(t *testing.T) {
	model := &promptCaptureModel{result: domain.ModelResult{
		Content: `{"schemaVersion":"v1","kind":"diagnosis","diagnosis":{"fixability":"insufficient_evidence","confidence":0.2,"causalReasoning":"not enough direct evidence","contradictions":[],"missingEvidence":[],"evidenceCitations":[],"recommendedNextAction":"collect"}}`,
	}}
	engine := NewAgentEngine(model, nil)
	if _, _, err := engine.TurnObservedWithConversationAndTools(
		context.Background(), RunIdentity{}, nil, 1, domain.RunStateDiagnosing,
		"project-1", "context", nil, nil,
	); err != nil {
		t.Fatalf("model turn: %v", err)
	}
	prompt := model.request.SystemPrompt + "\n" + model.request.UserMessage
	for _, want := range []string{
		"confidence must be a JSON number between 0 and 1",
		"never a label string such as low, medium, or high",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt)
		}
	}
}
