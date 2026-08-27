package domain

import (
	"context"
	"time"
)

// DockerContainerIdentity 是运行时解析出的当前容器身份；ID 不进入项目持久化配置。
type DockerContainerIdentity struct {
	Name   string `json:"name"`
	ID     string `json:"id"`
	Image  string `json:"image"`
	State  string `json:"state"`
	Status string `json:"status"`
}

// DockerLogQuery 是 gateway 校验后的 bounded Docker logs 查询。
type DockerLogQuery struct {
	Since         time.Time
	Until         time.Time
	Tail          int
	MaxBytes      int64
	Pattern       string
	ContextBefore int
	ContextAfter  int
}

// DockerLogResult 是有界 stdout/stderr 运行时证据；它不包含 credential 或远端命令。
type DockerLogResult struct {
	Container      DockerContainerIdentity `json:"container"`
	Stdout         string                  `json:"stdout"`
	Stderr         string                  `json:"stderr"`
	Truncated      bool                    `json:"truncated"`
	BytesRetrieved int64                   `json:"bytesRetrieved"`
	WindowLines    int64                   `json:"windowLines"`
	FilteredLines  int64                   `json:"filteredLines"`
}

// DockerEvidencePort 只解析已保存 SSH source 的 exact container name，
// 然后执行时间/行数/字节均有界的只读 Docker logs。
type DockerEvidencePort interface {
	ResolveDockerContainer(context.Context, EvidenceScope) (DockerContainerIdentity, error)
	ReadDockerLogs(context.Context, EvidenceScope, DockerLogQuery) (DockerLogResult, error)
}
