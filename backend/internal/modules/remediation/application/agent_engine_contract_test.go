package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"mendry/backend/internal/modules/remediation/domain"
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
		`{"schemaVersion":"v1","kind":"diagnosis"`,
		"fixability is a JSON string, never an object",
		"sourceCoverage is a JSON array, never an object",
		"timeAssessment.basis must be exactly one of paired_epoch, explicit_offset, contextual_zone, unresolved",
		"never put explanation text in basis",
		"status must be one of configured, inspected_success, inspected_empty, unavailable, not_applicable, not_inspected",
		"docker.logs",
		"no required first tool and no mandated sequence",
		"repository (list/read/search/history", "provider_evidence", "runtime_logs", "ssh_inspect",
		"Follow the strongest available evidence", "code-localization anchor",
		"deterministic defect", "conditional defect", "error-reporting boundary",
		"without collecting runtime evidence solely to fill optional time, host, request, or source-coverage fields",
		"timeAssessment.contradictory records an observed time mismatch for audit",
		"include that mismatch in materialContradictions only when it can invalidate",
		"AnalysisOriginal.time", "UTC log-event time", "since/until anchor", "window_lines", "returned_lines", "filtered", "truncated", "narrow the window", "pattern", "context_after", "goroutine frames",
		"repository-relative path", "repository.read_file", "repository.search",
		"non-retryable detail failure", "available fallback tools",
		"recoveryAction", "correct_request", "sanitized parameters", "retry_transient",
		"retriesRemaining", "use_fallback", "failed, unpersisted tool result",
	} {
		if !strings.Contains(model.request.SystemPrompt+model.request.UserMessage, want) {
			t.Fatalf("prompt missing %q: system=%q user=%q", want, model.request.SystemPrompt, model.request.UserMessage)
		}
	}
	if strings.Contains(model.request.SystemPrompt, "redacted observations provided") {
		t.Fatalf("prompt still claims operational evidence is redacted: %q", model.request.SystemPrompt)
	}
	if strings.Contains(model.request.SystemPrompt+model.request.UserMessage, `"evidenceRef":`) {
		t.Fatalf("prompt advertised an unsupported citation alias: %q", model.request.UserMessage)
	}
	for _, forbidden := range []string{
		"only docker.logs in the first collection turn",
		"wait for its observation before requesting",
		"must request", "first ls the hinted logPath",
	} {
		if strings.Contains(model.request.UserMessage, forbidden) {
			t.Fatalf("prompt still mandates tool ordering (%q): %q", forbidden, model.request.UserMessage)
		}
	}
}

// TestDiagnosisPromptIsGoalOrientedWithoutToolOrdering 证明 D1/R4：诊断 prompt
// 表达 objective、completion criteria、可用 capability classes 与
// code-localization 语义，不再编码 Docker-first/SSH-first 的工具顺序。
func TestDiagnosisPromptIsGoalOrientedWithoutToolOrdering(t *testing.T) {
	prompt := (&AgentEngine{}).buildPrompt(domain.RunStateDiagnosing)
	for _, want := range []string{
		"Objective:", "Completion criteria:",
		"service fact gate",
		"any advertised bounded read tool in any useful order",
		"no required first tool",
		"repository", "provider_evidence", "runtime_logs", "ssh_inspect",
		"Follow the strongest available evidence",
		"strong code-localization anchor",
		"surrounding control flow, callers, and production reachability",
		"deterministic defect", "conditional defect", "error-reporting boundary",
		"Request additional evidence only when it can change the causal conclusion, repair choice, or automation safety",
		"complete the causal explanation without collecting runtime evidence solely",
		"Tool guidance (no ordering is required)",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("goal-oriented prompt missing %q: %s", want, prompt)
		}
	}
	for _, forbidden := range []string{
		"only docker.logs in the first collection turn",
		"wait for its observation before requesting",
		"must request", "before requesting repository tools",
		"first ls the hinted logPath directory",
	} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("prompt encodes a provider decision tree (%q): %s", forbidden, prompt)
		}
	}
}

func TestDiagnosisPromptSeparatesTestAuthorizationFromCausalClosure(t *testing.T) {
	prompt := (&AgentEngine{}).buildPrompt(domain.RunStateDiagnosing)
	for _, want := range []string{
		"Test authorization is a separate audit dimension",
		"testPolicyMatched=false or unknown does not by itself make root-cause evidence insufficient",
		"record the unmatched test policy as an audit finding",
		"Use insufficient_evidence only when missing material evidence prevents causal explanation",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("diagnosis prompt missing %q: %s", want, prompt)
		}
	}
}

func TestDiagnosisPromptRequiresDockerCoverageRefinement(t *testing.T) {
	prompt := (&AgentEngine{}).buildPrompt(domain.RunStateDiagnosing)
	for _, want := range []string{
		"coverage_limited", "refinement_required", "window_lines=-1",
		"skipped an additional full-log count", "before relying on that runtime result to resolve a material question",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("diagnosis prompt missing %q: %s", want, prompt)
		}
	}
}

func TestPlanningPromptIncludesToolRecoveryContract(t *testing.T) {
	prompt := (&AgentEngine{}).buildPrompt(domain.RunStatePlanning)
	for _, want := range []string{"recoveryAction", "correct_request", "retry_transient", "retriesRemaining", "use_fallback"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("planning prompt missing %q: %s", want, prompt)
		}
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
