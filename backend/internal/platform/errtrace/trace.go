package errtrace

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
)

const (
	maximumFrames     = 64
	maximumErrorNodes = 64
)

// Trace 保存 error 包装时捕获的程序计数器序列。
type Trace []uintptr

// Capture 捕获调用方栈；skip 表示在 Capture 调用方之上额外跳过的帧数。
func Capture(skip int) Trace {
	if skip < 0 {
		skip = 0
	}
	programCounters := make([]uintptr, maximumFrames)
	count := runtime.Callers(skip+2, programCounters)
	return Trace(programCounters[:count:count])
}

// String 将栈格式化为包含 Go 函数名、源文件和行号的帧序列。
func (trace Trace) String() string {
	if len(trace) == 0 {
		return ""
	}

	frames := runtime.CallersFrames([]uintptr(trace))
	var formatted strings.Builder
	for {
		frame, more := frames.Next()
		if formatted.Len() > 0 {
			formatted.WriteByte('\n')
		}
		if frame.Function != "" {
			formatted.WriteString(frame.Function)
			formatted.WriteByte('\n')
		}
		_, _ = fmt.Fprintf(&formatted, "\t%s:%d", frame.File, frame.Line)
		if !more {
			break
		}
	}
	return formatted.String()
}

// Provider 由在包装时保存了调用栈的 error 实现。
type Provider interface {
	StackTrace() Trace
}

// FromError 有界遍历 error graph，并返回层级最深的已捕获栈。
func FromError(root error) (Trace, bool) {
	if root == nil {
		return nil, false
	}

	type node struct {
		err   error
		depth int
	}
	pending := []node{{err: root}}
	deepest := -1
	var selected Trace

	for visited := 0; len(pending) > 0 && visited < maximumErrorNodes; visited++ {
		current := pending[0]
		pending = pending[1:]
		if current.err == nil {
			continue
		}

		if provider, ok := current.err.(Provider); ok {
			trace := provider.StackTrace()
			if len(trace) > 0 && current.depth > deepest {
				deepest = current.depth
				selected = trace
			}
		}

		for _, cause := range unwrap(current.err) {
			pending = append(pending, node{err: cause, depth: current.depth + 1})
		}
	}

	return selected, len(selected) > 0
}

func unwrap(err error) []error {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		return joined.Unwrap()
	}
	if cause := errors.Unwrap(err); cause != nil {
		return []error{cause}
	}
	return nil
}
