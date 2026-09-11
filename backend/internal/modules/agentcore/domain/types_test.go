package domain_test

import (
	"testing"

	"mendry/backend/internal/modules/agentcore/domain"
)

func TestArtifactProvenanceRequiresVerificationSubject(t *testing.T) {
	base := domain.Artifact{Type: "check", SchemaVersion: "v1", Data: map[string]any{"matched": true}}
	verified := base
	verified.Provenance = domain.ProvenanceVerified
	verified.VerificationStatus = domain.VerificationPassed
	if err := verified.Validate(); err == nil {
		t.Fatal("verified artifact without subject was accepted")
	}
	verified.Subject = &domain.VerificationSubject{ArtifactID: "action-1"}
	if err := verified.Validate(); err != nil {
		t.Fatalf("linked verification rejected: %v", err)
	}
	model := base
	model.Provenance = domain.ProvenanceModel
	model.Subject = verified.Subject
	if err := model.Validate(); err == nil {
		t.Fatal("model artifact minted verification linkage")
	}
}
