package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"mendry/backend/internal/modules/projects/application"
)

type hotfixJobDB interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

type HotfixJobStore struct{ db hotfixJobDB }

func NewHotfixJobStore(db hotfixJobDB) *HotfixJobStore { return &HotfixJobStore{db: db} }

func (s *HotfixJobStore) Enqueue(ctx context.Context, job application.HotfixPreparationJob) error {
	if s == nil || s.db == nil || job.ProjectID == "" || job.TargetCommit == "" || job.Directory == "" {
		return fmt.Errorf("validation preparation job is invalid")
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO project_validation_preparations(project_id,target_commit,service_directory,status,attempt_count)
		VALUES($1,$2,$3,'queued',0)
		ON CONFLICT(project_id) DO UPDATE SET
			target_commit=EXCLUDED.target_commit,
			service_directory=EXCLUDED.service_directory,
			status='queued',
			attempt_count=0,
			updated_at=now()`, job.ProjectID, job.TargetCommit, job.Directory)
	if err != nil {
		return fmt.Errorf("enqueue validation preparation: %w", err)
	}
	return nil
}

func (s *HotfixJobStore) Recover(ctx context.Context, limit int) ([]application.HotfixPreparationJob, error) {
	if s == nil || s.db == nil || limit < 1 || limit > 128 {
		return nil, fmt.Errorf("validation preparation recovery limit is invalid")
	}
	if _, err := s.db.Exec(ctx, `UPDATE project_validation_preparations SET status='queued',updated_at=now() WHERE status='running'`); err != nil {
		return nil, fmt.Errorf("reset running validation preparations: %w", err)
	}
	return s.Queued(ctx, limit)
}

func (s *HotfixJobStore) Queued(ctx context.Context, limit int) ([]application.HotfixPreparationJob, error) {
	if s == nil || s.db == nil || limit < 1 || limit > 128 {
		return nil, fmt.Errorf("validation preparation queue limit is invalid")
	}
	rows, err := s.db.Query(ctx, `
		SELECT project_id::text,target_commit,service_directory
		FROM project_validation_preparations
		WHERE status='queued'
		ORDER BY updated_at,project_id
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list validation preparations: %w", err)
	}
	defer rows.Close()
	jobs := make([]application.HotfixPreparationJob, 0)
	for rows.Next() {
		var job application.HotfixPreparationJob
		if err := rows.Scan(&job.ProjectID, &job.TargetCommit, &job.Directory); err != nil {
			return nil, fmt.Errorf("scan validation preparation: %w", err)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate validation preparations: %w", err)
	}
	return jobs, nil
}

func (s *HotfixJobStore) MarkRunning(ctx context.Context, projectID, targetCommit string) error {
	return s.transition(ctx, projectID, targetCommit, "running", true)
}

func (s *HotfixJobStore) MarkFinished(ctx context.Context, projectID, targetCommit string, succeeded bool) error {
	status := "blocked"
	if succeeded {
		status = "completed"
	}
	return s.transition(ctx, projectID, targetCommit, status, false)
}

func (s *HotfixJobStore) transition(ctx context.Context, projectID, targetCommit, status string, increment bool) error {
	attempt := "attempt_count"
	if increment {
		attempt = "attempt_count+1"
	}
	tag, err := s.db.Exec(ctx, `UPDATE project_validation_preparations
		SET status=$3,attempt_count=`+attempt+`,updated_at=now()
		WHERE project_id=$1 AND target_commit=$2`, projectID, targetCommit, status)
	if err != nil {
		return fmt.Errorf("update validation preparation status: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("validation preparation job is stale")
	}
	return nil
}
