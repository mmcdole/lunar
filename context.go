package lua

import (
	"context"
	"errors"
)

const contextPollInterval uint16 = 256

// ErrNilContext reports a nil context passed to SetContext.
var ErrNilContext = errors.New("lua: nil context")

// executionControl belongs only to one active public call or resume.
// pendingExit makes the first os.exit request terminal across nested native
// calls, even if a Go callback ignores a returned error. The record is
// cleared before the public operation returns.
type executionControl struct {
	failure     *Error
	pendingExit *Error
}

// SetContext installs the context the runtime observes while Lua executes.
//
// The context is ambient: it outlives one call and applies to every thread of
// the State until SetContext replaces it or RemoveContext clears it.
// Cancellation stops execution and surfaces as a *Error in the ContextError
// category, so Lua pcall cannot catch it and a script cannot outlast it.
//
// The runtime observes cancellation at bounded safe points between
// instructions and around native calls, never inside one, so cancellation
// cannot preempt host code that is already running; a long-running callback
// observes its own Context. Loading also polls while reading, compiling, and
// decoding.
//
// SetContext takes effect immediately, including when a native callback
// installs a new deadline during the call it is running under. It is a State
// operation and must be serialized like any other; a host deciding to cancel
// from another goroutine does so through its own context.
//
//	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
//	defer cancel()
//	state.SetContext(ctx)
//	defer state.RemoveContext()
//	results, err := state.Call(handler)
//
// A context left installed applies to later operations. A host that cancels
// per operation clears it when that operation completes.
func (state *State) SetContext(ctx context.Context) error {
	if err := state.checkOpen(); err != nil {
		return err
	}
	if ctx == nil {
		return ErrNilContext
	}
	state.ambient = ctx
	state.ambientDone = ctx.Done()
	state.armExecutingThread()
	return nil
}

// RemoveContext clears the installed context. Execution already stopped by
// cancellation is not resumed.
func (state *State) RemoveContext() error {
	if err := state.checkOpen(); err != nil {
		return err
	}
	state.ambient = nil
	state.ambientDone = nil
	state.armExecutingThread()
	return nil
}

// armExecutingThread refreshes the polling budget so a context installed or
// cleared from a native callback takes effect during the call that changed it.
//
// Only an executing thread is touched. An idle State arms its budget at call
// entry, and leaving a ready main thread with a polling budget would break the
// invariant that it retains no execution state.
func (state *State) armExecutingThread() {
	if state.active != nil {
		state.active.resetContextBudget()
	}
}

// admitExecutionContext rejects an already-cancelled context before an
// operation starts, so lifecycle and ownership failures keep reporting first.
//
// An installed context outlives the operation that motivated it, so a State
// whose context was cancelled refuses every later operation until the host
// clears it. The failure reports the host's own cause unchanged; SetContext
// documents the lifetime that produced it.
func (state *State) admitExecutionContext() *Error {
	if state.ambientDone == nil ||
		!contextChannelClosed(state.ambientDone) {
		return nil
	}
	return newContextError(state.ambient, false)
}

func (state *State) endExecution() {
	state.execution = executionControl{}
}

func (thread *threadObject) resetContextBudget() {
	if thread.state.ambientDone == nil {
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
	state := thread.state
	if state.ambientDone == nil ||
		!contextChannelClosed(state.ambientDone) {
		return nil
	}
	if state.execution.failure == nil {
		state.execution.failure = newContextError(state.ambient, true)
	}
	return state.execution.failure
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
// It is the context installed with SetContext, or context.Background when none
// is installed. The Context may be retained after the callback returns; the
// borrowed Frame may not.
func (frame Frame) Context() context.Context {
	frame.activation()
	ctx := frame.thread.state.ambient
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
