package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadAPIDefaults(t *testing.T) {
	configuration, err := LoadAPI(mapLookup(nil))
	if err != nil {
		t.Fatalf("LoadAPI() error = %v", err)
	}

	if configuration.Common.Environment != "development" {
		t.Fatalf("Environment = %q", configuration.Common.Environment)
	}
	if configuration.Common.LogLevel != "info" {
		t.Fatalf("LogLevel = %q", configuration.Common.LogLevel)
	}
	if configuration.Common.LogFormat != "console" {
		t.Fatalf("LogFormat = %q", configuration.Common.LogFormat)
	}
	if configuration.Common.ShutdownTimeout != 15*time.Second {
		t.Fatalf("ShutdownTimeout = %s", configuration.Common.ShutdownTimeout)
	}
	if configuration.HTTP.Address != "127.0.0.1:8080" {
		t.Fatalf("Address = %q", configuration.HTTP.Address)
	}
	if configuration.HTTP.MaxBodyBytes != 1024*1024 || configuration.HTTP.CORSAllowedOrigin != "" || configuration.HTTP.RequestDebug {
		t.Fatalf("HTTP boundary = %#v", configuration.HTTP)
	}
	if configuration.Auth.SessionTTL != 24*time.Hour {
		t.Fatalf("Auth = %#v", configuration.Auth)
	}
	if configuration.WebhookAI.NormalizationTimeout != 60*time.Second {
		t.Fatalf("WebhookAI = %#v", configuration.WebhookAI)
	}
	if len(configuration.Encryption.Key) != 32 {
		t.Fatalf("Encryption key length = %d", len(configuration.Encryption.Key))
	}
	if configuration.PublicURL != testPublicURL {
		t.Fatalf("PublicURL = %q", configuration.PublicURL)
	}
	if configuration.PostgreSQL.URL != testPostgresURL || configuration.PostgreSQL.MinConnections != 1 || configuration.PostgreSQL.MaxConnections != 10 {
		t.Fatalf("PostgreSQL = %#v", configuration.PostgreSQL)
	}
	if configuration.PostgreSQL.ConnectTimeout != 5*time.Second || configuration.PostgreSQL.StatementTimeout != 30*time.Second {
		t.Fatalf("PostgreSQL = %#v", configuration.PostgreSQL)
	}
}

func TestLoadAPICustomValues(t *testing.T) {
	configuration, err := LoadAPI(mapLookup(map[string]string{
		EnvironmentKey:           "production",
		LogLevelKey:              "warn",
		ShutdownKey:              "30s",
		HTTPAddressKey:           "0.0.0.0:9000",
		HTTPReadHeaderKey:        "2s",
		HTTPReadKey:              "10s",
		HTTPWriteKey:             "20s",
		HTTPIdleKey:              "90s",
		HTTPMaxBodyBytesKey:      "2097152",
		HTTPCORSAllowedOriginKey: "https://console.example.com",
		AuthSessionTTLKey:        "12h",
		WebhookAITimeoutKey:      "75s",
		EncryptionKey:            "MTIzNDU2Nzg5MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI=",
		PublicURLKey:             "https://mendry.example.com/ingress",
	}))
	if err != nil {
		t.Fatalf("LoadAPI() error = %v", err)
	}

	if configuration.Common.Environment != "production" || configuration.Common.LogLevel != "warn" || configuration.Common.LogFormat != "json" {
		t.Fatalf("Common = %#v", configuration.Common)
	}
	if configuration.HTTP.Address != "0.0.0.0:9000" || configuration.HTTP.IdleTimeout != 90*time.Second {
		t.Fatalf("HTTP = %#v", configuration.HTTP)
	}
	if configuration.HTTP.MaxBodyBytes != 2*1024*1024 || configuration.HTTP.CORSAllowedOrigin != "https://console.example.com" {
		t.Fatalf("HTTP boundary = %#v", configuration.HTTP)
	}
	if configuration.Auth.SessionTTL != 12*time.Hour {
		t.Fatalf("Auth = %#v", configuration.Auth)
	}
	if configuration.WebhookAI.NormalizationTimeout != 75*time.Second {
		t.Fatalf("WebhookAI = %#v", configuration.WebhookAI)
	}
	if configuration.PublicURL != "https://mendry.example.com/ingress" {
		t.Fatalf("PublicURL = %q", configuration.PublicURL)
	}
}

