package application_test

import (
	"context"
	"strings"
	"testing"

	"mendry/backend/internal/modules/remediation/domain"
)

func TestINC3224SelectedChangeAbsentBlocksLifecycle(t *testing.T) {
	coord, store, checkpoints, effects, workspace, validation, publication := setupLifecycleCoordinator(t, []string{
		diagnosisEnvelope("code_fixable"), planEnvelope(),
		`{"schemaVersion":"v1","kind":"requestTool","requestTool":{"toolName":"workspace.read_file","parameters":{"path":"main.go"}}}`,
		`{"schemaVersion":"v1","kind":"stop","stop":{"code":"selected_change_absent_at_baseline","reason":"The selected change is absent at the deployed baseline; no patch was applied.","recommendedNextAction":"Reconcile the plan and deployed baseline before retrying."}}`,
	})
	startLifecyclePlan(t, coord, store)

	run, err := coord.ApplyPlan(context.Background(), "run-1", "p1")
	if err != nil {
		t.Fatalf("ApplyPlan() error = %v", err)
	}
	terminalReason := ""
	for _, effect := range store.effects {
		if effect.TerminalReason != "" {
			terminalReason = effect.TerminalReason
		}
	}
	if run.State != domain.RunStateBlockedManualReview || terminalReason != "patch_plan_precondition_mismatch" {
		t.Fatalf("state/reason = %s/%s", run.State, terminalReason)
	}
	if workspace.reads != 1 || workspace.patches != 0 || validation.calls != 0 || publication.calls != 0 {
		t.Fatalf("calls = read:%d patch:%d validation:%d publication:%d", workspace.reads, workspace.patches, validation.calls, publication.calls)
	}
	for _, effect := range effects.effects {
		if effect.Kind != domain.LifecycleEffectWorkspace {
			t.Fatalf("mismatched plan produced a patch, validation, or publication effect: %#v", effect)
		}
	}
	checkpoint := checkpoints.byRun["run-1"].Checkpoint
	if len(checkpoint.NextActions) != 1 || !strings.Contains(checkpoint.NextActions[0], "deployed baseline") || !strings.Contains(checkpoint.NextActions[0], "Do not patch or publish") {
		t.Fatalf("manual next actions = %v", checkpoint.NextActions)
	}
	for _, artifact := range checkpoint.Artifacts {
		if artifact.Kind == "patch" || artifact.Kind == "validation" {
			t.Fatalf("mismatched plan persisted an execution artifact: %#v", artifact)
		}
	}
}

func TestINC3224ProtocolAndStopCountersAreIndependent(t *testing.T) {
	invalid := `{"schemaVersion":"v1","kind":"requestTool","requestTool":{"tool":"workspace.status","parameters":{}}}`
	stop := `{"schemaVersion":"v1","kind":"stop","stop":{"reason":"Need to inspect the workspace","recommendedNextAction":"Inspect workspace status"}}`
	coord, store, checkpoints, _, workspace, validation, publication := setupLifecycleCoordinator(t, []string{
		diagnosisEnvelope("code_fixable"), planEnvelope(),
		invalid, invalid, stop, invalid,
		patchRequestEnvelope("diff --git a/main.go b/main.go\n"), patchCompleteEnvelope(),
		validationRequestEnvelope("unit"), validationAssessmentEnvelope(true),
	})
	startLifecyclePlan(t, coord, store)

	run, err := coord.ApplyPlan(context.Background(), "run-1", "p1")
	if err != nil {
		t.Fatalf("ApplyPlan() error = %v", err)
	}
	if run.State != domain.RunStateAwaitingHumanReview || workspace.patches != 1 || validation.calls != 1 || publication.calls != 1 {
		t.Fatalf("state/calls = %s patch:%d validation:%d publication:%d reason:%s", run.State, workspace.patches, validation.calls, publication.calls, run.TerminalReason)
	}
	var recoveries []domain.WorkingMemoryCheckpointV1
	for _, checkpoint := range checkpoints.appends {
		if checkpoint.Phase == string(domain.RunStatePatching) && checkpoint.Reason == domain.CheckpointReasonRecovery {
			recoveries = append(recoveries, checkpoint)
		}
	}
	if len(recoveries) != 4 {
		t.Fatalf("patch recovery checkpoints = %d, want 4", len(recoveries))
	}
	wantProtocol := []int{1, 2, 0, 1}
	wantStops := []int{0, 0, 1, 1}
	for index, checkpoint := range recoveries {
		progress := checkpoint.RecoveryProgress
		if progress == nil || progress.LifecycleRecoveryAttempts != wantProtocol[index] || progress.LifecycleStopAttempts != wantStops[index] {
			t.Fatalf("checkpoint %d progress = %#v, want protocol:%d stops:%d", index, progress, wantProtocol[index], wantStops[index])
		}
	}
}

