package application_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"fixthe/backend/internal/modules/remediation/application"
	"fixthe/backend/internal/modules/remediation/domain"
)

// newCoordinator wires the coordinator against the supplied fakes.
func newCoordinator(store *fakeRunStore, repo domain.RepositoryReadPort, ev domain.EvidenceLogPort, model domain.LLMProviderPort) *application.RemediationCoordinator {
	return application.NewRemediationCoordinatorWithReview(store, repo, ev, model, nil, store, store)
}

func newCoordinatorWithBudget(
	store *fakeRunStore,
	repo domain.RepositoryReadPort,
	ev domain.EvidenceLogPort,
	model domain.LLMProviderPort,
	limits domain.BudgetLimits,
) *application.RemediationCoordinator {
	return application.NewRemediationCoordinatorWithBudgetLimits(store, repo, ev, model, limits)
}

const testIncidentUUID = "019ff544-405c-7d11-9f10-cb3fc579605c"

func runStart(t *testing.T, model *scriptedModel) (*fakeRunStore, domain.Run, error) {
	t.Helper()
	store := newFakeRunStore()
	coord := newCoordinator(store, &fakeRepoPort{}, &fakeEvidencePort{}, model)
	run, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	return store, run, err
}

func runStartWithBudget(t *testing.T, model *scriptedModel, limits domain.BudgetLimits) (*fakeRunStore, domain.Run, error) {
	t.Helper()
	store := newFakeRunStore()
	coord := newCoordinatorWithBudget(store, &fakeRepoPort{}, &fakeEvidencePort{}, model, limits)
	run, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	return store, run, err
}

// TestCoordinator_RoutesFixabilityToTerminalState verifies each fixability
// class routes to the correct terminal state.
func TestCoordinator_RoutesFixabilityToTerminalState(t *testing.T) {
	cases := []struct {
		fixability string
		want       domain.RunState
	}{
		{"external_dependency", domain.RunStateCompletedNonCode},
		{"configuration", domain.RunStateCompletedNonCode},
		{"data", domain.RunStateCompletedNonCode},
		{"infrastructure", domain.RunStateCompletedNonCode},
		{"unsafe_to_automate", domain.RunStateBlockedManualReview},
	}

	for _, tc := range cases {
		t.Run(tc.fixability, func(t *testing.T) {
			model := &scriptedModel{responses: []string{diagnosisEnvelope(tc.fixability)}}
			store, run, err := runStart(t, model)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if run.IncidentID != testIncidentUUID {
				t.Errorf("IncidentID = %q, want UUID", run.IncidentID)
			}
			if store.state != tc.want {
				t.Errorf("final state = %s, want %s", store.state, tc.want)
			}
			if len(store.decisions) != 1 {
				t.Errorf("decisions recorded = %d, want 1", len(store.decisions))
			}
		})
	}
}

// TestCoordinator_CodeFixableReachesDiagnosisReady verifies the code-fixable
// path drives diagnosing → planning → diagnosis_ready_for_review.
func TestCoordinator_CodeFixableReachesDiagnosisReady(t *testing.T) {
	model := &scriptedModel{responses: []string{
		diagnosisEnvelope("code_fixable"),
		planEnvelope(),
	}}
	store, _, err := runStart(t, model)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.state != domain.RunStateDiagnosisReadyForReview {
		t.Fatalf("final state = %s, want diagnosis_ready_for_review", store.state)
	}
	if store.countTransitionsTo(domain.RunStatePlanning) != 1 {
		t.Errorf("expected one transition into planning")
	}
	if store.countTransitionsTo(domain.RunStateDiagnosisReadyForReview) != 1 {
		t.Errorf("expected one transition into diagnosis_ready_for_review")
	}
}

// TestCoordinator_InsufficientEvidenceBoundedLoop verifies the collect-more-
// context loop is bounded and terminates in blocked_manual_review.
func TestCoordinator_InsufficientEvidenceBoundedLoop(t *testing.T) {
	// Four insufficient diagnoses: loops 1,2,3 collect; the fourth is over the
	// bound (maxCollectLoops=3) and terminates.
	model := &scriptedModel{responses: []string{
		insufficientWithCollectEnvelope(),
		insufficientWithCollectEnvelope(),
		insufficientWithCollectEnvelope(),
		insufficientWithCollectEnvelope(),
	}}
	store, _, err := runStart(t, model)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.state != domain.RunStateBlockedManualReview {
		t.Fatalf("final state = %s, want blocked_manual_review", store.state)
	}
	if got := store.countTransitionsTo(domain.RunStateCollectingMoreContext); got != 3 {
		t.Errorf("collect loops = %d, want 3", got)
	}
	// Each collect iteration ran the model's single read tool.
	if len(store.invocations) != 3 {
		t.Errorf("tool invocations = %d, want 3", len(store.invocations))
	}
	if len(store.decisions) != 4 {
		t.Errorf("decisions = %d, want 4", len(store.decisions))
	}
}

