// Package logging projects asynchronous webhook failures into safe structured logs.
package logging

import (
	"context"
	"fmt"
	"log/slog"

	hookapplication "mendry/backend/internal/modules/hooks/application"
	"mendry/backend/internal/platform/errtrace"
	"mendry/backend/internal/platform/observability"
)

const eventWebhookIngestFailed = "webhook.ingest.failed"

// FailureReporter records failures that happen after the webhook client received 202.
type FailureReporter struct {
	logger *slog.Logger
}

// NewFailureReporter constructs the hooks background failure reporter.
func NewFailureReporter(logger *slog.Logger) (*FailureReporter, error) {
	if logger == nil {
		return nil, fmt.Errorf("webhook failure logger is required")
	}
	return &FailureReporter{logger: logger}, nil
}

var _ hookapplication.FailureReporter = (*FailureReporter)(nil)

// Report logs scope and diagnostics without the webhook token or raw payload.
func (r *FailureReporter) Report(ctx context.Context, failure hookapplication.BackgroundFailure, cause error) {
	if cause == nil {
		return
	}
	stack, ok := errtrace.FromError(cause)
	stackSource := "webhook_background_boundary"
	if ok {
		stackSource = "wrapped_error"
	} else {
		stack = errtrace.Capture(1)
	}
	observability.Log(ctx, r.logger, slog.LevelError, eventWebhookIngestFailed, "webhook background ingestion failed",
		slog.String(observability.FieldComponent, "hooks"),
		slog.String("project_id", failure.ProjectID),
		slog.String("source_id", failure.SourceID),
		slog.Time("occurred_at", failure.OccurredAt),
		slog.String(observability.FieldErrorType, fmt.Sprintf("%T", cause)),
		slog.String(observability.FieldErrorMessage, cause.Error()),
		slog.String(observability.FieldErrorStack, stack.String()),
		slog.String(observability.FieldErrorStackSource, stackSource),
	)
}
