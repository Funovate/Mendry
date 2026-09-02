package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"fixthe/backend/internal/modules/remediation/domain"
)

const (
	// ToolSourceSearchTools and ToolSourceRefreshTools are the only control
	// actions exposed by the dynamic catalog. Neither accepts a server name or
	// transport from the model.
	ToolSourceSearchTools  = "source.search_tools"
	ToolSourceRefreshTools = "source.refresh_tools"

	maxDynamicSchemaBytes     = 64 << 10
	maxDynamicSchemaDepth     = 8
	maxDynamicDescription     = 4096
	maxDynamicResultBytes     = 64 << 10
	maxDynamicTools           = 128
	maxTencentDetailAttempts  = 2
	priorTencentDetailFailure = "prior_detail_failure"
	dynamicDiscoveryLimit     = 5 * time.Second
	defaultToolSearchLimit    = 5
	maxToolSearchLimit        = 10
	maxToolSearchQuery        = 256
	maxToolSearchDescription  = 256
)

// ToolCatalog 保存每个 run、phase 的 tool snapshot；route map 保持私有，避免 model
// 构造任意 original MCP tool name。
type ToolCatalog struct {
	phase                  domain.RunState
	source                 domain.SourceCapabilitySnapshot
	policy                 domain.ToolPolicySnapshot
	scope                  domain.DynamicToolScope
	incidentID             string
	tencentDetailRequired  bool
	tencentDetailReady     bool
	tencentDetailAttempts  int
	tencentDetailRetryable bool
	tencentDetailFailure   string
	runtime                domain.DynamicToolRuntimePort
	base                   []domain.ToolDefinition
	routes                 map[string]dynamicToolRoute
	activated              map[string]struct{}
	definitions            []domain.ToolDefinition
	discoveryVersion       string
	discoveryHash          string
	discoveryTruncated     bool
	status                 catalogStatus
}

type dynamicToolRoute struct {
	definition domain.ToolDefinition
	call       domain.DynamicToolCall
	phases     map[domain.RunState]struct{}
}

type catalogStatus struct {
	Code      string
	Retryable bool
	Message   string
}

func legacySourceCapability(scope domain.EvidenceScope) domain.SourceCapabilitySnapshot {
	return domain.SourceCapabilitySnapshot{
		ProjectID: scope.ProjectID,
		SourceID:  scope.SourceID,
		Kind:      "legacy",
		Enabled:   scope.SourceID != "",
		Supported: true,
		Declared:  []string{"pull_collection", "context_collection"},
	}
}

// NewToolGatewayWithDynamicRuntime 注入受信任的 MCP runtime 与 policy resolver，
// 同时保持 legacy constructor 对既有 fake 的兼容。
func NewToolGatewayWithDynamicRuntime(
	repoPort domain.RepositoryReadPort,
	evidencePort domain.EvidenceLogPort,
	inspectPort domain.SSHInspectPort,
	runtime domain.DynamicToolRuntimePort,
	policy domain.ToolPolicyResolver,
) *ToolGateway {
	gateway := NewToolGateway(repoPort, evidencePort)
	gateway.inspectPort = inspectPort
	gateway.dynamicRuntime = runtime
	gateway.policyResolver = policy
	return gateway
}

