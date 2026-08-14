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

	migratecommand "fixthe/backend/internal/commands/migrate"
	"fixthe/backend/internal/commands/migrate/migratedb"
	"fixthe/backend/internal/modules/auth/adapter/postgres/authdb"
	incidentpostgres "fixthe/backend/internal/modules/incidents/adapter/postgres"
	incidentapplication "fixthe/backend/internal/modules/incidents/application"
	incidentdomain "fixthe/backend/internal/modules/incidents/domain"
	observationpostgres "fixthe/backend/internal/modules/observations/adapter/postgres"
	observationapplication "fixthe/backend/internal/modules/observations/application"
	observationdomain "fixthe/backend/internal/modules/observations/domain"
	projectpostgres "fixthe/backend/internal/modules/projects/adapter/postgres"
	projectdomain "fixthe/backend/internal/modules/projects/domain"
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
	if len(applied) != 4 || applied[0].Version != 1 || applied[1].Version != 2 || applied[2].Version != 3 || applied[3].Version != 4 ||
		len(applied[0].Checksum) != 64 || len(applied[1].Checksum) != 64 || len(applied[2].Checksum) != 64 || len(applied[3].Checksum) != 64 {
		t.Fatalf("applied migrations = %#v", applied)
	}

	assertMVPRelations(t, ctx, pool)
	assertSchemaComments(t, ctx, pool)
	assertMVPQueries(t, ctx, pool)
}

func resetMVPPostgreSQLSchema(ctx context.Context, pool *postgres.Pool, operation string) error {
	for _, table := range []string{
		"audit_events", "incidents", "observations", "project_triggers", "project_sources",
		"project_repositories", "project_secrets", "project_memberships",
		"project_environments", "projects", "users", "fixthe_schema_migrations",
	} {
		if _, err := pool.Exec(postgres.WithOperation(ctx, operation), "DROP TABLE IF EXISTS "+table); err != nil {
			return err
		}
	}
	return nil
}

