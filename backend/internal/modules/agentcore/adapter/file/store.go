package file

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"mendry/backend/internal/modules/agentcore/domain"
)

const (
	// SchemaVersion 是 local run snapshot 的磁盘格式版本。
	SchemaVersion = 1
	StateFileName = "state.json"
	lockFileName  = ".lock"
	tempFileName  = ".state.tmp"

	maxSnapshotBytes = 16 << 20
	maxRecords       = 4096
	maxTextBytes     = 64 << 10
	maxIDBytes       = 256
)

// Identity 固定 local run 与 trusted composition/event 的绑定，resume 时必须完全匹配。
type Identity struct {
	RunID             string `json:"runId"`
	EventID           string `json:"eventId"`
	ConfigurationHash string `json:"configurationHash"`
	EventHash         string `json:"eventHash"`
}

// Snapshot 是可供 inspect/render 使用的完整、copy-safe durable 投影。
type Snapshot struct {
	SchemaVersion int                       `json:"schemaVersion"`
	Revision      int64                     `json:"revision"`
	Identity      Identity                  `json:"identity"`
	Run           domain.Run                `json:"run"`
	Invocations   []domain.Invocation       `json:"invocations"`
	Results       []domain.InvocationResult `json:"results"`
	Artifacts     []domain.Artifact         `json:"artifacts"`
	MessageGroups [][]domain.ModelMessage   `json:"messageGroups"`
}

// Store 持有一个 run directory 的排他进程锁；Close 前其他进程不能打开它。
type Store struct {
	dir      string
	lockFile *os.File
	mu       sync.Mutex
	snapshot Snapshot
	closed   bool
}

// Create 创建私有 run directory、取得排他锁并 durable 写入初始快照。
func Create(ctx context.Context, dir string, identity Identity, run domain.Run) (*Store, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if err := validateIdentity(identity); err != nil {
		return nil, err
	}
	if err := validateRun(run, identity); err != nil {
		return nil, fmt.Errorf("initial run is invalid: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create run directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("set run directory permissions: %w", err)
	}

	store, err := lock(dir)
	if err != nil {
		return nil, err
	}
	statePath := filepath.Join(dir, StateFileName)
	tempPath := filepath.Join(dir, tempFileName)
	if _, err := os.Stat(statePath); err == nil {
		_ = store.Close()
		return nil, errors.New("run state already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		_ = store.Close()
		return nil, fmt.Errorf("inspect existing run state: %w", err)
	}
	if _, err := os.Stat(tempPath); err == nil {
		_ = store.Close()
		return nil, errors.New("an interrupted run-state write requires reconciliation")
	} else if !errors.Is(err, os.ErrNotExist) {
		_ = store.Close()
		return nil, fmt.Errorf("inspect interrupted run state: %w", err)
	}

	store.snapshot = Snapshot{
		SchemaVersion: SchemaVersion,
		Revision:      1,
		Identity:      identity,
		Run:           cloneRun(run),
	}
	if err := validateSnapshot(store.snapshot); err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("validate initial run state: %w", err)
	}
	if err := store.persist(); err != nil {
		_ = store.Close()
		return nil, err
	}
	return store, nil
}

// Open 取得排他锁并严格读取现有快照；expected 非零字段用于拒绝配置或事件漂移。
func Open(ctx context.Context, dir string, expected Identity) (*Store, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	store, err := lock(dir)
	if err != nil {
		return nil, err
	}
	encoded, err := readBounded(filepath.Join(dir, StateFileName), maxSnapshotBytes)
	if err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("read run state: %w", err)
	}
	if err := strictDecode(encoded, &store.snapshot); err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("decode run state: %w", err)
	}
	if err := validateSnapshot(store.snapshot); err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("validate run state: %w", err)
	}
	if err := matchIdentity(store.snapshot.Identity, expected); err != nil {
		_ = store.Close()
		return nil, err
	}
	// 原子替换失败前遗留的 temp 只代表未提交的候选快照。旧 state 已经是
	// 最后一个完整边界，删除候选而不是重放它，避免恢复路径产生盲目副作用。
	if err := removeInterruptedTemp(filepath.Join(dir, tempFileName), dir); err != nil {
		_ = store.Close()
		return nil, err
	}
	return store, nil
}

