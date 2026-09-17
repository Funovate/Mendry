package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"

	"mendry/backend/internal/modules/remediation/domain"
)

var (
	// ErrLifecycleUnavailable 表示 resilient lifecycle 的必要 companion port 未接入。
	ErrLifecycleUnavailable = errors.New("resilient remediation lifecycle is unavailable")
	// ErrLifecycleNotReady 表示当前 run 尚未到达或已经越过可执行 lifecycle phase。
	ErrLifecycleNotReady = errors.New("remediation lifecycle is not ready")
	// ErrLifecyclePlanNotFound 表示 operator 选择的 plan 不属于当前 run。
	ErrLifecyclePlanNotFound = errors.New("remediation plan is not found")
)

const (
	maxLifecycleRecoveryAttempts = 3
	defaultPublicationPrefix     = "hotfix/remediation"
)

// LifecyclePublicationPolicy 是 run 外部发布的安全项目策略投影。
type LifecyclePublicationPolicy struct {
	TargetBranch string
	BranchPrefix string
}

// SetPlanPolicyEvaluator 注入项目级候选计划 policy；nil 会关闭额外 policy
// evaluator，但 resilient run 仍要求 checkpoint/effect 端口存在。
func (c *RemediationCoordinator) SetPlanPolicyEvaluator(evaluator domain.PlanPolicyEvaluator) {
	c.planPolicy = evaluator
}

// SetWorkspacePort 注入隔离 workspace adapter。该 adapter 不应暴露 Git 写凭据。
func (c *RemediationCoordinator) SetWorkspacePort(workspace domain.WorkspacePort) {
	c.workspace = workspace
	c.lifecycleTools = NewLifecycleToolGateway(c.workspace, c.validation)
}

// SetValidationPort 注入只接受 approved command ID 的 sandbox validation adapter。
func (c *RemediationCoordinator) SetValidationPort(validation domain.ValidationPort) {
	c.validation = validation
	c.lifecycleTools = NewLifecycleToolGateway(c.workspace, c.validation)
}

// SetPublicationPort injects the legacy combined publisher for compatible deployments.
func (c *RemediationCoordinator) SetPublicationPort(publication domain.PublicationPort) {
	c.publication = publication
}

// SetGitPublicationPort injects branch publication independently from change requests.
func (c *RemediationCoordinator) SetGitPublicationPort(publication domain.GitPublicationPort) {
	c.gitPublication = publication
}

// SetChangeRequestPort injects provider capability probing and PR/MR creation.
func (c *RemediationCoordinator) SetChangeRequestPort(changeRequests domain.ChangeRequestPort) {
	c.changeRequests = changeRequests
}

// SetLifecycleStore 注入 workspace/patch/validation/publication 的 durable effect store。
func (c *RemediationCoordinator) SetLifecycleStore(store domain.LifecycleStore) {
	c.lifecycleStore = store
}

// SetValidationCommandVersions 设置 run 可见的 approved validation command 快照。
// map 的 value 是 immutable command version，不接受 shell 文本。
func (c *RemediationCoordinator) SetValidationCommandVersions(versions map[string]int64) {
	c.validationCommands = make(map[string]int64, len(versions))
	for commandID, version := range versions {
		c.validationCommands[commandID] = version
	}
}

// SetLifecyclePublicationPolicy 设置 publication target/ref 的安全默认策略。
func (c *RemediationCoordinator) SetLifecyclePublicationPolicy(policy LifecyclePublicationPolicy) {
	c.publicationPolicy = policy
}

// ApplyPlan 是显式 plan-review 之后进入 patching 的 resilient lifecycle 入口。
// 它不改变 diagnosis 结果；调用方必须提供当前 run 的 plan ID，所有外部效果
// 通过 durable lifecycle effect projection 和稳定幂等 key 恢复。
func (c *RemediationCoordinator) ApplyPlan(ctx context.Context, runID, planID string) (domain.Run, error) {
	if c.executor != nil && !c.executor.owns(ctx, runID) {
		return c.executor.Execute(ctx, runID, func(owned context.Context) (domain.Run, error) {
			return c.ApplyPlan(owned, runID, planID)
		})
	}
	agg, err := c.store.Get(ctx, runID)
	if err != nil {
		return domain.Run{}, fmt.Errorf("load remediation plan run: %w", err)
	}
	if agg.Run.AnalysisOnly {
		return agg.Run, ErrLifecycleUnavailable
	}
	if domain.ParseAgentLoopMode(string(agg.Run.AgentLoopMode)) != domain.AgentLoopModeResilientV1 {
		return agg.Run, ErrLifecycleUnavailable
	}
	if agg.Run.State != domain.RunStateDiagnosisReadyForReview {
		if isLifecycleActiveState(agg.Run.State) {
			return c.ResumeLifecycle(ctx, runID)
		}
		return agg.Run, fmt.Errorf("%w: state %s cannot accept a plan", ErrLifecycleNotReady, agg.Run.State)
	}
	if err := c.validateLifecycleDependencies(agg.Run); err != nil {
		return agg.Run, err
	}
	selected, err := selectRepairPlan(agg, planID)
	if err != nil {
		return agg.Run, err
	}
	if c.planPolicy != nil {
		decision, policyErr := c.planPolicy.EvaluatePlan(ctx, domain.PlanPolicyInput{
			RunID: agg.Run.RunID, BaselineCommit: agg.Run.DeployedCommit,
			Candidates: []domain.RepairPlanCandidate{selected}, RecommendedID: selected.PlanID,
		})
		if policyErr != nil {
			return agg.Run, fmt.Errorf("evaluate selected plan policy: %w", policyErr)
		}
		if !decision.Accepted {
			if decision.Severity == domain.RecoverySeverityPolicyBlocked || decision.Severity == domain.RecoverySeverityHardTerminal {
				return agg.Run, fmt.Errorf("%w: %s", ErrLifecycleNotReady, safePlanPolicyReason(decision.ReasonCode))
			}
			return agg.Run, fmt.Errorf("%w: selected plan is not policy compliant", ErrLifecycleNotReady)
		}
		for _, commandID := range decision.RequiredValidationIDs {
			if version := c.validationCommands[commandID]; version < 1 {
				return agg.Run, fmt.Errorf("%w: required validation command is not snapshotted", ErrLifecycleUnavailable)
			}
		}
	}

	ctx = withRunObservationContext(ctx, agg.Run)
	tracker, err := c.newLifecycleTracker(ctx, agg.Run)
	if err != nil {
		return domain.Run{}, err
	}
	tracker.lifecyclePlanID = selected.PlanID
	tracker.nextActions = []string{"apply selected plan " + boundedContinuationText(selected.PlanID, 128)}
	tracker.validationCommands = cloneValidationCommands(c.validationCommands)
	if len(agg.Run.ValidationCommands) > 0 {
		tracker.validationCommands = cloneValidationCommands(agg.Run.ValidationCommands)
	}
	publicationPolicy := c.publicationPolicy
	if strings.TrimSpace(agg.Run.PublicationTargetBranch) != "" {
		publicationPolicy.TargetBranch = agg.Run.PublicationTargetBranch
		publicationPolicy.BranchPrefix = agg.Run.PublicationBranchPrefix
	}
	tracker.publicationPolicy = checkpointPublicationPolicy(publicationPolicy)
	tracker.conversation = NewAgentConversation(lifecyclePlanContext(agg.Run, selected, agg.SuggestedDiff))
	ctx = withResilientRunState(ctx, tracker)
	budget := resumeRunBudget(
		restoredRunBudgetLimits(c.budgetLimits, tracker),
		restoredRunBudgetCounters(tracker, agg.Run.Budget),
	)
	ctx = withLifecycleBudget(ctx, budget)
	if localValidationEnabled(agg.Run.ExecutionProfile) && len(tracker.validationCommands) == 0 {
		return agg.Run, fmt.Errorf("%w: no approved validation command snapshot", ErrLifecycleUnavailable)
	}
	if tracker.alloc == nil {
		tracker.admitSoftBudget(c.budgetLimits, domain.RunStatePlanning)
	}
	if err := c.checkpointRun(ctx, tracker, domain.RunStatePlanning, domain.CheckpointReasonPhaseBoundary); err != nil {
		return domain.Run{}, c.fail(ctx, runID, domain.RunStateDiagnosisReadyForReview, markPersistenceFailure(err))
	}
	if exhausted, transitionErr := c.transitionBudgeted(ctx, budget, runID, domain.RunStateDiagnosisReadyForReview, domain.RunStatePatching, domain.Effect{}); transitionErr != nil {
		return domain.Run{}, transitionErr
	} else if exhausted {
		return c.loadLifecycleRun(ctx, runID)
	}
	if err := c.checkpointRun(ctx, tracker, domain.RunStatePatching, domain.CheckpointReasonPhaseBoundary); err != nil {
		return domain.Run{}, c.fail(ctx, runID, domain.RunStatePatching, markPersistenceFailure(err))
	}
	current, err := c.store.Get(ctx, runID)
	if err != nil {
		return domain.Run{}, fmt.Errorf("load patching run: %w", err)
	}
	if current.Run.ProjectID == "" {
		current.Run.ProjectID = agg.Run.ProjectID
	}
	if err := c.driveLifecycle(ctx, budget, current.Run, selected); err != nil {
		return domain.Run{}, err
	}
	return c.loadLifecycleRun(ctx, runID)
}

// ResumeLifecycle 从当前 active lifecycle phase 继续工作；它优先使用已成功
// effect projection，避免 process restart 重复创建 workspace、patch、validation
// 或 publication external effect。
func (c *RemediationCoordinator) ResumeLifecycle(ctx context.Context, runID string) (domain.Run, error) {
	if c.executor != nil && !c.executor.owns(ctx, runID) {
		return c.executor.Execute(ctx, runID, func(owned context.Context) (domain.Run, error) {
			return c.ResumeLifecycle(owned, runID)
		})
	}
	agg, err := c.store.Get(ctx, runID)
	if err != nil {
		return domain.Run{}, fmt.Errorf("load remediation lifecycle run: %w", err)
	}
	if agg.Run.AnalysisOnly {
		return agg.Run, ErrLifecycleUnavailable
	}
	if domain.ParseAgentLoopMode(string(agg.Run.AgentLoopMode)) != domain.AgentLoopModeResilientV1 {
		return agg.Run, ErrLifecycleUnavailable
	}
	if agg.Run.State == domain.RunStateAwaitingHumanReview {
		return agg.Run, nil
	}
	if !isLifecycleActiveState(agg.Run.State) {
		return agg.Run, fmt.Errorf("%w: state %s is not resumable", ErrLifecycleNotReady, agg.Run.State)
	}
	if err := c.validateLifecycleDependencies(agg.Run); err != nil {
		return agg.Run, err
	}
	selected, err := selectRepairPlan(agg, agg.RecommendedPlanID)
	if err != nil {
		return agg.Run, err
	}
	ctx = withRunObservationContext(ctx, agg.Run)
	tracker, err := c.newLifecycleTracker(ctx, agg.Run)
	if err != nil {
		return domain.Run{}, err
	}
	tracker.lifecyclePlanID = selected.PlanID
	tracker.conversation = NewAgentConversation(lifecyclePlanContext(agg.Run, selected, agg.SuggestedDiff))
	if tracker.publicationPolicy == nil || (localValidationEnabled(agg.Run.ExecutionProfile) && len(tracker.validationCommands) == 0) {
		return agg.Run, fmt.Errorf("%w: lifecycle policy snapshot is incomplete", ErrLifecycleUnavailable)
	}
	ctx = withResilientRunState(ctx, tracker)
	budget := resumeRunBudget(
		restoredRunBudgetLimits(c.budgetLimits, tracker),
		restoredRunBudgetCounters(tracker, agg.Run.Budget),
	)
	ctx = withLifecycleBudget(ctx, budget)
	if tracker.alloc == nil {
		phase := agg.Run.State
		if phase == domain.RunStateDiagnosisReadyForReview {
			phase = domain.RunStatePlanning
		}
		tracker.admitSoftBudget(c.budgetLimits, phase)
	}
	if tracker.checkpointNeedsRebuild {
		if err := c.checkpointRun(ctx, tracker, agg.Run.State, domain.CheckpointReasonPhaseBoundary); err != nil {
			return domain.Run{}, c.fail(ctx, runID, agg.Run.State, markPersistenceFailure(err))
		}
		tracker.checkpointNeedsRebuild = false
	}
	if agg.Run.State != domain.RunStatePublishing && localValidationEnabled(agg.Run.ExecutionProfile) && len(tracker.validationCommands) == 0 {
		return agg.Run, fmt.Errorf("%w: validation command snapshot is unavailable", ErrLifecycleUnavailable)
	}
	if err := c.driveLifecycle(ctx, budget, agg.Run, selected); err != nil {
		return domain.Run{}, err
	}
	return c.loadLifecycleRun(ctx, runID)
}

func (c *RemediationCoordinator) validateLifecycleDependencies(run domain.Run) error {
	missing := make([]string, 0, 5)
	if c.checkpointStore == nil {
		missing = append(missing, "checkpoint store")
	}
	if c.lifecycleStore == nil {
		missing = append(missing, "lifecycle store")
	}
	if c.workspace == nil {
		missing = append(missing, "workspace adapter")
	}
	if localValidationEnabled(run.ExecutionProfile) && c.validation == nil {
		missing = append(missing, "validation adapter")
	}
	if c.publication == nil && c.gitPublication == nil {
		missing = append(missing, "publication adapter")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: missing lifecycle dependencies: %s", ErrLifecycleUnavailable, strings.Join(missing, ", "))
	}
	targetBranch := c.publicationPolicy.TargetBranch
	if run.PublicationTargetBranch != "" {
		targetBranch = run.PublicationTargetBranch
	}
	if strings.TrimSpace(targetBranch) == "" {
		return fmt.Errorf("%w: publication target branch is required", ErrLifecycleUnavailable)
	}
	if run.ExecutionMode == domain.ExecutionModeAutoHotfix && !run.AnalysisOnly {
		if err := validateAutoHotfixSnapshot(run); err != nil {
			return fmt.Errorf("%w: %v", ErrLifecycleUnavailable, err)
		}
		if run.PublicationSnapshot.SCMProvider != "generic" && c.changeRequests == nil {
			return fmt.Errorf("%w: change-request adapter is required for this provider", ErrLifecycleUnavailable)
		}
	} else if len(c.validationCommands) == 0 && len(run.ValidationCommands) == 0 {
		return fmt.Errorf("%w: no approved validation command snapshot", ErrLifecycleUnavailable)
	}
	return nil
}

