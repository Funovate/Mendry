package git_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	projectapplication "fixthe/backend/internal/modules/projects/application"
	projectdomain "fixthe/backend/internal/modules/projects/domain"
	"fixthe/backend/internal/modules/remediation/adapter/git"
	"fixthe/backend/internal/modules/remediation/domain"
	"fixthe/backend/internal/platform/observability"
)

const (
	testProjectID = "019ff544-405c-7d21-9f10-cb3fc579605c"
	testSecretID  = "019ff544-405c-7d24-9f10-cb3fc579605c"
)

type staticConfig struct {
	cfg git.RepositoryConfig
}

func TestReaderLogsCloneAndFetchWithPublicIdentity(t *testing.T) {
	dir, old, _ := initLocalRepo(t)
	var output bytes.Buffer
	logger, err := observability.NewLogger(observability.LoggerOptions{
		Writer: &output, Level: "debug", Format: "json", Service: "test", Environment: "test",
	})
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}
	reader, err := git.NewReader(git.Options{
		Configs: staticConfig{cfg: git.RepositoryConfig{
			RemoteURL: "file://" + dir, Transport: "file", CredentialSecretID: testSecretID,
		}},
		Secrets: staticSecrets{secret: projectdomain.EncryptedSecret{Secret: projectdomain.Secret{
			ID: testSecretID, ProjectID: testProjectID,
		}}},
		Cipher: staticCipher{}, CacheDir: t.TempDir(), Logger: logger,
	})
	if err != nil {
		t.Fatalf("NewReader() error = %v", err)
	}
	ref := domain.RepoRef{ProjectID: testProjectID, RemoteURL: "file://" + dir, Commit: old}
	if _, err := reader.ListTree(context.Background(), ref, "", domain.TreeOptions{}); err != nil {
		t.Fatalf("first ListTree() error = %v", err)
	}
	if _, err := reader.ListTree(context.Background(), ref, "", domain.TreeOptions{}); err != nil {
		t.Fatalf("second ListTree() error = %v", err)
	}
	operations := map[string]bool{}
	metadataVisible := false
	scanner := bufio.NewScanner(&output)
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("decode log: %v", err)
		}
		if record[observability.FieldEvent] == observability.EventGitRequestCompleted {
			operations[record[observability.FieldGitOperation].(string)] = true
			metadataVisible = record[observability.FieldCredentialSecretID] == testSecretID &&
				record[observability.FieldTransport] == "file"
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan logs: %v", err)
	}
	if !operations["clone"] || !operations["fetch"] {
		t.Fatalf("git operations = %#v; logs=%s", operations, output.String())
	}
	if !metadataVisible {
		t.Fatalf("git credential metadata missing: %s", output.String())
	}
	if strings.Contains(output.String(), "unused") {
		t.Fatalf("git dependency log leaked credential material: %s", output.String())
	}
}

func (s staticConfig) LoadRepository(context.Context, string) (git.RepositoryConfig, error) {
	return s.cfg, nil
}

type staticSecrets struct {
	secret projectdomain.EncryptedSecret
}

func (s staticSecrets) GetEncryptedSecret(context.Context, string, string) (projectdomain.EncryptedSecret, error) {
	return s.secret, nil
}

type staticCipher struct{}

func (staticCipher) Encrypt(string, string, projectdomain.SecretKind, []byte) ([]byte, []byte, int32, error) {
	return nil, nil, 0, errors.New("unused")
}
func (staticCipher) Decrypt(string, string, projectdomain.SecretKind, []byte, []byte) ([]byte, error) {
	return []byte("unused"), nil
}
func (staticCipher) EncryptWebhookToken(string, string, []byte) ([]byte, []byte, error) {
	return nil, nil, errors.New("unused")
}
func (staticCipher) DecryptWebhookToken(string, string, []byte, []byte) ([]byte, error) {
	return nil, errors.New("unused")
}

