package http_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	authhttp "fixthe/backend/internal/modules/auth/adapter/http"
	authapplication "fixthe/backend/internal/modules/auth/application"
	authdomain "fixthe/backend/internal/modules/auth/domain"
	incidentapplication "fixthe/backend/internal/modules/incidents/application"
	projectapplication "fixthe/backend/internal/modules/projects/application"
	remediationhttp "fixthe/backend/internal/modules/remediation/adapter/http"
	"fixthe/backend/internal/modules/remediation/application"
	"fixthe/backend/internal/modules/remediation/domain"
	"fixthe/backend/internal/platform/httpserver"
)

type mockService struct {
	run             domain.Run
	review          application.Review
	err             error
	continueRun     domain.Run
	continueErr     error
	projectKey      string
	identifier      string
	generation      int64
	expectedRunID   string
	expectedVersion int64
}

func (m *mockService) StartRemediation(_ context.Context, _ authdomain.User, projectKey, identifier string, generation int64) (domain.Run, error) {
	m.projectKey, m.identifier, m.generation = projectKey, identifier, generation
	return m.run, m.err
}

func (m *mockService) ContinueRemediation(_ context.Context, _ authdomain.User, projectKey, identifier string, generation int64, expectedRunID string, expectedVersion int64) (domain.Run, error) {
	m.projectKey, m.identifier, m.generation = projectKey, identifier, generation
	m.expectedRunID, m.expectedVersion = expectedRunID, expectedVersion
	return m.continueRun, m.continueErr
}

func (m *mockService) GetRemediation(_ context.Context, _ authdomain.User, projectKey, identifier string) (application.Review, error) {
	m.projectKey, m.identifier = projectKey, identifier
	return m.review, m.err
}

type fakeAuthService struct {
	user authdomain.User
}

func (*fakeAuthService) Login(context.Context, string, []byte, string) (authapplication.LoginResult, error) {
	return authapplication.LoginResult{}, nil
}

func (f *fakeAuthService) Authenticate(context.Context, string) (authdomain.User, error) {
	if !f.user.Enabled {
		return authdomain.User{}, authapplication.ErrUnauthenticated
	}
	return f.user, nil
}

func (*fakeAuthService) Logout(context.Context, string) error { return nil }

func TestNewHandlerRequiresDependencies(t *testing.T) {
	authHandler, err := authhttp.NewHandler(authhttp.HandlerOptions{Service: &fakeAuthService{}})
	if err != nil {
		t.Fatalf("auth NewHandler() error = %v", err)
	}
	if _, err := remediationhttp.NewHandler(remediationhttp.HandlerOptions{Service: &mockService{}, Authentication: authHandler}); err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	if _, err := remediationhttp.NewHandler(remediationhttp.HandlerOptions{Service: &mockService{}}); err == nil {
		t.Fatal("NewHandler() missing auth error = nil")
	}
}

