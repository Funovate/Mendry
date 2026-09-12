package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"mendry/backend/internal/modules/remediation/domain"
	"mendry/backend/internal/modules/remediation/port"
)

// Compile-time assertion that the coordinator satisfies the port contract.
var _ port.Coordinator = (*RemediationCoordinator)(nil)

// defaultMaxCollectLoops bounds the collect-more-context loop.
const defaultMaxCollectLoops = 3

const maxConsecutiveProtocolFailures = 3

// RemediationCoordinator is the only component allowed to advance run state. It
// drives the diagnosis/planning walking-skeleton subset and, for resilient_v1,
// the explicit selected-plan lifecycle:
//
//	queued → preparing_context → diagnosing →
//	  {collecting_more_context loop | completed_non_code |
//	   blocked_manual_review | planning → diagnosis_ready_for_review}
//
//	 diagnosis_ready_for_review → patching → validating → publishing →
//	   awaiting_human_review
//
// Validation failures may return to patching within the bounded revision contract.
// Any active state can transition to failed. The coordinator holds only domain
// ports plus the in-process engine, assembler, and gateway; it never receives
// credentials or raw clients.
type RemediationCoordinator struct {
	store           domain.RunStore
	lookup          IncidentLookup
	remotes         RepositoryRemoteResolver
	reviews         ReviewRecorder
	notifications   NotificationSink
	contextAssem    *ContextAssembler
	agentEngine     *AgentEngine
	toolGateway     *ToolGateway
	sourceCaps      domain.SourceCapabilityResolver
	dynamicRuntime  domain.DynamicToolRuntimePort
	budgetLimits    domain.BudgetLimits
	maxCollectLoops int
	observer        RunObserver
	evidenceGate    *EvidenceGate
	bootstrapLoader domain.BootstrapEvidenceLoader
	// checkpointStore 是可选注入的 durable working-memory checkpoint store
	// （D2）。nil（默认）或 run 快照模式为 legacy 时，coordinator 完全走既有
	// 路径；只有 resilient_v1 run 才 append/load checkpoint。
	checkpointStore    domain.CheckpointStore
	planPolicy         domain.PlanPolicyEvaluator
	workspace          domain.WorkspacePort
	validation         domain.ValidationPort
	publication        domain.PublicationPort
	lifecycleStore     domain.LifecycleStore
	lifecycleTools     *LifecycleToolGateway
	validationCommands map[string]int64
	publicationPolicy  LifecyclePublicationPolicy
	// resilienceMetrics 是可选的低基数恢复/生命周期指标 observer（Phase 4）。
	// nil 或缺省时走 no-op，metrics 绝不改变 run/预算/检查点语义。
	resilienceMetrics ResilienceMetricObserver
}

// IncidentIdentity 是 remediation 需要的事故身份，不含凭据或客户端。
type IncidentIdentity struct {
	ID                  string
	ProjectID           string
	EnvironmentID       string
	SourceID            string
	Priority            string
	DeployedCommit      string
	Number              int64
	LifecycleGeneration int64
	ContextVersion      int64
}

// IncidentLookup 按事故编号或内部 UUID 解析项目/来源身份与 series key。
// 手动 HTTP 入口使用项目范围查询防止跨项目启动；冻结 Coordinator port
// 仍保留全局编号路径以证明其请求不携带凭据或数据库客户端。
// GetByID 供 Start 在已持有内部 UUID 时补齐 EnvironmentID / SourceID。
type IncidentLookup interface {
	GetByNumber(ctx context.Context, number int64) (IncidentIdentity, error)
	GetByProjectNumber(ctx context.Context, projectID string, number int64) (IncidentIdentity, error)
	GetByID(ctx context.Context, incidentID string) (IncidentIdentity, error)
}

// RepositoryRemoteResolver 按项目 UUID 返回不含 userinfo 的仓库 RemoteURL。
// 适配器在内部加载配置；application 不得因此导入 HTTP 或 postgres。
type RepositoryRemoteResolver interface {
	CredentialFreeRemoteURL(ctx context.Context, projectID string) (string, error)
}

// NewRemediationCoordinator wires the coordinator from the four frozen ports.
func NewRemediationCoordinator(
	store domain.RunStore,
	repoPort domain.RepositoryReadPort,
	evidencePort domain.EvidenceLogPort,
	llmPort domain.LLMProviderPort,
) *RemediationCoordinator {
	return newRemediationCoordinator(store, repoPort, evidencePort, llmPort, nil, nil, nil, DefaultBudgetLimits())
}

// NewRemediationCoordinatorWithBudgetLimits 使用显式预算构造 coordinator。
// 该入口供项目级策略和测试收紧预算；零值字段会回落到默认预算。
func NewRemediationCoordinatorWithBudgetLimits(
	store domain.RunStore,
	repoPort domain.RepositoryReadPort,
	evidencePort domain.EvidenceLogPort,
	llmPort domain.LLMProviderPort,
	limits domain.BudgetLimits,
) *RemediationCoordinator {
	return newRemediationCoordinator(store, repoPort, evidencePort, llmPort, nil, nil, nil, limits)
}

// NewRemediationCoordinatorWithLookup 注入事故查找 seam，供手动 start / 冻结 port 使用。
func NewRemediationCoordinatorWithLookup(
	store domain.RunStore,
	repoPort domain.RepositoryReadPort,
	evidencePort domain.EvidenceLogPort,
	llmPort domain.LLMProviderPort,
	lookup IncidentLookup,
) *RemediationCoordinator {
	return newRemediationCoordinator(store, repoPort, evidencePort, llmPort, lookup, nil, nil, DefaultBudgetLimits())
}

// NewRemediationCoordinatorWithReview 注入 review 持久化与终态通知 companion ports。
func NewRemediationCoordinatorWithReview(
	store domain.RunStore,
	repoPort domain.RepositoryReadPort,
	evidencePort domain.EvidenceLogPort,
	llmPort domain.LLMProviderPort,
	lookup IncidentLookup,
	reviews ReviewRecorder,
	notifications NotificationSink,
) *RemediationCoordinator {
	return newRemediationCoordinator(store, repoPort, evidencePort, llmPort, lookup, reviews, notifications, DefaultBudgetLimits())
}

// NewRemediationCoordinatorWithRuntime 注入事故查找、无凭据仓库 URL 解析、review 与通知。
func NewRemediationCoordinatorWithRuntime(
	store domain.RunStore,
	repoPort domain.RepositoryReadPort,
	evidencePort domain.EvidenceLogPort,
	llmPort domain.LLMProviderPort,
	lookup IncidentLookup,
	remotes RepositoryRemoteResolver,
	reviews ReviewRecorder,
	notifications NotificationSink,
) *RemediationCoordinator {
	coord := newRemediationCoordinator(store, repoPort, evidencePort, llmPort, lookup, reviews, notifications, DefaultBudgetLimits())
	coord.remotes = remotes
	return coord
}

// NewRemediationCoordinatorWithObservedRuntime 在完整 runtime wiring 上增加
// credential-free observer；既有构造器继续保持 no-op 兼容行为。
func NewRemediationCoordinatorWithObservedRuntime(
	store domain.RunStore,
	repoPort domain.RepositoryReadPort,
	evidencePort domain.EvidenceLogPort,
	llmPort domain.LLMProviderPort,
	lookup IncidentLookup,
	remotes RepositoryRemoteResolver,
	reviews ReviewRecorder,
	notifications NotificationSink,
	observer RunObserver,
) *RemediationCoordinator {
	coord := NewRemediationCoordinatorWithRuntime(
		store, repoPort, evidencePort, llmPort, lookup, remotes, reviews, notifications,
	)
	coord.observer = normalizeRunObserver(observer)
	return coord
}

// NewRemediationCoordinatorWithDynamicRuntime wires the optional source
// capability snapshot, MCP runtime, and versioned tool-policy resolver. The
// legacy constructors remain static and continue to support existing projects
// and tests without a policy row.
func NewRemediationCoordinatorWithDynamicRuntime(
	store domain.RunStore,
	repoPort domain.RepositoryReadPort,
	evidencePort domain.EvidenceLogPort,
	inspectPort domain.SSHInspectPort,
	llmPort domain.LLMProviderPort,
	lookup IncidentLookup,
	remotes RepositoryRemoteResolver,
	reviews ReviewRecorder,
	notifications NotificationSink,
	observer RunObserver,
	sourceCaps domain.SourceCapabilityResolver,
	dynamicRuntime domain.DynamicToolRuntimePort,
	policy domain.ToolPolicyResolver,
) *RemediationCoordinator {
	coord := NewRemediationCoordinatorWithObservedRuntime(
		store, repoPort, evidencePort, llmPort, lookup, remotes, reviews, notifications, observer,
	)
	coord.toolGateway = NewToolGatewayWithDynamicRuntime(repoPort, evidencePort, inspectPort, dynamicRuntime, policy)
	coord.agentEngine = NewAgentEngine(llmPort, coord.toolGateway)
	coord.sourceCaps = sourceCaps
	coord.dynamicRuntime = dynamicRuntime
	return coord
}

// NewRemediationCoordinatorWithEvidenceResolver adds the trusted persistence
// lookup used by the planning evidence gate without changing frozen ports.
func NewRemediationCoordinatorWithEvidenceResolver(
	store domain.RunStore,
	repoPort domain.RepositoryReadPort,
	evidencePort domain.EvidenceLogPort,
	llmPort domain.LLMProviderPort,
	resolver EvidenceResolver,
) *RemediationCoordinator {
	coord := newRemediationCoordinator(store, repoPort, evidencePort, llmPort, nil, nil, nil, DefaultBudgetLimits())
	coord.evidenceGate = NewEvidenceGate(resolver)
	return coord
}

// SetEvidenceResolver is intended for composition-root wiring of the trusted
// project/run evidence reader. It does not expose evidence or credentials.
func (c *RemediationCoordinator) SetEvidenceResolver(resolver EvidenceResolver) {
	c.evidenceGate = NewEvidenceGate(resolver)
}

// SetBootstrapEvidenceLoader wires the trusted incident-scoped evidence
// reader. Existing constructors remain compatible and simply start without a
// pre-run evidence snapshot until the composition root supplies one.
func (c *RemediationCoordinator) SetBootstrapEvidenceLoader(loader domain.BootstrapEvidenceLoader) {
	c.bootstrapLoader = loader
}

// SetCheckpointStore 注入 durable working-memory checkpoint store（D2）。
// 该端口可选：未注入时 resilient_v1 run 也不启用 checkpoint/recovery 路径
// （fail closed 到 legacy 行为），组合根可以在项目启用 resilient_v1 后随时
// 补齐而不改变 run 语义。
func (c *RemediationCoordinator) SetCheckpointStore(store domain.CheckpointStore) {
	c.checkpointStore = store
}

// SetResilienceMetricObserver 注入可选的低基数恢复/生命周期指标 observer。
// nil/缺省保持 no-op；metrics 事件只携带枚举 kind、run 身份、mode 与 reason
// code，绝不包含 evidence/model 内容，也绝不改变 run 语义（Phase 4）。
func (c *RemediationCoordinator) SetResilienceMetricObserver(observer ResilienceMetricObserver) {
	c.resilienceMetrics = normalizeResilienceMetricObserver(observer)
}

// SetDockerEvidencePort wires the credential-free Docker log port at the
// composition root; ordinary application callers cannot provide a container ID
// or a remote command.
func (c *RemediationCoordinator) SetDockerEvidencePort(port domain.DockerEvidencePort) {
	c.toolGateway.SetDockerEvidencePort(port)
}

// SetEvidenceReadPort 注入同 series 持久化证据的按 ID 分页读取端口（R9）。
// 没有该端口时 evidence.read 在 gateway 边界 fail closed。
func (c *RemediationCoordinator) SetEvidenceReadPort(port domain.EvidenceReadPort) {
	c.toolGateway.SetEvidenceReadPort(port)
}

// SetTencentCLSDetailPort 将受信任、incident-bound 的 Tencent CLS detail reader 接到
// mandatory pre-diagnosis evidence gate。
func (c *RemediationCoordinator) SetTencentCLSDetailPort(port domain.TencentCLSDetailPort) {
	c.toolGateway.SetTencentCLSDetailPort(port)
}

// SetRuntimeEvidenceWriter 在 composition root 注入 canonical runtime evidence
// writer。SSH inspect 与 Docker logs 的成功结果必须先持久化，才能作为可引用的
// evidence ID 进入模型上下文；没有 writer 时 observed 工具 fail closed。
func (c *RemediationCoordinator) SetRuntimeEvidenceWriter(writer RuntimeEvidenceWriter) {
	c.toolGateway.SetRuntimeEvidenceWriter(writer)
}

