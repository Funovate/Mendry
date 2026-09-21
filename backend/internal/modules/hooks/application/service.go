package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	incidentapplication "mendry/backend/internal/modules/incidents/application"
	incidentdomain "mendry/backend/internal/modules/incidents/domain"
	observationapplication "mendry/backend/internal/modules/observations/application"
	observationdomain "mendry/backend/internal/modules/observations/domain"
	projectapplication "mendry/backend/internal/modules/projects/application"
	projectdomain "mendry/backend/internal/modules/projects/domain"
	remediationdomain "mendry/backend/internal/modules/remediation/domain"
)

var (
	ErrInvalidInput      = errors.New("invalid webhook input")
	ErrWebhookNotFound   = errors.New("webhook not found")
	ErrAWSSNSTooLarge    = errors.New("AWS SNS message is too large")
	ErrInvalidProbeEvent = errors.New("invalid log probe event")
)

// TokenLookup 用路径 token 定位已启用的 signed_webhook 与同项目 source。
type TokenLookup interface {
	LookupWebhookToken(context.Context, string) (projectapplication.WebhookIngress, error)
}

// ObservationWriter 写入公开入站的 Event Stream 行，不要求 Session。
type ObservationWriter interface {
	CreateInbound(ctx context.Context, projectID, sourceID, message, fingerprint string, occurredAt time.Time) (observationdomain.Observation, error)
}

// IncidentIngester 按 fingerprint 打开或更新事故，不要求 Session。
type IncidentIngester interface {
	IngestInbound(context.Context, string, string, string, string, time.Time) (incidentdomain.Incident, bool, error)
}

// inboundEvidenceIngester 允许 hooks 在事故入库后、remediation 异步启动前
// 写入证据；旧的 IncidentIngester 实现仍可走兼容 fallback。
type inboundEvidenceIngester interface {
	IngestInboundWithEvidence(context.Context, string, string, string, string, time.Time, func(context.Context, incidentdomain.Incident) error) (incidentdomain.Incident, bool, error)
}

// analysisOnlyEvidenceIngester 把受信 provider 的执行约束写入 remediation root；
// 该约束必须与 incident/root transaction 一起持久化，不能只存在于 goroutine context。
type analysisOnlyEvidenceIngester interface {
	IngestInboundAnalysisOnlyWithEvidence(context.Context, string, string, string, string, time.Time, func(context.Context, incidentdomain.Incident) error) (incidentdomain.Incident, bool, error)
}

// BackgroundFailure 是已接收 webhook 在后台处理失败时的安全诊断上下文。
// 它不得包含 token、原始 payload 或模型输入。
type BackgroundFailure struct {
	ProjectID  string
	SourceID   string
	OccurredAt time.Time
}

// FailureReporter 记录客户端已收到 202 后发生的后台失败。
type FailureReporter interface {
	Report(context.Context, BackgroundFailure, error)
}

// EvidenceWriter 以不暴露数据库类型的方式持久化项目所属 evidence。
type EvidenceWriter interface {
	AppendEvidence(context.Context, remediationdomain.StoredEvidence) (remediationdomain.StoredEvidence, error)
}

// ProviderDetailEvidence 是 adapter 到 application 的安全投影；adapter 必须先移除 URL capability 及 credential/control 字段。
type ProviderDetailEvidence struct {
	Provider               string
	EvidenceKind           string
	DeduplicationKey       string
	Classification         remediationdomain.EvidenceClassification
	Outcome                string
	Available              bool
	Primary                bool
	TemporalCorrelation    bool
	OperationalCorrelation bool
	OccurredAt             *time.Time
	Payload                json.RawMessage
	Provenance             json.RawMessage
	ByteCount              int64
}

// Options 注入公开 ingress 的查找、归一化、Event Stream 写入、事故入站、失败诊断和可测试时钟。
type Options struct {
	Tokens       TokenLookup
	Analyzer     FingerprintAnalyzer
	Observations ObservationWriter
	Incidents    IncidentIngester
	Failures     FailureReporter
	Evidence     EvidenceWriter
	AWSSNS       AWSSNSVerifier
	Now          func() time.Time
}

// Service 编排未认证 webhook：同步定位 token、校验 provider envelope，
// 后台归一化原文、写 Observation 并入站事故。
type Service struct {
	tokens       TokenLookup
	analyzer     FingerprintAnalyzer
	observations ObservationWriter
	incidents    IncidentIngester
	failures     FailureReporter
	evidence     EvidenceWriter
	awsSNS       AWSSNSVerifier
	now          func() time.Time
}

