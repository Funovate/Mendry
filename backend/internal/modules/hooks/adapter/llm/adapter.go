// Package llm 将现有 OpenAI-compatible client 适配为 webhook 归一化端口。
package llm

import (
	"context"
	"fmt"

	hookapplication "mendry/backend/internal/modules/hooks/application"
	remediationdomain "mendry/backend/internal/modules/remediation/domain"
)

// Adapter 在不向 hooks 暴露 provider 类型的前提下复用 remediation LLM port。
type Adapter struct {
	client remediationdomain.LLMProviderPort
}

// NewAdapter 在现有 LLM provider 上构造 webhook classifier adapter。
func NewAdapter(client remediationdomain.LLMProviderPort) (*Adapter, error) {
	if client == nil {
		return nil, fmt.Errorf("webhook LLM client is required")
	}
	return &Adapter{client: client}, nil
}

var _ hookapplication.ModelPort = (*Adapter)(nil)

// Complete 执行一次无 tools、bounded 的 webhook grouping 模型调用。
func (a *Adapter) Complete(ctx context.Context, request hookapplication.ModelRequest) (hookapplication.ModelResponse, error) {
	result, err := a.client.Complete(ctx, remediationdomain.ModelTurn{
		ProjectID: request.ProjectID, SystemPrompt: request.SystemPrompt,
		UserMessage: request.UserMessage, MaxTokens: request.MaxTokens,
		Temperature: request.Temperature,
	})
	if err != nil {
		return hookapplication.ModelResponse{}, err
	}
	return hookapplication.ModelResponse{Content: result.Content}, nil
}
