package http_test

import (
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	authhttp "fixthe/backend/internal/modules/auth/adapter/http"
	authdomain "fixthe/backend/internal/modules/auth/domain"
	"fixthe/backend/internal/modules/remediation/application"
	"fixthe/backend/internal/modules/remediation/domain"
)

// TestGetRemediationSerializesRecoveryAndCheckpointProjection 验证 HTTP review
// DTO 序列化 additive recovery/checkpoint projection（D8/AC12）：只输出有界
// 安全字段，绝不输出 checkpoint 原始内容、evidence payload 或 recovery hash。
func TestGetRemediationSerializesRecoveryAndCheckpointProjection(t *testing.T) {
	service := &mockService{review: application.Review{
		RunID: "run-1", SeriesID: "series-1", Status: domain.RunStateDiagnosing,
		Generation: 2, AttemptNumber: 1, Version: 7,
		AgentLoopMode: domain.AgentLoopModeResilientV1, AgentLoopPolicyVersion: 3,
		Checkpoint: &application.ReviewCheckpoint{
			Sequence: 5, Phase: "diagnosing", Reason: domain.CheckpointReasonRecovery,
			ObservedRunVersion: 7, UpdatedAt: time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC),
		},
		Recovery: &application.ReviewRecovery{
			Active: true, Kind: string(domain.RecoveryChallengeKindToolFailure),
			Reason: "connector_timeout", Attempt: 1,
			AttemptedPathClasses: []string{"runtime_logs"},
			NextAction:           "inspect the exact deployed source",
			RemainingBudget: &domain.BudgetPlanProjection{
				SchemaVersion: domain.BudgetPlanSchemaVersionV1,
				Phase:         domain.RunStateDiagnosing,
				Consumed:      domain.BudgetAmount{ModelCalls: 1},
				Remaining:     domain.BudgetAmount{ModelCalls: 99},
			},
		},
	}}
	authService := &fakeAuthService{user: authdomain.User{Enabled: true}}
	handler := newHandler(t, service, authService)
	request := httptest.NewRequest(nethttp.MethodGet, "/api/v1/projects/payments/incidents/INC-2049/remediation", nil)
	request.AddCookie(&nethttp.Cookie{Name: authhttp.SessionCookieName, Value: "valid"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusOK {
		t.Fatalf("GET = %d %q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, want := range []string{
		`"agentLoopMode":"resilient_v1"`, `"agentLoopPolicyVersion":3`,
		`"checkpoint":`, `"recovery":`, `"active":true`,
		`"kind":"tool_failure"`, `"reason":"connector_timeout"`, `"attempt":1`,
		`"attemptedPathClasses":["runtime_logs"]`, `"nextAction":"inspect the exact deployed source"`,
		`"remainingBudget":`, `"consumed":`, `"remaining":`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET body missing %s: %s", want, body)
		}
	}
	for _, leaked := range []string{`"content"`, `"providerPayload"`, `"verifiedFacts"`, `"evidenceIndex"`, `"recoveries"`, `"outcomeRef"`} {
		if strings.Contains(body, leaked) {
			t.Fatalf("GET exposed checkpoint internals via %s: %s", leaked, body)
		}
	}
}

// TestGetRemediationLegacyReviewOmitsRecoveryProjections 验证 legacy review 的
// additive projection 字段缺省为空（兼容既有前端/JSON contract）。
func TestGetRemediationLegacyReviewOmitsRecoveryProjections(t *testing.T) {
	service := &mockService{review: application.Review{
		RunID: "run-1", SeriesID: "series-1", Status: domain.RunStateDiagnosisReadyForReview,
		Generation: 1, AttemptNumber: 1, Version: 3, AgentLoopMode: domain.AgentLoopModeLegacy,
	}}
	handler := newHandler(t, service, &fakeAuthService{user: authdomain.User{Enabled: true}})
	request := httptest.NewRequest(nethttp.MethodGet, "/api/v1/projects/payments/incidents/INC-2049/remediation", nil)
	request.AddCookie(&nethttp.Cookie{Name: authhttp.SessionCookieName, Value: "valid"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != nethttp.StatusOK {
		t.Fatalf("GET = %d %q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, `"agentLoopMode":"legacy"`) || !strings.Contains(body, `"agentLoopPolicyVersion":0`) {
		t.Fatalf("legacy mode missing from body: %s", body)
	}
	if strings.Contains(body, `"recovery"`) || strings.Contains(body, `"checkpoint"`) {
		t.Fatalf("legacy review must omit optional projections: %s", body)
	}
	// DTO 可被前端 schema 解码（JSON 结构合法）。
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil || len(envelope.Data) == 0 {
		t.Fatalf("GET body is not a valid envelope: %v", err)
	}
}