// NewService 在公开 ingress 可接收请求前验证全部依赖，并在配置 evidence 时锁定 enriched incident boundary。
func NewService(options Options) (*Service, error) {
	if options.Tokens == nil || options.Observations == nil || options.Incidents == nil || options.Now == nil {
		return nil, fmt.Errorf("webhook ingest dependencies are required")
	}
	if options.Evidence != nil {
		if _, ok := options.Incidents.(inboundEvidenceIngester); !ok {
			return nil, fmt.Errorf("webhook evidence requires an inbound evidence ingester")
		}
	}
	analyzer := options.Analyzer
	if analyzer == nil {
		analyzer = NewFingerprintAnalyzer(nil)
	}
	return &Service{
		tokens: options.Tokens, analyzer: analyzer, observations: options.Observations,
		incidents: options.Incidents, failures: options.Failures, evidence: options.Evidence,
		awsSNS: options.AWSSNS, now: options.Now,
	}, nil
}

type ProbeEvent struct {
	SchemaVersion int       `json:"schemaVersion"`
	EventID       string    `json:"eventId"`
	RuleID        string    `json:"ruleId"`
	ConfigVersion int64     `json:"configVersion"`
	MatchCount    int       `json:"matchCount"`
	WindowStarted time.Time `json:"windowStartedAt"`
	TriggeredAt   time.Time `json:"triggeredAt"`
	Host          string    `json:"host"`
	Samples       []string  `json:"samples"`
}

// Ingest 保持 generic webhook 的兼容入口。HTTP 适配器使用
// IngestWithContentType 为 provider-specific callback 提供 Content-Type。
func (s *Service) Ingest(ctx context.Context, token, raw string) error {
	return s.IngestWithContentType(ctx, token, raw, "")
}

// IngestWithContentType 同步验证正文、token 和已配置的 provider envelope；
// analyzer 的模型失败必须自行 fallback，保证 webhook 不因 LLM 不可用而丢失。
func (s *Service) IngestWithContentType(ctx context.Context, token, raw, contentType string) error {
	if err := validateInbound(raw); err != nil {
		return err
	}
	ingress, err := s.tokens.LookupWebhookToken(ctx, token)
	if err != nil {
		return mapLookupError(err)
	}
	if ingress.TriggerKind == "custom_rule" {
		event, err := parseProbeEvent(raw, ingress)
		if err != nil {
			return err
		}
		return s.processProbeEvent(ctx, ingress, event)
	}
	switch ingress.Provider {
	case projectdomain.WebhookProviderAWSCloudWatch, projectdomain.WebhookProviderTencentCLS, projectdomain.WebhookProviderGeneric:
		// Continue through the explicitly configured provider path below.
	default:
		return ErrWebhookNotFound
	}
	analyzerInput := raw
	var callback *TencentCLSCallback
	if ingress.Provider == projectdomain.WebhookProviderAWSCloudWatch {
		if len(raw) > 512*1024 {
			return ErrAWSSNSTooLarge
		}
		if s.awsSNS == nil || strings.TrimSpace(ingress.TopicARN) == "" {
			return ErrWebhookNotFound
		}
		receivedAt := s.now().UTC()
		verified, err := s.awsSNS.Verify(ctx, raw, ingress.TopicARN)
		if err != nil {
			return err
		}
		switch verified.Type {
		case AWSMessageSubscriptionConfirmation, AWSMessageUnsubscribeConfirmation:
			return nil
		case AWSMessageNotification:
			// Continue with the normalized notification below.
		default:
			return ErrInvalidAWSSNS
		}
		if verified.Alarm == nil {
			return ErrInvalidAWSSNS
		}
		failure := BackgroundFailure{ProjectID: ingress.ProjectID, SourceID: ingress.SourceID, OccurredAt: receivedAt}
		background := context.WithoutCancel(ctx)
		go func() {
			if err := s.processAWSInbound(background, ingress, verified, failure.OccurredAt); err != nil && s.failures != nil {
				s.failures.Report(background, failure, err)
			}
		}()
		return nil
	}
	if ingress.Provider == projectdomain.WebhookProviderTencentCLS {
		parsed, err := ParseTencentCLSCallback(raw, contentType)
		if err != nil {
			return ErrInvalidTencentCallback
		}
		callback = &parsed
		analyzerInput = string(parsed.SemanticPayload())
	}
	failure := BackgroundFailure{ProjectID: ingress.ProjectID, SourceID: ingress.SourceID, OccurredAt: s.now().UTC()}
	background := context.WithoutCancel(ctx)
	go func() {
		if err := s.processInbound(background, ingress, raw, analyzerInput, callback, failure.OccurredAt); err != nil && s.failures != nil {
			s.failures.Report(background, failure, err)
		}
	}()
	return nil
}

