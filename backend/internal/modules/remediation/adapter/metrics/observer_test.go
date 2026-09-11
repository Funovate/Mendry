package metrics_test

import (
	"context"
	"strings"
	"testing"

	"mendry/backend/internal/modules/remediation/adapter/metrics"
	"mendry/backend/internal/modules/remediation/application"
	"mendry/backend/internal/modules/remediation/domain"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func newTestObserver(t *testing.T) (*metrics.Observer, *metric.ManualReader) {
	t.Helper()
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	observer, err := metrics.NewObserver(metrics.Options{Meter: provider.Meter("test")})
	if err != nil {
		t.Fatalf("NewObserver() error = %v", err)
	}
	return observer, reader
}

func collectCounters(t *testing.T, reader *metric.ManualReader) map[string]int64 {
	t.Helper()
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	counts := make(map[string]int64)
	for _, scope := range collected.ScopeMetrics {
		for _, item := range scope.Metrics {
			sum, ok := item.Data.(metricdata.Sum[int64])
			if !ok {
				continue
			}
			for _, point := range sum.DataPoints {
				counts[item.Name] += point.Value
			}
		}
	}
	return counts
}

// collectAttributeValues 返回指定 counter 上某个 string 属性的取值计数，用于
// 断言 metric 属性是严格允许表 + fallback，而非任意文本截断。
func collectAttributeValues(t *testing.T, reader *metric.ManualReader, counter, attrKey string) map[string]int64 {
	t.Helper()
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	values := make(map[string]int64)
	for _, scope := range collected.ScopeMetrics {
		for _, item := range scope.Metrics {
			if item.Name != counter {
				continue
			}
			sum, ok := item.Data.(metricdata.Sum[int64])
			if !ok {
				continue
			}
			for _, point := range sum.DataPoints {
				for _, attr := range point.Attributes.ToSlice() {
					if attr.Key == attribute.Key(attrKey) {
						values[attr.Value.AsString()] += point.Value
					}
				}
			}
		}
	}
	return values
}

func sampleMetric(kind string) application.ResilienceMetric {
	return application.ResilienceMetric{
		Run:   application.RunIdentity{RunID: "run-1", SeriesID: "series-1", IncidentID: "incident-1", LifecycleGeneration: 1},
		Mode:  domain.AgentLoopModeResilientV1,
		Kind:  kind,
		Phase: domain.RunStateDiagnosing,
	}
}

// TestObserverMapsResilienceEventsToLowCardinalityCounters 验证每种事件映射到
// 独立 counter，且 no-progress checkpoint 额外计入 no_progress counter；
// RunIdentity 等身份不进属性。
func TestObserverMapsResilienceEventsToLowCardinalityCounters(t *testing.T) {
	observer, reader := newTestObserver(t)
	ctx := context.Background()
	observer.RecordResilienceMetric(ctx, sampleMetric(application.ResilienceMetricRunStarted))
	transition := sampleMetric(application.ResilienceMetricStateTransitioned)
	transition.From, transition.To = domain.RunStateDiagnosing, domain.RunStatePlanning
	observer.RecordResilienceMetric(ctx, transition)
	observer.RecordResilienceMetric(ctx, sampleMetric(application.ResilienceMetricRunStarted))
	checkpoint := sampleMetric(application.ResilienceMetricCheckpoint)
	checkpoint.Reason = domain.CheckpointReasonRecovery
	checkpoint.NoProgress = true
	observer.RecordResilienceMetric(ctx, checkpoint)
	observer.RecordResilienceMetric(ctx, sampleMetric(application.ResilienceMetricRunTerminal))
	budget := sampleMetric(application.ResilienceMetricBudgetSignal)
	budget.Reason = "soft_budget_crossed"
	observer.RecordResilienceMetric(ctx, budget)
	exhaustion := sampleMetric(application.ResilienceMetricExhaustion)
	exhaustion.Reason = "accepted"
	observer.RecordResilienceMetric(ctx, exhaustion)
	exhaustion.Reason = "rejected"
	observer.RecordResilienceMetric(ctx, exhaustion)
	observer.RecordResilienceMetric(ctx, sampleMetric(application.ResilienceMetricReconstruction))
	challenge := sampleMetric(application.ResilienceMetricChallenge)
	challenge.ChallengeKind = domain.RecoveryChallengeKindEvidenceCorrection
	observer.RecordResilienceMetric(ctx, challenge)
	success := sampleMetric(application.ResilienceMetricRecoverySuccess)
	success.Reason = domain.CheckpointReasonPhaseBoundary
	observer.RecordResilienceMetric(ctx, success)

	counts := collectCounters(t, reader)
	want := map[string]int64{
		"mendry.remediation.run.started":                2,
		"mendry.remediation.state.transitioned":         1,
		"mendry.remediation.run.terminal":               1,
		"mendry.remediation.checkpoint.persisted":       1,
		"mendry.remediation.recovery.no_progress":       1,
		"mendry.remediation.budget.signal":              1,
		"mendry.remediation.exhaustion.decided":         2,
		"mendry.remediation.continuation.reconstructed": 1,
		"mendry.remediation.recovery.challenge":         1,
		"mendry.remediation.recovery.success":           1,
	}
	for name, wantCount := range want {
		if counts[name] != wantCount {
			t.Fatalf("counter %s = %d, want %d (all = %#v)", name, counts[name], wantCount, counts)
		}
	}
	if len(counts) != len(want) {
		t.Fatalf("unexpected counters: %#v", counts)
	}
}

// TestObserverChallengeKindAttributeIsLowCardinality 验证 challenge counter 的
// kind 属性只允许 D5 枚举值：已知 kind 原样保留，未知 kind 折叠为 unknown。
func TestObserverChallengeKindAttributeIsLowCardinality(t *testing.T) {
	observer, reader := newTestObserver(t)
	ctx := context.Background()
	record := func(kind domain.RecoveryChallengeKind) {
		metric := sampleMetric(application.ResilienceMetricChallenge)
		metric.ChallengeKind = kind
		observer.RecordResilienceMetric(ctx, metric)
	}
	record(domain.RecoveryChallengeKindEvidenceCorrection)
	record(domain.RecoveryChallengeKindToolFailure)
	record(domain.RecoveryChallengeKindBudget)
	record(domain.RecoveryChallengeKindExhaustion)
	record("model_invented_kind")
	record("")

	values := collectAttributeValues(t, reader, "mendry.remediation.recovery.challenge", "kind")
	want := map[string]int64{
		"evidence_correction": 1,
		"tool_failure":        1,
		"budget":              1,
		"exhaustion":          1,
		"unknown":             2,
	}
	if len(values) != len(want) {
		t.Fatalf("kind attribute values = %#v, want %#v", values, want)
	}
	for value, count := range want {
		if values[value] != count {
			t.Fatalf("kind attribute %q = %d, want %d (%#v)", value, values[value], count, values)
		}
	}
}

// TestObserverStrictReasonAllowlistAndFallback 验证 reason 属性用严格允许表 +
// fallback，而不是任意文本截断：超长未知 reason 折叠为 unknown，不会产生
// 新的高基数属性值；canonical terminal reason 原样保留。
func TestObserverStrictReasonAllowlistAndFallback(t *testing.T) {
	observer, reader := newTestObserver(t)
	ctx := context.Background()

	terminal := sampleMetric(application.ResilienceMetricRunTerminal)
	terminal.TerminalReason = "exhaustion_proof"
	observer.RecordResilienceMetric(ctx, terminal)
	terminal.TerminalReason = strings.Repeat("x", 200) + "-arbitrary-reason"
	observer.RecordResilienceMetric(ctx, terminal)
	terminal.TerminalReason = ""
	observer.RecordResilienceMetric(ctx, terminal)

	checkpoint := sampleMetric(application.ResilienceMetricCheckpoint)
	checkpoint.Reason = domain.CheckpointReasonRecovery
	observer.RecordResilienceMetric(ctx, checkpoint)
	checkpoint.Reason = "a-future-freeform-reason"
	observer.RecordResilienceMetric(ctx, checkpoint)

	success := sampleMetric(application.ResilienceMetricRecoverySuccess)
	success.Reason = domain.CheckpointReasonPhaseBoundary
	observer.RecordResilienceMetric(ctx, success)
	success.Reason = strings.Repeat("y", 128)
	observer.RecordResilienceMetric(ctx, success)

	terminalValues := collectAttributeValues(t, reader, "mendry.remediation.run.terminal", "terminal_reason")
	wantTerminal := map[string]int64{"exhaustion_proof": 1, "unknown": 2}
	if len(terminalValues) != len(wantTerminal) {
		t.Fatalf("terminal_reason values = %#v, want %#v", terminalValues, wantTerminal)
	}
	for value, count := range wantTerminal {
		if terminalValues[value] != count {
			t.Fatalf("terminal_reason %q = %d, want %d (%#v)", value, terminalValues[value], count, terminalValues)
		}
	}
	checkpointValues := collectAttributeValues(t, reader, "mendry.remediation.checkpoint.persisted", "reason")
	wantCheckpoint := map[string]int64{"recovery": 1, "unknown": 1}
	if len(checkpointValues) != len(wantCheckpoint) {
		t.Fatalf("checkpoint reason values = %#v, want %#v", checkpointValues, wantCheckpoint)
	}
	for value, count := range wantCheckpoint {
		if checkpointValues[value] != count {
			t.Fatalf("checkpoint reason %q = %d, want %d (%#v)", value, checkpointValues[value], count, checkpointValues)
		}
	}
	successValues := collectAttributeValues(t, reader, "mendry.remediation.recovery.success", "reason")
	wantSuccess := map[string]int64{domain.CheckpointReasonPhaseBoundary: 1, "unknown": 1}
	if len(successValues) != len(wantSuccess) {
		t.Fatalf("recovery success reason values = %#v, want %#v", successValues, wantSuccess)
	}
	for value, count := range wantSuccess {
		if successValues[value] != count {
			t.Fatalf("recovery success reason %q = %d, want %d (%#v)", value, successValues[value], count, successValues)
		}
	}
}

// TestObserverRequiresMeter 验证构造器 fail closed。
func TestObserverRequiresMeter(t *testing.T) {
	if _, err := metrics.NewObserver(metrics.Options{}); err == nil {
		t.Fatal("NewObserver without meter must fail")
	}
}

// TestObserverUnknownKindIsNoOp 验证未知 kind 不产生任何计数，未来事件不会
// 在旧进程污染未命名 counter。
func TestObserverUnknownKindIsNoOp(t *testing.T) {
	observer, reader := newTestObserver(t)
	observer.RecordResilienceMetric(context.Background(), sampleMetric("future_kind"))
	if counts := collectCounters(t, reader); len(counts) != 0 {
		t.Fatalf("unknown kind produced counters: %#v", counts)
	}
}
