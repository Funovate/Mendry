package domain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// AgentLoopMode 是 run 创建时快照的项目 remediation 政策模式（D9）。
// 默认 legacy 保持现有行为；resilient_v1 按项目逐步启用，run 中途修改配置
// 不能改变已快照的语义。
type AgentLoopMode string

const (
	// AgentLoopModeLegacy 保留当前 coordinator 的既有行为。
	AgentLoopModeLegacy AgentLoopMode = "legacy"
	// AgentLoopModeResilientV1 启用 durable checkpoint/recovery 契约。
	AgentLoopModeResilientV1 AgentLoopMode = "resilient_v1"
)

// ParseAgentLoopMode 解析持久化的模式字符串；未知值保守回退到 legacy，
// 保证旧行与配置漂移都不会意外启用新语义。
func ParseAgentLoopMode(value string) AgentLoopMode {
	if AgentLoopMode(value) == AgentLoopModeResilientV1 {
		return AgentLoopModeResilientV1
	}
	return AgentLoopModeLegacy
}

// IsKnown 报告该模式是否为持久化允许值。
func (m AgentLoopMode) IsKnown() bool {
	switch m {
	case AgentLoopModeLegacy, AgentLoopModeResilientV1:
		return true
	default:
		return false
	}
}

// Checkpoint 常量：schema 标识、持久化字节上限与触发分类。
const (
	// CheckpointSchemaVersionV1 是 WorkingMemoryCheckpointV1 的 schema 标识。
	CheckpointSchemaVersionV1 = "v1"
	// MaxCheckpointPayloadBytes 是 canonical checkpoint JSON 的持久化上限；
	// 超过该上限的 checkpoint 在写入前被拒绝。
	MaxCheckpointPayloadBytes = 1 << 20

	// CheckpointReasonThreshold 等是 checkpoint 的触发分类（D2 reason 字段）。
	CheckpointReasonThreshold       = "threshold"
	CheckpointReasonPhaseBoundary   = "phase_boundary"
	CheckpointReasonRecovery        = "recovery"
	CheckpointReasonProcessShutdown = "process_shutdown"
)

// 字段级边界，全部按 rune 计数；持久化校验在 domain 边界完成。
const (
	maxCheckpointPhaseLength        = 64
	maxCheckpointReasonLength       = 64
	maxCheckpointTextLength         = 512
	maxCheckpointGoalLength         = 1024
	maxCheckpointEvidenceIDLength   = 128
	maxCheckpointProgressItemLen    = 256
	maxCheckpointVerifiedFacts      = 64
	maxCheckpointHypotheses         = 64
	maxCheckpointEvidenceIndex      = 256
	maxCheckpointQuestions          = 32
	maxCheckpointRecoveries         = 64
	maxCheckpointNextActions        = 32
	maxCheckpointEvidenceIDs        = 64
	maxCheckpointProgressItems      = 32
	maxCheckpointCompletionCriteria = 32
)

var (
	// ErrCheckpointNotFound 表示 run 尚无可加载的 latest checkpoint。
	ErrCheckpointNotFound = errors.New("remediation checkpoint not found")
	// ErrCheckpointInvalid 表示 checkpoint 内容违反 v1 schema 或字段边界。
	ErrCheckpointInvalid = errors.New("invalid remediation checkpoint")
	// ErrCheckpointCorrupt 表示持久化状态违反 hash/identity/sequence 不变量。
	ErrCheckpointCorrupt = errors.New("remediation checkpoint is corrupt")
	// ErrCheckpointConflict 表示并发 append 撞 sequence；调用方可安全重试。
	ErrCheckpointConflict = errors.New("remediation checkpoint append conflict")
)

// WorkingMemoryCheckpointV1 是 provider-neutral 的持久化工作记忆快照（D2）。
// JSON 字段顺序固定（canonical form）：字段顺序即持久化 payload 与 content
// hash 的唯一输入，因此载荷可以稳定地校验与重建。summaries 永远不成为证据：
// 每个 verified fact 都必须携带 evidenceIds，原始证据按 retention/授权策略
// 仍可重新读取。
type WorkingMemoryCheckpointV1 struct {
	SchemaVersion       string                        `json:"schemaVersion"`
	Sequence            int64                         `json:"sequence"`
	RunID               string                        `json:"runId"`
	SeriesID            string                        `json:"seriesId"`
	ContextVersion      int64                         `json:"contextVersion"`
	ObservedRunVersion  int64                         `json:"observedRunVersion"`
	Phase               string                        `json:"phase"`
	Objective           CheckpointObjective           `json:"objective"`
	VerifiedFacts       []CheckpointVerifiedFact      `json:"verifiedFacts"`
	ActiveHypotheses    []CheckpointHypothesis        `json:"activeHypotheses"`
	RejectedHypotheses  []CheckpointHypothesis        `json:"rejectedHypotheses"`
	EvidenceIndex       []CheckpointEvidenceIndexItem `json:"evidenceIndex"`
	UnresolvedQuestions []CheckpointQuestion          `json:"unresolvedQuestions"`
	PhaseProgress       CheckpointPhaseProgress       `json:"phaseProgress"`
	Recoveries          []CheckpointRecovery          `json:"recoveries"`
	NextActions         []string                      `json:"nextActions"`
	Budget              BudgetPlanRecoveryV1          `json:"budget"`
	Reason              string                        `json:"reason"`
}

