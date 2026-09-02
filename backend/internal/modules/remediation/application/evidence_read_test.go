package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"fixthe/backend/internal/modules/remediation/application"
	"fixthe/backend/internal/modules/remediation/domain"
)

// fakeEvidenceReadPort 是 canned EvidenceReadPort，记录请求并可按需返回
// 页面或稳定错误，供 gateway 路由/拒绝测试使用。
type fakeEvidenceReadPort struct {
	calls   int
	lastReq domain.EvidenceReadRequest
	result  domain.EvidenceReadPage
	err     error
}

func (f *fakeEvidenceReadPort) ReadEvidence(_ context.Context, request domain.EvidenceReadRequest) (domain.EvidenceReadPage, error) {
	f.calls++
	f.lastReq = request
	if f.err != nil {
		return domain.EvidenceReadPage{}, f.err
	}
	return f.result, nil
}

func mustEvidenceReadPage() domain.EvidenceReadPage {
	return domain.EvidenceReadPage{
		EvidenceID:           "0190-0000-0000-7000-0000000000aa",
		Kind:                 domain.EvidenceKindProviderDetail,
		StoredClassification: domain.EvidenceDirectFault,
		Provenance: domain.EvidenceReadProvenance{
			SourceID: "0190-0000-0000-7000-0000000000bb", SourceAttempt: 1,
		},
		ContentHash: strings.Repeat("a", 64),
		Content:     `{"record":"trusted provider detail"}`,
		Truncated:   false,
		ByteCount:   34,
	}
}

// mustEvidenceReadCatalog 构造带 run 身份与 legacy source capability 的
// diagnosing catalog；evidence.read 与 evidence.search/context 在同一位置广告。
func mustEvidenceReadCatalog(t *testing.T, gateway *application.ToolGateway, runID string, phase domain.RunState) *application.ToolCatalog {
	t.Helper()
	catalog, err := gateway.BuildCatalog(context.Background(), runID, phase,
		domain.EvidenceScope{ProjectID: "project-1", SourceID: "source-1"},
		domain.SourceCapabilitySnapshot{Kind: "legacy"})
	if err != nil {
		t.Fatalf("BuildCatalog() error = %v", err)
	}
	return catalog
}

func mustEvidenceReadGateway(readPort *fakeEvidenceReadPort) *application.ToolGateway {
	gateway := application.NewToolGateway(&fakeRepoPort{}, &fakeEvidencePort{})
	if readPort != nil {
		gateway.SetEvidenceReadPort(readPort)
	}
	return gateway
}

