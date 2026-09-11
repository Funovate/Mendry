package application

import (
	"context"
	"errors"
	"testing"

	coreapplication "mendry/backend/internal/modules/agentcore/application"
	coredomain "mendry/backend/internal/modules/agentcore/domain"
	"mendry/backend/internal/modules/remediation/domain"
)

func TestRemediationBridgeUsesSharedModelStepAcceptanceBoundary(t *testing.T) {
	provider := &bridgeModelProvider{result: domain.ModelResult{Content: "rejected"}}
	step := newSharedModelStep(provider)
	committed := false
	_, err := step.Execute(context.Background(), coreapplication.StepRequest{
		Turn: coredomain.ModelTurn{UserMessage: "diagnose"},
		Validate: func(coredomain.ModelResult) error {
			return errors.New("profile rejected response")
		},
		Commit: func(string, coredomain.ModelResult) error {
			committed = true
			return nil
		},
	})
	if err == nil || committed || provider.calls != 1 {
		t.Fatalf("err=%v committed=%v calls=%d", err, committed, provider.calls)
	}
}

type bridgeModelProvider struct {
	result domain.ModelResult
	calls  int
}

func (p *bridgeModelProvider) Complete(context.Context, domain.ModelTurn) (domain.ModelResult, error) {
	p.calls++
	return p.result, nil
}
