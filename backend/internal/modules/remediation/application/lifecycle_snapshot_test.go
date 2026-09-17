package application

import (
	"testing"

	"mendry/backend/internal/modules/remediation/domain"
)

func validAutoHotfixRunSnapshot() domain.Run {
	profile := domain.ExecutionProfileSnapshot{
		ImageDigest:       "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		WorkingDirectory:  ".",
		RequiredCommands:  []domain.ValidationCommandSnapshot{{ID: "unit", Version: 3, Argv: []string{"go", "test", "./..."}, TimeoutSeconds: 600}},
		CPULimit:          2,
		MemoryLimitMiB:    4096,
		WorkspaceLimitMiB: 10240,
	}
	return domain.Run{
		ExecutionMode:           domain.ExecutionModeAutoHotfix,
		ValidationCommands:      map[string]int64{"unit": 3},
		ValidationImageDigest:   profile.ImageDigest,
		ExecutionProfile:        profile,
		PublicationTargetBranch: "main",
		PublicationSnapshot: domain.PublicationSnapshot{
			RemoteURL: "https://git.example.test/team/app.git", SCMProvider: "github", Transport: "https",
			ProductionBranch: "main", GitCredentialSecretID: "secret-id", GitCredentialVersion: 2,
		},
		ChangePolicySnapshot: domain.ChangePolicySnapshot{
			AllowedPaths: []string{"**"}, DeniedPaths: []string{}, MaxChangedFiles: 10, MaxChangedLines: 400,
		},
	}
}

func TestValidateAutoHotfixSnapshot(t *testing.T) {
	if err := validateAutoHotfixSnapshot(validAutoHotfixRunSnapshot()); err != nil {
		t.Fatalf("valid snapshot rejected: %v", err)
	}

	localValidation := false
	basic := validAutoHotfixRunSnapshot()
	basic.ExecutionProfile = domain.ExecutionProfileSnapshot{Enabled: &localValidation}
	basic.ValidationCommands = map[string]int64{}
	basic.ValidationImageDigest = ""
	if err := validateAutoHotfixSnapshot(basic); err != nil {
		t.Fatalf("valid basic snapshot rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*domain.Run)
	}{
		{name: "missing image", mutate: func(run *domain.Run) { run.ExecutionProfile.ImageDigest = "" }},
		{name: "command version drift", mutate: func(run *domain.Run) { run.ValidationCommands["unit"] = 4 }},
		{name: "repository authority", mutate: func(run *domain.Run) {
			run.PublicationSnapshot.RemoteURL = "https://user:password@git.example.test/repo.git"
		}},
		{name: "target branch drift", mutate: func(run *domain.Run) { run.PublicationTargetBranch = "release" }},
		{name: "missing write credential version", mutate: func(run *domain.Run) { run.PublicationSnapshot.GitCredentialVersion = 0 }},
		{name: "path traversal policy", mutate: func(run *domain.Run) { run.ChangePolicySnapshot.AllowedPaths = []string{"../**"} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			run := validAutoHotfixRunSnapshot()
			test.mutate(&run)
			if err := validateAutoHotfixSnapshot(run); err == nil {
				t.Fatal("invalid immutable snapshot was accepted")
			}
		})
	}
}
