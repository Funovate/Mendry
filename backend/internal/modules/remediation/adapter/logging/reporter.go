// Package logging 将 remediation 失败投影为不含原始 payload 的结构化服务端日志。
package logging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	remediationapplication "mendry/backend/internal/modules/remediation/application"
	"mendry/backend/internal/platform/errtrace"
	"mendry/backend/internal/platform/observability"
)

const maximumErrorCauses = 32

// FailureReporter 记录 remediation coordinator 返回的完整错误链和栈。
type FailureReporter struct {
	logger *slog.Logger
}

// NewFailureReporter 构造异步 remediation 失败日志 reporter。
func NewFailureReporter(logger *slog.Logger) (*FailureReporter, error) {
	if logger == nil {
		return nil, fmt.Errorf("remediation failure logger is required")
	}
	return &FailureReporter{logger: logger}, nil
}

var (
	_ remediationapplication.FailureReporter = (*FailureReporter)(nil)
	_ remediationapplication.GateReporter    = (*FailureReporter)(nil)
)

// Report 记录失败原因、cause chain 和 origin stack，不记录 webhook/model payload。
func (r *FailureReporter) Report(ctx context.Context, request remediationapplication.TriggerRequest, cause error) {
	if cause == nil {
		return
	}
	stack, hasStack := errtrace.FromError(cause)
	stackSource := "remediation_boundary"
	if hasStack {
		stackSource = "wrapped_error"
	} else {
		stack = errtrace.Capture(1)
	}
	observability.Log(ctx, r.logger, slog.LevelError, observability.EventRemediationFailed, "remediation failed",
		slog.String(observability.FieldComponent, "remediation"),
		slog.String("incident_id", request.IncidentID),
		slog.Int64("lifecycle_generation", request.LifecycleGeneration),
		slog.String("priority", request.Priority),
		slog.String("reason", request.Reason),
		slog.String(observability.FieldErrorType, fmt.Sprintf("%T", cause)),
		slog.String(observability.FieldErrorMessage, cause.Error()),
		slog.Any(observability.FieldErrorCauses, collectErrorCauses(cause)),
		slog.String(observability.FieldErrorStack, stack.String()),
		slog.String(observability.FieldErrorStackSource, stackSource),
	)
}

func (r *FailureReporter) ReportGate(ctx context.Context, request remediationapplication.TriggerRequest, observation remediationapplication.GateObservation) {
	if observation.Outcome == "" || observation.Reason == "" {
		return
	}
	observability.Log(ctx, r.logger, slog.LevelInfo, observability.EventRemediationContinuationGate, "remediation continuation gate evaluated",
		slog.String(observability.FieldComponent, "remediation"),
		slog.String(observability.FieldIncidentID, request.IncidentID),
		slog.Int64(observability.FieldLifecycleGeneration, request.LifecycleGeneration),
		slog.Int64(observability.FieldContextVersion, request.ContextVersion),
		slog.String("priority", request.Priority),
		slog.String("trigger_reason", request.Reason),
		slog.String(observability.FieldGateOutcome, observation.Outcome),
		slog.String(observability.FieldGateReason, observation.Reason),
	)
}

type errorCause struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func collectErrorCauses(root error) []errorCause {
	causes := make([]errorCause, 0, maximumErrorCauses)
	pending := unwrapErrors(root)
	for len(pending) > 0 && len(causes) < maximumErrorCauses {
		current := pending[0]
		pending = pending[1:]
		if current == nil {
			continue
		}
		causes = append(causes, errorCause{Type: fmt.Sprintf("%T", current), Message: current.Error()})
		pending = append(pending, unwrapErrors(current)...)
	}
	return causes
}

func unwrapErrors(err error) []error {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		return joined.Unwrap()
	}
	if cause := errors.Unwrap(err); cause != nil {
		return []error{cause}
	}
	return nil
}