// BuildCatalog 读取 durable policy snapshot，并执行一次有界、best-effort 的 MCP
// discovery probe；discovery 失败只保留安全 capability 状态，不阻断首次诊断。
func (g *ToolGateway) BuildCatalog(
	ctx context.Context,
	runID string,
	phase domain.RunState,
	scope domain.EvidenceScope,
	source domain.SourceCapabilitySnapshot,
) (*ToolCatalog, error) {
	if source.Kind == "" {
		// Legacy callers do not have a source metadata loader. Preserve their
		// existing static repository/evidence catalog while new production
		// wiring supplies an explicit kind and capability declaration.
		source.Kind = "legacy"
		source.Enabled = true
		source.Supported = true
	}
	source.Kind = strings.TrimSpace(source.Kind)
	if source.Kind == "legacy" && len(source.Declared) == 0 && source.SourceID != "" {
		source.Declared = []string{"pull_collection", "context_collection"}
	}
	if source.Kind == "legacy" {
		if source.ProjectID == "" {
			source.ProjectID = scope.ProjectID
		}
		if source.SourceID == "" {
			source.SourceID = scope.SourceID
		}
	} else if strings.TrimSpace(source.ProjectID) == "" || strings.TrimSpace(source.SourceID) == "" || source.Version <= 0 {
		return nil, fmt.Errorf("source capability identity or version is invalid")
	}
	if source.ProjectID != scope.ProjectID || source.SourceID != scope.SourceID {
		return nil, fmt.Errorf("source capability has the wrong project or source")
	}
	source.Declared = append([]string(nil), source.Declared...)

	catalog := &ToolCatalog{
		phase:     phase,
		source:    source,
		scope:     domain.DynamicToolScope{RunID: runID, ProjectID: scope.ProjectID, SourceID: scope.SourceID, SourceVersion: source.Version},
		runtime:   g.dynamicRuntime,
		routes:    make(map[string]dynamicToolRoute),
		activated: make(map[string]struct{}),
	}
	// 保存完整 built-in 能力，DefinitionsForPhase 在每轮请求时再按 phase
	// 过滤。catalog 通常在 preparing_context 创建，不能因此丢失诊断工具。
	catalog.base = g.baseDefinitions(domain.RunStateDiagnosing, scope, source)

	if !source.Enabled {
		catalog.status = catalogStatus{Code: "source_disabled", Message: "configured source is disabled"}
		catalog.rebuild()
		return catalog, nil
	}
	if !source.Supported {
		catalog.status = catalogStatus{
			Code:    "capability_unavailable",
			Message: sourceCapabilityUnavailableMessage(source.Kind),
		}
		catalog.rebuild()
		return catalog, nil
	}
	if source.Kind != "mcp" {
		catalog.rebuild()
		return catalog, nil
	}
	if g.dynamicRuntime == nil {
		catalog.status = catalogStatus{Code: "capability_unavailable", Message: "MCP runtime is unavailable"}
		catalog.rebuild()
		return catalog, nil
	}
	if !source.Allows("pull_collection") && !source.Allows("context_collection") {
		catalog.status = catalogStatus{Code: "capability_unavailable", Message: "MCP source does not allow context collection"}
		catalog.rebuild()
		return catalog, nil
	}
	catalog.base = append(catalog.base,
		toolDefinition(ToolSourceSearchTools),
		toolDefinition(ToolSourceRefreshTools),
	)

	if g.policyResolver == nil {
		catalog.status = catalogStatus{
			Code:    "policy_unconfigured",
			Message: "MCP tools are unavailable because no project tool policy is configured",
		}
		catalog.rebuild()
		return catalog, nil
	}
	policy, err := g.policyResolver.ResolveToolPolicy(ctx, scope.ProjectID, scope.SourceID)
	if err != nil {
		return nil, fmt.Errorf("resolve remediation tool policy: %w", err)
	}
	if policy.ProjectID == "" && policy.SourceID == "" && policy.Version == 0 && policy.Hash == "" && len(policy.Entries) == 0 {
		// 旧的内存 fake 用零值表达缺失 policy；生产 resolver 返回带身份的空快照。
		policy.ProjectID = scope.ProjectID
		policy.SourceID = scope.SourceID
	}
	if err := policy.ValidateFor(scope.ProjectID, scope.SourceID); err != nil {
		return nil, fmt.Errorf("validate remediation tool policy: %w", err)
	}
	catalog.policy = cloneToolPolicy(policy)
	catalog.scope.PolicyVersion = policy.Version
	if policy.Version == 0 {
		catalog.status = catalogStatus{
			Code:    "policy_unconfigured",
			Message: "MCP tools are unavailable because no project tool policy is configured",
		}
		catalog.rebuild()
		return catalog, nil
	}

	if err := g.discoverIntoCatalog(ctx, catalog); err != nil {
		// discoverIntoCatalog returns only safe runtime errors. The catalog is
		// still usable for repository reads and the refresh action.
		catalog.routes = make(map[string]dynamicToolRoute)
		catalog.status = safeCatalogStatus(err)
	}
	catalog.rebuild()
	return catalog, nil
}

