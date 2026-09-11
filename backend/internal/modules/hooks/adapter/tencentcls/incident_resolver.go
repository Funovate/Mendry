package tencentcls

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	hooksapplication "mendry/backend/internal/modules/hooks/application"
	"mendry/backend/internal/modules/remediation/domain"
)

// IncidentDetailResolver 将已校验的 Tencent CLS client 接到 remediation tool 边界。
// 它按 incident identity 读取 callback，不接受 model URL，只持久化受信任的 detail evidence。
type IncidentDetailResolver struct {
	client    *Client
	callbacks domain.CallbackEvidenceLoader
	evidence  domain.EvidencePersistencePort
	now       func() time.Time
}

// NewIncidentDetailResolver 注入 callback loader、CLS client 与 remediation detail tool
// 使用的 evidence writer。
func NewIncidentDetailResolver(
	client *Client,
	callbacks domain.CallbackEvidenceLoader,
	evidence domain.EvidencePersistencePort,
	now func() time.Time,
) (*IncidentDetailResolver, error) {
	if client == nil || callbacks == nil || evidence == nil {
		return nil, fmt.Errorf("Tencent CLS incident detail dependencies are required")
	}
	if now == nil {
		now = time.Now
	}
	return &IncidentDetailResolver{client: client, callbacks: callbacks, evidence: evidence, now: now}, nil
}

var _ domain.TencentCLSDetailPort = (*IncidentDetailResolver)(nil)

// ResolveTencentCLSDetail 获取当前 incident 的已校验 callback，通过 adapter 请求
// provider detail，并只持久化满足 direct-fault trust predicate 的 provider_detail。
func (r *IncidentDetailResolver) ResolveTencentCLSDetail(ctx context.Context, request domain.TencentCLSDetailRequest) (domain.TencentCLSDetailResult, error) {
	if strings.TrimSpace(request.RunID) == "" || strings.TrimSpace(request.IncidentID) == "" ||
		strings.TrimSpace(request.ProjectID) == "" || strings.TrimSpace(request.EnvironmentID) == "" ||
		strings.TrimSpace(request.SourceID) == "" {
		return domain.TencentCLSDetailResult{}, providerDetailError("provider_detail_invalid", false, "Tencent CLS detail scope is invalid")
	}
	snapshot, err := r.callbacks.LoadTencentCLSCallbackSnapshot(ctx, request.IncidentID)
	if err != nil {
		return domain.TencentCLSDetailResult{}, providerDetailError("provider_detail_unavailable", true, "Tencent CLS callback is unavailable for this incident")
	}
	if snapshot.IncidentID != request.IncidentID || snapshot.ProjectID != request.ProjectID ||
		snapshot.EnvironmentID != request.EnvironmentID || snapshot.SourceID != request.SourceID {
		return domain.TencentCLSDetailResult{}, providerDetailError("provider_detail_invalid", false, "Tencent CLS callback scope does not match the run")
	}
	callback, err := hooksapplication.ParseTencentCLSCallback(snapshot.Payload, "application/json")
	if err != nil {
		return domain.TencentCLSDetailResult{}, providerDetailError("provider_detail_invalid", false, "Tencent CLS callback is invalid")
	}
	projection, observation, resolveErr := r.client.ResolveEvidenceObserved(ctx, callback)
	if resolveErr != nil || !projection.Available {
		return domain.TencentCLSDetailResult{}, mapConnectorError(resolveErr, observation)
	}
	if !trustedDirectProjection(projection, observation) {
		return domain.TencentCLSDetailResult{}, providerDetailError("provider_detail_invalid", false, "Tencent CLS detail projection is not trusted direct evidence")
	}
	payload := projection.Payload
	if len(payload) == 0 || len(projection.Provenance) == 0 {
		return domain.TencentCLSDetailResult{}, providerDetailError("provider_detail_invalid", false, "Tencent CLS detail projection is incomplete")
	}
	stored := domain.StoredEvidence{
		ProjectID:              request.ProjectID,
		EnvironmentID:          request.EnvironmentID,
		SourceID:               request.SourceID,
		IncidentID:             request.IncidentID,
		RunID:                  request.RunID,
		ObservationID:          snapshot.ObservationID,
		Provider:               projection.Provider,
		EvidenceKind:           projection.EvidenceKind,
		DeduplicationKey:       projection.DeduplicationKey,
		Classification:         projection.Classification,
		Outcome:                projection.Outcome,
		Available:              projection.Available,
		Primary:                projection.Primary,
		TemporalCorrelation:    projection.TemporalCorrelation,
		OperationalCorrelation: projection.OperationalCorrelation,
		OccurredAt:             optionalTime(snapshot.OccurredAt),
		IngestedAt:             r.now().UTC(),
		ContentHash:            contentHash(payload),
		ByteCount:              projection.ByteCount,
		Provenance:             projection.Provenance,
		Payload:                payload,
	}
	persisted, err := r.evidence.AppendEvidence(ctx, stored)
	if err != nil {
		return domain.TencentCLSDetailResult{}, providerDetailError("provider_detail_persistence", false, "Tencent CLS detail was fetched but could not be persisted")
	}
	return domain.TencentCLSDetailResult{
		Evidence:       persisted,
		BytesRetrieved: observation.BytesRetrieved,
	}, nil
}

