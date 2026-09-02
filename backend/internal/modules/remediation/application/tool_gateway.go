package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"fixthe/backend/internal/modules/remediation/domain"
)

// Read tool identifiers advertised by the gateway in this slice. Built-in
// repository, evidence, and SSH inspect tools exist; any other identifier
// (including reserved mutation/execution tools) is rejected as tool_unavailable.
const (
	ToolRepoListTree     = "repository.list_tree"
	ToolRepoReadFile     = "repository.read_file"
	ToolRepoSearch       = "repository.search"
	ToolRepoHistory      = "repository.history"
	ToolEvidenceSearch   = "evidence.search"
	ToolEvidenceContext  = "evidence.context"
	ToolEvidenceRead     = "evidence.read"
	ToolSSHInspect       = "ssh.inspect"
	ToolDockerLogs       = "docker.logs"
	ToolTencentCLSDetail = "evidence.tencent_cls_detail"
)

// ToolRejectionCode is a stable machine code for a rejected tool request. The
// gateway returns one of these before any adapter call when policy fails.
type ToolRejectionCode string

const (
	// RejectUnavailable means the tool identifier is not a registered read
	// tool (unknown id or a mutation/execution tool reserved for later slices).
	RejectUnavailable ToolRejectionCode = "tool_unavailable"
	// RejectOutOfPhase means the tool is registered but not advertised for the
	// current run phase.
	RejectOutOfPhase ToolRejectionCode = "tool_out_of_phase"
	// RejectPathScope means a path argument escaped the repository scope.
	RejectPathScope ToolRejectionCode = "path_out_of_scope"
	// RejectBudget means the request exceeded a size/time budget.
	RejectBudget ToolRejectionCode = "budget_exceeded"
	// RejectArguments means a required argument was missing or malformed.
	RejectArguments ToolRejectionCode = "invalid_arguments"
)

// ToolRejection is returned when the gateway refuses a tool request before any
// adapter is invoked.
type ToolRejection struct {
	Code    ToolRejectionCode
	Tool    string
	Message string
}

func (e *ToolRejection) Error() string {
	return fmt.Sprintf("%s: %s (%s)", e.Code, e.Message, e.Tool)
}

// RejectionCode extracts the ToolRejectionCode from an error, if present.
func RejectionCode(err error) (ToolRejectionCode, bool) {
	var rej *ToolRejection
	if errors.As(err, &rej) {
		return rej.Code, true
	}
	return "", false
}

// ToolResult 是 gateway 的有界 tool 结果。Summary 供状态和持久化使用；
// Payload 保留 adapter 已经裁剪且不含凭据的 domain value，之后由
// AgentConversation 再次裁剪并作为下一轮的 model observation。
type ToolResult struct {
	Tool           string
	Summary        string
	BytesRetrieved int64
	EvidenceIDs    []string
	// ActionRef 是 resilient_v1 coordinator 为一次真实工具执行生成的有界引用。
	// 它仅用于 exhaustion proof 的 capability/action 绑定，不授予任何工具权限。
	ActionRef          string
	RefinementRequired bool
	RefinementReason   string
	Payload            any
}