// TestToolGateway_EvidenceReadAdvertisedAndExecutable 证明 evidence.read 在
// diagnosing 阶段与 evidence.search/context 一起广告，并且通过 run catalog
// 执行：run 身份来自 catalog，模型只提供 evidenceId/cursor。
func TestToolGateway_EvidenceReadAdvertisedAndExecutable(t *testing.T) {
	t.Run("advertised alongside evidence.search and evidence.context", func(t *testing.T) {
		gateway := mustEvidenceReadGateway(nil)
		names := gateway.AdvertisedTools(domain.RunStateDiagnosing)
		if !containsToolName(names, application.ToolEvidenceRead) {
			t.Fatalf("evidence.read not advertised in diagnosing: %v", names)
		}
		catalog := mustEvidenceReadCatalog(t, gateway, "run-1", domain.RunStateDiagnosing)
		seen := false
		for _, definition := range catalog.DefinitionsForPhase(domain.RunStateDiagnosing) {
			if definition.Name == application.ToolEvidenceRead {
				seen = true
				if definition.Parameters["type"] != "object" || definition.Parameters["additionalProperties"] != false {
					t.Fatalf("evidence.read schema is unsafe: %#v", definition.Parameters)
				}
				required, ok := definition.Parameters["required"].([]string)
				if !ok || len(required) != 1 || required[0] != "evidenceId" {
					t.Fatalf("evidence.read required = %#v, want evidenceId", definition.Parameters["required"])
				}
			}
		}
		if !seen {
			t.Fatal("evidence.read missing from the diagnosing catalog definitions")
		}
	})

	t.Run("executable through the run catalog", func(t *testing.T) {
		readPort := &fakeEvidenceReadPort{result: mustEvidenceReadPage()}
		gateway := mustEvidenceReadGateway(readPort)
		catalog := mustEvidenceReadCatalog(t, gateway, "run-1", domain.RunStateDiagnosing)

		result, err := gateway.ExecuteToolWithCatalog(context.Background(), domain.RunStateDiagnosing,
			domain.RepoRef{}, domain.EvidenceScope{ProjectID: "project-1", SourceID: "source-1"}, catalog,
			application.ToolEvidenceRead, map[string]interface{}{
				"evidenceId": "0190-0000-0000-7000-0000000000aa",
				"cursor":     "opaque-page-token",
			})
		if err != nil {
			t.Fatalf("ExecuteToolWithCatalog() error = %v", err)
		}
		if readPort.calls != 1 {
			t.Fatalf("evidence read port calls = %d, want 1", readPort.calls)
		}
		if readPort.lastReq.RunID != "run-1" || readPort.lastReq.EvidenceID != "0190-0000-0000-7000-0000000000aa" ||
			readPort.lastReq.Cursor != "opaque-page-token" {
			t.Fatalf("evidence read request = %#v, want run-1 identity and passthrough params", readPort.lastReq)
		}
		if len(result.EvidenceIDs) != 1 || result.EvidenceIDs[0] != "0190-0000-0000-7000-0000000000aa" {
			t.Fatalf("ToolResult.EvidenceIDs = %v, want the persisted evidence id", result.EvidenceIDs)
		}
		page, ok := result.Payload.(domain.EvidenceReadPage)
		if !ok {
			t.Fatalf("ToolResult payload = %#v, want EvidenceReadPage", result.Payload)
		}
		if page.StoredClassification != domain.EvidenceDirectFault || page.Provenance.SourceAttempt != 1 ||
			page.ContentHash == "" || page.Content == "" {
			t.Fatalf("evidence read page is incomplete: %#v", page)
		}
		if result.BytesRetrieved != page.ByteCount {
			t.Fatalf("bytes retrieved = %d, want %d", result.BytesRetrieved, page.ByteCount)
		}
	})

	t.Run("paged result keeps the opaque next cursor and truncated flag", func(t *testing.T) {
		page := mustEvidenceReadPage()
		page.Truncated = true
		page.NextCursor = "opaque-next-page"
		page.Content = strings.Repeat("c", domain.MaxEvidenceReadPageBytes)
		page.ByteCount = domain.MaxEvidenceReadPageBytes
		readPort := &fakeEvidenceReadPort{result: page}
		gateway := mustEvidenceReadGateway(readPort)
		catalog := mustEvidenceReadCatalog(t, gateway, "run-1", domain.RunStateDiagnosing)

		result, err := gateway.ExecuteToolWithCatalog(context.Background(), domain.RunStateDiagnosing,
			domain.RepoRef{}, domain.EvidenceScope{ProjectID: "project-1", SourceID: "source-1"}, catalog,
			application.ToolEvidenceRead, map[string]interface{}{"evidenceId": page.EvidenceID})
		if err != nil {
			t.Fatalf("ExecuteToolWithCatalog() error = %v", err)
		}
		got := result.Payload.(domain.EvidenceReadPage)
		if !got.Truncated || got.NextCursor != "opaque-next-page" {
			t.Fatalf("paged result = truncated=%t cursor=%q, want truncated with next cursor", got.Truncated, got.NextCursor)
		}
		if len(result.EvidenceIDs) != 1 || result.EvidenceIDs[0] != page.EvidenceID {
			t.Fatalf("paged ToolResult.EvidenceIDs = %v, want %s", result.EvidenceIDs, page.EvidenceID)
		}
	})

	t.Run("not advertised in planning for legacy sources", func(t *testing.T) {
		readPort := &fakeEvidenceReadPort{result: mustEvidenceReadPage()}
		gateway := mustEvidenceReadGateway(readPort)
		catalog := mustEvidenceReadCatalog(t, gateway, "run-1", domain.RunStatePlanning)
		_, err := gateway.ExecuteToolWithCatalog(context.Background(), domain.RunStatePlanning,
			domain.RepoRef{}, domain.EvidenceScope{ProjectID: "project-1", SourceID: "source-1"}, catalog,
			application.ToolEvidenceRead, map[string]interface{}{"evidenceId": "0190-0000-0000-7000-0000000000aa"})
		if code, _ := application.RejectionCode(err); code != application.RejectUnavailable {
			t.Fatalf("rejection code = %s, want %s", code, application.RejectUnavailable)
		}
		if readPort.calls != 0 {
			t.Fatalf("evidence read port calls = %d, want 0", readPort.calls)
		}
	})
}

