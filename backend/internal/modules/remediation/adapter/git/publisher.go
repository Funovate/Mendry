package git

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"mendry/backend/internal/modules/remediation/domain"
)

const maxPublishPatchBytes = 64 << 10

// ArtifactReader returns a verified content-addressed blob.
type ArtifactReader interface {
	Get(context.Context, string, int64) ([]byte, error)
}

// GitCredentialResolver decrypts one project-scoped repository credential and verifies its version.
type GitCredentialResolver interface {
	ResolveRepositoryCredential(context.Context, string, string, int64, string) ([]byte, error)
}

type PublisherOptions struct {
	Command               string
	Artifacts             ArtifactReader
	Credentials           GitCredentialResolver
	KnownHostsFile        string
	LocalCommandTimeout   time.Duration
	NetworkCommandTimeout time.Duration
}

type Publisher struct {
	command               string
	artifacts             ArtifactReader
	credentials           GitCredentialResolver
	knownHostsFile        string
	allowLocalRemotes     bool
	localCommandTimeout   time.Duration
	networkCommandTimeout time.Duration
}

func NewPublisher(options PublisherOptions) (*Publisher, error) {
	if options.Command == "" {
		options.Command = "git"
	}
	if options.Artifacts == nil || options.Credentials == nil {
		return nil, fmt.Errorf("Git publisher requires artifacts and credential resolver")
	}
	knownHosts := ""
	if options.KnownHostsFile != "" {
		absolute, err := filepath.Abs(options.KnownHostsFile)
		if err != nil {
			return nil, fmt.Errorf("invalid SSH known-hosts path")
		}
		info, err := os.Stat(absolute)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("SSH known-hosts file must be a regular file")
		}
		knownHosts = absolute
	}
	return &Publisher{
		command: options.Command, artifacts: options.Artifacts,
		credentials: options.Credentials, knownHostsFile: knownHosts,
		localCommandTimeout: options.LocalCommandTimeout, networkCommandTimeout: options.NetworkCommandTimeout,
	}, nil
}

