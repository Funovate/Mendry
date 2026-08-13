//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	migratecommand "fixthe/backend/internal/commands/migrate"
	"fixthe/backend/internal/commands/migrate/migratedb"
	"fixthe/backend/internal/modules/auth/adapter/postgres/authdb"
	"fixthe/backend/internal/modules/incidents/adapter/postgres/incidentdb"
	"fixthe/backend/internal/platform/buildinfo"
	"fixthe/backend/internal/platform/config"
	"fixthe/backend/internal/platform/observability"
	"fixthe/backend/internal/platform/postgres"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestPostgreSQLMigrationsFromEmptyHistory(t *testing.T) {
	connectionURL, databaseName, err := isolatedPostgresTarget(os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	telemetryRuntime, err := observability.NewTelemetry(ctx, observability.TelemetryOptions{
		Service:     "fixthe-postgres-integration",
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

	pool, err := postgres.Open(ctx, postgres.PoolOptions{
		Configuration: integrationPostgresConfiguration(connectionURL),
		Application:   "fixthe-postgres-integration",
		Logger:        logger,
		Tracer:        telemetryRuntime.Tracer("fixthe/backend/tests/integration"),
		MeterProvider: telemetryRuntime.MeterProvider(),
	})
	if err != nil {
		t.Fatalf("open isolated PostgreSQL target: %v", err)
	}
	t.Cleanup(func() {
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := pool.Close(shutdownContext); err != nil {
			t.Errorf("close PostgreSQL pool: %v", err)
		}
	})

	// 只有 URL 中 database 与独立 isolation marker 完全一致且包含 test 标记时
	// 才能到达这里，因此清理范围严格限制为本 MVP 拥有的三张表。
	if err := resetMVPPostgreSQLSchema(ctx, pool, "integration.migration.reset"); err != nil {
		t.Fatalf("reset migration history in %s: %v", databaseName, err)
	}
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if err := resetMVPPostgreSQLSchema(cleanupContext, pool, "integration.migration.cleanup"); err != nil {
			t.Errorf("clean MVP schema: %v", err)
		}
	})

	runner, err := migratecommand.NewRunner(migratecommand.RunnerOptions{
		Connections: migratecommand.PoolSource{Pool: pool},
		Logger:      logger,
		LockTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("create migration runner: %v", err)
	}
	if err := runner.Run(ctx); err != nil {
		t.Fatalf("apply migrations from empty history: %v", err)
	}
	if err := runner.Run(ctx); err != nil {
		t.Fatalf("reapply migrations idempotently: %v", err)
	}

	applied, err := migratedb.New(pool).ListAppliedMigrations(
		postgres.WithOperation(ctx, "integration.migration.list_applied"),
	)
	if err != nil {
		t.Fatalf("list applied migrations: %v", err)
	}
	if len(applied) != 3 || applied[0].Version != 1 || applied[1].Version != 2 || applied[2].Version != 3 ||
		len(applied[0].Checksum) != 64 || len(applied[1].Checksum) != 64 || len(applied[2].Checksum) != 64 {
		t.Fatalf("applied migrations = %#v", applied)
	}

	assertMVPRelations(t, ctx, pool)
	assertSchemaComments(t, ctx, pool)
	assertMVPQueries(t, ctx, pool)
}

func resetMVPPostgreSQLSchema(ctx context.Context, pool *postgres.Pool, operation string) error {
	for _, table := range []string{"incidents", "users", "fixthe_schema_migrations"} {
		if _, err := pool.Exec(postgres.WithOperation(ctx, operation), "DROP TABLE IF EXISTS "+table); err != nil {
			return err
		}
	}
	return nil
}

func assertMVPRelations(t *testing.T, ctx context.Context, pool *postgres.Pool) {
	t.Helper()
	var usersExists, incidentsExists, outboxExists bool
	err := pool.QueryRow(postgres.WithOperation(ctx, "integration.migration.relations"), `
		SELECT to_regclass('public.users') IS NOT NULL,
		       to_regclass('public.incidents') IS NOT NULL,
		       to_regclass('public.job_outbox') IS NOT NULL
	`).Scan(&usersExists, &incidentsExists, &outboxExists)
	if err != nil {
		t.Fatalf("inspect migrated relations: %v", err)
	}
	if !usersExists || !incidentsExists || outboxExists {
		t.Fatalf("relations: users=%t incidents=%t job_outbox=%t", usersExists, incidentsExists, outboxExists)
	}
}

func assertSchemaComments(t *testing.T, ctx context.Context, pool *postgres.Pool) {
	t.Helper()
	expectedColumnCounts := map[string]int{
		"fixthe_schema_migrations": 4,
		"users":                    8,
		"incidents":                16,
	}
	for table, expectedColumnCount := range expectedColumnCounts {
		var tableComment string
		var columnCount, commentedColumnCount int
		err := pool.QueryRow(postgres.WithOperation(ctx, "integration.migration.schema_comments"), `
			SELECT COALESCE(obj_description(c.oid, 'pg_class'), ''),
			       count(*)::int,
			       count(col_description(c.oid, a.attnum)) FILTER (
			           WHERE COALESCE(col_description(c.oid, a.attnum), '') <> ''
			       )::int
			FROM pg_class AS c
			JOIN pg_namespace AS n ON n.oid = c.relnamespace
			JOIN pg_attribute AS a ON a.attrelid = c.oid
			WHERE n.nspname = 'public'
			  AND c.relname = $1
			  AND a.attnum > 0
			  AND NOT a.attisdropped
			GROUP BY c.oid
		`, table).Scan(&tableComment, &columnCount, &commentedColumnCount)
		if err != nil {
			t.Fatalf("inspect schema comments for %s: %v", table, err)
		}
		if tableComment == "" || columnCount != expectedColumnCount || commentedColumnCount != expectedColumnCount {
			t.Errorf("schema comments for %s: table=%q columns=%d commented=%d", table, tableComment, columnCount, commentedColumnCount)
		}
	}
}

func assertMVPQueries(t *testing.T, ctx context.Context, pool *postgres.Pool) {
	t.Helper()
	userID := mustUUID(t, "019ff544-405c-7d10-8f10-cb3fc579605c")
	user, err := authdb.New(pool).CreateUser(
		postgres.WithOperation(ctx, "integration.auth.create_user"),
		authdb.CreateUserParams{
			ID:           userID,
			Username:     "migration-admin",
			PasswordHash: "$2a$12$012345678901234567890u012345678901234567890123456789012",
			Role:         "admin",
		},
	)
	if err != nil {
		t.Fatalf("create user through generated query: %v", err)
	}
	if user.Username != "migration-admin" || user.Role != "admin" || !user.Enabled {
		t.Fatalf("created user = %#v", user)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	incidentQueries := incidentdb.New(pool)
	incident, err := incidentQueries.CreateIncident(
		postgres.WithOperation(ctx, "integration.incident.create"),
		incidentdb.CreateIncidentParams{
			ID:                  mustUUID(t, "019ff544-405c-7d11-9f10-cb3fc579605c"),
			Title:               "Integration migration incident",
			Fingerprint:         "integration:migration",
			Status:              "Open",
			Priority:            "P2",
			Source:              "integration-test",
			FirstSeen:           pgtype.Timestamptz{Time: now, Valid: true},
			LastSeen:            pgtype.Timestamptz{Time: now, Valid: true},
			OccurrenceCount:     1,
			HostCount:           1,
			Muted:               false,
			NotificationSummary: "Lifecycle default",
		},
	)
	if err != nil {
		t.Fatalf("create incident through generated query: %v", err)
	}
	if incident.IncidentNumber != 2049 || incident.Status != "Open" || incident.Version != 1 {
		t.Fatalf("created incident = %#v", incident)
	}

	updated, err := incidentQueries.UpdateIncidentStatus(
		postgres.WithOperation(ctx, "integration.incident.update_status"),
		incidentdb.UpdateIncidentStatusParams{IncidentNumber: incident.IncidentNumber, Status: "Recovered"},
	)
	if err != nil {
		t.Fatalf("update incident through generated query: %v", err)
	}
	if updated.Status != "Recovered" || updated.Version != 2 {
		t.Fatalf("updated incident = %#v", updated)
	}
}

func mustUUID(t *testing.T, value string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := id.Scan(value); err != nil {
		t.Fatalf("parse UUID %q: %v", value, err)
	}
	return id
}

func integrationPostgresConfiguration(connectionURL string) config.PostgreSQL {
	return config.PostgreSQL{
		URL:                connectionURL,
		ConnectTimeout:     5 * time.Second,
		AcquireTimeout:     2 * time.Second,
		StatementTimeout:   30 * time.Second,
		HealthTimeout:      2 * time.Second,
		MinConnections:     0,
		MaxConnections:     4,
		MaxConnLifetime:    5 * time.Minute,
		MaxConnIdleTime:    time.Minute,
		HealthCheckPeriod:  30 * time.Second,
		SlowQueryThreshold: 500 * time.Millisecond,
	}
}
