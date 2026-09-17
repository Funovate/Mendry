package config

import (
	"testing"
	"time"
)

func TestRemediationRecoveryConfiguration(t *testing.T) {
	defaults, err := LoadAPI(mapLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	if defaults.Remediation.RecoveryInterval != 15*time.Second || defaults.Remediation.Concurrency != 4 {
		t.Fatalf("defaults=%+v", defaults.Remediation)
	}
	custom, err := LoadAPI(mapLookup(map[string]string{RemediationRecoveryIntervalKey: "30s", RemediationConcurrencyKey: "8"}))
	if err != nil {
		t.Fatal(err)
	}
	if custom.Remediation.RecoveryInterval != 30*time.Second || custom.Remediation.Concurrency != 8 {
		t.Fatalf("custom=%+v", custom.Remediation)
	}
	for _, values := range []map[string]string{
		{RemediationRecoveryIntervalKey: "0s"}, {RemediationRecoveryIntervalKey: "6m"}, {RemediationConcurrencyKey: "0"}, {RemediationConcurrencyKey: "33"}, {RemediationConcurrencyKey: "bad"},
	} {
		if _, err := LoadAPI(mapLookup(values)); err == nil {
			t.Fatalf("accepted %v", values)
		}
	}
}
