package application_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"mendry/backend/internal/modules/remediation/application"
	"mendry/backend/internal/modules/remediation/domain"
)

// TestObservedSSHInspectPersistsCanonicalEvidence 覆盖 PRD R9/AC8：成功的
// ssh.inspect 先投影为 canonical payload 并持久化，ToolResult 携带证据 ID，
// observer 与模型看到的 payload 与持久化 payload 逐字节一致且不含凭据。
func TestObservedSSHInspectPersistsCanonicalEvidence(t *testing.T) {
	writer := &fakeRuntimeEvidenceWriter{}
	inspect := &fakeInspectPort{result: domain.SSHInspectResult{
		Command:        `'hostname' '-I'`,
		ExitCode:       0,
		Stdout:         "10.16.6.17 43.131.29.186\n-----BEGIN PRIVATE KEY-----\nMIIEowIBAAKCAQEA\n-----END PRIVATE KEY-----\n/tmp/mendry-sshlog-abc/id password=hunter2\nAWS_SECRET_ACCESS_KEY=aws-secret-value\n{\"github_token\":\"ghp-secret-value\"}",
		Stderr:         "bearer sk-secretvalue",
		BytesRetrieved: 321,
	}}
	observer := &toolPayloadObserver{}
	gateway := application.NewToolGatewayWithDynamicRuntime(&fakeRepoPort{}, &fakeEvidencePort{}, inspect, nil, nil)
	gateway.SetRuntimeEvidenceWriter(writer)
	scope := domain.EvidenceScope{
		ProjectID: "project-1", EnvironmentID: "environment-1", SourceID: "source-1",
		TimeRange: domain.TimeRange{
			Start: time.Date(2026, 8, 24, 7, 0, 0, 0, time.UTC),
			End:   time.Date(2026, 8, 24, 7, 20, 0, 0, time.UTC),
		},
	}
	result, err := gateway.ExecuteToolObservedWithCatalog(context.Background(), application.RunIdentity{
		RunID: "run-1", IncidentID: "incident-1",
	}, observer, 1, domain.RunStateDiagnosing, domain.RepoRef{}, scope, nil,
		application.ToolSSHInspect, map[string]interface{}{"command": "hostname -I"})
	if err != nil {
		t.Fatalf("ExecuteToolObservedWithCatalog() error = %v", err)
	}
	if inspect.calls != 1 {
		t.Fatalf("inspect calls = %d", inspect.calls)
	}
	if len(writer.evidence) != 1 {
		t.Fatalf("persisted evidence = %d, want 1", len(writer.evidence))
	}
	persisted := writer.evidence[0]
	if persisted.Provider != "ssh" || persisted.EvidenceKind != domain.EvidenceKindRuntime ||
		persisted.Classification != domain.EvidenceCorrelatedSupport || !persisted.Primary ||
		persisted.TemporalCorrelation || !persisted.OperationalCorrelation ||
		persisted.IncidentID != "incident-1" || persisted.RunID != "run-1" ||
		persisted.ProjectID != "project-1" || persisted.SourceID != "source-1" {
		t.Fatalf("persisted ownership/classification = %+v", persisted)
	}
	if len(persisted.EvidenceID) == 0 || len(persisted.ContentHash) != 64 ||
		!strings.HasPrefix(persisted.DeduplicationKey, "ssh.inspect:") ||
		persisted.DeduplicationKey != "ssh.inspect:"+persisted.ContentHash {
		t.Fatalf("persisted identity fields = %+v", persisted)
	}
	if len(result.EvidenceIDs) != 1 || result.EvidenceIDs[0] != persisted.EvidenceID {
		t.Fatalf("ToolResult evidence ids = %#v, want %s", result.EvidenceIDs, persisted.EvidenceID)
	}

	// canonical payload 与模型可见 payload 逐字节一致（R9）。
	persistedText := string(persisted.Payload)
	modelText := string(mustMarshal(t, result.Payload))
	if persistedText != modelText {
		t.Fatalf("persisted payload != model payload:\npersisted=%s\nmodel=%s", persistedText, modelText)
	}
	for _, secret := range []string{"hunter2", "sk-secretvalue", "aws-secret-value", "ghp-secret-value", "PRIVATE KEY", "mendry-sshlog"} {
		if strings.Contains(persistedText, secret) {
			t.Fatalf("canonical payload leaked %q: %s", secret, persistedText)
		}
	}
	if !strings.Contains(persistedText, "10.16.6.17 43.131.29.186") || !strings.Contains(persistedText, "[redacted]") {
		t.Fatalf("canonical payload lost host output or redaction marker: %s", persistedText)
	}
	// observer 结果同样只包含 canonical 投影（与持久化 payload 一致）。
	observerText := string(mustMarshal(t, observer.result))
	if observerText != persistedText {
		t.Fatalf("observer result != canonical payload:\nobserver=%s\npersisted=%s", observerText, persistedText)
	}
	if strings.Contains(observerText, "hunter2") {
		t.Fatalf("observer result leaked credential: %s", observerText)
	}
	// provenance 只记录逻辑身份，不含主机或连接 authority。
	var provenance map[string]interface{}
	if err := json.Unmarshal(persisted.Provenance, &provenance); err != nil {
		t.Fatalf("provenance = %v", err)
	}
	if provenance["tool"] != "ssh.inspect" || provenance["phase"] != "diagnosing" || provenance["projectionVersion"] != float64(1) {
		t.Fatalf("provenance = %#v", provenance)
	}
}

