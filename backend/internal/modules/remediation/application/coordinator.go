package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"fixthe/backend/internal/modules/remediation/domain"
	"fixthe/backend/internal/modules/remediation/port"
)

// Compile-time assertion that the coordinator satisfies the port contract.
var _ port.Coordinator = (*RemediationCoordinator)(nil)

// defaultMaxCollectLoops bounds the collect-more-context loop.
const defaultMaxCollectLoops = 3

const maxConsecutiveProtocolFailures = 3

// RemediationCoordinator is the only component allowed to advance run state. It
// drives the walking-skeleton subset of the state machine:
//
//	queued → preparing_context → diagnosing →
//	  {collecting_more_context loop | completed_non_code |
//	   blocked_manual_review | planning → diagnosis_ready_for_review}
//
// Any active state can transition to failed. Reserved states (patching,
// validating, publishing, awaiting_human_review) exist but are unreachable
// here. The coordinator holds only domain ports plus the in-process engine,
// assembler, and gateway; it never receives credentials or raw clients.
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

// SetDockerEvidencePort wires the credential-free Docker log port at the
// composition root; ordinary application callers cannot provide a container ID
// or a remote command.
func (c *RemediationCoordinator) SetDockerEvidencePort(port domain.DockerEvidencePort) {
	c.toolGateway.SetDockerEvidencePort(port)
}

// SetTencentCLSDetailPort 将受信任、incident-bound 的 Tencent CLS detail reader 接到
// mandatory pre-diagnosis evidence gate。
func (c *RemediationCoordinator) SetTencentCLSDetailPort(port domain.TencentCLSDetailPort) {
	c.toolGateway.SetTencentCLSDetailPort(port)
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
		store:           store,
		lookup:          lookup,
		reviews:         reviews,
		notifications:   notifications,
		contextAssem:    NewContextAssembler(repoPort, evidencePort),
		agentEngine:     NewAgentEngine(llmPort, gateway),
		toolGateway:     gateway,
		budgetLimits:    normalizeBudgetLimits(limits),
		maxCollectLoops: defaultMaxCollectLoops,
		observer:        noopRunObserver{},
		evidenceGate:    NewEvidenceGate(nil),
	}
}

// Start 用真实 UUID 创建或复用 series 根 run，仅当状态仍为 queued 时 drive。
func (c *RemediationCoordinator) Start(ctx context.Context, in domain.NewRun) (domain.Run, error) {
	run, err := c.store.CreateSeriesAndRun(ctx, in)
	if err != nil {
		return domain.Run{}, fmt.Errorf("create series and run: %w", err)
	}
	if run.State != domain.RunStateQueued {
		return run, nil
	}
	return c.runQueued(ctx, run, "", domain.RunStateDiagnosing, in.TriggerReason, in.Priority, nil)
}

// Continue 创建并驱动一个新的 linked attempt。已通过 evidence gate 的 durable
// code_fixable diagnosis 可直接恢复 planning；其余前置结果仍从 diagnosis 重新验证。
// AttemptStore 在事务内再次执行 predecessor/version 检查。
func (c *RemediationCoordinator) Continue(ctx context.Context, in domain.NextAttempt) (domain.Run, error) {
	child, brief, resumePhase, priorInvocations, err := c.prepareContinuation(ctx, in)
	if err != nil {
		return domain.Run{}, err
	}
	return c.runQueued(ctx, child, brief, resumePhase, child.TriggerReason, "", priorInvocations)
}

