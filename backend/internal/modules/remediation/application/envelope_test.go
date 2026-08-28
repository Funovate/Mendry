package application

import (
	"errors"
	"strings"
	"testing"

	"fixthe/backend/internal/modules/remediation/domain"
)

func TestDecodeEnvelope_ValidRequestTool(t *testing.T) {
	raw := `{
		"schemaVersion": "v1",
		"kind": "requestTool",
		"requestTool": {
			"toolName": "repository.read_file",
			"parameters": {"path": "main.go"}
		}
	}`
	env, err := DecodeEnvelope(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.Kind != "requestTool" {
		t.Errorf("expected kind requestTool, got %s", env.Kind)
	}
	if env.RequestTool.ToolName != "repository.read_file" {
		t.Errorf("expected toolName repository.read_file, got %s", env.RequestTool.ToolName)
	}
}

func TestDecodeEnvelope_ValidDiagnosis(t *testing.T) {
	raw := `{
		"schemaVersion": "v1",
		"kind": "diagnosis",
		"diagnosis": {
			"fixability": "code_fixable",
			"confidence": 0.85,
			"causalReasoning": "NPE in handler",
			"contradictions": [],
			"missingEvidence": [],
			"evidenceCitations": ["ev-123"],
			"recommendedNextAction": "Plan patch"
		}
	}`
	env, err := DecodeEnvelope(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.Kind != "diagnosis" {
		t.Errorf("expected kind diagnosis, got %s", env.Kind)
	}
	if env.Diagnosis.Fixability != "code_fixable" {
		t.Errorf("expected fixability code_fixable, got %s", env.Diagnosis.Fixability)
	}
}

func TestDecodeEnvelope_StructuredEvidenceDiagnosisFixture(t *testing.T) {
	raw := `{"schemaVersion":"v1","kind":"diagnosis","diagnosis":{` +
		`"fixability":"configuration","confidence":0.9,"causalReasoning":"root cause",` +
		`"contradictions":[],"missingEvidence":[],"evidenceCitations":["ev-1"],` +
		`"recommendedNextAction":"next","alertQuality":"enriched",` +
		`"causalClosure":{"explainsOriginalSymptom":true,"explanation":"root cause explains the alert"}}}`
	if _, err := DecodeEnvelope(raw); err != nil {
		t.Fatalf("DecodeEnvelope() error = %v", err)
	}
}

func TestDecodeEnvelope_EvidenceRefReturnsTypedStrictViolation(t *testing.T) {
	raw := `{"schemaVersion":"v1","kind":"diagnosis","diagnosis":{` +
		`"fixability":"insufficient_evidence","confidence":0.2,"causalReasoning":"need evidence",` +
		`"contradictions":[],"missingEvidence":[],"evidenceCitations":[{"evidenceRef":"ev-1","secretValue":"do-not-replay"}],` +
		`"recommendedNextAction":"collect"}}`

	_, err := DecodeEnvelope(raw)
	if err == nil {
		t.Fatal("expected evidenceRef to be rejected")
	}
	var fieldErr *domain.EvidenceCitationFieldError
	if !errors.As(err, &fieldErr) || fieldErr.Field != "evidenceRef" {
		t.Fatalf("error = %v, typed field error = %#v", err, fieldErr)
	}
	if strings.Contains(err.Error(), "do-not-replay") {
		t.Fatalf("decoder error leaked an arbitrary field value: %v", err)
	}
}

func TestDecodeEnvelope_AcceptsCanonicalEvidenceCitationObject(t *testing.T) {
	raw := `{"schemaVersion":"v1","kind":"diagnosis","diagnosis":{` +
		`"fixability":"insufficient_evidence","confidence":0.2,"causalReasoning":"need evidence",` +
		`"contradictions":[],"missingEvidence":[],"evidenceCitations":[{"evidenceId":"ev-1","classification":"contextual"}],` +
		`"recommendedNextAction":"collect"}}`

	env, err := DecodeEnvelope(raw)
	if err != nil {
		t.Fatalf("DecodeEnvelope() error = %v", err)
	}
	if got := env.Diagnosis.EvidenceCitations[0]; got.EvidenceID != "ev-1" || got.Classification != domain.EvidenceContextual {
		t.Fatalf("citation = %#v", got)
	}
}

func TestDecodeEnvelope_ValidPlanCandidates(t *testing.T) {
	raw := `{
		"schemaVersion": "v1",
		"kind": "planCandidates",
		"planCandidates": {
			"candidates": [
				{
					"planId": "plan-1",
					"evidenceRefs": ["ev-1"],
					"affectedFiles": ["main.go"],
					"intendedBehavior": "Add null check",
					"risk": "ordinary",
					"rollbackStrategy": "Revert commit"
				}
			],
			"recommendedId": "plan-1",
			"rationale": "Simplest fix",
			"suggestedDiff": "diff --git a/main.go"
		}
	}`
	env, err := DecodeEnvelope(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.Kind != "planCandidates" {
		t.Errorf("expected kind planCandidates, got %s", env.Kind)
	}
}

func TestDecodeEnvelope_ValidStop(t *testing.T) {
	raw := `{
		"schemaVersion": "v1",
		"kind": "stop",
		"stop": {
			"reason":                "Insufficient context",
			"recommendedNextAction": "Ask an operator to collect the missing runtime evidence"
		}
	}`
	env, err := DecodeEnvelope(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.Kind != "stop" {
		t.Errorf("expected kind stop, got %s", env.Kind)
	}
}

func TestDecodeEnvelope_StopRequiresRecommendedNextAction(t *testing.T) {
	for _, raw := range []string{
		`{"schemaVersion":"v1","kind":"stop","stop":{"reason":"cannot proceed"}}`,
		`{"schemaVersion":"v1","kind":"stop","stop":{"reason":"cannot proceed","recommendedNextAction":"   "}}`,
		`{"schemaVersion":"v1","kind":"stop","stop":null}`,
	} {
		_, err := DecodeEnvelope(raw)
		if err == nil || !strings.Contains(err.Error(), "stop: recommendedNextAction is required") {
			t.Fatalf("DecodeEnvelope() error = %v, want required stop recommendation", err)
		}
	}
}

func TestAgentEnvelopeSchemaRequiresStopRecommendation(t *testing.T) {
	schema := AgentEnvelopeSchema()
	properties, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("schema properties = %#v", schema["properties"])
	}
	stop, ok := properties["stop"].(map[string]interface{})
	if !ok {
		t.Fatalf("stop schema = %#v", properties["stop"])
	}
	required, ok := stop["required"].([]string)
	if !ok || len(required) != 2 || required[0] != "reason" || required[1] != "recommendedNextAction" {
		t.Fatalf("stop required fields = %#v", stop["required"])
	}
	stopProperties, ok := stop["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("stop properties = %#v", stop["properties"])
	}
	recommendation, ok := stopProperties["recommendedNextAction"].(map[string]interface{})
	if !ok || recommendation["minLength"] != 1 {
		t.Fatalf("stop recommendation schema = %#v", stopProperties["recommendedNextAction"])
	}
}

func TestDecodeEnvelope_UnknownSchemaVersion(t *testing.T) {
	raw := `{
		"schemaVersion": "v999",
		"kind": "requestTool",
		"requestTool": {"toolName": "x", "parameters": {}}
	}`
	_, err := DecodeEnvelope(raw)
	if err == nil {
		t.Fatal("expected error for unknown schema version")
	}
	if !strings.Contains(err.Error(), "schema version") {
		t.Errorf("expected schema version error, got: %v", err)
	}
}

func TestDecodeEnvelope_MultipleKinds(t *testing.T) {
	raw := `{
		"schemaVersion": "v1",
		"kind": "requestTool",
		"requestTool": {"toolName": "x", "parameters": {}},
		"diagnosis": {"fixability": "code_fixable", "confidence": 0.9, "causalReasoning": "x", "contradictions": [], "missingEvidence": [], "evidenceCitations": [], "recommendedNextAction": "x"}
	}`
	_, err := DecodeEnvelope(raw)
	if err == nil {
		t.Fatal("expected error for multiple kinds")
	}
	if !strings.Contains(err.Error(), "multiple kinds") {
		t.Errorf("expected multiple kinds error, got: %v", err)
	}
}

func TestDecodeEnvelope_NoKind(t *testing.T) {
	raw := `{
		"schemaVersion": "v1",
		"kind": "requestTool"
	}`
	_, err := DecodeEnvelope(raw)
	if err == nil {
		t.Fatal("expected error for no kind payload")
	}
	if !strings.Contains(err.Error(), "exactly one kind") {
		t.Errorf("expected exactly one kind error, got: %v", err)
	}
}

func TestDecodeEnvelope_UnknownFixability(t *testing.T) {
	raw := `{
		"schemaVersion": "v1",
		"kind": "diagnosis",
		"diagnosis": {
			"fixability": "unknown_class",
			"confidence": 0.5,
			"causalReasoning": "test",
			"contradictions": [],
			"missingEvidence": [],
			"evidenceCitations": [],
			"recommendedNextAction": "test"
		}
	}`
	_, err := DecodeEnvelope(raw)
	if err == nil {
		t.Fatal("expected error for unknown fixability")
	}
	if !strings.Contains(err.Error(), "unknown fixability") {
		t.Errorf("expected unknown fixability error, got: %v", err)
	}
}

func TestDecodeEnvelope_InvalidConfidence(t *testing.T) {
	raw := `{
		"schemaVersion": "v1",
		"kind": "diagnosis",
		"diagnosis": {
			"fixability": "code_fixable",
			"confidence": 1.5,
			"causalReasoning": "test",
			"contradictions": [],
			"missingEvidence": [],
			"evidenceCitations": [],
			"recommendedNextAction": "test"
		}
	}`
	_, err := DecodeEnvelope(raw)
	if err == nil {
		t.Fatal("expected error for invalid confidence")
	}
	if !strings.Contains(err.Error(), "confidence") {
		t.Errorf("expected confidence error, got: %v", err)
	}
}

func TestDecodeEnvelope_ReservedKind(t *testing.T) {
	raw := `{
		"schemaVersion": "v1",
		"kind": "patchComplete",
		"patchComplete": {
			"success": true,
			"message": "Done"
		}
	}`
	_, err := DecodeEnvelope(raw)
	if err == nil {
		t.Fatal("expected error for reserved kind")
	}
	if !strings.Contains(err.Error(), "reserved") {
		t.Errorf("expected reserved kind error, got: %v", err)
	}
}

func TestDecodeEnvelope_MissingRecommendedId(t *testing.T) {
	raw := `{
		"schemaVersion": "v1",
		"kind": "planCandidates",
		"planCandidates": {
			"candidates": [
				{"planId": "p1", "evidenceRefs": ["ev-1"], "affectedFiles": ["main.go"], "intendedBehavior": "x", "risk": "ordinary", "rollbackStrategy": "x"}
			],
			"recommendedId": "p2",
			"rationale": "x",
			"suggestedDiff": "x"
		}
	}`
	_, err := DecodeEnvelope(raw)
	if err == nil {
		t.Fatal("expected error for missing recommendedId")
	}
	if !strings.Contains(err.Error(), "not found in candidates") {
		t.Errorf("expected not found error, got: %v", err)
	}
}

func TestDecodeEnvelope_UnknownRisk(t *testing.T) {
	raw := `{
		"schemaVersion": "v1",
		"kind": "planCandidates",
		"planCandidates": {
			"candidates": [
				{"planId": "p1", "evidenceRefs": [], "affectedFiles": [], "intendedBehavior": "x", "risk": "unknown_risk", "rollbackStrategy": "x"}
			],
			"recommendedId": "p1",
			"rationale": "x",
			"suggestedDiff": "x"
		}
	}`
	_, err := DecodeEnvelope(raw)
	if err == nil {
		t.Fatal("expected error for unknown risk")
	}
	if !strings.Contains(err.Error(), "unknown risk") {
		t.Errorf("expected unknown risk error, got: %v", err)
	}
}

func TestDecodeEnvelope_RejectsUnknownFields(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{
			name: "top level",
			raw: `{
				"schemaVersion": "v1",
				"kind": "stop",
				"stop": {"reason": "done"},
				"extra": true
			}`,
		},
		{
			name: "nested payload",
			raw: `{
				"schemaVersion": "v1",
				"kind": "diagnosis",
				"diagnosis": {
					"fixability": "code_fixable",
					"confidence": 0.5,
					"causalReasoning": "test",
					"contradictions": [],
					"missingEvidence": [],
					"evidenceCitations": [],
					"recommendedNextAction": "test",
					"rawProviderResponse": "must not be accepted"
				}
			}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeEnvelope(tc.raw)
			if err == nil {
				t.Fatal("expected error for unknown field")
			}
			if !strings.Contains(err.Error(), "unknown field") {
				t.Errorf("expected unknown field error, got: %v", err)
			}
		})
	}
}

func TestDecodeEnvelope_RejectsMultipleJSONValues(t *testing.T) {
	raw := `{"schemaVersion":"v1","kind":"stop","stop":{"reason":"done"}}{}`
	_, err := DecodeEnvelope(raw)
	if err == nil {
		t.Fatal("expected error for multiple JSON values")
	}
	if !strings.Contains(err.Error(), "multiple JSON values") {
		t.Errorf("expected multiple JSON values error, got: %v", err)
	}
}