// Inspect 严格读取快照但不取得运行锁，供只读 operator inspection 使用。
func Inspect(ctx context.Context, dir string) (Snapshot, error) {
	if err := contextError(ctx); err != nil {
		return Snapshot{}, err
	}
	encoded, err := readBounded(filepath.Join(dir, StateFileName), maxSnapshotBytes)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read run state: %w", err)
	}
	var snapshot Snapshot
	if err := strictDecode(encoded, &snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("decode run state: %w", err)
	}
	if err := validateSnapshot(snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("validate run state: %w", err)
	}
	return cloneSnapshot(snapshot), nil
}

func lock(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("run directory is required")
	}
	lockFile, err := os.OpenFile(filepath.Join(dir, lockFileName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open run lock: %w", err)
	}
	if err := lockFile.Chmod(0o600); err != nil {
		_ = lockFile.Close()
		return nil, fmt.Errorf("set run lock permissions: %w", err)
	}
	// LOCK_NB 明确拒绝第二个 owner，避免两个进程各自基于旧快照写入。
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lockFile.Close()
		return nil, errors.New("run directory is locked by another process")
	}
	return &Store{dir: dir, lockFile: lockFile}, nil
}

// Close 释放 run directory 排他锁；可重复调用。
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var closeErr error
	if s.lockFile != nil {
		if err := syscall.Flock(int(s.lockFile.Fd()), syscall.LOCK_UN); err != nil {
			closeErr = err
		}
		if err := s.lockFile.Close(); closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

// Snapshot 返回当前 owner 内存中的 copy-safe 投影。
func (s *Store) Snapshot() (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureOpen(); err != nil {
		return Snapshot{}, err
	}
	return cloneSnapshot(s.snapshot), nil
}

// LoadRun 返回指定 run 的 copy-safe snapshot；错误的 run ID 不会被静默忽略。
func (s *Store) LoadRun(ctx context.Context, runID string) (domain.Run, error) {
	if err := contextError(ctx); err != nil {
		return domain.Run{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureOpen(); err != nil {
		return domain.Run{}, err
	}
	if runID != s.snapshot.Identity.RunID {
		return domain.Run{}, errors.New("run identity mismatch")
	}
	return cloneRun(s.snapshot.Run), nil
}

// SaveRun 保存 runner 的下一版本；profile/policy/input/budget limits 等 immutable 字段不能漂移。
func (s *Store) SaveRun(ctx context.Context, run domain.Run) error {
	return s.update(ctx, func(next *Snapshot) error {
		if run.ID != next.Identity.RunID || run.EventID != next.Identity.EventID {
			return errors.New("run identity cannot change")
		}
		if !sameImmutableRun(next.Run, run) {
			return errors.New("immutable run configuration cannot change")
		}
		if run.Version != next.Run.Version+1 {
			return fmt.Errorf("run version conflict: expected %d", next.Run.Version+1)
		}
		next.Run = cloneRun(run)
		return nil
	})
}

// ListInvocations 返回按 durable sequence 排序的调用意图。
func (s *Store) ListInvocations(ctx context.Context, runID string) ([]domain.Invocation, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureRun(ctx, runID); err != nil {
		return nil, err
	}
	return cloneInvocations(s.snapshot.Invocations), nil
}

// RecordInvocationIntent 在任何 executor side effect 前 durable 写入一次 pending/rejected intent。
func (s *Store) RecordInvocationIntent(ctx context.Context, invocation domain.Invocation) error {
	return s.update(ctx, func(next *Snapshot) error {
		if err := validateInvocation(invocation, next.Identity.RunID); err != nil {
			return fmt.Errorf("invocation intent is invalid: %w", err)
		}
		if invocation.State != domain.InvocationPending && invocation.State != domain.InvocationRejected {
			return errors.New("invocation intent must be pending or rejected")
		}
		for _, current := range next.Invocations {
			if current.ID == invocation.ID || current.Sequence == invocation.Sequence {
				return errors.New("invocation intent conflicts")
			}
		}
		next.Invocations = append(next.Invocations, cloneInvocation(invocation))
		sort.Slice(next.Invocations, func(i, j int) bool { return next.Invocations[i].Sequence < next.Invocations[j].Sequence })
		return nil
	})
}

// RecordInvocationResult 原子保存调用结果与同批 artifacts，并只解析 pending/unknown intent。
func (s *Store) RecordInvocationResult(ctx context.Context, result domain.InvocationResult, artifacts []domain.Artifact) error {
	return s.update(ctx, func(next *Snapshot) error {
		if err := validateInvocationResult(result); err != nil {
			return fmt.Errorf("invocation result is invalid: %w", err)
		}
		index := -1
		for i := range next.Invocations {
			if next.Invocations[i].ID == result.InvocationID && next.Invocations[i].Sequence == result.Sequence {
				index = i
				break
			}
		}
		if index < 0 {
			return errors.New("invocation result has no intent")
		}
		if next.Invocations[index].State != domain.InvocationPending && next.Invocations[index].State != domain.InvocationUnknown {
			return errors.New("invocation intent is already resolved")
		}
		for _, current := range next.Results {
			if current.InvocationID == result.InvocationID || current.Sequence == result.Sequence {
				return errors.New("invocation result conflicts")
			}
		}
		if err := validateArtifacts(next, artifacts, result.InvocationID); err != nil {
			return err
		}
		next.Invocations[index].State = result.State
		next.Invocations[index].OutputBytes = result.OutputBytes
		next.Results = append(next.Results, cloneResult(result))
		next.Artifacts = append(next.Artifacts, cloneArtifacts(artifacts)...)
		return nil
	})
}

// ListArtifacts 返回按写入顺序排列的 copy-safe artifact projection。
// ResolveInvocation records an operator-confirmed outcome for a pending or unknown invocation.
// It never executes the tool. The matching durable tool-call history, result, and waiting-to-running
// lifecycle transition are committed in one snapshot update.
func (s *Store) ResolveInvocation(ctx context.Context, invocationID string, state domain.InvocationState, code string, output any, completedAt time.Time) error {
	return s.update(ctx, func(next *Snapshot) error {
		if !validID(invocationID) {
			return errors.New("invocation identity is invalid")
		}
		if state != domain.InvocationSucceeded && state != domain.InvocationFailed {
			return errors.New("operator outcome must be succeeded or failed")
		}
		index := -1
		for i := range next.Invocations {
			if next.Invocations[i].ID == invocationID {
				index = i
				break
			}
		}
		if index < 0 {
			return errors.New("invocation was not found")
		}
		invocation := next.Invocations[index]
		if invocation.State != domain.InvocationPending && invocation.State != domain.InvocationUnknown {
			return errors.New("invocation is already resolved")
		}
		callID, ok := messageCallIDForSequence(next.MessageGroups, invocation.Sequence)
		if !ok {
			return errors.New("invocation has no matching durable tool call")
		}
		if code == "" {
			code = "operator_resolved"
		}
		if completedAt.IsZero() {
			completedAt = time.Now().UTC()
		}
		if !boundedText(code, maxIDBytes) {
			return errors.New("operator outcome code is invalid")
		}
		if output != nil {
			if err := boundedJSON(output, maxSnapshotBytes); err != nil {
				return fmt.Errorf("operator outcome output is invalid: %w", err)
			}
		}
		encoded, err := json.Marshal(map[string]any{"status": string(state), "code": code, "output": output, "manual": true})
		if err != nil || len(encoded) > maxTextBytes {
			return errors.New("operator outcome message exceeds the bound")
		}
		message := domain.ModelMessage{Role: "tool", ToolCallID: callID, Content: string(encoded)}
		if err := validateMessageAppend(next.MessageGroups, []domain.ModelMessage{message}); err != nil {
			return fmt.Errorf("operator outcome history is invalid: %w", err)
		}
		result := domain.InvocationResult{InvocationID: invocation.ID, Sequence: invocation.Sequence, State: state, Code: code, Output: output, OutputBytes: int64(len(encoded)), CompletedAt: completedAt}
		if err := validateInvocationResult(result); err != nil {
			return fmt.Errorf("operator outcome is invalid: %w", err)
		}
		for _, current := range next.Results {
			if current.InvocationID == result.InvocationID || current.Sequence == result.Sequence {
				return errors.New("invocation result conflicts")
			}
		}
		invocation.State = state
		invocation.OutputBytes = result.OutputBytes
		next.Invocations[index] = invocation
		next.Results = append(next.Results, cloneResult(result))
		next.MessageGroups = append(next.MessageGroups, []domain.ModelMessage{message})
		next.Run.Version++
		if !completedAt.IsZero() {
			next.Run.UpdatedAt = completedAt
		}
		if next.Run.State == domain.RunStateWaiting {
			next.Run.State = domain.RunStateRunning
			next.Run.ReasonCode = "operator_resolved"
		}
		return nil
	})
}

func messageCallIDForSequence(groups [][]domain.ModelMessage, sequence int64) (string, bool) {
	if sequence <= 0 {
		return "", false
	}
	var current int64
	for _, group := range groups {
		for _, message := range group {
			if message.Role != "assistant" {
				continue
			}
			for _, call := range message.ToolCalls {
				current++
				if current == sequence {
					return call.ID, call.ID != ""
				}
			}
		}
	}
	return "", false
}

func (s *Store) ListArtifacts(ctx context.Context, runID string) ([]domain.Artifact, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureRun(ctx, runID); err != nil {
		return nil, err
	}
	return cloneArtifacts(s.snapshot.Artifacts), nil
}

// AppendArtifacts 原子追加 model 或已绑定 invocation 的 artifacts。
func (s *Store) AppendArtifacts(ctx context.Context, runID string, artifacts []domain.Artifact) error {
	return s.update(ctx, func(next *Snapshot) error {
		if runID != next.Identity.RunID {
			return errors.New("artifact run identity mismatch")
		}
		if err := validateArtifacts(next, artifacts, ""); err != nil {
			return err
		}
		next.Artifacts = append(next.Artifacts, cloneArtifacts(artifacts)...)
		return nil
	})
}

// LoadMessages 返回持久化的扁平 provider-neutral history。
func (s *Store) LoadMessages(ctx context.Context, runID string) ([]domain.ModelMessage, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureRun(ctx, runID); err != nil {
		return nil, err
	}
	var messages []domain.ModelMessage
	for _, group := range s.snapshot.MessageGroups {
		messages = append(messages, cloneMessages(group)...)
	}
	return messages, nil
}

// AppendMessageGroup 原子追加一个 group，并保留 assistant tool call 与后续 tool result 的配对关系。
func (s *Store) AppendMessageGroup(ctx context.Context, runID string, messages []domain.ModelMessage) error {
	return s.update(ctx, func(next *Snapshot) error {
		if runID != next.Identity.RunID {
			return errors.New("message run identity mismatch")
		}
		if err := validateMessageAppend(next.MessageGroups, messages); err != nil {
			return err
		}
		next.MessageGroups = append(next.MessageGroups, cloneMessages(messages))
		return nil
	})
}

func (s *Store) update(ctx context.Context, change func(*Snapshot) error) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	next := cloneSnapshot(s.snapshot)
	if err := change(&next); err != nil {
		return err
	}
	next.Revision++
	if err := validateSnapshot(next); err != nil {
		return fmt.Errorf("validate next run state: %w", err)
	}
	previous := s.snapshot
	s.snapshot = next
	if err := s.persist(); err != nil {
		s.snapshot = previous
		return err
	}
	return nil
}

func (s *Store) persist() error {
	encoded, err := json.MarshalIndent(s.snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("encode run state: %w", err)
	}
	if len(encoded) > maxSnapshotBytes {
		return errors.New("run state exceeds snapshot bound")
	}
	encoded = append(encoded, '\n')

	temporary, err := os.OpenFile(filepath.Join(s.dir, tempFileName), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create temporary run state: %w", err)
	}
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporary.Name())
	}
	if err := temporary.Chmod(0o600); err != nil {
		cleanup()
		return fmt.Errorf("set temporary run state permissions: %w", err)
	}
	if err := writeAll(temporary, encoded); err != nil {
		cleanup()
		return fmt.Errorf("write temporary run state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync temporary run state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporary.Name())
		return fmt.Errorf("close temporary run state: %w", err)
	}
	if err := os.Rename(temporary.Name(), filepath.Join(s.dir, StateFileName)); err != nil {
		_ = os.Remove(temporary.Name())
		return fmt.Errorf("replace run state: %w", err)
	}
	// rename 后同步目录，确保名称更新本身跨掉电 durable。
	directory, err := os.Open(s.dir)
	if err != nil {
		return fmt.Errorf("open run directory for sync: %w", err)
	}
	if err := directory.Sync(); err != nil {
		_ = directory.Close()
		return fmt.Errorf("sync run directory: %w", err)
	}
	if err := directory.Close(); err != nil {
		return fmt.Errorf("close run directory: %w", err)
	}
	return nil
}

func (s *Store) ensureOpen() error {
	if s == nil || s.closed {
		return errors.New("run store is closed")
	}
	return nil
}

func (s *Store) ensureRun(ctx context.Context, runID string) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	if runID != s.snapshot.Identity.RunID {
		return errors.New("run identity mismatch")
	}
	return nil
}

