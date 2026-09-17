package command

import (
	"context"
	"encoding/json"
	"strings"

	"mendry/backend/internal/modules/agentcore/domain"
)

// FixtureProvider is deterministic and offline. It is intentionally explicit:
// it is useful for onboarding and boundary smoke tests, not a claim of provider validation.
type FixtureProvider struct{}

// NewFixtureProvider constructs the offline provider.
func NewFixtureProvider() FixtureProvider { return FixtureProvider{} }

// Complete returns a structured report for report goals and a fixed sequence of
// trusted MCP calls for workspace/verification goals. The result is deterministic
// so tests can exercise the real file and stdio boundaries around the model port.
func (FixtureProvider) Complete(_ context.Context, turn domain.ModelTurn) (domain.ModelResult, error) {
	goal := strings.ToLower(turn.UserMessage)
	if turn.Continuation != "" {
		goal = strings.ToLower(turn.Continuation)
	}
	if lastToolResult(turn.Messages, "rejected") {
		return fixtureReport(turn.UserMessage, "refused by configured policy")
	}
	if strings.Contains(goal, "workspace") || strings.Contains(goal, "write") || strings.Contains(goal, "verify") {
		if !hasObservation(turn.Messages) {
			for _, tool := range turn.Tools {
				if strings.Contains(tool.Name, "write") {
					return domain.ModelResult{Model: "fixture", Provider: "fixture", ToolCalls: []domain.ToolCall{{ID: "fixture-write-1", Name: tool.Name, Version: tool.Version, Arguments: map[string]any{"path": "fixture/reversible.txt", "content": "reversible local example\n", "actionId": "fixture-action-1"}}}}, nil
				}
			}
		}
		if hasObservation(turn.Messages) && !lastToolResult(turn.Messages, "passed") {
			for _, tool := range turn.Tools {
				if strings.Contains(tool.Name, "verify") {
					return domain.ModelResult{Model: "fixture", Provider: "fixture", ToolCalls: []domain.ToolCall{{ID: "fixture-verify-1", Name: tool.Name, Version: tool.Version, Arguments: map[string]any{"path": "fixture/reversible.txt", "expected": "reversible local example\n", "actionId": "fixture-action-1"}}}}, nil
				}
			}
		}
	}
	return fixtureReport(turn.UserMessage, "complete")
}

func fixtureReport(goal, status string) (domain.ModelResult, error) {
	encoded, _ := json.Marshal(map[string]any{"kind": "structured-report", "goal": goal, "status": status, "source": "offline-fixture"})
	return domain.ModelResult{Model: "fixture", Provider: "fixture", Content: string(encoded)}, nil
}

func hasObservation(messages []domain.ModelMessage) bool {
	for _, message := range messages {
		if message.Role == "tool" {
			return true
		}
	}
	return false
}

func lastToolResult(messages []domain.ModelMessage, value string) bool {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role != "tool" {
			continue
		}
		return strings.Contains(strings.ToLower(messages[index].Content), strings.ToLower(value))
	}
	return false
}

var _ domain.ModelProvider = FixtureProvider{}