func newRemediationCoordinator(
	store domain.RunStore,
	repoPort domain.RepositoryReadPort,
	evidencePort domain.EvidenceLogPort,
	llmPort domain.LLMProviderPort,
	lookup IncidentLookup,
	reviews ReviewRecorder,
	notifications NotificationSink,
	limits domain.BudgetLimits,
) *RemediationCoordinator {
	gateway := NewToolGateway(repoPort, evidencePort)
	return &RemediationCoordinator{
		store:              store,
		lookup:             lookup,
		reviews:            reviews,
		notifications:      notifications,
		contextAssem:       NewContextAssembler(repoPort, evidencePort),
		agentEngine:        NewAgentEngine(llmPort, gateway),
		toolGateway:        gateway,
		budgetLimits:       normalizeBudgetLimits(limits),
		maxCollectLoops:    defaultMaxCollectLoops,
		observer:           noopRunObserver{},
		evidenceGate:       NewEvidenceGate(nil),
		planPolicy:         NewDefaultPlanPolicy(),
		validationCommands: map[string]int64{},
		lifecycleTools:     NewLifecycleToolGateway(nil, nil),
	}
}

// Start 用真实 UUID 创建或复用 series 根 run，仅当状态仍为 queued 时 drive。
func (c *RemediationCoordinator) Start(ctx context.Context, in domain.NewRun) (domain.Run, error) {
	run, err := c.store.CreateSeriesAndRun(ctx, in)
	if err != nil {
		return domain.Run{}, fmt.Errorf("create series and run: %w", err)
	}
	if in.AnalysisOnly && !run.AnalysisOnly {
		return run, ErrLifecycleUnavailable
	}
	if run.State != domain.RunStateQueued {
		return run, nil
	}
	return c.runQueued(ctx, run, "", "", "", domain.RunStateDiagnosing, in.TriggerReason, in.Priority, nil)
}

// Continue 创建并驱动一个新的 linked attempt。已通过 evidence gate 的 durable
// code_fixable diagnosis 可直接恢复 planning；其余前置结果仍从 diagnosis 重新验证。
// AttemptStore 在事务内再次执行 predecessor/version 检查。
func (c *RemediationCoordinator) Continue(ctx context.Context, in domain.NextAttempt) (domain.Run, error) {
	prepared, err := c.prepareContinuation(ctx, in)
	if err != nil {
		return domain.Run{}, err
	}
	return c.runQueued(ctx, prepared.child, prepared.brief, prepared.priorEvidence, prepared.reconstruction,
		prepared.resumePhase, prepared.child.TriggerReason, "", prepared.priorInvocations)
}

// Resume 从 durable state 恢复同一个 resilient_v1 attempt。diagnosis/planning
// 从最新 checkpoint 重建 provider-neutral context；lifecycle phase 额外复用
// durable external-effect projection，终态重复调用保持幂等。
func (c *RemediationCoordinator) Resume(ctx context.Context, runID string) (domain.Run, error) {
	aggregate, err := c.store.Get(ctx, runID)
	if err != nil {
		return domain.Run{}, fmt.Errorf("load remediation run for resume: %w", err)
	}
	run := aggregate.Run
	if run.AnalysisOnly && (run.State == domain.RunStatePatching || run.State == domain.RunStateValidating ||
		run.State == domain.RunStatePublishing || run.State == domain.RunStateAwaitingHumanReview) {
		return run, ErrLifecycleUnavailable
	}
	if domain.ParseAgentLoopMode(string(run.AgentLoopMode)) != domain.AgentLoopModeResilientV1 || c.checkpointStore == nil {
		return run, ErrLifecycleUnavailable
	}
	switch run.State {
	case domain.RunStatePatching, domain.RunStateValidating, domain.RunStatePublishing, domain.RunStateAwaitingHumanReview:
		return c.ResumeLifecycle(ctx, runID)
	case domain.RunStateDiagnosisReadyForReview, domain.RunStateCompletedNonCode,
		domain.RunStateBlockedManualReview, domain.RunStateFailed, domain.RunStateBudgetExhausted:
		return run, nil
	case domain.RunStateCollectingMoreContext:
		// collecting_more_context 是 diagnosing 内部的 durable 子状态。checkpoint
		// 不保存 raw model turn；restart 先以无效果 transition 回到 diagnosing，
		// 再从进入/退出 collecting 边界的 provider-neutral checkpoint 继续。
		if err := c.transition(ctx, run.RunID, domain.RunStateCollectingMoreContext, domain.RunStateDiagnosing, domain.Effect{}); err != nil {
			return domain.Run{}, err
		}
		refreshed, loadErr := c.store.Get(ctx, run.RunID)
		if loadErr != nil {
			return domain.Run{}, fmt.Errorf("reload remediation run after collecting recovery: %w", loadErr)
		}
		return c.resumeAnalysis(ctx, refreshed)
	case domain.RunStatePreparingContext, domain.RunStateDiagnosing, domain.RunStatePlanning:
		return c.resumeAnalysis(ctx, aggregate)
	default:
		return run, fmt.Errorf("%w: state %s is not resumable", ErrLifecycleNotReady, run.State)
	}
}

func (c *RemediationCoordinator) resumeAnalysis(ctx context.Context, aggregate domain.RunAggregate) (domain.Run, error) {
	run := aggregate.Run
	ctx = withRunObservationContext(ctx, run)
	tracker := newResilientRunState(c.checkpointStore, run, "")
	analysisOnly := run.AnalysisOnly || run.TriggerReason == domain.TriggerOriginManualContinue
	tracker.analysisOnly = analysisOnly
	for _, invocation := range aggregate.ToolInvocations {
		tracker.recordPriorToolAction(invocation)
	}
	if c.checkpointStore != nil {
		if snapshot, err := c.checkpointStore.LoadLatestCheckpoint(ctx, run.RunID); err == nil {
			if err := restoreAnalysisCheckpoint(tracker, snapshot, run); err != nil {
				return domain.Run{}, err
			}
			tracker.reconstruction = renderCheckpointReconstruction(snapshot, nil, nil, aggregate.ToolInvocations)
		} else if !errors.Is(err, domain.ErrCheckpointNotFound) ||
			(run.State != domain.RunStatePreparingContext && !(run.State == domain.RunStateDiagnosing && run.Budget.ModelCalls == 0)) {
			// 初始 preparing→diagnosing after-window 尚无 checkpoint；zero model calls
			// 证明尚未进入首轮 provider turn，可从 run/bootstrap authority 重建。
			return domain.Run{}, fmt.Errorf("load remediation checkpoint for resume: %w", err)
		}
	} else if run.State != domain.RunStatePreparingContext &&
		!(run.State == domain.RunStateDiagnosing && run.Budget.ModelCalls == 0) {
		// Legacy runs without a checkpoint store retain the historical restart
		// boundary: only pre-first-turn states can be rebuilt safely.
		return domain.Run{}, fmt.Errorf("load remediation checkpoint for resume: %w", domain.ErrCheckpointNotFound)
	}
	if tracker.alloc == nil {
		budgetPhase := run.State
		if budgetPhase == domain.RunStatePreparingContext {
			budgetPhase = domain.RunStateDiagnosing
		}
		tracker.admitSoftBudget(c.budgetLimits, budgetPhase)
	}
	ctx = withResilientRunState(ctx, tracker)
	ref, scope, err := c.resolveRefs(ctx, run)
	if err != nil {
		return domain.Run{}, c.fail(ctx, run.RunID, run.State, err)
	}
	if err := c.drive(ctx, run.RunID, ref, scope, "", "", run.State, run.State, aggregate.ToolInvocations, analysisOnly); err != nil {
		return domain.Run{}, err
	}
	return c.loadLifecycleRun(ctx, run.RunID)
}

func restoreAnalysisCheckpoint(tracker *resilientRunState, snapshot domain.CheckpointSnapshot, run domain.Run) error {
	checkpoint := snapshot.Checkpoint
	if err := checkpoint.Validate(); err != nil {
		return fmt.Errorf("validate remediation checkpoint for resume: %w", err)
	}
	if checkpoint.RunID != run.RunID || checkpoint.SeriesID != run.SeriesID || checkpoint.ContextVersion != run.ContextVersion {
		return fmt.Errorf("validate remediation checkpoint for resume: identity mismatch: checkpoint %s/%s/%d, run %s/%s/%d",
			checkpoint.RunID, checkpoint.SeriesID, checkpoint.ContextVersion, run.RunID, run.SeriesID, run.ContextVersion)
	}
	tracker.observedVersion = run.Version
	tracker.evidenceIndex = append([]domain.CheckpointEvidenceIndexItem(nil), checkpoint.EvidenceIndex...)
	tracker.recoveries = append([]domain.CheckpointRecovery(nil), checkpoint.Recoveries...)
	tracker.nextActions = append([]string(nil), checkpoint.NextActions...)
	tracker.recoveryEpisodeOpen = checkpoint.Reason == domain.CheckpointReasonRecovery
	if checkpoint.RecoveryProgress != nil {
		tracker.restoreRecoveryProgressSnapshot(*checkpoint.RecoveryProgress)
	} else {
		tracker.restoreRecoveryProgress()
	}
	if checkpoint.Budget.SchemaVersion != "" {
		allocator, err := restorePhaseBudgetPlan(checkpoint.Budget)
		if err != nil {
			return fmt.Errorf("restore remediation budget for resume: %w", err)
		}
		tracker.alloc = allocator
	}
	if err := reconcileLifecycleCheckpoint(tracker, snapshot, run); err != nil {
		return err
	}
	tracker.checkpointNeedsRebuild = snapshot.NeedsRebuild || checkpoint.ObservedRunVersion < run.Version
	return nil
}

// preparedContinuation 是 prepareContinuation 的只读结果：queued child 及其
// bounded continuation 输入。brief 只含 predecessor 元数据；priorEvidence 是
// 同 series 的 runtime evidence/index 渲染；reconstruction 是 resilient_v1
// 下从 durable checkpoint 重建的工作记忆块（存在时取代 priorEvidence）。
type preparedContinuation struct {
	child            domain.Run
	brief            string
	priorEvidence    string
	reconstruction   string
	resumePhase      domain.RunState
	priorInvocations []domain.ToolInvocation
}

