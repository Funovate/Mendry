package git

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"mendry/backend/internal/modules/remediation/domain"
)

func TestPublisherShallowFetchContainsBaselineBlobs(t *testing.T) {
	root := t.TempDir()
	seed := filepath.Join(root, "seed")
	repo := filepath.Join(root, "repo")
	for _, dir := range []string{seed, repo} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	testGit(t, seed, "init", "-b", "main")
	testGit(t, seed, "config", "user.name", "test")
	testGit(t, seed, "config", "user.email", "test@example.invalid")
	writeTestFile(t, seed, "main.go", "package main\n")
	testGit(t, seed, "add", ".")
	testGit(t, seed, "commit", "-m", "baseline")
	baseline := strings.TrimSpace(string(testGit(t, seed, "rev-parse", "HEAD")))
	binary, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(root, "git-wrapper")
	script := "#!/bin/sh\nfor arg in \"$@\"; do\n case \"$arg\" in --filter*) exit 97 ;; push) " + strconv.Quote(binary) + " \"$@\"; exit 98 ;; esac\ndone\nexec " + strconv.Quote(binary) + " \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, seed, "main.go", "package main\n\nfunc fixed() {}\n")
	patch := testGit(t, seed, "diff", "--binary", baseline)
	testGit(t, seed, "add", ".")
	tree := strings.TrimSpace(string(testGit(t, seed, "write-tree")))
	sum := sha256.Sum256(patch)
	hash := hex.EncodeToString(sum[:])
	p := &Publisher{command: wrapper, artifacts: memoryArtifacts{values: map[string][]byte{"patch": patch}}, credentials: unusedCredentialResolver{}, allowLocalRemotes: true}
	result, err := p.PublishGit(context.Background(), domain.GitPublicationRequest{
		PublicationRequest: domain.PublicationRequest{
			RunID: "shallow-run", ProjectID: "project", BaselineCommit: baseline, TargetBranch: "main", BranchRef: "hotfix/shallow",
			CommitMessage: "fix", PatchArtifactRef: "patch", PatchContentHash: hash, ExpectedTreeHash: tree, IdempotencyKey: "publish:shallow",
			Repository:   domain.PublicationSnapshot{RemoteURL: (&url.URL{Scheme: "file", Path: seed}).String(), Transport: "file"},
			ChangePolicy: domain.ChangePolicySnapshot{AllowedPaths: []string{"**"}, MaxChangedFiles: 10, MaxChangedLines: 400}, WorkspaceLimitMiB: 1024,
		}, AuthoredAt: "2026-01-01T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("publication must succeed without a blob filter: %v", err)
	}
	if !result.AlreadyPublished || !result.RemoteVerified {
		t.Fatalf("ambiguous push must be reconciled: %+v", result)
	}
	p = &Publisher{command: wrapper}
	env := sanitizedGitEnvironment(os.Environ(), root)
	run := func(args ...string) []byte {
		out, err := p.runGit(context.Background(), repo, env, nil, args...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	run("init", "--quiet")
	run("remote", "add", "origin", "file://"+seed)
	run("fetch", "--no-tags", "--depth=1", "origin", baseline)
	run("ls-tree", "-r", "-l", "FETCH_HEAD")
	if strings.TrimSpace(string(run("rev-parse", "--is-shallow-repository"))) != "true" {
		t.Fatal("baseline must remain shallow")
	}
	run("checkout", "--detach", "FETCH_HEAD")
}

func TestPublisherCommandTimeoutIsRetryable(t *testing.T) {
	root := t.TempDir()
	wrapper := filepath.Join(root, "slow-git")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	p := &Publisher{command: wrapper, localCommandTimeout: 50 * time.Millisecond}
	started := time.Now()
	_, err := p.runGit(context.Background(), root, os.Environ(), nil, "ls-tree", "HEAD")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected timeout, got %v", err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("command cancellation did not complete promptly")
	}
	var failure *domain.LifecycleRuntimeError
	if !errors.As(lifecycleError("git_baseline_unsafe", false, err), &failure) || !failure.Retryable || failure.Code != "git_ls_tree_timeout" {
		t.Fatalf("unexpected timeout classification: %+v", failure)
	}
}

func TestPublisherParentCancellationIsPreserved(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &Publisher{command: "git"}
	_, err := p.runGit(ctx, t.TempDir(), os.Environ(), nil, "status")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected parent cancellation, got %v", err)
	}
}
