package application_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	authdomain "mendry/backend/internal/modules/auth/domain"
	projectapplication "mendry/backend/internal/modules/projects/application"
	projectdomain "mendry/backend/internal/modules/projects/domain"
	"mendry/backend/internal/modules/remediation/application"
	"mendry/backend/internal/modules/remediation/domain"
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
	identity := f.identity
	if identity.ContextVersion == 0 {
		identity.ContextVersion = 1
	}
	return identity, nil
}

func (f *fakeIncidentLookup) GetByID(context.Context, string) (application.IncidentIdentity, error) {
	if f.err != nil {
		return application.IncidentIdentity{}, f.err
	}
	return f.identity, nil
}

type fakeTriggerStarter struct {
	request     application.TriggerRequest
	next        domain.NextAttempt
	run         domain.Run
	continueRun domain.Run
	err         error
	continueErr error
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

func (f *fakeTriggerStarter) Continue(_ context.Context, input domain.NextAttempt) (domain.Run, error) {
	f.next = input
	if f.continueErr != nil {
		return domain.Run{}, f.continueErr
	}
	return f.continueRun, nil
}

type fakeBackgroundTrigger struct {
	*fakeTriggerStarter
	queueCalls int
}

func (f *fakeBackgroundTrigger) QueueContinuation(_ context.Context, input domain.NextAttempt) (domain.Run, error) {
	f.queueCalls++
	f.next = input
	if f.continueErr != nil {
		return domain.Run{}, f.continueErr
	}
	return f.continueRun, nil
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

func TestContinueRemediationCreatesTheNextAttemptWithOptimisticPredecessor(t *testing.T) {
	lookup := &fakeIncidentLookup{identity: application.IncidentIdentity{
		ID: testIncidentUUID, ProjectID: testProjectID, LifecycleGeneration: 2, DeployedCommit: "abc123", ContextVersion: 9,
	}}
	trigger := &fakeTriggerStarter{continueRun: domain.Run{
		RunID: "run-2", SeriesID: "series-1", IncidentID: testIncidentUUID,
		LifecycleGeneration: 2, DeployedCommit: "abc123", AttemptNumber: 2, State: domain.RunStateQueued,
	}}
	reviews := &fakeReviewQuery{agg: domain.RunAggregate{Run: domain.Run{
		RunID: "run-1", SeriesID: "series-1", IncidentID: testIncidentUUID,
		LifecycleGeneration: 2, DeployedCommit: "abc123", AttemptNumber: 1,
		State: domain.RunStateFailed, ContextVersion: 7, Version: 4,
	}}}
	service := newManualServiceWithReviews(t,
		&fakeProjectAccess{project: projectdomain.Project{ID: testProjectID}},
		lookup, trigger, reviews,
	)

	run, err := service.ContinueRemediation(context.Background(), authdomain.User{ID: "operator"}, "payments", "INC-2049", 2, "run-1", 4)
	if err != nil {
		t.Fatalf("ContinueRemediation() error = %v", err)
	}
	if run.RunID != "run-2" || run.AttemptNumber != 2 {
		t.Fatalf("child run = %#v", run)
	}
	if trigger.next.ContinuationOfRunID != "run-1" || trigger.next.SeriesID != "series-1" ||
		trigger.next.IncidentID != testIncidentUUID || trigger.next.ExpectedPreviousVersion != 4 ||
		trigger.next.ContextVersion != 9 || trigger.next.TriggerReason != domain.TriggerOriginManualContinue ||
		trigger.next.ContinuationReason != "operator requested continuation" {
		t.Fatalf("continuation request = %#v", trigger.next)
	}
}

func TestContinueRemediationUsesBackgroundQueueWhenAvailable(t *testing.T) {
	lookup := &fakeIncidentLookup{identity: application.IncidentIdentity{
		ID: testIncidentUUID, ProjectID: testProjectID, LifecycleGeneration: 2, DeployedCommit: "abc123", ContextVersion: 7,
	}}
	trigger := &fakeBackgroundTrigger{fakeTriggerStarter: &fakeTriggerStarter{continueRun: domain.Run{
		RunID: "run-2", SeriesID: "series-1", IncidentID: testIncidentUUID,
		LifecycleGeneration: 2, DeployedCommit: "abc123", AttemptNumber: 2, State: domain.RunStateQueued,
	}}}
	reviews := &fakeReviewQuery{agg: domain.RunAggregate{Run: domain.Run{
		RunID: "run-1", SeriesID: "series-1", IncidentID: testIncidentUUID,
		LifecycleGeneration: 2, DeployedCommit: "abc123", AttemptNumber: 1,
		State: domain.RunStateFailed, ContextVersion: 7, Version: 4,
	}}}
	service := newManualServiceWithReviews(t,
		&fakeProjectAccess{project: projectdomain.Project{ID: testProjectID}},
		lookup, trigger, reviews,
	)

	if _, err := service.ContinueRemediation(context.Background(), authdomain.User{ID: "operator"}, "payments", "INC-2049", 2, "run-1", 4); err != nil {
		t.Fatalf("ContinueRemediation() error = %v", err)
	}
	if trigger.queueCalls != 1 || trigger.next.ContinuationOfRunID != "run-1" {
		t.Fatalf("background continuation calls/input = %d/%#v", trigger.queueCalls, trigger.next)
	}
}

func TestContinueRemediationEnforcesManualEligibilityAndConcurrency(t *testing.T) {
	for _, test := range []struct {
		name  string
		state domain.RunState
		err   error
	}{
		{name: "failed", state: domain.RunStateFailed},
		{name: "budget exhausted", state: domain.RunStateBudgetExhausted},
		{name: "blocked manual review", state: domain.RunStateBlockedManualReview},
		{name: "ready for review", state: domain.RunStateDiagnosisReadyForReview, err: application.ErrUnsupportedContinuation},
		{name: "completed non code", state: domain.RunStateCompletedNonCode, err: application.ErrUnsupportedContinuation},
		{name: "active", state: domain.RunStateDiagnosing, err: application.ErrActiveAttempt},
	} {
		t.Run(test.name, func(t *testing.T) {
			trigger := &fakeTriggerStarter{continueRun: domain.Run{RunID: "run-2", SeriesID: "series-1", AttemptNumber: 2}}
			service := newManualServiceWithReviews(t,
				&fakeProjectAccess{project: projectdomain.Project{ID: testProjectID}},
				&fakeIncidentLookup{identity: application.IncidentIdentity{
					ID: testIncidentUUID, ProjectID: testProjectID, LifecycleGeneration: 2, DeployedCommit: "abc123",
				}},
				trigger,
				&fakeReviewQuery{agg: domain.RunAggregate{Run: domain.Run{
					RunID: "run-1", SeriesID: "series-1", IncidentID: testIncidentUUID,
					LifecycleGeneration: 2, DeployedCommit: "abc123", AttemptNumber: 1,
					State: test.state, Version: 4,
				}}},
			)
			_, err := service.ContinueRemediation(context.Background(), authdomain.User{ID: "operator"}, "payments", "INC-2049", 2, "run-1", 4)
			if test.err != nil {
				if !errors.Is(err, test.err) {
					t.Fatalf("error = %v, want %v", err, test.err)
				}
				if trigger.next.ContinuationOfRunID != "" {
					t.Fatal("ineligible continuation reached trigger")
				}
				return
			}
			if err != nil {
				t.Fatalf("ContinueRemediation() error = %v", err)
			}
		})
	}

	trigger := &fakeTriggerStarter{continueRun: domain.Run{RunID: "run-3", SeriesID: "series-1", AttemptNumber: 3}}
	service := newManualServiceWithReviews(t,
		&fakeProjectAccess{project: projectdomain.Project{ID: testProjectID}},
		&fakeIncidentLookup{identity: application.IncidentIdentity{
			ID: testIncidentUUID, ProjectID: testProjectID, LifecycleGeneration: 2, DeployedCommit: "abc123",
		}},
		trigger,
		&fakeReviewQuery{agg: domain.RunAggregate{Run: domain.Run{
			RunID: "run-2", SeriesID: "series-1", IncidentID: testIncidentUUID,
			LifecycleGeneration: 2, DeployedCommit: "abc123", AttemptNumber: 2,
			State: domain.RunStateFailed, Version: 5,
		}}},
	)
	if _, err := service.ContinueRemediation(context.Background(), authdomain.User{ID: "operator"}, "payments", "INC-2049", 2, "run-1", 4); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("stale predecessor error = %v", err)
	}
}

func TestContinueRemediationRequiresWriteAccessBeforeIncidentOrRunLookup(t *testing.T) {
	lookup := &fakeIncidentLookup{}
	reviews := &fakeReviewQuery{}
	service := newManualServiceWithReviews(t,
		&fakeProjectAccess{writeErr: projectapplication.ErrForbidden}, lookup,
		&fakeTriggerStarter{}, reviews,
	)
	if _, err := service.ContinueRemediation(context.Background(), authdomain.User{ID: "viewer"}, "payments", "INC-2049", 1, "run-1", 1); !errors.Is(err, projectapplication.ErrForbidden) {
		t.Fatalf("forbidden error = %v", err)
	}
	if lookup.projectID != "" || reviews.agg.Run.RunID != "" {
		t.Fatal("continuation looked up incident or run before project authorization")
	}
}

func TestStartRemediationUsesProjectScopedLookupAndManualTrigger(t *testing.T) {
	lookup := &fakeIncidentLookup{identity: application.IncidentIdentity{
		ID: testIncidentUUID, ProjectID: testProjectID, Priority: "Info",
		LifecycleGeneration: 2, DeployedCommit: "abc123", ContextVersion: 3,
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
		trigger.request.Reason != application.TriggerReasonManual || trigger.request.LifecycleGeneration != 2 ||
		trigger.request.ContextVersion != 3 {
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
		SuggestedDiff: "diff --git a/auth/token.go b/auth/token.go\n+apiKey := os.Getenv(\"APP_TOKEN\")\n+Authorization: Bearer sk-secretvalue\n+value: \"plain-secret\"\n+remote: https://alice:plain-secret@example.test/repo.git\n",
	}}
	projects := &fakeProjectAccess{project: projectdomain.Project{ID: testProjectID}}
	service := newManualServiceWithReviews(t, projects,
		&fakeIncidentLookup{identity: application.IncidentIdentity{
			ID: testIncidentUUID, ProjectID: testProjectID, LifecycleGeneration: 2, DeployedCommit: "abc123",
		}},
		&fakeTriggerStarter{},
		reviews,
	)
	review, err := service.GetRemediation(context.Background(), authdomain.User{ID: "viewer"}, "payments", "INC-2049")
	if err != nil {
		t.Fatalf("GetRemediation() error = %v", err)
	}
	if review.Diagnosis == nil || review.Diagnosis.Fixability != domain.FixabilityCodeFixable {
		t.Fatalf("review = %#v", review)
	}
	if review.ManualSuggestion != "apply the suggested patch" {
		t.Fatalf("manual suggestion = %q", review.ManualSuggestion)
	}
	if !strings.Contains(review.Diagnosis.CausalReasoning, "token") {
		t.Fatalf("safe diagnosis text was dropped: %#v", review.Diagnosis)
	}
	if !strings.Contains(review.SuggestedDiff, "auth/token.go") || !strings.Contains(review.SuggestedDiff, "[redacted]") {
		t.Fatalf("suggested diff = %q", review.SuggestedDiff)
	}
	if strings.Contains(review.SuggestedDiff, "sk-secretvalue") || strings.Contains(review.SuggestedDiff, "plain-secret") || strings.Contains(review.SuggestedDiff, "alice:plain-secret") || strings.Contains(fmt.Sprintf("%#v", review), "sk-secretvalue") {
		t.Fatalf("review leaked secret-like value: %#v", review)
	}
	if len(review.Diagnosis.EvidenceRefs) != 1 || review.Diagnosis.EvidenceRefs[0] != "ev-1" {
		t.Fatalf("evidence refs = %#v", review.Diagnosis.EvidenceRefs)
	}
}

func TestGetRemediationComputesContinuationAvailabilityAndHistory(t *testing.T) {
	now := time.Date(2026, 8, 27, 8, 0, 0, 0, time.UTC)
	reviews := &fakeReviewQuery{agg: domain.RunAggregate{
		Run: domain.Run{
			RunID: "run-2", SeriesID: "series-1", IncidentID: testIncidentUUID,
			LifecycleGeneration: 2, DeployedCommit: "abc123", AttemptNumber: 2,
			State: domain.RunStateFailed, TerminalReason: "provider_timeout", Retryable: true, Origin: domain.TriggerOriginManualContinue, Version: 6,
		},
		AttemptSummaries: []domain.AttemptSummary{
			{ID: "run-1", AttemptNumber: 1, Status: domain.RunStateFailed, Origin: domain.TriggerOriginAutomatic, ContextVersion: 1, TerminalReason: "provider_timeout", Retryable: true, Version: 4, CreatedAt: now, UpdatedAt: now},
			{ID: "run-2", AttemptNumber: 2, Status: domain.RunStateFailed, Origin: domain.TriggerOriginManualContinue, ContextVersion: 2, TerminalReason: "provider_timeout", Retryable: true, Version: 6, CreatedAt: now, UpdatedAt: now},
		},
	}}
	service := newManualServiceWithReviews(t,
		&fakeProjectAccess{project: projectdomain.Project{ID: testProjectID}},
		&fakeIncidentLookup{identity: application.IncidentIdentity{
			ID: testIncidentUUID, ProjectID: testProjectID, LifecycleGeneration: 2, DeployedCommit: "abc123",
		}},
		&fakeTriggerStarter{}, reviews,
	)

	review, err := service.GetRemediation(context.Background(), authdomain.User{ID: "operator"}, "payments", "INC-2049")
	if err != nil {
		t.Fatalf("GetRemediation() error = %v", err)
	}
	if !review.ContinuationAvailable || review.AttemptNumber != 2 || review.Version != 6 ||
		review.Origin != domain.TriggerOriginManualContinue || review.TerminalReason != "provider_timeout" || len(review.Attempts) != 2 {
		t.Fatalf("review metadata = %#v", review)
	}
	if review.Attempts[0].ID != "run-1" || review.Attempts[1].Origin != domain.TriggerOriginManualContinue {
		t.Fatalf("attempt history = %#v", review.Attempts)
	}
}

func TestGetRemediationManualSuggestionIsEmptyWithoutDecisionOrAction(t *testing.T) {
	for _, test := range []struct {
		name      string
		decisions []domain.Decision
	}{
		{name: "no decision"},
		{name: "empty action", decisions: []domain.Decision{{RecommendedNextAction: "   "}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := newManualServiceWithReviews(t,
				&fakeProjectAccess{project: projectdomain.Project{ID: testProjectID}},
				&fakeIncidentLookup{identity: application.IncidentIdentity{
					ID: testIncidentUUID, ProjectID: testProjectID, LifecycleGeneration: 2, DeployedCommit: "abc123",
				}},
				&fakeTriggerStarter{},
				&fakeReviewQuery{agg: domain.RunAggregate{
					Run: domain.Run{
						RunID: "run-1", SeriesID: "series-1", IncidentID: testIncidentUUID,
						LifecycleGeneration: 2, DeployedCommit: "abc123", State: domain.RunStateBlockedManualReview,
					},
					Decisions: test.decisions,
				}},
			)
			review, err := service.GetRemediation(context.Background(), authdomain.User{ID: "viewer"}, "payments", "INC-2049")
			if err != nil {
				t.Fatalf("GetRemediation() error = %v", err)
			}
			if review.ManualSuggestion != "" {
				t.Fatalf("manual suggestion = %q, want empty", review.ManualSuggestion)
			}
		})
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
		AgentLoopMode: domain.AgentLoopModeResilientV1,
		Summary:       "should not appear in metadata",
	})
	if len(meta) != 5 || meta["kind"] != application.NotificationDiagnosisReadyForReview ||
		meta["runId"] != "run-1" || meta["agentLoopMode"] != "resilient_v1" {
		t.Fatalf("metadata = %#v", meta)
	}
	// 未设置 mode 的旧构造回退到 legacy，仍写入白名单键。
	legacyMeta := application.NotificationMetadata(application.TerminalNotification{
		RunID: "run-2", Kind: application.NotificationInsufficientEvidence,
		Fixability: domain.FixabilityInsufficientEvidence, State: domain.RunStateBlockedManualReview,
	})
	if legacyMeta["agentLoopMode"] != "legacy" || len(legacyMeta) != 5 {
		t.Fatalf("legacy metadata = %#v", legacyMeta)
	}
	if _, ok := meta["summary"]; ok {
		t.Fatalf("summary leaked into metadata: %#v", meta)
	}
}
