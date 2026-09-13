//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	migratecommand "mendry/backend/internal/commands/migrate"
	"mendry/backend/internal/commands/migrate/migratedb"
	"mendry/backend/internal/modules/auth/adapter/postgres/authdb"
	incidentpostgres "mendry/backend/internal/modules/incidents/adapter/postgres"
	incidentapplication "mendry/backend/internal/modules/incidents/application"
	incidentdomain "mendry/backend/internal/modules/incidents/domain"
	observationpostgres "mendry/backend/internal/modules/observations/adapter/postgres"
	observationapplication "mendry/backend/internal/modules/observations/application"
	observationdomain "mendry/backend/internal/modules/observations/domain"
	projectpostgres "mendry/backend/internal/modules/projects/adapter/postgres"
	projectdomain "mendry/backend/internal/modules/projects/domain"
	"mendry/backend/internal/platform/buildinfo"
	"mendry/backend/internal/platform/config"
	"mendry/backend/internal/platform/observability"
	"mendry/backend/internal/platform/postgres"

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
		Service:     "mendry-postgres-integration",
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
		Application:   "mendry-postgres-integration",
		Logger:        logger,
		Tracer:        telemetryRuntime.Tracer("mendry/backend/tests/integration"),
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
	// 才能到达这里，因此清理范围严格限制为本 MVP 拥有的业务表。
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
	if len(applied) != 21 {
		t.Fatalf("applied migration count = %d, want 21: %#v", len(applied), applied)
	}
	for index, migration := range applied {
		if migration.Version != int64(index+1) || len(migration.Checksum) != 64 {
			t.Fatalf("applied migration[%d] = %#v, want version %d and sha256 checksum", index, migration, index+1)
		}
	}

	assertMVPRelations(t, ctx, pool)
	assertSchemaComments(t, ctx, pool)
	assertMVPQueries(t, ctx, pool)
}

func resetMVPPostgreSQLSchema(ctx context.Context, pool *postgres.Pool, operation string) error {
	for _, table := range []string{
		"remediation_token_usage", "overview_collection",
		"remediation_lifecycle_effect", "remediation_tool_invocation", "remediation_artifact", "remediation_plan",
		"remediation_decision", "remediation_run", "remediation_series",
		"audit_events", "incidents", "observations", "project_triggers", "project_sources",
		"project_repositories", "project_secrets", "project_memberships",
		"project_environments", "projects", "users", "fixthe_schema_migrations",
	} {
		if _, err := pool.Exec(postgres.WithOperation(ctx, operation), "DROP TABLE IF EXISTS "+table); err != nil {
			return err
		}
	}
	for _, function := range []string{"overview_run_state_time", "overview_record_token_usage"} {
		if _, err := pool.Exec(postgres.WithOperation(ctx, operation), "DROP FUNCTION IF EXISTS "+function+"()"); err != nil {
			return err
		}
	}
	return nil
}

func assertMVPRelations(t *testing.T, ctx context.Context, pool *postgres.Pool) {
	t.Helper()
	relations := []string{
		"users", "projects", "project_environments", "project_secrets",
		"project_repositories", "project_sources", "project_triggers", "observations", "incidents",
	}
	for _, relation := range relations {
		var exists bool
		if err := pool.QueryRow(postgres.WithOperation(ctx, "integration.migration.relations"),
			"SELECT to_regclass('public.' || $1) IS NOT NULL", relation).Scan(&exists); err != nil {
			t.Fatalf("inspect migrated relation %s: %v", relation, err)
		}
		if !exists {
			t.Errorf("relation %s does not exist", relation)
		}
	}
	for _, removed := range []string{"project_memberships", "audit_events"} {
		var exists bool
		if err := pool.QueryRow(ctx, "SELECT to_regclass('public.' || $1) IS NOT NULL", removed).Scan(&exists); err != nil || exists {
			t.Fatalf("removed relation %s: exists=%t error=%v", removed, exists, err)
		}
	}
	var outboxExists bool
	err := pool.QueryRow(postgres.WithOperation(ctx, "integration.migration.relations"), `
		SELECT to_regclass('public.job_outbox') IS NOT NULL
	`).Scan(&outboxExists)
	if err != nil {
		t.Fatalf("inspect migrated relations: %v", err)
	}
	if outboxExists {
		t.Fatal("job_outbox unexpectedly exists")
	}
}

