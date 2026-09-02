package bootstrap

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"fixthe/backend/internal/platform/buildinfo"
	"fixthe/backend/internal/platform/config"
)

func TestLoggerWritesToConsoleAndConfiguredFile(t *testing.T) {
	var output bytes.Buffer
	path := filepath.Join(t.TempDir(), "logs", "fixthe.log")
	processLogger, sink, err := logger(testOptions(&output, nil), "fixthe-test", config.Common{
		Environment: "test", LogLevel: "info", LogFormat: "json", LogFile: path,
	})
	if err != nil {
		t.Fatalf("logger() error = %v", err)
	}
	const recordCount = 32
	var writers sync.WaitGroup
	for sequence := range recordCount {
		writers.Add(1)
		go func() {
			defer writers.Done()
			processLogger.Info("local-file-marker", "sequence", sequence)
		}()
	}
	writers.Wait()
	if err := sink.Close(); err != nil {
		t.Fatalf("close log sink: %v", err)
	}

	fileOutput, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if output.String() != string(fileOutput) || strings.Count(string(fileOutput), "local-file-marker") != recordCount {
		t.Fatalf("console = %q, file = %q", output.String(), fileOutput)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat log file: %v", err)
	}
	if permissions := info.Mode().Perm(); permissions != 0o600 {
		t.Fatalf("log permissions = %o, want 600", permissions)
	}
}

func TestRunMigrateRequiresPostgreSQLBeforeResourceConstruction(t *testing.T) {
	var output bytes.Buffer
	err := RunMigrate(context.Background(), testOptions(&output, nil))
	if err == nil || !strings.Contains(err.Error(), config.PostgresURLKey) {
		t.Fatalf("RunMigrate() error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("output = %q", output.String())
	}
}

func TestRunSeedRequiresPostgreSQLBeforeResourceConstruction(t *testing.T) {
	var output bytes.Buffer
	err := RunSeed(context.Background(), testOptions(&output, nil))
	if err == nil || !strings.Contains(err.Error(), config.PostgresURLKey) {
		t.Fatalf("RunSeed() error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("output = %q", output.String())
	}
}

func TestRunAPIRejectsInvalidConfigurationSafely(t *testing.T) {
	const rawValue = "do-not-print-this-value"
	err := RunAPI(context.Background(), testOptions(&bytes.Buffer{}, map[string]string{
		config.LogLevelKey: rawValue,
	}))
	if err == nil {
		t.Fatal("RunAPI() error = nil")
	}
	if strings.Contains(err.Error(), rawValue) {
		t.Fatalf("error = %q", err)
	}
}

func testOptions(output *bytes.Buffer, values map[string]string) Options {
	lookup := func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
	return Options{
		Lookup: lookup,
		Output: output,
		Build: buildinfo.Info{
			Version:   "test",
			Commit:    "test",
			BuildDate: time.Unix(0, 0).UTC().Format(time.RFC3339),
		},
	}
}
