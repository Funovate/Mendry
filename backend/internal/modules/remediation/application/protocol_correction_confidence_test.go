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

func TestProtocolCorrectionForTimeAssessmentValidation(t *testing.T) {
	raw := `{"schemaVersion":"v1","kind":"diagnosis","diagnosis":{` +
		`"fixability":"insufficient_evidence","confidence":0.2,"causalReasoning":"need evidence",` +
		`"contradictions":[],"missingEvidence":[],"evidenceCitations":[],"recommendedNextAction":"review",` +
		`"timeAssessment":{"originalValues":[],"basis":"paired_epoch_values first: private-provider-detail","certainty":"low"}}}`
	_, cause := DecodeEnvelope(raw)
	if cause == nil || !errors.Is(cause, errInvalidDiagnosisTimeAssessment) {
		t.Fatalf("DecodeEnvelope() error = %v, want typed time assessment error", cause)
	}

	correction := ProtocolCorrectionFor(domain.RunStateDiagnosing, cause)
	if correction.Code != protocolCorrectionTimeAssessment || correction.Path != protocolCorrectionTimeAssessmentPath {
		t.Fatalf("correction = %#v", correction)
	}
	if correction.ExpectedField != "paired_epoch|explicit_offset|contextual_zone|unresolved" {
		t.Fatalf("correction expected field = %q", correction.ExpectedField)
	}
	for _, want := range []string{
		"invalid_time_assessment",
		"timeAssessment.basis must be exactly one of paired_epoch, explicit_offset, contextual_zone, unresolved",
		"never put explanation text in basis",
	} {
		conversation := NewAgentConversation("bootstrap")
		conversation.AppendProtocolError(domain.RunStateDiagnosing, correction)
		if text := conversation.ContextText(); !strings.Contains(text, want) {
			t.Fatalf("protocol correction missing %q: %s", want, text)
		}
	}
	if strings.Contains(correction.Message, "private-provider-detail") || strings.Contains(correction.Message, cause.Error()) {
		t.Fatalf("correction leaked validation details: %s", correction.Message)
	}
}

func TestProtocolCorrectionForObservedDiagnosisTypeMismatches(t *testing.T) {
	tests := []struct {
		name          string
		diagnosisJSON string
		wantCode      string
		wantPath      string
		wantField     string
	}{
		{
			name:          "fixability object",
			diagnosisJSON: `"fixability":{"classification":"insufficient_evidence","providerNote":"private-value"},"confidence":0.2,"sourceCoverage":[]`,
			wantCode:      protocolCorrectionFixability,
			wantPath:      protocolCorrectionFixabilityPath,
			wantField:     "string enum",
		},
		{
			name:          "source coverage object",
			diagnosisJSON: `"fixability":"insufficient_evidence","confidence":0.2,"sourceCoverage":{"status":"partial","providerNote":"private-value"}`,
			wantCode:      protocolCorrectionSourceCoverage,
			wantPath:      protocolCorrectionSourceCoveragePath,
			wantField:     "array",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := `{"schemaVersion":"v1","kind":"diagnosis","diagnosis":{` + tc.diagnosisJSON + `,"causalReasoning":"need evidence","contradictions":[],"missingEvidence":[],"evidenceCitations":[],"recommendedNextAction":"review"}}`
			_, cause := DecodeEnvelope(raw)
			if cause == nil {
				t.Fatal("DecodeEnvelope() unexpectedly accepted malformed diagnosis")
			}
			var typeErr *json.UnmarshalTypeError
			if !errors.As(cause, &typeErr) {
				t.Fatalf("DecodeEnvelope() error = %v, want JSON type error", cause)
			}

			correction := ProtocolCorrectionFor(domain.RunStateDiagnosing, cause)
			if correction.Code != tc.wantCode || correction.Path != tc.wantPath || correction.ExpectedField != tc.wantField {
				t.Fatalf("correction = %#v", correction)
			}
			for _, want := range []string{
				`{"schemaVersion":"v1","kind":"diagnosis"`,
				"fixability is a JSON string, never an object",
				"confidence is a number from 0 to 1",
				"sourceCoverage is a JSON array, never an object",
			} {
				if !strings.Contains(correction.Message, want) {
					t.Fatalf("correction missing %q: %s", want, correction.Message)
				}
			}
			for _, leaked := range []string{typeErr.Error(), "private-value", "providerNote"} {
				if strings.Contains(correction.Message, leaked) {
					t.Fatalf("correction leaked %q: %s", leaked, correction.Message)
				}
			}
		})
	}
}