func assertSchemaComments(t *testing.T, ctx context.Context, pool *postgres.Pool) {
	t.Helper()
	expectedColumnCounts := map[string]int{
		"fixthe_schema_migrations":     4,
		"users":                        7,
		"projects":                     7,
		"project_environments":         8,
		"project_secrets":              11,
		"project_repositories":         12,
		"project_sources":              11,
		"project_triggers":             13,
		"observations":                 13,
		"incidents":                    21,
		"remediation_series":           5,
		"remediation_run":              17,
		"remediation_decision":         11,
		"remediation_plan":             12,
		"remediation_artifact":         8,
		"remediation_tool_invocation":  8,
		"remediation_lifecycle_effect": 25,
		"project_llm_providers":        9,
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
		},
	)
	if err != nil {
		t.Fatalf("create user through generated query: %v", err)
	}
	if user.Username != "migration-admin" || !user.Enabled {
		t.Fatalf("created user = %#v", user)
	}
	_, secondUserErr := authdb.New(pool).CreateUser(ctx, authdb.CreateUserParams{
		ID:       mustUUID(t, "019ff544-405c-7d12-8f10-cb3fc579605c"),
		Username: "second-user", PasswordHash: user.PasswordHash,
	})
	if secondUserErr == nil || !strings.Contains(secondUserErr.Error(), "users_single_user") {
		t.Fatalf("second account must fail the single-user constraint: %v", secondUserErr)
	}

	projectRepository, err := projectpostgres.NewRepository(pool)
	if err != nil {
		t.Fatalf("create project repository: %v", err)
	}
	project, err := projectRepository.CreateProject(ctx, projectdomain.Project{
		ID: "019ff544-405c-7d21-9f10-cb3fc579605c", Key: "integration-project", Name: "Integration project",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if project.Key != "integration-project" {
		t.Fatalf("created project = %#v", project)
	}
	secretIDs := []string{
		"019ff544-405c-7d24-9f10-cb3fc579605c",
		"019ff544-405c-7d27-9f10-cb3fc579605c",
		"019ff544-405c-7d28-9f10-cb3fc579605c",
	}
	secretMaterials := []string{"encrypted-git-material", "encrypted-source-material", "encrypted-webhook-material"}
	secretKinds := []projectdomain.SecretKind{
		projectdomain.SecretGitCredential,
		projectdomain.SecretHTTPBearer,
		projectdomain.SecretWebhookHMAC,
	}
	for index := range secretIDs {
		createdSecret, createErr := projectRepository.CreateSecret(ctx, projectdomain.EncryptedSecret{
			Secret: projectdomain.Secret{ID: secretIDs[index], ProjectID: project.ID, Name: "integration-secret-" + string(rune('a'+index)),
				Kind: secretKinds[index], KeyVersion: 1},
			Ciphertext: []byte(secretMaterials[index]), Nonce: []byte("123456789012"),
		})
		if createErr != nil || createdSecret.ID != secretIDs[index] {
			t.Fatalf("create project secret %d = %#v, %v", index, createdSecret, createErr)
		}
	}
	listedSecrets, err := projectRepository.ListSecrets(ctx, project.ID)
	if err != nil || len(listedSecrets.Items) != len(secretIDs) || listedSecrets.Total != int64(len(secretIDs)) {
		t.Fatalf("project secret metadata = %#v, %v", listedSecrets, err)
	}
	secretJSON, err := json.Marshal(listedSecrets.Items)
	if err != nil {
		t.Fatalf("marshal secret metadata: %v", err)
	}
	for _, material := range secretMaterials {
		if strings.Contains(string(secretJSON), material) {
			t.Fatalf("secret metadata disclosed encrypted material: %s", secretJSON)
		}
	}

	configuration, err := projectRepository.UpsertConfiguration(ctx, project.ID, projectdomain.Configuration{
		Environment: projectdomain.Environment{ID: "019ff544-405c-7d22-9f10-cb3fc579605c", Key: "production", Name: "Production"},
		Repository: projectdomain.Repository{ID: "019ff544-405c-7d25-9f10-cb3fc579605c", RemoteURL: "https://github.com/example/service.git", SCMProvider: "github", Transport: "https",
			CredentialSecretID: &secretIDs[0], ProductionBranch: "main", DeployedCommit: "0123456789abcdef0123456789abcdef01234567"},
		Source: projectdomain.Source{ID: "019ff544-405c-7d23-9f10-cb3fc579605c", Kind: "cloud", CredentialSecretID: &secretIDs[1],
			Config: []byte(`{"schemaVersion":1,"provider":"tencent-cls","region":"ap-shanghai","resource":"integration-logset"}`), Capabilities: []string{"push_ingestion"}, Enabled: true},
		Trigger: projectdomain.Trigger{ID: "019ff544-405c-7d26-9f10-cb3fc579605c", Kind: "signed_webhook", SigningSecretID: &secretIDs[2],
			Config: []byte(`{"schemaVersion":1,"eventTypes":["error"],"deduplicationKey":"fingerprint"}`), Enabled: true},
		LLM: &projectdomain.LLMProvider{ID: "019ff544-405c-7d29-9f10-cb3fc579605c", Provider: "openai", BaseURL: "https://api.openai.com", CredentialSecretID: secretIDs[1], Model: "gpt-5.6"},
	})
	if err != nil {
		t.Fatalf("upsert project configuration: %v", err)
	}
	if configuration.Repository.CredentialSecretID == nil ||
		*configuration.Repository.CredentialSecretID != secretIDs[0] || configuration.Source.CredentialSecretID == nil ||
		*configuration.Source.CredentialSecretID != secretIDs[1] || configuration.Trigger.SigningSecretID == nil ||
		*configuration.Trigger.SigningSecretID != secretIDs[2] {
		t.Fatalf("configuration = %#v", configuration)
	}
	reloadedConfiguration, err := projectRepository.GetConfiguration(ctx, project.ID)
	if err != nil || reloadedConfiguration.Repository.RemoteURL != "https://github.com/example/service.git" {
		t.Fatalf("reloaded configuration = %#v, %v", reloadedConfiguration, err)
	}

	renamed, err := projectRepository.UpdateProjectName(ctx, projectdomain.Project{
		ID: project.ID, Key: project.Key, Name: "Renamed integration project", Description: project.Description,
	})
	if err != nil || renamed.Key != "integration-project" || renamed.Name != "Renamed integration project" || renamed.Version != project.Version+1 {
		t.Fatalf("rename project = %#v, %v", renamed, err)
	}
	reloadedAfterRename, err := projectRepository.GetConfiguration(ctx, project.ID)
	if err != nil || reloadedAfterRename.Environment.Name != "Production" ||
		reloadedAfterRename.Repository.CredentialSecretID == nil || *reloadedAfterRename.Repository.CredentialSecretID != secretIDs[0] {
		t.Fatalf("distinct environment was not preserved: %#v, %v", reloadedAfterRename, err)
	}
	syncedConfiguration, err := projectRepository.UpsertConfiguration(ctx, project.ID, projectdomain.Configuration{
		Environment: projectdomain.Environment{ID: configuration.Environment.ID, Key: "production", Name: renamed.Name},
		Repository:  reloadedAfterRename.Repository,
		Source:      reloadedAfterRename.Source,
		Trigger:     reloadedAfterRename.Trigger,
		LLM:         reloadedAfterRename.LLM,
	})
	if err != nil || syncedConfiguration.Environment.Name != renamed.Name {
		t.Fatalf("align environment name = %#v, %v", syncedConfiguration, err)
	}
	finalName, err := projectRepository.UpdateProjectName(ctx, projectdomain.Project{
		ID: project.ID, Key: project.Key, Name: "Final integration project", Description: project.Description,
	})
	if err != nil || finalName.Key != project.Key {
		t.Fatalf("second rename = %#v, %v", finalName, err)
	}
	reloadedAfterSync, err := projectRepository.GetConfiguration(ctx, project.ID)
	if err != nil || reloadedAfterSync.Environment.Name != "Final integration project" ||
		reloadedAfterSync.Repository.CredentialSecretID == nil || *reloadedAfterSync.Repository.CredentialSecretID != secretIDs[0] ||
		reloadedAfterSync.Source.CredentialSecretID == nil || *reloadedAfterSync.Source.CredentialSecretID != secretIDs[1] ||
		reloadedAfterSync.Trigger.SigningSecretID == nil || *reloadedAfterSync.Trigger.SigningSecretID != secretIDs[2] {
		t.Fatalf("synchronized environment or references = %#v, %v", reloadedAfterSync, err)
	}

	loadedSecret, err := projectRepository.GetEncryptedSecret(ctx, project.ID, secretIDs[0])
	if err != nil {
		t.Fatalf("get encrypted secret: %v", err)
	}
	nameOnly, err := projectRepository.UpdateSecret(ctx, projectdomain.EncryptedSecret{
		Secret: projectdomain.Secret{ID: loadedSecret.ID, ProjectID: project.ID, Name: "integration-secret-renamed",
			Kind: loadedSecret.Kind, KeyVersion: loadedSecret.KeyVersion},
		Ciphertext: loadedSecret.Ciphertext, Nonce: loadedSecret.Nonce,
	})
	if err != nil || nameOnly.ID != loadedSecret.ID || nameOnly.Kind != loadedSecret.Kind ||
		nameOnly.Name != "integration-secret-renamed" || nameOnly.KeyVersion != loadedSecret.KeyVersion ||
		nameOnly.Version != loadedSecret.Version+1 {
		t.Fatalf("name-only secret update = %#v, %v", nameOnly, err)
	}
	reloadedNameOnly, err := projectRepository.GetEncryptedSecret(ctx, project.ID, secretIDs[0])
	if err != nil || string(reloadedNameOnly.Ciphertext) != string(loadedSecret.Ciphertext) ||
		string(reloadedNameOnly.Nonce) != string(loadedSecret.Nonce) || reloadedNameOnly.KeyVersion != loadedSecret.KeyVersion {
		t.Fatalf("name-only secret material changed: %#v, %v", reloadedNameOnly, err)
	}
	rotated, err := projectRepository.UpdateSecret(ctx, projectdomain.EncryptedSecret{
		Secret: projectdomain.Secret{ID: loadedSecret.ID, ProjectID: project.ID, Name: "integration-secret-renamed",
			Kind: loadedSecret.Kind, KeyVersion: 2},
		Ciphertext: []byte("rotated-git-material"), Nonce: []byte("abcdefghijkl"),
	})
	if err != nil || rotated.ID != loadedSecret.ID || rotated.Kind != loadedSecret.Kind || rotated.KeyVersion != 2 ||
		rotated.Version != nameOnly.Version+1 {
		t.Fatalf("rotated secret = %#v, %v", rotated, err)
	}
	reloadedRotated, err := projectRepository.GetEncryptedSecret(ctx, project.ID, secretIDs[0])
	if err != nil || string(reloadedRotated.Ciphertext) != "rotated-git-material" || reloadedRotated.Kind != loadedSecret.Kind {
		t.Fatalf("rotated secret material = %#v, %v", reloadedRotated, err)
	}

	observationRepository, err := observationpostgres.NewRepository(pool)
	if err != nil {
		t.Fatalf("create observation repository: %v", err)
	}
	observation, err := observationRepository.Create(ctx, observationdomain.Observation{
		ID: "019ff544-405c-7d41-9f10-cb3fc579605c", ProjectID: project.ID, SourceID: configuration.Source.ID,
		OccurredAt: time.Now().UTC(), Level: "error", Message: "integration failure", Fingerprint: "integration:migration",
		Attributes: []byte(`{"component":"integration"}`),
	})
	if err != nil {
		t.Fatalf("create observation: %v", err)
	}
	if observation.ProjectID != project.ID || observation.SourceID != configuration.Source.ID {
		t.Fatalf("observation = %#v", observation)
	}
	observations, err := observationRepository.List(ctx, project.ID, 10)
	if err != nil || len(observations.Items) != 1 || observations.Items[0].ID != observation.ID {
		t.Fatalf("project observations = %#v, %v", observations, err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	incidentRepository, err := incidentpostgres.NewRepository(pool)
	if err != nil {
		t.Fatalf("create incident repository: %v", err)
	}
	incident, err := incidentRepository.Create(ctx, incidentdomain.Incident{
		InternalID:          "019ff544-405c-7d11-9f10-cb3fc579605c",
		ProjectID:           project.ID,
		EnvironmentID:       "repository-resolved",
		SourceID:            configuration.Source.ID,
		Title:               "Integration migration incident",
		Fingerprint:         "integration:migration",
		Status:              incidentdomain.StatusOpen,
		Priority:            incidentdomain.PriorityP2,
		Source:              "repository-resolved",
		FirstSeen:           now,
		LastSeen:            now,
		OccurrenceCount:     1,
		HostCount:           1,
		NotificationSummary: "Lifecycle default",
	}, nil)
	if err != nil {
		t.Fatalf("create incident through repository: %v", err)
	}
	if incident.Number != 2049 || incident.Status != incidentdomain.StatusOpen || incident.Version != 1 {
		t.Fatalf("created incident = %#v", incident)
	}

	loaded, err := incidentRepository.GetByNumber(ctx, project.ID, incident.Number)
	if err != nil || loaded.InternalID != incident.InternalID {
		t.Fatalf("get incident through repository = %#v, %v", loaded, err)
	}
	listed, err := incidentRepository.List(ctx, project.ID, 10)
	if err != nil || len(listed.Items) != 1 || listed.Items[0].Number != incident.Number {
		t.Fatalf("list incidents through repository = %#v, %v", listed, err)
	}

	updated, err := incidentRepository.UpdateStatus(ctx, project.ID, incident.Number, incidentdomain.StatusRecovered,
		incident.LifecycleGeneration, incident.DeployedCommit, nil)
	if err != nil {
		t.Fatalf("update incident through repository: %v", err)
	}
	if updated.Status != incidentdomain.StatusRecovered || updated.Version != 2 {
		t.Fatalf("updated incident = %#v", updated)
	}

	otherProject, err := projectRepository.CreateProject(ctx, projectdomain.Project{
		ID: "019ff544-405c-7d51-9f10-cb3fc579605c", Key: "other-integration-project", Name: "Other integration project",
	})
	if err != nil {
		t.Fatalf("create isolation project: %v", err)
	}
	otherProjectID := otherProject.ID
	if _, err := incidentRepository.GetByNumber(ctx, otherProjectID, incident.Number); !errors.Is(err, incidentapplication.ErrNotFound) {
		t.Fatalf("cross-project incident lookup error = %v", err)
	}
	otherObservations, err := observationRepository.List(ctx, otherProjectID, 10)
	if err != nil || len(otherObservations.Items) != 0 {
		t.Fatalf("cross-project observation list = %#v, %v", otherObservations, err)
	}
	if _, err := observationRepository.Create(ctx, observationdomain.Observation{
		ID: "019ff544-405c-7d42-9f10-cb3fc579605c", ProjectID: otherProjectID, SourceID: configuration.Source.ID,
		OccurredAt: time.Now().UTC(), Level: "error", Message: "cross-project attempt", Fingerprint: "integration:isolation",
		Attributes: []byte(`{}`),
	}); !errors.Is(err, observationapplication.ErrSourceNotFound) {
		t.Fatalf("cross-project observation create error = %v", err)
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