// BuildCatalogWithBootstrap 在首次 Tencent CLS detail 读取前只暴露无参数 detail tool；读取失败后保留安全 observation，并按失败可重试性开放有界的后备读取能力。
func (g *ToolGateway) BuildCatalogWithBootstrap(
	ctx context.Context,
	runID string,
	incidentID string,
	phase domain.RunState,
	scope domain.EvidenceScope,
	source domain.SourceCapabilitySnapshot,
	bootstrap domain.BootstrapEvidence,
) (*ToolCatalog, error) {
	catalog, err := g.BuildCatalog(ctx, runID, phase, scope, source)
	if err != nil {
		return nil, err
	}
	catalog.incidentID = incidentID
	catalog.tencentDetailRequired = requiresTencentCLSDetail(bootstrap)
	catalog.tencentDetailReady = hasTrustedTencentCLSDetail(bootstrap)
	catalog.rebuild()
	return catalog, nil
}

// BuildAnalysisOnlyCatalog creates the manual-continuation catalog without
// resolving source policy or probing a dynamic runtime. Persisted operational
// evidence is already present in bootstrap context, so only repository reads
// remain available to the analysis model.
func (g *ToolGateway) BuildAnalysisOnlyCatalog(
	runID string,
	phase domain.RunState,
	scope domain.EvidenceScope,
) *ToolCatalog {
	catalog := &ToolCatalog{
		phase:  phase,
		source: domain.SourceCapabilitySnapshot{Kind: "persisted_evidence"},
		scope: domain.DynamicToolScope{
			RunID: runID, ProjectID: scope.ProjectID, SourceID: scope.SourceID,
		},
		routes:    make(map[string]dynamicToolRoute),
		activated: make(map[string]struct{}),
	}
	catalog.base = g.baseDefinitions(phase, scope, domain.SourceCapabilitySnapshot{})
	catalog.rebuild()
	return catalog
}

func requiresTencentCLSDetail(bootstrap domain.BootstrapEvidence) bool {
	for _, record := range bootstrap.Records {
		if record.Provider == "tencent_cls" && record.EvidenceKind == domain.EvidenceKindNormalizedAlert {
			return !hasTrustedTencentCLSDetail(bootstrap)
		}
	}
	return false
}

func hasTrustedTencentCLSDetail(bootstrap domain.BootstrapEvidence) bool {
	for _, record := range bootstrap.Records {
		if trustedTencentDetailEvidence(record) {
			return true
		}
	}
	return false
}

func (c *ToolCatalog) tencentDetailGateClosed() bool {
	return c != nil && c.tencentDetailRequired && !c.tencentDetailReady && c.tencentDetailAttempts == 0
}

func (c *ToolCatalog) tencentDetailRetryAvailable() bool {
	return c != nil && c.tencentDetailRequired && !c.tencentDetailReady &&
		c.tencentDetailRetryable && c.tencentDetailAttempts < maxTencentDetailAttempts
}

// seedPriorTencentDetailFailure carries a predecessor's failed detail call into a
// continuation. A child must not repeat a non-retryable detail failure merely
// because its fresh bootstrap still contains the normalized Tencent alert.
func (c *ToolCatalog) seedPriorTencentDetailFailure(invocations []domain.ToolInvocation) {
	if c == nil || !c.tencentDetailRequired || c.tencentDetailReady {
		return
	}
	for _, invocation := range invocations {
		if invocation.ToolName != ToolTencentCLSDetail {
			continue
		}
		code, failed := priorTencentDetailFailureCode(invocation)
		if !failed {
			continue
		}
		c.tencentDetailAttempts = 1
		c.tencentDetailRetryable = false
		c.tencentDetailFailure = code
		c.rebuild()
		return
	}
}

