package application

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewServiceRejectsInvalidDependencies(t *testing.T) {
	tests := []struct {
		name         string
		timeout      time.Duration
		dependencies []Dependency
	}{
		{name: "timeout", timeout: 0},
		{name: "empty-name", timeout: time.Second, dependencies: []Dependency{{Check: func(context.Context) error { return nil }}}},
		{name: "nil-check", timeout: time.Second, dependencies: []Dependency{{Name: "postgresql"}}},
		{name: "duplicate", timeout: time.Second, dependencies: []Dependency{
			{Name: "postgresql", Check: func(context.Context) error { return nil }},
			{Name: "postgresql", Check: func(context.Context) error { return nil }},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewService(test.timeout, test.dependencies...); err == nil {
				t.Fatal("NewService() error = nil")
			}
		})
	}
}

func TestReadinessChecksAllDependencies(t *testing.T) {
	var checks atomic.Int32
	service, err := NewService(time.Second,
		Dependency{Name: "postgresql", Check: func(context.Context) error {
			checks.Add(1)
			return nil
		}},
		Dependency{Name: "redis", Check: func(context.Context) error {
			checks.Add(1)
			return errors.New("unavailable")
		}},
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	report := service.Readiness(context.Background())
	if checks.Load() != 2 || report.Status != "not_ready" || report.Dependencies["redis"] != "unavailable" {
		t.Fatalf("checks = %d, report = %#v", checks.Load(), report)
	}
	if _, exists := report.Dependencies["postgresql"]; exists {
		t.Fatalf("report = %#v", report)
	}
}

func TestReadinessReturnsWithinOverallTimeout(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	service, err := NewService(10*time.Millisecond, Dependency{
		Name: "postgresql",
		Check: func(context.Context) error {
			<-release
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	started := time.Now()
	report := service.Readiness(context.Background())
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("Readiness() elapsed = %s", elapsed)
	}
	if report.Status != "not_ready" || report.Dependencies["postgresql"] != "unavailable" {
		t.Fatalf("report = %#v", report)
	}
}
