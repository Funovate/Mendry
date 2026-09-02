package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Recovery 常量：schema 标识与 envelope 字段边界。
const (
	// RecoverySchemaVersionV1 是 RecoveryChallengeV1 的 schema 标识。
	RecoverySchemaVersionV1 = "v1"
	// MaxRecoveryChallengeMessageLength 是 challenge message 的 rune 上限。
	MaxRecoveryChallengeMessageLength = 1024

	maxRecoveryReasonCodeLength = 128
	maxRecoveryActionRefLength  = 256
	maxRecoveryCapabilityLength = 64
	maxRecoveryCapabilities     = 32
	maxRecoverySuggestedClasses = 16
	maxRecoveryBudgetEntries    = 16
	maxRecoveryBudgetKeyLength  = 64
)

// RecoveryChallengeKind 是 D5 envelope 的 kind 枚举。
type RecoveryChallengeKind string

const (
	RecoveryChallengeKindEvidenceCorrection RecoveryChallengeKind = "evidence_correction"
	RecoveryChallengeKindProtocolCorrection RecoveryChallengeKind = "protocol_correction"
	RecoveryChallengeKindToolFailure        RecoveryChallengeKind = "tool_failure"
	RecoveryChallengeKindContextRehydration RecoveryChallengeKind = "context_rehydration"
	RecoveryChallengeKindValidationRevision RecoveryChallengeKind = "validation_revision"
	RecoveryChallengeKindPublicationRetry   RecoveryChallengeKind = "publication_retry"
	// RecoveryChallengeKindBudget 是 D7 soft-budget 信号（soft_crossed /
	// reserve_touched）对应的 recoverable challenge class；它永不直接产生
	// budget_exhausted 硬终态。
	RecoveryChallengeKindBudget RecoveryChallengeKind = "budget"
	// RecoveryChallengeKindExhaustion 是 D6 exhaustion proof 流程对应的
	// challenge class：模型 stop / 空 insufficient_evidence / Docker refinement
	// 或 collect-loop 耗尽时要求并校验 exhaustion proposal，不完整证明作为
	// recoverable challenge 回喂同一循环。
	RecoveryChallengeKindExhaustion RecoveryChallengeKind = "exhaustion"
)

// IsKnown 报告 kind 是否属于 D5 允许值。
func (k RecoveryChallengeKind) IsKnown() bool {
	switch k {
	case RecoveryChallengeKindEvidenceCorrection, RecoveryChallengeKindProtocolCorrection,
		RecoveryChallengeKindToolFailure, RecoveryChallengeKindContextRehydration,
		RecoveryChallengeKindValidationRevision, RecoveryChallengeKindPublicationRetry,
		RecoveryChallengeKindBudget, RecoveryChallengeKindExhaustion:
		return true
	default:
		return false
	}
}

// RecoverySeverity 是 D5 envelope 的 severity 枚举。
type RecoverySeverity string

const (
	RecoverySeverityRecoverable   RecoverySeverity = "recoverable"
	RecoverySeverityPolicyBlocked RecoverySeverity = "policy_blocked"
	RecoverySeverityHardTerminal  RecoverySeverity = "hard_terminal"
)

// IsKnown 报告 severity 是否属于 D5 允许值。
func (s RecoverySeverity) IsKnown() bool {
	switch s {
	case RecoverySeverityRecoverable, RecoverySeverityPolicyBlocked, RecoverySeverityHardTerminal:
		return true
	default:
		return false
	}
}

