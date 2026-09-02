package redis

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	redisclient "github.com/redis/go-redis/v9"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestCommandHookLogsSafeNameWithoutKeyValueOrErrorMessage(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
	hook, err := NewCommandHook(logger, noop.NewTracerProvider().Tracer("test"),
		metricnoop.NewMeterProvider().Meter("test"), time.Second)
	if err != nil {
		t.Fatalf("NewCommandHook() error = %v", err)
	}
	const key = "secret-session-key"
	const value = "secret-session-value"
	const diagnostic = "ERR secret-server-diagnostic"
	command := redisclient.NewStatusCmd(context.Background(), "set", key, value)

	process := hook.ProcessHook(func(context.Context, redisclient.Cmder) error {
		return errors.New(diagnostic)
	})
	if err := process(context.Background(), command); err == nil {
		t.Fatal("ProcessHook() error = nil")
	}

	logOutput := output.String()
	for _, forbidden := range []string{key, value, diagnostic} {
		if strings.Contains(logOutput, forbidden) {
			t.Fatalf("log exposes %q: %s", forbidden, logOutput)
		}
	}
	if !strings.Contains(logOutput, `"redis.command.name":"set"`) ||
		!strings.Contains(logOutput, `"error_class":"internal"`) {
		t.Fatalf("log = %s", logOutput)
	}
}

func TestCommandNameRejectsUnsafeOrMissingNames(t *testing.T) {
	unsafe := redisclient.NewStatusCmd(context.Background(), "unsafe command", "secret")
	if got := commandName(unsafe); got != "unknown" {
		t.Fatalf("commandName() = %q", got)
	}
	if got := commandName(nil); got != "unknown" {
		t.Fatalf("commandName(nil) = %q", got)
	}
}
