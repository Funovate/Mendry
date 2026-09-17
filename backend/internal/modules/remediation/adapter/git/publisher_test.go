package git

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	projectsecret "mendry/backend/internal/modules/projects/adapter/secret"
	projectdomain "mendry/backend/internal/modules/projects/domain"
	"mendry/backend/internal/modules/remediation/domain"
)

type memoryArtifacts struct {
	values map[string][]byte
}

func (m memoryArtifacts) Get(_ context.Context, reference string, maxBytes int64) ([]byte, error) {
	value, ok := m.values[reference]
	if !ok || int64(len(value)) > maxBytes {
		return nil, os.ErrNotExist
	}
	return append([]byte(nil), value...), nil
}

func (m memoryArtifacts) Put(_ context.Context, content []byte) (string, string, error) {
	digest := sha256.Sum256(content)
	hash := hex.EncodeToString(digest[:])
	reference := "sha256:" + hash
	m.values[reference] = append([]byte(nil), content...)
	return reference, hash, nil
}

type unusedCredentialResolver struct{}

func (unusedCredentialResolver) ResolveRepositoryCredential(context.Context, string, string, int64, string) ([]byte, error) {
	return nil, nil
}

type storedGitSecret struct {
	secret projectdomain.EncryptedSecret
	err    error
}

func (s storedGitSecret) GetEncryptedSecret(context.Context, string, string) (projectdomain.EncryptedSecret, error) {
	return s.secret, s.err
}

func TestVersionedCredentialResolverRequiresTheSnapshottedSecretVersion(t *testing.T) {
	const projectID = "019ff544-405c-7d21-9f10-cb3fc579605c"
	const secretID = "019ff544-405c-7d24-9f10-cb3fc579605c"
	cipher, err := projectsecret.NewAESGCM(bytes.Repeat([]byte{0x37}, 32))
	if err != nil {
		t.Fatal(err)
	}
	plaintext := []byte("test-token-value")
	ciphertext, nonce, keyVersion, err := cipher.Encrypt(projectID, secretID, projectdomain.SecretGitCredential, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	secret := projectdomain.EncryptedSecret{
		Secret:     projectdomain.Secret{ID: secretID, ProjectID: projectID, Kind: projectdomain.SecretGitCredential, KeyVersion: keyVersion, Version: 7},
		Ciphertext: ciphertext, Nonce: nonce,
	}
	resolver, err := NewVersionedCredentialResolver(storedGitSecret{secret: secret}, cipher)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolver.ResolveGitCredential(context.Background(), projectID, secretID, 7)
	if err != nil || !bytes.Equal(resolved, plaintext) {
		t.Fatalf("ResolveGitCredential() = %q, %v", resolved, err)
	}
	zeroBytes(resolved)
	if _, err := resolver.ResolveGitCredential(context.Background(), projectID, secretID, 6); err == nil {
		t.Fatal("credential version drift did not fail closed")
	}
}

func TestVersionedCredentialResolverEnforcesCredentialKindsByUse(t *testing.T) {
	const projectID = "019ff544-405c-7d21-9f10-cb3fc579605c"
	const secretID = "019ff544-405c-7d24-9f10-cb3fc579605c"
	cipher, err := projectsecret.NewAESGCM(bytes.Repeat([]byte{0x29}, 32))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		kind      projectdomain.SecretKind
		transport string
		resolve   func(*VersionedCredentialResolver) ([]byte, error)
	}{
		{
			kind: projectdomain.SecretHTTPBearer, transport: "https",
			resolve: func(resolver *VersionedCredentialResolver) ([]byte, error) {
				return resolver.ResolveRepositoryCredential(context.Background(), projectID, secretID, 4, "https")
			},
		},
		{
			kind: projectdomain.SecretSSHPrivateKey, transport: "ssh",
			resolve: func(resolver *VersionedCredentialResolver) ([]byte, error) {
				return resolver.ResolveRepositoryCredential(context.Background(), projectID, secretID, 4, "ssh")
			},
		},
		{
			kind: projectdomain.SecretHTTPBearer, transport: "api",
			resolve: func(resolver *VersionedCredentialResolver) ([]byte, error) {
				return resolver.ResolveAPICredential(context.Background(), projectID, secretID, 4)
			},
		},
	} {
		plaintext := []byte("kind-scoped-secret")
		ciphertext, nonce, keyVersion, err := cipher.Encrypt(projectID, secretID, test.kind, plaintext)
		if err != nil {
			t.Fatal(err)
		}
		secret := projectdomain.EncryptedSecret{
			Secret:     projectdomain.Secret{ID: secretID, ProjectID: projectID, Kind: test.kind, KeyVersion: keyVersion, Version: 4},
			Ciphertext: ciphertext, Nonce: nonce,
		}
		resolver, err := NewVersionedCredentialResolver(storedGitSecret{secret: secret}, cipher)
		if err != nil {
			t.Fatal(err)
		}
		resolved, err := test.resolve(resolver)
		if err != nil || !bytes.Equal(resolved, plaintext) {
			t.Fatalf("resolve kind %s via %s = %q, %v", test.kind, test.transport, resolved, err)
		}
		zeroBytes(resolved)
		if test.kind == projectdomain.SecretHTTPBearer {
			if _, err := resolver.ResolveRepositoryCredential(context.Background(), projectID, secretID, 4, "ssh"); err == nil {
				t.Fatal("HTTPS bearer credential was accepted for SSH transport")
			}
		}
	}
}