// ExecuteToolObserved 包装唯一 tool 执行入口并发出有序、安全的 request/result observation。
// SSH/Docker 成功结果先投影为 canonical evidence 并持久化，再进入 observer；
// 投影或持久化失败时返回稳定错误且不暴露原始输出。
func (g *ToolGateway) ExecuteToolObserved(
	ctx context.Context,
	run RunIdentity,
	observer RunObserver,
	sequence int64,
	phase domain.RunState,
	ref domain.RepoRef,
	scope domain.EvidenceScope,
	tool string,
	params map[string]interface{},
) (ToolResult, error) {
	started := time.Now()
	result, err := g.ExecuteTool(ctx, phase, ref, scope, tool, params)
	if err == nil && isRuntimeEvidenceTool(tool) {
		canonical, evidenceID, persistErr := g.persistRuntimeEvidence(ctx, run, scope, phase, "", tool, result)
		if persistErr != nil {
			result = ToolResult{}
			err = persistErr
		} else {
			result.Payload = canonical
			result.EvidenceIDs = []string{evidenceID}
			applyCanonicalDockerCoverage(tool, canonical, &result)
		}
	}
	observation := ToolObservation{
		Run: run, Phase: phase, Sequence: sequence, Tool: tool,
		Duration: time.Since(started), Outcome: "success", Bytes: result.BytesRetrieved,
		Parameters: boundedConversationMap(params),
		Result:     boundedConversationValue(result.Payload, maxObservationBytes),
	}
	if err != nil {
		observation.Outcome = "failure"
		observation.FailureClass = "adapter"
		observation.ErrorMessage = err.Error()
		safe := classifyToolError(err)
		observation.ErrorCode = safe.Code
		observation.Retryable = safe.Retryable
		if code, ok := RejectionCode(err); ok {
			observation.Outcome = "rejected"
			observation.FailureClass = "policy"
			observation.RejectionCode = code
		}
	}
	normalizeRunObserver(observer).ToolCompleted(ctx, observation)
	return result, err
}

func applyCanonicalDockerCoverage(tool string, canonical any, result *ToolResult) {
	if tool != ToolDockerLogs || result == nil {
		return
	}
	var coverage struct {
		RefinementRequired bool   `json:"refinementRequired"`
		CoverageReason     string `json:"coverageReason"`
	}
	encoded, err := json.Marshal(canonical)
	if err != nil || json.Unmarshal(encoded, &coverage) != nil {
		return
	}
	result.RefinementRequired = coverage.RefinementRequired
	result.RefinementReason = coverage.CoverageReason
}

// ToolGateway validates and routes model tool requests to the read-only ports.
// It is the sole execution entry point for tool requests and enforces, before
// any adapter call:
//   - read-only availability (mutation/execution tools rejected tool_unavailable)
//   - phase-appropriate advertisement
//   - path scope (no traversal, no absolute paths)
//   - size/time budgets
//
// The gateway holds only domain ports; it never receives credentials or raw
// clients. Adapters inject their own credentials internally.
type ToolGateway struct {
	repoPort          domain.RepositoryReadPort
	evidencePort      domain.EvidenceLogPort
	evidenceReadPort  domain.EvidenceReadPort
	inspectPort       domain.SSHInspectPort
	dockerPort        domain.DockerEvidencePort
	tencentDetailPort domain.TencentCLSDetailPort
	dynamicRuntime    domain.DynamicToolRuntimePort
	policyResolver    domain.ToolPolicyResolver
	runtimeWriter     RuntimeEvidenceWriter

	maxReadBytes   int64
	maxTreeEntries int
	maxSearch      int
	maxLogLines    int
}

// NewToolGateway creates a gateway with conservative default budgets.
func NewToolGateway(
	repoPort domain.RepositoryReadPort,
	evidencePort domain.EvidenceLogPort,
) *ToolGateway {
	return &ToolGateway{
		repoPort:       repoPort,
		evidencePort:   evidencePort,
		maxReadBytes:   1 << 20, // 1 MiB per read_file
		maxTreeEntries: 500,
		maxSearch:      100,
		maxLogLines:    500,
	}
}

// SetEvidenceReadPort 注入同 series 持久化证据的按 ID 分页读取端口（R9）。
// 没有该端口时 evidence.read 在 gateway 边界 fail closed，不会访问数据库。
func (g *ToolGateway) SetEvidenceReadPort(port domain.EvidenceReadPort) {
	g.evidenceReadPort = port
}

// SetDockerEvidencePort enables the typed Docker logs capability for a saved
// Docker deployment without changing the legacy gateway constructor.
func (g *ToolGateway) SetDockerEvidencePort(port domain.DockerEvidencePort) {
	g.dockerPort = port
}

// SetTencentCLSDetailPort 注入 incident-bound 的 public detail reader，供 mandatory
// pre-diagnosis evidence gate 使用。
func (g *ToolGateway) SetTencentCLSDetailPort(port domain.TencentCLSDetailPort) {
	g.tencentDetailPort = port
}

