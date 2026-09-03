package application

import (
	"context"
	"fmt"

	"fixthe/backend/internal/modules/remediation/domain"
)

// appendToolRecoveryChallenge 把 resilient diagnosis/planning tool failure 统一
// 投影为 D5 challenge。原有 ToolResult observation 仍保留给模型，但只有安全
// code/message 和 durable recovery checkpoint 穿过本函数。
func (c *RemediationCoordinator) appendToolRecoveryChallenge(
	ctx context.Context,
	budget *runBudget,
	runID string,
	phase domain.RunState,
	req *RequestTool,
	cause error,
	conversation *AgentConversation,
) error {
	tracker := resilientStateFrom(ctx)
	if tracker == nil || req == nil {
		return nil
	}
	safe := classifyToolError(cause)
	tracker.recoveryAttempt++
	suggested := []string{"retry_transient", "use_fallback"}
	if safe.Code == string(RejectArguments) || safe.Code == string(RejectPathScope) || safe.Code == "connector_not_found" {
		suggested = []string{"correct_request", "use_fallback"}
	}
	challenge, err := NewRecoveryChallenge(
		domain.RecoveryChallengeKindToolFailure,
		domain.RecoverySeverityRecoverable,
		safe.Code,
		req.ToolName,
		tracker.recoveryCapabilities(),
		suggested,
		tracker.recoveryAttempt,
		budget.remaining(),
		fmt.Sprintf("The bounded tool %s failed: %s. Follow the suggested recovery class, use another advertised capability, or preserve the gap as unresolved evidence; the run remains active.", req.ToolName, safe.Message),
	)
	if err != nil {
		return c.fail(ctx, runID, phase, markPersistenceFailure(fmt.Errorf("build tool recovery challenge: %w", err)))
	}
	tracker.appendRecovery(domain.CheckpointRecovery{
		Kind: string(challenge.Kind), Action: req.ToolName, OutcomeRef: "challenge:" + challenge.ReasonCode,
	})
	if conversation != nil {
		conversation.AppendRecoveryChallenge(challenge)
	}
	if err := c.checkpointRun(ctx, tracker, phase, domain.CheckpointReasonRecovery); err != nil {
		return c.fail(ctx, runID, phase, markPersistenceFailure(err))
	}
	return nil
}
