package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type poolMetrics struct {
	registration metric.Registration
}

func newPoolMetrics(meter metric.Meter, pool *pgxpool.Pool) (*poolMetrics, error) {
	connections, err := meter.Int64ObservableGauge("mendry.postgres.pool.connections",
		metric.WithUnit("{connection}"),
		metric.WithDescription("Current PostgreSQL pool connections by state"),
	)
	if err != nil {
		return nil, fmt.Errorf("create PostgreSQL pool connections metric: %w", err)
	}
	maximum, err := meter.Int64ObservableGauge("mendry.postgres.pool.max_connections",
		metric.WithUnit("{connection}"),
		metric.WithDescription("Configured PostgreSQL pool connection limit"),
	)
	if err != nil {
		return nil, fmt.Errorf("create PostgreSQL pool limit metric: %w", err)
	}
	waiters, err := meter.Int64ObservableGauge("mendry.postgres.pool.wait_count",
		metric.WithUnit("{acquire}"),
		metric.WithDescription("Cumulative PostgreSQL acquires that waited for capacity"),
	)
	if err != nil {
		return nil, fmt.Errorf("create PostgreSQL pool wait metric: %w", err)
	}

	registration, err := meter.RegisterCallback(func(_ context.Context, observer metric.Observer) error {
		statistics := pool.Stat()
		observer.ObserveInt64(connections, int64(statistics.AcquiredConns()),
			metric.WithAttributes(attribute.String("state", "used")),
		)
		observer.ObserveInt64(connections, int64(statistics.IdleConns()),
			metric.WithAttributes(attribute.String("state", "idle")),
		)
		observer.ObserveInt64(maximum, int64(statistics.MaxConns()))
		observer.ObserveInt64(waiters, statistics.EmptyAcquireCount())
		return nil
	}, connections, maximum, waiters)
	if err != nil {
		return nil, fmt.Errorf("register PostgreSQL pool metric callback: %w", err)
	}
	return &poolMetrics{registration: registration}, nil
}

func (m *poolMetrics) close() error {
	return m.registration.Unregister()
}
