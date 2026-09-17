package application_test

import (
	"context"
	"testing"

	"mendry/backend/internal/modules/remediation/application"
	"mendry/backend/internal/modules/remediation/domain"
)

func TestRecoveryRechecksIncidentGenerationAfterCandidateScan(t *testing.T) {
	store := newFakeRunStore()
	run, err := store.CreateSeriesAndRun(context.Background(), domain.NewRun{IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "old"})
	if err != nil {
		t.Fatal(err)
	}
	model := &scriptedModel{}
	coord := application.NewRemediationCoordinatorWithLookup(store, &fakeRepoPort{}, &fakeEvidencePort{}, model,
		wiringLookup{identity: application.IncidentIdentity{ID: testIncidentUUID, LifecycleGeneration: 2, DeployedCommit: "new"}})
	attachRestartExecutor(t, coord, &restartWorkerLocks{})
	result, err := coord.Recover(context.Background(), run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != domain.RunStateQueued || len(store.transitions) != 0 || model.calls != 0 {
		t.Fatalf("obsolete candidate was executed: state=%s transitions=%v calls=%d", result.State, store.transitions, model.calls)
	}
}
