package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"mendry/backend/internal/modules/remediation/application"
	"mendry/backend/internal/modules/remediation/domain"
)

type interruptedTerminalCheckpoint struct {
	*fakeCheckpointStore
	entered   chan struct{}
	cancelled chan struct{}
	release   chan struct{}
}

func (s *interruptedTerminalCheckpoint) AppendCheckpoint(ctx context.Context, runID string, checkpoint domain.WorkingMemoryCheckpointV1) (domain.CheckpointSnapshot, error) {
	if len(s.appends) > 0 {
		close(s.entered)
		<-ctx.Done()
		close(s.cancelled)
		<-s.release
		// Simulate a write already committed at the cancellation boundary.
		// The subsequent terminal state mutation must still be rejected.
	}
	return s.fakeCheckpointStore.AppendCheckpoint(ctx, runID, checkpoint)
}

func TestRecoveryShutdownDuringTerminalCheckpointDoesNotCommitTerminalState(t *testing.T) {
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	checkpoints := &interruptedTerminalCheckpoint{
		fakeCheckpointStore: &fakeCheckpointStore{runStore: store},
		entered:             make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{}),
	}
	coord := newCoordinator(store, &fakeRepoPort{}, &fakeEvidencePort{}, &scriptedModel{responses: []string{diagnosisEnvelope("external_dependency")}})
	coord.SetCheckpointStore(checkpoints)
	root, shutdown := context.WithCancel(context.Background())
	defer shutdown()
	executor, err := application.NewRunExecutor(root, &restartWorkerLocks{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	coord.SetRunExecutor(executor)
	done := make(chan error, 1)
	go func() {
		_, err := coord.Start(context.Background(), domain.NewRun{IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123"})
		done <- err
	}()
	select {
	case <-checkpoints.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("terminal checkpoint did not start")
	}
	shutdown()
	select {
	case <-checkpoints.cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("terminal checkpoint ignored execution shutdown")
	}
	close(checkpoints.release)
	select {
	case err := <-done:
		if !errors.Is(err, application.ErrRunExecutionInterrupted) {
			t.Fatalf("error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("execution did not stop")
	}
	if store.state != domain.RunStateDiagnosing {
		t.Fatalf("terminal state committed during shutdown: %s", store.state)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := executor.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
