package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	incidentapplication "mendry/backend/internal/modules/incidents/application"
	"mendry/backend/internal/modules/remediation/application"
	"mendry/backend/internal/modules/remediation/domain"
)

type automaticGateStore struct {
	*fakeRunStore
	mu         sync.Mutex
	latest     domain.RunAggregate
	latestErr  error
	nextErrs   []error
	nextInput  domain.NextAttempt
	nextCalls  int
	successful int
}

func (s *automaticGateStore) GetLatestForIncident(context.Context, string, int64, string) (domain.RunAggregate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.latestErr != nil {
		return domain.RunAggregate{}, s.latestErr
	}
	return s.latest, nil
}

func (s *automaticGateStore) CreateNextAttempt(_ context.Context, in domain.NextAttempt) (domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextCalls++
	s.nextInput = in
	var err error
	if len(s.nextErrs) > 0 {
		err = s.nextErrs[0]
		s.nextErrs = s.nextErrs[1:]
	}
	if err != nil {
		return domain.Run{}, err
	}
	s.successful++
	return domain.Run{
		RunID:               "run-next",
		SeriesID:            in.SeriesID,
		IncidentID:          in.IncidentID,
		LifecycleGeneration: in.LifecycleGeneration,
		DeployedCommit:      in.DeployedCommit,
		AttemptNumber:       2,
		State:               domain.RunStateQueued,
		Origin:              in.Origin,
		TriggerReason:       in.TriggerReason,
		ContextVersion:      in.ContextVersion,
		Version:             1,
	}, nil
}

func eligibleAutomaticAggregate(state domain.RunState, contextVersion int64, retryable bool) domain.RunAggregate {
	return domain.RunAggregate{Run: domain.Run{
		RunID:               "run-previous",
		SeriesID:            "series-1",
		IncidentID:          testIncidentUUID,
		LifecycleGeneration: 1,
		DeployedCommit:      "abc123",
		AttemptNumber:       1,
		State:               state,
		ContextVersion:      contextVersion,
		Retryable:           retryable,
		Version:             7,
	}}
}

func automaticRequestForTest(contextVersion int64) incidentapplication.RemediationRequest {
	return incidentapplication.RemediationRequest{
		IncidentID:          testIncidentUUID,
		LifecycleGeneration: 1,
		DeployedCommit:      "abc123",
		Priority:            "P2",
		Reason:              application.TriggerReasonAutomatic,
		ContextVersion:      contextVersion,
	}
}

func TestTriggerAutomaticGateContinuesOnlyEligibleRuns(t *testing.T) {
	tests := []struct {
		name       string
		latest     domain.RunAggregate
		latestErr  error
		contextVer int64
		wantCalls  int
	}{
		{name: "missing series creates root", latestErr: application.ErrNotFound, contextVer: 1, wantCalls: 0},
		{name: "active run", latest: eligibleAutomaticAggregate(domain.RunStateDiagnosing, 1, true), contextVer: 2},
		{name: "ready for review", latest: eligibleAutomaticAggregate(domain.RunStateDiagnosisReadyForReview, 1, true), contextVer: 2},
		{name: "completed non code", latest: eligibleAutomaticAggregate(domain.RunStateCompletedNonCode, 1, true), contextVer: 2},
		{name: "non retryable failure", latest: eligibleAutomaticAggregate(domain.RunStateFailed, 1, false), contextVer: 2},
		{name: "unchanged evidence", latest: eligibleAutomaticAggregate(domain.RunStateFailed, 2, true), contextVer: 2},
		{name: "automatic ceiling", latest: eligibleAutomaticAggregate(domain.RunStateFailed, 1, true), contextVer: 2, wantCalls: 0},
	}
	tests[len(tests)-1].latest.AttemptSummaries = []domain.AttemptSummary{
		{Origin: domain.TriggerOriginAutomaticContinue},
		{Origin: domain.TriggerOriginAutomaticContinue},
		{Origin: domain.TriggerOriginAutomaticContinue},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &automaticGateStore{fakeRunStore: newFakeRunStore(), latest: test.latest, latestErr: test.latestErr}
			trigger, err := application.NewTrigger(store, nil)
			if err != nil {
				t.Fatalf("NewTrigger() error = %v", err)
			}
			if err := trigger.Emit(context.Background(), automaticRequestForTest(test.contextVer)); err != nil {
				t.Fatalf("Emit() error = %v", err)
			}
			if store.nextCalls != test.wantCalls {
				t.Fatalf("next-attempt calls = %d, want %d", store.nextCalls, test.wantCalls)
			}
			if test.latestErr != nil && store.created == nil {
				t.Fatal("missing series did not use the idempotent root path")
			}
		})
	}
}

func TestTriggerAutomaticGateRejectsAnalysisOnlyDowngrade(t *testing.T) {
	store := &automaticGateStore{
		fakeRunStore: newFakeRunStore(),
		latest:       eligibleAutomaticAggregate(domain.RunStateFailed, 1, true),
	}
	trigger, err := application.NewTrigger(store, nil)
	if err != nil {
		t.Fatalf("NewTrigger() error = %v", err)
	}
	request := automaticRequestForTest(2)
	request.AnalysisOnly = true
	if err := trigger.Emit(context.Background(), request); !errors.Is(err, application.ErrLifecycleUnavailable) {
		t.Fatalf("Emit() error = %v", err)
	}
	if store.nextCalls != 0 {
		t.Fatalf("analysis-only downgrade created %d child attempts", store.nextCalls)
	}
}