func TestStartRemediationRequiresAuthenticationAndReturnsRunEnvelope(t *testing.T) {
	service := &mockService{run: domain.Run{
		RunID: "run-1", SeriesID: "series-1", State: domain.RunStateQueued, LifecycleGeneration: 2,
	}}
	authService := &fakeAuthService{}
	handler := newHandler(t, service, authService)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, jsonRequest(nethttp.MethodPost, "/api/v1/projects/payments/incidents/INC-2049/remediation/start", `{"generation":2}`))
	if response.Code != nethttp.StatusUnauthorized || !strings.Contains(response.Body.String(), `"authentication_required"`) {
		t.Fatalf("unauthenticated response = %d %q", response.Code, response.Body.String())
	}

	authService.user = authdomain.User{Enabled: true, Role: authdomain.RoleViewer}
	request := jsonRequest(nethttp.MethodPost, "/api/v1/projects/payments/incidents/INC-2049/remediation/start", `{"generation":2}`)
	request.AddCookie(&nethttp.Cookie{Name: authhttp.SessionCookieName, Value: "valid"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusOK || service.projectKey != "payments" || service.identifier != "INC-2049" || service.generation != 2 {
		t.Fatalf("response = %d %q, service = %#v", response.Code, response.Body.String(), service)
	}
	var body struct {
		Code string `json:"code"`
		Data struct {
			RunID      string          `json:"runId"`
			SeriesID   string          `json:"seriesId"`
			Status     domain.RunState `json:"status"`
			Generation int64           `json:"generation"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil || body.Code != "ok" ||
		body.Data.RunID != "run-1" || body.Data.SeriesID != "series-1" || body.Data.Generation != 2 {
		t.Fatalf("body = %#v, error = %v", body, err)
	}
}

func TestRetryRemediationRequiresAuthenticationAndReturnsNewAttempt(t *testing.T) {
	service := &mockService{continueRun: domain.Run{
		RunID: "run-2", SeriesID: "series-1", State: domain.RunStateQueued,
		LifecycleGeneration: 2, AttemptNumber: 2, Version: 1,
	}}
	authService := &fakeAuthService{}
	handler := newHandler(t, service, authService)

	request := jsonRequest(nethttp.MethodPost, "/api/v1/projects/payments/incidents/INC-2049/remediation/retry", `{"generation":2,"runId":"run-1","version":4}`)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusUnauthorized || !strings.Contains(response.Body.String(), `"authentication_required"`) {
		t.Fatalf("unauthenticated response = %d %q", response.Code, response.Body.String())
	}

	authService.user = authdomain.User{Enabled: true, Role: authdomain.RoleOperator}
	request = jsonRequest(nethttp.MethodPost, "/api/v1/projects/payments/incidents/INC-2049/remediation/retry", `{"generation":2,"runId":"run-1","version":4}`)
	request.AddCookie(&nethttp.Cookie{Name: authhttp.SessionCookieName, Value: "valid"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusOK || service.projectKey != "payments" || service.identifier != "INC-2049" ||
		service.generation != 2 || service.expectedRunID != "run-1" || service.expectedVersion != 4 {
		t.Fatalf("response = %d %q, service = %#v", response.Code, response.Body.String(), service)
	}
	var body struct {
		Code string `json:"code"`
		Data struct {
			RunID         string          `json:"runId"`
			SeriesID      string          `json:"seriesId"`
			Status        domain.RunState `json:"status"`
			Generation    int64           `json:"generation"`
			AttemptNumber int32           `json:"attemptNumber"`
			Version       int64           `json:"version"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil || body.Code != "ok" ||
		body.Data.RunID != "run-2" || body.Data.SeriesID != "series-1" || body.Data.Status != domain.RunStateQueued ||
		body.Data.Generation != 2 || body.Data.AttemptNumber != 2 || body.Data.Version != 1 {
		t.Fatalf("body = %#v, error = %v", body, err)
	}
}

func TestRetryRemediationRejectsStrictJSONAndMapsContinuationErrors(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		code int
		want string
	}{
		{name: "invalid input", err: application.ErrInvalidInput, code: nethttp.StatusBadRequest, want: "invalid_request"},
		{name: "stale predecessor", err: application.ErrConflict, code: nethttp.StatusConflict, want: "remediation_conflict"},
		{name: "active attempt", err: application.ErrActiveAttempt, code: nethttp.StatusConflict, want: "remediation_active"},
		{name: "unsupported state", err: application.ErrUnsupportedContinuation, code: nethttp.StatusConflict, want: "remediation_unsupported"},
		{name: "project forbidden", err: projectapplication.ErrForbidden, code: nethttp.StatusForbidden, want: "forbidden"},
	} {
		handler := newHandler(t, &mockService{continueErr: test.err}, &fakeAuthService{user: authdomain.User{Enabled: true}})
		request := jsonRequest(nethttp.MethodPost, "/api/v1/projects/payments/incidents/INC-2049/remediation/retry", `{"generation":2,"runId":"run-1","version":4}`)
		request.AddCookie(&nethttp.Cookie{Name: authhttp.SessionCookieName, Value: "valid"})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.code || !strings.Contains(response.Body.String(), test.want) {
			t.Errorf("%s response = %d %q", test.name, response.Code, response.Body.String())
		}
	}

	handler := newHandler(t, &mockService{}, &fakeAuthService{user: authdomain.User{Enabled: true}})
	request := jsonRequest(nethttp.MethodPost, "/api/v1/projects/payments/incidents/INC-2049/remediation/retry", `{"generation":2,"runId":"run-1","version":4,"unexpected":true}`)
	request.AddCookie(&nethttp.Cookie{Name: authhttp.SessionCookieName, Value: "valid"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid_request") {
		t.Fatalf("unknown field response = %d %q", response.Code, response.Body.String())
	}
}

func TestStartRemediationRejectsInvalidJSONAndMapsApplicationErrors(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		err  error
		code int
		want string
	}{
		{name: "invalid input", body: `{"generation":0}`, err: application.ErrInvalidInput, code: nethttp.StatusBadRequest, want: "invalid_request"},
		{name: "stale generation", body: `{"generation":1}`, err: application.ErrConflict, code: nethttp.StatusConflict, want: "remediation_conflict"},
		{name: "incident not found", body: `{"generation":1}`, err: incidentapplication.ErrNotFound, code: nethttp.StatusNotFound, want: "incident_not_found"},
		{name: "project forbidden", body: `{"generation":1}`, err: projectapplication.ErrForbidden, code: nethttp.StatusForbidden, want: "forbidden"},
	} {
		service := &mockService{err: test.err}
		handler := newHandler(t, service, &fakeAuthService{user: authdomain.User{Enabled: true}})
		request := jsonRequest(nethttp.MethodPost, "/api/v1/projects/payments/incidents/INC-2049/remediation/start", test.body)
		request.AddCookie(&nethttp.Cookie{Name: authhttp.SessionCookieName, Value: "valid"})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.code || !strings.Contains(response.Body.String(), test.want) {
			t.Fatalf("%s response = %d %q", test.name, response.Code, response.Body.String())
		}
	}

	handler := newHandler(t, &mockService{}, &fakeAuthService{user: authdomain.User{Enabled: true}})
	request := jsonRequest(nethttp.MethodPost, "/api/v1/projects/payments/incidents/INC-2049/remediation/start", `{"generation":`)
	request.AddCookie(&nethttp.Cookie{Name: authhttp.SessionCookieName, Value: "valid"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid_request") {
		t.Fatalf("invalid JSON response = %d %q", response.Code, response.Body.String())
	}
}

func newHandler(t *testing.T, service *mockService, authService *fakeAuthService) nethttp.Handler {
	t.Helper()
	authHandler, err := authhttp.NewHandler(authhttp.HandlerOptions{Service: authService})
	if err != nil {
		t.Fatalf("auth NewHandler() error = %v", err)
	}
	handler, err := remediationhttp.NewHandler(remediationhttp.HandlerOptions{Service: service, Authentication: authHandler})
	if err != nil {
		t.Fatalf("remediation NewHandler() error = %v", err)
	}
	mux := nethttp.NewServeMux()
	handler.Register(mux)
	boundary, err := httpserver.Boundary(httpserver.BoundaryOptions{
		Handler: mux, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), MaxBodyBytes: 1024,
	})
	if err != nil {
		t.Fatalf("Boundary() error = %v", err)
	}
	return boundary
}

func jsonRequest(method, target, body string) *nethttp.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	return request
}

func TestGetRemediationRequiresAuthenticationAndReturnsSecretFreeEnvelope(t *testing.T) {
	service := &mockService{review: application.Review{
		RunID: "run-1", SeriesID: "series-1", Status: domain.RunStateDiagnosisReadyForReview,
		Generation: 2, DeployedCommit: "abc123", AttemptNumber: 1, Version: 3,
		Origin: "automatic", TerminalReason: "", ManualSuggestion: "apply suggested patch", Retryable: false, ContinuationAvailable: false,
		Attempts: []application.ReviewAttempt{{
			ID: "run-1", AttemptNumber: 1, Status: domain.RunStateDiagnosisReadyForReview,
			Origin: "automatic", ContextVersion: 2, Version: 3,
		}},
		SuggestedDiff: "diff --git a/auth/token.go\n+Authorization: Bearer [redacted]\n",
		Risk:          domain.RiskOrdinary,
		Diagnosis: &application.ReviewDiagnosis{
			Fixability: domain.FixabilityCodeFixable, Confidence: 0.9, CausalReasoning: "nil deref after token check",
			EvidenceRefs: []string{"ev-1"}, Contradictions: []string{}, MissingEvidence: []string{},
			RecommendedNextAction: "apply suggested patch",
		},
		Plans: []application.ReviewPlan{{
			PlanID: "p1", IntendedBehavior: "add nil check", Risk: domain.RiskOrdinary,
			Rationale: "simplest", EvidenceRefs: []string{"ev-1"}, AffectedFiles: []string{"auth/token.go"}, Recommended: true,
		}},
	}}
	authService := &fakeAuthService{}
	handler := newHandler(t, service, authService)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(nethttp.MethodGet, "/api/v1/projects/payments/incidents/INC-2049/remediation", nil))
	if response.Code != nethttp.StatusUnauthorized || !strings.Contains(response.Body.String(), `"authentication_required"`) {
		t.Fatalf("unauthenticated GET = %d %q", response.Code, response.Body.String())
	}

	authService.user = authdomain.User{Enabled: true, Role: authdomain.RoleViewer}
	request := httptest.NewRequest(nethttp.MethodGet, "/api/v1/projects/payments/incidents/INC-2049/remediation", nil)
	request.AddCookie(&nethttp.Cookie{Name: authhttp.SessionCookieName, Value: "valid"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusOK || service.projectKey != "payments" || service.identifier != "INC-2049" {
		t.Fatalf("viewer GET = %d %q, service = %#v", response.Code, response.Body.String(), service)
	}
	body := response.Body.String()
	if strings.Contains(body, "sk-") || strings.Contains(body, "prompt") || strings.Contains(body, "providerPayload") {
		t.Fatalf("GET leaked secret-like value: %s", body)
	}
	if !strings.Contains(body, `"status":"diagnosis_ready_for_review"`) ||
		!strings.Contains(body, `"suggestedDiff"`) ||
		!strings.Contains(body, `"plans"`) ||
		!strings.Contains(body, `"auth/token.go"`) ||
		!strings.Contains(body, `"manualSuggestion":"apply suggested patch"`) {
		t.Fatalf("GET body = %s", body)
	}
	if strings.Contains(body, `"toolInvocations"`) || strings.Contains(body, `"content"`) {
		t.Fatalf("GET exposed internal payloads: %s", body)
	}
}

func TestGetRemediationMapsApplicationErrors(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		code int
		want string
	}{
		{name: "not found", err: application.ErrNotFound, code: nethttp.StatusNotFound, want: "remediation_not_found"},
		{name: "project forbidden", err: projectapplication.ErrForbidden, code: nethttp.StatusForbidden, want: "forbidden"},
		{name: "project missing", err: projectapplication.ErrNotFound, code: nethttp.StatusNotFound, want: "project_not_found"},
		{name: "incident missing", err: incidentapplication.ErrNotFound, code: nethttp.StatusNotFound, want: "incident_not_found"},
	} {
		handler := newHandler(t, &mockService{err: test.err}, &fakeAuthService{user: authdomain.User{Enabled: true}})
		request := httptest.NewRequest(nethttp.MethodGet, "/api/v1/projects/payments/incidents/INC-2049/remediation", nil)
		request.AddCookie(&nethttp.Cookie{Name: authhttp.SessionCookieName, Value: "valid"})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.code || !strings.Contains(response.Body.String(), test.want) {
			t.Fatalf("%s GET = %d %q", test.name, response.Code, response.Body.String())
		}
	}
}

func TestGetRemediationOmitsUnknownPayloadFields(t *testing.T) {
	handler := newHandler(t, &mockService{review: application.Review{
		RunID: "run-1", SeriesID: "series-1", Status: domain.RunStateQueued, Generation: 1,
		Diagnosis:     &application.ReviewDiagnosis{CausalReasoning: "safe"},
		SuggestedDiff: "diff --git a/main.go",
	}}, &fakeAuthService{user: authdomain.User{Enabled: true}})
	request := httptest.NewRequest(nethttp.MethodGet, "/api/v1/projects/payments/incidents/INC-2049/remediation", nil)
	request.AddCookie(&nethttp.Cookie{Name: authhttp.SessionCookieName, Value: "valid"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != nethttp.StatusOK {
		t.Fatalf("GET = %d %q", response.Code, body)
	}
	for _, leaked := range []string{`"prompt"`, `"providerPayload"`, `"toolInvocations"`, `"content"`} {
		if strings.Contains(body, leaked) {
			t.Fatalf("GET exposed %s: %s", leaked, body)
		}
	}
}
