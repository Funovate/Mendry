package application

import (
	"context"
	"errors"
	"testing"
	"time"

	authdomain "mendry/backend/internal/modules/auth/domain"
	"mendry/backend/internal/modules/incidents/domain"
	projectapplication "mendry/backend/internal/modules/projects/application"
	projectdomain "mendry/backend/internal/modules/projects/domain"
)

const (
	testProjectID  = "019ff544-405c-7d21-9f10-cb3fc579605c"
	testCommitA    = "0123456789abcdef0123456789abcdef01234567"
	testCommitB    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testIncidentID = "019ff544-405c-7d11-9f10-cb3fc579605c"
)

type fakeRepository struct {
	incidents   []domain.Incident
	created     domain.Incident
	projectID   string
	number      int64
	fingerprint string
	lastSeen    time.Time
	updated     domain.Status
	generation  int64
	commit      string
	actorID     string
	error       error
	lookupErr   error
	remediation *RemediationRequest
}

func (f *fakeRepository) Create(_ context.Context, incident domain.Incident, actorID, _ string, remediation *RemediationRequest) (domain.Incident, error) {
	f.created, f.projectID, f.actorID = incident, incident.ProjectID, actorID
	f.remediation = remediation
	if f.error != nil {
		return domain.Incident{}, f.error
	}
	incident.Number, incident.Version = 2049, 1
	return incident, nil
}
func (f *fakeRepository) GetByNumber(_ context.Context, projectID string, number int64) (domain.Incident, error) {
	f.projectID, f.number = projectID, number
	if f.error != nil {
		return domain.Incident{}, f.error
	}
	return f.incidents[0], nil
}
func (f *fakeRepository) GetByFingerprint(_ context.Context, projectID, fingerprint string) (domain.Incident, error) {
	f.projectID, f.fingerprint = projectID, fingerprint
	if f.lookupErr != nil {
		return domain.Incident{}, f.lookupErr
	}
	if len(f.incidents) == 0 {
		return domain.Incident{}, ErrNotFound
	}
	return f.incidents[0], nil
}
func (f *fakeRepository) RecordOccurrence(_ context.Context, projectID, fingerprint string, lastSeen time.Time, _ string) (domain.Incident, error) {
	f.projectID, f.fingerprint, f.lastSeen = projectID, fingerprint, lastSeen
	if f.error != nil {
		return domain.Incident{}, f.error
	}
	incident := f.incidents[0]
	incident.LastSeen = lastSeen
	incident.OccurrenceCount++
	incident.Version++
	return incident, nil
}
func (f *fakeRepository) List(_ context.Context, projectID string, _ int32) (ListResult, error) {
	f.projectID = projectID
	return ListResult{Items: f.incidents, Total: int64(len(f.incidents))}, f.error
}
func (f *fakeRepository) UpdateStatus(_ context.Context, projectID string, number int64, status domain.Status, generation int64, commit, actorID, _ string, remediation *RemediationRequest) (domain.Incident, error) {
	f.projectID, f.number, f.updated, f.generation, f.commit, f.actorID = projectID, number, status, generation, commit, actorID
	f.remediation = remediation
	if f.error != nil {
		return domain.Incident{}, f.error
	}
	incident := f.incidents[0]
	incident.Status = status
	incident.LifecycleGeneration = generation
	incident.DeployedCommit = commit
	return incident, nil
}

type fakeProjects struct {
	role  projectdomain.Role
	error error
}

func (f *fakeProjects) ResolveAccess(context.Context, authdomain.User, string) (projectdomain.Project, error) {
	if f.error != nil {
		return projectdomain.Project{}, f.error
	}
	return projectdomain.Project{ID: testProjectID, Key: "payments", Role: f.role}, nil
}
func (f *fakeProjects) RequireIncidentWrite(ctx context.Context, user authdomain.User, key string) (projectdomain.Project, error) {
	project, err := f.ResolveAccess(ctx, user, key)
	if err != nil {
		return projectdomain.Project{}, err
	}
	if !project.CanWriteIncidents() {
		return projectdomain.Project{}, projectapplication.ErrForbidden
	}
	return project, nil
}

type fakeBaseline struct {
	commit string
	err    error
}

func (f *fakeBaseline) DeployedCommit(context.Context, string) (string, error) {
	return f.commit, f.err
}

type fakeTrigger struct {
	calls   []RemediationRequest
	err     error
	started chan struct{}
	release chan struct{}
}

