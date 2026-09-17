// Command mendry-api 启动对外提供 HTTP 服务的 API 进程。
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"mendry/backend/internal/bootstrap"
	"mendry/backend/internal/platform/buildinfo"
	"mendry/backend/internal/platform/config"
)

func main() {
	// 将 SIGINT 和 SIGTERM 统一转换为根 context 的取消信号，让下层按同一条
	// 生命周期链路停止接收请求并完成有界 drain。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	lookup, err := config.WithOptionalDotEnv(os.LookupEnv, ".env")
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "mendry-api: %v\n", err)
		os.Exit(1)
	}

	err = bootstrap.RunAPI(ctx, bootstrap.Options{
		Lookup: lookup,
		Output: os.Stdout,
		Build:  buildinfo.Current(),
	})
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "mendry-api: %v\n", err)
		os.Exit(1)
	}
}
