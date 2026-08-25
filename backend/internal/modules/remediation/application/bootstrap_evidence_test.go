package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"fixthe/backend/internal/modules/remediation/domain"
)

func TestPrepareBootstrapEvidenceUsesEpochAndProviderQueryInterval(t *testing.T) {
	value := prepareBootstrapEvidence(domain.BootstrapEvidence{
		Observation: &domain.TriggeringObservation{SourceID: "source-1"},
		Records: []domain.StoredEvidence{{
			SourceID: "source-1", Provider: "tencent_cls", EvidenceKind: domain.EvidenceKindProviderDetail,
			Outcome: "success", Payload: json.RawMessage(`{
				"alertQuality":"anchor_only",
				"alertTime":"2026-08-24 15:44:32",
				"FireTime":"2026-08-24 07:44:32.007 UTC",
				"ResultsSnapshot":{
					"QueryInterval":{"start":"2026-08-24 07:28:30","end":"2026-08-24 07:43:30"},
					"AnalysisInfo":[{"rows":[{"time":"2026-08-24 07:37:07.956","__TIMESTAMP__":1787557027956}]}]
				}
			}`),
		}},
	})

	wantStart := time.Date(2026, time.August, 24, 7, 28, 30, 0, time.UTC)
	wantEnd := time.Date(2026, time.August, 24, 7, 43, 30, 0, time.UTC)
	if !value.TimeRange.Start.Equal(wantStart) || !value.TimeRange.End.Equal(wantEnd) {
		t.Fatalf("time range = %s..%s, want %s..%s", value.TimeRange.Start, value.TimeRange.End, wantStart, wantEnd)
	}
	if value.TimeBasis != "paired_epoch" || value.TimeCertainty != "high" {
		t.Fatalf("time assessment = %s/%s, want paired_epoch/high", value.TimeBasis, value.TimeCertainty)
	}
	for _, want := range []string{
		"alertTime=2026-08-24 15:44:32",
		"FireTime=2026-08-24 07:44:32.007 UTC",
		"__TIMESTAMP__=1787557027956",
	} {
		if !containsString(value.OriginalTimeValues, want) {
			t.Fatalf("original time values missing %q: %#v", want, value.OriginalTimeValues)
		}
	}
	if len(value.Contradictions) != 0 {
		t.Fatalf("representative display/epoch values became contradictions: %#v", value.Contradictions)
	}
}

func TestPrepareBootstrapEvidenceFallsBackForTimestampOnlyAndUnresolvedClock(t *testing.T) {
	occurredAt := time.Date(2026, time.August, 24, 7, 44, 32, 7_000_000, time.UTC)
	value := prepareBootstrapEvidence(domain.BootstrapEvidence{
		Observation: &domain.TriggeringObservation{SourceID: "source-1", OccurredAt: occurredAt},
	})
	if !value.TimeRange.Start.Equal(occurredAt.Add(-bootstrapWindowPadding)) ||
		!value.TimeRange.End.Equal(occurredAt.Add(bootstrapWindowPadding)) {
		t.Fatalf("fallback time range = %s..%s", value.TimeRange.Start, value.TimeRange.End)
	}
	if value.TimeBasis != "unresolved" || value.TimeCertainty != "low" {
		t.Fatalf("fallback time assessment = %s/%s", value.TimeBasis, value.TimeCertainty)
	}
	if !containsString(value.MissingEvidence, "reconciled time-zone semantics") {
		t.Fatalf("fallback missing evidence = %#v", value.MissingEvidence)
	}

	unresolved := prepareBootstrapEvidence(domain.BootstrapEvidence{
		Records: []domain.StoredEvidence{{
			Provider: "generic", EvidenceKind: domain.EvidenceKindNormalizedAlert,
			SourceID: "source-1", Outcome: "success",
			Payload: json.RawMessage(`{"eventTime":"2026-08-24 15:44:32"}`),
		}},
	})
	if !unresolved.TimeRange.Start.IsZero() || unresolved.TimeBasis != "unresolved" || unresolved.TimeCertainty != "unresolved" {
		t.Fatalf("zone-less time = %#v", unresolved)
	}
}