func localValidationEnabled(profile domain.ExecutionProfileSnapshot) bool {
	// Snapshots written before the enabled flag remain enhanced snapshots.
	return profile.Enabled == nil || *profile.Enabled
}

func validateAutoHotfixSnapshot(run domain.Run) error {
	profile := run.ExecutionProfile
	if localValidationEnabled(profile) {
		if err := validateLocalValidationSnapshot(run, profile); err != nil {
			return err
		}
	} else if len(run.ValidationCommands) != 0 || run.ValidationImageDigest != "" {
		return fmt.Errorf("disabled local validation snapshot contains validation configuration")
	}

	repository := run.PublicationSnapshot
	remote, err := url.Parse(repository.RemoteURL)
	if err != nil || remote.Host == "" || remote.User != nil || remote.Scheme != repository.Transport || (remote.Scheme != "https" && remote.Scheme != "ssh") {
		return fmt.Errorf("immutable repository snapshot is invalid")
	}
	switch repository.SCMProvider {
	case "github", "gitlab", "gitee", "yunxiao", "generic":
	default:
		return fmt.Errorf("immutable SCM provider snapshot is invalid")
	}
	if repository.ProductionBranch == "" || repository.ProductionBranch != run.PublicationTargetBranch || repository.GitCredentialSecretID == "" || repository.GitCredentialVersion < 1 {
		return fmt.Errorf("immutable publication snapshot is incomplete")
	}
	if repository.RepositoryCredentialSecretID != "" && repository.RepositoryCredentialVersion < 1 {
		return fmt.Errorf("immutable repository credential snapshot is incomplete")
	}
	if repository.APICredentialSecretID != "" && repository.APICredentialVersion < 1 {
		return fmt.Errorf("immutable API credential snapshot is incomplete")
	}
	if repository.APIBaseURL != "" {
		apiURL, parseErr := url.Parse(repository.APIBaseURL)
		if parseErr != nil || apiURL.Host == "" || apiURL.User != nil || (apiURL.Scheme != "https" && apiURL.Scheme != "http") {
			return fmt.Errorf("immutable provider API URL snapshot is invalid")
		}
	}

	policy := run.ChangePolicySnapshot
	if len(policy.AllowedPaths) == 0 || len(policy.AllowedPaths) > 64 || len(policy.DeniedPaths) > 64 || policy.MaxChangedFiles < 1 || policy.MaxChangedFiles > 30 || policy.MaxChangedLines < 1 || policy.MaxChangedLines > 5000 {
		return fmt.Errorf("immutable change policy snapshot is incomplete")
	}
	for _, pattern := range append(append([]string{}, policy.AllowedPaths...), policy.DeniedPaths...) {
		if strings.TrimSpace(pattern) == "" || strings.HasPrefix(pattern, "/") || len(pattern) > 256 {
			return fmt.Errorf("immutable path policy snapshot is invalid")
		}
		for _, segment := range strings.Split(pattern, "/") {
			if segment == ".." {
				return fmt.Errorf("immutable path policy snapshot is invalid")
			}
		}
		if _, err := path.Match(pattern, ""); err != nil {
			return fmt.Errorf("immutable path policy snapshot is invalid")
		}
	}
	return nil
}

func validateLocalValidationSnapshot(run domain.Run, profile domain.ExecutionProfileSnapshot) error {
	if profile.ImageDigest != run.ValidationImageDigest || !strings.HasPrefix(profile.ImageDigest, "sha256:") || len(profile.ImageDigest) != len("sha256:")+64 {
		return fmt.Errorf("immutable validation image snapshot is incomplete")
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(profile.ImageDigest, "sha256:")); err != nil {
		return fmt.Errorf("immutable validation image snapshot is invalid")
	}
	workingDirectory := path.Clean(profile.WorkingDirectory)
	if profile.WorkingDirectory == "" || path.IsAbs(profile.WorkingDirectory) || workingDirectory == ".." || strings.HasPrefix(workingDirectory, "../") {
		return fmt.Errorf("immutable validation working directory is invalid")
	}
	if profile.CPULimit < 1 || profile.CPULimit > 16 || profile.MemoryLimitMiB < 256 || profile.MemoryLimitMiB > 65536 || profile.WorkspaceLimitMiB < 1024 || profile.WorkspaceLimitMiB > 102400 {
		return fmt.Errorf("immutable execution resource limits are invalid")
	}
	if len(profile.RequiredCommands) == 0 || len(profile.RequiredCommands) > 32 || len(profile.Preparation) > 16 || len(run.ValidationCommands) != len(profile.RequiredCommands) {
		return fmt.Errorf("immutable validation command snapshot is incomplete")
	}
	seenCommands := make(map[string]struct{}, len(profile.RequiredCommands)+len(profile.Preparation))
	for _, command := range append(append([]domain.ValidationCommandSnapshot{}, profile.Preparation...), profile.RequiredCommands...) {
		if command.ID == "" || len(command.ID) > 128 || command.Version < 1 || len(command.Argv) == 0 || len(command.Argv) > 64 || command.TimeoutSeconds < 1 || command.TimeoutSeconds > 3600 {
			return fmt.Errorf("immutable validation command snapshot is invalid")
		}
		if _, exists := seenCommands[command.ID]; exists {
			return fmt.Errorf("immutable validation command IDs are not unique")
		}
		seenCommands[command.ID] = struct{}{}
		for _, arg := range command.Argv {
			if strings.TrimSpace(arg) == "" || len(arg) > 4096 || strings.ContainsRune(arg, '\x00') {
				return fmt.Errorf("immutable validation argv is invalid")
			}
		}
	}
	for _, command := range profile.RequiredCommands {
		if run.ValidationCommands[command.ID] != command.Version {
			return fmt.Errorf("validation command version map does not match profile")
		}
	}
	return nil
}

func isLifecycleActiveState(state domain.RunState) bool {
	switch state {
	case domain.RunStatePatching, domain.RunStateValidating, domain.RunStatePublishing:
		return true
	default:
		return false
	}
}

func selectRepairPlan(agg domain.RunAggregate, planID string) (domain.RepairPlanCandidate, error) {
	planID = strings.TrimSpace(planID)
	if planID == "" {
		planID = strings.TrimSpace(agg.RecommendedPlanID)
	}
	for _, plan := range agg.Plans {
		if plan.PlanID == planID || (planID == "" && plan.Recommended) {
			return plan, nil
		}
	}
	return domain.RepairPlanCandidate{}, ErrLifecyclePlanNotFound
}

func (c *RemediationCoordinator) newLifecycleTracker(ctx context.Context, run domain.Run) (*resilientRunState, error) {
	tracker := newResilientRunState(c.checkpointStore, run, "")
	tracker.lifecyclePhase = run.State
	if tracker.lifecyclePhase == domain.RunStateDiagnosisReadyForReview {
		tracker.lifecyclePhase = domain.RunStatePlanning
	}
	if snapshot, err := c.checkpointStore.LoadLatestCheckpoint(ctx, run.RunID); err == nil {
		checkpoint := snapshot.Checkpoint
		if err := checkpoint.Validate(); err != nil {
			return nil, fmt.Errorf("validate lifecycle checkpoint: %w", err)
		}
		tracker.observedVersion = run.Version
		tracker.workspace = cloneCheckpointWorkspace(checkpoint.Workspace)
		tracker.artifacts = append([]domain.CheckpointArtifact(nil), checkpoint.Artifacts...)
		tracker.validation = cloneCheckpointValidation(checkpoint.Validation)
		tracker.publication = cloneCheckpointPublication(checkpoint.Publication)
		tracker.publicationPolicy = cloneCheckpointPublicationPolicy(checkpoint.PublicationPolicy)
		tracker.validationCommands = cloneValidationCommands(checkpoint.ValidationCommands)
		tracker.recoveries = append([]domain.CheckpointRecovery(nil), checkpoint.Recoveries...)
		// restart 中途 recovery：最近 durable checkpoint 是 recovery 触发时，把
		// episode 保持打开，使恢复后的第一个非 recovery durable checkpoint 结算
		// recovery-success（AC7 durable tracker state）。
		tracker.recoveryEpisodeOpen = checkpoint.Reason == domain.CheckpointReasonRecovery
		if checkpoint.RecoveryProgress != nil {
			tracker.restoreRecoveryProgressSnapshot(*checkpoint.RecoveryProgress)
		} else {
			tracker.restoreRecoveryProgress()
		}
		tracker.nextActions = append([]string(nil), checkpoint.NextActions...)
		if checkpoint.Budget.SchemaVersion != "" {
			allocator, restoreErr := restorePhaseBudgetPlan(checkpoint.Budget)
			if restoreErr != nil {
				return nil, fmt.Errorf("restore lifecycle budget: %w", restoreErr)
			}
			tracker.alloc = allocator
		}
		if err := reconcileLifecycleCheckpoint(tracker, snapshot, run); err != nil {
			return nil, err
		}
		tracker.checkpointNeedsRebuild = snapshot.NeedsRebuild || checkpoint.ObservedRunVersion < run.Version
	} else if errors.Is(err, domain.ErrCheckpointNotFound) {
		return nil, fmt.Errorf("%w: required lifecycle checkpoint is missing", ErrLifecycleUnavailable)
	} else {
		return nil, fmt.Errorf("load lifecycle checkpoint: %w", err)
	}
	effects, err := c.lifecycleStore.ListLifecycleEffects(ctx, run.RunID)
	if err != nil {
		return nil, fmt.Errorf("load lifecycle effects: %w", err)
	}
	for _, effect := range effects {
		if effect.State != domain.LifecycleEffectSucceeded {
			continue
		}
		switch effect.Kind {
		case domain.LifecycleEffectWorkspace:
			tracker.workspace = &domain.CheckpointWorkspace{
				WorkspaceID: effect.WorkspaceID, BaselineCommit: effect.BaselineCommit,
				BaseTreeHash: effect.BaseTreeHash, CurrentTreeHash: effect.ResultTreeHash, Version: maxInt64(effect.Attempt, 1),
			}
		case domain.LifecycleEffectPatch:
			appendCheckpointArtifact(&tracker.artifacts, domain.CheckpointArtifact{
				Kind: "patch", Reference: effect.ArtifactRef, ContentHash: effect.ContentHash,
				SizeBytes: 0,
			})
			if tracker.workspace != nil && effect.ResultTreeHash != "" {
				tracker.workspace.CurrentTreeHash = effect.ResultTreeHash
			}
			tracker.validation = nil
		case domain.LifecycleEffectValidation:
			validationTree := effect.ResultTreeHash
			if validationTree == "" && tracker.workspace != nil {
				// Pre-aggregation v1 effects omitted the tree identity. They remain readable,
				// but every newly executed validation must attest it explicitly.
				validationTree = tracker.workspace.CurrentTreeHash
			}
			if tracker.validation == nil || tracker.validation.TreeHash != validationTree {
				tracker.validation = &domain.CheckpointValidation{TreeHash: validationTree}
			}
			result := domain.CheckpointValidationResult{
				CommandID: effect.CommandID, CommandVersion: effect.CommandVersion,
				TreeHash: validationTree, Passed: effect.ValidationKnown && effect.ValidationPassed,
				OutputArtifact: effect.ArtifactRef, OutputHash: effect.ContentHash,
			}
			tracker.validation.CommandID = result.CommandID
			tracker.validation.CommandVersion = result.CommandVersion
			tracker.validation.OutputArtifact = result.OutputArtifact
			tracker.validation.OutputHash = result.OutputHash
			tracker.validation.Results = append(tracker.validation.Results, result)
			if tracker.workspace != nil {
				tracker.validation.Passed = validationSetPassed(tracker.validation, tracker.validationCommands, tracker.workspace.CurrentTreeHash)
			}
		case domain.LifecycleEffectPublication, domain.LifecycleEffectGitPublish, domain.LifecycleEffectChangeRequest:
			publication := tracker.publication
			if publication == nil {
				publication = &domain.CheckpointPublication{}
			}
			if effect.BranchRef != "" {
				publication.BranchRef = effect.BranchRef
			}
			if effect.TargetBranch != "" {
				publication.TargetBranch = effect.TargetBranch
			}
			if effect.CommitHash != "" {
				publication.CommitHash = effect.CommitHash
			}
			if effect.DraftChangeRef != "" {
				publication.DraftChangeRef = effect.DraftChangeRef
			}
			if effect.CompareURL != "" {
				publication.CompareURL = effect.CompareURL
			}
			publication.HumanReviewOnly = true
			if targetDiverged, found := targetDivergenceState(effect.Summary); found {
				publication.TargetDiverged = targetDiverged
			}
			tracker.publication = publication
		}
	}
	return tracker, nil
}