// TestCoordinator_InsufficientThenCodeFixable verifies the loop can recover:
// collect once, then a code-fixable diagnosis reaches diagnosis_ready.
func TestCoordinator_InsufficientThenCodeFixable(t *testing.T) {
	model := &scriptedModel{responses: []string{
		insufficientWithCollectEnvelope(),
		diagnosisEnvelope("code_fixable"),
		planEnvelope(),
	}}
	store, _, err := runStart(t, model)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.state != domain.RunStateDiagnosisReadyForReview {
		t.Fatalf("final state = %s, want diagnosis_ready_for_review", store.state)
	}
	if got := store.countTransitionsTo(domain.RunStateCollectingMoreContext); got != 1 {
		t.Errorf("collect loops = %d, want 1", got)
	}
	if len(store.invocations) != 1 {
		t.Errorf("tool invocations = %d, want 1", len(store.invocations))
	}
}

// TestCoordinator_RequestToolThenDiagnosis verifies a mid-diagnosis tool
// request is executed via the gateway and recorded, then diagnosis proceeds.
func TestCoordinator_RequestToolThenDiagnosis(t *testing.T) {
	repo := &fakeRepoPort{}
	store := newFakeRunStore()
	model := &scriptedModel{responses: []string{
		requestToolEnvelope("repository.read_file", "main.go"),
		diagnosisEnvelope("code_fixable"),
		planEnvelope(),
	}}
	coord := newCoordinator(store, repo, &fakeEvidencePort{}, model)

	_, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.state != domain.RunStateDiagnosisReadyForReview {
		t.Fatalf("final state = %s, want diagnosis_ready_for_review", store.state)
	}
	if len(store.invocations) != 1 {
		t.Fatalf("tool invocations = %d, want 1", len(store.invocations))
	}
	if inv := store.invocations[0]; inv.Error != "" {
		t.Errorf("expected successful tool invocation, got error %q", inv.Error)
	}
	// The read_file tool must reach the repository adapter. preparing_context
	// no longer performs an eager ListTree read.
	if repo.calls != 1 {
		t.Errorf("repository adapter calls = %d, want 1", repo.calls)
	}
	if len(model.turns) != 3 || !strings.Contains(model.turns[2].UserMessage, "tool_observation") {
		t.Fatalf("planning turn lost diagnosis/tool context: %#v", model.turns)
	}
}

func TestCoordinator_ToolObservationFeedsFollowingTurn(t *testing.T) {
	model := &scriptedModel{responses: []string{
		requestToolEnvelope(application.ToolRepoReadFile, "main.go"),
		diagnosisEnvelope("external_dependency"),
	}}
	store, _, err := runStart(t, model)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.state != domain.RunStateCompletedNonCode {
		t.Fatalf("final state = %s, want completed_non_code", store.state)
	}
	if len(model.turns) != 2 {
		t.Fatalf("model turns = %d, want 2", len(model.turns))
	}
	second := model.turns[1].UserMessage
	if !strings.Contains(second, "tool_observation") || !strings.Contains(second, "repository.read_file") ||
		!strings.Contains(second, `"status":"success"`) {
		t.Fatalf("second turn lost tool observation: %s", second)
	}
	if len(model.turns[1].Messages) == 0 {
		t.Fatal("second turn did not receive conversation history")
	}
	if !strings.Contains(model.turns[1].Continuation, "tool_observation") || strings.Contains(model.turns[1].Continuation, "incident_id") {
		t.Fatalf("second continuation is not incremental: %q", model.turns[1].Continuation)
	}
}

func TestCoordinator_NormalizesNativeToolCallsThroughGateway(t *testing.T) {
	model := &nativeScriptedModel{results: []domain.ModelResult{
		{
			Provider: "openai", Model: "gpt-5.6", UsageTokensOut: 2,
			ToolCalls: []domain.ToolCall{{ID: "call-1", Name: application.ToolRepoReadFile, Arguments: map[string]interface{}{"path": "main.go"}}},
		},
		{Content: diagnosisEnvelope("external_dependency"), Provider: "openai", Model: "gpt-5.6", UsageTokensOut: 2},
	}}
	store := newFakeRunStore()
	coord := newCoordinator(store, &fakeRepoPort{}, &fakeEvidencePort{}, model)
	if _, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(store.invocations) != 1 || store.invocations[0].Error != "" {
		t.Fatalf("native invocation = %#v", store.invocations)
	}
	if len(model.turns) != 2 || len(model.turns[1].Messages) < 3 {
		t.Fatalf("native tool history = %#v", model.turns)
	}
	last := model.turns[1].Messages[len(model.turns[1].Messages)-1]
	if last.Role != "tool" || last.ToolCallID != "call-1" {
		t.Fatalf("last native message = %#v", last)
	}
	if strings.Contains(model.turns[1].Continuation, "tool_observation") || strings.Contains(model.turns[1].Continuation, "main.go") {
		t.Fatalf("native result repeated in continuation: %q", model.turns[1].Continuation)
	}
}

