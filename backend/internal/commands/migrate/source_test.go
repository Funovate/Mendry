package migrate

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestLoadMigrationsOrdersAndChecksumsFiles(t *testing.T) {
	filesystem := fstest.MapFS{
		"migrations/000002_second.up.sql": {Data: []byte("SELECT 2;")},
		"migrations/000001_first.up.sql":  {Data: []byte("SELECT 1;")},
	}
	migrations, err := loadMigrations(filesystem)
	if err != nil {
		t.Fatalf("loadMigrations() error = %v", err)
	}
	if len(migrations) != 2 || migrations[0].Version != 1 || migrations[1].Version != 2 {
		t.Fatalf("migrations = %#v", migrations)
	}
	if len(migrations[0].Checksum) != 64 || migrations[0].Name != "first" {
		t.Fatalf("migration = %#v", migrations[0])
	}
}

func TestLoadMigrationsRejectsGapsAndUnexpectedFiles(t *testing.T) {
	tests := []struct {
		name       string
		filesystem fstest.MapFS
	}{
		{name: "gap", filesystem: fstest.MapFS{"migrations/000002_second.up.sql": {Data: []byte("SELECT 2;")}}},
		{name: "unexpected-name", filesystem: fstest.MapFS{"migrations/README.md": {Data: []byte("not a migration")}}},
		{name: "empty", filesystem: fstest.MapFS{"migrations/000001_empty.up.sql": {Data: nil}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := loadMigrations(test.filesystem); err == nil {
				t.Fatal("loadMigrations() error = nil")
			}
		})
	}
}

