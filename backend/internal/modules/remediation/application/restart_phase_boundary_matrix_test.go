package application_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"mendry/backend/internal/modules/remediation/application"
	"mendry/backend/internal/modules/remediation/domain"
)

type restartEffectFrontier int

const (
	restartNoEffects restartEffectFrontier = iota
	restartPatchSucceeded
	restartValidationFailed
	restartValidationPassed
	restartPublicationSucceeded
)

type restartMatrixFixture struct {
	store       *fakeRunStore
	checkpoints *fakeCheckpointStore
	effects     *fakeLifecycleStore
}

func buildRestartMatrixFixture(t *testing.T) restartMatrixFixture {
	t.Helper()
	coord, store, checkpoints, effects, workspace, validation, _ := setupLifecycleCoordinator(t, []string{
		insufficientWithCollectEnvelope(), diagnosisEnvelope("code_fixable"), planEnvelope(),
		patchRequestEnvelope("diff --git a/main.go b/main.go\n"), patchCompleteEnvelope(),
		validationRequestEnvelope("unit"), validationAssessmentEnvelope(false),
		patchRequestEnvelope("diff --git a/main.go b/main.go\n+changed\n"), patchCompleteEnvelope(),
		validationRequestEnvelope("unit"), validationAssessmentEnvelope(true),
	})
	workspace.results = []domain.PatchResult{
		{Applied: true, ArtifactRef: "sha256:patch-one", ContentHash: fmt.Sprintf("%064x", 1), ResultTreeHash: "tree-one", ChangedFiles: []string{"main.go"}, BytesRetrieved: 4, Summary: "patch applied"},
		{Applied: true, ArtifactRef: "sha256:patch-two", ContentHash: fmt.Sprintf("%064x", 2), ResultTreeHash: "tree-two", ChangedFiles: []string{"main.go"}, BytesRetrieved: 12, Summary: "revised patch applied"},
	}
	validation.results = []domain.ValidationResult{
		{Passed: false, ExitCode: 1, OutputArtifactRef: "sha256:failed-validation", OutputHash: fmt.Sprintf("%064x", 3), BytesRetrieved: 20, Summary: "validation failed"},
		{Passed: true, ExitCode: 0, OutputArtifactRef: "sha256:passed-validation", OutputHash: fmt.Sprintf("%064x", 4), BytesRetrieved: 20, Summary: "validation passed"},
	}
	startLifecyclePlan(t, coord, store)
	if _, err := coord.ApplyPlan(context.Background(), "run-1", "p1"); err != nil {
		t.Fatalf("build restart fixture: ApplyPlan() error = %v", err)
	}
	return restartMatrixFixture{store: store, checkpoints: checkpoints, effects: effects}
}

func restartCheckpoint(t *testing.T, checkpoints []domain.WorkingMemoryCheckpointV1, name string) domain.WorkingMemoryCheckpointV1 {
	t.Helper()
	var matches []domain.WorkingMemoryCheckpointV1
	for _, checkpoint := range checkpoints {
		switch name {
		case "diagnosing-first", "diagnosing-collect-entry", "collecting", "diagnosing-last":
			// checkpoint schema 将 collecting durable substate 归一为 diagnosing；
			// 顺序分别对应 diagnosing/collecting transition 边界。
			if checkpoint.Phase == string(domain.RunStateDiagnosing) {
				matches = append(matches, checkpoint)
			}
		case "planning-first", "planning-last":
			if checkpoint.Phase == string(domain.RunStatePlanning) {
				matches = append(matches, checkpoint)
			}
		case "patch-before-validation":
			if checkpoint.Phase == string(domain.RunStateValidating) {
				break
			}
			if checkpoint.Phase == string(domain.RunStatePatching) && len(checkpoint.Artifacts) > 0 {
				matches = append(matches, checkpoint)
			}
		case "validation-failed":
			if checkpoint.Phase == string(domain.RunStateValidating) && checkpoint.Validation != nil && !checkpoint.Validation.Passed {
				matches = append(matches, checkpoint)
			}
		case "validation-passed":
			if checkpoint.Phase == string(domain.RunStateValidating) && checkpoint.Validation != nil && checkpoint.Validation.Passed {
				matches = append(matches, checkpoint)
			}
		case "publishing":
			if checkpoint.Phase == string(domain.RunStatePublishing) && checkpoint.Publication != nil {
				matches = append(matches, checkpoint)
			}
		}
	}
	if len(matches) == 0 {
		t.Fatalf("fixture has no %s checkpoint", name)
	}
	if name == "planning-first" || name == "diagnosing-first" {
		return matches[0]
	}
	if name == "diagnosing-collect-entry" {
		if len(matches) < 2 {
			t.Fatalf("fixture has %d diagnosing checkpoints, want at least 2", len(matches))
		}
		return matches[1]
	}
	if name == "collecting" {
		if len(matches) < 3 {
			t.Fatalf("fixture has %d diagnosing checkpoints, want at least 3", len(matches))
		}
		return matches[2]
	}
	return matches[len(matches)-1]
}