// SetRuntimeEvidenceWriter 在 composition root 注入 canonical runtime evidence
// writer。没有 writer 时，observed SSH/Docker 工具 fail closed：成功输出必须先
// 持久化才能被模型引用，未持久化的原始结果不会进入模型或 observer。
func (g *ToolGateway) SetRuntimeEvidenceWriter(writer RuntimeEvidenceWriter) {
	g.runtimeWriter = writer
}

// AdvertisedTools returns the read tool identifiers available in the given
// phase. Terminal and reserved phases advertise nothing.
func (g *ToolGateway) AdvertisedTools(phase domain.RunState) []string {
	switch phase {
	case domain.RunStatePreparingContext,
		domain.RunStateDiagnosing,
		domain.RunStateCollectingMoreContext,
		domain.RunStatePlanning:
		return []string{
			ToolRepoListTree,
			ToolRepoReadFile,
			ToolRepoSearch,
			ToolRepoHistory,
			ToolEvidenceSearch,
			ToolEvidenceContext,
			ToolEvidenceRead,
			ToolSSHInspect,
		}
	default:
		return nil
	}
}

// AdvertisedToolsFor 根据已解析的 source capability 过滤 phase catalog。
// 旧的 AdvertisedTools 保留静态测试和兼容调用；实际 run 使用此方法，避免
// 在没有 source 的项目上广告一个必然失败的 evidence tool。
func (g *ToolGateway) AdvertisedToolsFor(phase domain.RunState, scope domain.EvidenceScope) []string {
	names := g.AdvertisedTools(phase)
	filtered := make([]string, 0, len(names))
	for _, name := range names {
		switch {
		case name == ToolSSHInspect:
			// 旧静态 catalog 没有 source kind，不能把 SSH inspect 广告成可执行。
			continue
		case strings.HasPrefix(name, "evidence.") && strings.TrimSpace(scope.SourceID) == "":
			continue
		default:
			filtered = append(filtered, name)
		}
	}
	return filtered
}

// AdvertisedToolDefinitions returns the tool schemas handed to the model for
// the given phase. No credentials or adapter details are exposed.
func (g *ToolGateway) AdvertisedToolDefinitions(phase domain.RunState) []domain.ToolDefinition {
	return g.advertisedToolDefinitions(g.AdvertisedTools(phase))
}

// AdvertisedToolDefinitionsFor 返回当前 source capability 可执行的 schemas。
func (g *ToolGateway) AdvertisedToolDefinitionsFor(phase domain.RunState, scope domain.EvidenceScope) []domain.ToolDefinition {
	return g.advertisedToolDefinitions(g.AdvertisedToolsFor(phase, scope))
}

func (g *ToolGateway) advertisedToolDefinitions(names []string) []domain.ToolDefinition {
	defs := make([]domain.ToolDefinition, 0, len(names))
	for _, name := range names {
		defs = append(defs, domain.ToolDefinition{
			Name:        name,
			Description: toolDescriptions[name],
			Parameters:  toolParameterSchema(name),
		})
	}
	return defs
}

