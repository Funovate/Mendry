package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"mendry/backend/internal/modules/observations/adapter/postgres/observationdb"
	"mendry/backend/internal/modules/observations/application"
	"mendry/backend/internal/modules/observations/domain"
	"mendry/backend/internal/platform/errtrace"
	platformpostgres "mendry/backend/internal/platform/postgres"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type Repository struct{ queries *observationdb.Queries }

func NewRepository(database observationdb.DBTX) (*Repository, error) {
	if database == nil {
		return nil, fmt.Errorf("observation PostgreSQL database is required")
	}
	return &Repository{queries: observationdb.New(database)}, nil
}

func (r *Repository) Create(ctx context.Context, observation domain.Observation) (domain.Observation, error) {
	observationID, err := uuidParameter(observation.ID, "observation")
	if err != nil {
		return domain.Observation{}, err
	}
	projectID, err := uuidParameter(observation.ProjectID, "project")
	if err != nil {
		return domain.Observation{}, err
	}
	sourceID, err := uuidParameter(observation.SourceID, "project source")
	if err != nil {
		return domain.Observation{}, application.ErrInvalidInput
	}
	scope, err := r.queries.GetObservationSourceScope(platformpostgres.WithOperation(ctx, "observation.source_scope"), observationdb.GetObservationSourceScopeParams{
		ProjectID: projectID, SourceID: sourceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Observation{}, application.ErrSourceNotFound
	}
	if err != nil {
		return domain.Observation{}, newRepositoryError("resolve observation source", err)
	}
	service := observation.Service
	if service == nil {
		service = scope.Service
	}
	row, err := r.queries.CreateObservation(platformpostgres.WithOperation(ctx, "observation.create"), observationdb.CreateObservationParams{
		ObservationID: observationID, ProjectID: projectID, EnvironmentID: scope.EnvironmentID, SourceID: sourceID,
		Service: service, OccurredAt: timeParameter(observation.OccurredAt), Level: observation.Level, Message: observation.Message,
		Host: observation.Host, RequestID: observation.RequestID, Fingerprint: observation.Fingerprint, Attributes: observation.Attributes,
	})
	if err != nil {
		return domain.Observation{}, newRepositoryError("insert observation", err)
	}
	return mapObservation(row)
}

func (r *Repository) List(ctx context.Context, projectID string, limit, offset int32) (application.ListResult, error) {
	projectUUID, err := uuidParameter(projectID, "project")
	if err != nil {
		return application.ListResult{}, err
	}
	rows, err := r.queries.ListObservations(platformpostgres.WithOperation(ctx, "observation.list"), observationdb.ListObservationsParams{
		ProjectID: projectUUID, ResultLimit: limit, ResultOffset: offset,
	})
	if err != nil {
		return application.ListResult{}, newRepositoryError("list observations", err)
	}
	observations := make([]domain.Observation, 0, len(rows))
	var total int64
	for _, row := range rows {
		observation, err := mapObservation(observationdb.Observation{ID: row.ID, ProjectID: row.ProjectID, EnvironmentID: row.EnvironmentID,
			SourceID: row.SourceID, Service: row.Service, OccurredAt: row.OccurredAt, Level: row.Level, Message: row.Message,
			Host: row.Host, RequestID: row.RequestID, Fingerprint: row.Fingerprint, Attributes: row.Attributes, IngestedAt: row.IngestedAt})
		if err != nil {
			return application.ListResult{}, fmt.Errorf("map listed observation: %w", err)
		}
		observations = append(observations, observation)
		total = row.TotalCount
	}
	return application.ListResult{Items: observations, Total: total}, nil
}

func mapObservation(row observationdb.Observation) (domain.Observation, error) {
	if !row.ID.Valid || !row.ProjectID.Valid || !row.EnvironmentID.Valid || !row.SourceID.Valid ||
		!row.OccurredAt.Valid || !row.IngestedAt.Valid || !json.Valid(row.Attributes) {
		return domain.Observation{}, fmt.Errorf("observation row has invalid generated values")
	}
	observation := domain.Observation{ID: uuidString(row.ID), ProjectID: uuidString(row.ProjectID),
		EnvironmentID: uuidString(row.EnvironmentID), SourceID: uuidString(row.SourceID), Service: row.Service,
		OccurredAt: row.OccurredAt.Time.UTC(), Level: row.Level, Message: row.Message, Host: row.Host,
		RequestID: row.RequestID, Fingerprint: row.Fingerprint, Attributes: row.Attributes, IngestedAt: row.IngestedAt.Time.UTC()}
	if err := observation.Validate(); err != nil {
		return domain.Observation{}, fmt.Errorf("validate observation row: %w", err)
	}
	return observation, nil
}

func uuidParameter(value, label string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(value)
	if err != nil || parsed.Version() != 7 {
		return pgtype.UUID{}, fmt.Errorf("%s ID must be UUIDv7", label)
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}
func uuidString(value pgtype.UUID) string { return uuid.UUID(value.Bytes).String() }
func timeParameter(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: !value.IsZero()}
}

type repositoryError struct {
	operation string
	cause     error
	stack     errtrace.Trace
}

func newRepositoryError(operation string, cause error) repositoryError {
	return repositoryError{operation: operation, cause: cause, stack: errtrace.Capture(1)}
}

func (e repositoryError) Error() string              { return e.operation + " failed" }
func (e repositoryError) Unwrap() error              { return e.cause }
func (e repositoryError) StackTrace() errtrace.Trace { return e.stack }
