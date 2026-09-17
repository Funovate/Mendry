package domain

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// SubmittedDiagnosis 常量：D4 审计行的字段边界。这些值镜像
// remediation_decision 的既有约束（数组/建议边界一致），reasoning 额外
// 有界，保证一次模型轮次的输出不可能超限。
const (
	// maxSubmittedReasoningLength 是 reasoning 的 rune 上限；单次模型轮次
	// 输出（max_tokens=16384）不可能超过该值。
	maxSubmittedReasoningLength = 131072
	maxSubmittedContradictions  = 32
	maxSubmittedMissingEvidence = 32
	maxSubmittedCitations       = 64
	maxSubmittedNextAction      = 2000
	maxSubmittedCorrectionCount = 32
	maxSubmittedEvidenceID      = 255
)

// SubmittedDiagnosisCorrectionKind 是有界 correction 分类：空、证据引用
// 元数据修正或已接受的 exhaustion proof。它只表示"发生过纠正"，不携带模型
// 文本或 connector 细节。
type SubmittedDiagnosisCorrectionKind string

const (
	// SubmittedCorrectionNone 表示无需纠正。
	SubmittedCorrectionNone SubmittedDiagnosisCorrectionKind = ""
	// SubmittedCorrectionEvidence 表示 citation classification 元数据修正
	// （R7/AC5）：模型声明的分类与持久化权威分类不一致。
	SubmittedCorrectionEvidence SubmittedDiagnosisCorrectionKind = "evidence_correction"
	// SubmittedCorrectionExhaustion 表示已接受的 D6 exhaustion proof 决策。
	SubmittedCorrectionExhaustion SubmittedDiagnosisCorrectionKind = "exhaustion"
)

// IsKnown 报告 correction kind 是否属于允许值。
func (k SubmittedDiagnosisCorrectionKind) IsKnown() bool {
	switch k {
	case SubmittedCorrectionNone, SubmittedCorrectionEvidence, SubmittedCorrectionExhaustion:
		return true
	default:
		return false
	}
}

// SubmittedEvidenceClassification 是 correctedEvidence 的单个条目：evidence ID
// 加持久化权威分类（R7）。分类来自服务端记录，模型文本不能创建或修改它。
// json tag 与 adapter 的 jsonb 数组键一致（evidenceId/storedClassification），
// 保证 jsonb 往返不依赖大小写不敏感匹配。
type SubmittedEvidenceClassification struct {
	EvidenceID           string                 `json:"evidenceId"`
	StoredClassification EvidenceClassification `json:"storedClassification"`
}

// SubmittedDiagnosisCorrection 是有界的 correction metadata（D4）：足够证明
// 一次纠正发生过（kind/count/corrected + 权威分类列表），但不含原始模型轮次。
type SubmittedDiagnosisCorrection struct {
	Kind              SubmittedDiagnosisCorrectionKind
	CorrectedEvidence []SubmittedEvidenceClassification
	CorrectionCount   int
	Corrected         bool
}

// SubmittedDiagnosis 是模型提交诊断（evidence gate 之前的原 envelope）的
// 有界审计投影（D4/INC-2270）。它只持久化 envelope 中已有的结构化字段，
// 绝不包含 raw model turn、prompt 或 conversation 文本；public review 仍使用
// 接受的 remediation_decision，本记录只供 audit 证明提交/纠正发生。
type SubmittedDiagnosis struct {
	RunID                 string
	Sequence              int64
	Fixability            FixabilityClass
	Confidence            float64
	CausalReasoning       string
	Contradictions        []string
	MissingEvidence       []string
	EvidenceCitations     []string
	RecommendedNextAction string
	Correction            SubmittedDiagnosisCorrection
	// GateOutcome 是服务端 evidence gate 的判定：""（未应用 gate）、
	// planning_eligible 或 rejected；由 gate decision 派生，模型文本不可写。
	GateOutcome string
	// DecisionID 是本次提交产生的 accepted decision（存在时）；审计可据此
	// join submitted→accepted。纠正后未产生 decision 时为空。
	DecisionID  string
	SubmittedAt time.Time
}

