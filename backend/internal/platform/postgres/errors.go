package postgres

import (
	"context"
	"errors"

	"mendry/backend/internal/platform/errtrace"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	errorClassCanceled      = "canceled"
	errorClassTimeout       = "timeout"
	errorClassConflict      = "conflict"
	errorClassConstraint    = "constraint"
	errorClassSerialization = "serialization"
	errorClassDeadlock      = "deadlock"
	errorClassUnavailable   = "unavailable"
	errorClassInternal      = "internal"
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

	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "40001":
			return errorClassSerialization
		case "40P01":
			return errorClassDeadlock
		case "23505":
			return errorClassConflict
		case "23502", "23503", "23514", "23P01":
			return errorClassConstraint
		case "08000", "08001", "08003", "08004", "08006", "08007", "08P01", "57P01", "57P02", "57P03":
			return errorClassUnavailable
		}
	}
	return errorClassInternal
}

func retryableTransactionError(err error) bool {
	classification := classifyError(err)
	return classification == errorClassSerialization || classification == errorClassDeadlock
}