// reconcileLifecycleCheckpoint 以 durable run state/counters 为恢复权威。外层
// checkpoint phase、allocator current/frontier/closed phases 与 durable phase
// 必须共同描述一个可重建窗口；validating -> patching 是唯一后退边，allocator
// 保持 validation frontier。durable counters 超出 checkpoint 的增量只补记一次，
// checkpoint 超前则拒绝，避免丢失或重复 consumption。
func reconcileLifecycleCheckpoint(tracker *resilientRunState, snapshot domain.CheckpointSnapshot, run domain.Run) error {
	checkpointPhase, checkpointOK := domain.BudgetPhaseFor(domain.RunState(snapshot.Checkpoint.Phase))
	durableState := run.State
	if durableState == domain.RunStateDiagnosisReadyForReview {
		durableState = domain.RunStatePlanning
	}
	durablePhase, durableOK := domain.BudgetPhaseFor(durableState)
	if !checkpointOK || !durableOK {
		return fmt.Errorf("reconcile lifecycle checkpoint: invalid phase %q -> %q", snapshot.Checkpoint.Phase, run.State)
	}
	needsRebuild := snapshot.NeedsRebuild || snapshot.Checkpoint.ObservedRunVersion < run.Version
	if !needsRebuild && checkpointPhase != durablePhase {
		return fmt.Errorf("reconcile lifecycle checkpoint: phase %q conflicts with durable phase %q", checkpointPhase, durablePhase)
	}
	if tracker.alloc == nil {
		return nil
	}
	current := tracker.alloc.current
	if current != checkpointPhase && !(checkpointPhase == domain.RunStatePatching && current == domain.RunStateValidating) {
		return fmt.Errorf("reconcile lifecycle budget: checkpoint phase %q conflicts with allocator current %q", checkpointPhase, current)
	}
	if tracker.alloc.frontier != domain.BudgetPhaseIndex(current) || tracker.alloc.closed[current] {
		return fmt.Errorf("reconcile lifecycle budget: allocator frontier/closed state conflicts with current phase %q", current)
	}
	checkpointConsumed := tracker.alloc.Projection().Consumed
	durableConsumed := budgetAmountFromCounters(run.Budget)
	// PostgreSQL 对 active run 不写 elapsed_ms；checkpoint 是进程重启时已消耗
	// wall-clock hard budget 的 durable authority。其他维度仍以 run counters 为准。
	if run.Budget.ElapsedSeconds == 0 && checkpointConsumed.ElapsedSeconds > 0 {
		durableConsumed.ElapsedSeconds = checkpointConsumed.ElapsedSeconds
	}
	delta := durableConsumed.Sub(checkpointConsumed)
	if !budgetAmountNonNegative(delta) {
		return fmt.Errorf("reconcile lifecycle budget: checkpoint consumption is ahead of durable counters")
	}
	// transition effect 在 durable state 变更时仍属于 source phase；必须先补
	// checkpoint→run counter delta，再 close/admit target，避免消耗后移到 reserve。
	if !delta.IsZero() {
		if _, err := tracker.alloc.Consume(current, delta); err != nil {
			return fmt.Errorf("reconcile lifecycle budget consumption: %w", err)
		}
	}

	if current != durablePhase {
		if durablePhase == domain.RunStatePatching && current == domain.RunStateValidating &&
			(checkpointPhase == domain.RunStatePatching || (checkpointPhase == domain.RunStateValidating && needsRebuild)) {
			// validation repair keeps the forward allocator frontier.
		} else {
			currentIndex := domain.BudgetPhaseIndex(current)
			durableIndex := domain.BudgetPhaseIndex(durablePhase)
			if currentIndex < 0 || durableIndex < currentIndex {
				return fmt.Errorf("reconcile lifecycle budget: allocator phase %q cannot recover durable phase %q", current, durablePhase)
			}
			for currentIndex < durableIndex {
				if err := tracker.alloc.ClosePhase(tracker.alloc.current); err != nil {
					return fmt.Errorf("reconcile lifecycle budget close phase: %w", err)
				}
				next := domain.BudgetPhaseOrder()[currentIndex+1]
				if err := tracker.alloc.AdmitPhase(next); err != nil {
					return fmt.Errorf("reconcile lifecycle budget admit phase: %w", err)
				}
				currentIndex++
			}
		}
	}
	tracker.lastMirroredElapsed = durableConsumed.ElapsedSeconds
	return nil
}

func budgetAmountFromCounters(counters domain.BudgetCounters) domain.BudgetAmount {
	return domain.BudgetAmount{
		ElapsedSeconds: counters.ElapsedSeconds, ModelCalls: counters.ModelCalls,
		ModelCostCents: counters.ModelCostCents, ToolCalls: counters.ToolCalls,
		EvidenceBytes: counters.EvidenceBytes, RepositoryBytes: counters.RepositoryBytes,
	}
}

func budgetAmountNonNegative(amount domain.BudgetAmount) bool {
	return amount.ElapsedSeconds >= 0 && amount.ModelCalls >= 0 && amount.ModelCostCents >= 0 &&
		amount.ToolCalls >= 0 && amount.EvidenceBytes >= 0 && amount.RepositoryBytes >= 0
}

func maxInt64(value int, fallback int64) int64 {
	if int64(value) < fallback {
		return fallback
	}
	return int64(value)
}

func (c *RemediationCoordinator) driveLifecycle(ctx context.Context, budget *runBudget, run domain.Run, plan domain.RepairPlanCandidate) error {
	tracker := resilientStateFrom(ctx)
	if tracker == nil {
		return ErrLifecycleUnavailable
	}
	switch run.State {
	case domain.RunStatePatching:
		tracker.lifecyclePhase = domain.RunStatePatching
		ready, err := c.ensureLifecycleWorkspace(ctx, budget, run, tracker)
		if err != nil || !ready {
			return err
		}
		return c.runPatching(ctx, budget, run, plan)
	case domain.RunStateValidating:
		tracker.lifecyclePhase = domain.RunStateValidating
		if tracker.workspace == nil {
			ready, err := c.ensureLifecycleWorkspace(ctx, budget, run, tracker)
			if err != nil || !ready {
				return err
			}
		}
		return c.runValidation(ctx, budget, run, plan)
	case domain.RunStatePublishing:
		tracker.lifecyclePhase = domain.RunStatePublishing
		return c.runPublication(ctx, budget, run, plan)
	case domain.RunStateAwaitingHumanReview:
		return nil
	default:
		return fmt.Errorf("%w: unsupported lifecycle state %s", ErrLifecycleNotReady, run.State)
	}
}

func (c *RemediationCoordinator) ensureLifecycleWorkspace(ctx context.Context, budget *runBudget, run domain.Run, tracker *resilientRunState) (bool, error) {
	key := lifecycleEffectKey("workspace", run.RunID, run.DeployedCommit)
	existing, err := c.lifecycleStore.GetLifecycleEffect(ctx, run.RunID, domain.LifecycleEffectWorkspace, key)
	if err == nil {
		switch existing.State {
		case domain.LifecycleEffectSucceeded:
			identity := domain.WorkspaceIdentity{
				WorkspaceID: existing.WorkspaceID, RunID: run.RunID, BaselineCommit: existing.BaselineCommit,
				BaseTreeHash: existing.BaseTreeHash, CurrentTreeHash: existing.ResultTreeHash, Version: maxInt64(existing.Attempt, 1),
			}
			if identity.BaselineCommit != run.DeployedCommit {
				return false, c.fail(ctx, run.RunID, domain.RunStatePatching, markConfigurationFailure(fmt.Errorf("workspace baseline does not match run")))
			}
			if validateErr := identity.Validate(); validateErr != nil {
				return false, c.fail(ctx, run.RunID, domain.RunStatePatching, markPersistenceFailure(validateErr))
			}
			tracker.workspace = &domain.CheckpointWorkspace{WorkspaceID: identity.WorkspaceID, BaselineCommit: identity.BaselineCommit, BaseTreeHash: identity.BaseTreeHash, CurrentTreeHash: identity.CurrentTreeHash, Version: identity.Version}
			return true, nil
		case domain.LifecycleEffectFailed:
			if existing.Attempt >= maxLifecycleRecoveryAttempts {
				return false, c.blockLifecycleForPolicy(ctx, budget, run.RunID, domain.RunStatePatching, "workspace_retry_exhausted")
			}
			return false, c.fail(ctx, run.RunID, domain.RunStatePatching, markConfigurationFailure(fmt.Errorf("workspace.ensure: %s", safeLifecycleErrorCode(existing.ErrorCode))))
		case domain.LifecycleEffectStarted, domain.LifecycleEffectRecoverable:
			if existing.Attempt >= maxLifecycleRecoveryAttempts {
				failed := existing
				failed.State = domain.LifecycleEffectFailed
				if strings.TrimSpace(failed.ErrorCode) == "" {
					failed.ErrorCode = "effect_retry_exhausted"
				}
				if _, saveErr := c.lifecycleStore.UpsertLifecycleEffect(ctx, failed); saveErr != nil {
					return false, c.fail(ctx, run.RunID, domain.RunStatePatching, markPersistenceFailure(saveErr))
				}
				return false, c.blockLifecycleForPolicy(ctx, budget, run.RunID, domain.RunStatePatching, "workspace_retry_exhausted")
			}
		}
	}
	if err != nil && !errors.Is(err, domain.ErrLifecycleEffectNotFound) {
		return false, c.fail(ctx, run.RunID, domain.RunStatePatching, markPersistenceFailure(err))
	}
	attempt := 1
	if err == nil && existing.Attempt > 0 {
		attempt = existing.Attempt + 1
	}
	started := domain.LifecycleEffect{
		RunID: run.RunID, Kind: domain.LifecycleEffectWorkspace, IdempotencyKey: key,
		State: domain.LifecycleEffectStarted, Attempt: attempt, BaselineCommit: run.DeployedCommit,
		Summary: "workspace preparation started",
	}
	if _, err := c.lifecycleStore.UpsertLifecycleEffect(ctx, started); err != nil {
		return false, c.fail(ctx, run.RunID, domain.RunStatePatching, markPersistenceFailure(err))
	}
	if err := c.checkpointRun(ctx, tracker, domain.RunStatePatching, domain.CheckpointReasonPhaseBoundary); err != nil {
		return false, c.fail(ctx, run.RunID, domain.RunStatePatching, markPersistenceFailure(err))
	}
	if exhausted, err := c.recordSameStateBudget(ctx, budget, run.RunID, domain.RunStatePatching, domain.Effect{ToolCalls: 1}); err != nil || exhausted {
		return exhausted, err
	}
	operationCtx, cancel := budget.operationContext(ctx)
	workspaceRequest := domain.WorkspaceRequest{
		RunID: run.RunID, ProjectID: run.ProjectID, BaselineCommit: run.DeployedCommit, IdempotencyKey: key,
		Profile: run.ExecutionProfile, Repository: run.PublicationSnapshot, ChangePolicy: run.ChangePolicySnapshot,
	}
	if err := workspaceRequest.Validate(); err != nil {
		return c.handleLifecycleExternalFailure(ctx, budget, run, tracker, domain.LifecycleEffectWorkspace, key, attempt, &domain.LifecycleRuntimeError{Code: "workspace_invalid", Cause: err}, domain.RunStatePatching, "workspace.ensure")
	}
	identity, callErr := c.workspace.Ensure(operationCtx, workspaceRequest)
	runDeadlineExceeded := runWorkDeadlineExceeded(operationCtx)
	cancel()
	if runDeadlineExceeded {
		return false, c.handleLifecycleDeadline(ctx, budget, run.RunID, domain.RunStatePatching)
	}
	if callErr != nil {
		return c.handleLifecycleExternalFailure(ctx, budget, run, tracker, domain.LifecycleEffectWorkspace, key, attempt, callErr, domain.RunStatePatching, "workspace.ensure")
	}
	if identity.RunID != run.RunID || identity.BaselineCommit != run.DeployedCommit {
		callErr = &domain.LifecycleRuntimeError{Code: "workspace_baseline_mismatch", Retryable: false}
		return c.handleLifecycleExternalFailure(ctx, budget, run, tracker, domain.LifecycleEffectWorkspace, key, attempt, callErr, domain.RunStatePatching, "workspace.ensure")
	}
	if err := identity.Validate(); err != nil {
		return c.handleLifecycleExternalFailure(ctx, budget, run, tracker, domain.LifecycleEffectWorkspace, key, attempt, &domain.LifecycleRuntimeError{Code: "workspace_invalid_response", Cause: err}, domain.RunStatePatching, "workspace.ensure")
	}
	tracker.workspace = &domain.CheckpointWorkspace{WorkspaceID: identity.WorkspaceID, BaselineCommit: identity.BaselineCommit, BaseTreeHash: identity.BaseTreeHash, CurrentTreeHash: identity.CurrentTreeHash, Version: identity.Version}
	if _, err := c.lifecycleStore.UpsertLifecycleEffect(ctx, domain.LifecycleEffect{
		RunID: run.RunID, Kind: domain.LifecycleEffectWorkspace, IdempotencyKey: key,
		State: domain.LifecycleEffectSucceeded, Attempt: attempt, BaselineCommit: identity.BaselineCommit,
		WorkspaceID: identity.WorkspaceID, BaseTreeHash: identity.BaseTreeHash, ResultTreeHash: identity.CurrentTreeHash,
		Summary: "workspace prepared",
	}); err != nil {
		return false, c.fail(ctx, run.RunID, domain.RunStatePatching, markPersistenceFailure(err))
	}
	if err := c.checkpointRun(ctx, tracker, domain.RunStatePatching, domain.CheckpointReasonPhaseBoundary); err != nil {
		return false, c.fail(ctx, run.RunID, domain.RunStatePatching, markPersistenceFailure(err))
	}
	return true, nil
}

