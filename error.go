package lua

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