func (p *Publisher) PublishGit(ctx context.Context, request domain.GitPublicationRequest) (domain.GitPublicationResult, error) {
	if p == nil || p.command == "" || p.artifacts == nil || p.credentials == nil {
		return domain.GitPublicationResult{}, lifecycleError("git_publisher_unavailable", false, nil)
	}
	if err := request.PublicationRequest.Validate(); err != nil {
		return domain.GitPublicationResult{}, lifecycleError("git_publication_request_invalid", false, err)
	}
	if !fullObjectID(request.BaselineCommit) {
		return domain.GitPublicationResult{}, lifecycleError("git_baseline_invalid", false, nil)
	}
	repository := request.Repository
	remote, err := url.Parse(repository.RemoteURL)
	localRemote := p.allowLocalRemotes && remote != nil && remote.Scheme == "file" && remote.User == nil
	if err != nil || remote.User != nil || remote.RawQuery != "" || remote.Fragment != "" || remote.Opaque != "" || remote.Scheme != repository.Transport || ((!localRemote && remote.Host == "") || (remote.Scheme != "https" && remote.Scheme != "ssh" && !localRemote)) {
		return domain.GitPublicationResult{}, lifecycleError("git_remote_invalid", false, err)
	}
	if !localRemote && (repository.GitCredentialSecretID == "" || repository.GitCredentialVersion < 1) {
		return domain.GitPublicationResult{}, lifecycleError("git_credential_snapshot_missing", false, nil)
	}
	if err := p.validateRef(ctx, request.BranchRef); err != nil {
		return domain.GitPublicationResult{}, err
	}
	if err := p.validateRef(ctx, request.TargetBranch); err != nil {
		return domain.GitPublicationResult{}, err
	}
	patch, err := p.artifacts.Get(ctx, request.PatchArtifactRef, maxPublishPatchBytes)
	if err != nil {
		return domain.GitPublicationResult{}, lifecycleError("patch_artifact_unavailable", false, err)
	}
	if len(patch) == 0 || len(patch) > maxPublishPatchBytes {
		return domain.GitPublicationResult{}, lifecycleError("patch_artifact_invalid", false, nil)
	}
	patchHash := sha256.Sum256(patch)
	if hex.EncodeToString(patchHash[:]) != strings.ToLower(request.PatchContentHash) {
		return domain.GitPublicationResult{}, lifecycleError("patch_artifact_hash_mismatch", false, nil)
	}

	var credential []byte
	if !localRemote {
		credential, err = p.credentials.ResolveRepositoryCredential(ctx, request.ProjectID, repository.GitCredentialSecretID, repository.GitCredentialVersion, repository.Transport)
		if err != nil {
			return domain.GitPublicationResult{}, lifecycleError("git_credential_unavailable", false, err)
		}
		defer zeroBytes(credential)
	}
	workspace, err := os.MkdirTemp("", "mendry-remediation-publish-")
	if err != nil {
		return domain.GitPublicationResult{}, lifecycleError("git_workspace_unavailable", true, err)
	}
	defer os.RemoveAll(workspace)
	if err := os.Chmod(workspace, 0o700); err != nil {
		return domain.GitPublicationResult{}, lifecycleError("git_workspace_unavailable", true, err)
	}
	authDir := filepath.Join(workspace, "auth")
	if err := os.Mkdir(authDir, 0o700); err != nil {
		return domain.GitPublicationResult{}, lifecycleError("git_workspace_unavailable", true, err)
	}
	var auth *gitAuthentication
	if localRemote {
		home := filepath.Join(authDir, "home")
		if err := os.Mkdir(home, 0o700); err != nil {
			return domain.GitPublicationResult{}, lifecycleError("git_workspace_unavailable", true, err)
		}
		auth = &gitAuthentication{directory: authDir, env: sanitizedGitEnvironment(os.Environ(), home)}
	} else {
		auth, err = newGitAuthentication(authDir, repository.Transport, credential, p.knownHostsFile)
		if err != nil {
			return domain.GitPublicationResult{}, lifecycleError("git_authentication_unavailable", false, err)
		}
	}
	defer auth.Close()

	repositoryDir := filepath.Join(workspace, "repo")
	if err := os.Mkdir(repositoryDir, 0o700); err != nil {
		return domain.GitPublicationResult{}, lifecycleError("git_workspace_unavailable", true, err)
	}
	git := func(args ...string) ([]byte, error) { return p.runGit(ctx, repositoryDir, auth.env, nil, args...) }
	if _, err := git("init", "--quiet"); err != nil {
		return domain.GitPublicationResult{}, lifecycleError("git_workspace_unavailable", true, err)
	}
	if _, err := git("-c", "core.hooksPath=/dev/null", "remote", "add", "origin", repository.RemoteURL); err != nil {
		return domain.GitPublicationResult{}, lifecycleError("git_remote_invalid", false, err)
	}
	if _, err := git("-c", "core.hooksPath=/dev/null", "fetch", "--no-tags", "--depth=1", "origin", request.BaselineCommit); err != nil {
		return domain.GitPublicationResult{}, lifecycleError("git_baseline_unavailable", false, err)
	}
	baseline, err := git("rev-parse", "FETCH_HEAD^{commit}")
	if err != nil || !sameObjectID(strings.TrimSpace(string(baseline)), request.BaselineCommit) {
		return domain.GitPublicationResult{}, lifecycleError("git_baseline_mismatch", false, err)
	}
	workspaceLimitMiB := request.WorkspaceLimitMiB
	if workspaceLimitMiB == 0 {
		workspaceLimitMiB = 10240
	}
	workspaceLimit := int64(workspaceLimitMiB) * 1024 * 1024
	if err := rejectUnsafeBaseline(func(args ...string) ([]byte, error) {
		return p.runGitWithLimit(ctx, repositoryDir, auth.env, nil, 64<<20, args...)
	}, "FETCH_HEAD", workspaceLimit); err != nil {
		return domain.GitPublicationResult{}, lifecycleError("git_baseline_unsafe", false, err)
	}
	if _, err := git("-c", "core.hooksPath=/dev/null", "checkout", "--quiet", "--detach", "FETCH_HEAD"); err != nil {
		return domain.GitPublicationResult{}, lifecycleError("git_baseline_unavailable", false, err)
	}
	baseline, err = git("rev-parse", "HEAD^{commit}")
	if err != nil || !sameObjectID(strings.TrimSpace(string(baseline)), request.BaselineCommit) {
		return domain.GitPublicationResult{}, lifecycleError("git_baseline_mismatch", false, err)
	}
	if err := checkGitWorkspaceSize(repositoryDir, workspaceLimitMiB); err != nil {
		return domain.GitPublicationResult{}, lifecycleError("git_workspace_size_limit", false, err)
	}
	targetHash, err := remoteRef(git, request.TargetBranch)
	if err != nil {
		return domain.GitPublicationResult{}, lifecycleError("git_remote_probe_failed", true, err)
	}
	if targetHash == "" {
		return domain.GitPublicationResult{}, lifecycleError("git_target_branch_missing", false, nil)
	}
	targetDiverged := !sameObjectID(targetHash, request.BaselineCommit)

	if existingHash, probeErr := remoteRef(git, request.BranchRef); probeErr != nil {
		return domain.GitPublicationResult{}, lifecycleError("git_remote_probe_failed", true, probeErr)
	} else if existingHash != "" {
		if err := p.verifyRemoteBranch(git, request.BranchRef, existingHash, request.BaselineCommit, request.ExpectedTreeHash); err != nil {
			return domain.GitPublicationResult{}, lifecycleError("git_branch_conflict", false, err)
		}
		return gitPublicationResult(request, existingHash, true, targetDiverged), nil
	}

	apply := []string{"-c", "core.hooksPath=/dev/null", "apply", "--index", "--binary"}
	if _, err := p.runGit(ctx, repositoryDir, auth.env, bytes.NewReader(patch), apply...); err != nil {
		return domain.GitPublicationResult{}, lifecycleError("git_patch_replay_failed", false, err)
	}
	if err := checkGitWorkspaceSize(repositoryDir, workspaceLimitMiB); err != nil {
		return domain.GitPublicationResult{}, lifecycleError("git_workspace_size_limit", false, err)
	}
	if err := p.validateTree(ctx, repositoryDir, auth.env, request.ChangePolicy); err != nil {
		return domain.GitPublicationResult{}, lifecycleError("change_policy_rejected", false, err)
	}
	tree, err := git("write-tree")
	if err != nil || !sameObjectID(strings.TrimSpace(string(tree)), request.ExpectedTreeHash) {
		return domain.GitPublicationResult{}, lifecycleError("publication_tree_mismatch", false, err)
	}
	createdAt, err := time.Parse(time.RFC3339, request.AuthoredAt)
	if err != nil {
		return domain.GitPublicationResult{}, lifecycleError("git_commit_time_invalid", false, err)
	}
	authorName := request.AuthorName
	if authorName == "" {
		authorName = "Mendry Remediation"
	}
	authorEmail := request.AuthorEmail
	if authorEmail == "" {
		authorEmail = "remediation@mendry.invalid"
	}
	env := append(append([]string(nil), auth.env...),
		"GIT_AUTHOR_NAME="+authorName, "GIT_AUTHOR_EMAIL="+authorEmail,
		"GIT_COMMITTER_NAME="+authorName, "GIT_COMMITTER_EMAIL="+authorEmail,
		"GIT_AUTHOR_DATE="+createdAt.UTC().Format(time.RFC3339),
		"GIT_COMMITTER_DATE="+createdAt.UTC().Format(time.RFC3339),
	)
	commit, err := p.runGit(ctx, repositoryDir, env, nil, "commit-tree", strings.TrimSpace(string(tree)), "-p", request.BaselineCommit, "-m", request.CommitMessage)
	if err != nil {
		return domain.GitPublicationResult{}, lifecycleError("git_commit_failed", false, err)
	}
	commitID := strings.TrimSpace(string(commit))
	if !fullObjectID(commitID) {
		return domain.GitPublicationResult{}, lifecycleError("git_commit_identity_invalid", false, nil)
	}
	_, pushErr := git("-c", "core.hooksPath=/dev/null", "push", "origin", commitID+":refs/heads/"+request.BranchRef)
	if pushErr != nil {
		// A concurrent retry may have published the same deterministic commit.
		existingHash, probeErr := remoteRef(git, request.BranchRef)
		if probeErr != nil || existingHash == "" {
			return domain.GitPublicationResult{}, lifecycleError("git_push_failed", true, pushErr)
		}
		if err := p.verifyRemoteBranch(git, request.BranchRef, existingHash, request.BaselineCommit, request.ExpectedTreeHash); err != nil {
			return domain.GitPublicationResult{}, lifecycleError("git_branch_conflict", false, err)
		}
		commitID = existingHash
		return gitPublicationResult(request, commitID, true, targetDiverged), nil
	}
	verifiedHash, err := remoteRef(git, request.BranchRef)
	if err != nil || !sameObjectID(verifiedHash, commitID) {
		return domain.GitPublicationResult{}, lifecycleError("git_remote_verification_failed", true, err)
	}
	if err := p.verifyRemoteBranch(git, request.BranchRef, verifiedHash, request.BaselineCommit, request.ExpectedTreeHash); err != nil {
		return domain.GitPublicationResult{}, lifecycleError("git_remote_verification_failed", false, err)
	}
	return gitPublicationResult(request, commitID, false, targetDiverged), nil
}

