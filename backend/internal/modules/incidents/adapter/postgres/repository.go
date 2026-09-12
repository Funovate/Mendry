package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"mendry/backend/internal/modules/incidents/adapter/postgres/incidentdb"
	"mendry/backend/internal/modules/incidents/application"
	"mendry/backend/internal/modules/incidents/domain"
	remediationpostgres "mendry/backend/internal/modules/remediation/adapter/postgres"
	remediationdomain "mendry/backend/internal/modules/remediation/domain"
	"mendry/backend/internal/platform/errtrace"
	platformpostgres "mendry/backend/internal/platform/postgres"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type querier interface {
	GetIncidentSourceScope(context.Context, incidentdb.GetIncidentSourceScopeParams) (incidentdb.GetIncidentSourceScopeRow, error)
	CreateIncident(context.Context, incidentdb.CreateIncidentParams) (incidentdb.CreateIncidentRow, error)
	GetIncidentByNumber(context.Context, incidentdb.GetIncidentByNumberParams) (incidentdb.GetIncidentByNumberRow, error)
	GetIncidentByFingerprint(context.Context, incidentdb.GetIncidentByFingerprintParams) (incidentdb.GetIncidentByFingerprintRow, error)
	GetIncidentByGlobalNumber(context.Context, int64) (incidentdb.GetIncidentByGlobalNumberRow, error)
	GetIncidentByID(context.Context, pgtype.UUID) (incidentdb.GetIncidentByIDRow, error)
	ListIncidents(context.Context, incidentdb.ListIncidentsParams) ([]incidentdb.ListIncidentsRow, error)
	RecordIncidentOccurrence(context.Context, incidentdb.RecordIncidentOccurrenceParams) (incidentdb.RecordIncidentOccurrenceRow, error)
	UpdateIncidentStatus(context.Context, incidentdb.UpdateIncidentStatusParams) (incidentdb.UpdateIncidentStatusRow, error)
}

