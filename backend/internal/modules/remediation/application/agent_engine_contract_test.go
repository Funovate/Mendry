package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"fixthe/backend/internal/modules/remediation/domain"
)

func TestDiagnosisPromptRequiresGlobalTimeAndEvidenceAssessment(t *testing.T) {
	model := &promptCaptureModel{result: domain.ModelResult{
		Content: `{"schemaVersion":"v1","kind":"diagnosis","diagnosis":{"fixability":"insufficient_evidence","confidence":0.2,"causalReasoning":"not enough direct evidence","contradictions":[],"missingEvidence":["direct_fault"],"evidenceCitations":[],"recommendedNextAction":"collect"}}`,
	}}
	engine := NewAgentEngine(model, nil)
	if _, _, err := engine.TurnObservedWithConversationAndTools(
		context.Background(), RunIdentity{}, nil, 1, domain.RunStateDiagnosing,
		"project-1", "triggering alert context", nil, nil,
	); err != nil {
		t.Fatalf("model turn: %v", err)
	}
	for _, want := range []string{
		"paired epoch values first", "explicit timestamp offsets", "source/system time-zone context",
		"timeAssessment", "sourceCoverage", "correlation", "causalClosure", "testSuspected",
		"evidence ID string", "evidenceId", "optional classification",
	} {
		if !strings.Contains(model.request.SystemPrompt+model.request.UserMessage, want) {
			t.Fatalf("prompt missing %q: system=%q user=%q", want, model.request.SystemPrompt, model.request.UserMessage)
		}
	}
	if strings.Contains(model.request.SystemPrompt, "redacted observations provided") {
		t.Fatalf("prompt still claims operational evidence is redacted: %q", model.request.SystemPrompt)
	}
	if strings.Contains(model.request.SystemPrompt+model.request.UserMessage, "evidenceRef") {
		t.Fatalf("prompt advertised an unsupported citation alias: %q", model.request.UserMessage)
	}
}

func TestAgentEngineUsesReasoningModelCompletionHeadroom(t *testing.T) {
	model := &promptCaptureModel{result: domain.ModelResult{
		Content: `{"schemaVersion":"v1","kind":"diagnosis","diagnosis":{"fixability":"insufficient_evidence","confidence":0.2,"causalReasoning":"not enough direct evidence","contradictions":[],"missingEvidence":["direct_fault"],"evidenceCitations":[],"recommendedNextAction":"collect"}}`,
	}}
	engine := NewAgentEngine(model, nil)
	if _, _, err := engine.TurnObservedWithConversationAndTools(
		context.Background(), RunIdentity{}, nil, 1, domain.RunStateDiagnosing,
		"project-1", "context", nil, nil,
	); err != nil {
		t.Fatalf("model turn: %v", err)
	}
	if model.request.MaxTokens != 8192 {
		t.Fatalf("MaxTokens = %d, want 8192", model.request.MaxTokens)
	}
}

func TestAgentEngineRejectsContentBeforeCommittingConversationHistory(t *testing.T) {
	model := &promptCaptureModel{result: domain.ModelResult{
		Content: `{"schemaVersion":"v1","kind":"diagnosis","diagnosis":"rejected-response"}`,
	}}
	conversation := NewAgentConversation("bootstrap-marker")
	engine := NewAgentEngine(model, nil)

	_, _, err := engine.TurnObservedWithConversationAndTools(
		context.Background(), RunIdentity{}, nil, 1, domain.RunStateDiagnosing,
		"project-1", "context", nil, conversation,
	)
	if err == nil || !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("Turn() error = %v, want invalid envelope", err)
	}
	if history := conversation.History(); len(history) != 0 {
		t.Fatalf("rejected response entered native history: %#v", history)
	}
	continuation := conversation.NativeContinuation("diagnose")
	if !strings.Contains(continuation, "bootstrap-marker") || strings.Contains(continuation, "rejected-response") {
		t.Fatalf("continuation = %q", continuation)
	}
}

func TestAgentEngineCommitsNativeToolsOnlyAfterAllCallsValidate(t *testing.T) {
	model := &promptCaptureModel{result: domain.ModelResult{
		ToolCalls: []domain.ToolCall{
			{ID: "call-1", Name: "repository.read_file", Arguments: map[string]interface{}{"path": "main.go"}},
			{ID: "call-2", Name: "repository.read_file"},
		},
	}}
	conversation := NewAgentConversation("bootstrap-marker")
	engine := NewAgentEngine(model, nil)

	_, _, err := engine.TurnObservedWithConversationAndTools(
		context.Background(), RunIdentity{}, nil, 1, domain.RunStateDiagnosing,
		"project-1", "context", nil, conversation,
	)
	if err == nil || !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("Turn() error = %v, want invalid envelope", err)
	}
	if history := conversation.History(); len(history) != 0 {
		t.Fatalf("invalid native response entered history: %#v", history)
	}
}

func TestAgentEngineCommitsValidNativeToolGroup(t *testing.T) {
	model := &promptCaptureModel{result: domain.ModelResult{
		ToolCalls: []domain.ToolCall{{
			ID: "call-1", Name: "repository.read_file", Arguments: map[string]interface{}{"path": "main.go"},
		}},
	}}
	conversation := NewAgentConversation("bootstrap-marker")
	engine := NewAgentEngine(model, nil)

	if _, _, err := engine.TurnObservedWithConversationAndTools(
		context.Background(), RunIdentity{}, nil, 1, domain.RunStateDiagnosing,
		"project-1", "context", nil, conversation,
	); err != nil {
		t.Fatalf("Turn() error = %v", err)
	}
	conversation.AppendToolResult(
		RequestTool{CallID: "call-1", ToolName: "repository.read_file"},
		ToolResult{Tool: "repository.read_file", Payload: map[string]interface{}{"path": "main.go"}}, nil,
	)
	history := conversation.History()
	if len(history) != 3 || history[0].Role != "user" || history[1].Role != "assistant" || len(history[1].ToolCalls) != 1 || history[2].Role != "tool" {
		t.Fatalf("native history = %#v", history)
	}
}

type promptCaptureModel struct {
	request domain.ModelTurn
	result  domain.ModelResult
}

func (m *promptCaptureModel) Complete(_ context.Context, request domain.ModelTurn) (domain.ModelResult, error) {
	m.request = request
	return m.result, nil
}
