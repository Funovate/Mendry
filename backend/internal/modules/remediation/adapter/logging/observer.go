package logging

import (
	"context"
	"fmt"
	"log/slog"

	"fixthe/backend/internal/modules/remediation/application"
	"fixthe/backend/internal/modules/remediation/domain"
	"fixthe/backend/internal/platform/observability"
)

// Observer 把 application observation 投影为稳定的 backend console/JSON 事件。
type Observer struct {
	logger *slog.Logger
}

// NewObserver 构造 remediation logging observer。
func NewObserver(logger *slog.Logger) (*Observer, error) {
	if logger == nil {
		return nil, fmt.Errorf("remediation observer logger is required")
	}
	return &Observer{logger: logger}, nil
}

var _ application.RunObserver = (*Observer)(nil)

// RunStarted 记录已持久化 queued run 的启动身份。
func (o *Observer) RunStarted(ctx context.Context, rec application.RunStartedObservation) {
	attrs := append(runAttrs(rec.Run),
		slog.String(observability.FieldPhase, string(rec.Phase)),
		slog.String("trigger_reason", rec.TriggerReason),
		slog.String("priority", rec.Priority),
		slog.String(observability.FieldOutcome, "started"),
	)
	observability.Log(ctx, o.logger, slog.LevelInfo, observability.EventRemediationRunStarted, "remediation run started", attrs...)
}

// StateTransitioned 记录成功提交后的状态迁移。
func (o *Observer) StateTransitioned(ctx context.Context, rec application.StateTransitionObservation) {
	outcome := "success"
	if rec.To == domain.RunStateBudgetExhausted {
		outcome = "stopped"
	}
	attrs := append(runAttrs(rec.Run),
		slog.String(observability.FieldFromState, string(rec.From)),
		slog.String(observability.FieldToState, string(rec.To)),
		slog.String(observability.FieldPhase, string(rec.To)),
		slog.Int64(observability.FieldModelCalls, int64(rec.Effect.ModelCalls)),
		slog.Int64(observability.FieldToolCalls, int64(rec.Effect.ToolCalls)),
		slog.Int64(observability.FieldModelTokensIn, rec.Effect.ModelTokensIn),
		slog.Int64(observability.FieldModelTokensOut, rec.Effect.ModelTokensOut),
		slog.Int64(observability.FieldRepositoryBytes, rec.Effect.RepositoryBytes),
		slog.Int64(observability.FieldEvidenceBytes, rec.Effect.EvidenceBytes),
		slog.String(observability.FieldOutcome, outcome),
	)
	if rec.BudgetExhaustedReason != "" {
		attrs = append(attrs, slog.String(observability.FieldBudgetExhaustedReason, rec.BudgetExhaustedReason))
	}
	observability.Log(ctx, o.logger, slog.LevelInfo, observability.EventRemediationStateTransition, "remediation state transitioned", attrs...)
}

// ContextCompleted 记录 repository/evidence 操作及 DEBUG payload。
func (o *Observer) ContextCompleted(ctx context.Context, rec application.ContextObservation) {
	attrs := append(runAttrs(rec.Run),
		slog.String(observability.FieldPhase, string(rec.Phase)),
		slog.String("operation", rec.Operation),
		slog.Int64(observability.FieldDurationMS, rec.Duration.Milliseconds()),
		slog.Int64("bytes", rec.Bytes),
		slog.String(observability.FieldOutcome, rec.Outcome),
	)
	if rec.FailureClass != "" {
		attrs = append(attrs, slog.String(observability.FieldErrorClass, rec.FailureClass))
	}
	observability.Log(ctx, o.logger, progressLevel(rec.Outcome), observability.EventRemediationContextComplete, "remediation context operation completed", attrs...)
	o.payload(ctx, observability.EventRemediationContextPayload, rec.Run, rec.Phase, 0, rec.PayloadKind, rec.Payload)
}