func restartBudget(checkpoint domain.WorkingMemoryCheckpointV1) domain.BudgetCounters {
	var counters domain.BudgetCounters
	for _, entry := range checkpoint.Budget.Consumed {
		counters.ElapsedSeconds += entry.Amount.ElapsedSeconds
		counters.ModelCalls += entry.Amount.ModelCalls
		counters.ModelCostCents += entry.Amount.ModelCostCents
		counters.ToolCalls += entry.Amount.ToolCalls
		counters.EvidenceBytes += entry.Amount.EvidenceBytes
		counters.RepositoryBytes += entry.Amount.RepositoryBytes
	}
	return counters
}

func seedRestartWindow(t *testing.T, fixture restartMatrixFixture, checkpoint domain.WorkingMemoryCheckpointV1, state domain.RunState, afterTransition bool, transitionEffect domain.Effect) int {
	t.Helper()
	fixture.store.state = state
	fixture.store.created.State = state
	fixture.store.created.Version = checkpoint.ObservedRunVersion
	fixture.store.budget = restartBudget(checkpoint)
	toolCount := int(fixture.store.budget.ToolCalls)
	if toolCount < len(fixture.store.invocations) {
		fixture.store.invocations = append([]domain.ToolInvocation(nil), fixture.store.invocations[:toolCount]...)
	}
	if afterTransition {
		fixture.store.created.Version++
		if hasRestartBudgetEffect(transitionEffect) {
			fixture.store.created.Version++
			fixture.store.budget.ModelCalls += int64(transitionEffect.ModelCalls)
			fixture.store.budget.ModelTokens += transitionEffect.ModelTokensIn + transitionEffect.ModelTokensOut
			fixture.store.budget.ModelCostCents += transitionEffect.ModelCostCents
			fixture.store.budget.ToolCalls += int64(transitionEffect.ToolCalls)
			fixture.store.budget.EvidenceBytes += transitionEffect.EvidenceBytes
			fixture.store.budget.RepositoryBytes += transitionEffect.RepositoryBytes
		}
	}
	fixture.store.transitions = nil
	fixture.store.effects = nil
	fixture.store.notifications = nil
	snapshot := domain.CheckpointSnapshot{
		RunID: checkpoint.RunID, SeriesID: checkpoint.SeriesID, ContextVersion: checkpoint.ContextVersion,
		ObservedRunVersion: checkpoint.ObservedRunVersion, Sequence: checkpoint.Sequence, Phase: checkpoint.Phase,
		Checkpoint: checkpoint, NeedsRebuild: afterTransition,
	}
	fixture.checkpoints.byRun = map[string]domain.CheckpointSnapshot{checkpoint.RunID: snapshot}
	return len(fixture.checkpoints.appends)
}