func gitPublicationResult(request domain.GitPublicationRequest, commit string, already, targetDiverged bool) domain.GitPublicationResult {
	summary := "branch published from the deployed baseline"
	if targetDiverged {
		summary = "target branch moved after deployment; branch remains based on the deployed commit"
	}
	return domain.GitPublicationResult{
		BranchRef: request.BranchRef, CommitHash: commit, BaselineCommit: request.BaselineCommit,
		TargetBranch: request.TargetBranch, ExpectedTreeHash: request.ExpectedTreeHash,
		AlreadyPublished: already, RemoteVerified: true, TargetDiverged: targetDiverged, Summary: summary,
	}
}

func (p *Publisher) validateRef(ctx context.Context, ref string) error {
	if strings.TrimSpace(ref) == "" || strings.ContainsAny(ref, "\x00\r\n") {
		return lifecycleError("git_ref_invalid", false, nil)
	}
	commandCtx, cancel := context.WithTimeout(ctx, p.commandTimeout("check-ref-format"))
	defer cancel()
	command := exec.CommandContext(commandCtx, p.command, "check-ref-format", "refs/heads/"+ref)
	configureGitProcess(command)
	command.WaitDelay = time.Second
	command.Env = sanitizedGitEnvironment(os.Environ(), os.TempDir())
	if err := command.Run(); err != nil {
		return lifecycleError("git_ref_invalid", false, err)
	}
	return nil
}

