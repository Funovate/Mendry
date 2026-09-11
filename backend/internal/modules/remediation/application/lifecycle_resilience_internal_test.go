package application

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"mendry/backend/internal/modules/remediation/domain"
)

func TestReconcileLifecycleCheckpointCrashWindows(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		checkpointPhase domain.RunState
		durablePhase    domain.RunState
		wantCurrent     domain.RunState
	}{
		{name: "planning to patching", checkpointPhase: domain.RunStatePlanning, durablePhase: domain.RunStatePatching, wantCurrent: domain.RunStatePatching},
		{name: "patching to validating", checkpointPhase: domain.RunStatePatching, durablePhase: domain.RunStateValidating, wantCurrent: domain.RunStateValidating},
		{name: "validating to publishing", checkpointPhase: domain.RunStateValidating, durablePhase: domain.RunStatePublishing, wantCurrent: domain.RunStatePublishing},
		{name: "validation repair keeps frontier", checkpointPhase: domain.RunStateValidating, durablePhase: domain.RunStatePatching, wantCurrent: domain.RunStateValidating},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracker := lifecycleBudgetTrackerAt(t, tt.checkpointPhase)
			snapshot := domain.CheckpointSnapshot{
				NeedsRebuild: true,
				Checkpoint: domain.WorkingMemoryCheckpointV1{
					Phase: string(tt.checkpointPhase), ObservedRunVersion: 4,
				},
			}
			run := domain.Run{State: tt.durablePhase, Version: 5}
			if err := reconcileLifecycleCheckpoint(tracker, snapshot, run); err != nil {
				t.Fatalf("reconcileLifecycleCheckpoint() error = %v", err)
			}
			if tracker.alloc == nil || tracker.alloc.current != tt.wantCurrent {
				t.Fatalf("allocator current = %v, want %v", tracker.alloc.current, tt.wantCurrent)
			}
			if tracker.alloc.frontier != domain.BudgetPhaseIndex(tt.wantCurrent) {
				t.Fatalf("allocator frontier = %d, want %d", tracker.alloc.frontier, domain.BudgetPhaseIndex(tt.wantCurrent))
			}
		})
	}
}

func TestReconcileLifecycleCheckpointAttributesTransitionDeltaToSourcePhase(t *testing.T) {
	t.Parallel()
	tracker := lifecycleBudgetTrackerAt(t, domain.RunStatePlanning)
	snapshot := domain.CheckpointSnapshot{NeedsRebuild: true, Checkpoint: domain.WorkingMemoryCheckpointV1{
		Phase: string(domain.RunStatePlanning), ObservedRunVersion: 4,
	}}
	if err := reconcileLifecycleCheckpoint(tracker, snapshot, domain.Run{
		State: domain.RunStatePatching, Version: 6, Budget: domain.BudgetCounters{ModelCalls: 1},
	}); err != nil {
		t.Fatalf("reconcileLifecycleCheckpoint() error = %v", err)
	}
	if got := tracker.alloc.consumed[domain.RunStatePlanning].ModelCalls; got != 1 {
		t.Fatalf("planning transition consumption = %d, want 1", got)
	}
	if got := tracker.alloc.consumed[domain.RunStatePatching].ModelCalls; got != 0 {
		t.Fatalf("patching consumption = %d, want 0", got)
	}
}

func TestReconcileLifecycleCheckpointDoesNotRemirrorRestoredElapsed(t *testing.T) {
	t.Parallel()
	tracker := lifecycleBudgetTrackerAt(t, domain.RunStatePlanning)
	if _, err := tracker.alloc.Consume(domain.RunStatePlanning, domain.BudgetAmount{ElapsedSeconds: 5}); err != nil {
		t.Fatalf("Consume() error = %v", err)
	}
	snapshot := domain.CheckpointSnapshot{Checkpoint: domain.WorkingMemoryCheckpointV1{
		Phase: string(domain.RunStatePlanning), ObservedRunVersion: 5,
	}}
	if err := reconcileLifecycleCheckpoint(tracker, snapshot, domain.Run{
		State: domain.RunStatePlanning, Version: 5, Budget: domain.BudgetCounters{},
	}); err != nil {
		t.Fatalf("reconcileLifecycleCheckpoint() error = %v", err)
	}
	if tracker.lastMirroredElapsed != 5 {
		t.Fatalf("last mirrored elapsed = %d, want 5", tracker.lastMirroredElapsed)
	}
	tracker.conversation = NewAgentConversation("restart")
	limits := DefaultBudgetLimits()
	budget := resumeRunBudget(limits, domain.BudgetCounters{ElapsedSeconds: 5})
	ctx := withResilientRunState(context.Background(), tracker)
	if err := (&RemediationCoordinator{}).softBudgetRecovery(ctx, budget, domain.RunStatePlanning, domain.Effect{}, false); err != nil {
		t.Fatalf("softBudgetRecovery() error = %v", err)
	}
	if got := tracker.alloc.Projection().Consumed.ElapsedSeconds; got != 5 {
		t.Fatalf("elapsed consumption after first resumed mirror = %d, want 5", got)
	}
}

