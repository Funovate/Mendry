package application

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"fixthe/backend/internal/modules/remediation/domain"
)

func TestProtocolCorrectionForConfidenceTypeMismatch(t *testing.T) {
	raw := `{"schemaVersion":"v1","kind":"diagnosis","diagnosis":{"fixability":"insufficient_evidence","confidence":"low","causalReasoning":"need evidence","evidenceCitations":[]}}`
	_, cause := DecodeEnvelope(raw)
	if cause == nil {
		t.Fatal("DecodeEnvelope() unexpectedly accepted string confidence")
	}
	var typeErr *json.UnmarshalTypeError
	if !errors.As(cause, &typeErr) {
		t.Fatalf("DecodeEnvelope() error = %v, want JSON type error", cause)
	}

	correction := ProtocolCorrectionFor(domain.RunStateDiagnosing, cause)
	if correction.Code != protocolCorrectionConfidence {
		t.Fatalf("correction code = %q, want %q", correction.Code, protocolCorrectionConfidence)
	}
	if correction.Path != protocolCorrectionConfidencePath || correction.ExpectedField != "number" {
		t.Fatalf("correction location = %#v", correction)
	}

	conversation := NewAgentConversation("bootstrap")
	conversation.AppendProtocolError(domain.RunStateDiagnosing, correction)
	text := conversation.ContextText()
	if !strings.Contains(text, "invalid_confidence") || !strings.Contains(text, "JSON number between 0 and 1") {
		t.Fatalf("conversation missing confidence correction: %s", text)
	}
	if strings.Contains(text, typeErr.Error()) {
		t.Fatalf("conversation leaked decoder details: %s", text)
	}
}
