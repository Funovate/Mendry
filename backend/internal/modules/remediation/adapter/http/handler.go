// Package http 实现 remediation 的 HTTP adapter。
package http

import (
	"context"
	"errors"
	"fmt"
	nethttp "net/http"
	"time"

	authhttp "fixthe/backend/internal/modules/auth/adapter/http"
	authdomain "fixthe/backend/internal/modules/auth/domain"
	incidentapplication "fixthe/backend/internal/modules/incidents/application"
	projectapplication "fixthe/backend/internal/modules/projects/application"
	"fixthe/backend/internal/modules/remediation/application"
	"fixthe/backend/internal/modules/remediation/domain"
	"fixthe/backend/internal/platform/httpserver"
)

type service interface {
	StartRemediation(context.Context, authdomain.User, string, string, int64) (domain.Run, error)
	GetRemediation(context.Context, authdomain.User, string, string) (application.Review, error)
}

type continuationService interface {
	ContinueRemediation(context.Context, authdomain.User, string, string, int64, string, int64) (domain.Run, error)
}

type authentication interface {
	RequireAuthentication(nethttp.Handler) nethttp.Handler
}

// HandlerOptions 声明 remediation HTTP adapter 的用例和认证 middleware 依赖。
type HandlerOptions struct {
	Service        service
	Authentication authentication
}

// Handler 负责 remediation HTTP DTO、认证边界和安全错误映射。
type Handler struct {
	service        service
	authentication authentication
}

// NewHandler 在注册 route 前验证 remediation HTTP 依赖。
func NewHandler(options HandlerOptions) (*Handler, error) {
	if options.Service == nil || options.Authentication == nil {
		return nil, fmt.Errorf("remediation HTTP dependencies are required")
	}
	return &Handler{service: options.Service, authentication: options.Authentication}, nil
}

// Register 注册受 Session 保护的 remediation endpoint。
func (h *Handler) Register(mux *nethttp.ServeMux) {
	mux.Handle("POST /api/v1/projects/{projectKey}/incidents/{id}/remediation/start", h.authentication.RequireAuthentication(nethttp.HandlerFunc(h.startRemediation)))
	mux.Handle("POST /api/v1/projects/{projectKey}/incidents/{id}/remediation/retry", h.authentication.RequireAuthentication(nethttp.HandlerFunc(h.retryRemediation)))
	mux.Handle("GET /api/v1/projects/{projectKey}/incidents/{id}/remediation", h.authentication.RequireAuthentication(nethttp.HandlerFunc(h.getRemediation)))
}

type startRemediationRequest struct {
	Generation int64 `json:"generation"`
}

type retryRemediationRequest struct {
	Generation int64  `json:"generation"`
	RunID      string `json:"runId"`
	Version    int64  `json:"version"`
}

type startRemediationResponse struct {
	RunID         string          `json:"runId"`
	SeriesID      string          `json:"seriesId"`
	Status        domain.RunState `json:"status"`
	Generation    int64           `json:"generation"`
	AttemptNumber int32           `json:"attemptNumber"`
	Version       int64           `json:"version"`
}

type reviewDiagnosisResponse struct {
	Fixability            domain.FixabilityClass      `json:"fixability"`
	Confidence            float64                     `json:"confidence"`
	CausalReasoning       string                      `json:"causalReasoning"`
	EvidenceRefs          []string                    `json:"evidenceRefs"`
	Contradictions        []string                    `json:"contradictions"`
	MissingEvidence       []string                    `json:"missingEvidence"`
	RecommendedNextAction string                      `json:"recommendedNextAction"`
	EvidenceAssessment    *evidenceAssessmentResponse `json:"evidenceAssessment,omitempty"`
}

type evidenceAssessmentResponse struct {
	ConfidenceCap       float64                `json:"confidenceCap"`
	EffectiveConfidence float64                `json:"effectiveConfidence"`
	PlanningEligible    bool                   `json:"planningEligible"`
	Outcome             domain.FixabilityClass `json:"outcome"`
	Reasons             []string               `json:"reasons"`
	MissingEvidence     []string               `json:"missingEvidence"`
	Contradictions      []string               `json:"contradictions"`
	DirectEvidenceIDs   []string               `json:"directEvidenceIds"`
}

type reviewPlanResponse struct {
	PlanID           string                    `json:"planId"`
	IntendedBehavior string                    `json:"intendedBehavior"`
	Risk             domain.RiskClassification `json:"risk"`
	Rationale        string                    `json:"rationale"`
	EvidenceRefs     []string                  `json:"evidenceRefs"`
	AffectedFiles    []string                  `json:"affectedFiles"`
	RollbackStrategy string                    `json:"rollbackStrategy,omitempty"`
	Recommended      bool                      `json:"recommended"`
}

