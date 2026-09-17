package git

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"mendry/backend/internal/modules/remediation/domain"
)

const (
	workspaceMetadataVersion = 1
	maxWorkspacePatchBytes   = 64 << 10
	maxWorkspaceFileBytes    = 1 << 20
	maxWorkspaceTreeBytes    = 64 << 20
	maxWorkspaceStateBytes   = 1 << 20
)

// ArtifactWriter stores immutable patch blobs; workspace state stores only references.
type ArtifactWriter interface {
	Put(context.Context, []byte) (string, string, error)
}

type WorkspaceArtifactStore interface {
	ArtifactWriter
	ArtifactReader
}

type WorkspaceOptions struct {
	Root           string
	GitCommand     string
	GitTimeout     time.Duration
	Artifacts      WorkspaceArtifactStore
	Credentials    RepositoryCredentialResolver
	KnownHostsFile string
}

type GitWorkspace struct {
	root           string
	command        string
	timeout        time.Duration
	artifacts      WorkspaceArtifactStore
	credentials    RepositoryCredentialResolver
	knownHostsFile string
	allowLocal     bool
	mu             sync.Mutex
}

type workspaceMetadata struct {
	FormatVersion     int                             `json:"formatVersion"`
	ProjectID         string                          `json:"projectId"`
	WorkspaceLimitMiB int                             `json:"workspaceLimitMiB"`
	Identity          domain.WorkspaceIdentity        `json:"identity"`
	Pending           *workspacePendingPatch          `json:"pending,omitempty"`
	Patches           map[string]workspacePatchRecord `json:"patches"`
}

type workspacePendingPatch struct {
	IdempotencyKey   string                      `json:"idempotencyKey"`
	PatchHash        string                      `json:"patchHash"`
	ExpectedTreeHash string                      `json:"expectedTreeHash"`
	ArtifactRef      string                      `json:"artifactRef"`
	ContentHash      string                      `json:"contentHash"`
	ChangePolicy     domain.ChangePolicySnapshot `json:"changePolicy"`
}

type workspacePatchRecord struct {
	PatchHash        string   `json:"patchHash"`
	ExpectedTreeHash string   `json:"expectedTreeHash"`
	ArtifactRef      string   `json:"artifactRef"`
	ContentHash      string   `json:"contentHash"`
	ResultTreeHash   string   `json:"resultTreeHash"`
	ChangedFiles     []string `json:"changedFiles"`
}

func NewGitWorkspace(options WorkspaceOptions) (*GitWorkspace, error) {
	if options.Artifacts == nil || strings.TrimSpace(options.Root) == "" {
		return nil, fmt.Errorf("workspace root and artifact store are required")
	}
	root, err := filepath.Abs(options.Root)
	if err != nil || root == string(filepath.Separator) {
		return nil, fmt.Errorf("workspace root is invalid")
	}
	if err := ensurePrivateWorkspaceRoot(root); err != nil {
		return nil, err
	}
	command := strings.TrimSpace(options.GitCommand)
	if command == "" {
		command = "git"
	}
	timeout := options.GitTimeout
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
	knownHosts := ""
	if options.KnownHostsFile != "" {
		knownHosts, err = filepath.Abs(options.KnownHostsFile)
		if err != nil {
			return nil, fmt.Errorf("SSH known-hosts path is invalid")
		}
		info, statErr := os.Stat(knownHosts)
		if statErr != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("SSH known-hosts file must be a regular file")
		}
	}
	return &GitWorkspace{
		root: root, command: command, timeout: timeout, artifacts: options.Artifacts,
		credentials: options.Credentials, knownHostsFile: knownHosts,
	}, nil
}

var _ domain.WorkspacePort = (*GitWorkspace)(nil)

