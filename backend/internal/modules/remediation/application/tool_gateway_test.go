package application_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"fixthe/backend/internal/modules/remediation/application"
	"fixthe/backend/internal/modules/remediation/domain"
)

// TestToolGateway_RejectsBeforeAdapterCall is the credential-isolation /
// policy-enforcement contract test: every invalid, out-of-phase, unavailable,
// oversized, or traversing tool request must be rejected before any adapter
// (repository/evidence port) is invoked.
func TestToolGateway_RejectsBeforeAdapterCall(t *testing.T) {
	cases := []struct {
		name     string
		phase    domain.RunState
		tool     string
		params   map[string]interface{}
		wantCode application.ToolRejectionCode
	}{
		{
			name:     "mutation tool is unavailable",
			phase:    domain.RunStateDiagnosing,
			tool:     "workspace.apply_patch",
			params:   map[string]interface{}{},
			wantCode: application.RejectUnavailable,
		},
		{
			name:     "unknown tool is unavailable",
			phase:    domain.RunStateDiagnosing,
			tool:     "repository.delete_file",
			params:   map[string]interface{}{},
			wantCode: application.RejectUnavailable,
		},
		{
			name:     "read tool out of phase in terminal state",
			phase:    domain.RunStateCompletedNonCode,
			tool:     application.ToolRepoReadFile,
			params:   map[string]interface{}{"path": "main.go"},
			wantCode: application.RejectOutOfPhase,
		},
		{
			name:     "relative traversal path rejected",
			phase:    domain.RunStateDiagnosing,
			tool:     application.ToolRepoReadFile,
			params:   map[string]interface{}{"path": "../../etc/passwd"},
			wantCode: application.RejectPathScope,
		},
		{
			name:     "absolute path rejected",
			phase:    domain.RunStateDiagnosing,
			tool:     application.ToolRepoReadFile,
			params:   map[string]interface{}{"path": "/etc/passwd"},
			wantCode: application.RejectPathScope,
		},
		{
			name:     "oversized read rejected before adapter",
			phase:    domain.RunStateDiagnosing,
			tool:     application.ToolRepoReadFile,
			params:   map[string]interface{}{"path": "main.go", "maxBytes": float64(1 << 30)},
			wantCode: application.RejectBudget,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeRepoPort{}
			ev := &fakeEvidencePort{}
			gw := application.NewToolGateway(repo, ev)

			_, err := gw.ExecuteTool(context.Background(), tc.phase, domain.RepoRef{}, domain.EvidenceScope{}, tc.tool, tc.params)
			if err == nil {
				t.Fatalf("expected rejection, got nil")
			}
			code, ok := application.RejectionCode(err)
			if !ok {
				t.Fatalf("expected a *ToolRejection, got %v", err)
			}
			if code != tc.wantCode {
				t.Errorf("rejection code = %s, want %s", code, tc.wantCode)
			}
			if repo.calls != 0 || ev.calls != 0 {
				t.Errorf("adapter was called before rejection: repo=%d evidence=%d", repo.calls, ev.calls)
			}
		})
	}
}

