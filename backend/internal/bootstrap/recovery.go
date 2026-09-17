package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type recoveryHTTPServer interface{ Run(context.Context) error }
type recoveryBackgroundWorker interface{ Run(context.Context) }
type recoveryExecutor interface{ Close(context.Context) error }

// runWithRecovery stops admission and drains execution ownership before the
// caller closes PostgreSQL, including when the HTTP listener fails to start.
func runWithRecovery(ctx context.Context, server recoveryHTTPServer, worker recoveryBackgroundWorker, executor recoveryExecutor, shutdownTimeout time.Duration) error {
	workerCtx, cancelWorker := context.WithCancel(ctx)
	defer cancelWorker()
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); worker.Run(workerCtx) }()
	serverErr := server.Run(ctx)
	cancelWorker()
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancelShutdown()
	closeErr := executor.Close(shutdownCtx)
	select {
	case <-workerDone:
		return errors.Join(serverErr, closeErr)
	case <-shutdownCtx.Done():
		return errors.Join(serverErr, closeErr, fmt.Errorf("stop remediation recovery worker: %w", shutdownCtx.Err()))
	}
}
