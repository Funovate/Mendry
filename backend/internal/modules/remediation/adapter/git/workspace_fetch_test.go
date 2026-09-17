package git

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"mendry/backend/internal/modules/remediation/domain"
)

func TestGitWorkspaceFetchesFullBaselineWithoutBlobFilter(t *testing.T) {
	gitCommand, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is unavailable")
	}
	root := t.TempDir()
	seed := filepath.Join(root, "seed")
	if err := os.Mkdir(seed, 0o700); err != nil {
		t.Fatal(err)
	}
	testGit(t, seed, "init", "-b", "main")
	testGit(t, seed, "config", "user.name", "workspace test")
	testGit(t, seed, "config", "user.email", "workspace@example.invalid")
	writeTestFile(t, seed, "main.go", "package main\n")
	testGit(t, seed, "add", "main.go")
	testGit(t, seed, "commit", "-m", "baseline")
	baseline := strings.TrimSpace(string(testGit(t, seed, "rev-parse", "HEAD")))

	// Local remotes may ignore filters, so reject the argument explicitly.
	wrapper := filepath.Join(root, "git-wrapper")
	script := "#!/bin/sh\nfor arg in \"$@\"; do\n  case \"$arg\" in\n    --filter*) exit 97 ;;\n  esac\ndone\nexec " + strconv.Quote(gitCommand) + " \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	workspace, err := NewGitWorkspace(WorkspaceOptions{
		Root: filepath.Join(root, "workspaces"), GitCommand: wrapper,
		Artifacts: memoryArtifacts{values: map[string][]byte{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	workspace.allowLocal = true
	identity, err := workspace.Ensure(context.Background(), domain.WorkspaceRequest{
		RunID: "run-full-fetch", ProjectID: "project-full-fetch", BaselineCommit: baseline,
		IdempotencyKey: "workspace:run-full-fetch",
		Profile:        domain.ExecutionProfileSnapshot{CPULimit: 2, MemoryLimitMiB: 4096, WorkspaceLimitMiB: 1024},
		Repository:     domain.PublicationSnapshot{RemoteURL: (&url.URL{Scheme: "file", Path: seed}).String(), Transport: "file"},
		ChangePolicy:   domain.ChangePolicySnapshot{AllowedPaths: []string{"**"}, MaxChangedFiles: 10, MaxChangedLines: 400},
	})
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if identity.BaselineCommit != baseline || identity.BaseTreeHash != identity.CurrentTreeHash {
		t.Fatalf("workspace identity = %+v", identity)
	}
	repo := filepath.Join(workspace.root, identity.WorkspaceID, "repo")
	if got := strings.TrimSpace(string(testGit(t, repo, "rev-parse", "--is-shallow-repository"))); got != "true" {
		t.Fatalf("repository should remain shallow, got %q", got)
	}
	content, err := os.ReadFile(filepath.Join(repo, "main.go"))
	if err != nil || string(content) != "package main\n" {
		t.Fatalf("checked-out baseline = %q, %v", content, err)
	}
}
