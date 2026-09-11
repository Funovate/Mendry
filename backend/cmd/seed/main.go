// Command mendry-seed 幂等写入显式的本地开发项目和事故数据。
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	lookup, err := config.WithOptionalDotEnv(os.LookupEnv, ".env")
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "mendry-seed: %v\n", err)
		os.Exit(1)
	}

	err = bootstrap.RunSeed(ctx, bootstrap.Options{
		Lookup: lookup,
		Output: os.Stdout,
		Build:  buildinfo.Current(),
	})
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "mendry-seed: %v\n", err)
		os.Exit(1)
	}
}