func TestEmbeddedMigrationsAreValid(t *testing.T) {
	migrations, err := loadMigrations(embeddedMigrations)
	if err != nil {
		t.Fatalf("loadMigrations() error = %v", err)
	}
	if len(migrations) != 11 || migrations[0].Version != 1 || migrations[1].Version != 2 || migrations[2].Version != 3 || migrations[3].Version != 4 || migrations[4].Version != 5 || migrations[5].Version != 6 || migrations[6].Version != 7 || migrations[7].Version != 8 || migrations[8].Version != 9 || migrations[9].Version != 10 || migrations[10].Version != 11 {
		t.Fatalf("migrations = %#v", migrations)
	}
	if migrations[1].Name != "create_mvp_data" {
		t.Fatalf("migration 000002 name = %q", migrations[1].Name)
	}
	if migrations[2].Name != "document_schema" {
		t.Fatalf("migration 000003 name = %q", migrations[2].Name)
	}
	if migrations[3].Name != "project_scope" {
		t.Fatalf("migration 000004 name = %q", migrations[3].Name)
	}
	if migrations[4].Name != "add_incident_remediation_fields" {
		t.Fatalf("migration 000005 name = %q", migrations[4].Name)
	}
	if migrations[5].Name != "remediation_persistence" {
		t.Fatalf("migration 000006 name = %q", migrations[5].Name)
	}
	if migrations[6].Name != "remediation_review_surface" {
		t.Fatalf("migration 000007 name = %q", migrations[6].Name)
	}
	if migrations[7].Name != "document_remediation_schema" {
		t.Fatalf("migration 000008 name = %q", migrations[7].Name)
	}
	if migrations[8].Name != "project_llm_provider" {
		t.Fatalf("migration 000009 name = %q", migrations[8].Name)
	}

	if migrations[9].Name != "webhook_ingress_token" {
		t.Fatalf("migration 000010 name = %q", migrations[9].Name)
	}
	if migrations[10].Name != "remove_configuration_names" {
		t.Fatalf("migration 000011 name = %q", migrations[10].Name)
	}
	requiredSchema := []string{
		"CREATE TABLE users",
		"CONSTRAINT users_role_known",
		"CREATE TABLE incidents",
		"CONSTRAINT incidents_status_known",
		"CONSTRAINT incidents_priority_known",
		"CREATE UNIQUE INDEX incidents_fingerprint_unique_idx",
	}
	for _, fragment := range requiredSchema {
		if !strings.Contains(migrations[1].SQL, fragment) {
			t.Errorf("migration 000002 does not contain %q", fragment)
		}
	}
	if strings.Contains(migrations[1].SQL, "job_outbox") {
		t.Fatal("migration 000002 still creates job_outbox")
	}

	requiredProjectSchema := []string{
		"CREATE TABLE projects",
		"CREATE TABLE project_environments",
		"CREATE TABLE project_memberships",
		"CREATE TABLE project_secrets",
		"CREATE TABLE project_repositories",
		"CREATE TABLE project_sources",
		"CREATE TABLE project_triggers",
		"CREATE TABLE observations",
		"CREATE TABLE audit_events",
		"ADD COLUMN project_id uuid",
		"incidents_project_environment_source_same_scope",
		"CREATE UNIQUE INDEX incidents_project_fingerprint_unique_idx",
	}
	for _, fragment := range requiredProjectSchema {
		if !strings.Contains(migrations[3].SQL, fragment) {
			t.Errorf("migration 000004 does not contain %q", fragment)
		}
	}
	for _, immutableMigrationFragment := range []string{"ALTER TABLE incidents", "legacy", "COMMENT ON TABLE projects"} {
		if strings.Contains(migrations[1].SQL, immutableMigrationFragment) || strings.Contains(migrations[2].SQL, immutableMigrationFragment) {
			t.Errorf("project scope fragment %q leaked into an already applied migration", immutableMigrationFragment)
		}
	}

	commentedColumns := map[string][]string{
		"fixthe_schema_migrations": {"version", "name", "checksum", "applied_at"},
		"users":                    {"id", "username", "password_hash", "role", "enabled", "version", "created_at", "updated_at"},
		"incidents": {
			"id", "incident_number", "title", "fingerprint", "status", "priority", "source",
			"first_seen", "last_seen", "occurrence_count", "host_count", "muted",
			"notification_summary", "version", "created_at", "updated_at",
		},
	}
	if tableCommentCount := strings.Count(migrations[2].SQL, "COMMENT ON TABLE "); tableCommentCount != len(commentedColumns) {
		t.Errorf("migration 000003 table comment count = %d, want %d", tableCommentCount, len(commentedColumns))
	}
	expectedColumnCommentCount := 0
	for table, columns := range commentedColumns {
		expectedColumnCommentCount += len(columns)
		if !strings.Contains(migrations[2].SQL, "COMMENT ON TABLE "+table+" IS") {
			t.Errorf("migration 000003 does not document table %q", table)
		}
		for _, column := range columns {
			fragment := "COMMENT ON COLUMN " + table + "." + column + " IS"
			if !strings.Contains(migrations[2].SQL, fragment) {
				t.Errorf("migration 000003 does not document column %q", table+"."+column)
			}
		}
	}
	if columnCommentCount := strings.Count(migrations[2].SQL, "COMMENT ON COLUMN "); columnCommentCount != expectedColumnCommentCount {
		t.Errorf("migration 000003 column comment count = %d, want %d", columnCommentCount, expectedColumnCommentCount)
	}

	commentedRemediationColumns := map[string][]string{
		"remediation_series": {
			"id", "incident_id", "lifecycle_generation", "deployed_commit", "created_at",
		},
		"remediation_run": {
			"id", "series_id", "attempt_number", "state", "started_at", "ended_at", "elapsed_ms",
			"model_calls", "model_tokens_in", "model_tokens_out", "model_cost_cents",
			"model_provider", "model_name", "tool_calls", "evidence_bytes", "repository_bytes", "version",
		},
		"remediation_decision": {
			"id", "run_id", "sequence", "fixability_class", "confidence_score", "reasoning",
			"decided_at", "contradictions", "missing_evidence", "evidence_citations", "recommended_next_action",
		},
		"remediation_plan": {
			"id", "run_id", "sequence", "title", "rationale", "risk_class", "is_recommended",
			"created_at", "plan_key", "evidence_refs", "affected_files", "rollback_strategy",
		},
		"remediation_artifact": {
			"id", "run_id", "artifact_type", "reference_path", "content_hash", "size_bytes", "created_at", "excerpt",
		},
		"remediation_tool_invocation": {
			"id", "run_id", "sequence", "tool_name", "phase", "invoked_at", "duration_ms", "outcome",
		},
	}
	if tableCommentCount := strings.Count(migrations[7].SQL, "COMMENT ON TABLE "); tableCommentCount != len(commentedRemediationColumns) {
		t.Errorf("migration 000008 table comment count = %d, want %d", tableCommentCount, len(commentedRemediationColumns))
	}
	expectedRemediationColumnCommentCount := 0
	for table, columns := range commentedRemediationColumns {
		expectedRemediationColumnCommentCount += len(columns)
		if !strings.Contains(migrations[7].SQL, "COMMENT ON TABLE "+table+" IS") {
			t.Errorf("migration 000008 does not document table %q", table)
		}
		for _, column := range columns {
			fragment := "COMMENT ON COLUMN " + table + "." + column + " IS"
			if !strings.Contains(migrations[7].SQL, fragment) {
				t.Errorf("migration 000008 does not document column %q", table+"."+column)
			}
		}
	}
	if columnCommentCount := strings.Count(migrations[7].SQL, "COMMENT ON COLUMN "); columnCommentCount != expectedRemediationColumnCommentCount {
		t.Errorf("migration 000008 column comment count = %d, want %d", columnCommentCount, expectedRemediationColumnCommentCount)
	}

	requiredConfigurationNameRemoval := []string{
		"DROP CONSTRAINT project_sources_project_name_unique",
		"DROP CONSTRAINT project_sources_name_bounded",
		"DROP COLUMN name",
		"DROP CONSTRAINT project_triggers_project_name_unique",
		"DROP CONSTRAINT project_triggers_name_bounded",
	}
	for _, fragment := range requiredConfigurationNameRemoval {
		if !strings.Contains(migrations[10].SQL, fragment) {
			t.Errorf("migration 000011 does not contain %q", fragment)
		}
	}

	requiredWebhookTokenSchema := []string{
		"ADD COLUMN ingress_token_hash bytea",
		"ADD COLUMN ingress_token_ciphertext bytea",
		"ADD COLUMN ingress_token_nonce bytea",
		"project_triggers_ingress_token_all_or_nothing",
		"CREATE UNIQUE INDEX project_triggers_ingress_token_hash_unique_idx",
		"COMMENT ON COLUMN project_triggers.ingress_token_hash IS",
		"COMMENT ON COLUMN project_triggers.ingress_token_ciphertext IS",
		"COMMENT ON COLUMN project_triggers.ingress_token_nonce IS",
	}
	for _, fragment := range requiredWebhookTokenSchema {
		if !strings.Contains(migrations[9].SQL, fragment) {
			t.Errorf("migration 000010 does not contain %q", fragment)
		}
	}
}
