// Package bootstrap 负责组装进程依赖并管理 API 和 migrate 的生命周期。
// 具体业务行为不应进入该包。
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"mendry/backend/internal/platform/buildinfo"
	"mendry/backend/internal/platform/config"
	"mendry/backend/internal/platform/observability"

	"go.opentelemetry.io/otel/trace"
)

// Options 提供 composition root 所需的进程边界依赖。
// 环境读取、日志输出和构建信息均由命令层显式注入，便于测试替换且避免全局状态。
type Options struct {
	Lookup config.Lookup
	Output io.Writer
	Build  buildinfo.Info
}

func logger(options Options, service string, common config.Common) (*slog.Logger, io.Closer, error) {
	sink, err := newLogSink(options.Output, common.LogFile)
	if err != nil {
		return nil, nil, err
	}
	logger, err := observability.NewLogger(observability.LoggerOptions{
		Writer:      options.Output,
		FileWriter:  sink.file,
		Level:       common.LogLevel,
		Format:      common.LogFormat,
		Service:     service,
		Environment: common.Environment,
		Build:       options.Build,
	})
	if err != nil {
		return nil, nil, errors.Join(fmt.Errorf("create logger: %w", err), sink.Close())
	}
	return logger, sink, nil
}

type logSink struct {
	file *os.File
}

// newLogSink 在 logger 构造前打开可选文件，确保路径或权限失败不会留下
// 已启动的依赖；stdout 与文件由独立 handler 格式化，保留终端颜色判断。
func newLogSink(output io.Writer, path string) (*logSink, error) {
	if output == nil {
		return nil, fmt.Errorf("log writer is required")
	}
	if path == "" {
		return &logSink{}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		return nil, errors.Join(fmt.Errorf("set log file permissions: %w", err), file.Close())
	}
	return &logSink{file: file}, nil
}

func (s *logSink) Close() error {
	if s == nil || s.file == nil {
		return nil
	}
	return s.file.Close()
}

func closeLogSink(result *error, sink io.Closer) {
	if result == nil || sink == nil {
		return
	}
	*result = errors.Join(*result, sink.Close())
}

func logStart(ctx context.Context, logger *slog.Logger, component string) {
	observability.Log(ctx, logger, slog.LevelInfo, observability.EventProcessStarting, "process starting",
		slog.String(observability.FieldComponent, component),
	)
}

func logStarted(ctx context.Context, logger *slog.Logger, component string) {
	observability.Log(ctx, logger, slog.LevelInfo, observability.EventProcessStarted, "process started",
		slog.String(observability.FieldComponent, component),
	)
}

func logStopped(ctx context.Context, logger *slog.Logger, component string) {
	// 进程停止时根 context 通常已经取消；移除取消状态但保留 trace/span 值，
	// 既确保最终日志可写入，也不会丢失 correlation。
	observability.Log(context.WithoutCancel(ctx), logger, slog.LevelInfo, observability.EventProcessStopped, "process stopped",
		slog.String(observability.FieldComponent, component),
	)
}

func telemetry(ctx context.Context, options Options, service, environment string) (*observability.Telemetry, error) {
	runtime, err := observability.NewTelemetry(ctx, observability.TelemetryOptions{
		Service:     service,
		Environment: environment,
		Build:       options.Build,
	})
	if err != nil {
		return nil, fmt.Errorf("create telemetry: %w", err)
	}
	return runtime, nil
}

func shutdownTelemetry(runtime *observability.Telemetry, timeout time.Duration) error {
	// 根 context 可能因 signal 已取消；flush 使用独立 stage budget，避免正常退出
	// 直接丢弃最后一批 span 和 metric。
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return runtime.Shutdown(ctx)
}

func finishProcess(ctx context.Context, span trace.Span, runtime *observability.Telemetry, timeout time.Duration, processError error) error {
	span.End()
	return errors.Join(processError, shutdownTelemetry(runtime, timeout))
}
