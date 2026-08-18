package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWithOptionalDotEnvMissingFileUsesBase(t *testing.T) {
	t.Parallel()

	base := mapLookup(map[string]string{LogLevelKey: "warn"})
	lookup, err := WithOptionalDotEnv(base, filepath.Join(t.TempDir(), ".env"))
	if err != nil {
		t.Fatalf("WithOptionalDotEnv() error = %v", err)
	}

	value, ok := lookup(LogLevelKey)
	if !ok || value != "warn" {
		t.Fatalf("lookup(%s) = %q, %v", LogLevelKey, value, ok)
	}
	if _, ok := lookup(PostgresURLKey); !ok {
		t.Fatal("missing file must still expose the injected base lookup")
	}
}

func TestWithOptionalDotEnvProcessEnvironmentWins(t *testing.T) {
	t.Parallel()

	path := writeDotEnv(t, "FIXTHE_LOG_LEVEL=debug\nFIXTHE_HTTP_ADDR=0.0.0.0:9000\n")
	lookup, err := WithOptionalDotEnv(mapLookup(map[string]string{LogLevelKey: "error"}), path)
	if err != nil {
		t.Fatalf("WithOptionalDotEnv() error = %v", err)
	}

	if value, ok := lookup(LogLevelKey); !ok || value != "error" {
		t.Fatalf("lookup(%s) = %q, %v", LogLevelKey, value, ok)
	}
	if value, ok := lookup(HTTPAddressKey); !ok || value != "0.0.0.0:9000" {
		t.Fatalf("lookup(%s) = %q, %v", HTTPAddressKey, value, ok)
	}
}

func TestWithOptionalDotEnvParsesSupportedSyntax(t *testing.T) {
	t.Parallel()

	path := writeDotEnv(t, "\ufeff# local overlay\n\nFIXTHE_ENVIRONMENT=development\nFIXTHE_HTTP_CORS_ALLOWED_ORIGIN=\nFIXTHE_POSTGRES_URL=\"postgres://fixthe:secret@127.0.0.1:5432/fixthe?sslmode=disable\"\nFIXTHE_REDIS_URL='redis://:password@127.0.0.1:6379'\n")
	lookup, err := WithOptionalDotEnv(func(string) (string, bool) { return "", false }, path)
	if err != nil {
		t.Fatalf("WithOptionalDotEnv() error = %v", err)
	}

	cases := map[string]string{
		EnvironmentKey:           "development",
		HTTPCORSAllowedOriginKey: "",
		PostgresURLKey:           "postgres://fixthe:secret@127.0.0.1:5432/fixthe?sslmode=disable",
		RedisURLKey:              "redis://:password@127.0.0.1:6379",
	}
	for key, want := range cases {
		value, ok := lookup(key)
		if !ok || value != want {
			t.Fatalf("lookup(%s) = %q, %v", key, value, ok)
		}
	}
}

func TestWithOptionalDotEnvRejectsMalformedLinesWithoutRawValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		reason  string
		secret  string
	}{
		{name: "missing assignment", content: "FIXTHE_LOG_LEVEL\n", reason: "must be KEY=VALUE", secret: "FIXTHE_LOG_LEVEL"},
		{name: "export prefix", content: "export FIXTHE_LOG_LEVEL=debug\n", reason: "must be KEY=VALUE", secret: "debug"},
		{name: "invalid key", content: "1LEVEL=debug\n", reason: "has an invalid key", secret: "debug"},
		{name: "inline comment", content: "FIXTHE_LOG_LEVEL=debug # local\n", reason: "has an inline comment", secret: "debug"},
		{name: "unclosed quote", content: "FIXTHE_LOG_LEVEL=\"debug\n", reason: "has invalid quoting", secret: "debug"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			path := writeDotEnv(t, test.content)
			_, err := WithOptionalDotEnv(func(string) (string, bool) { return "", false }, path)
			if err == nil {
				t.Fatal("WithOptionalDotEnv() error = nil")
			}
			message := err.Error()
			if !strings.Contains(message, path+":1") || !strings.Contains(message, test.reason) {
				t.Fatalf("error = %q", message)
			}
			if strings.Contains(message, test.secret) && test.secret != "FIXTHE_LOG_LEVEL" {
				t.Fatalf("error echoed raw value: %q", message)
			}
		})
	}
}

func TestWithOptionalDotEnvRequiresLookup(t *testing.T) {
	t.Parallel()

	_, err := WithOptionalDotEnv(nil, ".env")
	if err == nil {
		t.Fatal("WithOptionalDotEnv() error = nil")
	}
	if !strings.Contains(err.Error(), "lookup is required") {
		t.Fatalf("error = %q", err)
	}
}

func writeDotEnv(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write dotenv: %v", err)
	}
	return path
}

func TestWithOptionalDotEnvAcceptsEnvironmentExample(t *testing.T) {
	t.Parallel()

	lookup, err := WithOptionalDotEnv(func(string) (string, bool) { return "", false }, "../../../.env.example")
	if err != nil {
		t.Fatalf("WithOptionalDotEnv(.env.example) error = %v", err)
	}
	if value, ok := lookup(EnvironmentKey); !ok || value != "development" {
		t.Fatalf("lookup(%s) = %q, %v", EnvironmentKey, value, ok)
	}
	if value, ok := lookup(HTTPCORSAllowedOriginKey); !ok || value != "" {
		t.Fatalf("lookup(%s) = %q, %v", HTTPCORSAllowedOriginKey, value, ok)
	}
	if _, ok := lookup(BootstrapAdminPasswordKey); ok {
		t.Fatal("commented bootstrap password must stay unset")
	}
}
