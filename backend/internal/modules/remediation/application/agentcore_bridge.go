package application

import (
	"context"

	coreapplication "mendry/backend/internal/modules/agentcore/application"
	coredomain "mendry/backend/internal/modules/agentcore/domain"
	"mendry/backend/internal/modules/remediation/domain"
)

type sharedModelProvider struct {
	provider domain.LLMProviderPort
}

func (p sharedModelProvider) Complete(ctx context.Context, turn coredomain.ModelTurn) (coredomain.ModelResult, error) {
	result, err := p.provider.Complete(ctx, fromCoreModelTurn(turn))
	return toCoreModelResult(result), err
}

func newSharedModelStep(provider domain.LLMProviderPort) *coreapplication.ModelStep {
	step, err := coreapplication.NewModelStep(sharedModelProvider{provider: provider})
	if err != nil {
		return nil
	}
	return step
}

func coreStepRequest(
	turn domain.ModelTurn,
	validate func(domain.ModelResult) error,
	commit func(string, domain.ModelResult) error,
) coreapplication.StepRequest {
	return coreapplication.StepRequest{
		Turn:    toCoreModelTurn(turn),
		History: toCoreMessages(turn.Messages),
		Validate: func(result coredomain.ModelResult) error {
			return validate(fromCoreModelResult(result, domain.ModelResult{}))
		},
		Commit: func(user string, result coredomain.ModelResult) error {
			return commit(user, fromCoreModelResult(result, domain.ModelResult{}))
		},
	}
}

func toCoreModelTurn(turn domain.ModelTurn) coredomain.ModelTurn {
	return coredomain.ModelTurn{
		Binding:      turn.ProjectID,
		SystemPrompt: turn.SystemPrompt,
		UserMessage:  turn.UserMessage,
		Continuation: turn.Continuation,
		Messages:     toCoreMessages(turn.Messages),
		Tools:        toCoreDefinitions(turn.Tools),
		MaxTokens:    turn.MaxTokens,
		Temperature:  turn.Temperature,
	}
}

func fromCoreModelTurn(turn coredomain.ModelTurn) domain.ModelTurn {
	return domain.ModelTurn{
		ProjectID:    turn.Binding,
		SystemPrompt: turn.SystemPrompt,
		UserMessage:  turn.UserMessage,
		Continuation: turn.Continuation,
		Messages:     fromCoreMessages(turn.Messages),
		Tools:        fromCoreDefinitions(turn.Tools),
		MaxTokens:    turn.MaxTokens,
		Temperature:  turn.Temperature,
	}
}

func toCoreModelResult(result domain.ModelResult) coredomain.ModelResult {
	return coredomain.ModelResult{
		Content: result.Content, Provider: result.Provider, Model: result.Model,
		ToolCalls: toCoreCalls(result.ToolCalls), ModelCalls: result.ModelCalls,
		InputTokens: result.UsageTokensIn, OutputTokens: result.UsageTokensOut, UsageTokens: result.UsageTokens,
		UsageCostCents: result.UsageCostCents, OutputBytes: int64(len(result.Content)),
		RequestBytes: result.RequestBytes, ToolCount: result.ToolCount, ToolSchemaBytes: result.ToolSchemaBytes,
		CacheTokensReported: result.CacheTokensReported, CacheHitTokens: result.CacheHitTokens,
		CacheMissTokens: result.CacheMissTokens, FinishReason: result.FinishReason,
	}
}

func fromCoreModelResult(result coredomain.ModelResult, original domain.ModelResult) domain.ModelResult {
	original.Content = result.Content
	original.Provider = result.Provider
	original.Model = result.Model
	original.ToolCalls = fromCoreCalls(result.ToolCalls)
	original.ModelCalls = result.ModelCalls
	original.UsageTokensIn = result.InputTokens
	original.UsageTokensOut = result.OutputTokens
	original.UsageTokens = result.UsageTokens
	original.UsageCostCents = result.UsageCostCents
	original.RequestBytes = result.RequestBytes
	original.ToolCount = result.ToolCount
	original.ToolSchemaBytes = result.ToolSchemaBytes
	original.CacheTokensReported = result.CacheTokensReported
	original.CacheHitTokens = result.CacheHitTokens
	original.CacheMissTokens = result.CacheMissTokens
	original.FinishReason = result.FinishReason
	return original
}

func toCoreMessages(messages []domain.ModelMessage) []coredomain.ModelMessage {
	converted := make([]coredomain.ModelMessage, len(messages))
	for index, message := range messages {
		converted[index] = coredomain.ModelMessage{Role: message.Role, Content: message.Content, ToolCallID: message.ToolCallID, ToolCalls: toCoreCalls(message.ToolCalls)}
	}
	return converted
}

func fromCoreMessages(messages []coredomain.ModelMessage) []domain.ModelMessage {
	converted := make([]domain.ModelMessage, len(messages))
	for index, message := range messages {
		converted[index] = domain.ModelMessage{Role: message.Role, Content: message.Content, ToolCallID: message.ToolCallID, ToolCalls: fromCoreCalls(message.ToolCalls)}
	}
	return converted
}

func toCoreCalls(calls []domain.ToolCall) []coredomain.ToolCall {
	converted := make([]coredomain.ToolCall, len(calls))
	for index, call := range calls {
		converted[index] = coredomain.ToolCall{ID: call.ID, Name: call.Name, Arguments: call.Arguments}
	}
	return converted
}

func fromCoreCalls(calls []coredomain.ToolCall) []domain.ToolCall {
	converted := make([]domain.ToolCall, len(calls))
	for index, call := range calls {
		converted[index] = domain.ToolCall{ID: call.ID, Name: call.Name, Arguments: call.Arguments}
	}
	return converted
}

func toCoreDefinitions(definitions []domain.ToolDefinition) []coredomain.ToolDefinition {
	converted := make([]coredomain.ToolDefinition, len(definitions))
	for index, definition := range definitions {
		converted[index] = coredomain.ToolDefinition{Name: definition.Name, Version: "v1", Description: definition.Description, Parameters: definition.Parameters, Effect: coredomain.ToolEffectRead}
	}
	return converted
}

func fromCoreDefinitions(definitions []coredomain.ToolDefinition) []domain.ToolDefinition {
	converted := make([]domain.ToolDefinition, len(definitions))
	for index, definition := range definitions {
		converted[index] = domain.ToolDefinition{Name: definition.Name, Description: definition.Description, Parameters: definition.Parameters}
	}
	return converted
}
