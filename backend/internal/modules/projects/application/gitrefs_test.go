package application

import (
	"strings"
	"testing"
)

func TestParseGitLsRemoteUsesSymbolicHead(t *testing.T) {
	refs, err := ParseGitLsRemote(strings.Join([]string{
		"ref: refs/heads/release/2026.08	HEAD",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa	HEAD",
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb	refs/heads/main",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa	refs/heads/release/2026.08",
	}, "\n"))
	if err != nil {
		t.Fatalf("ParseGitLsRemote() error = %v", err)
	}
	if refs.DefaultBranch != "release/2026.08" || refs.DeployedCommit != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("refs = %#v", refs)
	}
	if len(refs.Branches) != 2 {
		t.Fatalf("branches = %#v", refs.Branches)
	}
}

func TestParseGitLsRemoteFallsBackToMain(t *testing.T) {
	refs, err := ParseGitLsRemote("cccccccccccccccccccccccccccccccccccccccc	refs/heads/main\ndddddddddddddddddddddddddddddddddddddddd	refs/heads/develop\n")
	if err != nil {
		t.Fatalf("ParseGitLsRemote() error = %v", err)
	}
	if refs.DefaultBranch != "main" || refs.DeployedCommit != "cccccccccccccccccccccccccccccccccccccccc" {
		t.Fatalf("refs = %#v", refs)
	}
}

func TestAuthenticatedHTTPSRemoteOmitsEmptyUsernamePrefix(t *testing.T) {
	remote, err := AuthenticatedHTTPSRemote("https://git.example.internal/app.git", "deploy:token-value")
	if err != nil {
		t.Fatalf("AuthenticatedHTTPSRemote() error = %v", err)
	}
	if remote != "https://deploy:token-value@git.example.internal/app.git" {
		t.Fatalf("remote = %q", remote)
	}
}

func TestParseGitLsRemoteRejectsEmptyHeads(t *testing.T) {
	if _, err := ParseGitLsRemote("not a git listing"); err != ErrGitUnreachable {
		t.Fatalf("error = %v", err)
	}
}