// CheckpointObjective 是当前目标与完成标准。
type CheckpointObjective struct {
	Goal               string   `json:"goal"`
	CompletionCriteria []string `json:"completionCriteria"`
}

// CheckpointVerifiedFact 是必须携带 evidence IDs 的已验证事实。
type CheckpointVerifiedFact struct {
	Statement   string   `json:"statement"`
	EvidenceIDs []string `json:"evidenceIds"`
}

// CheckpointHypothesis 是活动或已拒绝的假设；拒绝假设通过 Reason 记录原因。
type CheckpointHypothesis struct {
	ID          string   `json:"id"`
	Summary     string   `json:"summary"`
	EvidenceIDs []string `json:"evidenceIds"`
	Reason      string   `json:"reason"`
}

// CheckpointEvidenceIndexItem 是 evidence index 的单条有界定位记录。
type CheckpointEvidenceIndexItem struct {
	EvidenceID  string `json:"evidenceId"`
	Kind        string `json:"kind"`
	Locator     string `json:"locator"`
	ContentHash string `json:"contentHash"`
}

// CheckpointQuestion 是未解决的材料性问题标记。
type CheckpointQuestion struct {
	Question string `json:"question"`
	Material bool   `json:"material"`
}

// CheckpointPhaseProgress 是当前 phase 的完成/剩余条目。
type CheckpointPhaseProgress struct {
	Completed []string `json:"completed"`
	Remaining []string `json:"remaining"`
}

// CheckpointRecovery 是一次 recovery 尝试的定位记录。
type CheckpointRecovery struct {
	Kind       string `json:"kind"`
	Action     string `json:"action"`
	OutcomeRef string `json:"outcomeRef"`
}

// CheckpointSnapshot 是 latest working-memory snapshot 与解析后的 checkpoint。
// ContentHash 是 canonical payload 的 SHA-256；Sequence 对应 backing event。
type CheckpointSnapshot struct {
	RunID              string
	SeriesID           string
	ContextVersion     int64
	ObservedRunVersion int64
	DurableRunVersion  int64
	DurableRunState    RunState
	NeedsRebuild       bool
	Sequence           int64
	Phase              string
	ContentHash        string
	UpdatedAt          time.Time
	Checkpoint         WorkingMemoryCheckpointV1
}

// CheckpointEvent 是 audit 用途的 immutable event 有界投影。
type CheckpointEvent struct {
	EventID       string
	RunID         string
	Sequence      int64
	TriggerReason string
	ContentHash   string
	CreatedAt     time.Time
	Checkpoint    WorkingMemoryCheckpointV1
}

// CheckpointStore 持久化 provider-neutral working-memory checkpoints（D2）：
// append immutable event 与 latest snapshot 在同一事务内原子更新，加载时校验
// run/series/context 身份、schema、hash 与 sequence-backed-by-event 不变量。
type CheckpointStore interface {
	AppendCheckpoint(ctx context.Context, runID string, checkpoint WorkingMemoryCheckpointV1) (CheckpointSnapshot, error)
	LoadLatestCheckpoint(ctx context.Context, runID string) (CheckpointSnapshot, error)
	ListCheckpointEvents(ctx context.Context, runID string, limit int) ([]CheckpointEvent, error)
}

