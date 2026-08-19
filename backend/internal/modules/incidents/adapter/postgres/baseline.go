package postgres

import (
	"context"
	"errors"
	"fmt"

	"fixthe/backend/internal/modules/incidents/application"
	projectapplication "fixthe/backend/internal/modules/projects/application"
)

// ProjectBaseline 从项目配置读取触发时的 deployed commit。
type ProjectBaseline struct {
	repository projectapplication.Repository
}

// NewProjectBaseline 用项目 repository 实现事故模块的基线端口。
func NewProjectBaseline(repository projectapplication.Repository) (*ProjectBaseline, error) {
	if repository == nil {
		return nil, fmt.Errorf("project repository is required")
	}
	return &ProjectBaseline{repository: repository}, nil
}

// DeployedCommit 读取项目当前 repository 基线。配置不存在时返回空字符串，不阻断事故创建。
func (b *ProjectBaseline) DeployedCommit(ctx context.Context, projectID string) (string, error) {
	configuration, err := b.repository.GetConfiguration(ctx, projectID)
	if errors.Is(err, projectapplication.ErrConfigurationNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("load project configuration: %w", err)
	}
	return configuration.Repository.DeployedCommit, nil
}

var _ application.ProjectBaseline = (*ProjectBaseline)(nil)
