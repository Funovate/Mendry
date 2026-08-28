package application_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"fixthe/backend/internal/modules/remediation/application"
	"fixthe/backend/internal/modules/remediation/domain"
)

type catalogRuntime struct {
	discovered    domain.DynamicToolCatalog
	discoverErr   error
	result        domain.DynamicToolResult
	callErr       error
	discoverCalls int
	closeCalls    int
	calls         []domain.DynamicToolCall
}

func (r *catalogRuntime) Discover(context.Context, domain.DynamicToolScope) (domain.DynamicToolCatalog, error) {
	r.discoverCalls++
	return r.discovered, r.discoverErr
}

func (r *catalogRuntime) Call(_ context.Context, _ domain.DynamicToolScope, call domain.DynamicToolCall) (domain.DynamicToolResult, error) {
	r.calls = append(r.calls, call)
	return r.result, r.callErr
}

func (r *catalogRuntime) CloseRun(context.Context, string) error {
	r.closeCalls++
	return nil
}

type catalogPolicy struct {
	snapshot domain.ToolPolicySnapshot
	err      error
}

func (p *catalogPolicy) ResolveToolPolicy(context.Context, string, string) (domain.ToolPolicySnapshot, error) {
	return p.snapshot, p.err
}

func mcpSource() domain.SourceCapabilitySnapshot {
	return domain.SourceCapabilitySnapshot{
		ProjectID: "project-1", SourceID: "source-1", Kind: "mcp", Enabled: true, Supported: true,
		Declared: []string{"context_collection"}, Version: 4,
	}
}

func mcpPolicy(names ...string) domain.ToolPolicySnapshot {
	return mcpPolicyFor([]domain.RunState{domain.RunStateDiagnosing, domain.RunStateCollectingMoreContext}, names...)
}

func mcpPolicyFor(phases []domain.RunState, names ...string) domain.ToolPolicySnapshot {
	rawEntries := make([]map[string]interface{}, 0, len(names))
	for _, name := range names {
		rawEntries = append(rawEntries, map[string]interface{}{
			"toolName": name,
			"effect":   "read",
			"phases":   phases,
		})
	}
	raw, err := json.Marshal(rawEntries)
	if err != nil {
		panic(err)
	}
	policy, err := domain.ParseToolPolicy("project-1", "source-1", 1, "", raw)
	if err != nil {
		panic(err)
	}
	return policy
}

func mcpDiscovery(names ...string) domain.DynamicToolCatalog {
	tools := make([]domain.DynamicToolDefinition, 0, len(names))
	for _, name := range names {
		tools = append(tools, domain.DynamicToolDefinition{
			SourceID: "source-1", ServerID: "fixture-server", Name: name, Description: "bounded read",
			InputSchema: map[string]interface{}{
				"type": "object", "properties": map[string]interface{}{"query": map[string]interface{}{"type": "string"}},
				"required": []interface{}{"query"}, "additionalProperties": false,
			},
		})
	}
	catalog := domain.DynamicToolCatalog{SourceID: "source-1", ServerID: "fixture-server", Tools: tools}
	return withDiscoveryHash(catalog)
}

func withDiscoveryHash(catalog domain.DynamicToolCatalog) domain.DynamicToolCatalog {
	payload, err := json.Marshal(struct {
		Server    string                         `json:"server"`
		Tools     []domain.DynamicToolDefinition `json:"tools"`
		Truncated bool                           `json:"truncated"`
	}{catalog.ServerID, catalog.Tools, catalog.Truncated})
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(payload)
	catalog.Version = hex.EncodeToString(sum[:])
	catalog.Hash = catalog.Version
	return catalog
}

func buildDynamicCatalog(t *testing.T, runtime *catalogRuntime, policy domain.ToolPolicySnapshot, source domain.SourceCapabilitySnapshot) (*application.ToolCatalog, *application.ToolGateway) {
	t.Helper()
	gateway := application.NewToolGatewayWithDynamicRuntime(&fakeRepoPort{}, &fakeEvidencePort{}, nil, runtime, &catalogPolicy{snapshot: policy})
	catalog, err := gateway.BuildCatalog(context.Background(), "run-1", domain.RunStateDiagnosing,
		domain.EvidenceScope{ProjectID: "project-1", SourceID: "source-1"}, source)
	if err != nil {
		t.Fatalf("BuildCatalog() error = %v", err)
	}
	return catalog, gateway
}

