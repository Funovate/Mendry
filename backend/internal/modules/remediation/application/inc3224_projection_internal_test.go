package application

import (
	"strings"
	"testing"

	"mendry/backend/internal/modules/remediation/domain"
)

func TestINC3224CheckpointSuggestionOverridesOnlyLifecycleBlockers(t *testing.T) {
	const diagnosticAction = "Delete the fault injection route"
	const executionAction = "Reconcile the selected plan with the deployed baseline; do not publish"
	tests := []struct {
		name       string
		state      domain.RunState
		reason     string
		runID      string
		nextAction string
		want       string
	}{
		{name: "plan mismatch", state: domain.RunStateBlockedManualReview, reason: "patch_plan_precondition_mismatch", want: executionAction},
		{name: "patch protocol", state: domain.RunStateBlockedManualReview, reason: "patch_protocol_no_progress", want: executionAction},
		{name: "validation protocol", state: domain.RunStateBlockedManualReview, reason: "validation_protocol_no_progress", want: executionAction},
		{name: "model no progress", state: domain.RunStateBlockedManualReview, reason: "lifecycle_model_no_progress", want: executionAction},
		{name: "stop no progress", state: domain.RunStateBlockedManualReview, reason: "agent_stop_no_progress", want: executionAction},
		{name: "exhaustion keeps diagnosis", state: domain.RunStateBlockedManualReview, reason: "exhaustion_proof", want: diagnosticAction},
		{name: "plan policy keeps diagnosis", state: domain.RunStateBlockedManualReview, reason: "denied_control_plane_change", want: diagnosticAction},
		{name: "awaiting review keeps diagnosis", state: domain.RunStateAwaitingHumanReview, reason: "patch_plan_precondition_mismatch", want: diagnosticAction},
		{name: "active run keeps diagnosis", state: domain.RunStatePatching, reason: "patch_protocol_no_progress", want: diagnosticAction},
		{name: "foreign checkpoint rejected", state: domain.RunStateBlockedManualReview, reason: "patch_plan_precondition_mismatch", runID: "foreign-run", want: diagnosticAction},
		{name: "empty action keeps diagnosis", state: domain.RunStateBlockedManualReview, reason: "patch_plan_precondition_mismatch", nextAction: " ", want: diagnosticAction},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			review := &Review{RunID: "run-1", Status: tt.state, TerminalReason: tt.reason, ManualSuggestion: diagnosticAction}
			snapshot := sampleReviewCheckpoint("run-1", false, domain.CheckpointReasonPhaseBoundary, false)
			if tt.runID != "" {
				snapshot.RunID = tt.runID
			}
			nextAction := executionAction
			if tt.nextAction != "" {
				nextAction = tt.nextAction
			}
			snapshot.Checkpoint.NextActions = []string{nextAction}
			attachReviewCheckpoint(review, snapshot)
			if review.ManualSuggestion != tt.want {
				t.Fatalf("manual suggestion = %q, want %q", review.ManualSuggestion, tt.want)
			}
			if tt.state == domain.RunStateBlockedManualReview && review.Recovery != nil {
				t.Fatalf("terminal blocker displayed as active recovery: %#v", review.Recovery)
			}
		})
	}
}

func TestINC3224EnvelopeRejectsToolAlias(t *testing.T) {
	for _, raw := range []string{
		`{"schemaVersion":"v1","kind":"requestTool","requestTool":{"tool":"workspace.status","parameters":{}}}`,
		`{"schemaVersion":"v1","kind":"requestTool","requestTool":{"toolName":"workspace.status","tool":"workspace.status","parameters":{}}}`,
	} {
		if _, err := DecodeEnvelope(raw); err == nil || !strings.Contains(err.Error(), `unknown field "tool"`) {
			t.Fatalf("tool alias error = %v, want strict protocol rejection", err)
		}
	}
	for _, raw := range []string{
		`{"schemaVersion":"v1","kind":"requestTool","requestTool":{"toolName":"workspace.status","parameters":{}}}`,
		`{"schemaVersion":"v1","kind":"stop","stop":{"code":"selected_change_absent_at_baseline","reason":"Target absent at deployed baseline","recommendedNextAction":"Reconcile plan and baseline"}}`,
		`{"schemaVersion":"v1","kind":"stop","stop":{"reason":"Need operator review","recommendedNextAction":"Inspect workspace"}}`,
	} {
		if _, err := DecodeEnvelope(raw); err != nil {
			t.Fatalf("canonical or legacy envelope error = %v", err)
		}
	}
}