func rejectUnsafeBaseline(git func(...string) ([]byte, error), treeish string, workspaceLimit int64) error {
	entries, err := git("ls-tree", "-r", "-l", "--full-tree", "-z", treeish)
	if err != nil {
		return err
	}
	if len(entries) > maxWorkspaceTreeBytes {
		return fmt.Errorf("baseline tree exceeds its metadata limit")
	}
	var total int64
	for _, entry := range bytes.Split(entries, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		metadata, _, found := bytes.Cut(entry, []byte{'\t'})
		if !found {
			return fmt.Errorf("baseline tree entry is malformed")
		}
		fields := strings.Fields(string(metadata))
		if len(fields) != 4 || fields[0] == "120000" || fields[0] == "160000" || fields[1] != "blob" || !strings.HasPrefix(fields[0], "100") || fields[3] == "-" {
			return fmt.Errorf("baseline symlink, submodule, or special file is prohibited")
		}
		size, err := strconv.ParseInt(fields[3], 10, 64)
		if err != nil || size < 0 || size > workspaceLimit-total {
			return fmt.Errorf("baseline exceeds the workspace limit")
		}
		total += size
	}
	if total > workspaceLimit/2 {
		return fmt.Errorf("baseline leaves insufficient workspace headroom")
	}
	return nil
}

