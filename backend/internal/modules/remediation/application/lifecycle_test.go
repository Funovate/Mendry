package application_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"mendry/backend/internal/modules/remediation/application"
	"mendry/backend/internal/modules/remediation/domain"
)

type fakeLifecycleStore struct {
	effects map[string]domain.LifecycleEffect
	nextID  int
}

func (s *fakeLifecycleStore) GetLifecycleEffect(_ context.Context, runID string, kind domain.LifecycleEffectKind, key string) (domain.LifecycleEffect, error) {
	if effect, ok := s.effects[lifecycleEffectMapKey(runID, kind, key)]; ok {
		return effect, nil
	}
	return domain.LifecycleEffect{}, domain.ErrLifecycleEffectNotFound
}

func (s *fakeLifecycleStore) UpsertLifecycleEffect(_ context.Context, effect domain.LifecycleEffect) (domain.LifecycleEffect, error) {
	if err := effect.Validate(); err != nil {
		return domain.LifecycleEffect{}, err
	}
	if s.effects == nil {
		s.effects = make(map[string]domain.LifecycleEffect)
	}
	key := lifecycleEffectMapKey(effect.RunID, effect.Kind, effect.IdempotencyKey)
	if existing, ok := s.effects[key]; ok && existing.State == domain.LifecycleEffectSucceeded {
		return existing, nil
	}
	if effect.EffectID == "" {
		s.nextID++
		effect.EffectID = fmt.Sprintf("effect-%d", s.nextID)
	}
	now := time.Now().UTC()
	if effect.CreatedAt.IsZero() {
		effect.CreatedAt = now
	}
	if effect.UpdatedAt.IsZero() {
		effect.UpdatedAt = now
	}
	s.effects[key] = effect
	return effect, nil
}

func (s *fakeLifecycleStore) ListLifecycleEffects(_ context.Context, runID string) ([]domain.LifecycleEffect, error) {
	out := make([]domain.LifecycleEffect, 0)
	for _, effect := range s.effects {
		if effect.RunID == runID {
			out = append(out, effect)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.Before(out[j].UpdatedAt) })
	return out, nil
}

func lifecycleEffectMapKey(runID string, kind domain.LifecycleEffectKind, key string) string {
	return runID + "\x00" + string(kind) + "\x00" + key
}

type restartLifecycleStore struct {
	base     *fakeLifecycleStore
	fallback map[domain.LifecycleEffectKind]domain.LifecycleEffect
}

func (s *restartLifecycleStore) GetLifecycleEffect(ctx context.Context, runID string, kind domain.LifecycleEffectKind, key string) (domain.LifecycleEffect, error) {
	effect, err := s.base.GetLifecycleEffect(ctx, runID, kind, key)
	if err == nil {
		return effect, nil
	}
	if !errors.Is(err, domain.ErrLifecycleEffectNotFound) {
		return domain.LifecycleEffect{}, err
	}
	if effect, ok := s.fallback[kind]; ok {
		if effect.IdempotencyKey != "" && effect.IdempotencyKey != key {
			return domain.LifecycleEffect{}, domain.ErrLifecycleEffectNotFound
		}
		effect.RunID = runID
		if effect.IdempotencyKey == "" {
			effect.IdempotencyKey = key
		}
		return effect, nil
	}
	return domain.LifecycleEffect{}, err
}

func (s *restartLifecycleStore) UpsertLifecycleEffect(ctx context.Context, effect domain.LifecycleEffect) (domain.LifecycleEffect, error) {
	return s.base.UpsertLifecycleEffect(ctx, effect)
}

func (s *restartLifecycleStore) ListLifecycleEffects(ctx context.Context, runID string) ([]domain.LifecycleEffect, error) {
	effects, err := s.base.ListLifecycleEffects(ctx, runID)
	if err != nil {
		return nil, err
	}
	seen := make(map[domain.LifecycleEffectKind]bool, len(effects))
	for _, effect := range effects {
		seen[effect.Kind] = true
	}
	for kind, effect := range s.fallback {
		if seen[kind] {
			continue
		}
		effect.RunID = runID
		effects = append(effects, effect)
	}
	sort.SliceStable(effects, func(i, j int) bool {
		return lifecycleEffectKindOrder(effects[i].Kind) < lifecycleEffectKindOrder(effects[j].Kind)
	})
	return effects, nil
}

