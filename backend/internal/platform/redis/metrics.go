package redis

import (
	"context"
	"fmt"

	redisclient "github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type poolMetrics struct {
	registration metric.Registration
}

func newPoolMetrics(meter metric.Meter, client *redisclient.Client) (*poolMetrics, error) {
	connections, err := meter.Int64ObservableGauge("fixthe.redis.pool.connections",
		metric.WithUnit("{connection}"),
		metric.WithDescription("Current Redis pool connections by state"),
	)
	if err != nil {
		return nil, fmt.Errorf("create Redis pool connections metric: %w", err)
	}
	pending, err := meter.Int64ObservableGauge("fixthe.redis.pool.pending_requests",
		metric.WithUnit("{request}"),
		metric.WithDescription("Current Redis requests waiting for a connection"),
	)
	if err != nil {
		return nil, fmt.Errorf("create Redis pending requests metric: %w", err)
	}
	waits, err := meter.Int64ObservableCounter("fixthe.redis.pool.waits",
		metric.WithUnit("{wait}"),
		metric.WithDescription("Cumulative Redis connection pool waits"),
	)
	if err != nil {
		return nil, fmt.Errorf("create Redis pool waits metric: %w", err)
	}

	registration, err := meter.RegisterCallback(func(_ context.Context, observer metric.Observer) error {
		stats := client.PoolStats()
		observer.ObserveInt64(connections, int64(stats.TotalConns),
			metric.WithAttributes(attribute.String("state", "total")),
		)
		observer.ObserveInt64(connections, int64(stats.IdleConns),
			metric.WithAttributes(attribute.String("state", "idle")),
		)
		observer.ObserveInt64(pending, int64(stats.PendingRequests))
		observer.ObserveInt64(waits, int64(stats.WaitCount))
		return nil
	}, connections, pending, waits)
	if err != nil {
		return nil, fmt.Errorf("register Redis pool metric callback: %w", err)
	}
	return &poolMetrics{registration: registration}, nil
}

func (m *poolMetrics) close() error {
	return m.registration.Unregister()
}