func TestReconcileLifecycleCheckpointRejectsEqualVersionPhaseMismatch(t *testing.T) {
	t.Parallel()
	tracker := lifecycleBudgetTrackerAt(t, domain.RunStatePlanning)
	snapshot := domain.CheckpointSnapshot{Checkpoint: domain.WorkingMemoryCheckpointV1{Phase: string(domain.RunStatePlanning), ObservedRunVersion: 5}}
	if err := reconcileLifecycleCheckpoint(tracker, snapshot, domain.Run{State: domain.RunStatePatching, Version: 5}); err == nil {
		t.Fatal("equal-version phase mismatch was accepted")
	}
}

func TestReconcileLifecycleCheckpointAcceptsPersistedValidationRepairFrontier(t *testing.T) {
	t.Parallel()
	tracker := lifecycleBudgetTrackerAt(t, domain.RunStateValidating)
	if _, err := tracker.alloc.Consume(domain.RunStateValidating, domain.BudgetAmount{ModelCalls: 2}); err != nil {
		t.Fatalf("Consume() error = %v", err)
	}
	snapshot := domain.CheckpointSnapshot{Checkpoint: domain.WorkingMemoryCheckpointV1{Phase: string(domain.RunStatePatching), ObservedRunVersion: 5}}
	if err := reconcileLifecycleCheckpoint(tracker, snapshot, domain.Run{
		State: domain.RunStatePatching, Version: 5, Budget: domain.BudgetCounters{ModelCalls: 3},
	}); err != nil {
		t.Fatalf("reconcileLifecycleCheckpoint() error = %v", err)
	}
	if tracker.alloc.current != domain.RunStateValidating || tracker.alloc.frontier != domain.BudgetPhaseIndex(domain.RunStateValidating) {
		t.Fatalf("validation repair allocator = current %s frontier %d", tracker.alloc.current, tracker.alloc.frontier)
	}
	if got := tracker.alloc.Projection().Consumed.ModelCalls; got != 3 {
		t.Fatalf("reconciled model calls = %d, want durable 3", got)
	}
}

func TestReconcileLifecycleCheckpointRejectsEmbeddedPhaseAndCounterConflicts(t *testing.T) {
	t.Parallel()
	t.Run("embedded current is not derived from outer phase", func(t *testing.T) {
		tracker := lifecycleBudgetTrackerAt(t, domain.RunStatePatching)
		snapshot := domain.CheckpointSnapshot{Checkpoint: domain.WorkingMemoryCheckpointV1{
			Phase: string(domain.RunStateValidating), ObservedRunVersion: 5,
		}}
		if err := reconcileLifecycleCheckpoint(tracker, snapshot, domain.Run{State: domain.RunStateValidating, Version: 5}); err == nil {
			t.Fatal("outer/embedded phase conflict was accepted")
		}
	})
	t.Run("checkpoint consumption ahead of durable state", func(t *testing.T) {
		tracker := lifecycleBudgetTrackerAt(t, domain.RunStatePatching)
		if _, err := tracker.alloc.Consume(domain.RunStatePatching, domain.BudgetAmount{ToolCalls: 2}); err != nil {
			t.Fatalf("Consume() error = %v", err)
		}
		snapshot := domain.CheckpointSnapshot{Checkpoint: domain.WorkingMemoryCheckpointV1{
			Phase: string(domain.RunStatePatching), ObservedRunVersion: 5,
		}}
		if err := reconcileLifecycleCheckpoint(tracker, snapshot, domain.Run{
			State: domain.RunStatePatching, Version: 5, Budget: domain.BudgetCounters{ToolCalls: 1},
		}); err == nil {
			t.Fatal("checkpoint consumption ahead of durable counters was accepted")
		}
	})
}

