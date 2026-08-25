package application_test

import (
	"context"
	"strings"
	"testing"

	"fixthe/backend/internal/modules/remediation/application"
	"fixthe/backend/internal/modules/remediation/domain"
)

func TestAssembleInitialContextIncludesSSHInspectHints(t *testing.T) {
	assembler := application.NewContextAssembler(&fakeRepoPort{}, &fakeEvidencePort{})
	text, effect, err := assembler.AssembleInitialContextObserved(
		context.Background(), application.RunIdentity{}, nil,
		domain.RepoRef{ProjectID: "project-1", Commit: "abc123"},
		domain.EvidenceScope{ProjectID: "project-1", EnvironmentID: "env-1", SourceID: "source-1"},
		domain.SourceCapabilitySnapshot{
			Kind: "ssh", SSHHost: "logs.example.invalid", SSHUser: "app",
			SSHProjectFolder: "/srv/app", SSHLogPath: "/var/log",
		},
	)
	if err != nil {
		t.Fatalf("AssembleInitialContextObserved() error = %v", err)
	}
	if effect != (domain.Effect{}) {
		t.Fatalf("bootstrap effect = %#v, want empty", effect)
	}
	for _, want := range []string{
		"host=logs.example.invalid", "user=app", "projectFolder=/srv/app", "logPath=/var/log",
		"discover actual file names", "never auto-tails",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("bootstrap missing %q: %s", want, text)
		}
	}
}