func validateIdentity(identity Identity) error {
	if !validID(identity.RunID) || !validID(identity.EventID) {
		return errors.New("run identity is invalid")
	}
	if !validHash(identity.ConfigurationHash) || !validHash(identity.EventHash) {
		return errors.New("run identity hashes are invalid")
	}
	return nil
}

func matchIdentity(actual, expected Identity) error {
	checks := []struct {
		name string
		want string
		got  string
	}{
		{name: "run", want: expected.RunID, got: actual.RunID},
		{name: "event", want: expected.EventID, got: actual.EventID},
		{name: "configuration", want: expected.ConfigurationHash, got: actual.ConfigurationHash},
		{name: "event digest", want: expected.EventHash, got: actual.EventHash},
	}
	for _, check := range checks {
		if check.want != "" && check.want != check.got {
			return fmt.Errorf("%s identity mismatch", check.name)
		}
	}
	return nil
}

func validateSnapshot(snapshot Snapshot) error {
	if snapshot.SchemaVersion != SchemaVersion || snapshot.Revision <= 0 {
		return errors.New("unsupported state schema or revision")
	}
	if err := validateIdentity(snapshot.Identity); err != nil {
		return err
	}
	if err := validateRun(snapshot.Run, snapshot.Identity); err != nil {
		return fmt.Errorf("stored run is invalid: %w", err)
	}
	if len(snapshot.Invocations) > maxRecords || len(snapshot.Results) > maxRecords || len(snapshot.Artifacts) > maxRecords || totalMessages(snapshot.MessageGroups) > maxRecords {
		return errors.New("stored record count exceeds bound")
	}

	seenInvocations, seenSequences := map[string]bool{}, map[int64]bool{}
	for _, invocation := range snapshot.Invocations {
		if err := validateInvocation(invocation, snapshot.Run.ID); err != nil {
			return fmt.Errorf("stored invocation is invalid: %w", err)
		}
		if seenInvocations[invocation.ID] || seenSequences[invocation.Sequence] {
			return errors.New("stored invocation identity is duplicated")
		}
		seenInvocations[invocation.ID] = true
		seenSequences[invocation.Sequence] = true
	}
	seenResults := map[string]bool{}
	for _, result := range snapshot.Results {
		if err := validateInvocationResult(result); err != nil {
			return fmt.Errorf("stored invocation result is invalid: %w", err)
		}
		if !seenInvocations[result.InvocationID] || seenResults[result.InvocationID] {
			return errors.New("stored invocation result identity is invalid")
		}
		matched := false
		for _, invocation := range snapshot.Invocations {
			if invocation.ID != result.InvocationID {
				continue
			}
			matched = true
			if invocation.Sequence != result.Sequence || invocation.State != result.State {
				return errors.New("stored invocation result identity is invalid")
			}
		}
		if !matched {
			return errors.New("stored invocation result identity is invalid")
		}
		seenResults[result.InvocationID] = true
	}
	seenArtifacts := map[string]bool{}
	for _, artifact := range snapshot.Artifacts {
		if err := validateStoredArtifact(artifact, snapshot.Run.ID); err != nil {
			return fmt.Errorf("stored artifact is invalid: %w", err)
		}
		if seenArtifacts[artifact.ID] {
			return errors.New("stored artifact identity is duplicated")
		}
		if artifact.InvocationID != "" && !seenInvocations[artifact.InvocationID] {
			return errors.New("stored artifact invocation identity is invalid")
		}
		seenArtifacts[artifact.ID] = true
	}
	if err := validateAllMessages(snapshot.MessageGroups); err != nil {
		return err
	}
	return nil
}

