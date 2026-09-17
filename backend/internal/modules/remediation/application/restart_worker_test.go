package application_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"mendry/backend/internal/modules/remediation/application"
	"mendry/backend/internal/modules/remediation/domain"
)

type restartWorkerLocks struct {
	mu   sync.Mutex
	held map[string]bool
}
type restartWorkerLease struct {
	locks *restartWorkerLocks
	id    string
}

func (s *restartWorkerLocks) TryAcquireRun(_ context.Context, id string) (domain.RunExecutionLease, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.held == nil {
		s.held = map[string]bool{}
	}
	if s.held[id] {
		return nil, false, nil
	}
	s.held[id] = true
	return &restartWorkerLease{s, id}, true, nil
}
func (*restartWorkerLocks) ListRecoverableRunIDs(context.Context, string, int) ([]string, error) {
	return nil, nil
}
func (*restartWorkerLease) Check(context.Context) error { return nil }
func (l *restartWorkerLease) Release(context.Context) error {
	l.locks.mu.Lock()
	defer l.locks.mu.Unlock()
	delete(l.locks.held, l.id)
	return nil
}
func attachRestartExecutor(t *testing.T, c *application.RemediationCoordinator, locks *restartWorkerLocks) *application.RunExecutor {
	t.Helper()
	e, err := application.NewRunExecutor(context.Background(), locks, 2)
	if err != nil {
		t.Fatal(err)
	}
	c.SetRunExecutor(e)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := e.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return e
}