// prepareContinuation 只执行 continuation 的读取、校验和 queued child 持久化。
// 它不访问 connector 或 model，使 HTTP 可以在创建 attempt 后立即返回；调用方
// 负责决定同步驱动还是在脱离请求取消的后台上下文中驱动。
func (c *RemediationCoordinator) prepareContinuation(
	ctx context.Context,
	in domain.NextAttempt,
) (preparedContinuation, error) {
	if err := in.Validate(); err != nil {
		return preparedContinuation{}, fmt.Errorf("%w: %v", domain.ErrInvalidNextAttempt, err)
	}
	attempts, ok := c.store.(domain.AttemptStore)
	if !ok {
		return preparedContinuation{}, fmt.Errorf("continuation attempt store is required")
	}
	predecessor, err := c.store.Get(ctx, in.ContinuationOfRunID)
	if err != nil {
		return preparedContinuation{}, fmt.Errorf("load continuation predecessor: %w", err)
	}
	if err := validateContinuationPredecessor(predecessor.Run, in); err != nil {
		return preparedContinuation{}, err
	}
	var planningCheckpoint *domain.RunAggregate
	if predecessor.Run.ContextVersion == in.ContextVersion {
		checkpoints, ok := c.store.(domain.PlanningCheckpointStore)
		if !ok {
			return preparedContinuation{}, fmt.Errorf("continuation planning checkpoint store is required")
		}
		checkpoint, checkpointErr := checkpoints.GetLatestPlanningCheckpoint(
			ctx, in.SeriesID, in.ContextVersion, predecessor.Run.AttemptNumber,
		)
		switch {
		case checkpointErr == nil:
			if !validContinuationPlanningCheckpoint(checkpoint, predecessor.Run, in) {
				return preparedContinuation{}, fmt.Errorf("continuation planning checkpoint is invalid")
			}
			planningCheckpoint = &checkpoint
		case errors.Is(checkpointErr, domain.ErrPlanningCheckpointNotFound):
			// 同一 context 没有 durable code_fixable checkpoint 时按正常 diagnosis 路径继续。
		default:
			return preparedContinuation{}, fmt.Errorf("load continuation planning checkpoint: %w", checkpointErr)
		}
	}
	brief := buildContinuationBrief(predecessor, planningCheckpoint, in)
	resumePhase := continuationResumePhase(planningCheckpoint)
	priorEvidence := ""
	var priorRuntimeEvidence []domain.StoredEvidence
	var priorEvidenceIndex []domain.EvidenceIndexEntry
	if resumePhase == domain.RunStateDiagnosing {
		// diagnosis continuation 引用同 series 早期 attempt 的证据：runtime 记录以
		// sanitized 全量形式渲染并保留原始证据 ID（供引用与 evidence gate 解析），
		// 其余证据种类（provider_detail、normalized_alert 等）以紧凑索引进入 brief，
		// 模型凭索引条目通过 evidence.read 重新读取原始内容。
		// planning checkpoint 继续使用紧凑 brief，不加载任何证据记录。
		if loader, ok := c.store.(domain.ContinuationEvidenceStore); ok {
			var loadErr error
			priorRuntimeEvidence, loadErr = loader.ListContinuationRuntimeEvidence(ctx, domain.ContinuationEvidenceQuery{
				SeriesID: in.SeriesID, ThroughAttemptNumber: predecessor.Run.AttemptNumber, Limit: maxContinuationEvidence,
			})
			if loadErr != nil {
				return preparedContinuation{}, fmt.Errorf("load continuation runtime evidence: %w", loadErr)
			}
			if rendered := renderContinuationRuntimeEvidence(priorRuntimeEvidence); rendered != "" {
				priorEvidence += rendered
			}
			var indexErr error
			priorEvidenceIndex, indexErr = loader.ListContinuationEvidenceIndex(ctx, domain.ContinuationEvidenceQuery{
				SeriesID: in.SeriesID, ThroughAttemptNumber: predecessor.Run.AttemptNumber, Limit: maxContinuationEvidenceIndex,
			})
			if indexErr != nil {
				return preparedContinuation{}, fmt.Errorf("load continuation evidence index: %w", indexErr)
			}
			if rendered := renderContinuationEvidenceIndex(priorEvidenceIndex); rendered != "" {
				if priorEvidence != "" {
					priorEvidence += "\n\n"
				}
				priorEvidence += rendered
			}
		}
	}
	child, err := attempts.CreateNextAttempt(ctx, in)
	if err != nil {
		return preparedContinuation{}, err
	}
	if child.State != domain.RunStateQueued || child.RunID == "" || child.SeriesID != in.SeriesID ||
		child.IncidentID != in.IncidentID || child.LifecycleGeneration != in.LifecycleGeneration ||
		child.DeployedCommit != in.DeployedCommit || child.ContinuationOfRunID != in.ContinuationOfRunID ||
		child.AttemptNumber != predecessor.Run.AttemptNumber+1 ||
		(predecessor.Run.AnalysisOnly && !child.AnalysisOnly) {
		return preparedContinuation{}, domain.ErrStalePredecessor
	}
	// resilient_v1：从 predecessor 的 durable working-memory checkpoint 重建
	// 诊断上下文（D2/AC7）。只有 diagnosis 续跑才重建；planning 续跑沿用紧凑
	// brief。加载失败（无 checkpoint / 损坏 / 身份不一致）保持既有 brief 路径，
	// 不阻断 continuation。
	reconstruction := ""
	if resumePhase == domain.RunStateDiagnosing && c.checkpointStore != nil &&
		domain.ParseAgentLoopMode(string(child.AgentLoopMode)) == domain.AgentLoopModeResilientV1 {
		snapshot, loadErr := c.checkpointStore.LoadLatestCheckpoint(ctx, in.ContinuationOfRunID)
		switch {
		case loadErr == nil:
			if snapshot.Checkpoint.SeriesID == in.SeriesID && snapshot.ContextVersion == in.ContextVersion {
				reconstruction = renderCheckpointReconstruction(snapshot, priorRuntimeEvidence, priorEvidenceIndex, predecessor.ToolInvocations)
			}
		case errors.Is(loadErr, domain.ErrCheckpointNotFound):
			// 前驱没有 durable checkpoint：保持既有 brief 路径。
		default:
			// 损坏或身份不一致的 checkpoint 不阻断 continuation（best-effort）。
		}
	}
	// Phase 4 指标：resilient_v1 diagnosis continuation 从 predecessor durable
	// checkpoint 重建工作记忆（D2/AC7）时发一次 low-cardinality 事件。
	if reconstruction != "" {
		c.emitResilienceMetric(ctx, ResilienceMetric{
			Run: runIdentity(child), Mode: metricMode(child.AgentLoopMode),
			Kind: ResilienceMetricReconstruction, Phase: domain.RunStateDiagnosing,
		})
	}
	return preparedContinuation{
		child: child, brief: brief, priorEvidence: priorEvidence, reconstruction: reconstruction,
		resumePhase: resumePhase, priorInvocations: predecessor.ToolInvocations,
	}, nil
}

func continuationResumePhase(checkpoint *domain.RunAggregate) domain.RunState {
	if checkpoint != nil {
		// checkpoint 查询只返回同一 context 中最新的 durable code_fixable decision；
		// 后续失败 attempt 的低质量 diagnosis 不能覆盖已经通过的 evidence gate。
		return domain.RunStatePlanning
	}
	return domain.RunStateDiagnosing
}

func validContinuationPlanningCheckpoint(checkpoint domain.RunAggregate, predecessor domain.Run, in domain.NextAttempt) bool {
	if checkpoint.Run.RunID == "" || checkpoint.Run.SeriesID != in.SeriesID ||
		checkpoint.Run.IncidentID != in.IncidentID || checkpoint.Run.LifecycleGeneration != in.LifecycleGeneration ||
		checkpoint.Run.DeployedCommit != in.DeployedCommit || checkpoint.Run.ContextVersion != in.ContextVersion ||
		checkpoint.Run.AttemptNumber < 1 || checkpoint.Run.AttemptNumber > predecessor.AttemptNumber ||
		len(checkpoint.Decisions) == 0 {
		return false
	}
	return checkpoint.Decisions[len(checkpoint.Decisions)-1].Fixability == domain.FixabilityCodeFixable
}

func validateContinuationPredecessor(run domain.Run, in domain.NextAttempt) error {
	if run.RunID != in.ContinuationOfRunID || run.SeriesID != in.SeriesID ||
		run.IncidentID != in.IncidentID || run.LifecycleGeneration != in.LifecycleGeneration ||
		run.DeployedCommit != in.DeployedCommit || run.Version != in.ExpectedPreviousVersion {
		return domain.ErrStalePredecessor
	}
	return nil
}

func (c *RemediationCoordinator) runQueued(
	ctx context.Context,
	run domain.Run,
	continuationBrief string,
	priorEvidenceBlock string,
	reconstruction string,
	resumePhase domain.RunState,
	triggerReason string,
	priority string,
	priorInvocations []domain.ToolInvocation,
) (domain.Run, error) {
	started := time.Now()
	ctx = withRunObservationContext(ctx, run)
	if triggerReason == "" {
		triggerReason = run.TriggerReason
	}
	claimedRun, claimed, err := c.claimQueued(ctx, run)
	if err != nil {
		return domain.Run{}, err
	}
	if !claimed {
		return claimedRun, nil
	}
	analysisOnly := run.AnalysisOnly || triggerReason == domain.TriggerOriginManualContinue
	// resilient_v1 feature gate：只有 run 快照模式为 resilient_v1 且注入了
	// checkpoint store 时才挂载 per-run resilient 状态；legacy 或 nil store
	// 完全走既有路径（所有 checkpoint/recovery 辅助都是 no-op）。allocator 必须
	// 在任何可能触发 terminal checkpoint 的操作前 admit，覆盖 resolveRefs 等
	// pre-drive failure 路径。
	if c.checkpointStore != nil && domain.ParseAgentLoopMode(string(run.AgentLoopMode)) == domain.AgentLoopModeResilientV1 {
		tracker := newResilientRunState(c.checkpointStore, run, reconstruction)
		tracker.analysisOnly = analysisOnly
		tracker.admitSoftBudget(c.budgetLimits, resumePhase)
		// resilient_v1：predecessor attempt 的持久化 invocation ID/evidence ID
		// 按工具 capability 绑定，供 continuation exhaustion proof 引用。
		for _, invocation := range priorInvocations {
			tracker.recordPriorToolAction(invocation)
		}
		ctx = withResilientRunState(ctx, tracker)
	}
	c.observer.RunStarted(ctx, RunStartedObservation{
		Run: runIdentity(run), Phase: run.State, TriggerReason: triggerReason, Priority: priority,
		AgentLoopMode: domain.ParseAgentLoopMode(string(run.AgentLoopMode)), AgentLoopPolicyVersion: max(run.AgentLoopPolicyVersion, 0),
	})
	c.emitResilienceMetric(ctx, ResilienceMetric{
		Run: runIdentity(run), Mode: metricMode(run.AgentLoopMode), Kind: ResilienceMetricRunStarted, Phase: run.State,
	})
	c.observer.StateTransitioned(ctx, StateTransitionObservation{
		Run: runIdentity(run), From: domain.RunStateQueued, To: domain.RunStatePreparingContext,
		Effect: domain.Effect{},
	})
	ref, scope, err := c.resolveRefs(ctx, run)
	if err != nil {
		failure := c.fail(ctx, run.RunID, domain.RunStatePreparingContext, err)
		c.observeRunCompleted(ctx, run, started, domain.RunStateFailed, "failure")
		return domain.Run{}, failure
	}
	if err := c.drive(ctx, run.RunID, ref, scope, continuationBrief, priorEvidenceBlock, domain.RunStatePreparingContext, resumePhase, priorInvocations, analysisOnly); err != nil {
		c.observeRunCompleted(ctx, run, started, domain.RunStateFailed, "failure")
		return domain.Run{}, err
	}
	final, err := c.store.Get(context.WithoutCancel(ctx), run.RunID)
	if err != nil {
		c.observeRunCompleted(ctx, run, started, "", "failure")
		return domain.Run{}, fmt.Errorf("load final run: %w", err)
	}
	// Get 聚合不保证带回 series key；用创建结果补齐身份字段。
	final.Run.IncidentID = run.IncidentID
	final.Run.SeriesID = run.SeriesID
	final.Run.LifecycleGeneration = run.LifecycleGeneration
	final.Run.DeployedCommit = run.DeployedCommit
	c.observer.RunCompleted(ctx, RunCompletedObservation{
		Run: runIdentity(final.Run), TerminalState: final.Run.State,
		Duration: time.Since(started), Outcome: terminalOutcome(final.Run.State), Aggregate: final,
	})
	return final.Run, nil
}

func (c *RemediationCoordinator) observeRunCompleted(
	ctx context.Context,
	run domain.Run,
	started time.Time,
	fallback domain.RunState,
	outcome string,
) {
	aggregate, err := c.store.Get(context.WithoutCancel(ctx), run.RunID)
	if err != nil {
		aggregate.Run = run
		aggregate.Run.State = fallback
	}
	// Store 聚合可能省略 series key；observer 始终沿用创建结果的完整关联身份。
	aggregate.Run.RunID = run.RunID
	aggregate.Run.SeriesID = run.SeriesID
	aggregate.Run.IncidentID = run.IncidentID
	aggregate.Run.LifecycleGeneration = run.LifecycleGeneration
	c.observer.RunCompleted(ctx, RunCompletedObservation{
		Run: runIdentity(aggregate.Run), TerminalState: aggregate.Run.State,
		Duration: time.Since(started), Outcome: outcome, Aggregate: aggregate,
	})
}

func terminalOutcome(state domain.RunState) string {
	switch state {
	case domain.RunStateFailed:
		return "failure"
	case domain.RunStateBudgetExhausted, domain.RunStateBlockedManualReview:
		return "stopped"
	default:
		return "success"
	}
}

// claimQueued 只负责 queued→preparing_context 的单次 claim。若其他进程或
// goroutine 先完成 claim，失败方返回当前 durable state，不重复驱动同一 run。
func (c *RemediationCoordinator) claimQueued(ctx context.Context, run domain.Run) (domain.Run, bool, error) {
	if err := c.store.Transition(ctx, run.RunID, domain.RunStateQueued, domain.RunStatePreparingContext, domain.Effect{}); err == nil {
		return run, true, nil
	} else {
		current, getErr := c.store.Get(context.WithoutCancel(ctx), run.RunID)
		if getErr == nil && current.Run.RunID == run.RunID && current.Run.State != domain.RunStateQueued {
			// Store implementations may omit series identity on a compact read;
			// preserve the identity already returned by the create operation.
			current.Run.SeriesID = run.SeriesID
			current.Run.IncidentID = run.IncidentID
			current.Run.LifecycleGeneration = run.LifecycleGeneration
			current.Run.DeployedCommit = run.DeployedCommit
			return current.Run, false, nil
		}
		return domain.Run{}, false, fmt.Errorf("claim queued remediation run: %w", err)
	}
}