func activateDynamicTool(t *testing.T, gateway *application.ToolGateway, catalog *application.ToolCatalog, phase domain.RunState, query string) string {
	t.Helper()
	result, err := gateway.ExecuteToolWithCatalog(context.Background(), phase,
		domain.RepoRef{}, domain.EvidenceScope{ProjectID: "project-1", SourceID: "source-1"}, catalog,
		application.ToolSourceSearchTools, map[string]interface{}{"query": query, "limit": 1})
	if err != nil {
		t.Fatalf("search tools error = %v", err)
	}
	payload, ok := result.Payload.(map[string]interface{})
	if !ok {
		t.Fatalf("search payload = %#v", result.Payload)
	}
	matches, ok := payload["matches"].([]map[string]interface{})
	if !ok || len(matches) != 1 {
		t.Fatalf("search matches = %#v, want one", payload["matches"])
	}
	name, _ := matches[0]["name"].(string)
	if name == "" {
		t.Fatalf("search match has no name: %#v", matches[0])
	}
	return name
}

func TestAnalysisOnlyCatalogSkipsDynamicRuntimeAndEvidenceTools(t *testing.T) {
	runtime := &catalogRuntime{discovered: mcpDiscovery("query_logs")}
	gateway := application.NewToolGatewayWithDynamicRuntime(
		&fakeRepoPort{}, &fakeEvidencePort{}, nil, runtime, &catalogPolicy{snapshot: mcpPolicy("query_logs")},
	)
	catalog := gateway.BuildAnalysisOnlyCatalog("run-2", domain.RunStateDiagnosing, domain.EvidenceScope{
		ProjectID: "project-1", SourceID: "source-1",
	})
	definitions := catalog.DefinitionsForPhase(domain.RunStateDiagnosing)
	if len(definitions) != 4 {
		t.Fatalf("analysis-only definitions = %#v", definitions)
	}
	for _, definition := range definitions {
		if !strings.HasPrefix(definition.Name, "repository.") {
			t.Fatalf("analysis-only catalog exposed %q", definition.Name)
		}
	}
	if runtime.discoverCalls != 0 || len(runtime.calls) != 0 || runtime.closeCalls != 0 {
		t.Fatalf("analysis-only runtime calls = discover:%d call:%d close:%d", runtime.discoverCalls, len(runtime.calls), runtime.closeCalls)
	}
}

func TestDynamicCatalogFiltersPolicyAndPreservesNamespacedMetadata(t *testing.T) {
	runtime := &catalogRuntime{discovered: mcpDiscovery("query_errors", "delete_everything")}
	catalog, gateway := buildDynamicCatalog(t, runtime, mcpPolicy("query_errors"), mcpSource())
	definitions := catalog.DefinitionsForPhase(domain.RunStateDiagnosing)
	for _, definition := range definitions {
		if strings.HasPrefix(definition.Name, "mcp_") {
			t.Fatalf("dynamic schema was eagerly advertised: %#v", definition)
		}
	}
	publicName := activateDynamicTool(t, gateway, catalog, domain.RunStateDiagnosing, "query_errors")
	definitions = catalog.DefinitionsForPhase(domain.RunStateDiagnosing)
	var dynamic []domain.ToolDefinition
	for _, definition := range definitions {
		if strings.HasPrefix(definition.Name, "mcp_") {
			dynamic = append(dynamic, definition)
		}
	}
	if len(dynamic) != 1 || dynamic[0].Name != publicName || !strings.Contains(dynamic[0].Name, "query_errors") {
		t.Fatalf("unexpected dynamic definitions: %#v", dynamic)
	}
	if dynamic[0].Parameters["additionalProperties"] != false {
		t.Fatalf("dynamic schema lost its closed-object bound: %#v", dynamic[0].Parameters)
	}
}

