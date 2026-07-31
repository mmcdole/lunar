package lua

// SetInterrupt installs a callback the runtime consults while Lua executes.
// Returning a non-nil error stops execution and surfaces that error to the
// host as a *Error in the ContextError category, so Lua pcall cannot catch
// it and a script cannot defeat the interrupt.
//
// The interrupt is ambient: it outlives one call and applies to every
// thread of the State until RemoveInterrupt replaces it. That is what
// distinguishes it from the context-aware methods, whose cancellation is
// captured when the call begins and cannot be re-armed or detached while
// that call runs.
//
// The callback runs on the executing goroutine at the runtime's polling
// points, under the State's single-executor contract, and must not reenter
// the State. It is consulted between instructions and around native calls,
// never inside one, so an interrupt cannot preempt host code that is
// already running; long-running callbacks observe cancellation themselves.
//
// SetInterrupt is itself a State operation and must be serialized like any
// other. A host that decides to interrupt from another goroutine publishes
// that decision through its own synchronization, typically an atomic flag
// the callback reads:
//
//	var cancelled atomic.Bool
//	state.SetInterrupt(func() error {
//		if cancelled.Load() {
//			return errCancelled
//		}
//		return nil
//	})
func (state *State) SetInterrupt(interrupt func() error) error {
	if err := state.checkOpen(); err != nil {
		return err
	}
	if interrupt == nil {
		return state.RemoveInterrupt()
	}
	state.interrupt = interrupt
	state.armExecutingThread()
	return nil
}

// RemoveInterrupt clears the installed interrupt. Execution already stopped
// by an interrupt is not resumed.
func (state *State) RemoveInterrupt() error {
	if err := state.checkOpen(); err != nil {
		return err
	}
	state.interrupt = nil
	state.armExecutingThread()
	return nil
}

// armExecutingThread refreshes the polling budget so an interrupt changed
// from a native callback takes effect during the call that changed it.
//
// Only an executing thread is touched. An idle State arms its budget at
// call entry, and leaving a ready main thread with a polling budget would
// break the invariant that it retains no execution state.
func (state *State) armExecutingThread() {
	if state.active != nil {
		state.active.resetContextBudget()
	}
}

// pollInterrupt consults the installed interrupt. A refusal is reported in
// the ContextError category so it shares cancellation's contract: hosts see
// the original error through errors.Is and errors.As, and Lua cannot catch
// it.
//
// Unlike cancellation this does not memoize into executionControl, which
// only ordinary context-aware calls establish and clear. An interrupt
// applies to calls that install no context at all, so it must not leave
// state behind for the next one to trip over.
func pollInterrupt(state *State) *Error {
	if state == nil || state.interrupt == nil {
		return nil
	}
	refusal := state.interrupt()
	if refusal == nil {
		return nil
	}
	return newInterruptError(refusal)
}

func newInterruptError(refusal error) *Error {
	message := refusal.Error()
	return &Error{
		value:              errorStringValue(message),
		description:        message,
		category:           ContextError,
		sourcePositionable: true,
		cause:              refusal,
	}
}