func TestLoadAPIRemediationModelTimeout(t *testing.T) {
	configuration, err := LoadAPI(mapLookup(nil))
	if err != nil {
		t.Fatalf("LoadAPI() error = %v", err)
	}
	artifactRoot, err := filepath.Abs(filepath.Join(".var", "remediation", "artifacts"))
	if err != nil {
		t.Fatalf("resolve default artifact root: %v", err)
	}
	workspaceRoot, err := filepath.Abs(filepath.Join(".var", "remediation", "workspaces"))
	if err != nil {
		t.Fatalf("resolve default workspace root: %v", err)
	}
	if configuration.Remediation.ModelTurnTimeout != 5*time.Minute || configuration.Remediation.DockerCommand != "docker" || configuration.Remediation.GitCommand != "git" ||
		configuration.Remediation.ArtifactRoot != artifactRoot || configuration.Remediation.WorkspaceRoot != workspaceRoot {
		t.Fatalf("Remediation = %#v", configuration.Remediation)
	}

	configuration, err = LoadAPI(mapLookup(map[string]string{RemediationModelTimeoutKey: "7m"}))
	if err != nil {
		t.Fatalf("LoadAPI() error = %v", err)
	}
	if configuration.Remediation.ModelTurnTimeout != 7*time.Minute {
		t.Fatalf("Remediation = %#v", configuration.Remediation)
	}

	for _, value := range []string{"29s", "21m", "not-a-duration"} {
		_, err = LoadAPI(mapLookup(map[string]string{RemediationModelTimeoutKey: value}))
		if err == nil || !strings.Contains(err.Error(), RemediationModelTimeoutKey) {
			t.Fatalf("LoadAPI(%q) error = %v", value, err)
		}
		if strings.Contains(err.Error(), value) {
			t.Fatalf("error %q contains raw value", err)
		}
	}
}

func TestLoadAPIRemediationStorageRequiresPairedSeparateAbsoluteRoots(t *testing.T) {
	valid := map[string]string{
		RemediationArtifactRootKey: "/srv/mendry/artifacts", RemediationWorkspaceRootKey: "/srv/mendry/workspaces",
		RemediationDockerCommandKey: "/usr/bin/docker", RemediationGitCommandKey: "/usr/bin/git",
		RemediationSSHKnownHostsFileKey: "/etc/ssh/known_hosts",
		RemediationGoBuilderImageKey:    "registry.example/mendry/go@sha256:" + strings.Repeat("a", 64),
		RemediationNodeBuilderImageKey:  "sha256:" + strings.Repeat("b", 64),
	}
	configuration, err := LoadAPI(mapLookup(valid))
	if err != nil {
		t.Fatalf("LoadAPI() error = %v", err)
	}
	if configuration.Remediation.ArtifactRoot != valid[RemediationArtifactRootKey] || configuration.Remediation.WorkspaceRoot != valid[RemediationWorkspaceRootKey] ||
		configuration.Remediation.DockerCommand != valid[RemediationDockerCommandKey] || configuration.Remediation.GitCommand != valid[RemediationGitCommandKey] ||
		configuration.Remediation.SSHKnownHostsFile != valid[RemediationSSHKnownHostsFileKey] ||
		configuration.Remediation.GoBuilderImage != valid[RemediationGoBuilderImageKey] || configuration.Remediation.NodeBuilderImage != valid[RemediationNodeBuilderImageKey] {
		t.Fatalf("Remediation storage = %#v", configuration.Remediation)
	}

	invalid := []map[string]string{
		{RemediationArtifactRootKey: "/srv/mendry/artifacts"},
		{RemediationArtifactRootKey: "relative/artifacts", RemediationWorkspaceRootKey: "/srv/mendry/workspaces"},
		{RemediationArtifactRootKey: "/srv/mendry/data/artifacts", RemediationWorkspaceRootKey: "/srv/mendry/data"},
		{RemediationArtifactRootKey: "/srv/mendry/artifacts", RemediationWorkspaceRootKey: "/srv/mendry/workspaces", RemediationSSHKnownHostsFileKey: "known_hosts"},
		{RemediationGoBuilderImageKey: "golang:1.23-bookworm"},
		{RemediationNodeBuilderImageKey: "node@example:latest"},
	}
	for _, values := range invalid {
		if _, err := LoadAPI(mapLookup(values)); err == nil {
			t.Fatalf("LoadAPI(%v) unexpectedly succeeded", values)
		}
	}
}