func (c *RemediationCoordinator) runPatching(ctx context.Context, budget *runBudget, run domain.Run, plan domain.RepairPlanCandidate) error {
	tracker := resilientStateFrom(ctx)
	if tracker != nil {
		tracker.lifecyclePhase = domain.RunStatePatching
	}
	if tracker == nil || tracker.workspace == nil {
		return ErrLifecycleUnavailable
	}
	// process crash 后，成功的 patch effect 是权威结果；但失败 validation
	// 仍保留在 tracker 中，必须产生真正的新 patch。
	if tracker.validation == nil && hasCheckpointArtifact(tracker.artifacts, "patch") {
		return c.transitionToValidation(ctx, budget, run.RunID)
	}
	if c.lifecycleTools == nil {
		c.lifecycleTools = NewLifecycleToolGateway(c.workspace, c.validation)
	}
	for {
		if exhausted, err := c.admitOperation(ctx, budget, run.RunID, domain.RunStatePatching); err != nil || exhausted {
			return err
		}
		// D2/R13 自动 byte-threshold hybrid trigger（resilient_v1）：patching 的
		// workspace/补丁读取同样受 conversation 字节压力保护；检查点保留工件/树
		// 身份，不内联 patch 正文。失败按既有 contract 转为 persistence_failure。
		if checkpointErr := c.autoThresholdCheckpoint(ctx, tracker, domain.RunStatePatching, tracker.conversation, run.RunID); checkpointErr != nil {
			return checkpointErr
		}
		operationCtx, cancel := budget.operationContext(ctx)
		env, usage, err := c.agentEngine.TurnObservedWithConversationAndTools(
			operationCtx, observationRun(ctx), c.observer, nextObservationSequence(ctx), domain.RunStatePatching,
			run.ProjectID, tracker.conversation.ContextText(), c.lifecycleTools.DefinitionsForPhase(domain.RunStatePatching), tracker.conversation,
		)
		runDeadlineExceeded := operationDeadlineExceeded(operationCtx)
		cancel()
		if runDeadlineExceeded {
			_, transitionErr := c.recordElapsedOperation(ctx, budget, run.RunID, domain.RunStatePatching, modelEffect(usage))
			return transitionErr
		}
		if err != nil {
			done, handleErr := c.handleLifecycleTurnError(ctx, budget, run.RunID, domain.RunStatePatching, usage, err)
			if handleErr != nil || done {
				return handleErr
			}
			continue
		}
		exhausted, err := c.recordSameStateBudget(ctx, budget, run.RunID, domain.RunStatePatching, modelEffect(usage))
		if err != nil || exhausted {
			return err
		}
		switch env.Kind {
		case "requestTool":
			tracker.resetLifecycleProtocolNoProgress()
			for index := range env.RequestedTools() {
				requests := env.RequestedTools()
				exhausted, err = c.runLifecycleTool(ctx, budget, run, domain.RunStatePatching, tracker, &requests[index], "patch")
				if err != nil || exhausted {
					return err
				}
			}
		case "patchComplete":
			tracker.resetLifecycleProtocolNoProgress()
			if !env.PatchComplete.Success || tracker.validation != nil || !hasCheckpointArtifact(tracker.artifacts, "patch") {
				if err := c.appendLifecycleChallenge(ctx, domain.RecoveryChallengeKindValidationRevision, "patch_completion_not_ready", "patchComplete", []string{"workspace"}, []string{"inspect_workspace", "apply_changed_patch"}, "The patch is not ready for validation. Inspect the bounded workspace result and apply a changed patch before confirming completion."); err != nil {
					return err
				}
				continue
			}
			return c.transitionToValidation(ctx, budget, run.RunID)
		case "stop":
			done, err := c.handleLifecycleStop(ctx, budget, run.RunID, domain.RunStatePatching, env.Stop)
			if err != nil || done {
				return err
			}
		default:
			tracker.recordLifecycleFailure("agentEnvelope\x00unexpected_patch_envelope")
			if tracker.lifecycleRecoveryAttempts >= maxLifecycleRecoveryAttempts {
				return c.blockLifecycleForPolicy(ctx, budget, run.RunID, domain.RunStatePatching, "patch_protocol_no_progress")
			}
			if err := c.appendLifecycleChallenge(ctx, domain.RecoveryChallengeKindProtocolCorrection, "unexpected_patch_envelope", "agentEnvelope", []string{"workspace"}, []string{"correct_request"}, "Return a patchComplete envelope after a successful workspace patch, or request one advertised workspace tool."); err != nil {
				return err
			}
		}
	}
}

func (c *RemediationCoordinator) transitionToValidation(ctx context.Context, budget *runBudget, runID string) error {
	run, err := c.store.Get(ctx, runID)
	if err != nil {
		return fmt.Errorf("load run after patching: %w", err)
	}
	plan, err := selectRepairPlan(run, resilientStateFrom(ctx).lifecyclePlanID)
	if err != nil {
		return err
	}
	if !localValidationEnabled(run.Run.ExecutionProfile) {
		return c.transitionToPublication(ctx, budget, runID, plan)
	}
	if exhausted, err := c.transitionBudgeted(ctx, budget, runID, domain.RunStatePatching, domain.RunStateValidating, domain.Effect{}); err != nil || exhausted {
		return err
	}
	tracker := resilientStateFrom(ctx)
	if err := c.checkpointRun(ctx, tracker, domain.RunStateValidating, domain.CheckpointReasonPhaseBoundary); err != nil {
		return c.fail(ctx, runID, domain.RunStateValidating, markPersistenceFailure(err))
	}
	return c.runValidation(ctx, budget, run.Run, plan)
}

func (c *RemediationCoordinator) runValidation(ctx context.Context, budget *runBudget, run domain.Run, plan domain.RepairPlanCandidate) error {
	tracker := resilientStateFrom(ctx)
	if tracker != nil {
		tracker.lifecyclePhase = domain.RunStateValidating
	}
	if tracker == nil || tracker.workspace == nil {
		return ErrLifecycleUnavailable
	}
	if tracker.validation != nil && tracker.validation.Passed {
		return c.transitionToPublication(ctx, budget, run.RunID, plan)
	}
	if c.lifecycleTools == nil {
		c.lifecycleTools = NewLifecycleToolGateway(c.workspace, c.validation)
	}
	for {
		if exhausted, err := c.admitOperation(ctx, budget, run.RunID, domain.RunStateValidating); err != nil || exhausted {
			return err
		}
		// D2/R13 自动 byte-threshold hybrid trigger（resilient_v1），见 patching
		// 循环同位置；validation 阶段只读 bounded 结果引用，压力账本同样适用。
		if checkpointErr := c.autoThresholdCheckpoint(ctx, tracker, domain.RunStateValidating, tracker.conversation, run.RunID); checkpointErr != nil {
			return checkpointErr
		}
		operationCtx, cancel := budget.operationContext(ctx)
		env, usage, err := c.agentEngine.TurnObservedWithConversationAndTools(
			operationCtx, observationRun(ctx), c.observer, nextObservationSequence(ctx), domain.RunStateValidating,
			run.ProjectID, tracker.conversation.ContextText(), c.lifecycleTools.DefinitionsForPhase(domain.RunStateValidating), tracker.conversation,
		)
		runDeadlineExceeded := operationDeadlineExceeded(operationCtx)
		cancel()
		if runDeadlineExceeded {
			_, transitionErr := c.recordElapsedOperation(ctx, budget, run.RunID, domain.RunStateValidating, modelEffect(usage))
			return transitionErr
		}
		if err != nil {
			done, handleErr := c.handleLifecycleTurnError(ctx, budget, run.RunID, domain.RunStateValidating, usage, err)
			if handleErr != nil || done {
				return handleErr
			}
			continue
		}
		exhausted, err := c.recordSameStateBudget(ctx, budget, run.RunID, domain.RunStateValidating, modelEffect(usage))
		if err != nil || exhausted {
			return err
		}
		switch env.Kind {
		case "requestTool":
			tracker.resetLifecycleProtocolNoProgress()
			requests := env.RequestedTools()
			for index := range requests {
				exhausted, err = c.runLifecycleTool(ctx, budget, run, domain.RunStateValidating, tracker, &requests[index], "validation")
				if err != nil || exhausted {
					return err
				}
			}
		case "validationAssessment":
			tracker.resetLifecycleProtocolNoProgress()
			if tracker.validation == nil {
				if err := c.appendLifecycleChallenge(ctx, domain.RecoveryChallengeKindValidationRevision, "validation_result_required", "validationAssessment", []string{"validation"}, []string{"run_approved_validation"}, "The service has no authoritative validation result for this assessment. Run one approved validation command and assess its bounded result."); err != nil {
					return err
				}
				continue
			}
			if tracker.validation.Passed {
				return c.transitionToPublication(ctx, budget, run.RunID, plan)
			}
			if validationResultsPassButIncomplete(tracker.validation) {
				if err := c.appendLifecycleChallenge(ctx, domain.RecoveryChallengeKindValidationRevision, "validation_commands_incomplete", "validationAssessment", []string{"validation"}, []string{"run_remaining_validation"}, "Run every required validation command on the current tree before publication."); err != nil {
					return err
				}
				continue
			}
			return c.reviseAfterValidationFailure(ctx, budget, run, plan)
		case "stop":
			done, err := c.handleLifecycleStop(ctx, budget, run.RunID, domain.RunStateValidating, env.Stop)
			if err != nil || done {
				return err
			}
		default:
			tracker.recordLifecycleFailure("agentEnvelope\x00unexpected_validation_envelope")
			if tracker.lifecycleRecoveryAttempts >= maxLifecycleRecoveryAttempts {
				return c.blockLifecycleForPolicy(ctx, budget, run.RunID, domain.RunStateValidating, "validation_protocol_no_progress")
			}
			if err := c.appendLifecycleChallenge(ctx, domain.RecoveryChallengeKindProtocolCorrection, "unexpected_validation_envelope", "agentEnvelope", []string{"workspace", "validation"}, []string{"correct_request"}, "Return a validationAssessment envelope after an approved validation result, or request one advertised workspace tool."); err != nil {
				return err
			}
		}
	}
}

func (c *RemediationCoordinator) transitionToPublication(ctx context.Context, budget *runBudget, runID string, plan domain.RepairPlanCandidate) error {
	from := domain.RunStateValidating
	if tracker := resilientStateFrom(ctx); tracker != nil && tracker.lifecyclePhase == domain.RunStatePatching {
		from = domain.RunStatePatching
	}
	if exhausted, err := c.transitionBudgeted(ctx, budget, runID, from, domain.RunStatePublishing, domain.Effect{}); err != nil || exhausted {
		return err
	}
	tracker := resilientStateFrom(ctx)
	if err := c.checkpointRun(ctx, tracker, domain.RunStatePublishing, domain.CheckpointReasonPhaseBoundary); err != nil {
		return c.fail(ctx, runID, domain.RunStatePublishing, markPersistenceFailure(err))
	}
	run, err := c.store.Get(ctx, runID)
	if err != nil {
		return fmt.Errorf("load publishing run: %w", err)
	}
	return c.runPublication(ctx, budget, run.Run, plan)
}

func (c *RemediationCoordinator) reviseAfterValidationFailure(ctx context.Context, budget *runBudget, run domain.Run, plan domain.RepairPlanCandidate) error {
	tracker := resilientStateFrom(ctx)
	if tracker == nil || tracker.validation == nil {
		return ErrLifecycleUnavailable
	}
	fingerprint := recoveryFingerprint(tracker.validation.CommandID + "\x00" + tracker.validation.OutputHash)
	if fingerprint == tracker.lastValidationFingerprint {
		tracker.validationNoProgress++
	} else {
		tracker.lastValidationFingerprint = fingerprint
		tracker.validationNoProgress = 1
	}
	if tracker.validationNoProgress >= maxLifecycleRecoveryAttempts {
		return c.blockLifecycleForPolicy(ctx, budget, run.RunID, domain.RunStateValidating, "validation_no_progress")
	}
	if err := c.appendLifecycleChallenge(ctx, domain.RecoveryChallengeKindValidationRevision, "validation_failed", "workspace.run_validation", []string{"workspace", "validation"}, []string{"revise_patch", "rerun_validation"}, "The approved validation command failed. Inspect its bounded artifact, change the patch in the isolated workspace, and rerun validation; publication remains blocked until the service records a passing result."); err != nil {
		return err
	}
	if exhausted, err := c.transitionBudgeted(ctx, budget, run.RunID, domain.RunStateValidating, domain.RunStatePatching, domain.Effect{}); err != nil || exhausted {
		return err
	}
	if err := c.checkpointRun(ctx, tracker, domain.RunStatePatching, domain.CheckpointReasonPhaseBoundary); err != nil {
		return c.fail(ctx, run.RunID, domain.RunStatePatching, markPersistenceFailure(err))
	}
	return c.runPatching(ctx, budget, run, plan)
}

