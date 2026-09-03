package application_test

import (
	"context"
	"strings"
	"testing"

	"fixthe/backend/internal/modules/remediation/application"
	"fixthe/backend/internal/modules/remediation/domain"
)

func TestResilientLifecycleRejectedToolIsRecoverableWithoutAdapterEffect(t *testing.T) {
	coord, store, _, effects, workspace, validation, publication := setupLifecycleCoordinator(t, []string{
		diagnosisEnvelope("code_fixable"), planEnvelope(),
		validationRequestEnvelope("unit"),
		`{"schemaVersion":"v1","kind":"requestTool","requestTool":{"toolName":"workspace.read_file","parameters":{"path":"../secret"}}}`,
		patchRequestEnvelope(""),
		patchRequestEnvelope("diff --git a/main.go b/main.go\n"), patchCompleteEnvelope(),
		validationRequestEnvelope("unit"), validationAssessmentEnvelope(true),
	})
	startLifecyclePlan(t, coord, store)

	run, err := coord.ApplyPlan(context.Background(), "run-1", "p1")
	if err != nil {
		t.Fatalf("ApplyPlan() error = %v", err)
	}
	if run.State != domain.RunStateAwaitingHumanReview || workspace.reads != 0 || workspace.patches != 1 || validation.calls != 1 || publication.calls != 1 {
		t.Fatalf("state/calls = %s/%d/%d/%d/%d", run.State, workspace.reads, workspace.patches, validation.calls, publication.calls)
	}
	patchEffects := 0
	for _, effect := range effects.effects {
		if effect.Kind == domain.LifecycleEffectPatch {
			patchEffects++
			if effect.State != domain.LifecycleEffectSucceeded {
				t.Fatalf("rejected call created a non-success patch effect: %#v", effect)
			}
		}
	}
	if patchEffects != 1 {
		t.Fatalf("patch effects = %d, want only the accepted adapter effect", patchEffects)
	}
	if store.created.TerminalReason == "configuration_failure" {
		t.Fatalf("tool rejection became configuration failure: %#v", store.created)
	}
}

func TestResilientLifecycleStopNoProgressSurvivesRestart(t *testing.T) {
	coord, store, checkpoints, _, workspace, validation, publication := setupLifecycleCoordinator(t, []string{
		diagnosisEnvelope("code_fixable"), planEnvelope(),
		`{"schemaVersion":"v1","kind":"stop","stop":{"reason":"pause","recommendedNextAction":"continue recovery"}}`,
	})
	startLifecyclePlan(t, coord, store)
	forceLifecycleState(store, domain.RunStatePatching)
	snapshot := checkpoints.byRun["run-1"]
	snapshot.Checkpoint.Recoveries = []domain.CheckpointRecovery{
		{Kind: string(domain.RecoveryChallengeKindValidationRevision), Action: "stop", OutcomeRef: "challenge:agent_stop_requires_recovery"},
		{Kind: string(domain.RecoveryChallengeKindValidationRevision), Action: "stop", OutcomeRef: "challenge:agent_stop_requires_recovery"},
	}
	checkpoints.byRun["run-1"] = snapshot
	fallback := seededLifecycleEffects()
	delete(fallback, domain.LifecycleEffectPatch)
	delete(fallback, domain.LifecycleEffectValidation)
	delete(fallback, domain.LifecycleEffectPublication)
	coord.SetLifecycleStore(&restartLifecycleStore{base: &fakeLifecycleStore{}, fallback: fallback})

	run, err := coord.ResumeLifecycle(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("ResumeLifecycle() error = %v", err)
	}
	if run.State != domain.RunStateBlockedManualReview || workspace.ensures != 0 || workspace.patches != 0 || validation.calls != 0 || publication.calls != 0 {
		t.Fatalf("state/calls = %s/%d/%d/%d/%d", run.State, workspace.ensures, workspace.patches, validation.calls, publication.calls)
	}
}

