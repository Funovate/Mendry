package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"mendry/backend/internal/modules/remediation/domain"
)

func TestExecutionPersistenceSurvivesRequestCancellationButNotSessionLoss(t *testing.T) {
	locks := &executionTestStore{}
	executor := testExecutor(t, locks, 1)
	executor.checkInterval = time.Millisecond
	request, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()
	persisting := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := executor.Execute(request, "run", func(ctx context.Context) (domain.Run, error) {
			cancelRequest()
			persistence, cancel := executionPersistenceContext(ctx)
			defer cancel()
			if persistence.Err() != nil {
				close(persisting)
				return domain.Run{}, errors.New("request cancellation canceled terminal persistence")
			}
			close(persisting)
			select {
			case <-persistence.Done():
				return domain.Run{}, context.Cause(persistence)
			case <-time.After(2 * time.Second):
				return domain.Run{}, errors.New("lock monitoring stopped when request was canceled")
			}
		})
		done <- err
	}()
	receiveExecution(t, persisting)
	locks.mu.Lock()
	locks.checkErr = errors.New("session disconnected")
	locks.mu.Unlock()
	if err := receiveExecution(t, done); !errors.Is(err, ErrRunExecutionInterrupted) {
		t.Fatalf("persistence result = %v", err)
	}
}
