package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"fixthe/backend/internal/modules/hooks/application"
	incidentdomain "fixthe/backend/internal/modules/incidents/domain"
	observationdomain "fixthe/backend/internal/modules/observations/domain"
	projectapplication "fixthe/backend/internal/modules/projects/application"
	remediationdomain "fixthe/backend/internal/modules/remediation/domain"
)

type fakeTokens struct {
	token   string
	ingress projectapplication.WebhookIngress
	err     error
}

func (f *fakeTokens) LookupWebhookToken(_ context.Context, token string) (projectapplication.WebhookIngress, error) {
	f.token = token
	return f.ingress, f.err
}

type fakeObservations struct {
	projectID, sourceID, message, fingerprint string
	err                                       error
	called                                    chan struct{}
}

func (f *fakeObservations) CreateInbound(_ context.Context, projectID, sourceID, message, fingerprint string, _ time.Time) (observationdomain.Observation, error) {
	f.projectID, f.sourceID, f.message, f.fingerprint = projectID, sourceID, message, fingerprint
	if f.called != nil {
		f.called <- struct{}{}
	}
	return observationdomain.Observation{ID: "obs"}, f.err
}

type fakeIncidents struct {
	projectID, sourceID, title, fingerprint string
	incident                                incidentdomain.Incident
	created                                 bool
	err                                     error
	called                                  chan struct{}
}

func (f *fakeIncidents) IngestInbound(_ context.Context, projectID, sourceID, title, fingerprint string, _ time.Time) (incidentdomain.Incident, bool, error) {
	f.projectID, f.sourceID, f.title, f.fingerprint = projectID, sourceID, title, fingerprint
	if f.called != nil {
		f.called <- struct{}{}
	}
	return f.incident, f.created, f.err
}

func (f *fakeIncidents) IngestInboundWithEvidence(ctx context.Context, projectID, sourceID, title, fingerprint string, occurredAt time.Time, evidenceWriter func(context.Context, incidentdomain.Incident) error) (incidentdomain.Incident, bool, error) {
	incident, created, err := f.IngestInbound(ctx, projectID, sourceID, title, fingerprint, occurredAt)
	if err != nil || evidenceWriter == nil {
		return incident, created, err
	}
	if err := evidenceWriter(ctx, incident); err != nil {
		return incident, created, err
	}
	return incident, created, nil
}

type legacyIncidentFake struct {
	incident incidentdomain.Incident
	called   chan struct{}
}

func (f *legacyIncidentFake) IngestInbound(_ context.Context, projectID, sourceID, title, fingerprint string, _ time.Time) (incidentdomain.Incident, bool, error) {
	f.incident.ProjectID, f.incident.SourceID, f.incident.Title, f.incident.Fingerprint = projectID, sourceID, title, fingerprint
	if f.called != nil {
		f.called <- struct{}{}
	}
	return f.incident, true, nil
}

type fakeEvidence struct {
	records []remediationdomain.StoredEvidence
	called  chan struct{}
}

func (f *fakeEvidence) AppendEvidence(_ context.Context, evidence remediationdomain.StoredEvidence) (remediationdomain.StoredEvidence, error) {
	f.records = append(f.records, evidence)
	if f.called != nil {
		f.called <- struct{}{}
	}
	return evidence, nil
}

type blockingAnalyzer struct {
	started    chan struct{}
	release    chan struct{}
	normalized application.NormalizedInbound
}

func (a *blockingAnalyzer) Normalize(context.Context, string, string, string) (application.NormalizedInbound, error) {
	close(a.started)
	<-a.release
	return a.normalized, nil
}

type failingAnalyzer struct{ err error }

func (a failingAnalyzer) Normalize(context.Context, string, string, string) (application.NormalizedInbound, error) {
	return application.NormalizedInbound{}, a.err
}

type fixedAnalyzer struct {
	normalized application.NormalizedInbound
}

func (a fixedAnalyzer) Normalize(context.Context, string, string, string) (application.NormalizedInbound, error) {
	return a.normalized, nil
}

type fakeFailures struct {
	failure application.BackgroundFailure
	err     error
	called  chan struct{}
}

func (f *fakeFailures) Report(_ context.Context, failure application.BackgroundFailure, err error) {
	f.failure, f.err = failure, err
	f.called <- struct{}{}
}

type fakeModel struct {
	request application.ModelRequest
	result  application.ModelResponse
	err     error
}