func TestResilientLifecyclePublicationEffectRestartStates(t *testing.T) {
	tests := []struct {
		name      string
		state     domain.LifecycleEffectState
		attempt   int
		wantState domain.RunState
		wantCalls int
	}{
		{name: "started retries same key", state: domain.LifecycleEffectStarted, attempt: 1, wantState: domain.RunStateAwaitingHumanReview, wantCalls: 1},
		{name: "recoverable retries same key", state: domain.LifecycleEffectRecoverable, attempt: 1, wantState: domain.RunStateAwaitingHumanReview, wantCalls: 1},
		{name: "failed is terminal without retry", state: domain.LifecycleEffectFailed, attempt: 1, wantState: domain.RunStateBlockedManualReview, wantCalls: 0},
		{name: "exhausted started is terminal without retry", state: domain.LifecycleEffectStarted, attempt: 3, wantState: domain.RunStateBlockedManualReview, wantCalls: 0},
		{name: "exhausted recoverable is terminal without retry", state: domain.LifecycleEffectRecoverable, attempt: 3, wantState: domain.RunStateBlockedManualReview, wantCalls: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			coord, store, _, _, _, _, publication := setupLifecycleCoordinator(t, []string{diagnosisEnvelope("code_fixable"), planEnvelope()})
			startLifecyclePlan(t, coord, store)
			forceLifecycleState(store, domain.RunStatePublishing)
			fallback := seededLifecycleEffects()
			fallback[domain.LifecycleEffectPublication] = domain.LifecycleEffect{
				Kind: domain.LifecycleEffectPublication, State: tt.state, Attempt: tt.attempt,
				BaselineCommit: "abc123", WorkspaceID: "workspace-1", ResultTreeHash: "tree-patched",
				ArtifactRef: "sha256:patch", ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				BranchRef: "hotfix/remediation/run-1", TargetBranch: "production", ErrorCode: "transport", Summary: "publication failed",
			}
			coord.SetLifecycleStore(&restartLifecycleStore{base: &fakeLifecycleStore{}, fallback: fallback})

			run, err := coord.ResumeLifecycle(context.Background(), "run-1")
			if err != nil {
				t.Fatalf("ResumeLifecycle() error = %v", err)
			}
			if run.State != tt.wantState || publication.calls != tt.wantCalls {
				t.Fatalf("state/calls = %s/%d, want %s/%d", run.State, publication.calls, tt.wantState, tt.wantCalls)
			}
		})
	}
}

func TestResilientLifecycleClosedPatchKeyAllowsChangedPatch(t *testing.T) {
	oldPatch := "diff --git a/main.go b/main.go\n"
	newPatch := "diff --git a/main.go b/main.go\n@@ -1 +1 @@\n-old\n+new\n"
	coord, store, _, _, workspace, validation, publication := setupLifecycleCoordinator(t, []string{
		diagnosisEnvelope("code_fixable"), planEnvelope(),
		patchRequestEnvelope(oldPatch), patchRequestEnvelope(newPatch), patchCompleteEnvelope(),
		validationRequestEnvelope("unit"), validationAssessmentEnvelope(true),
	})
	startLifecyclePlan(t, coord, store)
	forceLifecycleState(store, domain.RunStatePatching)
	fallback := seededLifecycleEffects()
	delete(fallback, domain.LifecycleEffectValidation)
	delete(fallback, domain.LifecycleEffectPublication)
	fallback[domain.LifecycleEffectPatch] = domain.LifecycleEffect{
		Kind: domain.LifecycleEffectPatch, State: domain.LifecycleEffectFailed, Attempt: 3,
		// key 绑定 oldPatch、tree-base 与 run-1；changed patch 必须命中新 key。
		IdempotencyKey: "patch/5b4d9b50711ca1b7b6a8ae8ed7e984dab7a7e8e06580b3c5c4fe9aeaebc183a6", BaselineCommit: "abc123",
		WorkspaceID: "workspace-1", ErrorCode: "transport", Summary: "closed patch key",
	}
	coord.SetLifecycleStore(&restartLifecycleStore{base: &fakeLifecycleStore{}, fallback: fallback})

	run, err := coord.ResumeLifecycle(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("ResumeLifecycle() error = %v", err)
	}
	if run.State != domain.RunStateAwaitingHumanReview || workspace.patches != 1 || validation.calls != 1 || publication.calls != 1 {
		t.Fatalf("state/calls = %s patch:%d validation:%d publication:%d requests:%#v closedKey:%s", run.State, workspace.patches, validation.calls, publication.calls, workspace.patchesIn, fallback[domain.LifecycleEffectPatch].IdempotencyKey)
	}
	if len(workspace.patchesIn) != 1 || workspace.patchesIn[0].Patch != newPatch {
		t.Fatalf("executed patches = %#v, want only changed patch", workspace.patchesIn)
	}
}

