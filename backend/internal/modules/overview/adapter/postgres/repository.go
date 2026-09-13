package postgres

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"mendry/backend/internal/modules/overview/application"
	platformpostgres "mendry/backend/internal/platform/postgres"
)

//go:embed runs.sql
var runsSQL string

//go:embed snapshot.sql
var snapshotSQL string

//go:embed tasks.sql
var tasksSQL string

type Database interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}
type Repository struct{ database Database }

func NewRepository(database Database) (*Repository, error) {
	if database == nil {
		return nil, errors.New("overview database is required")
	}
	return &Repository{database: database}, nil
}

// One SQL statement gives all aggregates the same MVCC snapshot without holding
// an idle transaction open between multiple queries.
func (r *Repository) Snapshot(ctx context.Context, projectID string, w application.Window) (application.Snapshot, error) {
	var raw []byte
	var result application.Snapshot
	err := r.database.QueryRow(platformpostgres.WithOperation(ctx, "overview.snapshot"), runsSQL+snapshotSQL,
		projectID, w.Today, w.Now, w.Start, w.BucketStarts, w.BucketEnds, w.AttentionStart).Scan(&raw)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, err
	}
	result.GeneratedAt, result.Timezone, result.Range = w.Now, w.Timezone, w.Range
	result.Attention.Since = w.AttentionStart
	return result, nil
}
func (r *Repository) Tasks(ctx context.Context, projectID string, filter application.TaskFilter) (application.TaskPage, error) {
	if err := filter.Validate(); err != nil {
		return application.TaskPage{}, err
	}
	var raw []byte
	var result application.TaskPage
	err := r.database.QueryRow(platformpostgres.WithOperation(ctx, "overview.tasks"), runsSQL+tasksSQL,
		projectID, filter.State, filter.Scope, filter.Sort, filter.PageSize, (filter.Page-1)*filter.PageSize).Scan(&raw)
	if err != nil {
		return result, err
	}
	err = json.Unmarshal(raw, &result)
	return result, err
}