func seedRestartWithoutCheckpoint(fixture restartMatrixFixture, state domain.RunState, afterTransition bool, transitionEffect domain.Effect) (baseline int, seededVersion int64) {
	fixture.store.state = state
	fixture.store.created.State = state
	fixture.store.created.Version = 2 // queued -> preparing_context claim is already durable.
	fixture.store.budget = domain.BudgetCounters{}
	fixture.store.invocations = nil
	if afterTransition {
		fixture.store.created.Version++
		if hasRestartBudgetEffect(transitionEffect) {
			fixture.store.created.Version++
			fixture.store.budget.ModelCalls += int64(transitionEffect.ModelCalls)
			fixture.store.budget.ModelTokens += transitionEffect.ModelTokensIn + transitionEffect.ModelTokensOut
			fixture.store.budget.ModelCostCents += transitionEffect.ModelCostCents
			fixture.store.budget.ToolCalls += int64(transitionEffect.ToolCalls)
			fixture.store.budget.EvidenceBytes += transitionEffect.EvidenceBytes
			fixture.store.budget.RepositoryBytes += transitionEffect.RepositoryBytes
		}
	}
	fixture.store.transitions = nil
	fixture.store.effects = nil
	fixture.store.notifications = nil
	fixture.checkpoints.byRun = map[string]domain.CheckpointSnapshot{}
	return len(fixture.checkpoints.appends), fixture.store.created.Version
}

func hasRestartBudgetEffect(effect domain.Effect) bool {
	return effect.ModelCalls != 0 || effect.ModelTokensIn != 0 || effect.ModelTokensOut != 0 ||
		effect.ModelCostCents != 0 || effect.ToolCalls != 0 || effect.EvidenceBytes != 0 || effect.RepositoryBytes != 0
}

func seedRestartEffects(t *testing.T, source *fakeLifecycleStore, frontier restartEffectFrontier) *fakeLifecycleStore {
	t.Helper()
	target := &fakeLifecycleStore{effects: make(map[string]domain.LifecycleEffect)}
	if frontier == restartNoEffects {
		return target
	}
	ordered, err := source.ListLifecycleEffects(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("list fixture effects: %v", err)
	}
	for _, effect := range ordered {
		include := false
		switch frontier {
		case restartPatchSucceeded:
			include = effect.Kind == domain.LifecycleEffectWorkspace || effect.Kind == domain.LifecycleEffectPatch
		case restartValidationFailed:
			include = effect.Kind == domain.LifecycleEffectWorkspace || effect.Kind == domain.LifecycleEffectPatch ||
				(effect.Kind == domain.LifecycleEffectValidation && !effect.ValidationPassed)
		case restartValidationPassed:
			include = effect.Kind != domain.LifecycleEffectPublication
		case restartPublicationSucceeded:
			include = true
		}
		if include {
			target.effects[lifecycleEffectMapKey(effect.RunID, effect.Kind, effect.IdempotencyKey)] = effect
		}
		if frontier == restartPatchSucceeded && effect.Kind == domain.LifecycleEffectPatch {
			break
		}
		if frontier == restartValidationFailed && effect.Kind == domain.LifecycleEffectValidation && !effect.ValidationPassed {
			break
		}
	}
	return target
}

func freshRestartCoordinator(store *fakeRunStore, checkpoints *fakeCheckpointStore, effects *fakeLifecycleStore, responses []string) (*application.RemediationCoordinator, *scriptedModel, *fakeRepoPort, *lifecycleWorkspaceFake, *lifecycleValidationFake, *lifecyclePublicationFake) {
	model := &scriptedModel{responses: responses}
	repo := &fakeRepoPort{}
	coord := newCoordinator(store, repo, &fakeEvidencePort{}, model)
	workspace := &lifecycleWorkspaceFake{}
	validation := &lifecycleValidationFake{}
	publication := &lifecyclePublicationFake{}
	coord.SetCheckpointStore(checkpoints)
	coord.SetLifecycleStore(effects)
	coord.SetWorkspacePort(workspace)
	coord.SetValidationPort(validation)
	coord.SetPublicationPort(publication)
	coord.SetValidationCommandVersions(map[string]int64{"unit": 1})
	coord.SetLifecyclePublicationPolicy(application.LifecyclePublicationPolicy{TargetBranch: "main", BranchPrefix: "hotfix/remediation"})
	return coord, model, repo, workspace, validation, publication
}