func TestPlanningProtocolCorrectionsAreSpecificAndIncludeWireContract(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		cause    error
		wantCode string
		wantPath string
	}{
		{name: "empty candidates", raw: `{"schemaVersion":"v1","kind":"planCandidates","planCandidates":{"candidates":[],"recommendedId":"p1","rationale":"why","suggestedDiff":"diff"}}`, wantCode: protocolCorrectionPlanningCandidates, wantPath: "planCandidates.candidates"},
		{name: "missing recommended id", raw: `{"schemaVersion":"v1","kind":"planCandidates","planCandidates":{"candidates":[{"planId":"p1","risk":"ordinary"}],"rationale":"why","suggestedDiff":"diff"}}`, wantCode: protocolCorrectionPlanningRecommended, wantPath: "planCandidates.recommendedId"},
		{name: "missing suggested diff", raw: `{"schemaVersion":"v1","kind":"planCandidates","planCandidates":{"candidates":[{"planId":"p1","risk":"ordinary"}],"recommendedId":"p1","rationale":"why"}}`, wantCode: protocolCorrectionPlanningDiff, wantPath: "planCandidates.suggestedDiff"},
		{name: "missing candidate plan id", raw: `{"schemaVersion":"v1","kind":"planCandidates","planCandidates":{"candidates":[{"risk":"ordinary"}],"recommendedId":"p1","rationale":"why","suggestedDiff":"diff"}}`, wantCode: protocolCorrectionPlanningPlanID, wantPath: "planCandidates.candidates[].planId"},
		{name: "invalid risk", raw: `{"schemaVersion":"v1","kind":"planCandidates","planCandidates":{"candidates":[{"planId":"p1","risk":"private-risk-value"}],"recommendedId":"p1","rationale":"why","suggestedDiff":"diff"}}`, wantCode: protocolCorrectionPlanningRisk, wantPath: "planCandidates.candidates[].risk"},
		{name: "missing rationale", raw: `{"schemaVersion":"v1","kind":"planCandidates","planCandidates":{"candidates":[{"planId":"p1","evidenceRefs":["ev-1"],"affectedFiles":["main.go"],"intendedBehavior":"add nil check","risk":"ordinary","rollbackStrategy":"revert"}],"recommendedId":"p1","suggestedDiff":"diff"}}`, wantCode: protocolCorrectionPlanningRationale, wantPath: "planCandidates.rationale"},
		{name: "missing candidate evidence refs", raw: `{"schemaVersion":"v1","kind":"planCandidates","planCandidates":{"candidates":[{"planId":"p1","affectedFiles":["main.go"],"intendedBehavior":"add nil check","risk":"ordinary","rollbackStrategy":"revert"}],"recommendedId":"p1","rationale":"why","suggestedDiff":"diff"}}`, wantCode: protocolCorrectionPlanningEvidenceRefs, wantPath: "planCandidates.candidates[].evidenceRefs"},
		{name: "missing candidate affected files", raw: `{"schemaVersion":"v1","kind":"planCandidates","planCandidates":{"candidates":[{"planId":"p1","evidenceRefs":["ev-1"],"intendedBehavior":"add nil check","risk":"ordinary","rollbackStrategy":"revert"}],"recommendedId":"p1","rationale":"why","suggestedDiff":"diff"}}`, wantCode: protocolCorrectionPlanningAffectedFiles, wantPath: "planCandidates.candidates[].affectedFiles"},
		{name: "missing candidate intended behavior", raw: `{"schemaVersion":"v1","kind":"planCandidates","planCandidates":{"candidates":[{"planId":"p1","evidenceRefs":["ev-1"],"affectedFiles":["main.go"],"risk":"ordinary","rollbackStrategy":"revert"}],"recommendedId":"p1","rationale":"why","suggestedDiff":"diff"}}`, wantCode: protocolCorrectionPlanningBehavior, wantPath: "planCandidates.candidates[].intendedBehavior"},
		{name: "missing candidate rollback strategy", raw: `{"schemaVersion":"v1","kind":"planCandidates","planCandidates":{"candidates":[{"planId":"p1","evidenceRefs":["ev-1"],"affectedFiles":["main.go"],"intendedBehavior":"add nil check","risk":"ordinary"}],"recommendedId":"p1","rationale":"why","suggestedDiff":"diff"}}`, wantCode: protocolCorrectionPlanningRollback, wantPath: "planCandidates.candidates[].rollbackStrategy"},
		{name: "evidence refs wrong type", raw: `{"schemaVersion":"v1","kind":"planCandidates","planCandidates":{"candidates":[{"planId":"p1","evidenceRefs":"ev-1","affectedFiles":["main.go"],"intendedBehavior":"add nil check","risk":"ordinary","rollbackStrategy":"revert"}],"recommendedId":"p1","rationale":"why","suggestedDiff":"diff"}}`, wantCode: protocolCorrectionPlanningEvidenceRefs, wantPath: "planCandidates.candidates[].evidenceRefs"},
		{name: "recommended id not found", raw: `{"schemaVersion":"v1","kind":"planCandidates","planCandidates":{"candidates":[{"planId":"p1","evidenceRefs":["ev-1"],"affectedFiles":["main.go"],"intendedBehavior":"add nil check","risk":"ordinary","rollbackStrategy":"revert"}],"recommendedId":"private-plan-id","rationale":"why","suggestedDiff":"diff"}}`, wantCode: protocolCorrectionPlanningRecommended, wantPath: "planCandidates.recommendedId"},
		{name: "wrong schema", raw: `{"schemaVersion":"v2-private","kind":"planCandidates","planCandidates":{"candidates":[{"planId":"p1","risk":"ordinary"}],"recommendedId":"p1","rationale":"why","suggestedDiff":"diff"}}`, wantCode: protocolCorrectionPlanningSchema, wantPath: "schemaVersion"},
		{name: "wrong phase kind", cause: wrapEnvelopeError("validate agent envelope phase", validateEnvelopeForPhase(domain.RunStatePlanning, "diagnosis")), wantCode: protocolCorrectionPlanningKind, wantPath: "kind"},
		{name: "unknown field", raw: `{"schemaVersion":"v1","kind":"planCandidates","privateProviderField":"do-not-leak","planCandidates":{"candidates":[{"planId":"p1","risk":"ordinary"}],"recommendedId":"p1","rationale":"why","suggestedDiff":"diff"}}`, wantCode: protocolCorrectionPlanningShape, wantPath: "envelope"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cause := tc.cause
			if cause == nil {
				_, cause = DecodeEnvelope(tc.raw)
			}
			if cause == nil {
				t.Fatal("test input unexpectedly passed validation")
			}
			correction := ProtocolCorrectionFor(domain.RunStatePlanning, wrapEnvelopeError("planning response", cause))
			if correction.Code != tc.wantCode || correction.Path != tc.wantPath {
				t.Fatalf("correction = %#v", correction)
			}
			if !strings.Contains(correction.Message, planningWireContractInstruction) {
				t.Fatalf("correction omitted planning contract: %s", correction.Message)
			}
			conversation := NewAgentConversation("bootstrap")
			conversation.AppendProtocolError(domain.RunStatePlanning, correction)
			observed := conversation.ContextText()
			for _, want := range []string{tc.wantCode, tc.wantPath, "Planning wire contract:", "ordinary, high_risk, denied_control_plane"} {
				if !strings.Contains(observed, want) {
					t.Fatalf("model-visible correction missing %q: %s", want, observed)
				}
			}
			for _, leaked := range []string{"private-risk-value", "private-plan-id", "v2-private", "privateProviderField", "do-not-leak"} {
				if strings.Contains(correction.Message, leaked) || strings.Contains(observed, leaked) {
					t.Fatalf("correction leaked %q: %s", leaked, observed)
				}
			}
		})
	}
}

func TestPlanningPromptUsesCompleteWireContract(t *testing.T) {
	prompt := (&AgentEngine{}).buildPrompt(domain.RunStatePlanning)
	if !strings.Contains(prompt, planningWireContractInstruction) {
		t.Fatalf("planning prompt omitted shared contract: %s", prompt)
	}
	for _, want := range []string{
		`"schemaVersion":"v1"`, `"kind":"planCandidates"`, `"candidates"`,
		`"recommendedId"`, `"rationale"`, `"suggestedDiff"`,
		"ordinary, high_risk, denied_control_plane", "advertised read-only repository tools",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("planning prompt missing %q: %s", want, prompt)
		}
	}
}