func validateRun(run domain.Run, identity Identity) error {
	if !validID(run.ID) || run.ID != identity.RunID || !validID(run.EventID) || run.EventID != identity.EventID {
		return errors.New("stored run identity or version is invalid")
	}
	if run.Version <= 0 || !validRunState(run.State) {
		return errors.New("stored run version or state is invalid")
	}
	if !boundedText(run.Goal, 1<<20) || !boundedText(run.ProfileName, maxIDBytes) || !boundedText(run.ProfileVersion, maxIDBytes) || !boundedText(run.PolicyRef, maxIDBytes) || run.ConfigurationDigest != identity.ConfigurationHash || !validHash(run.ConfigurationDigest) {
		return errors.New("stored run configuration is invalid")
	}
	if run.Budget.Limits.MaxElapsed < 0 || run.Budget.Limits.MaxModelCalls < 0 || run.Budget.Limits.MaxToolCalls < 0 || run.Budget.Limits.MaxOutputBytes < 0 || run.Budget.Consumed.ModelCalls < 0 || run.Budget.Consumed.ToolCalls < 0 || run.Budget.Consumed.OutputBytes < 0 {
		return errors.New("stored run budget is invalid")
	}
	if len(run.Input) > 0 {
		if err := boundedJSON(run.Input, maxSnapshotBytes); err != nil {
			return fmt.Errorf("stored run input is invalid: %w", err)
		}
	}
	if len(run.Context) > 0 {
		if err := boundedJSON(run.Context, maxSnapshotBytes); err != nil {
			return fmt.Errorf("stored run context is invalid: %w", err)
		}
	}
	if run.Result != nil {
		if err := boundedJSON(run.Result, maxSnapshotBytes); err != nil {
			return fmt.Errorf("stored run result is invalid: %w", err)
		}
	}
	return nil
}

