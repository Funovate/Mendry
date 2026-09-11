package postgres

import (
	"context"
	"fmt"

	"mendry/backend/internal/modules/incidents/domain"
	"mendry/backend/internal/modules/remediation/application"
)

// IncidentLookup 把全局事故编号解析成 remediation 需要的内部身份。
type IncidentLookup struct {
	repository *Repository
}

// NewIncidentLookup 包装事故 PostgreSQL repository，不向 application 暴露 sqlc/pgx。
func NewIncidentLookup(repository *Repository) (*IncidentLookup, error) {
	if repository == nil {
		return nil, fmt.Errorf("incident repository is required")
	}
	return &IncidentLookup{repository: repository}, nil
}

// GetByNumber 按全局唯一事故编号返回内部 UUID 和已捕获的 series key。
func (l *IncidentLookup) GetByNumber(ctx context.Context, number int64) (application.IncidentIdentity, error) {
	incident, err := l.repository.GetByGlobalNumber(ctx, number)
	if err != nil {
		return application.IncidentIdentity{}, err
	}
	return identityFromIncident(incident), nil
}

// GetByProjectNumber 在已授权 project 范围内解析事故身份。
func (l *IncidentLookup) GetByProjectNumber(ctx context.Context, projectID string, number int64) (application.IncidentIdentity, error) {
	incident, err := l.repository.GetByNumber(ctx, projectID, number)
	if err != nil {
		return application.IncidentIdentity{}, err
	}
	return identityFromIncident(incident), nil
}

// GetByID 按内部 UUID 返回项目、环境和来源身份。
func (l *IncidentLookup) GetByID(ctx context.Context, incidentID string) (application.IncidentIdentity, error) {
	incident, err := l.repository.GetByID(ctx, incidentID)
	if err != nil {
		return application.IncidentIdentity{}, err
	}
	return identityFromIncident(incident), nil
}

func identityFromIncident(incident domain.Incident) application.IncidentIdentity {
	return application.IncidentIdentity{
		ID:                  incident.InternalID,
		ProjectID:           incident.ProjectID,
		EnvironmentID:       incident.EnvironmentID,
		SourceID:            incident.SourceID,
		Priority:            string(incident.Priority),
		DeployedCommit:      incident.DeployedCommit,
		Number:              incident.Number,
		LifecycleGeneration: incident.LifecycleGeneration,
		ContextVersion:      incident.Version,
	}
}
