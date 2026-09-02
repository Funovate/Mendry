package domain_test

import (
	"strings"
	"testing"
	"time"

	"fixthe/backend/internal/modules/remediation/domain"
)

func TestLifecycleContractsRejectUnsafeOrIncompleteValues(t *testing.T) {
	workspace := domain.WorkspaceIdentity{
		WorkspaceID: "workspace-1", RunID: "run-1", BaselineCommit: "commit-1",
		BaseTreeHash: "tree-1", CurrentTreeHash: "tree-1", Version: 1,
	}
	if err := workspace.Validate(); err != nil {
		t.Fatalf("valid workspace identity: %v", err)
	}

	cases := []struct {
		name  string
		check func() error
	}{
		{
			name: "patch requires expected tree and bounded key",
			check: func() error {
				return (domain.PatchRequest{WorkspaceID: "workspace-1", Patch: "diff", IdempotencyKey: "patch/1"}).Validate()
			},
		},
		{
			name: "validation result requires approved command version",
			check: func() error {
				return (domain.ValidationResult{RunID: "run-1", WorkspaceID: "workspace-1", CommandID: "unit", Summary: "failed"}).Validate()
			},
		},
		{
			name: "publication requires human gate",
			check: func() error {
				return (domain.PublicationResult{BranchRef: "hotfix/INC-1", CommitHash: "abc", BaselineCommit: "base", TargetBranch: "production"}).Validate()
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.check(); err == nil {
				t.Fatal("validation succeeded")
			}
		})
	}

	if err := (domain.WorkspaceIdentity{WorkspaceID: "w", RunID: "r", BaselineCommit: "b", BaseTreeHash: "x", CurrentTreeHash: "x", Version: 1}).Validate(); err != nil {
		t.Fatalf("workspace identity with bounded values: %v", err)
	}
	if err := (domain.PatchRequest{
		WorkspaceID: "workspace-1", Patch: "diff --git a/main.go b/main.go", ExpectedTreeHash: "tree-1", IdempotencyKey: "patch/1",
	}).Validate(); err != nil {
		t.Fatalf("valid patch request: %v", err)
	}
}

func TestLifecycleContractsPreservePublicationAndValidationMetadata(t *testing.T) {
	validation := domain.ValidationResult{
		RunID: "run-1", WorkspaceID: "workspace-1", CommandID: "unit", CommandVersion: 3,
		Passed: true, ExitCode: 0, OutputArtifactRef: "sha256:validation", OutputHash: strings.Repeat("a", 64),
		Summary: "all checks passed", StartedAt: time.Now().Add(-time.Second), CompletedAt: time.Now(),
	}
	if err := validation.Validate(); err != nil {
		t.Fatalf("valid validation result: %v", err)
	}

	publication := domain.PublicationResult{
		BranchRef: "hotfix/INC-1-fix", CommitHash: strings.Repeat("b", 40),
		DraftChangeRef: "change:1", CompareURL: "https://git.example.invalid/compare/hotfix/INC-1-fix",
		BaselineCommit: "base", TargetBranch: "production", HumanReviewRequired: true,
	}
	if err := publication.Validate(); err != nil {
		t.Fatalf("valid publication result: %v", err)
	}

	effect := domain.LifecycleEffect{
		RunID: "run-1", Kind: domain.LifecycleEffectPublication, IdempotencyKey: "publication/run-1",
		State: domain.LifecycleEffectSucceeded, Attempt: 1, BaselineCommit: "base",
		BranchRef: publication.BranchRef, TargetBranch: publication.TargetBranch, CommitHash: publication.CommitHash, DraftChangeRef: publication.DraftChangeRef,
		ArtifactRef: "sha256:patch", ContentHash: strings.Repeat("c", 64),
		CompareURL: publication.CompareURL, Summary: "published; human review required",
	}
	if err := effect.Validate(); err != nil {
		t.Fatalf("valid lifecycle effect: %v", err)
	}
	if err := (domain.LifecycleEffect{RunID: "run-1", Kind: domain.LifecycleEffectPublication, IdempotencyKey: "publication/run-1", State: domain.LifecycleEffectSucceeded, Attempt: 1, CompareURL: "https://user:token@example.invalid"}).Validate(); err != nil {
		// domain contract 在 compare link 进入 review projection 或 durable effect metadata
		// 之前拒绝 authority-bearing URL。
		if !strings.Contains(err.Error(), "authority") {
			t.Fatalf("unexpected compare URL error: %v", err)
		}
	} else {
		t.Fatal("authority-bearing compare URL was accepted")
	}
}

func TestPlanPolicyDecisionRequiresSelectionWhenAccepted(t *testing.T) {
	decision := domain.PlanPolicyDecision{Accepted: true, Severity: domain.RecoverySeverityRecoverable}
	if err := decision.Validate(); err == nil {
		t.Fatal("accepted policy decision without selected plan was accepted")
	}
	decision.SelectedPlanID = "plan-1"
	decision.AcceptedPlanIDs = []string{"plan-1"}
	if err := decision.Validate(); err != nil {
		t.Fatalf("valid plan policy decision: %v", err)
	}
}