func TestRecoveryWorkerResumesAfterProcessShutdown(t *testing.T) {
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	checkpoints := &fakeCheckpointStore{runStore: store}
	blocked := &blockingContinuationModel{entered: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{})}
	old := newCoordinator(store, &fakeRepoPort{}, &fakeEvidencePort{}, blocked)
	old.SetCheckpointStore(checkpoints)
	locks := &restartWorkerLocks{}
	executor := attachRestartExecutor(t, old, locks)
	done := make(chan error, 1)
	go func() {
		_, err := old.Start(context.Background(), domain.NewRun{IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123", TriggerReason: application.TriggerReasonManual})
		done <- err
	}()
	select {
	case <-blocked.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("model did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := executor.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, application.ErrRunExecutionInterrupted) {
		t.Fatalf("shutdown error=%v", err)
	}
	if store.state != domain.RunStateDiagnosing {
		t.Fatalf("shutdown persisted terminal state %s", store.state)
	}
	if len(checkpoints.appends) == 0 {
		t.Fatal("no durable checkpoint")
	}
	model := &scriptedModel{responses: []string{diagnosisEnvelope("external_dependency")}}
	fresh := newCoordinator(store, &fakeRepoPort{}, &fakeEvidencePort{}, model)
	fresh.SetCheckpointStore(checkpoints)
	attachRestartExecutor(t, fresh, locks)
	run, err := fresh.Recover(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.RunID != "run-1" || run.AttemptNumber != 1 || run.State != domain.RunStateCompletedNonCode {
		t.Fatalf("recovered run=%+v", run)
	}
	if model.calls != 1 || !strings.Contains(model.turns[0].UserMessage, "working_memory_reconstruction") {
		t.Fatal("checkpoint was not used to resume model")
	}
}

func TestRecoveryWorkerRebuildsQueuedAndPreCheckpointContinuation(t *testing.T) {
	for _, state := range []domain.RunState{domain.RunStateQueued, domain.RunStatePreparingContext} {
		t.Run(string(state), func(t *testing.T) {
			predecessor := eligibleAutomaticAggregate(domain.RunStateFailed, 1, true)
			predecessor.Decisions = []domain.Decision{{Fixability: domain.FixabilityCodeFixable, Confidence: 0.9, CausalReasoning: "durable predecessor diagnosis", EvidenceCitations: []string{"evidence-direct"}, RecommendedNextAction: "prepare a repair plan"}}
			store := &continuationStore{fakeRunStore: newFakeRunStore(), predecessor: predecessor, childMode: domain.AgentLoopModeResilientV1}
			in := domain.NextAttempt{ContinuationOfRunID: predecessor.Run.RunID, SeriesID: predecessor.Run.SeriesID, IncidentID: predecessor.Run.IncidentID, LifecycleGeneration: predecessor.Run.LifecycleGeneration, DeployedCommit: predecessor.Run.DeployedCommit, ContextVersion: predecessor.Run.ContextVersion, ExpectedPreviousVersion: predecessor.Run.Version, Origin: domain.TriggerOriginManualContinue, TriggerReason: domain.TriggerOriginManualContinue, ContinuationReason: "operator requested continuation"}
			child, err := store.CreateNextAttempt(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			store.childInput = domain.NextAttempt{} // Recovery must not call CreateNextAttempt again.
			store.created.ContinuationReason = in.ContinuationReason
			store.state = state
			store.created.State = state
			model := &scriptedModel{responses: []string{planEnvelope()}}
			coord := application.NewRemediationCoordinatorWithReview(store, &fakeRepoPort{}, &fakeEvidencePort{}, model, nil, store, store)
			coord.SetCheckpointStore(&fakeCheckpointStore{runStore: store.fakeRunStore})
			attachRestartExecutor(t, coord, &restartWorkerLocks{})
			run, err := coord.Recover(context.Background(), child.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if run.RunID != child.RunID || run.AttemptNumber != child.AttemptNumber || run.State != domain.RunStateDiagnosisReadyForReview {
				t.Fatalf("run=%+v", run)
			}
			if store.childInput.ContinuationOfRunID != "" {
				t.Fatal("recovery created another attempt")
			}
			if model.calls != 1 || !strings.Contains(model.turns[0].UserMessage, "durable predecessor diagnosis") {
				t.Fatalf("planning continuation context lost: calls=%d turns=%+v", model.calls, model.turns)
			}
		})
	}
}

func TestRecoveryWorkerReusesDurableLifecycleEffects(t *testing.T) {
	fixture := buildRestartMatrixFixture(t)
	checkpoint := restartCheckpoint(t, fixture.checkpoints.appends, "publishing")
	seedRestartWindow(t, fixture, checkpoint, domain.RunStatePublishing, false, domain.Effect{})
	effects := seedRestartEffects(t, fixture.effects, restartPublicationSucceeded)
	coord, model, _, workspace, validation, publication := freshRestartCoordinator(fixture.store, fixture.checkpoints, effects, nil)
	attachRestartExecutor(t, coord, &restartWorkerLocks{})
	run, err := coord.Recover(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.State != domain.RunStateAwaitingHumanReview {
		t.Fatalf("state=%s", run.State)
	}
	if model.calls != 0 || workspace.ensures != 0 || workspace.patches != 0 || validation.calls != 0 || publication.calls != 0 {
		t.Fatal("recovery repeated durable effects")
	}
}

func TestRecoveryWorkerLegacyInterruptionAndReviewBoundary(t *testing.T) {
	for _, state := range []domain.RunState{domain.RunStateDiagnosing, domain.RunStateDiagnosisReadyForReview, domain.RunStateAwaitingHumanReview} {
		t.Run(string(state), func(t *testing.T) {
			store := newFakeRunStore()
			_, err := store.CreateSeriesAndRun(context.Background(), domain.NewRun{IncidentID: testIncidentUUID, LifecycleGeneration: 1})
			if err != nil {
				t.Fatal(err)
			}
			store.state = state
			store.created.State = state
			model := &scriptedModel{}
			coord := newCoordinator(store, &fakeRepoPort{}, &fakeEvidencePort{}, model)
			attachRestartExecutor(t, coord, &restartWorkerLocks{})
			run, err := coord.Recover(context.Background(), "run-1")
			if err != nil {
				t.Fatal(err)
			}
			want := state
			if state == domain.RunStateDiagnosing {
				want = domain.RunStateFailed
			}
			if run.State != want || model.calls != 0 {
				t.Fatalf("state=%s model=%d", run.State, model.calls)
			}
			if state == domain.RunStateDiagnosing && (len(store.effects) != 1 || store.effects[0].TerminalReason != "recovery_checkpoint_unavailable") {
				t.Fatalf("effects=%+v", store.effects)
			}
		})
	}
}
