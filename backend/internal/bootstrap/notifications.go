package bootstrap

import (
	"context"
	"log/slog"
	notificationapplication "mendry/backend/internal/modules/notifications/application"
	"sync"
)

type notificationRecoveryWorkers struct {
	recovery      recoveryBackgroundWorker
	notifications *notificationapplication.Service
	logger        *slog.Logger
}

func (w *notificationRecoveryWorkers) Run(ctx context.Context) {
	var group sync.WaitGroup
	group.Add(2)
	go func() { defer group.Done(); w.recovery.Run(ctx) }()
	go func() {
		defer group.Done()
		w.notifications.Run(ctx, func(reason string) { w.logger.WarnContext(ctx, "notification worker", slog.String("reason", reason)) })
	}()
	group.Wait()
}
