// Command mendry-migrate 运行显式的数据库迁移入口。
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
	// signal cancellation 会贯穿 advisory lock 等待和 migration transaction，
	// 避免运维终止后命令仍继续获取锁或执行后续 schema 变更。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	lookup, err := config.WithOptionalDotEnv(os.LookupEnv, ".env")
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "mendry-migrate: %v\n", err)
		os.Exit(1)
	}

	err = bootstrap.RunMigrate(ctx, bootstrap.Options{
		Lookup: lookup,
		Output: os.Stdout,
		Build:  buildinfo.Current(),
	})
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "mendry-migrate: %v\n", err)
		os.Exit(1)
	}
}