func (f *fakeTrigger) Emit(_ context.Context, req RemediationRequest) error {
	f.calls = append(f.calls, req)
	if f.started != nil {
		f.started <- struct{}{}
	}
	if f.release != nil {
		<-f.release
	}
	return f.err
}

func TestCreateUsesResolvedProjectAndProjectRole(t *testing.T) {
	now := time.Date(2026, 8, 13, 1, 2, 3, 0, time.FixedZone("offset", 8*60*60))
	repository := &fakeRepository{}
	service := newTestService(t, repository, &fakeProjects{role: projectdomain.RoleOperator}, now, nil, nil)
	user := authdomain.User{ID: "019ff544-405c-7d10-8f10-cb3fc579605c", Enabled: true, Role: authdomain.RoleViewer}
	created, err := service.Create(context.Background(), user, "payments", CreateInput{
		Title: " PostgreSQL latency ", Fingerprint: " pg:latency ", SourceID: "019ff544-405c-7d23-9f10-cb3fc579605c",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Number != 2049 || repository.projectID != testProjectID || repository.actorID != user.ID ||
		repository.created.Title != "PostgreSQL latency" || repository.created.Status != domain.StatusOpen ||
		repository.created.Priority != domain.PriorityInfo || repository.created.OccurrenceCount != 1 ||
		repository.created.HostCount != 1 || repository.created.NotificationSummary != "Lifecycle default" ||
		repository.created.FirstSeen.Location() != time.UTC ||
		repository.created.LifecycleGeneration != 1 || repository.created.DeployedCommit != testCommitA {
		t.Fatalf("created = %#v, repository input = %#v", created, repository.created)
	}

	service = newTestService(t, repository, &fakeProjects{role: projectdomain.RoleViewer}, now, nil, nil)
	if _, err := service.Create(context.Background(), user, "payments", CreateInput{}); !errors.Is(err, projectapplication.ErrForbidden) {
		t.Fatalf("project viewer Create() error = %v", err)
	}
}

func TestCreateRejectsInvalidAggregateBeforePersistence(t *testing.T) {
	repository := &fakeRepository{}
	identifierCalls := 0
	service, err := NewService(Options{
		Repository: repository, Projects: &fakeProjects{role: projectdomain.RoleOperator},
		Baseline: &fakeBaseline{commit: testCommitA}, Remediation: &fakeTrigger{},
		NewIncidentID: func() (string, error) { identifierCalls++; return testIncidentID, nil }, Now: time.Now,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	hosts, occurrences := int64(2), int64(1)
	_, err = service.Create(context.Background(), authdomain.User{ID: "user", Enabled: true}, "payments", CreateInput{
		Title: "title", Fingerprint: "fingerprint", SourceID: "source", HostCount: &hosts, OccurrenceCount: &occurrences,
	})
	if !errors.Is(err, ErrInvalidInput) || repository.created.InternalID != "" || identifierCalls != 0 {
		t.Fatalf("error = %v, repository = %#v, identifier calls = %d", err, repository.created, identifierCalls)
	}
}

func TestReadAndStatusUpdateAreProjectScoped(t *testing.T) {
	repository := &fakeRepository{incidents: []domain.Incident{{Number: 2049, Status: domain.StatusOpen, LifecycleGeneration: 1, DeployedCommit: testCommitA}}}
	service := newTestService(t, repository, &fakeProjects{role: projectdomain.RoleOperator}, time.Now(), nil, nil)
	user := authdomain.User{ID: "019ff544-405c-7d10-8f10-cb3fc579605c", Enabled: true}
	if _, err := service.Get(context.Background(), user, "payments", "INC-2049"); err != nil || repository.projectID != testProjectID || repository.number != 2049 {
		t.Fatalf("Get() project = %q number = %d error = %v", repository.projectID, repository.number, err)
	}
	if _, err := service.UpdateStatus(context.Background(), user, "payments", "INC-2049", "Recovered"); err != nil ||
		repository.projectID != testProjectID || repository.updated != domain.StatusRecovered || repository.actorID != user.ID {
		t.Fatalf("UpdateStatus() project = %q status = %q error = %v", repository.projectID, repository.updated, err)
	}
	if _, err := service.UpdateStatus(context.Background(), user, "payments", "2049", "Recovered"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid ID error = %v", err)
	}
	if _, err := service.UpdateStatus(context.Background(), user, "payments", "INC-2049", "open"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid status error = %v", err)
	}
}

func TestListUsesProjectAccessAndNormalizesEmptyResult(t *testing.T) {
	repository := &fakeRepository{}
	service := newTestService(t, repository, &fakeProjects{role: projectdomain.RoleViewer}, time.Now(), nil, nil)
	incidents, err := service.List(context.Background(), authdomain.User{ID: "user", Enabled: true}, "payments", DefaultListLimit)
	if err != nil || incidents.Items == nil || len(incidents.Items) != 0 || incidents.Total != 0 || repository.projectID != testProjectID {
		t.Fatalf("List() = %#v, %v", incidents, err)
	}
	if _, err := service.List(context.Background(), authdomain.User{ID: "user", Enabled: true}, "payments", MaximumListLimit+1); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unbounded List() error = %v", err)
	}
}

func TestProjectAccessAndRepositoryErrorsKeepCategories(t *testing.T) {
	user := authdomain.User{ID: "user", Enabled: true}
	service := newTestService(t, &fakeRepository{}, &fakeProjects{error: projectapplication.ErrNotFound}, time.Now(), nil, nil)
	if _, err := service.Get(context.Background(), user, "hidden", "INC-2049"); !errors.Is(err, projectapplication.ErrNotFound) {
		t.Fatalf("project error = %v", err)
	}
	service = newTestService(t, &fakeRepository{incidents: []domain.Incident{{}}, error: ErrNotFound}, &fakeProjects{role: projectdomain.RoleViewer}, time.Now(), nil, nil)
	if _, err := service.Get(context.Background(), user, "payments", "INC-2049"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("repository error = %v", err)
	}
}

func TestCreateEmitsAutomaticRemediationForP1AndP2Only(t *testing.T) {
	now := time.Now().UTC()
	user := authdomain.User{ID: "user", Enabled: true}
	for _, tc := range []struct {
		priority string
		want     bool
	}{
		{priority: "P2", want: true},
		{priority: "P1", want: true},
		{priority: "Info", want: false},
		{priority: "", want: false},
	} {
		repository := &fakeRepository{}
		trigger := &fakeTrigger{}
		service := newTestService(t, repository, &fakeProjects{role: projectdomain.RoleOperator}, now, &fakeBaseline{commit: testCommitA}, trigger)
		created, err := service.Create(context.Background(), user, "payments", CreateInput{
			Title: "latency", Fingerprint: "pg:" + tc.priority, SourceID: "019ff544-405c-7d23-9f10-cb3fc579605c", Priority: tc.priority,
		})
		if err != nil {
			t.Fatalf("priority %q Create() error = %v", tc.priority, err)
		}
		if created.InternalID != testIncidentID {
			t.Fatalf("priority %q InternalID = %q, want UUID", tc.priority, created.InternalID)
		}
		if tc.want {
			if len(trigger.calls) != 1 {
				t.Fatalf("priority %q emit count = %d, want 1", tc.priority, len(trigger.calls))
			}
			got := trigger.calls[0]
			if got.IncidentID != testIncidentID || got.IncidentID == "2049" || got.LifecycleGeneration != 1 ||
				got.DeployedCommit != testCommitA || got.Reason != RemediationReasonAutomatic {
				t.Fatalf("priority %q emit = %#v", tc.priority, got)
			}
			if repository.remediation == nil || *repository.remediation != got {
				t.Fatalf("priority %q repository remediation = %#v, emit = %#v", tc.priority, repository.remediation, got)
			}
			continue
		}
		if len(trigger.calls) != 0 {
			t.Fatalf("priority %q emit count = %d, want 0", tc.priority, len(trigger.calls))
		}
		if repository.remediation != nil {
			t.Fatalf("priority %q repository remediation = %#v, want nil", tc.priority, repository.remediation)
		}
	}
}

func TestCreateFingerprintConflictDoesNotEmitTwice(t *testing.T) {
	repository := &fakeRepository{error: ErrConflict}
	trigger := &fakeTrigger{}
	service := newTestService(t, repository, &fakeProjects{role: projectdomain.RoleOperator}, time.Now(), nil, trigger)
	_, err := service.Create(context.Background(), authdomain.User{ID: "user", Enabled: true}, "payments", CreateInput{
		Title: "latency", Fingerprint: "pg:latency", SourceID: "019ff544-405c-7d23-9f10-cb3fc579605c", Priority: "P2",
	})
	if !errors.Is(err, ErrConflict) || len(trigger.calls) != 0 {
		t.Fatalf("conflict Create() error = %v, emits = %d", err, len(trigger.calls))
	}
}

func TestUpdateStatusReopenIncrementsGenerationAndEmitsP2(t *testing.T) {
	repository := &fakeRepository{incidents: []domain.Incident{{
		InternalID: testIncidentID, Number: 2049, Status: domain.StatusRecovered, Priority: domain.PriorityP2,
		LifecycleGeneration: 1, DeployedCommit: testCommitA,
	}}}
	trigger := &fakeTrigger{}
	service := newTestService(t, repository, &fakeProjects{role: projectdomain.RoleOperator}, time.Now(), &fakeBaseline{commit: testCommitB}, trigger)
	updated, err := service.UpdateStatus(context.Background(), authdomain.User{ID: "user", Enabled: true}, "payments", "INC-2049", "Open")
	if err != nil {
		t.Fatalf("reopen UpdateStatus() error = %v", err)
	}
	if updated.LifecycleGeneration != 2 || updated.DeployedCommit != testCommitB || repository.generation != 2 || repository.commit != testCommitB {
		t.Fatalf("reopen persisted = %#v, repo gen=%d commit=%q", updated, repository.generation, repository.commit)
	}
	if len(trigger.calls) != 1 {
		t.Fatalf("reopen emit count = %d, want 1", len(trigger.calls))
	}
	if got := trigger.calls[0]; got.IncidentID != testIncidentID || got.LifecycleGeneration != 2 ||
		got.DeployedCommit != testCommitB || got.Reason != RemediationReasonAutomatic {
		t.Fatalf("reopen emit = %#v", got)
	}
	if repository.remediation == nil || *repository.remediation != trigger.calls[0] {
		t.Fatalf("reopen repository remediation = %#v, emit = %#v", repository.remediation, trigger.calls[0])
	}
}

func TestUpdateStatusRecoverDoesNotIncrementOrEmit(t *testing.T) {
	repository := &fakeRepository{incidents: []domain.Incident{{
		InternalID: testIncidentID, Number: 2049, Status: domain.StatusOpen, Priority: domain.PriorityP2,
		LifecycleGeneration: 1, DeployedCommit: testCommitA,
	}}}
	trigger := &fakeTrigger{}
	service := newTestService(t, repository, &fakeProjects{role: projectdomain.RoleOperator}, time.Now(), &fakeBaseline{commit: testCommitB}, trigger)
	updated, err := service.UpdateStatus(context.Background(), authdomain.User{ID: "user", Enabled: true}, "payments", "INC-2049", "Recovered")
	if err != nil {
		t.Fatalf("recover UpdateStatus() error = %v", err)
	}
	if updated.LifecycleGeneration != 1 || updated.DeployedCommit != testCommitA || len(trigger.calls) != 0 {
		t.Fatalf("recover updated = %#v, emits = %d", updated, len(trigger.calls))
	}
}

func TestUpdateStatusReopenInfoIncrementsWithoutEmit(t *testing.T) {
	repository := &fakeRepository{incidents: []domain.Incident{{
		InternalID: testIncidentID, Number: 2049, Status: domain.StatusRecovered, Priority: domain.PriorityInfo,
		LifecycleGeneration: 1, DeployedCommit: testCommitA,
	}}}
	trigger := &fakeTrigger{}
	service := newTestService(t, repository, &fakeProjects{role: projectdomain.RoleOperator}, time.Now(), &fakeBaseline{commit: testCommitA}, trigger)
	updated, err := service.UpdateStatus(context.Background(), authdomain.User{ID: "user", Enabled: true}, "payments", "INC-2049", "Open")
	if err != nil || updated.LifecycleGeneration != 2 || len(trigger.calls) != 0 {
		t.Fatalf("info reopen updated = %#v, err = %v, emits = %d", updated, err, len(trigger.calls))
	}
}

func TestUpdateStatusReopenSameCommitKeepsIncrementedGeneration(t *testing.T) {
	repository := &fakeRepository{incidents: []domain.Incident{{
		InternalID: testIncidentID, Number: 2049, Status: domain.StatusRecovered, Priority: domain.PriorityP2,
		LifecycleGeneration: 1, DeployedCommit: testCommitA,
	}}}
	trigger := &fakeTrigger{}
	service := newTestService(t, repository, &fakeProjects{role: projectdomain.RoleOperator}, time.Now(), &fakeBaseline{commit: testCommitA}, trigger)
	updated, err := service.UpdateStatus(context.Background(), authdomain.User{ID: "user", Enabled: true}, "payments", "INC-2049", "Open")
	if err != nil || updated.LifecycleGeneration != 2 || updated.DeployedCommit != testCommitA || len(trigger.calls) != 1 {
		t.Fatalf("same-commit reopen = %#v, err = %v, emits = %d", updated, err, len(trigger.calls))
	}
}

func newTestService(t *testing.T, repository Repository, projects ProjectAccess, now time.Time, baseline ProjectBaseline, trigger RemediationTrigger) *Service {
	t.Helper()
	if baseline == nil {
		baseline = &fakeBaseline{commit: testCommitA}
	}
	if trigger == nil {
		trigger = &fakeTrigger{}
	}
	ids := []string{testIncidentID, "019ff544-405c-7d12-9f10-cb3fc579605c"}
	index := 0
	service, err := NewService(Options{Repository: repository, Projects: projects, Baseline: baseline, Remediation: trigger,
		NewIncidentID: func() (string, error) { id := ids[index%len(ids)]; index++; return id, nil }, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

func TestIngestInboundPersistsBeforeAsyncRemediation(t *testing.T) {
	now := time.Date(2026, 8, 19, 11, 41, 44, 0, time.UTC)
	repository := &fakeRepository{lookupErr: ErrNotFound}
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	defer close(release)
	trigger := &fakeTrigger{started: started, release: release}
	service := newTestService(t, repository, &fakeProjects{role: projectdomain.RoleOperator}, now, nil, trigger)

	type ingestResult struct {
		incident domain.Incident
		inserted bool
		err      error
	}
	done := make(chan ingestResult, 1)
	go func() {
		incident, inserted, err := service.IngestInbound(context.Background(), testProjectID, "019ff544-405c-7d23-9f10-cb3fc579605c", "【告警】测试信息", "【告警】测试信息", now)
		done <- ingestResult{incident: incident, inserted: inserted, err: err}
	}()

	var result ingestResult
	select {
	case result = <-done:
	case <-time.After(time.Second):
		t.Fatal("IngestInbound waited for asynchronous remediation")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("asynchronous remediation did not start")
	}
	if result.err != nil || !result.inserted || result.incident.Priority != domain.PriorityP2 || result.incident.Status != domain.StatusOpen {
		t.Fatalf("IngestInbound() = %#v", result)
	}
	if repository.actorID != "" || repository.remediation == nil || len(trigger.calls) != 1 || trigger.calls[0].Reason != RemediationReasonAutomatic {
		t.Fatalf("actor=%q remediation=%#v emits=%#v", repository.actorID, repository.remediation, trigger.calls)
	}
}

func TestIngestInboundEvidenceFailurePreservesIncidentWithoutRemediation(t *testing.T) {
	now := time.Date(2026, 8, 19, 11, 41, 44, 0, time.UTC)
	repository := &fakeRepository{lookupErr: ErrNotFound}
	trigger := &fakeTrigger{}
	service := newTestService(t, repository, &fakeProjects{role: projectdomain.RoleOperator}, now, nil, trigger)
	evidenceErr := errors.New("evidence writer unavailable")
	created, inserted, err := service.IngestInboundWithEvidence(
		context.Background(), testProjectID, "019ff544-405c-7d23-9f10-cb3fc579605c",
		"【告警】测试信息", "【告警】测试信息", now,
		func(context.Context, domain.Incident) error { return evidenceErr },
	)
	if !errors.Is(err, evidenceErr) || !inserted || created.InternalID == "" || len(trigger.calls) != 0 {
		t.Fatalf("incident=%#v inserted=%t err=%v emits=%d", created, inserted, err, len(trigger.calls))
	}
	if repository.remediation == nil {
		t.Fatal("incident was not committed with its automatic remediation metadata")
	}
}

func TestIngestInboundAnalysisOnlyPropagatesToEveryAutomaticEmission(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		repository *fakeRepository
	}{
		{name: "new root", repository: &fakeRepository{lookupErr: ErrNotFound}},
		{name: "repeated open alarm", repository: &fakeRepository{incidents: []domain.Incident{{
			InternalID: testIncidentID, Number: 2049, Status: domain.StatusOpen, Priority: domain.PriorityP2,
			LifecycleGeneration: 1, DeployedCommit: testCommitA, OccurrenceCount: 1, Version: 1, LastSeen: now.Add(-time.Hour),
		}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			started := make(chan struct{}, 1)
			trigger := &fakeTrigger{started: started}
			service := newTestService(t, test.repository, &fakeProjects{role: projectdomain.RoleOperator}, now, nil, trigger)
			_, _, err := service.IngestInboundAnalysisOnlyWithEvidence(
				context.Background(), testProjectID, "019ff544-405c-7d23-9f10-cb3fc579605c",
				"AWS CloudWatch alarm", "aws-cloudwatch:v1:fingerprint", now,
				func(context.Context, domain.Incident) error { return nil },
			)
			if err != nil {
				t.Fatalf("IngestInboundAnalysisOnlyWithEvidence() error = %v", err)
			}
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("analysis-only automatic emission did not start")
			}
			if len(trigger.calls) != 1 || !trigger.calls[0].AnalysisOnly {
				t.Fatalf("automatic calls = %#v", trigger.calls)
			}
			if test.repository.remediation != nil && !test.repository.remediation.AnalysisOnly {
				t.Fatalf("transactional remediation = %#v", test.repository.remediation)
			}
		})
	}
}

func TestIngestInboundBumpsOpenWithoutSecondEmit(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	repository := &fakeRepository{incidents: []domain.Incident{{
		InternalID: testIncidentID, Number: 2049, Status: domain.StatusOpen, Priority: domain.PriorityP2,
		OccurrenceCount: 1, LastSeen: now.Add(-time.Hour),
	}}}
	trigger := &fakeTrigger{}
	service := newTestService(t, repository, &fakeProjects{role: projectdomain.RoleOperator}, now, nil, trigger)
	updated, inserted, err := service.IngestInbound(context.Background(), testProjectID, "019ff544-405c-7d23-9f10-cb3fc579605c", "【告警】测试信息", "【告警】测试信息", now)
	if err != nil || inserted || updated.OccurrenceCount != 2 || len(trigger.calls) != 0 {
		t.Fatalf("bump = %#v inserted=%t emits=%d err=%v", updated, inserted, len(trigger.calls), err)
	}
}

func TestIngestInboundWithEvidenceGatesRepeatedOpenAfterEvidence(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	repository := &fakeRepository{incidents: []domain.Incident{{
		InternalID: testIncidentID, Number: 2049, Status: domain.StatusOpen, Priority: domain.PriorityP2,
		OccurrenceCount: 1, Version: 1, LastSeen: now.Add(-time.Hour),
	}}}
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	defer close(release)
	trigger := &fakeTrigger{started: started, release: release}
	service := newTestService(t, repository, &fakeProjects{role: projectdomain.RoleOperator}, now, nil, trigger)
	evidenceDone := make(chan struct{})
	updated, inserted, err := service.IngestInboundWithEvidence(
		context.Background(), testProjectID, "019ff544-405c-7d23-9f10-cb3fc579605c",
		"alert", "fingerprint", now,
		func(context.Context, domain.Incident) error {
			close(evidenceDone)
			return nil
		},
	)
	if err != nil || inserted || updated.OccurrenceCount != 2 || updated.Version != 2 {
		t.Fatalf("updated=%#v inserted=%t err=%v", updated, inserted, err)
	}
	select {
	case <-evidenceDone:
	case <-time.After(time.Second):
		t.Fatal("evidence writer did not run")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("automatic gate did not run after evidence")
	}
	if len(trigger.calls) != 1 || trigger.calls[0].ContextVersion != 2 || trigger.calls[0].Reason != RemediationReasonAutomatic {
		t.Fatalf("automatic gate calls = %#v", trigger.calls)
	}
}

func TestIngestInboundDoesNotReopenClosed(t *testing.T) {
	now := time.Now().UTC()
	repository := &fakeRepository{incidents: []domain.Incident{{
		InternalID: testIncidentID, Number: 2049, Status: domain.StatusClosed, Priority: domain.PriorityP2, OccurrenceCount: 3,
	}}}
	trigger := &fakeTrigger{}
	service := newTestService(t, repository, &fakeProjects{role: projectdomain.RoleOperator}, now, nil, trigger)
	current, inserted, err := service.IngestInbound(context.Background(), testProjectID, "019ff544-405c-7d23-9f10-cb3fc579605c", "closed", "closed", now)
	if err != nil || inserted || current.Status != domain.StatusClosed || current.OccurrenceCount != 3 || len(trigger.calls) != 0 {
		t.Fatalf("closed ingest = %#v inserted=%t emits=%d err=%v", current, inserted, len(trigger.calls), err)
	}
}