func TestCoordinator_ConnectorFailureIsModelVisibleAndSafe(t *testing.T) {
	model := &scriptedModel{responses: []string{
		requestToolEnvelope(application.ToolRepoReadFile, "main.go"),
		diagnosisEnvelope("external_dependency"),
	}}
	store := newFakeRunStore()
	repo := &failingReadRepoPort{err: fmt.Errorf("remote command failed: private-key=do-not-leak")}
	coord := newCoordinator(store, repo, &fakeEvidencePort{}, model)
	if _, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(model.turns) != 2 {
		t.Fatalf("model turns = %d, want 2", len(model.turns))
	}
	second := model.turns[1].UserMessage
	if !strings.Contains(second, "remote_execution") || !strings.Contains(second, `"retryable":true`) {
		t.Fatalf("second turn missing safe retryable error: %s", second)
	}
	if strings.Contains(second, "do-not-leak") || strings.Contains(second, "private-key") {
		t.Fatalf("second turn leaked raw connector error: %s", second)
	}
	if len(store.invocations) != 1 || store.invocations[0].Error != "remote_execution" {
		t.Fatalf("persisted invocation = %#v", store.invocations)
	}
}

func TestCoordinator_ConnectorUnavailableDoesNotBlockFirstTurn(t *testing.T) {
	model := &scriptedModel{responses: []string{diagnosisEnvelope("external_dependency")}}
	store := newFakeRunStore()
	repo := &failingListRepoPort{err: fmt.Errorf("remote command failed")}
	evidence := &failingSearchEvidencePort{err: fmt.Errorf("log connector unavailable")}
	coord := newCoordinator(store, repo, evidence, model)
	if _, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.state != domain.RunStateCompletedNonCode {
		t.Fatalf("final state = %s, want completed_non_code", store.state)
	}
	if model.calls != 1 {
		t.Fatalf("model calls = %d, want 1", model.calls)
	}
	if repo.calls != 0 || evidence.calls != 0 {
		t.Fatalf("preparation performed remote reads: repo=%d evidence=%d", repo.calls, evidence.calls)
	}
}

// TestCoordinator_StopReachesBlockedManualReview verifies a model stop routes
// to blocked_manual_review.
func TestCoordinator_StopReachesBlockedManualReview(t *testing.T) {
	model := &scriptedModel{responses: []string{stopEnvelope()}}
	store, _, err := runStart(t, model)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.state != domain.RunStateBlockedManualReview {
		t.Fatalf("final state = %s, want blocked_manual_review", store.state)
	}
}

func TestCoordinator_InvalidEnvelopeIsRetriedThenDiagnosed(t *testing.T) {
	model := &scriptedModel{responses: []string{
		`{"schemaVersion":"v1","kind":"diagnosis","diagnosis":"NPE in handler"}`,
		diagnosisEnvelope("external_dependency"),
	}}
	store, _, err := runStart(t, model)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.state != domain.RunStateCompletedNonCode {
		t.Fatalf("final state = %s, want completed_non_code", store.state)
	}
	if model.calls != 2 {
		t.Fatalf("model calls = %d, want 2", model.calls)
	}
	second := model.turns[1].UserMessage
	if !strings.Contains(second, "protocol_observation") || !strings.Contains(second, "invalid_envelope") {
		t.Fatalf("second turn missing protocol observation: %s", second)
	}
	if strings.Contains(second, "DiagnosisOutput") || strings.Contains(second, "cannot unmarshal") {
		t.Fatalf("second turn leaked decoder internals: %s", second)
	}
	if !strings.Contains(model.turns[1].Continuation, "protocol_observation") || strings.Contains(model.turns[1].Continuation, "incident_id") {
		t.Fatalf("protocol continuation is not incremental: %q", model.turns[1].Continuation)
	}
}

func TestCoordinator_InvalidEnvelopeRetryCountsAgainstModelBudget(t *testing.T) {
	limits := application.DefaultBudgetLimits()
	limits.MaxModelCalls = 1
	model := &scriptedModel{responses: []string{
		`not json`,
		diagnosisEnvelope("external_dependency"),
	}}
	store, run, err := runStartWithBudget(t, model, limits)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.State != domain.RunStateBudgetExhausted || store.state != domain.RunStateBudgetExhausted {
		t.Fatalf("final state = %s/%s, want budget_exhausted", run.State, store.state)
	}
	if model.calls != 2 {
		t.Fatalf("model calls = %d, want 2", model.calls)
	}
}

