package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"fixthe/backend/internal/modules/incidents/adapter/postgres/incidentdb"
	"fixthe/backend/internal/modules/incidents/application"
	"fixthe/backend/internal/modules/incidents/domain"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	incidentIDValue    = "019ff544-405c-7d11-9f10-cb3fc579605c"
	projectIDValue     = "019ff544-405c-7d21-9f10-cb3fc579605c"
	environmentIDValue = "019ff544-405c-7d22-9f10-cb3fc579605c"
	sourceIDValue      = "019ff544-405c-7d23-9f10-cb3fc579605c"
	actorIDValue       = "019ff544-405c-7d10-8f10-cb3fc579605c"
	auditIDValue       = "019ff544-405c-7d12-9f10-cb3fc579605c"
)

type fakeQueries struct {
	scope                                                     incidentdb.GetIncidentSourceScopeRow
	createRow                                                 incidentdb.CreateIncidentRow
	getRow                                                    incidentdb.GetIncidentByNumberRow
	listRows                                                  []incidentdb.ListIncidentsRow
	updateRow                                                 incidentdb.UpdateIncidentStatusRow
	scopeError, createError, getError, listError, updateError error
	createParams                                              incidentdb.CreateIncidentParams
	getParams                                                 incidentdb.GetIncidentByNumberParams
	listParams                                                incidentdb.ListIncidentsParams
	updateParams                                              incidentdb.UpdateIncidentStatusParams
}

func (f *fakeQueries) GetIncidentSourceScope(_ context.Context, params incidentdb.GetIncidentSourceScopeParams) (incidentdb.GetIncidentSourceScopeRow, error) {
	return f.scope, f.scopeError
}
func (f *fakeQueries) CreateIncident(_ context.Context, params incidentdb.CreateIncidentParams) (incidentdb.CreateIncidentRow, error) {
	f.createParams = params
	return f.createRow, f.createError
}
func (f *fakeQueries) GetIncidentByNumber(_ context.Context, params incidentdb.GetIncidentByNumberParams) (incidentdb.GetIncidentByNumberRow, error) {
	f.getParams = params
	return f.getRow, f.getError
}
func (f *fakeQueries) GetIncidentByGlobalNumber(_ context.Context, _ int64) (incidentdb.GetIncidentByGlobalNumberRow, error) {
	return incidentdb.GetIncidentByGlobalNumberRow{}, f.getError
}
func (f *fakeQueries) GetIncidentByID(_ context.Context, _ pgtype.UUID) (incidentdb.GetIncidentByIDRow, error) {
	return incidentdb.GetIncidentByIDRow{}, f.getError
}
func (f *fakeQueries) ListIncidents(_ context.Context, params incidentdb.ListIncidentsParams) ([]incidentdb.ListIncidentsRow, error) {
	f.listParams = params
	return f.listRows, f.listError
}
func (f *fakeQueries) UpdateIncidentStatus(_ context.Context, params incidentdb.UpdateIncidentStatusParams) (incidentdb.UpdateIncidentStatusRow, error) {
	f.updateParams = params
	return f.updateRow, f.updateError
}
func (f *fakeQueries) GetIncidentByFingerprint(_ context.Context, params incidentdb.GetIncidentByFingerprintParams) (incidentdb.GetIncidentByFingerprintRow, error) {
	return incidentdb.GetIncidentByFingerprintRow{}, f.getError
}
func (f *fakeQueries) RecordIncidentOccurrence(_ context.Context, params incidentdb.RecordIncidentOccurrenceParams) (incidentdb.RecordIncidentOccurrenceRow, error) {
	return incidentdb.RecordIncidentOccurrenceRow{}, f.updateError
}

func TestRepositoryCreateCarriesProjectSourceActorAndAudit(t *testing.T) {
	now := time.Date(2026, 8, 13, 1, 2, 3, 456000000, time.FixedZone("offset", 8*60*60))
	queries := &fakeQueries{scope: incidentdb.GetIncidentSourceScopeRow{EnvironmentID: uuidValue(t, environmentIDValue), Source: "mcp"}, createRow: validCreateRow(t, now.UTC())}
	repository := &Repository{queries: queries}
	created, err := repository.Create(context.Background(), validDomainIncident(now), actorIDValue, auditIDValue, nil)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Number != 2049 || created.ProjectID != projectIDValue || created.SourceID != sourceIDValue ||
		queries.createParams.ProjectID != uuidValue(t, projectIDValue) || queries.createParams.SourceID != uuidValue(t, sourceIDValue) ||
		queries.createParams.ActorUserID != uuidValue(t, actorIDValue) || queries.createParams.AuditID != uuidValue(t, auditIDValue) ||
		queries.createParams.Source != "mcp" || queries.createParams.FirstSeen.Time.Location() != time.UTC {
		t.Fatalf("created = %#v, params = %#v", created, queries.createParams)
	}
}

