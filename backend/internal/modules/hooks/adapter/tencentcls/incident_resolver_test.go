package tencentcls

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	hooksapplication "fixthe/backend/internal/modules/hooks/application"
	"fixthe/backend/internal/modules/remediation/domain"
)

func TestTrustedDirectProjectionRequiresObjectPayloadAndExplicitContradictions(t *testing.T) {
	validPayload := json.RawMessage(`{"RecordID":"record-1"}`)
	validProvenance := json.RawMessage(`{"adapter":"tencent_cls","detail_capability_validated":true,"detail_resolution":"validated_provider_detail_get_alert_detail","contradictions":[]}`)
	cases := []struct {
		name       string
		payload    json.RawMessage
		provenance json.RawMessage
		want       bool
	}{
		{name: "valid", payload: validPayload, provenance: validProvenance, want: true},
		{name: "null payload", payload: json.RawMessage(`null`), provenance: validProvenance},
		{name: "null contradictions", payload: validPayload, provenance: json.RawMessage(`{"adapter":"tencent_cls","detail_capability_validated":true,"detail_resolution":"validated_provider_detail_get_alert_detail","contradictions":null}`)},
		{name: "provider contradiction", payload: validPayload, provenance: json.RawMessage(`{"adapter":"tencent_cls","detail_capability_validated":true,"detail_resolution":"validated_provider_detail_get_alert_detail","contradictions":["topic mismatch"]}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			projection := hooksapplication.ProviderDetailEvidence{
				Provider: "tencent_cls", EvidenceKind: domain.EvidenceKindProviderDetail,
				Classification: domain.EvidenceDirectFault, Outcome: string(OutcomeSuccess),
				Available: true, Primary: true, Payload: tc.payload, Provenance: tc.provenance,
			}
			if got := trustedDirectProjection(projection, ConnectorObservation{}); got != tc.want {
				t.Fatalf("trustedDirectProjection() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestIncidentDetailResolverRejectsNonDirectOrContradictoryProjection(t *testing.T) {
	cases := []struct {
		name          string
		responseTopic string
		analysisType  string
		wantPayload   string
	}{
		{name: "contextual", responseTopic: "topic-1", analysisType: "context", wantPayload: `{"message":"context"}`},
		{name: "contradictory", responseTopic: "topic-2", analysisType: "original", wantPayload: `{"message":"fault"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, err := NewClient(Options{HTTPClient: &http.Client{Transport: resolverRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.Method == http.MethodGet {
					return resolverResponse(request, http.StatusOK, "text/html", `<script>var detail={"RecordId":"record-1"}</script>`), nil
				}
				return resolverResponse(request, http.StatusOK, "application/json", `{"data":{"RecordId":"record-1","TopicId":"`+tc.responseTopic+`","ResultsSnapshot":{"AnalysisInfo":[{"Type":"`+tc.analysisType+`"}],"RawResults":[`+tc.wantPayload+`]}}}`), nil
			})}})
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			writer := &resolverEvidenceWriter{}
			resolver, err := NewIncidentDetailResolver(client, resolverCallbackLoader{}, writer, time.Now)
			if err != nil {
				t.Fatalf("NewIncidentDetailResolver: %v", err)
			}
			_, err = resolver.ResolveTencentCLSDetail(context.Background(), domain.TencentCLSDetailRequest{
				RunID: "run-1", IncidentID: "incident-1", ProjectID: "project-1", EnvironmentID: "environment-1", SourceID: "source-1",
			})
			if err == nil {
				t.Fatal("ResolveTencentCLSDetail unexpectedly accepted untrusted projection")
			}
			runtimeErr, ok := err.(*domain.ToolRuntimeError)
			if !ok || runtimeErr.Code != "provider_detail_invalid" || writer.evidence.EvidenceID != "" {
				t.Fatalf("error=%#v writer=%#v", err, writer.evidence)
			}
		})
	}
}

func TestIncidentDetailResolverFetchesAndPersistsCurrentRunEvidence(t *testing.T) {
	client, err := NewClient(Options{HTTPClient: &http.Client{Transport: resolverRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodGet {
			return resolverResponse(request, http.StatusOK, "text/html", `<script>var detail={"RecordId":"record-1"}</script>`), nil
		}
		return resolverResponse(request, http.StatusOK, "application/json", `{"data":{"RecordId":"record-1","ResultsSnapshot":{"AnalysisInfo":[{"Type":"original"}],"RawResults":[{"message":"panic"}]}}}`), nil
	})}})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	writer := &resolverEvidenceWriter{}
	resolver, err := NewIncidentDetailResolver(client, resolverCallbackLoader{}, writer, func() time.Time {
		return time.Date(2026, time.August, 25, 10, 20, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatalf("NewIncidentDetailResolver: %v", err)
	}
	result, err := resolver.ResolveTencentCLSDetail(context.Background(), domain.TencentCLSDetailRequest{
		RunID: "run-1", IncidentID: "incident-1", ProjectID: "project-1", EnvironmentID: "environment-1", SourceID: "source-1",
	})
	if err != nil {
		t.Fatalf("ResolveTencentCLSDetail: %v", err)
	}
	if writer.evidence.RunID != "run-1" || writer.evidence.EvidenceKind != domain.EvidenceKindProviderDetail ||
		writer.evidence.Classification != domain.EvidenceDirectFault || result.Evidence.EvidenceID != "persisted-detail" {
		t.Fatalf("persisted/result evidence = %#v / %#v", writer.evidence, result.Evidence)
	}
	payload := string(writer.evidence.Payload)
	if !strings.Contains(payload, "RawResults") || !strings.Contains(payload, "panic") || strings.Contains(payload, "DetailUrl") {
		t.Fatalf("persisted payload = %s", payload)
	}
}

type resolverCallbackLoader struct {
	snapshot domain.CallbackEvidenceSnapshot
}

func (l resolverCallbackLoader) LoadTencentCLSCallbackSnapshot(context.Context, string) (domain.CallbackEvidenceSnapshot, error) {
	if l.snapshot.Payload != "" {
		return l.snapshot, nil
	}
	return domain.CallbackEvidenceSnapshot{
		IncidentID: "incident-1", ProjectID: "project-1", EnvironmentID: "environment-1", SourceID: "source-1",
		ObservationID: "observation-1",
		Payload:       `{"TopicId":"topic-1","DetailUrl":"https://alarm.cls.tencentcs.com/alert-1"}`,
		OccurredAt:    time.Date(2026, time.August, 25, 10, 12, 0, 0, time.UTC),
	}, nil
}

type resolverEvidenceWriter struct {
	evidence domain.StoredEvidence
}

func (w *resolverEvidenceWriter) AppendEvidence(_ context.Context, evidence domain.StoredEvidence) (domain.StoredEvidence, error) {
	w.evidence = evidence
	evidence.EvidenceID = "persisted-detail"
	return evidence, nil
}

func (w *resolverEvidenceWriter) ResolveEvidence(context.Context, string, []domain.EvidenceCitation) (domain.EvidenceResolution, error) {
	return domain.EvidenceResolution{}, nil
}

func (w *resolverEvidenceWriter) PersistEvidenceAssessment(context.Context, domain.EvidenceAssessment) error {
	return nil
}

type resolverRoundTripFunc func(*http.Request) (*http.Response, error)

func (f resolverRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func resolverResponse(request *http.Request, status int, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}
