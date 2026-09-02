package integration_test

import (
	"fmt"
	"strings"
	"testing"

	"fixthe/backend/internal/platform/config"
)

const (
	testRedisURLKey    = "FIXTHE_TEST_REDIS_URL"
	testRedisPrefixKey = "FIXTHE_TEST_REDIS_PREFIX"
)

func TestIsolatedRedisTargetFailsClosed(t *testing.T) {
	const secret = "do-not-print-this-password"
	tests := []struct {
		name   string
		values map[string]string
	}{
		{name: "missing values"},
		{name: "invalid URL", values: map[string]string{
			testRedisURLKey:    "redis://user:" + secret + "@%invalid:6379",
			testRedisPrefixKey: "fixthe:test:",
		}},
		{name: "non-test prefix", values: map[string]string{
			testRedisURLKey:    "redis://localhost:6379",
			testRedisPrefixKey: "fixthe:production:",
		}},
		{name: "test substring is not marker", values: map[string]string{
			testRedisURLKey:    "redis://localhost:6379",
			testRedisPrefixKey: "contest:",
		}},
		{name: "prefix lacks delimiter", values: map[string]string{
			testRedisURLKey:    "redis://localhost:6379",
			testRedisPrefixKey: "fixthe:test",
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := isolatedRedisTarget(mapLookup(test.values))
			if err == nil {
				t.Fatal("isolatedRedisTarget() error = nil")
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error exposes Redis credential: %q", err)
			}
		})
	}
}

func TestIsolatedRedisTargetAcceptsExplicitTestPrefix(t *testing.T) {
	const connectionURL = "redis://localhost:6379"
	url, prefix, err := isolatedRedisTarget(mapLookup(map[string]string{
		testRedisURLKey:    connectionURL,
		testRedisPrefixKey: "fixthe:test:",
	}))
	if err != nil {
		t.Fatalf("isolatedRedisTarget() error = %v", err)
	}
	if url != connectionURL || prefix != "fixthe:test:" {
		t.Fatalf("target = %q, %q", url, prefix)
	}
}

func isolatedRedisTarget(lookup func(string) (string, bool)) (string, string, error) {
	connectionURL, ok := lookup(testRedisURLKey)
	if !ok || strings.TrimSpace(connectionURL) == "" || strings.ContainsAny(connectionURL, "\x00\r\n") {
		return "", "", fmt.Errorf("%s is required and must be a single-line Redis URL", testRedisURLKey)
	}
	configuration, err := config.LoadRedis(mapLookup(map[string]string{config.RedisURLKey: connectionURL}))
	if err != nil || !configuration.Enabled {
		return "", "", fmt.Errorf("%s must contain a valid redis or rediss URL", testRedisURLKey)
	}
	prefix, ok := lookup(testRedisPrefixKey)
	if !ok || len(prefix) > 128 || !strings.HasSuffix(prefix, ":") || !hasTestMarker(prefix) {
		return "", "", fmt.Errorf("%s must be a bounded namespace ending in colon with a distinct test marker", testRedisPrefixKey)
	}
	return connectionURL, prefix, nil
}
