package application_test

import (
	"context"
	"strings"
	"testing"

	"mendry/backend/internal/modules/remediation/application"
	"mendry/backend/internal/modules/remediation/domain"
)

func policyInput(files ...string) domain.PlanPolicyInput {
	return domain.PlanPolicyInput{
		RunID: "run-1", BaselineCommit: "commit-1", RecommendedID: "plan-1",
		Candidates: []domain.RepairPlanCandidate{{
			PlanID: "plan-1", AffectedFiles: files, EvidenceRefs: []string{"evidence-1"},
			IntendedBehavior: "repair the fault", Risk: domain.RiskOrdinary, RollbackStrategy: "revert the change",
		}},
	}
}

func TestStaticPlanPolicyAcceptsOrdinarySourceAndRejectsControlPlane(t *testing.T) {
	policy := application.NewDefaultPlanPolicy()
	accepted, err := policy.EvaluatePlan(context.Background(), policyInput("internal/service/handler.go"))
	if err != nil {
		t.Fatalf("ordinary plan evaluation: %v", err)
	}
	if !accepted.Accepted || accepted.SelectedPlanID != "plan-1" {
		t.Fatalf("ordinary plan decision = %#v", accepted)
	}

	denied, err := policy.EvaluatePlan(context.Background(), policyInput(".github/workflows/release.yml"))
	if err != nil {
		t.Fatalf("control-plane evaluation: %v", err)
	}
	if denied.Accepted || denied.Severity != domain.RecoverySeverityPolicyBlocked || denied.ReasonCode != "denied_control_plane_change" {
		t.Fatalf("control-plane decision = %#v", denied)
	}
	if !strings.Contains(denied.Message, "manual review") {
		t.Fatalf("control-plane message = %q", denied.Message)
	}
}

func TestStaticPlanPolicyProvidesRecoverableFeedbackForRejectedRecommendation(t *testing.T) {
	policy := application.NewDefaultPlanPolicy()
	input := policyInput("go.mod")
	input.Candidates = append(input.Candidates, domain.RepairPlanCandidate{
		PlanID: "plan-2", AffectedFiles: []string{"internal/service/handler.go"}, EvidenceRefs: []string{"evidence-1"},
		IntendedBehavior: "repair the fault", Risk: domain.RiskOrdinary, RollbackStrategy: "revert the change",
	})
	decision, err := policy.EvaluatePlan(context.Background(), input)
	if err != nil {
		t.Fatalf("mixed plan evaluation: %v", err)
	}
	if decision.Accepted || decision.Severity != domain.RecoverySeverityRecoverable || decision.ReasonCode != "high_risk_policy_requires_opt_in" {
		t.Fatalf("mixed plan decision = %#v", decision)
	}
	if len(decision.AcceptedPlanIDs) != 1 || decision.AcceptedPlanIDs[0] != "plan-2" {
		t.Fatalf("approved alternatives = %#v", decision.AcceptedPlanIDs)
	}
}

func TestStaticPlanPolicyRequiresSpecializedValidationForOptedInHighRisk(t *testing.T) {
	policy := application.StaticPlanPolicy{AllowHighRisk: true}
	decision, err := policy.EvaluatePlan(context.Background(), policyInput("migrations/000018_lifecycle.sql"))
	if err != nil {
		t.Fatalf("high-risk plan evaluation: %v", err)
	}
	if decision.Accepted || decision.Severity != domain.RecoverySeverityRecoverable {
		t.Fatalf("high-risk decision = %#v", decision)
	}

	policy.ApprovedValidationIDs = []string{"unit", "migration-check"}
	decision, err = policy.EvaluatePlan(context.Background(), policyInput("migrations/000018_lifecycle.sql"))
	if err != nil {
		t.Fatalf("approved high-risk plan evaluation: %v", err)
	}
	if !decision.Accepted || len(decision.RequiredValidationIDs) != 2 {
		t.Fatalf("approved high-risk decision = %#v", decision)
	}
}
