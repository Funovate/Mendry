package application

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"mendry/backend/internal/modules/remediation/domain"
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

func TestBoundedConversationValueRedactsInspectOutput(t *testing.T) {
	value := domain.SSHInspectResult{
		Command: "cd -- '/srv/app' && 'ls' '/var/log'", ExitCode: 0,
		Stdout: "password=hunter2\n-----BEGIN OPENSSH PRIVATE KEY-----\nsecret\n-----END OPENSSH PRIVATE KEY-----\n/tmp/mendry-sshlog-abc/id\n",
		Stderr: "bearer sk-secretvalue",
	}
	encoded, err := json.Marshal(boundedConversationValue(value, 4096))
	if err != nil {
		t.Fatalf("marshal inspect payload: %v", err)
	}
	text := string(encoded)
	// canonical runtime evidence 的同一套脱敏也作用于 conversation 边界：
	// 凭据形态文本、PEM 私钥块与临时 key 路径都不进入模型上下文。
	for _, secret := range []string{"hunter2", "sk-secretvalue", "BEGIN OPENSSH PRIVATE KEY", "/tmp/mendry-sshlog-abc/id"} {
		if strings.Contains(text, secret) {
			t.Fatalf("inspect payload leaked %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, "password=[redacted]") || !strings.Contains(text, "[redacted]") {
		t.Fatalf("inspect payload was not redacted: %s", text)
	}
	if !strings.Contains(text, "'ls' '/var/log'") {
		t.Fatalf("inspect payload lost reconstructed command: %s", text)
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

func TestAppendToolResultOffersOneCorrectiveRequest(t *testing.T) {
	conversation := NewAgentConversation("bootstrap")
	request := RequestTool{ToolName: ToolRepoReadFile, Parameters: map[string]interface{}{"path": ""}}

	conversation.AppendToolResult(request, ToolResult{}, &ToolRejection{Code: RejectArguments, Tool: ToolRepoReadFile})
	first := lastConversationLine(conversation.ContextText())
	for _, want := range []string{`"code":"invalid_arguments"`, `"recoveryAction":"correct_request"`, `"failureAttempt":1`, `"retriesRemaining":1`} {
		if !strings.Contains(first, want) {
			t.Fatalf("first corrective observation missing %q: %s", want, first)
		}
	}

	conversation.AppendToolResult(request, ToolResult{}, &ToolRejection{Code: RejectArguments, Tool: ToolRepoReadFile})
	second := lastConversationLine(conversation.ContextText())
	for _, want := range []string{`"recoveryAction":"use_fallback"`, `"failureAttempt":2`, `"retriesRemaining":0`} {
		if !strings.Contains(second, want) {
			t.Fatalf("repeated corrective observation missing %q: %s", want, second)
		}
	}
}

func TestAppendToolResultOffersOneTransientRetry(t *testing.T) {
	conversation := NewAgentConversation("bootstrap")
	err := &domain.ToolRuntimeError{Code: "connector_timeout", Retryable: true}

	conversation.AppendToolResult(RequestTool{ToolName: ToolDockerLogs}, ToolResult{}, err)
	first := lastConversationLine(conversation.ContextText())
	for _, want := range []string{`"retryable":true`, `"recoveryAction":"retry_transient"`, `"failureAttempt":1`, `"retriesRemaining":1`} {
		if !strings.Contains(first, want) {
			t.Fatalf("transient observation missing %q: %s", want, first)
		}
	}

	conversation.AppendToolResult(RequestTool{ToolName: ToolDockerLogs}, ToolResult{}, err)
	second := lastConversationLine(conversation.ContextText())
	for _, want := range []string{`"recoveryAction":"use_fallback"`, `"failureAttempt":2`, `"retriesRemaining":0`} {
		if !strings.Contains(second, want) {
			t.Fatalf("repeated transient observation missing %q: %s", want, second)
		}
	}
}

func TestAppendToolResultNonRetryableFailureUsesFallback(t *testing.T) {
	conversation := NewAgentConversation("bootstrap")
	conversation.AppendToolResult(
		RequestTool{ToolName: ToolDockerLogs}, ToolResult{},
		&domain.ToolRuntimeError{Code: "authorization", Retryable: false},
	)

	observation := lastConversationLine(conversation.ContextText())
	for _, want := range []string{`"retryable":false`, `"recoveryAction":"use_fallback"`, `"failureAttempt":1`, `"retriesRemaining":0`} {
		if !strings.Contains(observation, want) {
			t.Fatalf("non-retryable observation missing %q: %s", want, observation)
		}
	}
}

func TestDockerLogRefinementStateRequiresANewerCompleteResult(t *testing.T) {
	conversation := NewAgentConversation("bootstrap")
	conversation.AppendToolResult(RequestTool{ToolName: ToolDockerLogs}, ToolResult{
		Tool: ToolDockerLogs, RefinementRequired: true, RefinementReason: "tail_limit",
		Payload: domain.DockerLogResult{CoverageLimited: true, RefinementRequired: true, CoverageReason: "tail_limit"},
	}, nil)
	version, reason, pending := conversation.PendingDockerLogRefinement()
	if version != 1 || reason != "tail_limit" || !pending {
		t.Fatalf("pending refinement = %d/%q/%t", version, reason, pending)
	}

	conversation.AppendDockerLogRefinement(reason)
	refinement := lastConversationLine(conversation.ContextText())
	for _, want := range []string{`"code":"docker_log_refinement_required"`, `"reason":"tail_limit"`, "exact alert event time"} {
		if !strings.Contains(refinement, want) {
			t.Fatalf("refinement observation missing %q: %s", want, refinement)
		}
	}

	conversation.AppendToolResult(RequestTool{ToolName: ToolDockerLogs}, ToolResult{
		Tool: ToolDockerLogs, Payload: domain.DockerLogResult{Stdout: "panic\n"},
	}, nil)
	version, reason, pending = conversation.PendingDockerLogRefinement()
	if version != 1 || reason != "" || pending {
		t.Fatalf("completed refinement = %d/%q/%t", version, reason, pending)
	}
}

func TestCorrectableToolErrorCodes(t *testing.T) {
	tests := map[string]bool{
		"invalid_arguments":       true,
		"path_out_of_scope":       true,
		"not_found":               true,
		"connector_not_found":     true,
		"authorization":           false,
		"connector_authorization": false,
		"budget_exceeded":         false,
	}
	for code, want := range tests {
		if got := correctableToolErrorCode(code); got != want {
			t.Errorf("correctableToolErrorCode(%q) = %t, want %t", code, got, want)
		}
	}
}

func lastConversationLine(text string) string {
	lines := strings.Split(text, "\n")
	return lines[len(lines)-1]
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

func TestConversationLedgerCountsBootstrapAndPendingObservationsOnce(t *testing.T) {
	bootstrap := strings.Repeat("b", 4000)
	conversation := NewAgentConversation(bootstrap)
	if got := conversation.CumulativeModelVisibleBytes(); got != 4000 {
		t.Fatalf("cumulative after bootstrap = %d, want 4000", got)
	}

	// 非 pending 文本快照（native tool result / assistant output 留在
	// observations 供 ContextText 有界回退的副本）在 resilient provider-native
	// 对话中不是独立交付：载荷由对应 native message 计一次，快照绝不额外计
	// （parent-review 双计修正）。
	before := conversation.cumulativeBytes
	conversation.appendItem("tool_observation", strings.Repeat("x", 5000), false)
	if delta := conversation.cumulativeBytes - before; delta != 0 {
		t.Fatalf("non-pending tool snapshot counted %d ledger bytes, want 0 (native tool message owns the payload)", delta)
	}
	before = conversation.cumulativeBytes
	conversation.appendItem("assistant_output", strings.Repeat("a", 5000), false)
	if delta := conversation.cumulativeBytes - before; delta != 0 {
		t.Fatalf("non-pending assistant snapshot counted %d ledger bytes, want 0 (assistant message owns the payload)", delta)
	}

	// strict-JSON/protocol pending 观察走 textual continuation 通道：追加时按
	// formatted block（含 kind/sequence wrapper）计一次。
	before = conversation.cumulativeBytes
	conversation.appendItem("tool_observation", strings.Repeat("x", 5000), true)
	pendingItem := conversation.pending[len(conversation.pending)-1]
	want := len(formatConversationItem(pendingItem))
	if delta := conversation.cumulativeBytes - before; delta != want {
		t.Fatalf("pending observation cumulative delta = %d, want formatted block bytes %d", delta, want)
	}
	// 超过 item 上限后旧观察被 trim，但累计压力不回退：被 trim 的内容曾经进入
	// provider-visible 通道，压力账本不能因逐出而回落（否则自动 trigger 会
	// 丢失 churn）。
	peak := conversation.cumulativeBytes
	for index := 0; index < maxConversationItems+8; index++ {
		conversation.appendItem("protocol_observation", "small", true)
	}
	if len(conversation.observations) != maxConversationItems {
		t.Fatalf("observations after trim = %d, want %d", len(conversation.observations), maxConversationItems)
	}
	if conversation.cumulativeBytes < peak {
		t.Fatalf("cumulative decreased after trim: %d < %d", conversation.cumulativeBytes, peak)
	}
}

func TestConversationPressureCountsPendingReplayWhenItBecomesHistory(t *testing.T) {
	conversation := NewAgentConversation("boot")
	conversation.appendItem("protocol_observation", "correct the envelope", true)
	afterEnqueue := conversation.CumulativeModelVisibleBytes()
	continuation := conversation.NativeContinuation("diagnose")
	if !strings.Contains(continuation, "correct the envelope") {
		t.Fatalf("prepared continuation omitted pending observation: %s", continuation)
	}

	conversation.RecordModelTurn(continuation, domain.ModelResult{Content: "assistant answer"})
	userMessage := conversation.messages[len(conversation.messages)-2]
	assistantMessage := conversation.messages[len(conversation.messages)-1]
	want := afterEnqueue + encodedModelMessage(userMessage) + encodedModelMessage(assistantMessage)
	if got := conversation.CumulativeModelVisibleBytes(); got != want {
		t.Fatalf("pressure after pending replay = %d, want %d", got, want)
	}
	if len(conversation.pending) != 0 {
		t.Fatalf("delivered pending observations = %d, want 0", len(conversation.pending))
	}
}

func TestConversationLedgerCountsNativeMessagesOnce(t *testing.T) {
	// strict-JSON 内容轮：成功 user continuation 和 assistant 两条 native
	// message 都成为可重放 history，因此各形成一次 context pressure；
	// assistant_output 文本快照不额外计（它不形成第二个 context 结构）。
	conversation := NewAgentConversation("boot")
	before := conversation.cumulativeBytes
	conversation.RecordModelTurn("user message", domain.ModelResult{Content: "assistant answer"})
	userMessage := conversation.messages[len(conversation.messages)-2]
	assistantMessage := conversation.messages[len(conversation.messages)-1]
	want := before + encodedModelMessage(userMessage) + encodedModelMessage(assistantMessage)
	if conversation.cumulativeBytes != want {
		t.Fatalf("cumulative after content turn = %d, want %d (assistant_output snapshot must not double-count)", conversation.cumulativeBytes, want)
	}

	// native tool-call 轮：user + assistant(toolCalls) 两条 message 进入 history；
	// user 文本即使含已排队 context，也按新增 replay/history 压力计量。
	before = conversation.cumulativeBytes
	conversation.RecordModelTurn("tool turn", domain.ModelResult{ToolCalls: []domain.ToolCall{{
		ID: "c1", Name: "repository.read_file", Arguments: map[string]interface{}{"path": "main.go"},
	}}})
	added := conversation.messages[len(conversation.messages)-2:]
	want = before + encodedModelMessage(added[0]) + encodedModelMessage(added[1])
	if conversation.cumulativeBytes != want {
		t.Fatalf("cumulative after native tool-call turn = %d, want %d", conversation.cumulativeBytes, want)
	}

	// native tool result：只有一条 native tool message 计入；tool_observation
	// 文本快照不额外计（payload 由 tool message 拥有）。完整组（assistant
	// tool_call + tool result）已落盘。
	before = conversation.cumulativeBytes
	conversation.AppendToolResult(RequestTool{ToolName: "repository.read_file", CallID: "c1", Parameters: map[string]interface{}{"path": "main.go"}}, ToolResult{Payload: "ok"}, nil)
	delta := conversation.cumulativeBytes - before
	toolMessage := conversation.messages[len(conversation.messages)-1]
	if toolMessage.Role != "tool" || toolMessage.ToolCallID != "c1" {
		t.Fatalf("native tool message is incomplete: %#v", toolMessage)
	}
	want = encodedModelMessage(toolMessage)
	if delta != want {
		t.Fatalf("native tool result cumulative delta = %d, want %d (textual snapshot must not double-count)", delta, want)
	}

	// strict-JSON tool result（CallID == ""）：没有 native tool message，只有
	// 一条 pending textual observation 计入 formatted block；payload 不重复。
	messagesBefore := len(conversation.messages)
	before = conversation.cumulativeBytes
	conversation.AppendToolResult(RequestTool{ToolName: "repository.read_file", Parameters: map[string]interface{}{"path": "other.go"}}, ToolResult{Payload: "strict payload"}, nil)
	if len(conversation.messages) != messagesBefore {
		t.Fatalf("strict-JSON tool result created a native message: %d != %d", len(conversation.messages), messagesBefore)
	}
	strictPending := conversation.pending[len(conversation.pending)-1]
	if strictPending.kind != "tool_observation" {
		t.Fatalf("strict-JSON pending item kind = %q, want tool_observation", strictPending.kind)
	}
	want = len(formatConversationItem(strictPending))
	if delta := conversation.cumulativeBytes - before; delta != want {
		t.Fatalf("strict-JSON tool result cumulative delta = %d, want pending block bytes %d", delta, want)
	}
}

func TestConversationLargeToolObservationIdentityTracksCompleteToolObservations(t *testing.T) {
	conversation := NewAgentConversation("boot")
	if sequence, bytes := conversation.LargeToolObservation(); sequence != 0 || bytes != 0 {
		t.Fatalf("large tool observation on empty conversation = %d/%d, want 0/0", sequence, bytes)
	}
	// protocol 观察不是工具观察，不建立大观察身份。
	conversation.AppendProtocolError(domain.RunStateDiagnosing)
	if sequence, _ := conversation.LargeToolObservation(); sequence != 0 {
		t.Fatalf("protocol observation created a tool observation identity: %d", sequence)
	}
	// strict-JSON 大观察建立身份并记录块字节。
	conversation.AppendToolResult(RequestTool{ToolName: "repository.read_file", Parameters: map[string]interface{}{"path": "main.go"}}, ToolResult{Payload: strings.Repeat("x", 50000)}, nil)
	bigSequence, bigBytes := conversation.LargeToolObservation()
	if bigSequence == 0 || bigBytes < int(toolOutputPressureBytes) {
		t.Fatalf("large strict-JSON observation not recorded: seq=%d bytes=%d", bigSequence, bigBytes)
	}
	// 后续小工具观察与 protocol 修正都不覆盖大观察身份（T2 consumed 水位的
	// 基础：被 checkpoint 覆盖的大观察不会因后续小观察重新成为“最新”）。
	conversation.AppendToolResult(RequestTool{ToolName: "repository.read_file", Parameters: map[string]interface{}{"path": "main.go"}}, ToolResult{Payload: "tiny"}, nil)
	conversation.AppendProtocolError(domain.RunStateDiagnosing)
	if sequence, bytes := conversation.LargeToolObservation(); sequence != bigSequence || bytes != bigBytes {
		t.Fatalf("small observation replaced large identity: %d/%d != %d/%d", sequence, bytes, bigSequence, bigBytes)
	}
	// 第二条大观察覆盖身份，sequence 单调递增。
	conversation.AppendToolResult(RequestTool{ToolName: "repository.read_file", Parameters: map[string]interface{}{"path": "other.go"}}, ToolResult{Payload: strings.Repeat("y", 50000)}, nil)
	secondSequence, secondBytes := conversation.LargeToolObservation()
	if secondSequence <= bigSequence || secondBytes < int(toolOutputPressureBytes) {
		t.Fatalf("second large observation did not advance identity: %d/%d vs %d/%d", secondSequence, secondBytes, bigSequence, bigBytes)
	}
}

func TestConversationCumulativeBytesSurvivesNativeHistoryEviction(t *testing.T) {
	// provider-native 历史超过 256 KiB 编码上限后 History() 按完整 user 组逐出
	// 最旧内容；累计压力账本不能因该丢失而回落，否则自动 byte-threshold 触发
	// 会在 provider-native 内容被逐出后失去 churn 追踪（D2/R13）。
	conversation := NewAgentConversation("boot")
	for turn := 1; turn <= 8; turn++ {
		conversation.RecordModelTurn(strings.Repeat("u", 60<<10), domain.ModelResult{ToolCalls: []domain.ToolCall{{
			ID: fmt.Sprintf("c%d", turn), Name: "repository.read_file", Arguments: map[string]interface{}{"path": "main.go"},
		}}})
		conversation.AppendToolResult(RequestTool{ToolName: "repository.read_file", CallID: fmt.Sprintf("c%d", turn), Parameters: map[string]interface{}{"path": "main.go"}}, ToolResult{Payload: strings.Repeat("t", 60<<10)}, nil)
		if int64(conversation.cumulativeBytes) > maxConversationBytes*3 {
			break
		}
	}
	storedBefore := encodedConversationBytes(conversation.messages)
	if storedBefore <= maxConversationBytes {
		t.Fatalf("precondition: stored history (%d bytes) must exceed the native bound", storedBefore)
	}
	history := conversation.History()
	if got := encodedConversationBytes(history); got > maxConversationBytes {
		t.Fatalf("bounded history = %d, want at most %d", got, maxConversationBytes)
	}
	// History() 只按完整组裁剪返回值，绝不修改存储的 messages（provider-native
	// 丢失是视图层行为），因此累计压力账本（每条 native message 在 append 时
	// 计一次）不受 History 逐出影响：ledger 在调用前后保持不变。
	if stored := encodedConversationBytes(conversation.messages); stored != storedBefore {
		t.Fatalf("History() mutated stored messages: %d != %d", stored, storedBefore)
	}
	ledgerBefore := conversation.CumulativeModelVisibleBytes()
	if total := conversation.CumulativeModelVisibleBytes(); total <= len("boot") {
		t.Fatalf("cumulative ledger did not record native content: %d", total)
	}
	// 继续追加 native 轮次时 ledger 单调增长（对逐出后的新内容仍有 churn 追踪）。
	conversation.RecordModelTurn(strings.Repeat("w", 10<<10), domain.ModelResult{ToolCalls: []domain.ToolCall{{
		ID: "c-late", Name: "repository.read_file", Arguments: map[string]interface{}{"path": "main.go"},
	}}})
	conversation.AppendToolResult(RequestTool{ToolName: "repository.read_file", CallID: "c-late", Parameters: map[string]interface{}{"path": "main.go"}}, ToolResult{Payload: strings.Repeat("s", 10<<10)}, nil)
	if total := conversation.CumulativeModelVisibleBytes(); total <= ledgerBefore {
		t.Fatalf("cumulative ledger regressed after History eviction: %d <= %d", total, ledgerBefore)
	}
}
