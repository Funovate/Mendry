package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseToolPolicyValidatesReadGrantsAndHash(t *testing.T) {
	policy, err := ParseToolPolicy("project-1", "source-1", 3, "", json.RawMessage(`[
        {"toolName":"query_errors","phases":["diagnosing","collecting_more_context"],"effect":"read"}
    ]`))
	if err != nil {
		t.Fatalf("ParseToolPolicy() error = %v", err)
	}
	if policy.Version != 3 || policy.Hash == "" || len(policy.Entries) != 1 {
		t.Fatalf("unexpected policy: %#v", policy)
	}
	if policy.Entries[0].ToolName != "query_errors" || policy.Entries[0].EffectClass != "read" {
		t.Fatalf("unexpected policy entry: %#v", policy.Entries[0])
	}
}

func TestParseToolPolicyRejectsAuthorityAndShapeDrift(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		hash string
	}{
		{name: "mutation effect", raw: `[{"toolName":"write","phases":["diagnosing"],"effect":"write"}]`},
		{name: "invalid phase", raw: `[{"toolName":"query","phases":["patching"],"effect":"read"}]`},
		{name: "duplicate phase", raw: `[{"toolName":"query","phases":["diagnosing","diagnosing"],"effect":"read"}]`},
		{name: "unknown field", raw: `[{"toolName":"query","phases":["diagnosing"],"effect":"read","readOnly":true}]`},
		{name: "hash mismatch", raw: `[{"toolName":"query","phases":["diagnosing"],"effect":"read"}]`, hash: strings.Repeat("0", 64)},
		{name: "trailing data", raw: `[{"toolName":"query","phases":["diagnosing"],"effect":"read"}] {}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseToolPolicy("project-1", "source-1", 1, test.hash, json.RawMessage(test.raw)); err == nil {
				t.Fatal("ParseToolPolicy() error = nil")
			}
		})
	}
}
