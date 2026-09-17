package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mendry/backend/internal/modules/remediation/domain"
)

type probeCredential struct {
	id      string
	version int64
}

func (p *probeCredential) ResolveRepositoryCredential(_ context.Context, _ string, id string, v int64, _ string) ([]byte, error) {
	p.id = id
	p.version = v
	return []byte("user:test-only"), nil
}
func TestPublicationCheckUsesWriteCredentialAndDryRun(t *testing.T) {
	root := t.TempDir()
	log := filepath.Join(root, "args")
	cmd := filepath.Join(root, "git-stub")
	if err := os.WriteFile(cmd, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > '"+log+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	credentials := &probeCredential{}
	w, err := NewGitWorkspace(WorkspaceOptions{Root: filepath.Join(root, "workspaces"), GitCommand: cmd, Artifacts: memoryArtifacts{values: map[string][]byte{}}, Credentials: credentials})
	if err != nil {
		t.Fatal(err)
	}
	request := domain.WorkspaceRequest{RunID: "check", ProjectID: "project", BaselineCommit: strings.Repeat("a", 40), IdempotencyKey: "check", Profile: domain.ExecutionProfileSnapshot{CPULimit: 2, MemoryLimitMiB: 4096, WorkspaceLimitMiB: 1024}, Repository: domain.PublicationSnapshot{RemoteURL: "https://example.test/repo.git", Transport: "https", GitCredentialSecretID: "write-secret", GitCredentialVersion: 3, RepositoryCredentialSecretID: "read-secret", RepositoryCredentialVersion: 1}}
	identity := domain.WorkspaceIdentity{WorkspaceID: workspaceID("project", "check"), RunID: "check", BaselineCommit: request.BaselineCommit, BaseTreeHash: strings.Repeat("b", 40), CurrentTreeHash: strings.Repeat("b", 40), Version: 1}
	directory := filepath.Join(w.root, identity.WorkspaceID)
	if err := os.MkdirAll(filepath.Join(directory, "repo"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := w.writeMetadata(directory, workspaceMetadata{FormatVersion: 1, ProjectID: "project", Identity: identity, WorkspaceLimitMiB: 1024, Patches: map[string]workspacePatchRecord{}}); err != nil {
		t.Fatal(err)
	}
	if err := w.CheckPublicationAccess(context.Background(), request, identity); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "push\n--dry-run\n--no-verify\n") || credentials.id != "write-secret" || credentials.version != 3 || strings.Contains(string(args), "test-only") {
		t.Fatalf("invalid probe: %s %+v", args, credentials)
	}
	request.ProjectID = "different"
	if err := w.CheckPublicationAccess(context.Background(), request, identity); err == nil {
		t.Fatal("cross-project identity accepted")
	}
}
