package lua

import (
	"context"
	"errors"
)

const contextPollInterval uint16 = 256

// ErrNilContext reports a nil context passed to a context-aware operation.
var ErrNilContext = errors.New("lua: nil context")

// executionContext belongs only to one active public call or resume. It is
// cleared before that operation returns, so a suspended Thread never retains
// the caller's context graph.
type executionContext struct {
	context context.Context
	done    <-chan struct{}
	failure *Error
}

func prepareExecutionContext(
	ctx context.Context,
) (executionContext, *Error) {
	if ctx == nil {
		return executionContext{}, nil
	}
	execution := executionContext{
		context: ctx,
		done:    ctx.Done(),
	}
	if execution.done != nil && contextChannelClosed(execution.done) {
		return executionContext{}, newContextError(ctx, false)
	}
	return execution, nil
}

func (state *State) beginExecutionContext(execution executionContext) {
	if state.execution.context != nil ||
		state.execution.done != nil ||
		state.execution.failure != nil {
		panic("lua: nested public execution context")
	}
	state.execution = execution
}

func (state *State) endExecutionContext() {
	state.execution = executionContext{}
}

func (thread *Thread) resetContextBudget() {
	if thread.state.execution.done == nil {
		thread.contextBudget = 0
		return
	}
	thread.contextBudget = contextPollInterval
}

func (thread *Thread) contextStepDue() bool {
	budget := thread.contextBudget
	if budget == 0 {
		return false
	}
	budget--
	thread.contextBudget = budget
	return budget == 0
}

func pollExecutionContext(thread *Thread) *Error {
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