func TestPrepareBootstrapEvidencePrefersExplicitOffsetAndRecordsConflict(t *testing.T) {
	value := prepareBootstrapEvidence(domain.BootstrapEvidence{
		Records: []domain.StoredEvidence{{
			Provider: "generic", EvidenceKind: domain.EvidenceKindNormalizedAlert, SourceID: "source-1", Outcome: "success",
			Payload: json.RawMessage(`{"eventTime":"2026-08-24T15:44:32+08:00"}`),
		}},
	})
	want := time.Date(2026, time.August, 24, 7, 44, 32, 0, time.UTC)
	if !value.TimeRange.Start.Equal(want.Add(-bootstrapWindowPadding)) || value.TimeBasis != "explicit_offset" || value.TimeCertainty != "high" {
		t.Fatalf("explicit offset assessment = %#v, want center %s", value, want)
	}

	conflict := prepareBootstrapEvidence(domain.BootstrapEvidence{
		Records: []domain.StoredEvidence{{
			Provider: "generic", EvidenceKind: domain.EvidenceKindNormalizedAlert, SourceID: "source-1", Outcome: "success",
			Payload: json.RawMessage(`{"eventTime":"2026-08-24T07:44:32Z","eventTimestamp":1787557027956}`),
		}},
	})
	if len(conflict.Contradictions) != 1 || !strings.Contains(conflict.Contradictions[0], "contradictory time evidence") {
		t.Fatalf("time conflict = %#v", conflict.Contradictions)
	}
}

func TestInitialContextIncludesTriggeringEvidenceButOmitsDetailURL(t *testing.T) {
	service := "checkout"
	assembler := NewContextAssembler(nil, nil)
	text, effect, err := assembler.AssembleInitialContextWithEvidenceObserved(
		context.Background(), RunIdentity{}, nil,
		domain.RepoRef{ProjectID: "project-1", Commit: "abc123"},
		domain.EvidenceScope{ProjectID: "project-1", EnvironmentID: "env-1", SourceID: "source-1"},
		domain.SourceCapabilitySnapshot{Kind: "mcp"},
		domain.BootstrapEvidence{
			Observation: &domain.TriggeringObservation{
				ID: "observation-1", ProjectID: "project-1", EnvironmentID: "env-1", SourceID: "source-1",
				Service: &service, Fingerprint: "fp-1", Level: "error", Attributes: json.RawMessage(`{}`),
			},
			Records: []domain.StoredEvidence{
				{EvidenceID: "evidence-alert", Provider: "generic", EvidenceKind: domain.EvidenceKindNormalizedAlert, SourceID: "source-1", Outcome: "success", Payload: json.RawMessage(`{"alertQuality":"sparse","originalSummary":"nil pointer","DetailUrl":"https://alarm.cls.tencentcs.com/secret"}`)},
				{EvidenceID: "evidence-runtime", Provider: "tencent_cls", EvidenceKind: domain.EvidenceKindProviderDetail, SourceID: "source-1", Outcome: "success", Payload: json.RawMessage(`{"matchedLog":"panic: nil pointer","DetailUrl":"https://alarm.cls.tencentcs.com/secret"}`)},
			},
		},
	)
	if err != nil {
		t.Fatalf("assemble context: %v", err)
	}
	if effect.EvidenceBytes <= 0 || !strings.Contains(text, "observation_ref=observation-1") ||
		!strings.Contains(text, "originalSummary") || !strings.Contains(text, "nil pointer") ||
		!strings.Contains(text, "evidence_ref=evidence-runtime") {
		t.Fatalf("context omitted trigger evidence: effect=%#v text=%s", effect, text)
	}
	if strings.Contains(text, "DetailUrl") || strings.Contains(text, "alarm.cls.tencentcs.com/secret") {
		t.Fatalf("context leaked detail URL: %s", text)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
}
