package application_test

import (
	"context"
	"encoding/json"
	"testing"

	"mendry/backend/internal/modules/remediation/application"
	"mendry/backend/internal/modules/remediation/domain"
)

type largeStatusWorkspace struct {
	lifecycleWorkspaceFake
}

func (w *largeStatusWorkspace) Status(_ context.Context, identity domain.WorkspaceIdentity) (domain.WorkspaceStatus, error) {
	return domain.WorkspaceStatus{Identity: identity, Clean: true, Bytes: 289555756}, nil
}

func TestLifecycleStatusChargesReturnedMetadataNotWorkspaceSize(t *testing.T) {
	workspace := &largeStatusWorkspace{}
	identity := domain.WorkspaceIdentity{
		WorkspaceID: "workspace-1", RunID: "run-1", BaselineCommit: "baseline",
		BaseTreeHash: "tree-base", CurrentTreeHash: "tree-base", Version: 1,
	}
	gateway := application.NewLifecycleToolGateway(workspace, nil)
	for _, phase := range []domain.RunState{domain.RunStatePatching, domain.RunStateValidating} {
		t.Run(string(phase), func(t *testing.T) {
			result, err := gateway.Execute(context.Background(), phase, identity, nil, domain.ExecutionProfileSnapshot{}, domain.ChangePolicySnapshot{}, "status-1", application.ToolWorkspaceStatus, map[string]interface{}{})
			if err != nil {
				t.Fatal(err)
			}
			status, ok := result.Payload.(domain.WorkspaceStatus)
			if !ok || status.Bytes != 289555756 {
				t.Fatalf("workspace capacity was not preserved: %+v", result.Payload)
			}
			payload, err := json.Marshal(status)
			if err != nil {
				t.Fatal(err)
			}
			if result.BytesRetrieved != int64(len(payload)) || result.BytesRetrieved >= application.DefaultBudgetLimits().MaxRepositoryBytes {
				t.Fatalf("status retrieval bytes = %d, want metadata size %d", result.BytesRetrieved, len(payload))
			}
			file, err := gateway.Execute(context.Background(), phase, identity, nil, domain.ExecutionProfileSnapshot{}, domain.ChangePolicySnapshot{}, "read-1", application.ToolWorkspaceReadFile, map[string]interface{}{"path": "main.go"})
			if err != nil {
				t.Fatal(err)
			}
			if file.BytesRetrieved != int64(len("package main\n")) {
				t.Fatalf("file retrieval bytes = %d", file.BytesRetrieved)
			}
		})
	}
}