func priorTencentDetailFailureCode(invocation domain.ToolInvocation) (string, bool) {
	if strings.TrimSpace(invocation.Error) != "" {
		code := safeContinuationErrorCode(invocation.Error)
		if code != "unknown" && strings.HasPrefix(code, "provider_detail_") {
			return code, true
		}
		// Older rows retained only Error=error. Treat unknown error metadata as
		// a failed detail attempt, but never replay its raw value.
		return priorTencentDetailFailure, true
	}
	switch strings.TrimSpace(invocation.ResultSummary) {
	case "error", "failure", "unavailable":
		// Legacy adapters may retain only the invocation outcome.
		return priorTencentDetailFailure, true
	default:
		return "", false
	}
}

func (c *ToolCatalog) markTencentDetailReady() {
	if c == nil {
		return
	}
	c.tencentDetailReady = true
	c.tencentDetailRetryable = false
	c.tencentDetailFailure = ""
	c.rebuild()
}

// markTencentDetailFailure 记录 provider observation 并打开其他有界读取工具。
// 可重试的传输失败最多保留一次 detail retry；invalid 或已耗尽的 detail
// 响应不能把 diagnosing 永久锁在同一个失败工具上。
func (c *ToolCatalog) markTencentDetailFailure(code string, retryable bool) {
	if c == nil {
		return
	}
	c.tencentDetailAttempts++
	c.tencentDetailFailure = code
	c.tencentDetailRetryable = retryable && c.tencentDetailAttempts < maxTencentDetailAttempts
	c.rebuild()
}

func (g *ToolGateway) baseDefinitions(
	phase domain.RunState,
	scope domain.EvidenceScope,
	source domain.SourceCapabilitySnapshot,
) []domain.ToolDefinition {
	if phase != domain.RunStateDiagnosing && phase != domain.RunStateCollectingMoreContext && phase != domain.RunStatePlanning {
		return nil
	}
	names := []string{ToolRepoListTree, ToolRepoReadFile, ToolRepoSearch, ToolRepoHistory}
	switch {
	case source.Kind == "ssh" && source.Enabled && source.Supported &&
		(source.Allows("pull_collection") || source.Allows("context_collection")) && phase != domain.RunStatePlanning:
		// Docker deployment 同时广告 typed docker.logs 与 generic ssh.inspect：
		// inspect 用于主机/网络/进程等运行时证据（如 INC-2267 的 hostname -I / ip addr show），
		// typed logs 用于 incident window 日志收集。两者都经过只读命令策略，成功结果
		// 先持久化为 remediation evidence 再进入模型上下文。
		if source.SSHDeploymentKind == "docker" {
			names = append(names, ToolDockerLogs)
		}
		names = append(names, ToolSSHInspect)
		// evidence.read 只读当前 run series 的持久化证据，不依赖任何 connector，
		// 因此 SSH/Docker source 与 cloud 一样广告它：continuation 索引条目按 ID
		// 重新读取原始证据时不需要日志窗口能力。evidence.search/context 仍绑定
		// cloud pull/context 日志窗口，不进入 SSH 分支；planning 由外层条件排除。
		names = append(names, ToolEvidenceRead)
	case phase != domain.RunStatePlanning && ((source.Kind == "legacy" && scope.SourceID != "") || (source.Enabled && source.Supported &&
		source.Kind == "cloud" &&
		(source.Allows("pull_collection") || source.Allows("context_collection")))):
		// evidence.read 只读持久化证据，不依赖 connector；广告位置与
		// evidence.search/context 相同，避免把只读持久化读取暴露给分析专用 catalog。
		names = append(names, ToolEvidenceSearch, ToolEvidenceContext, ToolEvidenceRead)
	}
	return g.advertisedToolDefinitionsForNames(names)
}

func isActiveToolPhase(phase domain.RunState) bool {
	switch phase {
	case domain.RunStatePreparingContext, domain.RunStateDiagnosing,
		domain.RunStateCollectingMoreContext, domain.RunStatePlanning:
		return true
	default:
		return false
	}
}

func toolDefinition(name string) domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        name,
		Description: toolDescriptions[name],
		Parameters:  toolParameterSchema(name),
	}
}

func (g *ToolGateway) advertisedToolDefinitionsForNames(names []string) []domain.ToolDefinition {
	defs := make([]domain.ToolDefinition, 0, len(names))
	for _, name := range names {
		if name == ToolSourceSearchTools || name == ToolSourceRefreshTools {
			defs = append(defs, toolDefinition(name))
			continue
		}
		defs = append(defs, domain.ToolDefinition{
			Name:        name,
			Description: toolDescriptions[name],
			Parameters:  toolParameterSchema(name),
		})
	}
	return defs
}

