// Package integration_test 验证显式提供的外部依赖与后端真实边界能够协同工作。
// 测试不会 provision 服务，并在目标缺少隔离证明时拒绝执行破坏性准备操作。
package integration_test

import (
	"fmt"
	"strings"
	"testing"
	"unicode"

	"github.com/jackc/pgx/v5"
)

const (
	testPostgresURLKey       = "MENDRY_TEST_POSTGRES_URL"
	testPostgresIsolationKey = "MENDRY_TEST_POSTGRES_ISOLATION"
)

func TestIsolatedPostgresTargetFailsClosed(t *testing.T) {
	const secret = "do-not-print-this-password"
	tests := []struct {
		name   string
		values map[string]string
	}{
		{name: "missing values"},
		{name: "invalid URL", values: map[string]string{
			testPostgresURLKey: "postgres://user:" + secret + "@%invalid/database",
		}},
		{name: "non-test database", values: map[string]string{
			testPostgresURLKey:       "postgres://localhost/mendry_production",
			testPostgresIsolationKey: "mendry_production",
		}},
		{name: "test substring is not marker", values: map[string]string{
			testPostgresURLKey:       "postgres://localhost/contest_production",
			testPostgresIsolationKey: "contest_production",
		}},
		{name: "isolation mismatch", values: map[string]string{
			testPostgresURLKey:       "postgres://localhost/mendry_test",
			testPostgresIsolationKey: "another_test",
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := isolatedPostgresTarget(mapLookup(test.values))
			if err == nil {
				t.Fatal("isolatedPostgresTarget() error = nil")
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error exposes connection credential: %q", err)
			}
		})
	}
}

func TestIsolatedPostgresTargetAcceptsExplicitTestDatabase(t *testing.T) {
	const connectionURL = "postgres://localhost/mendry_test?sslmode=disable"
	url, database, err := isolatedPostgresTarget(mapLookup(map[string]string{
		testPostgresURLKey:       connectionURL,
		testPostgresIsolationKey: "mendry_test",
	}))
	if err != nil {
		t.Fatalf("isolatedPostgresTarget() error = %v", err)
	}
	if url != connectionURL || database != "mendry_test" {
		t.Fatalf("target = %q, %q", url, database)
	}
}

func isolatedPostgresTarget(lookup func(string) (string, bool)) (string, string, error) {
	connectionURL, ok := lookup(testPostgresURLKey)
	if !ok || strings.TrimSpace(connectionURL) == "" || strings.ContainsAny(connectionURL, "\x00\r\n") {
		return "", "", fmt.Errorf("%s is required and must be a single-line pgx connection string", testPostgresURLKey)
	}
	parsed, err := pgx.ParseConfig(connectionURL)
	if err != nil || parsed.Database == "" {
		return "", "", fmt.Errorf("%s must contain a valid database name", testPostgresURLKey)
	}
	if !hasTestMarker(parsed.Database) {
		return "", "", fmt.Errorf("%s database name must contain a distinct test marker", testPostgresURLKey)
	}
	isolation, ok := lookup(testPostgresIsolationKey)
	if !ok || isolation == "" || isolation != parsed.Database {
		return "", "", fmt.Errorf("%s must exactly match the database name", testPostgresIsolationKey)
	}
	return connectionURL, parsed.Database, nil
}

func hasTestMarker(databaseName string) bool {
	parts := strings.FieldsFunc(strings.ToLower(databaseName), func(character rune) bool {
		return !unicode.IsLetter(character) && !unicode.IsDigit(character)
	})
	for _, part := range parts {
		if part == "test" {
			return true
		}
	}
	return false
}

func mapLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}
