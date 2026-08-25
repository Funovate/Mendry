package application

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"fixthe/backend/internal/modules/remediation/domain"
)

func TestRedactConversationValueRedactsTypedFileContent(t *testing.T) {
	value := domain.FileContent{
		Path:    "config.go",
		Content: []byte("password=hunter2 Authorization: Bearer sk-secretvalue https://alice:pass@example.test/repo.git"),
	}

	encoded, err := json.Marshal(boundedConversationValue(value, 4096))
	if err != nil {
		t.Fatalf("marshal bounded value: %v", err)
	}
	text := string(encoded)
	for _, secret := range []string{"hunter2", "sk-secretvalue", "alice:pass", "https://alice"} {
		if strings.Contains(text, secret) {
			t.Fatalf("conversation payload leaked %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, "[redacted]") {
		t.Fatalf("conversation payload was not redacted: %s", text)
	}
}

func TestBoundedConversationValueKeepsRawInspectOutput(t *testing.T) {
	value := domain.SSHInspectResult{
		Command: "cd -- '/srv/app' && 'ls' '/var/log'", ExitCode: 0,
		Stdout: "password=hunter2\n-----BEGIN OPENSSH PRIVATE KEY-----\nsecret\n-----END OPENSSH PRIVATE KEY-----\n/tmp/fixthe-sshlog-abc/id\n",
		Stderr: "sk-not-redacted-on-inspect",
	}
	encoded, err := json.Marshal(boundedConversationValue(value, 4096))
	if err != nil {
		t.Fatalf("marshal inspect payload: %v", err)
	}
	text := string(encoded)
	if !strings.Contains(text, "password=hunter2") || !strings.Contains(text, "sk-not-redacted-on-inspect") {
		t.Fatalf("inspect payload lost raw output: %s", text)
	}
	if strings.Contains(text, "BEGIN OPENSSH PRIVATE KEY") || strings.Contains(text, "/tmp/fixthe-sshlog-abc/id") {
		t.Fatalf("inspect payload leaked private key material: %s", text)
	}
}

func TestAppendProtocolErrorOmitsDecoderInternals(t *testing.T) {
	conversation := NewAgentConversation("bootstrap")
	conversation.AppendProtocolError(domain.RunStateDiagnosing)
	text := conversation.ContextText()
	if !strings.Contains(text, "protocol_observation") || !strings.Contains(text, "invalid_envelope") {
		t.Fatalf("protocol observation missing: %s", text)
	}
	if !strings.Contains(text, "diagnosis must be an object") {
		t.Fatalf("protocol observation missing correction: %s", text)
	}
	for _, leaked := range []string{"DiagnosisOutput", "cannot unmarshal", "envelope decode"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("protocol observation leaked %q: %s", leaked, text)
		}
	}
}

func TestAppendProtocolErrorUsesSpecificEvidenceCitationGuidance(t *testing.T) {
	conversation := NewAgentConversation("bootstrap")
	correction := ProtocolCorrectionFor(domain.RunStateDiagnosing, &domain.EvidenceCitationFieldError{Field: "evidenceRef"})
	conversation.AppendProtocolError(domain.RunStateDiagnosing, correction)

	text := conversation.ContextText()
	for _, want := range []string{
		"invalid_evidence_citation",
		"diagnosis.evidenceCitations[].evidenceRef",
		"expectedField",
		"evidenceId",
		"optional classification",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("protocol observation missing %q: %s", want, text)
		}
	}
	for _, leaked := range []string{"DiagnosisOutput", "cannot unmarshal", "secretValue", "do-not-replay"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("protocol observation leaked %q: %s", leaked, text)
		}
	}
}

func TestAppendProtocolErrorNormalizesUntrustedCorrectionFields(t *testing.T) {
	conversation := NewAgentConversation("bootstrap")
	conversation.AppendProtocolError(domain.RunStateDiagnosing, ProtocolCorrection{
		Code:          "invalid_evidence_citation",
		Path:          "model-controlled-path",
		ExpectedField: strings.Repeat("x", maxObservationBytes),
		Message:       "raw provider response",
	})

	text := conversation.ContextText()
	if strings.Contains(text, "model-controlled-path") || strings.Contains(text, "raw provider response") {
		t.Fatalf("untrusted correction fields entered context: %s", text)
	}
	if !strings.Contains(text, "diagnosis.evidenceCitations[].evidenceRef") || !strings.Contains(text, "evidenceId") {
		t.Fatalf("normalized correction lost canonical guidance: %s", text)
	}
}

func TestProtocolCorrectionFallbackDoesNotCopyUnknownDecoderText(t *testing.T) {
	conversation := NewAgentConversation("bootstrap")
	correction := ProtocolCorrectionFor(domain.RunStateDiagnosing, fmt.Errorf(`json: unknown field "modelSecret"`))
	conversation.AppendProtocolError(domain.RunStateDiagnosing, correction)

	text := conversation.ContextText()
	if strings.Contains(text, "modelSecret") || strings.Contains(text, "unknown field") {
		t.Fatalf("generic correction copied decoder text: %s", text)
	}
	if !strings.Contains(text, "invalid_envelope") || !strings.Contains(text, "evidenceId") {
		t.Fatalf("generic correction lost safe fallback: %s", text)
	}
}