func lifecycleEffectKindOrder(kind domain.LifecycleEffectKind) int {
	switch kind {
	case domain.LifecycleEffectWorkspace:
		return 0
	case domain.LifecycleEffectPatch:
		return 1
	case domain.LifecycleEffectValidation:
		return 2
	case domain.LifecycleEffectPublication:
		return 3
	default:
		return 4
	}
}

func seededLifecycleEffects() map[domain.LifecycleEffectKind]domain.LifecycleEffect {
	return map[domain.LifecycleEffectKind]domain.LifecycleEffect{
		domain.LifecycleEffectWorkspace: {
			Kind: domain.LifecycleEffectWorkspace, State: domain.LifecycleEffectSucceeded, Attempt: 1,
			BaselineCommit: "abc123", WorkspaceID: "workspace-1", BaseTreeHash: "tree-base", ResultTreeHash: "tree-base",
			Summary: "workspace prepared",
		},
		domain.LifecycleEffectPatch: {
			Kind: domain.LifecycleEffectPatch, State: domain.LifecycleEffectSucceeded, Attempt: 1,
			BaselineCommit: "abc123", WorkspaceID: "workspace-1", BaseTreeHash: "tree-base", ResultTreeHash: "tree-patched",
			ArtifactRef: "sha256:patch", ContentHash: strings.Repeat("a", 64), Summary: "patch applied",
		},
		domain.LifecycleEffectValidation: {
			Kind: domain.LifecycleEffectValidation, State: domain.LifecycleEffectSucceeded, Attempt: 1,
			BaselineCommit: "abc123", WorkspaceID: "workspace-1", CommandID: "unit", CommandVersion: 1,
			ValidationKnown: true, ValidationPassed: true, ArtifactRef: "sha256:validation", ContentHash: strings.Repeat("b", 64), Summary: "validation passed",
		},
		domain.LifecycleEffectPublication: {
			Kind: domain.LifecycleEffectPublication, State: domain.LifecycleEffectSucceeded, Attempt: 1,
			BaselineCommit: "abc123", WorkspaceID: "workspace-1", ResultTreeHash: "tree-patched",
			ArtifactRef: "sha256:patch", ContentHash: strings.Repeat("a", 64), BranchRef: "hotfix/remediation/run-1",
			TargetBranch: "production", CommitHash: "commit-published", Summary: "publication completed",
		},
	}
}

func forceLifecycleState(store *fakeRunStore, state domain.RunState) {
	store.state = state
	if store.created != nil {
		store.created.State = state
		store.created.Version++
	}
	if store.checkpointStore == nil {
		return
	}
	snapshot, ok := store.checkpointStore.byRun["run-1"]
	if !ok {
		return
	}
	snapshot.Checkpoint.ValidationCommands = map[string]int64{"unit": 1}
	snapshot.Checkpoint.PublicationPolicy = &domain.CheckpointPublicationPolicy{
		TargetBranch: "production", BranchPrefix: "hotfix/remediation/",
	}
	store.checkpointStore.byRun["run-1"] = snapshot
}

type lifecycleWorkspaceFake struct {
	identity  domain.WorkspaceIdentity
	ensures   int
	reads     int
	patches   int
	patchesIn []domain.PatchRequest
	results   []domain.PatchResult
	errors    []error
}

func (w *lifecycleWorkspaceFake) Ensure(_ context.Context, request domain.WorkspaceRequest) (domain.WorkspaceIdentity, error) {
	w.ensures++
	if err := request.Validate(); err != nil {
		return domain.WorkspaceIdentity{}, err
	}
	if w.identity.WorkspaceID == "" {
		w.identity = domain.WorkspaceIdentity{
			WorkspaceID: "workspace-1", RunID: request.RunID, BaselineCommit: request.BaselineCommit,
			BaseTreeHash: "tree-base", CurrentTreeHash: "tree-base", Version: 1,
		}
	}
	return w.identity, nil
}

func (w *lifecycleWorkspaceFake) Status(_ context.Context, identity domain.WorkspaceIdentity) (domain.WorkspaceStatus, error) {
	w.identity = identity
	return domain.WorkspaceStatus{Identity: identity, Clean: identity.CurrentTreeHash == identity.BaseTreeHash}, nil
}