type transactor interface {
	incidentdb.DBTX
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

type Repository struct {
	queries  querier
	database transactor
}

func NewRepository(database transactor) (*Repository, error) {
	if database == nil {
		return nil, fmt.Errorf("incident PostgreSQL database is required")
	}
	return &Repository{queries: incidentdb.New(database), database: database}, nil
}

func (r *Repository) Create(ctx context.Context, incident domain.Incident, remediation *application.RemediationRequest) (domain.Incident, error) {
	incidentID, err := uuidParameter(incident.InternalID, "incident")
	if err != nil {
		return domain.Incident{}, err
	}
	projectID, err := uuidParameter(incident.ProjectID, "project")
	if err != nil {
		return domain.Incident{}, err
	}
	sourceID, err := uuidParameter(incident.SourceID, "project source")
	if err != nil {
		return domain.Incident{}, application.ErrInvalidInput
	}
	if remediation != nil {
		if r.database == nil {
			return domain.Incident{}, fmt.Errorf("incident PostgreSQL transaction database is required")
		}
		tx, err := r.database.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return domain.Incident{}, newRepositoryError("begin incident remediation transaction", err)
		}
		defer tx.Rollback(ctx)

		created, err := r.create(ctx, incidentdb.New(tx), incidentdb.CreateIncidentParams{
			IncidentID: incidentID, ProjectID: projectID, SourceID: sourceID,
			Title: incident.Title, Fingerprint: incident.Fingerprint, Status: string(incident.Status), Priority: string(incident.Priority),
			FirstSeen: timeParameter(incident.FirstSeen), LastSeen: timeParameter(incident.LastSeen),
			OccurrenceCount: incident.OccurrenceCount, HostCount: incident.HostCount, Muted: incident.Muted,
			NotificationSummary: incident.NotificationSummary, LifecycleGeneration: incident.LifecycleGeneration,
			DeployedCommit: incident.DeployedCommit,
		})
		if err != nil {
			return domain.Incident{}, err
		}
		if err := createRemediationRoot(ctx, tx, remediation); err != nil {
			return domain.Incident{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.Incident{}, newRepositoryError("commit incident remediation transaction", err)
		}
		return created, nil
	}
	return r.create(ctx, r.queries, incidentdb.CreateIncidentParams{
		IncidentID: incidentID, ProjectID: projectID, SourceID: sourceID,
		Title: incident.Title, Fingerprint: incident.Fingerprint, Status: string(incident.Status), Priority: string(incident.Priority),
		FirstSeen: timeParameter(incident.FirstSeen), LastSeen: timeParameter(incident.LastSeen),
		OccurrenceCount: incident.OccurrenceCount, HostCount: incident.HostCount, Muted: incident.Muted,
		NotificationSummary: incident.NotificationSummary, LifecycleGeneration: incident.LifecycleGeneration,
		DeployedCommit: incident.DeployedCommit,
	})
}

func (r *Repository) create(ctx context.Context, queries querier, params incidentdb.CreateIncidentParams) (domain.Incident, error) {
	scope, err := queries.GetIncidentSourceScope(platformpostgres.WithOperation(ctx, "incident.source_scope"), incidentdb.GetIncidentSourceScopeParams{
		ProjectID: params.ProjectID, SourceID: params.SourceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Incident{}, application.ErrNotFound
	}
	if err != nil {
		return domain.Incident{}, newRepositoryError("resolve incident source", err)
	}
	params.EnvironmentID = scope.EnvironmentID
	params.Source = scope.Source
	row, err := queries.CreateIncident(platformpostgres.WithOperation(ctx, "incident.create"), params)
	if fingerprintConflict(err) {
		return domain.Incident{}, application.ErrConflict
	}
	if err != nil {
		return domain.Incident{}, newRepositoryError("insert incident", err)
	}
	return mapIncident(incidentRowFromCreate(row))
}

func (r *Repository) GetByGlobalNumber(ctx context.Context, number int64) (domain.Incident, error) {
	row, err := r.queries.GetIncidentByGlobalNumber(platformpostgres.WithOperation(ctx, "incident.get_by_global_number"), number)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Incident{}, application.ErrNotFound
	}
	if err != nil {
		return domain.Incident{}, newRepositoryError("query incident by global number", err)
	}
	return mapIncident(incidentRowFromGlobal(row))
}

// GetByID 按内部 UUIDv7 读取事故，供 remediation 在已持有 IncidentID 时解析项目/来源身份。
func (r *Repository) GetByID(ctx context.Context, incidentID string) (domain.Incident, error) {
	id, err := uuidParameter(incidentID, "incident")
	if err != nil {
		return domain.Incident{}, err
	}
	row, err := r.queries.GetIncidentByID(platformpostgres.WithOperation(ctx, "incident.get_by_id"), id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Incident{}, application.ErrNotFound
	}
	if err != nil {
		return domain.Incident{}, newRepositoryError("query incident by id", err)
	}
	return mapIncident(incidentRowFromGetByID(row))
}

func (r *Repository) GetByNumber(ctx context.Context, projectID string, number int64) (domain.Incident, error) {
	projectUUID, err := uuidParameter(projectID, "project")
	if err != nil {
		return domain.Incident{}, err
	}
	row, err := r.queries.GetIncidentByNumber(platformpostgres.WithOperation(ctx, "incident.get_by_number"), incidentdb.GetIncidentByNumberParams{
		ProjectID: projectUUID, IncidentNumber: number,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Incident{}, application.ErrNotFound
	}
	if err != nil {
		return domain.Incident{}, newRepositoryError("query incident by number", err)
	}
	return mapIncident(incidentRowFromGet(row))
}

func (r *Repository) GetByFingerprint(ctx context.Context, projectID, fingerprint string) (domain.Incident, error) {
	projectUUID, err := uuidParameter(projectID, "project")
	if err != nil {
		return domain.Incident{}, err
	}
	row, err := r.queries.GetIncidentByFingerprint(platformpostgres.WithOperation(ctx, "incident.get_by_fingerprint"), incidentdb.GetIncidentByFingerprintParams{
		ProjectID: projectUUID, Fingerprint: fingerprint,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Incident{}, application.ErrNotFound
	}
	if err != nil {
		return domain.Incident{}, newRepositoryError("query incident by fingerprint", err)
	}
	return mapIncident(incidentRowFromFingerprint(row))
}

func (r *Repository) RecordOccurrence(ctx context.Context, projectID, fingerprint string, lastSeen time.Time) (domain.Incident, error) {
	projectUUID, err := uuidParameter(projectID, "project")
	if err != nil {
		return domain.Incident{}, err
	}
	row, err := r.queries.RecordIncidentOccurrence(platformpostgres.WithOperation(ctx, "incident.record_occurrence"), incidentdb.RecordIncidentOccurrenceParams{
		LastSeen: timeParameter(lastSeen), ProjectID: projectUUID, Fingerprint: fingerprint,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Incident{}, application.ErrNotFound
	}
	if err != nil {
		return domain.Incident{}, newRepositoryError("record incident occurrence", err)
	}
	return mapIncident(incidentRowFromOccurrence(row))
}

func (r *Repository) List(ctx context.Context, projectID string, limit int32) (application.ListResult, error) {
	projectUUID, err := uuidParameter(projectID, "project")
	if err != nil {
		return application.ListResult{}, err
	}
	rows, err := r.queries.ListIncidents(platformpostgres.WithOperation(ctx, "incident.list"), incidentdb.ListIncidentsParams{
		ProjectID: projectUUID, ResultLimit: limit,
	})
	if err != nil {
		return application.ListResult{}, newRepositoryError("list incidents", err)
	}
	incidents := make([]domain.Incident, 0, len(rows))
	var total int64
	for _, row := range rows {
		incident, err := mapIncident(incidentRowFromList(row))
		if err != nil {
			return application.ListResult{}, fmt.Errorf("map listed incident: %w", err)
		}
		incidents = append(incidents, incident)
		total = row.TotalCount
	}
	return application.ListResult{Items: incidents, Total: total}, nil
}

func (r *Repository) UpdateStatus(ctx context.Context, projectID string, number int64, status domain.Status, generation int64, deployedCommit string, remediation *application.RemediationRequest) (domain.Incident, error) {
	projectUUID, err := uuidParameter(projectID, "project")
	if err != nil {
		return domain.Incident{}, err
	}
	params := incidentdb.UpdateIncidentStatusParams{
		Status: string(status), LifecycleGeneration: generation, DeployedCommit: deployedCommit,
		ProjectID: projectUUID, IncidentNumber: number,
	}
	if remediation != nil {
		if r.database == nil {
			return domain.Incident{}, fmt.Errorf("incident PostgreSQL transaction database is required")
		}
		tx, err := r.database.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return domain.Incident{}, newRepositoryError("begin incident remediation transaction", err)
		}
		defer tx.Rollback(ctx)

		updated, err := r.updateStatus(ctx, incidentdb.New(tx), params)
		if err != nil {
			return domain.Incident{}, err
		}
		if err := createRemediationRoot(ctx, tx, remediation); err != nil {
			return domain.Incident{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.Incident{}, newRepositoryError("commit incident remediation transaction", err)
		}
		return updated, nil
	}
	return r.updateStatus(ctx, r.queries, params)
}

func (r *Repository) updateStatus(ctx context.Context, queries querier, params incidentdb.UpdateIncidentStatusParams) (domain.Incident, error) {
	row, err := queries.UpdateIncidentStatus(platformpostgres.WithOperation(ctx, "incident.update_status"), params)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Incident{}, application.ErrNotFound
	}
	if err != nil {
		return domain.Incident{}, newRepositoryError("update incident status", err)
	}
	return mapIncident(incidentRowFromUpdate(row))
}

func createRemediationRoot(ctx context.Context, tx pgx.Tx, request *application.RemediationRequest) error {
	if request == nil {
		return nil
	}
	_, err := remediationpostgres.CreateSeriesAndRunOnDBTX(platformpostgres.WithOperation(ctx, "remediation.root.create"), tx, remediationdomain.NewRun{
		IncidentID:          request.IncidentID,
		LifecycleGeneration: request.LifecycleGeneration,
		DeployedCommit:      request.DeployedCommit,
		Priority:            request.Priority,
		TriggerReason:       request.Reason,
		ContextVersion:      request.ContextVersion,
		AnalysisOnly:        request.AnalysisOnly,
	})
	if err != nil {
		return newRepositoryError("create remediation root", err)
	}
	return nil
}

type incidentRow struct {
	id, projectID, environmentID, sourceID       pgtype.UUID
	number                                       int64
	title, fingerprint, status, priority, source string
	firstSeen, lastSeen                          pgtype.Timestamptz
	occurrenceCount, hostCount                   int64
	muted                                        bool
	notificationSummary                          string
	lifecycleGeneration                          int64
	deployedCommit                               string
	version                                      int64
	createdAt, updatedAt                         pgtype.Timestamptz
}

func mapIncident(row incidentRow) (domain.Incident, error) {
	if !row.id.Valid || !row.projectID.Valid || !row.environmentID.Valid || !row.sourceID.Valid ||
		row.number <= 0 || row.version <= 0 || !row.firstSeen.Valid || !row.lastSeen.Valid || !row.createdAt.Valid || !row.updatedAt.Valid {
		return domain.Incident{}, fmt.Errorf("incident row has invalid generated values")
	}
	status, err := domain.ParseStatus(row.status)
	if err != nil {
		return domain.Incident{}, fmt.Errorf("map incident status: %w", err)
	}
	priority, err := domain.ParsePriority(row.priority)
	if err != nil {
		return domain.Incident{}, fmt.Errorf("map incident priority: %w", err)
	}
	incident := domain.Incident{InternalID: uuidString(row.id), ProjectID: uuidString(row.projectID), EnvironmentID: uuidString(row.environmentID),
		SourceID: uuidString(row.sourceID), Number: row.number, Title: row.title, Fingerprint: row.fingerprint, Status: status,
		Priority: priority, Source: row.source, FirstSeen: row.firstSeen.Time.UTC(), LastSeen: row.lastSeen.Time.UTC(),
		OccurrenceCount: row.occurrenceCount, HostCount: row.hostCount, Muted: row.muted, NotificationSummary: row.notificationSummary,
		LifecycleGeneration: row.lifecycleGeneration, DeployedCommit: row.deployedCommit,
		Version: row.version, CreatedAt: row.createdAt.Time.UTC(), UpdatedAt: row.updatedAt.Time.UTC()}
	if err := incident.Validate(); err != nil {
		return domain.Incident{}, fmt.Errorf("validate incident row: %w", err)
	}
	return incident, nil
}

func incidentRowFromCreate(row incidentdb.CreateIncidentRow) incidentRow {
	return incidentRow{id: row.ID, projectID: row.ProjectID, environmentID: row.EnvironmentID, sourceID: row.SourceID, number: row.IncidentNumber, title: row.Title, fingerprint: row.Fingerprint, status: row.Status, priority: row.Priority, source: row.Source, firstSeen: row.FirstSeen, lastSeen: row.LastSeen, occurrenceCount: row.OccurrenceCount, hostCount: row.HostCount, muted: row.Muted, notificationSummary: row.NotificationSummary, lifecycleGeneration: row.LifecycleGeneration, deployedCommit: row.DeployedCommit, version: row.Version, createdAt: row.CreatedAt, updatedAt: row.UpdatedAt}
}
func incidentRowFromGet(row incidentdb.GetIncidentByNumberRow) incidentRow {
	return incidentRow{id: row.ID, projectID: row.ProjectID, environmentID: row.EnvironmentID, sourceID: row.SourceID, number: row.IncidentNumber, title: row.Title, fingerprint: row.Fingerprint, status: row.Status, priority: row.Priority, source: row.Source, firstSeen: row.FirstSeen, lastSeen: row.LastSeen, occurrenceCount: row.OccurrenceCount, hostCount: row.HostCount, muted: row.Muted, notificationSummary: row.NotificationSummary, lifecycleGeneration: row.LifecycleGeneration, deployedCommit: row.DeployedCommit, version: row.Version, createdAt: row.CreatedAt, updatedAt: row.UpdatedAt}
}
func incidentRowFromGlobal(row incidentdb.GetIncidentByGlobalNumberRow) incidentRow {
	return incidentRow{id: row.ID, projectID: row.ProjectID, environmentID: row.EnvironmentID, sourceID: row.SourceID, number: row.IncidentNumber, title: row.Title, fingerprint: row.Fingerprint, status: row.Status, priority: row.Priority, source: row.Source, firstSeen: row.FirstSeen, lastSeen: row.LastSeen, occurrenceCount: row.OccurrenceCount, hostCount: row.HostCount, muted: row.Muted, notificationSummary: row.NotificationSummary, lifecycleGeneration: row.LifecycleGeneration, deployedCommit: row.DeployedCommit, version: row.Version, createdAt: row.CreatedAt, updatedAt: row.UpdatedAt}
}
func incidentRowFromGetByID(row incidentdb.GetIncidentByIDRow) incidentRow {
	return incidentRow{id: row.ID, projectID: row.ProjectID, environmentID: row.EnvironmentID, sourceID: row.SourceID, number: row.IncidentNumber, title: row.Title, fingerprint: row.Fingerprint, status: row.Status, priority: row.Priority, source: row.Source, firstSeen: row.FirstSeen, lastSeen: row.LastSeen, occurrenceCount: row.OccurrenceCount, hostCount: row.HostCount, muted: row.Muted, notificationSummary: row.NotificationSummary, lifecycleGeneration: row.LifecycleGeneration, deployedCommit: row.DeployedCommit, version: row.Version, createdAt: row.CreatedAt, updatedAt: row.UpdatedAt}
}
func incidentRowFromList(row incidentdb.ListIncidentsRow) incidentRow {
	return incidentRow{id: row.ID, projectID: row.ProjectID, environmentID: row.EnvironmentID, sourceID: row.SourceID, number: row.IncidentNumber, title: row.Title, fingerprint: row.Fingerprint, status: row.Status, priority: row.Priority, source: row.Source, firstSeen: row.FirstSeen, lastSeen: row.LastSeen, occurrenceCount: row.OccurrenceCount, hostCount: row.HostCount, muted: row.Muted, notificationSummary: row.NotificationSummary, lifecycleGeneration: row.LifecycleGeneration, deployedCommit: row.DeployedCommit, version: row.Version, createdAt: row.CreatedAt, updatedAt: row.UpdatedAt}
}
func incidentRowFromUpdate(row incidentdb.UpdateIncidentStatusRow) incidentRow {
	return incidentRow{id: row.ID, projectID: row.ProjectID, environmentID: row.EnvironmentID, sourceID: row.SourceID, number: row.IncidentNumber, title: row.Title, fingerprint: row.Fingerprint, status: row.Status, priority: row.Priority, source: row.Source, firstSeen: row.FirstSeen, lastSeen: row.LastSeen, occurrenceCount: row.OccurrenceCount, hostCount: row.HostCount, muted: row.Muted, notificationSummary: row.NotificationSummary, lifecycleGeneration: row.LifecycleGeneration, deployedCommit: row.DeployedCommit, version: row.Version, createdAt: row.CreatedAt, updatedAt: row.UpdatedAt}
}
func incidentRowFromFingerprint(row incidentdb.GetIncidentByFingerprintRow) incidentRow {
	return incidentRow{id: row.ID, projectID: row.ProjectID, environmentID: row.EnvironmentID, sourceID: row.SourceID, number: row.IncidentNumber, title: row.Title, fingerprint: row.Fingerprint, status: row.Status, priority: row.Priority, source: row.Source, firstSeen: row.FirstSeen, lastSeen: row.LastSeen, occurrenceCount: row.OccurrenceCount, hostCount: row.HostCount, muted: row.Muted, notificationSummary: row.NotificationSummary, lifecycleGeneration: row.LifecycleGeneration, deployedCommit: row.DeployedCommit, version: row.Version, createdAt: row.CreatedAt, updatedAt: row.UpdatedAt}
}
func incidentRowFromOccurrence(row incidentdb.RecordIncidentOccurrenceRow) incidentRow {
	return incidentRow{id: row.ID, projectID: row.ProjectID, environmentID: row.EnvironmentID, sourceID: row.SourceID, number: row.IncidentNumber, title: row.Title, fingerprint: row.Fingerprint, status: row.Status, priority: row.Priority, source: row.Source, firstSeen: row.FirstSeen, lastSeen: row.LastSeen, occurrenceCount: row.OccurrenceCount, hostCount: row.HostCount, muted: row.Muted, notificationSummary: row.NotificationSummary, lifecycleGeneration: row.LifecycleGeneration, deployedCommit: row.DeployedCommit, version: row.Version, createdAt: row.CreatedAt, updatedAt: row.UpdatedAt}
}

func uuidParameter(value, label string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(value)
	if err != nil || parsed.Version() != 7 {
		return pgtype.UUID{}, fmt.Errorf("%s ID must be UUIDv7", label)
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}
func optionalUUIDParameter(value, label string) (pgtype.UUID, error) {
	if value == "" {
		return pgtype.UUID{}, nil
	}
	return uuidParameter(value, label)
}
func uuidString(value pgtype.UUID) string { return uuid.UUID(value.Bytes).String() }
func timeParameter(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: !value.IsZero()}
}
func fingerprintConflict(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.ConstraintName == "incidents_project_fingerprint_unique_idx"
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