func sameImmutableRun(previous, next domain.Run) bool {
	if previous.ID != next.ID || previous.EventID != next.EventID || previous.Goal != next.Goal || previous.ProfileName != next.ProfileName || previous.ProfileVersion != next.ProfileVersion || previous.PolicyRef != next.PolicyRef || previous.ConfigurationDigest != next.ConfigurationDigest || previous.Budget.Limits != next.Budget.Limits {
		return false
	}
	if !canonicalJSONEqual(previous.Input, next.Input) || !canonicalJSONEqual(previous.Context, next.Context) {
		return false
	}
	// queued run 首次被 runner claim 时才会写 StartedAt；之后这个生命周期
	// 起点不可被重置，否则 elapsed budget 会在 resume 时被凭空延长。
	if !previous.StartedAt.IsZero() && !previous.StartedAt.Equal(next.StartedAt) {
		return false
	}
	return true
}

func validateInvocation(invocation domain.Invocation, runID string) error {
	if !validID(invocation.ID) || invocation.RunID != runID || invocation.Sequence <= 0 || !boundedText(invocation.ToolName, maxIDBytes) || !boundedText(invocation.ToolVersion, maxIDBytes) || !validEffect(invocation.Effect) || !validHash(invocation.ArgumentsDigest) || !validInvocationState(invocation.State) || invocation.OutputBytes < 0 {
		return errors.New("invocation fields are invalid")
	}
	if invocation.ProviderCallID != "" && !boundedText(invocation.ProviderCallID, maxIDBytes) || invocation.IdempotencyKey != "" && !boundedText(invocation.IdempotencyKey, maxIDBytes) {
		return errors.New("invocation metadata is invalid")
	}
	return nil
}