type reviewResponse struct {
	RunID                 string                    `json:"runId"`
	SeriesID              string                    `json:"seriesId"`
	Status                domain.RunState           `json:"status"`
	Generation            int64                     `json:"generation"`
	DeployedCommit        string                    `json:"deployedCommit"`
	AttemptNumber         int32                     `json:"attemptNumber"`
	Version               int64                     `json:"version"`
	Origin                string                    `json:"origin"`
	TerminalReason        string                    `json:"terminalReason"`
	ManualSuggestion      string                    `json:"manualSuggestion"`
	Retryable             bool                      `json:"retryable"`
	ContinuationAvailable bool                      `json:"continuationAvailable"`
	Attempts              []reviewAttemptResponse   `json:"attempts"`
	Diagnosis             *reviewDiagnosisResponse  `json:"diagnosis"`
	Plans                 []reviewPlanResponse      `json:"plans"`
	SuggestedDiff         string                    `json:"suggestedDiff"`
	Risk                  domain.RiskClassification `json:"risk"`
}

type reviewAttemptResponse struct {
	ID             string          `json:"id"`
	AttemptNumber  int32           `json:"attemptNumber"`
	Status         domain.RunState `json:"status"`
	Origin         string          `json:"origin"`
	ContextVersion int64           `json:"contextVersion"`
	TerminalReason string          `json:"terminalReason"`
	Retryable      bool            `json:"retryable"`
	Version        int64           `json:"version"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
}

func (h *Handler) startRemediation(writer nethttp.ResponseWriter, request *nethttp.Request) {
	var payload startRemediationRequest
	if decodeError := httpserver.DecodeJSON(request, &payload); decodeError != nil {
		httpserver.WriteError(writer, request, *decodeError)
		return
	}
	principal, ok := authhttp.CurrentUser(request.Context())
	if !ok {
		writeApplicationError(writer, request, projectapplication.ErrForbidden)
		return
	}
	run, err := h.service.StartRemediation(
		request.Context(), principal, request.PathValue("projectKey"), request.PathValue("id"), payload.Generation,
	)
	if err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	if err := httpserver.WriteJSON(writer, nethttp.StatusOK, startRemediationResponse{
		RunID: run.RunID, SeriesID: run.SeriesID, Status: run.State, Generation: run.LifecycleGeneration,
		AttemptNumber: run.AttemptNumber, Version: run.Version,
	}); err != nil {
		httpserver.WriteInternalError(writer, request, err)
	}
}

func (h *Handler) retryRemediation(writer nethttp.ResponseWriter, request *nethttp.Request) {
	var payload retryRemediationRequest
	if decodeError := httpserver.DecodeJSON(request, &payload); decodeError != nil {
		httpserver.WriteError(writer, request, *decodeError)
		return
	}
	principal, ok := authhttp.CurrentUser(request.Context())
	if !ok {
		writeApplicationError(writer, request, projectapplication.ErrForbidden)
		return
	}
	continuer, ok := h.service.(continuationService)
	if !ok {
		httpserver.WriteInternalError(writer, request, fmt.Errorf("remediation continuation service is unavailable"))
		return
	}
	run, err := continuer.ContinueRemediation(
		request.Context(), principal, request.PathValue("projectKey"), request.PathValue("id"),
		payload.Generation, payload.RunID, payload.Version,
	)
	if err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	if err := httpserver.WriteJSON(writer, nethttp.StatusOK, startRemediationResponse{
		RunID: run.RunID, SeriesID: run.SeriesID, Status: run.State,
		Generation: run.LifecycleGeneration, AttemptNumber: run.AttemptNumber, Version: run.Version,
	}); err != nil {
		httpserver.WriteInternalError(writer, request, err)
	}
}

func (h *Handler) getRemediation(writer nethttp.ResponseWriter, request *nethttp.Request) {
	principal, ok := authhttp.CurrentUser(request.Context())
	if !ok {
		writeApplicationError(writer, request, projectapplication.ErrForbidden)
		return
	}
	review, err := h.service.GetRemediation(request.Context(), principal, request.PathValue("projectKey"), request.PathValue("id"))
	if err != nil {
		writeApplicationError(writer, request, err)
		return
	}
	if err := httpserver.WriteJSON(writer, nethttp.StatusOK, mapReview(review)); err != nil {
		httpserver.WriteInternalError(writer, request, err)
	}
}

func mapReview(review application.Review) reviewResponse {
	response := reviewResponse{
		RunID:                 review.RunID,
		SeriesID:              review.SeriesID,
		Status:                review.Status,
		Generation:            review.Generation,
		DeployedCommit:        review.DeployedCommit,
		AttemptNumber:         review.AttemptNumber,
		Version:               review.Version,
		Origin:                review.Origin,
		TerminalReason:        review.TerminalReason,
		ManualSuggestion:      review.ManualSuggestion,
		Retryable:             review.Retryable,
		ContinuationAvailable: review.ContinuationAvailable,
		Attempts:              make([]reviewAttemptResponse, 0, len(review.Attempts)),
		Plans:                 make([]reviewPlanResponse, 0, len(review.Plans)),
		SuggestedDiff:         review.SuggestedDiff,
		Risk:                  review.Risk,
	}
	if review.Diagnosis != nil {
		response.Diagnosis = &reviewDiagnosisResponse{
			Fixability:            review.Diagnosis.Fixability,
			Confidence:            review.Diagnosis.Confidence,
			CausalReasoning:       review.Diagnosis.CausalReasoning,
			EvidenceRefs:          review.Diagnosis.EvidenceRefs,
			Contradictions:        review.Diagnosis.Contradictions,
			MissingEvidence:       review.Diagnosis.MissingEvidence,
			RecommendedNextAction: review.Diagnosis.RecommendedNextAction,
			EvidenceAssessment:    mapEvidenceAssessment(review.Diagnosis.EvidenceAssessment),
		}
	}
	for _, plan := range review.Plans {
		response.Plans = append(response.Plans, reviewPlanResponse{
			PlanID:           plan.PlanID,
			IntendedBehavior: plan.IntendedBehavior,
			Risk:             plan.Risk,
			Rationale:        plan.Rationale,
			EvidenceRefs:     plan.EvidenceRefs,
			AffectedFiles:    plan.AffectedFiles,
			RollbackStrategy: plan.RollbackStrategy,
			Recommended:      plan.Recommended,
		})
	}
	for _, attempt := range review.Attempts {
		response.Attempts = append(response.Attempts, mapReviewAttempt(attempt))
	}
	return response
}

func mapReviewAttempt(attempt application.ReviewAttempt) reviewAttemptResponse {
	return reviewAttemptResponse{
		ID: attempt.ID, AttemptNumber: attempt.AttemptNumber, Status: attempt.Status,
		Origin: attempt.Origin, ContextVersion: attempt.ContextVersion,
		TerminalReason: attempt.TerminalReason, Retryable: attempt.Retryable,
		Version: attempt.Version, CreatedAt: attempt.CreatedAt, UpdatedAt: attempt.UpdatedAt,
	}
}

func mapEvidenceAssessment(value *domain.EvidenceGateDecision) *evidenceAssessmentResponse {
	if value == nil {
		return nil
	}
	return &evidenceAssessmentResponse{
		ConfidenceCap: value.ConfidenceCap, EffectiveConfidence: value.EffectiveConfidence,
		PlanningEligible: value.PlanningEligible, Outcome: value.Outcome,
		Reasons: value.Reasons, MissingEvidence: value.MissingEvidence,
		Contradictions: value.Contradictions, DirectEvidenceIDs: value.DirectEvidenceIDs,
	}
}

func writeApplicationError(writer nethttp.ResponseWriter, request *nethttp.Request, err error) {
	switch {
	case errors.Is(err, application.ErrInvalidInput), errors.Is(err, domain.ErrInvalidNextAttempt):
		httpserver.WriteError(writer, request, httpserver.Error{
			Status: nethttp.StatusBadRequest, Code: "invalid_request", Message: "Remediation request is invalid.",
		})
	case errors.Is(err, application.ErrActiveAttempt), errors.Is(err, domain.ErrActiveAttempt):
		httpserver.WriteError(writer, request, httpserver.Error{
			Status: nethttp.StatusConflict, Code: "remediation_active", Message: "A remediation attempt is already active.",
		})
	case errors.Is(err, application.ErrUnsupportedContinuation), errors.Is(err, domain.ErrUnsupportedState):
		httpserver.WriteError(writer, request, httpserver.Error{
			Status: nethttp.StatusConflict, Code: "remediation_unsupported", Message: "The current remediation result cannot be continued.",
		})
	case errors.Is(err, application.ErrConflict), errors.Is(err, domain.ErrStalePredecessor):
		httpserver.WriteError(writer, request, httpserver.Error{
			Status: nethttp.StatusConflict, Code: "remediation_conflict", Message: "Remediation request conflicts with the current incident.",
		})
	case errors.Is(err, application.ErrNotFound):
		httpserver.WriteError(writer, request, httpserver.Error{
			Status: nethttp.StatusNotFound, Code: "remediation_not_found", Message: "Remediation run was not found.",
		})
	case errors.Is(err, incidentapplication.ErrNotFound):
		httpserver.WriteError(writer, request, httpserver.Error{
			Status: nethttp.StatusNotFound, Code: "incident_not_found", Message: "Incident was not found.",
		})
	case errors.Is(err, projectapplication.ErrNotFound):
		httpserver.WriteError(writer, request, httpserver.Error{
			Status: nethttp.StatusNotFound, Code: "project_not_found", Message: "Project was not found.",
		})
	case errors.Is(err, projectapplication.ErrForbidden):
		httpserver.WriteError(writer, request, httpserver.Error{
			Status: nethttp.StatusForbidden, Code: "forbidden", Message: "You do not have permission to perform this action.",
		})
	default:
		httpserver.WriteInternalError(writer, request, err)
	}
}
