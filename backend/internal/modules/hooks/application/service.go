package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	incidentapplication "fixthe/backend/internal/modules/incidents/application"
	incidentdomain "fixthe/backend/internal/modules/incidents/domain"
	observationapplication "fixthe/backend/internal/modules/observations/application"
	observationdomain "fixthe/backend/internal/modules/observations/domain"
	projectapplication "fixthe/backend/internal/modules/projects/application"
)

var (
	ErrInvalidInput    = errors.New("invalid webhook input")
	ErrWebhookNotFound = errors.New("webhook not found")
)

const (
	maxTitleRunes           = 240
	maxFingerprintRunes     = 255
	maxObservationMessage   = 65536
	maxObservationNameBytes = 255
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
	IngestInbound(ctx context.Context, projectID, sourceID, title, fingerprint string, occurredAt time.Time) (incidentdomain.Incident, bool, error)
}

// Options 注入公开 ingress 的查找、Event Stream 写入、事故入站和可测试时钟。
type Options struct {
	Tokens       TokenLookup
	Observations ObservationWriter
	Incidents    IncidentIngester
	Now          func() time.Time
}

// Service 编排未认证 webhook：先规范化原文，再定位 token，最后写 Observation 并入站事故。
type Service struct {
	tokens       TokenLookup
	observations ObservationWriter
	incidents    IncidentIngester
	now          func() time.Time
}

// Result 是对外成功响应：只暴露公开事故编号，不回显 token 或原文。
type Result struct {
	IncidentID string
	Created    bool
}

// NewService 在接收公开请求前验证全部 webhook 依赖。
func NewService(options Options) (*Service, error) {
	if options.Tokens == nil || options.Observations == nil || options.Incidents == nil || options.Now == nil {
		return nil, fmt.Errorf("webhook ingest dependencies are required")
	}
	return &Service{tokens: options.Tokens, observations: options.Observations, incidents: options.Incidents, now: options.Now}, nil
}

// Ingest 先规范化原文再查 token，避免空正文对有效 token 做一次可观察的查找。
func (s *Service) Ingest(ctx context.Context, token, raw string) (Result, error) {
	title, fingerprint, message, err := NormalizeInbound(raw)
	if err != nil {
		return Result{}, err
	}
	ingress, err := s.tokens.LookupWebhookToken(ctx, token)
	if err != nil {
		return Result{}, mapLookupError(err)
	}
	occurredAt := s.now().UTC()
	if _, err := s.observations.CreateInbound(ctx, ingress.ProjectID, ingress.SourceID, message, fingerprint, occurredAt); err != nil {
		return Result{}, mapLookupError(err)
	}
	incident, created, err := s.incidents.IngestInbound(ctx, ingress.ProjectID, ingress.SourceID, title, fingerprint, occurredAt)
	if err != nil {
		return Result{}, mapLookupError(err)
	}
	identifier, err := incidentdomain.FormatID(incident.Number)
	if err != nil {
		return Result{}, err
	}
	return Result{IncidentID: identifier, Created: created}, nil
}

// NormalizeInbound 把不透明原文收成事故标题 / fingerprint 和 Observation message。
// 超长首行按事故与 Event Stream 的既有上限截断，而不是整单拒绝；
// fingerprint 还要满足 Observation 的 255 字节校验，因此额外按字节截断。
func NormalizeInbound(raw string) (title, fingerprint, message string, err error) {
	if !utf8.ValidString(raw) {
		return "", "", "", ErrInvalidInput
	}
	if strings.TrimSpace(raw) == "" {
		return "", "", "", ErrInvalidInput
	}
	line, err := firstNonEmptyLine(raw)
	if err != nil {
		return "", "", "", err
	}
	title = limitText(line, maxTitleRunes, maxTitleRunes*utf8.UTFMax)
	fingerprint = limitText(line, maxFingerprintRunes, maxObservationNameBytes)
	message = limitText(raw, maxObservationMessage, maxObservationMessage)
	if title == "" || fingerprint == "" || strings.TrimSpace(message) == "" {
		return "", "", "", ErrInvalidInput
	}
	return title, fingerprint, message, nil
}

func firstNonEmptyLine(raw string) (string, error) {
	for _, line := range strings.Split(raw, "\n") {
		collapsed := collapseSpace(line)
		if collapsed == "" {
			continue
		}
		return collapsed, nil
	}
	return "", ErrInvalidInput
}

func limitText(value string, maxRunes, maxBytes int) string {
	if utf8.RuneCountInString(value) > maxRunes {
		value = string([]rune(value)[:maxRunes])
	}
	if len(value) <= maxBytes {
		return value
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}

func collapseSpace(value string) string {
	fields := strings.FieldsFunc(value, unicode.IsSpace)
	return strings.Join(fields, " ")
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
