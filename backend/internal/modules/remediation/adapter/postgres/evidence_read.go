package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"mendry/backend/internal/modules/remediation/adapter/postgres/remediationdb"
	"mendry/backend/internal/modules/remediation/domain"
)

// evidenceReadCursorTTL 是 evidence.read cursor 的有效期：不透明、绑定证据行、
// 数分钟内过期，避免模型长期复用旧分页状态。
const evidenceReadCursorTTL = 5 * time.Minute

// Compile-time assertion that the adapter satisfies the evidence read port.
var _ domain.EvidenceReadPort = (*RunStore)(nil)

// ReadEvidence 按 evidence ID 读取当前 run series 内一页持久化证据（D3）。
// 归属校验由 SQL 完成：project/incident 通过 incident 连接固定，series 通过
// run 连接固定，attempt 单调性通过 EXISTS 子查询保证；pre-run 证据
// （run_id IS NULL，如 normalized_alert）始终可见。cursor 解码失败或过期时
// 返回 domain 错误，由 gateway 映射为稳定 tool rejection。证据行从不复制或
// 重新归属到 child run。
func (s *RunStore) ReadEvidence(ctx context.Context, request domain.EvidenceReadRequest) (domain.EvidenceReadPage, error) {
	if err := request.Validate(); err != nil {
		return domain.EvidenceReadPage{}, err
	}
	rid, err := parseRunID(request.RunID)
	if err != nil {
		return domain.EvidenceReadPage{}, err
	}
	evidenceUUID, err := uuid.Parse(request.EvidenceID)
	if err != nil {
		// 非 UUID 的 evidence ID 与未知 ID 一样不可解析，保持同一个稳定错误。
		return domain.EvidenceReadPage{}, domain.ErrEvidenceReadNotFound
	}

	row, err := remediationdb.New(s.db).GetRemediationEvidencePage(ctx, remediationdb.GetRemediationEvidencePageParams{
		RunID:      rid,
		EvidenceID: pgtype.UUID{Bytes: evidenceUUID, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.EvidenceReadPage{}, domain.ErrEvidenceReadNotFound
	}
	if err != nil {
		return domain.EvidenceReadPage{}, fmt.Errorf("get remediation evidence page: %w", err)
	}
	if !row.ID.Valid || !row.SourceID.Valid || !row.CreatedAt.Valid || !json.Valid(row.Payload) {
		return domain.EvidenceReadPage{}, fmt.Errorf("remediation evidence page row has invalid generated values")
	}

	// Cursor authority stays on the server. The model receives only a random
	// capability; the stored digest binds it to this run/evidence/hash/offset.
	offset, err := s.resolveEvidenceReadCursor(ctx, rid, row.ID, row.ContentHash, request.Cursor, time.Now().UTC())
	if err != nil {
		return domain.EvidenceReadPage{}, err
	}

	content := []byte(row.Payload)
	if offset < 0 {
		return domain.EvidenceReadPage{}, domain.ErrEvidenceReadCursorInvalid
	}
	start := offset
	if start > int64(len(content)) {
		start = int64(len(content))
	}
	// 防御性对齐 rune 边界：offset 由服务端产生，但拒绝任何可能切碎 UTF-8 的输入。
	for start > 0 && start < int64(len(content)) && !utf8.RuneStart(content[start]) {
		start--
	}
	end := start + domain.MaxEvidenceReadPageBytes
	if end > int64(len(content)) {
		end = int64(len(content))
	}
	for end > start && end < int64(len(content)) && !utf8.RuneStart(content[end]) {
		end--
	}

	page := domain.EvidenceReadPage{
		EvidenceID:           uuidString(row.ID),
		Kind:                 row.EvidenceKind,
		StoredClassification: domain.EvidenceClassification(row.Classification),
		Provenance: domain.EvidenceReadProvenance{
			SourceID:      uuidString(row.SourceID),
			SourceAttempt: row.SourceAttempt,
		},
		ContentHash: row.ContentHash,
		Content:     string(content[start:end]),
		Truncated:   end < int64(len(content)),
		ByteCount:   end - start,
	}
	if page.Truncated {
		page.NextCursor, err = s.createEvidenceReadCursor(ctx, rid, row.ID, row.ContentHash, end, time.Now().UTC())
		if err != nil {
			return domain.EvidenceReadPage{}, err
		}
	}
	if err := page.Validate(); err != nil {
		return domain.EvidenceReadPage{}, fmt.Errorf("validate remediation evidence read page: %w", err)
	}
	return page, nil
}

// createEvidenceReadCursor mints an unstructured 256-bit capability and stores
// only its SHA-256 digest. Offset and expiry never enter model-visible bytes.
func (s *RunStore) createEvidenceReadCursor(
	ctx context.Context,
	runID, evidenceID pgtype.UUID,
	contentHash string,
	offset int64,
	now time.Time,
) (string, error) {
	if offset <= 0 {
		return "", domain.ErrEvidenceReadCursorInvalid
	}
	token, digest, err := newEvidenceReadCursorToken()
	if err != nil {
		return "", err
	}
	q := remediationdb.New(s.db)
	if err := q.DeleteExpiredRemediationEvidenceReadCursors(ctx); err != nil {
		return "", fmt.Errorf("delete expired evidence read cursors: %w", err)
	}
	err = q.CreateRemediationEvidenceReadCursor(ctx, remediationdb.CreateRemediationEvidenceReadCursorParams{
		TokenHash:   digest,
		RunID:       runID,
		EvidenceID:  evidenceID,
		ContentHash: contentHash,
		ByteOffset:  offset,
		ExpiresAt:   pgtype.Timestamptz{Time: now.Add(evidenceReadCursorTTL), Valid: true},
	})
	if err != nil {
		return "", fmt.Errorf("persist evidence read cursor: %w", err)
	}
	return token, nil
}

func newEvidenceReadCursorToken() (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("generate evidence read cursor: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(token))
	return token, digest[:], nil
}

// resolveEvidenceReadCursor resolves a random capability against server-side
// bindings. Empty token means the first page. Unknown/cross-bound tokens fail
// identically, so evidence existence and cursor state are never disclosed.
func (s *RunStore) resolveEvidenceReadCursor(
	ctx context.Context,
	runID, evidenceID pgtype.UUID,
	contentHash, token string,
	now time.Time,
) (int64, error) {
	if strings.TrimSpace(token) == "" {
		return 0, nil
	}
	if len(token) > domain.MaxEvidenceReadCursorBytes {
		return 0, domain.ErrEvidenceReadCursorInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		return 0, domain.ErrEvidenceReadCursorInvalid
	}
	digest := sha256.Sum256([]byte(token))
	cursor, err := remediationdb.New(s.db).GetRemediationEvidenceReadCursor(ctx, remediationdb.GetRemediationEvidenceReadCursorParams{
		TokenHash:   digest[:],
		RunID:       runID,
		EvidenceID:  evidenceID,
		ContentHash: contentHash,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, domain.ErrEvidenceReadCursorInvalid
	}
	if err != nil {
		return 0, fmt.Errorf("resolve evidence read cursor: %w", err)
	}
	if !cursor.ExpiresAt.Valid || now.After(cursor.ExpiresAt.Time) {
		return 0, domain.ErrEvidenceReadCursorExpired
	}
	if cursor.ByteOffset <= 0 {
		return 0, domain.ErrEvidenceReadCursorInvalid
	}
	return cursor.ByteOffset, nil
}