// TestObservedDockerLogsPersistsDirectFaultClassification 覆盖 docker.logs 的
// canonical projection：返回日志行时分类为 direct_fault，空结果保持
// correlated_supporting，且 temporal correlation 为 true。
func TestObservedDockerLogsPersistsDirectFaultClassification(t *testing.T) {
	port := &dockerGatewayPort{result: domain.DockerLogResult{
		Container: domain.DockerContainerIdentity{Name: "checkout-api", ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		Stdout:    "panic: nil pointer\n", Stderr: "stack\n",
		BytesRetrieved: 25, WindowLines: 386130, FilteredLines: 0,
		Query: domain.DockerLogQueryMeta{
			Since: time.Date(2026, 8, 24, 7, 0, 0, 0, time.UTC),
			Until: time.Date(2026, 8, 24, 7, 20, 0, 0, time.UTC),
			Tail:  42,
		},
	}}
	writer := &fakeRuntimeEvidenceWriter{}
	gateway := application.NewToolGatewayWithDynamicRuntime(&fakeRepoPort{}, &fakeEvidencePort{}, &fakeInspectPort{}, nil, nil)
	gateway.SetDockerEvidencePort(port)
	gateway.SetRuntimeEvidenceWriter(writer)
	scope := domain.EvidenceScope{
		ProjectID: "project-1", EnvironmentID: "environment-1", SourceID: "source-1",
		TimeRange: domain.TimeRange{
			Start: time.Date(2026, 8, 24, 7, 0, 0, 0, time.UTC),
			End:   time.Date(2026, 8, 24, 7, 20, 0, 0, time.UTC),
		},
	}
	catalog, err := gateway.BuildCatalog(context.Background(), "run-1", domain.RunStateDiagnosing, scope,
		domain.SourceCapabilitySnapshot{
			ProjectID: "project-1", SourceID: "source-1", Kind: "ssh", Enabled: true, Supported: true,
			Declared: []string{"pull_collection"}, Version: 3,
			SSHDeploymentKind: "docker", SSHContainerName: "checkout-api",
		})
	if err != nil {
		t.Fatalf("BuildCatalog() error = %v", err)
	}
	result, err := gateway.ExecuteToolObservedWithCatalog(context.Background(), application.RunIdentity{
		RunID: "run-1", IncidentID: "incident-1",
	}, &toolPayloadObserver{}, 1, domain.RunStateDiagnosing, domain.RepoRef{}, scope, catalog,
		application.ToolDockerLogs, map[string]interface{}{
			"since": "2026-08-24T07:00:00Z", "until": "2026-08-24T07:20:00Z", "tail": 42,
		})
	if err != nil {
		t.Fatalf("ExecuteToolObservedWithCatalog() error = %v", err)
	}
	if len(writer.evidence) != 1 {
		t.Fatalf("persisted evidence = %d", len(writer.evidence))
	}
	persisted := writer.evidence[0]
	if persisted.Classification != domain.EvidenceDirectFault || !persisted.TemporalCorrelation ||
		!persisted.OperationalCorrelation || !persisted.Primary || persisted.Provider != "docker" {
		t.Fatalf("persisted docker evidence = %+v", persisted)
	}
	if len(result.EvidenceIDs) != 1 || result.EvidenceIDs[0] != persisted.EvidenceID {
		t.Fatalf("docker evidence ids = %#v", result.EvidenceIDs)
	}
	modelText := string(mustMarshal(t, result.Payload))
	if !strings.Contains(modelText, `"returnedLines":2`) || !strings.Contains(modelText, `"windowLines":386130`) ||
		!strings.Contains(modelText, `"tail":42`) || !strings.Contains(modelText, "checkout-api") {
		t.Fatalf("docker canonical payload = %s", modelText)
	}
	if string(persisted.Payload) != modelText {
		t.Fatalf("docker persisted payload != model payload")
	}
}

// TestObservedDockerStderrOnlyIsDirectFault 证明容器 stderr 也是 Docker runtime
// 日志正文：Go panic 等故障常只写 stderr，不能因 stdout 为空而降级为 supporting。
func TestObservedDockerStderrOnlyIsDirectFault(t *testing.T) {
	port := &dockerGatewayPort{result: domain.DockerLogResult{
		Container: domain.DockerContainerIdentity{Name: "checkout-api", ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		Stderr:    "panic: runtime error: invalid memory address\n",
		Query: domain.DockerLogQueryMeta{
			Since: time.Date(2026, 8, 24, 7, 0, 0, 0, time.UTC),
			Until: time.Date(2026, 8, 24, 7, 20, 0, 0, time.UTC),
			Tail:  42,
		},
	}}
	writer := &fakeRuntimeEvidenceWriter{}
	gateway := application.NewToolGatewayWithDynamicRuntime(&fakeRepoPort{}, &fakeEvidencePort{}, &fakeInspectPort{}, nil, nil)
	gateway.SetDockerEvidencePort(port)
	gateway.SetRuntimeEvidenceWriter(writer)
	scope := domain.EvidenceScope{
		ProjectID: "project-1", EnvironmentID: "environment-1", SourceID: "source-1",
		TimeRange: domain.TimeRange{
			Start: time.Date(2026, 8, 24, 7, 0, 0, 0, time.UTC),
			End:   time.Date(2026, 8, 24, 7, 20, 0, 0, time.UTC),
		},
	}
	catalog, err := gateway.BuildCatalog(context.Background(), "run-1", domain.RunStateDiagnosing, scope,
		domain.SourceCapabilitySnapshot{
			ProjectID: "project-1", SourceID: "source-1", Kind: "ssh", Enabled: true, Supported: true,
			Declared: []string{"pull_collection"}, Version: 3,
			SSHDeploymentKind: "docker", SSHContainerName: "checkout-api",
		})
	if err != nil {
		t.Fatalf("BuildCatalog() error = %v", err)
	}
	result, err := gateway.ExecuteToolObservedWithCatalog(context.Background(), application.RunIdentity{
		RunID: "run-1", IncidentID: "incident-1",
	}, &toolPayloadObserver{}, 1, domain.RunStateDiagnosing, domain.RepoRef{}, scope, catalog,
		application.ToolDockerLogs, map[string]interface{}{
			"since": "2026-08-24T07:00:00Z", "until": "2026-08-24T07:20:00Z", "tail": 42,
		})
	if err != nil {
		t.Fatalf("ExecuteToolObservedWithCatalog() error = %v", err)
	}
	if len(writer.evidence) != 1 || writer.evidence[0].Classification != domain.EvidenceDirectFault {
		t.Fatalf("stderr-only Docker evidence = %#v", writer.evidence)
	}
	if modelText := string(mustMarshal(t, result.Payload)); !strings.Contains(modelText, `"returnedLines":1`) {
		t.Fatalf("stderr-only canonical payload = %s", modelText)
	}
}

// TestObservedDockerEmptyResultStaysCorrelated 覆盖空日志窗口不提升为 direct_fault。
func TestObservedDockerEmptyResultStaysCorrelated(t *testing.T) {
	port := &dockerGatewayPort{result: domain.DockerLogResult{
		Container:   domain.DockerContainerIdentity{Name: "checkout-api", ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		WindowLines: 0, FilteredLines: 0,
		Query: domain.DockerLogQueryMeta{
			Since: time.Date(2026, 8, 24, 7, 0, 0, 0, time.UTC),
			Until: time.Date(2026, 8, 24, 7, 20, 0, 0, time.UTC),
			Tail:  42,
		},
	}}
	writer := &fakeRuntimeEvidenceWriter{}
	gateway := application.NewToolGatewayWithDynamicRuntime(&fakeRepoPort{}, &fakeEvidencePort{}, &fakeInspectPort{}, nil, nil)
	gateway.SetDockerEvidencePort(port)
	gateway.SetRuntimeEvidenceWriter(writer)
	scope := domain.EvidenceScope{
		ProjectID: "project-1", EnvironmentID: "environment-1", SourceID: "source-1",
		TimeRange: domain.TimeRange{
			Start: time.Date(2026, 8, 24, 7, 0, 0, 0, time.UTC),
			End:   time.Date(2026, 8, 24, 7, 20, 0, 0, time.UTC),
		},
	}
	catalog, err := gateway.BuildCatalog(context.Background(), "run-1", domain.RunStateDiagnosing, scope,
		domain.SourceCapabilitySnapshot{
			ProjectID: "project-1", SourceID: "source-1", Kind: "ssh", Enabled: true, Supported: true,
			Declared: []string{"pull_collection"}, Version: 3,
			SSHDeploymentKind: "docker", SSHContainerName: "checkout-api",
		})
	if err != nil {
		t.Fatalf("BuildCatalog() error = %v", err)
	}
	if _, err := gateway.ExecuteToolObservedWithCatalog(context.Background(), application.RunIdentity{
		RunID: "run-1", IncidentID: "incident-1",
	}, &toolPayloadObserver{}, 1, domain.RunStateDiagnosing, domain.RepoRef{}, scope, catalog,
		application.ToolDockerLogs, map[string]interface{}{
			"since": "2026-08-24T07:00:00Z", "until": "2026-08-24T07:20:00Z", "tail": 42,
		}); err != nil {
		t.Fatalf("ExecuteToolObservedWithCatalog() error = %v", err)
	}
	if writer.evidence[0].Outcome != "empty" || writer.evidence[0].Classification != domain.EvidenceCorrelatedSupport {
		t.Fatalf("empty docker evidence = %+v", writer.evidence[0])
	}
}

// TestObservedRuntimeEvidenceIdempotentDedup 覆盖同一 run 内相同输出的重试
// 返回同一 dedup key，变化后的输出生成新的不可变记录。
func TestObservedRuntimeEvidenceIdempotentDedup(t *testing.T) {
	writer := &fakeRuntimeEvidenceWriter{}
	inspect := &fakeInspectPort{result: domain.SSHInspectResult{Command: "'hostname' '-I'", Stdout: "10.16.6.17"}}
	gateway := application.NewToolGatewayWithDynamicRuntime(&fakeRepoPort{}, &fakeEvidencePort{}, inspect, nil, nil)
	gateway.SetRuntimeEvidenceWriter(writer)
	scope := domain.EvidenceScope{ProjectID: "project-1", EnvironmentID: "environment-1", SourceID: "source-1"}
	for turn := 0; turn < 2; turn++ {
		if _, err := gateway.ExecuteToolObservedWithCatalog(context.Background(), application.RunIdentity{
			RunID: "run-1", IncidentID: "incident-1",
		}, &toolPayloadObserver{}, int64(turn+1), domain.RunStateDiagnosing, domain.RepoRef{}, scope, nil,
			application.ToolSSHInspect, map[string]interface{}{"command": "hostname -I"}); err != nil {
			t.Fatalf("turn %d error = %v", turn, err)
		}
	}
	if len(writer.evidence) != 2 || writer.evidence[0].DeduplicationKey != writer.evidence[1].DeduplicationKey {
		t.Fatalf("identical retries must share dedup key: %#v", writer.evidence)
	}
	inspect.result = domain.SSHInspectResult{Command: "'hostname' '-I'", Stdout: "changed"}
	if _, err := gateway.ExecuteToolObservedWithCatalog(context.Background(), application.RunIdentity{
		RunID: "run-1", IncidentID: "incident-1",
	}, &toolPayloadObserver{}, 3, domain.RunStateDiagnosing, domain.RepoRef{}, scope, nil,
		application.ToolSSHInspect, map[string]interface{}{"command": "hostname -I"}); err != nil {
		t.Fatalf("changed-output error = %v", err)
	}
	if writer.evidence[2].DeduplicationKey == writer.evidence[0].DeduplicationKey {
		t.Fatalf("changed output must create a new immutable record")
	}
}

// TestObservedRuntimeEvidencePersistenceFailure 覆盖 PRD AC10：持久化失败返回
// 稳定、不可重试的 runtime_evidence_persistence，observer 不收到原始输出。
func TestObservedRuntimeEvidencePersistenceFailure(t *testing.T) {
	writer := &fakeRuntimeEvidenceWriter{errs: []error{jsonError("store down")}}
	observer := &toolPayloadObserver{}
	inspect := &fakeInspectPort{result: domain.SSHInspectResult{Command: "'hostname' '-I'", Stdout: "secret-raw-output"}}
	gateway := application.NewToolGatewayWithDynamicRuntime(&fakeRepoPort{}, &fakeEvidencePort{}, inspect, nil, nil)
	gateway.SetRuntimeEvidenceWriter(writer)
	scope := domain.EvidenceScope{ProjectID: "project-1", EnvironmentID: "environment-1", SourceID: "source-1"}
	_, err := gateway.ExecuteToolObservedWithCatalog(context.Background(), application.RunIdentity{
		RunID: "run-1", IncidentID: "incident-1",
	}, observer, 1, domain.RunStateDiagnosing, domain.RepoRef{}, scope, nil,
		application.ToolSSHInspect, map[string]interface{}{"command": "hostname -I"})
	if err == nil {
		t.Fatal("expected persistence failure")
	}
	if code, ok := application.RejectionCode(err); ok {
		t.Fatalf("persistence failure must not be a policy rejection: %s", code)
	}
	if !strings.Contains(err.Error(), "runtime_evidence_persistence") {
		t.Fatalf("persistence failure code = %v", err)
	}
	if observer.result != nil {
		text := string(mustMarshal(t, observer.result))
		if strings.Contains(text, "secret-raw-output") {
			t.Fatalf("observer received raw output on persistence failure: %s", text)
		}
	}
	if writer.evidence != nil {
		t.Fatalf("evidence should not be recorded on failure")
	}
}

func TestObservedDockerCanonicalTruncationRequiresRefinement(t *testing.T) {
	bigOutput := strings.Repeat("panic frame with bounded operational detail\n", 3000)
	port := &dockerGatewayPort{result: domain.DockerLogResult{
		Container: domain.DockerContainerIdentity{Name: "checkout-api", ID: strings.Repeat("a", 64)},
		Stderr:    bigOutput, BytesRetrieved: int64(len(bigOutput)), WindowLines: -1,
		Query: domain.DockerLogQueryMeta{
			Since: time.Date(2026, 8, 24, 7, 0, 0, 0, time.UTC),
			Until: time.Date(2026, 8, 24, 7, 20, 0, 0, time.UTC), Tail: 2000,
		},
	}}
	writer := &fakeRuntimeEvidenceWriter{}
	gateway := application.NewToolGatewayWithDynamicRuntime(&fakeRepoPort{}, &fakeEvidencePort{}, &fakeInspectPort{}, nil, nil)
	gateway.SetDockerEvidencePort(port)
	gateway.SetRuntimeEvidenceWriter(writer)
	scope := domain.EvidenceScope{
		ProjectID: "project-1", EnvironmentID: "environment-1", SourceID: "source-1",
		TimeRange: domain.TimeRange{
			Start: time.Date(2026, 8, 24, 7, 0, 0, 0, time.UTC),
			End:   time.Date(2026, 8, 24, 7, 20, 0, 0, time.UTC),
		},
	}
	catalog, err := gateway.BuildCatalog(context.Background(), "run-1", domain.RunStateDiagnosing, scope,
		domain.SourceCapabilitySnapshot{
			ProjectID: "project-1", SourceID: "source-1", Kind: "ssh", Enabled: true, Supported: true,
			Declared: []string{"pull_collection"}, Version: 3,
			SSHDeploymentKind: "docker", SSHContainerName: "checkout-api",
		})
	if err != nil {
		t.Fatalf("BuildCatalog() error = %v", err)
	}
	result, err := gateway.ExecuteToolObservedWithCatalog(context.Background(), application.RunIdentity{
		RunID: "run-1", IncidentID: "incident-1",
	}, &toolPayloadObserver{}, 1, domain.RunStateDiagnosing, domain.RepoRef{}, scope, catalog,
		application.ToolDockerLogs, map[string]interface{}{
			"since": "2026-08-24T07:00:00Z", "until": "2026-08-24T07:20:00Z", "tail": 2000,
		})
	if err != nil {
		t.Fatalf("ExecuteToolObservedWithCatalog() error = %v", err)
	}
	if !result.RefinementRequired || result.RefinementReason != "byte_limit" {
		t.Fatalf("canonical refinement = %t/%q", result.RefinementRequired, result.RefinementReason)
	}
	if len(writer.evidence) != 1 || len(writer.evidence[0].Payload) > 64<<10 {
		t.Fatalf("persisted evidence count/bytes = %d/%d", len(writer.evidence), len(writer.evidence[0].Payload))
	}
	modelText := string(mustMarshal(t, result.Payload))
	for _, want := range []string{`"truncated":true`, `"coverageLimited":true`, `"refinementRequired":true`, `"coverageReason":"byte_limit"`} {
		if !strings.Contains(modelText, want) {
			t.Fatalf("canonical payload missing %q: %s", want, modelText)
		}
	}
}

// TestObservedRuntimeEvidenceFailsClosedWithoutWriter 覆盖未配置 writer 时
// observed SSH/Docker 工具 fail closed。
func TestObservedRuntimeEvidenceFailsClosedWithoutWriter(t *testing.T) {
	inspect := &fakeInspectPort{}
	gateway := application.NewToolGatewayWithDynamicRuntime(&fakeRepoPort{}, &fakeEvidencePort{}, inspect, nil, nil)
	scope := domain.EvidenceScope{ProjectID: "project-1", EnvironmentID: "environment-1", SourceID: "source-1"}
	_, err := gateway.ExecuteToolObservedWithCatalog(context.Background(), application.RunIdentity{
		RunID: "run-1", IncidentID: "incident-1",
	}, &toolPayloadObserver{}, 1, domain.RunStateDiagnosing, domain.RepoRef{}, scope, nil,
		application.ToolSSHInspect, map[string]interface{}{"command": "hostname -I"})
	if err == nil || !strings.Contains(err.Error(), "runtime_evidence_persistence") {
		t.Fatalf("expected fail-closed error, got %v", err)
	}
}

// TestObservedSSHInspectLargeOutputFitsCanonicalBound 覆盖有界大输出：adapter
// 输出接近/超过 64KiB 时，canonical payload 通过迭代缩小流文本落在 64KiB 内并
// 标记 truncated，而不是整体持久化失败（PRD R6：不得削弱既有输出上限的可用性）。
func TestObservedSSHInspectLargeOutputFitsCanonicalBound(t *testing.T) {
	writer := &fakeRuntimeEvidenceWriter{}
	bigLine := "log line with details 0123456789 0123456789 0123456789 0123456789 0123456789\n" // 74 字节/行
	bigOutput := strings.Repeat(bigLine, 2000)                                                  // ~148KiB
	inspect := &fakeInspectPort{result: domain.SSHInspectResult{
		Command: `'cat' '/var/log/app.log'`, ExitCode: 0,
		Stdout: bigOutput, Stderr: "", BytesRetrieved: int64(len(bigOutput)),
	}}
	gateway := application.NewToolGatewayWithDynamicRuntime(&fakeRepoPort{}, &fakeEvidencePort{}, inspect, nil, nil)
	gateway.SetRuntimeEvidenceWriter(writer)
	scope := domain.EvidenceScope{ProjectID: "project-1", EnvironmentID: "environment-1", SourceID: "source-1"}
	result, err := gateway.ExecuteToolObservedWithCatalog(context.Background(), application.RunIdentity{
		RunID: "run-1", IncidentID: "incident-1",
	}, &toolPayloadObserver{}, 1, domain.RunStateDiagnosing, domain.RepoRef{}, scope, nil,
		application.ToolSSHInspect, map[string]interface{}{"command": "cat /var/log/app.log"})
	if err != nil {
		t.Fatalf("ExecuteToolObservedWithCatalog() error = %v", err)
	}
	if len(writer.evidence) != 1 {
		t.Fatalf("persisted evidence = %d, want 1", len(writer.evidence))
	}
	persisted := writer.evidence[0]
	if persisted.ByteCount > 64<<10 || len(persisted.Payload) > 64<<10 {
		t.Fatalf("canonical payload exceeds 64KiB: bytes=%d", persisted.ByteCount)
	}
	modelText := string(mustMarshal(t, result.Payload))
	if string(persisted.Payload) != modelText {
		t.Fatalf("large-output persisted payload != model payload")
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(persisted.Payload, &payload); err != nil {
		t.Fatalf("payload = %v", err)
	}
	if payload["truncated"] != true {
		t.Fatalf("large output must be marked truncated")
	}
	if len(payload["stdout"].(string)) == 0 || len(payload["stdout"].(string)) > 64<<10 {
		t.Fatalf("canonical stdout is not bounded")
	}
}

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return encoded
}

type jsonError string

func (e jsonError) Error() string { return string(e) }