func (w *lifecycleWorkspaceFake) ReadFile(_ context.Context, _ domain.WorkspaceIdentity, path string, _ int64) (domain.WorkspaceFile, error) {
	w.reads++
	return domain.WorkspaceFile{Path: path, Content: []byte("package main\n")}, nil
}

func (w *lifecycleWorkspaceFake) ApplyPatch(_ context.Context, identity domain.WorkspaceIdentity, request domain.PatchRequest) (domain.PatchResult, error) {
	w.patches++
	w.patchesIn = append(w.patchesIn, request)
	if len(w.errors) > 0 {
		err := w.errors[0]
		w.errors = w.errors[1:]
		if err != nil {
			return domain.PatchResult{}, err
		}
	}
	if len(w.results) == 0 {
		return domain.PatchResult{
			WorkspaceID: identity.WorkspaceID, Applied: true, ArtifactRef: fmt.Sprintf("sha256:patch-%d", w.patches),
			ContentHash: fmt.Sprintf("%064x", w.patches), ResultTreeHash: fmt.Sprintf("tree-patched-%d", w.patches),
			ChangedFiles: []string{"main.go"}, BytesRetrieved: int64(len(request.Patch)), Summary: "patch applied",
		}, nil
	}
	result := w.results[0]
	w.results = w.results[1:]
	result.WorkspaceID = identity.WorkspaceID
	return result, nil
}

func (w *lifecycleWorkspaceFake) Destroy(context.Context, domain.WorkspaceIdentity) error { return nil }

type lifecycleValidationFake struct {
	calls    int
	requests []domain.ValidationRequest
	results  []domain.ValidationResult
}

func (v *lifecycleValidationFake) Run(_ context.Context, request domain.ValidationRequest) (domain.ValidationResult, error) {
	v.calls++
	v.requests = append(v.requests, request)
	if len(v.results) == 0 {
		return domain.ValidationResult{
			RunID: request.RunID, WorkspaceID: request.WorkspaceID, CommandID: request.CommandID,
			CommandVersion: request.CommandVersion, Passed: true, ExitCode: 0,
			OutputArtifactRef: "sha256:validation", OutputHash: fmt.Sprintf("%064x", v.calls), BytesRetrieved: 8,
			Summary: "validation passed",
		}, nil
	}
	result := v.results[0]
	v.results = v.results[1:]
	result.RunID = request.RunID
	result.WorkspaceID = request.WorkspaceID
	result.CommandID = request.CommandID
	result.CommandVersion = request.CommandVersion
	return result, nil
}

type lifecyclePublicationFake struct {
	calls    int
	requests []domain.PublicationRequest
	results  []domain.PublicationResult
	errors   []error
}

func (p *lifecyclePublicationFake) Publish(_ context.Context, request domain.PublicationRequest) (domain.PublicationResult, error) {
	p.calls++
	p.requests = append(p.requests, request)
	if len(p.errors) > 0 {
		err := p.errors[0]
		p.errors = p.errors[1:]
		if err != nil {
			return domain.PublicationResult{}, err
		}
	}
	if len(p.results) > 0 {
		result := p.results[0]
		p.results = p.results[1:]
		return result, nil
	}
	return domain.PublicationResult{
		BranchRef: request.BranchRef, CommitHash: "commit-published", BaselineCommit: request.BaselineCommit,
		TargetBranch: request.TargetBranch, HumanReviewRequired: true, Summary: "published",
	}, nil
}

func setupLifecycleCoordinator(t *testing.T, responses []string) (*application.RemediationCoordinator, *fakeRunStore, *fakeCheckpointStore, *fakeLifecycleStore, *lifecycleWorkspaceFake, *lifecycleValidationFake, *lifecyclePublicationFake) {
	t.Helper()
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	checkpoints := &fakeCheckpointStore{runStore: store}
	store.checkpointStore = checkpoints
	model := &scriptedModel{responses: responses}
	coord := newCoordinator(store, &fakeRepoPort{}, &fakeEvidencePort{}, model)
	coord.SetCheckpointStore(checkpoints)
	lifecycleStore := &fakeLifecycleStore{}
	workspace := &lifecycleWorkspaceFake{}
	validation := &lifecycleValidationFake{}
	publication := &lifecyclePublicationFake{}
	coord.SetLifecycleStore(lifecycleStore)
	coord.SetWorkspacePort(workspace)
	coord.SetValidationPort(validation)
	coord.SetPublicationPort(publication)
	coord.SetValidationCommandVersions(map[string]int64{"unit": 1})
	return coord, store, checkpoints, lifecycleStore, workspace, validation, publication
}

