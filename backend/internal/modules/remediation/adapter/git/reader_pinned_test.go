package git_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"mendry/backend/internal/modules/remediation/domain"
)

func localGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func TestReaderFailsClosedForInvalidOrUnavailableCommit(t *testing.T) {
	dir, _, head := initLocalRepo(t)
	blob := localGitOutput(t, dir, "rev-parse", head+":main.go")
	tree := localGitOutput(t, dir, "rev-parse", head+"^{tree}")
	for _, commit := range []string{"", "abc123", "main", "HEAD", head+"~1", strings.Repeat("z", 40), strings.Repeat("f", 40), blob, tree} {
		t.Run(commit, func(t *testing.T) {
			reader := newTestReader(t, dir)
			ref := domain.RepoRef{ProjectID: testProjectID, Commit: commit}
			ctx := context.Background()
			if _, err := reader.ListTree(ctx, ref, "", domain.TreeOptions{}); err == nil {
				t.Fatal("ListTree accepted invalid baseline")
			}
			if _, err := reader.ReadFile(ctx, ref, "main.go", domain.ReadOptions{}); err == nil {
				t.Fatal("ReadFile accepted invalid baseline")
			}
			if _, err := reader.Search(ctx, ref, domain.SearchQuery{Pattern: "package"}); err == nil {
				t.Fatal("Search accepted invalid baseline")
			}
			if _, err := reader.History(ctx, ref, "", domain.HistoryOptions{}); err == nil {
				t.Fatal("History accepted invalid baseline")
			}
		})
	}
}

func TestReaderFetchesExactUnreferencedCommit(t *testing.T) {
	dir, old, _ := initLocalRepo(t)
	tree := localGitOutput(t, dir, "rev-parse", old+"^{tree}")
	commit := localGitOutput(t, dir, "commit-tree", tree, "-m", "unreferenced deployment")
	reader := newTestReader(t, dir)
	ref := domain.RepoRef{ProjectID: testProjectID, Commit: commit}
	listing, err := reader.ListTree(context.Background(), ref, "", domain.TreeOptions{})
	if err != nil || len(listing.Entries) != 2 {
		t.Fatalf("exact SHA fetch listing = %#v, err=%v", listing, err)
	}
	history, err := reader.History(context.Background(), ref, "", domain.HistoryOptions{})
	if err != nil || len(history.Commits) != 1 || history.Commits[0].Hash != commit {
		t.Fatalf("exact SHA fetch history = %#v, err=%v", history, err)
	}
}