func TestLoadCommonLogFormatDefaultsAndOverrides(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
		want   string
	}{
		{name: "development default", values: map[string]string{EnvironmentKey: "development"}, want: "console"},
		{name: "test default", values: map[string]string{EnvironmentKey: "test"}, want: "json"},
		{name: "staging default", values: map[string]string{EnvironmentKey: "staging"}, want: "json"},
		{name: "production default", values: map[string]string{EnvironmentKey: "production"}, want: "json"},
		{name: "development override", values: map[string]string{EnvironmentKey: "development", LogFormatKey: "json"}, want: "json"},
		{name: "production override", values: map[string]string{EnvironmentKey: "production", LogFormatKey: "console"}, want: "console"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configuration, err := LoadCommon(mapLookup(test.values))
			if err != nil {
				t.Fatalf("LoadCommon() error = %v", err)
			}
			if configuration.LogFormat != test.want {
				t.Fatalf("LogFormat = %q, want %q", configuration.LogFormat, test.want)
			}
		})
	}
}

func TestLoadCommonLogFileIsOptionalAndValidated(t *testing.T) {
	configuration, err := LoadCommon(mapLookup(nil))
	if err != nil {
		t.Fatalf("LoadCommon() error = %v", err)
	}
	if configuration.LogFile != "" {
		t.Fatalf("LogFile = %q, want disabled", configuration.LogFile)
	}

	configuration, err = LoadCommon(mapLookup(map[string]string{LogFileKey: " ./logs/mendry.log "}))
	if err != nil {
		t.Fatalf("LoadCommon() error = %v", err)
	}
	if configuration.LogFile != "./logs/mendry.log" {
		t.Fatalf("LogFile = %q", configuration.LogFile)
	}

	const invalidPath = "logs/bad\nsecret.log"
	_, err = LoadCommon(mapLookup(map[string]string{LogFileKey: invalidPath}))
	if err == nil || !strings.Contains(err.Error(), LogFileKey) {
		t.Fatalf("LoadCommon() error = %v", err)
	}
	if strings.Contains(err.Error(), invalidPath) {
		t.Fatalf("error %q contains raw log path", err)
	}
}

func TestLoadPostgreSQLCustomValues(t *testing.T) {
	configuration, err := LoadPostgreSQL(mapLookup(map[string]string{
		PostgresConnectTimeoutKey:   "3s",
		PostgresAcquireTimeoutKey:   "1s",
		PostgresStatementTimeoutKey: "45s",
		PostgresHealthTimeoutKey:    "1500ms",
		PostgresMinConnsKey:         "2",
		PostgresMaxConnsKey:         "12",
		PostgresMaxLifetimeKey:      "1h",
		PostgresMaxIdleTimeKey:      "10m",
		PostgresHealthPeriodKey:     "20s",
	}))
	if err != nil {
		t.Fatalf("LoadPostgreSQL() error = %v", err)
	}
	if configuration.MinConnections != 2 || configuration.MaxConnections != 12 {
		t.Fatalf("PostgreSQL = %#v", configuration)
	}
	if configuration.AcquireTimeout != time.Second || configuration.HealthTimeout != 1500*time.Millisecond {
		t.Fatalf("PostgreSQL = %#v", configuration)
	}
	if configuration.QueryDebug {
		t.Fatalf("QueryDebug default = %#v", configuration)
	}
}

func TestLoadPostgreSQLQueryDebug(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "true", raw: "true", want: true},
		{name: "TRUE", raw: "TRUE", want: true},
		{name: "false", raw: "false", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configuration, err := LoadPostgreSQL(mapLookup(map[string]string{PostgresQueryDebugKey: test.raw}))
			if err != nil {
				t.Fatalf("LoadPostgreSQL() error = %v", err)
			}
			if configuration.QueryDebug != test.want {
				t.Fatalf("QueryDebug = %v, want %v", configuration.QueryDebug, test.want)
			}
		})
	}
}

func TestLoadHTTPRequestDebug(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "true", raw: "true", want: true},
		{name: "TRUE", raw: "TRUE", want: true},
		{name: "false", raw: "false", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configuration, err := LoadAPI(mapLookup(map[string]string{HTTPRequestDebugKey: test.raw}))
			if err != nil {
				t.Fatalf("LoadAPI() error = %v", err)
			}
			if configuration.HTTP.RequestDebug != test.want {
				t.Fatalf("RequestDebug = %v, want %v", configuration.HTTP.RequestDebug, test.want)
			}
		})
	}
}