func parseProbeEvent(raw string, ingress projectapplication.WebhookIngress) (ProbeEvent, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var event ProbeEvent
	if err := decoder.Decode(&event); err != nil {
		return ProbeEvent{}, ErrInvalidProbeEvent
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) || event.SchemaVersion != 1 ||
		len(event.EventID) != 64 || event.ConfigVersion != ingress.TriggerVersion || event.MatchCount < 1 ||
		event.MatchCount > 100000 || event.TriggeredAt.IsZero() || event.WindowStarted.IsZero() ||
		event.WindowStarted.After(event.TriggeredAt) || len(strings.TrimSpace(event.Host)) > 255 ||
		len(event.Samples) < 1 || len(event.Samples) > 20 {
		return ProbeEvent{}, ErrInvalidProbeEvent
	}
	for _, char := range event.EventID {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return ProbeEvent{}, ErrInvalidProbeEvent
		}
	}
	knownRule := false
	for _, rule := range ingress.CustomRules.Rules {
		if rule.ID == event.RuleID {
			knownRule = true
			break
		}
	}
	if !knownRule {
		return ProbeEvent{}, ErrInvalidProbeEvent
	}
	for _, sample := range event.Samples {
		if len(strings.TrimSpace(sample)) == 0 || len(sample) > 4096 {
			return ProbeEvent{}, ErrInvalidProbeEvent
		}
	}
	return event, nil
}

func (s *Service) processProbeEvent(ctx context.Context, ingress projectapplication.WebhookIngress, event ProbeEvent) error {
	var rule projectdomain.CustomRule
	for _, candidate := range ingress.CustomRules.Rules {
		if candidate.ID == event.RuleID {
			rule = candidate
			break
		}
	}
	payload, err := json.Marshal(map[string]any{
		"schemaVersion": 1, "provider": "mendry_log_probe", "eventId": event.EventID,
		"ruleId": event.RuleID, "ruleName": rule.Name, "matchCount": event.MatchCount,
		"windowStartedAt": event.WindowStarted.UTC().Format(time.RFC3339Nano),
		"triggeredAt":     event.TriggeredAt.UTC().Format(time.RFC3339Nano), "host": event.Host,
		"samples": event.Samples,
	})
	if err != nil || len(payload) > maxObservationMessage {
		return ErrInvalidProbeEvent
	}
	fingerprint := "probe:" + event.EventID
	occurredAt := event.TriggeredAt.UTC()
	if _, err := s.observations.CreateInbound(ctx, ingress.ProjectID, ingress.SourceID, string(payload), fingerprint, occurredAt); err != nil {
		return fmt.Errorf("create probe observation: %w", err)
	}
	title := limitText("Log rule: "+rule.Name, maxTitleRunes, maxTitleRunes*4)
	if _, _, err := s.incidents.IngestInbound(ctx, ingress.ProjectID, ingress.SourceID, title, fingerprint, occurredAt); err != nil {
		return fmt.Errorf("ingest probe incident: %w", err)
	}
	return nil
}

