package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"fixthe/backend/internal/modules/remediation/adapter/postgres/remediationdb"
	"fixthe/backend/internal/modules/remediation/domain"
)

// CheckpointStore 实现 domain.CheckpointStore：immutable event append 与 latest
// snapshot 更新在同一事务内原子完成，加载时校验 run/series/context 身份、schema、
// canonical hash 与 sequence-backed-by-event 不变量。sequence 由存储分配
// （max+1），并发 append 撞 sequence 时返回 ErrCheckpointConflict 供调用方重试。
type CheckpointStore struct {
	db transactor
}

const maxCheckpointEventsListed = 64

// NewCheckpointStore 用进程 PostgreSQL 连接构造 CheckpointStore。
func NewCheckpointStore(database transactor) (*CheckpointStore, error) {
	if database == nil {
		return nil, fmt.Errorf("remediation database is required")
	}
	return &CheckpointStore{db: database}, nil
}

// Compile-time assertion that the adapter satisfies the checkpoint port.
var _ domain.CheckpointStore = (*CheckpointStore)(nil)

// AppendCheckpoint 在单个事务内追加 immutable event 并原子更新 latest snapshot。
// checkpoint 的 run/series/context 身份必须与持久化的 run 一致；sequence 由存储
// 分配并 stamp 进 checkpoint 后再计算 canonical hash，因此 hash 覆盖实际序列。
func (s *CheckpointStore) AppendCheckpoint(ctx context.Context, runID string, checkpoint domain.WorkingMemoryCheckpointV1) (domain.CheckpointSnapshot, error) {
	if err := checkpoint.Validate(); err != nil {
		return domain.CheckpointSnapshot{}, fmt.Errorf("%w: %v", domain.ErrCheckpointInvalid, err)
	}
	rid, err := parseRunID(runID)
	if err != nil {
		return domain.CheckpointSnapshot{}, err
	}
	// RunID 在进入事务前按字符串比对 fail closed：与 series/context 身份检查
	// 一致，payload 声称的 run 必须就是写入目标 run。
	if checkpoint.RunID != runID {
		return domain.CheckpointSnapshot{}, fmt.Errorf("%w: checkpoint run identity does not match run", domain.ErrCheckpointInvalid)
	}

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.CheckpointSnapshot{}, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	q := remediationdb.New(tx)
	run, series, err := loadCheckpointRunIdentity(ctx, q, rid)
	if err != nil {
		return domain.CheckpointSnapshot{}, err
	}
	if uuidString(series.ID) != checkpoint.SeriesID {
		return domain.CheckpointSnapshot{}, fmt.Errorf("%w: checkpoint series identity does not match run", domain.ErrCheckpointInvalid)
	}
	if run.ContextVersion != checkpoint.ContextVersion {
		return domain.CheckpointSnapshot{}, fmt.Errorf("%w: checkpoint context version does not match run", domain.ErrCheckpointInvalid)
	}
	if run.Version != checkpoint.ObservedRunVersion {
		return domain.CheckpointSnapshot{}, fmt.Errorf("%w: checkpoint observed run version does not match durable run", domain.ErrCheckpointInvalid)
	}
	if !checkpointPhaseMatchesRun(checkpoint.Phase, run.State) {
		return domain.CheckpointSnapshot{}, fmt.Errorf("%w: checkpoint phase does not match durable run state", domain.ErrCheckpointInvalid)
	}

	latest, err := q.GetRemediationCheckpointLatestSequence(ctx, rid)
	if err != nil {
		return domain.CheckpointSnapshot{}, fmt.Errorf("get checkpoint latest sequence: %w", err)
	}
	stamped, err := checkpoint.WithSequence(latest + 1)
	if err != nil {
		return domain.CheckpointSnapshot{}, err
	}
	payload, err := stamped.CanonicalEncode()
	if err != nil {
		return domain.CheckpointSnapshot{}, err
	}
	sum := sha256.Sum256(payload)
	hash := hex.EncodeToString(sum[:])

	_, err = q.CreateRemediationCheckpointEvent(ctx, remediationdb.CreateRemediationCheckpointEventParams{
		RunID:         rid,
		Sequence:      stamped.Sequence,
		TriggerReason: stamped.Reason,
		Payload:       payload,
		ContentHash:   hash,
	})
	if uniqueViolation(err) {
		// 并发 append 撞同一 sequence：不覆盖既有事件，调用方可重试。
		return domain.CheckpointSnapshot{}, domain.ErrCheckpointConflict
	}
	if err != nil {
		return domain.CheckpointSnapshot{}, fmt.Errorf("create checkpoint event: %w", err)
	}

	snapshot, err := q.UpsertRemediationWorkingMemory(ctx, remediationdb.UpsertRemediationWorkingMemoryParams{
		RunID:              rid,
		Sequence:           stamped.Sequence,
		ContextVersion:     stamped.ContextVersion,
		ObservedRunVersion: stamped.ObservedRunVersion,
		Phase:              stamped.Phase,
		ContentHash:        hash,
	})
	if err != nil {
		return domain.CheckpointSnapshot{}, fmt.Errorf("upsert working memory snapshot: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.CheckpointSnapshot{}, fmt.Errorf("commit transaction: %w", err)
	}
	return mapCheckpointSnapshot(snapshot, stamped, run)
}

// LoadLatestCheckpoint 读取并验证 latest snapshot：snapshot sequence 必须有
// backing event，payload 解码后必须通过 schema/身份校验，重算的 canonical hash
// 必须与 snapshot 的 content_hash 一致。任何不变量失败都返回 ErrCheckpointCorrupt。
func (s *CheckpointStore) LoadLatestCheckpoint(ctx context.Context, runID string) (domain.CheckpointSnapshot, error) {
	rid, err := parseRunID(runID)
	if err != nil {
		return domain.CheckpointSnapshot{}, err
	}
	q := remediationdb.New(s.db)

	snapshot, err := q.GetRemediationWorkingMemory(ctx, rid)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CheckpointSnapshot{}, domain.ErrCheckpointNotFound
	}
	if err != nil {
		return domain.CheckpointSnapshot{}, fmt.Errorf("get working memory snapshot: %w", err)
	}

	// FK 已保证 snapshot sequence 有 backing event；仍显式读取并比对 hash，
	// 让加载路径对约束层之外的任何不一致 fail closed。
	event, err := q.GetRemediationCheckpointEvent(ctx, remediationdb.GetRemediationCheckpointEventParams{
		RunID:    rid,
		Sequence: snapshot.Sequence,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CheckpointSnapshot{}, fmt.Errorf("%w: snapshot sequence %d is not backed by an event", domain.ErrCheckpointCorrupt, snapshot.Sequence)
	}
	if err != nil {
		return domain.CheckpointSnapshot{}, fmt.Errorf("get checkpoint backing event: %w", err)
	}
	if event.ContentHash != snapshot.ContentHash {
		return domain.CheckpointSnapshot{}, fmt.Errorf("%w: snapshot and event content hashes disagree", domain.ErrCheckpointCorrupt)
	}

	checkpoint, err := decodeCheckpoint(event.Payload)
	if err != nil {
		return domain.CheckpointSnapshot{}, err
	}
	run, series, err := loadCheckpointRunIdentity(ctx, q, rid)
	if err != nil {
		return domain.CheckpointSnapshot{}, err
	}
	if checkpoint.RunID != runID || uuidString(series.ID) != checkpoint.SeriesID || run.ContextVersion != checkpoint.ContextVersion {
		return domain.CheckpointSnapshot{}, fmt.Errorf("%w: checkpoint identity does not match run", domain.ErrCheckpointCorrupt)
	}
	if checkpoint.ObservedRunVersion > run.Version {
		return domain.CheckpointSnapshot{}, fmt.Errorf("%w: checkpoint run version is ahead of durable run", domain.ErrCheckpointCorrupt)
	}
	if checkpoint.ObservedRunVersion == run.Version && !checkpointPhaseMatchesRun(checkpoint.Phase, run.State) {
		return domain.CheckpointSnapshot{}, fmt.Errorf("%w: checkpoint phase conflicts with durable run state", domain.ErrCheckpointCorrupt)
	}
	if checkpoint.Sequence != snapshot.Sequence {
		return domain.CheckpointSnapshot{}, fmt.Errorf("%w: checkpoint sequence %d does not match snapshot %d", domain.ErrCheckpointCorrupt, checkpoint.Sequence, snapshot.Sequence)
	}
	if checkpoint.ObservedRunVersion != snapshot.ObservedRunVersion || checkpoint.Phase != snapshot.Phase {
		return domain.CheckpointSnapshot{}, fmt.Errorf("%w: checkpoint projection metadata disagrees with its event", domain.ErrCheckpointCorrupt)
	}
	canonical, err := checkpoint.CanonicalEncode()
	if err != nil {
		return domain.CheckpointSnapshot{}, err
	}
	sum := sha256.Sum256(canonical)
	if hex.EncodeToString(sum[:]) != snapshot.ContentHash {
		return domain.CheckpointSnapshot{}, fmt.Errorf("%w: checkpoint content hash mismatch", domain.ErrCheckpointCorrupt)
	}
	return mapCheckpointSnapshot(snapshot, checkpoint, run)
}

// ListCheckpointEvents 按 sequence 降序返回有界数量的 immutable events 供 audit。
// 每个 event 都重新解码并校验 canonical hash，任何不一致都 fail closed。
func (s *CheckpointStore) ListCheckpointEvents(ctx context.Context, runID string, limit int) ([]domain.CheckpointEvent, error) {
	rid, err := parseRunID(runID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > maxCheckpointEventsListed {
		limit = maxCheckpointEventsListed
	}
	rows, err := remediationdb.New(s.db).ListRemediationCheckpointEvents(ctx, remediationdb.ListRemediationCheckpointEventsParams{
		RunID:       rid,
		ResultLimit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list checkpoint events: %w", err)
	}
	events := make([]domain.CheckpointEvent, 0, len(rows))
	for _, row := range rows {
		checkpoint, decodeErr := decodeCheckpoint(row.Payload)
		if decodeErr != nil {
			return nil, decodeErr
		}
		canonical, encodeErr := checkpoint.CanonicalEncode()
		if encodeErr != nil {
			return nil, encodeErr
		}
		sum := sha256.Sum256(canonical)
		if hex.EncodeToString(sum[:]) != row.ContentHash || !row.ID.Valid || !row.CreatedAt.Valid {
			return nil, fmt.Errorf("%w: checkpoint event %d has an invalid hash or generated values", domain.ErrCheckpointCorrupt, row.Sequence)
		}
		// audit 列表同样 fail closed：payload 声称的 run 必须等于查询目标 run。
		if checkpoint.RunID != runID {
			return nil, fmt.Errorf("%w: checkpoint event %d run identity mismatch", domain.ErrCheckpointCorrupt, row.Sequence)
		}
		events = append(events, domain.CheckpointEvent{
			EventID:       uuidString(row.ID),
			RunID:         uuidString(row.RunID),
			Sequence:      row.Sequence,
			TriggerReason: row.TriggerReason,
			ContentHash:   row.ContentHash,
			CreatedAt:     row.CreatedAt.Time.UTC(),
			Checkpoint:    checkpoint,
		})
	}
	return events, nil
}

// loadCheckpointRunIdentity 读取 run 与其 series，供身份校验使用。
func loadCheckpointRunIdentity(ctx context.Context, q *remediationdb.Queries, rid pgtype.UUID) (remediationdb.RemediationRun, remediationdb.RemediationSeries, error) {
	run, err := q.GetRemediationRun(ctx, rid)
	if errors.Is(err, pgx.ErrNoRows) {
		return remediationdb.RemediationRun{}, remediationdb.RemediationSeries{}, fmt.Errorf("checkpoint run not found: %w", err)
	}
	if err != nil {
		return remediationdb.RemediationRun{}, remediationdb.RemediationSeries{}, fmt.Errorf("get checkpoint run: %w", err)
	}
	if !run.SeriesID.Valid {
		return remediationdb.RemediationRun{}, remediationdb.RemediationSeries{}, fmt.Errorf("checkpoint run has invalid generated values")
	}
	series, err := q.GetRemediationSeriesByID(ctx, run.SeriesID)
	if err != nil {
		return remediationdb.RemediationRun{}, remediationdb.RemediationSeries{}, fmt.Errorf("get checkpoint run series: %w", err)
	}
	return run, series, nil
}

// decodeCheckpoint 有界解码事件 payload：先按字节上限拒绝，再解码并做 schema 校验。
func decodeCheckpoint(payload []byte) (domain.WorkingMemoryCheckpointV1, error) {
	if len(payload) > domain.MaxCheckpointPayloadBytes {
		return domain.WorkingMemoryCheckpointV1{}, fmt.Errorf("%w: checkpoint payload exceeds %d bytes", domain.ErrCheckpointCorrupt, domain.MaxCheckpointPayloadBytes)
	}
	var checkpoint domain.WorkingMemoryCheckpointV1
	if err := json.Unmarshal(payload, &checkpoint); err != nil {
		return domain.WorkingMemoryCheckpointV1{}, fmt.Errorf("decode checkpoint payload: %w", err)
	}
	if err := checkpoint.Validate(); err != nil {
		return domain.WorkingMemoryCheckpointV1{}, err
	}
	return checkpoint, nil
}

func mapCheckpointSnapshot(row remediationdb.RemediationWorkingMemory, checkpoint domain.WorkingMemoryCheckpointV1, run remediationdb.RemediationRun) (domain.CheckpointSnapshot, error) {
	if !row.UpdatedAt.Valid {
		return domain.CheckpointSnapshot{}, fmt.Errorf("checkpoint snapshot row has invalid generated values")
	}
	return domain.CheckpointSnapshot{
		RunID:              uuidString(row.RunID),
		SeriesID:           checkpoint.SeriesID,
		ContextVersion:     row.ContextVersion,
		ObservedRunVersion: row.ObservedRunVersion,
		DurableRunVersion:  run.Version,
		DurableRunState:    domain.RunState(run.State),
		NeedsRebuild:       row.ObservedRunVersion < run.Version,
		Sequence:           row.Sequence,
		Phase:              row.Phase,
		ContentHash:        row.ContentHash,
		UpdatedAt:          row.UpdatedAt.Time.UTC(),
		Checkpoint:         checkpoint,
	}, nil
}

func checkpointPhaseMatchesRun(phase, runState string) bool {
	state := domain.RunState(runState)
	if state == domain.RunStateQueued || state == domain.RunStatePreparingContext {
		return phase == string(domain.RunStateDiagnosing)
	}
	budgetPhase, ok := domain.BudgetPhaseFor(state)
	return ok && phase == string(budgetPhase)
}