func TestResilientRestartPhaseBoundaryMatrix(t *testing.T) {
	type matrixCase struct {
		name                     string
		checkpoint               string
		withoutCheckpoint        bool
		state                    domain.RunState
		afterTransition          bool
		transitionEffect         domain.Effect
		effects                  restartEffectFrontier
		responses                []string
		applyPlan                bool
		wantState                domain.RunState
		wantModelCalls           int
		wantRepoCalls            int
		wantWorkspaceCalls       int
		wantPatchCalls           int
		wantValidationCalls      int
		wantPublishCalls         int
		terminalNoRebuild        bool
		wantFirstCheckpointPhase domain.RunState
	}
	cases := []matrixCase{
		{name: "preparing before diagnosis transition", withoutCheckpoint: true, state: domain.RunStatePreparingContext, responses: []string{diagnosisEnvelope("code_fixable"), planEnvelope()}, wantState: domain.RunStateDiagnosisReadyForReview, wantModelCalls: 2, wantFirstCheckpointPhase: domain.RunStateDiagnosing},
		{name: "preparing after diagnosis transition", withoutCheckpoint: true, state: domain.RunStateDiagnosing, afterTransition: true, transitionEffect: domain.Effect{RepositoryBytes: 1}, responses: []string{diagnosisEnvelope("code_fixable"), planEnvelope()}, wantState: domain.RunStateDiagnosisReadyForReview, wantModelCalls: 2, wantFirstCheckpointPhase: domain.RunStateDiagnosing},
		{name: "diagnosis before collecting transition", checkpoint: "diagnosing-collect-entry", state: domain.RunStateDiagnosing, responses: []string{insufficientWithCollectEnvelope(), diagnosisEnvelope("code_fixable"), planEnvelope()}, wantState: domain.RunStateDiagnosisReadyForReview, wantModelCalls: 3, wantRepoCalls: 1},
		{name: "diagnosis after collecting transition", checkpoint: "diagnosing-collect-entry", state: domain.RunStateCollectingMoreContext, afterTransition: true, transitionEffect: domain.Effect{ModelCalls: 1}, responses: []string{insufficientWithCollectEnvelope(), diagnosisEnvelope("code_fixable"), planEnvelope()}, wantState: domain.RunStateDiagnosisReadyForReview, wantModelCalls: 3, wantRepoCalls: 1, wantFirstCheckpointPhase: domain.RunStateDiagnosing},
		{name: "collecting before diagnosis transition", checkpoint: "collecting", state: domain.RunStateCollectingMoreContext, responses: []string{diagnosisEnvelope("code_fixable"), planEnvelope()}, wantState: domain.RunStateDiagnosisReadyForReview, wantModelCalls: 2, wantFirstCheckpointPhase: domain.RunStateDiagnosing},
		{name: "collecting after diagnosis transition", checkpoint: "collecting", state: domain.RunStateDiagnosing, afterTransition: true, responses: []string{diagnosisEnvelope("code_fixable"), planEnvelope()}, wantState: domain.RunStateDiagnosisReadyForReview, wantModelCalls: 2},
		{name: "diagnosis before planning transition", checkpoint: "diagnosing-last", state: domain.RunStateDiagnosing, responses: []string{diagnosisEnvelope("code_fixable"), planEnvelope()}, wantState: domain.RunStateDiagnosisReadyForReview, wantModelCalls: 2},
		{name: "diagnosis after planning transition", checkpoint: "diagnosing-last", state: domain.RunStatePlanning, afterTransition: true, transitionEffect: domain.Effect{ModelCalls: 1}, responses: []string{planEnvelope()}, wantState: domain.RunStateDiagnosisReadyForReview, wantModelCalls: 1},
		{name: "planning before transition", checkpoint: "planning-first", state: domain.RunStatePlanning, responses: []string{planEnvelope()}, wantState: domain.RunStateDiagnosisReadyForReview, wantModelCalls: 1},
		{name: "planning after transition is terminal review", checkpoint: "planning-first", state: domain.RunStateDiagnosisReadyForReview, afterTransition: true, transitionEffect: domain.Effect{ModelCalls: 1}, wantState: domain.RunStateDiagnosisReadyForReview, terminalNoRebuild: true},
		{name: "ApplyPlan before transition", checkpoint: "planning-last", state: domain.RunStateDiagnosisReadyForReview, applyPlan: true, responses: []string{patchRequestEnvelope("diff --git a/main.go b/main.go\n"), patchCompleteEnvelope(), validationRequestEnvelope("unit"), validationAssessmentEnvelope(true)}, wantState: domain.RunStateAwaitingHumanReview, wantModelCalls: 4, wantWorkspaceCalls: 1, wantPatchCalls: 1, wantValidationCalls: 1, wantPublishCalls: 1},
		{name: "ApplyPlan after transition", checkpoint: "planning-last", state: domain.RunStatePatching, afterTransition: true, responses: []string{patchRequestEnvelope("diff --git a/main.go b/main.go\n"), patchCompleteEnvelope(), validationRequestEnvelope("unit"), validationAssessmentEnvelope(true)}, wantState: domain.RunStateAwaitingHumanReview, wantModelCalls: 4, wantWorkspaceCalls: 1, wantPatchCalls: 1, wantValidationCalls: 1, wantPublishCalls: 1},
		{name: "patching before validation transition", checkpoint: "patch-before-validation", state: domain.RunStatePatching, effects: restartPatchSucceeded, responses: []string{validationRequestEnvelope("unit"), validationAssessmentEnvelope(true)}, wantState: domain.RunStateAwaitingHumanReview, wantModelCalls: 2, wantValidationCalls: 1, wantPublishCalls: 1},
		{name: "patching after validation transition", checkpoint: "patch-before-validation", state: domain.RunStateValidating, afterTransition: true, effects: restartPatchSucceeded, responses: []string{validationRequestEnvelope("unit"), validationAssessmentEnvelope(true)}, wantState: domain.RunStateAwaitingHumanReview, wantModelCalls: 2, wantValidationCalls: 1, wantPublishCalls: 1},
		{name: "validation before repair transition", checkpoint: "validation-failed", state: domain.RunStateValidating, effects: restartValidationFailed, responses: []string{validationAssessmentEnvelope(false), patchRequestEnvelope("diff --git a/main.go b/main.go\n+restart\n"), patchCompleteEnvelope(), validationRequestEnvelope("unit"), validationAssessmentEnvelope(true)}, wantState: domain.RunStateAwaitingHumanReview, wantModelCalls: 5, wantPatchCalls: 1, wantValidationCalls: 1, wantPublishCalls: 1},
		{name: "validation after repair transition", checkpoint: "validation-failed", state: domain.RunStatePatching, afterTransition: true, transitionEffect: domain.Effect{ModelCalls: 1}, effects: restartValidationFailed, responses: []string{patchRequestEnvelope("diff --git a/main.go b/main.go\n+restart\n"), patchCompleteEnvelope(), validationRequestEnvelope("unit"), validationAssessmentEnvelope(true)}, wantState: domain.RunStateAwaitingHumanReview, wantModelCalls: 4, wantPatchCalls: 1, wantValidationCalls: 1, wantPublishCalls: 1},
		{name: "validation before publication transition", checkpoint: "validation-passed", state: domain.RunStateValidating, effects: restartValidationPassed, wantState: domain.RunStateAwaitingHumanReview, wantPublishCalls: 1},
		{name: "validation after publication transition", checkpoint: "validation-passed", state: domain.RunStatePublishing, afterTransition: true, effects: restartValidationPassed, wantState: domain.RunStateAwaitingHumanReview, wantPublishCalls: 1},
		{name: "publication before human review transition", checkpoint: "publishing", state: domain.RunStatePublishing, effects: restartPublicationSucceeded, wantState: domain.RunStateAwaitingHumanReview},
		{name: "publication after human review transition", checkpoint: "publishing", state: domain.RunStateAwaitingHumanReview, afterTransition: true, effects: restartPublicationSucceeded, wantState: domain.RunStateAwaitingHumanReview, terminalNoRebuild: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := buildRestartMatrixFixture(t)
			hasCheckpoint := !tc.withoutCheckpoint
			var (
				checkpoint    domain.WorkingMemoryCheckpointV1
				baseline      int
				seededVersion int64
			)
			if hasCheckpoint {
				checkpoint = restartCheckpoint(t, fixture.checkpoints.appends, tc.checkpoint)
				baseline = seedRestartWindow(t, fixture, checkpoint, tc.state, tc.afterTransition, tc.transitionEffect)
				seededVersion = fixture.store.created.Version
				seeded := fixture.checkpoints.byRun[checkpoint.RunID]
				if tc.afterTransition {
					if fixture.store.created.Version <= checkpoint.ObservedRunVersion || !seeded.NeedsRebuild {
						t.Fatalf("after-transition window is not stale: run version=%d checkpoint version=%d rebuild=%t", fixture.store.created.Version, checkpoint.ObservedRunVersion, seeded.NeedsRebuild)
					}
				} else if fixture.store.created.Version != checkpoint.ObservedRunVersion || seeded.NeedsRebuild {
					t.Fatalf("before-transition window does not match source: run version=%d checkpoint version=%d rebuild=%t", fixture.store.created.Version, checkpoint.ObservedRunVersion, seeded.NeedsRebuild)
				}
			} else {
				baseline, seededVersion = seedRestartWithoutCheckpoint(fixture, tc.state, tc.afterTransition, tc.transitionEffect)
			}
			effects := seedRestartEffects(t, fixture.effects, tc.effects)
			coord, model, repo, workspace, validation, publication := freshRestartCoordinator(fixture.store, fixture.checkpoints, effects, tc.responses)

			var (
				run domain.Run
				err error
			)
			if tc.applyPlan {
				run, err = coord.ApplyPlan(context.Background(), "run-1", "p1")
			} else {
				run, err = coord.Resume(context.Background(), "run-1")
			}
			if err != nil {
				t.Fatalf("restart error = %v", err)
			}
			if run.State != tc.wantState {
				t.Fatalf("state = %s, want %s", run.State, tc.wantState)
			}
			if model.calls != tc.wantModelCalls {
				t.Fatalf("fresh model calls = %d, want %d", model.calls, tc.wantModelCalls)
			}
			if hasCheckpoint && (tc.state == domain.RunStateDiagnosing || tc.state == domain.RunStatePlanning) && model.calls > 0 &&
				!strings.Contains(model.turns[0].UserMessage, `"kind":"working_memory_reconstruction"`) {
				t.Fatalf("fresh analysis model did not receive durable reconstruction: %s", model.turns[0].UserMessage)
			}
			if tc.checkpoint == "collecting" && model.calls > 0 {
				firstTurn := model.turns[0].UserMessage
				if !strings.Contains(firstTurn, `"completedActions":[{"actionRef":"action:repository:1","capability":"repository","outcome":"succeeded"`) {
					t.Fatalf("tool-complete collecting reconstruction omitted durable action progress: %s", firstTurn)
				}
			}
			if repo.calls != tc.wantRepoCalls {
				t.Fatalf("fresh repository calls = %d, want %d", repo.calls, tc.wantRepoCalls)
			}
			if workspace.ensures != tc.wantWorkspaceCalls || workspace.patches != tc.wantPatchCalls || validation.calls != tc.wantValidationCalls || publication.calls != tc.wantPublishCalls {
				t.Fatalf("fresh external calls workspace/patch/validation/publication = %d/%d/%d/%d, want %d/%d/%d/%d", workspace.ensures, workspace.patches, validation.calls, publication.calls, tc.wantWorkspaceCalls, tc.wantPatchCalls, tc.wantValidationCalls, tc.wantPublishCalls)
			}
			newCheckpoints := fixture.checkpoints.appends[baseline:]
			if tc.wantFirstCheckpointPhase != "" {
				if len(newCheckpoints) == 0 || newCheckpoints[0].Phase != string(tc.wantFirstCheckpointPhase) {
					t.Fatalf("first recovery checkpoint = %#v, want phase %s", newCheckpoints, tc.wantFirstCheckpointPhase)
				}
			}
			if tc.afterTransition && !tc.terminalNoRebuild {
				if len(newCheckpoints) == 0 {
					t.Fatalf("transition-after recovery appended no checkpoint")
				}
				if hasCheckpoint && newCheckpoints[0].ObservedRunVersion <= checkpoint.ObservedRunVersion {
					t.Fatalf("stale checkpoint was not rebuilt: old version=%d new=%#v", checkpoint.ObservedRunVersion, newCheckpoints)
				}
				if !hasCheckpoint && newCheckpoints[0].ObservedRunVersion < seededVersion {
					t.Fatalf("initial boundary checkpoint version = %d, want >= seeded %d", newCheckpoints[0].ObservedRunVersion, seededVersion)
				}
				expectedPhase := tc.state
				if tc.wantFirstCheckpointPhase != "" {
					expectedPhase = tc.wantFirstCheckpointPhase
				}
				if newCheckpoints[0].Phase != string(expectedPhase) {
					t.Fatalf("first rebuilt checkpoint phase = %s, want recovery phase %s", newCheckpoints[0].Phase, expectedPhase)
				}
			}
			if tc.terminalNoRebuild && len(newCheckpoints) != 0 {
				t.Fatalf("terminal human-review restart appended checkpoints: %#v", newCheckpoints)
			}
			for _, appended := range newCheckpoints {
				if appended.RunID != "run-1" || appended.SeriesID != "series-1" || appended.ContextVersion != 1 || appended.ObservedRunVersion <= 0 {
					t.Fatalf("checkpoint identity = %#v", appended)
				}
			}
		})
	}
}

