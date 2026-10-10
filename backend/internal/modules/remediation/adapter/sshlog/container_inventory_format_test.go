package sshlog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"text/template"
)

// Whole-object serialization must not be used: Docker's formatter can evaluate
// additional fields when marshaled, even though the inventory does not use them.
type inventoryFormatFixture struct {
	ID, Names, Image, State, Status string
}

func (inventoryFormatFixture) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("unexpected whole-object serialization")
}

func TestDockerInventoryFormatRoundTrip(t *testing.T) {
	format, err := template.New("inventory").Funcs(template.FuncMap{
		"json": func(value any) (string, error) {
			encoded, err := json.Marshal(value)
			return string(encoded), err
		},
	}).Parse(dockerInventoryFormat)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"running", "restarting", "exited"} {
		t.Run(state, func(t *testing.T) {
			fixture := inventoryFormatFixture{
				ID: strings.Repeat("a", 64), Names: "checkout-api",
				Image: "registry.example/app:v1", State: state,
				Status: "status with \"quotes\", \\slashes\tand\nnewlines",
			}
			var output bytes.Buffer
			if err := format.Execute(&output, fixture); err != nil {
				t.Fatal(err)
			}
			containers, err := parseDockerInventory(output.String())
			if err != nil {
				t.Fatal(err)
			}
			if len(containers) != 1 {
				t.Fatalf("containers = %#v", containers)
			}
			got := containers[0]
			if got.ID != fixture.ID || got.Name != fixture.Names || got.Image != fixture.Image || got.State != fixture.State || got.Status != fixture.Status {
				t.Fatalf("inventory fields did not round-trip: %#v", got)
			}
		})
	}
}
