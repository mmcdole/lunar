package lua

import (
	"context"
	"errors"
)

const contextPollInterval uint16 = 256

// ErrNilContext reports a nil context passed to a context-aware operation.
var ErrNilContext = errors.New("lua: nil context")

// executionControl belongs only to one active public call or resume. Context
// fields are populated only for context-aware entry points. pendingExit makes
// the first os.exit request terminal across nested native calls, even if a Go
// callback ignores a returned error. The whole record is cleared before the
// public operation returns.
type executionControl struct {
	context     context.Context
	done        <-chan struct{}
	failure     *Error
	pendingExit *Error
}

func prepareExecutionContext(
	ctx context.Context,
) (executionControl, *Error) {
	if ctx == nil {
		return executionControl{}, nil
	}
	execution := executionControl{
		context: ctx,
		done:    ctx.Done(),
	}
	if execution.done != nil && contextChannelClosed(execution.done) {
		return executionControl{}, newContextError(ctx, false)
	}
	return execution, nil
}

func (state *State) beginExecutionContext(execution executionControl) {
	if state.execution.context != nil ||
		state.execution.done != nil ||
		state.execution.failure != nil ||
		state.execution.pendingExit != nil {
		panic("lua: nested public execution context")
	}
	state.execution = execution
}

func (state *State) endExecutionContext() {
	state.execution = executionControl{}
}

func (thread *threadObject) resetContextBudget() {
	if thread.state.execution.done == nil {
		thread.contextBudget = 0
		return
	}
	thread.contextBudget = contextPollInterval
}

func (thread *threadObject) contextStepDue() bool {
	budget := thread.contextBudget
	if budget == 0 {
		return false
	}
	budget--
	thread.contextBudget = budget
	return budget == 0
}

func pollExecutionContext(thread *threadObject) *Error {
	thread.resetContextBudget()
	execution := &thread.state.execution
	if execution.done == nil || !contextChannelClosed(execution.done) {
		return nil
	}
	if execution.failure == nil {
		execution.failure = newContextError(execution.context, true)
	}
	return execution.failure
}

func contextChannelClosed(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

func newContextError(
	ctx context.Context,
	positionable bool,
) *Error {
	status := ctx.Err()
	if status == nil {
		status = context.Canceled
	}
	explanation := context.Cause(ctx)
	if explanation == nil {
		explanation = status
	}
	cause := explanation
	if !errors.Is(explanation, status) {
		cause = errors.Join(status, explanation)
	}
	message := explanation.Error()
	return &Error{
		value:              errorStringValue(message),
		description:        message,
		category:           ContextError,
		sourcePositionable: positionable,
		cause:              cause,
	}
}

// Context returns the context governing this native callback.
//
// Context-aware calls return the exact context supplied by their caller.
// Ordinary calls return context.Background. The Context may be retained after
// the callback returns; the borrowed Frame may not.
func (frame Frame) Context() context.Context {
	frame.activation()
	ctx := frame.thread.state.execution.context
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