func initLocalRepo(t *testing.T) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = dir
		command.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=tester",
			"GIT_AUTHOR_EMAIL=tester@example.invalid",
			"GIT_COMMITTER_NAME=tester",
			"GIT_COMMITTER_EMAIL=tester@example.invalid",
		)
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	run("init", "-q")
	run("config", "user.name", "tester")
	run("config", "user.email", "tester@example.invalid")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "readme.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "first")
	old := strings.TrimSpace(run("rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(dir, "later.go"), []byte("package later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "binary.bin"), []byte{0x00, 0x01, 0x02}, 0o644); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("x", 64)
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(big+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "second")
	newCommit := strings.TrimSpace(run("rev-parse", "HEAD"))
	return dir, old, newCommit
}

func newTestReader(t *testing.T, dir string) *git.Reader {
	t.Helper()
	reader, err := git.NewReader(git.Options{
		Configs: staticConfig{cfg: git.RepositoryConfig{
			RemoteURL: "file://" + dir,
			Transport: "file",
		}},
		Secrets:  staticSecrets{secret: projectdomain.EncryptedSecret{Secret: projectdomain.Secret{ID: testSecretID, ProjectID: testProjectID}}},
		Cipher:   staticCipher{},
		CacheDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewReader() error = %v", err)
	}
	return reader
}

func TestReaderReadsExactDeployedCommit(t *testing.T) {
	dir, old, _ := initLocalRepo(t)
	reader := newTestReader(t, dir)
	ref := domain.RepoRef{ProjectID: testProjectID, RemoteURL: "file://" + dir, Commit: old}

	listing, err := reader.ListTree(context.Background(), ref, "", domain.TreeOptions{MaxDepth: 2, MaxEntries: 50})
	if err != nil {
		t.Fatalf("ListTree() error = %v", err)
	}
	paths := map[string]bool{}
	for _, entry := range listing.Entries {
		paths[entry.Path] = true
	}
	if !paths["main.go"] || paths["later.go"] {
		t.Fatalf("list at old commit = %#v", listing.Entries)
	}

	content, err := reader.ReadFile(context.Background(), ref, "main.go", domain.ReadOptions{MaxBytes: 1024})
	if err != nil || !strings.Contains(string(content.Content), "package main") {
		t.Fatalf("ReadFile() = %#v, err=%v", content, err)
	}
	if _, err := reader.ReadFile(context.Background(), ref, "later.go", domain.ReadOptions{MaxBytes: 1024}); err == nil {
		t.Fatal("ReadFile(later.go) at old commit succeeded")
	}
}

func TestReaderHonorsBoundsAndBinaryRefusal(t *testing.T) {
	dir, _, head := initLocalRepo(t)
	reader := newTestReader(t, dir)
	ref := domain.RepoRef{ProjectID: testProjectID, RemoteURL: "file://" + dir, Commit: head}

	listing, err := reader.ListTree(context.Background(), ref, "", domain.TreeOptions{MaxDepth: 1, MaxEntries: 1})
	if err != nil || !listing.Truncated || len(listing.Entries) != 1 {
		t.Fatalf("ListTree bounds = %#v err=%v", listing, err)
	}

	binary, err := reader.ReadFile(context.Background(), ref, "binary.bin", domain.ReadOptions{MaxBytes: 1024})
	if err != nil || !binary.Truncated || binary.Reason != "binary" || len(binary.Content) != 0 {
		t.Fatalf("binary ReadFile = %#v err=%v", binary, err)
	}

	oversized, err := reader.ReadFile(context.Background(), ref, "big.txt", domain.ReadOptions{MaxBytes: 8})
	if err != nil || !oversized.Truncated || oversized.Reason != "oversized" || len(oversized.Content) != 8 {
		t.Fatalf("oversized ReadFile = %#v err=%v", oversized, err)
	}

	search, err := reader.Search(context.Background(), ref, domain.SearchQuery{Pattern: "package", MaxResults: 1})
	if err != nil || !search.Truncated || len(search.Matches) != 1 {
		t.Fatalf("Search bounds = %#v err=%v", search, err)
	}

	history, err := reader.History(context.Background(), ref, "", domain.HistoryOptions{MaxCommits: 1})
	if err != nil || !history.Truncated || len(history.Commits) != 1 {
		t.Fatalf("History bounds = %#v err=%v", history, err)
	}
}

func TestReaderRejectsPathTraversalAndHidesCredentials(t *testing.T) {
	dir, _, head := initLocalRepo(t)
	reader := newTestReader(t, dir)
	ref := domain.RepoRef{ProjectID: testProjectID, RemoteURL: "file://" + dir, Commit: head}
	if _, err := reader.ReadFile(context.Background(), ref, "../secret", domain.ReadOptions{MaxBytes: 16}); err == nil {
		t.Fatal("traversal succeeded")
	}
	if _, err := reader.ListTree(context.Background(), ref, "/etc/passwd", domain.TreeOptions{}); err == nil {
		t.Fatal("absolute path succeeded")
	}

	token := "super-secret-token"
	pem := "-----BEGIN OPENSSH PRIVATE KEY-----\nsecret-key\n-----END OPENSSH PRIVATE KEY-----\n"
	err := projectapplication.ErrGitUnreachable
	if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), pem) {
		t.Fatal("unreachable error leaked credential")
	}
	sum := sha256.Sum256([]byte(token))
	if hex.EncodeToString(sum[:]) == token {
		t.Fatal("hash equals raw token")
	}
	url, err := reader.CredentialFreeRemoteURL(context.Background(), testProjectID)
	if err != nil || strings.Contains(url, "@") || strings.Contains(url, token) {
		t.Fatalf("CredentialFreeRemoteURL() = %q err=%v", url, err)
	}
}

