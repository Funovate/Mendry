package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	authdomain "mendry/backend/internal/modules/auth/domain"
	"mendry/backend/internal/modules/observations/domain"
	projectdomain "mendry/backend/internal/modules/projects/domain"
)

var (
	ErrInvalidInput   = errors.New("invalid observation input")
	ErrSourceNotFound = errors.New("observation source not found")
)

const (
	DefaultListLimit int32 = 50
	MaximumListLimit int32 = 100
)

// ListResult carries observations and the server-side total for a list response.
type ListResult struct {
	Items []domain.Observation
	Total int64
}

type ProjectAccess interface {
	ResolveAccess(context.Context, authdomain.User, string) (projectdomain.Project, error)
	RequireIncidentWrite(context.Context, authdomain.User, string) (projectdomain.Project, error)
}

type Repository interface {
	Create(context.Context, domain.Observation) (domain.Observation, error)
	List(context.Context, string, int32, int32) (ListResult, error)
}

type Options struct {
	Projects   ProjectAccess
	Repository Repository
	NewID      func() (string, error)
	Now        func() time.Time
}

type Service struct {
	projects   ProjectAccess
	repository Repository
	newID      func() (string, error)
	now        func() time.Time
}

func NewService(options Options) (*Service, error) {
	if options.Projects == nil || options.Repository == nil || options.NewID == nil || options.Now == nil {
		return nil, fmt.Errorf("observation service dependencies are required")
	}
	return &Service{projects: options.Projects, repository: options.Repository, newID: options.NewID, now: options.Now}, nil
}

type CreateInput struct {
	SourceID    string
	Service     *string
	OccurredAt  *time.Time
	Level       string
	Message     string
	Host        *string
	RequestID   *string
	Fingerprint string
	Attributes  json.RawMessage
}

func (s *Service) List(ctx context.Context, principal authdomain.User, projectKey string, limit, offset int32) (ListResult, error) {
	project, err := s.projects.ResolveAccess(ctx, principal, projectKey)
	if err != nil {
		return ListResult{}, err
	}
	if limit < 1 || limit > MaximumListLimit || offset < 0 {
		return ListResult{}, ErrInvalidInput
	}
	result, err := s.repository.List(ctx, project.ID, limit, offset)
	if err != nil {
		return ListResult{}, err
	}
	if result.Items == nil {
		result.Items = []domain.Observation{}
	}
	return result, nil
}

func (s *Service) Create(ctx context.Context, principal authdomain.User, projectKey string, input CreateInput) (domain.Observation, error) {
	project, err := s.projects.RequireIncidentWrite(ctx, principal, projectKey)
	if err != nil {
		return domain.Observation{}, err
	}
	return s.create(ctx, project.ID, input)
}

// CreateInbound 写入公开 webhook 的 Event Stream 行，不要求 Session。
func (s *Service) CreateInbound(ctx context.Context, projectID, sourceID, message, fingerprint string, occurredAt time.Time) (domain.Observation, error) {
	return s.create(ctx, projectID, CreateInput{
		SourceID: sourceID, OccurredAt: &occurredAt, Level: "error", Message: message, Fingerprint: fingerprint,
		Attributes: json.RawMessage(`{}`),
	})
}

func (s *Service) create(ctx context.Context, projectID string, input CreateInput) (domain.Observation, error) {
	id, err := s.newID()
	if err != nil {
		return domain.Observation{}, fmt.Errorf("generate observation ID: %w", err)
	}
	occurredAt := s.now().UTC()
	if input.OccurredAt != nil {
		occurredAt = input.OccurredAt.UTC()
	}
	attributes := input.Attributes
	if len(attributes) == 0 {
		attributes = json.RawMessage(`{}`)
	}
	observation := domain.Observation{ID: id, ProjectID: projectID, SourceID: input.SourceID, Service: input.Service,
		OccurredAt: occurredAt, Level: input.Level, Message: input.Message, Host: input.Host, RequestID: input.RequestID,
		Fingerprint: input.Fingerprint, Attributes: attributes}
	// Environment 由 repository 根据同项目 source 解析，此处使用临时占位满足其余字段校验。
	observation.EnvironmentID = "repository-resolved"
	if err := observation.Validate(); err != nil {
		return domain.Observation{}, ErrInvalidInput
	}
	observation.EnvironmentID = ""
	return s.repository.Create(ctx, observation)
}