// prepareContinuation 只执行 continuation 的读取、校验和 queued child 持久化。
// 它不访问 connector 或 model，使 HTTP 可以在创建 attempt 后立即返回；调用方
// 负责决定同步驱动还是在脱离请求取消的后台上下文中驱动。
func (c *RemediationCoordinator) prepareContinuation(
	ctx context.Context,
	in domain.NextAttempt,
) (domain.Run, string, domain.RunState, []domain.ToolInvocation, error) {
	if err := in.Validate(); err != nil {
		return domain.Run{}, "", domain.RunStateDiagnosing, nil, fmt.Errorf("%w: %v", domain.ErrInvalidNextAttempt, err)
	}
	attempts, ok := c.store.(domain.AttemptStore)
	if !ok {
		return domain.Run{}, "", domain.RunStateDiagnosing, nil, fmt.Errorf("continuation attempt store is required")
	}
	predecessor, err := c.store.Get(ctx, in.ContinuationOfRunID)
	if err != nil {
		return domain.Run{}, "", domain.RunStateDiagnosing, nil, fmt.Errorf("load continuation predecessor: %w", err)
	}
	if err := validateContinuationPredecessor(predecessor.Run, in); err != nil {
		return domain.Run{}, "", domain.RunStateDiagnosing, nil, err
	}
	var planningCheckpoint *domain.RunAggregate
	if predecessor.Run.ContextVersion == in.ContextVersion {
		checkpoints, ok := c.store.(domain.PlanningCheckpointStore)
		if !ok {
			return domain.Run{}, "", domain.RunStateDiagnosing, nil, fmt.Errorf("continuation planning checkpoint store is required")
		}
		checkpoint, checkpointErr := checkpoints.GetLatestPlanningCheckpoint(
			ctx, in.SeriesID, in.ContextVersion, predecessor.Run.AttemptNumber,
		)
		switch {
		case checkpointErr == nil:
			if !validContinuationPlanningCheckpoint(checkpoint, predecessor.Run, in) {
				return domain.Run{}, "", domain.RunStateDiagnosing, nil, fmt.Errorf("continuation planning checkpoint is invalid")
			}
			planningCheckpoint = &checkpoint
		case errors.Is(checkpointErr, domain.ErrPlanningCheckpointNotFound):
			// 同一 context 没有 durable code_fixable checkpoint 时按正常 diagnosis 路径继续。
		default:
			return domain.Run{}, "", domain.RunStateDiagnosing, nil, fmt.Errorf("load continuation planning checkpoint: %w", checkpointErr)
		}
	}
	brief := buildContinuationBrief(predecessor, planningCheckpoint, in)
	resumePhase := continuationResumePhase(planningCheckpoint)
	child, err := attempts.CreateNextAttempt(ctx, in)
	if err != nil {
		return domain.Run{}, "", domain.RunStateDiagnosing, nil, err
	}
	if child.State != domain.RunStateQueued || child.RunID == "" || child.SeriesID != in.SeriesID ||
		child.IncidentID != in.IncidentID || child.LifecycleGeneration != in.LifecycleGeneration ||
		child.DeployedCommit != in.DeployedCommit || child.ContinuationOfRunID != in.ContinuationOfRunID ||
		child.AttemptNumber != predecessor.Run.AttemptNumber+1 {
		return domain.Run{}, "", domain.RunStateDiagnosing, nil, domain.ErrStalePredecessor
	}
	return child, brief, resumePhase, predecessor.ToolInvocations, nil
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
	c.observer.RunStarted(ctx, RunStartedObservation{
		Run: runIdentity(run), Phase: run.State, TriggerReason: triggerReason, Priority: priority,
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
	analysisOnly := triggerReason == domain.TriggerOriginManualContinue
	if err := c.drive(ctx, run.RunID, ref, scope, continuationBrief, resumePhase, priorInvocations, analysisOnly); err != nil {
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
	resumePhase domain.RunState,
	priorInvocations []domain.ToolInvocation,
	analysisOnly bool,
) error {
	budget := newRunBudget(c.budgetLimits)

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
	if continuationBrief != "" {
		// brief 只是 predecessor 的 hypothesis；fresh incident/evidence bootstrap
		// 保留在同一 model context 中并具有更高权威。
		initialContext = continuationBrief + "\n\n" + initialContext
	}
	conversation := NewAgentConversation(initialContext)
	if resumePhase == domain.RunStatePlanning {
		return c.planFrom(ctx, budget, runID, ref, scope, catalog, domain.RunStatePreparingContext, contextEffect, conversation)
	}

	// preparing_context → diagnosing
	exhausted, err := c.transitionBudgeted(ctx, budget, runID, domain.RunStatePreparingContext, domain.RunStateDiagnosing, contextEffect)
	if err != nil || exhausted {
		return err
	}

	collectLoops := 0
	protocolFailures := 0
	for {
		if exhausted, err := c.admitOperation(ctx, budget, runID, domain.RunStateDiagnosing); err != nil || exhausted {
			return err
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
				conversation.AppendProtocolError(domain.RunStateDiagnosing, requiredTencentDetailCorrection())
				continue
			}
			protocolFailures = 0
			diagnosis := env.Diagnosis
			if diagnosis.Fixability == domain.FixabilityCodeFixable {
				if c.evidenceGate == nil {
					c.evidenceGate = NewEvidenceGate(nil)
				}
				gated, _, gateErr := c.evidenceGate.Apply(ctx, runID, diagnosis)
				if gateErr != nil {
					return c.fail(ctx, runID, domain.RunStateDiagnosing, markPersistenceFailure(fmt.Errorf("evaluate evidence gate: %w", gateErr)))
				}
				diagnosis = gated
			}
			if err := c.appendDecision(ctx, runID, diagnosis); err != nil {
				return c.fail(ctx, runID, domain.RunStateDiagnosing, markPersistenceFailure(err))
			}
			done, err := c.routeDiagnosis(ctx, budget, runID, ref, scope, catalog, diagnosis, usage, &collectLoops, conversation)
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
				conversation.AppendProtocolError(domain.RunStateDiagnosing, requiredTencentDetailCorrection())
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
			exhausted, err := c.transitionBudgeted(ctx, budget, runID, domain.RunStateDiagnosing, domain.RunStateBlockedManualReview, effect)
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
			// insufficient_evidence 是完整终态 diagnosis。没有明确 tool request 时重放
			// 同一 prompt 不会增加 evidence，analysis-only continuation 尤其不能空转。
			return true, c.blockForInsufficientEvidence(ctx, budget, runID, usage, diag.Fixability)
		}
		if *collectLoops >= c.maxCollectLoops {
			return true, c.blockForInsufficientEvidence(ctx, budget, runID, usage, diag.Fixability)
		}
		*collectLoops++
		done, err := c.collectMoreContext(ctx, budget, runID, ref, scope, catalog, diag, usage, conversation)
		return done, err

	case domain.FixabilityCodeFixable:
		return true, c.plan(ctx, budget, runID, ref, scope, catalog, usage, conversation)

	default:
		return true, c.fail(ctx, runID, domain.RunStateDiagnosing,
			fmt.Errorf("unknown fixability %q", diag.Fixability))
	}
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
	exhausted, err := c.transitionBudgeted(ctx, budget, runID, from, domain.RunStatePlanning, initialEffect)
	if err != nil || exhausted {
		return err
	}

	protocolFailures := 0
	for {
		if exhausted, err := c.admitOperation(ctx, budget, runID, domain.RunStatePlanning); err != nil || exhausted {
			return err
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

// notifyTerminal 在到达 PRD 列出的四个终态后写入 in-console audit。
// 通知失败会使本次 run 失败：控制台必须能看到 durable 结果，测试也保持确定性。
func (c *RemediationCoordinator) notifyTerminal(ctx context.Context, runID string, state domain.RunState, fixability domain.FixabilityClass) error {
	kind := NotificationKindForTerminal(state, fixability)
	if kind == "" || c.notifications == nil {
		return nil
	}
	if err := c.notifications.Notify(ctx, TerminalNotification{
		RunID:      runID,
		Kind:       kind,
		Summary:    notificationSummary(kind),
		Fixability: fixability,
		State:      state,
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
	if conversation != nil {
		conversation.AppendToolResult(*req, res, err)
	}
	// Best-effort audit record; a storage error here does not change the model's
	// decision path and is surfaced on the next transition instead.
	_ = c.store.RecordToolInvocation(ctx, runID, inv)
	if runDeadlineExceeded {
		exhausted, budgetErr := c.recordElapsedOperation(ctx, budget, runID, phase, effect)
		return exhausted, res, budgetErr
	}
	exhausted, budgetErr := c.recordSameStateBudget(ctx, budget, runID, phase, effect)
	return exhausted, res, budgetErr
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
	return false, c.transition(ctx, runID, from, to, effect)
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
	effect = terminalEffectForState(to, effect, reason)
	transitionContext := ctx
	if isTerminalStateForApplication(to) {
		// Terminal persistence must survive a caller/run operation deadline; the
		// run budget and database transaction still bound the actual write.
		transitionContext = context.WithoutCancel(ctx)
	}
	if err := c.store.Transition(transitionContext, runID, from, to, effect); err != nil {
		return fmt.Errorf("transition %s→%s: %w", from, to, err)
	}
	c.observer.StateTransitioned(ctx, StateTransitionObservation{
		Run: observationRun(ctx), From: from, To: to, Effect: effect, BudgetExhaustedReason: string(reason),
	})
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
