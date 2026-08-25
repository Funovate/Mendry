package application_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	authdomain "fixthe/backend/internal/modules/auth/domain"
	projectapplication "fixthe/backend/internal/modules/projects/application"
	projectdomain "fixthe/backend/internal/modules/projects/domain"
	"fixthe/backend/internal/modules/remediation/application"
	"fixthe/backend/internal/modules/remediation/domain"
)

const testProjectID = "019ff544-405c-7d21-9f10-cb3fc579605c"

type fakeProjectAccess struct {
	project   projectdomain.Project
	writeErr  error
	accessErr error
}

func (f *fakeProjectAccess) ResolveAccess(context.Context, authdomain.User, string) (projectdomain.Project, error) {
	if f.accessErr != nil {
		return projectdomain.Project{}, f.accessErr
	}
	return f.project, nil
}

func (f *fakeProjectAccess) RequireIncidentWrite(context.Context, authdomain.User, string) (projectdomain.Project, error) {
	if f.writeErr != nil {
		return projectdomain.Project{}, f.writeErr
	}
	return f.project, nil
}

type fakeIncidentLookup struct {
	identity  application.IncidentIdentity
	projectID string
	number    int64
	err       error
}

func (f *fakeIncidentLookup) GetByNumber(context.Context, int64) (application.IncidentIdentity, error) {
	return application.IncidentIdentity{}, errors.New("global lookup should not be used")
}

func (f *fakeIncidentLookup) GetByProjectNumber(_ context.Context, projectID string, number int64) (application.IncidentIdentity, error) {
	f.projectID, f.number = projectID, number
	if f.err != nil {
		return application.IncidentIdentity{}, f.err
	}
	return f.identity, nil
}

func (f *fakeIncidentLookup) GetByID(context.Context, string) (application.IncidentIdentity, error) {
	if f.err != nil {
		return application.IncidentIdentity{}, f.err
	}
	return f.identity, nil
}

type fakeTriggerStarter struct {
	request application.TriggerRequest
	run     domain.Run
	err     error
}

func (f *fakeTriggerStarter) Start(_ context.Context, request application.TriggerRequest) (domain.Run, error) {
	f.request = request
	if f.err != nil {
		return domain.Run{}, f.err
	}
	if f.run.RunID == "" {
		f.run = domain.Run{
			RunID: request.IncidentID, SeriesID: "series-1", State: domain.RunStateQueued,
			IncidentID: request.IncidentID, LifecycleGeneration: request.LifecycleGeneration,
			DeployedCommit: request.DeployedCommit,
		}
	}
	return f.run, nil
}

func TestNewServiceRequiresDependencies(t *testing.T) {
	if _, err := application.NewService(application.ServiceOptions{}); err == nil {
		t.Fatal("NewService() error = nil")
	}
	service := newManualService(t, &fakeProjectAccess{project: projectdomain.Project{ID: testProjectID}}, &fakeIncidentLookup{}, &fakeTriggerStarter{})
	if service == nil {
		t.Fatal("NewService() = nil")
	}
}

func TestStartRemediationUsesProjectScopedLookupAndManualTrigger(t *testing.T) {
	lookup := &fakeIncidentLookup{identity: application.IncidentIdentity{
		ID: testIncidentUUID, ProjectID: testProjectID, Priority: "Info",
		LifecycleGeneration: 2, DeployedCommit: "abc123",
	}}
	trigger := &fakeTriggerStarter{}
	service := newManualService(t, &fakeProjectAccess{project: projectdomain.Project{ID: testProjectID}}, lookup, trigger)

	run, err := service.StartRemediation(context.Background(), authdomain.User{ID: "user"}, "payments", "INC-2049", 2)
	if err != nil {
		t.Fatalf("StartRemediation() error = %v", err)
	}
	if lookup.projectID != testProjectID || lookup.number != 2049 {
		t.Fatalf("lookup project=%q number=%d", lookup.projectID, lookup.number)
	}
	if trigger.request.IncidentID != testIncidentUUID || trigger.request.Priority != "Info" ||
		trigger.request.Reason != application.TriggerReasonManual || trigger.request.LifecycleGeneration != 2 {
		t.Fatalf("trigger request = %#v", trigger.request)
	}
	if run.RunID == "" || run.LifecycleGeneration != 2 {
		t.Fatalf("run = %#v", run)
	}
}