func remoteRef(git func(...string) ([]byte, error), ref string) (string, error) {
	output, err := git("-c", "core.hooksPath=/dev/null", "ls-remote", "--heads", "origin", "refs/heads/"+ref)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(output))
	if len(fields) == 0 {
		return "", nil
	}
	if len(fields) != 2 || fields[1] != "refs/heads/"+ref || !fullObjectID(fields[0]) {
		return "", fmt.Errorf("remote ref response is invalid")
	}
	return fields[0], nil
}

func (p *Publisher) verifyRemoteBranch(git func(...string) ([]byte, error), ref, commit, baseline, expectedTree string) error {
	if _, err := git("-c", "core.hooksPath=/dev/null", "fetch", "--no-tags", "--depth=1", "origin", "refs/heads/"+ref); err != nil {
		return err
	}
	fetched, err := git("rev-parse", "FETCH_HEAD^{commit}")
	if err != nil || !sameObjectID(strings.TrimSpace(string(fetched)), commit) {
		return fmt.Errorf("remote branch changed during verification")
	}
	metadata, err := git("cat-file", "-p", commit)
	if err != nil {
		return err
	}
	headers := bytes.SplitN(metadata, []byte("\n\n"), 2)[0]
	parent := ""
	tree := ""
	parentCount := 0
	for _, line := range bytes.Split(headers, []byte{'\n'}) {
		if bytes.HasPrefix(line, []byte("parent ")) {
			parent = strings.TrimPrefix(string(line), "parent ")
			parentCount++
		}
		if bytes.HasPrefix(line, []byte("tree ")) {
			tree = strings.TrimPrefix(string(line), "tree ")
		}
	}
	if parentCount != 1 || !sameObjectID(parent, baseline) {
		return fmt.Errorf("remote hotfix commit does not have the deployed baseline as its only parent")
	}
	if !sameObjectID(tree, expectedTree) {
		return fmt.Errorf("remote hotfix tree does not match the validated tree")
	}
	return nil
}

func (p *Publisher) validateTree(ctx context.Context, repositoryDir string, env []string, policy domain.ChangePolicySnapshot) error {
	git := func(args ...string) ([]byte, error) { return p.runGit(ctx, repositoryDir, env, nil, args...) }
	modes, err := git("diff", "--cached", "--raw", "-z", "--no-renames", "HEAD")
	if err != nil {
		return err
	}
	for _, entry := range bytes.Split(modes, []byte{0}) {
		if bytes.Contains(entry, []byte(" 120000 ")) || bytes.Contains(entry, []byte(" 160000 ")) {
			return fmt.Errorf("symlink and submodule changes are prohibited")
		}
	}
	numstat, err := git("diff", "--cached", "--numstat", "-z", "--no-renames", "HEAD")
	if err != nil {
		return err
	}
	entries := bytes.Split(bytes.TrimSuffix(numstat, []byte{0}), []byte{0})
	if len(entries) == 0 || (len(entries) == 1 && len(entries[0]) == 0) {
		return fmt.Errorf("publication patch is empty")
	}
	maxFiles := policy.MaxChangedFiles
	if maxFiles < 1 || maxFiles > 30 {
		return fmt.Errorf("configured changed-file limit is invalid")
	}
	maxLines := policy.MaxChangedLines
	if maxLines < 1 || maxLines > 5000 {
		return fmt.Errorf("configured changed-line limit is invalid")
	}
	if len(entries) > maxFiles {
		return fmt.Errorf("changed-file limit exceeded")
	}
	var changedLines int64
	for _, entry := range entries {
		first := bytes.IndexByte(entry, '\t')
		second := -1
		if first >= 0 {
			if relative := bytes.IndexByte(entry[first+1:], '\t'); relative >= 0 {
				second = first + 1 + relative
			}
		}
		if first < 0 || second < 0 || second == len(entry)-1 {
			return fmt.Errorf("numstat entry is invalid")
		}
		added, removed := string(entry[:first]), string(entry[first+1:second])
		if added == "-" || removed == "-" {
			return fmt.Errorf("binary changes are prohibited")
		}
		addedCount, addErr := strconv.ParseInt(added, 10, 64)
		removedCount, removeErr := strconv.ParseInt(removed, 10, 64)
		if addErr != nil || removeErr != nil || addedCount < 0 || removedCount < 0 {
			return fmt.Errorf("change line count is invalid")
		}
		changedLines += addedCount + removedCount
		name := string(entry[second+1:])
		if !publishablePath(name) || !matchesAnyPath(policy.AllowedPaths, name) || matchesAnyPath(policy.DeniedPaths, name) {
			return fmt.Errorf("changed path is outside the automatic publication policy")
		}
	}
	if changedLines > int64(maxLines) {
		return fmt.Errorf("changed-line limit exceeded")
	}
	return nil
}