func TestRepositoryScopesReadAndStatusByProject(t *testing.T) {
	now := time.Now().UTC()
	queries := &fakeQueries{getRow: validGetRow(t, now), updateRow: validUpdateRow(t, now)}
	repository := &Repository{queries: queries}
	if _, err := repository.GetByNumber(context.Background(), projectIDValue, 2049); err != nil ||
		queries.getParams.ProjectID != uuidValue(t, projectIDValue) || queries.getParams.IncidentNumber != 2049 {
		t.Fatalf("GetByNumber() params = %#v, error = %v", queries.getParams, err)
	}
	if _, err := repository.UpdateStatus(context.Background(), projectIDValue, 2049, domain.StatusClosed, 1, "abc123", actorIDValue, auditIDValue, nil); err != nil ||
		queries.updateParams.ProjectID != uuidValue(t, projectIDValue) || queries.updateParams.ActorUserID != uuidValue(t, actorIDValue) ||
		queries.updateParams.AuditID != uuidValue(t, auditIDValue) || queries.updateParams.LifecycleGeneration != 1 ||
		queries.updateParams.DeployedCommit != "abc123" {
		t.Fatalf("UpdateStatus() params = %#v, error = %v", queries.updateParams, err)
	}
	queries.listRows = []incidentdb.ListIncidentsRow{validListRow(t, now)}
	if _, err := repository.List(context.Background(), projectIDValue, 25); err != nil ||
		queries.listParams.ProjectID != uuidValue(t, projectIDValue) || queries.listParams.ResultLimit != 25 {
		t.Fatalf("List() params = %#v, error = %v", queries.listParams, err)
	}
}

func TestRepositoryMapsNotFoundConflictAndSafeErrors(t *testing.T) {
	repository := &Repository{queries: &fakeQueries{getError: pgx.ErrNoRows, updateError: pgx.ErrNoRows}}
	if _, err := repository.GetByNumber(context.Background(), projectIDValue, 2049); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("GetByNumber() error = %v", err)
	}
	if _, err := repository.UpdateStatus(context.Background(), projectIDValue, 2049, domain.StatusClosed, 1, "", actorIDValue, auditIDValue, nil); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("UpdateStatus() error = %v", err)
	}

	repository = &Repository{queries: &fakeQueries{scopeError: pgx.ErrNoRows}}
	if _, err := repository.Create(context.Background(), validDomainIncident(time.Now()), actorIDValue, auditIDValue, nil); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("cross-project source Create() error = %v", err)
	}

	postgresError := &pgconn.PgError{Code: "23505", ConstraintName: "incidents_project_fingerprint_unique_idx", Message: "sensitive database detail"}
	queries := &fakeQueries{scope: incidentdb.GetIncidentSourceScopeRow{EnvironmentID: uuidValue(t, environmentIDValue), Source: "cloud"}, createError: postgresError}
	repository = &Repository{queries: queries}
	if _, err := repository.Create(context.Background(), validDomainIncident(time.Now()), actorIDValue, auditIDValue, nil); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("Create() conflict error = %v", err)
	}

	cause := errors.New("sensitive database detail")
	repository = &Repository{queries: &fakeQueries{listError: cause}}
	_, err := repository.List(context.Background(), projectIDValue, 50)
	if !errors.Is(err, cause) || err.Error() == cause.Error() {
		t.Fatalf("List() error = %v", err)
	}
}

func TestRepositoryRejectsCorruptStoredRows(t *testing.T) {
	row := validGetRow(t, time.Now().UTC())
	row.Status = "open"
	repository := &Repository{queries: &fakeQueries{getRow: row}}
	if _, err := repository.GetByNumber(context.Background(), projectIDValue, 2049); err == nil {
		t.Fatal("GetByNumber() error = nil")
	}
	listRow := validListRow(t, time.Now().UTC())
	listRow.ProjectID.Valid = false
	repository = &Repository{queries: &fakeQueries{listRows: []incidentdb.ListIncidentsRow{listRow}}}
	if _, err := repository.List(context.Background(), projectIDValue, 50); err == nil {
		t.Fatal("List() error = nil")
	}
}