func TestTriggerDrivesTransactionallyCreatedQueuedRoot(t *testing.T) {
	store := newFakeRunStore()
	root, err := store.CreateSeriesAndRun(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
		Priority: "P2", TriggerReason: application.TriggerReasonAutomatic, ContextVersion: 1,
	})
	if err != nil {
		t.Fatalf("CreateSeriesAndRun() error = %v", err)
	}
	coordinator := application.NewRemediationCoordinatorWithReview(
		store, &fakeRepoPort{}, &fakeEvidencePort{}, &scriptedModel{
			responses: []string{diagnosisEnvelope("external_dependency")},
		}, nil, store, store,
	)
	trigger, err := application.NewTrigger(store, coordinator)
	if err != nil {
		t.Fatalf("NewTrigger() error = %v", err)
	}
	if err := trigger.Emit(context.Background(), automaticRequestForTest(1)); err != nil {
		t.Fatalf("Emit() error = %v", err)
	}
	if store.created == nil || store.created.RunID != root.RunID || store.state != domain.RunStateCompletedNonCode {
		t.Fatalf("root = %#v, state = %s; transactionally created root was not driven", store.created, store.state)
	}
}

func TestTriggerAutomaticGateCarriesSeriesAndExpectedPredecessor(t *testing.T) {
	store := &automaticGateStore{
		fakeRunStore: newFakeRunStore(),
		latest:       eligibleAutomaticAggregate(domain.RunStateFailed, 1, true),
	}
	trigger, err := application.NewTrigger(store, nil)
	if err != nil {
		t.Fatalf("NewTrigger() error = %v", err)
	}
	if err := trigger.Emit(context.Background(), automaticRequestForTest(2)); err != nil {
		t.Fatalf("Emit() error = %v", err)
	}
	if store.nextCalls != 1 || store.nextInput.ContinuationOfRunID != "run-previous" ||
		store.nextInput.SeriesID != "series-1" || store.nextInput.ExpectedPreviousVersion != 7 ||
		store.nextInput.ContextVersion != 2 || store.nextInput.TriggerReason != domain.TriggerOriginAutomaticContinue {
		t.Fatalf("continuation input = %#v, calls = %d", store.nextInput, store.nextCalls)
	}
}

func TestTriggerAutomaticGateTreatsConcurrentStaleChildAsNoOp(t *testing.T) {
	store := &automaticGateStore{
		fakeRunStore: newFakeRunStore(),
		latest:       eligibleAutomaticAggregate(domain.RunStateFailed, 1, true),
		nextErrs:     []error{nil, domain.ErrStalePredecessor},
	}
	trigger, err := application.NewTrigger(store, nil)
	if err != nil {
		t.Fatalf("NewTrigger() error = %v", err)
	}

	var wait sync.WaitGroup
	wait.Add(2)
	errorsSeen := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wait.Done()
			errorsSeen <- trigger.Emit(context.Background(), automaticRequestForTest(2))
		}()
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatalf("concurrent Emit() error = %v", err)
		}
	}
	if store.successful != 1 || store.nextCalls != 2 {
		t.Fatalf("successful children/calls = %d/%d, want 1/2", store.successful, store.nextCalls)
	}
}

type continuationStore struct {
	*fakeRunStore
	predecessor               domain.RunAggregate
	checkpoint                domain.RunAggregate
	checkpointErr             error
	checkpointCalls           int
	checkpointSeriesID        string
	checkpointContext         int64
	checkpointThroughAttempt  int32
	childInput                domain.NextAttempt
	continuationEvidence      []domain.StoredEvidence
	continuationQuery         domain.ContinuationEvidenceQuery
	continuationCalls         int
	continuationEvidenceIndex []domain.EvidenceIndexEntry
	continuationIndexQuery    domain.ContinuationEvidenceQuery
	continuationIndexCalls    int
	// childMode 是 CreateNextAttempt 返回 child 时快照的 agent loop 模式
	// （真实存储从 predecessor 继承）；空值保持 legacy。
	childMode domain.AgentLoopMode
}

func (s *continuationStore) ListContinuationRuntimeEvidence(_ context.Context, query domain.ContinuationEvidenceQuery) ([]domain.StoredEvidence, error) {
	s.continuationCalls++
	s.continuationQuery = query
	return append([]domain.StoredEvidence(nil), s.continuationEvidence...), nil
}

func (s *continuationStore) ListContinuationEvidenceIndex(_ context.Context, query domain.ContinuationEvidenceQuery) ([]domain.EvidenceIndexEntry, error) {
	s.continuationIndexCalls++
	s.continuationIndexQuery = query
	return append([]domain.EvidenceIndexEntry(nil), s.continuationEvidenceIndex...), nil
}

func (s *continuationStore) Get(ctx context.Context, runID string) (domain.RunAggregate, error) {
	if runID == s.predecessor.Run.RunID {
		return s.predecessor, nil
	}
	return s.fakeRunStore.Get(ctx, runID)
}

func (s *continuationStore) GetLatestPlanningCheckpoint(_ context.Context, seriesID string, contextVersion int64, throughAttemptNumber int32) (domain.RunAggregate, error) {
	s.checkpointCalls++
	s.checkpointSeriesID = seriesID
	s.checkpointContext = contextVersion
	s.checkpointThroughAttempt = throughAttemptNumber
	if s.checkpointErr != nil {
		return domain.RunAggregate{}, s.checkpointErr
	}
	checkpoint := s.checkpoint
	if checkpoint.Run.RunID == "" {
		checkpoint = s.predecessor
	}
	if checkpoint.Run.SeriesID != seriesID || checkpoint.Run.ContextVersion != contextVersion ||
		checkpoint.Run.AttemptNumber > throughAttemptNumber || len(checkpoint.Decisions) == 0 ||
		checkpoint.Decisions[len(checkpoint.Decisions)-1].Fixability != domain.FixabilityCodeFixable {
		return domain.RunAggregate{}, domain.ErrPlanningCheckpointNotFound
	}
	return checkpoint, nil
}

