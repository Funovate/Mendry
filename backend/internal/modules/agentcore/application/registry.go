package application

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"sync"

	"mendry/backend/internal/modules/agentcore/domain"
)

const (
	maxToolSchemaBytes = 64 << 10
	maxToolSchemaDepth = 8
)

var (
	toolNamePattern    = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)
	toolVersionPattern = regexp.MustCompile(`^v[1-9][0-9]*$`)
)

// ToolRegistry 是 instance-owned trusted definitions 与 executor bindings。
type ToolRegistry struct {
	mu      sync.RWMutex
	entries map[string]registeredTool
}

type registeredTool struct {
	definition domain.ToolDefinition
	executor   domain.ToolExecutor
}

// NewToolRegistry 创建无全局状态的 trusted registry。
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{entries: make(map[string]registeredTool)}
}

// Register 校验 name/version/schema/effect 后原子注册 definition 与 executor。
func (r *ToolRegistry) Register(definition domain.ToolDefinition, executor domain.ToolExecutor) error {
	if r == nil {
		return errors.New("tool registry is nil")
	}
	if executor == nil {
		return errors.New("tool executor is required")
	}
	if !toolNamePattern.MatchString(definition.Name) {
		return errors.New("tool name must be a namespaced identifier")
	}
	if !toolVersionPattern.MatchString(definition.Version) {
		return errors.New("tool version must use vN form")
	}
	if definition.Effect != domain.ToolEffectRead && definition.Effect != domain.ToolEffectWrite {
		return errors.New("tool effect must be read or write")
	}
	definition.Parameters = cloneMap(definition.Parameters)
	if err := validateSchema(definition.Parameters, 0); err != nil {
		return fmt.Errorf("validate tool schema: %w", err)
	}
	encoded, err := json.Marshal(definition.Parameters)
	if err != nil || len(encoded) > maxToolSchemaBytes {
		return errors.New("tool schema exceeds bound")
	}
	key := registryKey(definition.Name, definition.Version)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.entries[key]; exists {
		return errors.New("tool registration collides with an existing name and version")
	}
	r.entries[key] = registeredTool{definition: definition, executor: executor}
	return nil
}

// Resolve 返回 copy-safe definition 与 registry-owned executor binding。
func (r *ToolRegistry) Resolve(name, version string) (domain.ToolDefinition, domain.ToolExecutor, bool) {
	if r == nil {
		return domain.ToolDefinition{}, nil, false
	}
	r.mu.RLock()
	entry, ok := r.entries[registryKey(name, version)]
	r.mu.RUnlock()
	if !ok {
		return domain.ToolDefinition{}, nil, false
	}
	definition := entry.definition
	definition.Parameters = cloneMap(definition.Parameters)
	return definition, entry.executor, true
}

// Definitions 返回按 name/version 稳定排序的 copy-safe catalog。
func (r *ToolRegistry) Definitions() []domain.ToolDefinition {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	keys := make([]string, 0, len(r.entries))
	for key := range r.entries {
		keys = append(keys, key)
	}
	sortStrings(keys)
	definitions := make([]domain.ToolDefinition, 0, len(keys))
	for _, key := range keys {
		definition := r.entries[key].definition
		definition.Parameters = cloneMap(definition.Parameters)
		definitions = append(definitions, definition)
	}
	r.mu.RUnlock()
	return definitions
}

// ValidateArguments 使用 trusted registration 的闭合 JSON schema 校验模型参数。
func (r *ToolRegistry) ValidateArguments(definition domain.ToolDefinition, arguments map[string]any) error {
	if arguments == nil {
		arguments = map[string]any{}
	}
	return validateValue(definition.Parameters, arguments, "arguments")
}

func registryKey(name, version string) string { return name + "\x00" + version }

func validateSchema(schema map[string]any, depth int) error {
	if depth > maxToolSchemaDepth || schema == nil {
		return errors.New("schema is missing or too deeply nested")
	}
	if schema["type"] != "object" || schema["additionalProperties"] != false {
		return errors.New("root schema must be a closed object")
	}
	if properties, ok := schema["properties"].(map[string]any); !ok || properties == nil {
		return errors.New("root schema properties must be an object")
	}
	return validateSchemaNode(schema, depth)
}