// RecoveryChallengeV1 是 D5 的 provider-neutral recovery envelope：一个统一的
// 安全错误/挑战契约，跨 evidence correction、protocol、tool、context、validation
// 与 publication 阶段复用。message 必须已经过调用方 sanitize；本类型只负责
// 枚举与边界校验。
type RecoveryChallengeV1 struct {
	SchemaVersion            string                `json:"schemaVersion"`
	Kind                     RecoveryChallengeKind `json:"kind"`
	Severity                 RecoverySeverity      `json:"severity"`
	ReasonCode               string                `json:"reasonCode"`
	FailedActionRef          string                `json:"failedActionRef"`
	AvailableCapabilities    []string              `json:"availableCapabilities"`
	SuggestedRecoveryClasses []string              `json:"suggestedRecoveryClasses"`
	Attempt                  int                   `json:"attempt"`
	RemainingBudget          map[string]int64      `json:"remainingBudget"`
	Message                  string                `json:"message"`
}

// Validate 校验 RecoveryChallengeV1 的 schema、枚举与字段边界。
func (c RecoveryChallengeV1) Validate() error {
	if c.SchemaVersion != RecoverySchemaVersionV1 {
		return fmt.Errorf("unknown recovery challenge schema version %q", c.SchemaVersion)
	}
	if !c.Kind.IsKnown() {
		return fmt.Errorf("unknown recovery challenge kind %q", c.Kind)
	}
	if !c.Severity.IsKnown() {
		return fmt.Errorf("unknown recovery severity %q", c.Severity)
	}
	if err := boundedText("reason code", c.ReasonCode, maxRecoveryReasonCodeLength); err != nil {
		return err
	}
	if err := boundedOptionalText("failed action ref", c.FailedActionRef, maxRecoveryActionRefLength); err != nil {
		return err
	}
	if c.Attempt < 1 {
		return fmt.Errorf("recovery attempt must be positive")
	}
	if err := boundedOptionalText("recovery message", c.Message, MaxRecoveryChallengeMessageLength); err != nil {
		return err
	}
	if len(c.AvailableCapabilities) > maxRecoveryCapabilities {
		return fmt.Errorf("available capabilities exceed %d entries", maxRecoveryCapabilities)
	}
	for _, capability := range c.AvailableCapabilities {
		if err := boundedText("capability", capability, maxRecoveryCapabilityLength); err != nil {
			return err
		}
	}
	if len(c.SuggestedRecoveryClasses) > maxRecoverySuggestedClasses {
		return fmt.Errorf("suggested recovery classes exceed %d entries", maxRecoverySuggestedClasses)
	}
	for _, class := range c.SuggestedRecoveryClasses {
		if err := boundedText("recovery class", class, maxRecoveryCapabilityLength); err != nil {
			return err
		}
	}
	if len(c.RemainingBudget) > maxRecoveryBudgetEntries {
		return fmt.Errorf("remaining budget exceeds %d entries", maxRecoveryBudgetEntries)
	}
	for key, value := range c.RemainingBudget {
		if strings.TrimSpace(key) == "" || utf8.RuneCountInString(key) > maxRecoveryBudgetKeyLength {
			return fmt.Errorf("remaining budget key is invalid")
		}
		if value < 0 {
			return fmt.Errorf("remaining budget value for %q must not be negative", key)
		}
	}
	return nil
}

// FailureFingerprint 确定性捕获 "unchanged failing action" 检测输入
// （failedActionRef + capability + error code）。它不是 universal one-retry rule：
// 相同指纹只表示同一动作没有变化，是否重试由进度感知的 recovery 逻辑决定。
type FailureFingerprint struct {
	FailedActionRef string
	Capability      string
	ErrorCode       string
}

// Key 返回指纹的稳定 SHA-256 hex；字段以 NUL 分隔避免拼接歧义。
func (f FailureFingerprint) Key() string {
	sum := sha256.Sum256([]byte(f.FailedActionRef + "\x00" + f.Capability + "\x00" + f.ErrorCode))
	return hex.EncodeToString(sum[:])
}

// Equals 比较三个检测输入是否完全相同；任一字段变化都视为新指纹。
func (f FailureFingerprint) Equals(other FailureFingerprint) bool {
	return f.FailedActionRef == other.FailedActionRef &&
		f.Capability == other.Capability &&
		f.ErrorCode == other.ErrorCode
}
