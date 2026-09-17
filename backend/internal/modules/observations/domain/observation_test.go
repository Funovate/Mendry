package domain

import (
	"encoding/json"
	"testing"
	"time"
)

func TestObservationAttributesAreBoundedAllowlistedScalars(t *testing.T) {
	observation := Observation{ID: "id", ProjectID: "project", EnvironmentID: "environment", SourceID: "source",
		OccurredAt: time.Now(), Level: "error", Message: "database timeout", Fingerprint: "db:timeout",
		Attributes: json.RawMessage(`{"component":"api","traceId":"abc","region":"ap-shanghai","zone":null}`)}
	if err := observation.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	for _, attributes := range []string{`{"authorization":"secret"}`, `{"component":{"nested":true}}`, `{} {}`, `[]`} {
		candidate := observation
		candidate.Attributes = json.RawMessage(attributes)
		if err := candidate.Validate(); err == nil {
			t.Errorf("attributes %q accepted", attributes)
		}
	}
}