func TestCoordinator_ProviderErrorStillFailsRun(t *testing.T) {
	model := &scriptedModel{responses: []string{}}
	store, _, err := runStart(t, model)
	if err == nil {
		t.Fatal("expected error for provider failure")
	}
	if store.state != domain.RunStateFailed {
		t.Errorf("final state = %s, want failed", store.state)
	}
}

// TestCoordinator_ContextByteBudgetExhaustion verifies that metadata-only
// preparation does not consume repository bytes or block the first model turn.
func TestCoordinator_ContextByteBudgetExhaustion(t *testing.T) {
	limits := application.DefaultBudgetLimits()
	limits.MaxRepositoryBytes = 1
	model := &scriptedModel{responses: []string{diagnosisEnvelope("code_fixable"), planEnvelope()}}
	store, run, err := runStartWithBudget(t, model, limits)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.State != domain.RunStateDiagnosisReadyForReview || store.state != domain.RunStateDiagnosisReadyForReview {
		t.Fatalf("final state = %s/%s, want diagnosis_ready_for_review", run.State, store.state)
	}
	if model.calls != 2 {
		t.Fatalf("model calls = %d, want 2", model.calls)
	}
	if store.budget.RepositoryBytes != 0 {
		t.Fatalf("repository bytes = %d, want 0", store.budget.RepositoryBytes)
	}
}

// TestCoordinator_ModelCallBudgetExhaustion verifies the plan turn is recorded
// and terminates as budget_exhausted instead of reaching review.
func TestCoordinator_ModelCallBudgetExhaustion(t *testing.T) {
	limits := application.DefaultBudgetLimits()
	limits.MaxModelCalls = 1
	model := &scriptedModel{responses: []string{
		diagnosisEnvelope("code_fixable"),
		planEnvelope(),
	}}
	store, run, err := runStartWithBudget(t, model, limits)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.State != domain.RunStateBudgetExhausted || store.state != domain.RunStateBudgetExhausted {
		t.Fatalf("final state = %s/%s, want budget_exhausted", run.State, store.state)
	}
	if model.calls != 2 {
		t.Fatalf("model calls = %d, want 2", model.calls)
	}
	if store.budget.ModelCalls != 2 {
		t.Fatalf("persisted model calls = %d, want 2", store.budget.ModelCalls)
	}
	if store.countTransitionsTo(domain.RunStateDiagnosisReadyForReview) != 0 {
		t.Fatal("run reached diagnosis_ready_for_review despite model-call exhaustion")
	}
}

func TestCoordinator_ModelUsageMetadataIsPersisted(t *testing.T) {
	model := &scriptedModel{
		responses:      []string{diagnosisEnvelope("external_dependency")},
		provider:       "openai",
		model:          "gpt-5.6",
		usageTokensIn:  11,
		usageTokensOut: 17,
		usageCostCents: 3,
	}
	store, run, err := runStart(t, model)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.State != domain.RunStateCompletedNonCode {
		t.Fatalf("final state = %s, want completed_non_code", run.State)
	}
	if store.provider != "openai" || store.modelName != "gpt-5.6" {
		t.Fatalf("model metadata = %q/%q, want openai/gpt-5.6", store.provider, store.modelName)
	}
	if store.budget.ModelTokens != 28 {
		t.Fatalf("model tokens = %d, want 28", store.budget.ModelTokens)
	}
	if store.budget.ModelCostCents != 3 {
		t.Fatalf("model cost cents = %d, want 3", store.budget.ModelCostCents)
	}
}

func TestCoordinator_ModelCostBudgetExhaustion(t *testing.T) {
	limits := application.DefaultBudgetLimits()
	limits.MaxModelCostCents = 1
	model := &scriptedModel{
		responses:      []string{diagnosisEnvelope("external_dependency")},
		usageCostCents: 2,
	}
	store, run, err := runStartWithBudget(t, model, limits)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.State != domain.RunStateBudgetExhausted || store.state != domain.RunStateBudgetExhausted {
		t.Fatalf("final state = %s/%s, want budget_exhausted", run.State, store.state)
	}
	if store.budget.ModelCostCents != 2 {
		t.Fatalf("persisted model cost cents = %d, want 2", store.budget.ModelCostCents)
	}
}

func TestCoordinator_ElapsedBudgetExhaustion(t *testing.T) {
	limits := application.DefaultBudgetLimits()
	limits.MaxElapsed = 50 * time.Millisecond
	store := newFakeRunStore()
	model := &delayedModel{delay: 80 * time.Millisecond, responses: []string{diagnosisEnvelope("external_dependency")}}
	coord := newCoordinatorWithBudget(store, &fakeRepoPort{}, &fakeEvidencePort{}, model, limits)

	run, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.State != domain.RunStateCompletedNonCode || store.state != domain.RunStateCompletedNonCode {
		t.Fatalf("final state = %s/%s, want completed_non_code", run.State, store.state)
	}
	if model.calls != 1 {
		t.Fatalf("model calls = %d, want 1", model.calls)
	}
}