func (s *Service) processAWSInbound(ctx context.Context, ingress projectapplication.WebhookIngress, message VerifiedAWSMessage, receivedAt time.Time) error {
	alarm := message.Alarm
	if alarm == nil {
		return ErrInvalidAWSSNS
	}
	normalizedContext := map[string]any{
		"schemaVersion": 1,
		"provider":      string(projectdomain.WebhookProviderAWSCloudWatch),
		"topicArn":      message.TopicARN,
		"sns": map[string]any{
			"messageId": message.MessageID, "timestamp": message.SNSTimestamp.UTC().Format(time.RFC3339Nano),
		},
		"alarm": map[string]any{
			"name": alarm.Name, "arn": alarm.ARN, "state": alarm.State, "reason": alarm.Reason,
			"stateChangeTime": alarm.StateChangedAt.UTC().Format(time.RFC3339Nano), "region": alarm.Region,
			"accountId": alarm.AccountID, "description": alarm.Description, "alarmRule": alarm.AlarmRule,
			"trigger": allowlistedAWSTrigger(alarm.Trigger),
		},
	}
	payload, err := json.Marshal(normalizedContext)
	if err != nil || len(payload) > maxObservationMessage {
		return fmt.Errorf("normalize AWS CloudWatch alarm: %w", ErrInvalidAWSSNS)
	}
	fingerprint := hashBytes("aws-cloudwatch:v1", ingress.ProjectID, ingress.SourceID, []byte(alarm.ARN))
	observation, err := s.observations.CreateInbound(ctx, ingress.ProjectID, ingress.SourceID, string(payload), fingerprint, receivedAt)
	if err != nil {
		return fmt.Errorf("create AWS inbound observation: %w", err)
	}
	if alarm.State != "ALARM" {
		return nil
	}
	title := limitText("AWS CloudWatch alarm: "+alarm.Name, maxTitleRunes, maxTitleRunes*4)
	persist := func(persistContext context.Context, incident incidentdomain.Incident) error {
		return s.persistAWSInboundEvidence(persistContext, ingress, observation, incident, payload)
	}
	if s.evidence == nil {
		return fmt.Errorf("ingest AWS inbound incident: evidence writer is unavailable")
	}
	enriched, ok := s.incidents.(analysisOnlyEvidenceIngester)
	if !ok {
		return fmt.Errorf("ingest AWS inbound incident: analysis-only ingester is unavailable")
	}
	if _, _, err := enriched.IngestInboundAnalysisOnlyWithEvidence(ctx, ingress.ProjectID, ingress.SourceID, title, fingerprint, receivedAt, persist); err != nil {
		return fmt.Errorf("ingest AWS inbound incident: %w", err)
	}
	return nil
}

func (s *Service) persistAWSInboundEvidence(ctx context.Context, ingress projectapplication.WebhookIngress, observation observationdomain.Observation, incident incidentdomain.Incident, payload []byte) error {
	provenance, err := json.Marshal(map[string]string{
		"kind": "webhook", "provider": string(projectdomain.WebhookProviderAWSCloudWatch), "observation_id": observation.ID,
	})
	if err != nil {
		return fmt.Errorf("encode AWS alert provenance: %w", err)
	}
	return s.appendEvidence(ctx, remediationdomain.StoredEvidence{
		ProjectID: ingress.ProjectID, EnvironmentID: observation.EnvironmentID, SourceID: ingress.SourceID,
		IncidentID: incident.InternalID, ObservationID: observation.ID, Provider: string(projectdomain.WebhookProviderAWSCloudWatch),
		EvidenceKind: remediationdomain.EvidenceKindNormalizedAlert, DeduplicationKey: "observation:" + observation.ID + ":normalized-alert",
		Classification: remediationdomain.EvidenceContextual, Outcome: "success", Available: true,
		OccurredAt: timePointer(observation.OccurredAt), IngestedAt: s.now().UTC(), ContentHash: contentHash(payload),
		ByteCount: int64(len(payload)), Provenance: provenance, Payload: payload,
	})
}

func allowlistedAWSTrigger(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var source map[string]any
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil
	}
	allowed := map[string]struct{}{
		"MetricName": {}, "Namespace": {}, "Statistic": {}, "ExtendedStatistic": {}, "Period": {},
		"EvaluationPeriods": {}, "DatapointsToAlarm": {}, "ComparisonOperator": {}, "Threshold": {},
		"TreatMissingData": {}, "EvaluateLowSampleCountPercentile": {}, "Dimensions": {}, "Metrics": {},
	}
	out := make(map[string]any)
	for key, value := range source {
		if _, ok := allowed[key]; !ok {
			continue
		}
		encoded, err := json.Marshal(value)
		if err == nil && len(encoded) <= 8192 {
			out[key] = value
		}
	}
	return out
}