func TestProtocolCorrectionUsesPhaseFallbackOutsideDiagnosis(t *testing.T) {
	correction := ProtocolCorrectionFor(domain.RunStatePlanning, &domain.EvidenceCitationFieldError{Field: "evidenceRef"})
	if correction.Code != "invalid_envelope" || !strings.Contains(correction.Message, "planCandidates") {
		t.Fatalf("planning correction = %#v, want planCandidates fallback", correction)
	}
	if strings.Contains(correction.Message, "evidenceId") || correction.Path != "" {
		t.Fatalf("planning correction exposed diagnosis-specific guidance: %#v", correction)
	}

	conversation := NewAgentConversation("bootstrap")
	conversation.AppendProtocolError(domain.RunStatePlanning, ProtocolCorrection{Code: "invalid_evidence_citation"})
	text := conversation.ContextText()
	if !strings.Contains(text, "planCandidates") || strings.Contains(text, "evidenceId") {
		t.Fatalf("planning normalization exposed diagnosis-specific guidance: %s", text)
	}
}

func TestRedactConversationValueNormalizesStructPayload(t *testing.T) {
	value := domain.SearchResult{Matches: []domain.SearchMatch{{
		Path: "handler.go", LineNumber: 12, Line: "token: secret-value",
	}}}

	encoded, err := json.Marshal(redactConversationValue(value))
	if err != nil {
		t.Fatalf("marshal normalized value: %v", err)
	}
	text := string(encoded)
	if strings.Contains(text, "secret-value") {
		t.Fatalf("normalized struct leaked secret assignment: %s", text)
	}
	if !strings.Contains(text, "[redacted]") {
		t.Fatalf("normalized struct was not redacted: %s", text)
	}
}

func TestNativeContinuationAcknowledgesBootstrapOnlyAfterSuccessfulTurn(t *testing.T) {
	conversation := NewAgentConversation("bootstrap-marker")
	first := conversation.NativeContinuation("diagnose")
	if !strings.Contains(first, "bootstrap-marker") {
		t.Fatalf("first continuation = %q", first)
	}
	if retry := conversation.NativeContinuation("diagnose"); retry != first {
		t.Fatalf("unacknowledged retry changed: first=%q retry=%q", first, retry)
	}
	conversation.RecordModelTurn(first, domain.ModelResult{Content: "assistant-marker"})
	next := conversation.NativeContinuation("diagnose again")
	if strings.Contains(next, "bootstrap-marker") || strings.Contains(next, "assistant-marker") {
		t.Fatalf("next continuation repeated native history: %q", next)
	}
}

func TestNativeContinuationDeliversStrictToolObservationOnce(t *testing.T) {
	conversation := NewAgentConversation("bootstrap")
	first := conversation.NativeContinuation("diagnose")
	conversation.RecordModelTurn(first, domain.ModelResult{Content: `{"schemaVersion":"v1","kind":"requestTool"}`})
	conversation.AppendToolResult(RequestTool{ToolName: ToolRepoReadFile, Parameters: map[string]interface{}{"path": "main.go"}},
		ToolResult{Tool: ToolRepoReadFile, Payload: map[string]interface{}{"path": "main.go"}}, nil)

	second := conversation.NativeContinuation("diagnose")
	if !strings.Contains(second, "tool_observation") || !strings.Contains(second, ToolRepoReadFile) {
		t.Fatalf("strict continuation = %q", second)
	}
	conversation.RecordModelTurn(second, domain.ModelResult{Content: `{"schemaVersion":"v1","kind":"stop"}`})
	if third := conversation.NativeContinuation("planning"); strings.Contains(third, "tool_observation") {
		t.Fatalf("planning continuation repeated tool observation: %q", third)
	}
}

func TestNativeContinuationKeepsNativeToolResultOnlyInHistory(t *testing.T) {
	conversation := NewAgentConversation("bootstrap")
	first := conversation.NativeContinuation("diagnose")
	conversation.RecordModelTurn(first, domain.ModelResult{ToolCalls: []domain.ToolCall{{
		ID: "call-1", Name: ToolRepoReadFile, Arguments: map[string]interface{}{"path": "main.go"},
	}}})
	conversation.AppendToolResult(RequestTool{CallID: "call-1", ToolName: ToolRepoReadFile},
		ToolResult{Tool: ToolRepoReadFile, Payload: map[string]interface{}{"path": "main.go"}}, nil)

	next := conversation.NativeContinuation("diagnose")
	if strings.Contains(next, "tool_observation") || strings.Contains(next, "main.go") {
		t.Fatalf("native tool result was duplicated in continuation: %q", next)
	}
	history := conversation.History()
	last := history[len(history)-1]
	if last.Role != "tool" || last.ToolCallID != "call-1" || !strings.Contains(last.Content, "main.go") {
		t.Fatalf("native tool history = %#v", history)
	}
}

