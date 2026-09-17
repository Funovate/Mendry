package postgres

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"mendry/backend/internal/modules/projects/application"
	"mendry/backend/internal/modules/projects/domain"
)

type hotfixTransactor interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}
type HotfixCommitter struct{ db hotfixTransactor }

var dependencyHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func NewHotfixCommitter(db hotfixTransactor) *HotfixCommitter { return &HotfixCommitter{db: db} }

// CommitPreparedHotfix locks all inputs used by the preflight so a simultaneous
// repository edit or credential rotation cannot enable stale execution settings.
func (c *HotfixCommitter) CommitPreparedHotfix(ctx context.Context, projectID string, request application.HotfixPreparationRequest, original, policy domain.RemediationPolicy) (domain.RemediationPolicy, error) {
	if err := domain.ValidateRemediationPolicy(policy); err != nil ||
		policy.ValidationProfile.PreparedCommit != request.Repository.DeployedCommit ||
		strings.TrimSpace(policy.ValidationProfile.ToolchainID) == "" ||
		strings.TrimSpace(policy.ValidationProfile.BuildPlanVersion) == "" ||
		!dependencyHashPattern.MatchString(policy.ValidationProfile.DependencyHash) {
		return domain.RemediationPolicy{}, application.ErrInvalidInput
	}
	tx, err := c.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return domain.RemediationPolicy{}, err
	}
	defer tx.Rollback(ctx)
	var version int64
	if err := tx.QueryRow(ctx, `SELECT agent_loop_policy_version FROM projects WHERE id=$1 FOR UPDATE`, projectID).Scan(&version); err != nil {
		return domain.RemediationPolicy{}, err
	}
	if version != original.Version {
		return domain.RemediationPolicy{}, application.ErrConflict
	}
	var repoID string
	if err := tx.QueryRow(ctx, `SELECT id::text, version FROM project_repositories WHERE project_id=$1 FOR UPDATE`, projectID).Scan(&repoID, &version); err != nil {
		return domain.RemediationPolicy{}, err
	}
	if repoID != request.Repository.ID || version != request.Repository.Version {
		return domain.RemediationPolicy{}, application.ErrConflict
	}
	for _, secret := range []domain.Secret{request.GitCredential, request.RepositoryCredential} {
		if secret.ID == "" {
			continue
		}
		if err := tx.QueryRow(ctx, `SELECT version FROM project_secrets WHERE project_id=$1 AND id=$2 FOR SHARE`, projectID, secret.ID).Scan(&version); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.RemediationPolicy{}, application.ErrConflict
			}
			return domain.RemediationPolicy{}, err
		}
		if version != secret.Version {
			return domain.RemediationPolicy{}, application.ErrConflict
		}
	}
	repository, err := NewRepository(tx)
	if err != nil {
		return domain.RemediationPolicy{}, err
	}
	saved, err := repository.UpsertRemediationPolicy(ctx, projectID, policy)
	if err != nil {
		return domain.RemediationPolicy{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.RemediationPolicy{}, err
	}
	return saved, nil
}
