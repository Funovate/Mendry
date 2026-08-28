package logging_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	hooklogging "fixthe/backend/internal/modules/hooks/adapter/logging"
	hookapplication "fixthe/backend/internal/modules/hooks/application"
)

func TestFailureReporterLogsSafeScopeAndDiagnostics(t *testing.T) {
	var output strings.Builder
	reporter, err := hooklogging.NewFailureReporter(slog.New(slog.NewJSONHandler(&output, nil)))
	if err != nil {
		t.Fatalf("NewFailureReporter() error = %v", err)
	}
	reporter.Report(context.Background(), hookapplication.BackgroundFailure{
		ProjectID: "project-1", SourceID: "source-1", OccurredAt: time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC),
	}, errors.New("persist incident failed"))
	logged := output.String()
	if !strings.Contains(logged, `"event":"webhook.ingest.failed"`) || !strings.Contains(logged, `"project_id":"project-1"`) {
		t.Fatalf("log = %s", logged)
	}
	if !strings.Contains(logged, `"error_message":"persist incident failed"`) || !strings.Contains(logged, `"source_id":"source-1"`) {
		t.Fatalf("log diagnostics = %s", logged)
	}
}