func (s *continuationStore) CreateNextAttempt(_ context.Context, in domain.NextAttempt) (domain.Run, error) {
	s.childInput = in
	child := domain.Run{
		RunID:               "run-next",
		SeriesID:            in.SeriesID,
		IncidentID:          in.IncidentID,
		LifecycleGeneration: in.LifecycleGeneration,
		DeployedCommit:      in.DeployedCommit,
		AttemptNumber:       s.predecessor.Run.AttemptNumber + 1,
		State:               domain.RunStateQueued,
		Origin:              in.Origin,
		TriggerReason:       in.TriggerReason,
		ContinuationOfRunID: in.ContinuationOfRunID,
		ContextVersion:      in.ContextVersion,
		Version:             1,
		AgentLoopMode:       s.childMode,
	}
	s.created = &child
	s.state = domain.RunStateQueued
	s.decisions = nil
	s.plans = nil
	s.invocations = nil
	s.effects = nil
	s.budget = domain.BudgetCounters{}
	return child, nil
}

type blockingContinuationModel struct {
	entered  chan struct{}
	release  chan struct{}
	finished chan struct{}
	response string
}

func (m *blockingContinuationModel) Complete(ctx context.Context, _ domain.ModelTurn) (domain.ModelResult, error) {
	close(m.entered)
	select {
	case <-m.release:
	case <-ctx.Done():
		close(m.finished)
		return domain.ModelResult{}, ctx.Err()
	}
	close(m.finished)
	return domain.ModelResult{Content: m.response, FinishReason: "stop"}, nil
}

// TestTriggerQueueContinuationReturnsBeforeCoordinatorDrive proves the protected
// manual path only waits for durable child creation, not for model execution.
func TestTriggerQueueContinuationReturnsBeforeCoordinatorDrive(t *testing.T) {
	predecessor := eligibleAutomaticAggregate(domain.RunStateFailed, 1, true)
	store := &continuationStore{fakeRunStore: newFakeRunStore(), predecessor: predecessor}
	model := &blockingContinuationModel{
		entered: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{}),
		response: diagnosisEnvelope("external_dependency"),
	}
	coord := application.NewRemediationCoordinatorWithReview(store, &fakeRepoPort{}, &fakeEvidencePort{}, model, nil, store, store)
	trigger, err := application.NewTrigger(store, coord)
	if err != nil {
		t.Fatalf("NewTrigger() error = %v", err)
	}

	next := domain.NextAttempt{
		ContinuationOfRunID:     predecessor.Run.RunID,
		SeriesID:                predecessor.Run.SeriesID,
		IncidentID:              predecessor.Run.IncidentID,
		LifecycleGeneration:     predecessor.Run.LifecycleGeneration,
		DeployedCommit:          predecessor.Run.DeployedCommit,
		ContextVersion:          predecessor.Run.ContextVersion + 1,
		ExpectedPreviousVersion: predecessor.Run.Version,
		Origin:                  domain.TriggerOriginManualContinue,
		TriggerReason:           domain.TriggerOriginManualContinue,
		ContinuationReason:      "operator requested continuation",
	}
	returned := make(chan struct{})
	var run domain.Run
	var queueErr error
	go func() {
		run, queueErr = trigger.QueueContinuation(context.Background(), next)
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("QueueContinuation waited for the coordinator drive")
	}
	if queueErr != nil || run.State != domain.RunStateQueued || run.AttemptNumber != 2 {
		t.Fatalf("queued run/error = %#v/%v", run, queueErr)
	}
	select {
	case <-model.entered:
	case <-time.After(time.Second):
		t.Fatal("background coordinator drive did not start")
	}
	close(model.release)
	select {
	case <-model.finished:
	case <-time.After(time.Second):
		t.Fatal("background coordinator drive did not finish")
	}
}

