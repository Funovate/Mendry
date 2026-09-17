package application

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"mendry/backend/internal/modules/agentcore/domain"
)

// PolicyInput 是 advertisement/dispatch 共享的 trusted policy 输入。
type PolicyInput struct {
	Run        domain.Run
	Definition domain.ToolDefinition
	Arguments  map[string]any
	Stage      string
}

// PolicyDecision 是稳定且可审计的授权结果。
type PolicyDecision struct {
	Allowed bool
	Code    string
}

// Policy 在 advertisement 与 dispatch 两个时点独立授权。
type Policy interface {
	Evaluate(context.Context, PolicyInput) (PolicyDecision, error)
}

// AllowAllPolicy 允许所有已 trusted registration 的 capability；预算和 durable intent 仍然生效。
type AllowAllPolicy struct{}

// Evaluate 返回显式 allow-all 决策。
func (AllowAllPolicy) Evaluate(context.Context, PolicyInput) (PolicyDecision, error) {
	return PolicyDecision{Allowed: true, Code: "allowed"}, nil
}

// RestrictionConfig 定义简单的 tool/effect/path 限制；空 allowlist 表示不按该维度限制。
type RestrictionConfig struct {
	AllowedTools   []string
	AllowedEffects []domain.ToolEffect
	AllowedRoots   []string
	PathFields     []string
}

// RestrictionPolicy 是 instance-owned configured restriction policy。
type RestrictionPolicy struct {
	config RestrictionConfig
}

// NewRestrictionPolicy 复制配置，避免调用方在 run 中途改变授权语义。
func NewRestrictionPolicy(config RestrictionConfig) *RestrictionPolicy {
	config.AllowedTools = append([]string(nil), config.AllowedTools...)
	config.AllowedEffects = append([]domain.ToolEffect(nil), config.AllowedEffects...)
	config.AllowedRoots = append([]string(nil), config.AllowedRoots...)
	config.PathFields = append([]string(nil), config.PathFields...)
	return &RestrictionPolicy{config: config}
}

// Evaluate 按 tool/effect/path 顺序 fail closed；advertisement 不含参数时只检查静态维度。
func (p *RestrictionPolicy) Evaluate(_ context.Context, input PolicyInput) (PolicyDecision, error) {
	if p == nil {
		return PolicyDecision{Code: "policy_unconfigured"}, nil
	}
	if len(p.config.AllowedTools) > 0 && !containsString(p.config.AllowedTools, input.Definition.Name) {
		return PolicyDecision{Code: "tool_denied"}, nil
	}
	if len(p.config.AllowedEffects) > 0 && !containsEffect(p.config.AllowedEffects, input.Definition.Effect) {
		return PolicyDecision{Code: "effect_denied"}, nil
	}
	if input.Stage == "advertisement" || len(p.config.AllowedRoots) == 0 {
		return PolicyDecision{Allowed: true, Code: "allowed"}, nil
	}
	for _, field := range p.config.PathFields {
		raw, exists := input.Arguments[field]
		if !exists {
			continue
		}
		path, ok := raw.(string)
		if !ok || !allowedPath(path, p.config.AllowedRoots) {
			return PolicyDecision{Code: "path_denied"}, nil
		}
	}
	return PolicyDecision{Allowed: true, Code: "allowed"}, nil
}

func allowedPath(path string, roots []string) bool {
	if filepath.IsAbs(path) {
		return false
	}
	cleaned := filepath.Clean(path)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return false
	}
	for _, root := range roots {
		cleanRoot := filepath.Clean(root)
		if cleaned == cleanRoot || strings.HasPrefix(cleaned, cleanRoot+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func containsEffect(values []domain.ToolEffect, wanted domain.ToolEffect) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func deniedError(decision PolicyDecision) error {
	code := decision.Code
	if code == "" {
		code = "policy_denied"
	}
	return fmt.Errorf("tool request denied: %s", code)
}
