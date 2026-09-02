package redis

import (
	"context"
	"errors"
	"net"

	"fixthe/backend/internal/platform/errtrace"

	redisclient "github.com/redis/go-redis/v9"
)

const (
	errorClassCanceled    = "canceled"
	errorClassTimeout     = "timeout"
	errorClassNotFound    = "not_found"
	errorClassUnavailable = "unavailable"
	errorClassInternal    = "internal"
)

type safeError struct {
	operation string
	cause     error
	stack     errtrace.Trace
}

func newSafeError(operation string, cause error) safeError {
	return safeError{operation: operation, cause: cause, stack: errtrace.Capture(1)}
}

func (e safeError) Error() string {
	return e.operation + " failed"
}

func (e safeError) Unwrap() error {
	return e.cause
}

func (e safeError) StackTrace() errtrace.Trace {
	return e.stack
}

func classifyError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return errorClassCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errorClassTimeout
	}
	if errors.Is(err, redisclient.Nil) {
		return errorClassNotFound
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		if networkError.Timeout() {
			return errorClassTimeout
		}
		return errorClassUnavailable
	}
	return errorClassInternal
}