func TestLoadPostgreSQLRejectsUnsafeValuesWithoutRawInput(t *testing.T) {
	const secretURL = "postgres://secret-user:secret-password@db.example.com/app\ninjected"
	tests := []struct {
		name   string
		key    string
		value  string
		values map[string]string
	}{
		{name: "missing-url", key: PostgresURLKey, values: map[string]string{PostgresURLKey: ""}},
		{name: "multiline-url", key: PostgresURLKey, value: secretURL, values: map[string]string{PostgresURLKey: secretURL}},
		{name: "connect-timeout", key: PostgresConnectTimeoutKey, value: "99h", values: map[string]string{PostgresConnectTimeoutKey: "99h"}},
		{name: "min-connections", key: PostgresMinConnsKey, value: "101", values: map[string]string{PostgresMinConnsKey: "101"}},
		{name: "min-exceeds-max", key: PostgresMinConnsKey, values: map[string]string{PostgresMinConnsKey: "11", PostgresMaxConnsKey: "10"}},
		{name: "query-debug", key: PostgresQueryDebugKey, value: "pretty-secret-marker", values: map[string]string{PostgresQueryDebugKey: "pretty-secret-marker"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := LoadPostgreSQL(mapLookup(test.values))
			if err == nil || !strings.Contains(err.Error(), test.key) {
				t.Fatalf("LoadPostgreSQL() error = %v", err)
			}
			if test.value != "" && strings.Contains(err.Error(), test.value) {
				t.Fatalf("error %q contains raw value", err)
			}
		})
	}
}

func TestLoadRedisDisabledByDefaultAndCustomValues(t *testing.T) {
	disabled, err := LoadRedis(mapLookup(map[string]string{RedisURLKey: ""}))
	if err != nil {
		t.Fatalf("LoadRedis() disabled error = %v", err)
	}
	if disabled.Enabled || disabled.URL != "" {
		t.Fatalf("disabled Redis = %#v", disabled)
	}

	configuration, err := LoadRedis(mapLookup(map[string]string{
		RedisURLKey:             "rediss://cache-user:cache-password@redis.example.com:6380",
		RedisDialTimeoutKey:     "2s",
		RedisReadTimeoutKey:     "1500ms",
		RedisWriteTimeoutKey:    "2s",
		RedisPoolTimeoutKey:     "1s",
		RedisHealthTimeoutKey:   "1500ms",
		RedisPoolSizeKey:        "20",
		RedisMinIdleConnsKey:    "4",
		RedisMaxRetriesKey:      "3",
		RedisMinRetryBackoffKey: "20ms",
		RedisMaxRetryBackoffKey: "400ms",
		RedisMaxIdleTimeKey:     "10m",
		RedisMaxLifetimeKey:     "1h",
		RedisDatabaseKey:        "7",
		RedisSlowCommandKey:     "300ms",
	}))
	if err != nil {
		t.Fatalf("LoadRedis() error = %v", err)
	}
	if !configuration.Enabled || configuration.PoolSize != 20 || configuration.MinIdleConnections != 4 || configuration.Database != 7 {
		t.Fatalf("Redis = %#v", configuration)
	}
	if configuration.ReadTimeout != 1500*time.Millisecond || configuration.MaxRetries != 3 || configuration.MaxConnLifetime != time.Hour {
		t.Fatalf("Redis = %#v", configuration)
	}
}

func TestLoadRedisRejectsUnsafeValuesWithoutRawInput(t *testing.T) {
	const secretURL = "redis://cache-user:secret-password@redis.example.com/0?hidden=secret"
	tests := []struct {
		name   string
		key    string
		value  string
		values map[string]string
	}{
		{name: "URL parameters", key: RedisURLKey, value: secretURL, values: map[string]string{RedisURLKey: secretURL}},
		{name: "multiline URL", key: RedisURLKey, value: "redis://secret\ninjected", values: map[string]string{RedisURLKey: "redis://secret\ninjected"}},
		{name: "pool size", key: RedisPoolSizeKey, value: "5000", values: map[string]string{RedisURLKey: "redis://localhost:6379", RedisPoolSizeKey: "5000"}},
		{name: "minimum exceeds pool", key: RedisMinIdleConnsKey, values: map[string]string{RedisURLKey: "redis://localhost:6379", RedisPoolSizeKey: "2", RedisMinIdleConnsKey: "3"}},
		{name: "retry order", key: RedisMinRetryBackoffKey, values: map[string]string{RedisURLKey: "redis://localhost:6379", RedisMinRetryBackoffKey: "1s", RedisMaxRetryBackoffKey: "10ms"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := LoadRedis(mapLookup(test.values))
			if err == nil || !strings.Contains(err.Error(), test.key) {
				t.Fatalf("LoadRedis() error = %v", err)
			}
			if test.value != "" && strings.Contains(err.Error(), test.value) {
				t.Fatalf("error %q contains raw value", err)
			}
		})
	}
}