func TestDynamicCatalogMissingPolicyAndCapabilityExposeNoMCPTool(t *testing.T) {
	t.Run("missing policy", func(t *testing.T) {
		runtime := &catalogRuntime{discovered: mcpDiscovery("query_errors")}
		catalog, _ := buildDynamicCatalog(t, runtime, domain.ToolPolicySnapshot{}, mcpSource())
		for _, definition := range catalog.DefinitionsForPhase(domain.RunStateDiagnosing) {
			if strings.HasPrefix(definition.Name, "mcp_") {
				t.Fatalf("dynamic tool exposed without policy: %#v", definition)
			}
		}
		if !strings.Contains(catalog.StatusText(), "policy_unconfigured") {
			t.Fatalf("missing policy status = %q", catalog.StatusText())
		}
	})

	t.Run("undeclared source capability", func(t *testing.T) {
		runtime := &catalogRuntime{discovered: mcpDiscovery("query_errors")}
		source := mcpSource()
		source.Declared = []string{"push_ingestion"}
		catalog, _ := buildDynamicCatalog(t, runtime, mcpPolicy("query_errors"), source)
		for _, definition := range catalog.DefinitionsForPhase(domain.RunStateDiagnosing) {
			if strings.HasPrefix(definition.Name, "mcp_") || definition.Name == application.ToolSourceRefreshTools {
				t.Fatalf("tool exposed without context capability: %#v", definition)
			}
		}
	})
}

func TestDynamicCatalogRejectsCollisionAndInvalidApprovedSchema(t *testing.T) {
	tests := []struct {
		name       string
		discovered domain.DynamicToolCatalog
		wantStatus string
	}{
		{name: "duplicate server tool", discovered: mcpDiscovery("query_errors", "query_errors"), wantStatus: "invalid_response"},
		{name: "missing schema", discovered: domain.DynamicToolCatalog{SourceID: "source-1", ServerID: "fixture-server", Tools: []domain.DynamicToolDefinition{{SourceID: "source-1", ServerID: "fixture-server", Name: "query_errors"}}}, wantStatus: "invalid_response"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := &catalogRuntime{discovered: test.discovered}
			catalog, _ := buildDynamicCatalog(t, runtime, mcpPolicy("query_errors"), mcpSource())
			if !strings.Contains(catalog.StatusText(), test.wantStatus) {
				t.Fatalf("catalog status = %q, want %q", catalog.StatusText(), test.wantStatus)
			}
			for _, definition := range catalog.DefinitionsForPhase(domain.RunStateDiagnosing) {
				if strings.HasPrefix(definition.Name, "mcp_") {
					t.Fatalf("invalid MCP tool was advertised: %#v", definition)
				}
			}
			if len(runtime.calls) != 0 {
				t.Fatalf("invalid discovery unexpectedly called runtime: %#v", runtime.calls)
			}
		})
	}
}

func TestDynamicCatalogRejectsMismatchedDiscoveryIdentityAndVersion(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(domain.DynamicToolCatalog) domain.DynamicToolCatalog
		wantMessage string
	}{
		{
			name: "source identity",
			mutate: func(catalog domain.DynamicToolCatalog) domain.DynamicToolCatalog {
				catalog.SourceID = "source-elsewhere"
				return withDiscoveryHash(catalog)
			},
			wantMessage: "wrong source",
		},
		{
			name: "server identity",
			mutate: func(catalog domain.DynamicToolCatalog) domain.DynamicToolCatalog {
				catalog.Tools[0].ServerID = "server-elsewhere"
				return withDiscoveryHash(catalog)
			},
			wantMessage: "invalid tool identity",
		},
		{
			name: "catalog hash",
			mutate: func(catalog domain.DynamicToolCatalog) domain.DynamicToolCatalog {
				catalog.Hash = "invalid"
				catalog.Version = "invalid"
				return catalog
			},
			wantMessage: "invalid catalog version",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := &catalogRuntime{discovered: test.mutate(mcpDiscovery("query_errors"))}
			catalog, _ := buildDynamicCatalog(t, runtime, mcpPolicy("query_errors"), mcpSource())
			if !strings.Contains(catalog.StatusText(), "invalid_response") {
				t.Fatalf("catalog status = %q, want invalid_response", catalog.StatusText())
			}
			if strings.Contains(catalog.StatusText(), test.wantMessage) == false {
				t.Fatalf("catalog status = %q, want %q", catalog.StatusText(), test.wantMessage)
			}
			for _, definition := range catalog.DefinitionsForPhase(domain.RunStateDiagnosing) {
				if strings.HasPrefix(definition.Name, "mcp_") {
					t.Fatalf("invalid discovery was advertised: %#v", definition)
				}
			}
		})
	}
}

