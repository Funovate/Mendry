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
	if len(migrations) != 3 || migrations[0].Version != 1 || migrations[1].Version != 2 || migrations[2].Version != 3 {
		t.Fatalf("migrations = %#v", migrations)
	}
	if migrations[1].Name != "create_mvp_data" {
		t.Fatalf("migration 000002 name = %q", migrations[1].Name)
	}
	if migrations[2].Name != "document_schema" {
		t.Fatalf("migration 000003 name = %q", migrations[2].Name)
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
}