// TestToolGateway_EvidenceReadRejectsBeforePort 证明 gateway 在端口调用前拒绝
// 无 run 身份、缺参、越界 cursor 与未知参数，并把端口返回的稳定错误映射为
// 正确的 ToolRejection 代码。
func TestToolGateway_EvidenceReadRejectsBeforePort(t *testing.T) {
	evidenceID := "0190-0000-0000-7000-0000000000aa"
	scope := domain.EvidenceScope{ProjectID: "project-1", SourceID: "source-1"}

	t.Run("unknown or out-of-series evidence maps to tool_unavailable", func(t *testing.T) {
		readPort := &fakeEvidenceReadPort{err: domain.ErrEvidenceReadNotFound}
		gateway := mustEvidenceReadGateway(readPort)
		catalog := mustEvidenceReadCatalog(t, gateway, "run-1", domain.RunStateDiagnosing)
		_, err := gateway.ExecuteToolWithCatalog(context.Background(), domain.RunStateDiagnosing,
			domain.RepoRef{}, scope, catalog, application.ToolEvidenceRead,
			map[string]interface{}{"evidenceId": evidenceID})
		if code, _ := application.RejectionCode(err); code != application.RejectUnavailable {
			t.Fatalf("rejection code = %s, want %s", code, application.RejectUnavailable)
		}
		if readPort.calls != 1 {
			t.Fatalf("evidence read port calls = %d, want 1", readPort.calls)
		}
	})

	t.Run("invalid cursor maps to invalid_arguments", func(t *testing.T) {
		readPort := &fakeEvidenceReadPort{err: domain.ErrEvidenceReadCursorInvalid}
		gateway := mustEvidenceReadGateway(readPort)
		catalog := mustEvidenceReadCatalog(t, gateway, "run-1", domain.RunStateDiagnosing)
		_, err := gateway.ExecuteToolWithCatalog(context.Background(), domain.RunStateDiagnosing,
			domain.RepoRef{}, scope, catalog, application.ToolEvidenceRead,
			map[string]interface{}{"evidenceId": evidenceID, "cursor": "tampered"})
		if code, _ := application.RejectionCode(err); code != application.RejectArguments {
			t.Fatalf("rejection code = %s, want %s", code, application.RejectArguments)
		}
	})

	t.Run("expired cursor maps to invalid_arguments", func(t *testing.T) {
		readPort := &fakeEvidenceReadPort{err: domain.ErrEvidenceReadCursorExpired}
		gateway := mustEvidenceReadGateway(readPort)
		catalog := mustEvidenceReadCatalog(t, gateway, "run-1", domain.RunStateDiagnosing)
		_, err := gateway.ExecuteToolWithCatalog(context.Background(), domain.RunStateDiagnosing,
			domain.RepoRef{}, scope, catalog, application.ToolEvidenceRead,
			map[string]interface{}{"evidenceId": evidenceID, "cursor": "expired"})
		if code, _ := application.RejectionCode(err); code != application.RejectArguments {
			t.Fatalf("rejection code = %s, want %s", code, application.RejectArguments)
		}
	})

	t.Run("missing evidenceId is rejected before the port", func(t *testing.T) {
		readPort := &fakeEvidenceReadPort{result: mustEvidenceReadPage()}
		gateway := mustEvidenceReadGateway(readPort)
		catalog := mustEvidenceReadCatalog(t, gateway, "run-1", domain.RunStateDiagnosing)
		_, err := gateway.ExecuteToolWithCatalog(context.Background(), domain.RunStateDiagnosing,
			domain.RepoRef{}, scope, catalog, application.ToolEvidenceRead, map[string]interface{}{})
		if code, _ := application.RejectionCode(err); code != application.RejectArguments {
			t.Fatalf("rejection code = %s, want %s", code, application.RejectArguments)
		}
		if readPort.calls != 0 {
			t.Fatalf("evidence read port calls = %d, want 0", readPort.calls)
		}
	})

	t.Run("oversized cursor is rejected before the port", func(t *testing.T) {
		readPort := &fakeEvidenceReadPort{result: mustEvidenceReadPage()}
		gateway := mustEvidenceReadGateway(readPort)
		catalog := mustEvidenceReadCatalog(t, gateway, "run-1", domain.RunStateDiagnosing)
		_, err := gateway.ExecuteToolWithCatalog(context.Background(), domain.RunStateDiagnosing,
			domain.RepoRef{}, scope, catalog, application.ToolEvidenceRead,
			map[string]interface{}{"evidenceId": evidenceID, "cursor": strings.Repeat("x", domain.MaxEvidenceReadCursorBytes+1)})
		if code, _ := application.RejectionCode(err); code != application.RejectArguments {
			t.Fatalf("rejection code = %s, want %s", code, application.RejectArguments)
		}
		if readPort.calls != 0 {
			t.Fatalf("evidence read port calls = %d, want 0", readPort.calls)
		}
	})

	t.Run("URL-style parameter is rejected before the port", func(t *testing.T) {
		readPort := &fakeEvidenceReadPort{result: mustEvidenceReadPage()}
		gateway := mustEvidenceReadGateway(readPort)
		catalog := mustEvidenceReadCatalog(t, gateway, "run-1", domain.RunStateDiagnosing)
		_, err := gateway.ExecuteToolWithCatalog(context.Background(), domain.RunStateDiagnosing,
			domain.RepoRef{}, scope, catalog, application.ToolEvidenceRead,
			map[string]interface{}{"evidenceId": evidenceID, "url": "https://alarm.example/raw"})
		if code, _ := application.RejectionCode(err); code != application.RejectArguments {
			t.Fatalf("rejection code = %s, want %s", code, application.RejectArguments)
		}
		if readPort.calls != 0 {
			t.Fatalf("evidence read port calls = %d, want 0", readPort.calls)
		}
	})

	t.Run("missing run identity in the catalog is rejected", func(t *testing.T) {
		readPort := &fakeEvidenceReadPort{result: mustEvidenceReadPage()}
		gateway := mustEvidenceReadGateway(readPort)
		catalog := mustEvidenceReadCatalog(t, gateway, "", domain.RunStateDiagnosing)
		_, err := gateway.ExecuteToolWithCatalog(context.Background(), domain.RunStateDiagnosing,
			domain.RepoRef{}, scope, catalog, application.ToolEvidenceRead,
			map[string]interface{}{"evidenceId": evidenceID})
		if code, _ := application.RejectionCode(err); code != application.RejectUnavailable {
			t.Fatalf("rejection code = %s, want %s", code, application.RejectUnavailable)
		}
		if readPort.calls != 0 {
			t.Fatalf("evidence read port calls = %d, want 0", readPort.calls)
		}
	})

	t.Run("legacy ExecuteTool without a catalog is rejected", func(t *testing.T) {
		readPort := &fakeEvidenceReadPort{result: mustEvidenceReadPage()}
		gateway := mustEvidenceReadGateway(readPort)
		_, err := gateway.ExecuteTool(context.Background(), domain.RunStateDiagnosing,
			domain.RepoRef{}, scope, application.ToolEvidenceRead,
			map[string]interface{}{"evidenceId": evidenceID})
		if code, _ := application.RejectionCode(err); code != application.RejectUnavailable {
			t.Fatalf("rejection code = %s, want %s", code, application.RejectUnavailable)
		}
		if readPort.calls != 0 {
			t.Fatalf("evidence read port calls = %d, want 0", readPort.calls)
		}
	})

	t.Run("missing evidence read port is rejected", func(t *testing.T) {
		gateway := mustEvidenceReadGateway(nil)
		catalog := mustEvidenceReadCatalog(t, gateway, "run-1", domain.RunStateDiagnosing)
		_, err := gateway.ExecuteToolWithCatalog(context.Background(), domain.RunStateDiagnosing,
			domain.RepoRef{}, scope, catalog, application.ToolEvidenceRead,
			map[string]interface{}{"evidenceId": evidenceID})
		if code, _ := application.RejectionCode(err); code != application.RejectUnavailable {
			t.Fatalf("rejection code = %s, want %s", code, application.RejectUnavailable)
		}
	})

	t.Run("port failures remain adapter failures", func(t *testing.T) {
		readPort := &fakeEvidenceReadPort{err: errors.New("database unavailable")}
		gateway := mustEvidenceReadGateway(readPort)
		catalog := mustEvidenceReadCatalog(t, gateway, "run-1", domain.RunStateDiagnosing)
		_, err := gateway.ExecuteToolWithCatalog(context.Background(), domain.RunStateDiagnosing,
			domain.RepoRef{}, scope, catalog, application.ToolEvidenceRead,
			map[string]interface{}{"evidenceId": evidenceID})
		if code, ok := application.RejectionCode(err); ok {
			t.Fatalf("adapter failure was misclassified as rejection %s", code)
		}
		if err == nil || !strings.Contains(err.Error(), "evidence read") {
			t.Fatalf("adapter failure error = %v, want wrapped evidence read error", err)
		}
	})
}