func TestDynamicCatalogKeepsPhaseSpecificPolicyEntries(t *testing.T) {
	runtime := &catalogRuntime{discovered: mcpDiscovery("query_errors")}
	catalog, gateway := buildDynamicCatalog(t, runtime,
		mcpPolicyFor([]domain.RunState{domain.RunStatePlanning}, "query_errors"), mcpSource())
	for _, definition := range catalog.DefinitionsForPhase(domain.RunStateDiagnosing) {
		if strings.HasPrefix(definition.Name, "mcp_") {
			t.Fatalf("planning-only tool was advertised during diagnosis: %#v", definition)
		}
	}
	result, err := gateway.ExecuteToolWithCatalog(context.Background(), domain.RunStateDiagnosing,
		domain.RepoRef{}, domain.EvidenceScope{ProjectID: "project-1", SourceID: "source-1"}, catalog,
		application.ToolSourceSearchTools, map[string]interface{}{"query": "query_errors"})
	if err != nil {
		t.Fatalf("diagnosing search error = %v", err)
	}
	if result.Payload.(map[string]interface{})["count"] != 0 {
		t.Fatalf("diagnosing search bypassed phase policy: %#v", result.Payload)
	}
	activateDynamicTool(t, gateway, catalog, domain.RunStatePlanning, "query_errors")
	planning := catalog.DefinitionsForPhase(domain.RunStatePlanning)
	found := false
	for _, definition := range planning {
		found = found || strings.HasPrefix(definition.Name, "mcp_")
	}
	if !found {
		t.Fatalf("planning-only tool was dropped from the catalog: %#v", planning)
	}
}

func TestDynamicCatalogRefreshClearsFailedDiscoveryAndReplacesRoutes(t *testing.T) {
	runtime := &catalogRuntime{discovered: mcpDiscovery("query_errors")}
	catalog, gateway := buildDynamicCatalog(t, runtime, mcpPolicy("query_errors", "query_new"), mcpSource())
	activateDynamicTool(t, gateway, catalog, domain.RunStateDiagnosing, "query_errors")
	initialVersion := catalog.Version()
	runtime.discoverErr = &domain.ToolRuntimeError{Code: "transport", Retryable: true, Message: "MCP discovery failed"}
	_, err := gateway.ExecuteToolWithCatalog(context.Background(), domain.RunStateDiagnosing,
		domain.RepoRef{}, domain.EvidenceScope{ProjectID: "project-1", SourceID: "source-1"}, catalog,
		application.ToolSourceRefreshTools, nil)
	if err == nil {
		t.Fatal("refresh error = nil")
	}
	if catalog.Version() == initialVersion {
		t.Fatal("failed refresh retained the previous catalog version")
	}
	for _, definition := range catalog.DefinitionsForPhase(domain.RunStateDiagnosing) {
		if strings.HasPrefix(definition.Name, "mcp_") {
			t.Fatalf("failed refresh retained a dynamic route: %#v", definition)
		}
	}

	runtime.discoverErr = nil
	runtime.discovered = mcpDiscovery("query_new")
	_, err = gateway.ExecuteToolWithCatalog(context.Background(), domain.RunStateDiagnosing,
		domain.RepoRef{}, domain.EvidenceScope{ProjectID: "project-1", SourceID: "source-1"}, catalog,
		application.ToolSourceRefreshTools, nil)
	if err != nil {
		t.Fatalf("successful refresh error = %v", err)
	}
	definitions := catalog.DefinitionsForPhase(domain.RunStateDiagnosing)
	for _, definition := range definitions {
		if strings.Contains(definition.Name, "query_errors") {
			t.Fatalf("old dynamic route survived refresh: %#v", definition)
		}
	}
	found := false
	for _, definition := range definitions {
		found = found || strings.Contains(definition.Name, "query_new")
	}
	if found {
		t.Fatalf("refresh retained activation for a replacement route: %#v", definitions)
	}
	activateDynamicTool(t, gateway, catalog, domain.RunStateDiagnosing, "query_new")
}

