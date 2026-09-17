package application_test

import (
	"context"
	"testing"

	"mendry/backend/internal/modules/agentcore/application"
	"mendry/backend/internal/modules/agentcore/domain"
)

func TestLinkedVerificationRequiresPassedStatus(t *testing.T) {
	for _, mode := range []domain.CompletionMode{domain.CompletionActionWithVerification, domain.CompletionPipelineVerification} {
		for _, status := range []domain.VerificationStatus{"", domain.VerificationFailed, domain.VerificationPassed} {
			t.Run(string(mode)+"/"+string(status), func(t *testing.T) {
				actionType, verificationType := "action", "verification"
				if mode == domain.CompletionPipelineVerification {
					actionType, verificationType = "pipeline.run", "pipeline.verification"
				}
				artifacts := []domain.Artifact{
					{ID: "action-1", Type: actionType, SchemaVersion: "v1", Data: "action observed", Provenance: domain.ProvenanceObserved},
					{Type: verificationType, SchemaVersion: "v1", Data: "check result", Provenance: domain.ProvenanceVerified, Subject: &domain.VerificationSubject{ArtifactID: "action-1"}, VerificationStatus: status},
				}
				result, err := application.NewEvaluatorRegistry().Evaluate(context.Background(), domain.CompletionContract{Mode: mode, Version: "v1"}, artifacts)
				if err != nil || result.Complete != (status == domain.VerificationPassed) {
					t.Fatalf("status=%q result=%+v err=%v", status, result, err)
				}
			})
		}
	}
}