func toolParameterSchema(tool string) map[string]interface{} {
	object := func(properties map[string]interface{}, required ...string) map[string]interface{} {
		if properties == nil {
			properties = map[string]interface{}{}
		}
		schema := map[string]interface{}{
			"type":                 "object",
			"properties":           properties,
			"additionalProperties": false,
		}
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema
	}
	stringProperty := func(description string) map[string]interface{} {
		return map[string]interface{}{"type": "string", "description": description}
	}
	switch tool {
	case ToolRepoListTree:
		return object(map[string]interface{}{
			"path": stringProperty("Optional repository-relative directory."),
		})
	case ToolRepoReadFile:
		return object(map[string]interface{}{
			"path":     stringProperty("Repository-relative file path."),
			"maxBytes": map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 1 << 20},
		}, "path")
	case ToolRepoSearch:
		return object(map[string]interface{}{
			"query":    stringProperty("Literal or adapter-supported search pattern."),
			"pathGlob": stringProperty("Optional repository-relative path glob."),
		}, "query")
	case ToolRepoHistory:
		return object(map[string]interface{}{
			"path": stringProperty("Optional repository-relative path."),
		})
	case ToolEvidenceSearch:
		return object(map[string]interface{}{
			"keywords": map[string]interface{}{
				"type":  "array",
				"items": map[string]interface{}{"type": "string"},
			},
			"level": stringProperty("Optional log level filter."),
		})
	case ToolEvidenceContext:
		return object(map[string]interface{}{
			"evidenceId": stringProperty("Evidence identifier returned by evidence.search."),
		}, "evidenceId")
	case ToolEvidenceRead:
		return object(map[string]interface{}{
			"evidenceId": stringProperty("Evidence identifier of persisted trusted evidence in this remediation series."),
			"cursor":     stringProperty("Optional opaque cursor returned by a previous evidence.read page."),
		}, "evidenceId")
	case ToolSSHInspect:
		return object(map[string]interface{}{
			"command": stringProperty("Read-only inspect command to parse and execute. List the hinted logPath directory and discover actual file names before reading; the harness never auto-tails logPath. Successful results are persisted as citable evidence."),
		}, "command")
	case ToolDockerLogs:
		return object(map[string]interface{}{
			"since":          stringProperty("RFC3339 start of the bounded incident window."),
			"until":          stringProperty("RFC3339 end of the bounded incident window."),
			"tail":           map[string]interface{}{"type": "integer", "minimum": 1, "maximum": maxDockerLogLines},
			"pattern":        map[string]interface{}{"type": "string", "description": "Optional bounded regular expression matched remotely before the tail limit.", "minLength": 1, "maxLength": maxDockerPatternBytes},
			"context_after":  map[string]interface{}{"type": "integer", "description": "Lines to include after each matching line.", "minimum": 0, "maximum": 100},
			"context_before": map[string]interface{}{"type": "integer", "description": "Lines to include before each matching line.", "minimum": 0, "maximum": 100},
		}, "since", "until", "tail")
	case ToolTencentCLSDetail:
		return object(nil)
	case ToolSourceSearchTools:
		return object(map[string]interface{}{
			"query": map[string]interface{}{
				"type": "string", "description": "Tool capability to find.",
				"minLength": 1, "maxLength": maxToolSearchQuery,
			},
			"limit": map[string]interface{}{
				"type": "integer", "minimum": 1, "maximum": maxToolSearchLimit,
			},
		}, "query")
	default:
		return object(nil)
	}
}

var toolDescriptions = map[string]string{
	ToolRepoListTree:       "List repository entries at the current production branch tip.",
	ToolRepoReadFile:       "Read a bounded file at the current production branch tip.",
	ToolRepoSearch:         "Search repository content at the current production branch tip.",
	ToolRepoHistory:        "Read bounded commit history from the current production branch for a path.",
	ToolEvidenceSearch:     "Search bounded, redacted evidence/log windows.",
	ToolEvidenceContext:    "Read bounded context around an evidence anchor.",
	ToolEvidenceRead:       "Re-read a bounded page of persisted trusted evidence by evidence ID within this remediation series. Use the returned opaque cursor to page remaining content.",
	ToolSSHInspect:         "Inspect the SSH host with a read-only command (host identity, network, process, file, system log, and read-only Docker forms). List the hinted logPath directory first and discover actual file names before reading; the harness never auto-tails logPath. Successful results are persisted as citable evidence.",
	ToolDockerLogs:         "Read bounded stdout and stderr from the configured Docker container. The container identity comes from saved project configuration.",
	ToolTencentCLSDetail:   "Required for Tencent CLS webhooks: fetch the current incident's validated public detail record. This tool takes no URL and must succeed before diagnosis or stop.",
	ToolSourceSearchTools:  "Find and activate approved MCP tools for the current remediation phase.",
	ToolSourceRefreshTools: "Refresh the approved MCP tool catalog for this source.",
}