func publishablePath(value string) bool {
	value = strings.ReplaceAll(value, "\\", "/")
	if value == "" || strings.HasPrefix(value, "/") || strings.ContainsRune(value, '\x00') {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == ".." || segment == ".git" {
			return false
		}
	}
	lower := strings.ToLower(value)
	for _, denied := range []string{".github/workflows/", ".gitlab-ci", ".circleci/", ".buildkite/", "deploy/", "deployment/", "kubernetes/", "k8s/", "helm/", "terraform/", "pulumi/", "migrations/", "migration/", ".env", "secret", "credential", "vendor/"} {
		if strings.Contains(lower, denied) {
			return false
		}
	}
	extension := strings.ToLower(filepath.Ext(value))
	switch extension {
	case ".go", ".js", ".jsx", ".ts", ".tsx", ".py", ".rb", ".php", ".rs", ".java", ".kt", ".cs", ".c", ".h", ".cc", ".cpp", ".vue", ".svelte":
		return true
	default:
		return false
	}
}

func matchesAnyPath(patterns []string, value string) bool {
	for _, pattern := range patterns {
		if matchPathSegments(strings.Split(strings.Trim(pattern, "/"), "/"), strings.Split(strings.Trim(value, "/"), "/")) {
			return true
		}
	}
	return false
}

func matchPathSegments(patterns, value []string) bool {
	if len(patterns) == 0 {
		return len(value) == 0
	}
	if patterns[0] == "**" {
		return matchPathSegments(patterns[1:], value) || (len(value) > 0 && matchPathSegments(patterns, value[1:]))
	}
	if len(value) == 0 {
		return false
	}
	matched, err := filepath.Match(patterns[0], value[0])
	return err == nil && matched && matchPathSegments(patterns[1:], value[1:])
}

func (p *Publisher) runGit(ctx context.Context, directory string, env []string, stdin io.Reader, args ...string) ([]byte, error) {
	return p.runGitWithLimit(ctx, directory, env, stdin, 64<<10, args...)
}

func gitOperation(args []string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == "-c" {
			i++
			continue
		}
		return args[i]
	}
	return "unknown"
}

func (p *Publisher) commandTimeout(operation string) time.Duration {
	if operation == "fetch" || operation == "push" || operation == "ls-remote" {
		if p.networkCommandTimeout > 0 {
			return p.networkCommandTimeout
		}
		return 3 * time.Minute
	}
	if p.localCommandTimeout > 0 {
		return p.localCommandTimeout
	}
	return time.Minute
}

type gitCommandTimeout struct{ operation string }

func (e *gitCommandTimeout) Error() string { return "Git " + e.operation + " timed out" }
func (e *gitCommandTimeout) Unwrap() error { return context.DeadlineExceeded }

func (p *Publisher) runGitWithLimit(ctx context.Context, directory string, env []string, stdin io.Reader, maxOutput int, args ...string) ([]byte, error) {
	operation := gitOperation(args)
	commandCtx, cancel := context.WithTimeout(ctx, p.commandTimeout(operation))
	defer cancel()
	started := time.Now()
	command := exec.CommandContext(commandCtx, p.command, args...)
	configureGitProcess(command)
	command.WaitDelay = time.Second
	command.Dir = directory
	command.Env = env
	command.Stdin = stdin
	stdout := limitedBuffer{limit: maxOutput}
	command.Stdout = &stdout
	command.Stderr = io.Discard
	err := command.Run()
	slog.InfoContext(ctx, "remediation Git command completed", "operation", operation, "elapsed_ms", time.Since(started).Milliseconds(), "succeeded", err == nil, "cancelled", commandCtx.Err() != nil)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
			return nil, &gitCommandTimeout{operation: operation}
		}
		return nil, err
	}
	if stdout.truncated {
		return nil, fmt.Errorf("Git command output exceeded its limit")
	}
	return stdout.Bytes(), nil
}