func TestUUIDParameterRequiresVersionSeven(t *testing.T) {
	valid, err := uuidParameter(incidentIDValue, "incident")
	if err != nil || !valid.Valid {
		t.Fatalf("uuidParameter(v7) = %#v, %v", valid, err)
	}
	for _, value := range []string{"not-a-uuid", "550e8400-e29b-41d4-a716-446655440000"} {
		if _, err := uuidParameter(value, "incident"); err == nil {
			t.Errorf("uuidParameter(%q) error = nil", value)
		}
	}
}

type rowValues struct {
	id, projectID, environmentID, sourceID pgtype.UUID
	timestamp                              pgtype.Timestamptz
}

func validValues(t *testing.T, now time.Time) rowValues {
	return rowValues{uuidValue(t, incidentIDValue), uuidValue(t, projectIDValue), uuidValue(t, environmentIDValue), uuidValue(t, sourceIDValue), pgtype.Timestamptz{Time: now.UTC(), Valid: true}}
}
func validCreateRow(t *testing.T, now time.Time) incidentdb.CreateIncidentRow {
	v := validValues(t, now)
	return incidentdb.CreateIncidentRow{ID: v.id, ProjectID: v.projectID, EnvironmentID: v.environmentID, SourceID: v.sourceID, IncidentNumber: 2049, Title: "Database latency", Fingerprint: "pg:latency", Status: "Open", Priority: "P2", Source: "mcp", FirstSeen: v.timestamp, LastSeen: v.timestamp, OccurrenceCount: 2, HostCount: 1, NotificationSummary: "Lifecycle default", Version: 1, CreatedAt: v.timestamp, UpdatedAt: v.timestamp}
}
func validGetRow(t *testing.T, now time.Time) incidentdb.GetIncidentByNumberRow {
	v := validValues(t, now)
	return incidentdb.GetIncidentByNumberRow{ID: v.id, ProjectID: v.projectID, EnvironmentID: v.environmentID, SourceID: v.sourceID, IncidentNumber: 2049, Title: "Database latency", Fingerprint: "pg:latency", Status: "Open", Priority: "P2", Source: "mcp", FirstSeen: v.timestamp, LastSeen: v.timestamp, OccurrenceCount: 2, HostCount: 1, NotificationSummary: "Lifecycle default", Version: 1, CreatedAt: v.timestamp, UpdatedAt: v.timestamp}
}
func validListRow(t *testing.T, now time.Time) incidentdb.ListIncidentsRow {
	v := validValues(t, now)
	return incidentdb.ListIncidentsRow{ID: v.id, ProjectID: v.projectID, EnvironmentID: v.environmentID, SourceID: v.sourceID, IncidentNumber: 2049, Title: "Database latency", Fingerprint: "pg:latency", Status: "Open", Priority: "P2", Source: "mcp", FirstSeen: v.timestamp, LastSeen: v.timestamp, OccurrenceCount: 2, HostCount: 1, NotificationSummary: "Lifecycle default", Version: 1, CreatedAt: v.timestamp, UpdatedAt: v.timestamp}
}
func validUpdateRow(t *testing.T, now time.Time) incidentdb.UpdateIncidentStatusRow {
	v := validValues(t, now)
	return incidentdb.UpdateIncidentStatusRow{ID: v.id, ProjectID: v.projectID, EnvironmentID: v.environmentID, SourceID: v.sourceID, IncidentNumber: 2049, Title: "Database latency", Fingerprint: "pg:latency", Status: "Closed", Priority: "P2", Source: "mcp", FirstSeen: v.timestamp, LastSeen: v.timestamp, OccurrenceCount: 2, HostCount: 1, NotificationSummary: "Lifecycle default", Version: 2, CreatedAt: v.timestamp, UpdatedAt: v.timestamp}
}

func validDomainIncident(now time.Time) domain.Incident {
	return domain.Incident{InternalID: incidentIDValue, ProjectID: projectIDValue, EnvironmentID: "repository-resolved", SourceID: sourceIDValue, Title: "Database latency", Fingerprint: "pg:latency", Status: domain.StatusOpen, Priority: domain.PriorityP2, Source: "repository-resolved", FirstSeen: now, LastSeen: now, OccurrenceCount: 2, HostCount: 1, NotificationSummary: "Lifecycle default"}
}
func uuidValue(t *testing.T, value string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := id.Scan(value); err != nil {
		t.Fatalf("parse UUID %q: %v", value, err)
	}
	return id
}
