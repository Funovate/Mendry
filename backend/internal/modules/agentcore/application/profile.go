package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"mendry/backend/internal/modules/agentcore/domain"
)

// ProfileRegistry 是 instance-owned versioned workflow registry；event payload 只能引用已注册 profile。
type ProfileRegistry struct {
	mu       sync.RWMutex
	profiles map[string]domain.Profile
}

// NewProfileRegistry 创建空的 profile extension registry。
func NewProfileRegistry() *ProfileRegistry {
	return &ProfileRegistry{profiles: make(map[string]domain.Profile)}
}

// Register 校验并注册 profile name/version；冲突时 fail closed。
func (r *ProfileRegistry) Register(profile domain.Profile) error {
	if r == nil || profile == nil {
		return errors.New("profile is required")
	}
	definition := profile.Definition()
	if strings.TrimSpace(definition.Name) == "" || !toolVersionPattern.MatchString(definition.Version) {
		return errors.New("profile name and vN version are required")
	}
	if strings.TrimSpace(string(definition.Completion.Mode)) == "" || !toolVersionPattern.MatchString(definition.Completion.Version) {
		return errors.New("profile completion contract is invalid")
	}
	key := registryKey(definition.Name, definition.Version)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.profiles[key]; exists {
		return errors.New("profile already registered")
	}
	r.profiles[key] = profile
	return nil
}

// Resolve 返回 composition 注册的 exact profile version。
func (r *ProfileRegistry) Resolve(name, version string) (domain.Profile, error) {
	if r == nil {
		return nil, errors.New("profile registry is nil")
	}
	r.mu.RLock()
	profile := r.profiles[registryKey(name, version)]
	r.mu.RUnlock()
	if profile == nil {
		return nil, fmt.Errorf("profile %s/%s is not registered", name, version)
	}
	return profile, nil
}

// GoalProfileOptions 配置通用自由文本目标和 completion contract。
type GoalProfileOptions struct {
	Name                string
	Version             string
	SystemPrompt        string
	SummaryArtifactType string
	Completion          domain.CompletionContract
	MaxTokens           int
}

// GoalProfile 是非 incident 的通用 goal workflow；工具执行仍完全由 Runner 拥有。
type GoalProfile struct {
	options GoalProfileOptions
}

// NewGoalProfile 创建版本化 generic profile；配置来自 trusted composition，不来自 event payload。
func NewGoalProfile(options GoalProfileOptions) (*GoalProfile, error) {
	if strings.TrimSpace(options.Name) == "" || !toolVersionPattern.MatchString(options.Version) {
		return nil, errors.New("goal profile name and vN version are required")
	}
	if strings.TrimSpace(string(options.Completion.Mode)) == "" || !toolVersionPattern.MatchString(options.Completion.Version) {
		return nil, errors.New("goal profile completion contract is invalid")
	}
	if strings.TrimSpace(options.SystemPrompt) == "" {
		return nil, errors.New("goal profile system prompt is required")
	}
	if options.MaxTokens <= 0 {
		options.MaxTokens = 4096
	}
	if options.SummaryArtifactType == "" {
		options.SummaryArtifactType = "solution"
	}
	options.Completion.Parameters = cloneMap(options.Completion.Parameters)
	return &GoalProfile{options: options}, nil
}

// Definition 返回 immutable profile/completion version snapshot。
func (p *GoalProfile) Definition() domain.ProfileDefinition {
	return domain.ProfileDefinition{Name: p.options.Name, Version: p.options.Version, Completion: domain.CompletionContract{Mode: p.options.Completion.Mode, Version: p.options.Completion.Version, Parameters: cloneMap(p.options.Completion.Parameters)}}
}

// Prepare 生成下一轮 goal prompt；catalog/history 由 Runner 单独注入和裁剪。
func (p *GoalProfile) Prepare(_ context.Context, input domain.ProfileContext) (domain.ModelTurn, error) {
	return domain.ModelTurn{SystemPrompt: p.options.SystemPrompt, UserMessage: input.Run.Goal, MaxTokens: p.options.MaxTokens, Temperature: 0}, nil
}

// Interpret 将非工具自由文本标记为 model-authored summary，不能伪造 observed effect。
func (p *GoalProfile) Interpret(_ context.Context, result domain.ModelResult) (domain.ProfileResult, error) {
	if strings.TrimSpace(result.Content) == "" {
		return domain.ProfileResult{}, errors.New("goal profile requires non-empty model content")
	}
	return domain.ProfileResult{
		Stop:      true,
		Result:    map[string]any{"summary": result.Content},
		Artifacts: []domain.Artifact{{Type: p.options.SummaryArtifactType, SchemaVersion: "v1", Data: result.Content, Provenance: domain.ProvenanceModel}},
	}, nil
}