func (w *GitWorkspace) Ensure(ctx context.Context, request domain.WorkspaceRequest) (domain.WorkspaceIdentity, error) {
	if w == nil || w.root == "" || w.artifacts == nil {
		return domain.WorkspaceIdentity{}, workspaceError("workspace_unavailable", true, nil)
	}
	if err := validateWorkspaceRequest(request, w.allowLocal); err != nil {
		return domain.WorkspaceIdentity{}, workspaceError("workspace_request_invalid", false, err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	workspaceID := workspaceID(request.ProjectID, request.RunID)
	directory := filepath.Join(w.root, workspaceID)
	if info, err := os.Lstat(directory); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return domain.WorkspaceIdentity{}, workspaceError("workspace_identity_conflict", false, nil)
		}
		metadata, metadataErr := w.readMetadata(directory)
		if errors.Is(metadataErr, os.ErrNotExist) {
			if removeErr := os.RemoveAll(directory); removeErr != nil {
				return domain.WorkspaceIdentity{}, workspaceError("workspace_cleanup_failed", true, removeErr)
			}
		} else {
			if metadataErr != nil {
				return domain.WorkspaceIdentity{}, workspaceError("workspace_recovery_required", false, metadataErr)
			}
			if metadata.ProjectID != request.ProjectID || metadata.WorkspaceLimitMiB != request.Profile.WorkspaceLimitMiB || metadata.Identity.WorkspaceID != workspaceID ||
				metadata.Identity.RunID != request.RunID || !sameObjectID(metadata.Identity.BaselineCommit, request.BaselineCommit) {
				return domain.WorkspaceIdentity{}, workspaceError("workspace_identity_conflict", false, nil)
			}
			if err := w.recoverPending(ctx, directory, &metadata); err != nil {
				return domain.WorkspaceIdentity{}, workspaceError("workspace_recovery_required", false, err)
			}
			if err := w.verifyWorkspace(ctx, directory, request, metadata); err != nil {
				return domain.WorkspaceIdentity{}, workspaceError("workspace_recovery_required", false, err)
			}
			return metadata.Identity, nil
		}
	} else if !os.IsNotExist(err) {
		return domain.WorkspaceIdentity{}, workspaceError("workspace_unavailable", true, err)
	}
	if err := os.Mkdir(directory, 0o700); err != nil {
		if os.IsExist(err) {
			metadata, loadErr := w.readMetadata(directory)
			if errors.Is(loadErr, os.ErrNotExist) {
				return domain.WorkspaceIdentity{}, workspaceError("workspace_initialization_in_progress", true, loadErr)
			}
			if loadErr != nil || metadata.ProjectID != request.ProjectID || metadata.WorkspaceLimitMiB != request.Profile.WorkspaceLimitMiB ||
				metadata.Identity.WorkspaceID != workspaceID || metadata.Identity.RunID != request.RunID ||
				!sameObjectID(metadata.Identity.BaselineCommit, request.BaselineCommit) {
				return domain.WorkspaceIdentity{}, workspaceError("workspace_identity_conflict", false, loadErr)
			}
			if recoverErr := w.recoverPending(ctx, directory, &metadata); recoverErr != nil {
				return domain.WorkspaceIdentity{}, workspaceError("workspace_recovery_required", false, recoverErr)
			}
			if verifyErr := w.verifyWorkspace(ctx, directory, request, metadata); verifyErr != nil {
				return domain.WorkspaceIdentity{}, workspaceError("workspace_recovery_required", false, verifyErr)
			}
			return metadata.Identity, nil
		}
		return domain.WorkspaceIdentity{}, workspaceError("workspace_unavailable", true, err)
	}
	repo := filepath.Join(directory, "repo")
	if err := os.Mkdir(repo, 0o700); err != nil {
		return domain.WorkspaceIdentity{}, workspaceError("workspace_unavailable", true, err)
	}
	gitCtx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	auth, cleanup, err := w.authentication(gitCtx, request)
	if err != nil {
		return domain.WorkspaceIdentity{}, workspaceError("repository_credential_unavailable", false, err)
	}
	defer cleanup()
	git := func(args ...string) ([]byte, error) { return w.runGit(gitCtx, repo, auth, nil, args...) }
	if _, err := git("init", "--quiet"); err != nil {
		return domain.WorkspaceIdentity{}, workspaceError("workspace_git_unavailable", true, err)
	}
	if _, err := git("-c", "core.hooksPath=/dev/null", "remote", "add", "origin", request.Repository.RemoteURL); err != nil {
		return domain.WorkspaceIdentity{}, workspaceError("workspace_repository_invalid", false, err)
	}
	// Size validation and checkout need local blobs to avoid per-object lazy fetches.
	if _, err := git("-c", "core.hooksPath=/dev/null", "fetch", "--no-tags", "--depth=1", "origin", request.BaselineCommit); err != nil {
		return domain.WorkspaceIdentity{}, workspaceError("workspace_baseline_unavailable", false, err)
	}
	baseline, err := git("rev-parse", "FETCH_HEAD^{commit}")
	if err != nil || !sameObjectID(strings.TrimSpace(string(baseline)), request.BaselineCommit) {
		return domain.WorkspaceIdentity{}, workspaceError("workspace_baseline_mismatch", false, err)
	}
	workspaceLimit := int64(request.Profile.WorkspaceLimitMiB) * 1024 * 1024
	if err := rejectUnsafeBaseline(func(args ...string) ([]byte, error) {
		return w.runGitWithLimit(gitCtx, repo, auth, nil, maxWorkspaceTreeBytes, args...)
	}, "FETCH_HEAD", workspaceLimit); err != nil {
		return domain.WorkspaceIdentity{}, workspaceError("workspace_baseline_unsafe", false, err)
	}
	if _, err := git("-c", "core.hooksPath=/dev/null", "checkout", "--quiet", "--detach", "FETCH_HEAD"); err != nil {
		return domain.WorkspaceIdentity{}, workspaceError("workspace_baseline_unavailable", false, err)
	}
	baseline, err = git("rev-parse", "HEAD^{commit}")
	if err != nil || !sameObjectID(strings.TrimSpace(string(baseline)), request.BaselineCommit) {
		return domain.WorkspaceIdentity{}, workspaceError("workspace_baseline_mismatch", false, err)
	}
	tree, err := git("write-tree")
	if err != nil || !fullObjectID(strings.TrimSpace(string(tree))) {
		return domain.WorkspaceIdentity{}, workspaceError("workspace_tree_unavailable", true, err)
	}
	identity := domain.WorkspaceIdentity{
		WorkspaceID: workspaceID, RunID: request.RunID, BaselineCommit: request.BaselineCommit,
		BaseTreeHash: strings.TrimSpace(string(tree)), CurrentTreeHash: strings.TrimSpace(string(tree)), Version: 1,
	}
	if err := w.checkWorkspaceSize(repo, request.Profile.WorkspaceLimitMiB); err != nil {
		return domain.WorkspaceIdentity{}, workspaceError("workspace_size_limit", false, err)
	}
	metadata := workspaceMetadata{
		FormatVersion: workspaceMetadataVersion, ProjectID: request.ProjectID,
		WorkspaceLimitMiB: request.Profile.WorkspaceLimitMiB, Identity: identity,
		Patches: map[string]workspacePatchRecord{},
	}
	if err := w.writeMetadata(directory, metadata); err != nil {
		return domain.WorkspaceIdentity{}, workspaceError("workspace_persistence_failed", true, err)
	}
	return identity, nil
}