type limitedBuffer struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	originalLength := len(value)
	remaining := b.limit - b.Len()
	if remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
			b.truncated = true
		}
		_, _ = b.Buffer.Write(value)
	} else if originalLength > 0 {
		b.truncated = true
	}
	return originalLength, nil
}

func (b *limitedBuffer) Bytes() []byte {
	return b.Buffer.Bytes()
}

type gitAuthentication struct {
	directory string
	env       []string
}

func (a *gitAuthentication) Close() {
	if a == nil {
		return
	}
	zeroEnvironment(a.env, "MENDRY_GIT_USERNAME", "MENDRY_GIT_PASSWORD")
	_ = os.RemoveAll(a.directory)
}

func newGitAuthentication(directory, transport string, credential []byte, knownHostsFile string) (*gitAuthentication, error) {
	home := filepath.Join(directory, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		return nil, err
	}
	env := sanitizedGitEnvironment(os.Environ(), home)
	switch transport {
	case "https":
		username, password := splitGitCredential(string(credential))
		askpass := filepath.Join(directory, "askpass")
		body := "#!/bin/sh\ncase \"$1\" in\n  *Username*) printf '%s\\n' \"$MENDRY_GIT_USERNAME\" ;;\n  *) printf '%s\\n' \"$MENDRY_GIT_PASSWORD\" ;;\nesac\n"
		if err := os.WriteFile(askpass, []byte(body), 0o700); err != nil {
			return nil, err
		}
		env = append(env, "GIT_ASKPASS="+askpass, "GIT_ASKPASS_REQUIRE=force", "MENDRY_GIT_USERNAME="+username, "MENDRY_GIT_PASSWORD="+password)
	case "ssh":
		if knownHostsFile == "" {
			return nil, fmt.Errorf("SSH publication requires operator-pinned host keys")
		}
		keyPath := filepath.Join(directory, "id_git")
		if err := os.WriteFile(keyPath, credential, 0o600); err != nil {
			return nil, err
		}
		sshCommand := "ssh -i " + shellQuote(keyPath) + " -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=" + shellQuote(knownHostsFile) + " -o GlobalKnownHostsFile=/dev/null -o BatchMode=yes"
		env = append(env, "GIT_SSH_COMMAND="+sshCommand)
	default:
		return nil, fmt.Errorf("Git transport is unsupported")
	}
	return &gitAuthentication{directory: directory, env: env}, nil
}

func sanitizedGitEnvironment(source []string, home string) []string {
	result := make([]string, 0, len(source)+8)
	for _, entry := range source {
		key, _, found := strings.Cut(entry, "=")
		if !found || strings.HasPrefix(key, "GIT_") || key == "HOME" || key == "SSH_AUTH_SOCK" || key == "GIT_TRACE" || key == "GIT_CURL_VERBOSE" {
			continue
		}
		result = append(result, entry)
	}
	return append(result,
		"HOME="+home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.followRedirects", "GIT_CONFIG_VALUE_0=false",
		"GIT_TERMINAL_PROMPT=0", "GIT_LFS_SKIP_SMUDGE=1",
	)
}

func splitGitCredential(value string) (string, string) {
	username, password, found := strings.Cut(value, ":")
	if !found || username == "" {
		return "x-access-token", value
	}
	return username, password
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func fullObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func sameObjectID(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}

func lifecycleError(code string, retryable bool, cause error) error {
	var timeout *gitCommandTimeout
	if errors.As(cause, &timeout) && code != "git_push_failed" {
		code = "git_" + strings.ReplaceAll(timeout.operation, "-", "_") + "_timeout"
		retryable = true
	}
	return &domain.LifecycleRuntimeError{Code: code, Retryable: retryable, Cause: cause}
}

func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func zeroEnvironment(environment []string, keys ...string) {
	for _, key := range keys {
		prefix := key + "="
		for index, item := range environment {
			if strings.HasPrefix(item, prefix) {
				environment[index] = prefix
			}
		}
	}
}