func TestResetValidationNoProgressIsScoped(t *testing.T) {
	t.Parallel()
	tracker := &resilientRunState{
		planFeedbackAttempts: 4, planFeedbackNoProgress: 2, lastPlanFeedbackFingerprint: "plan",
		lifecycleRecoveryAttempts: 3, lastLifecycleRecoveryFingerprint: "lifecycle", lastLifecycleRecoveryClass: "tool",
		lifecycleChallengeAttempts: 5, lifecycleStopAttempts: 2,
		validationNoProgress: 3, lastValidationFingerprint: "validation",
		exhaustionProposalAttempts: 6,
	}

	tracker.resetValidationNoProgress()

	if tracker.validationNoProgress != 0 || tracker.lastValidationFingerprint != "" {
		t.Fatalf("validation progress = %d/%q, want cleared", tracker.validationNoProgress, tracker.lastValidationFingerprint)
	}
	if tracker.planFeedbackAttempts != 4 || tracker.planFeedbackNoProgress != 2 || tracker.lastPlanFeedbackFingerprint != "plan" ||
		tracker.lifecycleRecoveryAttempts != 3 || tracker.lastLifecycleRecoveryFingerprint != "lifecycle" || tracker.lastLifecycleRecoveryClass != "tool" ||
		tracker.lifecycleChallengeAttempts != 5 || tracker.lifecycleStopAttempts != 2 || tracker.exhaustionProposalAttempts != 6 {
		t.Fatalf("unrelated recovery progress changed: %#v", tracker.recoveryProgressSnapshot())
	}
}

func TestRecoveryJournalRetainsNewestBoundaryAndRestoresProgress(t *testing.T) {
	t.Parallel()
	tracker := lifecycleBudgetTrackerAt(t, domain.RunStatePlanning)
	for index := 0; index < maxResilientRecoveries+1; index++ {
		tracker.appendRecovery(domain.CheckpointRecovery{Kind: "tool_failure", Action: fmt.Sprintf("action-%d", index), OutcomeRef: "challenge:transport"})
	}
	if len(tracker.recoveries) != maxResilientRecoveries || tracker.recoveries[0].Action != "action-1" || tracker.recoveries[len(tracker.recoveries)-1].Action != "action-64" {
		t.Fatalf("recovery journal boundary = %#v", tracker.recoveries)
	}
	tracker.run = domain.Run{RunID: "run-1", SeriesID: "series-1", ContextVersion: 1}
	tracker.observedVersion = 1
	checkpoint, err := tracker.buildCheckpoint(domain.RunStatePlanning, domain.CheckpointReasonRecovery)
	if err != nil {
		t.Fatalf("buildCheckpoint() error = %v", err)
	}
	if len(checkpoint.Recoveries) != maxResilientRecoveries {
		t.Fatalf("checkpoint recoveries = %d, want %d", len(checkpoint.Recoveries), maxResilientRecoveries)
	}

	progress := &resilientRunState{}
	planFingerprint := recoveryFingerprint("same-plan")
	validationFingerprint := recoveryFingerprint("unit\x00output")
	lifecycleFingerprint := recoveryFingerprint("model_turn\x00connector_timeout")
	for index := 0; index < 2; index++ {
		progress.appendRecovery(domain.CheckpointRecovery{Kind: "validation_revision", Action: "revise_plan", OutcomeRef: recoveryProgressRef("plan_policy", planFingerprint)})
		progress.appendRecovery(domain.CheckpointRecovery{Kind: "validation_revision", Action: "revise_patch", OutcomeRef: recoveryProgressRef("validation_failed", validationFingerprint)})
		progress.appendRecovery(domain.CheckpointRecovery{Kind: "context_rehydration", Action: "lifecycle:model_turn", OutcomeRef: recoveryProgressRef("connector_timeout", lifecycleFingerprint)})
		progress.appendRecovery(domain.CheckpointRecovery{Kind: "validation_revision", Action: "stop", OutcomeRef: recoveryProgressRef("agent_stop_requires_recovery", "stop")})
	}
	progress.restoreRecoveryProgress()
	if progress.planFeedbackAttempts != 2 || progress.planFeedbackNoProgress != 2 || progress.lastPlanFeedbackFingerprint != planFingerprint {
		t.Fatalf("restored plan progress = attempts %d noProgress %d fingerprint %q", progress.planFeedbackAttempts, progress.planFeedbackNoProgress, progress.lastPlanFeedbackFingerprint)
	}
	if progress.validationNoProgress != 2 || progress.lastValidationFingerprint != validationFingerprint {
		t.Fatalf("restored validation progress = noProgress %d fingerprint %q", progress.validationNoProgress, progress.lastValidationFingerprint)
	}
	if progress.lifecycleRecoveryAttempts != 2 || progress.lastLifecycleRecoveryFingerprint != lifecycleFingerprint || progress.lifecycleStopAttempts != 2 {
		t.Fatalf("restored lifecycle progress = attempts %d fingerprint %q stops %d", progress.lifecycleRecoveryAttempts, progress.lastLifecycleRecoveryFingerprint, progress.lifecycleStopAttempts)
	}
}