func TestHistoryTruncationKeepsNativeToolCallGroupsIntact(t *testing.T) {
	conversation := NewAgentConversation("bootstrap")
	conversation.RecordModelTurn("first turn", domain.ModelResult{ToolCalls: []domain.ToolCall{
		{ID: "call-1", Name: ToolRepoReadFile, Arguments: map[string]interface{}{"path": "first.go"}},
		{ID: "call-2", Name: ToolRepoReadFile, Arguments: map[string]interface{}{"path": "second.go"}},
	}})
	for _, callID := range []string{"call-1", "call-2"} {
		conversation.AppendToolResult(
			RequestTool{CallID: callID, ToolName: ToolRepoReadFile},
			ToolResult{Tool: ToolRepoReadFile, Payload: map[string]interface{}{"path": callID + ".go"}},
			nil,
		)
	}
	for index := 0; index < 10; index++ {
		conversation.RecordModelTurn(
			fmt.Sprintf("user-%d", index),
			domain.ModelResult{Content: fmt.Sprintf("assistant-%d", index)},
		)
	}
	conversation.RecordModelTurn("retained tool turn", domain.ModelResult{ToolCalls: []domain.ToolCall{
		{ID: "call-3", Name: ToolRepoReadFile, Arguments: map[string]interface{}{"path": "third.go"}},
		{ID: "call-4", Name: ToolRepoReadFile, Arguments: map[string]interface{}{"path": "fourth.go"}},
	}})
	for _, callID := range []string{"call-3", "call-4"} {
		conversation.AppendToolResult(
			RequestTool{CallID: callID, ToolName: ToolRepoReadFile},
			ToolResult{Tool: ToolRepoReadFile, Payload: map[string]interface{}{"path": callID + ".go"}},
			nil,
		)
	}
	for index := 10; index < 13; index++ {
		conversation.RecordModelTurn(
			fmt.Sprintf("user-%d", index),
			domain.ModelResult{Content: fmt.Sprintf("assistant-%d", index)},
		)
	}

	history := conversation.History()
	if len(history) > maxConversationItems {
		t.Fatalf("history items = %d, want at most %d", len(history), maxConversationItems)
	}
	if len(history) == 0 || history[0].Role != "user" {
		t.Fatalf("history starts with %#v, want a user turn", history)
	}
	pendingToolCalls := make(map[string]struct{})
	for _, message := range history {
		switch message.Role {
		case "assistant":
			for _, call := range message.ToolCalls {
				pendingToolCalls[call.ID] = struct{}{}
			}
		case "tool":
			if _, exists := pendingToolCalls[message.ToolCallID]; !exists {
				t.Fatalf("history retained orphaned tool message %#v", message)
			}
			delete(pendingToolCalls, message.ToolCallID)
		}
	}
	if len(pendingToolCalls) != 0 {
		t.Fatalf("history omitted tool results for calls %#v", pendingToolCalls)
	}
	if got := history[len(history)-1].Content; got != "assistant-12" {
		t.Fatalf("last history content = %q, want latest assistant response", got)
	}
}

func TestNativeContinuationDeliversProtocolCorrectionOnce(t *testing.T) {
	conversation := NewAgentConversation("bootstrap")
	first := conversation.NativeContinuation("diagnose")
	conversation.RecordModelTurn(first, domain.ModelResult{Content: `{"invalid":true}`})
	conversation.AppendProtocolError(domain.RunStateDiagnosing)
	second := conversation.NativeContinuation("diagnose")
	if !strings.Contains(second, "protocol_observation") || !strings.Contains(second, "invalid_envelope") {
		t.Fatalf("protocol continuation = %q", second)
	}
	conversation.RecordModelTurn(second, domain.ModelResult{Content: `{"schemaVersion":"v1","kind":"stop"}`})
	if third := conversation.NativeContinuation("diagnose"); strings.Contains(third, "protocol_observation") {
		t.Fatalf("protocol correction repeated: %q", third)
	}
}

func TestHistoryEnforcesAggregateBytesByEvictingOldestCompleteTurns(t *testing.T) {
	conversation := NewAgentConversation("bootstrap")
	for index := 0; index < 4; index++ {
		conversation.RecordModelTurn(
			fmt.Sprintf("user-%d:%s", index, strings.Repeat("u", maxMessageBytes)),
			domain.ModelResult{Content: fmt.Sprintf("assistant-%d:%s", index, strings.Repeat("a", maxMessageBytes))},
		)
	}

	history := conversation.History()
	if got := encodedConversationBytes(history); got > maxConversationBytes {
		t.Fatalf("history bytes = %d, want at most %d", got, maxConversationBytes)
	}
	if len(history) == 0 || !strings.HasPrefix(history[len(history)-1].Content, "assistant-3:") {
		t.Fatalf("history did not retain latest complete turn: %#v", history)
	}
	for _, message := range history {
		if strings.HasPrefix(message.Content, "user-0:") || strings.HasPrefix(message.Content, "assistant-0:") {
			t.Fatalf("history retained oldest turn beyond byte bound: %#v", history)
		}
	}
}