func validateInvocationResult(result domain.InvocationResult) error {
	if !validID(result.InvocationID) || result.Sequence <= 0 || !validInvocationState(result.State) || result.State == domain.InvocationPending || result.State == domain.InvocationRejected || !boundedText(result.Code, maxIDBytes) || result.OutputBytes < 0 {
		return errors.New("result fields are invalid")
	}
	if result.Output != nil {
		if err := boundedJSON(result.Output, maxSnapshotBytes); err != nil {
			return fmt.Errorf("result output is invalid: %w", err)
		}
	}
	return nil
}

func validateArtifacts(snapshot *Snapshot, artifacts []domain.Artifact, invocationID string) error {
	seen := make(map[string]bool, len(snapshot.Artifacts)+len(artifacts))
	for _, current := range snapshot.Artifacts {
		seen[current.ID] = true
	}
	knownInvocations := make(map[string]bool, len(snapshot.Invocations))
	for _, invocation := range snapshot.Invocations {
		knownInvocations[invocation.ID] = true
	}
	for _, artifact := range artifacts {
		if artifact.InvocationID != invocationID {
			return errors.New("artifact invocation identity is invalid")
		}
		if artifact.InvocationID != "" && !knownInvocations[artifact.InvocationID] {
			return errors.New("artifact invocation identity is invalid")
		}
		if err := validateStoredArtifact(artifact, snapshot.Run.ID); err != nil {
			return err
		}
		if seen[artifact.ID] {
			return errors.New("artifact identity is invalid")
		}
		seen[artifact.ID] = true
	}
	return nil
}

func validateStoredArtifact(artifact domain.Artifact, runID string) error {
	if !validID(artifact.ID) || artifact.RunID != runID {
		return errors.New("artifact identity is invalid")
	}
	if artifact.InvocationID != "" && !validID(artifact.InvocationID) {
		return errors.New("artifact invocation identity is invalid")
	}
	if err := artifact.Validate(); err != nil {
		return err
	}
	return nil
}

func validateAllMessages(groups [][]domain.ModelMessage) error {
	var accepted [][]domain.ModelMessage
	for _, group := range groups {
		if err := validateMessageAppend(accepted, group); err != nil {
			return fmt.Errorf("stored message history is invalid: %w", err)
		}
		accepted = append(accepted, cloneMessages(group))
	}
	return nil
}