// ModelTurnCompleted 记录模型轮次 metadata 及逻辑 request/response。
func (o *Observer) ModelTurnCompleted(ctx context.Context, rec application.ModelTurnObservation) {
	modelCalls := rec.Response.ModelCalls
	if modelCalls <= 0 {
		modelCalls = 1
	}
	attrs := append(runAttrs(rec.Run),
		slog.String(observability.FieldPhase, string(rec.Phase)),
		slog.Int64(observability.FieldSequence, rec.Sequence),
		slog.Int64(observability.FieldDurationMS, rec.Duration.Milliseconds()),
		slog.String("envelope_kind", rec.EnvelopeKind),
		slog.String(observability.FieldLLMModel, rec.Response.Model),
		slog.Int(observability.FieldModelCalls, modelCalls),
		slog.Int64(observability.FieldModelTokensIn, rec.Response.UsageTokensIn),
		slog.Int64(observability.FieldModelTokensOut, rec.Response.UsageTokensOut),
		slog.Int64(observability.FieldRequestBytes, rec.Response.RequestBytes),
		slog.Int(observability.FieldToolCount, rec.Response.ToolCount),
		slog.Int64(observability.FieldToolSchemaBytes, rec.Response.ToolSchemaBytes),
		slog.String(observability.FieldOutcome, rec.Outcome),
	)
	if rec.Response.FinishReason != "" {
		attrs = append(attrs, slog.String(observability.FieldModelFinishReason, rec.Response.FinishReason))
	}
	if rec.Response.CacheTokensReported {
		attrs = append(attrs,
			slog.Int64(observability.FieldModelCacheHitTokens, rec.Response.CacheHitTokens),
			slog.Int64(observability.FieldModelCacheMissTokens, rec.Response.CacheMissTokens),
		)
	}
	if rec.FailureClass != "" {
		attrs = append(attrs, slog.String(observability.FieldErrorClass, rec.FailureClass))
	}
	attrs = appendDiagnostic(attrs, rec.ErrorMessage)
	observability.Log(ctx, o.logger, progressLevel(rec.Outcome), observability.EventRemediationModelComplete, "remediation model turn completed", attrs...)
	o.payload(ctx, observability.EventRemediationModelPayload, rec.Run, rec.Phase, rec.Sequence, "model_request", rec.Request)
	if rec.Response.Content != "" {
		o.payload(ctx, observability.EventRemediationModelPayload, rec.Run, rec.Phase, rec.Sequence, "model_response", rec.Response)
	}
}

// ToolCompleted 记录 tool metadata、policy rejection 及安全 request/result。
func (o *Observer) ToolCompleted(ctx context.Context, rec application.ToolObservation) {
	attrs := append(runAttrs(rec.Run),
		slog.String(observability.FieldPhase, string(rec.Phase)),
		slog.Int64(observability.FieldSequence, rec.Sequence),
		slog.String(observability.FieldToolName, rec.Tool),
		slog.Int64(observability.FieldDurationMS, rec.Duration.Milliseconds()),
		slog.Int64("bytes", rec.Bytes),
		slog.String(observability.FieldOutcome, rec.Outcome),
	)
	if rec.FailureClass != "" {
		attrs = append(attrs, slog.String(observability.FieldErrorClass, rec.FailureClass))
	}
	if rec.RejectionCode != "" {
		attrs = append(attrs, slog.String(observability.FieldToolRejectionCode, string(rec.RejectionCode)))
	}
	if rec.Outcome != "success" {
		if rec.ErrorCode != "" {
			attrs = append(attrs, slog.String(observability.FieldErrorCode, rec.ErrorCode))
		}
		attrs = append(attrs, slog.Bool(observability.FieldRetryable, rec.Retryable))
		attrs = appendDiagnostic(attrs, rec.ErrorMessage)
	}
	observability.Log(ctx, o.logger, progressLevel(rec.Outcome), observability.EventRemediationToolComplete, "remediation tool call completed", attrs...)
	o.payload(ctx, observability.EventRemediationToolPayload, rec.Run, rec.Phase, rec.Sequence, "tool_parameters", rec.Parameters)
	if rec.Result != nil {
		o.payload(ctx, observability.EventRemediationToolPayload, rec.Run, rec.Phase, rec.Sequence, "tool_result", rec.Result)
	}
}