func TestCoordinatorContinueResumesPlanningAfterDurableCodeDiagnosis(t *testing.T) {
	predecessor := domain.RunAggregate{
		Run: domain.Run{
			RunID:               "run-previous",
			SeriesID:            "series-1",
			IncidentID:          testIncidentUUID,
			LifecycleGeneration: 1,
			DeployedCommit:      "abc123",
			AttemptNumber:       1,
			State:               domain.RunStateBlockedManualReview,
			ContextVersion:      5,
			TerminalReason:      "invalid_envelope",
			Version:             7,
		},
		Decisions: []domain.Decision{{
			Fixability:            domain.FixabilityCodeFixable,
			Confidence:            0.88,
			CausalReasoning:       "direct runtime evidence established the fault",
			EvidenceCitations:     []string{"evidence-direct"},
			RecommendedNextAction: "prepare a repair plan",
		}},
	}
	store := &continuationStore{fakeRunStore: newFakeRunStore(), predecessor: predecessor}
	repo := &fakeRepoPort{}
	model := &scriptedModel{responses: []string{planEnvelope()}}
	coord := application.NewRemediationCoordinatorWithReview(store, repo, &fakeEvidencePort{}, model, nil, store, store)
	coord.SetBootstrapEvidenceLoader(&bootstrapEvidenceLoader{value: domain.BootstrapEvidence{
		Records: []domain.StoredEvidence{{
			EvidenceID: "fresh-alert", Provider: "tencent_cls", EvidenceKind: domain.EvidenceKindNormalizedAlert,
			Classification: domain.EvidenceContextual, Outcome: "success", Available: true,
			Payload: json.RawMessage(`{"title":"repeated alert without detail"}`), Provenance: json.RawMessage(`{}`),
		}},
	}})

	run, err := coord.Continue(context.Background(), domain.NextAttempt{
		ContinuationOfRunID:     predecessor.Run.RunID,
		SeriesID:                predecessor.Run.SeriesID,
		IncidentID:              predecessor.Run.IncidentID,
		LifecycleGeneration:     predecessor.Run.LifecycleGeneration,
		DeployedCommit:          predecessor.Run.DeployedCommit,
		ContextVersion:          5,
		ExpectedPreviousVersion: predecessor.Run.Version,
		Origin:                  domain.TriggerOriginManualContinue,
		TriggerReason:           domain.TriggerOriginManualContinue,
		ContinuationReason:      "operator requested continuation",
	})
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if run.State != domain.RunStateDiagnosisReadyForReview || len(store.plans) != 1 {
		t.Fatalf("continued run/plans = %#v/%#v", run, store.plans)
	}
	if len(model.turns) != 1 || !strings.Contains(model.turns[0].UserMessage, "Planning wire contract") {
		t.Fatalf("model turns = %#v, want one planning turn", model.turns)
	}
	if store.countTransitionsTo(domain.RunStateDiagnosing) != 0 || store.countTransitionsTo(domain.RunStatePlanning) != 1 {
		t.Fatalf("transitions = %#v, want preparing_context to planning", store.transitions)
	}
	if repo.calls != 0 || len(store.invocations) != 0 {
		t.Fatalf("planning retry recollected evidence: repo=%d invocations=%#v", repo.calls, store.invocations)
	}
	for _, tool := range model.turns[0].Tools {
		if tool.Name == application.ToolTencentCLSDetail || tool.Name == application.ToolDockerLogs {
			t.Fatalf("planning retry exposed diagnosis evidence tool %q", tool.Name)
		}
	}
	if !strings.Contains(model.turns[0].UserMessage, "direct runtime evidence established the fault") ||
		!strings.Contains(model.turns[0].UserMessage, "evidence-direct") ||
		!strings.Contains(model.turns[0].UserMessage, `"planningCheckpoint"`) {
		t.Fatalf("planning turn lost predecessor diagnosis: %s", model.turns[0].UserMessage)
	}
}

func TestCoordinatorContinueRecoversLatestPlanningCheckpointAcrossPollutedChain(t *testing.T) {
	checkpoint := domain.RunAggregate{
		Run: domain.Run{
			RunID: "run-1", SeriesID: "series-1", IncidentID: testIncidentUUID,
			LifecycleGeneration: 1, DeployedCommit: "abc123", AttemptNumber: 1,
			State: domain.RunStateBlockedManualReview, ContextVersion: 5, Version: 7,
		},
		Decisions: []domain.Decision{{
			Fixability: domain.FixabilityCodeFixable, Confidence: 0.91,
			CausalReasoning:   "evidence-gated checkpoint from attempt one",
			EvidenceCitations: []string{"evidence-direct"},
		}},
	}
	predecessor := domain.RunAggregate{
		Run: domain.Run{
			RunID: "run-5", SeriesID: "series-1", IncidentID: testIncidentUUID,
			LifecycleGeneration: 1, DeployedCommit: "abc123", AttemptNumber: 5,
			State: domain.RunStateBlockedManualReview, ContinuationOfRunID: "run-4",
			ContextVersion: 5, TerminalReason: "insufficient_evidence", Version: 11,
		},
		Decisions: []domain.Decision{{
			Fixability: domain.FixabilityInsufficientEvidence, Confidence: 0.2,
			CausalReasoning: "polluted immediate predecessor diagnosis",
		}},
	}
	store := &continuationStore{
		fakeRunStore: newFakeRunStore(), predecessor: predecessor, checkpoint: checkpoint,
	}
	model := &scriptedModel{responses: []string{planEnvelope()}}
	coord := application.NewRemediationCoordinatorWithReview(
		store, &fakeRepoPort{}, &fakeEvidencePort{}, model, nil, store, store,
	)

	run, err := coord.Continue(context.Background(), domain.NextAttempt{
		ContinuationOfRunID: predecessor.Run.RunID, SeriesID: predecessor.Run.SeriesID,
		IncidentID: predecessor.Run.IncidentID, LifecycleGeneration: predecessor.Run.LifecycleGeneration,
		DeployedCommit: predecessor.Run.DeployedCommit, ContextVersion: 5,
		ExpectedPreviousVersion: predecessor.Run.Version, Origin: domain.TriggerOriginManualContinue,
		TriggerReason: domain.TriggerOriginManualContinue, ContinuationReason: "operator requested continuation",
	})
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if run.AttemptNumber != 6 || run.State != domain.RunStateDiagnosisReadyForReview {
		t.Fatalf("continued run = %#v", run)
	}
	if store.checkpointCalls != 1 || store.checkpointSeriesID != "series-1" ||
		store.checkpointContext != 5 || store.checkpointThroughAttempt != 5 {
		t.Fatalf("checkpoint lookup = calls=%d series=%q context=%d through=%d", store.checkpointCalls, store.checkpointSeriesID, store.checkpointContext, store.checkpointThroughAttempt)
	}
	if len(model.turns) != 1 || !strings.Contains(model.turns[0].UserMessage, "Planning wire contract") ||
		!strings.Contains(model.turns[0].UserMessage, "evidence-gated checkpoint from attempt one") ||
		!strings.Contains(model.turns[0].UserMessage, `"runId":"run-1"`) ||
		strings.Contains(model.turns[0].UserMessage, "polluted immediate predecessor diagnosis") {
		t.Fatalf("planning turn did not use the durable checkpoint: %#v", model.turns)
	}
	if store.countTransitionsTo(domain.RunStateDiagnosing) != 0 {
		t.Fatalf("polluted chain restarted diagnosis: %#v", store.transitions)
	}
}

