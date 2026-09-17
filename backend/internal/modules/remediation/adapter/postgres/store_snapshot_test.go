package postgres

import (
	"reflect"
	"testing"

	"mendry/backend/internal/modules/remediation/domain"
)

func TestDecodeRunSnapshot(t *testing.T) {
	raw := []byte(`{"imageDigest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","workingDirectory":".","preparation":[],"requiredCommands":[{"id":"unit","version":2,"argv":["go","test","./..."],"timeoutSeconds":600}],"cpuLimit":2,"memoryLimitMiB":4096,"workspaceLimitMiB":10240}`)
	got := decodeRunSnapshot[domain.ExecutionProfileSnapshot](raw)
	want := domain.ExecutionProfileSnapshot{
		ImageDigest:       "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		WorkingDirectory:  ".",
		Preparation:       []domain.ValidationCommandSnapshot{},
		RequiredCommands:  []domain.ValidationCommandSnapshot{{ID: "unit", Version: 2, Argv: []string{"go", "test", "./..."}, TimeoutSeconds: 600}},
		CPULimit:          2,
		MemoryLimitMiB:    4096,
		WorkspaceLimitMiB: 10240,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decoded execution profile = %#v, want %#v", got, want)
	}
	if invalid := decodeRunSnapshot[domain.ExecutionProfileSnapshot]([]byte(`{"requiredCommands":"invalid"}`)); !reflect.DeepEqual(invalid, domain.ExecutionProfileSnapshot{}) {
		t.Fatalf("invalid snapshot must fail closed, got %#v", invalid)
	}
}
