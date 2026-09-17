package application_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"mendry/backend/internal/modules/remediation/application"
	"mendry/backend/internal/modules/remediation/domain"
)

type fakeTencentDetailPort struct {
	calls  int
	result domain.TencentCLSDetailResult
	err    error
}

func (p *fakeTencentDetailPort) ResolveTencentCLSDetail(_ context.Context, _ domain.TencentCLSDetailRequest) (domain.TencentCLSDetailResult, error) {
	p.calls++
	return p.result, p.err
}

type sequencedTencentDetailPort struct {
	calls  int
	errors []error
	result domain.TencentCLSDetailResult
}

func (p *sequencedTencentDetailPort) ResolveTencentCLSDetail(_ context.Context, _ domain.TencentCLSDetailRequest) (domain.TencentCLSDetailResult, error) {
	index := p.calls
	p.calls++
	if index < len(p.errors) && p.errors[index] != nil {
		return domain.TencentCLSDetailResult{}, p.errors[index]
	}
	return p.result, nil
}

func tencentBootstrapEvidence() domain.BootstrapEvidence {
	return domain.BootstrapEvidence{Records: []domain.StoredEvidence{{
		EvidenceID: "alert-1", Provider: "tencent_cls", EvidenceKind: domain.EvidenceKindNormalizedAlert,
		Outcome: "success", Available: true, Payload: json.RawMessage(`{"title":"Tencent alert"}`),
	}}}
}

func tencentDetailResult() domain.TencentCLSDetailResult {
	payload := json.RawMessage(`{"recordId":"record-1","ResultsSnapshot":{"AnalysisInfo":[{"Type":"original"}],"RawResults":[{"message":"fault"}]}}`)
	return domain.TencentCLSDetailResult{
		Evidence: domain.StoredEvidence{
			EvidenceID: "detail-1", Provider: "tencent_cls", EvidenceKind: domain.EvidenceKindProviderDetail,
			Classification: domain.EvidenceDirectFault, Outcome: "success", Available: true, Primary: true,
			Payload: payload, Provenance: json.RawMessage(`{"adapter":"tencent_cls","detail_capability_validated":true,"detail_resolution":"validated_provider_detail_get_alert_detail","contradictions":[]}`), ByteCount: int64(len(payload)),
		},
		BytesRetrieved: int64(len(payload)),
	}
}