func startLifecyclePlan(t *testing.T, coord *application.RemediationCoordinator, store *fakeRunStore) domain.Run {
	t.Helper()
	run, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123", ContextVersion: 1,
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if run.State != domain.RunStateDiagnosisReadyForReview {
		t.Fatalf("state after planning = %s, want diagnosis_ready_for_review", run.State)
	}
	store.created.ProjectID = testProjectID
	return run
}

func patchRequestEnvelope(patch string) string {
	return fmt.Sprintf(`{"schemaVersion":"v1","kind":"requestTool","requestTool":{"toolName":%q,"parameters":{"patch":%q}}}`, application.ToolWorkspaceApplyPatch, patch)
}

func validationRequestEnvelope(commandID string) string {
	return fmt.Sprintf(`{"schemaVersion":"v1","kind":"requestTool","requestTool":{"toolName":%q,"parameters":{"commandId":%q}}}`, application.ToolWorkspaceRunValidation, commandID)
}

func patchCompleteEnvelope() string {
	return `{"schemaVersion":"v1","kind":"patchComplete","patchComplete":{"success":true,"message":"patch is ready for validation"}}`
}

func validationAssessmentEnvelope(passed bool) string {
	return fmt.Sprintf(`{"schemaVersion":"v1","kind":"validationAssessment","validationAssessment":{"passed":%t,"summary":"bounded validation result"}}`, passed)
}

