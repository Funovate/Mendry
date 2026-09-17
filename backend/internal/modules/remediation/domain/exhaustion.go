package domain

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Exhaustion 常量：D6 exhaustion proposal 的字段边界。schema 版本由外层
// envelope（schemaVersion/kind）携带，payload 本身镜像 D6 JSON 的五个字段。
const (
	// maxExhaustionGoalLength 是 unresolvedGoal 的 rune 上限。
	maxExhaustionGoalLength = 1024
	// maxExhaustionHandoffLength 是 handoff 的 rune 上限。
	maxExhaustionHandoffLength = 512
	// maxExhaustionCapabilityLength 是 capability class 名的 rune 上限。
	maxExhaustionCapabilityLength = 64
	// maxExhaustionReasonCodeLength 是 untried reason code 的 rune 上限。
	maxExhaustionReasonCodeLength = 64
	// maxExhaustionPaths 是 attemptedPaths 的条目上限。
	maxExhaustionPaths = 16
	// maxExhaustionUntried 是 untriedCapabilities 的条目上限。
	maxExhaustionUntried = 16
	// maxExhaustionRefs 是单个 path/untried 的引用条目上限。
	maxExhaustionRefs = 32
	// maxExhaustionRefLength 是单个引用（outcomeRef / evidenceRef）的 rune 上限。
	maxExhaustionRefLength = 256
)

// ExhaustionReasonCode 是 untriedCapabilities[].reasonCode 的有界枚举：
// 每条未尝试 capability 必须给出 factual / policy-backed 原因（D6/R19），
// 服务只校验原因是否被覆盖，不编码诊断决策树。
type ExhaustionReasonCode string

const (
	// ExhaustionReasonUnavailable 表示该 capability 在当前 run 不可用
	// （未广告、connector 失败且无可替代路径）。
	ExhaustionReasonUnavailable ExhaustionReasonCode = "unavailable"
	// ExhaustionReasonIrrelevant 表示该 capability 对当前因果链不构成
	// 材料性证据（materiality 由模型声明，服务校验引用与覆盖）。
	ExhaustionReasonIrrelevant ExhaustionReasonCode = "irrelevant"
	// ExhaustionReasonUnsafe 表示使用该 capability 违反 safety/policy。
	ExhaustionReasonUnsafe ExhaustionReasonCode = "unsafe"
	// ExhaustionReasonBudgetProhibited 表示剩余/保留预算不足以支撑该动作。
	ExhaustionReasonBudgetProhibited ExhaustionReasonCode = "budget_prohibited"
)

// IsKnown 报告 reason code 是否属于 D6 允许值。
func (r ExhaustionReasonCode) IsKnown() bool {
	switch r {
	case ExhaustionReasonUnavailable, ExhaustionReasonIrrelevant,
		ExhaustionReasonUnsafe, ExhaustionReasonBudgetProhibited:
		return true
	default:
		return false
	}
}

// KnownCapabilityClasses 返回 D1 的稳定 capability class 集合。recovery 与
// exhaustion validation 用它判定广告/未尝试的覆盖，而不编码固定调查顺序。
func KnownCapabilityClasses() []string {
	return []string{
		"repository",
		"provider_evidence",
		"runtime_logs",
		"ssh_inspect",
		"workspace",
		"validation",
		"publication",
	}
}

// isKnownCapabilityClass 报告值是否属于 D1 稳定 class 集合。
func isKnownCapabilityClass(value string) bool {
	for _, class := range KnownCapabilityClasses() {
		if class == value {
			return true
		}
	}
	return false
}

// ExhaustionAttemptedPath 是 exhaustion proof 中一条已尝试路径：capability
// class 加该路径的 observation/evidence 引用（D6 attemptedPaths）。
type ExhaustionAttemptedPath struct {
	Capability  string   `json:"capability"`
	OutcomeRefs []string `json:"outcomeRefs"`
}

// ExhaustionUntriedCapability 是 exhaustion proof 中一条未尝试 capability：
// capability class 加 factual / policy-backed reason code 与证据引用
// （D6 untriedCapabilities）。
type ExhaustionUntriedCapability struct {
	Capability   string   `json:"capability"`
	ReasonCode   string   `json:"reasonCode"`
	EvidenceRefs []string `json:"evidenceRefs"`
}

