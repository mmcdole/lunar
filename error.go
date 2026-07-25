package lua

import (
	"fmt"
	"strings"
)

// ErrorCategory classifies an Error without replacing its arbitrary Lua error
// Value.
type ErrorCategory uint8

const (
	// RuntimeError identifies an error raised while executing Lua.
	RuntimeError ErrorCategory = iota
	// SyntaxError identifies source or bytecode rejected before execution.
	SyntaxError
	// ResourceError identifies an execution resource-limit failure.
	ResourceError
	// ContextError identifies cancellation or deadline expiry.
	ContextError
)

// TraceFrame is an immutable source-level traceback entry.
type TraceFrame struct {
	// Source is the source identifier recorded by the Prototype.
	Source string
	// Function is the best available Lua function name.
	Function string
	// Line is the one-based source line, or zero when unavailable.
	Line int
	// TailCalls is the number of frames eliminated immediately below this
	// surviving frame by proper tail calls.
	TailCalls uint32
}

// Error is a protected Lua failure.
//
// It owns the original Lua Value and a compact traceback snapshot. Formatting
// never invokes Lua, tostring, or a metamethod, so Error remains safe after the
// owning State closes.
type Error struct {
	value       Value
	description string
	traceback   []TraceFrame
	category    ErrorCategory
	cause       error
}

// Error returns a stable non-executing description.
func (err *Error) Error() string {
	if err == nil {
		return "<nil>"
	}
	if err.description != "" {
		return err.description
	}
	return "lua error"
}

// Unwrap returns the underlying Go cause, if any.
func (err *Error) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.cause
}

// Value returns the arbitrary Lua error Value.
func (err *Error) Value() Value {
	if err == nil {
		return Value{}
	}
	return err.value
}

// Category returns the broad error category.
func (err *Error) Category() ErrorCategory {
	if err == nil {
		return RuntimeError
	}
	return err.category
}

// Traceback returns an owned copy of the traceback.
func (err *Error) Traceback() []TraceFrame {
	if err == nil || len(err.traceback) == 0 {
		return nil
	}
	return append([]TraceFrame(nil), err.traceback...)
}

func newSourceSyntaxError(
	source string,
	line uint32,
	format string,
	arguments ...any,
) *Error {
	return &Error{
		value: Nil(),
		description: fmt.Sprintf(
			"%s:%d: %s",
			sourceID(source),
			line,
			fmt.Sprintf(format, arguments...),
		),
		category: SyntaxError,
	}
}

func newResourceError(format string, arguments ...any) *Error {
	return &Error{
		value:       Nil(),
		description: fmt.Sprintf(format, arguments...),
		category:    ResourceError,
	}
}

const maxSourceID = 59

func sourceID(source string) string {
	if source == "" {
		return "?"
	}
	switch source[0] {
	case '=':
		return truncateSource(source[1:], maxSourceID, false)
	case '@':
		const pathTail = 52
		path := source[1:]
		if len(path) <= pathTail {
			return path
		}
		return "..." + path[len(path)-pathTail:]
	default:
		line := source
		endedAtNewline := false
		if end := strings.IndexAny(line, "\r\n"); end >= 0 {
			line = line[:end]
			endedAtNewline = true
		}
		const prefix = `[string "`
		const suffix = `"]`
		available := maxSourceID - len(prefix) - len(suffix)
		if endedAtNewline && len(line)+3 <= available {
			line += "..."
		} else if endedAtNewline || len(line) > available {
			line = truncateSource(line, available, true)
		}
		return prefix + line + suffix
	}
}

func truncateSource(source string, limit int, ellipsis bool) string {
	if len(source) <= limit {
		return source
	}
	if !ellipsis || limit < 3 {
		return source[:limit]
	}
	return source[:limit-3] + "..."
}