func TestLifecyclePlanPolicyFeedsRecoverableFeedbackIntoPlanning(t *testing.T) {
	responses := []string{
		diagnosisEnvelope("code_fixable"),
		`{"schemaVersion":"v1","kind":"planCandidates","planCandidates":{"candidates":[` +
			`{"planId":"high","evidenceRefs":["ev-1"],"affectedFiles":["go.mod"],"intendedBehavior":"change dependency","risk":"high_risk","rollbackStrategy":"revert"},` +
			`{"planId":"ordinary","evidenceRefs":["ev-1"],"affectedFiles":["main.go"],"intendedBehavior":"fix handler","risk":"ordinary","rollbackStrategy":"revert"}],` +
			`"recommendedId":"high","rationale":"first choice","suggestedDiff":"diff --git a/go.mod"}}`,
		planEnvelope(),
	}
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	checkpoints := &fakeCheckpointStore{runStore: store}
	model := &scriptedModel{responses: responses}
	coord := newCoordinator(store, &fakeRepoPort{}, &fakeEvidencePort{}, model)
	coord.SetCheckpointStore(checkpoints)

	if _, err := coord.Start(context.Background(), domain.NewRun{IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123", ContextVersion: 1}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if store.state != domain.RunStateDiagnosisReadyForReview || model.calls != 3 {
		t.Fatalf("state/model calls = %s/%d", store.state, model.calls)
	}
	if !strings.Contains(model.turns[2].UserMessage, "high_risk_policy_requires_opt_in") ||
		!strings.Contains(model.turns[2].UserMessage, "ordinary") {
		t.Fatalf("planning policy feedback missing: %s", model.turns[2].UserMessage)
	}
	if len(checkpoints.appends) < 2 {
		t.Fatalf("planning checkpoints = %d, want recovery checkpoint", len(checkpoints.appends))
	}
}

func TestAnalysisOnlyRunRejectsInitialAndResumedLifecycleEffects(t *testing.T) {
	coord, store, _, _, workspace, validation, publication := setupLifecycleCoordinator(t, []string{
		diagnosisEnvelope("code_fixable"), planEnvelope(),
	})
	startLifecyclePlan(t, coord, store)
	store.created.AnalysisOnly = true

	if _, err := coord.ApplyPlan(context.Background(), "run-1", "p1"); !errors.Is(err, application.ErrLifecycleUnavailable) {
		t.Fatalf("ApplyPlan() error = %v", err)
	}
	forceLifecycleState(store, domain.RunStatePatching)
	if _, err := coord.ResumeLifecycle(context.Background(), "run-1"); !errors.Is(err, application.ErrLifecycleUnavailable) {
		t.Fatalf("ResumeLifecycle() error = %v", err)
	}
	if workspace.patches != 0 || validation.calls != 0 || publication.calls != 0 {
		t.Fatalf("analysis-only external effects = patch:%d validation:%d publication:%d", workspace.patches, validation.calls, publication.calls)
	}
}

func TestResilientLifecycleRunsPatchValidationPublicationWithCheckpointIdentities(t *testing.T) {
	coord, store, checkpoints, effects, workspace, validation, publication := setupLifecycleCoordinator(t, []string{
		diagnosisEnvelope("code_fixable"), planEnvelope(),
		patchRequestEnvelope("diff --git a/main.go b/main.go\n"), patchCompleteEnvelope(),
		validationRequestEnvelope("unit"), validationAssessmentEnvelope(true),
	})
	startLifecyclePlan(t, coord, store)

	run, err := coord.ApplyPlan(context.Background(), "run-1", "p1")
	if err != nil {
		t.Fatalf("ApplyPlan() error = %v", err)
	}
	if run.State != domain.RunStateAwaitingHumanReview || store.state != domain.RunStateAwaitingHumanReview {
		t.Fatalf("final state = %s/%s, want awaiting_human_review", run.State, store.state)
	}
	if workspace.ensures != 1 || workspace.patches != 1 || validation.calls != 1 || publication.calls != 1 {
		t.Fatalf("external calls workspace=%d patch=%d validation=%d publication=%d", workspace.ensures, workspace.patches, validation.calls, publication.calls)
	}
	if len(publication.requests) != 1 || publication.requests[0].BaselineCommit != "abc123" || publication.requests[0].TargetBranch != "production" {
		t.Fatalf("publication request = %#v", publication.requests)
	}
	if len(store.notifications) != 2 || store.notifications[len(store.notifications)-1].Kind != application.NotificationChangeReadyForReview {
		t.Fatalf("notifications = %#v", store.notifications)
	}
	var workspaceEffect, patchEffect, validationEffect, publicationEffect domain.LifecycleEffect
	for _, effect := range effects.effects {
		switch effect.Kind {
		case domain.LifecycleEffectWorkspace:
			workspaceEffect = effect
		case domain.LifecycleEffectPatch:
			patchEffect = effect
		case domain.LifecycleEffectValidation:
			validationEffect = effect
		case domain.LifecycleEffectPublication:
			publicationEffect = effect
		}
	}
	if workspaceEffect.State != domain.LifecycleEffectSucceeded || workspaceEffect.BaselineCommit != "abc123" || workspaceEffect.WorkspaceID == "" {
		t.Fatalf("workspace effect = %#v", workspaceEffect)
	}
	if patchEffect.State != domain.LifecycleEffectSucceeded || patchEffect.ArtifactRef == "" || patchEffect.ResultTreeHash == "" {
		t.Fatalf("patch effect = %#v", patchEffect)
	}
	if validationEffect.State != domain.LifecycleEffectSucceeded || !validationEffect.ValidationKnown || !validationEffect.ValidationPassed || validationEffect.CommandID != "unit" {
		t.Fatalf("validation effect = %#v", validationEffect)
	}
	if publicationEffect.State != domain.LifecycleEffectSucceeded || publicationEffect.BranchRef == "" || publicationEffect.CommitHash == "" {
		t.Fatalf("publication effect = %#v", publicationEffect)
	}
	last := checkpoints.appends[len(checkpoints.appends)-1]
	if last.Publication == nil || !last.Publication.HumanReviewOnly || last.Workspace == nil || len(last.Artifacts) == 0 || last.Validation == nil {
		t.Fatalf("final lifecycle checkpoint = %#v", last)
	}
}

func TestResilientLifecycleValidationFailureRequiresChangedPatch(t *testing.T) {
	coord, store, _, effects, workspace, validation, publication := setupLifecycleCoordinator(t, []string{
		diagnosisEnvelope("code_fixable"), planEnvelope(),
		patchRequestEnvelope("diff --git a/main.go b/main.go\n"), patchCompleteEnvelope(),
		validationRequestEnvelope("unit"), validationAssessmentEnvelope(false),
		patchRequestEnvelope("diff --git a/main.go b/main.go\n+changed\n"), patchCompleteEnvelope(),
		validationRequestEnvelope("unit"), validationAssessmentEnvelope(true),
	})
	workspace.results = []domain.PatchResult{
		{Applied: true, ArtifactRef: "sha256:patch-one", ContentHash: strings.Repeat("1", 64), ResultTreeHash: "tree-one", ChangedFiles: []string{"main.go"}, BytesRetrieved: 4, Summary: "patch applied"},
		{Applied: true, ArtifactRef: "sha256:patch-two", ContentHash: strings.Repeat("2", 64), ResultTreeHash: "tree-two", ChangedFiles: []string{"main.go"}, BytesRetrieved: 12, Summary: "revised patch applied"},
	}
	validation.results = []domain.ValidationResult{
		{Passed: false, ExitCode: 1, OutputArtifactRef: "sha256:failed-validation", OutputHash: strings.Repeat("3", 64), BytesRetrieved: 20, Summary: "validation failed"},
		{Passed: true, ExitCode: 0, OutputArtifactRef: "sha256:passed-validation", OutputHash: strings.Repeat("4", 64), BytesRetrieved: 20, Summary: "validation passed"},
	}
	startLifecyclePlan(t, coord, store)

	run, err := coord.ApplyPlan(context.Background(), "run-1", "p1")
	if err != nil {
		t.Fatalf("ApplyPlan() error = %v", err)
	}
	if run.State != domain.RunStateAwaitingHumanReview || workspace.patches != 2 || validation.calls != 2 || publication.calls != 1 {
		t.Fatalf("state/calls = %s/%d/%d/%d", run.State, workspace.patches, validation.calls, publication.calls)
	}
	patchCount := 0
	for _, effect := range effects.effects {
		if effect.Kind == domain.LifecycleEffectPatch && effect.State == domain.LifecycleEffectSucceeded {
			patchCount++
		}
	}
	if patchCount != 2 {
		t.Fatalf("successful patch effects = %d, want 2", patchCount)
	}
	if len(store.transitions) < 7 {
		t.Fatalf("transitions = %#v, want validation revision transition", store.transitions)
	}
}

func TestResilientLifecyclePublicationRetryReusesIdempotencyKeyAfterRestart(t *testing.T) {
	coord, store, _, effects, _, _, publication := setupLifecycleCoordinator(t, []string{
		diagnosisEnvelope("code_fixable"), planEnvelope(),
		patchRequestEnvelope("diff --git a/main.go b/main.go\n"), patchCompleteEnvelope(),
		validationRequestEnvelope("unit"), validationAssessmentEnvelope(true),
	})
	publication.errors = []error{&domain.LifecycleRuntimeError{Code: "transport", Retryable: true}}
	startLifecyclePlan(t, coord, store)

	run, err := coord.ApplyPlan(context.Background(), "run-1", "p1")
	if err != nil {
		t.Fatalf("ApplyPlan() transient error = %v", err)
	}
	if run.State != domain.RunStatePublishing || store.state != domain.RunStatePublishing || publication.calls != 1 {
		t.Fatalf("after transient publication state/calls = %s/%s/%d", run.State, store.state, publication.calls)
	}
	firstKey := publication.requests[0].IdempotencyKey
	firstBranch := publication.requests[0].BranchRef
	if firstKey == "" {
		t.Fatal("first publication key is empty")
	}
	for _, effect := range effects.effects {
		if effect.Kind == domain.LifecycleEffectPublication && (effect.State != domain.LifecycleEffectRecoverable || effect.ErrorCode != "transport") {
			t.Fatalf("publication recovery effect = %#v", effect)
		}
	}

	coord.SetLifecyclePublicationPolicy(application.LifecyclePublicationPolicy{TargetBranch: "changed-after-start", BranchPrefix: "changed-after-start"})
	run, err = coord.ResumeLifecycle(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("ResumeLifecycle() error = %v", err)
	}
	if run.State != domain.RunStateAwaitingHumanReview || publication.calls != 2 {
		t.Fatalf("after resume state/calls = %s/%d", run.State, publication.calls)
	}
	if publication.requests[1].IdempotencyKey != firstKey {
		t.Fatalf("publication keys = %q/%q, want same key", firstKey, publication.requests[1].IdempotencyKey)
	}
	if publication.requests[1].TargetBranch != "production" || publication.requests[1].BranchRef != firstBranch {
		t.Fatalf("publication policy changed across restart: first=%#v second=%#v", publication.requests[0], publication.requests[1])
	}
	before := publication.calls
	if _, err := coord.ResumeLifecycle(context.Background(), "run-1"); err != nil {
		t.Fatalf("ResumeLifecycle() after success error = %v", err)
	}
	if publication.calls != before {
		t.Fatalf("successful publication was repeated: calls=%d before=%d", publication.calls, before)
	}
}

func TestResilientLifecycleRestartAfterWorkspaceEffectContinuesPatching(t *testing.T) {
	coord, store, _, _, workspace, validation, publication := setupLifecycleCoordinator(t, []string{
		diagnosisEnvelope("code_fixable"), planEnvelope(),
		patchRequestEnvelope("diff --git a/main.go b/main.go\n"), patchCompleteEnvelope(),
		validationRequestEnvelope("unit"), validationAssessmentEnvelope(true),
	})
	startLifecyclePlan(t, coord, store)
	forceLifecycleState(store, domain.RunStatePatching)
	fallback := seededLifecycleEffects()
	delete(fallback, domain.LifecycleEffectPatch)
	delete(fallback, domain.LifecycleEffectValidation)
	delete(fallback, domain.LifecycleEffectPublication)
	coord.SetLifecycleStore(&restartLifecycleStore{base: &fakeLifecycleStore{}, fallback: fallback})

	run, err := coord.ResumeLifecycle(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("ResumeLifecycle() error = %v", err)
	}
	if run.State != domain.RunStateAwaitingHumanReview || workspace.ensures != 0 || workspace.patches != 1 || validation.calls != 1 || publication.calls != 1 {
		t.Fatalf("state/calls = %s/%d/%d/%d/%d", run.State, workspace.ensures, workspace.patches, validation.calls, publication.calls)
	}
}
func TestResilientLifecycleRestartReusesWorkspaceAndPatchEffects(t *testing.T) {
	coord, store, _, _, workspace, validation, publication := setupLifecycleCoordinator(t, []string{
		diagnosisEnvelope("code_fixable"), planEnvelope(),
		validationRequestEnvelope("unit"), validationAssessmentEnvelope(true),
	})
	startLifecyclePlan(t, coord, store)
	forceLifecycleState(store, domain.RunStatePatching)
	base := &fakeLifecycleStore{}
	fallback := seededLifecycleEffects()
	delete(fallback, domain.LifecycleEffectValidation)
	delete(fallback, domain.LifecycleEffectPublication)
	coord.SetLifecycleStore(&restartLifecycleStore{base: base, fallback: fallback})
	run, err := coord.ResumeLifecycle(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("ResumeLifecycle() error = %v", err)
	}
	if run.State != domain.RunStateAwaitingHumanReview || workspace.ensures != 0 || workspace.patches != 0 || validation.calls != 1 || publication.calls != 1 {
		t.Fatalf("state/calls = %s/%d/%d/%d/%d", run.State, workspace.ensures, workspace.patches, validation.calls, publication.calls)
	}
}

func TestResilientLifecycleRestartSkipsValidationWhenResultWasDurable(t *testing.T) {
	coord, store, _, _, workspace, validation, publication := setupLifecycleCoordinator(t, []string{
		diagnosisEnvelope("code_fixable"), planEnvelope(),
	})
	startLifecyclePlan(t, coord, store)
	forceLifecycleState(store, domain.RunStateValidating)
	fallback := seededLifecycleEffects()
	delete(fallback, domain.LifecycleEffectPublication)
	coord.SetLifecycleStore(&restartLifecycleStore{base: &fakeLifecycleStore{}, fallback: fallback})

	run, err := coord.ResumeLifecycle(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("ResumeLifecycle() error = %v", err)
	}
	if run.State != domain.RunStateAwaitingHumanReview || workspace.ensures != 0 || workspace.patches != 0 || validation.calls != 0 || publication.calls != 1 {
		t.Fatalf("state/calls = %s/%d/%d/%d/%d", run.State, workspace.ensures, workspace.patches, validation.calls, publication.calls)
	}
}

func TestResilientLifecycleRestartSkipsPublicationWhenResultWasDurable(t *testing.T) {
	coord, store, _, _, _, _, publication := setupLifecycleCoordinator(t, []string{
		diagnosisEnvelope("code_fixable"), planEnvelope(),
	})
	startLifecyclePlan(t, coord, store)
	forceLifecycleState(store, domain.RunStatePublishing)
	coord.SetLifecycleStore(&restartLifecycleStore{base: &fakeLifecycleStore{}, fallback: seededLifecycleEffects()})

	run, err := coord.ResumeLifecycle(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("ResumeLifecycle() error = %v", err)
	}
	if run.State != domain.RunStateAwaitingHumanReview || publication.calls != 0 {
		t.Fatalf("state/publication calls = %s/%d", run.State, publication.calls)
	}
}

func TestLifecycleToolGatewayDoesNotExposePatchContentAsBase64(t *testing.T) {
	workspace := &lifecycleWorkspaceFake{identity: domain.WorkspaceIdentity{WorkspaceID: "w", RunID: "run-1", BaselineCommit: "base", BaseTreeHash: "tree", CurrentTreeHash: "tree", Version: 1}}
	gateway := application.NewLifecycleToolGateway(workspace, nil)
	result, err := gateway.Execute(context.Background(), domain.RunStatePatching, workspace.identity, nil, "patch/1", application.ToolWorkspaceReadFile, map[string]interface{}{"path": "main.go"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Payload == nil {
		t.Fatal("workspace read payload is nil")
	}
	conversation := application.NewAgentConversation("")
	conversation.AppendToolResult(application.RequestTool{ToolName: application.ToolWorkspaceReadFile}, result, nil)
	encoded := conversation.ContextText()
	if strings.Contains(encoded, "cGFja2FnZSBtYWlu") || !strings.Contains(encoded, "package main") {
		t.Fatalf("workspace read was not rendered as bounded text: %s", encoded)
	}
}

func TestResilientDiagnosisToolFailureUsesUnifiedRecoveryChallenge(t *testing.T) {
	store := newFakeRunStore()
	store.mode = domain.AgentLoopModeResilientV1
	checkpoints := &fakeCheckpointStore{runStore: store}
	repo := &failingReadRepoPort{err: errors.New("connector timeout")}
	model := &scriptedModel{responses: []string{
		requestToolEnvelope(application.ToolRepoReadFile, "main.go"),
		diagnosisEnvelope("external_dependency"),
	}}
	coord := newCoordinator(store, repo, &fakeEvidencePort{}, model)
	coord.SetCheckpointStore(checkpoints)
	if _, err := coord.Start(context.Background(), domain.NewRun{IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123", ContextVersion: 1}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if store.state != domain.RunStateCompletedNonCode || len(model.turns) != 2 {
		t.Fatalf("state/turns = %s/%d", store.state, len(model.turns))
	}
	if !strings.Contains(model.turns[1].UserMessage, `"kind":"tool_failure"`) ||
		!strings.Contains(model.turns[1].UserMessage, `"reasonCode":"connector_timeout"`) {
		t.Fatalf("unified tool recovery challenge missing: %s", model.turns[1].UserMessage)
	}
	if checkpoints.countReason(domain.CheckpointReasonRecovery) != 1 {
		t.Fatalf("recovery checkpoints = %d, want 1", checkpoints.countReason(domain.CheckpointReasonRecovery))
	}
}
func TestLifecycleToolGatewayRejectsUnapprovedValidationCommand(t *testing.T) {
	workspace := &lifecycleWorkspaceFake{identity: domain.WorkspaceIdentity{WorkspaceID: "w", RunID: "run-1", BaselineCommit: "base", BaseTreeHash: "tree", CurrentTreeHash: "tree", Version: 1}}
	validation := &lifecycleValidationFake{}
	gateway := application.NewLifecycleToolGateway(workspace, validation)
	_, err := gateway.Execute(context.Background(), domain.RunStateValidating, workspace.identity, map[string]int64{"unit": 1}, "validation/1", application.ToolWorkspaceRunValidation, map[string]interface{}{"commandId": "shell"})
	if err == nil {
		t.Fatal("unapproved validation command was accepted")
	}
	if validation.calls != 0 {
		t.Fatalf("validation adapter calls = %d, want 0", validation.calls)
	}
}
func TestLifecyclePublicationResultRejectsMissingHumanGate(t *testing.T) {
	result := domain.PublicationResult{BranchRef: "hotfix/a", CommitHash: "abc", BaselineCommit: "base", TargetBranch: "production"}
	if err := result.Validate(); err == nil {
		t.Fatal("publication result without human gate was accepted")
	}
}