// ExhaustionProposalV1 是 D6 的 provider-neutral exhaustion proof envelope：
// 当模型声明所有可行路径已耗尽时，服务验证覆盖、recovery 与预算后才允许
// 进入 blocked_manual_review。字段边界在 domain 校验；服务 validator 负责
// 覆盖/归属/预算校验。bestConclusion 是模型的最佳结论（通常
// insufficient_evidence），不代表服务接受其因果判断。
type ExhaustionProposalV1 struct {
	UnresolvedGoal      string                        `json:"unresolvedGoal"`
	AttemptedPaths      []ExhaustionAttemptedPath     `json:"attemptedPaths"`
	UntriedCapabilities []ExhaustionUntriedCapability `json:"untriedCapabilities"`
	BestConclusion      FixabilityClass               `json:"bestConclusion"`
	Handoff             string                        `json:"handoff"`
}

// Validate 校验 ExhaustionProposalV1 的必填字段与边界：goal/handoff
// 非空且有界，bestConclusion 必须是已知 fixability class，capability 必须是
// D1 稳定 class，reason code 必须是 D6 枚举，refs 数量与长度有界。
func (p ExhaustionProposalV1) Validate() error {
	if err := boundedText("exhaustion unresolved goal", p.UnresolvedGoal, maxExhaustionGoalLength); err != nil {
		return err
	}
	if err := boundedText("exhaustion handoff", p.Handoff, maxExhaustionHandoffLength); err != nil {
		return err
	}
	if !isKnownFixability(p.BestConclusion) {
		return fmt.Errorf("unknown exhaustion best conclusion %q", p.BestConclusion)
	}
	if len(p.AttemptedPaths) > maxExhaustionPaths {
		return fmt.Errorf("exhaustion attempted paths exceed %d entries", maxExhaustionPaths)
	}
	for _, path := range p.AttemptedPaths {
		if err := boundedText("exhaustion capability", path.Capability, maxExhaustionCapabilityLength); err != nil {
			return err
		}
		if !isKnownCapabilityClass(path.Capability) {
			return fmt.Errorf("unknown exhaustion capability %q", path.Capability)
		}
		if len(path.OutcomeRefs) == 0 {
			return fmt.Errorf("exhaustion attempted capability %q requires an outcome ref", path.Capability)
		}
		if err := validateExhaustionRefs("exhaustion outcome refs", path.OutcomeRefs); err != nil {
			return err
		}
	}
	if len(p.UntriedCapabilities) > maxExhaustionUntried {
		return fmt.Errorf("exhaustion untried capabilities exceed %d entries", maxExhaustionUntried)
	}
	for _, untried := range p.UntriedCapabilities {
		if err := boundedText("exhaustion capability", untried.Capability, maxExhaustionCapabilityLength); err != nil {
			return err
		}
		if !isKnownCapabilityClass(untried.Capability) {
			return fmt.Errorf("unknown exhaustion capability %q", untried.Capability)
		}
		if err := boundedText("exhaustion reason code", untried.ReasonCode, maxExhaustionReasonCodeLength); err != nil {
			return err
		}
		if !ExhaustionReasonCode(untried.ReasonCode).IsKnown() {
			return fmt.Errorf("unknown exhaustion reason code %q", untried.ReasonCode)
		}
		if ExhaustionReasonCode(untried.ReasonCode) != ExhaustionReasonBudgetProhibited && len(untried.EvidenceRefs) == 0 {
			return fmt.Errorf("exhaustion untried capability %q requires factual evidence refs", untried.Capability)
		}
		if err := validateExhaustionRefs("exhaustion evidence refs", untried.EvidenceRefs); err != nil {
			return err
		}
	}
	return nil
}

// validateExhaustionRefs 校验引用列表的数量、非空值与长度边界；
// attempted path 以及非预算类 untried reason 的必填关系由 Validate 单独校验，
// 引用归属和 capability 绑定由 application validator 进一步校验。
func validateExhaustionRefs(label string, refs []string) error {
	if len(refs) > maxExhaustionRefs {
		return fmt.Errorf("%s exceed %d entries", label, maxExhaustionRefs)
	}
	for _, ref := range refs {
		if strings.TrimSpace(ref) == "" {
			return fmt.Errorf("%s must not contain empty refs", label)
		}
		if utf8.RuneCountInString(ref) > maxExhaustionRefLength {
			return fmt.Errorf("%s ref exceeds %d characters", label, maxExhaustionRefLength)
		}
	}
	return nil
}

// isKnownFixability 报告 fixability 是否属于诊断允许值。
func isKnownFixability(value FixabilityClass) bool {
	switch value {
	case FixabilityCodeFixable, FixabilityNoChangeNeeded, FixabilityExternalDependency, FixabilityConfiguration,
		FixabilityData, FixabilityInfrastructure, FixabilityInsufficientEvidence,
		FixabilityUnsafeToAutomate:
		return true
	default:
		return false
	}
}