// RunCompleted 记录 terminal state 与最终预算计数。
func (o *Observer) RunCompleted(ctx context.Context, rec application.RunCompletedObservation) {
	attrs := append(runAttrs(rec.Run),
		slog.String(observability.FieldTerminalState, string(rec.TerminalState)),
		slog.String(observability.FieldPhase, string(rec.TerminalState)),
		slog.Int64(observability.FieldDurationMS, rec.Duration.Milliseconds()),
		slog.Int64(observability.FieldModelCalls, rec.Aggregate.Run.Budget.ModelCalls),
		slog.Int64(observability.FieldToolCalls, rec.Aggregate.Run.Budget.ToolCalls),
		slog.Int64(observability.FieldModelTokens, rec.Aggregate.Run.Budget.ModelTokens),
		slog.Int64(observability.FieldRepositoryBytes, rec.Aggregate.Run.Budget.RepositoryBytes),
		slog.Int64(observability.FieldEvidenceBytes, rec.Aggregate.Run.Budget.EvidenceBytes),
		slog.String(observability.FieldOutcome, rec.Outcome),
	)
	observability.Log(ctx, o.logger, progressLevel(rec.Outcome), observability.EventRemediationRunCompleted, "remediation run completed", attrs...)
}

func (o *Observer) payload(ctx context.Context, event string, run application.RunIdentity, phase domain.RunState, sequence int64, kind string, value any) {
	if value == nil || kind == "" {
		return
	}
	snapshot, err := observability.SnapshotRemediationPayload(value)
	if err != nil || snapshot.Text == "" {
		return
	}
	attrs := append(runAttrs(run),
		slog.String(observability.FieldPhase, string(phase)),
		slog.String(observability.FieldPayloadKind, kind),
		slog.String(observability.FieldPayload, snapshot.Text),
		slog.Int(observability.FieldPayloadBytes, snapshot.Bytes),
		slog.Int(observability.FieldPayloadLoggedBytes, snapshot.LoggedBytes),
		slog.Bool(observability.FieldPayloadTruncated, snapshot.Truncated),
		slog.String(observability.FieldPayloadSHA256, snapshot.SHA256),
	)
	if sequence > 0 {
		attrs = append(attrs, slog.Int64(observability.FieldSequence, sequence))
	}
	observability.Log(ctx, o.logger, slog.LevelDebug, event, "remediation payload", attrs...)
}

func runAttrs(run application.RunIdentity) []slog.Attr {
	attrs := []slog.Attr{
		slog.String(observability.FieldComponent, "remediation"),
		slog.String(observability.FieldRunID, run.RunID),
	}
	if run.SeriesID != "" {
		attrs = append(attrs, slog.String(observability.FieldSeriesID, run.SeriesID))
	}
	if run.IncidentID != "" {
		attrs = append(attrs, slog.String(observability.FieldIncidentID, run.IncidentID))
	}
	if run.LifecycleGeneration > 0 {
		attrs = append(attrs, slog.Int64(observability.FieldLifecycleGeneration, run.LifecycleGeneration))
	}
	return attrs
}

func progressLevel(outcome string) slog.Level {
	if outcome == "failure" {
		return slog.LevelError
	}
	return slog.LevelInfo
}

func appendDiagnostic(attrs []slog.Attr, raw string) []slog.Attr {
	message, truncated := observability.SnapshotDiagnostic(raw)
	if message == "" {
		return attrs
	}
	attrs = append(attrs, slog.String(observability.FieldErrorMessage, message))
	if truncated {
		attrs = append(attrs, slog.Bool(observability.FieldErrorMessageTruncated, true))
	}
	return attrs
}
