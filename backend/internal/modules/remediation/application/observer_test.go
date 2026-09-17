package application_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"mendry/backend/internal/modules/remediation/application"
	"mendry/backend/internal/modules/remediation/domain"
)

type recordingRunObserver struct {
	events    []string
	sequences []int64
}

func (o *recordingRunObserver) RunStarted(_ context.Context, rec application.RunStartedObservation) {
	o.events = append(o.events, "run:"+string(rec.Phase))
}

func (o *recordingRunObserver) StateTransitioned(_ context.Context, rec application.StateTransitionObservation) {
	o.events = append(o.events, fmt.Sprintf("state:%s>%s", rec.From, rec.To))
}

func (o *recordingRunObserver) ContextCompleted(_ context.Context, rec application.ContextObservation) {
	o.events = append(o.events, "context:"+rec.Operation+":"+rec.Outcome)
}

func (o *recordingRunObserver) ModelTurnCompleted(_ context.Context, rec application.ModelTurnObservation) {
	o.events = append(o.events, "model:"+string(rec.Phase)+":"+rec.Outcome)
	o.sequences = append(o.sequences, rec.Sequence)
}

func (o *recordingRunObserver) ToolCompleted(_ context.Context, rec application.ToolObservation) {
	o.events = append(o.events, "tool:"+rec.Tool+":"+rec.Outcome)
	o.sequences = append(o.sequences, rec.Sequence)
}

func (o *recordingRunObserver) RunCompleted(_ context.Context, rec application.RunCompletedObservation) {
	o.events = append(o.events, "completed:"+string(rec.TerminalState)+":"+rec.Outcome)
}

func TestCoordinatorObserverEmitsOrderedRunTrace(t *testing.T) {
	store := newFakeRunStore()
	observer := &recordingRunObserver{}
	model := &scriptedModel{responses: []string{
		requestToolEnvelope(application.ToolRepoReadFile, "main.go"),
		diagnosisEnvelope("code_fixable"),
		planEnvelope(),
	}}
	coordinator := application.NewRemediationCoordinatorWithObservedRuntime(
		store, &fakeRepoPort{}, &fakeEvidencePort{}, model, nil, nil, store, store, observer,
	)
	coordinator.SetEvidenceResolver(fakeEvidenceResolver{})
	run, err := coordinator.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 2, DeployedCommit: "abc123",
		Priority: "P2", TriggerReason: application.TriggerReasonAutomatic,
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if run.State != domain.RunStateDiagnosisReadyForReview {
		t.Fatalf("terminal state = %s", run.State)
	}
	wantSequences := []int64{1, 2, 3, 4}
	if !reflect.DeepEqual(observer.sequences, wantSequences) {
		t.Fatalf("sequences = %v, want %v", observer.sequences, wantSequences)
	}
	wantEvents := []string{
		"run:queued",
		"state:queued>preparing_context",
		"context:bootstrap.metadata:success",
		"state:preparing_context>diagnosing",
		"model:diagnosing:success",
		"state:diagnosing>diagnosing",
		"tool:repository.read_file:success",
		"state:diagnosing>diagnosing",
		"model:diagnosing:success",
		"state:diagnosing>planning",
		"model:planning:success",
		"state:planning>diagnosis_ready_for_review",
		"completed:diagnosis_ready_for_review:success",
	}
	if !reflect.DeepEqual(observer.events, wantEvents) {
		t.Fatalf("events =\n%v\nwant =\n%v", observer.events, wantEvents)
	}
}