// isRegistered reports whether the identifier is one of the read tools that
// exist at all in this slice.
func (g *ToolGateway) isRegistered(tool string) bool {
	switch tool {
	case ToolRepoListTree, ToolRepoReadFile, ToolRepoSearch, ToolRepoHistory,
		ToolEvidenceSearch, ToolEvidenceContext, ToolEvidenceRead, ToolSSHInspect, ToolDockerLogs, ToolTencentCLSDetail:
		return true
	default:
		return false
	}
}

// isAdvertised reports whether the tool is advertised for the phase.
func (g *ToolGateway) isAdvertised(tool string, phase domain.RunState) bool {
	for _, t := range g.AdvertisedTools(phase) {
		if t == tool {
			return true
		}
	}
	return false
}

// ExecuteTool validates policy and, only if every check passes, routes the
// request to the appropriate read-only port. All rejections return a
// *ToolRejection and are guaranteed to happen before any adapter call.
func (g *ToolGateway) ExecuteTool(
	ctx context.Context,
	phase domain.RunState,
	ref domain.RepoRef,
	scope domain.EvidenceScope,
	tool string,
	params map[string]interface{},
) (ToolResult, error) {
	// 1. Availability: unknown ids and reserved mutation/execution tools are
	// rejected as tool_unavailable before anything else.
	if !g.isRegistered(tool) {
		return ToolResult{}, &ToolRejection{Code: RejectUnavailable, Tool: tool, Message: "tool is not a registered read tool"}
	}

	// 2. Phase: the tool must be advertised for the current phase.
	if !g.isAdvertised(tool, phase) {
		return ToolResult{}, &ToolRejection{Code: RejectOutOfPhase, Tool: tool, Message: fmt.Sprintf("tool not available in phase %s", phase)}
	}
	if strings.HasPrefix(tool, "evidence.") && strings.TrimSpace(scope.SourceID) == "" {
		return ToolResult{}, &ToolRejection{Code: RejectUnavailable, Tool: tool, Message: "evidence source is unavailable"}
	}
	if tool == ToolSSHInspect {
		if strings.TrimSpace(scope.SourceID) == "" {
			return ToolResult{}, &ToolRejection{Code: RejectUnavailable, Tool: tool, Message: "SSH inspect source is unavailable"}
		}
		if g.inspectPort == nil {
			return ToolResult{}, &ToolRejection{Code: RejectUnavailable, Tool: tool, Message: "SSH inspect port is unavailable"}
		}
	}
	if err := validateToolParameters(tool, params); err != nil {
		return ToolResult{}, &ToolRejection{Code: RejectArguments, Tool: tool, Message: err.Error()}
	}

	// 3. Path scope: any path-bearing argument must stay within the repo.
	if raw, ok := params["path"]; ok {
		path, isStr := raw.(string)
		if !isStr {
			return ToolResult{}, &ToolRejection{Code: RejectArguments, Tool: tool, Message: "path must be a string"}
		}
		if err := validateRepoPath(path); err != nil {
			return ToolResult{}, &ToolRejection{Code: RejectPathScope, Tool: tool, Message: err.Error()}
		}
	}

	// 4. Route to the read-only port with bounded options.
	switch tool {
	case ToolRepoListTree:
		return g.execRepoListTree(ctx, ref, params)
	case ToolRepoReadFile:
		return g.execRepoReadFile(ctx, ref, params)
	case ToolRepoSearch:
		return g.execRepoSearch(ctx, ref, params)
	case ToolRepoHistory:
		return g.execRepoHistory(ctx, ref, params)
	case ToolEvidenceSearch:
		return g.execEvidenceSearch(ctx, scope, params)
	case ToolEvidenceContext:
		return g.execEvidenceContext(ctx, scope, params)
	case ToolEvidenceRead:
		// 无 catalog 的执行路径没有 run 身份，无法执行 series 归属校验；
		// fail closed，避免把不受 run 约束的证据 ID 交给端口。
		return ToolResult{}, &ToolRejection{Code: RejectUnavailable, Tool: tool, Message: "evidence.read requires the run catalog identity"}
	case ToolSSHInspect:
		return g.execSSHInspect(ctx, scope, params)
	default:
		// Unreachable: isRegistered already gated the identifier.
		return ToolResult{}, &ToolRejection{Code: RejectUnavailable, Tool: tool, Message: "unhandled tool"}
	}
}