// Remediate 满足冻结 port：只接收事故编号和 generation。
// 生产路径必须通过 IncidentLookup 解析内部 UUID，不得把编号格式化成 IncidentID。
func (c *RemediationCoordinator) Remediate(
	ctx context.Context,
	req port.CoordinatorRequest,
) (port.CoordinatorResponse, error) {
	in, err := c.newRunFromRequest(ctx, req)
	if err != nil {
		return port.CoordinatorResponse{}, err
	}
	run, err := c.Start(ctx, in)
	if err != nil {
		return port.CoordinatorResponse{}, err
	}
	return port.CoordinatorResponse{
		Success: true,
		Message: fmt.Sprintf("run %s ended in state %s", run.RunID, run.State),
	}, nil
}

func (c *RemediationCoordinator) newRunFromRequest(ctx context.Context, req port.CoordinatorRequest) (domain.NewRun, error) {
	if c.lookup == nil {
		return domain.NewRun{}, fmt.Errorf("incident lookup is required")
	}
	identity, err := c.lookup.GetByNumber(ctx, req.IncidentNumber)
	if err != nil {
		return domain.NewRun{}, fmt.Errorf("lookup incident: %w", err)
	}
	return domain.NewRun{
		IncidentID:          identity.ID,
		LifecycleGeneration: identity.LifecycleGeneration,
		DeployedCommit:      identity.DeployedCommit,
		Priority:            identity.Priority,
		TriggerReason:       TriggerReasonManual,
		ContextVersion:      identity.ContextVersion,
	}, nil
}

// resolveRefs 用事故身份和项目配置填充 RepoRef / EvidenceScope。
// RemoteURL 必须来自解析器，不得由 coordinator 拼造；解析失败则终止 run。
func (c *RemediationCoordinator) resolveRefs(ctx context.Context, run domain.Run) (domain.RepoRef, domain.EvidenceScope, error) {
	identity := IncidentIdentity{ID: run.IncidentID}
	if c.lookup != nil {
		loaded, err := c.lookup.GetByID(ctx, run.IncidentID)
		if err != nil {
			return domain.RepoRef{}, domain.EvidenceScope{}, fmt.Errorf("lookup incident identity: %w", err)
		}
		identity = loaded
	}
	remoteURL := ""
	if c.remotes != nil {
		if identity.ProjectID == "" {
			return domain.RepoRef{}, domain.EvidenceScope{}, markConfigurationFailure(fmt.Errorf("resolve repository remote: project id is required"))
		}
		resolved, err := c.remotes.CredentialFreeRemoteURL(ctx, identity.ProjectID)
		if err != nil {
			return domain.RepoRef{}, domain.EvidenceScope{}, markConfigurationFailure(fmt.Errorf("resolve repository remote: %w", err))
		}
		if resolved == "" {
			return domain.RepoRef{}, domain.EvidenceScope{}, markConfigurationFailure(fmt.Errorf("resolve repository remote: remote URL is empty"))
		}
		remoteURL = resolved
	}
	return domain.RepoRef{
			ProjectID: identity.ProjectID,
			RemoteURL: remoteURL,
			Commit:    run.DeployedCommit,
		}, domain.EvidenceScope{
			ProjectID:     identity.ProjectID,
			EnvironmentID: identity.EnvironmentID,
			SourceID:      identity.SourceID,
		}, nil
}

