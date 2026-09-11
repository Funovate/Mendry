package application_test

import (
	"context"
	"testing"

	"mendry/backend/internal/modules/agentcore/application"
	"mendry/backend/internal/modules/agentcore/domain"
)

func TestEvaluatorRegistrySupportsCustomVersionedEvaluator(t *testing.T) {
	registry := application.NewEvaluatorRegistry()
	called := false
	err := registry.Register("custom_report", "v2", application.CompletionEvaluatorFunc(func(_ context.Context, contract domain.CompletionContract, artifacts []domain.Artifact) (application.CompletionEvaluation, error) {
		called = true
		return application.CompletionEvaluation{Complete: len(artifacts) == 1, Reason: "custom"}, nil
	}))
	if err != nil {
		t.Fatalf("register evaluator: %v", err)
	}
	result, err := registry.Evaluate(context.Background(), domain.CompletionContract{Mode: "custom_report", Version: "v2"}, []domain.Artifact{{Type: "report", SchemaVersion: "v1", Provenance: domain.ProvenanceModel}})
	if err != nil || !called || !result.Complete {
		t.Fatalf("result=%+v called=%v err=%v", result, called, err)
	}
}

func TestActionCompletionRejectsModelAuthoredActionAndUnlinkedVerification(t *testing.T) {
	registry := application.NewEvaluatorRegistry()
	contract := domain.CompletionContract{Mode: domain.CompletionActionWithVerification, Version: "v1"}
	artifacts := []domain.Artifact{
		{ID: "claim", Type: "action", SchemaVersion: "v1", Data: "action claim", Provenance: domain.ProvenanceModel},
		{Type: "verification", SchemaVersion: "v1", Data: "check passed", Provenance: domain.ProvenanceVerified, Subject: &domain.VerificationSubject{ArtifactID: "claim"}, VerificationStatus: domain.VerificationPassed},
	}
	result, err := registry.Evaluate(context.Background(), contract, artifacts)
	if err != nil || result.Complete {
		t.Fatalf("model-created action completed contract: result=%+v err=%v", result, err)
	}

	artifacts[0] = domain.Artifact{ID: "observed", Type: "action", SchemaVersion: "v1", Data: "action observed", Provenance: domain.ProvenanceObserved}
	result, err = registry.Evaluate(context.Background(), contract, artifacts)
	if err != nil || result.Complete {
		t.Fatalf("unlinked verification completed contract: result=%+v err=%v", result, err)
	}
}