func TestGitWorkspaceAppliesAndRecoversPatchIdempotently(t *testing.T) {
	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	repair := filepath.Join(root, "repair")
	if err := os.Mkdir(seed, 0o700); err != nil {
		t.Fatal(err)
	}
	testGit(t, "", "init", "--bare", bare)
	testGit(t, seed, "init", "-b", "main")
	testGit(t, seed, "config", "user.name", "workspace test")
	testGit(t, seed, "config", "user.email", "workspace@example.invalid")
	writeTestFile(t, seed, "main.go", "package main\n\nfunc status() string { return \"deployed\" }\n")
	testGit(t, seed, "add", "main.go")
	testGit(t, seed, "commit", "-m", "deployed")
	testGit(t, seed, "remote", "add", "origin", bare)
	testGit(t, seed, "push", "origin", "main:refs/heads/main")
	baseline := strings.TrimSpace(string(testGit(t, seed, "rev-parse", "HEAD")))
	remoteURL := (&url.URL{Scheme: "file", Path: bare}).String()

	testGit(t, "", "clone", bare, repair)
	testGit(t, repair, "checkout", "--detach", baseline)
	writeTestFile(t, repair, "main.go", "package main\n\nfunc status() string { return \"fixed\" }\n")
	patch := testGit(t, repair, "diff", "--binary", baseline, "--")

	workspace, err := NewGitWorkspace(WorkspaceOptions{Root: filepath.Join(root, "workspaces"), Artifacts: memoryArtifacts{values: map[string][]byte{}}})
	if err != nil {
		t.Fatal(err)
	}
	workspace.allowLocal = true
	request := domain.WorkspaceRequest{
		RunID: "run-workspace", ProjectID: "project-workspace", BaselineCommit: baseline,
		IdempotencyKey: "workspace:run-workspace",
		Profile:        domain.ExecutionProfileSnapshot{CPULimit: 2, MemoryLimitMiB: 4096, WorkspaceLimitMiB: 1024},
		Repository:     domain.PublicationSnapshot{RemoteURL: remoteURL, Transport: "file"},
		ChangePolicy:   domain.ChangePolicySnapshot{AllowedPaths: []string{"**"}, MaxChangedFiles: 10, MaxChangedLines: 400},
	}
	partialDirectory := filepath.Join(workspace.root, workspaceID(request.ProjectID, request.RunID))
	if err := os.Mkdir(partialDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(partialDirectory, "partial"), []byte("interrupted setup"), 0o600); err != nil {
		t.Fatal(err)
	}
	identity, err := workspace.Ensure(context.Background(), request)
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if !sameObjectID(identity.BaselineCommit, baseline) || identity.BaseTreeHash != identity.CurrentTreeHash {
		t.Fatalf("initial workspace identity = %+v", identity)
	}
	file, err := workspace.ReadFile(context.Background(), identity, "main.go", 1024)
	if err != nil || !strings.Contains(string(file.Content), "deployed") {
		t.Fatalf("ReadFile() = %q, %v", file.Content, err)
	}
	status, err := workspace.Status(context.Background(), identity)
	if err != nil || !status.Clean {
		t.Fatalf("initial Status() = %+v, %v", status, err)
	}
	patchRequest := domain.PatchRequest{
		WorkspaceID: identity.WorkspaceID, Patch: string(patch), ExpectedTreeHash: identity.CurrentTreeHash,
		IdempotencyKey: "patch:run-workspace:1", ChangePolicy: request.ChangePolicy,
	}
	result, err := workspace.ApplyPatch(context.Background(), identity, patchRequest)
	if err != nil {
		t.Fatalf("ApplyPatch() error = %v", err)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("PatchResult.Validate() error = %v", err)
	}
	identity.CurrentTreeHash = result.ResultTreeHash
	identity.Version++
	status, err = workspace.Status(context.Background(), identity)
	if err != nil || status.Clean || len(status.ChangedFiles) != 1 || status.ChangedFiles[0] != "main.go" {
		t.Fatalf("patched Status() = %+v, %v", status, err)
	}
	duplicate, err := workspace.ApplyPatch(context.Background(), identity, patchRequest)
	if err != nil || !duplicate.AlreadyApplied || duplicate.ResultTreeHash != result.ResultTreeHash {
		t.Fatalf("idempotent ApplyPatch() = %+v, %v", duplicate, err)
	}
	recovered, err := workspace.Ensure(context.Background(), request)
	if err != nil || recovered.CurrentTreeHash != result.ResultTreeHash || recovered.Version != identity.Version {
		t.Fatalf("recovered Ensure() = %+v, %v", recovered, err)
	}
	if err := workspace.Destroy(context.Background(), recovered); err != nil {
		t.Fatalf("Destroy() error = %v", err)
	}
}

