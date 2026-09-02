//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"os"
	"testing"
	"time"

	"fixthe/backend/internal/platform/buildinfo"
	"fixthe/backend/internal/platform/config"
	"fixthe/backend/internal/platform/observability"
	platformredis "fixthe/backend/internal/platform/redis"
)

func TestRedisConnectivityAndOwnedKeyCleanup(t *testing.T) {
	connectionURL, prefix, err := isolatedRedisTarget(os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	identifier := make([]byte, 16)
	if _, err := rand.Read(identifier); err != nil {
		t.Fatalf("create isolated Redis key suffix: %v", err)
	}
	key := prefix + hex.EncodeToString(identifier)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	telemetryRuntime, err := observability.NewTelemetry(ctx, observability.TelemetryOptions{
		Service:     "fixthe-redis-integration",
		Environment: "test",
		Build:       buildinfo.Info{Version: "test", Commit: "test", BuildDate: time.Unix(0, 0).UTC().Format(time.RFC3339)},
	})
	if err != nil {
		t.Fatalf("create telemetry: %v", err)
	}
	t.Cleanup(func() {
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := telemetryRuntime.Shutdown(shutdownContext); err != nil {
			t.Errorf("shutdown telemetry: %v", err)
		}
	})

	configuration, err := config.LoadRedis(mapLookup(map[string]string{config.RedisURLKey: connectionURL}))
	if err != nil {
		t.Fatalf("load Redis integration configuration: %v", err)
	}
	client, err := platformredis.Open(ctx, platformredis.ClientOptions{
		Configuration: configuration,
		Application:   "fixthe-redis-integration",
		Logger:        logger,
		Tracer:        telemetryRuntime.Tracer("fixthe/backend/tests/integration"),
		MeterProvider: telemetryRuntime.MeterProvider(),
	})
	if err != nil {
		t.Fatalf("open isolated Redis target: %v", err)
	}
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if err := client.Del(cleanupContext, key).Err(); err != nil {
			t.Errorf("delete owned Redis key: %v", err)
		}
		if err := client.Close(cleanupContext); err != nil {
			t.Errorf("close Redis client: %v", err)
		}
	})

	const value = "integration-value"
	if err := client.Set(ctx, key, value, time.Minute).Err(); err != nil {
		t.Fatalf("set owned Redis key: %v", err)
	}
	stored, err := client.Get(ctx, key).Result()
	if err != nil {
		t.Fatalf("get owned Redis key: %v", err)
	}
	if stored != value {
		t.Fatalf("stored value = %q", stored)
	}
}