// DefinitionsForPhase returns a copy of the current phase catalog. Dynamic
// definitions are filtered by the snapshotted policy grant for that phase.
func (c *ToolCatalog) DefinitionsForPhase(phase domain.RunState) []domain.ToolDefinition {
	if c == nil {
		return nil
	}
	return c.definitionsForPhase(phase)
}

// AdvertisedToolDefinitionsForCatalog is the provider-facing catalog entry
// point. It keeps the legacy static definition helpers intact while allowing a
// run to use its snapshotted source/policy capabilities.
func (g *ToolGateway) AdvertisedToolDefinitionsForCatalog(catalog *ToolCatalog, phase domain.RunState) []domain.ToolDefinition {
	if catalog == nil {
		return g.AdvertisedToolDefinitions(phase)
	}
	return catalog.DefinitionsForPhase(phase)
}

// StatusText is included in bootstrap context and contains no connector
// details. It is intentionally short because the model also receives schemas.
func (c *ToolCatalog) StatusText() string {
	if c == nil {
		return "tool catalog: unavailable"
	}
	if c.tencentDetailRequired {
		if c.tencentDetailFailure != "" {
			return fmt.Sprintf("tool catalog: tencent_cls_detail_required=true ready=%t detail_attempts=%d detail_failure=%s detail_retry_available=%t fallback_tools=true tools=%d version=%s", c.tencentDetailReady, c.tencentDetailAttempts, c.tencentDetailFailure, c.tencentDetailRetryAvailable(), len(c.definitions), c.Version())
		}
		return fmt.Sprintf("tool catalog: tencent_cls_detail_required=true ready=%t tools=%d version=%s", c.tencentDetailReady, len(c.definitions), c.Version())
	}
	if c.status.Code != "" {
		return fmt.Sprintf("tool catalog: status=%s retryable=%t message=%s version=%s", c.status.Code, c.status.Retryable, c.status.Message, c.Version())
	}
	return fmt.Sprintf("tool catalog: source=%s tools=%d version=%s", c.source.Kind, len(c.definitions), c.Version())
}

// Version is a deterministic catalog hash safe to persist and expose.
func (c *ToolCatalog) Version() string {
	if c == nil {
		return ""
	}
	return catalogHash(c.definitions, c.source.Version, c.policy.Version, c.policy.Hash,
		c.discoveryVersion, c.discoveryHash, c.discoveryTruncated, c.status.Code)
}

func (c *ToolCatalog) rebuild() {
	if c == nil {
		return
	}
	c.definitions = c.definitionsForPhase(c.phase)
}

func (c *ToolCatalog) definitionsForPhase(phase domain.RunState) []domain.ToolDefinition {
	if c == nil {
		return nil
	}
	if phase != domain.RunStateDiagnosing && phase != domain.RunStateCollectingMoreContext && phase != domain.RunStatePlanning {
		return nil
	}
	if c.tencentDetailGateClosed() {
		if phase != domain.RunStateDiagnosing && phase != domain.RunStateCollectingMoreContext {
			return nil
		}
		return []domain.ToolDefinition{toolDefinition(ToolTencentCLSDetail)}
	}
	definitions := make([]domain.ToolDefinition, 0, len(c.base)+len(c.activated)+1)
	for _, definition := range c.base {
		if phase == domain.RunStatePlanning && (definition.Name == ToolEvidenceSearch || definition.Name == ToolEvidenceContext || definition.Name == ToolEvidenceRead || definition.Name == ToolSSHInspect) {
			continue
		}
		if definition.Name == ToolSourceSearchTools && c.status.Code != "" {
			continue
		}
		definitions = append(definitions, definition)
	}
	for name := range c.activated {
		route, ok := c.routes[name]
		if !ok {
			continue
		}
		if len(route.phases) > 0 {
			if _, ok := route.phases[phase]; !ok {
				continue
			}
		}
		definitions = append(definitions, route.definition)
	}
	if c.tencentDetailRetryAvailable() && (phase == domain.RunStateDiagnosing || phase == domain.RunStateCollectingMoreContext) {
		definitions = append(definitions, toolDefinition(ToolTencentCLSDetail))
	}
	// Discovery order is not authoritative. Sort by model-visible identity so
	// provider requests and hashes remain deterministic across paginated calls.
	sort.SliceStable(definitions, func(i, j int) bool { return definitions[i].Name < definitions[j].Name })
	return cloneToolDefinitions(definitions)
}

