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
	// ResourceError identifies a deterministic call or execution
	// resource-limit failure. It remains an ordinary Lua error; the category
	// is additional information for Go callers.
	ResourceError
	// ContextError identifies cancellation or deadline expiry requested by
	// the host. Lua protected calls do not catch it.
	ContextError
	// ExitError identifies an os.exit request returned to the host. Lua
	// protected calls do not catch it.
	ExitError
	// LimitError identifies a host ceiling configured through Options, such
	// as MaxHeapBytes. Unlike ResourceError, which reports a limit Lua 5.1
	// itself defines and a script may legitimately recover from, a host
	// ceiling exists to be enforced against the script: Lua protected calls
	// do not catch it, and it ends the outer operation.
	LimitError
)

// ExitRequest reports that Lua called os.exit.
//
// Lunar never terminates the Go process itself. An os.exit call returns an
// *Error that unwraps to *ExitRequest, allowing the application to apply its
// own process, service, or request-lifecycle policy.
type ExitRequest struct {
	code int
}

// ExitCode returns the status supplied to os.exit.
func (request *ExitRequest) ExitCode() int {
	if request == nil {
		return 0
	}
	return request.code
}

// Error returns a stable host-facing description of the request.
func (request *ExitRequest) Error() string {
	if request == nil {
		return "lua: exit requested"
	}
	return fmt.Sprintf(
		"lua: exit requested with status %d",
		request.code,
	)
}

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

// String renders one traceback entry the way Lua positions a frame:
// "chunk.lua:12: in function 'name'". It never executes Lua and stays valid
// after the owning State closes.
func (entry TraceFrame) String() string {
	location := entry.Source
	if location == "" {
		location = "?"
	} else {
		location = sourceID(location)
	}
	if entry.Line > 0 {
		location = fmt.Sprintf("%s:%d", location, entry.Line)
	}
	switch {
	case entry.Function != "":
		location += fmt.Sprintf(": in function '%s'", entry.Function)
	case entry.Source == "=[Go]":
		location += ": in native function"
	default:
		location += ": in function <anonymous>"
	}
	if entry.TailCalls != 0 {
		location += fmt.Sprintf(
			" (+%d tail call(s))",
			entry.TailCalls,
		)
	}
	return location
}

// Error is a protected execution failure.
//
// It owns the original Lua Value and a compact traceback snapshot. Formatting
// never invokes Lua, tostring, or a metamethod, so Error remains safe after the
// owning State closes.
type Error struct {
	value              Value
	compactValue       slot
	description        string
	traceback          []TraceFrame
	category           ErrorCategory
	hasCompactValue    bool
	sourcePositionable bool
	sourcePositioned   bool
	cause              error
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
	if !err.value.Valid() && err.hasCompactValue {
		return err.compactValue.owningValue()
	}
	return err.value
}

func (err *Error) valueSlot() (slot, bool) {
	if err == nil {
		return slot{}, false
	}
	if err.value.Valid() {
		return slotFromValue(err.value), true
	}
	if err.hasCompactValue {
		return err.compactValue, true
	}
	return slot{}, false
}

func (err *Error) mustValueSlot(owner *runtimeState) slot {
	if owner == nil {
		panic("lua: Lua error has no target runtime")
	}
	if err != nil && err.hasCompactValue {
		// The value never left this execution graph, so its allocation or
		// ingress debt has already been recorded.
		value := err.compactValue
		if acceptErr := owner.acceptSlot(value); acceptErr != nil {
			panic(acceptErr)
		}
		return value
	}
	value, valid := err.valueSlot()
	if !valid {
		panic("lua: invalid Lua error")
	}
	if acceptErr := owner.acceptSlot(value); acceptErr != nil {
		panic(acceptErr)
	}
	return owner.importAcceptedSlot(value)
}

func (err *Error) exposeValue() *Error {
	if err == nil || err.value.Valid() || !err.hasCompactValue {
		return err
	}
	err.value = err.compactValue.owningValue()
	err.compactValue = nilSlot
	err.hasCompactValue = false
	return err
}

func exposeLuaError(err error) error {
	if failure, ok := err.(*Error); ok {
		failure.exposeValue()
	}
	return err
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
	message := fmt.Sprintf(
		"%s:%d: %s",
		sourceID(source),
		line,
		fmt.Sprintf(format, arguments...),
	)
	return &Error{
		value:       errorStringValue(message),
		description: message,
		category:    SyntaxError,
	}
}

func newResourceError(format string, arguments ...any) *Error {
	message := fmt.Sprintf(format, arguments...)
	return &Error{
		value:              errorStringValue(message),
		description:        message,
		category:           ResourceError,
		sourcePositionable: true,
	}
}

// newHeapLimitError reports Options.MaxHeapBytes enforcement. It is
// deliberately not a ResourceError: a script must not be able to catch the
// ceiling that bounds it and keep allocating.
func newHeapLimitError() *Error {
	const message = "heap limit exceeded"
	return &Error{
		value:              errorStringValue(message),
		description:        message,
		category:           LimitError,
		sourcePositionable: true,
	}
}

func newExitError(code int) *Error {
	request := &ExitRequest{code: code}
	message := fmt.Sprintf("exit requested with status %d", code)
	return &Error{
		value:              errorStringValue(message),
		description:        message,
		category:           ExitError,
		sourcePositionable: true,
		cause:              request,
	}
}

func isHostControlFailure(failure *Error) bool {
	if failure == nil {
		return false
	}
	switch failure.category {
	case ContextError, ExitError, LimitError:
		return true
	default:
		return false
	}
}

func (state *State) rememberExitRequest(failure *Error) {
	if state == nil || failure == nil || failure.category != ExitError {
		return
	}
	if state.execution.pendingExit == nil {
		state.execution.pendingExit = failure
	}
}

func executionErrorDescription(
	prototype *Prototype,
	pc int,
	message string,
) string {
	source := sourceID(prototype.SourceName())
	if line := prototype.lineAt(pc); line != 0 {
		return fmt.Sprintf("%s:%d: %s", source, line, message)
	}
	return fmt.Sprintf("%s: %s", source, message)
}

func (err *Error) positionExecutionFailure(
	state *State,
	prototype *Prototype,
	pc int,
) {
	if err == nil ||
		!err.sourcePositionable ||
		err.sourcePositioned ||
		prototype == nil {
		return
	}
	message := executionErrorDescription(prototype, pc, err.description)
	err.value = state.String(message)
	err.compactValue = nilSlot
	err.hasCompactValue = false
	err.description = message
	err.sourcePositioned = true
}

func errorStringValue(message string) Value {
	return stringValue(newStringRef(message))
}

const maxSourceID = 59

func sourceID(source string) string {
	if source == "" {
		return `[string ""]`
	}
	switch source[0] {
	case '=':
		literal := source[1:]
		if len(literal) > maxSourceID {
			literal = literal[:maxSourceID]
		}
		return literal
	case '@':
		const pathTail = 52
		path := source[1:]
		if len(path) <= pathTail {
			return path
		}
		return "..." + path[len(path)-pathTail:]
	default:
		line := source
		truncated := false
		if end := strings.IndexAny(line, "\r\n"); end >= 0 {
			line = line[:end]
			truncated = true
		}
		const prefix = `[string "`
		const suffix = `"]`
		const sourceBytes = 43
		if len(line) > sourceBytes {
			line = line[:sourceBytes]
			truncated = true
		}
		if truncated {
			line += "..."
		}
		return prefix + line + suffix
	}
}
