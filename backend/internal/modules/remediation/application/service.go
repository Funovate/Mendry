package application

import (
	"context"
	"errors"
	"fmt"

	authdomain "fixthe/backend/internal/modules/auth/domain"
	incidentdomain "fixthe/backend/internal/modules/incidents/domain"
	projectdomain "fixthe/backend/internal/modules/projects/domain"
	"fixthe/backend/internal/modules/remediation/domain"
)

var (
	// ErrInvalidInput 表示 remediation 手动启动请求不符合稳定输入 contract。
	ErrInvalidInput = errors.New("invalid remediation input")
	// ErrConflict 表示请求携带的事故代数已不是当前生命周期。
	ErrConflict = errors.New("remediation conflict")
	// ErrNotFound 表示当前事故还没有 remediation series/run。
	ErrNotFound = errors.New("remediation not found")
)

// ProjectAccess 是 remediation 读写用例需要的项目授权 contract。
type ProjectAccess interface {
	ResolveAccess(context.Context, authdomain.User, string) (projectdomain.Project, error)
	RequireIncidentWrite(context.Context, authdomain.User, string) (projectdomain.Project, error)
}

// TriggerStarter 是手动 start 使用的同步触发 seam。
type TriggerStarter interface {
	Start(context.Context, TriggerRequest) (domain.Run, error)
}

// ServiceOptions 声明 remediation 手动用例依赖。
type ServiceOptions struct {
	Projects  ProjectAccess
	Incidents IncidentLookup
	Trigger   TriggerStarter
	Reviews   ReviewQuery
}

// Service 组合 remediation 手动启动和 review 读取用例；自动触发仍由 incidents.Service 调用 Trigger。
type Service struct {
	projects  ProjectAccess
	incidents IncidentLookup
	trigger   TriggerStarter
	reviews   ReviewQuery
}

// NewService 验证并创建 remediation application service。
func NewService(options ServiceOptions) (*Service, error) {
	if options.Projects == nil || options.Incidents == nil || options.Trigger == nil || options.Reviews == nil {
		return nil, fmt.Errorf("remediation service dependencies are required")
	}
	return &Service{
		projects:  options.Projects,
		incidents: options.Incidents,
		trigger:   options.Trigger,
		reviews:   options.Reviews,
	}, nil
}

// StartRemediation 手动启动一个项目范围内的 incident remediation run。
// generation 必须匹配当前事故代数，避免旧页面或跨生命周期请求启动错误 series。
func (s *Service) StartRemediation(ctx context.Context, principal authdomain.User, projectKey, identifier string, generation int64) (domain.Run, error) {
	if generation < 1 {
		return domain.Run{}, ErrInvalidInput
	}
	number, err := incidentdomain.ParseID(identifier)
	if err != nil {
		return domain.Run{}, ErrInvalidInput
	}
	project, err := s.projects.RequireIncidentWrite(ctx, principal, projectKey)
	if err != nil {
		return domain.Run{}, err
	}
	identity, err := s.incidents.GetByProjectNumber(ctx, project.ID, number)
	if err != nil {
		return domain.Run{}, err
	}
	if identity.LifecycleGeneration != generation {
		return domain.Run{}, ErrConflict
	}
	return s.trigger.Start(ctx, TriggerRequest{
		IncidentID:          identity.ID,
		LifecycleGeneration: identity.LifecycleGeneration,
		DeployedCommit:      identity.DeployedCommit,
		Priority:            identity.Priority,
		Reason:              TriggerReasonManual,
	})
}

// GetRemediation 返回当前事故 series key 下最新 run 的无秘密 review chain。
// 项目成员和 system admin 可读；没有 series/run 时返回 ErrNotFound。
func (s *Service) GetRemediation(ctx context.Context, principal authdomain.User, projectKey, identifier string) (Review, error) {
	number, err := incidentdomain.ParseID(identifier)
	if err != nil {
		return Review{}, ErrInvalidInput
	}
	project, err := s.projects.ResolveAccess(ctx, principal, projectKey)
	if err != nil {
		return Review{}, err
	}
	identity, err := s.incidents.GetByProjectNumber(ctx, project.ID, number)
	if err != nil {
		return Review{}, err
	}
	agg, err := s.reviews.GetLatestForIncident(ctx, identity.ID, identity.LifecycleGeneration, identity.DeployedCommit)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Review{}, ErrNotFound
		}
		return Review{}, fmt.Errorf("load remediation review: %w", err)
	}
	return buildReview(agg), nil
}