func validateToolParameters(tool string, params map[string]interface{}) error {
	if params == nil {
		return fmt.Errorf("parameters must be an object")
	}
	allowed := map[string]bool{}
	required := map[string]bool{}
	for key := range toolParameterSchema(tool)["properties"].(map[string]interface{}) {
		allowed[key] = true
	}
	if raw, ok := toolParameterSchema(tool)["required"].([]string); ok {
		for _, key := range raw {
			required[key] = true
		}
	}
	for key := range params {
		if !allowed[key] {
			return fmt.Errorf("unknown parameter %q", key)
		}
	}
	for key := range required {
		if _, ok := params[key]; !ok {
			return fmt.Errorf("%s is required", key)
		}
	}
	for key, value := range params {
		switch key {
		case "path", "query", "pathGlob", "level", "evidenceId", "command", "since", "until", "pattern":
			if _, ok := value.(string); !ok {
				return fmt.Errorf("%s must be a string", key)
			}
		case "maxBytes", "tail":
			n, ok := numericArg(value)
			if !ok || !isIntegerArgument(value) || n < 1 {
				return fmt.Errorf("%s must be a positive integer", key)
			}
		case "context_after", "context_before":
			if !isIntegerArgument(value) {
				return fmt.Errorf("%s must be an integer", key)
			}
		case "cursor":
			if _, ok := value.(string); !ok {
				return fmt.Errorf("cursor must be a string")
			}
			if len(value.(string)) > domain.MaxEvidenceReadCursorBytes {
				return fmt.Errorf("cursor exceeds the %d byte bound", domain.MaxEvidenceReadCursorBytes)
			}
		case "keywords":
			switch items := value.(type) {
			case []interface{}:
				for _, item := range items {
					if _, ok := item.(string); !ok {
						return fmt.Errorf("keywords must contain only strings")
					}
				}
			case []string:
			default:
				return fmt.Errorf("keywords must be an array")
			}
		}
	}
	return nil
}

// validateRepoPath rejects absolute paths and traversal outside the repo root.
func validateRepoPath(path string) error {
	if path == "" {
		return nil
	}
	if strings.HasPrefix(path, "/") {
		return fmt.Errorf("absolute paths are not allowed")
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == ".." {
			return fmt.Errorf("path traversal is not allowed")
		}
	}
	return nil
}

func (g *ToolGateway) execRepoListTree(ctx context.Context, ref domain.RepoRef, params map[string]interface{}) (ToolResult, error) {
	path, _ := params["path"].(string)
	listing, err := g.repoPort.ListTree(ctx, ref, path, domain.TreeOptions{MaxDepth: 2, MaxEntries: g.maxTreeEntries})
	if err != nil {
		return ToolResult{}, fmt.Errorf("list_tree: %w", err)
	}
	return ToolResult{
		Tool:           ToolRepoListTree,
		Summary:        fmt.Sprintf("%d entries (truncated=%t)", len(listing.Entries), listing.Truncated),
		BytesRetrieved: repositoryListingBytes(listing),
		Payload:        listing,
	}, nil
}

func (g *ToolGateway) execRepoReadFile(ctx context.Context, ref domain.RepoRef, params map[string]interface{}) (ToolResult, error) {
	path, ok := params["path"].(string)
	if !ok || path == "" {
		return ToolResult{}, &ToolRejection{Code: RejectArguments, Tool: ToolRepoReadFile, Message: "path is required"}
	}
	// Enforce the read budget before calling the adapter.
	maxBytes := g.maxReadBytes
	if req, ok := numericArg(params["maxBytes"]); ok {
		if req > g.maxReadBytes {
			return ToolResult{}, &ToolRejection{Code: RejectBudget, Tool: ToolRepoReadFile, Message: fmt.Sprintf("maxBytes %d exceeds limit %d", req, g.maxReadBytes)}
		}
		maxBytes = req
	}
	content, err := g.repoPort.ReadFile(ctx, ref, path, domain.ReadOptions{MaxBytes: maxBytes})
	if err != nil {
		return ToolResult{}, fmt.Errorf("read_file: %w", err)
	}
	return ToolResult{
		Tool:           ToolRepoReadFile,
		Summary:        fmt.Sprintf("%s (%d bytes, truncated=%t)", content.Path, len(content.Content), content.Truncated),
		BytesRetrieved: int64(len(content.Content)),
		Payload:        content,
	}, nil
}