func (c *RemediationCoordinator) runPublication(ctx context.Context, budget *runBudget, run domain.Run, plan domain.RepairPlanCandidate) error {
	tracker := resilientStateFrom(ctx)
	if tracker != nil {
		tracker.lifecyclePhase = domain.RunStatePublishing
	}
	if tracker == nil || tracker.workspace == nil || (localValidationEnabled(run.ExecutionProfile) && (tracker.validation == nil || !tracker.validation.Passed)) {
		return c.blockLifecycleForPolicy(ctx, budget, run.RunID, domain.RunStatePublishing, "publication_validation_required")
	}
	patchArtifact, ok := latestCheckpointArtifact(tracker.artifacts, "patch")
	if !ok {
		return c.blockLifecycleForPolicy(ctx, budget, run.RunID, domain.RunStatePublishing, "publication_patch_artifact_required")
	}
	if tracker.publicationPolicy == nil {
		tracker.publicationPolicy = checkpointPublicationPolicy(c.publicationPolicy)
		if err := c.checkpointRun(ctx, tracker, domain.RunStatePublishing, domain.CheckpointReasonPhaseBoundary); err != nil {
			return c.fail(ctx, run.RunID, domain.RunStatePublishing, markPersistenceFailure(err))
		}
	}
	policy := c.publicationPolicy
	if tracker.publicationPolicy != nil {
		policy = LifecyclePublicationPolicy{TargetBranch: tracker.publicationPolicy.TargetBranch, BranchPrefix: tracker.publicationPolicy.BranchPrefix}
	}
	if policy.TargetBranch == "" {
		return c.blockLifecycleForPolicy(ctx, budget, run.RunID, domain.RunStatePublishing, "publication_target_required")
	}
	if policy.BranchPrefix == "" {
		policy.BranchPrefix = defaultPublicationPrefix
	}
	branchRef := strings.TrimSuffix(policy.BranchPrefix, "/") + "/" + run.IncidentID + "/" + run.RunID
	publicationEffectKind := domain.LifecycleEffectPublication
	publicationEffectName := "publication"
	if c.gitPublication != nil {
		publicationEffectKind = domain.LifecycleEffectGitPublish
		publicationEffectName = "git_publish"
	}
	key := lifecycleEffectKey(publicationEffectName, run.RunID, patchArtifact.ContentHash+"\x00"+tracker.workspace.CurrentTreeHash)
	existing, err := c.lifecycleStore.GetLifecycleEffect(ctx, run.RunID, publicationEffectKind, key)
	if err == nil {
		switch existing.State {
		case domain.LifecycleEffectSucceeded:
			targetDiverged := hasTargetDivergence(existing.Summary)
			tracker.publication = &domain.CheckpointPublication{
				BranchRef: existing.BranchRef, TargetBranch: existing.TargetBranch, CommitHash: existing.CommitHash,
				DraftChangeRef: existing.DraftChangeRef, CompareURL: existing.CompareURL,
				HumanReviewOnly: true, TargetDiverged: targetDiverged,
			}
			if publicationEffectKind == domain.LifecycleEffectGitPublish {
				return c.runChangeRequest(ctx, budget, run, tracker, domain.PublicationResult{
					BranchRef: existing.BranchRef, TargetBranch: existing.TargetBranch,
					CommitHash: existing.CommitHash, BaselineCommit: existing.BaselineCommit,
					HumanReviewRequired: true, AlreadyPublished: true, TargetDiverged: targetDiverged,
					Summary: existing.Summary,
				}, plan)
			}
			return c.finishPublication(ctx, budget, run.RunID)
		case domain.LifecycleEffectFailed:
			if existing.Attempt >= maxLifecycleRecoveryAttempts {
				return c.blockLifecycleForPolicy(ctx, budget, run.RunID, domain.RunStatePublishing, "publication_retry_exhausted")
			}
			return c.blockLifecycleForPolicy(ctx, budget, run.RunID, domain.RunStatePublishing, existing.ErrorCode)
		case domain.LifecycleEffectStarted, domain.LifecycleEffectRecoverable:
			if existing.Attempt >= maxLifecycleRecoveryAttempts {
				failed := existing
				failed.State = domain.LifecycleEffectFailed
				if strings.TrimSpace(failed.ErrorCode) == "" {
					failed.ErrorCode = "effect_retry_exhausted"
				}
				if _, saveErr := c.lifecycleStore.UpsertLifecycleEffect(ctx, failed); saveErr != nil {
					return c.fail(ctx, run.RunID, domain.RunStatePublishing, markPersistenceFailure(saveErr))
				}
				return c.blockLifecycleForPolicy(ctx, budget, run.RunID, domain.RunStatePublishing, "publication_retry_exhausted")
			}
		}
	}
	if err != nil && !errors.Is(err, domain.ErrLifecycleEffectNotFound) {
		return c.fail(ctx, run.RunID, domain.RunStatePublishing, markPersistenceFailure(err))
	}
	if err == nil {
		if existing.TargetBranch != "" {
			policy.TargetBranch = existing.TargetBranch
		}
		if existing.BranchRef != "" {
			branchRef = existing.BranchRef
		}
	}
	attempt := 1
	if err == nil && existing.Attempt > 0 {
		attempt = existing.Attempt + 1
	}
	started := domain.LifecycleEffect{
		RunID: run.RunID, Kind: publicationEffectKind, IdempotencyKey: key,
		State: domain.LifecycleEffectStarted, Attempt: attempt, BaselineCommit: run.DeployedCommit,
		WorkspaceID: tracker.workspace.WorkspaceID, ResultTreeHash: tracker.workspace.CurrentTreeHash,
		ArtifactRef: patchArtifact.Reference, ContentHash: patchArtifact.ContentHash,
		BranchRef: branchRef, TargetBranch: policy.TargetBranch, Summary: "publication started",
	}
	if _, err := c.lifecycleStore.UpsertLifecycleEffect(ctx, started); err != nil {
		return c.fail(ctx, run.RunID, domain.RunStatePublishing, markPersistenceFailure(err))
	}
	if err := c.checkpointRun(ctx, tracker, domain.RunStatePublishing, domain.CheckpointReasonPhaseBoundary); err != nil {
		return c.fail(ctx, run.RunID, domain.RunStatePublishing, markPersistenceFailure(err))
	}
	if exhausted, err := c.recordSameStateBudget(ctx, budget, run.RunID, domain.RunStatePublishing, domain.Effect{ToolCalls: 1}); err != nil || exhausted {
		return err
	}
	operationCtx, cancel := budget.operationContext(ctx)
	publicationRequest := domain.PublicationRequest{
		RunID: run.RunID, ProjectID: run.ProjectID, BaselineCommit: run.DeployedCommit,
		TargetBranch: policy.TargetBranch, BranchRef: branchRef,
		CommitMessage:    "fix(remediation): apply " + boundedContinuationText(plan.PlanID, 96),
		PatchArtifactRef: patchArtifact.Reference, PatchContentHash: patchArtifact.ContentHash,
		ExpectedTreeHash: tracker.workspace.CurrentTreeHash, IdempotencyKey: key,
		Repository: run.PublicationSnapshot, ChangePolicy: run.ChangePolicySnapshot,
		WorkspaceLimitMiB: run.ExecutionProfile.WorkspaceLimitMiB,
	}
	if err := publicationRequest.Validate(); err != nil {
		return c.handlePublicationFailure(ctx, budget, run, tracker, publicationEffectKind, key, attempt, &domain.LifecycleRuntimeError{Code: "publication_invalid_response", Retryable: false, Cause: err})
	}
	gitResult := domain.GitPublicationResult{}
	var result domain.PublicationResult
	var callErr error
	if c.gitPublication != nil {
		gitRequest := domain.GitPublicationRequest{
			PublicationRequest: publicationRequest,
			AuthoredAt:         run.CreatedAt.UTC().Format(time.RFC3339),
		}
		gitResult, callErr = c.gitPublication.PublishGit(operationCtx, gitRequest)
		if callErr == nil && (!gitResult.RemoteVerified || gitResult.ExpectedTreeHash != publicationRequest.ExpectedTreeHash) {
			callErr = &domain.LifecycleRuntimeError{Code: "publication_tree_mismatch", Retryable: false}
		}
		result = domain.PublicationResult{
			BranchRef: gitResult.BranchRef, CommitHash: gitResult.CommitHash,
			BaselineCommit: gitResult.BaselineCommit, TargetBranch: gitResult.TargetBranch,
			HumanReviewRequired: true, AlreadyPublished: gitResult.AlreadyPublished,
			TargetDiverged: gitResult.TargetDiverged, Summary: gitResult.Summary,
		}
	} else {
		result, callErr = c.publication.Publish(operationCtx, publicationRequest)
	}
	runDeadlineExceeded := runWorkDeadlineExceeded(operationCtx)
	cancel()
	if runDeadlineExceeded {
		return c.handleLifecycleDeadline(ctx, budget, run.RunID, domain.RunStatePublishing)
	}
	if callErr != nil {
		return c.handlePublicationFailure(ctx, budget, run, tracker, publicationEffectKind, key, attempt, callErr)
	}
	if err := result.Validate(); err != nil {
		return c.handlePublicationFailure(ctx, budget, run, tracker, publicationEffectKind, key, attempt, &domain.LifecycleRuntimeError{Code: "publication_invalid_response", Retryable: false, Cause: err})
	}
	if result.BaselineCommit != run.DeployedCommit || result.TargetBranch != policy.TargetBranch || result.BranchRef != branchRef {
		return c.handlePublicationFailure(ctx, budget, run, tracker, publicationEffectKind, key, attempt, &domain.LifecycleRuntimeError{Code: "publication_baseline_mismatch", Retryable: false})
	}
	tracker.publication = &domain.CheckpointPublication{
		BranchRef: result.BranchRef, TargetBranch: result.TargetBranch, CommitHash: result.CommitHash,
		DraftChangeRef: result.DraftChangeRef, CompareURL: result.CompareURL,
		HumanReviewOnly: true, TargetDiverged: result.TargetDiverged,
	}
	publicationSummary := strings.TrimSpace(result.Summary)
	if publicationSummary == "" {
		publicationSummary = "branch published; human review required"
	}
	publicationSummary = targetDivergenceSummary(result.TargetDiverged, publicationSummary)
	if _, err := c.lifecycleStore.UpsertLifecycleEffect(ctx, domain.LifecycleEffect{
		RunID: run.RunID, Kind: publicationEffectKind, IdempotencyKey: key,
		State: domain.LifecycleEffectSucceeded, Attempt: attempt, BaselineCommit: run.DeployedCommit,
		WorkspaceID: tracker.workspace.WorkspaceID, ResultTreeHash: tracker.workspace.CurrentTreeHash,
		ArtifactRef: patchArtifact.Reference, ContentHash: patchArtifact.ContentHash,
		BranchRef: result.BranchRef, TargetBranch: result.TargetBranch, CommitHash: result.CommitHash, DraftChangeRef: result.DraftChangeRef,
		CompareURL: result.CompareURL, Summary: publicationSummary,
	}); err != nil {
		return c.fail(ctx, run.RunID, domain.RunStatePublishing, markPersistenceFailure(err))
	}
	if err := c.checkpointRun(ctx, tracker, domain.RunStatePublishing, domain.CheckpointReasonPhaseBoundary); err != nil {
		return c.fail(ctx, run.RunID, domain.RunStatePublishing, markPersistenceFailure(err))
	}
	if publicationEffectKind == domain.LifecycleEffectGitPublish {
		return c.runChangeRequest(ctx, budget, run, tracker, result, plan)
	}
	return c.finishPublication(ctx, budget, run.RunID)
}

func (c *RemediationCoordinator) runChangeRequest(ctx context.Context, budget *runBudget, run domain.Run, tracker *resilientRunState, publication domain.PublicationResult, plan domain.RepairPlanCandidate) error {
	if c.changeRequests == nil {
		return c.finishPublication(ctx, budget, run.RunID)
	}
	capabilities, err := c.changeRequests.ProbeCapabilities(ctx, run.ProjectID, run.PublicationSnapshot)
	if err != nil {
		return c.handlePublicationFailure(ctx, budget, run, tracker, domain.LifecycleEffectChangeRequest,
			lifecycleEffectKey("change_request", run.RunID, publication.CommitHash), 1, err)
	}
	if capabilities.Status == domain.CapabilityUnsupported || (capabilities.Status == domain.CapabilitySupported && !capabilities.DraftSupported) {
		return c.finishPublication(ctx, budget, run.RunID)
	}
	if capabilities.Status != domain.CapabilitySupported {
		return c.blockLifecycleForPolicy(ctx, budget, run.RunID, domain.RunStatePublishing, "change_request_capability_unknown")
	}
	lookup := domain.ChangeRequestLookup{
		ProjectID: run.ProjectID, RepositoryID: capabilities.RepositoryID,
		SourceBranch: publication.BranchRef, TargetBranch: publication.TargetBranch,
		PublicationSnapshot: run.PublicationSnapshot,
	}
	change, err := c.changeRequests.FindChangeRequest(ctx, lookup)
	if err == nil && change.Reference == "" {
		change, err = c.changeRequests.CreateChangeRequest(ctx, domain.ChangeRequestRequest{
			ChangeRequestLookup: lookup,
			Title:               "Hotfix for incident " + run.IncidentID,
			Body:                "Incident: " + run.IncidentID + "\n\nBaseline: " + run.DeployedCommit + "\n\nPlan: " + plan.PlanID,
			Draft:               capabilities.DraftSupported, CommitHash: publication.CommitHash,
			IdempotencyKey: lifecycleEffectKey("change_request", run.RunID, publication.CommitHash),
		})
	}
	key := lifecycleEffectKey("change_request", run.RunID, publication.CommitHash)
	if err != nil {
		return c.handlePublicationFailure(ctx, budget, run, tracker, domain.LifecycleEffectChangeRequest, key, 1, err)
	}
	tracker.publication.DraftChangeRef = change.Reference
	tracker.publication.CompareURL = change.URL
	if _, err := c.lifecycleStore.UpsertLifecycleEffect(ctx, domain.LifecycleEffect{
		RunID: run.RunID, Kind: domain.LifecycleEffectChangeRequest, IdempotencyKey: key,
		State: domain.LifecycleEffectSucceeded, Attempt: 1, BaselineCommit: run.DeployedCommit,
		BranchRef: publication.BranchRef, TargetBranch: publication.TargetBranch, CommitHash: publication.CommitHash,
		DraftChangeRef: change.Reference, CompareURL: change.URL, Summary: "change request ready for human review",
	}); err != nil {
		return c.fail(ctx, run.RunID, domain.RunStatePublishing, markPersistenceFailure(err))
	}
	if err := c.checkpointRun(ctx, tracker, domain.RunStatePublishing, domain.CheckpointReasonPhaseBoundary); err != nil {
		return c.fail(ctx, run.RunID, domain.RunStatePublishing, markPersistenceFailure(err))
	}
	return c.finishPublication(ctx, budget, run.RunID)
}

func (c *RemediationCoordinator) finishPublication(ctx context.Context, budget *runBudget, runID string) error {
	if exhausted, err := c.transitionBudgeted(ctx, budget, runID, domain.RunStatePublishing, domain.RunStateAwaitingHumanReview, domain.Effect{TerminalReason: "awaiting_human_review"}); err != nil || exhausted {
		return err
	}
	return c.notifyTerminal(ctx, runID, domain.RunStateAwaitingHumanReview, domain.FixabilityCodeFixable)
}

