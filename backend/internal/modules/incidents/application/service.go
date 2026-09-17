package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	authdomain "mendry/backend/internal/modules/auth/domain"
	"mendry/backend/internal/modules/incidents/domain"
	projectdomain "mendry/backend/internal/modules/projects/domain"
)

var (
	ErrInvalidInput = errors.New("invalid incident input")
	ErrNotFound     = errors.New("incident not found")
	ErrConflict     = errors.New("incident conflict")
)

const (
	// DefaultListLimit 是未指定分页大小时返回的事故上限。
	DefaultListLimit int32 = 50
	// MaximumListLimit 限制单次事故列表读取，避免无界数据库和 JSON 工作量。
	MaximumListLimit int32 = 100
	// RemediationReasonAutomatic 是合格新建/reopen 的自动触发原因。
	RemediationReasonAutomatic = "automatic"
	// RemediationReasonManual 是 admin/operator 手动启动（含 Info）的原因。
	RemediationReasonManual = "manual"
)

// ListResult carries incidents and the server-side total for a list response.
type ListResult struct {
	Items []domain.Incident
	Total int64
}

// ListQuery carries pagination and filter parameters for listing incidents.
type ListQuery struct {
	Limit  int32
	Offset int32
	Status *domain.Status
}

// Repository 是事故用例需要的最小 persistence contract。
type Repository interface {
	Create(context.Context, domain.Incident, *RemediationRequest) (domain.Incident, error)
	GetByNumber(context.Context, string, int64) (domain.Incident, error)
	GetByFingerprint(context.Context, string, string) (domain.Incident, error)
	RecordOccurrence(context.Context, string, string, time.Time) (domain.Incident, error)
	List(context.Context, string, ListQuery) (ListResult, error)
	UpdateStatus(context.Context, string, int64, domain.Status, int64, string, *RemediationRequest) (domain.Incident, error)
}

type ProjectAccess interface {
	ResolveAccess(context.Context, authdomain.User, string) (projectdomain.Project, error)
	RequireIncidentWrite(context.Context, authdomain.User, string) (projectdomain.Project, error)
}

// ProjectBaseline 在触发时从项目配置读取不可变 deployed commit。
// 适配器负责把“配置不存在”映射为空字符串；本接口不暴露项目模块类型。
type ProjectBaseline interface {
	DeployedCommit(ctx context.Context, projectID string) (string, error)
}

// RemediationRequest 是事故模块拥有的触发载荷，避免 application 循环依赖。
// IncidentID 必须是内部 UUIDv7，不得使用公开事故编号。
type RemediationRequest struct {
	IncidentID          string
	LifecycleGeneration int64
	DeployedCommit      string
	Priority            string
	Reason              string
	ContextVersion      int64
	// AnalysisOnly 由受信 ingress 决定并写入 root run；后续 continuation 只继承。
	AnalysisOnly bool
}

// RemediationTrigger 是 remediation 触发 seam。
// IngestInbound 会在入库事务提交后异步调用；已认证的 Create / UpdateStatus 仍同步调用。
type RemediationTrigger interface {
	Emit(ctx context.Context, req RemediationRequest) error
}

// CreateInput 描述创建事故时允许调用方覆盖的 MVP 字段。
type CreateInput struct {
	Title               string
	Fingerprint         string
	Priority            string
	SourceID            string
	FirstSeen           *time.Time
	LastSeen            *time.Time
	OccurrenceCount     *int64
	HostCount           *int64
	Muted               *bool
	NotificationSummary *string
}

// Options 注入事故 repository、项目基线、remediation seam、UUIDv7 生成器和可测试时钟。
type Options struct {
	Repository    Repository
	Projects      ProjectAccess
	Baseline      ProjectBaseline
	Remediation   RemediationTrigger
	NewIncidentID func() (string, error)
	Now           func() time.Time
}

// Service 实现事故列表、详情、创建和状态更新用例。
type Service struct {
	repository    Repository
	projects      ProjectAccess
	baseline      ProjectBaseline
	remediation   RemediationTrigger
	newIncidentID func() (string, error)
	now           func() time.Time
}

// NewService 在接收业务请求前验证全部事故依赖。
func NewService(options Options) (*Service, error) {
	if options.Repository == nil || options.Projects == nil || options.Baseline == nil ||
		options.Remediation == nil || options.NewIncidentID == nil || options.Now == nil {
		return nil, fmt.Errorf("incident application dependencies are required")
	}
	return &Service{
		repository: options.Repository, projects: options.Projects, baseline: options.Baseline,
		remediation: options.Remediation, newIncidentID: options.NewIncidentID, now: options.Now,
	}, nil
}

