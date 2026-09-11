package application_test

import (
	"testing"

	"mendry/backend/internal/modules/agentcore/application"
	"mendry/backend/internal/modules/agentcore/domain"
)

func TestSelectPairedHistoryKeepsCompleteGroupsAndCopiesArguments(t *testing.T) {
	messages := []domain.ModelMessage{
		{Role: "user", Content: "orphaned oldest"},
		{Role: "assistant", ToolCalls: []domain.ToolCall{{ID: "incomplete", Name: "repo.read", Arguments: map[string]any{"path": "old.go"}}}},
		{Role: "user", Content: "complete"},
		{Role: "assistant", ToolCalls: []domain.ToolCall{{ID: "call-1", Name: "repo.read", Arguments: map[string]any{"path": "main.go"}}}},
		{Role: "tool", ToolCallID: "call-1", Content: "result"},
	}

	history := application.SelectPairedHistory(messages, application.DefaultHistoryBounds())
	if len(history) != 3 || history[0].Content != "complete" || history[2].ToolCallID != "call-1" {
		t.Fatalf("history = %#v", history)
	}
	history[1].ToolCalls[0].Arguments["path"] = "mutated.go"
	if messages[3].ToolCalls[0].Arguments["path"] != "main.go" {
		t.Fatalf("source arguments mutated: %#v", messages[3].ToolCalls[0].Arguments)
	}
}

func TestPairedHistoryRejectsOrphanAndDuplicateToolMessages(t *testing.T) {
	history := application.NewPairedHistory(nil, application.DefaultHistoryBounds())
	if err := history.AppendToolResult("missing", "result"); err == nil {
		t.Fatal("orphan tool result was accepted")
	}
	if err := history.CommitModelTurn("user", domain.ModelResult{ToolCalls: []domain.ToolCall{{ID: "call-1", Name: "repo.read"}}}); err != nil {
		t.Fatalf("commit tool call: %v", err)
	}
	if err := history.AppendToolResult("call-1", "result"); err != nil {
		t.Fatalf("append tool result: %v", err)
	}
	if err := history.AppendToolResult("call-1", "duplicate"); err == nil {
		t.Fatal("duplicate tool result was accepted")
	}
	if got := history.Snapshot(); len(got) != 3 {
		t.Fatalf("paired snapshot length = %d", len(got))
	}
}