func (c *ToolCatalog) hasDefinition(phase domain.RunState, name string) bool {
	for _, definition := range c.DefinitionsForPhase(phase) {
		if definition.Name == name {
			return true
		}
	}
	return false
}

func (g *ToolGateway) discoverIntoCatalog(ctx context.Context, catalog *ToolCatalog) error {
	if catalog == nil || catalog.runtime == nil {
		return &domain.ToolRuntimeError{Code: "capability_unavailable", Message: "MCP runtime is unavailable"}
	}
	probeCtx, cancel := context.WithTimeout(ctx, dynamicDiscoveryLimit)
	defer cancel()
	discovered, err := catalog.runtime.Discover(probeCtx, catalog.scope)
	if err != nil {
		return err
	}
	if discovered.SourceID != catalog.scope.SourceID {
		return &domain.ToolRuntimeError{Code: "invalid_response", Message: "MCP discovery returned the wrong source"}
	}
	if strings.TrimSpace(discovered.ServerID) == "" {
		return &domain.ToolRuntimeError{Code: "invalid_response", Message: "MCP discovery returned no server identity"}
	}
	if discovered.Version == "" || discovered.Hash == "" || discovered.Version != discovered.Hash ||
		discovered.Hash != dynamicDiscoveryHash(discovered.ServerID, discovered.Tools, discovered.Truncated) {
		return &domain.ToolRuntimeError{Code: "invalid_response", Message: "MCP discovery returned an invalid catalog version"}
	}
	if len(discovered.Tools) > maxDynamicTools {
		return &domain.ToolRuntimeError{Code: "invalid_response", Message: "MCP discovery returned too many tools"}
	}
	routes := make(map[string]dynamicToolRoute)
	seen := make(map[string]struct{}, len(discovered.Tools))
	for _, discoveredTool := range discovered.Tools {
		if discoveredTool.SourceID != catalog.scope.SourceID || discoveredTool.ServerID != discovered.ServerID ||
			strings.TrimSpace(discoveredTool.Name) == "" {
			return &domain.ToolRuntimeError{Code: "invalid_response", Message: "MCP discovery returned an invalid tool identity"}
		}
		if _, exists := seen[discoveredTool.ServerID+"\x00"+discoveredTool.Name]; exists {
			return &domain.ToolRuntimeError{Code: "invalid_response", Message: "MCP discovery returned a duplicate tool"}
		}
		seen[discoveredTool.ServerID+"\x00"+discoveredTool.Name] = struct{}{}
		phases := policyPhases(catalog.policy, discoveredTool.Name)
		if len(phases) == 0 {
			continue
		}
		schema, err := boundedSchema(discoveredTool.InputSchema)
		if err != nil {
			return &domain.ToolRuntimeError{Code: "invalid_response", Message: "MCP discovery returned an invalid approved tool schema"}
		}
		publicName := namespaceMCPTool(catalog.scope.SourceID, discoveredTool.ServerID, discoveredTool.Name)
		if publicName == "" || definitionExists(catalog.base, publicName) {
			return &domain.ToolRuntimeError{Code: "invalid_response", Message: "MCP tool namespace collides with an existing tool"}
		}
		if _, exists := routes[publicName]; exists {
			return &domain.ToolRuntimeError{Code: "invalid_response", Message: "MCP tool namespace collides with an existing tool"}
		}
		routes[publicName] = dynamicToolRoute{
			definition: domain.ToolDefinition{
				Name:        publicName,
				Description: boundedText(discoveredTool.Description, maxDynamicDescription),
				Parameters:  schema,
			},
			call: domain.DynamicToolCall{
				SourceID: catalog.scope.SourceID,
				ServerID: discoveredTool.ServerID,
				Name:     discoveredTool.Name,
			},
			phases: phases,
		}
	}
	// Only publish a fully validated discovery result. A refresh failure must
	// never leave a mixture of old routes and a partial new catalog.
	catalog.routes = routes
	catalog.activated = make(map[string]struct{})
	catalog.discoveryVersion = discovered.Version
	catalog.discoveryHash = discovered.Hash
	catalog.discoveryTruncated = discovered.Truncated
	catalog.status = catalogStatus{}
	return nil
}