// TestToolGateway_ExecutesAdvertisedReadTools verifies happy-path routing to the
// read-only ports so the rejection tests are meaningful (routing does work).
func TestToolGateway_ExecutesAdvertisedReadTools(t *testing.T) {
	t.Run("read_file routes to repository port", func(t *testing.T) {
		repo := &fakeRepoPort{}
		gw := application.NewToolGateway(repo, &fakeEvidencePort{})
		res, err := gw.ExecuteTool(context.Background(), domain.RunStateDiagnosing, domain.RepoRef{}, domain.EvidenceScope{},
			application.ToolRepoReadFile, map[string]interface{}{"path": "main.go"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if repo.calls != 1 {
			t.Errorf("repository adapter calls = %d, want 1", repo.calls)
		}
		if res.Tool != application.ToolRepoReadFile {
			t.Errorf("result tool = %s, want %s", res.Tool, application.ToolRepoReadFile)
		}
	})

	t.Run("evidence search routes to evidence port", func(t *testing.T) {
		ev := &fakeEvidencePort{}
		gw := application.NewToolGateway(&fakeRepoPort{}, ev)
		res, err := gw.ExecuteTool(context.Background(), domain.RunStateDiagnosing, domain.RepoRef{}, domain.EvidenceScope{SourceID: "source-1"},
			application.ToolEvidenceSearch, map[string]interface{}{"level": "error"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ev.calls != 1 {
			t.Errorf("evidence adapter calls = %d, want 1", ev.calls)
		}
		if len(res.EvidenceIDs) == 0 {
			t.Errorf("expected evidence IDs in result summary")
		}
	})

	t.Run("evidence tool rejects missing source capability", func(t *testing.T) {
		ev := &fakeEvidencePort{}
		gw := application.NewToolGateway(&fakeRepoPort{}, ev)
		_, err := gw.ExecuteTool(context.Background(), domain.RunStateDiagnosing, domain.RepoRef{}, domain.EvidenceScope{},
			application.ToolEvidenceSearch, map[string]interface{}{})
		if code, _ := application.RejectionCode(err); code != application.RejectUnavailable {
			t.Fatalf("rejection code = %s, want %s", code, application.RejectUnavailable)
		}
		if ev.calls != 0 {
			t.Fatalf("evidence adapter calls = %d, want 0", ev.calls)
		}
	})
}

// TestToolGateway_AdvertisesOnlyReadTools verifies no mutation/execution tools
// are advertised in any reachable phase of this slice.
func TestToolGateway_AdvertisesOnlyReadTools(t *testing.T) {
	gw := application.NewToolGateway(&fakeRepoPort{}, &fakeEvidencePort{})
	readOnly := map[string]bool{
		application.ToolRepoListTree:    true,
		application.ToolRepoReadFile:    true,
		application.ToolRepoSearch:      true,
		application.ToolRepoHistory:     true,
		application.ToolEvidenceSearch:  true,
		application.ToolEvidenceContext: true,
		application.ToolSSHInspect:      true,
	}
	phases := []domain.RunState{
		domain.RunStatePreparingContext,
		domain.RunStateDiagnosing,
		domain.RunStateCollectingMoreContext,
		domain.RunStatePlanning,
	}
	for _, phase := range phases {
		for _, tool := range gw.AdvertisedTools(phase) {
			if !readOnly[tool] {
				t.Errorf("phase %s advertises non-read tool %q", phase, tool)
			}
		}
	}
	// Terminal state advertises nothing.
	if got := gw.AdvertisedTools(domain.RunStateCompletedNonCode); len(got) != 0 {
		t.Errorf("terminal state advertises tools: %v", got)
	}
}

func TestToolGateway_AdvertisedDefinitionsAreBoundedSchemas(t *testing.T) {
	gw := application.NewToolGateway(&fakeRepoPort{}, &fakeEvidencePort{})
	defs := gw.AdvertisedToolDefinitionsFor(domain.RunStateDiagnosing, domain.EvidenceScope{})
	if len(defs) != 4 {
		t.Fatalf("definitions without source = %d, want 4 repository tools", len(defs))
	}
	for _, definition := range defs {
		if definition.Parameters["type"] != "object" || definition.Parameters["additionalProperties"] != false {
			t.Fatalf("definition %s has unsafe schema: %#v", definition.Name, definition.Parameters)
		}
	}
	defs = gw.AdvertisedToolDefinitionsFor(domain.RunStateDiagnosing, domain.EvidenceScope{SourceID: "source-1"})
	if len(defs) != 6 {
		t.Fatalf("definitions with source = %d, want 6 repository+evidence tools", len(defs))
	}
	for _, definition := range defs {
		if definition.Name == application.ToolSSHInspect {
			t.Fatal("legacy static catalog advertised ssh.inspect")
		}
	}
}

func TestToolGatewayObserverReceivesRedactedTypedPayload(t *testing.T) {
	repo := &secretReadRepoPort{}
	observer := &toolPayloadObserver{}
	gw := application.NewToolGateway(repo, &fakeEvidencePort{})
	_, err := gw.ExecuteToolObserved(context.Background(), application.RunIdentity{}, observer, 1,
		domain.RunStateDiagnosing, domain.RepoRef{}, domain.EvidenceScope{}, application.ToolRepoReadFile,
		map[string]interface{}{"path": "config.go"})
	if err != nil {
		t.Fatalf("ExecuteToolObserved() error = %v", err)
	}
	encoded, err := json.Marshal(observer.result)
	if err != nil {
		t.Fatalf("marshal observer result: %v", err)
	}
	text := string(encoded)
	content := "password=hunter2 Authorization: Bearer sk-secretvalue"
	for _, secret := range []string{content, base64.StdEncoding.EncodeToString([]byte(content)), "hunter2", "sk-secretvalue"} {
		if strings.Contains(text, secret) {
			t.Fatalf("observer result leaked %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, "[redacted]") {
		t.Fatalf("observer result was not redacted: %s", text)
	}
}

func TestToolGatewayObserverReceivesFailureDiagnosticSeparately(t *testing.T) {
	rawErr := "read ssh log: remote command failed: command=tail -- /var/log/app.log; stderr=Permission denied"
	observer := &toolPayloadObserver{}
	gateway := application.NewToolGateway(
		&failingReadRepoPort{err: fmt.Errorf("%s", rawErr)},
		&fakeEvidencePort{},
	)
	_, err := gateway.ExecuteToolObserved(context.Background(), application.RunIdentity{}, observer, 1,
		domain.RunStateDiagnosing, domain.RepoRef{}, domain.EvidenceScope{}, application.ToolRepoReadFile,
		map[string]interface{}{"path": "main.go"})
	if err == nil {
		t.Fatal("expected adapter failure")
	}
	if observer.observation.FailureClass != "adapter" ||
		observer.observation.ErrorCode != "connector_authorization" ||
		observer.observation.Retryable ||
		!strings.Contains(observer.observation.ErrorMessage, rawErr) {
		t.Fatalf("tool failure observation = %+v", observer.observation)
	}
}

type secretReadRepoPort struct{ fakeRepoPort }

func (f *secretReadRepoPort) ReadFile(_ context.Context, ref domain.RepoRef, path string, _ domain.ReadOptions) (domain.FileContent, error) {
	f.calls++
	f.lastRef = ref
	return domain.FileContent{
		Path: path, Content: []byte("password=hunter2 Authorization: Bearer sk-secretvalue"),
	}, nil
}

type toolPayloadObserver struct {
	parameters  map[string]interface{}
	result      any
	observation application.ToolObservation
}

func (o *toolPayloadObserver) RunStarted(context.Context, application.RunStartedObservation) {}
func (o *toolPayloadObserver) StateTransitioned(context.Context, application.StateTransitionObservation) {
}
func (o *toolPayloadObserver) ContextCompleted(context.Context, application.ContextObservation) {}
func (o *toolPayloadObserver) ModelTurnCompleted(context.Context, application.ModelTurnObservation) {
}
func (o *toolPayloadObserver) ToolCompleted(_ context.Context, rec application.ToolObservation) {
	o.parameters = rec.Parameters
	o.result = rec.Result
	o.observation = rec
}
func (o *toolPayloadObserver) RunCompleted(context.Context, application.RunCompletedObservation) {}

func TestToolGateway_SSHInspectRejectsBeforeAdapter(t *testing.T) {
	inspect := &fakeInspectPort{}
	gateway := application.NewToolGatewayWithDynamicRuntime(&fakeRepoPort{}, &fakeEvidencePort{}, inspect, nil, nil)

	_, err := gateway.ExecuteTool(context.Background(), domain.RunStateDiagnosing, domain.RepoRef{},
		domain.EvidenceScope{ProjectID: "project-1", SourceID: "source-1"},
		application.ToolSSHInspect, map[string]interface{}{"command": "ls; rm -rf /"})
	if code, _ := application.RejectionCode(err); code != application.RejectArguments {
		t.Fatalf("rejection code = %v, want %s (err=%v)", code, application.RejectArguments, err)
	}
	if inspect.calls != 0 {
		t.Fatalf("inspect adapter calls = %d, want 0", inspect.calls)
	}
}

func TestToolGateway_SSHInspectUnavailableWithoutPort(t *testing.T) {
	evidence := &fakeEvidencePort{}
	gateway := application.NewToolGateway(&fakeRepoPort{}, evidence)
	_, err := gateway.ExecuteTool(context.Background(), domain.RunStateDiagnosing, domain.RepoRef{},
		domain.EvidenceScope{ProjectID: "project-1", SourceID: "source-1"},
		application.ToolSSHInspect, map[string]interface{}{"command": "ls /var/log"})
	if code, _ := application.RejectionCode(err); code != application.RejectUnavailable {
		t.Fatalf("rejection code = %v, want %s (err=%v)", code, application.RejectUnavailable, err)
	}
	if evidence.calls != 0 {
		t.Fatalf("evidence adapter calls = %d, want 0", evidence.calls)
	}
}

func TestToolGateway_SSHInspectRoutesReconstructedCommand(t *testing.T) {
	inspect := &fakeInspectPort{}
	gateway := application.NewToolGatewayWithDynamicRuntime(&fakeRepoPort{}, &fakeEvidencePort{}, inspect, nil, nil)
	result, err := gateway.ExecuteTool(context.Background(), domain.RunStateDiagnosing, domain.RepoRef{},
		domain.EvidenceScope{ProjectID: "project-1", SourceID: "source-1"},
		application.ToolSSHInspect, map[string]interface{}{"command": "ls /var/log | grep app"})
	if err != nil {
		t.Fatalf("ExecuteTool() error = %v", err)
	}
	if inspect.calls != 1 || inspect.lastCommand != `'ls' '/var/log' | 'grep' 'app'` {
		t.Fatalf("inspect command = %#v calls=%d", inspect.lastCommand, inspect.calls)
	}
	if result.Tool != application.ToolSSHInspect {
		t.Fatalf("result tool = %s", result.Tool)
	}
}

func TestToolGateway_SSHInspectAcceptsQuotedLiteralPatterns(t *testing.T) {
	inspect := &fakeInspectPort{}
	gateway := application.NewToolGatewayWithDynamicRuntime(&fakeRepoPort{}, &fakeEvidencePort{}, inspect, nil, nil)
	_, err := gateway.ExecuteTool(context.Background(), domain.RunStateDiagnosing, domain.RepoRef{},
		domain.EvidenceScope{ProjectID: "project-1", SourceID: "source-1"},
		application.ToolSSHInspect, map[string]interface{}{"command": "grep '[0-9]+' app.log"})
	if err != nil {
		t.Fatalf("quoted inspect pattern should be accepted: %v", err)
	}
	if inspect.lastCommand != `'grep' '[0-9]+' 'app.log'` {
		t.Fatalf("inspect command = %#v", inspect.lastCommand)
	}
}

func TestToolGateway_SSHInspectRejectsUnquotedGlobBeforeAdapter(t *testing.T) {
	inspect := &fakeInspectPort{}
	gateway := application.NewToolGatewayWithDynamicRuntime(&fakeRepoPort{}, &fakeEvidencePort{}, inspect, nil, nil)
	_, err := gateway.ExecuteTool(context.Background(), domain.RunStateDiagnosing, domain.RepoRef{},
		domain.EvidenceScope{ProjectID: "project-1", SourceID: "source-1"},
		application.ToolSSHInspect, map[string]interface{}{"command": "ls *.log"})
	if code, _ := application.RejectionCode(err); code != application.RejectArguments {
		t.Fatalf("rejection code = %v, want %s (err=%v)", code, application.RejectArguments, err)
	}
	if inspect.calls != 0 {
		t.Fatalf("inspect adapter calls = %d, want 0", inspect.calls)
	}
}