type blockingModel struct{}

func (blockingModel) Complete(ctx context.Context, _ application.ModelRequest) (application.ModelResponse, error) {
	<-ctx.Done()
	return application.ModelResponse{}, ctx.Err()
}

func (f *fakeModel) Complete(_ context.Context, request application.ModelRequest) (application.ModelResponse, error) {
	f.request = request
	return f.result, f.err
}

func TestNormalizeInboundUsesFirstNonEmptyLineForPlainText(t *testing.T) {
	title, fingerprint, message, err := application.NormalizeInbound("\n  【告警】测试信息  \n告警等级：\n")
	if err != nil || title != "【告警】测试信息" || fingerprint != "【告警】测试信息" || !strings.Contains(message, "告警等级：") {
		t.Fatalf("NormalizeInbound() = %q %q %q %v", title, fingerprint, message, err)
	}
	if _, _, _, err := application.NormalizeInbound("   \n\t"); !errors.Is(err, application.ErrInvalidInput) {
		t.Fatalf("empty body error = %v", err)
	}
	if _, _, _, err := application.NormalizeInbound(string([]byte{0xff, 0xfe})); !errors.Is(err, application.ErrInvalidInput) {
		t.Fatalf("invalid UTF-8 error = %v", err)
	}
	title, fingerprint, _, err = application.NormalizeInbound(strings.Repeat("告", 300) + "\n触发时间：2026-08-10")
	if err != nil || len([]rune(title)) != 240 || len(fingerprint) > 255 {
		t.Fatalf("truncated inbound = titleRunes=%d fingerprintBytes=%d err=%v", len([]rune(title)), len(fingerprint), err)
	}
}

func TestJSONFallbackIgnoresVolatileFields(t *testing.T) {
	analyzer := application.NewFingerprintAnalyzer(nil)
	first, err := analyzer.Normalize(context.Background(), "project", "source", `{
  "UIN": "100013370924",
  "Alarm": "测试信息",
  "Topic": "payment",
  "occurredAt": "2026-08-20T07:22:52Z",
  "DetailUrl": "https://example.test/100"
}`)
	if err != nil {
		t.Fatalf("first normalize error = %v", err)
	}
	second, err := analyzer.Normalize(context.Background(), "project", "source", `{"Topic":"payment","DetailUrl":"https://example.test/200","Alarm":"测试信息","UIN":"different","occurredAt":"later"}`)
	if err != nil {
		t.Fatalf("second normalize error = %v", err)
	}
	if first.Title != "测试信息" || first.Fingerprint == "{" || !strings.HasPrefix(first.Fingerprint, "fallback:v1:") {
		t.Fatalf("normalized JSON = %#v", first)
	}
	if first.Fingerprint != second.Fingerprint {
		t.Fatalf("volatile fields changed fingerprint: %q != %q", first.Fingerprint, second.Fingerprint)
	}
	if first.Message == "" || !strings.Contains(first.Message, `"UIN"`) {
		t.Fatalf("raw message was not retained: %q", first.Message)
	}
}

func TestModelAnalyzerHashesValidatedGroupingFields(t *testing.T) {
	model := &fakeModel{result: application.ModelResponse{Content: `{"title":"支付告警","grouping_fields":{"alarm":"测试信息","category":"availability"}}`}}
	analyzer := application.NewFingerprintAnalyzer(model)
	normalized, err := analyzer.Normalize(context.Background(), "project", "source", `{"UIN":"100","Alarm":"测试信息","Token":"secret"}`)
	if err != nil {
		t.Fatalf("normalize error = %v", err)
	}
	if !strings.HasPrefix(normalized.Fingerprint, "ai:v1:") || normalized.Title != "支付告警" {
		t.Fatalf("normalized = %#v", normalized)
	}
	if strings.Contains(model.request.UserMessage, "secret") || strings.Contains(model.request.UserMessage, "100") {
		t.Fatalf("model request leaked volatile/secret input: %q", model.request.UserMessage)
	}
	if model.request.MaxTokens != 8192 || model.request.Temperature != 0 {
		t.Fatalf("model bounds = %#v", model.request)
	}
}

