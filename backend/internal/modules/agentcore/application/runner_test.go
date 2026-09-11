package application_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"mendry/backend/internal/modules/agentcore/application"
	"mendry/backend/internal/modules/agentcore/domain"
)

type memoryStore struct {
	mu               sync.Mutex
	run              domain.Run
	invocations      []domain.Invocation
	results          []domain.InvocationResult
	artifacts        []domain.Artifact
	messages         []domain.ModelMessage
	intentErr        error
	resultErr        error
	messageErr       error
	intentBeforeCall bool
}

func (s *memoryStore) LoadRun(context.Context, string) (domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.run, nil
}
func (s *memoryStore) SaveRun(_ context.Context, run domain.Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.run = run
	return nil
}
func (s *memoryStore) ListInvocations(context.Context, string) ([]domain.Invocation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]domain.Invocation(nil), s.invocations...), nil
}
func (s *memoryStore) RecordInvocationIntent(_ context.Context, invocation domain.Invocation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.intentErr != nil {
		return s.intentErr
	}
	s.intentBeforeCall = true
	s.invocations = append(s.invocations, invocation)
	return nil
}
func (s *memoryStore) RecordInvocationResult(_ context.Context, result domain.InvocationResult, artifacts []domain.Artifact) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resultErr != nil {
		return s.resultErr
	}
	s.results = append(s.results, result)
	s.artifacts = append(s.artifacts, artifacts...)
	for index := range s.invocations {
		if s.invocations[index].ID == result.InvocationID {
			s.invocations[index].State = result.State
			s.invocations[index].OutputBytes = result.OutputBytes
		}
	}
	return nil
}
func (s *memoryStore) ListArtifacts(context.Context, string) ([]domain.Artifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]domain.Artifact(nil), s.artifacts...), nil
}
func (s *memoryStore) AppendArtifacts(_ context.Context, _ string, artifacts []domain.Artifact) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.artifacts = append(s.artifacts, artifacts...)
	return nil
}
func (s *memoryStore) LoadMessages(context.Context, string) ([]domain.ModelMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]domain.ModelMessage(nil), s.messages...), nil
}
func (s *memoryStore) AppendMessageGroup(_ context.Context, _ string, messages []domain.ModelMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.messageErr != nil {
		return s.messageErr
	}
	s.messages = append(s.messages, messages...)
	return nil
}

type sequenceProvider struct {
	results []domain.ModelResult
	calls   int
}

func (p *sequenceProvider) Complete(_ context.Context, turn domain.ModelTurn) (domain.ModelResult, error) {
	if p.calls >= len(p.results) {
		return domain.ModelResult{}, errors.New("unexpected model call")
	}
	result := p.results[p.calls]
	p.calls++
	if len(turn.Tools) == 0 && len(result.ToolCalls) > 0 {
		return domain.ModelResult{}, errors.New("tool catalog missing")
	}
	return result, nil
}

type cancelingProvider struct {
	cancel context.CancelFunc
	calls  int
}

func (p *cancelingProvider) Complete(context.Context, domain.ModelTurn) (domain.ModelResult, error) {
	p.calls++
	p.cancel()
	return domain.ModelResult{}, context.Canceled
}

func profileDefinition(mode domain.CompletionMode) domain.ProfileDefinition {
	return domain.ProfileDefinition{Name: "generic", Version: "v1", Completion: domain.CompletionContract{Mode: mode, Version: "v1"}}
}

