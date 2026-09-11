package application

import (
	"context"

	"mendry/backend/internal/modules/remediation/domain"
)

func (c *RemediationCoordinator) observeLifecycleTool(ctx context.Context, phase domain.RunState, req *RequestTool, result ToolResult, cause error) {
	if req == nil {
		return
	}
	observation := ToolObservation{
		Run: observationRun(ctx), Phase: phase, Sequence: nextObservationSequence(ctx), Tool: req.ToolName,
		Outcome: "success", Parameters: boundedConversationMap(req.Parameters),
		Result: boundedConversationValue(result.Payload, maxObservationBytes), Bytes: result.BytesRetrieved,
	}
	if cause != nil {
		observation.Outcome = "failure"
		observation.FailureClass = "adapter"
		observation.ErrorCode, observation.Retryable = lifecycleErrorInfo(cause)
		if rejection, ok := RejectionCode(cause); ok {
			observation.Outcome = "rejected"
			observation.FailureClass = "policy"
			observation.RejectionCode = rejection
		}
	}
	normalizeRunObserver(c.observer).ToolCompleted(ctx, observation)
}