// TestResilientRestartRejectsMissingRequiredCheckpoint 证明 no-checkpoint 兼容
// 仅限首轮模型前的 initial boundary；其余 analysis/lifecycle phase 均 fail closed。
func TestResilientRestartRejectsMissingRequiredCheckpoint(t *testing.T) {
	t.Run("diagnosing after model usage", func(t *testing.T) {
		fixture := buildRestartMatrixFixture(t)
		seedRestartWithoutCheckpoint(fixture, domain.RunStateDiagnosing, false, domain.Effect{})
		fixture.store.budget.ModelCalls = 1
		model := &scriptedModel{responses: []string{diagnosisEnvelope("code_fixable")}}
		coord := newCoordinator(fixture.store, &fakeRepoPort{}, &fakeEvidencePort{}, model)
		coord.SetCheckpointStore(fixture.checkpoints)

		if _, err := coord.Resume(context.Background(), "run-1"); !errors.Is(err, domain.ErrCheckpointNotFound) {
			t.Fatalf("Resume() error = %v, want checkpoint not found", err)
		}
		if model.calls != 0 {
			t.Fatalf("model calls = %d, want 0", model.calls)
		}
	})

	t.Run("active lifecycle", func(t *testing.T) {
		fixture := buildRestartMatrixFixture(t)
		seedRestartWithoutCheckpoint(fixture, domain.RunStatePatching, false, domain.Effect{})
		effects := seedRestartEffects(t, fixture.effects, restartPatchSucceeded)
		coord, model, repo, workspace, validation, publication := freshRestartCoordinator(
			fixture.store, fixture.checkpoints, effects, nil,
		)

		if _, err := coord.Resume(context.Background(), "run-1"); !errors.Is(err, application.ErrLifecycleUnavailable) {
			t.Fatalf("Resume() error = %v, want lifecycle unavailable", err)
		}
		if model.calls != 0 || repo.calls != 0 || workspace.ensures != 0 || workspace.patches != 0 || validation.calls != 0 || publication.calls != 0 {
			t.Fatalf("missing checkpoint invoked adapters: model/repo/workspace/patch/validation/publication = %d/%d/%d/%d/%d/%d", model.calls, repo.calls, workspace.ensures, workspace.patches, validation.calls, publication.calls)
		}
	})

	t.Run("incomplete lifecycle policy snapshot", func(t *testing.T) {
		fixture := buildRestartMatrixFixture(t)
		checkpoint := restartCheckpoint(t, fixture.checkpoints.appends, "planning-last")
		checkpoint.ValidationCommands = nil
		checkpoint.PublicationPolicy = nil
		seedRestartWindow(t, fixture, checkpoint, domain.RunStatePatching, true, domain.Effect{})
		effects := seedRestartEffects(t, fixture.effects, restartNoEffects)
		coord, model, repo, workspace, validation, publication := freshRestartCoordinator(
			fixture.store, fixture.checkpoints, effects, nil,
		)

		if _, err := coord.Resume(context.Background(), "run-1"); !errors.Is(err, application.ErrLifecycleUnavailable) {
			t.Fatalf("Resume() error = %v, want lifecycle unavailable", err)
		}
		if model.calls != 0 || repo.calls != 0 || workspace.ensures != 0 || workspace.patches != 0 || validation.calls != 0 || publication.calls != 0 {
			t.Fatalf("incomplete policy invoked adapters: model/repo/workspace/patch/validation/publication = %d/%d/%d/%d/%d/%d", model.calls, repo.calls, workspace.ensures, workspace.patches, validation.calls, publication.calls)
		}
	})
}