func (s *Service) processInbound(ctx context.Context, ingress projectapplication.WebhookIngress, raw, analyzerInput string, callback *TencentCLSCallback, occurredAt time.Time) error {
	normalized, err := s.analyzer.Normalize(ctx, ingress.ProjectID, ingress.SourceID, analyzerInput)
	if err != nil {
		return fmt.Errorf("normalize webhook inbound: %w", err)
	}
	if callback != nil {
		fingerprint, err := callback.semanticFingerprint(ingress.ProjectID, ingress.SourceID)
		if err != nil {
			return fmt.Errorf("fingerprint tencent cls inbound: %w", err)
		}
		normalized.Fingerprint = fingerprint
	}
	// 已接受的 callback 即使从 semantic fingerprint 中移除了 provider metadata，存储的 Observation 仍保留完整正文。
	normalized.Message = raw
	observation, err := s.observations.CreateInbound(ctx, ingress.ProjectID, ingress.SourceID, normalized.Message, normalized.Fingerprint, occurredAt)
	if err != nil {
		return fmt.Errorf("create inbound observation: %w", err)
	}

	persist := func(persistContext context.Context, incident incidentdomain.Incident) error {
		return s.persistInboundEvidence(persistContext, ingress, observation, incident, normalized, callback)
	}
	if s.evidence != nil {
		enriched, ok := s.incidents.(inboundEvidenceIngester)
		if !ok {
			return fmt.Errorf("ingest inbound incident: evidence ingester is unavailable")
		}
		if _, _, err := enriched.IngestInboundWithEvidence(ctx, ingress.ProjectID, ingress.SourceID, normalized.Title, normalized.Fingerprint, occurredAt, persist); err != nil {
			return fmt.Errorf("ingest inbound incident: %w", err)
		}
		return nil
	}
	if _, _, err := s.incidents.IngestInbound(ctx, ingress.ProjectID, ingress.SourceID, normalized.Title, normalized.Fingerprint, occurredAt); err != nil {
		return fmt.Errorf("ingest inbound incident: %w", err)
	}
	return nil
}

func (s *Service) persistInboundEvidence(ctx context.Context, ingress projectapplication.WebhookIngress, observation observationdomain.Observation, incident incidentdomain.Incident, normalized NormalizedInbound, callback *TencentCLSCallback) error {
	if s.evidence == nil {
		return nil
	}
	provider := string(ingress.Provider)
	if provider == "" {
		provider = string(projectdomain.WebhookProviderGeneric)
	}
	quality := remediationdomain.AlertQualitySparse
	if callback != nil {
		quality = remediationdomain.AlertQualityAnchorOnly
	}
	alertPayload := map[string]any{
		"provider":        provider,
		"alertQuality":    quality,
		"title":           normalized.Title,
		"fingerprint":     normalized.Fingerprint,
		"originalSummary": normalized.Title,
	}
	if callback != nil {
		alertPayload["providerFields"] = callback.EvidenceFields()
	}
	payload, err := json.Marshal(alertPayload)
	if err != nil {
		return fmt.Errorf("encode normalized alert evidence: %w", err)
	}
	provenance, err := json.Marshal(map[string]string{
		"kind":           "webhook",
		"provider":       provider,
		"observation_id": observation.ID,
	})
	if err != nil {
		return fmt.Errorf("encode normalized alert provenance: %w", err)
	}
	if err := s.appendEvidence(ctx, remediationdomain.StoredEvidence{
		ProjectID: ingress.ProjectID, EnvironmentID: observation.EnvironmentID, SourceID: ingress.SourceID,
		IncidentID: incident.InternalID, ObservationID: observation.ID, Provider: provider,
		EvidenceKind: remediationdomain.EvidenceKindNormalizedAlert, DeduplicationKey: "observation:" + observation.ID + ":normalized-alert",
		Classification: remediationdomain.EvidenceContextual, Outcome: "success", Available: true,
		OccurredAt: timePointer(observation.OccurredAt), IngestedAt: s.now().UTC(), ContentHash: contentHash(payload),
		ByteCount: int64(len(payload)), Provenance: provenance, Payload: payload,
	}); err != nil {
		return err
	}
	return nil
}

func (s *Service) appendEvidence(ctx context.Context, evidence remediationdomain.StoredEvidence) error {
	if _, err := s.evidence.AppendEvidence(ctx, evidence); err != nil {
		return fmt.Errorf("append %s evidence: %w", evidence.EvidenceKind, err)
	}
	return nil
}

func contentHash(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func timePointer(value time.Time) *time.Time {
	value = value.UTC()
	return &value
}

func mapLookupError(err error) error {
	switch {
	case errors.Is(err, projectapplication.ErrInvalidInput), errors.Is(err, observationapplication.ErrInvalidInput),
		errors.Is(err, incidentapplication.ErrInvalidInput):
		return ErrInvalidInput
	case errors.Is(err, projectapplication.ErrNotFound), errors.Is(err, projectapplication.ErrConfigurationNotFound),
		errors.Is(err, observationapplication.ErrSourceNotFound), errors.Is(err, incidentapplication.ErrNotFound):
		return ErrWebhookNotFound
	default:
		return err
	}
}