func TestModelAnalyzerScopesFingerprintByProjectAndSource(t *testing.T) {
	model := &fakeModel{result: application.ModelResponse{Content: `{"title":"same","grouping_fields":{"alarm":"same"}}`}}
	analyzer := application.NewFingerprintAnalyzer(model)
	first, err := analyzer.Normalize(context.Background(), "project-a", "source", `{"Alarm":"one"}`)
	if err != nil {
		t.Fatalf("first normalize error = %v", err)
	}
	second, err := analyzer.Normalize(context.Background(), "project-b", "source", `{"Alarm":"one"}`)
	if err != nil {
		t.Fatalf("second normalize error = %v", err)
	}
	third, err := analyzer.Normalize(context.Background(), "project-a", "source-b", `{"Alarm":"one"}`)
	if err != nil {
		t.Fatalf("third normalize error = %v", err)
	}
	if first.Fingerprint == second.Fingerprint || first.Fingerprint == third.Fingerprint {
		t.Fatalf("cross-scope fingerprints collided: %q %q %q", first.Fingerprint, second.Fingerprint, third.Fingerprint)
	}
}

func TestModelAnalyzerFallsBackOnInvalidResponse(t *testing.T) {
	for _, response := range []string{`not-json`, `{"title":"too much"}`, `{"title":"ok","grouping_fields":{"alarm":""}}`} {
		model := &fakeModel{result: application.ModelResponse{Content: response}}
		normalized, err := application.NewFingerprintAnalyzer(model).Normalize(context.Background(), "project", "source", `{"Alarm":"测试信息"}`)
		if err != nil {
			t.Fatalf("response %q error = %v", response, err)
		}
		if !strings.HasPrefix(normalized.Fingerprint, "fallback:v1:") {
			t.Fatalf("response %q did not fallback: %#v", response, normalized)
		}
	}
}
func TestModelAnalyzerFallsBackOnProviderFailure(t *testing.T) {
	model := &fakeModel{err: errors.New("provider unavailable")}
	analyzer := application.NewFingerprintAnalyzer(model)
	normalized, err := analyzer.Normalize(context.Background(), "project", "source", `{"Alarm":"测试信息","UIN":"100"}`)
	if err != nil {
		t.Fatalf("fallback error = %v", err)
	}
	if !strings.HasPrefix(normalized.Fingerprint, "fallback:v1:") || normalized.Title != "测试信息" {
		t.Fatalf("fallback = %#v", normalized)
	}
}

func TestModelAnalyzerUsesConfiguredTimeoutAndFallsBack(t *testing.T) {
	started := time.Now()
	normalized, err := application.NewFingerprintAnalyzerWithTimeout(blockingModel{}, 10*time.Millisecond).Normalize(
		context.Background(), "project", "source", `{"Alarm":"测试信息"}`,
	)
	if err != nil {
		t.Fatalf("normalize error = %v", err)
	}
	if elapsed := time.Since(started); elapsed < 10*time.Millisecond || elapsed > time.Second {
		t.Fatalf("configured timeout elapsed = %s", elapsed)
	}
	if !strings.HasPrefix(normalized.Fingerprint, "fallback:v1:") {
		t.Fatalf("timeout did not fallback: %#v", normalized)
	}
}

func TestNewServiceRejectsLegacyIncidentWhenEvidenceIsConfigured(t *testing.T) {
	_, err := application.NewService(application.Options{
		Tokens:       &fakeTokens{},
		Observations: &fakeObservations{},
		Incidents:    &legacyIncidentFake{},
		Evidence:     &fakeEvidence{},
		Now:          time.Now,
	})
	if err == nil || !strings.Contains(err.Error(), "inbound evidence ingester") {
		t.Fatalf("NewService() error = %v, want enriched incident dependency error", err)
	}
}

