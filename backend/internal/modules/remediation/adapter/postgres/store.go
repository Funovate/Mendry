package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	notificationpostgres "mendry/backend/internal/modules/notifications/adapter/postgres"
	notificationdomain "mendry/backend/internal/modules/notifications/domain"
	projectdomain "mendry/backend/internal/modules/projects/domain"
	"mendry/backend/internal/modules/remediation/adapter/postgres/remediationdb"
	"mendry/backend/internal/modules/remediation/application"
	"mendry/backend/internal/modules/remediation/domain"
)

// transactor 是 RunStore 需要的最小数据库合同：查询加上事务。
// 平台 *postgres.Pool 和测试用 *pgxpool.Pool 都满足。
type transactor interface {
	remediationdb.DBTX
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

// RunStore implements domain.RunStore using PostgreSQL persistence. Run and
// series identifiers cross the port as opaque strings (UUIDs internally); rows
// are mapped to feature-owned domain types.
type RunStore struct {
	db            transactor
	notifications notificationpostgres.Enqueuer
}

func (s *RunStore) SetNotificationEnqueuer(enqueuer notificationpostgres.Enqueuer) {
	s.notifications = enqueuer
}

const (
	maxSuggestedDiffBytes     = 64 * 1024
	maxAttemptSummaries       = 64
	maxAutomaticContinuations = 3
)

// Compile-time assertion that the adapter satisfies the frozen port and review companions.
var (
	_ domain.RunStore                 = (*RunStore)(nil)
	_ domain.AttemptStore             = (*RunStore)(nil)
	_ domain.ReconfiguredAttemptStore = (*RunStore)(nil)
	_ domain.PlanningCheckpointStore  = (*RunStore)(nil)
	_ domain.EvidencePersistencePort  = (*RunStore)(nil)
	_ domain.BootstrapEvidenceLoader  = (*RunStore)(nil)
	_ domain.CallbackEvidenceLoader   = (*RunStore)(nil)
	_ domain.ToolPolicyResolver       = (*RunStore)(nil)
	_ domain.LifecycleStore           = (*RunStore)(nil)
	_ application.ReviewRecorder      = (*RunStore)(nil)
	_ application.ReviewQuery         = (*RunStore)(nil)
	_ application.NotificationSink    = (*RunStore)(nil)
)

// NewRunStore 用进程 PostgreSQL 连接构造 RunStore。
func NewRunStore(database transactor) (*RunStore, error) {
	if database == nil {
		return nil, fmt.Errorf("remediation database is required")
	}
	return &RunStore{db: wrapExecutionAwareDatabase(database)}, nil
}

// ResolveToolPolicy loads the immutable read allowlist for one project/source
// pair. A missing row is deliberately an empty policy: discovery may continue
// for operator diagnostics, but no dynamic MCP tool can be exposed.
func (s *RunStore) ResolveToolPolicy(ctx context.Context, projectID, sourceID string) (domain.ToolPolicySnapshot, error) {
	projectUUID, err := parseScopedUUID(projectID, "project")
	if err != nil {
		return domain.ToolPolicySnapshot{}, err
	}
	sourceUUID, err := parseScopedUUID(sourceID, "source")
	if err != nil {
		return domain.ToolPolicySnapshot{}, err
	}
	row, err := remediationdb.New(s.db).GetRemediationToolPolicy(ctx, remediationdb.GetRemediationToolPolicyParams{
		ProjectID: projectUUID,
		SourceID:  sourceUUID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ToolPolicySnapshot{ProjectID: projectID, SourceID: sourceID}, nil
	}
	if err != nil {
		return domain.ToolPolicySnapshot{}, fmt.Errorf("get remediation tool policy: %w", err)
	}
	if !row.ProjectID.Valid || !row.SourceID.Valid || uuidString(row.ProjectID) != projectID || uuidString(row.SourceID) != sourceID {
		return domain.ToolPolicySnapshot{}, fmt.Errorf("remediation tool policy row has invalid ownership")
	}
	policy, err := domain.ParseToolPolicy(projectID, sourceID, row.Version, row.PolicyHash, row.Entries)
	if err != nil {
		return domain.ToolPolicySnapshot{}, fmt.Errorf("validate remediation tool policy: %w", err)
	}
	return policy, nil
}

// CreateSeriesAndRunOnDBTX 在调用方拥有的 DBTX 上创建或复用 series/root run。
// 它不 begin/commit/rollback；incident 事务接线依赖这个 helper，把 root run
// 持久化和事故创建/重开保持在同一个 PostgreSQL transaction 中。
func CreateSeriesAndRunOnDBTX(ctx context.Context, database remediationdb.DBTX, in domain.NewRun) (domain.Run, error) {
	if database == nil {
		return domain.Run{}, fmt.Errorf("remediation database is required")
	}
	return createSeriesAndRun(ctx, remediationdb.New(database), in)
}

// CreateSeriesAndRun 按唯一 key 创建或复用 series 的根 run。
// 同一 key 已有 run 时返回第一个根 run，不插入 attempt 2。
// 后续手动 retry API 会走单独的下一 attempt 路径。
func (s *RunStore) CreateSeriesAndRun(ctx context.Context, in domain.NewRun) (domain.Run, error) {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.Run{}, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	run, err := createSeriesAndRun(ctx, remediationdb.New(tx), in)
	if err != nil {
		return domain.Run{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.Run{}, fmt.Errorf("commit transaction: %w", err)
	}

	return run, nil
}

// CreateNextAttempt 在锁定所属 series row 后原子接纳新的 queued child。该锁使同一
// series 的所有创建者看到同一个 latest predecessor，不能越过 active-attempt 或自动 ceiling。
func (s *RunStore) CreateNextAttempt(ctx context.Context, in domain.NextAttempt) (domain.Run, error) {
	if err := in.Validate(); err != nil {
		return domain.Run{}, fmt.Errorf("%w: %v", domain.ErrInvalidNextAttempt, err)
	}

	predecessorID, err := parseRunID(in.ContinuationOfRunID)
	if err != nil {
		return domain.Run{}, err
	}
	seriesID, err := parseScopedUUID(in.SeriesID, "series")
	if err != nil {
		return domain.Run{}, err
	}
	incidentID, err := parseScopedUUID(in.IncidentID, "incident")
	if err != nil {
		return domain.Run{}, err
	}
	origin := in.TriggerReason
	if origin == "" {
		origin = in.Origin
	}

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.Run{}, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	q := remediationdb.New(tx)
	// incident row 先以 FOR SHARE 锁定，再锁 series；这与 lifecycle 写事务的
	// incident→series 顺序一致，并阻止 context cursor 在 child commit 前推进。
	currentContextVersion, err := q.LockRemediationIncidentContextVersion(ctx, incidentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Run{}, domain.ErrStalePredecessor
	}
	if err != nil {
		return domain.Run{}, fmt.Errorf("lock remediation incident context version: %w", err)
	}
	if currentContextVersion != in.ContextVersion {
		return domain.Run{}, domain.ErrStalePredecessor
	}

	series, err := q.LockRemediationSeriesForRun(ctx, predecessorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Run{}, domain.ErrStalePredecessor
	}
	if err != nil {
		return domain.Run{}, fmt.Errorf("lock remediation series: %w", err)
	}
	if !series.ID.Valid || !series.IncidentID.Valid ||
		!sameUUID(series.ID, seriesID) || !sameUUID(series.IncidentID, incidentID) ||
		series.LifecycleGeneration != in.LifecycleGeneration || series.DeployedCommit != in.DeployedCommit {
		return domain.Run{}, domain.ErrStalePredecessor
	}

	predecessor, err := q.GetRemediationRun(ctx, predecessorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Run{}, domain.ErrStalePredecessor
	}
	if err != nil {
		return domain.Run{}, fmt.Errorf("get remediation predecessor: %w", err)
	}
	if !predecessor.ID.Valid || !sameUUID(predecessor.ID, predecessorID) ||
		!sameUUID(predecessor.SeriesID, series.ID) {
		return domain.Run{}, domain.ErrStalePredecessor
	}

	runs, err := q.GetRemediationRunsBySeriesID(ctx, series.ID)
	if err != nil {
		return domain.Run{}, fmt.Errorf("list remediation attempts: %w", err)
	}
	if len(runs) == 0 {
		return domain.Run{}, domain.ErrStalePredecessor
	}
	latest := runs[len(runs)-1]
	if !sameUUID(latest.ID, predecessorID) || latest.Version != in.ExpectedPreviousVersion {
		return domain.Run{}, domain.ErrStalePredecessor
	}

	for _, candidate := range runs {
		if isActiveAttemptState(domain.RunState(candidate.State)) {
			return domain.Run{}, domain.ErrActiveAttempt
		}
	}

	automaticContinuations := 0
	for _, candidate := range runs {
		if candidate.TriggerReason == domain.TriggerReasonAutomaticContinue {
			automaticContinuations++
		}
	}

	switch origin {
	case domain.TriggerReasonAutomaticContinue:
		if automaticContinuations >= maxAutomaticContinuations {
			return domain.Run{}, domain.ErrAutomaticCeiling
		}
		if latest.State != string(domain.RunStateFailed) || !latest.Retryable || in.ContextVersion <= latest.ContextVersion {
			return domain.Run{}, domain.ErrAutomaticGateRejected
		}
	case domain.TriggerReasonManualContinue:
		switch domain.RunState(latest.State) {
		case domain.RunStateFailed, domain.RunStateBudgetExhausted, domain.RunStateBlockedManualReview:
		default:
			return domain.Run{}, domain.ErrUnsupportedState
		}
	default:
		// Validate 已拒绝其他 origin；保留此 guard 以便 contract 扩展时仍在存储边界 fail closed。
		return domain.Run{}, domain.ErrUnsupportedState
	}

	child, err := q.CreateRemediationNextRun(ctx, remediationdb.CreateRemediationNextRunParams{
		SeriesID:            series.ID,
		AttemptNumber:       latest.AttemptNumber + 1,
		ContinuationOfRunID: predecessorID,
		TriggerReason:       origin,
		ContinuationReason:  strings.TrimSpace(in.ContinuationReason),
		ContextVersion:      in.ContextVersion,
	})
	if uniqueViolation(err) {
		return domain.Run{}, domain.ErrStalePredecessor
	}
	if err != nil {
		return domain.Run{}, fmt.Errorf("create remediation continuation: %w", err)
	}

	run := mapRunRowToDomainRun(child)
	run.IncidentID = uuidString(series.IncidentID)
	run.LifecycleGeneration = series.LifecycleGeneration
	run.DeployedCommit = series.DeployedCommit
	if err := tx.Commit(ctx); err != nil {
		return domain.Run{}, fmt.Errorf("commit transaction: %w", err)
	}
	return run, nil
}

// CreateReconfiguredAttempt creates a queued linked attempt from the current
// project policy. Ordinary continuation intentionally does not call this method.
func (s *RunStore) CreateReconfiguredAttempt(ctx context.Context, in domain.NextAttempt) (domain.Run, error) {
	if err := in.Validate(); err != nil {
		return domain.Run{}, fmt.Errorf("%w: %v", domain.ErrInvalidNextAttempt, err)
	}
	if in.TriggerReason != domain.TriggerOriginManualReconfigure && in.Origin != domain.TriggerOriginManualReconfigure {
		return domain.Run{}, domain.ErrUnsupportedState
	}
	predecessorID, err := parseRunID(in.ContinuationOfRunID)
	if err != nil {
		return domain.Run{}, err
	}
	seriesID, err := parseScopedUUID(in.SeriesID, "series")
	if err != nil {
		return domain.Run{}, err
	}
	incidentID, err := parseScopedUUID(in.IncidentID, "incident")
	if err != nil {
		return domain.Run{}, err
	}

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.Run{}, fmt.Errorf("begin reconfigured attempt transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	q := remediationdb.New(tx)
	currentContextVersion, err := q.LockRemediationIncidentContextVersion(ctx, incidentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Run{}, domain.ErrStalePredecessor
	}
	if err != nil {
		return domain.Run{}, fmt.Errorf("lock reconfigured incident context version: %w", err)
	}
	if currentContextVersion != in.ContextVersion {
		return domain.Run{}, domain.ErrStalePredecessor
	}
	status, err := q.GetRemediationIncidentStatus(ctx, incidentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Run{}, domain.ErrStalePredecessor
	}
	if err != nil {
		return domain.Run{}, fmt.Errorf("get reconfigured incident status: %w", err)
	}
	if status != "Open" {
		return domain.Run{}, domain.ErrUnsupportedState
	}
	if err := validateCurrentReconfigurationPolicy(ctx, tx, incidentID); err != nil {
		return domain.Run{}, err
	}

	series, err := q.LockRemediationSeriesForRun(ctx, predecessorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Run{}, domain.ErrStalePredecessor
	}
	if err != nil {
		return domain.Run{}, fmt.Errorf("lock reconfigured remediation series: %w", err)
	}
	if !series.ID.Valid || !series.IncidentID.Valid || !sameUUID(series.ID, seriesID) ||
		!sameUUID(series.IncidentID, incidentID) || series.LifecycleGeneration != in.LifecycleGeneration ||
		series.DeployedCommit != in.DeployedCommit {
		return domain.Run{}, domain.ErrStalePredecessor
	}
	predecessor, err := q.GetRemediationRun(ctx, predecessorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Run{}, domain.ErrStalePredecessor
	}
	if err != nil {
		return domain.Run{}, fmt.Errorf("get reconfigured predecessor: %w", err)
	}
	if !predecessor.ID.Valid || !sameUUID(predecessor.ID, predecessorID) || !sameUUID(predecessor.SeriesID, series.ID) {
		return domain.Run{}, domain.ErrStalePredecessor
	}
	runs, err := q.GetRemediationRunsBySeriesID(ctx, series.ID)
	if err != nil {
		return domain.Run{}, fmt.Errorf("list reconfigured remediation attempts: %w", err)
	}
	if len(runs) == 0 {
		return domain.Run{}, domain.ErrStalePredecessor
	}
	latest := runs[len(runs)-1]
	if !sameUUID(latest.ID, predecessorID) || latest.Version != in.ExpectedPreviousVersion {
		return domain.Run{}, domain.ErrStalePredecessor
	}
	for _, candidate := range runs {
		if isActiveAttemptState(domain.RunState(candidate.State)) {
			return domain.Run{}, domain.ErrActiveAttempt
		}
	}
	switch domain.RunState(latest.State) {
	case domain.RunStateDiagnosisReadyForReview, domain.RunStateFailed,
		domain.RunStateBudgetExhausted, domain.RunStateBlockedManualReview:
	default:
		return domain.Run{}, domain.ErrUnsupportedState
	}
	child, err := q.CreateRemediationReconfiguredRun(ctx, remediationdb.CreateRemediationReconfiguredRunParams{
		SeriesID: series.ID, AttemptNumber: latest.AttemptNumber + 1,
		ContinuationOfRunID: predecessorID, ContinuationReason: strings.TrimSpace(in.ContinuationReason),
		ContextVersion: in.ContextVersion,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Run{}, domain.ErrReconfigurationUnavailable
	}
	if uniqueViolation(err) {
		return domain.Run{}, domain.ErrStalePredecessor
	}
	if err != nil {
		return domain.Run{}, fmt.Errorf("create reconfigured remediation attempt: %w", err)
	}
	run := mapRunRowToDomainRun(child)
	run.IncidentID = uuidString(series.IncidentID)
	run.LifecycleGeneration = series.LifecycleGeneration
	run.DeployedCommit = series.DeployedCommit
	if err := tx.Commit(ctx); err != nil {
		return domain.Run{}, fmt.Errorf("commit reconfigured remediation attempt: %w", err)
	}
	return run, nil
}

func validateCurrentReconfigurationPolicy(ctx context.Context, tx pgx.Tx, incidentID pgtype.UUID) error {
	var (
		agentLoopMode, executionMode string
		policyVersion                int64
		validationJSON               []byte
		publicationJSON              []byte
		changePolicyJSON             []byte
	)
	err := tx.QueryRow(ctx, `
		SELECT project.agent_loop_mode,
		       project.agent_loop_policy_version,
		       project.remediation_execution_mode,
		       project.remediation_validation_profile,
		       project.remediation_publication,
		       project.remediation_change_policy
		FROM incidents AS incident
		JOIN projects AS project ON project.id = incident.project_id
		JOIN project_repositories AS repository ON repository.project_id = project.id
		WHERE incident.id = $1
		FOR UPDATE OF project, repository`, incidentID).Scan(
		&agentLoopMode, &policyVersion, &executionMode, &validationJSON, &publicationJSON, &changePolicyJSON,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrStalePredecessor
	}
	if err != nil {
		return fmt.Errorf("load current remediation policy: %w", err)
	}
	var validationProfile projectdomain.ValidationProfile
	var publication projectdomain.RemediationPublication
	var changePolicy projectdomain.RemediationChangePolicy
	if err := json.Unmarshal(validationJSON, &validationProfile); err != nil {
		return domain.ErrReconfigurationUnavailable
	}
	if err := json.Unmarshal(publicationJSON, &publication); err != nil {
		return domain.ErrReconfigurationUnavailable
	}
	if err := json.Unmarshal(changePolicyJSON, &changePolicy); err != nil {
		return domain.ErrReconfigurationUnavailable
	}
	policy := projectdomain.RemediationPolicy{
		AgentLoopMode:     projectdomain.AgentLoopMode(agentLoopMode),
		ExecutionMode:     projectdomain.RemediationExecutionMode(executionMode),
		ValidationProfile: validationProfile,
		Publication:       publication,
		ChangePolicy:      changePolicy,
		Version:           policyVersion,
	}
	if err := projectdomain.ValidateRemediationPolicy(policy); err != nil ||
		policy.AgentLoopMode != projectdomain.AgentLoopModeResilientV1 ||
		policy.ExecutionMode != projectdomain.RemediationExecutionAutoHotfix {
		return domain.ErrReconfigurationUnavailable
	}
	if _, err := uuid.Parse(strings.TrimSpace(policy.Publication.GitCredentialSecretID)); err != nil {
		return domain.ErrReconfigurationUnavailable
	}
	if apiID := strings.TrimSpace(policy.Publication.APICredentialSecretID); apiID != "" {
		if _, err := uuid.Parse(apiID); err != nil {
			return domain.ErrReconfigurationUnavailable
		}
	}
	return nil
}

// GetLatestPlanningCheckpoint 返回同一 series/context 中、截至 expected predecessor
// attempt 最近一次以 code_fixable durable decision 通过 evidence gate 的 aggregate。
func (s *RunStore) GetLatestPlanningCheckpoint(
	ctx context.Context,
	seriesID string,
	contextVersion int64,
	throughAttemptNumber int32,
) (domain.RunAggregate, error) {
	seriesUUID, err := parseScopedUUID(seriesID, "series")
	if err != nil {
		return domain.RunAggregate{}, err
	}
	if contextVersion < 0 || throughAttemptNumber < 1 {
		return domain.RunAggregate{}, fmt.Errorf("planning checkpoint bounds are invalid")
	}
	row, err := remediationdb.New(s.db).GetLatestRemediationPlanningCheckpoint(ctx, remediationdb.GetLatestRemediationPlanningCheckpointParams{
		SeriesID:             seriesUUID,
		ContextVersion:       contextVersion,
		ThroughAttemptNumber: throughAttemptNumber,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RunAggregate{}, domain.ErrPlanningCheckpointNotFound
	}
	if err != nil {
		return domain.RunAggregate{}, fmt.Errorf("get remediation planning checkpoint: %w", err)
	}
	return s.Get(ctx, uuidString(row.ID))
}

func isActiveAttemptState(state domain.RunState) bool {
	switch state {
	case domain.RunStateQueued, domain.RunStatePreparingContext, domain.RunStateDiagnosing,
		domain.RunStateCollectingMoreContext, domain.RunStatePlanning, domain.RunStateRunning,
		domain.RunStatePatching, domain.RunStateValidating, domain.RunStatePublishing:
		return true
	default:
		return false
	}
}

func createSeriesAndRun(ctx context.Context, q *remediationdb.Queries, in domain.NewRun) (domain.Run, error) {
	incidentID, err := uuid.Parse(in.IncidentID)
	if err != nil {
		return domain.Run{}, fmt.Errorf("parse incident id %q: %w", in.IncidentID, err)
	}

	series, err := q.CreateRemediationSeries(ctx, remediationdb.CreateRemediationSeriesParams{
		IncidentID:          pgtype.UUID{Bytes: incidentID, Valid: true},
		LifecycleGeneration: in.LifecycleGeneration,
		DeployedCommit:      in.DeployedCommit,
	})
	if err != nil {
		return domain.Run{}, fmt.Errorf("create series: %w", err)
	}

	existing, err := q.GetRemediationRunsBySeriesID(ctx, series.ID)
	if err != nil {
		return domain.Run{}, fmt.Errorf("count series runs: %w", err)
	}
	if len(existing) > 0 {
		return mapCreatedRun(existing[0], series, in.IncidentID), nil
	}

	run, err := q.CreateRemediationRun(ctx, remediationdb.CreateRemediationRunParams{
		SeriesID:       series.ID,
		AttemptNumber:  1,
		State:          string(domain.RunStateQueued),
		ContextVersion: in.ContextVersion,
		TriggerReason:  safeTriggerReason(in.TriggerReason),
		AnalysisOnly:   in.AnalysisOnly,
	})
	if uniqueViolation(err) {
		existing, loadErr := q.GetRemediationRunsBySeriesID(ctx, series.ID)
		if loadErr != nil {
			return domain.Run{}, fmt.Errorf("reload series runs after conflict: %w", loadErr)
		}
		if len(existing) == 0 {
			return domain.Run{}, fmt.Errorf("create run: %w", err)
		}
		return mapCreatedRun(existing[0], series, in.IncidentID), nil
	}
	if err != nil {
		return domain.Run{}, fmt.Errorf("create run: %w", err)
	}

	return mapCreatedRun(run, series, in.IncidentID), nil
}

func mapCreatedRun(run remediationdb.RemediationRun, series remediationdb.RemediationSeries, incidentID string) domain.Run {
	triggerReason := safeTriggerReason(run.TriggerReason)
	return domain.Run{
		RunID:                   uuidString(run.ID),
		SeriesID:                uuidString(series.ID),
		IncidentID:              incidentID,
		LifecycleGeneration:     series.LifecycleGeneration,
		DeployedCommit:          series.DeployedCommit,
		AttemptNumber:           run.AttemptNumber,
		State:                   domain.RunState(run.State),
		Origin:                  triggerReason,
		TriggerReason:           triggerReason,
		ContinuationOfRunID:     optionalUUIDString(run.ContinuationOfRunID),
		ContinuationReason:      run.ContinuationReason,
		ContextVersion:          run.ContextVersion,
		TerminalReason:          run.TerminalReason,
		Retryable:               run.Retryable,
		AgentLoopMode:           domain.ParseAgentLoopMode(run.AgentLoopMode),
		AgentLoopPolicyVersion:  run.AgentLoopPolicyVersion,
		ExecutionMode:           domain.ParseExecutionMode(run.ExecutionMode),
		ValidationCommands:      decodeValidationCommands(run.ValidationCommands),
		ValidationImageDigest:   run.ValidationImageDigest,
		ExecutionProfile:        decodeRunSnapshot[domain.ExecutionProfileSnapshot](run.ExecutionProfile),
		PublicationSnapshot:     decodeRunSnapshot[domain.PublicationSnapshot](run.PublicationSnapshot),
		ChangePolicySnapshot:    decodeRunSnapshot[domain.ChangePolicySnapshot](run.ChangePolicy),
		PublicationTargetBranch: run.PublicationTargetBranch,
		PublicationBranchPrefix: run.PublicationBranchPrefix,
		AnalysisOnly:            run.AnalysisOnly,
		Version:                 run.Version,
		CreatedAt:               run.StartedAt.Time,
		UpdatedAt:               run.StartedAt.Time,
	}
}

func decodeRunSnapshot[T any](raw []byte) T {
	var value T
	if len(raw) == 0 || string(raw) == "null" {
		return value
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		var zero T
		return zero
	}
	return value
}

func decodeValidationCommands(raw []byte) map[string]int64 {
	commands := map[string]int64{}
	if len(raw) == 0 {
		return commands
	}
	if err := json.Unmarshal(raw, &commands); err != nil {
		return map[string]int64{}
	}
	return commands
}

func uniqueViolation(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505"
}

// AppendDecision records a diagnosis decision with the next sequence number.
func (s *RunStore) AppendDecision(ctx context.Context, runID string, d domain.Decision) error {
	rid, err := parseRunID(runID)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	q := remediationdb.New(tx)

	decisions, err := q.GetRemediationDecisionsByRunID(ctx, rid)
	if err != nil {
		return fmt.Errorf("get decisions: %w", err)
	}

	var confidence pgtype.Numeric
	if err := confidence.Scan(strconv.FormatFloat(d.Confidence, 'f', -1, 64)); err != nil {
		return fmt.Errorf("convert confidence to numeric: %w", err)
	}

	_, err = q.CreateRemediationDecision(ctx, remediationdb.CreateRemediationDecisionParams{
		RunID:                 rid,
		Sequence:              int32(len(decisions) + 1),
		FixabilityClass:       string(d.Fixability),
		ConfidenceScore:       confidence,
		Reasoning:             d.CausalReasoning,
		Contradictions:        cloneStrings(d.Contradictions),
		MissingEvidence:       cloneStrings(d.MissingEvidence),
		EvidenceCitations:     cloneStrings(d.EvidenceCitations),
		RecommendedNextAction: d.RecommendedNextAction,
	})
	if err != nil {
		return fmt.Errorf("create decision: %w", err)
	}

	return tx.Commit(ctx)
}

// AppendEvidence stores a bounded project-owned operational record. The
// deduplication key makes callback/detail retries idempotent and never stores a
// DetailUrl capability.
func (s *RunStore) AppendEvidence(ctx context.Context, evidence domain.StoredEvidence) (domain.StoredEvidence, error) {
	if evidence.Classification == "" {
		evidence.Classification = domain.EvidenceContextual
	}
	if evidence.Outcome == "" {
		evidence.Outcome = "success"
	}
	if err := evidence.Validate(); err != nil {
		return domain.StoredEvidence{}, err
	}
	contentHash := sha256.Sum256(evidence.Payload)
	if hex.EncodeToString(contentHash[:]) != strings.ToLower(evidence.ContentHash) {
		return domain.StoredEvidence{}, fmt.Errorf("stored evidence content hash is invalid")
	}
	projectID, err := parseScopedUUID(evidence.ProjectID, "project")
	if err != nil {
		return domain.StoredEvidence{}, err
	}
	environmentID, err := parseScopedUUID(evidence.EnvironmentID, "environment")
	if err != nil {
		return domain.StoredEvidence{}, err
	}
	sourceID, err := parseScopedUUID(evidence.SourceID, "source")
	if err != nil {
		return domain.StoredEvidence{}, err
	}
	incidentID, err := parseScopedUUID(evidence.IncidentID, "incident")
	if err != nil {
		return domain.StoredEvidence{}, err
	}
	runID, err := optionalRunUUID(evidence.RunID)
	if err != nil {
		return domain.StoredEvidence{}, err
	}
	observationID, err := optionalScopedUUID(evidence.ObservationID, "observation")
	if err != nil {
		return domain.StoredEvidence{}, err
	}
	row, err := remediationdb.New(s.db).CreateRemediationEvidence(ctx, remediationdb.CreateRemediationEvidenceParams{
		ProjectID: projectID, EnvironmentID: environmentID, SourceID: sourceID, IncidentID: incidentID,
		RunID: runID, ObservationID: observationID, Provider: evidence.Provider, EvidenceKind: evidence.EvidenceKind,
		DeduplicationKey: evidence.DeduplicationKey, Classification: string(evidence.Classification), Outcome: evidence.Outcome,
		Available: evidence.Available, PrimaryEvidence: evidence.Primary, TemporalCorrelation: evidence.TemporalCorrelation,
		OperationalCorrelation: evidence.OperationalCorrelation, OccurredAt: optionalTime(evidence.OccurredAt),
		ContentHash: strings.ToLower(evidence.ContentHash), ByteCount: evidence.ByteCount,
		Provenance: evidence.Provenance, Payload: evidence.Payload,
	})
	if err != nil {
		return domain.StoredEvidence{}, fmt.Errorf("create remediation evidence: %w", err)
	}
	return mapStoredEvidence(row)
}

// LoadBootstrapEvidence resolves the latest inbound Observation that has
// normalized alert evidence, then loads only the pre-run evidence attached to
// that Observation. This keeps a reopened incident from inheriting runtime
// evidence collected by an earlier run.
func (s *RunStore) LoadBootstrapEvidence(ctx context.Context, incidentID string) (domain.BootstrapEvidence, error) {
	id, err := parseScopedUUID(incidentID, "incident")
	if err != nil {
		return domain.BootstrapEvidence{}, err
	}
	q := remediationdb.New(s.db)
	observation, err := q.GetLatestRemediationObservationForIncident(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.BootstrapEvidence{}, nil
	}
	if err != nil {
		return domain.BootstrapEvidence{}, fmt.Errorf("get triggering observation: %w", err)
	}
	if !observation.ID.Valid || !observation.ProjectID.Valid || !observation.EnvironmentID.Valid ||
		!observation.SourceID.Valid || !observation.OccurredAt.Valid || !observation.IngestedAt.Valid ||
		!json.Valid(observation.Attributes) {
		return domain.BootstrapEvidence{}, fmt.Errorf("triggering observation row has invalid generated values")
	}
	rows, err := q.ListRemediationEvidenceForObservation(ctx, remediationdb.ListRemediationEvidenceForObservationParams{
		IncidentID: id, ObservationID: observation.ID, ResultLimit: 64,
	})
	if err != nil {
		return domain.BootstrapEvidence{}, fmt.Errorf("list triggering evidence: %w", err)
	}
	records := make([]domain.StoredEvidence, 0, len(rows))
	for _, row := range rows {
		record, err := mapStoredEvidence(row)
		if err != nil {
			return domain.BootstrapEvidence{}, fmt.Errorf("map triggering evidence: %w", err)
		}
		records = append(records, record)
	}
	return domain.BootstrapEvidence{
		Observation: &domain.TriggeringObservation{
			ID: uuidString(observation.ID), ProjectID: uuidString(observation.ProjectID),
			EnvironmentID: uuidString(observation.EnvironmentID), SourceID: uuidString(observation.SourceID),
			Service: observation.Service, OccurredAt: observation.OccurredAt.Time.UTC(),
			Level: observation.Level, Host: observation.Host, RequestID: observation.RequestID,
			Fingerprint: observation.Fingerprint, Attributes: append([]byte(nil), observation.Attributes...),
			IngestedAt: observation.IngestedAt.Time.UTC(),
		},
		Records: records, SourceCoverage: sourceCoverage(rows),
	}, nil
}

// LoadTencentCLSCallbackSnapshot 返回受信任 detail retry 所需的已接收入站 callback。
// raw payload 保留在 adapter 边界内，绝不进入 bootstrap model context。
func (s *RunStore) LoadTencentCLSCallbackSnapshot(ctx context.Context, incidentID string) (domain.CallbackEvidenceSnapshot, error) {
	id, err := parseScopedUUID(incidentID, "incident")
	if err != nil {
		return domain.CallbackEvidenceSnapshot{}, err
	}
	observation, err := remediationdb.New(s.db).GetLatestRemediationObservationForIncident(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CallbackEvidenceSnapshot{}, fmt.Errorf("triggering callback is unavailable")
	}
	if err != nil {
		return domain.CallbackEvidenceSnapshot{}, fmt.Errorf("get triggering callback: %w", err)
	}
	if !observation.ID.Valid || !observation.ProjectID.Valid || !observation.EnvironmentID.Valid ||
		!observation.SourceID.Valid || !observation.OccurredAt.Valid || !observation.IngestedAt.Valid ||
		strings.TrimSpace(observation.Message) == "" {
		return domain.CallbackEvidenceSnapshot{}, fmt.Errorf("triggering callback row has invalid generated values")
	}
	return domain.CallbackEvidenceSnapshot{
		IncidentID:    incidentID,
		ProjectID:     uuidString(observation.ProjectID),
		EnvironmentID: uuidString(observation.EnvironmentID),
		SourceID:      uuidString(observation.SourceID),
		ObservationID: uuidString(observation.ID),
		Payload:       observation.Message,
		OccurredAt:    observation.OccurredAt.Time.UTC(),
		IngestedAt:    observation.IngestedAt.Time.UTC(),
	}, nil
}

// ResolveEvidence resolves citations through the run's incident and project
// ownership joins. Unknown or cross-run IDs remain absent so the gate records
// them as missing evidence rather than trusting model text.
func (s *RunStore) ResolveEvidence(ctx context.Context, runID string, citations []domain.EvidenceCitation) (domain.EvidenceResolution, error) {
	if len(citations) > 64 {
		return domain.EvidenceResolution{}, fmt.Errorf("evidence citations exceed bounds")
	}
	rid, err := parseRunID(runID)
	if err != nil {
		return domain.EvidenceResolution{}, err
	}
	q := remediationdb.New(s.db)
	rows, err := q.ListRemediationEvidenceForRun(ctx, remediationdb.ListRemediationEvidenceForRunParams{RunID: rid, ResultLimit: 128})
	if err != nil {
		return domain.EvidenceResolution{}, fmt.Errorf("list remediation evidence: %w", err)
	}
	byID := make(map[string]remediationdb.RemediationEvidence, len(rows))
	for _, row := range rows {
		byID[uuidString(row.ID)] = row
	}
	resolution := domain.EvidenceResolution{Records: make([]domain.EvidenceRecord, 0, len(citations)), Sources: sourceCoverage(rows)}
	for _, citation := range citations {
		id := strings.TrimSpace(citation.EvidenceID)
		if id == "" {
			continue
		}
		row, ok := byID[id]
		if !ok {
			row, err = q.GetRemediationEvidenceForRun(ctx, remediationdb.GetRemediationEvidenceForRunParams{RunID: rid, EvidenceID: parseEvidenceUUID(id)})
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return domain.EvidenceResolution{}, fmt.Errorf("resolve remediation evidence: %w", err)
			}
		}
		resolution.Records = append(resolution.Records, domain.EvidenceRecord{
			EvidenceID: uuidString(row.ID), Classification: domain.EvidenceClassification(row.Classification),
			SourceID: uuidString(row.SourceID), Available: row.Available, Primary: row.PrimaryEvidence,
			TemporalCorrelation: row.TemporalCorrelation, OperationalCorrelation: row.OperationalCorrelation,
		})
		if row.Classification == string(domain.EvidenceContradictory) || !row.Available {
			resolution.MaterialContradictions = appendUniqueSafe(resolution.MaterialContradictions, "persisted evidence is unavailable or contradictory")
		}
	}
	resolution.Correlation = correlationFromEvidence(rows)
	return resolution, nil
}

// ListContinuationRuntimeEvidence 返回同 series 中 attempt_number <= through 的
// runtime evidence，供 diagnosis continuation 引用。查询保持 attempt 单调，
// 绝不返回未来 attempt 或跨 series/incident 的证据行；证据行仍归属原 attempt，
// 不复制、不重新赋值给 child run。
func (s *RunStore) ListContinuationRuntimeEvidence(ctx context.Context, query domain.ContinuationEvidenceQuery) ([]domain.StoredEvidence, error) {
	seriesID, err := parseScopedUUID(query.SeriesID, "series")
	if err != nil {
		return nil, err
	}
	if query.ThroughAttemptNumber < 0 {
		return nil, fmt.Errorf("continuation through attempt number is invalid")
	}
	limit := query.Limit
	if limit <= 0 || limit > 64 {
		limit = 64
	}
	rows, err := remediationdb.New(s.db).ListContinuationRuntimeEvidence(ctx, remediationdb.ListContinuationRuntimeEvidenceParams{
		SeriesID: seriesID, ThroughAttemptNumber: query.ThroughAttemptNumber, ResultLimit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list continuation runtime evidence: %w", err)
	}
	records := make([]domain.StoredEvidence, 0, len(rows))
	for _, row := range rows {
		record, mapErr := mapStoredEvidence(row)
		if mapErr != nil {
			return nil, mapErr
		}
		records = append(records, record)
	}
	return records, nil
}

// ListContinuationEvidenceIndex 返回同 series 中 attempt_number <= through 的
// 非 runtime 持久化证据紧凑索引（D3/R10）：只含身份与元数据，绝不内联 payload。
// 查询与 ListContinuationRuntimeEvidence 保持相同的归属/单调约束；pre-run
// 证据只有在 ingestion generation/commit baseline 与 series 精确匹配时可见。
// runtime 记录继续由全量 payload 查询提供，避免
// 高容量 runtime 行挤占索引配额；证据行从不复制、不重新归属给 child run。
func (s *RunStore) ListContinuationEvidenceIndex(ctx context.Context, query domain.ContinuationEvidenceQuery) ([]domain.EvidenceIndexEntry, error) {
	seriesID, err := parseScopedUUID(query.SeriesID, "series")
	if err != nil {
		return nil, err
	}
	if query.ThroughAttemptNumber < 0 {
		return nil, fmt.Errorf("continuation through attempt number is invalid")
	}
	limit := query.Limit
	if limit <= 0 || limit > 64 {
		limit = 64
	}
	rows, err := remediationdb.New(s.db).ListContinuationEvidenceIndex(ctx, remediationdb.ListContinuationEvidenceIndexParams{
		SeriesID: seriesID, ThroughAttemptNumber: query.ThroughAttemptNumber, ResultLimit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list continuation evidence index: %w", err)
	}
	entries := make([]domain.EvidenceIndexEntry, 0, len(rows))
	for _, row := range rows {
		entry, mapErr := mapEvidenceIndexEntry(row)
		if mapErr != nil {
			return nil, mapErr
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// PersistEvidenceAssessment stores the decision after verifying its run-owned
// incident identity. Model-provided confidence is bounded again at this DB
// adapter boundary.
func (s *RunStore) PersistEvidenceAssessment(ctx context.Context, assessment domain.EvidenceAssessment) error {
	if assessment.ConfidenceCap < 0 || assessment.ConfidenceCap > 1 || assessment.EffectiveConfidence < 0 || assessment.EffectiveConfidence > 1 {
		return fmt.Errorf("evidence assessment confidence is invalid")
	}
	rid, err := parseRunID(assessment.RunID)
	if err != nil {
		return err
	}
	q := remediationdb.New(s.db)
	run, err := q.GetRemediationRun(ctx, rid)
	if err != nil {
		return fmt.Errorf("get assessment run: %w", err)
	}
	series, err := q.GetRemediationSeriesByID(ctx, run.SeriesID)
	if err != nil {
		return fmt.Errorf("get assessment series: %w", err)
	}
	if assessment.IncidentID == "" {
		assessment.IncidentID = uuidString(series.IncidentID)
	}
	if uuidString(series.IncidentID) != assessment.IncidentID {
		return fmt.Errorf("evidence assessment incident ownership is invalid")
	}
	incidentID, err := parseScopedUUID(assessment.IncidentID, "incident")
	if err != nil {
		return err
	}
	if assessment.ProjectID == "" {
		projectIDRow, err := q.GetRemediationRunProject(ctx, rid)
		if err != nil {
			return fmt.Errorf("get assessment project: %w", err)
		}
		assessment.ProjectID = uuidString(projectIDRow)
	}
	projectID, err := parseScopedUUID(assessment.ProjectID, "project")
	if err != nil {
		return err
	}
	var confidenceCap, effectiveConfidence pgtype.Numeric
	if err := confidenceCap.Scan(strconv.FormatFloat(assessment.ConfidenceCap, 'f', -1, 64)); err != nil {
		return fmt.Errorf("convert assessment confidence cap: %w", err)
	}
	if err := effectiveConfidence.Scan(strconv.FormatFloat(assessment.EffectiveConfidence, 'f', -1, 64)); err != nil {
		return fmt.Errorf("convert assessment confidence: %w", err)
	}
	if _, err := q.UpsertRemediationEvidenceAssessment(ctx, remediationdb.UpsertRemediationEvidenceAssessmentParams{
		RunID: rid, ProjectID: projectID, IncidentID: incidentID, ConfidenceCap: confidenceCap,
		EffectiveConfidence: effectiveConfidence, PlanningEligible: assessment.PlanningEligible,
		Outcome: string(assessment.Outcome), Reasons: cloneStrings(assessment.Reasons),
		MissingEvidence: cloneStrings(assessment.MissingEvidence), Contradictions: cloneStrings(assessment.Contradictions),
		DirectEvidenceIds: cloneStrings(assessment.DirectEvidenceIDs),
	}); err != nil {
		return fmt.Errorf("persist evidence assessment: %w", err)
	}
	return nil
}

// RecordToolInvocation records a tool invocation with the next sequence number.
// No secret-bearing parameters are persisted — only tool identity, phase,
// timing, and a coarse outcome.
func (s *RunStore) RecordToolInvocation(ctx context.Context, runID string, t domain.ToolInvocation) error {
	rid, err := parseRunID(runID)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	q := remediationdb.New(tx)
	if _, err := q.LockRemediationSeriesForRun(ctx, rid); err != nil {
		return fmt.Errorf("lock invocation series: %w", err)
	}

	invocations, err := q.GetRemediationToolInvocationsByRunID(ctx, rid)
	if err != nil {
		return fmt.Errorf("get invocations: %w", err)
	}

	var durationMs *int64
	if !t.InvokedAt.IsZero() && !t.CompletedAt.IsZero() {
		d := t.CompletedAt.Sub(t.InvokedAt).Milliseconds()
		durationMs = &d
	}

	outcome := "success"
	if t.Error != "" {
		outcome = "error"
	}

	_, err = q.CreateRemediationToolInvocation(ctx, remediationdb.CreateRemediationToolInvocationParams{
		RunID:       rid,
		Sequence:    int32(len(invocations) + 1),
		ToolName:    t.ToolName,
		Phase:       string(t.Phase),
		DurationMs:  durationMs,
		Outcome:     outcome,
		OutcomeRef:  t.InvocationID,
		EvidenceIds: cloneStrings(t.EvidenceIDs),
		ErrorCode:   sanitizeToolErrorCode(t.Error),
	})
	if err != nil {
		return fmt.Errorf("create invocation: %w", err)
	}

	return tx.Commit(ctx)
}

func sanitizeToolErrorCode(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) > 128 {
		return "error"
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') ||
			char == '_' || char == '-' || char == '.' {
			continue
		}
		return "error"
	}
	return value
}

// Transition advances a run from one state to another with optimistic locking
// and applies the budget effect counters in the same transaction.
func (s *RunStore) Transition(ctx context.Context, runID string, fromState, toState domain.RunState, effect domain.Effect) error {
	rid, err := parseRunID(runID)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	q := remediationdb.New(tx)

	run, err := q.GetRemediationRun(ctx, rid)
	if err != nil {
		return fmt.Errorf("get run: %w", err)
	}

	if run.State != string(fromState) {
		return fmt.Errorf("state mismatch: expected %s, got %s", fromState, run.State)
	}

	_, err = q.UpdateRemediationRunState(ctx, remediationdb.UpdateRemediationRunStateParams{
		ID:             rid,
		State:          string(toState),
		Column3:        isTerminalState(toState),
		Version:        run.Version,
		TerminalReason: safeModelIdentity(effect.TerminalReason),
		Retryable:      effect.Retryable,
	})
	if err != nil {
		return fmt.Errorf("update state: %w", err)
	}

	if effect.ModelCalls > 0 || effect.ModelTokensIn > 0 || effect.ModelTokensOut > 0 ||
		effect.ModelCostCents > 0 || effect.ModelProvider != "" || effect.ModelName != "" ||
		effect.ToolCalls > 0 || effect.EvidenceBytes > 0 || effect.RepositoryBytes > 0 {
		_, err = q.IncrementRunCounters(ctx, remediationdb.IncrementRunCountersParams{
			ID:              rid,
			ModelCalls:      int32(effect.ModelCalls),
			ModelTokensIn:   effect.ModelTokensIn,
			ModelTokensOut:  effect.ModelTokensOut,
			ModelCostCents:  effect.ModelCostCents,
			ModelProvider:   safeModelIdentity(effect.ModelProvider),
			ModelName:       safeModelIdentity(effect.ModelName),
			ToolCalls:       int32(effect.ToolCalls),
			EvidenceBytes:   effect.EvidenceBytes,
			RepositoryBytes: effect.RepositoryBytes,
		})
		if err != nil {
			return fmt.Errorf("increment counters: %w", err)
		}
	}

	if s.notifications != nil && notificationdomain.IsStoppingResult(string(toState), run.ExecutionMode, run.AnalysisOnly) {
		if err := s.notifications.Enqueue(ctx, tx, notificationdomain.Event{Kind: "result", RunID: runID, State: string(toState)}); err != nil {
			return fmt.Errorf("enqueue remediation result notification: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// Get loads the full run aggregate: the run plus its decisions, plans, tool
// invocations, and artifact references.
func (s *RunStore) Get(ctx context.Context, runID string) (domain.RunAggregate, error) {
	rid, err := parseRunID(runID)
	if err != nil {
		return domain.RunAggregate{}, err
	}

	q := remediationdb.New(s.db)

	run, err := q.GetRemediationRun(ctx, rid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.RunAggregate{}, fmt.Errorf("run not found: %w", err)
		}
		return domain.RunAggregate{}, fmt.Errorf("get run: %w", err)
	}

	decisions, err := q.GetRemediationDecisionsByRunID(ctx, rid)
	if err != nil {
		return domain.RunAggregate{}, fmt.Errorf("get decisions: %w", err)
	}

	plans, err := q.GetRemediationPlansByRunID(ctx, rid)
	if err != nil {
		return domain.RunAggregate{}, fmt.Errorf("get plans: %w", err)
	}

	invocations, err := q.GetRemediationToolInvocationsByRunID(ctx, rid)
	if err != nil {
		return domain.RunAggregate{}, fmt.Errorf("get invocations: %w", err)
	}

	artifacts, err := q.GetRemediationArtifactsByRunID(ctx, rid)
	if err != nil {
		return domain.RunAggregate{}, fmt.Errorf("get artifacts: %w", err)
	}

	series, err := q.GetRemediationSeriesByID(ctx, run.SeriesID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.RunAggregate{}, fmt.Errorf("run series not found: %w", err)
		}
		return domain.RunAggregate{}, fmt.Errorf("get run series: %w", err)
	}
	projectID, err := q.GetRemediationRunProject(ctx, rid)
	if err != nil {
		return domain.RunAggregate{}, fmt.Errorf("get run project: %w", err)
	}
	if !projectID.Valid {
		return domain.RunAggregate{}, fmt.Errorf("run project identity is invalid")
	}
	attempts, err := q.GetRemediationRunsBySeriesID(ctx, run.SeriesID)
	if err != nil {
		return domain.RunAggregate{}, fmt.Errorf("get attempt summaries: %w", err)
	}

	agg := domain.RunAggregate{
		Run:                mapRunRowToDomainRun(run),
		Decisions:          mapDecisions(decisions),
		Plans:              mapPlans(plans),
		ToolInvocations:    mapInvocations(invocations),
		ArtifactReferences: mapArtifacts(artifacts),
		SuggestedDiff:      suggestedDiffFromArtifacts(artifacts),
		RecommendedPlanID:  recommendedPlanID(plans),
		AttemptSummaries:   mapAttemptSummaries(attempts),
	}
	agg.Run.ProjectID = uuidString(projectID)
	agg.Run.IncidentID = uuidString(series.IncidentID)
	agg.Run.LifecycleGeneration = series.LifecycleGeneration
	agg.Run.DeployedCommit = series.DeployedCommit
	return agg, nil
}

// AppendPlans 写入候选计划；recommendedID 只标记推荐项，不改变冻结 RunStore。
func (s *RunStore) AppendPlans(ctx context.Context, runID string, plans []domain.RepairPlanCandidate, recommendedID string) error {
	rid, err := parseRunID(runID)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	q := remediationdb.New(tx)
	existing, err := q.GetRemediationPlansByRunID(ctx, rid)
	if err != nil {
		return fmt.Errorf("get plans: %w", err)
	}
	for i, plan := range plans {
		planKey := strings.TrimSpace(plan.PlanID)
		_, err = q.CreateRemediationPlan(ctx, remediationdb.CreateRemediationPlanParams{
			RunID:            rid,
			Sequence:         int32(len(existing) + i + 1),
			Title:            plan.IntendedBehavior,
			Rationale:        plan.Rationale,
			RiskClass:        string(plan.Risk),
			IsRecommended:    plan.Recommended || (recommendedID != "" && planKey == recommendedID),
			PlanKey:          planKey,
			EvidenceRefs:     cloneStrings(plan.EvidenceRefs),
			AffectedFiles:    cloneStrings(plan.AffectedFiles),
			RollbackStrategy: plan.RollbackStrategy,
		})
		if err != nil {
			return fmt.Errorf("create plan: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// RecordSuggestedDiff 把有界 unified diff 存进 artifact.excerpt，并记录 content hash。
func (s *RunStore) RecordSuggestedDiff(ctx context.Context, runID string, diff string) error {
	rid, err := parseRunID(runID)
	if err != nil {
		return err
	}
	excerpt := boundSuggestedDiff(diff)
	sum := sha256.Sum256([]byte(excerpt))
	hash := hex.EncodeToString(sum[:])
	_, err = remediationdb.New(s.db).CreateRemediationArtifact(ctx, remediationdb.CreateRemediationArtifactParams{
		RunID:         rid,
		ArtifactType:  "suggested_diff",
		ReferencePath: "sha256:" + hash,
		ContentHash:   hash,
		SizeBytes:     int64(len(excerpt)),
		Excerpt:       excerpt,
	})
	if err != nil {
		return fmt.Errorf("create suggested diff artifact: %w", err)
	}
	return nil
}

// GetLifecycleEffect 返回指定幂等 key 的 latest effect；不存在时返回稳定 sentinel，
// 调用方可安全执行同一 key 的恢复，而不会猜测外部效果状态。
func (s *RunStore) GetLifecycleEffect(ctx context.Context, runID string, kind domain.LifecycleEffectKind, idempotencyKey string) (domain.LifecycleEffect, error) {
	rid, err := parseRunID(runID)
	if err != nil {
		return domain.LifecycleEffect{}, err
	}
	if !kind.IsKnown() {
		return domain.LifecycleEffect{}, fmt.Errorf("unknown lifecycle effect kind %q", kind)
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return domain.LifecycleEffect{}, fmt.Errorf("lifecycle effect idempotency key is required")
	}
	row, err := remediationdb.New(s.db).GetRemediationLifecycleEffect(ctx, remediationdb.GetRemediationLifecycleEffectParams{
		RunID: rid, EffectKind: string(kind), IdempotencyKey: idempotencyKey,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.LifecycleEffect{}, domain.ErrLifecycleEffectNotFound
	}
	if err != nil {
		return domain.LifecycleEffect{}, fmt.Errorf("get remediation lifecycle effect: %w", err)
	}
	return mapLifecycleEffect(row)
}

// UpsertLifecycleEffect 原子更新 effect projection。SQL 层保护已 succeeded 的
// 记录不被后续 uncertain/failure 重写，保证 process restart 可复用成功身份。
func (s *RunStore) UpsertLifecycleEffect(ctx context.Context, effect domain.LifecycleEffect) (domain.LifecycleEffect, error) {
	if err := effect.Validate(); err != nil {
		return domain.LifecycleEffect{}, err
	}
	rid, err := parseRunID(effect.RunID)
	if err != nil {
		return domain.LifecycleEffect{}, err
	}
	row, err := remediationdb.New(s.db).UpsertRemediationLifecycleEffect(ctx, remediationdb.UpsertRemediationLifecycleEffectParams{
		RunID: rid, EffectKind: string(effect.Kind), IdempotencyKey: effect.IdempotencyKey,
		State: string(effect.State), Attempt: int32(effect.Attempt), BaselineCommit: effect.BaselineCommit,
		WorkspaceID: effect.WorkspaceID, BaseTreeHash: effect.BaseTreeHash, ResultTreeHash: effect.ResultTreeHash,
		ArtifactRef: effect.ArtifactRef, ContentHash: effect.ContentHash, CommandID: effect.CommandID,
		CommandVersion: effect.CommandVersion, ValidationKnown: effect.ValidationKnown, ValidationPassed: effect.ValidationPassed,
		BranchRef: effect.BranchRef, TargetBranch: effect.TargetBranch, CommitHash: effect.CommitHash, DraftChangeRef: effect.DraftChangeRef,
		CompareUrl: effect.CompareURL, ErrorCode: effect.ErrorCode, Summary: effect.Summary,
	})
	if err != nil {
		return domain.LifecycleEffect{}, fmt.Errorf("upsert remediation lifecycle effect: %w", err)
	}
	return mapLifecycleEffect(row)
}

// ListLifecycleEffects 返回 run 的有界 effect projection，供恢复和审计使用；不返回
// patch、验证输出或任何 provider payload。
func (s *RunStore) ListLifecycleEffects(ctx context.Context, runID string) ([]domain.LifecycleEffect, error) {
	rid, err := parseRunID(runID)
	if err != nil {
		return nil, err
	}
	rows, err := remediationdb.New(s.db).ListRemediationLifecycleEffects(ctx, rid)
	if err != nil {
		return nil, fmt.Errorf("list remediation lifecycle effects: %w", err)
	}
	effects := make([]domain.LifecycleEffect, 0, len(rows))
	for _, row := range rows {
		effect, mapErr := mapLifecycleEffect(row)
		if mapErr != nil {
			return nil, mapErr
		}
		effects = append(effects, effect)
	}
	return effects, nil
}

func mapLifecycleEffect(row remediationdb.RemediationLifecycleEffect) (domain.LifecycleEffect, error) {
	if !row.ID.Valid || !row.RunID.Valid || !row.CreatedAt.Valid || !row.UpdatedAt.Valid {
		return domain.LifecycleEffect{}, fmt.Errorf("remediation lifecycle effect row has invalid generated values")
	}
	effect := domain.LifecycleEffect{
		EffectID: uuidString(row.ID), RunID: uuidString(row.RunID), Kind: domain.LifecycleEffectKind(row.EffectKind),
		IdempotencyKey: row.IdempotencyKey, State: domain.LifecycleEffectState(row.State), Attempt: int(row.Attempt),
		BaselineCommit: row.BaselineCommit, WorkspaceID: row.WorkspaceID, BaseTreeHash: row.BaseTreeHash,
		ResultTreeHash: row.ResultTreeHash, ArtifactRef: row.ArtifactRef, ContentHash: row.ContentHash,
		CommandID: row.CommandID, CommandVersion: row.CommandVersion, ValidationKnown: row.ValidationKnown,
		ValidationPassed: row.ValidationPassed, BranchRef: row.BranchRef, TargetBranch: row.TargetBranch, CommitHash: row.CommitHash,
		DraftChangeRef: row.DraftChangeRef, CompareURL: row.CompareUrl, ErrorCode: row.ErrorCode,
		Summary: row.Summary, CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
	}
	if err := effect.Validate(); err != nil {
		return domain.LifecycleEffect{}, fmt.Errorf("validate remediation lifecycle effect row: %w", err)
	}
	return effect, nil
}

func (s *RunStore) GetLatestForIncident(ctx context.Context, incidentID string, generation int64, deployedCommit string) (domain.RunAggregate, error) {
	incidentUUID, err := uuid.Parse(incidentID)
	if err != nil {
		return domain.RunAggregate{}, fmt.Errorf("parse incident id %q: %w", incidentID, err)
	}
	q := remediationdb.New(s.db)
	series, err := q.GetRemediationSeries(ctx, remediationdb.GetRemediationSeriesParams{
		IncidentID:          pgtype.UUID{Bytes: incidentUUID, Valid: true},
		LifecycleGeneration: generation,
		DeployedCommit:      deployedCommit,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.RunAggregate{}, application.ErrNotFound
		}
		return domain.RunAggregate{}, fmt.Errorf("get series: %w", err)
	}
	runs, err := q.GetRemediationRunsBySeriesID(ctx, series.ID)
	if err != nil {
		return domain.RunAggregate{}, fmt.Errorf("list series runs: %w", err)
	}
	if len(runs) == 0 {
		return domain.RunAggregate{}, application.ErrNotFound
	}
	latest := runs[len(runs)-1]
	return s.Get(ctx, uuidString(latest.ID))
}

// Notify intentionally does not persist terminal events in the single-user system.
func (s *RunStore) Notify(context.Context, application.TerminalNotification) error {
	return nil
}

// GetRun retrieves a single run as a domain.Attempt. This adapter-specific
// accessor exposes attempt-level fields (attempt number, ended-at, elapsed)
// that the RunAggregate view does not carry.
func (s *RunStore) GetRun(ctx context.Context, runID uuid.UUID) (*domain.Attempt, error) {
	q := remediationdb.New(s.db)

	run, err := q.GetRemediationRun(ctx, pgtype.UUID{Bytes: runID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("run not found: %w", err)
		}
		return nil, fmt.Errorf("get run: %w", err)
	}

	return mapRunRowToAttempt(run), nil
}

func parseRunID(runID string) (pgtype.UUID, error) {
	u, err := uuid.Parse(runID)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("parse run id %q: %w", runID, err)
	}
	return pgtype.UUID{Bytes: u, Valid: true}, nil
}

func parseEvidenceUUID(value string) pgtype.UUID {
	u, err := uuid.Parse(value)
	if err != nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: u, Valid: true}
}

func optionalRunUUID(value string) (pgtype.UUID, error) {
	if strings.TrimSpace(value) == "" {
		return pgtype.UUID{}, nil
	}
	return parseRunID(value)
}

func optionalScopedUUID(value, label string) (pgtype.UUID, error) {
	if strings.TrimSpace(value) == "" {
		return pgtype.UUID{}, nil
	}
	return parseScopedUUID(value, label)
}

func optionalTime(value *time.Time) pgtype.Timestamptz {
	if value == nil || value.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

func mapStoredEvidence(row remediationdb.RemediationEvidence) (domain.StoredEvidence, error) {
	if !row.ID.Valid || !row.ProjectID.Valid || !row.EnvironmentID.Valid || !row.SourceID.Valid || !row.IncidentID.Valid ||
		!row.IngestedAt.Valid || !row.CreatedAt.Valid || !row.UpdatedAt.Valid || !json.Valid(row.Provenance) || !json.Valid(row.Payload) {
		return domain.StoredEvidence{}, fmt.Errorf("remediation evidence row has invalid generated values")
	}
	evidence := domain.StoredEvidence{
		EvidenceID: uuidString(row.ID), ProjectID: uuidString(row.ProjectID), EnvironmentID: uuidString(row.EnvironmentID),
		SourceID: uuidString(row.SourceID), IncidentID: uuidString(row.IncidentID), Provider: row.Provider,
		EvidenceKind: row.EvidenceKind, DeduplicationKey: row.DeduplicationKey,
		Classification: domain.EvidenceClassification(row.Classification), Outcome: row.Outcome,
		Available: row.Available, Primary: row.PrimaryEvidence, TemporalCorrelation: row.TemporalCorrelation,
		OperationalCorrelation: row.OperationalCorrelation, IngestedAt: row.IngestedAt.Time.UTC(),
		ContentHash: row.ContentHash, ByteCount: row.ByteCount, Provenance: append([]byte(nil), row.Provenance...),
		Payload: append([]byte(nil), row.Payload...), CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
	}
	if row.RunID.Valid {
		evidence.RunID = uuidString(row.RunID)
	}
	if row.ObservationID.Valid {
		evidence.ObservationID = uuidString(row.ObservationID)
	}
	if row.OccurredAt.Valid {
		occurredAt := row.OccurredAt.Time.UTC()
		evidence.OccurredAt = &occurredAt
	}
	if err := evidence.Validate(); err != nil {
		return domain.StoredEvidence{}, fmt.Errorf("validate remediation evidence row: %w", err)
	}
	return evidence, nil
}

func mapEvidenceIndexEntry(row remediationdb.ListContinuationEvidenceIndexRow) (domain.EvidenceIndexEntry, error) {
	if !row.ID.Valid || strings.TrimSpace(row.EvidenceKind) == "" || strings.TrimSpace(row.Provider) == "" {
		return domain.EvidenceIndexEntry{}, fmt.Errorf("remediation evidence index row has invalid values")
	}
	if err := domain.ValidateEvidenceClassification(domain.EvidenceClassification(row.Classification)); err != nil {
		return domain.EvidenceIndexEntry{}, fmt.Errorf("remediation evidence index classification is invalid: %w", err)
	}
	if len(row.ContentHash) != 64 || row.SourceAttempt < 0 {
		return domain.EvidenceIndexEntry{}, fmt.Errorf("remediation evidence index bounds are invalid")
	}
	return domain.EvidenceIndexEntry{
		EvidenceID: uuidString(row.ID), Kind: row.EvidenceKind, Provider: row.Provider,
		Classification: domain.EvidenceClassification(row.Classification),
		SourceAttempt:  row.SourceAttempt, ContentHash: row.ContentHash,
	}, nil
}

func sourceCoverage(rows []remediationdb.RemediationEvidence) []domain.SourceCoverage {
	coverage := make([]domain.SourceCoverage, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if !row.SourceID.Valid {
			continue
		}
		sourceID := uuidString(row.SourceID)
		if _, ok := seen[sourceID]; ok {
			continue
		}
		seen[sourceID] = struct{}{}
		status := domain.SourceUnavailable
		if row.Available && row.Outcome == "success" {
			status = domain.SourceInspectedSuccess
		} else if row.Outcome == "empty" {
			status = domain.SourceInspectedEmpty
		}
		coverage = append(coverage, domain.SourceCoverage{
			SourceID: sourceID, Kind: row.Provider, Primary: row.PrimaryEvidence, Status: status,
			DirectBridge: row.PrimaryEvidence && row.OperationalCorrelation,
		})
	}
	return coverage
}

func correlationFromEvidence(rows []remediationdb.RemediationEvidence) *domain.CorrelationAssessment {
	correlation := &domain.CorrelationAssessment{}
	for _, row := range rows {
		correlation.Temporal = correlation.Temporal || row.TemporalCorrelation
		correlation.Operational = correlation.Operational || row.OperationalCorrelation
		correlation.DirectBridge = correlation.DirectBridge || row.PrimaryEvidence
	}
	return correlation
}

func appendUniqueSafe(values []string, addition string) []string {
	for _, value := range values {
		if value == addition {
			return values
		}
	}
	if addition != "" {
		values = append(values, addition)
	}
	return values
}

func parseScopedUUID(value, label string) (pgtype.UUID, error) {
	u, err := uuid.Parse(value)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("parse %s id %q: %w", label, value, err)
	}
	return pgtype.UUID{Bytes: u, Valid: true}, nil
}

func sameUUID(left, right pgtype.UUID) bool {
	return left.Valid && right.Valid && left.Bytes == right.Bytes
}

func uuidString(u pgtype.UUID) string {
	return uuid.UUID(u.Bytes).String()
}

func optionalUUIDString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuidString(u)
}

func safeTriggerReason(value string) string {
	if domain.TriggerOrigin(value).IsKnown() {
		return value
	}
	return ""
}

func mapRunRowToDomainRun(row remediationdb.RemediationRun) domain.Run {
	var elapsedSeconds int64
	if row.ElapsedMs != nil {
		elapsedSeconds = *row.ElapsedMs / 1000
	}
	triggerReason := safeTriggerReason(row.TriggerReason)
	return domain.Run{
		RunID:                   uuidString(row.ID),
		SeriesID:                uuidString(row.SeriesID),
		AttemptNumber:           row.AttemptNumber,
		State:                   domain.RunState(row.State),
		Origin:                  triggerReason,
		TriggerReason:           triggerReason,
		ContinuationOfRunID:     optionalUUIDString(row.ContinuationOfRunID),
		ContinuationReason:      row.ContinuationReason,
		ContextVersion:          row.ContextVersion,
		TerminalReason:          row.TerminalReason,
		Retryable:               row.Retryable,
		AgentLoopMode:           domain.ParseAgentLoopMode(row.AgentLoopMode),
		AgentLoopPolicyVersion:  row.AgentLoopPolicyVersion,
		ExecutionMode:           domain.ParseExecutionMode(row.ExecutionMode),
		ValidationCommands:      decodeValidationCommands(row.ValidationCommands),
		ValidationImageDigest:   row.ValidationImageDigest,
		ExecutionProfile:        decodeRunSnapshot[domain.ExecutionProfileSnapshot](row.ExecutionProfile),
		PublicationSnapshot:     decodeRunSnapshot[domain.PublicationSnapshot](row.PublicationSnapshot),
		ChangePolicySnapshot:    decodeRunSnapshot[domain.ChangePolicySnapshot](row.ChangePolicy),
		PublicationTargetBranch: row.PublicationTargetBranch,
		PublicationBranchPrefix: row.PublicationBranchPrefix,
		AnalysisOnly:            row.AnalysisOnly,
		Budget: domain.BudgetCounters{
			ElapsedSeconds:  elapsedSeconds,
			ModelCalls:      int64(row.ModelCalls),
			ModelTokens:     row.ModelTokensIn + row.ModelTokensOut,
			ModelCostCents:  row.ModelCostCents,
			ToolCalls:       int64(row.ToolCalls),
			EvidenceBytes:   row.EvidenceBytes,
			RepositoryBytes: row.RepositoryBytes,
		},
		ModelProvider: row.ModelProvider,
		ModelName:     row.ModelName,
		Version:       row.Version,
		CreatedAt:     row.StartedAt.Time,
		UpdatedAt:     row.StartedAt.Time,
	}
}

func mapAttemptSummaries(rows []remediationdb.RemediationRun) []domain.AttemptSummary {
	if len(rows) > maxAttemptSummaries {
		rows = rows[len(rows)-maxAttemptSummaries:]
	}
	out := make([]domain.AttemptSummary, 0, len(rows))
	for _, row := range rows {
		triggerReason := safeTriggerReason(row.TriggerReason)
		out = append(out, domain.AttemptSummary{
			ID:                  uuidString(row.ID),
			RunID:               uuidString(row.ID),
			AttemptNumber:       row.AttemptNumber,
			Status:              domain.RunState(row.State),
			Origin:              triggerReason,
			ContinuationOfRunID: optionalUUIDString(row.ContinuationOfRunID),
			ContinuationReason:  row.ContinuationReason,
			ContextVersion:      row.ContextVersion,
			TerminalReason:      row.TerminalReason,
			Retryable:           row.Retryable,
			Version:             row.Version,
			CreatedAt:           row.StartedAt.Time,
			UpdatedAt:           row.StartedAt.Time,
		})
	}
	return out
}

func mapDecisions(rows []remediationdb.RemediationDecision) []domain.Decision {
	out := make([]domain.Decision, 0, len(rows))
	for _, r := range rows {
		confidence := 0.0
		if f, err := r.ConfidenceScore.Float64Value(); err == nil && f.Valid {
			confidence = f.Float64
		}
		out = append(out, domain.Decision{
			DecisionID:            uuidString(r.ID),
			Fixability:            domain.FixabilityClass(r.FixabilityClass),
			Confidence:            confidence,
			CausalReasoning:       r.Reasoning,
			Contradictions:        cloneStrings(r.Contradictions),
			MissingEvidence:       cloneStrings(r.MissingEvidence),
			EvidenceCitations:     cloneStrings(r.EvidenceCitations),
			RecommendedNextAction: r.RecommendedNextAction,
			RecordedAt:            r.DecidedAt.Time,
		})
	}
	return out
}

func mapPlans(rows []remediationdb.RemediationPlan) []domain.RepairPlanCandidate {
	out := make([]domain.RepairPlanCandidate, 0, len(rows))
	for _, r := range rows {
		planID := r.PlanKey
		if planID == "" {
			planID = uuidString(r.ID)
		}
		out = append(out, domain.RepairPlanCandidate{
			PlanID:           planID,
			EvidenceRefs:     cloneStrings(r.EvidenceRefs),
			AffectedFiles:    cloneStrings(r.AffectedFiles),
			IntendedBehavior: r.Title,
			Risk:             domain.RiskClassification(r.RiskClass),
			RollbackStrategy: r.RollbackStrategy,
			Rationale:        r.Rationale,
			Recommended:      r.IsRecommended,
		})
	}
	return out
}

func mapInvocations(rows []remediationdb.RemediationToolInvocation) []domain.ToolInvocation {
	out := make([]domain.ToolInvocation, 0, len(rows))
	for _, r := range rows {
		invocationID := uuidString(r.ID)
		if r.OutcomeRef != nil && *r.OutcomeRef != "" {
			invocationID = *r.OutcomeRef
		}
		inv := domain.ToolInvocation{
			InvocationID:  invocationID,
			ToolName:      r.ToolName,
			Phase:         domain.RunState(r.Phase),
			ResultSummary: r.Outcome,
			EvidenceIDs:   cloneStrings(r.EvidenceIds),
			InvokedAt:     r.InvokedAt.Time,
		}
		if r.ErrorCode != nil && *r.ErrorCode != "" {
			inv.Error = *r.ErrorCode
		} else if r.Outcome == "error" {
			// Historical rows predate error_code and only preserve coarse outcome.
			inv.Error = r.Outcome
		}
		out = append(out, inv)
	}
	return out
}

func mapArtifacts(rows []remediationdb.RemediationArtifact) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ReferencePath)
	}
	return out
}

func mapRunRowToAttempt(row remediationdb.RemediationRun) *domain.Attempt {
	var endedAt *time.Time
	if row.EndedAt.Valid {
		t := row.EndedAt.Time
		endedAt = &t
	}

	return &domain.Attempt{
		ID:                  uuid.UUID(row.ID.Bytes),
		SeriesID:            uuid.UUID(row.SeriesID.Bytes),
		AttemptNumber:       int(row.AttemptNumber),
		State:               domain.RunState(row.State),
		StartedAt:           row.StartedAt.Time,
		EndedAt:             endedAt,
		ElapsedMS:           row.ElapsedMs,
		ModelCalls:          int(row.ModelCalls),
		ModelTokensIn:       row.ModelTokensIn,
		ModelTokensOut:      row.ModelTokensOut,
		ModelCostCents:      row.ModelCostCents,
		ModelProvider:       row.ModelProvider,
		ModelName:           row.ModelName,
		ToolCalls:           int(row.ToolCalls),
		EvidenceBytes:       row.EvidenceBytes,
		RepositoryBytes:     row.RepositoryBytes,
		Origin:              safeTriggerReason(row.TriggerReason),
		TriggerReason:       safeTriggerReason(row.TriggerReason),
		ContinuationOfRunID: uuid.UUID(row.ContinuationOfRunID.Bytes),
		ContinuationReason:  row.ContinuationReason,
		ContextVersion:      row.ContextVersion,
		TerminalReason:      row.TerminalReason,
		Retryable:           row.Retryable,
		Version:             row.Version,
	}
}

func safeModelIdentity(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 || containsSensitiveMarker(value) {
		return ""
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			continue
		}
		switch r {
		case '.', '-', '_', '/', ':':
			continue
		default:
			return ""
		}
	}
	return value
}

func containsSensitiveMarker(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{
		"password", "token", "secret", "credential", "authorization",
		"apikey", "api_key", "api-key", "bearer", "sk-",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func isTerminalState(state domain.RunState) bool {
	switch state {
	case domain.RunStateFailed, domain.RunStateBudgetExhausted,
		domain.RunStateCompletedNonCode, domain.RunStateBlockedManualReview,
		domain.RunStateDiagnosisReadyForReview, domain.RunStateAwaitingHumanReview:
		return true
	default:
		return false
	}
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

func boundSuggestedDiff(diff string) string {
	if len(diff) <= maxSuggestedDiffBytes {
		return diff
	}
	cut := maxSuggestedDiffBytes
	for cut > 0 && !utf8.RuneStart(diff[cut]) {
		cut--
	}
	return diff[:cut]
}

func suggestedDiffFromArtifacts(rows []remediationdb.RemediationArtifact) string {
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].ArtifactType == "suggested_diff" && rows[i].Excerpt != "" {
			return rows[i].Excerpt
		}
	}
	return ""
}

func recommendedPlanID(rows []remediationdb.RemediationPlan) string {
	for _, row := range rows {
		if row.IsRecommended {
			if row.PlanKey != "" {
				return row.PlanKey
			}
			return uuidString(row.ID)
		}
	}
	return ""
}
