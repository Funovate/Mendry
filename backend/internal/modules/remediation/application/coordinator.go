package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"fixthe/backend/internal/modules/remediation/domain"
	"fixthe/backend/internal/modules/remediation/port"
)

// Compile-time assertion that the coordinator satisfies the port contract.
var _ port.Coordinator = (*RemediationCoordinator)(nil)

// defaultMaxCollectLoops bounds the collect-more-context loop.
const defaultMaxCollectLoops = 3

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
	started := time.Now()
	ctx = withRunObservationContext(ctx, run)
	c.observer.RunStarted(ctx, RunStartedObservation{
		Run: runIdentity(run), Phase: run.State, TriggerReason: in.TriggerReason, Priority: in.Priority,
	})
	ref, scope, err := c.resolveRefs(ctx, run)
	if err != nil {
		failure := c.fail(ctx, run.RunID, domain.RunStateQueued, err)
		c.observeRunCompleted(ctx, run, started, domain.RunStateFailed, "failure")
		return domain.Run{}, failure
	}
	if err := c.drive(ctx, run.RunID, ref, scope); err != nil {
		c.observeRunCompleted(ctx, run, started, domain.RunStateFailed, "failure")
		return domain.Run{}, err
	}
	final, err := c.store.Get(ctx, run.RunID)
	if err != nil {
		c.observeRunCompleted(ctx, run, started, "", "failure")
		return domain.Run{}, fmt.Errorf("load final run: %w", err)
	}
	// Get 聚合不保证带回 series key；用创建结果补齐身份字段。
	final.Run.IncidentID = run.IncidentID
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
	aggregate, err := c.store.Get(ctx, run.RunID)
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
			return domain.RepoRef{}, domain.EvidenceScope{}, fmt.Errorf("resolve repository remote: project id is required")
		}
		resolved, err := c.remotes.CredentialFreeRemoteURL(ctx, identity.ProjectID)
		if err != nil {
			return domain.RepoRef{}, domain.EvidenceScope{}, fmt.Errorf("resolve repository remote: %w", err)
		}
		if resolved == "" {
			return domain.RepoRef{}, domain.EvidenceScope{}, fmt.Errorf("resolve repository remote: remote URL is empty")
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

// drive runs the state machine from queued to a terminal state.
func (c *RemediationCoordinator) drive(ctx context.Context, runID string, ref domain.RepoRef, scope domain.EvidenceScope) error {
	budget := newRunBudget(c.budgetLimits)

	// queued → preparing_context
	if err := c.transition(ctx, runID, domain.RunStateQueued, domain.RunStatePreparingContext, domain.Effect{}); err != nil {
		return err
	}

	source := legacySourceCapability(scope)
	if c.sourceCaps != nil {
		resolved, err := c.sourceCaps.ResolveSourceCapability(ctx, scope.ProjectID, scope.SourceID)
		if err != nil {
			return c.fail(ctx, runID, domain.RunStatePreparingContext,
				fmt.Errorf("resolve source capability: %w", err))
		}
		source = resolved
	}
	catalog, err := c.toolGateway.BuildCatalog(ctx, runID, domain.RunStateDiagnosing, scope, source)
	if err != nil {
		return c.fail(ctx, runID, domain.RunStatePreparingContext, err)
	}
	if c.dynamicRuntime != nil {
		defer func() { _ = c.dynamicRuntime.CloseRun(context.Background(), runID) }()
	}

	initialContext, contextEffect, err := c.contextAssem.AssembleInitialContextObserved(
		ctx, observationRun(ctx), c.observer, ref, scope, source,
	)
	if err != nil {
		return c.fail(ctx, runID, domain.RunStatePreparingContext, err)
	}
	initialContext += "\n" + catalog.StatusText()
	conversation := NewAgentConversation(initialContext)

	// preparing_context → diagnosing
	exhausted, err := c.transitionBudgeted(ctx, budget, runID, domain.RunStatePreparingContext, domain.RunStateDiagnosing, contextEffect)
	if err != nil || exhausted {
		return err
	}

	collectLoops := 0
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
		runDeadlineExceeded := runWorkDeadlineExceeded(operationCtx)
		cancelOperation()
		if runDeadlineExceeded {
			_, transitionErr := c.recordElapsedOperation(ctx, budget, runID, domain.RunStateDiagnosing, modelEffect(usage))
			return transitionErr
		}
		if err != nil {
			exhausted, transitionErr := c.handleTurnError(ctx, budget, runID, domain.RunStateDiagnosing, usage, conversation, err)
			if transitionErr != nil || exhausted {
				return transitionErr
			}
			continue
		}

		switch env.Kind {
		case "requestTool":
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
			if err := c.appendDecision(ctx, runID, env.Diagnosis); err != nil {
				return err
			}
			done, err := c.routeDiagnosis(ctx, budget, runID, ref, scope, catalog, env.Diagnosis, usage, &collectLoops, conversation)
			if err != nil {
				return err
			}
			if done {
				return nil
			}
			// Not done → collected more context; re-diagnose.
			continue

		case "stop":
			// The model deliberately gives up; a human must take over.
			exhausted, err := c.transitionBudgeted(ctx, budget, runID, domain.RunStateDiagnosing, domain.RunStateBlockedManualReview, modelEffect(usage))
			if err != nil || exhausted {
				return err
			}
			return c.notifyTerminal(ctx, runID, domain.RunStateBlockedManualReview, "")
		default:
			exhausted, err := c.handleTurnError(
				ctx, budget, runID, domain.RunStateDiagnosing, usage, conversation,
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
		exhausted, err := c.transitionBudgeted(ctx, budget, runID, domain.RunStateDiagnosing, domain.RunStateCompletedNonCode, modelEffect(usage))
		if err != nil || exhausted {
			return true, err
		}
		return true, c.notifyTerminal(ctx, runID, domain.RunStateCompletedNonCode, diag.Fixability)

	case domain.FixabilityUnsafeToAutomate:
		exhausted, err := c.transitionBudgeted(ctx, budget, runID, domain.RunStateDiagnosing, domain.RunStateBlockedManualReview, modelEffect(usage))
		if err != nil || exhausted {
			return true, err
		}
		return true, c.notifyTerminal(ctx, runID, domain.RunStateBlockedManualReview, diag.Fixability)

	case domain.FixabilityInsufficientEvidence:
		if *collectLoops >= c.maxCollectLoops {
			// Exhausted the bounded loop without enough evidence.
			exhausted, err := c.transitionBudgeted(ctx, budget, runID, domain.RunStateDiagnosing, domain.RunStateBlockedManualReview, modelEffect(usage))
			if err != nil || exhausted {
				return true, err
			}
			return true, c.notifyTerminal(ctx, runID, domain.RunStateBlockedManualReview, diag.Fixability)
		}
		*collectLoops++
		done, err := c.collectMoreContext(ctx, budget, runID, ref, scope, catalog, diag, usage, conversation)
		return done, err

	case domain.FixabilityCodeFixable:
		return true, c.plan(ctx, budget, runID, ref.ProjectID, scope, catalog, usage, conversation)

	default:
		return true, c.fail(ctx, runID, domain.RunStateDiagnosing,
			fmt.Errorf("unknown fixability %q", diag.Fixability))
	}
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
func (c *RemediationCoordinator) plan(ctx context.Context, budget *runBudget, runID, projectID string, scope domain.EvidenceScope, catalog *ToolCatalog, diagUsage domain.ModelResult, conversation *AgentConversation) error {
	exhausted, err := c.transitionBudgeted(ctx, budget, runID, domain.RunStateDiagnosing, domain.RunStatePlanning, modelEffect(diagUsage))
	if err != nil || exhausted {
		return err
	}

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
			domain.RunStatePlanning, projectID, contextText,
			c.toolGateway.AdvertisedToolDefinitionsForCatalog(catalog, domain.RunStatePlanning), conversation,
		)
		runDeadlineExceeded := runWorkDeadlineExceeded(operationCtx)
		cancelOperation()
		if runDeadlineExceeded {
			_, transitionErr := c.recordElapsedOperation(ctx, budget, runID, domain.RunStatePlanning, modelEffect(usage))
			return transitionErr
		}
		if err != nil {
			exhausted, transitionErr := c.handleTurnError(ctx, budget, runID, domain.RunStatePlanning, usage, conversation, err)
			if transitionErr != nil || exhausted {
				return transitionErr
			}
			continue
		}
		if env.Kind != "planCandidates" || env.PlanCandidates == nil {
			exhausted, transitionErr := c.handleTurnError(
				ctx, budget, runID, domain.RunStatePlanning, usage, conversation,
				wrapEnvelopeError("expected planCandidates in planning", fmt.Errorf("kind %q", env.Kind)),
			)
			if transitionErr != nil || exhausted {
				return transitionErr
			}
			continue
		}

		// 先写入计划和建议 diff，再进入 diagnosis_ready_for_review，保证 GET 能读到完整 review chain。
		if err := c.recordPlans(ctx, runID, env.PlanCandidates); err != nil {
			return c.fail(ctx, runID, domain.RunStatePlanning, err)
		}

		exhausted, err = c.transitionBudgeted(ctx, budget, runID, domain.RunStatePlanning, domain.RunStateDiagnosisReadyForReview, modelEffect(usage))
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
		EvidenceCitations:     diag.EvidenceCitations,
		RecommendedNextAction: diag.RecommendedNextAction,
	}
	if err := c.store.AppendDecision(ctx, runID, d); err != nil {
		return fmt.Errorf("append decision: %w", err)
	}
	return nil
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
	cause error,
) (bool, error) {
	exhausted, err := c.recordSameStateBudget(ctx, budget, runID, phase, modelEffect(usage))
	if err != nil || exhausted {
		return exhausted, err
	}
	if errors.Is(cause, ErrInvalidEnvelope) {
		if conversation != nil {
			conversation.AppendProtocolError(phase)
		}
		return false, nil
	}
	return true, c.fail(ctx, runID, phase, cause)
}

// fail transitions from a known active state to failed.
func (c *RemediationCoordinator) fail(ctx context.Context, runID string, from domain.RunState, cause error) error {
	if err := c.transition(ctx, runID, from, domain.RunStateFailed, domain.Effect{}); err != nil {
		return fmt.Errorf("transition to failed after %v: %w", cause, err)
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
	if err := c.store.Transition(ctx, runID, from, to, effect); err != nil {
		return fmt.Errorf("transition %s→%s: %w", from, to, err)
	}
	c.observer.StateTransitioned(ctx, StateTransitionObservation{
		Run: observationRun(ctx), From: from, To: to, Effect: effect, BudgetExhaustedReason: string(reason),
	})
	return nil
}

// modelEffect builds a budget effect crediting one model call and its tokens.
func modelEffect(usage domain.ModelResult) domain.Effect {
	tokensIn := usage.UsageTokensIn
	tokensOut := usage.UsageTokensOut
	if tokensIn == 0 && tokensOut == 0 && usage.UsageTokens != 0 {
		// 旧 fake/adapter 只返回总 token；在它们迁移前按 output 记账，
		// 保持既有预算行为，同时让新 adapter 能分别记录 input/output。
		tokensOut = usage.UsageTokens
	}
	return domain.Effect{
		ModelCalls:     1,
		ModelTokensIn:  tokensIn,
		ModelTokensOut: tokensOut,
		ModelCostCents: usage.UsageCostCents,
		ModelProvider:  usage.Provider,
		ModelName:      usage.Model,
	}
}