// drive 从已 claim 的 preparing_context 驱动 state machine 到终态。continuation 每次使用新的
// budget 与 bounded predecessor brief；已通过 gate 的 code_fixable diagnosis 从 planning 恢复。
func (c *RemediationCoordinator) drive(
	ctx context.Context,
	runID string,
	ref domain.RepoRef,
	scope domain.EvidenceScope,
	continuationBrief string,
	priorEvidenceBlock string,
	startState domain.RunState,
	resumePhase domain.RunState,
	priorInvocations []domain.ToolInvocation,
	analysisOnly bool,
) error {
	tracker := resilientStateFrom(ctx)
	limits := c.budgetLimits
	if tracker != nil && tracker.alloc != nil {
		// restart 必须沿用 run 创建时写入 checkpoint 的 immutable hard ceiling；
		// 新进程配置只能用于没有 durable budget snapshot 的旧数据。
		limits = tracker.alloc.plan.Ceiling
	}
	budget := newRunBudget(limits)
	if startState != domain.RunStatePreparingContext && tracker != nil {
		budget = resumeRunBudget(limits, restoredRunBudgetCounters(tracker, tracker.run.Budget))
	}

	// runQueued 已经以 optimistic transition claim queued；此处从
	// preparing_context 继续，避免重复推进或并发驱动同一 root。
	bootstrap := domain.BootstrapEvidence{}
	if c.bootstrapLoader != nil {
		loaded, err := c.bootstrapLoader.LoadBootstrapEvidence(ctx, observationRun(ctx).IncidentID)
		if err != nil {
			return c.fail(ctx, runID, domain.RunStatePreparingContext,
				markPersistenceFailure(fmt.Errorf("load triggering evidence: %w", err)))
		}
		bootstrap = prepareBootstrapEvidence(loaded)
		scope.TimeRange = bootstrap.TimeRange
		// resilient_v1：bootstrap 预存证据只记入 evidence authority 集合，
		// 可支持 materiality 说明，但不能冒充任何工具 capability 已执行。
		if tracker := resilientStateFrom(ctx); tracker != nil {
			for _, record := range bootstrap.Records {
				tracker.recordEvidenceRefs([]string{record.EvidenceID})
			}
		}
	}

	source := legacySourceCapability(scope)
	if c.sourceCaps != nil {
		resolved, err := c.sourceCaps.ResolveSourceCapability(ctx, scope.ProjectID, scope.SourceID)
		if err != nil {
			return c.fail(ctx, runID, domain.RunStatePreparingContext,
				markConfigurationFailure(fmt.Errorf("resolve source capability: %w", err)))
		}
		source = resolved
	}
	var catalog *ToolCatalog
	var err error
	switch {
	case analysisOnly:
		if resumePhase != domain.RunStatePlanning {
			resumePhase = domain.RunStateDiagnosing
		}
		catalog = c.toolGateway.BuildAnalysisOnlyCatalog(runID, resumePhase, scope)
	case resumePhase == domain.RunStatePlanning:
		// predecessor 已经以 durable code_fixable decision 通过 evidence gate。
		// planning retry 不重新打开当前重复告警的 Tencent detail/log gate。
		catalog, err = c.toolGateway.BuildCatalog(ctx, runID, domain.RunStatePlanning, scope, source)
	default:
		resumePhase = domain.RunStateDiagnosing
		catalog, err = c.toolGateway.BuildCatalogWithBootstrap(
			ctx, runID, observationRun(ctx).IncidentID, domain.RunStateDiagnosing, scope, source, bootstrap,
		)
		if err == nil {
			catalog.seedPriorTencentDetailFailure(priorInvocations)
		}
	}
	if err != nil {
		return c.fail(ctx, runID, domain.RunStatePreparingContext, markConfigurationFailure(err))
	}
	if tracker != nil {
		tracker.analysisOnly = analysisOnly
	}
	if c.dynamicRuntime != nil && !analysisOnly {
		defer func() { _ = c.dynamicRuntime.CloseRun(context.Background(), runID) }()
	}

	contextSource := source
	if analysisOnly {
		contextSource = domain.SourceCapabilitySnapshot{Kind: "persisted_evidence"}
	}
	initialContext, contextEffect, err := c.contextAssem.AssembleInitialContextWithEvidenceObserved(
		ctx, observationRun(ctx), c.observer, ref, scope, contextSource, bootstrap,
	)
	if err != nil {
		return c.fail(ctx, runID, domain.RunStatePreparingContext, markConfigurationFailure(err))
	}
	initialContext += "\n" + catalog.StatusText()
	if analysisOnly {
		initialContext += "\nManual continuation analysis mode: analyze the persisted operational evidence above without refreshing it. External evidence, source, SSH, Docker, Tencent detail, and dynamic runtime tools are unavailable; repository read-only tools may be used for code analysis."
	}
	if continuationBrief != "" || priorEvidenceBlock != "" || (tracker != nil && tracker.reconstruction != "") {
		// continuation 输入组装：主 brief（predecessor 元数据）、可选
		// runtime-only evidence 块、可选 durable checkpoint 重建块。
		// 重建块存在时取代 runtime-only evidence 块（D2/AC7），但保留主
		// brief；两者都不存在时保持既有拼接顺序不变（legacy 字节一致）。
		parts := make([]string, 0, 3)
		if tracker != nil && tracker.reconstruction != "" {
			parts = append(parts, tracker.reconstruction)
			if continuationBrief != "" {
				parts = append(parts, continuationBrief)
			}
		} else {
			if continuationBrief != "" {
				parts = append(parts, continuationBrief)
			}
			if priorEvidenceBlock != "" {
				parts = append(parts, priorEvidenceBlock)
			}
		}
		initialContext = strings.Join(parts, "\n\n") + "\n\n" + initialContext
	}
	conversation := NewAgentConversation(initialContext)
	if tracker != nil {
		tracker.conversation = conversation
	}
	if startState == domain.RunStatePlanning {
		return c.planFrom(ctx, budget, runID, ref, scope, catalog, domain.RunStatePlanning, domain.Effect{}, conversation)
	}
	if resumePhase == domain.RunStatePlanning {
		return c.planFrom(ctx, budget, runID, ref, scope, catalog, domain.RunStatePreparingContext, contextEffect, conversation)
	}

	var exhausted bool
	if startState != domain.RunStateDiagnosing {
		// preparing_context → diagnosing
		exhausted, err := c.transitionBudgeted(ctx, budget, runID, domain.RunStatePreparingContext, domain.RunStateDiagnosing, contextEffect)
		if err != nil || exhausted {
			return err
		}
		// (a) D2 forced set：preparing_context → diagnosing 边界后的强制 checkpoint。
		if checkpointErr := c.checkpointRun(ctx, tracker, domain.RunStateDiagnosing, domain.CheckpointReasonPhaseBoundary); checkpointErr != nil {
			return c.fail(ctx, runID, domain.RunStateDiagnosing, markPersistenceFailure(checkpointErr))
		}
	} else if tracker != nil && tracker.checkpointNeedsRebuild {
		if checkpointErr := c.checkpointRun(ctx, tracker, domain.RunStateDiagnosing, domain.CheckpointReasonPhaseBoundary); checkpointErr != nil {
			return c.fail(ctx, runID, domain.RunStateDiagnosing, markPersistenceFailure(checkpointErr))
		}
		tracker.checkpointNeedsRebuild = false
	}

	collectLoops := 0
	causalClosureReassessed := false
	challengedDockerRefinementVersion := 0
	protocolFailures := 0
	for {
		if exhausted, err := c.admitOperation(ctx, budget, runID, domain.RunStateDiagnosing); err != nil || exhausted {
			return err
		}
		// D2/R13 自动 byte-threshold hybrid trigger（resilient_v1）：每轮 provider
		// 调用前评估 context/tool-output 字节压力；触发只追加 durable checkpoint，
		// 不改变本轮的预算/协议/终止语义。失败按既有 contract 转为
		// persistence_failure。
		if checkpointErr := c.autoThresholdCheckpoint(ctx, tracker, domain.RunStateDiagnosing, conversation, runID); checkpointErr != nil {
			return checkpointErr
		}
		operationCtx, cancelOperation := budget.operationContext(ctx)
		env, usage, err := c.agentEngine.TurnObservedWithConversationAndTools(
			operationCtx, observationRun(ctx), c.observer, nextObservationSequence(ctx),
			domain.RunStateDiagnosing, ref.ProjectID, conversation.ContextText(),
			c.toolGateway.AdvertisedToolDefinitionsForCatalog(catalog, domain.RunStateDiagnosing), conversation,
		)
		runDeadlineExceeded := operationDeadlineExceeded(operationCtx)
		cancelOperation()
		if runDeadlineExceeded {
			_, transitionErr := c.recordElapsedOperation(ctx, budget, runID, domain.RunStateDiagnosing, modelEffect(usage))
			return transitionErr
		}
		if err != nil {
			exhausted, transitionErr := c.handleTurnError(ctx, budget, runID, domain.RunStateDiagnosing, usage, conversation, &protocolFailures, err)
			if transitionErr != nil || exhausted {
				return transitionErr
			}
			continue
		}

		// R18/D6：resilient_v1 下模型可返回 exhaustion proof envelope；先于
		// 普通 switch 处理，保证 legacy 的 default 分支（unexpected kind）
		// 字节不变。接受→blocked_manual_review；拒绝→recoverable challenge。
		if tracker := resilientStateFrom(ctx); tracker != nil && env.Kind == "exhaustion" {
			// 强制 Tencent detail 证据门保持权威：gate 关闭（尚未尝试 detail）时
			// exhaustion proof 与 diagnosis/stop 一样被 required_direct_evidence
			// 拒绝，模型必须先调用 evidence.tencent_cls_detail（R20 能力目录
			// 覆盖校验的前提是目录已包含该能力路径）。
			if catalog.tencentDetailGateClosed() {
				exhausted, err := c.recordSameStateBudget(ctx, budget, runID, domain.RunStateDiagnosing, modelEffect(usage))
				if err != nil || exhausted {
					return err
				}
				c.appendRequiredDetailCorrection(ctx, conversation)
				continue
			}
			done, err := c.handleExhaustionEnvelope(ctx, budget, runID, usage, env.Exhaustion, catalog, conversation)
			if err != nil {
				return err
			}
			if done {
				return nil
			}
			continue
		}
		switch env.Kind {
		case "requestTool":
			protocolFailures = 0
			// The model asks for a read tool mid-diagnosis. Execute via the
			// gateway (policy enforced there) and record the invocation. A
			// rejection is fed back as agent context, not a run failure; the
			// loop re-prompts the model.
			exhausted, err := c.recordSameStateBudget(ctx, budget, runID, domain.RunStateDiagnosing, modelEffect(usage))
			if err != nil || exhausted {
				return err
			}
			requests := env.RequestedTools()
			for index := range requests {
				exhausted, _, err = c.runTool(ctx, budget, runID, domain.RunStateDiagnosing, ref, scope, catalog, &requests[index], conversation)
				if err != nil || exhausted {
					return err
				}
			}
			continue

		case "diagnosis":
			if catalog.tencentDetailGateClosed() {
				exhausted, err := c.recordSameStateBudget(ctx, budget, runID, domain.RunStateDiagnosing, modelEffect(usage))
				if err != nil || exhausted {
					return err
				}
				c.appendRequiredDetailCorrection(ctx, conversation)
				continue
			}
			challenged, terminal, err := c.challengePendingDockerRefinement(
				ctx, budget, runID, usage, conversation, &challengedDockerRefinementVersion,
			)
			if err != nil || terminal {
				return err
			}
			if challenged {
				continue
			}
			protocolFailures = 0
			diagnosis := env.Diagnosis
			// D4/INC-2270 audit：先捕获模型提交的原始 pre-gate envelope（有界结构化
			// 字段，不含 raw model turn / prompt / conversation）。submitted 行的
			// fixability/confidence 始终是模型原值，即使 gate 硬拒绝或 citation
			// classification 修正也不会被改写；audit persistence 是 hard blocker，
			// 不能在缺少原 submission 的情况下继续 decision/route。
			submitted := submittedDiagnosisFrom(diagnosis)
			if diagnosis.Fixability == domain.FixabilityCodeFixable {
				if c.evidenceGate == nil {
					c.evidenceGate = NewEvidenceGate(nil)
				}
				gated, decision, mismatches, gateErr := c.evidenceGate.Apply(ctx, runID, diagnosis)
				if gateErr != nil {
					return c.fail(ctx, runID, domain.RunStateDiagnosing, markPersistenceFailure(fmt.Errorf("evaluate evidence gate: %w", gateErr)))
				}
				diagnosis = gated
				if len(mismatches) > 0 {
					// R6/R7/AC5：可纠正的 citation classification 元数据差异回喂同一
					// 循环（evidence_correction challenge），模型修正后重新走 gate；
					// 不终态化，也不静默改写结论。
					// 同时把差异写入 submitted 行的 correction metadata（权威 stored
					// classification），gate 判定独立记录在 gate_outcome，供审计证明
					// 纠正发生过；本轮不产生 accepted decision，decision 链接为空。
					submitted.Correction = submittedCorrectionFromMismatches(mismatches)
					submitted.GateOutcome = submittedGateOutcome(decision)
					if err := c.appendSubmittedDiagnosis(ctx, runID, submitted, false); err != nil {
						return c.fail(ctx, runID, domain.RunStateDiagnosing, markPersistenceFailure(err))
					}
					if err := c.challengeEvidenceCorrections(ctx, budget, runID, usage, mismatches, conversation); err != nil {
						return err
					}
					continue
				}
				submitted.GateOutcome = submittedGateOutcome(decision)
				if !decision.PlanningEligible {
					if resilientStateFrom(ctx) != nil {
						// R6/R8：failed fact check 保留 agent 的 fixability/confidence，
						// 持久化 pre-gate submission 后以 structured challenge 回到同一
						// resilient loop；不得创建改写后的 accepted decision。
						if err := c.appendSubmittedDiagnosis(ctx, runID, submitted, false); err != nil {
							return c.fail(ctx, runID, domain.RunStateDiagnosing, markPersistenceFailure(err))
						}
						if err := c.challengeFactCheck(ctx, budget, runID, usage, decision, conversation); err != nil {
							return err
						}
						continue
					}
					// legacy mode 保留原有 rollout 行为；resilient_v1 永不走此改写。
					diagnosis.Fixability = domain.FixabilityInsufficientEvidence
				}
			}
			if err := c.appendDecision(ctx, runID, diagnosis); err != nil {
				return c.fail(ctx, runID, domain.RunStateDiagnosing, markPersistenceFailure(err))
			}
			// D4：把 submitted 行与刚创建的 accepted decision 关联（audit
			// submitted→accepted join）；无对应 decision 时保持 NULL。
			if err := c.appendSubmittedDiagnosis(ctx, runID, submitted, true); err != nil {
				return c.fail(ctx, runID, domain.RunStateDiagnosing, markPersistenceFailure(err))
			}
			done, err := c.routeDiagnosis(
				ctx, budget, runID, ref, scope, catalog, diagnosis, usage,
				&collectLoops, &causalClosureReassessed, conversation,
			)
			if err != nil {
				return err
			}
			if done {
				return nil
			}
			// Not done → collected more context; re-diagnose.
			continue

		case "stop":
			if catalog.tencentDetailGateClosed() {
				exhausted, err := c.recordSameStateBudget(ctx, budget, runID, domain.RunStateDiagnosing, modelEffect(usage))
				if err != nil || exhausted {
					return err
				}
				c.appendRequiredDetailCorrection(ctx, conversation)
				continue
			}
			challenged, terminal, err := c.challengePendingDockerRefinement(
				ctx, budget, runID, usage, conversation, &challengedDockerRefinementVersion,
			)
			if err != nil || terminal {
				return err
			}
			if challenged {
				continue
			}
			if env.Stop == nil || strings.TrimSpace(env.Stop.RecommendedNextAction) == "" {
				// stop 也必须留下可执行的人工交接建议；缺失时沿用协议错误的
				// 预算与有界重试路径，不能把不完整的 stop 直接终态化。
				validationErr := wrapEnvelopeError("validate stop", errStopRecommendationRequired)
				exhausted, err := c.handleTurnError(
					ctx, budget, runID, domain.RunStateDiagnosing, usage, conversation, &protocolFailures,
					validationErr,
				)
				if err != nil || exhausted {
					return err
				}
				continue
			}
			protocolFailures = 0
			if tracker := resilientStateFrom(ctx); tracker != nil {
				// R18/D6：resilient_v1 下 stop 不直接终态化；先要求并校验
				// exhaustion proposal（模型下一轮返回 exhaustion envelope）。
				if err := c.requestExhaustionProposal(ctx, budget, runID, usage, conversation, "stop"); err != nil {
					return err
				}
				continue
			}
			// 将模型的 stop 转成结构化 diagnosis，保证人工建议进入 review chain，
			// 同时保留 blocked_manual_review 的原有终态和安全 terminal reason。
			stopDiagnosis := &DiagnosisOutput{
				Fixability:            domain.FixabilityUnsafeToAutomate,
				Confidence:            0,
				CausalReasoning:       env.Stop.Reason,
				RecommendedNextAction: env.Stop.RecommendedNextAction,
			}
			if err := c.appendDecision(ctx, runID, stopDiagnosis); err != nil {
				return c.fail(ctx, runID, domain.RunStateDiagnosing, markPersistenceFailure(err))
			}
			// The model deliberately gives up; a human must take over.
			effect := modelEffect(usage)
			effect.TerminalReason = "blocked_manual_review"
			exhausted, err = c.transitionBudgeted(ctx, budget, runID, domain.RunStateDiagnosing, domain.RunStateBlockedManualReview, effect)
			if err != nil || exhausted {
				return err
			}
			return c.notifyTerminal(ctx, runID, domain.RunStateBlockedManualReview, stopDiagnosis.Fixability)
		default:
			exhausted, err := c.handleTurnError(
				ctx, budget, runID, domain.RunStateDiagnosing, usage, conversation, &protocolFailures,
				wrapEnvelopeError("unexpected envelope kind in diagnosing", fmt.Errorf("kind %q", env.Kind)),
			)
			if err != nil || exhausted {
				return err
			}
			continue
		}
	}
}

func (c *RemediationCoordinator) challengePendingDockerRefinement(
	ctx context.Context,
	budget *runBudget,
	runID string,
	usage domain.ModelResult,
	conversation *AgentConversation,
	challengedVersion *int,
) (bool, bool, error) {
	version, reason, pending := conversation.PendingDockerLogRefinement()
	if !pending {
		return false, false, nil
	}
	if version <= *challengedVersion {
		if tracker := resilientStateFrom(ctx); tracker != nil {
			// R18/D6：resilient_v1 下 Docker refinement 耗尽不直接终态化；
			// 先要求 exhaustion proposal（challenged=true 让主循环继续）。
			if err := c.requestExhaustionProposal(ctx, budget, runID, usage, conversation, "docker_refinement_exhausted"); err != nil {
				return false, true, err
			}
			return true, false, nil
		}
		effect := modelEffect(usage)
		effect.TerminalReason = "insufficient_evidence"
		exhausted, err := c.transitionBudgeted(
			ctx, budget, runID, domain.RunStateDiagnosing, domain.RunStateBlockedManualReview, effect,
		)
		if err != nil || exhausted {
			return false, true, err
		}
		return false, true, c.notifyTerminal(
			ctx, runID, domain.RunStateBlockedManualReview, domain.FixabilityInsufficientEvidence,
		)
	}
	*challengedVersion = version
	exhausted, err := c.recordSameStateBudget(ctx, budget, runID, domain.RunStateDiagnosing, modelEffect(usage))
	if err != nil || exhausted {
		return true, exhausted, err
	}
	conversation.AppendDockerLogRefinement(reason)
	return true, false, nil
}

// challengeEvidenceCorrections 把 gate 发现的可纠正 citation classification
// 差异（R7）作为 recoverable evidence_correction challenge 回喂同一循环，并
// 在 resilient_v1 下记录 recovery checkpoint。该模型轮次仍按正常 diagnosis
// 计入预算；消息只含 evidence ID 与持久化权威分类，不携带模型输出或凭据。
// legacy run（无 resilient state）同样收到 challenge：R7 不区分模式，且
// domain gate 的 mismatch 解耦已经改变了 legacy 的结果路径。
func (c *RemediationCoordinator) challengeEvidenceCorrections(
	ctx context.Context,
	budget *runBudget,
	runID string,
	usage domain.ModelResult,
	mismatches []CitationClassificationMismatch,
	conversation *AgentConversation,
) error {
	exhausted, err := c.recordSameStateBudget(ctx, budget, runID, domain.RunStateDiagnosing, modelEffect(usage))
	if err != nil || exhausted {
		return err
	}
	attempt := 1
	available := []string{"repository", "provider_evidence", "runtime_logs", "ssh_inspect"}
	if tracker := resilientStateFrom(ctx); tracker != nil {
		tracker.evidenceCorrectionAttempts++
		attempt = tracker.evidenceCorrectionAttempts
		available = tracker.recoveryCapabilities()
	}
	challenge, err := NewRecoveryChallenge(
		domain.RecoveryChallengeKindEvidenceCorrection,
		domain.RecoverySeverityRecoverable,
		"citation_classification_mismatch",
		"evidenceCitations",
		available,
		[]string{"correct_citation"},
		attempt,
		budget.remaining(),
		evidenceCorrectionMessage(mismatches),
	)
	if err != nil {
		return c.fail(ctx, runID, domain.RunStateDiagnosing, markPersistenceFailure(fmt.Errorf("build evidence correction challenge: %w", err)))
	}
	if tracker := resilientStateFrom(ctx); tracker != nil {
		// D2 recovery-triggered checkpoint：策略变化（evidence metadata 修正）
		// 后强制持久化 working memory，供重启/续跑重建。
		tracker.appendRecovery(domain.CheckpointRecovery{
			Kind: string(challenge.Kind), Action: "correct_citation", OutcomeRef: "challenge:" + challenge.ReasonCode,
		})
		if err := c.checkpointRun(ctx, tracker, domain.RunStateDiagnosing, domain.CheckpointReasonRecovery); err != nil {
			return c.fail(ctx, runID, domain.RunStateDiagnosing, markPersistenceFailure(err))
		}
	}
	// D5 challenge 追加与 per-kind 指标在同一共享出口完成（见
	// appendRecoveryChallenge），legacy run 只追加不计数。
	c.appendRecoveryChallenge(ctx, domain.RunStateDiagnosing, conversation, challenge)
	return nil
}