func TestLoadProcessConfigurations(t *testing.T) {
	migrate, err := LoadMigrate(mapLookup(map[string]string{PostgresMigrationLockKey: "45s"}))
	if err != nil {
		t.Fatalf("LoadMigrate() error = %v", err)
	}
	if migrate.MigrationLockTimeout != 45*time.Second {
		t.Fatalf("MigrationLockTimeout = %s", migrate.MigrationLockTimeout)
	}
	bootstrapAdmin, err := LoadBootstrapAdmin(mapLookup(map[string]string{RedisURLKey: ""}))
	if err != nil {
		t.Fatalf("LoadBootstrapAdmin() error = %v", err)
	}
	if bootstrapAdmin.PostgreSQL.URL != testPostgresURL {
		t.Fatalf("BootstrapAdmin = %#v", bootstrapAdmin)
	}
}

func TestLoadAPIRequiresRedisForSessions(t *testing.T) {
	_, err := LoadAPI(mapLookup(map[string]string{RedisURLKey: ""}))
	if err == nil || !strings.Contains(err.Error(), RedisURLKey) {
		t.Fatalf("LoadAPI() error = %v", err)
	}
}

func TestLoadAPIRejectsInvalidEncryptionKeyWithoutRawValue(t *testing.T) {
	const rawValue = "plain-secret-key-material"
	for _, value := range []string{"", rawValue, "c2hvcnQ=", "bad\nvalue"} {
		_, err := LoadAPI(mapLookup(map[string]string{EncryptionKey: value}))
		if err == nil || !strings.Contains(err.Error(), EncryptionKey) {
			t.Fatalf("LoadAPI() error = %v", err)
		}
		if value != "" && strings.Contains(err.Error(), value) {
			t.Fatalf("error %q contains raw encryption key", err)
		}
	}
}

func TestLoadAPIReportsLogFieldWithoutRawValue(t *testing.T) {
	const rawValue = "not-a-log-option-secret-marker"
	for _, key := range []string{LogLevelKey, LogFormatKey} {
		t.Run(key, func(t *testing.T) {
			_, err := LoadAPI(mapLookup(map[string]string{key: rawValue}))
			if err == nil {
				t.Fatal("LoadAPI() error = nil")
			}
			if !strings.Contains(err.Error(), key) {
				t.Fatalf("error %q does not name field", err)
			}
			if strings.Contains(err.Error(), rawValue) {
				t.Fatalf("error %q contains raw value", err)
			}
		})
	}
}

func TestLoadAPIRejectsInvalidHTTPRequestDebugWithoutRawValue(t *testing.T) {
	const rawValue = "pretty-secret-marker"
	_, err := LoadAPI(mapLookup(map[string]string{HTTPRequestDebugKey: rawValue}))
	if err == nil || !strings.Contains(err.Error(), HTTPRequestDebugKey) {
		t.Fatalf("LoadAPI() error = %v", err)
	}
	if strings.Contains(err.Error(), rawValue) {
		t.Fatalf("error %q contains raw value", err)
	}
}

func TestLoadAPIRejectsInvalidPublicURLWithoutRawValue(t *testing.T) {
	const rawValue = "https://user:pretty-secret-marker@hooks.example.com/hooks/"
	for _, value := range []string{"", rawValue, "not-a-url", "ftp://hooks.example.com", "https://hooks.example.com/", "https://hooks.example.com?token=1"} {
		_, err := LoadAPI(mapLookup(map[string]string{PublicURLKey: value}))
		if err == nil || !strings.Contains(err.Error(), PublicURLKey) {
			t.Fatalf("LoadAPI() error = %v", err)
		}
		if value != "" && strings.Contains(err.Error(), value) {
			t.Fatalf("error %q contains raw public URL", err)
		}
	}
}