func (g *ToolGateway) execRepoSearch(ctx context.Context, ref domain.RepoRef, params map[string]interface{}) (ToolResult, error) {
	pattern, ok := params["query"].(string)
	if !ok || pattern == "" {
		return ToolResult{}, &ToolRejection{Code: RejectArguments, Tool: ToolRepoSearch, Message: "query is required"}
	}
	glob, _ := params["pathGlob"].(string)
	res, err := g.repoPort.Search(ctx, ref, domain.SearchQuery{Pattern: pattern, PathGlob: glob, MaxResults: g.maxSearch})
	if err != nil {
		return ToolResult{}, fmt.Errorf("search: %w", err)
	}
	return ToolResult{
		Tool:           ToolRepoSearch,
		Summary:        fmt.Sprintf("%d matches (truncated=%t)", len(res.Matches), res.Truncated),
		BytesRetrieved: repositorySearchBytes(res),
		Payload:        res,
	}, nil
}

func (g *ToolGateway) execRepoHistory(ctx context.Context, ref domain.RepoRef, params map[string]interface{}) (ToolResult, error) {
	path, _ := params["path"].(string)
	hist, err := g.repoPort.History(ctx, ref, path, domain.HistoryOptions{MaxCommits: 50})
	if err != nil {
		return ToolResult{}, fmt.Errorf("history: %w", err)
	}
	return ToolResult{
		Tool:           ToolRepoHistory,
		Summary:        fmt.Sprintf("%d commits (truncated=%t)", len(hist.Commits), hist.Truncated),
		BytesRetrieved: repositoryHistoryBytes(hist),
		Payload:        hist,
	}, nil
}

func (g *ToolGateway) execEvidenceSearch(ctx context.Context, scope domain.EvidenceScope, params map[string]interface{}) (ToolResult, error) {
	var keywords []string
	if kw, ok := params["keywords"].([]interface{}); ok {
		for _, k := range kw {
			if s, ok := k.(string); ok {
				keywords = append(keywords, s)
			}
		}
	}
	level, _ := params["level"].(string)
	page, err := g.evidencePort.Search(ctx, scope, domain.LogQuery{Keywords: keywords, Level: level, MaxLines: g.maxLogLines, MaxBytes: g.maxReadBytes})
	if err != nil {
		return ToolResult{}, fmt.Errorf("evidence search: %w", err)
	}
	return ToolResult{
		Tool:           ToolEvidenceSearch,
		Summary:        fmt.Sprintf("%d evidence lines (truncated=%t)", len(page.Lines), page.Truncated),
		BytesRetrieved: evidencePageBytes(page),
		EvidenceIDs:    evidenceIDs(page),
		Payload:        page,
	}, nil
}

func (g *ToolGateway) execSSHInspect(ctx context.Context, scope domain.EvidenceScope, params map[string]interface{}) (ToolResult, error) {
	command, ok := params["command"].(string)
	if !ok || strings.TrimSpace(command) == "" {
		return ToolResult{}, &ToolRejection{Code: RejectArguments, Tool: ToolSSHInspect, Message: "command is required"}
	}
	parsed, err := parseInspectCommand(command)
	if err != nil {
		return ToolResult{}, &ToolRejection{Code: RejectArguments, Tool: ToolSSHInspect, Message: err.Error()}
	}
	if g.inspectPort == nil {
		return ToolResult{}, &ToolRejection{Code: RejectUnavailable, Tool: ToolSSHInspect, Message: "SSH inspect port is unavailable"}
	}
	result, err := g.inspectPort.Inspect(ctx, scope, domain.SSHInspectRequest{Command: parsed.Command})
	if err != nil {
		return ToolResult{}, fmt.Errorf("ssh inspect: %w", err)
	}
	return ToolResult{
		Tool:           ToolSSHInspect,
		Summary:        fmt.Sprintf("ssh inspect exit=%d truncated=%t bytes=%d", result.ExitCode, result.Truncated, result.BytesRetrieved),
		BytesRetrieved: result.BytesRetrieved,
		Payload:        result,
	}, nil
}