// challengeFactCheck 把 non-metadata fact-gate rejection 作为统一的
// evidence_correction challenge 回到 resilient loop。相同服务端 fingerprint
// 连续三次无进展才请求 exhaustion proof；不同缺口/证据集合会重置计数。
func (c *RemediationCoordinator) challengeFactCheck(
	ctx context.Context,
	budget *runBudget,
	runID string,
	usage domain.ModelResult,
	decision domain.EvidenceGateDecision,
	conversation *AgentConversation,
) error {
	tracker := resilientStateFrom(ctx)
	if tracker == nil {
		return fmt.Errorf("fact-check challenge requires resilient run state")
	}
	exhausted, err := c.recordSameStateBudget(ctx, budget, runID, domain.RunStateDiagnosing, modelEffect(usage))
	if err != nil || exhausted {
		return err
	}
	tracker.factCheckAttempts++
	fingerprint := domain.FailureFingerprint{
		FailedActionRef: strings.Join(append(append([]string(nil), decision.Reasons...), decision.MissingEvidence...), "\x00"),
		Capability:      "provider_evidence",
		ErrorCode:       "fact_check_rejected",
	}.Key()
	if fingerprint == tracker.lastFactCheckFingerprint {
		tracker.factCheckNoProgress++
	} else {
		tracker.lastFactCheckFingerprint = fingerprint
		tracker.factCheckNoProgress = 1
	}
	if tracker.factCheckNoProgress >= maxConsecutiveProtocolFailures {
		return c.requestExhaustionProposalAfterRecorded(ctx, budget, runID, conversation, "fact_check_no_progress")
	}
	challenge, err := NewRecoveryChallenge(
		domain.RecoveryChallengeKindEvidenceCorrection,
		domain.RecoverySeverityRecoverable,
		"fact_check_rejected",
		"evidenceAssessment",
		tracker.recoveryCapabilities(),
		[]string{"rehydrate_evidence", "use_alternative", "revise_materiality"},
		tracker.factCheckAttempts,
		budget.remaining(),
		factCheckChallengeMessage(decision),
	)
	if err != nil {
		return c.fail(ctx, runID, domain.RunStateDiagnosing, markPersistenceFailure(fmt.Errorf("build fact-check challenge: %w", err)))
	}
	tracker.appendRecovery(domain.CheckpointRecovery{
		Kind: string(challenge.Kind), Action: "correct_fact_check", OutcomeRef: "challenge:fact_check_rejected",
	})
	if err := c.checkpointRun(ctx, tracker, domain.RunStateDiagnosing, domain.CheckpointReasonRecovery); err != nil {
		return c.fail(ctx, runID, domain.RunStateDiagnosing, markPersistenceFailure(err))
	}
	c.appendRecoveryChallenge(ctx, domain.RunStateDiagnosing, conversation, challenge)
	return nil
}

func factCheckChallengeMessage(decision domain.EvidenceGateDecision) string {
	parts := append([]string(nil), decision.Reasons...)
	if len(decision.MissingEvidence) > 0 {
		parts = append(parts, "missing: "+strings.Join(decision.MissingEvidence, ", "))
	}
	if len(decision.Contradictions) > 0 {
		parts = append(parts, "contradictions: "+strings.Join(decision.Contradictions, ", "))
	}
	return "The submitted code_fixable diagnosis failed the service fact check: " + strings.Join(parts, "; ") +
		". The diagnosis fixability and confidence were not rewritten. Re-read owned evidence, correct factual citations, " +
		"or revise which unresolved facts are material to the causal chain, then submit a new diagnosis."
}

// evidenceCorrectionMessage 生成 evidence_correction challenge 的服务端固定
// 文案：逐条列出 evidence ID 与持久化权威分类（均非凭据），并说明该差异是
// 可纠正元数据，不改变 fixability/confidence（R7）。
func evidenceCorrectionMessage(mismatches []CitationClassificationMismatch) string {
	parts := make([]string, 0, len(mismatches))
	for _, mismatch := range mismatches {
		parts = append(parts, mismatch.EvidenceID+" is stored as "+string(mismatch.StoredClassification))
	}
	return "Citation classification metadata does not match the persisted authoritative classification: " +
		strings.Join(parts, "; ") +
		". Re-read the evidence by evidenceId and return the stored classification in evidenceCitations. " +
		"A classification mismatch is correctable metadata and does not by itself change fixability or confidence."
}

// routeDiagnosis applies the fixability routing rules. It returns done=true when
// the run reached a terminal state, or done=false when it moved through a
// bounded collect-more-context loop and diagnosis should be re-run.
func (c *RemediationCoordinator) routeDiagnosis(
	ctx context.Context,
	budget *runBudget,
	runID string,
	ref domain.RepoRef,
	scope domain.EvidenceScope,
	catalog *ToolCatalog,
	diag *DiagnosisOutput,
	usage domain.ModelResult,
	collectLoops *int,
	causalClosureReassessed *bool,
	conversation *AgentConversation,
) (bool, error) {
	switch diag.Fixability {
	case domain.FixabilityExternalDependency, domain.FixabilityConfiguration,
		domain.FixabilityData, domain.FixabilityInfrastructure:
		effect := modelEffect(usage)
		effect.TerminalReason = "completed_non_code"
		exhausted, err := c.transitionBudgeted(ctx, budget, runID, domain.RunStateDiagnosing, domain.RunStateCompletedNonCode, effect)
		if err != nil || exhausted {
			return true, err
		}
		return true, c.notifyTerminal(ctx, runID, domain.RunStateCompletedNonCode, diag.Fixability)

	case domain.FixabilityUnsafeToAutomate:
		effect := modelEffect(usage)
		effect.TerminalReason = "blocked_manual_review"
		exhausted, err := c.transitionBudgeted(ctx, budget, runID, domain.RunStateDiagnosing, domain.RunStateBlockedManualReview, effect)
		if err != nil || exhausted {
			return true, err
		}
		return true, c.notifyTerminal(ctx, runID, domain.RunStateBlockedManualReview, diag.Fixability)

	case domain.FixabilityInsufficientEvidence:
		if diag.CollectMoreContext == nil || len(diag.CollectMoreContext.ToolCalls) == 0 {
			if diagnosisNeedsCausalClosureReassessment(diag) && !*causalClosureReassessed {
				// 因果闭环与证据不足不能同时作为最终结论。先消耗一次正常模型预算
				// 强制复核缺失项的 materiality；复核仍失败时才进入人工交接。
				*causalClosureReassessed = true
				exhausted, err := c.recordSameStateBudget(ctx, budget, runID, domain.RunStateDiagnosing, modelEffect(usage))
				if err != nil || exhausted {
					return true, err
				}
				conversation.AppendCausalClosureReassessment()
				return false, nil
			}
			// 没有因果闭环，也没有明确 tool request 时，重放同一 prompt 不会
			// 增加 evidence；此时保留完整终态 diagnosis 并交给人工复核。
			if tracker := resilientStateFrom(ctx); tracker != nil {
				// R18/D6：resilient_v1 下空 insufficient_evidence 不直接终态化；
				// 先要求 exhaustion proposal。
				return false, c.requestExhaustionProposal(ctx, budget, runID, usage, conversation, "insufficient_evidence")
			}
			return true, c.blockForInsufficientEvidence(ctx, budget, runID, usage, diag.Fixability)
		}
		if *collectLoops >= c.maxCollectLoops {
			if tracker := resilientStateFrom(ctx); tracker != nil {
				// R18/D6：resilient_v1 下 collect-loop 耗尽不直接终态化；
				// 先要求 exhaustion proposal。
				return false, c.requestExhaustionProposal(ctx, budget, runID, usage, conversation, "collect_loop_exhausted")
			}
			return true, c.blockForInsufficientEvidence(ctx, budget, runID, usage, diag.Fixability)
		}
		*collectLoops++
		done, err := c.collectMoreContext(ctx, budget, runID, ref, scope, catalog, diag, usage, conversation)
		return done, err

	case domain.FixabilityCodeFixable:
		// 收敛复位（F1）：gate 准入的 code_fixable 诊断是 durable 前进。清空
		// diagnosing 的 fact-check/exhaustion 连续 no-progress 计数，避免已结算
		// 的 loop 把进入 planning 后的 checkpoint（phase boundary / 后续
		// lifecycle recovery）长期误标为 no-progress。
		if tracker := resilientStateFrom(ctx); tracker != nil {
			tracker.resetDiagnosisNoProgress()
		}
		// (c) D2 forced set：进入 planning 边界前的强制 checkpoint。
		if checkpointErr := c.checkpointRun(ctx, resilientStateFrom(ctx), domain.RunStateDiagnosing, domain.CheckpointReasonPhaseBoundary); checkpointErr != nil {
			return true, c.fail(ctx, runID, domain.RunStateDiagnosing, markPersistenceFailure(checkpointErr))
		}
		return true, c.plan(ctx, budget, runID, ref, scope, catalog, usage, conversation)

	default:
		return true, c.fail(ctx, runID, domain.RunStateDiagnosing,
			fmt.Errorf("unknown fixability %q", diag.Fixability))
	}
}

func diagnosisNeedsCausalClosureReassessment(diag *DiagnosisOutput) bool {
	return diag != nil && diag.CausalClosure != nil && diag.CausalClosure.ExplainsOriginalSymptom
}

func (c *RemediationCoordinator) blockForInsufficientEvidence(
	ctx context.Context,
	budget *runBudget,
	runID string,
	usage domain.ModelResult,
	fixability domain.FixabilityClass,
) error {
	effect := modelEffect(usage)
	effect.TerminalReason = "insufficient_evidence"
	exhausted, err := c.transitionBudgeted(ctx, budget, runID, domain.RunStateDiagnosing, domain.RunStateBlockedManualReview, effect)
	if err != nil || exhausted {
		return err
	}
	return c.notifyTerminal(ctx, runID, domain.RunStateBlockedManualReview, fixability)
}

// collectMoreContext performs one bounded collect-more-context iteration:
// diagnosing → collecting_more_context, run the model's requested read tools,
// then collecting_more_context → diagnosing.
func (c *RemediationCoordinator) collectMoreContext(
	ctx context.Context,
	budget *runBudget,
	runID string,
	ref domain.RepoRef,
	scope domain.EvidenceScope,
	catalog *ToolCatalog,
	diag *DiagnosisOutput,
	usage domain.ModelResult,
	conversation *AgentConversation,
) (bool, error) {
	// durable transition 前保存 provider-neutral diagnosis memory；usage 由后续
	// transition 原子计入 run counters，after-window restore 会补 allocator delta。
	if checkpointErr := c.checkpointRun(ctx, resilientStateFrom(ctx), domain.RunStateDiagnosing, domain.CheckpointReasonPhaseBoundary); checkpointErr != nil {
		return true, c.fail(ctx, runID, domain.RunStateDiagnosing, markPersistenceFailure(checkpointErr))
	}
	exhausted, err := c.transitionBudgeted(ctx, budget, runID, domain.RunStateDiagnosing, domain.RunStateCollectingMoreContext, modelEffect(usage))
	if err != nil || exhausted {
		return exhausted, err
	}

	if diag.CollectMoreContext != nil {
		for i := range diag.CollectMoreContext.ToolCalls {
			exhausted, _, err := c.runTool(ctx, budget, runID, domain.RunStateCollectingMoreContext, ref, scope, catalog, &diag.CollectMoreContext.ToolCalls[i], conversation)
			if err != nil || exhausted {
				return exhausted, err
			}
		}
	}

	// 工具结果已通过 invocation/evidence store 持久化；退出 collecting 前的
	// checkpoint 只保存有界 evidence index 与 allocator，不保存 raw observation。
	if checkpointErr := c.checkpointRun(ctx, resilientStateFrom(ctx), domain.RunStateCollectingMoreContext, domain.CheckpointReasonPhaseBoundary); checkpointErr != nil {
		return true, c.fail(ctx, runID, domain.RunStateCollectingMoreContext, markPersistenceFailure(checkpointErr))
	}
	return false, c.transition(ctx, runID, domain.RunStateCollectingMoreContext, domain.RunStateDiagnosing, domain.Effect{})
}