func TestDynamicCatalogRejectsWrongExecutionScope(t *testing.T) {
	runtime := &catalogRuntime{discovered: mcpDiscovery("query_errors")}
	catalog, gateway := buildDynamicCatalog(t, runtime, mcpPolicy("query_errors"), mcpSource())
	_, err := gateway.ExecuteToolWithCatalog(context.Background(), domain.RunStateDiagnosing,
		domain.RepoRef{}, domain.EvidenceScope{ProjectID: "project-elsewhere", SourceID: "source-1"}, catalog,
		"mcp_source-1_fixture-server_query_errors_ignored", map[string]interface{}{})
	if code, _ := application.RejectionCode(err); code != application.RejectUnavailable {
		t.Fatalf("wrong execution scope rejection = %s, want %s (err=%v)", code, application.RejectUnavailable, err)
	}
	if len(runtime.calls) != 0 {
		t.Fatalf("wrong scope reached runtime: %#v", runtime.calls)
	}
}

func TestDynamicCatalogBoundsSuccessfulResultBeforeModelContext(t *testing.T) {
	runtime := &catalogRuntime{
		discovered: mcpDiscovery("query_errors"),
		result: domain.DynamicToolResult{
			Payload: strings.Repeat("bounded-result-", 10000),
		},
	}
	catalog, gateway := buildDynamicCatalog(t, runtime, mcpPolicy("query_errors"), mcpSource())
	publicName := activateDynamicTool(t, gateway, catalog, domain.RunStateDiagnosing, "query_errors")
	result, err := gateway.ExecuteToolWithCatalog(context.Background(), domain.RunStateDiagnosing,
		domain.RepoRef{}, domain.EvidenceScope{ProjectID: "project-1", SourceID: "source-1"}, catalog,
		publicName, map[string]interface{}{"query": "timeout"})
	if err != nil {
		t.Fatalf("bounded dynamic call error = %v", err)
	}
	payload, ok := result.Payload.(map[string]interface{})
	if !ok || payload["truncated"] != true {
		t.Fatalf("dynamic result payload = %#v, want truncated marker", result.Payload)
	}
}

func TestDynamicCatalogValidatesArgumentsBeforeTrustedCall(t *testing.T) {
	runtime := &catalogRuntime{
		discovered: mcpDiscovery("query_errors"),
		result:     domain.DynamicToolResult{Payload: map[string]interface{}{"ok": true}, BytesRetrieved: 11},
	}
	catalog, gateway := buildDynamicCatalog(t, runtime, mcpPolicy("query_errors"), mcpSource())
	publicName := activateDynamicTool(t, gateway, catalog, domain.RunStateDiagnosing, "query_errors")
	_, err := gateway.ExecuteToolWithCatalog(context.Background(), domain.RunStateDiagnosing,
		domain.RepoRef{}, domain.EvidenceScope{ProjectID: "project-1", SourceID: "source-1"}, catalog, publicName, map[string]interface{}{})
	if code, _ := application.RejectionCode(err); code != application.RejectArguments {
		t.Fatalf("invalid dynamic arguments code = %s, want %s (err=%v)", code, application.RejectArguments, err)
	}
	if len(runtime.calls) != 0 {
		t.Fatalf("invalid arguments reached runtime: %#v", runtime.calls)
	}
	_, err = gateway.ExecuteToolWithCatalog(context.Background(), domain.RunStateDiagnosing,
		domain.RepoRef{}, domain.EvidenceScope{ProjectID: "project-1", SourceID: "source-1"}, catalog, publicName,
		map[string]interface{}{"query": "timeout"})
	if err != nil {
		t.Fatalf("valid dynamic call error = %v", err)
	}
	if len(runtime.calls) != 1 || runtime.calls[0].Name != "query_errors" || runtime.calls[0].Arguments["query"] != "timeout" {
		t.Fatalf("trusted call mapping = %#v", runtime.calls)
	}
}

