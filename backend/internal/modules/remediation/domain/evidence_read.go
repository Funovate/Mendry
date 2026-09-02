package domain

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// evidence.read 的领域边界（D3）：模型只能按 evidence ID 重新读取当前
// remediation series 内有界页面的持久化受信任证据；不能选择 URL、路径、原始
// offset 或跨 series/incident 的证据。分页 cursor 是服务端所有的不透明 token，
// 模型不能构造或解释它。
const (
	// MaxEvidenceReadCursorBytes 是 opaque cursor 的字节上限；超过该上限的
	// cursor 在参数校验或 cursor 解码处被拒绝。
	MaxEvidenceReadCursorBytes = 512
	// MaxEvidenceReadPageBytes 是单页内容的字节上限，与 canonical evidence
	// 投影/模型可见 observation 的既有 64 KiB 上限一致。
	MaxEvidenceReadPageBytes = 64 << 10
)

var (
	// ErrEvidenceReadNotFound 表示目标证据不存在、不属于当前 run 的 series，
	// 或属于未来 attempt；调用方映射为稳定的 tool rejection。
	ErrEvidenceReadNotFound = errors.New("remediation evidence read target is not available")
	// ErrEvidenceReadCursorInvalid 表示 cursor 无法解码、完整性校验失败，或
	// 与目标证据行不绑定；模型需要从第一页重新读取。
	ErrEvidenceReadCursorInvalid = errors.New("remediation evidence read cursor is invalid")
	// ErrEvidenceReadCursorExpired 表示 cursor 已超过有效期；模型需要从第一页
	// 重新读取。
	ErrEvidenceReadCursorExpired = errors.New("remediation evidence read cursor is expired")
)

// EvidenceReadRequest 是 evidence.read 的有界输入。RunID 来自执行上下文
// （run catalog），模型只能提供 EvidenceID 与可选 Cursor；RunID 由 gateway
// 填充，模型无法选择运行身份。
type EvidenceReadRequest struct {
	RunID      string
	EvidenceID string
	Cursor     string
}

// Validate 校验 evidence.read 请求的边界：身份必填且长度受限，cursor 可选且
// 受字节上限约束。
func (r EvidenceReadRequest) Validate() error {
	if strings.TrimSpace(r.RunID) == "" || strings.TrimSpace(r.EvidenceID) == "" {
		return fmt.Errorf("evidence read run and evidence identity are required")
	}
	if len(r.RunID) > 128 || len(r.EvidenceID) > 128 {
		return fmt.Errorf("evidence read identity exceeds bounds")
	}
	if len(r.Cursor) > MaxEvidenceReadCursorBytes {
		return fmt.Errorf("evidence read cursor exceeds %d bytes", MaxEvidenceReadCursorBytes)
	}
	return nil
}

// EvidenceReadProvenance 是页面返回的有界 provenance 投影（D3）：sourceId 来自
// 持久化行的 source 归属，sourceAttempt 是收集该证据的 attempt 序号（pre-run
// 证据为 0）。完整 stored provenance jsonb 不进入模型页面。
type EvidenceReadProvenance struct {
	SourceID      string `json:"sourceId"`
	SourceAttempt int32  `json:"sourceAttempt"`
}

// EvidenceReadPage 是 evidence.read 的一页输出。Content 是持久化 payload 的
// 一页文本（UTF-8 rune 边界切片）；当整个 payload 小于页面上限时单页即完整
// 内容。NextCursor 只在 truncated 时出现，由服务端签名并绑定到该证据行。
type EvidenceReadPage struct {
	EvidenceID           string                 `json:"evidenceId"`
	Kind                 string                 `json:"kind"`
	StoredClassification EvidenceClassification `json:"storedClassification"`
	Provenance           EvidenceReadProvenance `json:"provenance"`
	ContentHash          string                 `json:"contentHash"`
	Content              string                 `json:"content"`
	NextCursor           string                 `json:"nextCursor,omitempty"`
	Truncated            bool                   `json:"truncated"`
	ByteCount            int64                  `json:"byteCount"`
}

// Validate 校验 evidence.read 页面输出的完整性边界：身份、分类、hash、字节数、
// content 上限，以及 truncated 与 nextCursor 的一致性。
func (p EvidenceReadPage) Validate() error {
	if strings.TrimSpace(p.EvidenceID) == "" || strings.TrimSpace(p.Kind) == "" || strings.TrimSpace(p.ContentHash) == "" {
		return fmt.Errorf("evidence read page identity is incomplete")
	}
	if err := ValidateEvidenceClassification(p.StoredClassification); err != nil {
		return err
	}
	if len(p.ContentHash) != 64 || p.ByteCount < 0 || p.ByteCount > MaxEvidenceReadPageBytes {
		return fmt.Errorf("evidence read page bounds are invalid")
	}
	if len(p.Content) > MaxEvidenceReadPageBytes {
		return fmt.Errorf("evidence read page content exceeds %d bytes", MaxEvidenceReadPageBytes)
	}
	if p.Truncated && strings.TrimSpace(p.NextCursor) == "" {
		return fmt.Errorf("truncated evidence read page must carry a next cursor")
	}
	if !p.Truncated && p.NextCursor != "" {
		return fmt.Errorf("complete evidence read page must not carry a next cursor")
	}
	if len(p.NextCursor) > MaxEvidenceReadCursorBytes {
		return fmt.Errorf("evidence read next cursor exceeds %d bytes", MaxEvidenceReadCursorBytes)
	}
	return nil
}

// EvidenceReadPort 是同 series 持久化证据的按 ID 分页读取边界（R9/R10，D3）。
// 实现必须校验 project/incident/series 归属、attempt 单调性、lifecycle 身份
// 与 cursor 完整性；证据行不复制、不重新归属到 child run。实现不得接受 URL、
// 路径、原始 offset 或跨 series 的证据 ID。
type EvidenceReadPort interface {
	ReadEvidence(context.Context, EvidenceReadRequest) (EvidenceReadPage, error)
}
