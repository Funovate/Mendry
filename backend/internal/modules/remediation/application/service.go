package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	authdomain "fixthe/backend/internal/modules/auth/domain"
	incidentdomain "fixthe/backend/internal/modules/incidents/domain"
	projectdomain "fixthe/backend/internal/modules/projects/domain"
	"fixthe/backend/internal/modules/remediation/domain"
)

var (
	// ErrInvalidInput 表示 remediation 手动启动请求不符合稳定输入 contract。
	ErrInvalidInput = errors.New("invalid remediation input")
	// ErrConflict 表示请求携带的事故代数或 predecessor 已不是当前生命周期。
	ErrConflict = errors.New("remediation conflict")
	// ErrNotFound 表示当前事故还没有 remediation series/run。
	ErrNotFound = errors.New("remediation not found")
	// ErrActiveAttempt 表示当前 series 已有正在执行的 attempt。
	ErrActiveAttempt = errors.New("remediation attempt is active")
	// ErrUnsupportedContinuation 表示当前终态不允许由 operator 继续。
	ErrUnsupportedContinuation = errors.New("remediation continuation is unsupported")
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

// TriggerContinuer 是 manual/automatic continuation 共用的 next-attempt seam。
// 实现必须在持久化边界再次校验 predecessor、series 和 optimistic version。
type TriggerContinuer interface {
	Continue(context.Context, domain.NextAttempt) (domain.Run, error)
}

// BackgroundTriggerContinuer 为 HTTP 手动 continuation 提供快速确认路径。
// 实现只在 queued attempt 持久化成功后返回，实际 coordinator 驱动在后台完成；
// 不支持该接口的旧 trigger 仍通过 TriggerContinuer 保持同步兼容行为。
type BackgroundTriggerContinuer interface {
	QueueContinuation(context.Context, domain.NextAttempt) (domain.Run, error)
}

// ServiceOptions 声明 remediation 手动用例依赖。
// Checkpoints 是可选的 durable checkpoint 摘要 reader：注入后 GET review 会为
// resilient_v1 run 附加 D8 recovery/checkpoint projection；legacy 与既有部署
// 不注入时行为不变。
type ServiceOptions struct {
	Projects    ProjectAccess
	Incidents   IncidentLookup
	Trigger     TriggerStarter
	Reviews     ReviewQuery
	Checkpoints CheckpointReviewReader
}

// Service 组合 remediation 手动启动和 review 读取用例；自动触发仍由 incidents.Service 调用 Trigger。
type Service struct {
	projects    ProjectAccess
	incidents   IncidentLookup
	trigger     TriggerStarter
	reviews     ReviewQuery
	checkpoints CheckpointReviewReader
}

// NewService 验证并创建 remediation application service。
func NewService(options ServiceOptions) (*Service, error) {
	if options.Projects == nil || options.Incidents == nil || options.Trigger == nil || options.Reviews == nil {
		return nil, fmt.Errorf("remediation service dependencies are required")
	}
	return &Service{
		projects:    options.Projects,
		incidents:   options.Incidents,
		trigger:     options.Trigger,
		reviews:     options.Reviews,
		checkpoints: options.Checkpoints,
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
	if identity.ContextVersion < 1 {
		return domain.Run{}, fmt.Errorf("incident context version is invalid")
	}
	return s.trigger.Start(ctx, TriggerRequest{
		IncidentID:          identity.ID,
		LifecycleGeneration: identity.LifecycleGeneration,
		DeployedCommit:      identity.DeployedCommit,
		Priority:            identity.Priority,
		Reason:              TriggerReasonManual,
		ContextVersion:      identity.ContextVersion,
	})
}

// ContinueRemediation 创建当前 incident series 的下一个 manual attempt。
// 它先完成项目授权和事故身份解析，再以 expected run/version 调用共享
// continuation seam；旧 attempt 由持久化层保持不可变并在事务中重新校验。
func (s *Service) ContinueRemediation(
	ctx context.Context,
	principal authdomain.User,
	projectKey string,
	identifier string,
	generation int64,
	expectedRunID string,
	expectedVersion int64,
) (domain.Run, error) {
	expectedRunID = strings.TrimSpace(expectedRunID)
	if generation < 1 || expectedRunID == "" || expectedVersion < 1 {
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
	if identity.ProjectID != "" && identity.ProjectID != project.ID {
		return domain.Run{}, ErrNotFound
	}
	if identity.LifecycleGeneration != generation {
		return domain.Run{}, ErrConflict
	}
	if identity.ContextVersion < 1 {
		return domain.Run{}, fmt.Errorf("incident context version is invalid")
	}

	latest, err := s.reviews.GetLatestForIncident(ctx, identity.ID, identity.LifecycleGeneration, identity.DeployedCommit)
	if errors.Is(err, ErrNotFound) {
		return domain.Run{}, ErrNotFound
	}
	if err != nil {
		return domain.Run{}, fmt.Errorf("load latest remediation attempt: %w", err)
	}
	if latest.Run.RunID != expectedRunID || latest.Run.Version != expectedVersion {
		return domain.Run{}, ErrConflict
	}
	if !runMatchesIncident(latest.Run, identity) {
		return domain.Run{}, ErrConflict
	}
	if latest.Run.SeriesID == "" {
		return domain.Run{}, ErrNotFound
	}
	if aggregateHasActiveAttempt(latest) {
		return domain.Run{}, ErrActiveAttempt
	}
	if !isManualContinuationState(latest.Run.State) {
		return domain.Run{}, ErrUnsupportedContinuation
	}

	continuer, ok := s.trigger.(TriggerContinuer)
	if !ok {
		return domain.Run{}, fmt.Errorf("remediation continuation is unavailable")
	}
	continuation := domain.NextAttempt{
		ContinuationOfRunID:     latest.Run.RunID,
		SeriesID:                latest.Run.SeriesID,
		IncidentID:              identity.ID,
		LifecycleGeneration:     identity.LifecycleGeneration,
		DeployedCommit:          identity.DeployedCommit,
		ContextVersion:          identity.ContextVersion,
		ExpectedPreviousVersion: expectedVersion,
		Origin:                  domain.TriggerOriginManualContinue,
		TriggerReason:           domain.TriggerOriginManualContinue,
		ContinuationReason:      "operator requested continuation",
	}
	var child domain.Run
	if backgroundContinuer, ok := s.trigger.(BackgroundTriggerContinuer); ok {
		child, err = backgroundContinuer.QueueContinuation(ctx, continuation)
	} else {
		child, err = continuer.Continue(ctx, continuation)
	}
	if err != nil {
		return domain.Run{}, normalizeContinuationError(err)
	}
	if child.RunID == "" || child.RunID == expectedRunID || child.AttemptNumber != latest.Run.AttemptNumber+1 ||
		(child.SeriesID != "" && child.SeriesID != latest.Run.SeriesID) ||
		(child.IncidentID != "" && child.IncidentID != identity.ID) ||
		(child.LifecycleGeneration != 0 && child.LifecycleGeneration != identity.LifecycleGeneration) ||
		(child.DeployedCommit != "" && child.DeployedCommit != identity.DeployedCommit) {
		return domain.Run{}, ErrConflict
	}
	return child, nil
}

func runMatchesIncident(run domain.Run, identity IncidentIdentity) bool {
	return (run.IncidentID == "" || run.IncidentID == identity.ID) &&
		(run.LifecycleGeneration == 0 || run.LifecycleGeneration == identity.LifecycleGeneration) &&
		(run.DeployedCommit == "" || run.DeployedCommit == identity.DeployedCommit)
}

func normalizeContinuationError(err error) error {
	switch {
	case errors.Is(err, domain.ErrInvalidNextAttempt):
		return ErrInvalidInput
	case errors.Is(err, domain.ErrStalePredecessor):
		return ErrConflict
	case errors.Is(err, domain.ErrActiveAttempt):
		return ErrActiveAttempt
	case errors.Is(err, domain.ErrUnsupportedState):
		return ErrUnsupportedContinuation
	default:
		return err
	}
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
	if identity.ProjectID != "" && identity.ProjectID != project.ID {
		return Review{}, ErrNotFound
	}
	agg, err := s.reviews.GetLatestForIncident(ctx, identity.ID, identity.LifecycleGeneration, identity.DeployedCommit)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Review{}, ErrNotFound
		}
		return Review{}, fmt.Errorf("load remediation review: %w", err)
	}
	if !runMatchesIncident(agg.Run, identity) {
		return Review{}, ErrNotFound
	}
	review := buildReview(agg)
	review.ContinuationAvailable = project.CanWriteIncidents() &&
		isManualContinuationState(agg.Run.State) && !aggregateHasActiveAttempt(agg)
	// D8：resilient_v1 run 且注入了 checkpoint reader 时附加最新 durable
	// checkpoint/recovery projection。任何加载/校验错误都只跳过 projection，
	// review 页面保持可用（legacy run 与未注入部署完全不受影响）。
	if s.checkpoints != nil && agg.Run.AgentLoopMode == domain.AgentLoopModeResilientV1 {
		snapshot, loadErr := s.checkpoints.LoadLatestCheckpoint(ctx, agg.Run.RunID)
		if loadErr == nil {
			attachReviewCheckpoint(&review, snapshot)
		}
	}
	return review, nil
}
