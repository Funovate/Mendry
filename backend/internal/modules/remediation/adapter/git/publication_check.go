package git

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"

	"mendry/backend/internal/modules/remediation/domain"
)

// CheckPublicationAccess probes receive-pack with the publication credential.
// A dry run cannot guarantee later server hooks accept a real push.
func (w *GitWorkspace) CheckPublicationAccess(ctx context.Context, request domain.WorkspaceRequest, identity domain.WorkspaceIdentity) error {
	if err := validateWorkspaceRequest(request, w.allowLocal); err != nil {
		return workspaceError("publication_check_invalid", false, err)
	}
	if err := identity.Validate(); err != nil {
		return workspaceError("publication_check_invalid", false, err)
	}
	if identity.RunID != request.RunID || identity.WorkspaceID != workspaceID(request.ProjectID, request.RunID) || !sameObjectID(identity.BaselineCommit, request.BaselineCommit) {
		return workspaceError("workspace_identity_conflict", false, nil)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	directory := filepath.Join(w.root, identity.WorkspaceID)
	metadata, err := w.readMetadata(directory)
	if err != nil || metadata.ProjectID != request.ProjectID || metadata.Identity.RunID != request.RunID {
		return workspaceError("workspace_identity_conflict", false, err)
	}
	if request.Repository.GitCredentialSecretID == "" || request.Repository.GitCredentialVersion < 1 {
		return workspaceError("publication_credential_missing", false, nil)
	}
	request.Repository.RepositoryCredentialSecretID = request.Repository.GitCredentialSecretID
	request.Repository.RepositoryCredentialVersion = request.Repository.GitCredentialVersion
	checkCtx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	auth, cleanup, err := w.authentication(checkCtx, request)
	if err != nil {
		return workspaceError("publication_credential_unavailable", false, err)
	}
	defer cleanup()
	sum := sha256.Sum256([]byte(request.RunID))
	ref := "HEAD:refs/heads/hotfix/remediation/check-" + hex.EncodeToString(sum[:8])
	_, err = w.runGit(checkCtx, filepath.Join(directory, "repo"), auth, nil, "-c", "core.hooksPath=/dev/null", "push", "--dry-run", "--no-verify", request.Repository.RemoteURL, ref)
	if err != nil {
		return workspaceError("publication_access_check_failed", false, err)
	}
	return nil
}
