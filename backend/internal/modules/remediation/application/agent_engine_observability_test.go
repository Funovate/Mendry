package application_test

import (
	"context"
	"strings"
	"testing"

	"fixthe/backend/internal/modules/remediation/application"
	"fixthe/backend/internal/modules/remediation/domain"
)

func TestAgentEngineDecodeFailureObservationIncludesConcreteReason(t *testing.T) {
	model := &fixedModelResult{result: domain.ModelResult{
		Content:  `{"schemaVersion":"v1","kind":"diagnosis","type":"unexpected"}`,
		Provider: "openai", Model: "gpt-test",
	}}
	observer := &modelObservationRecorder{}
	engine := application.NewAgentEngine(model, nil)

	_, _, err := engine.TurnObservedWithConversationAndTools(
		context.Background(), application.RunIdentity{RunID: "run-1"}, observer, 7,
		domain.RunStateDiagnosing, "project-1", "bounded context", nil, nil,
	)
	if err == nil || !strings.Contains(err.Error(), `unknown field "type"`) {
		t.Fatalf("Turn() error = %v, want unknown field type", err)
	}
	if observer.model.Outcome != "failure" || observer.model.FailureClass != "decode" || observer.model.Sequence != 7 {
		t.Fatalf("model observation = %+v", observer.model)
	}
	if !strings.Contains(observer.model.ErrorMessage, `unknown field "type"`) {
		t.Fatalf("model diagnostic = %q", observer.model.ErrorMessage)
	}
}

type fixedModelResult struct {
	result domain.ModelResult
}

func (m *fixedModelResult) Complete(context.Context, domain.ModelTurn) (domain.ModelResult, error) {
	return m.result, nil
}

type modelObservationRecorder struct {
	model application.ModelTurnObservation
}

func (o *modelObservationRecorder) RunStarted(context.Context, application.RunStartedObservation) {}
func (o *modelObservationRecorder) StateTransitioned(context.Context, application.StateTransitionObservation) {
}
func (o *modelObservationRecorder) ContextCompleted(context.Context, application.ContextObservation) {
}
func (o *modelObservationRecorder) ModelTurnCompleted(_ context.Context, rec application.ModelTurnObservation) {
	o.model = rec
}
func (o *modelObservationRecorder) ToolCompleted(context.Context, application.ToolObservation) {}
func (o *modelObservationRecorder) RunCompleted(context.Context, application.RunCompletedObservation) {
}
