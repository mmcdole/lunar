package lua

import "fmt"

// ResultCapacityError reports that CallInto produced more results than its
// destination can hold.
//
// Required is the exact result count produced by Lua. Available is the length
// of the supplied destination. Lua side effects have already occurred, but
// the destination remains unchanged.
type ResultCapacityError struct {
	Required  int
	Available int
}

// Error returns a stable description of the insufficient result capacity.
func (err *ResultCapacityError) Error() string {
	if err == nil {
		return "lua: insufficient result capacity"
	}
	return fmt.Sprintf(
		"lua: call produced %d results; destination holds %d",
		err.Required,
		err.Available,
	)
}

// Call invokes callable in protected mode on state's main Thread.
//
// Callable may be a Function or a value with a Function-valued __call
// metamethod. The returned slice and its Values are owned by the caller and
// remain valid across later calls and after State.Close. A call with no
// results returns a nil slice.
//
// Invalid or foreign Values and a State already executing are rejected before
// Lua runs. Lua failures are returned as *Error. A panic from a NativeFunc is
// propagated after the State has been restored to a callable state.
func (state *State) Call(
	callable Value,
	arguments ...Value,
) ([]Value, error) {
	results, _, err := state.callMain(
		callable,
		arguments,
		nil,
		true,
	)
	return results, err
}

// CallInto invokes callable in protected mode on state's main Thread and
// writes its results into destination.
//
// Arguments are copied before execution, so arguments and destination may
// overlap. On success, count entries are written and the destination tail is
// unchanged. On a Lua failure or ingress error, destination is unchanged and
// count is zero. If Lua produces more than len(destination) results, count is
// the required size and the returned *ResultCapacityError describes the
// shortfall; destination is still unchanged. Lua side effects completed before
// that result-count check are not rolled back. Panics from NativeFunc behave as
// documented by Call.
func (state *State) CallInto(
	callable Value,
	arguments []Value,
	destination []Value,
) (count int, err error) {
	_, count, err = state.callMain(
		callable,
		arguments,
		destination,
		false,
	)
	return count, err
}

func (state *State) callMain(
	callable Value,
	arguments []Value,
	destination []Value,
	allocateResults bool,
) (owned []Value, count int, err error) {
	thread, err := state.prepareMainCall(callable, arguments)
	if err != nil {
		return nil, 0, err
	}

	thread.status = ThreadRunning
	defer thread.resetMainCall()

	required := 1 + len(arguments)
	thread.reserveValues(required)
	writeSlot(&thread.values[0], slotFromValue(callable))
	for index, argument := range arguments {
		writeSlot(
			&thread.values[index+1],
			slotFromValue(argument),
		)
	}
	thread.top = required

	if failure := thread.startMainCall(); failure != nil {
		return nil, 0, failure
	}
	result := execute(thread, 0)
	if result.kind == executionFailed {
		return nil, 0, result.err
	}
	if result.kind != executionReturned ||
		len(thread.frames) != 0 ||
		len(thread.continuations) != 0 ||
		thread.openUpvalues != nil ||
		thread.frameExtent != 0 {
		panic("lua: executor returned an invalid main-thread state")
	}

	count = thread.top
	if allocateResults {
		if count == 0 {
			return nil, 0, nil
		}
		owned = make([]Value, count)
		for index := range owned {
			owned[index] = thread.values[index].owningValue()
		}
		return owned, count, nil
	}
	if count > len(destination) {
		return nil, count, &ResultCapacityError{
			Required:  count,
			Available: len(destination),
		}
	}
	for index := 0; index < count; index++ {
		destination[index] = thread.values[index].owningValue()
	}
	return nil, count, nil
}

func (state *State) prepareMainCall(
	callable Value,
	arguments []Value,
) (*Thread, error) {
	if err := state.checkOpen(); err != nil {
		return nil, err
	}
	thread := state.main
	if thread == nil || thread.status != ThreadReady {
		return nil, ErrRunning
	}
	if thread.top != 0 ||
		thread.frameExtent != 0 ||
		len(thread.frames) != 0 ||
		len(thread.continuations) != 0 ||
		thread.openUpvalues != nil ||
		thread.activeNativeToken != 0 {
		panic("lua: ready main thread retains execution state")
	}
	if err := state.runtime.accept(callable); err != nil {
		return nil, err
	}
	for _, argument := range arguments {
		if err := state.runtime.accept(argument); err != nil {
			return nil, err
		}
	}
	if len(arguments) >= state.options.MaxValues ||
		uint64(len(arguments))+1 > uint64(^uint32(0)) {
		return nil, newResourceError(
			"value stack limit of %d exceeded",
			state.options.MaxValues,
		)
	}
	return thread, nil
}

func (thread *Thread) startMainCall() *Error {
	callable := thread.values[0]
	if function, direct := functionSlot(callable); direct {
		return thread.pushFunctionCall(
			function,
			0,
			thread.top-1,
			allResults,
		)
	}
	if function := callMetamethodFunction(thread, callable); function != nil {
		return thread.pushFunctionMetamethodCall(
			function,
			0,
			thread.top-1,
			allResults,
		)
	}
	message := fmt.Sprintf(
		"attempt to call a %s value",
		callable.kind(),
	)
	return &Error{
		value:       thread.state.String(message),
		description: message,
		category:    RuntimeError,
	}
}

func (thread *Thread) resetMainCall() {
	extent := thread.liveValueExtent()
	if len(thread.frames) != 0 {
		thread.unwindCalls(0)
	}
	thread.closeUpvalues(0)
	for index := range thread.frames {
		thread.frames[index] = activation{}
	}
	thread.frames = thread.frames[:0]
	for index := range thread.continuations {
		thread.continuations[index] = executionContinuation{}
	}
	thread.continuations = thread.continuations[:0]
	thread.top = 0
	thread.frameExtent = 0
	thread.activeNativeToken = 0
	thread.clearInactive(0, extent)
	thread.status = ThreadReady
}
