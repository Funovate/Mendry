package application_test

import (
	"strings"
	"testing"

	"mendry/backend/internal/modules/remediation/domain"
)

func TestCoordinator_ConfidenceTypeMismatchRetriesWithSpecificCorrection(t *testing.T) {
	model := &scriptedModel{responses: []string{
		`{"schemaVersion":"v1","kind":"diagnosis","diagnosis":{"fixability":"insufficient_evidence","confidence":"low","causalReasoning":"need evidence","evidenceCitations":[],"recommendedNextAction":"collect"}}`,
		diagnosisEnvelope("external_dependency"),
	}}
	store, run, err := runStart(t, model)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.State != domain.RunStateCompletedNonCode || store.state != domain.RunStateCompletedNonCode {
		t.Fatalf("final state = %s/%s, want completed_non_code", run.State, store.state)
	}
	if model.calls != 2 || len(model.turns) != 2 {
		t.Fatalf("model calls/turns = %d/%d, want 2/2", model.calls, len(model.turns))
	}
	second := model.turns[1].UserMessage
	for _, want := range []string{
		"invalid_confidence",
		"diagnosis.confidence",
		"JSON number between 0 and 1",
	} {
		if !strings.Contains(second, want) {
			t.Fatalf("retry prompt missing %q: %s", want, second)
		}
	}
	if strings.Contains(second, "cannot unmarshal") || strings.Contains(second, "DiagnosisOutput") {
		t.Fatalf("retry prompt leaked decoder details: %s", second)
	}
}
