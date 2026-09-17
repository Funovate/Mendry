package git

import (
	"context"
	"fmt"
	"strings"

	projectapplication "mendry/backend/internal/modules/projects/application"
	projectdomain "mendry/backend/internal/modules/projects/domain"
)

const maxGitCredentialBytes = 64 << 10

// VersionedCredentialResolver decrypts only the credential version captured by a run.
type VersionedCredentialResolver struct {
	secrets SecretLoader
	cipher  projectapplication.Cipher
}

type RepositoryCredentialResolver interface {
	ResolveRepositoryCredential(context.Context, string, string, int64, string) ([]byte, error)
}

func NewVersionedCredentialResolver(secrets SecretLoader, cipher projectapplication.Cipher) (*VersionedCredentialResolver, error) {
	if secrets == nil || cipher == nil {
		return nil, fmt.Errorf("Git credential resolver dependencies are required")
	}
	return &VersionedCredentialResolver{secrets: secrets, cipher: cipher}, nil
}

func (r *VersionedCredentialResolver) ResolveGitCredential(ctx context.Context, projectID, secretID string, version int64) ([]byte, error) {
	return r.resolve(ctx, projectID, secretID, version, func(kind projectdomain.SecretKind) bool {
		return kind == projectdomain.SecretGitCredential
	})
}

func (r *VersionedCredentialResolver) ResolveAPICredential(ctx context.Context, projectID, secretID string, version int64) ([]byte, error) {
	return r.resolve(ctx, projectID, secretID, version, func(kind projectdomain.SecretKind) bool {
		return kind == projectdomain.SecretHTTPBearer
	})
}

func (r *VersionedCredentialResolver) ResolveRepositoryCredential(ctx context.Context, projectID, secretID string, version int64, transport string) ([]byte, error) {
	return r.resolve(ctx, projectID, secretID, version, func(kind projectdomain.SecretKind) bool {
		switch transport {
		case "https":
			return kind == projectdomain.SecretGitCredential || kind == projectdomain.SecretHTTPBearer
		case "ssh":
			return kind == projectdomain.SecretSSHPrivateKey
		default:
			return false
		}
	})
}

func (r *VersionedCredentialResolver) resolve(ctx context.Context, projectID, secretID string, version int64, allowed func(projectdomain.SecretKind) bool) ([]byte, error) {
	if r == nil || r.secrets == nil || r.cipher == nil || strings.TrimSpace(projectID) == "" || strings.TrimSpace(secretID) == "" || version < 1 {
		return nil, fmt.Errorf("Git credential snapshot is invalid")
	}
	secret, err := r.secrets.GetEncryptedSecret(ctx, projectID, secretID)
	if err != nil {
		return nil, fmt.Errorf("load Git credential snapshot: %w", err)
	}
	if secret.ProjectID != projectID || secret.ID != secretID || !allowed(secret.Kind) || secret.Version != version {
		return nil, fmt.Errorf("Git credential version no longer matches the run snapshot")
	}
	credential, err := r.cipher.Decrypt(projectID, secretID, secret.Kind, secret.Ciphertext, secret.Nonce)
	if err != nil {
		return nil, fmt.Errorf("decrypt Git credential: %w", err)
	}
	if len(credential) == 0 || len(credential) > maxGitCredentialBytes {
		zeroBytes(credential)
		return nil, fmt.Errorf("Git credential size is invalid")
	}
	return credential, nil
}

var _ GitCredentialResolver = (*VersionedCredentialResolver)(nil)