func validateSchemaNode(schema map[string]any, depth int) error {
	if depth > maxToolSchemaDepth {
		return errors.New("schema nesting exceeds bound")
	}
	typeName, _ := schema["type"].(string)
	switch typeName {
	case "object", "array", "string", "number", "integer", "boolean", "null":
	default:
		return fmt.Errorf("unsupported schema type %q", typeName)
	}
	if typeName == "object" {
		properties, ok := schema["properties"].(map[string]any)
		if !ok || properties == nil || schema["additionalProperties"] != false {
			return errors.New("object schema must declare closed properties")
		}
		for _, required := range schemaStrings(schema["required"]) {
			if _, exists := properties[required]; !exists {
				return fmt.Errorf("required property %q is not declared", required)
			}
		}
	}
	if typeName == "array" {
		if _, ok := schema["items"].(map[string]any); !ok {
			return errors.New("array schema must declare items")
		}
	}
	if properties, ok := schema["properties"].(map[string]any); ok {
		for name, raw := range properties {
			if strings.TrimSpace(name) == "" {
				return errors.New("schema property name is empty")
			}
			child, ok := raw.(map[string]any)
			if !ok {
				return errors.New("schema property is not an object")
			}
			if err := validateSchemaNode(child, depth+1); err != nil {
				return err
			}
		}
	}
	if raw, exists := schema["items"]; exists {
		child, ok := raw.(map[string]any)
		if !ok {
			return errors.New("schema items is not an object")
		}
		if err := validateSchemaNode(child, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func validateValue(schema map[string]any, value any, path string) error {
	typeName, _ := schema["type"].(string)
	if !matchesType(typeName, value) {
		return fmt.Errorf("%s has invalid type", path)
	}
	if enum, ok := schema["enum"].([]any); ok {
		matched := false
		for _, candidate := range enum {
			left, _ := json.Marshal(candidate)
			right, _ := json.Marshal(value)
			if string(left) == string(right) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s is not an allowed value", path)
		}
	}
	if object, ok := value.(map[string]any); ok {
		properties, _ := schema["properties"].(map[string]any)
		for _, name := range schemaStrings(schema["required"]) {
			if _, exists := object[name]; !exists {
				return fmt.Errorf("%s.%s is required", path, name)
			}
		}
		closed, _ := schema["additionalProperties"].(bool)
		for name, child := range object {
			raw, exists := properties[name]
			if !exists {
				if !closed {
					return fmt.Errorf("%s.%s is not allowed", path, name)
				}
				continue
			}
			childSchema, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("%s.%s schema is invalid", path, name)
			}
			if err := validateValue(childSchema, child, path+"."+name); err != nil {
				return err
			}
		}
	}
	if array, ok := value.([]any); ok {
		if itemSchema, ok := schema["items"].(map[string]any); ok {
			for index, item := range array {
				if err := validateValue(itemSchema, item, fmt.Sprintf("%s[%d]", path, index)); err != nil {
					return err
				}
			}
		}
	}
	if number, ok := numberValue(value); ok {
		if minimum, ok := numberValue(schema["minimum"]); ok && number < minimum {
			return fmt.Errorf("%s is below minimum", path)
		}
		if maximum, ok := numberValue(schema["maximum"]); ok && number > maximum {
			return fmt.Errorf("%s exceeds maximum", path)
		}
	}
	return nil
}

func schemaStrings(value any) []string {
	switch values := value.(type) {
	case []string:
		return values
	case []any:
		result := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}

func matchesType(typeName string, value any) bool {
	switch typeName {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number":
		_, ok := numberValue(value)
		return ok
	case "integer":
		number, ok := numberValue(value)
		return ok && math.Trunc(number) == number
	case "null":
		return value == nil
	default:
		return false
	}
}

func numberValue(value any) (float64, bool) {
	switch number := value.(type) {
	case int:
		return float64(number), true
	case int32:
		return float64(number), true
	case int64:
		return float64(number), true
	case float32:
		return float64(number), true
	case float64:
		return number, true
	default:
		return 0, false
	}
}

func sortStrings(values []string) {
	for index := 1; index < len(values); index++ {
		for cursor := index; cursor > 0 && values[cursor] < values[cursor-1]; cursor-- {
			values[cursor], values[cursor-1] = values[cursor-1], values[cursor]
		}
	}
}