func TestLegacyIncidentFallbackRemainsAvailableWithoutEvidence(t *testing.T) {
	incidents := &legacyIncidentFake{called: make(chan struct{}, 1)}
	service, err := application.NewService(application.Options{
		Tokens:       &fakeTokens{ingress: projectapplication.WebhookIngress{ProjectID: "project", SourceID: "source"}},
		Observations: &fakeObservations{},
		Incidents:    incidents,
		Now:          time.Now,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if err := service.Ingest(context.Background(), "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ", "legacy alert"); err != nil {
		t.Fatalf("Ingest() error = %v", err)
	}
	select {
	case <-incidents.called:
	case <-time.After(time.Second):
		t.Fatal("legacy incident fallback did not run")
	}
}

func TestIngestReturnsBeforeAnalyzerAndPersistsInBackground(t *testing.T) {
	tokens := &fakeTokens{ingress: projectapplication.WebhookIngress{ProjectID: "project", SourceID: "source"}}
	observations := &fakeObservations{called: make(chan struct{}, 1)}
	incidents := &fakeIncidents{incident: incidentdomain.Incident{Number: 2049}, created: true, called: make(chan struct{}, 1)}
	analyzer := &blockingAnalyzer{
		started: make(chan struct{}), release: make(chan struct{}),
		normalized: application.NormalizedInbound{Title: "【告警】测试信息", Fingerprint: "fingerprint", Message: "raw"},
	}
	service, err := application.NewService(application.Options{Tokens: tokens, Analyzer: analyzer, Observations: observations, Incidents: incidents, Now: func() time.Time {
		return time.Date(2026, 8, 19, 11, 41, 44, 0, time.UTC)
	}})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	returned := make(chan error, 1)
	go func() {
		returned <- service.Ingest(context.Background(), "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ", "【告警】测试信息\n触发时间：2026-08-10")
	}()
	select {
	case <-analyzer.started:
	case <-time.After(time.Second):
		t.Fatal("analyzer did not start")
	}
	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("Ingest() error = %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		close(analyzer.release)
		t.Fatal("Ingest waited for analyzer")
	}
	select {
	case <-observations.called:
		t.Fatal("observation was written before analyzer completed")
	default:
	}
	close(analyzer.release)
	select {
	case <-incidents.called:
	case <-time.After(time.Second):
		t.Fatal("background incident ingestion did not complete")
	}
	if tokens.token != "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ" || observations.message == "" || incidents.title != "【告警】测试信息" {
		t.Fatalf("tokens=%#v observations=%#v incidents=%#v", tokens, observations, incidents)
	}
}

func TestIngestReportsBackgroundFailureWithoutPayload(t *testing.T) {
	reporter := &fakeFailures{called: make(chan struct{}, 1)}
	service, err := application.NewService(application.Options{
		Tokens:   &fakeTokens{ingress: projectapplication.WebhookIngress{ProjectID: "project", SourceID: "source"}},
		Analyzer: failingAnalyzer{err: errors.New("classifier failed")}, Observations: &fakeObservations{},
		Incidents: &fakeIncidents{}, Failures: reporter, Now: time.Now,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if err := service.Ingest(context.Background(), "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ", "sensitive payload"); err != nil {
		t.Fatalf("Ingest() error = %v", err)
	}
	select {
	case <-reporter.called:
	case <-time.After(time.Second):
		t.Fatal("background failure was not reported")
	}
	if reporter.failure.ProjectID != "project" || reporter.failure.SourceID != "source" || reporter.err == nil || strings.Contains(reporter.err.Error(), "sensitive payload") {
		t.Fatalf("reported failure = %#v err=%v", reporter.failure, reporter.err)
	}
}

func TestIngestMapsUnknownToken(t *testing.T) {
	service, err := application.NewService(application.Options{
		Tokens: &fakeTokens{err: projectapplication.ErrNotFound}, Observations: &fakeObservations{}, Incidents: &fakeIncidents{}, Now: time.Now,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if err := service.Ingest(context.Background(), "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ", "alert"); !errors.Is(err, application.ErrWebhookNotFound) {
		t.Fatalf("unknown token error = %v", err)
	}
}

func TestTencentIngressUsesDeterministicProviderFingerprint(t *testing.T) {
	ingest := func(raw, modelFingerprint string) string {
		t.Helper()
		incidents := &fakeIncidents{called: make(chan struct{}, 1)}
		observations := &fakeObservations{called: make(chan struct{}, 1)}
		service, err := application.NewService(application.Options{
			Tokens: &fakeTokens{ingress: projectapplication.WebhookIngress{
				ProjectID: "project", SourceID: "source", Provider: "tencent_cls",
			}},
			Analyzer: fixedAnalyzer{normalized: application.NormalizedInbound{
				Title: "model title", Fingerprint: modelFingerprint, Message: "semantic payload",
			}},
			Observations: observations, Incidents: incidents, Now: time.Now,
		})
		if err != nil {
			t.Fatalf("NewService() error = %v", err)
		}
		if err := service.IngestWithContentType(context.Background(), "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ", raw, "application/json"); err != nil {
			t.Fatalf("IngestWithContentType() error = %v", err)
		}
		select {
		case <-incidents.called:
		case <-time.After(time.Second):
			t.Fatal("tencent incident ingestion did not complete")
		}
		if observations.fingerprint != incidents.fingerprint {
			t.Fatalf("observation fingerprint = %q, incident fingerprint = %q", observations.fingerprint, incidents.fingerprint)
		}
		return incidents.fingerprint
	}

	first := ingest(`{"TopicId":"topic-1","Alarm":"fixthe","Topic":"prod-service-cvm","TriggerParams":"count=1","DetailUrl":"https://alarm.cls.tencentcs.com/first"}`, "ai:v1:first")
	second := ingest(`{"TopicId":"topic-2","Alarm":" fixthe ","Topic":"prod-service-cvm","TriggerParams":"-","DetailUrl":"https://alarm.cls.tencentcs.com/second"}`, "ai:v1:second")
	changedAlarm := ingest(`{"TopicId":"topic-3","Alarm":"database unavailable","Topic":"prod-service-cvm","DetailUrl":"https://alarm.cls.tencentcs.com/third"}`, "ai:v1:third")
	changedTopic := ingest(`{"TopicId":"topic-4","Alarm":"fixthe","Topic":"staging-service-cvm","DetailUrl":"https://alarm.cls.tencentcs.com/fourth"}`, "ai:v1:fourth")

	if first != second {
		t.Fatalf("equivalent tencent callbacks changed fingerprint: %q != %q", first, second)
	}
	if first == changedAlarm || first == changedTopic {
		t.Fatalf("different tencent semantic fields shared fingerprint: first=%q alarm=%q topic=%q", first, changedAlarm, changedTopic)
	}
	if !strings.HasPrefix(first, "tencent-cls:v1:") {
		t.Fatalf("tencent fingerprint = %q", first)
	}
}

func TestTencentIngressPersistsOnlyNormalizedAlert(t *testing.T) {
	observations := &fakeObservations{called: make(chan struct{}, 1)}
	incidents := &fakeIncidents{
		incident: incidentdomain.Incident{InternalID: "incident"},
		called:   make(chan struct{}, 1),
	}
	evidence := &fakeEvidence{called: make(chan struct{}, 1)}
	service, err := application.NewService(application.Options{
		Tokens: &fakeTokens{ingress: projectapplication.WebhookIngress{
			ProjectID: "project", SourceID: "source", Provider: "tencent_cls",
		}},
		Observations: observations, Incidents: incidents, Evidence: evidence, Now: time.Now,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	raw := `{"TopicId":"topic","Alarm":"service unavailable","DetailUrl":"https://alarm.cls.tencentcs.com/short-capability"}`
	if err := service.IngestWithContentType(context.Background(), "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ", raw, "application/json"); err != nil {
		t.Fatalf("IngestWithContentType() error = %v", err)
	}
	select {
	case <-evidence.called:
	case <-time.After(time.Second):
		t.Fatal("normalized alert evidence was not persisted")
	}
	if observations.message != raw {
		t.Fatalf("observation message = %q, want complete callback", observations.message)
	}
	if len(evidence.records) != 1 || evidence.records[0].EvidenceKind != remediationdomain.EvidenceKindNormalizedAlert {
		t.Fatalf("ingress evidence = %#v, want only normalized alert", evidence.records)
	}
	encoded := string(evidence.records[0].Payload)
	if strings.Contains(encoded, "DetailUrl") || strings.Contains(encoded, "connector_observation") || strings.Contains(encoded, "provider_detail") {
		t.Fatalf("ingress evidence leaked detail material: %s", encoded)
	}
}

func TestIngestWithContentTypeRejectsInvalidTencentCallbackSynchronously(t *testing.T) {
	observations := &fakeObservations{called: make(chan struct{}, 1)}
	service, err := application.NewService(application.Options{
		Tokens: &fakeTokens{ingress: projectapplication.WebhookIngress{
			ProjectID: "project", SourceID: "source", Provider: "tencent_cls",
		}},
		Observations: observations, Incidents: &fakeIncidents{}, Now: time.Now,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	err = service.IngestWithContentType(context.Background(), "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ", `{"TopicId":"topic"}`, "application/json")
	if !errors.Is(err, application.ErrInvalidTencentCallback) {
		t.Fatalf("invalid callback error = %v", err)
	}
	select {
	case <-observations.called:
		t.Fatal("invalid callback started background persistence")
	default:
	}
}
