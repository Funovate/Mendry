package application

import (
	"context"
	"errors"

	"mendry/backend/internal/modules/agentcore/domain"
)

// ModelStep 在共享 acceptance boundary 上执行 provider call，并只提交已验证响应。
type ModelStep struct {
	provider domain.ModelProvider
}

// NewModelStep 创建 provider-neutral model step。
func NewModelStep(provider domain.ModelProvider) (*ModelStep, error) {
	if provider == nil {
		return nil, errors.New("model provider is required")
	}
	return &ModelStep{provider: provider}, nil
}

// StepRequest 把 bounded history、profile validation 与 accepted commit 组合为一轮。
type StepRequest struct {
	Turn     domain.ModelTurn
	History  []domain.ModelMessage
	Validate func(domain.ModelResult) error
	Commit   func(string, domain.ModelResult) error
}

// Execute 调用 provider，验证完整响应，再提交 accepted history；失败响应不进入历史。
func (s *ModelStep) Execute(ctx context.Context, request StepRequest) (domain.ModelResult, error) {
	if s == nil || s.provider == nil {
		return domain.ModelResult{}, errors.New("model step is not configured")
	}
	request.Turn.Messages = SelectPairedHistory(request.History, DefaultHistoryBounds())
	result, err := s.provider.Complete(ctx, request.Turn)
	if err != nil {
		return result, err
	}
	if result.Content != "" && len(result.ToolCalls) > 0 {
		return result, errors.New("model content and tool calls cannot be mixed")
	}
	if request.Validate != nil {
		if err := request.Validate(result); err != nil {
			return result, err
		}
	}
	if request.Commit != nil {
		user := request.Turn.Continuation
		if user == "" {
			user = request.Turn.UserMessage
		}
		if err := request.Commit(user, result); err != nil {
			return result, err
		}
	}
	return result, nil
}
