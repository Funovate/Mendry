package postgres

import (
	"errors"
	"strings"
	"testing"

	"fixthe/backend/internal/platform/errtrace"
)

func repositoryErrorForStackTest() error {
	return newRepositoryError("test project query", errors.New("root cause"))
}

func TestRepositoryErrorCapturesWrappingLocation(t *testing.T) {
	trace, ok := errtrace.FromError(repositoryErrorForStackTest())
	if !ok {
		t.Fatal("repositoryError did not expose a captured stack")
	}
	formatted := trace.String()
	if !strings.Contains(formatted, "postgres.repositoryErrorForStackTest") ||
		!strings.Contains(formatted, "repository_stack_test.go:") {
		t.Fatalf("trace = %q", formatted)
	}
}
