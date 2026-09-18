package domain

import "testing"

func TestStoppingResultSemantics(t *testing.T) {
	for _, state := range []string{"completed_non_code", "blocked_manual_review", "failed", "budget_exhausted", "awaiting_human_review"} {
		if !IsStoppingResult(state, "auto_hotfix", false) {
			t.Errorf("%s must notify", state)
		}
	}
	if IsStoppingResult("diagnosis_ready_for_review", "auto_hotfix", false) {
		t.Fatal("intermediate hotfix diagnosis notified")
	}
	if !IsStoppingResult("diagnosis_ready_for_review", "analysis_only", false) || !IsStoppingResult("diagnosis_ready_for_review", "auto_hotfix", true) {
		t.Fatal("stopped diagnosis not notified")
	}
	for _, state := range []string{"queued", "diagnosing", "planning", "patching", "validating", "publishing"} {
		if IsStoppingResult(state, "analysis_only", false) {
			t.Errorf("active state %s notified", state)
		}
	}
}