func trustedDirectProjection(projection hooksapplication.ProviderDetailEvidence, observation ConnectorObservation) bool {
	if projection.Provider != "tencent_cls" || projection.EvidenceKind != domain.EvidenceKindProviderDetail ||
		projection.Classification != domain.EvidenceDirectFault || projection.Outcome != string(OutcomeSuccess) ||
		!projection.Available || !projection.Primary || len(observation.Contradictions) != 0 {
		return false
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(projection.Payload, &payload) != nil || len(payload) == 0 {
		return false
	}
	var provenance struct {
		Adapter                   string   `json:"adapter"`
		DetailCapabilityValidated bool     `json:"detail_capability_validated"`
		DetailResolution          string   `json:"detail_resolution"`
		Contradictions            []string `json:"contradictions"`
	}
	if json.Unmarshal(projection.Provenance, &provenance) != nil || provenance.Contradictions == nil {
		return false
	}
	return provenance.Adapter == "tencent_cls" && provenance.DetailCapabilityValidated &&
		provenance.DetailResolution == "validated_provider_detail_get_alert_detail" &&
		len(provenance.Contradictions) == 0
}

func mapConnectorError(cause error, observation ConnectorObservation) error {
	code := string(observation.Outcome)
	retryable := true
	message := "Tencent CLS detail is unavailable"
	var connectorErr *ConnectorError
	if errors.As(cause, &connectorErr) && connectorErr != nil {
		code = connectorErr.Code
		retryable = connectorErr.Retryable
	}
	switch code {
	case string(OutcomeRedirectRejected):
		return providerDetailError("provider_detail_redirect_rejected", false, "Tencent CLS detail redirect was rejected by the URL allowlist")
	case string(OutcomeInvalid):
		return providerDetailError("provider_detail_invalid", false, "Tencent CLS detail response was invalid")
	case string(OutcomeOversized):
		return providerDetailError("provider_detail_oversized", false, "Tencent CLS detail response exceeded the size bound")
	case string(OutcomeTimeout):
		return providerDetailError("provider_detail_timeout", true, "Tencent CLS detail request timed out")
	case string(OutcomeUnavailable), "":
		return providerDetailError("provider_detail_unavailable", retryable, message)
	default:
		return providerDetailError("provider_detail_unavailable", retryable, message)
	}
}

func providerDetailError(code string, retryable bool, message string) error {
	return &domain.ToolRuntimeError{Code: code, Retryable: retryable, Message: message}
}

func optionalTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	value = value.UTC()
	return &value
}

func contentHash(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}
