package lua

import (
	"errors"
	"fmt"
)

// ErrInvalidResultCount reports a fixed result count outside the supported
// range for CallN or Frame.CallN.
var ErrInvalidResultCount = errors.New("lua: invalid result count")

// ResultCapacityError reports that an Into operation produced more results
// than its destination can hold.
//
// Required is the exact result count produced by Lua. Available is the length
// of the supplied destination. Lua side effects have already occurred, but
// the destination remains unchanged. Results returns the completed values.
type ResultCapacityError struct {
	Required  int
	Available int
	results   []Value
}

// Error returns a stable description of the insufficient result capacity.
func (err *ResultCapacityError) Error() string {
	if err == nil {
		return "lua: insufficient result capacity"
	}
	return fmt.Sprintf(
		"lua: operation produced %d results; destination holds %d",
		err.Required,
		err.Available,
	)
}

// Results returns a caller-owned copy of the completed results.
//
// The Values remain valid across later operations and after State.Close.
func (err *ResultCapacityError) Results() []Value {
	if err == nil || len(err.results) == 0 {
		return nil
	}
	results := make([]Value, len(err.results))
	copy(results, err.results)
	return results
}

func newResultCapacityError(
	results []slot,
	available int,
) *ResultCapacityError {
	owned := make([]Value, len(results))
	for index := range results {
		owned[index] = results[index].owningValue()
	}
	return &ResultCapacityError{
		Required:  len(results),
		Available: available,
		results:   owned,
	}
}

// Call invokes callable in protected mode on state's main Thread.
//
// Callable may be a Function or a value with a Function-valued __call
// metamethod. The returned slice and its Values are owned by the caller and
// remain valid across later calls and after State.Close. A call with no
// results returns a nil slice.
//
// Call and the other State.Call* methods are outer entry points and require an
// idle State. A NativeFunc that calls Lua synchronously, including through
// helper or application code, must pass its borrowed Frame along and use the
// matching Frame.Call* method. Calling through this State or frame.State()
// while the callback is active returns ErrRunning.
//
// Invalid or foreign Values are rejected before Lua runs. Execution failures
// are returned as *Error. An ExitError unwraps to *ExitRequest and asks the
// host to apply its own lifecycle policy; Lunar neither closes the State nor
// terminates the process. A panic from a NativeFunc is propagated after the
// State has been restored to a callable state.
func (state *State) Call(
	callable Value,
	arguments ...Value,
) ([]Value, error) {
	results, _, err := state.callMain(
		callable,
		arguments,
		nil,
		true,
		allResults,
	)
	return results, err
}

// CallN invokes callable like Call and applies Lua's fixed-result adjustment.
//
// Exactly resultCount owning Values are returned. Missing results are padded
// with Nil and extra results are discarded. A zero resultCount executes the
// call and returns a nil slice. An unsupported resultCount returns
// ErrInvalidResultCount before Lua executes.
func (state *State) CallN(
	callable Value,
	resultCount int,
	arguments ...Value,
) ([]Value, error) {
	thread, err := state.prepareMainCall(callable, arguments)
	if err != nil {
		return nil, err
	}
	if err := validateFixedResultCount(resultCount); err != nil {
		return nil, err
	}
	results, _, err := state.runMainCall(
		thread,
		callable,
		arguments,
		nil,
		true,
		resultCount,
	)
	return results, err
}

// CallOne invokes callable like Call and applies Lua's one-result adjustment.
//
// A call with no results returns Nil. If callable returns several results,
// only the first is retained.
func (state *State) CallOne(
	callable Value,
	arguments ...Value,
) (Value, error) {
	var destination [1]Value
	_, _, err := state.callMain(
		callable,
		arguments,
		destination[:],
		false,
		1,
	)
	if err != nil {
		return Value{}, err
	}
	return destination[0], nil
}

// CallDiscard invokes callable like Call and discards all results.
func (state *State) CallDiscard(
	callable Value,
	arguments ...Value,
) error {
	_, _, err := state.callMain(
		callable,
		arguments,
		nil,
		false,
		0,
	)
	return err
}

// CallInto invokes callable in protected mode on state's main Thread and
// writes its results into destination.
//
// Arguments are copied before execution, so arguments and destination may
// overlap. On success, count entries are written and the destination tail is
// unchanged. On an execution failure or ingress error, destination is
// unchanged and count is zero. If Lua produces more than len(destination)
// results, count is the required size and the returned *ResultCapacityError
// describes the shortfall; destination is still unchanged. Lua side effects
// completed before that result-count check are not rolled back. Panics from
// NativeFunc behave as documented by Call.
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
		allResults,
	)
	return count, err
}

func validateFixedResultCount(resultCount int) error {
	if resultCount < 0 || resultCount > maxFixedResults {
		return ErrInvalidResultCount
	}
	return nil
}