func validateMessageAppend(existing [][]domain.ModelMessage, messages []domain.ModelMessage) error {
	if len(messages) == 0 || len(messages) > maxRecords {
		return errors.New("message group is invalid")
	}
	pending, seen, err := messageState(existing)
	if err != nil {
		return err
	}
	if messages[0].Role == "tool" {
		for _, message := range messages {
			if message.Role != "tool" {
				return errors.New("tool result group contains a non-tool message")
			}
			if err := collectToolResult(message, pending); err != nil {
				return err
			}
		}
		return nil
	}
	if len(pending) != 0 {
		return errors.New("cannot append a model group while tool calls are pending")
	}
	if messages[0].Role != "user" || len(messages) != 2 || messages[1].Role != "assistant" {
		return errors.New("model message group must contain user and assistant messages")
	}
	if messages[0].ToolCallID != "" || len(messages[0].ToolCalls) != 0 {
		return errors.New("user message cannot contain tool metadata")
	}
	if err := validateMessage(messages[0]); err != nil {
		return err
	}
	if err := validateMessage(messages[1]); err != nil {
		return err
	}
	if messages[1].Content != "" && len(messages[1].ToolCalls) != 0 {
		return errors.New("assistant content and tool calls cannot be mixed")
	}
	if messages[1].Content == "" && len(messages[1].ToolCalls) == 0 {
		return errors.New("assistant message must contain content or tool calls")
	}
	for _, call := range messages[1].ToolCalls {
		if err := collectToolCall(call, pending, seen); err != nil {
			return err
		}
	}
	return nil
}

func messageState(groups [][]domain.ModelMessage) (map[string]struct{}, map[string]struct{}, error) {
	pending := make(map[string]struct{})
	seen := make(map[string]struct{})
	for _, group := range groups {
		if len(group) == 0 {
			return nil, nil, errors.New("stored message group is empty")
		}
		if group[0].Role == "tool" {
			for _, message := range group {
				if message.Role != "tool" {
					return nil, nil, errors.New("stored tool result group contains a non-tool message")
				}
				if err := collectToolResult(message, pending); err != nil {
					return nil, nil, fmt.Errorf("stored message history is invalid: %w", err)
				}
			}
			continue
		}
		if len(pending) != 0 {
			return nil, nil, errors.New("stored model group was appended before tool calls were resolved")
		}
		if group[0].Role != "user" || len(group) != 2 || group[1].Role != "assistant" {
			return nil, nil, errors.New("stored model message group is invalid")
		}
		if err := validateMessage(group[0]); err != nil {
			return nil, nil, err
		}
		if err := validateMessage(group[1]); err != nil {
			return nil, nil, err
		}
		if group[1].Content != "" && len(group[1].ToolCalls) != 0 || group[1].Content == "" && len(group[1].ToolCalls) == 0 {
			return nil, nil, errors.New("stored assistant message content and tool calls are invalid")
		}
		for _, call := range group[1].ToolCalls {
			if err := collectToolCall(call, pending, seen); err != nil {
				return nil, nil, err
			}
		}
	}
	return pending, seen, nil
}

func validateMessage(message domain.ModelMessage) error {
	if message.Role != "user" && message.Role != "assistant" && message.Role != "tool" && message.Role != "system" {
		return errors.New("message role is invalid")
	}
	if !boundedText(message.Content, maxTextBytes) || !boundedText(message.ToolCallID, maxIDBytes) {
		return errors.New("message text is invalid")
	}
	if message.Role != "tool" && message.ToolCallID != "" {
		return errors.New("only tool messages may contain a tool result ID")
	}
	if message.Role == "tool" && len(message.ToolCalls) != 0 {
		return errors.New("tool result cannot contain tool calls")
	}
	return nil
}

func collectToolResult(message domain.ModelMessage, pending map[string]struct{}) error {
	if err := validateMessage(message); err != nil {
		return err
	}
	if message.ToolCallID == "" {
		return errors.New("tool result id is required")
	}
	if _, exists := pending[message.ToolCallID]; !exists {
		return errors.New("tool result has no pending call")
	}
	delete(pending, message.ToolCallID)
	return nil
}

func collectToolCall(call domain.ToolCall, pending, seen map[string]struct{}) error {
	if !validID(call.ID) || !boundedText(call.Name, maxIDBytes) || call.Version != "" && !boundedText(call.Version, maxIDBytes) {
		return errors.New("message tool call is invalid")
	}
	if _, exists := seen[call.ID]; exists {
		return errors.New("message tool call id is duplicated")
	}
	if err := boundedJSON(call.Arguments, maxTextBytes); err != nil {
		return fmt.Errorf("message tool arguments are invalid: %w", err)
	}
	seen[call.ID] = struct{}{}
	pending[call.ID] = struct{}{}
	return nil
}

