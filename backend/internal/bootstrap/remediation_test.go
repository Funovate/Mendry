package bootstrap_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	authhttp "mendry/backend/internal/modules/auth/adapter/http"
	authapplication "mendry/backend/internal/modules/auth/application"
	authdomain "mendry/backend/internal/modules/auth/domain"
	projectdomain "mendry/backend/internal/modules/projects/domain"
	remediationhttp "mendry/backend/internal/modules/remediation/adapter/http"
	"mendry/backend/internal/modules/remediation/application"
	"mendry/backend/internal/modules/remediation/domain"
)

const (
	bootstrapProjectID    = "019ff544-405c-7d21-9f10-cb3fc579605c"
	bootstrapIncidentUUID = "019ff544-405c-7d11-9f10-cb3fc579605c"
)

type fakeManualProjectAccess struct{}

func (fakeManualProjectAccess) ResolveAccess(context.Context, authdomain.User, string) (projectdomain.Project, error) {
	return projectdomain.Project{ID: bootstrapProjectID, Key: "payments"}, nil
}

func (fakeManualProjectAccess) RequireIncidentWrite(context.Context, authdomain.User, string) (projectdomain.Project, error) {
	return projectdomain.Project{ID: bootstrapProjectID, Key: "payments"}, nil
}

type fakeManualIncidentLookup struct{}

func (fakeManualIncidentLookup) GetByNumber(context.Context, int64) (application.IncidentIdentity, error) {
	return application.IncidentIdentity{}, nil
}

func (fakeManualIncidentLookup) GetByProjectNumber(context.Context, string, int64) (application.IncidentIdentity, error) {
	return application.IncidentIdentity{
		ID: bootstrapIncidentUUID, ProjectID: bootstrapProjectID, Priority: "Info",
		LifecycleGeneration: 1, DeployedCommit: "abc123", ContextVersion: 1,
	}, nil
}

func (fakeManualIncidentLookup) GetByID(context.Context, string) (application.IncidentIdentity, error) {
	return application.IncidentIdentity{
		ID: bootstrapIncidentUUID, ProjectID: bootstrapProjectID, Priority: "Info",
		LifecycleGeneration: 1, DeployedCommit: "abc123", ContextVersion: 1,
	}, nil
}

type fakeManualTrigger struct{}

func (fakeManualTrigger) Start(_ context.Context, request application.TriggerRequest) (domain.Run, error) {
	return domain.Run{
		RunID: request.IncidentID, SeriesID: "series-1", IncidentID: request.IncidentID,
		LifecycleGeneration: request.LifecycleGeneration, DeployedCommit: request.DeployedCommit,
		State: domain.RunStateQueued,
	}, nil
}

type fakeManualReviews struct{}

func (fakeManualReviews) Get(context.Context, string) (domain.RunAggregate, error) {
	return domain.RunAggregate{}, application.ErrNotFound
}

func (fakeManualReviews) GetLatestForIncident(context.Context, string, int64, string) (domain.RunAggregate, error) {
	return domain.RunAggregate{
		Run: domain.Run{
			RunID: bootstrapIncidentUUID, SeriesID: "series-1", State: domain.RunStateDiagnosisReadyForReview,
			LifecycleGeneration: 1, DeployedCommit: "abc123",
		},
		Decisions: []domain.Decision{{
			Fixability: domain.FixabilityCodeFixable, Confidence: 0.8, CausalReasoning: "nil deref",
			EvidenceCitations: []string{"ev-1"}, RecommendedNextAction: "apply suggested patch",
		}},
		Plans: []domain.RepairPlanCandidate{{
			PlanID: "p1", IntendedBehavior: "add nil check", Risk: domain.RiskOrdinary,
			Rationale: "simplest", EvidenceRefs: []string{"ev-1"}, AffectedFiles: []string{"main.go"}, Recommended: true,
		}},
		SuggestedDiff: "diff --git a/main.go",
	}, nil
}

type fakeBootstrapAuthService struct{}

func (fakeBootstrapAuthService) Login(context.Context, string, []byte, string) (authapplication.LoginResult, error) {
	return authapplication.LoginResult{}, nil
}

func (fakeBootstrapAuthService) Authenticate(context.Context, string) (authdomain.User, error) {
	return authdomain.User{ID: "019ff544-405c-7d10-8f10-cb3fc579605c", Enabled: true}, nil
}

func (fakeBootstrapAuthService) Logout(context.Context, string) error { return nil }

func newRemediationSmokeService(t *testing.T) *application.Service {
	t.Helper()
	service, err := application.NewService(application.ServiceOptions{
		Projects: fakeManualProjectAccess{}, Incidents: fakeManualIncidentLookup{}, Trigger: fakeManualTrigger{}, Reviews: fakeManualReviews{},
	})
	if err != nil {
		t.Fatalf("create remediation service: %v", err)
	}
	return service
}

func newRemediationSmokeHandler(t *testing.T, service *application.Service) http.Handler {
	t.Helper()
	authHandler, err := authhttp.NewHandler(authhttp.HandlerOptions{Service: fakeBootstrapAuthService{}})
	if err != nil {
		t.Fatalf("create auth handler: %v", err)
	}
	handler, err := remediationhttp.NewHandler(remediationhttp.HandlerOptions{Service: service, Authentication: authHandler})
	if err != nil {
		t.Fatalf("create remediation handler: %v", err)
	}
	mux := http.NewServeMux()
	handler.Register(mux)
	return mux
}

// TestRemediationManualStartSmoke verifies the protected manual-start wiring:
// authenticated HTTP request -> handler -> project-scoped service -> trigger.
func TestRemediationManualStartSmoke(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mux := newRemediationSmokeHandler(t, newRemediationSmokeService(t))
	body, _ := json.Marshal(map[string]int64{"generation": 1})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/payments/incidents/INC-123/remediation/start", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: authhttp.SessionCookieName, Value: "valid"})
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			RunID      string `json:"runId"`
			Status     string `json:"status"`
			Generation int64  `json:"generation"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data.RunID != bootstrapIncidentUUID || resp.Data.Status != string(domain.RunStateQueued) || resp.Data.Generation != 1 {
		t.Fatalf("response = %#v", resp)
	}
}

func TestRemediationReviewGetSmoke(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mux := newRemediationSmokeHandler(t, newRemediationSmokeService(t))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/payments/incidents/INC-123/remediation", nil)
	req.AddCookie(&http.Cookie{Name: authhttp.SessionCookieName, Value: "valid"})
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			Status        string `json:"status"`
			SuggestedDiff string `json:"suggestedDiff"`
			Plans         []struct {
				PlanID string `json:"planId"`
			} `json:"plans"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data.Status != string(domain.RunStateDiagnosisReadyForReview) || resp.Data.SuggestedDiff == "" || len(resp.Data.Plans) != 1 {
		t.Fatalf("response = %#v", resp)
	}
}