func TestCoordinatorContinueDoesNotReuseCheckpointAcrossContextVersions(t *testing.T) {
	checkpoint := domain.RunAggregate{
		Run: domain.Run{
			RunID: "run-1", SeriesID: "series-1", IncidentID: testIncidentUUID,
			LifecycleGeneration: 1, DeployedCommit: "abc123", AttemptNumber: 1,
			State: domain.RunStateBlockedManualReview, ContextVersion: 5, Version: 7,
		},
		Decisions: []domain.Decision{{Fixability: domain.FixabilityCodeFixable}},
	}
	predecessor := domain.RunAggregate{Run: domain.Run{
		RunID: "run-5", SeriesID: "series-1", IncidentID: testIncidentUUID,
		LifecycleGeneration: 1, DeployedCommit: "abc123", AttemptNumber: 5,
		State: domain.RunStateFailed, ContextVersion: 5, Version: 11,
	}}
	store := &continuationStore{
		fakeRunStore: newFakeRunStore(), predecessor: predecessor, checkpoint: checkpoint,
	}
	model := &scriptedModel{responses: []string{diagnosisEnvelope("external_dependency")}}
	coord := application.NewRemediationCoordinatorWithReview(
		store, &fakeRepoPort{}, &fakeEvidencePort{}, model, nil, store, store,
	)

	run, err := coord.Continue(context.Background(), domain.NextAttempt{
		ContinuationOfRunID: predecessor.Run.RunID, SeriesID: predecessor.Run.SeriesID,
		IncidentID: predecessor.Run.IncidentID, LifecycleGeneration: predecessor.Run.LifecycleGeneration,
		DeployedCommit: predecessor.Run.DeployedCommit, ContextVersion: 6,
		ExpectedPreviousVersion: predecessor.Run.Version, Origin: domain.TriggerOriginManualContinue,
		TriggerReason: domain.TriggerOriginManualContinue, ContinuationReason: "operator requested continuation",
	})
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if run.State != domain.RunStateCompletedNonCode || store.checkpointCalls != 0 ||
		store.countTransitionsTo(domain.RunStateDiagnosing) != 1 || store.countTransitionsTo(domain.RunStatePlanning) != 0 {
		t.Fatalf("new-context continuation = run=%#v checkpointCalls=%d transitions=%#v", run, store.checkpointCalls, store.transitions)
	}
}

func TestCoordinatorContinueUsesBoundedBriefAndFreshEvidence(t *testing.T) {
	predecessor := domain.RunAggregate{
		Run: domain.Run{
			RunID:               "run-previous",
			SeriesID:            "series-1",
			IncidentID:          testIncidentUUID,
			LifecycleGeneration: 1,
			DeployedCommit:      "abc123",
			AttemptNumber:       1,
			State:               domain.RunStateFailed,
			Origin:              domain.TriggerOriginAutomatic,
			ContextVersion:      4,
			TerminalReason:      "provider_transport",
			Retryable:           true,
			Version:             7,
		},
		Decisions: []domain.Decision{{
			Fixability:            domain.FixabilityInsufficientEvidence,
			Confidence:            0.31,
			CausalReasoning:       "old diagnosis with password=should-not-leak",
			EvidenceCitations:     []string{"evidence-previous"},
			MissingEvidence:       []string{"runtime logs"},
			Contradictions:        []string{"old contradictory summary"},
			RecommendedNextAction: "collect current evidence",
		}},
		Plans: []domain.RepairPlanCandidate{{
			PlanID:           "plan-previous",
			AffectedFiles:    []string{"internal/handler.go"},
			Risk:             domain.RiskOrdinary,
			RollbackStrategy: "revert the patch",
			Rationale:        "old plan rationale",
			EvidenceRefs:     []string{"evidence-previous"},
		}},
		ToolInvocations: []domain.ToolInvocation{{
			ToolName:          application.ToolRepoReadFile,
			Phase:             domain.RunStateDiagnosing,
			ResultSummary:     "success",
			ParametersSummary: "authorization=should-not-leak",
			Error:             "provider response raw",
			BytesRetrieved:    128,
		}},
		ArtifactReferences: []string{"sha256:previous-artifact"},
		SuggestedDiff:      "diff --git a/internal/handler.go b/internal/handler.go\n+secret=should-not-leak",
	}
	store := &continuationStore{fakeRunStore: newFakeRunStore(), predecessor: predecessor}
	model := &scriptedModel{responses: []string{diagnosisEnvelope("external_dependency")}}
	coord := application.NewRemediationCoordinatorWithReview(
		store, &fakeRepoPort{}, &fakeEvidencePort{}, model, nil, store, store,
	)
	loader := &bootstrapEvidenceLoader{value: domain.BootstrapEvidence{
		Records: []domain.StoredEvidence{{
			EvidenceID: "fresh-evidence", Provider: "webhook", EvidenceKind: domain.EvidenceKindNormalizedAlert,
			Classification: domain.EvidenceContextual, Outcome: "success", Available: true,
			Payload: json.RawMessage(`{"currentFault":"fresh evidence"}`), Provenance: json.RawMessage(`{}`),
		}},
	}}
	coord.SetBootstrapEvidenceLoader(loader)

	next := domain.NextAttempt{
		ContinuationOfRunID:     predecessor.Run.RunID,
		SeriesID:                predecessor.Run.SeriesID,
		IncidentID:              predecessor.Run.IncidentID,
		LifecycleGeneration:     predecessor.Run.LifecycleGeneration,
		DeployedCommit:          predecessor.Run.DeployedCommit,
		ContextVersion:          5,
		ExpectedPreviousVersion: predecessor.Run.Version,
		Origin:                  domain.TriggerOriginAutomaticContinue,
		TriggerReason:           domain.TriggerOriginAutomaticContinue,
		ContinuationReason:      "new inbound evidence persisted",
	}
	run, err := coord.Continue(context.Background(), next)
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if run.State != domain.RunStateCompletedNonCode || run.AttemptNumber != 2 {
		t.Fatalf("continued run = %#v", run)
	}
	if store.childInput.ContinuationOfRunID != predecessor.Run.RunID || store.childInput.ContextVersion != 5 {
		t.Fatalf("child input = %#v", store.childInput)
	}
	if predecessor.Run.State != domain.RunStateFailed || !predecessor.Run.Retryable {
		t.Fatalf("predecessor was mutated: %#v", predecessor.Run)
	}
	if len(model.turns) != 1 {
		t.Fatalf("model turns = %d, want 1", len(model.turns))
	}
	message := model.turns[0].UserMessage
	for _, want := range []string{
		"remediation_continuation_brief", "provider_transport", "old diagnosis", "evidence-previous",
		"plan-previous", "repository.read_file", "new inbound evidence persisted", "fresh-evidence", "fresh evidence",
		"hypotheses", "current bootstrap evidence is authoritative",
	} {
		if !contains(message, want) {
			t.Fatalf("continuation message missing %q: %s", want, message)
		}
	}
	for _, leaked := range []string{"should-not-leak", "provider response raw", "authorization=should-not-leak"} {
		if contains(message, leaked) {
			t.Fatalf("continuation message leaked %q: %s", leaked, message)
		}
	}
}