func (g *ToolGateway) refreshCatalog(ctx context.Context, catalog *ToolCatalog) error {
	if catalog == nil || catalog.runtime == nil {
		return &domain.ToolRuntimeError{Code: "capability_unavailable", Message: "MCP runtime is unavailable"}
	}
	if catalog.policy.Version <= 0 && len(catalog.policy.Entries) == 0 {
		catalog.status = catalogStatus{
			Code:    "policy_unconfigured",
			Message: "MCP tools are unavailable because no project tool policy is configured",
		}
		catalog.rebuild()
		return &domain.ToolRuntimeError{Code: "policy_unconfigured", Message: "MCP tool policy is not configured"}
	}
	if err := g.discoverIntoCatalog(ctx, catalog); err != nil {
		catalog.routes = make(map[string]dynamicToolRoute)
		catalog.activated = make(map[string]struct{})
		catalog.discoveryVersion = ""
		catalog.discoveryHash = ""
		catalog.discoveryTruncated = false
		catalog.status = safeCatalogStatus(err)
		catalog.rebuild()
		return err
	}
	catalog.rebuild()
	return nil
}

func policyPhases(policy domain.ToolPolicySnapshot, name string) map[domain.RunState]struct{} {
	phases := make(map[domain.RunState]struct{})
	for _, entry := range policy.Entries {
		if entry.ToolName != name || entry.EffectClass != "read" {
			continue
		}
		for _, phase := range entry.Phases {
			phases[phase] = struct{}{}
		}
	}
	return phases
}

func namespaceMCPTool(sourceID, serverID, name string) string {
	parts := []string{"mcp", sanitizeToolPart(sourceID), sanitizeToolPart(serverID), sanitizeToolPart(name)}
	raw := strings.Join(parts, "_")
	sum := sha256.Sum256([]byte(sourceID + "\x00" + serverID + "\x00" + name))
	identity := hex.EncodeToString(sum[:])[:8]
	maxName := 128 - len(identity) - 1
	if maxName <= 0 {
		return ""
	}
	if len(raw) > maxName {
		raw = raw[:maxName]
	}
	return raw + "_" + identity
}

func sanitizeToolPart(value string) string {
	var b strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

func boundedSchema(schema map[string]interface{}) (map[string]interface{}, error) {
	if schema == nil {
		return nil, fmt.Errorf("input schema is missing")
	}
	encoded, err := json.Marshal(schema)
	if err != nil || len(encoded) > maxDynamicSchemaBytes {
		return nil, fmt.Errorf("input schema exceeds bound")
	}
	if err := validateSchemaShape(schema, 0); err != nil {
		return nil, err
	}
	return cloneJSONMap(schema), nil
}

func cloneToolPolicy(policy domain.ToolPolicySnapshot) domain.ToolPolicySnapshot {
	policy.Entries = append([]domain.ToolPolicyEntry(nil), policy.Entries...)
	for index := range policy.Entries {
		policy.Entries[index].Phases = append([]domain.RunState(nil), policy.Entries[index].Phases...)
	}
	return policy
}

func cloneToolDefinitions(definitions []domain.ToolDefinition) []domain.ToolDefinition {
	if len(definitions) == 0 {
		return nil
	}
	out := make([]domain.ToolDefinition, len(definitions))
	for index, definition := range definitions {
		out[index] = definition
		out[index].Parameters = cloneJSONMap(definition.Parameters)
	}
	return out
}

func cloneJSONMap(value map[string]interface{}) map[string]interface{} {
	if value == nil {
		return nil
	}
	return cloneJSONValue(value).(map[string]interface{})
}

func cloneJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(typed))
		for key, child := range typed {
			out[key] = cloneJSONValue(child)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(typed))
		for index, child := range typed {
			out[index] = cloneJSONValue(child)
		}
		return out
	case []string:
		return append([]string(nil), typed...)
	case []map[string]interface{}:
		out := make([]map[string]interface{}, len(typed))
		for index, child := range typed {
			out[index] = cloneJSONMap(child)
		}
		return out
	case map[string]string:
		out := make(map[string]string, len(typed))
		for key, child := range typed {
			out[key] = child
		}
		return out
	default:
		return value
	}
}

