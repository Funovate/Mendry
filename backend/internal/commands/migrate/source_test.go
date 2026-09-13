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
	if len(migrations) != 21 {
		t.Fatalf("migration count = %d, want 21", len(migrations))
	}
	for index, migration := range migrations {
		if migration.Version != int64(index+1) {
			t.Fatalf("migration[%d].Version = %d, want %d", index, migration.Version, index+1)
		}
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
	if migrations[11].Name != "remediation_tool_policy" {
		t.Fatalf("migration 000012 name = %q", migrations[11].Name)
	}
	if migrations[12].Name != "remediation_evidence" {
		t.Fatalf("migration 000013 name = %q", migrations[12].Name)
	}
	if migrations[13].Name != "scope_remediation_evidence_dedup" {
		t.Fatalf("migration 000014 name = %q", migrations[13].Name)
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
	if migrations[14].Name != "remediation_continuation" {
		t.Fatalf("migration 000015 name = %q", migrations[14].Name)
	}
	requiredContinuationSchema := []string{
		"ADD COLUMN continuation_of_run_id uuid",
		"ADD COLUMN trigger_reason text NOT NULL DEFAULT ''",
		"ADD COLUMN continuation_reason text NOT NULL DEFAULT ''",
		"ADD COLUMN context_version bigint NOT NULL DEFAULT 0",
		"ADD COLUMN terminal_reason text NOT NULL DEFAULT ''",
		"ADD COLUMN retryable boolean NOT NULL DEFAULT false",
		"remediation_run_continuation_of_run_fk",
		"remediation_run_context_version_nonnegative",
		"COMMENT ON COLUMN remediation_run.continuation_of_run_id IS",
		"COMMENT ON COLUMN remediation_run.trigger_reason IS",
		"COMMENT ON COLUMN remediation_run.continuation_reason IS",
		"COMMENT ON COLUMN remediation_run.context_version IS",
		"COMMENT ON COLUMN remediation_run.terminal_reason IS",
		"COMMENT ON COLUMN remediation_run.retryable IS",
	}
	for _, fragment := range requiredContinuationSchema {
		if !strings.Contains(migrations[14].SQL, fragment) {
			t.Errorf("migration 000015 does not contain %q", fragment)
		}
	}

	if migrations[15].Name != "working_memory_checkpoint" {
		t.Fatalf("migration 000016 name = %q", migrations[15].Name)
	}
	requiredCheckpointSchema := []string{
		"ADD COLUMN agent_loop_policy_version bigint NOT NULL DEFAULT 1",
		"ADD COLUMN agent_loop_mode text NOT NULL DEFAULT 'legacy'",
		"projects_agent_loop_mode_known",
		"remediation_run_agent_loop_mode_known",
		"baseline_lifecycle_generation bigint",
		"baseline_deployed_commit text",
		"remediation_evidence_baseline_dedup_idx",
		"CREATE TABLE remediation_checkpoint_event",
		"remediation_checkpoint_event_run_sequence_unique",
		"remediation_checkpoint_event_sequence_positive",
		"remediation_checkpoint_event_content_hash_sha256",
		"CREATE TABLE remediation_working_memory",
		"observed_run_version bigint NOT NULL",
		"CREATE TABLE remediation_evidence_read_cursor",
		"remediation_working_memory_sequence_positive",
		"remediation_working_memory_context_version_nonnegative",
		"remediation_working_memory_event_backing",
		"remediation_checkpoint_event_immutable",
		"remediation_checkpoint_event_immutable_trigger",
		"RETURN OLD",
		"COMMENT ON COLUMN remediation_run.agent_loop_mode IS",
		"COMMENT ON TABLE remediation_checkpoint_event IS",
		"COMMENT ON TABLE remediation_working_memory IS",
	}
	for _, fragment := range requiredCheckpointSchema {
		if !strings.Contains(migrations[15].SQL, fragment) {
			t.Errorf("migration 000016 does not contain %q", fragment)
		}
	}

	if migrations[16].Name != "submitted_diagnosis" {
		t.Fatalf("migration 000017 name = %q", migrations[16].Name)
	}
	requiredSubmittedDiagnosisSchema := []string{
		"ALTER TABLE remediation_tool_invocation",
		"ADD COLUMN outcome_ref text",
		"ADD COLUMN evidence_ids text[] NOT NULL",
		"ADD COLUMN error_code text",
		"remediation_tool_invocation_run_outcome_ref_unique",
		"COMMENT ON COLUMN remediation_tool_invocation.outcome_ref IS",
		"COMMENT ON COLUMN remediation_tool_invocation.evidence_ids IS",
		"COMMENT ON COLUMN remediation_tool_invocation.error_code IS",
		"CREATE TABLE remediation_submitted_diagnosis",
		"remediation_submitted_diagnosis_run_sequence_unique",
		"remediation_submitted_diagnosis_sequence_positive",
		"remediation_submitted_diagnosis_fixability_known",
		"remediation_submitted_diagnosis_confidence_valid",
		"remediation_submitted_diagnosis_reasoning_bounded",
		"remediation_submitted_diagnosis_correction_kind_known",
		"remediation_submitted_diagnosis_correction_count_valid",
		"remediation_submitted_diagnosis_corrected_consistent",
		"remediation_submitted_diagnosis_gate_outcome_known",
		"idx_remediation_submitted_diagnosis_run",
		"COMMENT ON TABLE remediation_submitted_diagnosis IS",
		"COMMENT ON COLUMN remediation_submitted_diagnosis.correction_evidence IS",
		"COMMENT ON COLUMN remediation_submitted_diagnosis.decision_id IS",
	}
	for _, fragment := range requiredSubmittedDiagnosisSchema {
		if !strings.Contains(migrations[16].SQL, fragment) {
			t.Errorf("migration 000017 does not contain %q", fragment)
		}
	}

	if migrations[17].Name != "remediation_lifecycle_effect" {
		t.Fatalf("migration 000018 name = %q", migrations[17].Name)
	}
	requiredLifecycleSchema := []string{
		"CREATE TABLE remediation_lifecycle_effect",
		"remediation_lifecycle_effect_run_key_unique",
		"remediation_lifecycle_effect_state_known",
		"remediation_lifecycle_effect_baseline_bounded",
		"COMMENT ON TABLE remediation_lifecycle_effect IS",
		"COMMENT ON COLUMN remediation_lifecycle_effect.idempotency_key IS",
		"COMMENT ON COLUMN remediation_lifecycle_effect.validation_known IS",
		"COMMENT ON COLUMN remediation_lifecycle_effect.compare_url IS",
	}
	for _, fragment := range requiredLifecycleSchema {
		if !strings.Contains(migrations[17].SQL, fragment) {
			t.Errorf("migration 000018 does not contain %q", fragment)
		}
	}
	if migrations[18].Name != "remediation_analysis_only" || !strings.Contains(migrations[18].SQL, "ADD COLUMN analysis_only boolean NOT NULL DEFAULT false") {
		t.Fatalf("migration 000019 is invalid: %#v", migrations[18])
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
	requiredToolPolicySchema := []string{
		"CREATE TABLE remediation_tool_policy",
		"remediation_tool_policy_source_same_project",
		"remediation_tool_policy_version_positive",
		"remediation_tool_policy_hash_bounded",
		"remediation_tool_policy_entries_bounded",
	}
	for _, fragment := range requiredToolPolicySchema {
		if !strings.Contains(migrations[11].SQL, fragment) {
			t.Errorf("migration 000012 does not contain %q", fragment)
		}
	}
	requiredEvidenceSchema := []string{
		"CREATE TABLE remediation_evidence",
		"CREATE TABLE remediation_evidence_assessment",
		"remediation_evidence_project_environment_source_same_scope",
		"remediation_evidence_classification_known",
		"remediation_evidence_payload_bounded",
		"remediation_evidence_assessment_confidence_cap_valid",
		"remediation_evidence_assessment_arrays_bounded",
	}
	for _, fragment := range requiredEvidenceSchema {
		if !strings.Contains(migrations[12].SQL, fragment) {
			t.Errorf("migration 000013 does not contain %q", fragment)
		}
	}
	requiredEvidenceDedupScope := []string{
		"DROP INDEX remediation_evidence_project_dedup_idx",
		"evidence.incident_id <> series.incident_id",
		"SET run_id = NULL",
		"remediation_evidence_scope_dedup_idx",
		"(project_id, incident_id, run_id, deduplication_key)",
		"NULLS NOT DISTINCT",
	}
	for _, fragment := range requiredEvidenceDedupScope {
		if !strings.Contains(migrations[13].SQL, fragment) {
			t.Errorf("migration 000014 does not contain %q", fragment)
		}
	}
}