func (c *RemediationCoordinator) handlePublicationFailure(ctx context.Context, budget *runBudget, run domain.Run, tracker *resilientRunState, kind domain.LifecycleEffectKind, key string, attempt int, cause error) error {
	code, retryable := lifecycleErrorInfo(cause)
	state := domain.LifecycleEffectRecoverable
	if !retryable || attempt >= maxLifecycleRecoveryAttempts {
		state = domain.LifecycleEffectFailed
	}
	projection := domain.LifecycleEffect{
		RunID: run.RunID, Kind: kind, IdempotencyKey: key,
		State: state, Attempt: attempt, BaselineCommit: run.DeployedCommit,
		WorkspaceID: tracker.workspace.WorkspaceID, TargetBranch: publicationTargetBranch(tracker, c.publicationPolicy),
		ErrorCode: code, Summary: lifecycleSafeMessage(code),
	}
	if previous, getErr := c.lifecycleStore.GetLifecycleEffect(ctx, run.RunID, kind, key); getErr == nil {
		projection.BranchRef = previous.BranchRef
		projection.TargetBranch = previous.TargetBranch
		projection.CommitHash = previous.CommitHash
		projection.DraftChangeRef = previous.DraftChangeRef
		projection.CompareURL = previous.CompareURL
		projection.ResultTreeHash = previous.ResultTreeHash
		projection.ArtifactRef = previous.ArtifactRef
		projection.ContentHash = previous.ContentHash
		if hasTargetDivergence(previous.Summary) {
			projection.Summary = targetDivergenceSummary(true, lifecycleSafeMessage(code))
		}
	} else if !errors.Is(getErr, domain.ErrLifecycleEffectNotFound) {
		return c.fail(ctx, run.RunID, domain.RunStatePublishing, markPersistenceFailure(getErr))
	}
	if _, err := c.lifecycleStore.UpsertLifecycleEffect(ctx, projection); err != nil {
		return c.fail(ctx, run.RunID, domain.RunStatePublishing, markPersistenceFailure(err))
	}
	if !retryable {
		return c.blockLifecycleForPolicy(ctx, budget, run.RunID, domain.RunStatePublishing, code)
	}
	if attempt >= maxLifecycleRecoveryAttempts {
		return c.blockLifecycleForPolicy(ctx, budget, run.RunID, domain.RunStatePublishing, "publication_retry_exhausted")
	}
	if err := c.appendLifecycleChallenge(ctx, domain.RecoveryChallengeKindPublicationRetry, code, "publication", []string{"publication"}, []string{"retry_publication"}, "Publication did not complete. The same idempotency key is retained; retry after the bounded SCM failure is available, and the human merge gate remains mandatory."); err != nil {
		return err
	}
	return nil
}

func (c *RemediationCoordinator) handleLifecycleExternalFailure(
	ctx context.Context,
	budget *runBudget,
	run domain.Run,
	tracker *resilientRunState,
	kind domain.LifecycleEffectKind,
	key string,
	attempt int,
	cause error,
	phase domain.RunState,
	action string,
) (bool, error) {
	code, retryable := lifecycleErrorInfo(cause)
	state := domain.LifecycleEffectRecoverable
	if !retryable || attempt >= maxLifecycleRecoveryAttempts {
		state = domain.LifecycleEffectFailed
	}
	if _, err := c.lifecycleStore.UpsertLifecycleEffect(ctx, domain.LifecycleEffect{
		RunID: run.RunID, Kind: kind, IdempotencyKey: key, State: state, Attempt: attempt,
		BaselineCommit: run.DeployedCommit, ErrorCode: code, Summary: lifecycleSafeMessage(code),
	}); err != nil {
		return false, c.fail(ctx, run.RunID, phase, markPersistenceFailure(err))
	}
	if !retryable {
		return false, c.fail(ctx, run.RunID, phase, markConfigurationFailure(fmt.Errorf("%s: %s", action, code)))
	}
	if attempt >= maxLifecycleRecoveryAttempts {
		return false, c.blockLifecycleForPolicy(ctx, budget, run.RunID, phase, string(kind)+"_retry_exhausted")
	}
	if err := c.appendLifecycleChallenge(ctx, domain.RecoveryChallengeKindToolFailure, code, action, lifecycleCapabilitiesForPhase(phase), []string{"retry_transient", "use_fallback"}, "A bounded lifecycle tool failed. Retry the same idempotent operation while available, or choose another advertised recovery path; the run remains active."); err != nil {
		return false, err
	}
	_ = tracker
	_ = budget
	return false, nil
}

func (c *RemediationCoordinator) runLifecycleTool(ctx context.Context, budget *runBudget, run domain.Run, phase domain.RunState, tracker *resilientRunState, req *RequestTool, kind string) (bool, error) {
	if tracker == nil || tracker.workspace == nil || req == nil {
		return false, ErrLifecycleUnavailable
	}
	tool := req.ToolName
	if rejection := lifecycleRequestRejection(phase, tracker.validationCommands, tool, req.Parameters); rejection != nil {
		return c.handleLifecycleToolRejection(ctx, budget, run, phase, tracker, req, rejection)
	}
	key := lifecycleEffectKey(kind, run.RunID, tool+"\x00"+tracker.workspace.CurrentTreeHash+"\x00"+requestValue(req.Parameters, "patch")+"\x00"+requestValue(req.Parameters, "commandId"))
	var cached *domain.LifecycleEffect
	effectAttempt := 1
	if effectKind, ok := lifecycleEffectKindForTool(tool); ok {
		effect, err := c.lifecycleStore.GetLifecycleEffect(ctx, run.RunID, effectKind, key)
		if err == nil {
			switch effect.State {
			case domain.LifecycleEffectSucceeded:
				cached = &effect
				effectAttempt = effect.Attempt
				if effectAttempt < 1 {
					effectAttempt = 1
				}
			case domain.LifecycleEffectFailed:
				return c.handleDurableFailedLifecycleTool(ctx, budget, run, phase, tracker, req, effect)
			case domain.LifecycleEffectStarted, domain.LifecycleEffectRecoverable:
				if effect.Attempt >= maxLifecycleRecoveryAttempts {
					failed := effect
					failed.State = domain.LifecycleEffectFailed
					if strings.TrimSpace(failed.ErrorCode) == "" {
						failed.ErrorCode = "effect_retry_exhausted"
					}
					if _, saveErr := c.lifecycleStore.UpsertLifecycleEffect(ctx, failed); saveErr != nil {
						return false, c.fail(ctx, run.RunID, phase, markPersistenceFailure(saveErr))
					}
					return c.handleDurableFailedLifecycleTool(ctx, budget, run, phase, tracker, req, failed)
				}
			}
		} else if !errors.Is(err, domain.ErrLifecycleEffectNotFound) {
			return false, c.fail(ctx, run.RunID, phase, markPersistenceFailure(err))
		}
		if cached == nil {
			if err == nil && effect.Attempt > 0 {
				effectAttempt = effect.Attempt + 1
			}
			if _, err := c.lifecycleStore.UpsertLifecycleEffect(ctx, lifecycleToolStartedEffect(run, phase, req, effectKind, key, effectAttempt, tracker)); err != nil {
				return false, c.fail(ctx, run.RunID, phase, markPersistenceFailure(err))
			}
			if err := c.checkpointRun(ctx, tracker, phase, domain.CheckpointReasonPhaseBoundary); err != nil {
				return false, c.fail(ctx, run.RunID, phase, markPersistenceFailure(err))
			}
		}
	}

	var result ToolResult
	var callErr error
	runDeadlineExceeded := false
	if cached != nil {
		result = lifecycleToolResultFromEffect(tool, *cached, tracker.workspace.CurrentTreeHash)
	} else {
		operationCtx, cancel := budget.operationContext(ctx)
		workspace := checkpointWorkspaceIdentity(*tracker.workspace)
		workspace.RunID = run.RunID
		result, callErr = c.lifecycleTools.Execute(
			operationCtx, phase, workspace, tracker.validationCommands,
			run.ExecutionProfile, run.ChangePolicySnapshot, key, tool, req.Parameters,
		)
		runDeadlineExceeded = runWorkDeadlineExceeded(operationCtx)
		cancel()
		if runDeadlineExceeded {
			// 先走统一 invocation/effect persistence，再由硬 elapsed ceiling
			// terminalize；外部 adapter 仍使用同一幂等 key 供后续人工核查。
			callErr = context.DeadlineExceeded
		}
	}

	invocation := domain.ToolInvocation{ToolName: tool, Phase: phase, BytesRetrieved: result.BytesRetrieved}
	if callErr != nil {
		safeCode, _ := lifecycleErrorInfo(callErr)
		invocation.Error = safeCode
	} else {
		invocation.ResultSummary = sanitizeReviewText(result.Summary)
	}
	invocation.EvidenceIDs = append([]string(nil), result.EvidenceIDs...)
	invocation.InvocationID = tracker.recordToolAction(tool, result.EvidenceIDs)
	if err := c.store.RecordToolInvocation(ctx, run.RunID, invocation); err != nil {
		return false, c.fail(ctx, run.RunID, phase, markPersistenceFailure(fmt.Errorf("record lifecycle tool invocation: %w", err)))
	}
	if callErr != nil {
		tracker.conversation.AppendToolResult(*req, result, callErr)
		code, retryable := lifecycleErrorInfo(callErr)
		if effectKind, ok := lifecycleEffectKindForTool(tool); ok {
			effect, getErr := c.lifecycleStore.GetLifecycleEffect(ctx, run.RunID, effectKind, key)
			attempt := 1
			if getErr == nil && effect.Attempt > 0 {
				attempt = effect.Attempt
			}
			state := domain.LifecycleEffectRecoverable
			if !retryable || attempt >= maxLifecycleRecoveryAttempts {
				state = domain.LifecycleEffectFailed
			}
			if _, saveErr := c.lifecycleStore.UpsertLifecycleEffect(ctx, domain.LifecycleEffect{
				RunID: run.RunID, Kind: effectKind, IdempotencyKey: key, State: state, Attempt: attempt,
				BaselineCommit: run.DeployedCommit, WorkspaceID: tracker.workspace.WorkspaceID,
				ErrorCode: code, Summary: lifecycleSafeMessage(code),
			}); saveErr != nil {
				return false, c.fail(ctx, run.RunID, phase, markPersistenceFailure(saveErr))
			}
		}
		c.observeLifecycleTool(ctx, phase, req, result, callErr)
		if exhausted, budgetErr := c.recordSameStateBudget(ctx, budget, run.RunID, phase, lifecycleToolEffect(result, tool)); budgetErr != nil || exhausted {
			return exhausted, budgetErr
		}
		if runDeadlineExceeded {
			_, budgetErr := c.recordElapsedOperation(ctx, budget, run.RunID, phase, domain.Effect{})
			return true, budgetErr
		}
		if !retryable && !isPatchCorrectionTool(tool) {
			return false, c.fail(ctx, run.RunID, phase, markConfigurationFailure(fmt.Errorf("lifecycle tool %s: %s", tool, code)))
		}
		tracker.recordLifecycleFailure(tool + "\x00" + key + "\x00" + code)
		if tracker.lifecycleRecoveryAttempts >= maxLifecycleRecoveryAttempts {
			return true, c.blockLifecycleForPolicy(ctx, budget, run.RunID, phase, "lifecycle_tool_no_progress")
		}
		if err := c.appendLifecycleChallenge(ctx, domain.RecoveryChallengeKindToolFailure, code, tool, lifecycleCapabilitiesForPhase(phase), []string{"correct_request", "retry_transient", "use_fallback"}, lifecycleSafeMessage(code)); err != nil {
			return false, err
		}
		return false, nil
	}

	tracker.resetLifecycleNoProgress()
	tracker.conversation.AppendToolResult(*req, result, nil)
	if err := c.persistLifecycleToolSuccess(ctx, run, phase, tracker, req, key, effectAttempt, result); err != nil {
		return false, err
	}
	c.observeLifecycleTool(ctx, phase, req, result, nil)
	if exhausted, err := c.recordSameStateBudget(ctx, budget, run.RunID, phase, lifecycleToolEffect(result, tool)); err != nil || exhausted {
		return exhausted, err
	}
	return false, nil
}

// handleDurableFailedLifecycleTool 关闭已失败的同一 effect key，但把安全结果回喂
// 模型，使 changed patch 或另一 approved validation 仍可使用新的 key。
func (c *RemediationCoordinator) handleDurableFailedLifecycleTool(ctx context.Context, budget *runBudget, run domain.Run, phase domain.RunState, tracker *resilientRunState, req *RequestTool, effect domain.LifecycleEffect) (bool, error) {
	code := safeLifecycleErrorCode(effect.ErrorCode)
	result := ToolResult{Tool: req.ToolName, Summary: lifecycleSafeMessage(code)}
	cause := &domain.LifecycleRuntimeError{Code: code, Retryable: false}
	invocation := domain.ToolInvocation{ToolName: req.ToolName, Phase: phase, Error: code}
	invocation.InvocationID = tracker.recordToolAction(req.ToolName, nil)
	if err := c.store.RecordToolInvocation(ctx, run.RunID, invocation); err != nil {
		return false, c.fail(ctx, run.RunID, phase, markPersistenceFailure(fmt.Errorf("record closed lifecycle tool invocation: %w", err)))
	}
	tracker.conversation.AppendToolResult(*req, result, cause)
	c.observeLifecycleTool(ctx, phase, req, result, cause)
	if exhausted, err := c.recordSameStateBudget(ctx, budget, run.RunID, phase, domain.Effect{ToolCalls: 1}); err != nil || exhausted {
		return exhausted, err
	}
	tracker.recordLifecycleFailure(req.ToolName + "\x00" + effect.IdempotencyKey + "\x00" + code)
	if tracker.lifecycleRecoveryAttempts >= maxLifecycleRecoveryAttempts {
		return true, c.blockLifecycleForPolicy(ctx, budget, run.RunID, phase, "lifecycle_tool_no_progress")
	}
	if err := c.appendLifecycleChallenge(ctx, domain.RecoveryChallengeKindToolFailure, code, req.ToolName, lifecycleCapabilitiesForPhase(phase), []string{"correct_request", "use_alternative"}, "The prior effect with this idempotency key failed and is closed. Submit a changed patch or another approved validation request; the failed effect will not be executed again."); err != nil {
		return false, err
	}
	return false, nil
}