// List 返回按最近观测时间倒序排列的有界事故集合。
func (s *Service) List(ctx context.Context, principal authdomain.User, projectKey string, query ListQuery) (ListResult, error) {
	project, err := s.projects.ResolveAccess(ctx, principal, projectKey)
	if err != nil {
		return ListResult{}, err
	}
	if query.Limit < 1 || query.Limit > MaximumListLimit || query.Offset < 0 {
		return ListResult{}, ErrInvalidInput
	}
	if query.Status != nil {
		if _, err := domain.ParseStatus(string(*query.Status)); err != nil {
			return ListResult{}, ErrInvalidInput
		}
	}
	incidents, err := s.repository.List(ctx, project.ID, query)
	if err != nil {
		return ListResult{}, fmt.Errorf("list incidents: %w", err)
	}
	if incidents.Items == nil {
		incidents.Items = []domain.Incident{}
	}
	return incidents, nil
}

// Get 返回一个外部事故标识对应的事故。
func (s *Service) Get(ctx context.Context, principal authdomain.User, projectKey, identifier string) (domain.Incident, error) {
	project, err := s.projects.ResolveAccess(ctx, principal, projectKey)
	if err != nil {
		return domain.Incident{}, err
	}
	number, err := domain.ParseID(identifier)
	if err != nil {
		return domain.Incident{}, ErrInvalidInput
	}
	incident, err := s.repository.GetByNumber(ctx, project.ID, number)
	if err != nil {
		return domain.Incident{}, fmt.Errorf("get incident: %w", err)
	}
	return incident, nil
}

// Create 创建一个初始为 Open 的事故；所有默认值在 application 边界确定。
func (s *Service) Create(ctx context.Context, principal authdomain.User, projectKey string, input CreateInput) (domain.Incident, error) {
	project, err := s.projects.RequireIncidentWrite(ctx, principal, projectKey)
	if err != nil {
		return domain.Incident{}, err
	}
	commit, err := s.baseline.DeployedCommit(ctx, project.ID)
	if err != nil {
		return domain.Incident{}, fmt.Errorf("load project deployed commit: %w", err)
	}
	incident, err := s.newIncident(project.ID, commit, input)
	if err != nil {
		return domain.Incident{}, err
	}
	remediation := automaticRequest(incident)
	created, err := s.repository.Create(ctx, incident, remediation)
	if err != nil {
		return domain.Incident{}, fmt.Errorf("create incident: %w", err)
	}
	if remediation != nil {
		if err := s.emitAutomatic(ctx, created, false); err != nil {
			return domain.Incident{}, err
		}
	}
	return created, nil
}

// UpdateStatus 修改一个事故的生命周期状态，并在 Recovered→Open 时递增代数、重捕获 deployed commit。
func (s *Service) UpdateStatus(ctx context.Context, principal authdomain.User, projectKey, identifier, statusValue string) (domain.Incident, error) {
	project, err := s.projects.RequireIncidentWrite(ctx, principal, projectKey)
	if err != nil {
		return domain.Incident{}, err
	}
	number, err := domain.ParseID(identifier)
	if err != nil {
		return domain.Incident{}, ErrInvalidInput
	}
	status, err := domain.ParseStatus(strings.TrimSpace(statusValue))
	if err != nil {
		return domain.Incident{}, ErrInvalidInput
	}
	current, err := s.repository.GetByNumber(ctx, project.ID, number)
	if err != nil {
		return domain.Incident{}, fmt.Errorf("get incident: %w", err)
	}
	generation := domain.NextLifecycleGeneration(current.Status, status, current.LifecycleGeneration)
	commit := current.DeployedCommit
	reopened := current.Status == domain.StatusRecovered && status == domain.StatusOpen
	if reopened {
		// reopen 必须重读当前项目配置：deployed commit 变化会形成新的 series key。
		commit, err = s.baseline.DeployedCommit(ctx, project.ID)
		if err != nil {
			return domain.Incident{}, fmt.Errorf("load project deployed commit: %w", err)
		}
	}
	var remediation *RemediationRequest
	if reopened {
		remediation = automaticRequest(domain.Incident{
			InternalID: current.InternalID, LifecycleGeneration: generation,
			DeployedCommit: commit, Priority: current.Priority, Version: current.Version + 1,
		})
	}
	updated, err := s.repository.UpdateStatus(ctx, project.ID, number, status, generation, commit, remediation)
	if err != nil {
		return domain.Incident{}, fmt.Errorf("update incident status: %w", err)
	}
	if remediation != nil {
		if err := s.emitAutomatic(ctx, updated, false); err != nil {
			return domain.Incident{}, err
		}
	}
	return updated, nil
}