func TestTencentDetailGateRejectsContextualOrContradictoryPortEvidence(t *testing.T) {
	cases := []struct {
		name           string
		classification domain.EvidenceClassification
		primary        bool
		provenance     json.RawMessage
		payload        json.RawMessage
		emptyPayload   bool
	}{
		{name: "contextual", classification: domain.EvidenceContextual, primary: true},
		{name: "contradictory", classification: domain.EvidenceContradictory, primary: true},
		{name: "non-primary", classification: domain.EvidenceDirectFault, primary: false},
		{name: "provenance contradiction", classification: domain.EvidenceDirectFault, primary: true, provenance: json.RawMessage(`{"adapter":"tencent_cls","detail_capability_validated":true,"detail_resolution":"validated_provider_detail_get_alert_detail","contradictions":["topic mismatch"]}`)},
		{name: "null contradictions", classification: domain.EvidenceDirectFault, primary: true, provenance: json.RawMessage(`{"adapter":"tencent_cls","detail_capability_validated":true,"detail_resolution":"validated_provider_detail_get_alert_detail","contradictions":null}`)},
		{name: "empty provenance", classification: domain.EvidenceDirectFault, primary: true},
		{name: "malformed provenance", classification: domain.EvidenceDirectFault, primary: true, provenance: json.RawMessage(`{"adapter":`)},
		{name: "incomplete provenance", classification: domain.EvidenceDirectFault, primary: true, provenance: json.RawMessage(`{"adapter":"tencent_cls"}`)},
		{name: "null payload", classification: domain.EvidenceDirectFault, primary: true, payload: json.RawMessage(`null`)},
		{name: "array payload", classification: domain.EvidenceDirectFault, primary: true, payload: json.RawMessage(`[]`)},
		{name: "empty payload", classification: domain.EvidenceDirectFault, primary: true, emptyPayload: true}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gateway := application.NewToolGatewayWithDynamicRuntime(&fakeRepoPort{}, &fakeEvidencePort{}, nil, nil, nil)
			portResult := tencentDetailResult()
			portResult.Evidence.Classification = tc.classification
			portResult.Evidence.Primary = tc.primary
			portResult.Evidence.Provenance = tc.provenance
			if tc.payload != nil {
				portResult.Evidence.Payload = tc.payload
			}
			if tc.emptyPayload {
				portResult.Evidence.Payload = nil
			}
			port := &fakeTencentDetailPort{result: portResult}
			gateway.SetTencentCLSDetailPort(port)
			scope := domain.EvidenceScope{ProjectID: "project-1", SourceID: "source-1"}
			source := domain.SourceCapabilitySnapshot{
				ProjectID: "project-1", SourceID: "source-1", Kind: "ssh", Enabled: true, Supported: true,
				Declared: []string{"pull_collection"}, Version: 1, SSHDeploymentKind: "docker",
			}
			catalog, err := gateway.BuildCatalogWithBootstrap(context.Background(), "run-1", "incident-1", domain.RunStateDiagnosing, scope, source, tencentBootstrapEvidence())
			if err != nil {
				t.Fatalf("BuildCatalogWithBootstrap: %v", err)
			}
			_, err = gateway.ExecuteToolWithCatalog(context.Background(), domain.RunStateDiagnosing, domain.RepoRef{}, scope, catalog, application.ToolTencentCLSDetail, map[string]interface{}{})
			runtimeErr, ok := err.(*domain.ToolRuntimeError)
			definitions := catalog.DefinitionsForPhase(domain.RunStateDiagnosing)
			if !ok || runtimeErr.Code != "provider_detail_invalid" || containsToolName(definitionNames(definitions), application.ToolTencentCLSDetail) ||
				!containsToolName(definitionNames(definitions), application.ToolDockerLogs) {
				t.Fatalf("result error=%#v catalog=%#v", err, definitions)
			}
		})
	}
}

func TestCatalogRequiresTencentDetailBeforeNormalTools(t *testing.T) {
	gateway := application.NewToolGatewayWithDynamicRuntime(&fakeRepoPort{}, &fakeEvidencePort{}, nil, nil, nil)
	port := &fakeTencentDetailPort{result: tencentDetailResult()}
	gateway.SetTencentCLSDetailPort(port)
	scope := domain.EvidenceScope{ProjectID: "project-1", SourceID: "source-1"}
	source := domain.SourceCapabilitySnapshot{
		ProjectID: "project-1", SourceID: "source-1", Kind: "ssh", Enabled: true, Supported: true,
		Declared: []string{"pull_collection"}, Version: 1, SSHDeploymentKind: "docker",
	}
	catalog, err := gateway.BuildCatalogWithBootstrap(context.Background(), "run-1", "incident-1", domain.RunStateDiagnosing, scope, source, tencentBootstrapEvidence())
	if err != nil {
		t.Fatalf("BuildCatalogWithBootstrap: %v", err)
	}
	definitions := catalog.DefinitionsForPhase(domain.RunStateDiagnosing)
	if len(definitions) != 1 || definitions[0].Name != application.ToolTencentCLSDetail {
		t.Fatalf("initial definitions = %#v, want only Tencent detail tool", definitions)
	}
	properties, ok := definitions[0].Parameters["properties"].(map[string]interface{})
	if !ok || len(properties) != 0 {
		t.Fatalf("Tencent detail parameters properties = %#v, want an empty JSON object", definitions[0].Parameters["properties"])
	}
	encodedParameters, err := json.Marshal(definitions[0].Parameters)
	if err != nil || strings.Contains(string(encodedParameters), `"properties":null`) {
		t.Fatalf("Tencent detail parameter schema = %s, err=%v", encodedParameters, err)
	}
	result, err := gateway.ExecuteToolWithCatalog(context.Background(), domain.RunStateDiagnosing, domain.RepoRef{}, scope, catalog, application.ToolTencentCLSDetail, map[string]interface{}{})
	if err != nil {
		t.Fatalf("ExecuteToolWithCatalog: %v", err)
	}
	if port.calls != 1 || len(result.EvidenceIDs) != 1 || result.EvidenceIDs[0] != "detail-1" {
		t.Fatalf("detail calls/result = %d/%#v", port.calls, result)
	}
	var names []string
	for _, definition := range catalog.DefinitionsForPhase(domain.RunStateDiagnosing) {
		names = append(names, definition.Name)
	}
	if !containsToolName(names, application.ToolDockerLogs) || containsToolName(names, application.ToolTencentCLSDetail) {
		t.Fatalf("post-detail definitions = %v", names)
	}
}