func TestCoordinator_ElapsedBudgetBlocksNextToolOperation(t *testing.T) {
	limits := application.DefaultBudgetLimits()
	limits.MaxElapsed = 50 * time.Millisecond
	store := newFakeRunStore()
	model := &delayedModel{delay: 80 * time.Millisecond, responses: []string{
		requestToolEnvelope(application.ToolRepoReadFile, "main.go"),
	}}
	coord := newCoordinatorWithBudget(store, &fakeRepoPort{}, &fakeEvidencePort{}, model, limits)

	run, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.State != domain.RunStateBudgetExhausted || store.state != domain.RunStateBudgetExhausted {
		t.Fatalf("final state = %s/%s, want budget_exhausted", run.State, store.state)
	}
	if model.calls != 1 {
		t.Fatalf("model calls = %d, want 1", model.calls)
	}
	if len(store.invocations) != 0 {
		t.Fatalf("elapsed admission allowed tool invocations: %#v", store.invocations)
	}
	if store.budget.ModelCalls != 1 || store.budget.ToolCalls != 0 {
		t.Fatalf("persisted budget = %#v", store.budget)
	}
}

type delayedModel struct {
	delay     time.Duration
	responses []string
	calls     int
}

func (m *delayedModel) Complete(context.Context, domain.ModelTurn) (domain.ModelResult, error) {
	time.Sleep(m.delay)
	if m.calls >= len(m.responses) {
		return domain.ModelResult{}, fmt.Errorf("delayed model exhausted after %d calls", m.calls)
	}
	content := m.responses[m.calls]
	m.calls++
	return domain.ModelResult{Content: content, Provider: "fake", Model: "slow", UsageTokensOut: 1}, nil
}

// TestCoordinator_ToolCallBudgetExhaustion verifies model-requested read tools
// are counted per run and stop the loop when the hard limit is crossed.
func TestCoordinator_ToolCallBudgetExhaustion(t *testing.T) {
	limits := application.DefaultBudgetLimits()
	limits.MaxToolCalls = 1
	model := &scriptedModel{responses: []string{
		requestToolEnvelope("repository.read_file", "main.go"),
		requestToolEnvelope("repository.read_file", "main.go"),
		diagnosisEnvelope("code_fixable"),
	}}
	store, run, err := runStartWithBudget(t, model, limits)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.State != domain.RunStateBudgetExhausted || store.state != domain.RunStateBudgetExhausted {
		t.Fatalf("final state = %s/%s, want budget_exhausted", run.State, store.state)
	}
	if len(store.invocations) != 2 {
		t.Fatalf("tool invocations = %d, want 2", len(store.invocations))
	}
	if store.budget.ToolCalls != 2 {
		t.Fatalf("persisted tool calls = %d, want 2", store.budget.ToolCalls)
	}
	if len(store.decisions) != 0 {
		t.Fatalf("decisions = %d, want 0 after tool-call exhaustion", len(store.decisions))
	}
}

