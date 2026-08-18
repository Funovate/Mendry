package errtrace

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

type tracedError struct {
	message string
	cause   error
	trace   Trace
}

func (e tracedError) Error() string     { return e.message }
func (e tracedError) Unwrap() error     { return e.cause }
func (e tracedError) StackTrace() Trace { return e.trace }

func captureTestCaller() Trace {
	return Capture(0)
}

func TestCaptureFormatsCallerFunctionFileAndLine(t *testing.T) {
	formatted := captureTestCaller().String()
	for _, expected := range []string{"errtrace.captureTestCaller", "trace_test.go:"} {
		if !strings.Contains(formatted, expected) {
			t.Fatalf("trace %q does not contain %q", formatted, expected)
		}
	}
}

func TestFromErrorSelectsDeepestCapturedCause(t *testing.T) {
	deepestTrace := Trace{3}
	deepest := tracedError{message: "deepest", cause: errors.New("root"), trace: deepestTrace}
	middle := fmt.Errorf("middle: %w", deepest)
	outer := tracedError{message: "outer", cause: middle, trace: Trace{1}}

	actual, ok := FromError(outer)
	if !ok || len(actual) != 1 || actual[0] != deepestTrace[0] {
		t.Fatalf("FromError() = %#v, %t", actual, ok)
	}
}

func TestFromErrorTraversesJoinedCauses(t *testing.T) {
	want := Trace{7}
	joined := errors.Join(errors.New("first"), tracedError{message: "second", trace: want})

	actual, ok := FromError(joined)
	if !ok || len(actual) != 1 || actual[0] != want[0] {
		t.Fatalf("FromError() = %#v, %t", actual, ok)
	}
}

func TestFromErrorRejectsMissingTrace(t *testing.T) {
	if trace, ok := FromError(fmt.Errorf("wrapper: %w", errors.New("root"))); ok || trace != nil {
		t.Fatalf("FromError() = %#v, %t", trace, ok)
	}
}