func sourceCapabilityUnavailableMessage(kind string) string {
	switch kind {
	case "cloud":
		return "cloud log connector is unavailable"
	case "mcp":
		return "MCP source capability is unavailable"
	default:
		return "configured source capability is unavailable"
	}
}

func validateSchemaShape(value any, depth int) error {
	if depth > maxDynamicSchemaDepth {
		return fmt.Errorf("input schema nesting exceeds bound")
	}
	switch current := value.(type) {
	case map[string]interface{}:
		for key, child := range current {
			if len(key) > 128 {
				return fmt.Errorf("input schema key exceeds bound")
			}
			if err := validateSchemaShape(child, depth+1); err != nil {
				return err
			}
		}
	case []interface{}:
		for _, child := range current {
			if err := validateSchemaShape(child, depth+1); err != nil {
				return err
			}
		}
	case string, bool, float64, int, int64, nil:
	default:
		return fmt.Errorf("input schema contains unsupported value")
	}
	return nil
}

func definitionExists(definitions []domain.ToolDefinition, name string) bool {
	for _, definition := range definitions {
		if definition.Name == name {
			return true
		}
	}
	return false
}

func dynamicDiscoveryHash(serverID string, tools []domain.DynamicToolDefinition, truncated bool) string {
	payload, err := json.Marshal(struct {
		Server    string                         `json:"server"`
		Tools     []domain.DynamicToolDefinition `json:"tools"`
		Truncated bool                           `json:"truncated"`
	}{serverID, tools, truncated})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func catalogHash(definitions []domain.ToolDefinition, sourceVersion, policyVersion int64, policyHash, discoveryVersion, discoveryHash string, discoveryTruncated bool, status string) string {
	payload, _ := json.Marshal(struct {
		Definitions        []domain.ToolDefinition `json:"definitions"`
		SourceVersion      int64                   `json:"sourceVersion"`
		PolicyVersion      int64                   `json:"policyVersion"`
		PolicyHash         string                  `json:"policyHash"`
		DiscoveryVersion   string                  `json:"discoveryVersion"`
		DiscoveryHash      string                  `json:"discoveryHash"`
		DiscoveryTruncated bool                    `json:"discoveryTruncated"`
		Status             string                  `json:"status"`
	}{definitions, sourceVersion, policyVersion, policyHash, discoveryVersion, discoveryHash, discoveryTruncated, status})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func safeCatalogStatus(err error) catalogStatus {
	if runtimeErr, ok := err.(*domain.ToolRuntimeError); ok {
		code := safeRuntimeCode(runtimeErr.Code)
		return catalogStatus{
			Code:      code,
			Retryable: safeRuntimeRetryable(code, runtimeErr.Retryable),
			Message:   safeCatalogRuntimeMessage(code, runtimeErr.Message),
		}
	}
	return catalogStatus{Code: "capability_unavailable", Retryable: true, Message: "MCP tool discovery failed"}
}

func safeCatalogRuntimeMessage(code, message string) string {
	if code == "invalid_response" {
		switch message {
		case "MCP discovery returned the wrong source",
			"MCP discovery returned no server identity",
			"MCP discovery returned an invalid catalog version",
			"MCP discovery returned too many tools",
			"MCP discovery returned an invalid tool identity",
			"MCP discovery returned a duplicate tool",
			"MCP discovery returned an invalid approved tool schema",
			"MCP tool namespace collides with an existing tool":
			return message
		}
	}
	return safeRuntimeMessage(code)
}