func assertMVPRelations(t *testing.T, ctx context.Context, pool *postgres.Pool) {
	t.Helper()
	relations := []string{
		"users", "projects", "project_environments", "project_memberships", "project_secrets",
		"project_repositories", "project_sources", "project_triggers", "observations", "incidents", "audit_events",
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
		"fixthe_schema_migrations": 4,
		"users":                    8,
		"projects":                 7,
		"project_environments":     8,
		"project_memberships":      6,
		"project_secrets":          11,
		"project_repositories":     12,
		"project_sources":          12,
		"project_triggers":         11,
		"observations":             13,
		"incidents":                19,
		"audit_events":             9,
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

	projectRepository, err := projectpostgres.NewRepository(pool)
	if err != nil {
		t.Fatalf("create project repository: %v", err)
	}
	project, err := projectRepository.CreateProject(ctx, projectdomain.Project{
		ID: "019ff544-405c-7d21-9f10-cb3fc579605c", Key: "integration-project", Name: "Integration project",
		Role: projectdomain.RoleAdmin,
	}, "019ff544-405c-7d10-8f10-cb3fc579605c", "019ff544-405c-7d31-9f10-cb3fc579605c")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if project.Key != "integration-project" || project.Role != projectdomain.RoleAdmin {
		t.Fatalf("created project = %#v", project)
	}
	memberUser, err := authdb.New(pool).CreateUser(
		postgres.WithOperation(ctx, "integration.auth.create_member_user"),
		authdb.CreateUserParams{
			ID:           mustUUID(t, "019ff544-405c-7d12-8f10-cb3fc579605c"),
			Username:     "migration-operator",
			PasswordHash: "$2a$12$012345678901234567890u012345678901234567890123456789012",
			Role:         "viewer",
		},
	)
	if err != nil || memberUser.Role != "viewer" {
		t.Fatalf("create project member user = %#v, %v", memberUser, err)
	}
	member, err := projectRepository.UpsertMember(ctx, project.ID, memberUser.Username, projectdomain.RoleOperator,
		"019ff544-405c-7d10-8f10-cb3fc579605c", "019ff544-405c-7d38-9f10-cb3fc579605c")
	if err != nil || member.Role != projectdomain.RoleOperator {
		t.Fatalf("upsert project member = %#v, %v", member, err)
	}
	members, err := projectRepository.ListMembers(ctx, project.ID)
	if err != nil || len(members.Items) != 2 || members.Total != 2 {
		t.Fatalf("project members = %#v, %v", members, err)
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
		}, "019ff544-405c-7d10-8f10-cb3fc579605c", "019ff544-405c-7d3"+string(rune('5'+index))+"-9f10-cb3fc579605c")
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
		Source: projectdomain.Source{ID: "019ff544-405c-7d23-9f10-cb3fc579605c", Name: "production-logs", Kind: "cloud", CredentialSecretID: &secretIDs[1],
			Config: []byte(`{"schemaVersion":1,"provider":"tencent-cls","region":"ap-shanghai","resource":"integration-logset"}`), Capabilities: []string{"push_ingestion"}, Enabled: true},
		Trigger: projectdomain.Trigger{ID: "019ff544-405c-7d26-9f10-cb3fc579605c", Name: "error-webhook", Kind: "signed_webhook", SigningSecretID: &secretIDs[2],
			Config: []byte(`{"schemaVersion":1,"eventTypes":["error"],"deduplicationKey":"fingerprint"}`), Enabled: true},
	}, "019ff544-405c-7d10-8f10-cb3fc579605c", "019ff544-405c-7d32-9f10-cb3fc579605c")
	if err != nil {
		t.Fatalf("upsert project configuration: %v", err)
	}
	if configuration.Source.Name != "production-logs" || configuration.Repository.CredentialSecretID == nil ||
		*configuration.Repository.CredentialSecretID != secretIDs[0] || configuration.Source.CredentialSecretID == nil ||
		*configuration.Source.CredentialSecretID != secretIDs[1] || configuration.Trigger.SigningSecretID == nil ||
		*configuration.Trigger.SigningSecretID != secretIDs[2] {
		t.Fatalf("configuration = %#v", configuration)
	}
	reloadedConfiguration, err := projectRepository.GetConfiguration(ctx, project.ID)
	if err != nil || reloadedConfiguration.Repository.RemoteURL != "https://github.com/example/service.git" ||
		reloadedConfiguration.Trigger.Name != "error-webhook" {
		t.Fatalf("reloaded configuration = %#v, %v", reloadedConfiguration, err)
	}

	renamed, err := projectRepository.UpdateProjectName(ctx, projectdomain.Project{
		ID: project.ID, Key: project.Key, Name: "Renamed integration project", Description: project.Description,
		Role: projectdomain.RoleAdmin,
	}, "019ff544-405c-7d10-8f10-cb3fc579605c", "019ff544-405c-7d61-9f10-cb3fc579605c")
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
	}, "019ff544-405c-7d10-8f10-cb3fc579605c", "019ff544-405c-7d65-9f10-cb3fc579605c")
	if err != nil || syncedConfiguration.Environment.Name != renamed.Name {
		t.Fatalf("align environment name = %#v, %v", syncedConfiguration, err)
	}
	finalName, err := projectRepository.UpdateProjectName(ctx, projectdomain.Project{
		ID: project.ID, Key: project.Key, Name: "Final integration project", Description: project.Description,
		Role: projectdomain.RoleAdmin,
	}, "019ff544-405c-7d10-8f10-cb3fc579605c", "019ff544-405c-7d64-9f10-cb3fc579605c")
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
	}, "019ff544-405c-7d10-8f10-cb3fc579605c", "019ff544-405c-7d62-9f10-cb3fc579605c", false)
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
	}, "019ff544-405c-7d10-8f10-cb3fc579605c", "019ff544-405c-7d63-9f10-cb3fc579605c", true)
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
	}, "019ff544-405c-7d10-8f10-cb3fc579605c", "019ff544-405c-7d33-9f10-cb3fc579605c")
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
		"019ff544-405c-7d10-8f10-cb3fc579605c", "019ff544-405c-7d34-9f10-cb3fc579605c")
	if err != nil {
		t.Fatalf("update incident through repository: %v", err)
	}
	if updated.Status != incidentdomain.StatusRecovered || updated.Version != 2 {
		t.Fatalf("updated incident = %#v", updated)
	}

	otherProject, err := projectRepository.CreateProject(ctx, projectdomain.Project{
		ID: "019ff544-405c-7d51-9f10-cb3fc579605c", Key: "other-integration-project", Name: "Other integration project",
		Role: projectdomain.RoleAdmin,
	}, "019ff544-405c-7d12-8f10-cb3fc579605c", "019ff544-405c-7d52-9f10-cb3fc579605c")
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
	auditEvents, err := projectRepository.ListAuditEvents(ctx, project.ID, 20)
	if err != nil || len(auditEvents.Items) < 9 {
		t.Fatalf("audit events = %#v, %v", auditEvents, err)
	}
	assertProjectUpdateAudits(t, auditEvents.Items)
	auditJSON, err := json.Marshal(auditEvents.Items)
	if err != nil {
		t.Fatalf("marshal audit events: %v", err)
	}
	for _, material := range secretMaterials {
		if strings.Contains(string(auditJSON), material) {
			t.Fatalf("audit events disclosed encrypted material: %s", auditJSON)
		}
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

func assertProjectUpdateAudits(t *testing.T, events []projectdomain.AuditEvent) {
	t.Helper()
	var renamed, secretUpdated int
	for _, event := range events {
		switch event.Action {
		case "project.renamed":
			renamed++
			assertJSONObjectKeys(t, event.Metadata, "projectKey")
		case "project.secret.updated":
			secretUpdated++
			assertJSONObjectKeys(t, event.Metadata, "name", "kind", "rotated")
		}
		payload := string(event.Metadata)
		for _, material := range []string{"encrypted-git-material", "encrypted-source-material", "encrypted-webhook-material", "rotated-git-material"} {
			if strings.Contains(payload, material) {
				t.Fatalf("audit metadata disclosed secret material: %s", payload)
			}
		}
	}
	if renamed != 2 || secretUpdated != 2 {
		t.Fatalf("update audit counts renamed=%d secretUpdated=%d events=%#v", renamed, secretUpdated, events)
	}
}

func assertJSONObjectKeys(t *testing.T, raw []byte, keys ...string) {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode audit metadata %s: %v", raw, err)
	}
	if len(payload) != len(keys) {
		t.Fatalf("audit metadata keys = %#v, want %v", payload, keys)
	}
	for _, key := range keys {
		if _, ok := payload[key]; !ok {
			t.Fatalf("audit metadata missing %q: %#v", key, payload)
		}
	}
}
