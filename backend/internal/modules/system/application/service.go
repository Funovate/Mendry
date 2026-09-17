// Package application 定义 system 模块的进程健康状态用例，不依赖 HTTP 或基础设施实现。
package application

import (
	"context"
	"fmt"
	"time"
)

// Report 是健康 endpoint 返回的稳定 JSON 表示。
type Report struct {
	Status       string            `json:"status"`
	Dependencies map[string]string `json:"dependencies,omitempty"`
}

// Dependency 是 composition root 注入的进程必需依赖 readiness probe。
// Name 必须是稳定低基数标识；Check 的错误内容不会进入 HTTP response。
type Dependency struct {
	Name  string
	Check func(context.Context) error
}

// Service 提供 liveness 和 readiness 查询，并且只持有当前进程必需依赖。
type Service struct {
	dependencies []Dependency
	timeout      time.Duration
}

// NewService 验证并复制依赖列表，避免无界、重复或运行时才 panic 的 probe。
func NewService(timeout time.Duration, dependencies ...Dependency) (Service, error) {
	if timeout <= 0 {
		return Service{}, fmt.Errorf("readiness timeout must be positive")
	}
	names := make(map[string]struct{}, len(dependencies))
	for _, dependency := range dependencies {
		if dependency.Name == "" || dependency.Check == nil {
			return Service{}, fmt.Errorf("readiness dependency name and check are required")
		}
		if _, exists := names[dependency.Name]; exists {
			return Service{}, fmt.Errorf("readiness dependency names must be unique")
		}
		names[dependency.Name] = struct{}{}
	}
	return Service{
		dependencies: append([]Dependency(nil), dependencies...),
		timeout:      timeout,
	}, nil
}

// Liveness 报告进程自身是否仍可运行，不受外部依赖状态影响。
func (Service) Liveness() Report {
	return Report{Status: "alive"}
}

// Readiness 并发检查当前进程必需依赖，并只公开失败依赖的稳定名称。
// 原始 endpoint、credential 和 dependency error 不会进入 HTTP payload。
func (s Service) Readiness(ctx context.Context) Report {
	if len(s.dependencies) == 0 {
		return Report{Status: "ready"}
	}

	readinessContext, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	type result struct {
		name string
		err  error
	}
	results := make(chan result, len(s.dependencies))
	pending := make(map[string]struct{}, len(s.dependencies))
	for _, dependency := range s.dependencies {
		pending[dependency.Name] = struct{}{}
		go func() {
			results <- result{name: dependency.Name, err: dependency.Check(readinessContext)}
		}()
	}

	failures := make(map[string]string)
	for len(pending) > 0 {
		select {
		case result := <-results:
			delete(pending, result.name)
			if result.err != nil {
				failures[result.name] = "unavailable"
			}
		case <-readinessContext.Done():
			// 即使某个错误实现忽略 context，endpoint 仍在总 timeout 内返回；
			// buffered channel 允许迟到的 goroutine 结束时退出而不阻塞。
			for name := range pending {
				failures[name] = "unavailable"
			}
			pending = nil
		}
	}
	if len(failures) > 0 {
		return Report{Status: "not_ready", Dependencies: failures}
	}
	return Report{Status: "ready"}
}