func TestINC3224StopCountersSurviveRestart(t *testing.T) {
	for _, format := range []string{"durable progress", "durable progress with evicted journal", "legacy journal"} {
		t.Run(format, func(t *testing.T) {
			coord, store, checkpoints, _, workspace, validation, publication := setupLifecycleCoordinator(t, []string{
				diagnosisEnvelope("code_fixable"), planEnvelope(),
				`{"schemaVersion":"v1","kind":"stop","stop":{"reason":"pause","recommendedNextAction":"inspect workspace"}}`,
			})
			startLifecyclePlan(t, coord, store)
			forceLifecycleState(store, domain.RunStatePatching)
			snapshot := checkpoints.byRun["run-1"]
			snapshot.Checkpoint.Recoveries = []domain.CheckpointRecovery{
				{Kind: string(domain.RecoveryChallengeKindProtocolCorrection), Action: "lifecycle:model_turn", OutcomeRef: "challenge:invalid_envelope"},
				{Kind: string(domain.RecoveryChallengeKindProtocolCorrection), Action: "lifecycle:model_turn", OutcomeRef: "challenge:invalid_envelope"},
				{Kind: string(domain.RecoveryChallengeKindValidationRevision), Action: "stop", OutcomeRef: "challenge:agent_stop_requires_recovery"},
				{Kind: string(domain.RecoveryChallengeKindValidationRevision), Action: "stop", OutcomeRef: "challenge:agent_stop_requires_recovery"},
			}
			snapshot.Checkpoint.RecoveryProgress = nil
			if format != "legacy journal" {
				snapshot.Checkpoint.RecoveryProgress = &domain.CheckpointRecoveryProgress{
					LifecycleRecoveryAttempts: 2, LastLifecycleRecoveryClass: "protocol",
					LastLifecycleRecoveryFingerprint: strings.Repeat("a", 64),
					LifecycleStopAttempts:            2, LifecycleChallengeAttempts: 4,
				}
			}
			if format == "durable progress with evicted journal" {
				snapshot.Checkpoint.Recoveries = nil
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
			terminalReason := ""
			for _, effect := range store.effects {
				if effect.TerminalReason != "" {
					terminalReason = effect.TerminalReason
				}
			}
			if run.State != domain.RunStateBlockedManualReview || terminalReason != "agent_stop_no_progress" {
				t.Fatalf("state/reason = %s/%s, want independent stop limit", run.State, terminalReason)
			}
			if workspace.ensures != 0 || workspace.patches != 0 || validation.calls != 0 || publication.calls != 0 {
				t.Fatalf("restart calls = ensure:%d patch:%d validation:%d publication:%d", workspace.ensures, workspace.patches, validation.calls, publication.calls)
			}
			progress := checkpoints.byRun["run-1"].Checkpoint.RecoveryProgress
			if progress == nil || progress.LifecycleStopAttempts != 3 || progress.LifecycleRecoveryAttempts != 0 {
				t.Fatalf("terminal progress = %#v, want stops:3 protocol:0", progress)
			}
		})
	}
}