func TestRecoveryProgressSnapshotSurvivesJournalEvictionAndLegacyFallback(t *testing.T) {
	t.Parallel()
	tracker := lifecycleBudgetTrackerAt(t, domain.RunStatePatching)
	tracker.run = domain.Run{RunID: "run-1", SeriesID: "series-1", ContextVersion: 1}
	tracker.observedVersion = 1
	tracker.recordLifecycleFailure("workspace.apply_patch\x00transport")
	tracker.recordLifecycleFailure("workspace.apply_patch\x00transport")
	for index := 0; index < maxResilientRecoveries+10; index++ {
		tracker.appendRecovery(domain.CheckpointRecovery{Kind: "tool_failure", Action: fmt.Sprintf("audit-%d", index), OutcomeRef: "challenge:audit"})
	}
	checkpoint, err := tracker.buildCheckpoint(domain.RunStatePatching, domain.CheckpointReasonRecovery)
	if err != nil {
		t.Fatalf("buildCheckpoint() error = %v", err)
	}
	if checkpoint.RecoveryProgress == nil || checkpoint.RecoveryProgress.LifecycleRecoveryAttempts != 2 {
		t.Fatalf("recovery progress snapshot = %#v", checkpoint.RecoveryProgress)
	}
	restarted := &resilientRunState{recoveries: checkpoint.Recoveries}
	restarted.restoreRecoveryProgressSnapshot(*checkpoint.RecoveryProgress)
	if restarted.lifecycleRecoveryAttempts != 2 || restarted.lastLifecycleRecoveryFingerprint == "" {
		t.Fatalf("restored snapshot = attempts %d fingerprint %q", restarted.lifecycleRecoveryAttempts, restarted.lastLifecycleRecoveryFingerprint)
	}

	legacy := &resilientRunState{recoveries: []domain.CheckpointRecovery{
		{Kind: "tool_failure", Action: ToolWorkspaceApplyPatch, OutcomeRef: "challenge:transport"},
		{Kind: "tool_failure", Action: ToolWorkspaceApplyPatch, OutcomeRef: "challenge:transport"},
		{Kind: "protocol_correction", Action: "agentEnvelope", OutcomeRef: "challenge:invalid_envelope"},
		{Kind: "protocol_correction", Action: "agentEnvelope", OutcomeRef: "challenge:invalid_envelope"},
	}}
	legacy.restoreRecoveryProgress()
	if legacy.lifecycleRecoveryAttempts != 2 || legacy.lastLifecycleRecoveryClass != "protocol" ||
		len(legacy.lastLifecycleRecoveryFingerprint) != 64 {
		t.Fatalf("legacy recovery restore = attempts %d class %q fingerprint %q", legacy.lifecycleRecoveryAttempts, legacy.lastLifecycleRecoveryClass, legacy.lastLifecycleRecoveryFingerprint)
	}
}

func TestLifecycleProgressResetAndChallengeAttemptNumbering(t *testing.T) {
	t.Parallel()
	tracker := &resilientRunState{
		run:          domain.Run{RunID: "run-1", SeriesID: "series-1", ContextVersion: 1},
		conversation: NewAgentConversation("bootstrap"), lifecyclePhase: domain.RunStatePatching,
	}
	tracker.recordLifecycleFailure("model_turn\x00transport")
	tracker.resetLifecycleProtocolNoProgress()
	tracker.recordLifecycleFailure("model_turn\x00transport")
	if tracker.lifecycleRecoveryAttempts != 1 {
		t.Fatalf("alternating failure/success attempts = %d, want 1", tracker.lifecycleRecoveryAttempts)
	}
	coord := &RemediationCoordinator{}
	ctx := withResilientRunState(context.Background(), tracker)
	if err := coord.appendLifecycleChallenge(ctx, domain.RecoveryChallengeKindToolFailure, "transport", ToolWorkspaceApplyPatch, []string{"workspace"}, []string{"retry_transient"}, "retry"); err != nil {
		t.Fatalf("appendLifecycleChallenge() error = %v", err)
	}
	if !strings.Contains(tracker.conversation.ContextText(), `"attempt":1`) {
		t.Fatalf("first challenge attempt is not 1: %s", tracker.conversation.ContextText())
	}
}

func lifecycleBudgetTrackerAt(t *testing.T, phase domain.RunState) *resilientRunState {
	t.Helper()
	plan, err := NewBudgetPlan(DefaultBudgetLimits(), nil, nil, domain.RecoveryReserve{})
	if err != nil {
		t.Fatalf("NewBudgetPlan() error = %v", err)
	}
	allocator, err := newPhaseBudgetPlan(plan)
	if err != nil {
		t.Fatalf("newPhaseBudgetPlan() error = %v", err)
	}
	for _, candidate := range domain.BudgetPhaseOrder() {
		if allocator.current != "" {
			if err := allocator.ClosePhase(allocator.current); err != nil {
				t.Fatalf("ClosePhase() error = %v", err)
			}
		}
		if err := allocator.AdmitPhase(candidate); err != nil {
			t.Fatalf("AdmitPhase() error = %v", err)
		}
		if candidate == phase {
			break
		}
	}
	return &resilientRunState{alloc: allocator}
}