func validID(value string) bool {
	return strings.TrimSpace(value) != "" && len(value) <= maxIDBytes && strings.IndexByte(value, 0) < 0 && utf8.ValidString(value)
}

func boundedText(value string, max int) bool {
	return len(value) <= max && utf8.ValidString(value) && strings.IndexByte(value, 0) < 0
}

func validHash(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validRunState(value domain.RunState) bool {
	switch value {
	case domain.RunStateQueued, domain.RunStateRunning, domain.RunStateWaiting, domain.RunStateSucceeded, domain.RunStateFailed, domain.RunStateCancelled:
		return true
	default:
		return false
	}
}

func validEffect(value domain.ToolEffect) bool {
	return value == domain.ToolEffectRead || value == domain.ToolEffectWrite
}

func validInvocationState(value domain.InvocationState) bool {
	switch value {
	case domain.InvocationPending, domain.InvocationSucceeded, domain.InvocationFailed, domain.InvocationUnknown, domain.InvocationRejected, domain.InvocationInterrupted:
		return true
	default:
		return false
	}
}

func totalMessages(groups [][]domain.ModelMessage) int {
	total := 0
	for _, group := range groups {
		total += len(group)
	}
	return total
}

func strictDecode(encoded []byte, destination any) error {
	if len(bytes.TrimSpace(encoded)) == 0 {
		return errors.New("JSON is empty")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON contains trailing data")
		}
		return fmt.Errorf("JSON contains trailing data: %w", err)
	}
	return nil
}

func readBounded(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	value, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(value)) > limit {
		return nil, errors.New("file exceeds bound")
	}
	return value, nil
}

func removeInterruptedTemp(path, dir string) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect interrupted run state: %w", err)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("reconcile interrupted run state: %w", err)
	}
	directory, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open run directory after reconciliation: %w", err)
	}
	if err := directory.Sync(); err != nil {
		_ = directory.Close()
		return fmt.Errorf("sync run directory after reconciliation: %w", err)
	}
	return directory.Close()
}

func writeAll(writer io.Writer, value []byte) error {
	for len(value) > 0 {
		written, err := writer.Write(value)
		if err != nil {
			return err
		}
		if written <= 0 {
			return io.ErrShortWrite
		}
		value = value[written:]
	}
	return nil
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func canonicalJSONEqual(left, right any) bool {
	leftBytes, leftErr := json.Marshal(left)
	rightBytes, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftBytes, rightBytes)
}

func boundedJSON(value any, limit int) error {
	if value == nil {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(encoded) > limit {
		return errors.New("JSON value exceeds bound")
	}
	return nil
}

func cloneSnapshot(value Snapshot) Snapshot {
	encoded, _ := json.Marshal(value)
	var cloned Snapshot
	_ = json.Unmarshal(encoded, &cloned)
	return cloned
}

func cloneRun(value domain.Run) domain.Run {
	encoded, _ := json.Marshal(value)
	var cloned domain.Run
	_ = json.Unmarshal(encoded, &cloned)
	return cloned
}

func cloneInvocation(value domain.Invocation) domain.Invocation {
	encoded, _ := json.Marshal(value)
	var cloned domain.Invocation
	_ = json.Unmarshal(encoded, &cloned)
	return cloned
}

func cloneInvocations(value []domain.Invocation) []domain.Invocation {
	encoded, _ := json.Marshal(value)
	var cloned []domain.Invocation
	_ = json.Unmarshal(encoded, &cloned)
	return cloned
}

func cloneResult(value domain.InvocationResult) domain.InvocationResult {
	encoded, _ := json.Marshal(value)
	var cloned domain.InvocationResult
	_ = json.Unmarshal(encoded, &cloned)
	return cloned
}

func cloneArtifacts(value []domain.Artifact) []domain.Artifact {
	encoded, _ := json.Marshal(value)
	var cloned []domain.Artifact
	_ = json.Unmarshal(encoded, &cloned)
	return cloned
}

func cloneMessages(value []domain.ModelMessage) []domain.ModelMessage {
	encoded, _ := json.Marshal(value)
	var cloned []domain.ModelMessage
	_ = json.Unmarshal(encoded, &cloned)
	return cloned
}

var _ domain.RunStore = (*Store)(nil)
