// Command mendry-bootstrap-admin 幂等创建首个本地管理员账号。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"mendry/backend/internal/bootstrap"
	"mendry/backend/internal/platform/buildinfo"
	"mendry/backend/internal/platform/config"
)

func main() {
	username := flag.String("username", "", "normalized administrator username")
	flag.Parse()
	if *username == "" || flag.NArg() != 0 {
		_, _ = fmt.Fprintln(os.Stderr, "usage: mendry-bootstrap-admin --username <username>")
		os.Exit(2)
	}

	lookup, err := config.WithOptionalDotEnv(os.LookupEnv, ".env")
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "mendry-bootstrap-admin: %v\n", err)
		os.Exit(1)
	}

	passwordValue, ok := lookup(config.BootstrapAdminPasswordKey)
	if !ok || passwordValue == "" {
		_, _ = fmt.Fprintf(os.Stderr, "mendry-bootstrap-admin: %s is required\n", config.BootstrapAdminPasswordKey)
		os.Exit(2)
	}
	password := []byte(passwordValue)
	passwordValue = ""
	defer clear(password)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err = bootstrap.RunBootstrapAdmin(ctx, bootstrap.BootstrapAdminOptions{
		Options:  bootstrap.Options{Lookup: lookup, Output: os.Stdout, Build: buildinfo.Current()},
		Username: *username,
		Password: password,
	})
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "mendry-bootstrap-admin: %v\n", err)
		os.Exit(1)
	}
}