// IngestInbound 按 fingerprint 打开或更新事故；新建事故提交后异步触发 remediation，Closed / Recovered 不重开。
func (s *Service) IngestInbound(ctx context.Context, projectID, sourceID, title, fingerprint string, occurredAt time.Time) (domain.Incident, bool, error) {
	return s.ingestInbound(ctx, projectID, sourceID, title, fingerprint, occurredAt, false, nil)
}

// IngestInboundWithEvidence 在事故写入后、remediation 异步启动前运行一次
// evidenceWriter。这个窄回调保持事故模块不依赖 remediation；失败仍保留已提交
// 的事故并返回错误，避免在证据不完整时触发 remediation。
func (s *Service) IngestInboundWithEvidence(ctx context.Context, projectID, sourceID, title, fingerprint string, occurredAt time.Time, evidenceWriter func(context.Context, domain.Incident) error) (domain.Incident, bool, error) {
	return s.ingestInbound(ctx, projectID, sourceID, title, fingerprint, occurredAt, false, evidenceWriter)
}

// IngestInboundAnalysisOnlyWithEvidence 为受信 provider 创建不可执行 repair effects 的 run。
// occurredAt 仍使用接收时间；provider 原始时间由 Observation/evidence 单独保存。
func (s *Service) IngestInboundAnalysisOnlyWithEvidence(ctx context.Context, projectID, sourceID, title, fingerprint string, occurredAt time.Time, evidenceWriter func(context.Context, domain.Incident) error) (domain.Incident, bool, error) {
	return s.ingestInbound(ctx, projectID, sourceID, title, fingerprint, occurredAt, true, evidenceWriter)
}

func (s *Service) ingestInbound(ctx context.Context, projectID, sourceID, title, fingerprint string, occurredAt time.Time, analysisOnly bool, evidenceWriter func(context.Context, domain.Incident) error) (domain.Incident, bool, error) {
	current, err := s.repository.GetByFingerprint(ctx, projectID, fingerprint)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return domain.Incident{}, false, fmt.Errorf("get incident by fingerprint: %w", err)
	}
	if errors.Is(err, ErrNotFound) {
		created, createErr := s.createInbound(ctx, projectID, sourceID, title, fingerprint, occurredAt, analysisOnly)
		if createErr == nil {
			if evidenceWriter != nil {
				if evidenceErr := evidenceWriter(ctx, created); evidenceErr != nil {
					return created, true, fmt.Errorf("persist inbound evidence: %w", evidenceErr)
				}
			}
			s.emitAutomaticAsync(ctx, created, analysisOnly)
			return created, true, nil
		}
		if !errors.Is(createErr, ErrConflict) {
			return domain.Incident{}, false, createErr
		}
		current, err = s.repository.GetByFingerprint(ctx, projectID, fingerprint)
		if err != nil {
			return domain.Incident{}, false, fmt.Errorf("reload incident after conflict: %w", err)
		}
	}
	if current.Status != domain.StatusOpen {
		return current, false, nil
	}
	updated, err := s.repository.RecordOccurrence(ctx, projectID, fingerprint, occurredAt.UTC())
	if err != nil {
		return domain.Incident{}, false, fmt.Errorf("record incident occurrence: %w", err)
	}
	if evidenceWriter != nil {
		if evidenceErr := evidenceWriter(ctx, updated); evidenceErr != nil {
			return updated, false, fmt.Errorf("persist inbound evidence: %w", evidenceErr)
		}
		// Repeated open observations can only enter the automatic continuation gate
		// after their occurrence and evidence are both committed successfully.
		s.emitAutomaticAsync(ctx, updated, analysisOnly)
	}
	return updated, false, nil
}

func (s *Service) createInbound(ctx context.Context, projectID, sourceID, title, fingerprint string, occurredAt time.Time, analysisOnly bool) (domain.Incident, error) {
	commit, err := s.baseline.DeployedCommit(ctx, projectID)
	if err != nil {
		return domain.Incident{}, fmt.Errorf("load project deployed commit: %w", err)
	}
	seen := occurredAt.UTC()
	priority := string(domain.PriorityP2)
	incident, err := s.newIncident(projectID, commit, CreateInput{
		Title: title, Fingerprint: fingerprint, Priority: priority, SourceID: sourceID, FirstSeen: &seen, LastSeen: &seen,
	})
	if err != nil {
		return domain.Incident{}, err
	}
	remediation := automaticRequest(incident)
	if remediation != nil {
		remediation.AnalysisOnly = analysisOnly
	}
	created, err := s.repository.Create(ctx, incident, remediation)
	if err != nil {
		return domain.Incident{}, fmt.Errorf("create incident: %w", err)
	}
	return created, nil
}