// plan drives diagnosing → planning → diagnosis_ready_for_review. The suggested
// diff is advisory only; nothing is written or published.
func (c *RemediationCoordinator) plan(ctx context.Context, budget *runBudget, runID string, ref domain.RepoRef, scope domain.EvidenceScope, catalog *ToolCatalog, diagUsage domain.ModelResult, conversation *AgentConversation) error {
	return c.planFrom(ctx, budget, runID, ref, scope, catalog, domain.RunStateDiagnosing, modelEffect(diagUsage), conversation)
}

func (c *RemediationCoordinator) planFrom(
	ctx context.Context,
	budget *runBudget,
	runID string,
	ref domain.RepoRef,
	scope domain.EvidenceScope,
	catalog *ToolCatalog,
	from domain.RunState,
	initialEffect domain.Effect,
	conversation *AgentConversation,
) error {
	var exhausted bool
	if from != domain.RunStatePlanning {
		exhausted, err := c.transitionBudgeted(ctx, budget, runID, from, domain.RunStatePlanning, initialEffect)
		if err != nil || exhausted {
			return err
		}
		if checkpointErr := c.checkpointRun(ctx, resilientStateFrom(ctx), domain.RunStatePlanning, domain.CheckpointReasonPhaseBoundary); checkpointErr != nil {
			return c.fail(ctx, runID, domain.RunStatePlanning, markPersistenceFailure(checkpointErr))
		}
	} else if tracker := resilientStateFrom(ctx); tracker != nil && tracker.checkpointNeedsRebuild {
		if checkpointErr := c.checkpointRun(ctx, tracker, domain.RunStatePlanning, domain.CheckpointReasonPhaseBoundary); checkpointErr != nil {
			return c.fail(ctx, runID, domain.RunStatePlanning, markPersistenceFailure(checkpointErr))
		}
		tracker.checkpointNeedsRebuild = false
	}

	protocolFailures := 0
	for {
		if exhausted, err := c.admitOperation(ctx, budget, runID, domain.RunStatePlanning); err != nil || exhausted {
			return err
		}
		// D2/R13 自动 byte-threshold hybrid trigger（resilient_v1），见 diagnosing
		// 循环同位置；planning 工具读取同样受 conversation 字节压力保护。
		if checkpointErr := c.autoThresholdCheckpoint(ctx, resilientStateFrom(ctx), domain.RunStatePlanning, conversation, runID); checkpointErr != nil {
			return checkpointErr
		}
		contextText := ""
		if conversation != nil {
			contextText = conversation.ContextText()
		}
		operationCtx, cancelOperation := budget.operationContext(ctx)
		env, usage, err := c.agentEngine.TurnObservedWithConversationAndTools(
			operationCtx, observationRun(ctx), c.observer, nextObservationSequence(ctx),
			domain.RunStatePlanning, ref.ProjectID, contextText,
			c.toolGateway.AdvertisedToolDefinitionsForCatalog(catalog, domain.RunStatePlanning), conversation,
		)
		runDeadlineExceeded := operationDeadlineExceeded(operationCtx)
		cancelOperation()
		if runDeadlineExceeded {
			_, transitionErr := c.recordElapsedOperation(ctx, budget, runID, domain.RunStatePlanning, modelEffect(usage))
			return transitionErr
		}
		if err != nil {
			exhausted, transitionErr := c.handleTurnError(ctx, budget, runID, domain.RunStatePlanning, usage, conversation, &protocolFailures, err)
			if transitionErr != nil || exhausted {
				return transitionErr
			}
			continue
		}
		switch env.Kind {
		case "requestTool":
			protocolFailures = 0
			// planning 允许继续读取 catalog 授权的仓库上下文；所有调用仍经过
			// gateway、预算和 invocation 审计，结果写回同一 conversation 后再规划。
			exhausted, err = c.recordSameStateBudget(ctx, budget, runID, domain.RunStatePlanning, modelEffect(usage))
			if err != nil || exhausted {
				return err
			}
			requests := env.RequestedTools()
			for index := range requests {
				exhausted, _, err = c.runTool(ctx, budget, runID, domain.RunStatePlanning, ref, scope, catalog, &requests[index], conversation)
				if err != nil || exhausted {
					return err
				}
			}
			continue

		case "planCandidates":
			protocolFailures = 0
			if tracker := resilientStateFrom(ctx); tracker != nil && c.planPolicy != nil {
				policyDecision, policyErr := c.planPolicy.EvaluatePlan(ctx, domain.PlanPolicyInput{
					RunID: runID, BaselineCommit: ref.Commit,
					Candidates: repairPlanCandidates(env.PlanCandidates), RecommendedID: env.PlanCandidates.RecommendedID,
				})
				if policyErr != nil {
					return c.fail(ctx, runID, domain.RunStatePlanning, markConfigurationFailure(fmt.Errorf("evaluate plan policy: %w", policyErr)))
				}
				done, policyErr := c.handlePlanPolicyDecision(ctx, budget, runID, usage, policyDecision, conversation)
				if policyErr != nil {
					return policyErr
				}
				if done {
					return nil
				}
				if !policyDecision.Accepted {
					continue
				}
			}
		default:
			exhausted, transitionErr := c.handleTurnError(
				ctx, budget, runID, domain.RunStatePlanning, usage, conversation, &protocolFailures,
				wrapEnvelopeError("unexpected envelope kind in planning", fmt.Errorf("kind %q", env.Kind)),
			)
			if transitionErr != nil || exhausted {
				return transitionErr
			}
			continue
		}

		// 先写入计划和建议 diff，再进入 diagnosis_ready_for_review，保证 GET 能读到完整 review chain。
		if err := c.recordPlans(ctx, runID, env.PlanCandidates); err != nil {
			return c.fail(ctx, runID, domain.RunStatePlanning, markPersistenceFailure(err))
		}

		effect := modelEffect(usage)
		effect.TerminalReason = "diagnosis_ready_for_review"
		exhausted, err = c.transitionBudgeted(ctx, budget, runID, domain.RunStatePlanning, domain.RunStateDiagnosisReadyForReview, effect)
		if err != nil || exhausted {
			return err
		}
		return c.notifyTerminal(ctx, runID, domain.RunStateDiagnosisReadyForReview, domain.FixabilityCodeFixable)
	}
}

func (c *RemediationCoordinator) recordPlans(ctx context.Context, runID string, output *PlanCandidatesOutput) error {
	if c.reviews == nil {
		return nil
	}
	plans := make([]domain.RepairPlanCandidate, 0, len(output.Candidates))
	for _, candidate := range output.Candidates {
		plans = append(plans, domain.RepairPlanCandidate{
			PlanID:           candidate.PlanID,
			EvidenceRefs:     candidate.EvidenceRefs,
			AffectedFiles:    candidate.AffectedFiles,
			IntendedBehavior: candidate.IntendedBehavior,
			Risk:             domain.RiskClassification(candidate.Risk),
			RollbackStrategy: candidate.RollbackStrategy,
			Rationale:        output.Rationale,
			Recommended:      candidate.PlanID == output.RecommendedID,
		})
	}
	if err := c.reviews.AppendPlans(ctx, runID, plans, output.RecommendedID); err != nil {
		return fmt.Errorf("append plans: %w", err)
	}
	if err := c.reviews.RecordSuggestedDiff(ctx, runID, output.SuggestedDiff); err != nil {
		return fmt.Errorf("record suggested diff: %w", err)
	}
	return nil
}

// notifyTerminal 在到达 PRD 列出的四个终态后发送终态通知。
// 通知失败会使本次 run 失败：控制台必须能看到 durable 结果，测试也保持确定性。
func (c *RemediationCoordinator) notifyTerminal(ctx context.Context, runID string, state domain.RunState, fixability domain.FixabilityClass) error {
	kind := NotificationKindForTerminal(state, fixability)
	if kind == "" || c.notifications == nil {
		return nil
	}
	// 审计元数据记录 run 创建时快照的 D9 政策模式（agentLoopMode），
	// 读取失败保持 legacy 缺省，通知本身不受影响。
	mode := domain.AgentLoopModeLegacy
	if current, err := c.store.Get(context.WithoutCancel(ctx), runID); err == nil &&
		domain.ParseAgentLoopMode(string(current.Run.AgentLoopMode)).IsKnown() {
		mode = domain.ParseAgentLoopMode(string(current.Run.AgentLoopMode))
	}
	if err := c.notifications.Notify(ctx, TerminalNotification{
		RunID:      runID,
		Kind:       kind,
		Summary:    notificationSummary(kind),
		Fixability: fixability,
		State:      state,
		// AgentLoopMode 记录到通知 metadata 白名单，不进 summary。
		AgentLoopMode: mode,
	}); err != nil {
		return fmt.Errorf("notify terminal %s: %w", kind, err)
	}
	return nil
}

// runTool executes a single tool request through the gateway and records the
// invocation (including gateway rejections, which are recorded but non-fatal).
func (c *RemediationCoordinator) runTool(
	ctx context.Context,
	budget *runBudget,
	runID string,
	phase domain.RunState,
	ref domain.RepoRef,
	scope domain.EvidenceScope,
	catalog *ToolCatalog,
	req *RequestTool,
	conversation *AgentConversation,
) (bool, ToolResult, error) {
	if exhausted, err := c.admitOperation(ctx, budget, runID, phase); err != nil || exhausted {
		return exhausted, ToolResult{}, err
	}
	inv := domain.ToolInvocation{
		ToolName: req.ToolName,
		Phase:    phase,
	}
	operationCtx, cancelOperation := budget.operationContext(ctx)
	res, err := c.toolGateway.ExecuteToolObservedWithCatalog(
		operationCtx, observationRun(ctx), c.observer, nextObservationSequence(ctx),
		phase, ref, scope, catalog, req.ToolName, req.Parameters,
	)
	runDeadlineExceeded := runWorkDeadlineExceeded(operationCtx)
	cancelOperation()
	effect := domain.Effect{ToolCalls: 1}
	if err != nil {
		safe := classifyToolError(err)
		inv.Error = safe.Code
	} else {
		inv.ResultSummary = res.Summary
		inv.BytesRetrieved = res.BytesRetrieved
		inv.EvidenceIDs = res.EvidenceIDs
		effect = toolResultEffect(res)
	}
	// resilient_v1 为每次真实工具执行生成 capability-bound action ref。先将
	// invocation audit 持久化成功，再把 observation/action ref 暴露给模型；
	// 否则 action 不具备可用于 exhaustion proof 的 durable recovery authority。
	tracker := resilientStateFrom(ctx)
	if tracker != nil {
		res.ActionRef = tracker.recordToolAction(req.ToolName, res.EvidenceIDs)
		inv.InvocationID = res.ActionRef
	}
	if recordErr := c.store.RecordToolInvocation(ctx, runID, inv); recordErr != nil && tracker != nil {
		return false, res, c.fail(ctx, runID, phase, markPersistenceFailure(fmt.Errorf("record tool invocation: %w", recordErr)))
	}
	if conversation != nil {
		conversation.AppendToolResult(*req, res, err)
	}
	// (5) resilient_v1：成功的 evidence.read 把证据 ID 记入进程内 checkpoint
	// evidence index，随下一次强制 AppendCheckpoint 持久化（D3/R15）；不解码
	// payload 或凭据。legacy 下 tracker 为 nil，本调用是 no-op。
	if tracker != nil && err == nil && req.ToolName == ToolEvidenceRead {
		if page, ok := res.Payload.(domain.EvidenceReadPage); ok {
			tracker.recordEvidenceRead(page)
		}
	}
	if runDeadlineExceeded {
		exhausted, budgetErr := c.recordElapsedOperation(ctx, budget, runID, phase, effect)
		return exhausted, res, budgetErr
	}
	exhausted, budgetErr := c.recordSameStateBudget(ctx, budget, runID, phase, effect)
	if budgetErr != nil || exhausted {
		return exhausted, res, budgetErr
	}
	if tracker != nil && err != nil {
		if recoveryErr := c.appendToolRecoveryChallenge(ctx, budget, runID, phase, req, err, conversation); recoveryErr != nil {
			return false, res, recoveryErr
		}
	}
	return false, res, nil
}

