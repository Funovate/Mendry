package domain

import (
	"testing"
	"time"
)

func TestIncidentIDRoundTrip(t *testing.T) {
	identifier, err := FormatID(2049)
	if err != nil || identifier != "INC-2049" {
		t.Fatalf("FormatID(2049) = %q, %v", identifier, err)
	}
	number, err := ParseID(identifier)
	if err != nil || number != 2049 {
		t.Fatalf("ParseID(%q) = %d, %v", identifier, number, err)
	}
	for _, value := range []string{"", "2049", "inc-2049", "INC-", "INC-0", "INC-02049", "INC--1", "INC-+1"} {
		if _, err := ParseID(value); err == nil {
			t.Errorf("ParseID(%q) error = nil", value)
		}
	}
}

func TestParseStatusAndPriorityAreExhaustive(t *testing.T) {
	for _, value := range []string{"Open", "Recovered", "Closed"} {
		if _, err := ParseStatus(value); err != nil {
			t.Errorf("ParseStatus(%q) error = %v", value, err)
		}
	}
	if _, err := ParseStatus("open"); err == nil {
		t.Fatal("ParseStatus(open) error = nil")
	}
	for _, value := range []string{"Info", "P2", "P1"} {
		if _, err := ParsePriority(value); err != nil {
			t.Errorf("ParsePriority(%q) error = %v", value, err)
		}
	}
	if _, err := ParsePriority("P0"); err == nil {
		t.Fatal("ParsePriority(P0) error = nil")
	}
}

func TestNextLifecycleGenerationIncrementsOnlyOnReopen(t *testing.T) {
	if got := NextLifecycleGeneration(StatusRecovered, StatusOpen, 1); got != 2 {
		t.Fatalf("reopen generation = %d, want 2", got)
	}
	if got := NextLifecycleGeneration(StatusRecovered, StatusOpen, 0); got != 2 {
		t.Fatalf("reopen from unset generation = %d, want 2", got)
	}
	if got := NextLifecycleGeneration(StatusOpen, StatusRecovered, 1); got != 1 {
		t.Fatalf("recover generation = %d, want 1", got)
	}
	if got := NextLifecycleGeneration(StatusOpen, StatusClosed, 3); got != 3 {
		t.Fatalf("close generation = %d, want 3", got)
	}
	if got := NextLifecycleGeneration(StatusOpen, StatusRecovered, 0); got != 1 {
		t.Fatalf("zero current generation = %d, want 1", got)
	}
}

func TestValidateIncidentInvariants(t *testing.T) {
	now := time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC)
	valid := Incident{
		InternalID: "uuid-v7", ProjectID: "project", EnvironmentID: "environment", SourceID: "source-id",
		Title: "Database latency", Fingerprint: "postgres:latency",
		Status: StatusOpen, Priority: PriorityP2, Source: "postgres-monitor",
		FirstSeen: now, LastSeen: now, OccurrenceCount: 2, HostCount: 1,
		NotificationSummary: "Lifecycle default",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Incident)
	}{
		{name: "unknown status", mutate: func(value *Incident) { value.Status = "open" }},
		{name: "unknown priority", mutate: func(value *Incident) { value.Priority = "P0" }},
		{name: "empty title", mutate: func(value *Incident) { value.Title = "  " }},
		{name: "seen order", mutate: func(value *Incident) { value.LastSeen = now.Add(-time.Second) }},
		{name: "host count", mutate: func(value *Incident) { value.HostCount = 3 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("Validate() error = nil")
			}
		})
	}
}