func TestGitWorkspaceRecoversAppliedPatchAfterRestart(t *testing.T) {
	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	repair := filepath.Join(root, "repair")
	if err := os.Mkdir(seed, 0o700); err != nil {
		t.Fatal(err)
	}
	testGit(t, "", "init", "--bare", bare)
	testGit(t, seed, "init", "-b", "main")
	testGit(t, seed, "config", "user.name", "workspace recovery test")
	testGit(t, seed, "config", "user.email", "workspace-recovery@example.invalid")
	writeTestFile(t, seed, "main.go", "package main\n\nfunc status() string { return \"deployed\" }\n")
	testGit(t, seed, "add", "main.go")
	testGit(t, seed, "commit", "-m", "deployed")
	testGit(t, seed, "remote", "add", "origin", bare)
	testGit(t, seed, "push", "origin", "main:refs/heads/main")
	baseline := strings.TrimSpace(string(testGit(t, seed, "rev-parse", "HEAD")))
	remoteURL := (&url.URL{Scheme: "file", Path: bare}).String()
	testGit(t, "", "clone", bare, repair)
	testGit(t, repair, "checkout", "--detach", baseline)
	writeTestFile(t, repair, "main.go", "package main\n\nfunc status() string { return \"recovered\" }\n")
	patch := testGit(t, repair, "diff", "--binary", baseline, "--")

	artifacts := memoryArtifacts{values: map[string][]byte{}}
	workspace, err := NewGitWorkspace(WorkspaceOptions{Root: filepath.Join(root, "workspaces"), Artifacts: artifacts})
	if err != nil {
		t.Fatal(err)
	}
	workspace.allowLocal = true
	request := domain.WorkspaceRequest{
		RunID: "run-workspace-recovery", ProjectID: "project-workspace", BaselineCommit: baseline,
		IdempotencyKey: "workspace:run-workspace-recovery",
		Profile:        domain.ExecutionProfileSnapshot{CPULimit: 2, MemoryLimitMiB: 4096, WorkspaceLimitMiB: 1024},
		Repository:     domain.PublicationSnapshot{RemoteURL: remoteURL, Transport: "file"},
		ChangePolicy:   domain.ChangePolicySnapshot{AllowedPaths: []string{"**"}, MaxChangedFiles: 10, MaxChangedLines: 400},
	}
	identity, err := workspace.Ensure(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	patchRequest := domain.PatchRequest{
		WorkspaceID: identity.WorkspaceID, Patch: string(patch), ExpectedTreeHash: identity.CurrentTreeHash,
		IdempotencyKey: "patch:run-workspace-recovery:1", ChangePolicy: request.ChangePolicy,
	}
	patchDigest := sha256.Sum256(patch)
	patchHash := hex.EncodeToString(patchDigest[:])
	incomingRef, incomingHash, err := artifacts.Put(context.Background(), patch)
	if err != nil || incomingHash != patchHash {
		t.Fatalf("persist incoming patch: ref=%q hash=%q err=%v", incomingRef, incomingHash, err)
	}
	directory := filepath.Join(workspace.root, identity.WorkspaceID)
	metadata, err := workspace.readMetadata(directory)
	if err != nil {
		t.Fatal(err)
	}
	metadata.Pending = &workspacePendingPatch{
		IdempotencyKey: patchRequest.IdempotencyKey, PatchHash: patchHash,
		ExpectedTreeHash: identity.CurrentTreeHash, ArtifactRef: incomingRef,
		ContentHash: incomingHash, ChangePolicy: request.ChangePolicy,
	}
	if err := workspace.writeMetadata(directory, metadata); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(directory, "repo")
	env := sanitizedGitEnvironment(os.Environ(), filepath.Join(directory, "home"))
	if _, err := workspace.runGit(context.Background(), repo, env, bytes.NewReader(patch), "-c", "core.hooksPath=/dev/null", "apply", "--index", "--binary"); err != nil {
		t.Fatal(err)
	}

	recovered, err := workspace.Ensure(context.Background(), request)
	if err != nil {
		t.Fatalf("Ensure() did not reconcile the pending patch: %v", err)
	}
	if recovered.Version != identity.Version+1 || sameObjectID(recovered.CurrentTreeHash, identity.CurrentTreeHash) {
		t.Fatalf("recovered identity = %+v", recovered)
	}
	result, err := workspace.ApplyPatch(context.Background(), recovered, patchRequest)
	if err != nil || !result.AlreadyApplied || result.ResultTreeHash != recovered.CurrentTreeHash {
		t.Fatalf("recovered patch result = %+v, %v", result, err)
	}
	if err := workspace.Destroy(context.Background(), recovered); err != nil {
		t.Fatal(err)
	}
}

func TestPublisherReplaysPatchAndReusesVerifiedBranch(t *testing.T) {
	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	repair := filepath.Join(root, "repair")
	if err := os.Mkdir(seed, 0o700); err != nil {
		t.Fatal(err)
	}
	testGit(t, "", "init", "--bare", bare)
	testGit(t, seed, "init", "-b", "main")
	testGit(t, seed, "config", "user.name", "publisher test")
	testGit(t, seed, "config", "user.email", "publisher@example.invalid")
	writeTestFile(t, seed, "main.go", "package main\n\nfunc status() string { return \"deployed\" }\n")
	testGit(t, seed, "add", "main.go")
	testGit(t, seed, "commit", "-m", "deployed")
	testGit(t, seed, "remote", "add", "origin", bare)
	testGit(t, seed, "push", "origin", "main:refs/heads/main")
	baseline := strings.TrimSpace(string(testGit(t, seed, "rev-parse", "HEAD")))

	writeTestFile(t, seed, "main.go", "package main\n\nfunc status() string { return \"target moved\" }\n")
	testGit(t, seed, "commit", "-am", "advance target")
	testGit(t, seed, "push", "origin", "main:refs/heads/main")

	testGit(t, "", "clone", bare, repair)
	testGit(t, repair, "checkout", "--detach", baseline)
	writeTestFile(t, repair, "main.go", "package main\n\nfunc status() string { return \"fixed\" }\n")
	patch := testGit(t, repair, "diff", "--binary", baseline, "--")
	testGit(t, repair, "add", "-A")
	expectedTree := strings.TrimSpace(string(testGit(t, repair, "write-tree")))
	patchDigest := sha256.Sum256(patch)
	patchHash := hex.EncodeToString(patchDigest[:])
	artifactRef := "sha256:" + patchHash
	remoteURL := (&url.URL{Scheme: "file", Path: bare}).String()

	publisher := &Publisher{
		command:           "git",
		artifacts:         memoryArtifacts{values: map[string][]byte{artifactRef: patch}},
		credentials:       unusedCredentialResolver{},
		allowLocalRemotes: true,
	}
	request := domain.GitPublicationRequest{
		PublicationRequest: domain.PublicationRequest{
			RunID: "run-123", ProjectID: "project-123", BaselineCommit: baseline,
			TargetBranch: "main", BranchRef: "hotfix/remediation/incident-123/run-123",
			CommitMessage: "fix(remediation): apply plan-1", PatchArtifactRef: artifactRef,
			PatchContentHash: patchHash, ExpectedTreeHash: expectedTree, IdempotencyKey: "git_publish:run-123:patch-1",
			Repository:        domain.PublicationSnapshot{RemoteURL: remoteURL, Transport: "file"},
			ChangePolicy:      domain.ChangePolicySnapshot{AllowedPaths: []string{"**"}, MaxChangedFiles: 10, MaxChangedLines: 400},
			WorkspaceLimitMiB: 1024,
		},
		AuthoredAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).Format(time.RFC3339),
	}

	first, err := publisher.PublishGit(context.Background(), request)
	if err != nil {
		t.Fatalf("first publish failed: %v", err)
	}
	if !first.RemoteVerified || !first.TargetDiverged || first.AlreadyPublished {
		t.Fatalf("unexpected first publish result: %+v", first)
	}
	if first.BaselineCommit != baseline || first.ExpectedTreeHash != expectedTree || first.BranchRef != request.BranchRef {
		t.Fatalf("publication result lost immutable identities: %+v", first)
	}
	if !strings.Contains(first.Summary, "deployed commit") {
		t.Fatalf("target divergence summary missing: %q", first.Summary)
	}

	second, err := publisher.PublishGit(context.Background(), request)
	if err != nil {
		t.Fatalf("idempotent publish failed: %v", err)
	}
	if !second.AlreadyPublished || second.CommitHash != first.CommitHash || !second.RemoteVerified {
		t.Fatalf("existing branch was not safely reused: first=%+v second=%+v", first, second)
	}
	remoteHash := strings.Fields(string(testGit(t, "", "ls-remote", "--heads", bare, "refs/heads/"+request.BranchRef)))[0]
	if remoteHash != first.CommitHash {
		t.Fatalf("remote branch hash = %s, want %s", remoteHash, first.CommitHash)
	}
}