func TestCoordinator_EmitsTerminalNotificationsAndPersistsPlans(t *testing.T) {
	t.Run("non_code", func(t *testing.T) {
		store, _, err := runStart(t, &scriptedModel{responses: []string{diagnosisEnvelope("configuration")}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(store.notifications) != 1 || store.notifications[0].Kind != application.NotificationNonCodeDiagnosed {
			t.Fatalf("notifications = %#v", store.notifications)
		}
	})
	t.Run("unsafe_manual_review", func(t *testing.T) {
		store, _, err := runStart(t, &scriptedModel{responses: []string{diagnosisEnvelope("unsafe_to_automate")}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(store.notifications) != 1 || store.notifications[0].Kind != application.NotificationManualReviewRequired {
			t.Fatalf("notifications = %#v", store.notifications)
		}
	})
	t.Run("insufficient_evidence", func(t *testing.T) {
		store, _, err := runStart(t, &scriptedModel{responses: []string{
			insufficientWithCollectEnvelope(),
			insufficientWithCollectEnvelope(),
			insufficientWithCollectEnvelope(),
			insufficientWithCollectEnvelope(),
		}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(store.notifications) != 1 || store.notifications[0].Kind != application.NotificationInsufficientEvidence {
			t.Fatalf("notifications = %#v", store.notifications)
		}
	})
	t.Run("code_fixable_persists_plan", func(t *testing.T) {
		store, _, err := runStart(t, &scriptedModel{responses: []string{diagnosisEnvelope("code_fixable"), planEnvelope()}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(store.plans) != 1 || store.recommendedID != "p1" || store.suggestedDiff == "" {
			t.Fatalf("plans=%#v recommended=%q diff=%q", store.plans, store.recommendedID, store.suggestedDiff)
		}
		if len(store.notifications) != 1 || store.notifications[0].Kind != application.NotificationDiagnosisReadyForReview {
			t.Fatalf("notifications = %#v", store.notifications)
		}
	})
	t.Run("failed_and_budget_do_not_notify", func(t *testing.T) {
		failedStore, _, err := runStart(t, &scriptedModel{responses: []string{`not json`}})
		if err == nil {
			t.Fatal("expected model error")
		}
		if len(failedStore.notifications) != 0 {
			t.Fatalf("failed notifications = %#v", failedStore.notifications)
		}
		limits := application.DefaultBudgetLimits()
		limits.MaxModelCalls = 1
		budgetStore, _, err := runStartWithBudget(t, &scriptedModel{responses: []string{diagnosisEnvelope("code_fixable"), planEnvelope()}}, limits)
		if err != nil {
			t.Fatalf("unexpected budget error: %v", err)
		}
		if budgetStore.state != domain.RunStateBudgetExhausted {
			t.Fatalf("state = %s, want budget_exhausted", budgetStore.state)
		}
		if len(budgetStore.notifications) != 0 {
			t.Fatalf("budget notifications = %#v", budgetStore.notifications)
		}
	})
}

type wiringLookup struct {
	identity application.IncidentIdentity
}

func (w wiringLookup) GetByNumber(context.Context, int64) (application.IncidentIdentity, error) {
	return w.identity, nil
}
func (w wiringLookup) GetByProjectNumber(context.Context, string, int64) (application.IncidentIdentity, error) {
	return w.identity, nil
}
func (w wiringLookup) GetByID(context.Context, string) (application.IncidentIdentity, error) {
	return w.identity, nil
}

type staticRemote string

func (s staticRemote) CredentialFreeRemoteURL(context.Context, string) (string, error) {
	return string(s), nil
}

func TestCoordinator_ResolvesCredentialFreeRefs(t *testing.T) {
	store := newFakeRunStore()
	repo := &fakeRepoPort{}
	evidence := &fakeEvidencePort{}
	model := &scriptedModel{responses: []string{
		requestToolEnvelope(application.ToolRepoListTree, ""),
		requestEvidenceSearchEnvelope(),
		diagnosisEnvelope("configuration"),
	}}
	const (
		projectID     = "019ff544-405c-7d21-9f10-cb3fc579605c"
		environmentID = "019ff544-405c-7d22-9f10-cb3fc579605c"
		sourceID      = "019ff544-405c-7d23-9f10-cb3fc579605c"
		remoteURL     = "https://git.example.internal/app.git"
	)
	coord := application.NewRemediationCoordinatorWithRuntime(
		store, repo, evidence, model,
		wiringLookup{identity: application.IncidentIdentity{
			ID: testIncidentUUID, ProjectID: projectID, EnvironmentID: environmentID, SourceID: sourceID,
			Priority: "P2", DeployedCommit: "abc123", LifecycleGeneration: 1,
		}},
		staticRemote(remoteURL),
		store, store,
	)
	if _, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if repo.lastRef.ProjectID != projectID || repo.lastRef.RemoteURL != remoteURL || repo.lastRef.Commit != "abc123" {
		t.Fatalf("RepoRef = %#v", repo.lastRef)
	}
	if evidence.lastScope.ProjectID != projectID || evidence.lastScope.EnvironmentID != environmentID || evidence.lastScope.SourceID != sourceID {
		t.Fatalf("EvidenceScope = %#v", evidence.lastScope)
	}
}

func TestCoordinator_SSHInspectHintsReachFirstTurnWithoutEagerRead(t *testing.T) {
	store := newFakeRunStore()
	repo := &fakeRepoPort{}
	evidence := &fakeEvidencePort{}
	inspect := &fakeInspectPort{}
	model := &scriptedModel{responses: []string{
		requestSSHInspectEnvelope("ls /var/log | grep app"),
		diagnosisEnvelope("configuration"),
	}}
	const (
		projectID     = "019ff544-405c-7d21-9f10-cb3fc579605c"
		environmentID = "019ff544-405c-7d22-9f10-cb3fc579605c"
		sourceID      = "019ff544-405c-7d23-9f10-cb3fc579605c"
	)
	coord := application.NewRemediationCoordinatorWithDynamicRuntime(
		store, repo, evidence, inspect, model,
		wiringLookup{identity: application.IncidentIdentity{
			ID: testIncidentUUID, ProjectID: projectID, EnvironmentID: environmentID, SourceID: sourceID,
			Priority: "P2", DeployedCommit: "abc123", LifecycleGeneration: 1,
		}},
		staticRemote("https://git.example.internal/app.git"),
		store, store, nil,
		staticSourceCaps{snapshot: domain.SourceCapabilitySnapshot{
			ProjectID: projectID, SourceID: sourceID, Kind: "ssh", Enabled: true, Supported: true,
			Declared: []string{"pull_collection"}, Version: 4,
			SSHHost: "logs.example.invalid", SSHUser: "app", SSHProjectFolder: "/srv/app", SSHLogPath: "/var/log",
		}},
		nil, nil,
	)
	if _, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if inspect.calls != 1 || evidence.calls != 0 || repo.calls != 0 {
		t.Fatalf("eager or unexpected reads: inspect=%d evidence=%d repo=%d", inspect.calls, evidence.calls, repo.calls)
	}
	if inspect.lastCommand != `'ls' '/var/log' | 'grep' 'app'` {
		t.Fatalf("inspect command = %q", inspect.lastCommand)
	}
	first := model.turns[0].UserMessage
	for _, want := range []string{"logs.example.invalid", "app", "/srv/app", "/var/log", "ssh.inspect", "never auto-tails"} {
		if !strings.Contains(first, want) {
			t.Fatalf("first turn missing %q: %s", want, first)
		}
	}
	var sawInspect bool
	for _, tool := range model.turns[0].Tools {
		switch tool.Name {
		case application.ToolSSHInspect:
			sawInspect = true
		case application.ToolEvidenceSearch, application.ToolEvidenceContext:
			t.Fatalf("SSH run advertised evidence tool %s", tool.Name)
		}
	}
	if !sawInspect {
		t.Fatal("first turn did not advertise ssh.inspect")
	}
}

type deferredDynamicHarnessModel struct {
	turns       []domain.ModelTurn
	dynamicName string
}

func (m *deferredDynamicHarnessModel) Complete(_ context.Context, req domain.ModelTurn) (domain.ModelResult, error) {
	turn := req
	turn.Messages = append([]domain.ModelMessage(nil), req.Messages...)
	turn.Tools = append([]domain.ToolDefinition(nil), req.Tools...)
	m.turns = append(m.turns, turn)

	var content string
	switch len(m.turns) {
	case 1:
		content = `{"schemaVersion":"v1","kind":"requestTool","requestTool":{` +
			`"toolName":"source.search_tools","parameters":{"query":"query errors","limit":1}}}`
	case 2:
		for _, tool := range req.Tools {
			if strings.HasPrefix(tool.Name, "mcp_") && strings.Contains(tool.Name, "query_errors") {
				m.dynamicName = tool.Name
				break
			}
		}
		if m.dynamicName == "" {
			return domain.ModelResult{}, fmt.Errorf("activated dynamic tool is not provider-visible")
		}
		content = fmt.Sprintf(`{"schemaVersion":"v1","kind":"requestTool","requestTool":{`+
			`"toolName":%q,"parameters":{"query":"timeout"}}}`, m.dynamicName)
	case 3:
		content = diagnosisEnvelope("code_fixable")
	case 4:
		content = planEnvelope()
	default:
		return domain.ModelResult{}, fmt.Errorf("unexpected model turn %d", len(m.turns))
	}

	return domain.ModelResult{
		Content: content, Provider: "fake", Model: "deferred-dynamic-harness",
		UsageTokensOut: 42, FinishReason: "stop",
	}, nil
}

func TestCoordinator_DeferredDynamicToolCompletesCodeFixableHarness(t *testing.T) {
	const (
		projectID     = "project-1"
		environmentID = "environment-1"
		sourceID      = "source-1"
	)
	store := newFakeRunStore()
	runtime := &catalogRuntime{
		discovered: mcpDiscovery("query_errors", "query_deployments"),
		result: domain.DynamicToolResult{
			Payload:        map[string]interface{}{"matches": []interface{}{"timeout at handler.go:42"}},
			BytesRetrieved: 24,
		},
	}
	model := &deferredDynamicHarnessModel{}
	coord := application.NewRemediationCoordinatorWithDynamicRuntime(
		store, &fakeRepoPort{}, &fakeEvidencePort{}, nil, model,
		wiringLookup{identity: application.IncidentIdentity{
			ID: testIncidentUUID, ProjectID: projectID, EnvironmentID: environmentID, SourceID: sourceID,
			Priority: "P2", DeployedCommit: "abc123", LifecycleGeneration: 1,
		}},
		staticRemote("https://git.example.internal/app.git"),
		store, store, nil,
		staticSourceCaps{snapshot: mcpSource()},
		runtime, &catalogPolicy{snapshot: mcpPolicy("query_errors", "query_deployments")},
	)

	run, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if run.State != domain.RunStateDiagnosisReadyForReview || store.state != domain.RunStateDiagnosisReadyForReview {
		t.Fatalf("final state = %s/%s, want diagnosis_ready_for_review", run.State, store.state)
	}
	if len(model.turns) != 4 {
		t.Fatalf("model turns = %d, want 4", len(model.turns))
	}
	if len(runtime.calls) != 1 || runtime.calls[0].Name != "query_errors" || runtime.calls[0].Arguments["query"] != "timeout" {
		t.Fatalf("dynamic runtime calls = %#v", runtime.calls)
	}
	if len(store.invocations) != 2 || store.invocations[0].ToolName != application.ToolSourceSearchTools || store.invocations[1].ToolName != model.dynamicName {
		t.Fatalf("tool invocation chain = %#v", store.invocations)
	}

	toolMetrics := func(tools []domain.ToolDefinition) (int, int) {
		schemaBytes := 0
		for _, tool := range tools {
			encoded, marshalErr := json.Marshal(tool.Parameters)
			if marshalErr != nil {
				t.Fatalf("marshal tool schema %s: %v", tool.Name, marshalErr)
			}
			schemaBytes += len(encoded)
		}
		return len(tools), schemaBytes
	}
	firstCount, firstSchemaBytes := toolMetrics(model.turns[0].Tools)
	secondCount, secondSchemaBytes := toolMetrics(model.turns[1].Tools)
	thirdCount, thirdSchemaBytes := toolMetrics(model.turns[2].Tools)
	planningCount, planningSchemaBytes := toolMetrics(model.turns[3].Tools)
	if secondCount != firstCount+1 || secondSchemaBytes <= firstSchemaBytes {
		t.Fatalf("activation metrics first=(%d,%d) second=(%d,%d)", firstCount, firstSchemaBytes, secondCount, secondSchemaBytes)
	}
	if thirdCount != secondCount || thirdSchemaBytes != secondSchemaBytes {
		t.Fatalf("activated schema changed before diagnosis: second=(%d,%d) third=(%d,%d)", secondCount, secondSchemaBytes, thirdCount, thirdSchemaBytes)
	}
	if planningCount != firstCount || planningSchemaBytes != firstSchemaBytes {
		t.Fatalf("planning did not reapply phase policy: first=(%d,%d) planning=(%d,%d)", firstCount, firstSchemaBytes, planningCount, planningSchemaBytes)
	}
	for _, tool := range model.turns[0].Tools {
		if strings.HasPrefix(tool.Name, "mcp_") {
			t.Fatalf("first turn eagerly advertised dynamic schema: %#v", tool)
		}
	}

	if !strings.Contains(model.turns[0].Continuation, "remediation bootstrap:") {
		t.Fatalf("first continuation lost bootstrap context: %q", model.turns[0].Continuation)
	}
	if !strings.Contains(model.turns[1].Continuation, "tool_observation") || strings.Contains(model.turns[1].Continuation, "remediation bootstrap:") {
		t.Fatalf("search continuation is not incremental: %q", model.turns[1].Continuation)
	}
	if !strings.Contains(model.turns[2].Continuation, "tool_observation") || strings.Contains(model.turns[2].Continuation, "source.search_tools") || strings.Contains(model.turns[2].Continuation, "remediation bootstrap:") {
		t.Fatalf("dynamic result continuation replayed prior context: %q", model.turns[2].Continuation)
	}
	if strings.Contains(model.turns[3].Continuation, "tool_observation") || strings.Contains(model.turns[3].Continuation, "remediation bootstrap:") {
		t.Fatalf("planning continuation replayed diagnosis context: %q", model.turns[3].Continuation)
	}
	if len(model.turns[3].Messages) <= len(model.turns[2].Messages) {
		t.Fatalf("planning history did not retain diagnosis: diagnosing=%d planning=%d", len(model.turns[2].Messages), len(model.turns[3].Messages))
	}
	if len(store.plans) != 1 || store.recommendedID != "p1" || store.suggestedDiff == "" {
		t.Fatalf("persisted review = plans=%#v recommended=%q diff=%q", store.plans, store.recommendedID, store.suggestedDiff)
	}
}

type staticSourceCaps struct {
	snapshot domain.SourceCapabilitySnapshot
}

func (s staticSourceCaps) ResolveSourceCapability(context.Context, string, string) (domain.SourceCapabilitySnapshot, error) {
	return s.snapshot, nil
}

func TestCoordinator_FailsWhenRemoteCannotBeResolved(t *testing.T) {
	store := newFakeRunStore()
	coord := application.NewRemediationCoordinatorWithRuntime(
		store, &fakeRepoPort{}, &fakeEvidencePort{}, &scriptedModel{responses: []string{diagnosisEnvelope("configuration")}},
		wiringLookup{identity: application.IncidentIdentity{ID: testIncidentUUID, ProjectID: testProjectID, LifecycleGeneration: 1}},
		staticRemote(""),
		store, store,
	)
	_, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	if err == nil {
		t.Fatal("expected empty remote URL to fail the run")
	}
	if store.state != domain.RunStateFailed {
		t.Fatalf("state = %s, want failed", store.state)
	}
}