func TestCoordinatorShowsTencentDetailFailureAndRequiresRetry(t *testing.T) {
	model := &scriptedModel{responses: []string{
		fmt.Sprintf(`{"schemaVersion":"v1","kind":"requestTool","requestTool":{"toolName":%q,"parameters":{}}}`, application.ToolTencentCLSDetail),
		fmt.Sprintf(`{"schemaVersion":"v1","kind":"requestTool","requestTool":{"toolName":%q,"parameters":{}}}`, application.ToolTencentCLSDetail),
		diagnosisEnvelope("external_dependency"),
	}}
	store := newFakeRunStore()
	coord := application.NewRemediationCoordinatorWithReview(store, &fakeRepoPort{}, &fakeEvidencePort{}, model, nil, store, store)
	coord.SetBootstrapEvidenceLoader(&bootstrapEvidenceLoader{value: tencentBootstrapEvidence()})
	detail := &sequencedTencentDetailPort{
		errors: []error{&domain.ToolRuntimeError{Code: "provider_detail_timeout", Retryable: true, Message: "detail timed out"}},
		result: tencentDetailResult(),
	}
	coord.SetTencentCLSDetailPort(detail)

	run, err := coord.Start(context.Background(), domain.NewRun{IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if run.State != domain.RunStateCompletedNonCode || detail.calls != 2 || model.calls != 3 {
		t.Fatalf("state/detail/model calls = %s/%d/%d", run.State, detail.calls, model.calls)
	}
	if !strings.Contains(model.turns[1].UserMessage, "provider_detail_timeout") {
		t.Fatalf("retry turn missing safe detail failure: %s", model.turns[1].UserMessage)
	}
}

func TestCoordinatorBlocksDiagnosisUntilTencentDetailSucceeds(t *testing.T) {
	model := &scriptedModel{responses: []string{
		diagnosisEnvelope("external_dependency"),
		fmt.Sprintf(`{"schemaVersion":"v1","kind":"requestTool","requestTool":{"toolName":%q,"parameters":{}}}`, application.ToolTencentCLSDetail),
		diagnosisEnvelope("external_dependency"),
	}}
	store := newFakeRunStore()
	coord := application.NewRemediationCoordinatorWithReview(store, &fakeRepoPort{}, &fakeEvidencePort{}, model, nil, store, store)
	coord.SetBootstrapEvidenceLoader(&bootstrapEvidenceLoader{value: tencentBootstrapEvidence()})
	detail := &fakeTencentDetailPort{result: tencentDetailResult()}
	coord.SetTencentCLSDetailPort(detail)

	run, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if run.State != domain.RunStateCompletedNonCode || detail.calls != 1 || model.calls != 3 {
		t.Fatalf("state/detail/model calls = %s/%d/%d", run.State, detail.calls, model.calls)
	}
	if !strings.Contains(model.turns[1].UserMessage, "required_direct_evidence") {
		t.Fatalf("second turn missing mandatory detail correction: %s", model.turns[1].UserMessage)
	}
}

func TestCoordinatorUsesFallbackToolsAfterNonRetryableTencentDetailFailure(t *testing.T) {
	model := &scriptedModel{responses: []string{
		fmt.Sprintf(`{"schemaVersion":"v1","kind":"requestTool","requestTool":{"toolName":%q,"parameters":{}}}`, application.ToolTencentCLSDetail),
		fmt.Sprintf(`{"schemaVersion":"v1","kind":"requestTool","requestTool":{"toolName":%q,"parameters":{"since":"2026-08-26T09:02:00Z","until":"2026-08-26T09:12:00Z","tail":500}}}`, application.ToolDockerLogs),
		diagnosisEnvelope("external_dependency"),
	}}
	store := newFakeRunStore()
	source := domain.SourceCapabilitySnapshot{
		ProjectID: testProjectID, SourceID: "source-1", Kind: "ssh", Enabled: true, Supported: true,
		Declared: []string{"pull_collection"}, Version: 1,
		SSHDeploymentKind: "docker", SSHContainerName: "real-estate-api",
	}
	coord := application.NewRemediationCoordinatorWithDynamicRuntime(
		store, &fakeRepoPort{}, &fakeEvidencePort{}, nil, model,
		wiringLookup{identity: application.IncidentIdentity{
			ID: testIncidentUUID, ProjectID: testProjectID, EnvironmentID: "environment-1", SourceID: "source-1",
			DeployedCommit: "abc123", LifecycleGeneration: 1,
		}}, nil, store, store, nil, staticSourceCaps{snapshot: source}, nil, nil,
	)
	coord.SetBootstrapEvidenceLoader(&bootstrapEvidenceLoader{value: func() domain.BootstrapEvidence {
		value := tencentBootstrapEvidence()
		value.TimeRange = domain.TimeRange{
			Start: time.Date(2026, 8, 26, 9, 2, 0, 0, time.UTC),
			End:   time.Date(2026, 8, 26, 9, 12, 0, 0, time.UTC),
		}
		return value
	}()})
	docker := &dockerGatewayPort{result: domain.DockerLogResult{
		Container:      domain.DockerContainerIdentity{Name: "real-estate-api", ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		Stdout:         "panic: nil pointer\n",
		BytesRetrieved: 18,
	}}
	coord.SetDockerEvidencePort(docker)
	detail := &fakeTencentDetailPort{err: &domain.ToolRuntimeError{Code: "provider_detail_invalid", Message: "empty analysis"}}
	coord.SetTencentCLSDetailPort(detail)

	run, err := coord.Start(context.Background(), domain.NewRun{
		IncidentID: testIncidentUUID, LifecycleGeneration: 1, DeployedCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if run.State != domain.RunStateCompletedNonCode || detail.calls != 1 || len(docker.queries) != 1 || model.calls != 3 {
		t.Fatalf("state/detail/docker/model calls = %s/%d/%d/%d", run.State, detail.calls, len(docker.queries), model.calls)
	}
	if containsToolName(definitionNames(model.turns[1].Tools), application.ToolTencentCLSDetail) ||
		!containsToolName(definitionNames(model.turns[1].Tools), application.ToolDockerLogs) {
		t.Fatalf("fallback tool catalog = %#v", model.turns[1].Tools)
	}
	if !strings.Contains(model.turns[1].UserMessage, "provider_detail_invalid") {
		t.Fatalf("fallback turn lost detail failure observation: %s", model.turns[1].UserMessage)
	}
}

func TestCoordinatorManualContinuationReusesPersistedEvidenceWithoutCollection(t *testing.T) {
	for _, tc := range []struct {
		name            string
		invocation      domain.ToolInvocation
		wantFailureCode string
	}{
		{
			name: "safe provider detail code",
			invocation: domain.ToolInvocation{
				ToolName: application.ToolTencentCLSDetail,
				Error:    "provider_detail_invalid",
			},
			wantFailureCode: "provider_detail_invalid",
		},
		{
			name: "legacy generic error",
			invocation: domain.ToolInvocation{
				ToolName:      application.ToolTencentCLSDetail,
				ResultSummary: "error",
				Error:         "error",
			},
			wantFailureCode: `"errorCode":"unknown"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			predecessor := domain.RunAggregate{
				Run: domain.Run{
					RunID:               "run-previous",
					SeriesID:            "series-1",
					IncidentID:          testIncidentUUID,
					LifecycleGeneration: 1,
					DeployedCommit:      "abc123",
					AttemptNumber:       2,
					State:               domain.RunStateFailed,
					Version:             7,
				},
				ToolInvocations: []domain.ToolInvocation{tc.invocation},
			}
			store := &continuationStore{fakeRunStore: newFakeRunStore(), predecessor: predecessor}
			model := &scriptedModel{responses: []string{diagnosisEnvelope("external_dependency")}}
			source := domain.SourceCapabilitySnapshot{
				ProjectID: testProjectID, SourceID: "source-1", Kind: "ssh", Enabled: true, Supported: true,
				Declared: []string{"pull_collection"}, Version: 1, SSHDeploymentKind: "docker", SSHContainerName: "real-estate-api",
			}
			coord := application.NewRemediationCoordinatorWithDynamicRuntime(
				store, &fakeRepoPort{}, &fakeEvidencePort{}, nil, model,
				wiringLookup{identity: application.IncidentIdentity{
					ID: testIncidentUUID, ProjectID: testProjectID, EnvironmentID: "environment-1", SourceID: "source-1",
					DeployedCommit: "abc123", LifecycleGeneration: 1,
				}}, nil, store, store, nil, staticSourceCaps{snapshot: source}, nil, nil,
			)
			bootstrap := tencentBootstrapEvidence()
			bootstrap.Records[0].Payload = json.RawMessage(`{"title":"Tencent alert","DetailUrl":"https://alarm.example/raw","password":"keep-me","token":"sk-evidence12345"}`)
			bootstrap.TimeRange = domain.TimeRange{
				Start: time.Date(2026, 8, 26, 9, 2, 0, 0, time.UTC),
				End:   time.Date(2026, 8, 26, 9, 12, 0, 0, time.UTC),
			}
			coord.SetBootstrapEvidenceLoader(&bootstrapEvidenceLoader{value: bootstrap})
			docker := &dockerGatewayPort{result: domain.DockerLogResult{
				Container:      domain.DockerContainerIdentity{Name: "real-estate-api", ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
				Stdout:         "panic: nil pointer\n",
				BytesRetrieved: 18,
			}}
			coord.SetDockerEvidencePort(docker)
			detail := &fakeTencentDetailPort{err: &domain.ToolRuntimeError{Code: "provider_detail_invalid", Message: "must not be called"}}
			coord.SetTencentCLSDetailPort(detail)

			run, err := coord.Continue(context.Background(), domain.NextAttempt{
				ContinuationOfRunID:     predecessor.Run.RunID,
				SeriesID:                predecessor.Run.SeriesID,
				IncidentID:              predecessor.Run.IncidentID,
				LifecycleGeneration:     predecessor.Run.LifecycleGeneration,
				DeployedCommit:          predecessor.Run.DeployedCommit,
				ContextVersion:          8,
				ExpectedPreviousVersion: predecessor.Run.Version,
				Origin:                  domain.TriggerOriginManualContinue,
				TriggerReason:           domain.TriggerOriginManualContinue,
				ContinuationReason:      "operator requested continuation",
			})
			if err != nil {
				t.Fatalf("Continue() error = %v", err)
			}
			if run.State != domain.RunStateCompletedNonCode || detail.calls != 0 || len(docker.queries) != 0 || model.calls != 1 {
				t.Fatalf("state/detail/docker/model calls = %s/%d/%d/%d turns=%#v", run.State, detail.calls, len(docker.queries), model.calls, model.turns)
			}
			if len(model.turns) != 1 {
				t.Fatalf("continuation turns = %#v", model.turns)
			}
			for _, definition := range model.turns[0].Tools {
				if !strings.HasPrefix(definition.Name, "repository.") {
					t.Fatalf("manual continuation exposed external evidence tool %q", definition.Name)
				}
			}
			for _, want := range []string{tc.wantFailureCode, "Manual continuation analysis mode", "DetailUrl", "https://alarm.example/raw", `"password":"keep-me"`, `"token":"sk-evidence12345"`} {
				if !strings.Contains(model.turns[0].UserMessage, want) {
					t.Fatalf("continuation context missing %q: %s", want, model.turns[0].UserMessage)
				}
			}
		})
	}
}

func definitionNames(definitions []domain.ToolDefinition) []string {
	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		names = append(names, definition.Name)
	}
	return names
}

func containsToolName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}
