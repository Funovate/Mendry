package application_test

import (
	"context"
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
		Container: domain.DockerContainerIdentity{Name: "checkout-api", ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		Stdout:    "panic: nil pointer\n", Stderr: "stack\n", BytesRetrieved: 25,
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

	valid := map[string]interface{}{
		"since": "2026-08-24T06:55:00Z", "until": "2026-08-24T07:25:00Z", "tail": 42,
	}
	_, err = gateway.ExecuteToolWithCatalog(context.Background(), domain.RunStateDiagnosing,
		domain.RepoRef{}, scope, catalog, application.ToolDockerLogs, valid)
	if err != nil {
		t.Fatalf("ExecuteToolWithCatalog(docker.logs) error = %v", err)
	}
	if len(port.queries) != 1 || port.queries[0].Tail != 42 || !port.queries[0].Since.Equal(time.Date(2026, 8, 24, 6, 55, 0, 0, time.UTC)) {
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
