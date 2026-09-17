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
	RecoveryProgress    *CheckpointRecoveryProgress   `json:"recoveryProgress,omitempty"`
	NextActions         []string                      `json:"nextActions"`
	Workspace           *CheckpointWorkspace          `json:"workspace,omitempty"`
	Artifacts           []CheckpointArtifact          `json:"artifacts,omitempty"`
	Validation          *CheckpointValidation         `json:"validation,omitempty"`
	Publication         *CheckpointPublication        `json:"publication,omitempty"`
	PublicationPolicy   *CheckpointPublicationPolicy  `json:"publicationPolicy,omitempty"`
	ValidationCommands  map[string]int64              `json:"validationCommands,omitempty"`
	Budget              BudgetPlanRecoveryV1          `json:"budget"`
	Reason              string                        `json:"reason"`
}

// CheckpointWorkspace 是工作区外部身份的 checkpoint 投影；不保存仓库内容。
type CheckpointWorkspace struct {
	WorkspaceID     string `json:"workspaceId"`
	BaselineCommit  string `json:"baselineCommit"`
	BaseTreeHash    string `json:"baseTreeHash"`
	CurrentTreeHash string `json:"currentTreeHash"`
	Version         int64  `json:"version"`
}

// CheckpointArtifact 是 content-addressed patch 或验证结果的安全引用。
type CheckpointArtifact struct {
	Kind        string `json:"kind"`
	Reference   string `json:"reference"`
	ContentHash string `json:"contentHash"`
	SizeBytes   int64  `json:"sizeBytes"`
}

// CheckpointValidation is the aggregate result for every required command on one tree.
type CheckpointValidation struct {
	CommandID      string                       `json:"commandId,omitempty"`
	CommandVersion int64                        `json:"commandVersion,omitempty"`
	Passed         bool                         `json:"passed"`
	OutputArtifact string                       `json:"outputArtifact,omitempty"`
	OutputHash     string                       `json:"outputHash,omitempty"`
	TreeHash       string                       `json:"treeHash,omitempty"`
	Results        []CheckpointValidationResult `json:"results,omitempty"`
}

type CheckpointValidationResult struct {
	CommandID      string `json:"commandId"`
	CommandVersion int64  `json:"commandVersion"`
	TreeHash       string `json:"treeHash"`
	ImageDigest    string `json:"imageDigest,omitempty"`
	Passed         bool   `json:"passed"`
	OutputArtifact string `json:"outputArtifact,omitempty"`
	OutputHash     string `json:"outputHash,omitempty"`
}

// CheckpointPublicationPolicy 是 run 创建时快照的 publication target/ref policy。
type CheckpointPublicationPolicy struct {
	TargetBranch string `json:"targetBranch"`
	BranchPrefix string `json:"branchPrefix"`
}