func TestStartRemediationRejectsInvalidAndStaleGeneration(t *testing.T) {
	service := newManualService(t,
		&fakeProjectAccess{project: projectdomain.Project{ID: testProjectID}},
		&fakeIncidentLookup{identity: application.IncidentIdentity{ID: testIncidentUUID, LifecycleGeneration: 2}},
		&fakeTriggerStarter{},
	)
	if _, err := service.StartRemediation(context.Background(), authdomain.User{ID: "user"}, "payments", "2049", 2); !errors.Is(err, application.ErrInvalidInput) {
		t.Fatalf("invalid id error = %v", err)
	}
	if _, err := service.StartRemediation(context.Background(), authdomain.User{ID: "user"}, "payments", "INC-2049", 0); !errors.Is(err, application.ErrInvalidInput) {
		t.Fatalf("invalid generation error = %v", err)
	}
	if _, err := service.StartRemediation(context.Background(), authdomain.User{ID: "user"}, "payments", "INC-2049", 1); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("stale generation error = %v", err)
	}
}

func TestStartRemediationRequiresIncidentWrite(t *testing.T) {
	service := newManualService(t,
		&fakeProjectAccess{writeErr: projectapplication.ErrForbidden},
		&fakeIncidentLookup{},
		&fakeTriggerStarter{},
	)
	if _, err := service.StartRemediation(context.Background(), authdomain.User{ID: "user"}, "payments", "INC-2049", 1); !errors.Is(err, projectapplication.ErrForbidden) {
		t.Fatalf("project error = %v", err)
	}
}

func newManualService(t *testing.T, projects application.ProjectAccess, incidents application.IncidentLookup, trigger application.TriggerStarter) *application.Service {
	t.Helper()
	return newManualServiceWithReviews(t, projects, incidents, trigger, &fakeReviewQuery{})
}

