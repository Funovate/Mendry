package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const maxContinuationReasonLength = 512

var (
	// ErrInvalidNextAttempt 标记在任何数据库工作开始前被拒绝的 continuation 请求。
	ErrInvalidNextAttempt = errors.New("invalid remediation next attempt")
	// 以下 sentinel 是安全、稳定的 eligibility 分类；调用方可使用 errors.Is，
	// 不会因此接收数据库细节或请求 payload。
	ErrStalePredecessor      = errors.New("stale remediation predecessor")
	ErrActiveAttempt         = errors.New("remediation attempt is active")
	ErrUnsupportedState      = errors.New("remediation continuation state is unsupported")
	ErrAutomaticGateRejected = errors.New("automatic continuation gate rejected")
	ErrAutomaticCeiling      = errors.New("automatic continuation ceiling reached")
	// ErrPlanningCheckpointNotFound 表示同一 series/context 中不存在已通过 evidence gate
	// 的 durable code_fixable decision；这是正常的 diagnosis fallback，不是存储失败。
	ErrPlanningCheckpointNotFound = errors.New("remediation planning checkpoint not found")
	// 以下别名让 application seam 可以使用更具描述性的分类名称。
	ErrUnsupportedContinuation = ErrUnsupportedState
	ErrAutomaticCeilingReached = ErrAutomaticCeiling
	ErrCeilingExhausted        = ErrAutomaticCeiling
)

// NextAttempt 描述一个新的 linked attempt；不携带 prompt、credential、authenticated URL、
// provider payload 或原始 webhook 数据。
type NextAttempt struct {
	ContinuationOfRunID     string
	SeriesID                string
	IncidentID              string
	LifecycleGeneration     int64
	DeployedCommit          string
	ContextVersion          int64
	ExpectedPreviousVersion int64
	Origin                  string
	TriggerReason           string
	ContinuationReason      string
}

// Validate 在持久化前检查 provider-neutral continuation contract。
func (n NextAttempt) Validate() error {
	required := []struct {
		name  string
		value string
	}{
		{name: "continuation predecessor", value: n.ContinuationOfRunID},
		{name: "series", value: n.SeriesID},
		{name: "incident", value: n.IncidentID},
		{name: "deployed commit", value: n.DeployedCommit},
	}
	for _, field := range required {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%s is required", field.name)
		}
	}
	if n.LifecycleGeneration < 1 {
		return fmt.Errorf("lifecycle generation must be positive")
	}
	if n.ContextVersion < 0 {
		return fmt.Errorf("context version cannot be negative")
	}
	if n.ExpectedPreviousVersion < 1 {
		return fmt.Errorf("expected predecessor version must be positive")
	}
	origin := n.TriggerReason
	if origin == "" {
		origin = n.Origin
	}
	if n.Origin != "" && n.TriggerReason != "" && n.Origin != n.TriggerReason {
		return fmt.Errorf("continuation origins do not match")
	}
	if origin != TriggerOriginAutomaticContinue && origin != TriggerOriginManualContinue {
		return fmt.Errorf("trigger reason is not a continuation origin")
	}
	if err := validateContinuationReason(n.ContinuationReason); err != nil {
		return err
	}
	return nil
}

func validateContinuationReason(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("continuation reason is required")
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("continuation reason is not valid UTF-8")
	}
	if utf8.RuneCountInString(value) > maxContinuationReasonLength {
		return fmt.Errorf("continuation reason exceeds bounds")
	}
	lower := strings.ToLower(value)
	for _, marker := range []string{
		"sk-", "bearer ", "password", "secret", "credential", "authorization",
		"token", "api_key", "apikey", "api-key", "http://", "https://", "-----begin",
	} {
		if strings.Contains(lower, marker) {
			return fmt.Errorf("continuation reason contains sensitive metadata")
		}
	}
	for _, r := range value {
		if r == '\x00' || r == '\r' || r == '\n' {
			return fmt.Errorf("continuation reason contains unsupported control characters")
		}
	}
	return nil
}

// Attempt 表示一个 remediation series 中的单次 run attempt。
type Attempt struct {
	ID                  uuid.UUID
	SeriesID            uuid.UUID
	AttemptNumber       int
	State               RunState
	StartedAt           time.Time
	EndedAt             *time.Time
	ElapsedMS           *int64
	ModelCalls          int
	ModelTokensIn       int64
	ModelTokensOut      int64
	ModelCostCents      int64
	ModelProvider       string
	ModelName           string
	ToolCalls           int
	EvidenceBytes       int64
	RepositoryBytes     int64
	Origin              string
	TriggerReason       string
	ContinuationOfRunID uuid.UUID
	ContinuationReason  string
	ContextVersion      int64
	TerminalReason      string
	Retryable           bool
	Version             int64
}

// Validate 检查 attempt 的字段值是否满足 domain 不变量。
func (a *Attempt) Validate() error {
	if a.ID == uuid.Nil {
		return fmt.Errorf("attempt ID cannot be nil")
	}
	if a.SeriesID == uuid.Nil {
		return fmt.Errorf("series ID cannot be nil")
	}
	if a.AttemptNumber < 0 {
		return fmt.Errorf("attempt number cannot be negative")
	}
	if _, err := ParseRunState(string(a.State)); err != nil {
		return fmt.Errorf("invalid state: %w", err)
	}
	if a.EndedAt != nil && a.EndedAt.Before(a.StartedAt) {
		return fmt.Errorf("ended_at cannot be before started_at")
	}
	if a.ContextVersion < 0 {
		return fmt.Errorf("context version cannot be negative")
	}
	if a.TriggerReason != "" && !TriggerOrigin(a.TriggerReason).IsKnown() {
		return fmt.Errorf("invalid trigger reason")
	}
	if a.Origin != "" && !TriggerOrigin(a.Origin).IsKnown() {
		return fmt.Errorf("invalid attempt origin")
	}
	if a.Origin != "" && a.TriggerReason != "" && a.Origin != a.TriggerReason {
		return fmt.Errorf("attempt origins do not match")
	}
	if a.ContinuationReason != "" {
		if err := validateContinuationReason(a.ContinuationReason); err != nil {
			return err
		}
	}
	if a.Version < 0 {
		return fmt.Errorf("version cannot be negative")
	}
	return nil
}

// Effect 表示一次 state transition 产生的副作用及安全终态 metadata。
type Effect struct {
	ModelCalls      int
	ModelTokensIn   int64
	ModelTokensOut  int64
	ModelCostCents  int64
	ModelProvider   string
	ModelName       string
	ToolCalls       int
	EvidenceBytes   int64
	RepositoryBytes int64
	TerminalReason  string
	Retryable       bool
}
