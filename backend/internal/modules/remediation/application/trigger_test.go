package application_test

import (
	"context"
	"errors"
	"testing"

	incidentapplication "mendry/backend/internal/modules/incidents/application"
	"mendry/backend/internal/modules/remediation/application"
	"mendry/backend/internal/modules/remediation/domain"
)

func TestTriggerEmitSkipsNonQualifyingAutomaticInfo(t *testing.T) {
	store := newFakeRunStore()
	trigger, err := application.NewTrigger(store, nil)
	if err != nil {
		t.Fatalf("NewTrigger() error = %v", err)
	}
	if err := trigger.Emit(context.Background(), incidentapplication.RemediationRequest{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
		Priority: "Info", Reason: application.TriggerReasonAutomatic,
	}); err != nil {
		t.Fatalf("Emit() error = %v", err)
	}
	if store.created != nil {
		t.Fatalf("Info automatic created a run: %#v", store.created)
	}
}

func TestTriggerEmitCreatesUUIDRootAndIsIdempotent(t *testing.T) {
	store := newFakeRunStore()
	trigger, err := application.NewTrigger(store, nil)
	if err != nil {
		t.Fatalf("NewTrigger() error = %v", err)
	}
	req := incidentapplication.RemediationRequest{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
		Priority: "P2", Reason: application.TriggerReasonAutomatic,
	}
	if err := trigger.Emit(context.Background(), req); err != nil {
		t.Fatalf("first Emit() error = %v", err)
	}
	first := *store.created
	if first.IncidentID != testIncidentUUID || first.IncidentID == "2049" {
		t.Fatalf("IncidentID = %q, want UUID", first.IncidentID)
	}
	if err := trigger.Emit(context.Background(), req); err != nil {
		t.Fatalf("second Emit() error = %v", err)
	}
	if store.created.RunID != first.RunID {
		t.Fatalf("duplicate root run: first=%s second=%s", first.RunID, store.created.RunID)
	}
}

func TestTriggerManualInfoCreatesRoot(t *testing.T) {
	store := newFakeRunStore()
	trigger, err := application.NewTrigger(store, nil)
	if err != nil {
		t.Fatalf("NewTrigger() error = %v", err)
	}
	run, err := trigger.Start(context.Background(), application.TriggerRequest{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
		Priority: "Info", Reason: application.TriggerReasonManual,
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if run.State != domain.RunStateQueued || run.IncidentID != testIncidentUUID {
		t.Fatalf("manual Info run = %#v", run)
	}
}

type failureReporter struct {
	request application.TriggerRequest
	err     error
}

func (f *failureReporter) Report(_ context.Context, request application.TriggerRequest, err error) {
	f.request, f.err = request, err
}

func TestTriggerReportsStoreFailure(t *testing.T) {
	store := newFakeRunStore()
	store.createErr = errors.New("store unavailable")
	reporter := &failureReporter{}
	trigger, err := application.NewTriggerWithReporter(store, nil, reporter)
	if err != nil {
		t.Fatalf("NewTriggerWithReporter() error = %v", err)
	}
	_, err = trigger.Start(context.Background(), application.TriggerRequest{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
		Priority: "P2", Reason: application.TriggerReasonAutomatic,
	})
	if err == nil || reporter.err == nil || reporter.request.IncidentID != testIncidentUUID {
		t.Fatalf("err=%v reporter=%#v", err, reporter)
	}
	if reporter.err.Error() != "create series and run: store unavailable" {
		t.Fatalf("reported error = %v", reporter.err)
	}
}