func lifecycleRequestRejection(phase domain.RunState, commandVersions map[string]int64, tool string, params map[string]interface{}) *ToolRejection {
	if !lifecycleToolAdvertised(tool, phase) {
		return &ToolRejection{Code: RejectOutOfPhase, Tool: tool, Message: "lifecycle tool is unavailable in the current phase"}
	}
	if tool == ToolWorkspaceReadFile {
		pathValue, _ := params["path"].(string)
		if err := validateRepoPath(pathValue); err != nil {
			return &ToolRejection{Code: RejectPathScope, Tool: tool, Message: "path must be repository-relative"}
		}
	}
	if err := validateLifecycleToolParams(tool, params); err != nil {
		return &ToolRejection{Code: RejectArguments, Tool: tool, Message: err.Error()}
	}
	if tool == ToolWorkspaceRunValidation {
		commandID, _ := params["commandId"].(string)
		if commandVersions[commandID] < 1 {
			return &ToolRejection{Code: RejectUnavailable, Tool: tool, Message: "validation command is not approved for this run"}
		}
	}
	return nil
}

// handleLifecycleToolRejection 把 policy/phase/schema/path 拒绝作为 bounded
// model feedback 持久化；拒绝发生在 effect/adaptor 之前，绝不升级为配置故障。
func (c *RemediationCoordinator) handleLifecycleToolRejection(ctx context.Context, budget *runBudget, run domain.Run, phase domain.RunState, tracker *resilientRunState, req *RequestTool, rejection *ToolRejection) (bool, error) {
	result := ToolResult{Tool: req.ToolName}
	code, _ := RejectionCode(rejection)
	invocation := domain.ToolInvocation{ToolName: req.ToolName, Phase: phase, Error: string(code)}
	invocation.InvocationID = tracker.recordToolAction(req.ToolName, nil)
	if err := c.store.RecordToolInvocation(ctx, run.RunID, invocation); err != nil {
		return false, c.fail(ctx, run.RunID, phase, markPersistenceFailure(fmt.Errorf("record rejected lifecycle tool invocation: %w", err)))
	}
	tracker.conversation.AppendToolResult(*req, result, rejection)
	c.observeLifecycleTool(ctx, phase, req, result, rejection)
	if exhausted, err := c.recordSameStateBudget(ctx, budget, run.RunID, phase, domain.Effect{ToolCalls: 1}); err != nil || exhausted {
		return exhausted, err
	}
	tracker.recordLifecycleFailure(req.ToolName + "\x00" + lifecycleRequestParametersFingerprint(req.Parameters) + "\x00" + string(code))
	if tracker.lifecycleRecoveryAttempts >= maxLifecycleRecoveryAttempts {
		return true, c.blockLifecycleForPolicy(ctx, budget, run.RunID, phase, "lifecycle_tool_no_progress")
	}
	if err := c.appendLifecycleChallenge(ctx, domain.RecoveryChallengeKindProtocolCorrection, string(code), req.ToolName, lifecycleCapabilitiesForPhase(phase), []string{"correct_request", "use_fallback"}, lifecycleSafeMessage(string(code))); err != nil {
		return false, err
	}
	return false, nil
}

func isPatchCorrectionTool(tool string) bool {
	return tool == ToolWorkspaceApplyPatch || tool == ToolWorkspaceReadFile || tool == ToolWorkspaceStatus
}

func lifecycleRequestParametersFingerprint(parameters map[string]interface{}) string {
	encoded, err := json.Marshal(parameters)
	if err != nil {
		return "invalid_parameters"
	}
	return recoveryFingerprint(string(encoded))
}

func lifecycleEffectKindForTool(tool string) (domain.LifecycleEffectKind, bool) {
	switch tool {
	case ToolWorkspaceApplyPatch:
		return domain.LifecycleEffectPatch, true
	case ToolWorkspaceRunValidation:
		return domain.LifecycleEffectValidation, true
	default:
		return "", false
	}
}

func lifecycleToolStartedEffect(run domain.Run, phase domain.RunState, req *RequestTool, kind domain.LifecycleEffectKind, key string, attempt int, tracker *resilientRunState) domain.LifecycleEffect {
	effect := domain.LifecycleEffect{
		RunID: run.RunID, Kind: kind, IdempotencyKey: key, State: domain.LifecycleEffectStarted,
		Attempt: attempt, BaselineCommit: run.DeployedCommit, WorkspaceID: tracker.workspace.WorkspaceID,
		Summary: "lifecycle tool started",
	}
	if kind == domain.LifecycleEffectValidation {
		commandID, _ := req.Parameters["commandId"].(string)
		effect.CommandID = commandID
		effect.CommandVersion = tracker.validationCommands[commandID]
	}
	_ = phase
	return effect
}

func (c *RemediationCoordinator) persistLifecycleToolSuccess(ctx context.Context, run domain.Run, phase domain.RunState, tracker *resilientRunState, req *RequestTool, key string, attempt int, result ToolResult) error {
	switch req.ToolName {
	case ToolWorkspaceApplyPatch:
		patch, ok := result.Payload.(domain.PatchResult)
		if !ok {
			return c.fail(ctx, run.RunID, phase, markPersistenceFailure(fmt.Errorf("patch result type is invalid")))
		}
		tracker.validation = nil
		if tracker.workspace != nil {
			tracker.workspace.CurrentTreeHash = patch.ResultTreeHash
		}
		appendCheckpointArtifact(&tracker.artifacts, domain.CheckpointArtifact{Kind: "patch", Reference: patch.ArtifactRef, ContentHash: patch.ContentHash, SizeBytes: patch.BytesRetrieved})
		if _, err := c.lifecycleStore.UpsertLifecycleEffect(ctx, domain.LifecycleEffect{
			RunID: run.RunID, Kind: domain.LifecycleEffectPatch, IdempotencyKey: key, State: domain.LifecycleEffectSucceeded,
			Attempt: attempt, BaselineCommit: run.DeployedCommit, WorkspaceID: patch.WorkspaceID,
			BaseTreeHash: tracker.workspace.BaseTreeHash, ResultTreeHash: patch.ResultTreeHash,
			ArtifactRef: patch.ArtifactRef, ContentHash: patch.ContentHash, Summary: "patch applied",
		}); err != nil {
			return c.fail(ctx, run.RunID, phase, markPersistenceFailure(err))
		}
		// changed patch 只有在 success effect 持久化后才算 validation repair 进展；
		// 仅清理 validation 指纹，保留其他 recovery class 的 no-progress 计数。
		tracker.resetValidationNoProgress()
	case ToolWorkspaceRunValidation:
		validation, ok := result.Payload.(domain.ValidationResult)
		if !ok {
			return c.fail(ctx, run.RunID, phase, markPersistenceFailure(fmt.Errorf("validation result type is invalid")))
		}
		resultEntry := domain.CheckpointValidationResult{
			CommandID: validation.CommandID, CommandVersion: validation.CommandVersion,
			TreeHash: validation.TreeHash, ImageDigest: validation.ImageDigest, Passed: validation.Passed,
			OutputArtifact: validation.OutputArtifactRef, OutputHash: validation.OutputHash,
		}
		results := []domain.CheckpointValidationResult{}
		if tracker.validation != nil && tracker.validation.TreeHash == validation.TreeHash {
			results = append(results, tracker.validation.Results...)
		}
		replaced := false
		for index := range results {
			if results[index].CommandID == validation.CommandID {
				results[index] = resultEntry
				replaced = true
				break
			}
		}
		if !replaced {
			results = append(results, resultEntry)
		}
		tracker.validation = &domain.CheckpointValidation{
			CommandID: validation.CommandID, CommandVersion: validation.CommandVersion,
			OutputArtifact: validation.OutputArtifactRef, OutputHash: validation.OutputHash,
			TreeHash: validation.TreeHash, Results: results,
		}
		tracker.validation.Passed = validationSetPassed(tracker.validation, tracker.validationCommands, tracker.workspace.CurrentTreeHash)
		if validation.OutputArtifactRef != "" {
			appendCheckpointArtifact(&tracker.artifacts, domain.CheckpointArtifact{Kind: "validation", Reference: validation.OutputArtifactRef, ContentHash: validation.OutputHash, SizeBytes: validation.BytesRetrieved})
		}
		if _, err := c.lifecycleStore.UpsertLifecycleEffect(ctx, domain.LifecycleEffect{
			RunID: run.RunID, Kind: domain.LifecycleEffectValidation, IdempotencyKey: key, State: domain.LifecycleEffectSucceeded,
			Attempt: attempt, BaselineCommit: run.DeployedCommit, WorkspaceID: validation.WorkspaceID,
			ResultTreeHash: validation.TreeHash,
			CommandID:      validation.CommandID, CommandVersion: validation.CommandVersion,
			ValidationKnown: true, ValidationPassed: validation.Passed,
			ArtifactRef: validation.OutputArtifactRef, ContentHash: validation.OutputHash, Summary: sanitizeReviewText(validation.Summary),
		}); err != nil {
			return c.fail(ctx, run.RunID, phase, markPersistenceFailure(err))
		}
	}
	if err := c.checkpointRun(ctx, tracker, phase, domain.CheckpointReasonPhaseBoundary); err != nil {
		return c.fail(ctx, run.RunID, phase, markPersistenceFailure(err))
	}
	return nil
}

func validationResultsPassButIncomplete(validation *domain.CheckpointValidation) bool {
	if validation == nil || len(validation.Results) == 0 {
		return false
	}
	for _, result := range validation.Results {
		if !result.Passed {
			return false
		}
	}
	return true
}

func validationSetPassed(validation *domain.CheckpointValidation, commands map[string]int64, treeHash string) bool {
	if validation == nil || len(commands) == 0 || validation.TreeHash != treeHash {
		return false
	}
	passed := make(map[string]int64, len(validation.Results))
	for _, result := range validation.Results {
		if result.Passed && result.TreeHash == treeHash {
			passed[result.CommandID] = result.CommandVersion
		}
	}
	for commandID, version := range commands {
		if passed[commandID] != version {
			return false
		}
	}
	return true
}

func lifecycleToolResultFromEffect(tool string, effect domain.LifecycleEffect, currentTreeHash string) ToolResult {
	switch tool {
	case ToolWorkspaceApplyPatch:
		return ToolResult{Tool: tool, Summary: effect.Summary, BytesRetrieved: 0, Payload: domain.PatchResult{
			WorkspaceID: effect.WorkspaceID, Applied: true, AlreadyApplied: true, ArtifactRef: effect.ArtifactRef,
			ContentHash: effect.ContentHash, ResultTreeHash: effect.ResultTreeHash, Summary: effect.Summary,
		}}
	case ToolWorkspaceRunValidation:
		validationTree := effect.ResultTreeHash
		if validationTree == "" {
			validationTree = currentTreeHash
		}
		return ToolResult{Tool: tool, Summary: effect.Summary, Payload: domain.ValidationResult{
			RunID: effect.RunID, WorkspaceID: effect.WorkspaceID, CommandID: effect.CommandID,
			CommandVersion: effect.CommandVersion, TreeHash: validationTree, Passed: effect.ValidationPassed,
			OutputArtifactRef: effect.ArtifactRef, OutputHash: effect.ContentHash, Summary: effect.Summary,
		}}
	default:
		return ToolResult{Tool: tool, Summary: effect.Summary}
	}
}

func lifecycleToolEffect(result ToolResult, tool string) domain.Effect {
	effect := domain.Effect{ToolCalls: 1}
	if strings.HasPrefix(tool, "workspace.read") || tool == ToolWorkspaceStatus || tool == ToolWorkspaceApplyPatch {
		effect.RepositoryBytes = result.BytesRetrieved
	} else if tool == ToolWorkspaceRunValidation {
		effect.EvidenceBytes = result.BytesRetrieved
	}
	return effect
}

func (c *RemediationCoordinator) handleLifecycleToolDeadline(ctx context.Context, budget *runBudget, run domain.Run, phase domain.RunState, tracker *resilientRunState, req *RequestTool, result ToolResult) (bool, error) {
	tracker.conversation.AppendToolResult(*req, result, context.DeadlineExceeded)
	_, err := c.recordElapsedOperation(ctx, budget, run.RunID, phase, lifecycleToolEffect(result, req.ToolName))
	return true, err
}

func (c *RemediationCoordinator) handleLifecycleDeadline(ctx context.Context, budget *runBudget, runID string, phase domain.RunState) error {
	_, err := c.recordElapsedOperation(ctx, budget, runID, phase, domain.Effect{})
	return err
}

func (c *RemediationCoordinator) handleLifecycleTurnError(ctx context.Context, budget *runBudget, runID string, phase domain.RunState, usage domain.ModelResult, cause error) (bool, error) {
	if exhausted, err := c.recordSameStateBudget(ctx, budget, runID, phase, modelEffect(usage)); err != nil || exhausted {
		return true, err
	}
	tracker := resilientStateFrom(ctx)
	if tracker == nil {
		return true, ErrLifecycleUnavailable
	}
	code, retryable := lifecycleErrorInfo(cause)
	if !retryable && !errors.Is(cause, ErrInvalidEnvelope) {
		return true, c.fail(ctx, runID, phase, cause)
	}
	tracker.recordLifecycleFailure("model_turn\x00" + code)
	if tracker.lifecycleRecoveryAttempts >= maxLifecycleRecoveryAttempts {
		reason := "lifecycle_model_no_progress"
		if errors.Is(cause, ErrInvalidEnvelope) {
			switch phase {
			case domain.RunStatePatching:
				reason = "patch_protocol_no_progress"
			case domain.RunStateValidating:
				reason = "validation_protocol_no_progress"
			}
		}
		return true, c.blockLifecycleForPolicy(ctx, budget, runID, phase, reason)
	}
	kind := domain.RecoveryChallengeKindContextRehydration
	suggested := []string{"retry_transient", "rehydrate_context"}
	message := "The model turn did not complete. Retry the bounded turn from durable lifecycle context; no external effect was acknowledged."
	if errors.Is(cause, ErrInvalidEnvelope) {
		kind = domain.RecoveryChallengeKindProtocolCorrection
		suggested = []string{"correct_request"}
		correction := ProtocolCorrectionFor(phase, cause)
		code = correction.Code
		message = correction.Message
	}
	if err := c.appendLifecycleChallenge(ctx, kind, code, "model_turn", lifecycleCapabilitiesForPhase(phase), suggested, message); err != nil {
		return true, err
	}
	return false, nil
}

