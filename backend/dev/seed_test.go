package dev

import (
	"strings"
	"testing"
)

func TestIncidentSeedIsExplicitAndIdempotent(t *testing.T) {
	seed := SeedSQL()
	for _, insertion := range []string{
		"INSERT INTO projects", "INSERT INTO project_environments",
		"INSERT INTO project_repositories", "INSERT INTO project_sources",
		"INSERT INTO project_triggers", "INSERT INTO incidents",
	} {
		if !strings.Contains(seed, insertion) {
			t.Errorf("seed.sql does not contain %q", insertion)
		}
	}
	if !strings.Contains(seed, "ON CONFLICT DO NOTHING") || !strings.Contains(seed, "project_id, environment_id, source_id") {
		t.Fatal("seed.sql must contain idempotent project-scoped incident fixtures")
	}
	if strings.Contains(seed, "INSERT INTO users") || strings.Contains(seed, "password_hash") ||
		strings.Contains(seed, "INSERT INTO project_secrets") || strings.Contains(seed, "ciphertext") {
		t.Fatal("seed.sql must not create users or credentials")
	}
}