func TestLoadAPIRejectsInvalidAddressAndDuration(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
		field  string
	}{
		{name: "address", values: map[string]string{HTTPAddressKey: "localhost"}, field: HTTPAddressKey},
		{name: "duration", values: map[string]string{ShutdownKey: "0s"}, field: ShutdownKey},
		{name: "body size", values: map[string]string{HTTPMaxBodyBytesKey: "100"}, field: HTTPMaxBodyBytesKey},
		{name: "CORS path", values: map[string]string{HTTPCORSAllowedOriginKey: "https://console.example.com/path"}, field: HTTPCORSAllowedOriginKey},
		{name: "CORS credentials", values: map[string]string{HTTPCORSAllowedOriginKey: "https://user:secret@console.example.com"}, field: HTTPCORSAllowedOriginKey},
		{name: "session TTL", values: map[string]string{AuthSessionTTLKey: "1m"}, field: AuthSessionTTLKey},
		{name: "webhook AI timeout", values: map[string]string{WebhookAITimeoutKey: "11m"}, field: WebhookAITimeoutKey},
		{name: "request debug", values: map[string]string{HTTPRequestDebugKey: "pretty-secret-marker"}, field: HTTPRequestDebugKey},
		{name: "public URL", values: map[string]string{PublicURLKey: "https://hooks.example.com/"}, field: PublicURLKey},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := LoadAPI(mapLookup(test.values))
			if err == nil || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("LoadAPI() error = %v", err)
			}
		})
	}
}

func TestConfigurationPrefersMendryAndFallsBackToFixthe(t *testing.T) {
	legacy := mapLookup(map[string]string{"FIXTHE_LOG_LEVEL": "warn", "FIXTHE_HTTP_ADDR": "0.0.0.0:9000"})
	common, err := LoadCommon(legacy)
	if err != nil {
		t.Fatalf("LoadCommon() error = %v", err)
	}
	if common.LogLevel != "warn" {
		t.Fatalf("legacy log level = %q", common.LogLevel)
	}
	if _, err := LoadAPI(mapLookup(map[string]string{
		"FIXTHE_LOG_LEVEL": "warn",
		LogLevelKey:        "",
	})); err == nil || !strings.Contains(err.Error(), LogLevelKey) {
		t.Fatalf("explicit empty MENDRY value should win with validation error, got %v", err)
	}
}

func TestConfigurationLegacyValuesSupportTypedSettings(t *testing.T) {
	lookup := mapLookup(map[string]string{
		"FIXTHE_AUTH_SESSION_TTL":    "12h",
		"FIXTHE_HTTP_MAX_BODY_BYTES": "2048",
		"FIXTHE_POSTGRES_MAX_CONNS":  "20",
		"FIXTHE_REDIS_POOL_SIZE":     "12",
		"FIXTHE_REDIS_URL":           "redis://localhost:6379",
		"FIXTHE_ENCRYPTION_KEY":      testEncryptionKey,
		"FIXTHE_PUBLIC_URL":          testPublicURL,
		"FIXTHE_POSTGRES_URL":        testPostgresURL,
	})
	configuration, err := LoadAPI(lookup)
	if err != nil {
		t.Fatalf("LoadAPI() error = %v", err)
	}
	if configuration.Auth.SessionTTL != 12*time.Hour || configuration.HTTP.MaxBodyBytes != 2048 ||
		configuration.PostgreSQL.MaxConnections != 20 || configuration.Redis.PoolSize != 12 {
		t.Fatalf("legacy configuration = %#v", configuration)
	}
}

func mapLookup(values map[string]string) Lookup {
	return func(key string) (string, bool) {
		value, ok := values[key]
		if key == PostgresURLKey && !ok {
			if _, legacySet := values[legacyEnvironmentKey(key)]; !legacySet {
				return testPostgresURL, true
			}
		}
		if key == RedisURLKey && !ok {
			if _, legacySet := values[legacyEnvironmentKey(key)]; !legacySet {
				return testRedisURL, true
			}
		}
		if key == EncryptionKey && !ok {
			if _, legacySet := values[legacyEnvironmentKey(key)]; !legacySet {
				return testEncryptionKey, true
			}
		}
		if key == PublicURLKey && !ok {
			if _, legacySet := values[legacyEnvironmentKey(key)]; !legacySet {
				return testPublicURL, true
			}
		}
		return value, ok
	}
}

const testPostgresURL = "postgres://test:test@localhost:5432/mendry_test?sslmode=disable"
const testRedisURL = "redis://localhost:6379"
const testEncryptionKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
const testPublicURL = "http://127.0.0.1:8080"