func TestDynamicCatalogReturnsDetachedPhaseAndSchemaSnapshots(t *testing.T) {
	runtime := &catalogRuntime{discovered: mcpDiscovery("query_errors")}
	policy := mcpPolicy("query_errors")
	source := mcpSource()
	catalog, gateway := buildDynamicCatalog(t, runtime, policy, source)
	activateDynamicTool(t, gateway, catalog, domain.RunStateDiagnosing, "query_errors")

	first := catalog.DefinitionsForPhase(domain.RunStateDiagnosing)
	var dynamic *domain.ToolDefinition
	for index := range first {
		if strings.HasPrefix(first[index].Name, "mcp_") {
			dynamic = &first[index]
			break
		}
	}
	if dynamic == nil {
		t.Fatal("dynamic tool was not advertised")
	}
	dynamic.Parameters["additionalProperties"] = true
	properties := dynamic.Parameters["properties"].(map[string]interface{})
	properties["query"].(map[string]interface{})["type"] = "integer"
	dynamic.Name = "mutated"

	planning := catalog.DefinitionsForPhase(domain.RunStatePlanning)
	if len(planning) == 0 {
		t.Fatal("planning snapshot is empty")
	}
	diagnosing := catalog.DefinitionsForPhase(domain.RunStateDiagnosing)
	for _, definition := range diagnosing {
		if definition.Name == "mutated" || strings.HasPrefix(definition.Name, "mcp_") && definition.Parameters["additionalProperties"] != false {
			t.Fatalf("catalog was mutated through returned definition: %#v", definition)
		}
	}
	for _, definition := range diagnosing {
		if strings.HasPrefix(definition.Name, "mcp_") {
			querySchema := definition.Parameters["properties"].(map[string]interface{})["query"].(map[string]interface{})
			if querySchema["type"] != "string" {
				t.Fatalf("catalog schema was mutated through returned definition: %#v", definition.Parameters)
			}
		}
	}

	if len(first) != len(diagnosing) {
		t.Fatalf("phase lookup changed catalog contents: first=%d diagnosing=%d", len(first), len(diagnosing))
	}
}

func TestDynamicCatalogSearchUsesDeterministicBoundedSelection(t *testing.T) {
	runtime := &catalogRuntime{discovered: mcpDiscovery("query_zeta", "query_alpha", "query_beta")}
	catalog, gateway := buildDynamicCatalog(t, runtime,
		mcpPolicy("query_zeta", "query_alpha", "query_beta"), mcpSource())
	result, err := gateway.ExecuteToolWithCatalog(context.Background(), domain.RunStateDiagnosing,
		domain.RepoRef{}, domain.EvidenceScope{ProjectID: "project-1", SourceID: "source-1"}, catalog,
		application.ToolSourceSearchTools, map[string]interface{}{"query": "query", "limit": 2})
	if err != nil {
		t.Fatalf("search tools error = %v", err)
	}
	matches := result.Payload.(map[string]interface{})["matches"].([]map[string]interface{})
	if len(matches) != 2 || !strings.Contains(matches[0]["name"].(string), "query_alpha") || !strings.Contains(matches[1]["name"].(string), "query_beta") {
		t.Fatalf("deterministic matches = %#v", matches)
	}
	definitions := catalog.DefinitionsForPhase(domain.RunStateDiagnosing)
	dynamicCount := 0
	for _, definition := range definitions {
		if strings.HasPrefix(definition.Name, "mcp_") {
			dynamicCount++
			if strings.Contains(definition.Name, "query_zeta") {
				t.Fatalf("limit did not bound activation: %#v", definitions)
			}
		}
	}
	if dynamicCount != 2 {
		t.Fatalf("activated dynamic count = %d, want 2", dynamicCount)
	}
}