// Validate 校验 SubmittedDiagnosis 的有界结构化字段。错误只引用字段名与
// 边界，不携带模型值；调用方（coordinator）不应把错误原文回喂模型。
func (d SubmittedDiagnosis) Validate() error {
	if !isKnownFixability(d.Fixability) {
		return fmt.Errorf("submitted diagnosis has unknown fixability %q", d.Fixability)
	}
	if d.Confidence < 0 || d.Confidence > 1 {
		return fmt.Errorf("submitted diagnosis confidence must be between 0 and 1")
	}
	if utf8.RuneCountInString(d.CausalReasoning) > maxSubmittedReasoningLength {
		return fmt.Errorf("submitted diagnosis reasoning exceeds %d characters", maxSubmittedReasoningLength)
	}
	if len(d.Contradictions) > maxSubmittedContradictions {
		return fmt.Errorf("submitted diagnosis contradictions exceed %d entries", maxSubmittedContradictions)
	}
	if len(d.MissingEvidence) > maxSubmittedMissingEvidence {
		return fmt.Errorf("submitted diagnosis missing evidence exceeds %d entries", maxSubmittedMissingEvidence)
	}
	if len(d.EvidenceCitations) > maxSubmittedCitations {
		return fmt.Errorf("submitted diagnosis evidence citations exceed %d entries", maxSubmittedCitations)
	}
	if utf8.RuneCountInString(d.RecommendedNextAction) > maxSubmittedNextAction {
		return fmt.Errorf("submitted diagnosis next action exceeds %d characters", maxSubmittedNextAction)
	}
	if !d.Correction.Kind.IsKnown() {
		return fmt.Errorf("submitted diagnosis has unknown correction kind %q", d.Correction.Kind)
	}
	if len(d.Correction.CorrectedEvidence) > maxSubmittedCorrectionCount {
		return fmt.Errorf("submitted diagnosis correction evidence exceeds %d entries", maxSubmittedCorrectionCount)
	}
	for _, item := range d.Correction.CorrectedEvidence {
		id := strings.TrimSpace(item.EvidenceID)
		if id == "" || utf8.RuneCountInString(id) > maxSubmittedEvidenceID {
			return fmt.Errorf("submitted diagnosis correction evidence id is invalid")
		}
		if err := ValidateEvidenceClassification(item.StoredClassification); err != nil {
			return fmt.Errorf("submitted diagnosis correction classification: %w", err)
		}
	}
	if d.Correction.CorrectionCount < 0 || d.Correction.CorrectionCount > maxSubmittedCorrectionCount {
		return fmt.Errorf("submitted diagnosis correction count is out of bounds")
	}
	switch d.Correction.Kind {
	case SubmittedCorrectionNone:
		if d.Correction.Corrected || d.Correction.CorrectionCount != 0 || len(d.Correction.CorrectedEvidence) != 0 {
			return fmt.Errorf("submitted diagnosis without correction kind must have empty correction metadata")
		}
	case SubmittedCorrectionEvidence:
		if !d.Correction.Corrected || len(d.Correction.CorrectedEvidence) == 0 ||
			d.Correction.CorrectionCount != len(d.Correction.CorrectedEvidence) {
			return fmt.Errorf("submitted diagnosis evidence correction count must match non-empty evidence metadata")
		}
	case SubmittedCorrectionExhaustion:
		if !d.Correction.Corrected || d.Correction.CorrectionCount != 1 || len(d.Correction.CorrectedEvidence) != 0 {
			return fmt.Errorf("submitted diagnosis exhaustion correction must have count one and no evidence metadata")
		}
	}
	switch d.GateOutcome {
	case "", "planning_eligible", "rejected":
	default:
		return fmt.Errorf("submitted diagnosis has unknown gate outcome %q", d.GateOutcome)
	}
	if d.DecisionID != "" && utf8.RuneCountInString(d.DecisionID) > 64 {
		return fmt.Errorf("submitted diagnosis decision id is out of bounds")
	}
	return nil
}

// SubmittedDiagnosisStore 是 D4 提交诊断审计持久化的 companion port。它独立
// 于冻结的 RunStore，但 coordinator 要求生产 store 实现该 capability；缺失或
// 写入失败属于 persistence/consistency blocker，不能静默跳过。实现不得暴露
// 数据库或 HTTP 类型。
type SubmittedDiagnosisStore interface {
	AppendSubmittedDiagnosis(ctx context.Context, runID string, d SubmittedDiagnosis) error
	ListSubmittedDiagnoses(ctx context.Context, runID string) ([]SubmittedDiagnosis, error)
	// LatestDecisionID 返回 run 最近一次 accepted decision 的 ID；尚无
	// decision 时返回空字符串。冻结的 RunStore.AppendDecision 不返回创建的
	// decision ID，审计链在 append 后用它解析 submitted→accepted join。
	LatestDecisionID(ctx context.Context, runID string) (string, error)
}