// TestResilientRestartPreservesCheckpointElapsedBudget 证明 active PostgreSQL run
// 的 elapsed_ms 为 NULL 时，restart 仍从 checkpoint 恢复已消耗 wall-clock budget。
func TestResilientRestartPreservesCheckpointElapsedBudget(t *testing.T) {
	fixture := buildRestartMatrixFixture(t)
	checkpoint := restartCheckpoint(t, fixture.checkpoints.appends, "planning-first")
	limits := application.DefaultBudgetLimits()
	limits.MaxElapsed = time.Second
	plan, err := application.NewBudgetPlan(limits, nil, nil, domain.RecoveryReserve{})
	if err != nil {
		t.Fatalf("NewBudgetPlan() error = %v", err)
	}
	checkpoint.Budget.Plan = plan
	currentConsumed := false
	for i := range checkpoint.Budget.Consumed {
		checkpoint.Budget.Consumed[i].Amount.ElapsedSeconds = 0
		if checkpoint.Budget.Consumed[i].Phase == checkpoint.Budget.CurrentPhase {
			checkpoint.Budget.Consumed[i].Amount.ElapsedSeconds = 1
			currentConsumed = true
		}
	}
	if !currentConsumed {
		checkpoint.Budget.Consumed = append(checkpoint.Budget.Consumed, domain.PhaseBudgetConsumption{
			Phase: checkpoint.Budget.CurrentPhase, Amount: domain.BudgetAmount{ElapsedSeconds: 1},
		})
	}
	seedRestartWindow(t, fixture, checkpoint, domain.RunStatePlanning, false, domain.Effect{})
	fixture.store.budget.ElapsedSeconds = 0 // PostgreSQL active-run projection.

	model := &scriptedModel{responses: []string{planEnvelope()}}
	coord := newCoordinatorWithBudget(fixture.store, &fakeRepoPort{}, &fakeEvidencePort{}, model, application.DefaultBudgetLimits())
	coord.SetCheckpointStore(fixture.checkpoints)

	run, err := coord.Resume(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if run.State != domain.RunStateBudgetExhausted || model.calls != 0 {
		t.Fatalf("restart state/model calls = %s/%d, want budget_exhausted/0", run.State, model.calls)
	}
}

// TestResilientRestartUsesDurableHardBudgetCeiling 证明 restart 采用 run 创建时
// checkpointed budget plan 的 immutable ceiling，而不是新进程的当前配置。
func TestResilientRestartUsesDurableHardBudgetCeiling(t *testing.T) {
	fixture := buildRestartMatrixFixture(t)
	checkpoint := restartCheckpoint(t, fixture.checkpoints.appends, "planning-first")
	seedRestartWindow(t, fixture, checkpoint, domain.RunStatePlanning, false, domain.Effect{})
	usedBefore := fixture.store.budget.ModelCalls

	drifted := application.DefaultBudgetLimits()
	drifted.MaxModelCalls = usedBefore
	model := &scriptedModel{responses: []string{planEnvelope()}}
	coord := newCoordinatorWithBudget(fixture.store, &fakeRepoPort{}, &fakeEvidencePort{}, model, drifted)
	coord.SetCheckpointStore(fixture.checkpoints)

	run, err := coord.Resume(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if run.State != domain.RunStateDiagnosisReadyForReview {
		t.Fatalf("state = %s, want diagnosis_ready_for_review; restart used drifted process ceiling", run.State)
	}
	if model.calls != 1 || fixture.store.budget.ModelCalls != usedBefore+1 {
		t.Fatalf("model budget after restart = calls %d durable %d, want 1/%d", model.calls, fixture.store.budget.ModelCalls, usedBefore+1)
	}
}