func goalProfile(t *testing.T, mode domain.CompletionMode) *application.GoalProfile {
	t.Helper()
	profile, err := application.NewGoalProfile(application.GoalProfileOptions{
		Name: "generic", Version: "v1", SystemPrompt: "Complete the configured goal.",
		Completion: domain.CompletionContract{Mode: mode, Version: "v1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

type checkingExecutor struct {
	store  *memoryStore
	calls  int
	result domain.ToolExecution
	err    error
}

func (e *checkingExecutor) Execute(context.Context, domain.ToolCall) (domain.ToolExecution, error) {
	e.calls++
	e.store.mu.Lock()
	before := e.store.intentBeforeCall
	e.store.mu.Unlock()
	if !before {
		return domain.ToolExecution{}, errors.New("executor ran before intent")
	}
	return e.result, e.err
}

func baseRun() domain.Run {
	return domain.Run{ID: "run-1", Goal: "produce a result", ProfileName: "generic", ProfileVersion: "v1", State: domain.RunStateQueued, Budget: domain.Budget{Limits: domain.BudgetLimits{MaxModelCalls: 8, MaxToolCalls: 8, MaxOutputBytes: 1 << 20}}}
}

func nextIDs() func() string {
	sequence := 0
	return func() string { sequence++; return fmt.Sprintf("id-%d", sequence) }
}

func newRunner(t *testing.T, store *memoryStore, provider domain.ModelProvider, registry *application.ToolRegistry, policy application.Policy) *application.Runner {
	t.Helper()
	runner, err := application.NewRunner(application.RunnerOptions{Store: store, Provider: provider, Tools: registry, Policy: policy, NewID: nextIDs(), Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

func TestRunnerUsesSameLoopForSolutionAndActionVerificationProfiles(t *testing.T) {
	t.Run("solution", func(t *testing.T) {
		store := &memoryStore{run: baseRun()}
		provider := &sequenceProvider{results: []domain.ModelResult{{Content: "report", ModelCalls: 1}}}
		runner := newRunner(t, store, provider, application.NewToolRegistry(), application.AllowAllPolicy{})
		profile := goalProfile(t, domain.CompletionSolutionDelivered)
		run, err := runner.Drive(context.Background(), "run-1", profile)
		if err != nil || run.State != domain.RunStateSucceeded || len(store.artifacts) != 1 || store.artifacts[0].Provenance != domain.ProvenanceModel {
			t.Fatalf("run=%+v artifacts=%+v err=%v", run, store.artifacts, err)
		}
	})

	t.Run("action plus verification", func(t *testing.T) {
		store := &memoryStore{run: baseRun()}
		registry := application.NewToolRegistry()
		executor := &checkingExecutor{store: store, result: domain.ToolExecution{Artifacts: []domain.Artifact{
			{ID: "action-1", Type: "action", SchemaVersion: "v1", ExternalID: "remote-1", Data: map[string]any{"written": true}, Provenance: domain.ProvenanceObserved},
			{Type: "verification", SchemaVersion: "v1", Data: map[string]any{"matched": true}, Provenance: domain.ProvenanceVerified, Subject: &domain.VerificationSubject{ArtifactID: "action-1"}, VerificationStatus: domain.VerificationPassed},
		}}}
		if err := registry.Register(domain.ToolDefinition{Name: "workspace.change", Version: "v1", Effect: domain.ToolEffectWrite, Parameters: closedSchema(nil)}, executor); err != nil {
			t.Fatal(err)
		}
		provider := &sequenceProvider{results: []domain.ModelResult{
			{ToolCalls: []domain.ToolCall{{ID: "call-1", Name: "workspace.change", Version: "v1", Arguments: map[string]any{}}}},
			{Content: "done"},
		}}
		runner := newRunner(t, store, provider, registry, application.AllowAllPolicy{})
		run, err := runner.Drive(context.Background(), "run-1", goalProfile(t, domain.CompletionActionWithVerification))
		if err != nil || run.State != domain.RunStateSucceeded || executor.calls != 1 || len(store.results) != 1 {
			t.Fatalf("run=%+v calls=%d results=%+v err=%v", run, executor.calls, store.results, err)
		}
		if len(store.messages) != 5 || store.messages[2].Role != "tool" {
			t.Fatalf("durable paired history = %#v", store.messages)
		}
	})
}

func TestRunnerIntentFailurePreventsExecution(t *testing.T) {
	store := &memoryStore{run: baseRun(), intentErr: errors.New("disk unavailable")}
	registry := application.NewToolRegistry()
	executor := &checkingExecutor{store: store}
	_ = registry.Register(domain.ToolDefinition{Name: "workspace.change", Version: "v1", Effect: domain.ToolEffectWrite, Parameters: closedSchema(nil)}, executor)
	provider := &sequenceProvider{results: []domain.ModelResult{{ToolCalls: []domain.ToolCall{{ID: "call-1", Name: "workspace.change", Version: "v1", Arguments: map[string]any{}}}}}}
	runner := newRunner(t, store, provider, registry, application.AllowAllPolicy{})
	_, err := runner.Drive(context.Background(), "run-1", goalProfile(t, domain.CompletionSolutionDelivered))
	if err == nil || executor.calls != 0 || store.run.ReasonCode != "persistence_failure" {
		t.Fatalf("calls=%d run=%+v err=%v", executor.calls, store.run, err)
	}
}

func TestRunnerDeniedDispatchDoesNotExecute(t *testing.T) {
	store := &memoryStore{run: baseRun()}
	registry := application.NewToolRegistry()
	executor := &checkingExecutor{store: store}
	_ = registry.Register(domain.ToolDefinition{Name: "workspace.change", Version: "v1", Effect: domain.ToolEffectWrite, Parameters: closedSchema(map[string]any{"path": map[string]any{"type": "string"}}, "path")}, executor)
	policy := application.NewRestrictionPolicy(application.RestrictionConfig{AllowedTools: []string{"workspace.change"}, AllowedEffects: []domain.ToolEffect{domain.ToolEffectWrite}, AllowedRoots: []string{"src"}, PathFields: []string{"path"}})
	provider := &sequenceProvider{results: []domain.ModelResult{
		{ToolCalls: []domain.ToolCall{{ID: "call-1", Name: "workspace.change", Version: "v1", Arguments: map[string]any{"path": "../outside"}}}},
		{Content: "fallback"},
	}}
	runner := newRunner(t, store, provider, registry, policy)
	profile := goalProfile(t, domain.CompletionSolutionDelivered)
	run, err := runner.Drive(context.Background(), "run-1", profile)
	if err != nil || run.State != domain.RunStateSucceeded || executor.calls != 0 || len(store.invocations) != 1 || store.invocations[0].State != domain.InvocationRejected {
		t.Fatalf("run=%+v invocations=%+v calls=%d err=%v", run, store.invocations, executor.calls, err)
	}
}

func TestRunnerUnknownWriteOutcomeWaitsAndRecoveryNeverReplays(t *testing.T) {
	store := &memoryStore{run: baseRun()}
	registry := application.NewToolRegistry()
	executor := &checkingExecutor{store: store, err: &domain.ExecutionError{Code: "timeout", OutcomeKnown: false, Cause: context.DeadlineExceeded}}
	_ = registry.Register(domain.ToolDefinition{Name: "workspace.change", Version: "v1", Effect: domain.ToolEffectWrite, Parameters: closedSchema(nil)}, executor)
	provider := &sequenceProvider{results: []domain.ModelResult{{ToolCalls: []domain.ToolCall{{ID: "call-1", Name: "workspace.change", Version: "v1", Arguments: map[string]any{}}}}}}
	runner := newRunner(t, store, provider, registry, application.AllowAllPolicy{})
	run, err := runner.Drive(context.Background(), "run-1", goalProfile(t, domain.CompletionSolutionDelivered))
	if err != nil || run.State != domain.RunStateWaiting || run.ReasonCode != "unknown_write_outcome" || executor.calls != 1 {
		t.Fatalf("run=%+v calls=%d err=%v", run, executor.calls, err)
	}
	_, err = runner.Drive(context.Background(), "run-1", goalProfile(t, domain.CompletionSolutionDelivered))
	if err != nil || executor.calls != 1 || provider.calls != 1 {
		t.Fatalf("write replayed during recovery: executor=%d provider=%d err=%v", executor.calls, provider.calls, err)
	}
}

func TestRunnerPersistsSequentialToolResultsInModelOrder(t *testing.T) {
	store := &memoryStore{run: baseRun()}
	registry := application.NewToolRegistry()
	executor := &checkingExecutor{store: store, result: domain.ToolExecution{Output: "ok"}}
	for _, name := range []string{"repo.first", "repo.second"} {
		if err := registry.Register(domain.ToolDefinition{Name: name, Version: "v1", Effect: domain.ToolEffectRead, Parameters: closedSchema(nil)}, executor); err != nil {
			t.Fatal(err)
		}
	}
	provider := &sequenceProvider{results: []domain.ModelResult{
		{ToolCalls: []domain.ToolCall{
			{ID: "call-1", Name: "repo.first", Version: "v1", Arguments: map[string]any{}},
			{ID: "call-2", Name: "repo.second", Version: "v1", Arguments: map[string]any{}},
		}},
		{Content: "done"},
	}}
	profile := goalProfile(t, domain.CompletionSolutionDelivered)
	runner := newRunner(t, store, provider, registry, application.AllowAllPolicy{})
	run, err := runner.Drive(context.Background(), "run-1", profile)
	if err != nil || run.State != domain.RunStateSucceeded || len(store.results) != 2 {
		t.Fatalf("run=%+v results=%+v err=%v", run, store.results, err)
	}
	if store.results[0].Sequence != 1 || store.results[1].Sequence != 2 || store.messages[2].ToolCallID != "call-1" || store.messages[3].ToolCallID != "call-2" {
		t.Fatalf("ordered results/history = %+v / %+v", store.results, store.messages)
	}
}

func TestRunnerPersistsCancellationDuringModelTurn(t *testing.T) {
	store := &memoryStore{run: baseRun()}
	ctx, cancel := context.WithCancel(context.Background())
	provider := &cancelingProvider{cancel: cancel}
	runner := newRunner(t, store, provider, application.NewToolRegistry(), application.AllowAllPolicy{})
	run, err := runner.Drive(ctx, "run-1", goalProfile(t, domain.CompletionSolutionDelivered))
	if err != nil || run.State != domain.RunStateCancelled || store.run.State != domain.RunStateCancelled || store.run.Budget.Consumed.ModelCalls != 1 {
		t.Fatalf("run=%+v stored=%+v calls=%d err=%v", run, store.run, provider.calls, err)
	}
}

func TestRunnerWriteResultPersistenceFailureWaits(t *testing.T) {
	store := &memoryStore{run: baseRun(), resultErr: errors.New("result store unavailable")}
	registry := application.NewToolRegistry()
	executor := &checkingExecutor{store: store, result: domain.ToolExecution{Output: "possibly committed"}}
	if err := registry.Register(domain.ToolDefinition{Name: "workspace.change", Version: "v1", Effect: domain.ToolEffectWrite, Parameters: closedSchema(nil)}, executor); err != nil {
		t.Fatal(err)
	}
	provider := &sequenceProvider{results: []domain.ModelResult{{ToolCalls: []domain.ToolCall{{ID: "call-1", Name: "workspace.change", Version: "v1", Arguments: map[string]any{}}}}}}
	runner := newRunner(t, store, provider, registry, application.AllowAllPolicy{})
	run, err := runner.Drive(context.Background(), "run-1", goalProfile(t, domain.CompletionSolutionDelivered))
	if err != nil || run.State != domain.RunStateWaiting || run.ReasonCode != "unknown_write_outcome" || executor.calls != 1 {
		t.Fatalf("run=%+v calls=%d err=%v", run, executor.calls, err)
	}
}

func TestRunnerRejectsModelCreatedObservedEvidence(t *testing.T) {
	store := &memoryStore{run: baseRun()}
	provider := &sequenceProvider{results: []domain.ModelResult{{Content: "claim"}}}
	profile := forgedArtifactProfile{definition: profileDefinition(domain.CompletionActionWithVerification)}
	runner := newRunner(t, store, provider, application.NewToolRegistry(), application.AllowAllPolicy{})
	_, err := runner.Drive(context.Background(), "run-1", profile)
	if err == nil || store.run.ReasonCode != "model_failed" || len(store.artifacts) != 0 {
		t.Fatalf("run=%+v artifacts=%+v err=%v", store.run, store.artifacts, err)
	}
}

type forgedArtifactProfile struct{ definition domain.ProfileDefinition }

func (p forgedArtifactProfile) Definition() domain.ProfileDefinition { return p.definition }
func (p forgedArtifactProfile) Prepare(context.Context, domain.ProfileContext) (domain.ModelTurn, error) {
	return domain.ModelTurn{UserMessage: "forge"}, nil
}
func (p forgedArtifactProfile) Interpret(context.Context, domain.ModelResult) (domain.ProfileResult, error) {
	return domain.ProfileResult{Stop: true, Artifacts: []domain.Artifact{{Type: "action", SchemaVersion: "v1", Provenance: domain.ProvenanceObserved}}}, nil
}