func (s *Service) emitAutomatic(ctx context.Context, incident domain.Incident, analysisOnly bool) error {
	request := automaticRequest(incident)
	if request == nil {
		return nil
	}
	request.AnalysisOnly = analysisOnly
	if err := s.remediation.Emit(ctx, *request); err != nil {
		return fmt.Errorf("start remediation: %w", err)
	}
	return nil
}

func (s *Service) emitAutomaticAsync(ctx context.Context, incident domain.Incident, analysisOnly bool) {
	if automaticRequest(incident) == nil {
		return
	}
	// 事故与 remediation root 已在入库事务中提交；脱离 HTTP request context，
	// 避免响应返回或客户端断开取消尚未开始的 remediation。
	background := context.WithoutCancel(ctx)
	go func() {
		// remediation coordinator 会把启动后的失败写回 run 状态；Webhook 不等待该结果。
		_ = s.emitAutomatic(background, incident, analysisOnly)
	}()
}

func automaticRequest(incident domain.Incident) *RemediationRequest {
	if incident.Priority != domain.PriorityP1 && incident.Priority != domain.PriorityP2 {
		return nil
	}
	contextVersion := incident.Version
	if contextVersion < 1 {
		// Create transactions assign the first durable incident version while
		// assembling the root remediation row.
		contextVersion = 1
	}
	return &RemediationRequest{
		IncidentID:          incident.InternalID,
		LifecycleGeneration: incident.LifecycleGeneration,
		DeployedCommit:      incident.DeployedCommit,
		Priority:            string(incident.Priority),
		Reason:              RemediationReasonAutomatic,
		ContextVersion:      contextVersion,
	}
}

func (s *Service) newIncident(projectID, deployedCommit string, input CreateInput) (domain.Incident, error) {
	title, err := domain.NormalizeText(input.Title, "incident title", 240)
	if err != nil {
		return domain.Incident{}, invalidInput(err)
	}
	fingerprint, err := domain.NormalizeText(input.Fingerprint, "incident fingerprint", 255)
	if err != nil {
		return domain.Incident{}, invalidInput(err)
	}
	sourceID, err := domain.NormalizeText(input.SourceID, "incident source ID", 64)
	if err != nil {
		return domain.Incident{}, invalidInput(err)
	}

	priority := domain.PriorityInfo
	if strings.TrimSpace(input.Priority) != "" {
		priority, err = domain.ParsePriority(strings.TrimSpace(input.Priority))
		if err != nil {
			return domain.Incident{}, invalidInput(err)
		}
	}
	now := s.now().UTC()
	firstSeen, lastSeen := observedTimes(now, input.FirstSeen, input.LastSeen)
	occurrenceCount := optionalInt64(input.OccurrenceCount, 1)
	hostCount := optionalInt64(input.HostCount, 1)
	muted := optionalBool(input.Muted, false)
	notificationSummary := "Lifecycle default"
	if input.NotificationSummary != nil {
		notificationSummary, err = domain.NormalizeText(*input.NotificationSummary, "incident notification summary", 160)
		if err != nil {
			return domain.Incident{}, invalidInput(err)
		}
	}
	incident := domain.Incident{
		// 输入校验先使用非空占位；真实 ID 只在全部用户字段合法后生成。
		InternalID: "pending-generation", ProjectID: projectID, EnvironmentID: "repository-resolved", SourceID: sourceID,
		Title: title, Fingerprint: fingerprint, Status: domain.StatusOpen, Priority: priority, Source: "repository-resolved",
		FirstSeen: firstSeen, LastSeen: lastSeen, OccurrenceCount: occurrenceCount,
		HostCount: hostCount, Muted: muted, NotificationSummary: notificationSummary,
		LifecycleGeneration: domain.InitialLifecycleGeneration, DeployedCommit: deployedCommit,
	}
	if err := incident.Validate(); err != nil {
		return domain.Incident{}, invalidInput(err)
	}
	identifier, err := s.newIncidentID()
	if err != nil {
		return domain.Incident{}, fmt.Errorf("generate incident ID: %w", err)
	}
	incident.InternalID = identifier
	return incident, nil
}

func invalidInput(err error) error {
	return fmt.Errorf("%w: %v", ErrInvalidInput, err)
}

func observedTimes(now time.Time, first, last *time.Time) (time.Time, time.Time) {
	if first == nil && last == nil {
		return now, now
	}
	if first == nil {
		value := last.UTC()
		return value, value
	}
	firstValue := first.UTC()
	if last == nil {
		return firstValue, firstValue
	}
	return firstValue, last.UTC()
}

func optionalInt64(value *int64, fallback int64) int64 {
	if value == nil {
		return fallback
	}
	return *value
}

func optionalBool(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}