// appendDecision maps and persists the model's diagnosis.
func (c *RemediationCoordinator) appendDecision(ctx context.Context, runID string, diag *DiagnosisOutput) error {
	d := domain.Decision{
		Fixability:            diag.Fixability,
		Confidence:            diag.Confidence,
		CausalReasoning:       diag.CausalReasoning,
		Contradictions:        diag.Contradictions,
		MissingEvidence:       diag.MissingEvidence,
		EvidenceCitations:     evidenceCitationIDs(diag.EvidenceCitations),
		RecommendedNextAction: diag.RecommendedNextAction,
		EvidenceAssessment:    diag.EvidenceAssessment,
	}
	if err := c.store.AppendDecision(ctx, runID, d); err != nil {
		return fmt.Errorf("append decision: %w", err)
	}
	return nil
}

func evidenceCitationIDs(values []domain.EvidenceCitation) []string {
	ids := make([]string, 0, len(values))
	for _, value := range values {
		if value.EvidenceID != "" {
			ids = append(ids, value.EvidenceID)
		}
	}
	return ids
}

// handleTurnError 区分可纠正的信封错误和不可恢复的提供商/基础设施错误。
// 解码失败会计入模型预算并回喂下一轮；只有越过硬预算或真正的提供商失败才终止。
func (c *RemediationCoordinator) handleTurnError(
	ctx context.Context,
	budget *runBudget,
	runID string,
	phase domain.RunState,
	usage domain.ModelResult,
	conversation *AgentConversation,
	protocolFailures *int,
	cause error,
) (bool, error) {
	exhausted, err := c.recordSameStateBudget(ctx, budget, runID, phase, modelEffect(usage))
	if err != nil || exhausted {
		return exhausted, err
	}
	if errors.Is(cause, ErrInvalidEnvelope) {
		if protocolFailures != nil {
			*protocolFailures = *protocolFailures + 1
		}
		if tracker := resilientStateFrom(ctx); tracker != nil {
			attempt := 1
			if protocolFailures != nil && *protocolFailures > 0 {
				attempt = *protocolFailures
			}
			if phase == domain.RunStateDiagnosing && attempt >= maxConsecutiveProtocolFailures {
				return false, c.requestExhaustionProposalAfterRecorded(
					ctx, budget, runID, conversation, "protocol_no_progress",
				)
			}
			correction := ProtocolCorrectionFor(phase, cause)
			challenge, buildErr := NewRecoveryChallenge(
				domain.RecoveryChallengeKindProtocolCorrection,
				domain.RecoverySeverityRecoverable,
				correction.Code,
				"agentEnvelope",
				tracker.recoveryCapabilities(),
				[]string{"correct_request"},
				attempt,
				budget.remaining(),
				correction.Message,
			)
			if buildErr != nil {
				return true, c.fail(ctx, runID, phase, markPersistenceFailure(fmt.Errorf("build protocol recovery challenge: %w", buildErr)))
			}
			tracker.appendRecovery(domain.CheckpointRecovery{
				Kind: string(challenge.Kind), Action: "correct_envelope", OutcomeRef: "challenge:" + challenge.ReasonCode,
			})
			if checkpointErr := c.checkpointRun(ctx, tracker, phase, domain.CheckpointReasonRecovery); checkpointErr != nil {
				return true, c.fail(ctx, runID, phase, markPersistenceFailure(checkpointErr))
			}
			if conversation != nil {
				c.appendRecoveryChallenge(ctx, phase, conversation, challenge)
			}
			return false, nil
		}
		if protocolFailures != nil && *protocolFailures >= maxConsecutiveProtocolFailures {
			if err := c.transition(ctx, runID, phase, domain.RunStateBlockedManualReview, domain.Effect{
				TerminalReason: "invalid_envelope",
			}); err != nil {
				return true, err
			}
			return true, c.notifyTerminal(ctx, runID, domain.RunStateBlockedManualReview, "")
		}
		if conversation != nil {
			conversation.AppendProtocolError(phase, ProtocolCorrectionFor(phase, cause))
		}
		return false, nil
	}
	return true, c.fail(ctx, runID, phase, cause)
}

// fail transitions from a known active state to failed and records only the
// typed, safe classification that controls future automatic eligibility.
func (c *RemediationCoordinator) fail(ctx context.Context, runID string, from domain.RunState, cause error) error {
	classification := classifyTerminalFailure(cause)
	target := domain.RunStateFailed
	budgetReason := budgetExhaustionReason("")
	if classification.reason == string(budgetReasonElapsed) {
		target = domain.RunStateBudgetExhausted
		budgetReason = budgetReasonElapsed
	}
	if err := c.transitionWithReason(ctx, runID, from, target, domain.Effect{
		TerminalReason: classification.reason,
		Retryable:      classification.retryable,
	}, budgetReason); err != nil {
		return fmt.Errorf("transition to terminal after %v: %w", cause, err)
	}
	return fmt.Errorf("run %s failed in %s: %w", runID, from, cause)
}

// transitionBudgeted 先累计本次 effect，再决定是否把目标状态改为
// budget_exhausted。穿透预算的那一次消耗仍写入 RunStore，便于审计看到
// 触发终止的模型/工具/字节计数。
func (c *RemediationCoordinator) transitionBudgeted(
	ctx context.Context,
	budget *runBudget,
	runID string,
	from, to domain.RunState,
	effect domain.Effect,
) (bool, error) {
	if reason := budget.consume(effect); reason != "" && to != domain.RunStateBudgetExhausted {
		return true, c.transitionWithReason(ctx, runID, from, domain.RunStateBudgetExhausted, effect, reason)
	}
	if err := c.transition(ctx, runID, from, to, effect); err != nil {
		return false, err
	}
	// resilient_v1：把本次已持久化的消耗镜像记入 soft-budget allocator。
	// same-state recovery 可以追加 checkpoint；跨 phase 时只记账，随后关闭旧
	// phase 并接纳新 phase，避免在 durable state 已改变后写旧 phase checkpoint。
	if err := c.softBudgetRecovery(ctx, budget, from, effect, from == to); err != nil {
		return false, c.fail(ctx, runID, to, markPersistenceFailure(err))
	}
	if tracker := resilientStateFrom(ctx); tracker != nil {
		if err := tracker.advanceSoftBudget(from, to); err != nil {
			return false, c.fail(ctx, runID, to, markPersistenceFailure(err))
		}
	}
	return false, nil
}

// recordSameStateBudget 在不改变 phase 的模型/tool 消耗后持久化预算计数。
// 这保持了冻结的 RunStore 接口：预算记账仍通过 Transition 完成，而不新增
// 一个只为当前同步实现服务的方法。
func (c *RemediationCoordinator) recordSameStateBudget(
	ctx context.Context,
	budget *runBudget,
	runID string,
	state domain.RunState,
	effect domain.Effect,
) (bool, error) {
	if !hasBudgetEffect(effect) && budget.resourceExhaustionReason() == "" {
		return false, nil
	}
	return c.transitionBudgeted(ctx, budget, runID, state, state, effect)
}

func (c *RemediationCoordinator) recordElapsedOperation(
	ctx context.Context,
	budget *runBudget,
	runID string,
	state domain.RunState,
	effect domain.Effect,
) (bool, error) {
	_ = budget.consume(effect)
	return true, c.transitionWithReason(ctx, runID, state, domain.RunStateBudgetExhausted, effect, budgetReasonElapsed)
}

func (c *RemediationCoordinator) admitOperation(ctx context.Context, budget *runBudget, runID string, state domain.RunState) (bool, error) {
	reason := budget.admissionExhaustionReason()
	if reason == "" {
		return false, nil
	}
	return true, c.transitionWithReason(ctx, runID, state, domain.RunStateBudgetExhausted, domain.Effect{}, reason)
}

// transition is the single state-advancing primitive; the store enforces the
// optimistic version check and from-state guard.
func (c *RemediationCoordinator) transition(ctx context.Context, runID string, from, to domain.RunState, effect domain.Effect) error {
	return c.transitionWithReason(ctx, runID, from, to, effect, "")
}

func (c *RemediationCoordinator) transitionWithReason(ctx context.Context, runID string, from, to domain.RunState, effect domain.Effect, reason budgetExhaustionReason) error {
	transitionContext := ctx
	var checkpointErr error
	if isTerminalStateForApplication(to) {
		// Terminal persistence must survive a caller/run operation deadline; the
		// run budget and database transaction still bound the actual write.
		transitionContext = context.WithoutCancel(ctx)
		tracker := resilientStateFrom(ctx)
		// 放弃型终态（failed/budget_exhausted/blocked_manual_review）先静默关闭
		// recovery episode，使随后的 phase_boundary checkpoint 不结算
		// recovery-success：run 没有从 recovery 收敛回前进路径。业务结论终态
		// （diagnosis_ready_for_review 等）保留 open episode，由其终态前
		// checkpoint 结算一次 success。
		if tracker != nil && metricAbandonmentTerminal(to) {
			tracker.recoveryEpisodeOpen = false
		}
		// required terminal checkpoint 失败本身就是 persistence/consistency
		// terminal blocker。直接把本次 transition 改为 failed，不能继续原终态，
		// 也不能递归重试已经标记 unavailable 的 checkpoint store。
		if tracker == nil || !tracker.checkpointUnavailable {
			checkpointErr = c.checkpointRun(transitionContext, tracker, from, domain.CheckpointReasonPhaseBoundary)
		}
		if checkpointErr != nil {
			to = domain.RunStateFailed
			reason = ""
			effect = domain.Effect{TerminalReason: "persistence_failure", Retryable: false}
		}
	}
	effect = terminalEffectForState(to, effect, reason)
	if err := c.store.Transition(transitionContext, runID, from, to, effect); err != nil {
		return fmt.Errorf("transition %s→%s: %w", from, to, err)
	}
	c.observer.StateTransitioned(ctx, StateTransitionObservation{
		Run: observationRun(ctx), From: from, To: to, Effect: effect, BudgetExhaustedReason: string(reason),
	})
	// Phase 4 指标：resilient run 的每次 durable transition 与进入终态事件都只
	// 携带低基数 state/reason code，不影响 run 语义。
	if tracker := resilientStateFrom(ctx); tracker != nil {
		mode := metricMode(tracker.run.AgentLoopMode)
		transitionMetric := ResilienceMetric{
			Run: observationRun(ctx), Mode: mode, Kind: ResilienceMetricStateTransitioned,
			From: from, To: to,
		}
		if reason != "" {
			transitionMetric.Reason = string(reason)
		}
		c.emitResilienceMetric(ctx, transitionMetric)
		if isTerminalStateForApplication(to) {
			c.emitResilienceMetric(ctx, ResilienceMetric{
				Run: observationRun(ctx), Mode: mode, Kind: ResilienceMetricRunTerminal,
				Phase: to, TerminalReason: effect.TerminalReason,
			})
		}
	}
	if checkpointErr != nil {
		return fmt.Errorf("persist required checkpoint before terminal transition: %w", checkpointErr)
	}
	return nil
}

func operationDeadlineExceeded(ctx context.Context) bool {
	return runWorkDeadlineExceeded(ctx) || errors.Is(ctx.Err(), context.DeadlineExceeded)
}

// modelEffect builds a budget effect crediting one model call and its tokens.
func modelEffect(usage domain.ModelResult) domain.Effect {
	tokensIn := usage.UsageTokensIn
	tokensOut := usage.UsageTokensOut
	modelCalls := usage.ModelCalls
	if modelCalls <= 0 {
		modelCalls = 1
	}
	if tokensIn == 0 && tokensOut == 0 && usage.UsageTokens != 0 {
		// 旧 fake/adapter 只返回总 token；在它们迁移前按 output 记账，
		// 保持既有预算行为，同时让新 adapter 能分别记录 input/output。
		tokensOut = usage.UsageTokens
	}
	return domain.Effect{
		ModelCalls:     modelCalls,
		ModelTokensIn:  tokensIn,
		ModelTokensOut: tokensOut,
		ModelCostCents: usage.UsageCostCents,
		ModelProvider:  usage.Provider,
		ModelName:      usage.Model,
	}
}