// CheckpointPublication 是发布外部效果的 branch/commit/change-request 身份。
type CheckpointPublication struct {
	BranchRef       string `json:"branchRef"`
	TargetBranch    string `json:"targetBranch"`
	CommitHash      string `json:"commitHash"`
	DraftChangeRef  string `json:"draftChangeRef,omitempty"`
	CompareURL      string `json:"compareUrl,omitempty"`
	HumanReviewOnly bool   `json:"humanReviewOnly"`
	TargetDiverged  bool   `json:"targetDiverged,omitempty"`
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

// CheckpointRecoveryProgress 是 no-progress/retry 控制状态的有界权威快照。
// Recoveries 仅保留审计 journal；该字段避免 journal 淘汰旧条目后重启重新授予
// 尝试次数。omitempty 保持 pre-change v1 checkpoint 的 canonical JSON/hash。
type CheckpointRecoveryProgress struct {
	RecoveryAttempts                 int    `json:"recoveryAttempts"`
	EvidenceCorrectionAttempts       int    `json:"evidenceCorrectionAttempts"`
	FactCheckAttempts                int    `json:"factCheckAttempts"`
	FactCheckNoProgress              int    `json:"factCheckNoProgress"`
	LastFactCheckFingerprint         string `json:"lastFactCheckFingerprint,omitempty"`
	PlanFeedbackAttempts             int    `json:"planFeedbackAttempts"`
	PlanFeedbackNoProgress           int    `json:"planFeedbackNoProgress"`
	LastPlanFeedbackFingerprint      string `json:"lastPlanFeedbackFingerprint,omitempty"`
	LifecycleRecoveryAttempts        int    `json:"lifecycleRecoveryAttempts"`
	LastLifecycleRecoveryFingerprint string `json:"lastLifecycleRecoveryFingerprint,omitempty"`
	LastLifecycleRecoveryClass       string `json:"lastLifecycleRecoveryClass,omitempty"`
	LifecycleChallengeAttempts       int    `json:"lifecycleChallengeAttempts"`
	LifecycleStopAttempts            int    `json:"lifecycleStopAttempts"`
	ValidationNoProgress             int    `json:"validationNoProgress"`
	LastValidationFingerprint        string `json:"lastValidationFingerprint,omitempty"`
	ExhaustionProposalAttempts       int    `json:"exhaustionProposalAttempts"`
	// EpisodeRecoveryAttempts 是当前（最新）recovery episode 已执行的 recovery
	// checkpoint 数（D8 attempt）：每次 recovery 触发的 checkpoint 追加时递增，
	// 非 recovery checkpoint（durable 前进/终态）关闭 episode 后归零。它是
	// review/API 的 D8 attempt 唯一权威来源，不等于跨 episode 累计的 journal
	// 长度（journal 仅作 audit 历史，可能跨多个 episode）。
	EpisodeRecoveryAttempts int `json:"episodeRecoveryAttempts"`
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
	if c.RecoveryProgress != nil {
		if err := c.RecoveryProgress.validate(); err != nil {
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
	if err := validateCheckpointLifecycle(c); err != nil {
		return fmt.Errorf("%w: %v", ErrCheckpointInvalid, err)
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

func validateCheckpointLifecycle(c WorkingMemoryCheckpointV1) error {
	if c.Workspace != nil {
		workspace := c.Workspace
		for label, value := range map[string]string{
			"workspace id":                workspace.WorkspaceID,
			"workspace baseline commit":   workspace.BaselineCommit,
			"workspace base tree hash":    workspace.BaseTreeHash,
			"workspace current tree hash": workspace.CurrentTreeHash,
		} {
			if err := boundedText(label, value, 512); err != nil {
				return err
			}
		}
		if workspace.Version < 1 {
			return fmt.Errorf("workspace version must be positive")
		}
	}
	if len(c.Artifacts) > maxCheckpointEvidenceIDs {
		return fmt.Errorf("lifecycle artifacts exceed %d entries", maxCheckpointEvidenceIDs)
	}
	for _, artifact := range c.Artifacts {
		if err := boundedText("artifact kind", artifact.Kind, 64); err != nil {
			return err
		}
		if err := boundedText("artifact reference", artifact.Reference, 512); err != nil {
			return err
		}
		if err := boundedText("artifact content hash", artifact.ContentHash, 128); err != nil {
			return err
		}
		if err := validateContentHash("artifact content hash", artifact.ContentHash); err != nil {
			return err
		}
		if artifact.SizeBytes < 0 || artifact.SizeBytes > MaxCheckpointPayloadBytes {
			return fmt.Errorf("artifact size is out of bounds")
		}
	}
	if c.Validation != nil {
		if err := boundedText("validation command id", c.Validation.CommandID, 128); err != nil {
			return err
		}
		if c.Validation.CommandVersion < 1 {
			return fmt.Errorf("validation command version must be positive")
		}
		if err := boundedOptionalText("validation artifact", c.Validation.OutputArtifact, 512); err != nil {
			return err
		}
		if c.Validation.OutputArtifact != "" && c.Validation.OutputHash == "" {
			return fmt.Errorf("validation artifact requires output hash")
		}
		if err := boundedOptionalText("validation output hash", c.Validation.OutputHash, 128); err != nil {
			return err
		}
		if c.Validation.OutputHash != "" {
			if err := validateContentHash("validation output hash", c.Validation.OutputHash); err != nil {
				return err
			}
		}
		if c.Validation.TreeHash != "" {
			if err := boundedText("validation tree hash", c.Validation.TreeHash, 128); err != nil {
				return err
			}
		}
		if len(c.Validation.Results) > 32 {
			return fmt.Errorf("validation results exceed bounds")
		}
		for _, result := range c.Validation.Results {
			if err := boundedText("validation result command id", result.CommandID, 128); err != nil {
				return err
			}
			if result.CommandVersion < 1 {
				return fmt.Errorf("validation result command version must be positive")
			}
			if err := boundedText("validation result tree hash", result.TreeHash, 128); err != nil {
				return err
			}
			if result.TreeHash != c.Validation.TreeHash {
				return fmt.Errorf("validation results must bind one tree")
			}
		}
	}
	if c.PublicationPolicy != nil {
		if err := boundedText("publication policy target branch", c.PublicationPolicy.TargetBranch, 256); err != nil {
			return err
		}
		if err := boundedText("publication policy branch prefix", c.PublicationPolicy.BranchPrefix, 256); err != nil {
			return err
		}
	}
	if len(c.ValidationCommands) > 32 {
		return fmt.Errorf("validation command snapshot exceeds 32 entries")
	}
	for commandID, version := range c.ValidationCommands {
		if err := boundedText("validation command snapshot id", commandID, 128); err != nil {
			return err
		}
		if version < 1 {
			return fmt.Errorf("validation command snapshot version must be positive")
		}
	}
	if c.Publication != nil {
		if err := boundedText("publication branch ref", c.Publication.BranchRef, 256); err != nil {
			return err
		}
		if err := boundedText("publication target branch", c.Publication.TargetBranch, 256); err != nil {
			return err
		}
		if err := boundedText("publication commit hash", c.Publication.CommitHash, 256); err != nil {
			return err
		}
		if err := boundedOptionalText("publication draft change ref", c.Publication.DraftChangeRef, 512); err != nil {
			return err
		}
		if err := boundedOptionalText("publication compare URL", c.Publication.CompareURL, 1024); err != nil {
			return err
		}
		if !c.Publication.HumanReviewOnly {
			return fmt.Errorf("publication checkpoint must preserve human review gate")
		}
	}
	return nil
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

func (p CheckpointRecoveryProgress) validate() error {
	const maxRecoveryProgressCount = 1_000_000
	for label, value := range map[string]int{
		"recovery attempts":            p.RecoveryAttempts,
		"evidence correction attempts": p.EvidenceCorrectionAttempts,
		"fact check attempts":          p.FactCheckAttempts,
		"fact check no progress":       p.FactCheckNoProgress,
		"plan feedback attempts":       p.PlanFeedbackAttempts,
		"plan feedback no progress":    p.PlanFeedbackNoProgress,
		"lifecycle recovery attempts":  p.LifecycleRecoveryAttempts,
		"lifecycle challenge attempts": p.LifecycleChallengeAttempts,
		"lifecycle stop attempts":      p.LifecycleStopAttempts,
		"validation no progress":       p.ValidationNoProgress,
		"exhaustion proposal attempts": p.ExhaustionProposalAttempts,
		"episode recovery attempts":    p.EpisodeRecoveryAttempts,
	} {
		if value < 0 || value > maxRecoveryProgressCount {
			return fmt.Errorf("%s is out of bounds", label)
		}
	}
	if p.FactCheckNoProgress > p.FactCheckAttempts || p.PlanFeedbackNoProgress > p.PlanFeedbackAttempts {
		return fmt.Errorf("recovery no-progress count exceeds its attempt count")
	}
	if (p.FactCheckNoProgress > 0) != (p.LastFactCheckFingerprint != "") ||
		(p.PlanFeedbackNoProgress > 0) != (p.LastPlanFeedbackFingerprint != "") ||
		(p.ValidationNoProgress > 0) != (p.LastValidationFingerprint != "") {
		return fmt.Errorf("recovery no-progress count and fingerprint must be present together")
	}
	if (p.LifecycleRecoveryAttempts > 0) != (p.LastLifecycleRecoveryFingerprint != "") ||
		(p.LifecycleRecoveryAttempts > 0) != (p.LastLifecycleRecoveryClass != "") {
		return fmt.Errorf("lifecycle recovery count, fingerprint, and class must be present together")
	}
	for label, fingerprint := range map[string]string{
		"fact check fingerprint":         p.LastFactCheckFingerprint,
		"plan feedback fingerprint":      p.LastPlanFeedbackFingerprint,
		"lifecycle recovery fingerprint": p.LastLifecycleRecoveryFingerprint,
		"validation fingerprint":         p.LastValidationFingerprint,
	} {
		if fingerprint == "" {
			continue
		}
		if len(fingerprint) != sha256.Size*2 {
			return fmt.Errorf("%s must be a sha256 hex value", label)
		}
		if _, err := hex.DecodeString(fingerprint); err != nil {
			return fmt.Errorf("%s must be a sha256 hex value", label)
		}
	}
	if err := boundedOptionalText("lifecycle recovery class", p.LastLifecycleRecoveryClass, 64); err != nil {
		return err
	}
	return nil
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
