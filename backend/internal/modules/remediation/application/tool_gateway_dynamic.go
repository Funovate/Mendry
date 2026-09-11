package application

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"mendry/backend/internal/modules/remediation/domain"
)

// ExecuteToolObservedWithCatalog 是 dynamic runtime 对应的 observed 执行入口，
// 发出与 legacy gateway 相同的 credential-free observation，包括 policy rejection
// 与安全 connector failure classification。SSH/Docker 成功结果先持久化为 canonical
// evidence 并替换 ToolResult payload 与证据 ID，再进入 observer 和 conversation。
func (g *ToolGateway) ExecuteToolObservedWithCatalog(
	ctx context.Context,
	run RunIdentity,
	observer RunObserver,
	sequence int64,
	phase domain.RunState,
	ref domain.RepoRef,
	scope domain.EvidenceScope,
	catalog *ToolCatalog,
	tool string,
	params map[string]interface{},
) (ToolResult, error) {
	started := timeNow()
	result, err := g.ExecuteToolWithCatalog(ctx, phase, ref, scope, catalog, tool, params)
	if err == nil && isRuntimeEvidenceTool(tool) {
		catalogVersion := ""
		if catalog != nil {
			catalogVersion = catalog.Version()
		}
		canonical, evidenceID, persistErr := g.persistRuntimeEvidence(ctx, run, scope, phase, catalogVersion, tool, result)
		if persistErr != nil {
			// 原始成功输出不得进入 observer 或 conversation：持久化失败就是
			// 稳定、不可重试的 runtime evidence persistence 失败。
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
		Duration: timeSince(started), Outcome: "success", Bytes: result.BytesRetrieved,
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

// ExecuteToolWithCatalog 在调用 built-in port 或 dynamic MCP runtime 前，依据
// immutable per-run catalog 校验 model request 并完成路由。
func (g *ToolGateway) ExecuteToolWithCatalog(
	ctx context.Context,
	phase domain.RunState,
	ref domain.RepoRef,
	scope domain.EvidenceScope,
	catalog *ToolCatalog,
	tool string,
	params map[string]interface{},
) (ToolResult, error) {
	if catalog == nil {
		return g.ExecuteTool(ctx, phase, ref, scope, tool, params)
	}
	if scope.ProjectID != catalog.scope.ProjectID || scope.SourceID != catalog.scope.SourceID {
		return ToolResult{}, &ToolRejection{Code: RejectUnavailable, Tool: tool, Message: "tool catalog belongs to another source scope"}
	}
	if !catalog.hasDefinition(phase, tool) {
		return ToolResult{}, &ToolRejection{Code: RejectUnavailable, Tool: tool, Message: "tool is not in the run catalog"}
	}
	if tool == ToolSourceSearchTools {
		return searchAndActivateTools(catalog, phase, params)
	}
	if tool == ToolSourceRefreshTools {
		if err := g.refreshCatalog(ctx, catalog); err != nil {
			return ToolResult{
				Tool:    tool,
				Summary: "MCP tool catalog refresh failed",
				Payload: map[string]interface{}{
					"status":  "unavailable",
					"catalog": catalog.Version(),
				},
			}, err
		}
		return ToolResult{
			Tool:    tool,
			Summary: fmt.Sprintf("MCP tool catalog refreshed (%d tools)", len(catalog.DefinitionsForPhase(phase))),
			Payload: map[string]interface{}{
				"status":  "ready",
				"catalog": catalog.Version(),
				"tools":   len(catalog.DefinitionsForPhase(phase)),
			},
		}, nil
	}
	if tool == ToolTencentCLSDetail {
		if err := validateToolParameters(tool, params); err != nil {
			return ToolResult{}, &ToolRejection{Code: RejectArguments, Tool: tool, Message: err.Error()}
		}
		return g.execTencentCLSDetail(ctx, scope, catalog)
	}
	if tool == ToolEvidenceRead {
		if err := validateToolParameters(tool, params); err != nil {
			return ToolResult{}, &ToolRejection{Code: RejectArguments, Tool: tool, Message: err.Error()}
		}
		if strings.TrimSpace(catalog.scope.RunID) == "" {
			return ToolResult{}, &ToolRejection{Code: RejectUnavailable, Tool: tool, Message: "evidence.read run identity is unavailable"}
		}
		return g.execEvidenceRead(ctx, catalog.scope.RunID, scope, params)
	}
	if tool == ToolDockerLogs {
		if err := validateToolParameters(tool, params); err != nil {
			return ToolResult{}, &ToolRejection{Code: RejectArguments, Tool: tool, Message: err.Error()}
		}
		return g.execDockerLogs(ctx, scope, catalog.source, params)
	}
	if route, ok := catalog.routes[tool]; ok {
		if err := validateDynamicArguments(route.definition.Parameters, params); err != nil {
			return ToolResult{}, &ToolRejection{Code: RejectArguments, Tool: tool, Message: err.Error()}
		}
		if catalog.runtime == nil {
			return ToolResult{}, &domain.ToolRuntimeError{Code: "capability_unavailable", Message: "MCP runtime is unavailable"}
		}
		call := route.call
		call.Arguments = params
		result, err := catalog.runtime.Call(ctx, catalog.scope, call)
		if err != nil {
			return ToolResult{Tool: tool}, err
		}
		payload, bytesRetrieved, truncated := boundDynamicPayload(result.Payload)
		if result.BytesRetrieved > bytesRetrieved {
			bytesRetrieved = result.BytesRetrieved
		}
		if bytesRetrieved > maxDynamicResultBytes {
			bytesRetrieved = maxDynamicResultBytes
		}
		return ToolResult{
			Tool:           tool,
			Summary:        fmt.Sprintf("MCP tool result (%d bytes, truncated=%t)", bytesRetrieved, result.Truncated || truncated),
			BytesRetrieved: bytesRetrieved,
			EvidenceIDs:    boundedEvidenceIDs(result.EvidenceIDs),
			Payload:        payload,
		}, nil
	}
	// A catalog entry with no dynamic route is a built-in tool. The legacy
	// validator still owns path and adapter-specific parameter bounds.
	return g.ExecuteTool(ctx, phase, ref, scope, tool, params)
}

func trustedTencentDetailEvidence(evidence domain.StoredEvidence) bool {
	if strings.TrimSpace(evidence.EvidenceID) == "" || evidence.Provider != "tencent_cls" ||
		evidence.EvidenceKind != domain.EvidenceKindProviderDetail ||
		evidence.Classification != domain.EvidenceDirectFault || evidence.Outcome != "success" ||
		!evidence.Available || !evidence.Primary {
		return false
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(evidence.Payload, &payload) != nil || len(payload) == 0 {
		return false
	}
	var provenance struct {
		Adapter                   string   `json:"adapter"`
		DetailCapabilityValidated bool     `json:"detail_capability_validated"`
		DetailResolution          string   `json:"detail_resolution"`
		Contradictions            []string `json:"contradictions"`
	}
	if err := json.Unmarshal(evidence.Provenance, &provenance); err != nil || provenance.Contradictions == nil {
		return false
	}
	return provenance.Adapter == "tencent_cls" && provenance.DetailCapabilityValidated &&
		provenance.DetailResolution == "validated_provider_detail_get_alert_detail" &&
		len(provenance.Contradictions) == 0
}

func (g *ToolGateway) execTencentCLSDetail(ctx context.Context, scope domain.EvidenceScope, catalog *ToolCatalog) (ToolResult, error) {
	if g.tencentDetailPort == nil {
		return g.recordTencentDetailFailure(catalog, &domain.ToolRuntimeError{Code: "capability_unavailable", Message: "Tencent CLS detail reader is unavailable"})
	}
	if catalog == nil || strings.TrimSpace(catalog.incidentID) == "" || strings.TrimSpace(catalog.scope.RunID) == "" {
		return g.recordTencentDetailFailure(catalog, &domain.ToolRuntimeError{Code: "provider_detail_invalid", Message: "Tencent CLS detail scope is unavailable"})
	}
	result, err := g.tencentDetailPort.ResolveTencentCLSDetail(ctx, domain.TencentCLSDetailRequest{
		RunID:         catalog.scope.RunID,
		IncidentID:    catalog.incidentID,
		ProjectID:     scope.ProjectID,
		EnvironmentID: scope.EnvironmentID,
		SourceID:      scope.SourceID,
	})
	if err != nil {
		return g.recordTencentDetailFailure(catalog, err)
	}
	if !trustedTencentDetailEvidence(result.Evidence) {
		return g.recordTencentDetailFailure(catalog, &domain.ToolRuntimeError{Code: "provider_detail_invalid", Message: "Tencent CLS detail reader returned untrusted evidence"})
	}
	catalog.markTencentDetailReady()
	bytesRetrieved := result.BytesRetrieved
	if bytesRetrieved <= 0 {
		bytesRetrieved = result.Evidence.ByteCount
	}
	return ToolResult{
		Tool:           ToolTencentCLSDetail,
		Summary:        fmt.Sprintf("Tencent CLS provider detail (%d bytes)", bytesRetrieved),
		BytesRetrieved: bytesRetrieved,
		EvidenceIDs:    []string{result.Evidence.EvidenceID},
		Payload: map[string]interface{}{
			"evidenceId":     result.Evidence.EvidenceID,
			"provider":       result.Evidence.Provider,
			"evidenceKind":   result.Evidence.EvidenceKind,
			"classification": result.Evidence.Classification,
			"available":      result.Evidence.Available,
			"content":        json.RawMessage(result.Evidence.Payload),
		},
	}, nil
}

func (g *ToolGateway) recordTencentDetailFailure(catalog *ToolCatalog, err error) (ToolResult, error) {
	if catalog != nil {
		safe := classifyToolError(err)
		catalog.markTencentDetailFailure(safe.Code, safe.Retryable)
	}
	return ToolResult{Tool: ToolTencentCLSDetail}, err
}

type toolSearchMatch struct {
	name        string
	description string
	rank        int
	original    string
}

func searchAndActivateTools(catalog *ToolCatalog, phase domain.RunState, params map[string]interface{}) (ToolResult, error) {
	if err := validateDynamicArguments(toolParameterSchema(ToolSourceSearchTools), params); err != nil {
		return ToolResult{}, &ToolRejection{Code: RejectArguments, Tool: ToolSourceSearchTools, Message: err.Error()}
	}
	query, _ := params["query"].(string)
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return ToolResult{}, &ToolRejection{Code: RejectArguments, Tool: ToolSourceSearchTools, Message: "arguments.query must not be blank"}
	}
	limit := defaultToolSearchLimit
	if raw, ok := params["limit"]; ok {
		value, _ := numericValue(raw)
		limit = int(value)
	}

	matches := make([]toolSearchMatch, 0, len(catalog.routes))
	for publicName, route := range catalog.routes {
		if _, allowed := route.phases[phase]; !allowed {
			continue
		}
		rank, matched := toolSearchRank(query, route.call.Name, publicName, route.definition.Description)
		if !matched {
			continue
		}
		matches = append(matches, toolSearchMatch{
			name: publicName, original: route.call.Name, rank: rank,
			description: boundedText(route.definition.Description, maxToolSearchDescription),
		})
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].rank != matches[j].rank {
			return matches[i].rank < matches[j].rank
		}
		if matches[i].original != matches[j].original {
			return matches[i].original < matches[j].original
		}
		return matches[i].name < matches[j].name
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	compact := make([]map[string]interface{}, 0, len(matches))
	for _, match := range matches {
		catalog.activated[match.name] = struct{}{}
		compact = append(compact, map[string]interface{}{
			"name": match.name, "description": match.description,
		})
	}
	catalog.rebuild()
	return ToolResult{
		Tool:    ToolSourceSearchTools,
		Summary: fmt.Sprintf("Activated %d approved MCP tools", len(matches)),
		Payload: map[string]interface{}{
			"matches": compact,
			"count":   len(matches),
			"catalog": catalog.Version(),
		},
	}, nil
}

func toolSearchRank(query, originalName, publicName, description string) (int, bool) {
	originalName = strings.ToLower(originalName)
	publicName = strings.ToLower(publicName)
	description = strings.ToLower(description)
	switch {
	case query == originalName || query == publicName:
		return 0, true
	case strings.Contains(originalName, query) || strings.Contains(publicName, query):
		return 1, true
	case strings.Contains(description, query):
		return 2, true
	}
	searchable := originalName + " " + publicName + " " + description
	for _, term := range strings.Fields(query) {
		if !strings.Contains(searchable, term) {
			return 0, false
		}
	}
	return 3, true
}

func boundDynamicPayload(value any) (any, int64, bool) {
	redacted := redactConversationValue(value)
	encoded, err := json.Marshal(redacted)
	if err != nil {
		return map[string]interface{}{"truncated": true, "reason": "unserializable"}, 0, true
	}
	if len(encoded) <= maxDynamicResultBytes {
		return redacted, int64(len(encoded)), false
	}
	return map[string]interface{}{
		"truncated": true,
		"bytes":     len(encoded),
		"preview":   boundedText(string(encoded), maxDynamicResultBytes),
	}, maxDynamicResultBytes, true
}

func boundedEvidenceIDs(ids []string) []string {
	if len(ids) > 128 {
		ids = ids[:128]
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = boundedText(id, 256)
		if id != "" {
			out = append(out, id)
		}
	}
	return out
}

func validateDynamicArguments(schema map[string]interface{}, value map[string]interface{}) error {
	if value == nil {
		value = map[string]interface{}{}
	}
	return validateJSONSchemaValue(schema, value, "arguments")
}

func validateJSONSchemaValue(schema map[string]interface{}, value any, path string) error {
	if schema == nil {
		return fmt.Errorf("%s schema is missing", path)
	}
	if enum, ok := schema["enum"].([]interface{}); ok && !containsJSONValue(enum, value) {
		return fmt.Errorf("%s is not an allowed value", path)
	}
	if required, ok := schema["required"].([]interface{}); ok {
		object, objectOK := value.(map[string]interface{})
		if !objectOK {
			return fmt.Errorf("%s must be an object", path)
		}
		for _, raw := range required {
			name, ok := raw.(string)
			if ok {
				if _, exists := object[name]; !exists {
					return fmt.Errorf("%s.%s is required", path, name)
				}
			}
		}
	}
	if required, ok := schema["required"].([]string); ok {
		object, objectOK := value.(map[string]interface{})
		if !objectOK {
			return fmt.Errorf("%s must be an object", path)
		}
		for _, name := range required {
			if _, exists := object[name]; !exists {
				return fmt.Errorf("%s.%s is required", path, name)
			}
		}
	}

	typeName, _ := schema["type"].(string)
	if typeName != "" && !jsonTypeMatches(typeName, value) {
		return fmt.Errorf("%s has invalid type", path)
	}
	if object, ok := value.(map[string]interface{}); ok {
		properties, _ := schema["properties"].(map[string]interface{})
		additional, hasAdditional := schema["additionalProperties"].(bool)
		for key, child := range object {
			property, exists := properties[key]
			if !exists {
				if hasAdditional && !additional {
					return fmt.Errorf("%s.%s is not allowed", path, key)
				}
				continue
			}
			childSchema, ok := property.(map[string]interface{})
			if !ok {
				return fmt.Errorf("%s.%s schema is invalid", path, key)
			}
			if err := validateJSONSchemaValue(childSchema, child, path+"."+key); err != nil {
				return err
			}
		}
	}
	if items, ok := value.([]interface{}); ok {
		if itemSchema, ok := schema["items"].(map[string]interface{}); ok {
			for index, item := range items {
				if err := validateJSONSchemaValue(itemSchema, item, fmt.Sprintf("%s[%d]", path, index)); err != nil {
					return err
				}
			}
		}
	}
	if min, ok := numericSchema(schema["minimum"]); ok {
		if number, ok := numericValue(value); !ok || number < min {
			return fmt.Errorf("%s is below the minimum", path)
		}
	}
	if max, ok := numericSchema(schema["maximum"]); ok {
		if number, ok := numericValue(value); !ok || number > max {
			return fmt.Errorf("%s exceeds the maximum", path)
		}
	}
	if minLength, ok := numericSchema(schema["minLength"]); ok {
		if stringValue, ok := value.(string); !ok || float64(len([]rune(stringValue))) < minLength {
			return fmt.Errorf("%s is shorter than allowed", path)
		}
	}
	if maxLength, ok := numericSchema(schema["maxLength"]); ok {
		if stringValue, ok := value.(string); !ok || float64(len([]rune(stringValue))) > maxLength {
			return fmt.Errorf("%s is longer than allowed", path)
		}
	}
	return nil
}

func jsonTypeMatches(typeName string, value any) bool {
	switch typeName {
	case "object":
		_, ok := value.(map[string]interface{})
		return ok
	case "array":
		_, ok := value.([]interface{})
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number":
		_, ok := numericValue(value)
		return ok
	case "integer":
		number, ok := numericValue(value)
		return ok && math.Trunc(number) == number
	case "null":
		return value == nil
	default:
		return false
	}
}

func numericSchema(value any) (float64, bool) {
	switch n := value.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

func numericValue(value any) (float64, bool) {
	switch n := value.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	default:
		return 0, false
	}
}

func containsJSONValue(values []interface{}, value interface{}) bool {
	for _, candidate := range values {
		left, _ := json.Marshal(candidate)
		right, _ := json.Marshal(value)
		if string(left) == string(right) {
			return true
		}
	}
	return false
}

// These variables keep the time calls replaceable in focused gateway tests
// without making the public runtime contract carry a clock.
var (
	timeNow   = time.Now
	timeSince = time.Since
)
