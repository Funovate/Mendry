package application

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"fixthe/backend/internal/modules/remediation/domain"
)

// ExecuteToolObservedWithCatalog is the dynamic-runtime counterpart to the
// legacy gateway method. It emits the same credential-free observation shape,
// including policy rejection and safe connector failure classifications.
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

// ExecuteToolWithCatalog validates a model request against the immutable
// per-run catalog before routing either to a built-in port or the dynamic MCP
// runtime. The old ExecuteTool path remains available for legacy callers.
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