func TestPublisherRejectsDisallowedPathsBeforePush(t *testing.T) {
	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	repair := filepath.Join(root, "repair")
	if err := os.Mkdir(seed, 0o700); err != nil {
		t.Fatal(err)
	}
	testGit(t, "", "init", "--bare", bare)
	testGit(t, seed, "init", "-b", "main")
	testGit(t, seed, "config", "user.name", "publisher test")
	testGit(t, seed, "config", "user.email", "publisher@example.invalid")
	writeTestFile(t, seed, "main.go", "package main\n")
	testGit(t, seed, "add", "main.go")
	testGit(t, seed, "commit", "-m", "deployed")
	testGit(t, seed, "remote", "add", "origin", bare)
	testGit(t, seed, "push", "origin", "main:refs/heads/main")
	baseline := strings.TrimSpace(string(testGit(t, seed, "rev-parse", "HEAD")))
	testGit(t, "", "clone", bare, repair)
	testGit(t, repair, "checkout", "--detach", baseline)
	writeTestFile(t, repair, "main.go", "package main\n\nfunc changed() {}\n")
	patch := testGit(t, repair, "diff", "--binary", baseline, "--")
	testGit(t, repair, "add", "-A")
	expectedTree := strings.TrimSpace(string(testGit(t, repair, "write-tree")))
	patchDigest := sha256.Sum256(patch)
	patchHash := hex.EncodeToString(patchDigest[:])
	artifactRef := "sha256:" + patchHash
	remoteURL := (&url.URL{Scheme: "file", Path: bare}).String()
	publisher := &Publisher{
		command: "git", artifacts: memoryArtifacts{values: map[string][]byte{artifactRef: patch}},
		credentials: unusedCredentialResolver{}, allowLocalRemotes: true,
	}
	request := domain.GitPublicationRequest{
		PublicationRequest: domain.PublicationRequest{
			RunID: "run-456", ProjectID: "project-123", BaselineCommit: baseline,
			TargetBranch: "main", BranchRef: "hotfix/remediation/incident-456/run-456",
			CommitMessage: "fix(remediation): apply plan-1", PatchArtifactRef: artifactRef,
			PatchContentHash: patchHash, ExpectedTreeHash: expectedTree, IdempotencyKey: "git_publish:run-456:patch-1",
			Repository:        domain.PublicationSnapshot{RemoteURL: remoteURL, Transport: "file"},
			ChangePolicy:      domain.ChangePolicySnapshot{AllowedPaths: []string{"src/**"}, MaxChangedFiles: 10, MaxChangedLines: 400},
			WorkspaceLimitMiB: 1024,
		},
		AuthoredAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).Format(time.RFC3339),
	}
	if _, err := publisher.PublishGit(context.Background(), request); err == nil {
		t.Fatal("expected disallowed path to fail closed")
	}
	if output := testGit(t, "", "ls-remote", "--heads", bare, "refs/heads/"+request.BranchRef); len(strings.TrimSpace(string(output))) != 0 {
		t.Fatalf("branch was published despite policy rejection: %q", output)
	}
}

func writeTestFile(t *testing.T, directory, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func testGit(t *testing.T, directory string, args ...string) []byte {
	t.Helper()
	command := exec.Command("git", args...)
	if directory != "" {
		command.Dir = directory
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
	return output
}