func (c *RemediationCoordinator) handleLifecycleStop(ctx context.Context, budget *runBudget, runID string, phase domain.RunState, output *StopOutput) (bool, error) {
	tracker := resilientStateFrom(ctx)
	if tracker == nil {
		return true, ErrLifecycleUnavailable
	}
	tracker.resetLifecycleProtocolNoProgress()
	tracker.lifecycleStopAttempts++
	if phase == domain.RunStatePatching && output != nil && output.Code == "selected_change_absent_at_baseline" {
		tracker.nextActions = []string{"Reconcile the selected repair plan with the deployed baseline commit and workspace tree; review the incident evidence before selecting a new plan. Do not patch or publish the mismatched plan."}
		return true, c.blockLifecycleForPolicy(ctx, budget, runID, phase, "patch_plan_precondition_mismatch")
	}
	if tracker.lifecycleStopAttempts >= maxLifecycleRecoveryAttempts {
		return true, c.blockLifecycleForPolicy(ctx, budget, runID, phase, "agent_stop_no_progress")
	}
	if err := c.appendLifecycleChallenge(ctx, domain.RecoveryChallengeKindValidationRevision, "agent_stop_requires_recovery", "stop", lifecycleCapabilitiesForPhase(phase), []string{"inspect_workspace", "retry_transient"}, "A model stop is not a terminal lifecycle conclusion while a bounded workspace, validation, or publication recovery path remains. Continue with an advertised recovery action or provide a safe changed result."); err != nil {
		return true, err
	}
	return false, nil
}

func (c *RemediationCoordinator) appendLifecycleChallenge(ctx context.Context, kind domain.RecoveryChallengeKind, code, action string, capabilities, suggested []string, message string) error {
	tracker := resilientStateFrom(ctx)
	if tracker == nil {
		return ErrLifecycleUnavailable
	}
	tracker.lifecycleChallengeAttempts++
	challenge, err := NewRecoveryChallenge(kind, domain.RecoverySeverityRecoverable, code, action, capabilities, suggested, tracker.lifecycleChallengeAttempts, lifecycleRemainingBudget(ctx), message)
	if err != nil {
		return c.fail(ctx, tracker.run.RunID, lifecyclePhaseFromTracker(tracker), markPersistenceFailure(err))
	}
	journalAction := "lifecycle:" + action
	fingerprint := tracker.lastLifecycleRecoveryFingerprint
	if fingerprint == "" {
		fingerprint = action + "\x00" + code
	}
	outcomeRef := recoveryProgressRef(code, fingerprint)
	if action == "stop" {
		journalAction = "stop"
	}
	if code == "validation_failed" {
		journalAction = "revise_patch"
		outcomeRef = recoveryProgressRef(code, tracker.lastValidationFingerprint)
	}
	tracker.appendRecovery(domain.CheckpointRecovery{Kind: string(kind), Action: journalAction, OutcomeRef: outcomeRef})
	phase := lifecyclePhaseFromTracker(tracker)
	// D5 challenge 追加与 per-kind 指标在同一共享出口完成；nil conversation 时
	// 跳过，与既有行为一致。
	c.appendRecoveryChallenge(ctx, phase, tracker.conversation, challenge)
	if err := c.checkpointRun(ctx, tracker, phase, domain.CheckpointReasonRecovery); err != nil {
		return c.fail(ctx, tracker.run.RunID, phase, markPersistenceFailure(err))
	}
	return nil
}

func lifecyclePhaseFromTracker(tracker *resilientRunState) domain.RunState {
	if tracker == nil || tracker.lifecyclePhase == "" {
		return domain.RunStatePatching
	}
	return tracker.lifecyclePhase
}

type lifecycleBudgetContextKey struct{}

func withLifecycleBudget(ctx context.Context, budget *runBudget) context.Context {
	return context.WithValue(ctx, lifecycleBudgetContextKey{}, budget)
}

func lifecycleRemainingBudget(ctx context.Context) map[string]int64 {
	if budget, ok := ctx.Value(lifecycleBudgetContextKey{}).(*runBudget); ok && budget != nil {
		return budget.remaining()
	}
	return map[string]int64{}
}

func (c *RemediationCoordinator) blockLifecycleForPolicy(ctx context.Context, budget *runBudget, runID string, phase domain.RunState, reason string) error {
	if tracker := resilientStateFrom(ctx); tracker != nil {
		switch reason {
		case "patch_protocol_no_progress", "validation_protocol_no_progress", "lifecycle_model_no_progress", "agent_stop_no_progress":
			tracker.nextActions = []string{"Review the lifecycle protocol failures and reconcile the selected repair plan with the deployed baseline and persisted workspace results before retrying. Do not publish an unvalidated or unchanged patch."}
		}
	}
	if _, err := c.transitionBudgeted(ctx, budget, runID, phase, domain.RunStateBlockedManualReview, domain.Effect{TerminalReason: safeLifecycleTerminalReason(reason)}); err != nil {
		return err
	}
	return c.notifyTerminal(ctx, runID, domain.RunStateBlockedManualReview, domain.FixabilityUnsafeToAutomate)
}

func safeLifecycleTerminalReason(reason string) string {
	switch reason {
	case "denied_control_plane_change", "high_risk_policy_requires_opt_in", "plan_policy_no_progress", "no_policy_compliant_plan",
		"publication_retry_exhausted", "workspace_retry_exhausted", "patch_retry_exhausted", "validation_retry_exhausted",
		"publication_validation_required", "publication_patch_artifact_required",
		"patch_protocol_no_progress", "validation_protocol_no_progress", "validation_no_progress",
		"lifecycle_tool_no_progress", "lifecycle_model_no_progress", "agent_stop_no_progress", "patch_plan_precondition_mismatch":
		return reason
	default:
		if safe := safeLifecycleErrorCode(reason); safe != "lifecycle_failure" {
			return safe
		}
		return "lifecycle_policy_blocked"
	}
}

func lifecycleErrorInfo(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	var lifecycleErr *domain.LifecycleRuntimeError
	if errors.As(err, &lifecycleErr) {
		return safeLifecycleErrorCode(lifecycleErr.Code), lifecycleErr.Retryable
	}
	var providerErr *domain.ProviderRuntimeError
	if errors.As(err, &providerErr) {
		code := safeProviderFailureCode(providerErr.Code)
		return code, providerErr.Retryable && isRetryableProviderFailureCode(code)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "connector_timeout", true
	}
	if errors.Is(err, context.Canceled) {
		return "canceled", false
	}
	if errors.Is(err, ErrInvalidEnvelope) {
		return "invalid_envelope", true
	}
	if code, ok := RejectionCode(err); ok {
		return string(code), code != RejectOutOfPhase
	}
	return "lifecycle_failure", true
}

func safeLifecycleErrorCode(code string) string {
	switch code {
	case "workspace_unavailable", "workspace_invalid", "workspace_invalid_response", "workspace_baseline_mismatch",
		"patch_invalid_response", "validation_unavailable", "validation_invalid_response", "validation_command_unapproved",
		"publication_invalid_response", "publication_baseline_mismatch", "connector_timeout", "transport", "rate_limit",
		"remote_execution", "authentication", "authorization", "not_found", "invalid_arguments", "tool_unavailable",
		"connector_failure", "canceled", "invalid_envelope":
		return code
	default:
		return "lifecycle_failure"
	}
}

func lifecycleSafeMessage(code string) string {
	switch code {
	case "workspace_unavailable":
		return "isolated workspace is unavailable"
	case "workspace_baseline_mismatch":
		return "isolated workspace baseline does not match the run"
	case "patch_invalid_response":
		return "workspace patch returned an invalid bounded result"
	case "validation_unavailable":
		return "approved validation runner is unavailable"
	case "validation_invalid_response":
		return "validation runner returned an invalid bounded result"
	case "publication_invalid_response":
		return "publisher returned an invalid bounded result"
	case "publication_baseline_mismatch":
		return "publisher did not confirm the immutable run baseline"
	case "connector_timeout":
		return "lifecycle connector timed out"
	case "transport":
		return "lifecycle connector transport failed"
	case "rate_limit":
		return "lifecycle connector rate limit reached"
	case "invalid_arguments":
		return "lifecycle tool arguments are invalid"
	case "tool_unavailable":
		return "lifecycle tool is unavailable"
	default:
		return "lifecycle effect failed; use the bounded recovery path"
	}
}

func lifecycleCapabilitiesForPhase(phase domain.RunState) []string {
	switch phase {
	case domain.RunStatePatching:
		return []string{"workspace"}
	case domain.RunStateValidating:
		return []string{"workspace", "validation"}
	case domain.RunStatePublishing:
		return []string{"publication"}
	default:
		return []string{}
	}
}

func lifecycleEffectKey(kind, runID, value string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + runID + "\x00" + value))
	return kind + "/" + hex.EncodeToString(sum[:])
}

func shortLifecycleID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 12 {
		value = value[:12]
	}
	value = strings.NewReplacer("/", "-", "\\", "-", " ", "-").Replace(value)
	if value == "" {
		return "run"
	}
	return value
}

func requestValue(values map[string]interface{}, key string) string {
	value, _ := values[key].(string)
	if key == "patch" {
		// effect key 只携带完整 bounded patch 的固定长度 identity，既不截断
		// suffix 差异，也不把 patch 文本写入 durable idempotency metadata。
		sum := sha256.Sum256([]byte(value))
		return hex.EncodeToString(sum[:])
	}
	return boundedContinuationText(value, 256)
}

func checkpointWorkspaceIdentity(value domain.CheckpointWorkspace) domain.WorkspaceIdentity {
	return domain.WorkspaceIdentity{WorkspaceID: value.WorkspaceID, RunID: "", BaselineCommit: value.BaselineCommit, BaseTreeHash: value.BaseTreeHash, CurrentTreeHash: value.CurrentTreeHash, Version: value.Version}
}

func lifecyclePlanContext(run domain.Run, plan domain.RepairPlanCandidate, suggestedDiff string) string {
	return "## Selected repair plan\n" +
		"runId=" + boundedContinuationText(run.RunID, 128) + "\n" +
		"baselineCommit=" + boundedContinuationText(run.DeployedCommit, 256) + "\n" +
		"planId=" + boundedContinuationText(plan.PlanID, 128) + "\n" +
		"risk=" + safeContinuationRisk(plan.Risk) + "\n" +
		"affectedFiles=" + strings.Join(boundedContinuationList(plan.AffectedFiles), ", ") + "\n" +
		"intendedBehavior=" + boundedContinuationText(plan.IntendedBehavior, 2048) + "\n" +
		"rollbackStrategy=" + boundedContinuationText(plan.RollbackStrategy, 2048) + "\n" +
		"evidenceRefs=" + strings.Join(boundedContinuationList(plan.EvidenceRefs), ", ") + "\n" +
		"suggestedDiff:\n" + boundedContinuationText(sanitizeSuggestedDiff(suggestedDiff), maxObservationBytes)
}

func appendCheckpointArtifact(items *[]domain.CheckpointArtifact, artifact domain.CheckpointArtifact) {
	if items == nil || artifact.Reference == "" {
		return
	}
	for index := range *items {
		if (*items)[index].Reference == artifact.Reference && (*items)[index].Kind == artifact.Kind {
			(*items)[index] = artifact
			return
		}
	}
	*items = append(*items, artifact)
	if len(*items) > 64 {
		*items = (*items)[len(*items)-64:]
	}
}

func hasCheckpointArtifact(items []domain.CheckpointArtifact, kind string) bool {
	_, ok := latestCheckpointArtifact(items, kind)
	return ok
}

func latestCheckpointArtifact(items []domain.CheckpointArtifact, kind string) (domain.CheckpointArtifact, bool) {
	for index := len(items) - 1; index >= 0; index-- {
		if items[index].Kind == kind && items[index].Reference != "" {
			return items[index], true
		}
	}
	return domain.CheckpointArtifact{}, false
}

// restoredRunBudgetCounters 合并 durable run counters 与 checkpoint elapsed。
// active PostgreSQL run 的 elapsed_ms 为 NULL，因此只有该维度允许由 checkpoint
// 恢复；model/tool/byte/cost counters 始终由 remediation_run 掌权。
func restoredRunBudgetCounters(tracker *resilientRunState, durable domain.BudgetCounters) domain.BudgetCounters {
	if tracker == nil || tracker.alloc == nil || durable.ElapsedSeconds != 0 {
		return durable
	}
	checkpointElapsed := tracker.alloc.Projection().Consumed.ElapsedSeconds
	if checkpointElapsed > durable.ElapsedSeconds {
		durable.ElapsedSeconds = checkpointElapsed
	}
	return durable
}

// restoredRunBudgetLimits 优先返回 checkpoint 内 immutable budget plan 的
// hard ceiling；fallback 仅用于没有 durable allocator 的旧 checkpoint。
func restoredRunBudgetLimits(fallback domain.BudgetLimits, tracker *resilientRunState) domain.BudgetLimits {
	if tracker != nil && tracker.alloc != nil {
		return tracker.alloc.plan.Ceiling
	}
	return fallback
}

func resumeRunBudget(limits domain.BudgetLimits, used domain.BudgetCounters) *runBudget {
	now := time.Now()
	startedAt := now.Add(-time.Duration(maxInt64Value(used.ElapsedSeconds, 0)) * time.Second)
	return &runBudget{limits: normalizeBudgetLimits(limits), startedAt: startedAt, now: time.Now, used: used}
}

func maxInt64Value(value, fallback int64) int64 {
	if value < fallback {
		return fallback
	}
	return value
}

func loadRunForLifecycle(ctx context.Context, store domain.RunStore, runID string) (domain.Run, error) {
	agg, err := store.Get(ctx, runID)
	if err != nil {
		return domain.Run{}, err
	}
	return agg.Run, nil
}

func (c *RemediationCoordinator) loadLifecycleRun(ctx context.Context, runID string) (domain.Run, error) {
	return loadRunForLifecycle(ctx, c.store, runID)
}