func newManualServiceWithReviews(t *testing.T, projects application.ProjectAccess, incidents application.IncidentLookup, trigger application.TriggerStarter, reviews application.ReviewQuery) *application.Service {
	t.Helper()
	service, err := application.NewService(application.ServiceOptions{
		Projects: projects, Incidents: incidents, Trigger: trigger, Reviews: reviews,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

type fakeReviewQuery struct {
	agg domain.RunAggregate
	err error
}

func (f *fakeReviewQuery) Get(context.Context, string) (domain.RunAggregate, error) {
	return f.agg, f.err
}

func (f *fakeReviewQuery) GetLatestForIncident(context.Context, string, int64, string) (domain.RunAggregate, error) {
	if f.err != nil {
		return domain.RunAggregate{}, f.err
	}
	return f.agg, nil
}

func TestGetRemediationAllowsViewerAndStripsSecrets(t *testing.T) {
	reviews := &fakeReviewQuery{agg: domain.RunAggregate{
		Run: domain.Run{
			RunID: "run-1", SeriesID: "series-1", State: domain.RunStateDiagnosisReadyForReview,
			LifecycleGeneration: 2, DeployedCommit: "abc123", AttemptNumber: 1,
		},
		Decisions: []domain.Decision{{
			Fixability: domain.FixabilityCodeFixable, Confidence: 0.9,
			CausalReasoning:   "nil deref after missing token validation",
			EvidenceCitations: []string{"ev-1", "sk-secretvalue"}, Contradictions: []string{"none"},
			MissingEvidence: []string{}, RecommendedNextAction: "apply the suggested patch",
		}},
		Plans: []domain.RepairPlanCandidate{{
			PlanID: "p1", IntendedBehavior: "add nil check", Risk: domain.RiskOrdinary,
			Rationale: "simplest fix", EvidenceRefs: []string{"ev-1"}, AffectedFiles: []string{"main.go"},
			Recommended: true,
		}},
		SuggestedDiff: "diff --git a/auth/token.go b/auth/token.go\n+apiKey := os.Getenv(\"APP_TOKEN\")\n+Authorization: Bearer sk-secretvalue\n",
	}}
	projects := &fakeProjectAccess{project: projectdomain.Project{ID: testProjectID, Role: projectdomain.RoleViewer}}
	service := newManualServiceWithReviews(t, projects,
		&fakeIncidentLookup{identity: application.IncidentIdentity{
			ID: testIncidentUUID, ProjectID: testProjectID, LifecycleGeneration: 2, DeployedCommit: "abc123",
		}},
		&fakeTriggerStarter{},
		reviews,
	)
	review, err := service.GetRemediation(context.Background(), authdomain.User{ID: "viewer", Role: authdomain.RoleViewer}, "payments", "INC-2049")
	if err != nil {
		t.Fatalf("GetRemediation() error = %v", err)
	}
	if review.Diagnosis == nil || review.Diagnosis.Fixability != domain.FixabilityCodeFixable {
		t.Fatalf("review = %#v", review)
	}
	if !strings.Contains(review.Diagnosis.CausalReasoning, "token") {
		t.Fatalf("safe diagnosis text was dropped: %#v", review.Diagnosis)
	}
	if !strings.Contains(review.SuggestedDiff, "auth/token.go") || !strings.Contains(review.SuggestedDiff, "[redacted]") {
		t.Fatalf("suggested diff = %q", review.SuggestedDiff)
	}
	if strings.Contains(review.SuggestedDiff, "sk-secretvalue") || strings.Contains(fmt.Sprintf("%#v", review), "sk-secretvalue") {
		t.Fatalf("review leaked secret-like value: %#v", review)
	}
	if len(review.Diagnosis.EvidenceRefs) != 1 || review.Diagnosis.EvidenceRefs[0] != "ev-1" {
		t.Fatalf("evidence refs = %#v", review.Diagnosis.EvidenceRefs)
	}
}

func TestGetRemediationRequiresProjectReadAccess(t *testing.T) {
	service := newManualServiceWithReviews(t,
		&fakeProjectAccess{accessErr: projectapplication.ErrForbidden},
		&fakeIncidentLookup{},
		&fakeTriggerStarter{},
		&fakeReviewQuery{},
	)
	if _, err := service.GetRemediation(context.Background(), authdomain.User{ID: "outsider"}, "payments", "INC-2049"); !errors.Is(err, projectapplication.ErrForbidden) {
		t.Fatalf("forbidden GET error = %v", err)
	}
}

func TestGetRemediationMasksUnknownProjectAndMissingRun(t *testing.T) {
	service, err := application.NewService(application.ServiceOptions{
		Projects:  &fakeProjectAccess{accessErr: projectapplication.ErrNotFound},
		Incidents: &fakeIncidentLookup{},
		Trigger:   &fakeTriggerStarter{},
		Reviews:   &fakeReviewQuery{},
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if _, err := service.GetRemediation(context.Background(), authdomain.User{ID: "user"}, "payments", "INC-2049"); !errors.Is(err, projectapplication.ErrNotFound) {
		t.Fatalf("unknown project error = %v", err)
	}
	service, err = application.NewService(application.ServiceOptions{
		Projects: &fakeProjectAccess{project: projectdomain.Project{ID: testProjectID}},
		Incidents: &fakeIncidentLookup{identity: application.IncidentIdentity{
			ID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
		}},
		Trigger: &fakeTriggerStarter{},
		Reviews: &fakeReviewQuery{err: application.ErrNotFound},
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if _, err := service.GetRemediation(context.Background(), authdomain.User{ID: "user"}, "payments", "INC-2049"); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("missing run error = %v", err)
	}
	if _, err := service.GetRemediation(context.Background(), authdomain.User{ID: "user"}, "payments", "2049"); !errors.Is(err, application.ErrInvalidInput) {
		t.Fatalf("invalid id error = %v", err)
	}
}

func TestNotificationKindAndMetadataStayAllowlisted(t *testing.T) {
	if got := application.NotificationKindForTerminal(domain.RunStateFailed, ""); got != "" {
		t.Fatalf("failed kind = %q", got)
	}
	if got := application.NotificationKindForTerminal(domain.RunStateBudgetExhausted, ""); got != "" {
		t.Fatalf("budget kind = %q", got)
	}
	meta := application.NotificationMetadata(application.TerminalNotification{
		RunID: "run-1", Kind: application.NotificationDiagnosisReadyForReview,
		Fixability: domain.FixabilityCodeFixable, State: domain.RunStateDiagnosisReadyForReview,
		Summary: "should not appear in metadata",
	})
	if len(meta) != 4 || meta["kind"] != application.NotificationDiagnosisReadyForReview || meta["runId"] != "run-1" {
		t.Fatalf("metadata = %#v", meta)
	}
	if _, ok := meta["summary"]; ok {
		t.Fatalf("summary leaked into metadata: %#v", meta)
	}
}