func (state *State) callMain(
	callable Value,
	arguments []Value,
	destination []Value,
	allocateResults bool,
	wantedResults int,
) (owned []Value, count int, err error) {
	thread, err := state.prepareMainCall(callable, arguments)
	if err != nil {
		return nil, 0, err
	}
	return state.runMainCall(
		thread,
		callable,
		arguments,
		destination,
		allocateResults,
		wantedResults,
	)
}

func (state *State) runMainCall(
	thread *threadObject,
	callable Value,
	arguments []Value,
	destination []Value,
	allocateResults bool,
	wantedResults int,
) (owned []Value, count int, err error) {
	if failure := state.admitExecutionContext(); failure != nil {
		return nil, 0, failure
	}
	// A State with no installed context leaves the budget alone: it is
	// already zero, and skipping the write keeps unguarded calls on exactly
	// the path they had before cancellation existed.
	if state.ambientDone != nil {
		thread.resetContextBudget()
	}
	state.active = thread
	thread.status = ThreadRunning
	defer func() {
		thread.resetMainCall()
		state.active = nil
		thread.contextBudget = 0
		state.endExecution()
	}()

	resultBase, failure := startStagedCall(
		thread,
		slotFromValue(callable),
		callArguments{
			owning:    arguments,
			admission: callCallableAdmission,
		},
		wantedResults,
	)
	if failure != nil {
		return nil, 0, failure.exposeValue()
	}
	if resultBase != 0 {
		panic("lua: main call staged at a nonzero result base")
	}
	result := execute(thread, 0)
	if result.kind == executionFailed {
		return nil, 0, result.err.exposeValue()
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
		return nil, count, newResultCapacityError(
			thread.values[:count],
			len(destination),
		)
	}
	for index := 0; index < count; index++ {
		destination[index] = thread.values[index].owningValue()
	}
	return nil, count, nil
}

// callMainCompactNone invokes one compact callable on an otherwise idle main
// thread and discards its results. It is used by runtime lifecycle work such
// as close-time finalization, where materializing public Values would add a
// second representation to an entirely internal call.
func (state *State) callMainCompactNone(
	callable slot,
	arguments []slot,
) *Error {
	thread, err := state.prepareReadyMainThread()
	if err != nil {
		panic("lua: compact main call entered an unavailable State")
	}
	if err := state.runtime.acceptSlot(callable); err != nil {
		panic("lua: compact main call received an invalid callable")
	}
	for _, argument := range arguments {
		if err := state.runtime.acceptSlot(argument); err != nil {
			panic("lua: compact main call received an invalid argument")
		}
	}
	if len(arguments) >= state.options.MaxValues ||
		uint64(len(arguments))+1 > uint64(^uint32(0)) {
		return newResourceError(
			"value stack limit of %d exceeded",
			state.options.MaxValues,
		)
	}

	state.active = thread
	thread.status = ThreadRunning
	defer func() {
		thread.resetMainCall()
		state.active = nil
		state.endExecution()
	}()

	resultBase, failure := startStagedCall(
		thread,
		callable,
		callArguments{compact: arguments},
		0,
	)
	if failure != nil {
		return failure
	}
	if resultBase != 0 {
		panic("lua: compact main call staged at a nonzero result base")
	}
	result := execute(thread, 0)
	if result.kind == executionFailed {
		return result.err
	}
	if result.kind != executionReturned ||
		len(thread.frames) != 0 ||
		len(thread.continuations) != 0 ||
		thread.openUpvalues != nil ||
		thread.frameExtent != 0 {
		panic("lua: compact executor returned an invalid main-thread state")
	}
	return nil
}

func (state *State) prepareMainCall(
	callable Value,
	arguments []Value,
) (*threadObject, error) {
	thread, err := state.prepareReadyMainThread()
	if err != nil {
		return nil, err
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

func (state *State) prepareReadyMainThread() (*threadObject, error) {
	if err := state.checkOpen(); err != nil {
		return nil, err
	}
	thread := state.main
	if state.active != nil ||
		thread == nil ||
		thread.status != ThreadReady {
		return nil, ErrRunning
	}
	if thread.top != 0 ||
		thread.frameExtent != 0 ||
		len(thread.frames) != 0 ||
		len(thread.continuations) != 0 ||
		thread.openUpvalues != nil ||
		thread.activeNativeToken != 0 ||
		thread.nativeCallDepth != 0 ||
		thread.errorHandlerDepth != 0 ||
		thread.contextBudget != 0 ||
		state.runtime.nativeCallDepth != 0 ||
		state.execution.failure != nil ||
		state.execution.pendingExit != nil ||
		state.limits.values != state.options.MaxValues ||
		state.limits.frames != state.options.MaxFrames {
		panic("lua: ready main thread retains execution state")
	}
	return thread, nil
}

func (thread *threadObject) resetMainCall() {
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
	thread.nativeCallDepth = 0
	thread.errorHandlerDepth = 0
	thread.state.limits = resourceLimits{
		values: thread.state.options.MaxValues,
		frames: thread.state.options.MaxFrames,
	}
	thread.clearInactive(0, extent)
	thread.status = ThreadReady
}