// Validate 校验 WorkingMemoryCheckpointV1 的 schema 与字段边界。
// Sequence 允许 0（尚未由存储分配）；持久化前必须通过 WithSequence 赋值。
func (c WorkingMemoryCheckpointV1) Validate() error {
	if c.SchemaVersion != CheckpointSchemaVersionV1 {
		return fmt.Errorf("%w: unknown checkpoint schema version %q", ErrCheckpointInvalid, c.SchemaVersion)
	}
	if strings.TrimSpace(c.RunID) == "" || strings.TrimSpace(c.SeriesID) == "" {
		return fmt.Errorf("%w: checkpoint run and series identity are required", ErrCheckpointInvalid)
	}
	if c.Sequence < 0 {
		return fmt.Errorf("%w: checkpoint sequence must not be negative", ErrCheckpointInvalid)
	}
	if c.ContextVersion < 0 {
		return fmt.Errorf("%w: checkpoint context version must not be negative", ErrCheckpointInvalid)
	}
	if c.ObservedRunVersion < 1 {
		return fmt.Errorf("%w: checkpoint observed run version must be positive", ErrCheckpointInvalid)
	}
	if err := boundedText("phase", c.Phase, maxCheckpointPhaseLength); err != nil {
		return fmt.Errorf("%w: %v", ErrCheckpointInvalid, err)
	}
	if !isKnownCheckpointReason(c.Reason) {
		return fmt.Errorf("%w: unknown checkpoint reason %q", ErrCheckpointInvalid, c.Reason)
	}
	if err := c.Objective.validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrCheckpointInvalid, err)
	}
	if len(c.VerifiedFacts) > maxCheckpointVerifiedFacts {
		return fmt.Errorf("%w: verified facts exceed %d entries", ErrCheckpointInvalid, maxCheckpointVerifiedFacts)
	}
	for _, fact := range c.VerifiedFacts {
		if err := fact.validate(); err != nil {
			return fmt.Errorf("%w: %v", ErrCheckpointInvalid, err)
		}
	}
	if len(c.ActiveHypotheses) > maxCheckpointHypotheses || len(c.RejectedHypotheses) > maxCheckpointHypotheses {
		return fmt.Errorf("%w: hypotheses exceed %d entries", ErrCheckpointInvalid, maxCheckpointHypotheses)
	}
	for _, hypothesis := range append(c.ActiveHypotheses, c.RejectedHypotheses...) {
		if err := hypothesis.validate(); err != nil {
			return fmt.Errorf("%w: %v", ErrCheckpointInvalid, err)
		}
	}
	if len(c.EvidenceIndex) > maxCheckpointEvidenceIndex {
		return fmt.Errorf("%w: evidence index exceeds %d entries", ErrCheckpointInvalid, maxCheckpointEvidenceIndex)
	}
	for _, item := range c.EvidenceIndex {
		if err := item.validate(); err != nil {
			return fmt.Errorf("%w: %v", ErrCheckpointInvalid, err)
		}
	}
	if len(c.UnresolvedQuestions) > maxCheckpointQuestions {
		return fmt.Errorf("%w: unresolved questions exceed %d entries", ErrCheckpointInvalid, maxCheckpointQuestions)
	}
	for _, question := range c.UnresolvedQuestions {
		if err := boundedText("question", question.Question, maxCheckpointTextLength); err != nil {
			return fmt.Errorf("%w: %v", ErrCheckpointInvalid, err)
		}
	}
	if err := c.PhaseProgress.validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrCheckpointInvalid, err)
	}
	if len(c.Recoveries) > maxCheckpointRecoveries {
		return fmt.Errorf("%w: recoveries exceed %d entries", ErrCheckpointInvalid, maxCheckpointRecoveries)
	}
	for _, recovery := range c.Recoveries {
		if err := recovery.validate(); err != nil {
			return fmt.Errorf("%w: %v", ErrCheckpointInvalid, err)
		}
	}
	if len(c.NextActions) > maxCheckpointNextActions {
		return fmt.Errorf("%w: next actions exceed %d entries", ErrCheckpointInvalid, maxCheckpointNextActions)
	}
	for _, action := range c.NextActions {
		if err := boundedText("next action", action, maxCheckpointProgressItemLen); err != nil {
			return fmt.Errorf("%w: %v", ErrCheckpointInvalid, err)
		}
	}
	if err := c.Budget.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrCheckpointInvalid, err)
	}
	return nil
}

// WithSequence 返回指定 sequence 的新副本；持久化前由存储分配并调用，
// 保证 sequence 单调且非零。
func (c WorkingMemoryCheckpointV1) WithSequence(sequence int64) (WorkingMemoryCheckpointV1, error) {
	if sequence < 1 {
		return WorkingMemoryCheckpointV1{}, fmt.Errorf("%w: checkpoint sequence must be positive", ErrCheckpointInvalid)
	}
	c.Sequence = sequence
	return c, nil
}

// CanonicalEncode 返回有界 canonical JSON：字段顺序固定、无多余空白，
// 是持久化 payload 与 content hash 的唯一输入。Sequence 必须已赋值。
func (c WorkingMemoryCheckpointV1) CanonicalEncode() ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if c.Sequence < 1 {
		return nil, fmt.Errorf("%w: checkpoint sequence is not assigned", ErrCheckpointInvalid)
	}
	payload, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("encode checkpoint: %w", err)
	}
	if len(payload) > MaxCheckpointPayloadBytes {
		return nil, fmt.Errorf("%w: checkpoint payload exceeds %d bytes", ErrCheckpointInvalid, MaxCheckpointPayloadBytes)
	}
	return payload, nil
}