// TestToolGateway_EvidenceReadAdvertisedForSSHDockerSource 覆盖 implement.md
// slice 3：SSH/Docker source 的 catalog 广告 evidence.read（持久化重读，不依赖
// connector），但只广告这一个 evidence 工具（evidence.search/context 是 cloud
// 日志窗口工具，不进入 SSH 分支），并且 evidence.read 通过 catalog 路径正常执行；
// planning 阶段仍排除该工具。
func TestToolGateway_EvidenceReadAdvertisedForSSHDockerSource(t *testing.T) {
	for _, deployment := range []string{"host", "docker"} {
		t.Run(deployment, func(t *testing.T) {
			readPort := &fakeEvidenceReadPort{result: mustEvidenceReadPage()}
			gateway := mustEvidenceReadGateway(readPort)
			source := domain.SourceCapabilitySnapshot{
				ProjectID: "project-1", SourceID: "source-1", Kind: "ssh", Enabled: true, Supported: true,
				Declared: []string{"pull_collection"}, Version: 3,
				SSHDeploymentKind: deployment, SSHContainerName: "checkout-api",
			}
			catalog, err := gateway.BuildCatalog(context.Background(), "run-1", domain.RunStateDiagnosing,
				domain.EvidenceScope{ProjectID: "project-1", SourceID: "source-1"}, source)
			if err != nil {
				t.Fatalf("BuildCatalog() error = %v", err)
			}
			var sawRead, sawSearch, sawInspect, sawDocker bool
			for _, definition := range catalog.DefinitionsForPhase(domain.RunStateDiagnosing) {
				switch definition.Name {
				case application.ToolEvidenceRead:
					sawRead = true
				case application.ToolEvidenceSearch, application.ToolEvidenceContext:
					sawSearch = true
				case application.ToolSSHInspect:
					sawInspect = true
				case application.ToolDockerLogs:
					sawDocker = true
				}
			}
			// Docker deployment 同时广告 typed docker.logs 与 generic ssh.inspect
			// （tool_catalog.go baseDefinitions 的 documented 行为：inspect 覆盖
			// 主机/网络/进程证据，typed logs 覆盖 incident-window 收集）；host 只
			// 广告 inspect。evidence.search/context 均不得出现在 SSH 分支。
			if !sawRead || sawSearch || !sawInspect || sawDocker != (deployment == "docker") {
				t.Fatalf("ssh/%s catalog read=%t search=%t inspect=%t docker=%t",
					deployment, sawRead, sawSearch, sawInspect, sawDocker)
			}

			result, err := gateway.ExecuteToolWithCatalog(context.Background(), domain.RunStateDiagnosing,
				domain.RepoRef{}, domain.EvidenceScope{ProjectID: "project-1", SourceID: "source-1"}, catalog,
				application.ToolEvidenceRead, map[string]interface{}{"evidenceId": mustEvidenceReadPage().EvidenceID})
			if err != nil {
				t.Fatalf("ExecuteToolWithCatalog(evidence.read) error = %v", err)
			}
			if readPort.calls != 1 || result.Tool != application.ToolEvidenceRead ||
				readPort.lastReq.RunID != "run-1" {
				t.Fatalf("evidence.read execution = calls:%d result:%#v request:%#v",
					readPort.calls, result, readPort.lastReq)
			}
			for _, definition := range catalog.DefinitionsForPhase(domain.RunStatePlanning) {
				if definition.Name == application.ToolEvidenceRead {
					t.Fatalf("planning SSH catalog advertised evidence.read")
				}
			}
		})
	}
}