func TestResilientLifecyclePatchKeyUsesCompletePayloadIdentity(t *testing.T) {
	prefix := strings.Repeat("x", 256)
	firstPatch := prefix + "-first"
	secondPatch := prefix + "-second"
	coord, store, _, effects, workspace, validation, publication := setupLifecycleCoordinator(t, []string{
		diagnosisEnvelope("code_fixable"), planEnvelope(),
		patchRequestEnvelope(firstPatch), patchRequestEnvelope(secondPatch), patchCompleteEnvelope(),
		validationRequestEnvelope("unit"), validationAssessmentEnvelope(true),
	})
	workspace.errors = []error{&domain.LifecycleRuntimeError{Code: "transport", Retryable: true}}
	startLifecyclePlan(t, coord, store)

	run, err := coord.ApplyPlan(context.Background(), "run-1", "p1")
	if err != nil {
		t.Fatalf("ApplyPlan() error = %v", err)
	}
	if run.State != domain.RunStateAwaitingHumanReview || workspace.patches != 2 || validation.calls != 1 || publication.calls != 1 {
		t.Fatalf("state/calls = %s patch:%d validation:%d publication:%d", run.State, workspace.patches, validation.calls, publication.calls)
	}
	if len(workspace.patchesIn) != 2 || workspace.patchesIn[0].ExpectedTreeHash != workspace.patchesIn[1].ExpectedTreeHash {
		t.Fatalf("patch requests did not share one tree: %#v", workspace.patchesIn)
	}
	if workspace.patchesIn[0].IdempotencyKey == workspace.patchesIn[1].IdempotencyKey {
		t.Fatalf("suffix-only patch change reused key %q", workspace.patchesIn[0].IdempotencyKey)
	}
	for _, request := range workspace.patchesIn {
		if len(request.IdempotencyKey) > 256 {
			t.Fatalf("idempotency key length = %d, want <= 256", len(request.IdempotencyKey))
		}
		if strings.Contains(request.IdempotencyKey, prefix) {
			t.Fatalf("idempotency key exposed patch text: %q", request.IdempotencyKey)
		}
	}
	patchEffects := 0
	for _, effect := range effects.effects {
		if effect.Kind == domain.LifecycleEffectPatch {
			patchEffects++
		}
	}
	if patchEffects != 2 {
		t.Fatalf("patch effects = %d, want distinct recoverable and successful effects", patchEffects)
	}
}

func TestResilientLifecycleChangedPatchesResetValidationNoProgress(t *testing.T) {
	responses := []string{diagnosisEnvelope("code_fixable"), planEnvelope()}
	for index := 1; index <= 4; index++ {
		responses = append(responses,
			patchRequestEnvelope(strings.Repeat("diff", index)), patchCompleteEnvelope(),
			validationRequestEnvelope("unit"), validationAssessmentEnvelope(index == 4),
		)
	}
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	checkpoints := &fakeCheckpointStore{runStore: store}
	model := &scriptedModel{responses: responses}
	limits := application.DefaultBudgetLimits()
	limits.MaxModelCalls = 64
	limits.MaxToolCalls = 64
	coord := newCoordinatorWithBudget(store, &fakeRepoPort{}, &fakeEvidencePort{}, model, limits)
	effects := &fakeLifecycleStore{}
	workspace := &lifecycleWorkspaceFake{}
	validation := &lifecycleValidationFake{}
	publication := &lifecyclePublicationFake{}
	coord.SetCheckpointStore(checkpoints)
	coord.SetLifecycleStore(effects)
	coord.SetWorkspacePort(workspace)
	coord.SetValidationPort(validation)
	coord.SetPublicationPort(publication)
	coord.SetValidationCommandVersions(map[string]int64{"unit": 1})
	sharedOutputHash := strings.Repeat("a", 64)
	for index := 0; index < 3; index++ {
		validation.results = append(validation.results, domain.ValidationResult{
			Passed: false, ExitCode: 1, OutputArtifactRef: "sha256:failed-validation", OutputHash: sharedOutputHash,
			BytesRetrieved: 20, Summary: "same validation failure",
		})
	}
	validation.results = append(validation.results, domain.ValidationResult{
		Passed: true, ExitCode: 0, OutputArtifactRef: "sha256:passed-validation", OutputHash: sharedOutputHash,
		BytesRetrieved: 20, Summary: "validation passed",
	})
	startLifecyclePlan(t, coord, store)
	store.plans = []domain.RepairPlanCandidate{{
		PlanID: "p1", EvidenceRefs: []string{"ev-1"}, AffectedFiles: []string{"main.go"},
		IntendedBehavior: "add nil check", Risk: domain.RiskClassification("ordinary"),
		RollbackStrategy: "revert", Recommended: true,
	}}
	store.recommendedID = "p1"
	store.suggestedDiff = "diff --git a/main.go b/main.go"

	run, err := coord.ApplyPlan(context.Background(), "run-1", "p1")
	if err != nil {
		t.Fatalf("ApplyPlan() error = %v", err)
	}
	if run.State != domain.RunStateAwaitingHumanReview || workspace.patches != 4 || validation.calls != 4 || publication.calls != 1 {
		t.Fatalf("state/calls = %s patch:%d validation:%d publication:%d terminal:%s", run.State, workspace.patches, validation.calls, publication.calls, store.created.TerminalReason)
	}
	if store.created.TerminalReason == "validation_no_progress" {
		t.Fatal("identical validation output blocked genuinely changed patches")
	}
}

