package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"mendry/backend/internal/modules/agentcore/domain"
)

const defaultMaxSteps = 32

// RunnerOptions 注入所有 instance-owned ports、registries、policy、clock 和 ID source。
type RunnerOptions struct {
	Store      domain.RunStore
	Provider   domain.ModelProvider
	Tools      *ToolRegistry
	Policy     Policy
	Evaluators *EvaluatorRegistry
	MaxSteps   int
	Now        func() time.Time
	NewID      func() string
}

// Runner 共享 prepare→model→authorize→intent→execute→observe→evaluate 的有界循环。
type Runner struct {
	store      domain.RunStore
	step       *ModelStep
	tools      *ToolRegistry
	policy     Policy
	evaluators *EvaluatorRegistry
	maxSteps   int
	now        func() time.Time
	newID      func() string
}

// NewRunner 校验依赖并创建不含全局状态的通用 runner。
func NewRunner(options RunnerOptions) (*Runner, error) {
	if options.Store == nil || options.Provider == nil || options.Tools == nil || options.Policy == nil {
		return nil, errors.New("runner store, provider, tools and policy are required")
	}
	step, err := NewModelStep(options.Provider)
	if err != nil {
		return nil, err
	}
	if options.Evaluators == nil {
		options.Evaluators = NewEvaluatorRegistry()
	}
	if options.MaxSteps <= 0 {
		options.MaxSteps = defaultMaxSteps
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.NewID == nil {
		options.NewID = randomID
	}
	return &Runner{store: options.Store, step: step, tools: options.Tools, policy: options.Policy, evaluators: options.Evaluators, maxSteps: options.MaxSteps, now: options.Now, newID: options.NewID}, nil
}

// Drive 恢复 durable run 并执行 bounded loop；unresolved write 永远不自动 replay。
func (r *Runner) Drive(ctx context.Context, runID string, profile domain.Profile) (domain.Run, error) {
	if profile == nil {
		return domain.Run{}, errors.New("profile is required")
	}
	run, err := r.store.LoadRun(ctx, runID)
	if err != nil {
		return domain.Run{}, fmt.Errorf("load run: %w", err)
	}
	definition := profile.Definition()
	if definition.Name == "" || !toolVersionPattern.MatchString(definition.Version) || !toolVersionPattern.MatchString(definition.Completion.Version) {
		return run, r.fail(ctx, &run, "invalid_profile", errors.New("profile definition is invalid"))
	}
	if run.ProfileName != definition.Name || run.ProfileVersion != definition.Version {
		return run, r.fail(ctx, &run, "profile_mismatch", errors.New("run profile snapshot does not match"))
	}

	invocations, err := r.store.ListInvocations(ctx, run.ID)
	if err != nil {
		return run, fmt.Errorf("load invocations: %w", err)
	}
	sort.Slice(invocations, func(i, j int) bool { return invocations[i].Sequence < invocations[j].Sequence })
	if reconcileInvocationBudget(&run, invocations) {
		if err := r.saveRun(context.WithoutCancel(ctx), &run); err != nil {
			return run, err
		}
	}
	if waiting, recoverErr := r.recoverPending(ctx, &run, invocations); waiting || recoverErr != nil {
		return run, recoverErr
	}
	messages, err := r.store.LoadMessages(ctx, run.ID)
	if err != nil {
		return run, fmt.Errorf("load messages: %w", err)
	}
	history := NewPairedHistory(messages, DefaultHistoryBounds())
	if run.State == domain.RunStateQueued {
		run.State = domain.RunStateRunning
		if run.StartedAt.IsZero() {
			run.StartedAt = r.now()
		}
		if err := r.saveRun(ctx, &run); err != nil {
			return run, err
		}
	}
	if run.State != domain.RunStateRunning {
		return run, nil
	}

	for stepIndex := 0; stepIndex < r.maxSteps; stepIndex++ {
		if err := ctx.Err(); err != nil {
			run.State = domain.RunStateCancelled
			run.ReasonCode = "cancelled"
			return run, r.saveRun(context.WithoutCancel(ctx), &run)
		}
		if reason := budgetReason(run, r.now(), true, false, 0); reason != "" {
			return run, r.fail(ctx, &run, reason, errors.New("run budget exhausted"))
		}
		artifacts, err := r.store.ListArtifacts(ctx, run.ID)
		if err != nil {
			return run, fmt.Errorf("load artifacts: %w", err)
		}
		catalog, err := r.advertised(ctx, run)
		if err != nil {
			return run, r.fail(ctx, &run, "policy_failure", err)
		}
		turn, err := profile.Prepare(ctx, domain.ProfileContext{Run: run, History: history.Snapshot(), Tools: catalog, Invocations: append([]domain.Invocation(nil), invocations...), Artifacts: append([]domain.Artifact(nil), artifacts...)})
		if err != nil {
			return run, r.fail(ctx, &run, "profile_prepare_failed", err)
		}
		turn.Tools = catalog
		var interpreted domain.ProfileResult
		modelAccounted := false
		commitPersistenceFailure := false
		operationCtx, cancelOperation := operationContext(ctx, run)
		result, err := r.step.Execute(operationCtx, StepRequest{
			Turn: turn, History: history.Snapshot(),
			Validate: func(result domain.ModelResult) error {
				if len(result.ToolCalls) > 0 {
					return r.validateCalls(result.ToolCalls)
				}
				var interpretErr error
				interpreted, interpretErr = profile.Interpret(ctx, result)
				if interpretErr != nil {
					return interpretErr
				}
				return validateModelArtifacts(interpreted.Artifacts)
			},
			Commit: func(user string, result domain.ModelResult) error {
				group, groupErr := modelGroup(user, result)
				if groupErr != nil {
					return groupErr
				}
				accountModelResult(&run, result)
				modelAccounted = true
				// 先持久化预算，再提交 accepted history；crash 最多保守多计一次，不能漏计已接受轮次。
				if saveErr := r.saveRun(ctx, &run); saveErr != nil {
					commitPersistenceFailure = true
					return saveErr
				}
				if storeErr := r.store.AppendMessageGroup(ctx, run.ID, group); storeErr != nil {
					commitPersistenceFailure = true
					return fmt.Errorf("append accepted model history: %w", storeErr)
				}
				return history.CommitModelTurn(user, result)
			},
		})
		operationDeadlineExceeded := operationCtx.Err() != nil
		cancelOperation()
		if !modelAccounted {
			accountModelResult(&run, result)
		}
		if err != nil {
			if ctx.Err() != nil {
				run.State = domain.RunStateCancelled
				run.ReasonCode = "cancelled"
				return run, r.saveRun(context.WithoutCancel(ctx), &run)
			}
			if operationDeadlineExceeded {
				return run, r.fail(ctx, &run, "elapsed_budget_exhausted", err)
			}
			if commitPersistenceFailure {
				return run, r.fail(ctx, &run, "persistence_failure", err)
			}
			return run, r.fail(ctx, &run, "model_failed", err)
		}
		if reason := budgetReason(run, r.now(), false, false, 0); reason != "" {
			return run, r.fail(ctx, &run, reason, errors.New("run budget exhausted"))
		}
		if len(result.ToolCalls) > 0 {
			for _, call := range result.ToolCalls {
				waiting, invokeErr := r.invoke(ctx, &run, call, history, &invocations)
				if invokeErr != nil || waiting {
					return run, invokeErr
				}
			}
			continue
		}
		if len(interpreted.Artifacts) > 0 {
			for index := range interpreted.Artifacts {
				prepareModelArtifact(&interpreted.Artifacts[index], run.ID, r.newID(), r.now())
			}
			if err := r.store.AppendArtifacts(ctx, run.ID, interpreted.Artifacts); err != nil {
				return run, r.fail(ctx, &run, "persistence_failure", err)
			}
			artifacts = append(artifacts, interpreted.Artifacts...)
		}
		evaluation, err := r.evaluators.Evaluate(ctx, definition.Completion, artifacts)
		if err != nil {
			return run, r.fail(ctx, &run, "completion_evaluation_failed", err)
		}
		if evaluation.Complete {
			run.State = domain.RunStateSucceeded
			run.ReasonCode = evaluation.Reason
			run.Result = cloneMap(interpreted.Result)
			return run, r.saveRun(ctx, &run)
		}
		if interpreted.Stop {
			run.State = domain.RunStateWaiting
			run.ReasonCode = evaluation.Reason
			return run, r.saveRun(ctx, &run)
		}
	}
	return run, r.fail(ctx, &run, "step_limit", errors.New("runner step limit reached"))
}

func (r *Runner) advertised(ctx context.Context, run domain.Run) ([]domain.ToolDefinition, error) {
	definitions := r.tools.Definitions()
	allowed := make([]domain.ToolDefinition, 0, len(definitions))
	for _, definition := range definitions {
		decision, err := r.policy.Evaluate(ctx, PolicyInput{Run: run, Definition: definition, Stage: "advertisement"})
		if err != nil {
			return nil, err
		}
		if decision.Allowed {
			allowed = append(allowed, definition)
		}
	}
	return allowed, nil
}

func (r *Runner) validateCalls(calls []domain.ToolCall) error {
	seen := make(map[string]struct{}, len(calls))
	for _, call := range calls {
		if call.ID == "" {
			return errors.New("tool call id is required")
		}
		if _, exists := seen[call.ID]; exists {
			return errors.New("duplicate tool call id")
		}
		seen[call.ID] = struct{}{}
		definition, _, ok := r.tools.Resolve(call.Name, call.Version)
		if !ok {
			return errors.New("model requested an unregistered tool version")
		}
		if err := r.tools.ValidateArguments(definition, call.Arguments); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) invoke(ctx context.Context, run *domain.Run, call domain.ToolCall, history *PairedHistory, invocations *[]domain.Invocation) (bool, error) {
	definition, executor, ok := r.tools.Resolve(call.Name, call.Version)
	if !ok {
		return false, r.fail(ctx, run, "tool_unavailable", errors.New("tool disappeared before dispatch"))
	}
	decision, err := r.policy.Evaluate(ctx, PolicyInput{Run: *run, Definition: definition, Arguments: cloneMap(call.Arguments), Stage: "dispatch"})
	if err != nil {
		return false, r.fail(ctx, run, "policy_failure", err)
	}
	sequence := nextInvocationSequence(*invocations)
	invocation := domain.Invocation{ID: r.newID(), RunID: run.ID, Sequence: sequence, ToolName: definition.Name, ToolVersion: definition.Version, Effect: definition.Effect, ArgumentsDigest: argumentsDigest(call.Arguments), IdempotencyKey: r.newID(), Authorized: decision.Allowed, State: domain.InvocationPending, CreatedAt: r.now()}
	if !decision.Allowed {
		invocation.State = domain.InvocationRejected
		if err := r.store.RecordInvocationIntent(ctx, invocation); err != nil {
			return false, r.fail(ctx, run, "persistence_failure", err)
		}
		*invocations = append(*invocations, invocation)
		if err := r.appendToolResult(ctx, run.ID, history, call.ID, map[string]any{"status": "rejected", "code": decision.Code}); err != nil {
			return false, r.fail(ctx, run, "persistence_failure", err)
		}
		return false, nil
	}
	if reason := budgetReason(*run, r.now(), false, true, 0); reason != "" {
		return false, r.fail(ctx, run, reason, errors.New("tool budget exhausted"))
	}
	// intent 必须在任何 executor side effect 前 durable；该写入失败时禁止调用 adapter。
	if err := r.store.RecordInvocationIntent(ctx, invocation); err != nil {
		return false, r.fail(ctx, run, "persistence_failure", err)
	}
	*invocations = append(*invocations, invocation)
	if err := ctx.Err(); err != nil {
		return r.interruptedIntent(ctx, run, invocation, call.ID, history, err)
	}
	operationCtx, cancelOperation := operationContext(ctx, *run)
	execution, executeErr := executor.Execute(operationCtx, call)
	operationStopped := operationCtx.Err() != nil
	cancelOperation()
	run.Budget.Consumed.ToolCalls++
	run.Budget.Consumed.OutputBytes += execution.OutputBytes
	state := domain.InvocationSucceeded
	code := ""
	if executeErr != nil {
		state = domain.InvocationFailed
		code = "tool_failed"
		var typed *domain.ExecutionError
		known := errors.As(executeErr, &typed) && typed.OutcomeKnown
		if typed != nil && typed.Code != "" {
			code = typed.Code
		}
		if definition.Effect == domain.ToolEffectWrite && !known {
			state = domain.InvocationUnknown
		} else if definition.Effect == domain.ToolEffectRead && operationStopped {
			state = domain.InvocationInterrupted
		}
	}
	artifacts, artifactErr := prepareToolArtifacts(execution.Artifacts, run.ID, invocation.ID, r.newID, r.now)
	if artifactErr != nil {
		return false, r.fail(ctx, run, "invalid_tool_artifact", artifactErr)
	}
	result := domain.InvocationResult{InvocationID: invocation.ID, Sequence: invocation.Sequence, State: state, Code: code, Output: execution.Output, OutputBytes: execution.OutputBytes, CompletedAt: r.now()}
	if err := r.store.RecordInvocationResult(context.WithoutCancel(ctx), result, artifacts); err != nil {
		// write 已经发出但结果无法 durable，必须进入 waiting，不能把 persistence error 当作可重试调用。
		if definition.Effect == domain.ToolEffectWrite {
			run.State = domain.RunStateWaiting
			run.ReasonCode = "unknown_write_outcome"
			return true, r.saveRun(context.WithoutCancel(ctx), run)
		}
		return false, r.fail(ctx, run, "persistence_failure", err)
	}
	for index := range *invocations {
		if (*invocations)[index].ID == invocation.ID {
			(*invocations)[index].State = state
			(*invocations)[index].OutputBytes = execution.OutputBytes
			break
		}
	}
	if state == domain.InvocationUnknown {
		run.State = domain.RunStateWaiting
		run.ReasonCode = "unknown_write_outcome"
		return true, r.saveRun(context.WithoutCancel(ctx), run)
	}
	observation := map[string]any{"status": string(state), "code": code, "output": execution.Output}
	observationCtx := ctx
	if operationStopped {
		observationCtx = context.WithoutCancel(ctx)
	}
	if err := r.appendToolResult(observationCtx, run.ID, history, call.ID, observation); err != nil {
		return false, r.fail(ctx, run, "persistence_failure", err)
	}
	if operationStopped {
		if ctx.Err() != nil {
			run.State = domain.RunStateCancelled
			run.ReasonCode = "cancelled"
			return true, r.saveRun(context.WithoutCancel(ctx), run)
		}
		return false, r.fail(ctx, run, "elapsed_budget_exhausted", context.DeadlineExceeded)
	}
	if reason := budgetReason(*run, r.now(), false, false, 0); reason != "" {
		return false, r.fail(ctx, run, reason, errors.New("run budget exhausted"))
	}
	return false, r.saveRun(ctx, run)
}

func (r *Runner) recoverPending(ctx context.Context, run *domain.Run, invocations []domain.Invocation) (bool, error) {
	for index := range invocations {
		invocation := invocations[index]
		if invocation.State != domain.InvocationPending && invocation.State != domain.InvocationUnknown {
			continue
		}
		if invocation.Effect == domain.ToolEffectWrite {
			run.State = domain.RunStateWaiting
			run.ReasonCode = "unknown_write_outcome"
			return true, r.saveRun(context.WithoutCancel(ctx), run)
		}
		result := domain.InvocationResult{InvocationID: invocation.ID, Sequence: invocation.Sequence, State: domain.InvocationInterrupted, Code: "interrupted_read", CompletedAt: r.now()}
		if err := r.store.RecordInvocationResult(context.WithoutCancel(ctx), result, nil); err != nil {
			return false, err
		}
		invocations[index].State = domain.InvocationInterrupted
	}
	return false, nil
}

func (r *Runner) interruptedIntent(ctx context.Context, run *domain.Run, invocation domain.Invocation, callID string, history *PairedHistory, cause error) (bool, error) {
	if invocation.Effect == domain.ToolEffectWrite {
		result := domain.InvocationResult{InvocationID: invocation.ID, Sequence: invocation.Sequence, State: domain.InvocationUnknown, Code: "interrupted", CompletedAt: r.now()}
		_ = r.store.RecordInvocationResult(context.WithoutCancel(ctx), result, nil)
		run.State = domain.RunStateWaiting
		run.ReasonCode = "unknown_write_outcome"
		return true, r.saveRun(context.WithoutCancel(ctx), run)
	}
	result := domain.InvocationResult{InvocationID: invocation.ID, Sequence: invocation.Sequence, State: domain.InvocationInterrupted, Code: "interrupted_read", CompletedAt: r.now()}
	if err := r.store.RecordInvocationResult(context.WithoutCancel(ctx), result, nil); err != nil {
		return false, err
	}
	_ = r.appendToolResult(context.WithoutCancel(ctx), run.ID, history, callID, map[string]any{"status": "interrupted", "code": "interrupted_read"})
	run.State = domain.RunStateCancelled
	run.ReasonCode = "cancelled"
	return true, r.saveRun(context.WithoutCancel(ctx), run)
}

func (r *Runner) appendToolResult(ctx context.Context, runID string, history *PairedHistory, callID string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	message := domain.ModelMessage{Role: "tool", ToolCallID: callID, Content: boundedString(string(encoded), DefaultHistoryBounds().MaxMessageBytes)}
	if err := r.store.AppendMessageGroup(ctx, runID, []domain.ModelMessage{message}); err != nil {
		return err
	}
	return history.AppendToolResult(callID, message.Content)
}

func (r *Runner) saveRun(ctx context.Context, run *domain.Run) error {
	run.UpdatedAt = r.now()
	run.Version++
	if err := r.store.SaveRun(ctx, *run); err != nil {
		return fmt.Errorf("save run: %w", err)
	}
	return nil
}

func (r *Runner) fail(ctx context.Context, run *domain.Run, code string, cause error) error {
	run.State = domain.RunStateFailed
	run.ReasonCode = code
	if err := r.saveRun(context.WithoutCancel(ctx), run); err != nil {
		return err
	}
	return fmt.Errorf("%s: %w", code, cause)
}

func accountModelResult(run *domain.Run, result domain.ModelResult) {
	modelCalls := int64(result.ModelCalls)
	if modelCalls == 0 {
		modelCalls = 1
	}
	run.Budget.Consumed.ModelCalls += modelCalls
	outputBytes := result.OutputBytes
	if outputBytes == 0 {
		outputBytes = int64(len(result.Content))
	}
	run.Budget.Consumed.OutputBytes += outputBytes
}

func reconcileInvocationBudget(run *domain.Run, invocations []domain.Invocation) bool {
	var calls int64
	var outputBytes int64
	for _, invocation := range invocations {
		if !invocation.Authorized || invocation.State == domain.InvocationRejected {
			continue
		}
		calls++
		outputBytes += invocation.OutputBytes
	}
	changed := false
	if run.Budget.Consumed.ToolCalls < calls {
		run.Budget.Consumed.ToolCalls = calls
		changed = true
	}
	if run.Budget.Consumed.OutputBytes < outputBytes {
		run.Budget.Consumed.OutputBytes = outputBytes
		changed = true
	}
	return changed
}

func operationContext(parent context.Context, run domain.Run) (context.Context, context.CancelFunc) {
	if run.Budget.Limits.MaxElapsed <= 0 || run.StartedAt.IsZero() {
		return context.WithCancel(parent)
	}
	return context.WithDeadline(parent, run.StartedAt.Add(run.Budget.Limits.MaxElapsed))
}

func budgetReason(run domain.Run, now time.Time, beforeModel, beforeTool bool, additionalOutput int64) string {
	limits := run.Budget.Limits
	used := run.Budget.Consumed
	if limits.MaxElapsed > 0 && !run.StartedAt.IsZero() && now.Sub(run.StartedAt) >= limits.MaxElapsed {
		return "elapsed_budget_exhausted"
	}
	if limits.MaxModelCalls > 0 && ((beforeModel && used.ModelCalls >= limits.MaxModelCalls) || used.ModelCalls > limits.MaxModelCalls) {
		return "model_budget_exhausted"
	}
	if limits.MaxToolCalls > 0 && ((beforeTool && used.ToolCalls >= limits.MaxToolCalls) || used.ToolCalls > limits.MaxToolCalls) {
		return "tool_budget_exhausted"
	}
	if limits.MaxOutputBytes > 0 && used.OutputBytes+additionalOutput > limits.MaxOutputBytes {
		return "output_budget_exhausted"
	}
	return ""
}

func modelGroup(user string, result domain.ModelResult) ([]domain.ModelMessage, error) {
	history := NewPairedHistory(nil, DefaultHistoryBounds())
	if err := history.CommitModelTurn(user, result); err != nil {
		return nil, err
	}
	return history.messages, nil
}

func validateModelArtifacts(artifacts []domain.Artifact) error {
	for _, artifact := range artifacts {
		if artifact.Provenance != "" && artifact.Provenance != domain.ProvenanceModel {
			return errors.New("profile cannot create observed or verified artifacts")
		}
		artifact.Provenance = domain.ProvenanceModel
		if err := artifact.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func prepareModelArtifact(artifact *domain.Artifact, runID, id string, now time.Time) {
	artifact.ID = id
	artifact.RunID = runID
	artifact.InvocationID = ""
	artifact.Provenance = domain.ProvenanceModel
	artifact.Subject = nil
	artifact.CreatedAt = now
}

func prepareToolArtifacts(artifacts []domain.Artifact, runID, invocationID string, newID func() string, now func() time.Time) ([]domain.Artifact, error) {
	prepared := make([]domain.Artifact, len(artifacts))
	for index, artifact := range artifacts {
		artifact.ID = strings.TrimSpace(artifact.ID)
		if artifact.ID == "" {
			artifact.ID = newID()
		}
		artifact.RunID = runID
		artifact.InvocationID = invocationID
		artifact.CreatedAt = now()
		if artifact.Provenance == domain.ProvenanceModel {
			return nil, errors.New("tool executor cannot create model-authored artifacts")
		}
		if err := artifact.Validate(); err != nil {
			return nil, err
		}
		prepared[index] = artifact
	}
	return prepared, nil
}

func argumentsDigest(arguments map[string]any) string {
	encoded, _ := json.Marshal(arguments)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func nextInvocationSequence(invocations []domain.Invocation) int64 {
	var sequence int64
	for _, invocation := range invocations {
		if invocation.Sequence > sequence {
			sequence = invocation.Sequence
		}
	}
	return sequence + 1
}

func randomID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err == nil {
		return hex.EncodeToString(value[:])
	}
	// Preserve service availability if the OS entropy source fails. The timestamp and
	// call-local address provide a process-local fallback without global mutable state.
	fallback := sha256.Sum256([]byte(fmt.Sprintf("%d:%p", time.Now().UnixNano(), &value)))
	return hex.EncodeToString(fallback[:16])
}
