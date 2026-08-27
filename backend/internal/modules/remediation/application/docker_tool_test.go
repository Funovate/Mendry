package application_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"fixthe/backend/internal/modules/remediation/application"
	"fixthe/backend/internal/modules/remediation/domain"
)

type dockerGatewayPort struct {
	queries []domain.DockerLogQuery
	result  domain.DockerLogResult
}

func (p *dockerGatewayPort) ResolveDockerContainer(context.Context, domain.EvidenceScope) (domain.DockerContainerIdentity, error) {
	return p.result.Container, nil
}

func (p *dockerGatewayPort) ReadDockerLogs(_ context.Context, _ domain.EvidenceScope, query domain.DockerLogQuery) (domain.DockerLogResult, error) {
	p.queries = append(p.queries, query)
	return p.result, nil
}

func TestDockerCatalogAdvertisesTypedLogsWithoutGenericInspect(t *testing.T) {
	port := &dockerGatewayPort{result: domain.DockerLogResult{
		Container:      domain.DockerContainerIdentity{Name: "checkout-api", ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		Stdout:         "panic: nil pointer\n",
		Stderr:         "stack\n",
		BytesRetrieved: 25,
		WindowLines:    386130,
		FilteredLines:  0,
	}}
	gateway := application.NewToolGatewayWithDynamicRuntime(&fakeRepoPort{}, &fakeEvidencePort{}, &fakeInspectPort{}, nil, nil)
	gateway.SetDockerEvidencePort(port)
	scope := domain.EvidenceScope{
		ProjectID: "project-1", SourceID: "source-1",
		TimeRange: domain.TimeRange{
			Start: time.Date(2026, 8, 24, 7, 0, 0, 0, time.UTC),
			End:   time.Date(2026, 8, 24, 7, 20, 0, 0, time.UTC),
		},
	}
	source := domain.SourceCapabilitySnapshot{
		ProjectID: "project-1", SourceID: "source-1", Kind: "ssh", Enabled: true, Supported: true,
		Declared: []string{"pull_collection"}, Version: 3,
		SSHDeploymentKind: "docker", SSHContainerName: "checkout-api",
	}
	catalog, err := gateway.BuildCatalog(context.Background(), "run-1", domain.RunStateDiagnosing, scope, source)
	if err != nil {
		t.Fatalf("BuildCatalog() error = %v", err)
	}
	definitions := catalog.DefinitionsForPhase(domain.RunStateDiagnosing)
	var dockerDefinition *domain.ToolDefinition
	for index := range definitions {
		switch definitions[index].Name {
		case application.ToolDockerLogs:
			dockerDefinition = &definitions[index]
		case application.ToolSSHInspect:
			t.Fatalf("Docker catalog advertised generic SSH inspect: %#v", definitions)
		}
	}
	if dockerDefinition == nil {
		t.Fatalf("Docker catalog omitted typed logs: %#v", definitions)
	}
	properties := dockerDefinition.Parameters["properties"].(map[string]interface{})
	if _, ok := properties["containerName"]; ok {
		t.Fatal("Docker tool accepts a containerName parameter")
	}
	if _, ok := properties["containerId"]; ok {
		t.Fatal("Docker tool accepts a containerId parameter")
	}
	if _, ok := properties["pattern"]; !ok {
		t.Fatal("Docker tool omitted pattern")
	}
	if _, ok := properties["context_after"]; !ok {
		t.Fatal("Docker tool omitted context_after")
	}
	if _, ok := properties["context_before"]; !ok {
		t.Fatal("Docker tool omitted context_before")
	}
	if properties["tail"].(map[string]interface{})["maximum"] != 2000 {
		t.Fatalf("Docker tail schema = %#v", properties["tail"])
	}

	valid := map[string]interface{}{
		"since": "2026-08-24T06:55:00Z", "until": "2026-08-24T07:25:00Z", "tail": 42,
	}
	toolResult, err := gateway.ExecuteToolWithCatalog(context.Background(), domain.RunStateDiagnosing,
		domain.RepoRef{}, scope, catalog, application.ToolDockerLogs, valid)
	if err != nil {
		t.Fatalf("ExecuteToolWithCatalog(docker.logs) error = %v", err)
	}
	if !strings.Contains(toolResult.Summary, "window_lines=386130") || !strings.Contains(toolResult.Summary, "returned_lines=1") || !strings.Contains(toolResult.Summary, "filtered=0") || !strings.Contains(toolResult.Summary, "truncated=false") {
		t.Fatalf("Docker summary = %q", toolResult.Summary)
	}
	if len(port.queries) != 1 || port.queries[0].Tail != 42 || !port.queries[0].Since.Equal(time.Date(2026, 8, 24, 6, 55, 0, 0, time.UTC)) || port.queries[0].Pattern != "" || port.queries[0].ContextBefore != 0 || port.queries[0].ContextAfter != 0 {
		t.Fatalf("Docker queries = %#v", port.queries)
	}

	for _, extra := range []string{"containerName", "containerId"} {
		params := map[string]interface{}{"since": valid["since"], "until": valid["until"], "tail": 42, extra: "attacker-selected"}
		_, err := gateway.ExecuteToolWithCatalog(context.Background(), domain.RunStateDiagnosing,
			domain.RepoRef{}, scope, catalog, application.ToolDockerLogs, params)
		if code, ok := application.RejectionCode(err); !ok || code != application.RejectArguments {
			t.Fatalf("Docker extra parameter %q rejection = %v, code=%v ok=%t", extra, err, code, ok)
		}
	}
	if len(port.queries) != 1 {
		t.Fatalf("invalid Docker requests reached adapter: %#v", port.queries)
	}
}

func TestDockerRejectsUnsafePatternsBeforeAdapter(t *testing.T) {
	gateway, port, scope, catalog := newDockerCatalogForTest(t)
	patterns := map[string]string{
		"semicolon":    "; rm",
		"ampersand":    "panic&fatal",
		"less-than":    "panic< fatal",
		"greater-than": "panic>fatal",
		"backtick":     "`panic`",
		"dollar":       "$HOME",
		"single-quote": "panic'fault",
		"double-quote": `panic"fault`,
		"backslash":    `panic\fault`,
		"newline":      "panic\nfault",
		"blank":        "   ",
		"oversized":    strings.Repeat("a", 257),
	}
	for name, pattern := range patterns {
		t.Run(name, func(t *testing.T) {
			params := dockerLogParams()
			params["pattern"] = pattern
			_, err := gateway.ExecuteToolWithCatalog(context.Background(), domain.RunStateDiagnosing,
				domain.RepoRef{}, scope, catalog, application.ToolDockerLogs, params)
			if code, ok := application.RejectionCode(err); !ok || code != application.RejectArguments {
				t.Fatalf("pattern %q rejection = %v, code=%v ok=%t", pattern, err, code, ok)
			}
		})
	}
	if len(port.queries) != 0 {
		t.Fatalf("unsafe Docker patterns reached adapter: %#v", port.queries)
	}
}

func TestDockerAcceptsPatternContextAndTailBounds(t *testing.T) {
	gateway, port, scope, catalog := newDockerCatalogForTest(t)
	params := dockerLogParams()
	params["tail"] = 2000
	params["pattern"] = "panic.*"
	params["context_after"] = 100
	params["context_before"] = 100
	if _, err := gateway.ExecuteToolWithCatalog(context.Background(), domain.RunStateDiagnosing,
		domain.RepoRef{}, scope, catalog, application.ToolDockerLogs, params); err != nil {
		t.Fatalf("maximum Docker bounds rejected: %v", err)
	}
	if len(port.queries) != 1 || port.queries[0].Tail != 2000 || port.queries[0].ContextAfter != 100 || port.queries[0].ContextBefore != 100 {
		t.Fatalf("maximum Docker query = %#v", port.queries)
	}
	cases := map[string]map[string]interface{}{
		"tail above maximum":          {"tail": 2001},
		"context after above maximum": {"context_after": 101},
		"context before negative":     {"context_before": -1},
		"context after fractional":    {"context_after": 1.5},
		"context before string":       {"context_before": "1"},
	}
	for name, override := range cases {
		t.Run(name, func(t *testing.T) {
			invalid := dockerLogParams()
			for key, value := range override {
				invalid[key] = value
			}
			_, err := gateway.ExecuteToolWithCatalog(context.Background(), domain.RunStateDiagnosing,
				domain.RepoRef{}, scope, catalog, application.ToolDockerLogs, invalid)
			if code, ok := application.RejectionCode(err); !ok || code != application.RejectArguments {
				t.Fatalf("bounds rejection = %v, code=%v ok=%t", err, code, ok)
			}
		})
	}
	if len(port.queries) != 1 {
		t.Fatalf("invalid Docker bounds reached adapter: %#v", port.queries)
	}
}

func TestDockerFilteredSummaryReportsCoverage(t *testing.T) {
	gateway, _, scope, catalog := newDockerCatalogForTest(t)
	params := dockerLogParams()
	params["pattern"] = "panic.*"
	result, err := gateway.ExecuteToolWithCatalog(context.Background(), domain.RunStateDiagnosing,
		domain.RepoRef{}, scope, catalog, application.ToolDockerLogs, params)
	if err != nil {
		t.Fatalf("filtered Docker logs = %v", err)
	}
	if !strings.Contains(result.Summary, "window_lines=386130") || !strings.Contains(result.Summary, "returned_lines=2") || !strings.Contains(result.Summary, "filtered=1") || !strings.Contains(result.Summary, "truncated=false") {
		t.Fatalf("filtered Docker summary = %q", result.Summary)
	}
}

func TestDockerObservationIncludesCoverageFields(t *testing.T) {
	conversation := application.NewAgentConversation("bootstrap")
	conversation.AppendToolResult(application.RequestTool{ToolName: application.ToolDockerLogs}, application.ToolResult{
		Tool: application.ToolDockerLogs,
		Payload: domain.DockerLogResult{
			Stdout:        "panic\nframe\n",
			WindowLines:   386130,
			FilteredLines: 1,
		},
	}, nil)
	continuation := conversation.NativeContinuation("diagnose")
	for _, want := range []string{"\"window_lines\":386130", "\"returned_lines\":2", "\"filtered\":1"} {
		if !strings.Contains(continuation, want) {
			t.Fatalf("Docker continuation missing %q: %s", want, continuation)
		}
	}
}

func newDockerCatalogForTest(t *testing.T) (*application.ToolGateway, *dockerGatewayPort, domain.EvidenceScope, *application.ToolCatalog) {
	t.Helper()
	port := &dockerGatewayPort{result: domain.DockerLogResult{
		Container: domain.DockerContainerIdentity{Name: "checkout-api", ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		Stdout:    "panic\nframe\n", WindowLines: 386130, FilteredLines: 1,
	}}
	gateway := application.NewToolGatewayWithDynamicRuntime(&fakeRepoPort{}, &fakeEvidencePort{}, &fakeInspectPort{}, nil, nil)
	gateway.SetDockerEvidencePort(port)
	scope := domain.EvidenceScope{
		ProjectID: "project-1", SourceID: "source-1",
		TimeRange: domain.TimeRange{
			Start: time.Date(2026, 8, 24, 7, 0, 0, 0, time.UTC),
			End:   time.Date(2026, 8, 24, 7, 20, 0, 0, time.UTC),
		},
	}
	catalog, err := gateway.BuildCatalog(context.Background(), "run-1", domain.RunStateDiagnosing, scope, domain.SourceCapabilitySnapshot{
		ProjectID: "project-1", SourceID: "source-1", Kind: "ssh", Enabled: true, Supported: true,
		Declared: []string{"pull_collection"}, Version: 3,
		SSHDeploymentKind: "docker", SSHContainerName: "checkout-api",
	})
	if err != nil {
		t.Fatalf("BuildCatalog() error = %v", err)
	}
	return gateway, port, scope, catalog
}

func dockerLogParams() map[string]interface{} {
	return map[string]interface{}{
		"since": "2026-08-24T06:55:00Z", "until": "2026-08-24T07:25:00Z", "tail": 42,
	}
}

func TestDockerCatalogRejectsMutationAndExecutionOperations(t *testing.T) {
	port := &dockerGatewayPort{}
	gateway := application.NewToolGatewayWithDynamicRuntime(&fakeRepoPort{}, &fakeEvidencePort{}, &fakeInspectPort{}, nil, nil)
	gateway.SetDockerEvidencePort(port)
	scope := domain.EvidenceScope{
		ProjectID: "project-1", SourceID: "source-1",
		TimeRange: domain.TimeRange{
			Start: time.Date(2026, 8, 24, 7, 0, 0, 0, time.UTC),
			End:   time.Date(2026, 8, 24, 7, 20, 0, 0, time.UTC),
		},
	}
	catalog, err := gateway.BuildCatalog(context.Background(), "run-1", domain.RunStateDiagnosing, scope, domain.SourceCapabilitySnapshot{
		ProjectID: "project-1", SourceID: "source-1", Kind: "ssh", Enabled: true, Supported: true,
		Declared: []string{"pull_collection"}, Version: 3, SSHDeploymentKind: "docker", SSHContainerName: "checkout-api",
	})
	if err != nil {
		t.Fatalf("BuildCatalog() error = %v", err)
	}
	for _, tool := range []string{
		"docker.exec", "docker.attach", "docker.copy", "docker.start", "docker.stop", "docker.restart",
		"docker.kill", "docker.remove", "docker.update", "docker.image.pull", "docker.network.connect", "docker.volume.create",
	} {
		_, err := gateway.ExecuteToolWithCatalog(context.Background(), domain.RunStateDiagnosing,
			domain.RepoRef{}, scope, catalog, tool, map[string]interface{}{})
		if code, ok := application.RejectionCode(err); !ok || code != application.RejectUnavailable {
			t.Fatalf("prohibited tool %q rejection = %v, code=%v ok=%t", tool, err, code, ok)
		}
	}
	if len(port.queries) != 0 {
		t.Fatalf("prohibited Docker operations reached adapter: %#v", port.queries)
	}
}

func TestHostCatalogKeepsInspectAndOmitsDockerLogs(t *testing.T) {
	gateway := application.NewToolGatewayWithDynamicRuntime(&fakeRepoPort{}, &fakeEvidencePort{}, &fakeInspectPort{}, nil, nil)
	catalog, err := gateway.BuildCatalog(context.Background(), "run-1", domain.RunStateDiagnosing,
		domain.EvidenceScope{ProjectID: "project-1", SourceID: "source-1"}, domain.SourceCapabilitySnapshot{
			ProjectID: "project-1", SourceID: "source-1", Kind: "ssh", Enabled: true, Supported: true,
			Declared: []string{"pull_collection"}, Version: 3, SSHDeploymentKind: "host",
		})
	if err != nil {
		t.Fatalf("BuildCatalog() error = %v", err)
	}
	var inspect, docker bool
	for _, definition := range catalog.DefinitionsForPhase(domain.RunStateDiagnosing) {
		inspect = inspect || definition.Name == application.ToolSSHInspect
		docker = docker || definition.Name == application.ToolDockerLogs
	}
	if !inspect || docker {
		t.Fatalf("host catalog inspect=%t docker=%t", inspect, docker)
	}
}
