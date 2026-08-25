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

	"fixthe/backend/internal/modules/remediation/adapter/postgres/remediationdb"
	"fixthe/backend/internal/modules/remediation/application"
	"fixthe/backend/internal/modules/remediation/domain"
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
	db transactor
}

const maxSuggestedDiffBytes = 64 * 1024

// Compile-time assertion that the adapter satisfies the frozen port and review companions.
var (
	_ domain.RunStore                = (*RunStore)(nil)
	_ domain.EvidencePersistencePort = (*RunStore)(nil)
	_ domain.BootstrapEvidenceLoader = (*RunStore)(nil)
	_ domain.ToolPolicyResolver      = (*RunStore)(nil)
	_ application.ReviewRecorder     = (*RunStore)(nil)
	_ application.ReviewQuery        = (*RunStore)(nil)
	_ application.NotificationSink   = (*RunStore)(nil)
)

// NewRunStore 用进程 PostgreSQL 连接构造 RunStore。
func NewRunStore(database transactor) (*RunStore, error) {
	if database == nil {
		return nil, fmt.Errorf("remediation database is required")
	}
	return &RunStore{db: database}, nil
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
		SeriesID:      series.ID,
		AttemptNumber: 1,
		State:         string(domain.RunStateQueued),
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
	return domain.Run{
		RunID:               uuidString(run.ID),
		SeriesID:            uuidString(series.ID),
		IncidentID:          incidentID,
		LifecycleGeneration: series.LifecycleGeneration,
		DeployedCommit:      series.DeployedCommit,
		AttemptNumber:       run.AttemptNumber,
		State:               domain.RunState(run.State),
		Version:             run.Version,
		CreatedAt:           run.StartedAt.Time,
		UpdatedAt:           run.StartedAt.Time,
	}
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
		RunID:      rid,
		Sequence:   int32(len(invocations) + 1),
		ToolName:   t.ToolName,
		Phase:      string(t.Phase),
		DurationMs: durationMs,
		Outcome:    outcome,
	})
	if err != nil {
		return fmt.Errorf("create invocation: %w", err)
	}

	return tx.Commit(ctx)
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
		ID:      rid,
		State:   string(toState),
		Column3: isTerminalState(toState),
		Version: run.Version,
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

	agg := domain.RunAggregate{
		Run:                mapRunRowToDomainRun(run),
		Decisions:          mapDecisions(decisions),
		Plans:              mapPlans(plans),
		ToolInvocations:    mapInvocations(invocations),
		ArtifactReferences: mapArtifacts(artifacts),
		SuggestedDiff:      suggestedDiffFromArtifacts(artifacts),
		RecommendedPlanID:  recommendedPlanID(plans),
	}
	series, err := q.GetRemediationSeriesByID(ctx, run.SeriesID)
	if err == nil {
		agg.Run.IncidentID = uuidString(series.IncidentID)
		agg.Run.LifecycleGeneration = series.LifecycleGeneration
		agg.Run.DeployedCommit = series.DeployedCommit
	}
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

// GetLatestForIncident 读取 series key 下 attempt 最大的 run；没有 series 时返回 ErrNotFound。
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

// Notify 通过 run→series→incident 连接写入系统 actor 的 audit_events。
// metadata 只允许 runId/state/fixability/kind，禁止 prompt、diff、凭据和原始日志。
func (s *RunStore) Notify(ctx context.Context, n application.TerminalNotification) error {
	rid, err := parseRunID(n.RunID)
	if err != nil {
		return err
	}
	auditID, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("allocate audit id: %w", err)
	}
	metadata, err := json.Marshal(application.NotificationMetadata(n))
	if err != nil {
		return fmt.Errorf("encode notification metadata: %w", err)
	}
	summary := strings.TrimSpace(n.Summary)
	if summary == "" {
		summary = "Remediation reached a reviewable outcome."
	}
	if utf8.RuneCountInString(summary) > 240 {
		summary = string([]rune(summary)[:240])
	}
	affected, err := remediationdb.New(s.db).CreateRemediationAuditEvent(ctx, remediationdb.CreateRemediationAuditEventParams{
		AuditID:  pgtype.UUID{Bytes: auditID, Valid: true},
		Action:   n.Kind,
		Summary:  summary,
		Metadata: metadata,
		RunID:    rid,
	})
	if err != nil {
		return fmt.Errorf("create remediation audit event: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("create remediation audit event: run %s not found", n.RunID)
	}
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

func uuidString(u pgtype.UUID) string {
	return uuid.UUID(u.Bytes).String()
}

func mapRunRowToDomainRun(row remediationdb.RemediationRun) domain.Run {
	var elapsedSeconds int64
	if row.ElapsedMs != nil {
		elapsedSeconds = *row.ElapsedMs / 1000
	}
	return domain.Run{
		RunID:         uuidString(row.ID),
		SeriesID:      uuidString(row.SeriesID),
		AttemptNumber: row.AttemptNumber,
		State:         domain.RunState(row.State),
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
		inv := domain.ToolInvocation{
			InvocationID:  uuidString(r.ID),
			ToolName:      r.ToolName,
			Phase:         domain.RunState(r.Phase),
			ResultSummary: r.Outcome,
			InvokedAt:     r.InvokedAt.Time,
		}
		if r.Outcome == "error" {
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
		ID:              uuid.UUID(row.ID.Bytes),
		SeriesID:        uuid.UUID(row.SeriesID.Bytes),
		AttemptNumber:   int(row.AttemptNumber),
		State:           domain.RunState(row.State),
		StartedAt:       row.StartedAt.Time,
		EndedAt:         endedAt,
		ElapsedMS:       row.ElapsedMs,
		ModelCalls:      int(row.ModelCalls),
		ModelTokensIn:   row.ModelTokensIn,
		ModelTokensOut:  row.ModelTokensOut,
		ModelCostCents:  row.ModelCostCents,
		ModelProvider:   row.ModelProvider,
		ModelName:       row.ModelName,
		ToolCalls:       int(row.ToolCalls),
		EvidenceBytes:   row.EvidenceBytes,
		RepositoryBytes: row.RepositoryBytes,
		Version:         row.Version,
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
		domain.RunStateDiagnosisReadyForReview:
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
