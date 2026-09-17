package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"mendry/backend/internal/modules/agentcore/domain"
)

// CompletionEvaluation 是 evaluator 的确定性输出。
type CompletionEvaluation struct {
	Complete bool
	Reason   string
}

// CompletionEvaluator 根据 trusted profile contract 和 durable artifacts 判断完成。
type CompletionEvaluator interface {
	Evaluate(context.Context, domain.CompletionContract, []domain.Artifact) (CompletionEvaluation, error)
}

// CompletionEvaluatorFunc 便于 composition 注册自定义 evaluator，而无需修改 runner。
type CompletionEvaluatorFunc func(context.Context, domain.CompletionContract, []domain.Artifact) (CompletionEvaluation, error)

// Evaluate 实现 CompletionEvaluator。
func (f CompletionEvaluatorFunc) Evaluate(ctx context.Context, contract domain.CompletionContract, artifacts []domain.Artifact) (CompletionEvaluation, error) {
	return f(ctx, contract, artifacts)
}

// EvaluatorRegistry 是 instance-owned completion extension registry。
type EvaluatorRegistry struct {
	mu         sync.RWMutex
	evaluators map[string]CompletionEvaluator
}

// NewEvaluatorRegistry 注册内置 v1 evaluator。
func NewEvaluatorRegistry() *EvaluatorRegistry {
	registry := &EvaluatorRegistry{evaluators: make(map[string]CompletionEvaluator)}
	_ = registry.Register(domain.CompletionSolutionDelivered, "v1", CompletionEvaluatorFunc(evaluateSolution))
	_ = registry.Register(domain.CompletionActionWithVerification, "v1", CompletionEvaluatorFunc(evaluateActionVerification))
	_ = registry.Register(domain.CompletionObservedRemoteBranch, "v1", CompletionEvaluatorFunc(evaluateObservedBranch))
	_ = registry.Register(domain.CompletionPipelineVerification, "v1", CompletionEvaluatorFunc(evaluatePipelineVerification))
	return registry
}

// Register 添加 evaluator；同 mode/version 冲突时 fail closed。
func (r *EvaluatorRegistry) Register(mode domain.CompletionMode, version string, evaluator CompletionEvaluator) error {
	if r == nil || evaluator == nil {
		return errors.New("completion evaluator is required")
	}
	if strings.TrimSpace(string(mode)) == "" || !toolVersionPattern.MatchString(version) {
		return errors.New("completion mode and vN version are required")
	}
	key := string(mode) + "\x00" + version
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.evaluators[key]; exists {
		return errors.New("completion evaluator already registered")
	}
	r.evaluators[key] = evaluator
	return nil
}

// Evaluate 只解析 composition 已注册的 mode/version，不执行 event/model 提供的代码。
func (r *EvaluatorRegistry) Evaluate(ctx context.Context, contract domain.CompletionContract, artifacts []domain.Artifact) (CompletionEvaluation, error) {
	if r == nil {
		return CompletionEvaluation{}, errors.New("completion registry is nil")
	}
	r.mu.RLock()
	evaluator := r.evaluators[string(contract.Mode)+"\x00"+contract.Version]
	r.mu.RUnlock()
	if evaluator == nil {
		return CompletionEvaluation{}, fmt.Errorf("completion evaluator %s/%s is not registered", contract.Mode, contract.Version)
	}
	return evaluator.Evaluate(ctx, contract, append([]domain.Artifact(nil), artifacts...))
}

func evaluateSolution(_ context.Context, contract domain.CompletionContract, artifacts []domain.Artifact) (CompletionEvaluation, error) {
	artifactType := parameterString(contract.Parameters, "artifactType", "solution")
	for _, artifact := range artifacts {
		if artifact.Type == artifactType && artifact.Provenance == domain.ProvenanceModel {
			return CompletionEvaluation{Complete: true, Reason: "solution_delivered"}, nil
		}
	}
	return CompletionEvaluation{Reason: "solution_artifact_required"}, nil
}

func evaluateActionVerification(_ context.Context, contract domain.CompletionContract, artifacts []domain.Artifact) (CompletionEvaluation, error) {
	actionType := parameterString(contract.Parameters, "actionType", "action")
	verificationType := parameterString(contract.Parameters, "verificationType", "verification")
	return linkedVerification(artifacts, actionType, verificationType)
}

func evaluateObservedBranch(_ context.Context, contract domain.CompletionContract, artifacts []domain.Artifact) (CompletionEvaluation, error) {
	actionType := parameterString(contract.Parameters, "artifactType", "scm.branch")
	for _, artifact := range artifacts {
		if artifact.Type == actionType && artifact.Provenance == domain.ProvenanceObserved && artifact.ExternalID != "" {
			return CompletionEvaluation{Complete: true, Reason: "remote_branch_observed"}, nil
		}
	}
	return CompletionEvaluation{Reason: "observed_remote_branch_required"}, nil
}

func evaluatePipelineVerification(_ context.Context, contract domain.CompletionContract, artifacts []domain.Artifact) (CompletionEvaluation, error) {
	actionType := parameterString(contract.Parameters, "actionType", "pipeline.run")
	verificationType := parameterString(contract.Parameters, "verificationType", "pipeline.verification")
	return linkedVerification(artifacts, actionType, verificationType)
}

func linkedVerification(artifacts []domain.Artifact, actionType, verificationType string) (CompletionEvaluation, error) {
	actionsByID := make(map[string]domain.Artifact)
	actionsByExternalID := make(map[string]domain.Artifact)
	for _, artifact := range artifacts {
		if artifact.Type == actionType && artifact.Provenance == domain.ProvenanceObserved {
			actionsByID[artifact.ID] = artifact
			if artifact.ExternalID != "" {
				actionsByExternalID[artifact.ExternalID] = artifact
			}
		}
	}
	for _, artifact := range artifacts {
		// verified 表示可信来源；只有明确 passed 的检查才能证明 action 已完成。
		if artifact.Type != verificationType || artifact.Provenance != domain.ProvenanceVerified || artifact.Subject == nil || artifact.VerificationStatus != domain.VerificationPassed {
			continue
		}
		_, byID := actionsByID[artifact.Subject.ArtifactID]
		_, byExternalID := actionsByExternalID[artifact.Subject.ExternalID]
		if byID || byExternalID {
			return CompletionEvaluation{Complete: true, Reason: "action_verified"}, nil
		}
	}
	return CompletionEvaluation{Reason: "linked_observed_action_and_verification_required"}, nil
}

func parameterString(parameters map[string]any, name, fallback string) string {
	value, _ := parameters[name].(string)
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