func TestResilientLifecycleFailedEffectsAreNotReexecuted(t *testing.T) {
	tests := []struct {
		name  string
		phase domain.RunState
		kind  domain.LifecycleEffectKind
	}{
		{name: "workspace", phase: domain.RunStatePatching, kind: domain.LifecycleEffectWorkspace},
		{name: "patch", phase: domain.RunStatePatching, kind: domain.LifecycleEffectPatch},
		{name: "validation", phase: domain.RunStateValidating, kind: domain.LifecycleEffectValidation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			responses := []string{diagnosisEnvelope("code_fixable"), planEnvelope()}
			if tt.kind == domain.LifecycleEffectPatch {
				request := patchRequestEnvelope("diff --git a/main.go b/main.go\n")
				responses = append(responses, request, request, request)
			}
			if tt.kind == domain.LifecycleEffectValidation {
				request := validationRequestEnvelope("unit")
				responses = append(responses, request, request, request)
			}
			coord, store, _, _, workspace, validation, publication := setupLifecycleCoordinator(t, responses)
			startLifecyclePlan(t, coord, store)
			forceLifecycleState(store, tt.phase)
			fallback := seededLifecycleEffects()
			delete(fallback, domain.LifecycleEffectPublication)
			if tt.kind == domain.LifecycleEffectWorkspace {
				fallback = map[domain.LifecycleEffectKind]domain.LifecycleEffect{}
			}
			fallback[tt.kind] = domain.LifecycleEffect{
				Kind: tt.kind, State: domain.LifecycleEffectFailed, Attempt: 1, BaselineCommit: "abc123",
				WorkspaceID: "workspace-1", ErrorCode: "invalid_arguments", Summary: "non-retryable failure",
			}
			coord.SetLifecycleStore(&restartLifecycleStore{base: &fakeLifecycleStore{}, fallback: fallback})

			run, err := coord.ResumeLifecycle(context.Background(), "run-1")
			if tt.kind == domain.LifecycleEffectWorkspace {
				if err == nil || store.state != domain.RunStateFailed {
					t.Fatalf("workspace failure result = state %s err %v", store.state, err)
				}
			} else {
				if err != nil || run.State != domain.RunStateBlockedManualReview {
					t.Fatalf("closed tool path result = state %s err %v", run.State, err)
				}
			}
			if workspace.ensures != 0 || workspace.patches != 0 || validation.calls != 0 || publication.calls != 0 {
				t.Fatalf("failed effect was reexecuted: workspace=%d patch=%d validation=%d publication=%d", workspace.ensures, workspace.patches, validation.calls, publication.calls)
			}
			if tt.kind == domain.LifecycleEffectWorkspace && store.state != domain.RunStateFailed {
				t.Fatalf("state = %s, want failed", store.state)
			}
			if tt.kind != domain.LifecycleEffectWorkspace && store.state != domain.RunStateBlockedManualReview {
				t.Fatalf("state = %s, want blocked_manual_review", store.state)
			}
		})
	}
}