func TestDynamicCatalogExposesNoToolsOutsideModelOperationPhases(t *testing.T) {
	runtime := &catalogRuntime{discovered: mcpDiscovery("query_errors")}
	catalog, _ := buildDynamicCatalog(t, runtime, mcpPolicy("query_errors"), mcpSource())
	if definitions := catalog.DefinitionsForPhase(domain.RunStatePreparingContext); len(definitions) != 0 {
		t.Fatalf("preparing context definitions = %#v, want none", definitions)
	}
	if definitions := catalog.DefinitionsForPhase(domain.RunStateDiagnosisReadyForReview); len(definitions) != 0 {
		t.Fatalf("terminal definitions = %#v, want none", definitions)
	}
}

func TestCatalogAdvertisesSSHInspectInsteadOfEvidenceTools(t *testing.T) {
	gateway := application.NewToolGatewayWithDynamicRuntime(&fakeRepoPort{}, &fakeEvidencePort{}, &fakeInspectPort{}, nil, nil)
	catalog, err := gateway.BuildCatalog(context.Background(), "run-1", domain.RunStateDiagnosing,
		domain.EvidenceScope{ProjectID: "project-1", SourceID: "source-1"},
		domain.SourceCapabilitySnapshot{
			ProjectID: "project-1", SourceID: "source-1", Kind: "ssh", Enabled: true, Supported: true,
			Declared: []string{"pull_collection"}, Version: 3,
		})
	if err != nil {
		t.Fatalf("BuildCatalog() error = %v", err)
	}
	var sawInspect, sawEvidence bool
	for _, definition := range catalog.DefinitionsForPhase(domain.RunStateDiagnosing) {
		switch definition.Name {
		case application.ToolSSHInspect:
			sawInspect = true
		case application.ToolEvidenceSearch, application.ToolEvidenceContext:
			sawEvidence = true
		}
	}
	if !sawInspect || sawEvidence {
		t.Fatalf("SSH catalog inspect=%t evidence=%t definitions=%#v", sawInspect, sawEvidence, catalog.DefinitionsForPhase(domain.RunStateDiagnosing))
	}
}

func TestCatalogAdvertisesEvidenceToolsForCloudSource(t *testing.T) {
	gateway := application.NewToolGatewayWithDynamicRuntime(&fakeRepoPort{}, &fakeEvidencePort{}, nil, nil, nil)
	catalog, err := gateway.BuildCatalog(context.Background(), "run-1", domain.RunStateDiagnosing,
		domain.EvidenceScope{ProjectID: "project-1", SourceID: "source-1"},
		domain.SourceCapabilitySnapshot{
			ProjectID: "project-1", SourceID: "source-1", Kind: "cloud", Enabled: true, Supported: true,
			Declared: []string{"pull_collection"}, Version: 2,
		})
	if err != nil {
		t.Fatalf("BuildCatalog() error = %v", err)
	}
	var sawInspect, sawEvidence bool
	for _, definition := range catalog.DefinitionsForPhase(domain.RunStateDiagnosing) {
		switch definition.Name {
		case application.ToolSSHInspect:
			sawInspect = true
		case application.ToolEvidenceSearch, application.ToolEvidenceContext:
			sawEvidence = true
		}
	}
	if sawInspect || !sawEvidence {
		t.Fatalf("cloud catalog inspect=%t evidence=%t", sawInspect, sawEvidence)
	}
}

func TestDynamicCatalogRedactsUntrustedRuntimeErrorMessage(t *testing.T) {
	runtime := &catalogRuntime{
		discovered:  mcpDiscovery("query_errors"),
		discoverErr: &domain.ToolRuntimeError{Code: "transport", Retryable: true, Message: "authorization=sk-untrusted-secret"},
	}
	catalog, _ := buildDynamicCatalog(t, runtime, mcpPolicy("query_errors"), mcpSource())
	status := catalog.StatusText()
	if strings.Contains(status, "sk-untrusted-secret") || !strings.Contains(status, "connector transport failed") {
		t.Fatalf("unsafe catalog status = %q", status)
	}
}