func TestCoordinatorCallerDeadlinePersistsBudgetExhaustion(t *testing.T) {
	store := newFakeRunStore()
	limits := application.DefaultBudgetLimits()
	coord := newCoordinatorWithBudget(store, &fakeRepoPort{}, &fakeEvidencePort{}, &delayedModel{
		delay:     50 * time.Millisecond,
		responses: []string{diagnosisEnvelope("external_dependency")},
	}, limits)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	run, err := coord.Start(ctx, domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if run.State != domain.RunStateBudgetExhausted || store.state != domain.RunStateBudgetExhausted {
		t.Fatalf("state = %s/%s, want budget_exhausted", run.State, store.state)
	}
	if len(store.effects) == 0 || store.effects[len(store.effects)-1].TerminalReason != "elapsed" || store.effects[len(store.effects)-1].Retryable {
		t.Fatalf("terminal effect = %#v", store.effects)
	}
}

// TestCoordinatorContinueLoadsPriorRuntimeEvidence 覆盖设计第 6 步：diagnosis
// continuation 加载同 series 早期 attempt 的 sanitized runtime evidence（保留原始
// 证据 ID），planning checkpoint 路径保持紧凑 brief 不加载运行时记录。
func TestCoordinatorContinueLoadsPriorRuntimeEvidence(t *testing.T) {
	predecessor := eligibleAutomaticAggregate(domain.RunStateFailed, 5, true)
	predecessor.Run.TerminalReason = "provider_transport"
	predecessor.Run.Retryable = true
	store := &continuationStore{
		fakeRunStore: newFakeRunStore(),
		predecessor:  predecessor,
		continuationEvidence: []domain.StoredEvidence{{
			EvidenceID: "ev-ssh-prior", Provider: "ssh", EvidenceKind: domain.EvidenceKindRuntime,
			Classification: domain.EvidenceCorrelatedSupport, Outcome: "success", Available: true,
			Payload: json.RawMessage(`{"command":"'hostname' '-I'","stdout":"10.16.6.17 password=[redacted]"}`),
		}, {
			EvidenceID: "ev-docker-prior", Provider: "docker", EvidenceKind: domain.EvidenceKindRuntime,
			Classification: domain.EvidenceDirectFault, Outcome: "success", Available: true,
			Payload: json.RawMessage(`{"stdout":"panic: nil pointer\n"}`),
		}},
	}
	model := &scriptedModel{responses: []string{diagnosisEnvelope("external_dependency")}}
	coord := application.NewRemediationCoordinatorWithReview(
		store, &fakeRepoPort{}, &fakeEvidencePort{}, model, nil, store, store,
	)

	run, err := coord.Continue(context.Background(), domain.NextAttempt{
		ContinuationOfRunID:     predecessor.Run.RunID,
		SeriesID:                predecessor.Run.SeriesID,
		IncidentID:              predecessor.Run.IncidentID,
		LifecycleGeneration:     predecessor.Run.LifecycleGeneration,
		DeployedCommit:          predecessor.Run.DeployedCommit,
		ContextVersion:          6,
		ExpectedPreviousVersion: predecessor.Run.Version,
		Origin:                  domain.TriggerOriginAutomaticContinue,
		TriggerReason:           domain.TriggerOriginAutomaticContinue,
		ContinuationReason:      "new inbound evidence persisted",
	})
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if run.State != domain.RunStateCompletedNonCode {
		t.Fatalf("continued run = %#v", run)
	}
	if store.continuationCalls != 1 || store.continuationQuery.SeriesID != "series-1" ||
		store.continuationQuery.ThroughAttemptNumber != 1 {
		t.Fatalf("continuation evidence query = %#v calls=%d", store.continuationQuery, store.continuationCalls)
	}
	message := model.turns[0].UserMessage
	if !contains(message, "prior_runtime_evidence") || !contains(message, "ev-ssh-prior") ||
		!contains(message, "ev-docker-prior") || !contains(message, "10.16.6.17") ||
		!contains(message, "panic: nil pointer") {
		t.Fatalf("diagnosis continuation omitted prior runtime evidence: %s", message)
	}
	// canonical 持久化 payload 已脱敏，渲染时不允许重新引入凭据。
	if contains(message, "hunter2") {
		t.Fatalf("diagnosis continuation leaked credential: %s", message)
	}
}

// TestCoordinatorContinueRendersSameSeriesEvidenceIndex 覆盖 implement.md
// slice 3：diagnosis continuation 在 runtime 全量记录之外，还渲染同 series 非
// runtime 证据（provider_detail、pre-run normalized_alert）的紧凑索引，保留证据
// ID 与来源 attempt、绝不内联 payload；模型凭索引条目通过 evidence.read 重新
// 读取原始内容（R10/R15）。
func TestCoordinatorContinueRendersSameSeriesEvidenceIndex(t *testing.T) {
	predecessor := eligibleAutomaticAggregate(domain.RunStateFailed, 5, true)
	predecessor.Run.TerminalReason = "provider_transport"
	predecessor.Run.Retryable = true
	store := &continuationStore{
		fakeRunStore: newFakeRunStore(),
		predecessor:  predecessor,
		continuationEvidence: []domain.StoredEvidence{{
			EvidenceID: "ev-ssh-prior", Provider: "ssh", EvidenceKind: domain.EvidenceKindRuntime,
			Classification: domain.EvidenceCorrelatedSupport, Outcome: "success", Available: true,
			Payload: json.RawMessage(`{"command":"'hostname' '-I'","stdout":"10.16.6.17"}`),
		}},
		continuationEvidenceIndex: []domain.EvidenceIndexEntry{{
			EvidenceID: "ev-detail-prior", Kind: domain.EvidenceKindProviderDetail,
			Provider: "tencent_cls", Classification: domain.EvidenceDirectFault,
			SourceAttempt: 1, ContentHash: strings.Repeat("a", 64),
		}, {
			EvidenceID: "ev-alert-pre-run", Kind: domain.EvidenceKindNormalizedAlert,
			Provider: "tencent_cls", Classification: domain.EvidenceContextual,
			SourceAttempt: 0, ContentHash: strings.Repeat("b", 64),
		}, {
			EvidenceID: "ev-credential-shaped", Kind: "repository",
			Provider: "ssh password=should-not-leak", Classification: domain.EvidenceCorrelatedSupport,
			SourceAttempt: 2, ContentHash: strings.Repeat("c", 64),
		}},
	}
	model := &scriptedModel{responses: []string{diagnosisEnvelope("external_dependency")}}
	coord := application.NewRemediationCoordinatorWithReview(
		store, &fakeRepoPort{}, &fakeEvidencePort{}, model, nil, store, store,
	)

	run, err := coord.Continue(context.Background(), domain.NextAttempt{
		ContinuationOfRunID:     predecessor.Run.RunID,
		SeriesID:                predecessor.Run.SeriesID,
		IncidentID:              predecessor.Run.IncidentID,
		LifecycleGeneration:     predecessor.Run.LifecycleGeneration,
		DeployedCommit:          predecessor.Run.DeployedCommit,
		ContextVersion:          6,
		ExpectedPreviousVersion: predecessor.Run.Version,
		Origin:                  domain.TriggerOriginAutomaticContinue,
		TriggerReason:           domain.TriggerOriginAutomaticContinue,
		ContinuationReason:      "new inbound evidence persisted",
	})
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if run.State != domain.RunStateCompletedNonCode {
		t.Fatalf("continued run = %#v", run)
	}
	// 两个 loader 都在 diagnosis 路径被调用，且使用同一 series/attempt 边界。
	if store.continuationCalls != 1 || store.continuationIndexCalls != 1 ||
		store.continuationIndexQuery.SeriesID != "series-1" ||
		store.continuationIndexQuery.ThroughAttemptNumber != 1 {
		t.Fatalf("continuation loaders = runtime:%d index:%d query=%#v",
			store.continuationCalls, store.continuationIndexCalls, store.continuationIndexQuery)
	}
	message := model.turns[0].UserMessage
	for _, want := range []string{
		"prior_runtime_evidence", "ev-ssh-prior", "10.16.6.17",
		"prior_evidence_index", "ev-detail-prior", "ev-alert-pre-run",
		"provider_detail", "normalized_alert", "direct_fault", "evidence.read",
	} {
		if !contains(message, want) {
			t.Fatalf("diagnosis continuation missing %q: %s", want, message)
		}
	}
	// 索引只含紧凑元数据：条目带 sourceAttempt，且索引区不出现 payload 键。
	// JSON 键按字母序排列，kind 位于块尾；从 entries 位置切到 kind 位置。
	entriesPos := strings.Index(message, `"entries":[`)
	kindPos := strings.Index(message, `"kind":"prior_evidence_index"`)
	if entriesPos < 0 || kindPos < entriesPos {
		t.Fatalf("diagnosis continuation omitted the evidence index: %s", message)
	}
	indexBlock := message[entriesPos:kindPos]
	for _, want := range []string{
		`"evidenceId":"ev-detail-prior"`, `"sourceAttempt":1`,
		`"sourceAttempt":0`, `"contentHash"`, `"classification"`,
	} {
		if !strings.Contains(indexBlock, want) {
			t.Fatalf("evidence index missing %s: %s", want, indexBlock)
		}
	}
	if strings.Contains(indexBlock, `"payload"`) {
		t.Fatalf("evidence index inlined a payload: %s", indexBlock)
	}
	// 索引元数据同样经过既有 sanitize 脱敏（R15 不引入凭据）。
	if strings.Contains(message, "should-not-leak") {
		t.Fatalf("evidence index leaked credential-shaped metadata: %s", message)
	}
}

// TestCoordinatorContinuePlanningKeepsCompactBrief 覆盖 planning checkpoint 继续时
// 不加载 runtime evidence 记录，保持既有紧凑 brief 路径。
func TestCoordinatorContinuePlanningKeepsCompactBrief(t *testing.T) {
	checkpoint := domain.RunAggregate{
		Run: domain.Run{
			RunID: "run-1", SeriesID: "series-1", IncidentID: testIncidentUUID,
			LifecycleGeneration: 1, DeployedCommit: "abc123", AttemptNumber: 1,
			State: domain.RunStateBlockedManualReview, ContextVersion: 5, Version: 7,
		},
		Decisions: []domain.Decision{{Fixability: domain.FixabilityCodeFixable, Confidence: 0.91}},
	}
	predecessor := domain.RunAggregate{
		Run: domain.Run{
			RunID: "run-2", SeriesID: "series-1", IncidentID: testIncidentUUID,
			LifecycleGeneration: 1, DeployedCommit: "abc123", AttemptNumber: 2,
			State: domain.RunStateFailed, ContextVersion: 5, Version: 9,
		},
	}
	store := &continuationStore{
		fakeRunStore: newFakeRunStore(), predecessor: predecessor, checkpoint: checkpoint,
		continuationEvidence: []domain.StoredEvidence{{
			EvidenceID: "ev-ssh-prior", Provider: "ssh", EvidenceKind: domain.EvidenceKindRuntime,
			Classification: domain.EvidenceCorrelatedSupport, Outcome: "success", Available: true,
			Payload: json.RawMessage(`{"stdout":"10.16.6.17"}`),
		}},
	}
	model := &scriptedModel{responses: []string{planEnvelope()}}
	coord := application.NewRemediationCoordinatorWithReview(
		store, &fakeRepoPort{}, &fakeEvidencePort{}, model, nil, store, store,
	)

	run, err := coord.Continue(context.Background(), domain.NextAttempt{
		ContinuationOfRunID: predecessor.Run.RunID, SeriesID: predecessor.Run.SeriesID,
		IncidentID: predecessor.Run.IncidentID, LifecycleGeneration: predecessor.Run.LifecycleGeneration,
		DeployedCommit: predecessor.Run.DeployedCommit, ContextVersion: 5,
		ExpectedPreviousVersion: predecessor.Run.Version, Origin: domain.TriggerOriginManualContinue,
		TriggerReason: domain.TriggerOriginManualContinue, ContinuationReason: "operator requested continuation",
	})
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if run.State != domain.RunStateDiagnosisReadyForReview {
		t.Fatalf("planning continuation run = %#v", run)
	}
	if store.continuationCalls != 0 {
		t.Fatalf("planning continuation loaded runtime evidence: calls=%d", store.continuationCalls)
	}
	if store.continuationIndexCalls != 0 {
		t.Fatalf("planning continuation loaded the evidence index: calls=%d", store.continuationIndexCalls)
	}
	message := model.turns[0].UserMessage
	if contains(message, "prior_runtime_evidence") || contains(message, "ev-ssh-prior") {
		t.Fatalf("planning continuation should keep the compact checkpoint brief: %s", message)
	}
}

func TestCoordinatorContinueRejectsStalePredecessor(t *testing.T) {
	predecessor := eligibleAutomaticAggregate(domain.RunStateFailed, 1, true)
	store := &continuationStore{fakeRunStore: newFakeRunStore(), predecessor: predecessor}
	coord := application.NewRemediationCoordinatorWithReview(store, &fakeRepoPort{}, &fakeEvidencePort{}, &scriptedModel{}, nil, store, store)
	_, err := coord.Continue(context.Background(), domain.NextAttempt{
		ContinuationOfRunID:     predecessor.Run.RunID,
		SeriesID:                predecessor.Run.SeriesID,
		IncidentID:              predecessor.Run.IncidentID,
		LifecycleGeneration:     predecessor.Run.LifecycleGeneration,
		DeployedCommit:          "different-commit",
		ContextVersion:          2,
		ExpectedPreviousVersion: predecessor.Run.Version,
		Origin:                  domain.TriggerOriginAutomaticContinue,
		TriggerReason:           domain.TriggerOriginAutomaticContinue,
		ContinuationReason:      "new inbound evidence persisted",
	})
	if !errors.Is(err, domain.ErrStalePredecessor) || store.childInput.ContinuationOfRunID != "" {
		t.Fatalf("stale continuation error = %v, child input = %#v", err, store.childInput)
	}
}

func TestCoordinatorContinueRejectsAnalysisOnlyDowngrade(t *testing.T) {
	predecessor := eligibleAutomaticAggregate(domain.RunStateFailed, 1, true)
	predecessor.Run.AnalysisOnly = true
	store := &continuationStore{fakeRunStore: newFakeRunStore(), predecessor: predecessor}
	model := &scriptedModel{}
	coord := application.NewRemediationCoordinatorWithReview(store, &fakeRepoPort{}, &fakeEvidencePort{}, model, nil, store, store)
	_, err := coord.Continue(context.Background(), domain.NextAttempt{
		ContinuationOfRunID: predecessor.Run.RunID, SeriesID: predecessor.Run.SeriesID,
		IncidentID: predecessor.Run.IncidentID, LifecycleGeneration: predecessor.Run.LifecycleGeneration,
		DeployedCommit: predecessor.Run.DeployedCommit, ContextVersion: predecessor.Run.ContextVersion,
		ExpectedPreviousVersion: predecessor.Run.Version, Origin: domain.TriggerOriginManualContinue,
		TriggerReason: domain.TriggerOriginManualContinue, ContinuationReason: "operator requested continuation",
	})
	if !errors.Is(err, domain.ErrStalePredecessor) || model.calls != 0 || store.childInput.ContinuationOfRunID == "" {
		t.Fatalf("analysis-only downgrade error=%v model_calls=%d child_input=%#v", err, model.calls, store.childInput)
	}
}

func contains(value, substring string) bool {
	return len(substring) == 0 || strings.Contains(value, substring)
}