// ContentHash 返回 canonical payload 的 SHA-256 hex；与持久化的 content_hash 比对。
func (c WorkingMemoryCheckpointV1) ContentHash() (string, error) {
	payload, err := c.CanonicalEncode()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func (o CheckpointObjective) validate() error {
	if err := boundedText("objective goal", o.Goal, maxCheckpointGoalLength); err != nil {
		return err
	}
	if len(o.CompletionCriteria) > maxCheckpointCompletionCriteria {
		return fmt.Errorf("completion criteria exceed %d entries", maxCheckpointCompletionCriteria)
	}
	for _, criteria := range o.CompletionCriteria {
		if err := boundedText("completion criteria", criteria, maxCheckpointTextLength); err != nil {
			return err
		}
	}
	return nil
}

func (f CheckpointVerifiedFact) validate() error {
	if err := boundedText("verified fact statement", f.Statement, maxCheckpointTextLength); err != nil {
		return err
	}
	// R15：verified facts 必须保留 evidence IDs，summaries 永远不能成为证据。
	if len(f.EvidenceIDs) == 0 {
		return fmt.Errorf("verified fact must retain at least one evidence id")
	}
	return validateEvidenceIDList("verified fact", f.EvidenceIDs)
}

func (h CheckpointHypothesis) validate() error {
	if err := boundedText("hypothesis id", h.ID, maxCheckpointEvidenceIDLength); err != nil {
		return err
	}
	if err := boundedText("hypothesis summary", h.Summary, maxCheckpointTextLength); err != nil {
		return err
	}
	if err := boundedOptionalText("hypothesis reason", h.Reason, maxCheckpointTextLength); err != nil {
		return err
	}
	return validateEvidenceIDList("hypothesis", h.EvidenceIDs)
}

func (i CheckpointEvidenceIndexItem) validate() error {
	if err := boundedText("evidence index id", i.EvidenceID, maxCheckpointEvidenceIDLength); err != nil {
		return err
	}
	if err := boundedText("evidence index kind", i.Kind, 96); err != nil {
		return err
	}
	if err := boundedText("evidence index locator", i.Locator, maxCheckpointProgressItemLen); err != nil {
		return err
	}
	return boundedText("evidence index content hash", i.ContentHash, 64)
}

func (p CheckpointPhaseProgress) validate() error {
	if len(p.Completed) > maxCheckpointProgressItems || len(p.Remaining) > maxCheckpointProgressItems {
		return fmt.Errorf("phase progress exceeds %d entries", maxCheckpointProgressItems)
	}
	for _, item := range append(p.Completed, p.Remaining...) {
		if err := boundedText("phase progress item", item, maxCheckpointProgressItemLen); err != nil {
			return err
		}
	}
	return nil
}

func (r CheckpointRecovery) validate() error {
	if err := boundedText("recovery kind", r.Kind, 64); err != nil {
		return err
	}
	if err := boundedText("recovery action", r.Action, maxCheckpointTextLength); err != nil {
		return err
	}
	return boundedOptionalText("recovery outcome ref", r.OutcomeRef, maxCheckpointProgressItemLen)
}

func validateEvidenceIDList(label string, ids []string) error {
	if len(ids) > maxCheckpointEvidenceIDs {
		return fmt.Errorf("%s evidence ids exceed %d entries", label, maxCheckpointEvidenceIDs)
	}
	for _, id := range ids {
		if err := boundedText(label+" evidence id", id, maxCheckpointEvidenceIDLength); err != nil {
			return err
		}
	}
	return nil
}

func boundedText(label, value string, max int) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", label)
	}
	if utf8.RuneCountInString(value) > max {
		return fmt.Errorf("%s exceeds %d characters", label, max)
	}
	return nil
}

// boundedOptionalText 允许空值；非空时按 rune 数限制上限，用于仅 rejected
// hypotheses 或可选 outcome 引用的语义字段。
func boundedOptionalText(label, value string, max int) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	if utf8.RuneCountInString(value) > max {
		return fmt.Errorf("%s exceeds %d characters", label, max)
	}
	return nil
}

func isKnownCheckpointReason(reason string) bool {
	switch reason {
	case CheckpointReasonThreshold, CheckpointReasonPhaseBoundary,
		CheckpointReasonRecovery, CheckpointReasonProcessShutdown:
		return true
	default:
		return false
	}
}
