package redis

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"mendry/backend/internal/platform/config"

	redisclient "github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/maintnotifications"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestBuildClientOptionsAppliesExplicitBounds(t *testing.T) {
	configuration := testConfiguration()
	options, err := buildClientOptions(configuration)
	if err != nil {
		t.Fatalf("buildClientOptions() error = %v", err)
	}
	if options.PoolSize != configuration.PoolSize || options.MaxActiveConns != configuration.PoolSize ||
		options.MaxConcurrentDials != configuration.PoolSize || options.MinIdleConns != configuration.MinIdleConnections {
		t.Fatalf("pool options = %#v", options)
	}
	if options.ClientName != "" || options.DB != configuration.Database || options.DialTimeout != configuration.DialTimeout ||
		!options.ContextTimeoutEnabled || options.MaxRetries != configuration.MaxRetries {
		t.Fatalf("client options = %#v", options)
	}
	if options.MaintNotificationsConfig == nil || options.MaintNotificationsConfig.Mode != maintnotifications.ModeDisabled {
		t.Fatalf("MaintNotificationsConfig = %#v", options.MaintNotificationsConfig)
	}
}

func TestOpenRejectsInvalidClientNameBeforeConnecting(t *testing.T) {
	configuration := testConfiguration()
	_, err := Open(context.Background(), ClientOptions{
		Configuration: configuration,
		Application:   "invalid client name",
		Logger:        slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)),
		Tracer:        noop.NewTracerProvider().Tracer("test"),
		MeterProvider: metricnoop.NewMeterProvider(),
	})
	if err == nil || !strings.Contains(err.Error(), "client name") {
		t.Fatalf("Open() error = %v", err)
	}
}

func TestBuildClientOptionsMapsDisabledRetriesAndTLS(t *testing.T) {
	configuration := testConfiguration()
	configuration.URL = "rediss://cache-user:cache-password@redis.example.com:6380"
	configuration.MaxRetries = 0
	options, err := buildClientOptions(configuration)
	if err != nil {
		t.Fatalf("buildClientOptions() error = %v", err)
	}
	if options.MaxRetries != -1 {
		t.Fatalf("MaxRetries = %d", options.MaxRetries)
	}
	if options.TLSConfig == nil || options.TLSConfig.MinVersion == 0 {
		t.Fatalf("TLSConfig = %#v", options.TLSConfig)
	}
}

func TestBuildClientOptionsDoesNotExposeConnectionURL(t *testing.T) {
	const secret = "redis://cache-user:secret-password@%invalid:6379"
	configuration := testConfiguration()
	configuration.URL = secret
	_, err := buildClientOptions(configuration)
	if err == nil {
		t.Fatal("buildClientOptions() error = nil")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "secret-password") {
		t.Fatalf("error exposes Redis URL: %q", err)
	}
}

func TestRedisErrorClassification(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{err: redisclient.Nil, want: errorClassNotFound},
		{err: errors.New("unknown"), want: errorClassInternal},
	}
	for _, test := range tests {
		if got := classifyError(test.err); got != test.want {
			t.Fatalf("classifyError(%v) = %q, want %q", test.err, got, test.want)
		}
	}
}

func TestCloseIsConcurrentSafeAndLogsClosedStateOnce(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	rawClient := redisclient.NewClient(&redisclient.Options{Addr: "127.0.0.1:6379"})
	metrics, err := newPoolMetrics(metricnoop.NewMeterProvider().Meter("test"), rawClient)
	if err != nil {
		t.Fatalf("newPoolMetrics() error = %v", err)
	}
	client := &Client{
		Client:    rawClient,
		logger:    logger,
		metrics:   metrics,
		closeDone: make(chan struct{}),
	}

	// 并发 shutdown caller 必须共享同一次资源关闭与 lifecycle event。
	const callers = 8
	errorsByCaller := make([]error, callers)
	var waitGroup sync.WaitGroup
	for index := range callers {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			errorsByCaller[index] = client.Close(ctx)
		}()
	}
	waitGroup.Wait()
	for index, closeError := range errorsByCaller {
		if closeError != nil {
			t.Fatalf("Close() caller %d error = %v", index, closeError)
		}
	}
	if got := strings.Count(output.String(), `"event":"redis.pool.state"`); got != 1 {
		t.Fatalf("closed event count = %d, log = %s", got, output.String())
	}
	if !strings.Contains(output.String(), `"health":"closed"`) {
		t.Fatalf("closed event missing final health: %s", output.String())
	}
}

func testConfiguration() config.Redis {
	return config.Redis{
		Enabled:              true,
		URL:                  "redis://cache-user:cache-password@localhost:6379",
		DialTimeout:          5 * time.Second,
		ReadTimeout:          3 * time.Second,
		WriteTimeout:         3 * time.Second,
		PoolTimeout:          2 * time.Second,
		HealthTimeout:        2 * time.Second,
		PoolSize:             10,
		MinIdleConnections:   1,
		MaxRetries:           2,
		MinRetryBackoff:      10 * time.Millisecond,
		MaxRetryBackoff:      500 * time.Millisecond,
		MaxConnIdleTime:      5 * time.Minute,
		MaxConnLifetime:      30 * time.Minute,
		Database:             0,
		SlowCommandThreshold: 250 * time.Millisecond,
	}
}