func (w *GitWorkspace) Status(ctx context.Context, identity domain.WorkspaceIdentity) (domain.WorkspaceStatus, error) {
	if err := identity.Validate(); err != nil {
		return domain.WorkspaceStatus{}, workspaceError("workspace_identity_invalid", false, err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	directory := filepath.Join(w.root, identity.WorkspaceID)
	metadata, repo, err := w.loadIdentity(directory, identity)
	if err != nil {
		return domain.WorkspaceStatus{}, err
	}
	gitCtx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	auth := sanitizedGitEnvironment(os.Environ(), filepath.Join(directory, "home"))
	git := func(args ...string) ([]byte, error) { return w.runGit(gitCtx, repo, auth, nil, args...) }
	if err := w.ensureNoUnstagedChanges(git); err != nil {
		return domain.WorkspaceStatus{}, workspaceError("workspace_tree_changed", false, err)
	}
	tree, err := git("write-tree")
	if err != nil || !sameObjectID(strings.TrimSpace(string(tree)), metadata.Identity.CurrentTreeHash) {
		return domain.WorkspaceStatus{}, workspaceError("workspace_tree_changed", false, err)
	}
	changed, err := w.changedFiles(git)
	if err != nil {
		return domain.WorkspaceStatus{}, workspaceError("workspace_status_failed", true, err)
	}
	usage, err := w.workspaceUsage(repo)
	if err != nil || usage > int64(metadata.WorkspaceLimitMiB)*1024*1024 {
		return domain.WorkspaceStatus{}, workspaceError("workspace_size_limit", false, err)
	}
	metadata.Identity.CurrentTreeHash = strings.TrimSpace(string(tree))
	return domain.WorkspaceStatus{
		Identity: metadata.Identity, Clean: len(changed) == 0,
		ChangedFiles: changed, Bytes: usage,
	}, nil
}

func (w *GitWorkspace) ResolveWorkspaceMount(ctx context.Context, runID, workspaceID, expectedTreeHash string) (string, error) {
	if strings.TrimSpace(runID) == "" || len(runID) > 128 || len(workspaceID) != len("workspace-")+32 || !strings.HasPrefix(workspaceID, "workspace-") {
		return "", workspaceError("workspace_identity_invalid", false, nil)
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(workspaceID, "workspace-")); err != nil {
		return "", workspaceError("workspace_identity_invalid", false, err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	directory := filepath.Join(w.root, workspaceID)
	directoryInfo, err := os.Lstat(directory)
	if err != nil || !directoryInfo.IsDir() || directoryInfo.Mode()&os.ModeSymlink != 0 {
		return "", workspaceError("workspace_identity_conflict", false, err)
	}
	metadata, err := w.readMetadata(directory)
	if err != nil || metadata.Identity.WorkspaceID != workspaceID || metadata.Identity.RunID != runID || metadata.Pending != nil {
		return "", workspaceError("workspace_identity_conflict", false, err)
	}
	repo := filepath.Join(directory, "repo")
	gitCtx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	env := sanitizedGitEnvironment(os.Environ(), filepath.Join(directory, "home"))
	git := func(args ...string) ([]byte, error) { return w.runGit(gitCtx, repo, env, nil, args...) }
	if err := w.ensureNoUnstagedChanges(git); err != nil {
		return "", workspaceError("workspace_tree_changed", false, err)
	}
	tree, err := git("write-tree")
	if err != nil || !sameObjectID(strings.TrimSpace(string(tree)), expectedTreeHash) ||
		!sameObjectID(strings.TrimSpace(string(tree)), metadata.Identity.CurrentTreeHash) {
		return "", workspaceError("workspace_tree_changed", false, err)
	}
	if err := w.checkWorkspaceSize(repo, metadata.WorkspaceLimitMiB); err != nil {
		return "", workspaceError("workspace_size_limit", false, err)
	}
	return repo, nil
}

func (w *GitWorkspace) ReadFile(ctx context.Context, identity domain.WorkspaceIdentity, name string, maxBytes int64) (domain.WorkspaceFile, error) {
	if err := ctx.Err(); err != nil {
		return domain.WorkspaceFile{}, err
	}
	if err := identity.Validate(); err != nil {
		return domain.WorkspaceFile{}, workspaceError("workspace_identity_invalid", false, err)
	}
	if err := safeWorkspacePath(name); err != nil {
		return domain.WorkspaceFile{}, workspaceError("workspace_path_invalid", false, err)
	}
	if maxBytes < 1 || maxBytes > maxWorkspaceFileBytes {
		maxBytes = maxWorkspaceFileBytes
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	_, repo, err := w.loadIdentity(filepath.Join(w.root, identity.WorkspaceID), identity)
	if err != nil {
		return domain.WorkspaceFile{}, err
	}
	filePath := filepath.Join(repo, filepath.FromSlash(name))
	if err := rejectSymlinkPath(repo, filePath); err != nil {
		return domain.WorkspaceFile{}, workspaceError("workspace_path_invalid", false, err)
	}
	info, err := os.Stat(filePath)
	if err != nil {
		return domain.WorkspaceFile{}, workspaceError("workspace_file_unavailable", false, err)
	}
	if !info.Mode().IsRegular() || info.Size() < 0 {
		return domain.WorkspaceFile{}, workspaceError("workspace_file_invalid", false, nil)
	}
	file, err := os.Open(filePath)
	if err != nil {
		return domain.WorkspaceFile{}, workspaceError("workspace_file_unavailable", false, err)
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return domain.WorkspaceFile{}, workspaceError("workspace_file_unavailable", false, err)
	}
	truncated := int64(len(content)) > maxBytes
	if truncated {
		content = content[:maxBytes]
	}
	return domain.WorkspaceFile{Path: name, Content: content, Truncated: truncated}, nil
}

func (w *GitWorkspace) ApplyPatch(ctx context.Context, identity domain.WorkspaceIdentity, request domain.PatchRequest) (domain.PatchResult, error) {
	if err := identity.Validate(); err != nil {
		return domain.PatchResult{}, workspaceError("workspace_identity_invalid", false, err)
	}
	if err := request.Validate(); err != nil || request.WorkspaceID != identity.WorkspaceID {
		return domain.PatchResult{}, workspaceError("patch_request_invalid", false, err)
	}
	patch := []byte(request.Patch)
	patchSum := sha256.Sum256(patch)
	patchHash := hex.EncodeToString(patchSum[:])
	w.mu.Lock()
	defer w.mu.Unlock()
	directory := filepath.Join(w.root, identity.WorkspaceID)
	metadata, repo, err := w.loadIdentity(directory, identity)
	if err != nil {
		return domain.PatchResult{}, err
	}
	if previous, ok := metadata.Patches[request.IdempotencyKey]; ok {
		if previous.PatchHash != patchHash || previous.ExpectedTreeHash != request.ExpectedTreeHash {
			return domain.PatchResult{}, workspaceError("patch_idempotency_conflict", false, nil)
		}
		return patchResult(identity.WorkspaceID, previous, true), nil
	}
	gitCtx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	env := sanitizedGitEnvironment(os.Environ(), filepath.Join(directory, "home"))
	git := func(args ...string) ([]byte, error) { return w.runGit(gitCtx, repo, env, nil, args...) }
	if metadata.Pending != nil {
		pending := metadata.Pending
		if pending.IdempotencyKey == request.IdempotencyKey && pending.PatchHash == patchHash && sameObjectID(pending.ExpectedTreeHash, request.ExpectedTreeHash) {
			currentTree, treeErr := git("write-tree")
			if treeErr != nil {
				return domain.PatchResult{}, workspaceError("workspace_tree_unavailable", true, treeErr)
			}
			if !sameObjectID(strings.TrimSpace(string(currentTree)), pending.ExpectedTreeHash) {
				return w.completePendingPatch(gitCtx, directory, repo, env, &metadata, true)
			}
		} else {
			currentTree, treeErr := git("write-tree")
			if treeErr != nil || !sameObjectID(strings.TrimSpace(string(currentTree)), pending.ExpectedTreeHash) {
				return domain.PatchResult{}, workspaceError("workspace_recovery_required", false, treeErr)
			}
			metadata.Pending = nil
			if err := w.writeMetadata(directory, metadata); err != nil {
				return domain.PatchResult{}, workspaceError("workspace_persistence_failed", true, err)
			}
		}
	}
	if identity.Version != metadata.Identity.Version || !sameObjectID(request.ExpectedTreeHash, metadata.Identity.CurrentTreeHash) {
		return domain.PatchResult{}, workspaceError("workspace_version_conflict", false, nil)
	}
	if err := w.ensureNoUnstagedChanges(git); err != nil {
		return domain.PatchResult{}, workspaceError("workspace_tree_changed", false, err)
	}
	currentTree, err := git("write-tree")
	if err != nil || !sameObjectID(strings.TrimSpace(string(currentTree)), request.ExpectedTreeHash) {
		return domain.PatchResult{}, workspaceError("workspace_version_conflict", false, err)
	}
	incomingRef, incomingHash, err := w.artifacts.Put(gitCtx, patch)
	if err != nil || incomingHash != patchHash {
		return domain.PatchResult{}, workspaceError("patch_artifact_unavailable", true, err)
	}
	metadata.Pending = &workspacePendingPatch{
		IdempotencyKey: request.IdempotencyKey, PatchHash: patchHash,
		ExpectedTreeHash: request.ExpectedTreeHash, ArtifactRef: incomingRef,
		ContentHash: incomingHash, ChangePolicy: request.ChangePolicy,
	}
	if err := w.writeMetadata(directory, metadata); err != nil {
		return domain.PatchResult{}, workspaceError("workspace_persistence_failed", true, err)
	}
	if _, err := w.runGit(gitCtx, repo, env, bytes.NewReader(patch), "-c", "core.hooksPath=/dev/null", "apply", "--check", "--index", "--binary"); err != nil {
		return domain.PatchResult{}, workspaceError("patch_replay_failed", false, err)
	}
	if _, err := w.runGit(gitCtx, repo, env, bytes.NewReader(patch), "-c", "core.hooksPath=/dev/null", "apply", "--index", "--binary"); err != nil {
		return domain.PatchResult{}, workspaceError("patch_replay_failed", false, err)
	}
	return w.completePendingPatch(gitCtx, directory, repo, env, &metadata, false)
}

func (w *GitWorkspace) recoverPending(ctx context.Context, directory string, metadata *workspaceMetadata) error {
	if metadata.Pending == nil {
		return nil
	}
	repo := filepath.Join(directory, "repo")
	gitCtx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	env := sanitizedGitEnvironment(os.Environ(), filepath.Join(directory, "home"))
	git := func(args ...string) ([]byte, error) { return w.runGit(gitCtx, repo, env, nil, args...) }
	tree, err := git("write-tree")
	if err != nil {
		return err
	}
	if sameObjectID(strings.TrimSpace(string(tree)), metadata.Pending.ExpectedTreeHash) {
		return nil
	}
	_, err = w.completePendingPatch(gitCtx, directory, repo, env, metadata, true)
	return err
}

func (w *GitWorkspace) completePendingPatch(ctx context.Context, directory, repo string, env []string, metadata *workspaceMetadata, alreadyApplied bool) (domain.PatchResult, error) {
	if metadata.Pending == nil {
		return domain.PatchResult{}, workspaceError("workspace_recovery_required", false, nil)
	}
	pending := *metadata.Pending
	patch, err := w.artifacts.Get(ctx, pending.ArtifactRef, maxWorkspacePatchBytes)
	if err != nil {
		return domain.PatchResult{}, workspaceError("patch_artifact_unavailable", false, err)
	}
	patchSum := sha256.Sum256(patch)
	if len(patch) == 0 || hex.EncodeToString(patchSum[:]) != pending.PatchHash || pending.ContentHash != pending.PatchHash {
		return domain.PatchResult{}, workspaceError("patch_artifact_hash_mismatch", false, nil)
	}
	git := func(args ...string) ([]byte, error) { return w.runGit(ctx, repo, env, nil, args...) }
	tree, err := git("write-tree")
	if err != nil {
		return domain.PatchResult{}, workspaceError("workspace_tree_unavailable", true, err)
	}
	currentTree := strings.TrimSpace(string(tree))
	if sameObjectID(currentTree, pending.ExpectedTreeHash) {
		return domain.PatchResult{}, workspaceError("patch_not_applied", false, nil)
	}
	if _, err := w.runGit(ctx, repo, env, bytes.NewReader(patch), "-c", "core.hooksPath=/dev/null", "apply", "--reverse", "--check", "--index", "--binary"); err != nil {
		return domain.PatchResult{}, workspaceError("workspace_recovery_required", false, err)
	}
	validator := &Publisher{command: w.command}
	if err := validator.validateTree(ctx, repo, env, pending.ChangePolicy); err != nil {
		return domain.PatchResult{}, w.rollbackRejectedPatch(ctx, directory, repo, env, metadata, pending.ExpectedTreeHash, "change_policy_rejected", err)
	}
	if err := w.checkWorkspaceSize(repo, metadata.WorkspaceLimitMiB); err != nil {
		return domain.PatchResult{}, w.rollbackRejectedPatch(ctx, directory, repo, env, metadata, pending.ExpectedTreeHash, "workspace_size_limit", err)
	}
	fullPatch, err := git("diff", "--cached", "--binary", "HEAD", "--")
	if err != nil || len(fullPatch) == 0 || len(fullPatch) > maxWorkspacePatchBytes {
		return domain.PatchResult{}, workspaceError("patch_artifact_invalid", false, err)
	}
	artifactRef, contentHash, err := w.artifacts.Put(ctx, fullPatch)
	if err != nil {
		return domain.PatchResult{}, workspaceError("patch_artifact_unavailable", true, err)
	}
	changed, err := w.changedFiles(git)
	if err != nil {
		return domain.PatchResult{}, workspaceError("workspace_status_failed", true, err)
	}
	record := workspacePatchRecord{
		PatchHash: pending.PatchHash, ExpectedTreeHash: pending.ExpectedTreeHash,
		ArtifactRef: artifactRef, ContentHash: contentHash,
		ResultTreeHash: currentTree, ChangedFiles: changed,
	}
	metadata.Patches[pending.IdempotencyKey] = record
	metadata.Identity.CurrentTreeHash = currentTree
	metadata.Identity.Version++
	metadata.Pending = nil
	if err := w.writeMetadata(directory, *metadata); err != nil {
		return domain.PatchResult{}, workspaceError("workspace_persistence_failed", true, err)
	}
	result := patchResult(metadata.Identity.WorkspaceID, record, alreadyApplied)
	result.BytesRetrieved = int64(len(fullPatch))
	return result, nil
}

func (w *GitWorkspace) rollbackRejectedPatch(ctx context.Context, directory, repo string, env []string, metadata *workspaceMetadata, tree, code string, cause error) error {
	if _, err := w.runGit(ctx, repo, env, nil, "read-tree", "--reset", "-u", tree); err != nil {
		return workspaceError("workspace_rollback_failed", true, err)
	}
	if _, err := w.runGit(ctx, repo, env, nil, "clean", "-fd", "--"); err != nil {
		return workspaceError("workspace_rollback_failed", true, err)
	}
	metadata.Pending = nil
	if err := w.writeMetadata(directory, *metadata); err != nil {
		return workspaceError("workspace_persistence_failed", true, err)
	}
	return workspaceError(code, false, cause)
}

func (w *GitWorkspace) Destroy(ctx context.Context, identity domain.WorkspaceIdentity) error {
	if err := identity.Validate(); err != nil {
		return workspaceError("workspace_identity_invalid", false, err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	directory := filepath.Join(w.root, identity.WorkspaceID)
	metadata, err := w.readMetadata(directory)
	if err != nil {
		return workspaceError("workspace_unavailable", false, err)
	}
	if metadata.Identity.RunID != identity.RunID || !sameObjectID(metadata.Identity.BaselineCommit, identity.BaselineCommit) {
		return workspaceError("workspace_identity_conflict", false, nil)
	}
	if err := os.RemoveAll(directory); err != nil {
		return workspaceError("workspace_destroy_failed", true, err)
	}
	return nil
}

func validateWorkspaceRequest(request domain.WorkspaceRequest, allowLocal bool) error {
	if strings.TrimSpace(request.RunID) == "" || len(request.RunID) > 128 || strings.TrimSpace(request.ProjectID) == "" || len(request.ProjectID) > 128 ||
		!fullObjectID(request.BaselineCommit) || strings.TrimSpace(request.IdempotencyKey) == "" || len(request.IdempotencyKey) > 256 {
		return fmt.Errorf("workspace request identity is invalid")
	}
	if request.Profile.CPULimit < 1 || request.Profile.CPULimit > 16 || request.Profile.MemoryLimitMiB < 256 || request.Profile.MemoryLimitMiB > 65536 ||
		request.Profile.WorkspaceLimitMiB < 1024 || request.Profile.WorkspaceLimitMiB > maxProfileWorkspaceMiB {
		return fmt.Errorf("workspace resource snapshot is invalid")
	}
	remote, err := url.Parse(request.Repository.RemoteURL)
	local := remote != nil && remote.Scheme == "file"
	if err != nil || remote.User != nil || remote.RawQuery != "" || remote.Fragment != "" || remote.Opaque != "" || remote.Scheme != request.Repository.Transport ||
		(local && !allowLocal) || (!local && (remote.Host == "" || (remote.Scheme != "https" && remote.Scheme != "ssh"))) {
		return fmt.Errorf("workspace repository snapshot is invalid")
	}
	if request.Repository.Transport == "ssh" && request.Repository.RepositoryCredentialSecretID == "" {
		return fmt.Errorf("SSH repository credential is required")
	}
	if request.Repository.RepositoryCredentialSecretID != "" && request.Repository.RepositoryCredentialVersion < 1 {
		return fmt.Errorf("workspace repository credential snapshot is invalid")
	}
	return nil
}

func (w *GitWorkspace) authentication(ctx context.Context, request domain.WorkspaceRequest) ([]string, func(), error) {
	directory, err := os.MkdirTemp("", "mendry-remediation-read-auth-")
	if err != nil {
		return nil, func() {}, err
	}
	_ = os.Chmod(directory, 0o700)
	cleanupDirectory := func() { _ = os.RemoveAll(directory) }
	credentialID := request.Repository.RepositoryCredentialSecretID
	if credentialID == "" {
		if request.Repository.Transport == "ssh" {
			cleanupDirectory()
			return nil, func() {}, fmt.Errorf("SSH repository credential is required")
		}
		home := filepath.Join(directory, "home")
		if err := os.Mkdir(home, 0o700); err != nil {
			cleanupDirectory()
			return nil, func() {}, err
		}
		return sanitizedGitEnvironment(os.Environ(), home), cleanupDirectory, nil
	}
	if w.credentials == nil {
		cleanupDirectory()
		return nil, func() {}, fmt.Errorf("repository credential resolver is unavailable")
	}
	credential, err := w.credentials.ResolveRepositoryCredential(ctx, request.ProjectID, credentialID, request.Repository.RepositoryCredentialVersion, request.Repository.Transport)
	if err != nil {
		cleanupDirectory()
		return nil, func() {}, err
	}
	defer zeroBytes(credential)
	auth, err := newGitAuthentication(directory, request.Repository.Transport, credential, w.knownHostsFile)
	if err != nil {
		cleanupDirectory()
		return nil, func() {}, err
	}
	return auth.env, auth.Close, nil
}

func (w *GitWorkspace) verifyWorkspace(ctx context.Context, directory string, request domain.WorkspaceRequest, metadata workspaceMetadata) error {
	repo := filepath.Join(directory, "repo")
	info, err := os.Lstat(repo)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("workspace repository directory is invalid")
	}
	gitCtx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	env := sanitizedGitEnvironment(os.Environ(), filepath.Join(directory, "home"))
	git := func(args ...string) ([]byte, error) { return w.runGit(gitCtx, repo, env, nil, args...) }
	remote, err := git("remote", "get-url", "origin")
	if err != nil || strings.TrimSpace(string(remote)) != request.Repository.RemoteURL {
		return fmt.Errorf("workspace repository snapshot changed")
	}
	baseline, err := git("rev-parse", "HEAD^{commit}")
	if err != nil || !sameObjectID(strings.TrimSpace(string(baseline)), request.BaselineCommit) {
		return fmt.Errorf("workspace baseline changed")
	}
	if err := w.ensureNoUnstagedChanges(git); err != nil {
		return err
	}
	tree, err := git("write-tree")
	if err != nil || !sameObjectID(strings.TrimSpace(string(tree)), metadata.Identity.CurrentTreeHash) {
		return fmt.Errorf("workspace tree does not match durable metadata")
	}
	return w.checkWorkspaceSize(repo, request.Profile.WorkspaceLimitMiB)
}

func (w *GitWorkspace) loadIdentity(directory string, identity domain.WorkspaceIdentity) (workspaceMetadata, string, error) {
	metadata, err := w.readMetadata(directory)
	if err != nil {
		return workspaceMetadata{}, "", workspaceError("workspace_unavailable", false, err)
	}
	if metadata.Identity.WorkspaceID != identity.WorkspaceID || metadata.Identity.RunID != identity.RunID ||
		!sameObjectID(metadata.Identity.BaselineCommit, identity.BaselineCommit) ||
		!sameObjectID(metadata.Identity.BaseTreeHash, identity.BaseTreeHash) {
		return workspaceMetadata{}, "", workspaceError("workspace_identity_conflict", false, nil)
	}
	return metadata, filepath.Join(directory, "repo"), nil
}

func (w *GitWorkspace) readMetadata(directory string) (workspaceMetadata, error) {
	filePath := filepath.Join(directory, "metadata.json")
	info, err := os.Lstat(filePath)
	if err != nil {
		return workspaceMetadata{}, err
	}
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > maxWorkspaceStateBytes {
		return workspaceMetadata{}, fmt.Errorf("workspace metadata type or size is invalid")
	}
	content, err := os.ReadFile(filePath)
	if err != nil {
		return workspaceMetadata{}, err
	}
	var metadata workspaceMetadata
	if err := json.Unmarshal(content, &metadata); err != nil || metadata.FormatVersion != workspaceMetadataVersion || metadata.Patches == nil ||
		metadata.WorkspaceLimitMiB < 1024 || metadata.WorkspaceLimitMiB > maxProfileWorkspaceMiB {
		return workspaceMetadata{}, fmt.Errorf("workspace metadata is invalid")
	}
	if err := metadata.Identity.Validate(); err != nil {
		return workspaceMetadata{}, err
	}
	return metadata, nil
}

func (w *GitWorkspace) writeMetadata(directory string, metadata workspaceMetadata) error {
	content, err := json.Marshal(metadata)
	if err != nil || len(content) > maxWorkspaceStateBytes {
		return fmt.Errorf("workspace metadata exceeds its limit")
	}
	file, err := os.CreateTemp(directory, ".metadata-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, filepath.Join(directory, "metadata.json")); err != nil {
		return err
	}
	return nil
}

func (w *GitWorkspace) changedFiles(git func(...string) ([]byte, error)) ([]string, error) {
	output, err := git("diff", "--cached", "--name-only", "-z", "--no-renames", "HEAD", "--")
	if err != nil {
		return nil, err
	}
	files := make([]string, 0)
	for _, entry := range bytes.Split(output, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		name := string(entry)
		if err := safeWorkspacePath(name); err != nil {
			return nil, err
		}
		files = append(files, name)
	}
	return files, nil
}

func (w *GitWorkspace) ensureNoUnstagedChanges(git func(...string) ([]byte, error)) error {
	output, err := git("status", "--porcelain=v1", "-z", "--no-renames", "--untracked-files=all")
	if err != nil {
		return err
	}
	for _, entry := range bytes.Split(output, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		if len(entry) < 3 || entry[0] == '?' || entry[1] != ' ' {
			return fmt.Errorf("workspace has untracked or unstaged changes")
		}
	}
	return nil
}

func (w *GitWorkspace) checkWorkspaceSize(directory string, limitMiB int) error {
	return checkGitWorkspaceSize(directory, limitMiB)
}

func checkGitWorkspaceSize(directory string, limitMiB int) error {
	if limitMiB < 1 {
		return fmt.Errorf("workspace size limit is invalid")
	}
	limit := int64(limitMiB) * 1024 * 1024
	total, err := measureWorkspaceUsage(directory)
	if err != nil {
		return err
	}
	if total > limit {
		return fmt.Errorf("workspace size limit exceeded")
	}
	return nil
}

func (w *GitWorkspace) workspaceUsage(directory string) (int64, error) {
	return measureWorkspaceUsage(directory)
}

func measureWorkspaceUsage(directory string) (int64, error) {
	var total int64
	err := filepath.WalkDir(directory, func(_ string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("workspace symlinks are prohibited")
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() < 0 {
			return fmt.Errorf("workspace file type is invalid")
		}
		total += info.Size()
		return nil
	})
	return total, err
}

func (w *GitWorkspace) runGit(ctx context.Context, repo string, env []string, stdin io.Reader, args ...string) ([]byte, error) {
	return w.runGitWithLimit(ctx, repo, env, stdin, 64<<10, args...)
}

func (w *GitWorkspace) runGitWithLimit(ctx context.Context, repo string, env []string, stdin io.Reader, maxOutput int, args ...string) ([]byte, error) {
	publisher := &Publisher{command: w.command}
	return publisher.runGitWithLimit(ctx, repo, env, stdin, maxOutput, args...)
}

func workspaceID(projectID, runID string) string {
	sum := sha256.Sum256([]byte(projectID + "\x00" + runID))
	return "workspace-" + hex.EncodeToString(sum[:16])
}

func patchResult(workspaceID string, record workspacePatchRecord, alreadyApplied bool) domain.PatchResult {
	return domain.PatchResult{
		WorkspaceID: workspaceID, Applied: !alreadyApplied, AlreadyApplied: alreadyApplied,
		ArtifactRef: record.ArtifactRef, ContentHash: record.ContentHash,
		ResultTreeHash: record.ResultTreeHash, ChangedFiles: append([]string(nil), record.ChangedFiles...),
		BytesRetrieved: 0, Summary: fmt.Sprintf("validated patch tree %s", record.ResultTreeHash),
	}
}

func safeWorkspacePath(name string) error {
	if name == "" || strings.ContainsRune(name, '\x00') || strings.ContainsAny(name, "\r\n") || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "\\") {
		return fmt.Errorf("workspace path is invalid")
	}
	for _, component := range strings.Split(strings.ReplaceAll(name, "\\", "/"), "/") {
		if component == "" || component == "." || component == ".." || component == ".git" {
			return fmt.Errorf("workspace path is invalid")
		}
	}
	return nil
}

func rejectSymlinkPath(root, target string) error {
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || relative == ".." {
		return fmt.Errorf("workspace path escaped its root")
	}
	current := root
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("workspace symlinks are prohibited")
		}
	}
	return nil
}

func ensurePrivateWorkspaceRoot(root string) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("create workspace root: %w", err)
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("workspace root must be a real directory")
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return fmt.Errorf("secure workspace root: %w", err)
	}
	return nil
}

func workspaceError(code string, retryable bool, cause error) error {
	return &domain.LifecycleRuntimeError{Code: code, Retryable: retryable, Cause: cause}
}

// maxProfileWorkspaceMiB is a hard safety ceiling; active requests are checked against their own snapshot.
const maxProfileWorkspaceMiB = 102400