func (g *ToolGateway) execEvidenceContext(ctx context.Context, scope domain.EvidenceScope, params map[string]interface{}) (ToolResult, error) {
	evidenceID, ok := params["evidenceId"].(string)
	if !ok || evidenceID == "" {
		return ToolResult{}, &ToolRejection{Code: RejectArguments, Tool: ToolEvidenceContext, Message: "evidenceId is required"}
	}
	page, err := g.evidencePort.GetContext(ctx, scope, domain.EvidenceAnchor{EvidenceID: evidenceID, LinesBefore: 20, LinesAfter: 20})
	if err != nil {
		return ToolResult{}, fmt.Errorf("evidence context: %w", err)
	}
	return ToolResult{
		Tool:           ToolEvidenceContext,
		Summary:        fmt.Sprintf("%d evidence lines around %s", len(page.Lines), evidenceID),
		BytesRetrieved: evidencePageBytes(page),
		EvidenceIDs:    evidenceIDs(page),
		Payload:        page,
	}, nil
}

// execEvidenceRead 按 run 身份 + evidence ID 调用 EvidenceReadPort，并映射
// domain 错误到稳定的 ToolRejection 代码：目标证据不可见（未知/跨 series/
// 未来 attempt）是 tool_unavailable，无效/过期 cursor 是 invalid_arguments。
// 返回的页面携带证据 ID，供后续 checkpoint evidence index 引用。
func (g *ToolGateway) execEvidenceRead(ctx context.Context, runID string, scope domain.EvidenceScope, params map[string]interface{}) (ToolResult, error) {
	evidenceID, ok := params["evidenceId"].(string)
	if !ok || strings.TrimSpace(evidenceID) == "" {
		return ToolResult{}, &ToolRejection{Code: RejectArguments, Tool: ToolEvidenceRead, Message: "evidenceId is required"}
	}
	cursor, _ := params["cursor"].(string)
	if g.evidenceReadPort == nil {
		return ToolResult{}, &ToolRejection{Code: RejectUnavailable, Tool: ToolEvidenceRead, Message: "evidence read port is unavailable"}
	}
	page, err := g.evidenceReadPort.ReadEvidence(ctx, domain.EvidenceReadRequest{
		RunID:      runID,
		EvidenceID: evidenceID,
		Cursor:     cursor,
	})
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrEvidenceReadNotFound):
			return ToolResult{}, &ToolRejection{Code: RejectUnavailable, Tool: ToolEvidenceRead, Message: "evidence is not available in this run series"}
		case errors.Is(err, domain.ErrEvidenceReadCursorInvalid), errors.Is(err, domain.ErrEvidenceReadCursorExpired):
			return ToolResult{}, &ToolRejection{Code: RejectArguments, Tool: ToolEvidenceRead, Message: "cursor is invalid or expired; request the first page again"}
		default:
			return ToolResult{}, fmt.Errorf("evidence read: %w", err)
		}
	}
	return ToolResult{
		Tool:           ToolEvidenceRead,
		Summary:        fmt.Sprintf("%s (%d bytes, truncated=%t)", page.EvidenceID, page.ByteCount, page.Truncated),
		BytesRetrieved: page.ByteCount,
		EvidenceIDs:    []string{page.EvidenceID},
		Payload:        page,
	}, nil
}

func evidenceIDs(page domain.EvidencePage) []string {
	ids := make([]string, 0, len(page.Lines))
	for _, l := range page.Lines {
		ids = append(ids, l.EvidenceID)
	}
	return ids
}

// numericArg coerces a JSON-decoded numeric argument to int64.
func numericArg(v interface{}) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int:
		return int64(n), true
	case int64:
		return n, true
	default:
		return 0, false
	}
}