func TestReaderPreservesSafeGitDiagnostic(t *testing.T) {
	dir := t.TempDir()
	stub := filepath.Join(dir, "git-stub")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nprintf '%s\\n' 'fatal: Could not resolve host: codeup.aliyun.com' >&2\nexit 128\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	reader, err := git.NewReader(git.Options{
		Configs: staticConfig{cfg: git.RepositoryConfig{RemoteURL: "file:///tmp/repository", Transport: "file"}},
		Secrets: staticSecrets{secret: projectdomain.EncryptedSecret{Secret: projectdomain.Secret{ID: testSecretID, ProjectID: testProjectID}}},
		Cipher:  staticCipher{}, GitCommand: stub, CacheDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewReader() error = %v", err)
	}
	_, err = reader.ListTree(context.Background(), domain.RepoRef{ProjectID: testProjectID, Commit: "deadbeef"}, "", domain.TreeOptions{})
	if !errors.Is(err, projectapplication.ErrGitUnreachable) {
		t.Fatalf("error = %v, want ErrGitUnreachable", err)
	}
	if !strings.Contains(err.Error(), "Could not resolve host: codeup.aliyun.com") {
		t.Fatalf("error = %v, want safe git diagnostic", err)
	}
}

func TestReaderCachedOriginHasNoUserinfo(t *testing.T) {
	dir, old, _ := initLocalRepo(t)
	cache := t.TempDir()
	reader, err := git.NewReader(git.Options{
		Configs: staticConfig{cfg: git.RepositoryConfig{
			RemoteURL: "file://" + dir,
			Transport: "file",
		}},
		Secrets:  staticSecrets{secret: projectdomain.EncryptedSecret{Secret: projectdomain.Secret{ID: testSecretID, ProjectID: testProjectID}}},
		Cipher:   staticCipher{},
		CacheDir: cache,
	})
	if err != nil {
		t.Fatalf("NewReader() error = %v", err)
	}
	ref := domain.RepoRef{ProjectID: testProjectID, RemoteURL: "file://" + dir, Commit: old}
	if _, err := reader.ListTree(context.Background(), ref, "", domain.TreeOptions{MaxEntries: 8}); err != nil {
		t.Fatalf("ListTree() error = %v", err)
	}
	found := false
	entries, err := os.ReadDir(cache)
	if err != nil {
		t.Fatalf("readdir cache: %v", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		command := exec.Command("git", "remote", "get-url", "origin")
		command.Dir = filepath.Join(cache, entry.Name())
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git remote get-url: %v\n%s", err, out)
		}
		origin := strings.TrimSpace(string(out))
		if strings.Contains(origin, "@") || strings.Contains(origin, "token") {
			t.Fatalf("cached origin leaked credential: %q", origin)
		}
		found = true
	}
	if !found {
		t.Fatal("expected a cached clone")
	}
}
