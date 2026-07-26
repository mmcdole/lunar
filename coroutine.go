package lua

import (
	"context"
	"errors"
	"fmt"
)

// ErrMainThread reports an attempt to resume a State's main Thread through
// the coroutine interface.
var ErrMainThread = errors.New("lua: main thread cannot be resumed")

// NewThread constructs a suspended coroutine whose first resume invokes
// callable.
//
// Callable may be a Function or a value with a Function-valued __call
// metamethod. It must belong to state. Construction does not execute Lua.
// The new Thread inherits the creating Thread's global-environment pointer;
// later pointer replacement on either Thread is isolated.
// Lua's coroutine.create is intentionally narrower and accepts only Lua
// Functions, as required by Lua 5.1.
func (state *State) NewThread(callable Value) (*Thread, error) {
	if err := state.checkOpen(); err != nil {
		return nil, err
	}
	if err := state.runtime.accept(callable); err != nil {
		return nil, err
	}
	parent := state.currentThread()
	compact := slotFromValue(callable)
	if _, direct := functionSlot(compact); !direct &&
		callMetamethodFunction(parent, compact) == nil {
		message := fmt.Sprintf(
			"attempt to call a %s value",
			compact.kind(),
		)
		return nil, newCoroutineFailure(state, message)
	}

	capacity := 16
	if capacity > state.options.MaxValues {
		capacity = state.options.MaxValues
	}
	thread := &Thread{
		objectHeader: objectHeader{owner: state.runtime},
		state:        state,
		globals:      parent.globals,
		values:       make([]slot, 1, capacity),
		top:          1,
		status:       ThreadSuspended,
	}
	writeSlot(&thread.values[0], compact)
	return thread, nil
}

// Resume starts or continues a suspended coroutine.
//
// On a yield, status is ThreadSuspended and results are the yielded values.
// On a final return, status is ThreadDead and results are the function's
// return values. A Lua failure also leaves the coroutine dead and is returned
// as *Error. The returned slice and Values are owned by the caller.
func (thread *Thread) Resume(
	arguments ...Value,
) (results []Value, status ThreadStatus, err error) {
	run, err := thread.resumeExternal(resumeArguments{values: arguments})
	if err != nil {
		return nil, thread.Status(), err
	}
	defer run.release()
	if run.failure != nil {
		return nil, run.status, run.failure
	}
	if run.count == 0 {
		return nil, run.status, nil
	}
	results = make([]Value, run.count)
	for index := range results {
		results[index] = run.thread.values[run.first+index].owningValue()
	}
	return results, run.status, nil
}

// ResumeContext starts or continues a suspended coroutine like Resume while
// making ctx available to native callbacks and interrupting Lua execution when
// ctx is cancelled.
//
// A nil context returns ErrNilContext. A context already cancelled at
// admission does not start or advance the coroutine. A cancellation observed
// after execution starts leaves the coroutine dead; Lua-visible side effects
// completed before cancellation remain.
func (thread *Thread) ResumeContext(
	ctx context.Context,
	arguments ...Value,
) (results []Value, status ThreadStatus, err error) {
	if ctx == nil {
		return nil, thread.Status(), ErrNilContext
	}
	run, err := thread.resumeExternalContext(
		ctx,
		resumeArguments{values: arguments},
	)
	if err != nil {
		return nil, thread.Status(), err
	}
	return collectResumeResults(&run)
}

func collectResumeResults(
	run *threadResumeResult,
) (results []Value, status ThreadStatus, err error) {
	if run == nil {
		panic("lua: nil coroutine result")
	}
	defer run.release()
	if run.failure != nil {
		return nil, run.status, run.failure
	}
	if run.count == 0 {
		return nil, run.status, nil
	}
	results = make([]Value, run.count)
	for index := range results {
		results[index] = run.thread.values[run.first+index].owningValue()
	}
	return results, run.status, nil
}

// ResumeInto starts or continues a suspended coroutine and writes its yielded
// or returned values into destination.
//
// Arguments are copied before execution, so arguments and destination may
// overlap. If destination is too short, count reports the required size and
// the coroutine has already yielded or returned, but destination is unchanged.
// The call may be retried only by resuming from the coroutine's new status,
// not by repeating the completed transition.
func (thread *Thread) ResumeInto(
	arguments []Value,
	destination []Value,
) (count int, status ThreadStatus, err error) {
	run, err := thread.resumeExternal(resumeArguments{values: arguments})
	if err != nil {
		return 0, thread.Status(), err
	}
	defer run.release()
	if run.failure != nil {
		return 0, run.status, run.failure
	}
	if run.count > len(destination) {
		return run.count, run.status, &ResultCapacityError{
			Required:  run.count,
			Available: len(destination),
		}
	}
	for index := 0; index < run.count; index++ {
		destination[index] =
			run.thread.values[run.first+index].owningValue()
	}
	return run.count, run.status, nil
}

// ResumeIntoContext starts or continues a suspended coroutine like
// ResumeContext and writes yielded or returned values into destination.
//
// On cancellation count is zero and destination is unchanged.
func (thread *Thread) ResumeIntoContext(
	ctx context.Context,
	arguments []Value,
	destination []Value,
) (count int, status ThreadStatus, err error) {
	if ctx == nil {
		return 0, thread.Status(), ErrNilContext
	}
	run, err := thread.resumeExternalContext(
		ctx,
		resumeArguments{values: arguments},
	)
	if err != nil {
		return 0, thread.Status(), err
	}
	return copyResumeResults(&run, destination)
}

func copyResumeResults(
	run *threadResumeResult,
	destination []Value,
) (count int, status ThreadStatus, err error) {
	if run == nil {
		panic("lua: nil coroutine result")
	}
	defer run.release()
	if run.failure != nil {
		return 0, run.status, run.failure
	}
	if run.count > len(destination) {
		return run.count, run.status, &ResultCapacityError{
			Required:  run.count,
			Available: len(destination),
		}
	}
	for index := 0; index < run.count; index++ {
		destination[index] =
			run.thread.values[run.first+index].owningValue()
	}
	return run.count, run.status, nil
}

type resumeArguments struct {
	values  []Value
	compact []slot
}

func (arguments resumeArguments) count() int {
	if len(arguments.values) != 0 && len(arguments.compact) != 0 {
		panic("lua: mixed coroutine argument representations")
	}
	if arguments.compact != nil {
		return len(arguments.compact)
	}
	return len(arguments.values)
}

func (arguments resumeArguments) copyTo(destination []slot, count int) {
	if count < 0 || count > arguments.count() || count > len(destination) {
		panic("lua: invalid coroutine argument copy")
	}
	if arguments.compact != nil {
		copy(destination[:count], arguments.compact[:count])
		return
	}
	for index := 0; index < count; index++ {
		writeSlot(&destination[index], slotFromValue(arguments.values[index]))
	}
}

type threadResumeResult struct {
	thread  *Thread
	failure *Error
	first   int
	count   int
	status  ThreadStatus
}

func (result *threadResumeResult) release() {
	if result == nil || result.thread == nil || result.failure != nil {
		return
	}
	thread := result.thread
	thread.clearInactive(result.first, result.first+result.count)
	switch result.status {
	case ThreadSuspended:
		frame := thread.frames[len(thread.frames)-1]
		thread.top = result.first
		thread.frameExtent = int(frame.callerExtent)
	case ThreadDead:
		thread.values = nil
		thread.frames = nil
		thread.continuations = nil
		thread.top = 0
		thread.frameExtent = 0
	default:
		panic("lua: invalid coroutine result status")
	}
	result.thread = nil
}

func (thread *Thread) resumeExternal(
	arguments resumeArguments,
) (threadResumeResult, error) {
	if thread == nil ||
		thread.owner == nil ||
		thread.state == nil ||
		thread.owner.closed.Load() {
		return threadResumeResult{}, ErrClosed
	}
	if thread.main {
		return threadResumeResult{}, ErrMainThread
	}
	state := thread.state
	if state.active != nil {
		return threadResumeResult{}, ErrRunning
	}
	if state.execution.context != nil ||
		state.execution.done != nil ||
		state.execution.failure != nil ||
		thread.contextBudget != 0 {
		panic("lua: idle coroutine state retains execution context")
	}
	if arguments.compact != nil {
		panic("lua: compact arguments at public coroutine boundary")
	}
	for _, argument := range arguments.values {
		if err := thread.owner.accept(argument); err != nil {
			return threadResumeResult{}, err
		}
	}
	return resumeThread(nil, thread, arguments), nil
}

func (thread *Thread) resumeExternalContext(
	ctx context.Context,
	arguments resumeArguments,
) (threadResumeResult, error) {
	if thread == nil ||
		thread.owner == nil ||
		thread.state == nil ||
		thread.owner.closed.Load() {
		return threadResumeResult{}, ErrClosed
	}
	if thread.main {
		return threadResumeResult{}, ErrMainThread
	}
	state := thread.state
	if state.active != nil {
		return threadResumeResult{}, ErrRunning
	}
	if state.execution.context != nil ||
		state.execution.done != nil ||
		state.execution.failure != nil ||
		thread.contextBudget != 0 {
		panic("lua: idle coroutine state retains execution context")
	}
	if arguments.compact != nil {
		panic("lua: compact arguments at public coroutine boundary")
	}
	for _, argument := range arguments.values {
		if err := thread.owner.accept(argument); err != nil {
			return threadResumeResult{}, err
		}
	}
	if failure := thread.preflightExternalResume(
		arguments.count(),
	); failure != nil {
		return threadResumeResult{
			thread:  thread,
			failure: failure,
			status:  thread.status,
		}, nil
	}
	execution, failure := prepareExecutionContext(ctx)
	if failure != nil {
		return threadResumeResult{
			thread:  thread,
			failure: failure,
			status:  thread.status,
		}, nil
	}
	state.beginExecutionContext(execution)
	thread.resetContextBudget()
	defer func() {
		thread.contextBudget = 0
		state.endExecutionContext()
	}()
	return resumeThread(nil, thread, arguments), nil
}

func resumeThread(
	caller *Thread,
	thread *Thread,
	arguments resumeArguments,
) threadResumeResult {
	if thread == nil ||
		thread.owner == nil ||
		thread.state == nil ||
		thread.owner.closed.Load() {
		panic("lua: invalid internal coroutine resume")
	}
	state := thread.state
	if caller == nil {
		if state.active != nil {
			panic("lua: external coroutine resume while state is active")
		}
	} else {
		if caller == thread {
			return threadResumeResult{
				thread: thread,
				failure: newCoroutineFailure(
					state,
					"cannot resume running coroutine",
				),
				status: thread.status,
			}
		}
		if caller.owner != thread.owner ||
			caller.state != state ||
			state.active != caller ||
			caller.status != ThreadRunning {
			panic("lua: invalid internal coroutine caller")
		}
	}
	if thread.status != ThreadSuspended {
		return threadResumeResult{
			thread: thread,
			failure: newCoroutineFailure(
				state,
				fmt.Sprintf(
					"cannot resume %s coroutine",
					coroutineStatusName(caller, thread),
				),
			),
			status: thread.status,
		}
	}
	if failure := thread.preflightResume(arguments.count()); failure != nil {
		return threadResumeResult{
			thread:  thread,
			failure: failure,
			status:  thread.status,
		}
	}
	previousActive := state.active
	previousLimits := state.limits
	if caller != nil {
		caller.status = ThreadNormal
	}
	state.active = thread
	state.limits = resourceLimits{
		values: state.options.MaxValues,
		frames: state.options.MaxFrames,
	}
	thread.status = ThreadRunning
	guardedChild := caller != nil && state.execution.done != nil
	if guardedChild {
		thread.resetContextBudget()
	}
	finished := false
	defer func() {
		if !finished {
			thread.discardCoroutineExecution()
			thread.status = ThreadDead
		}
		state.limits = previousLimits
		state.active = previousActive
		if guardedChild {
			thread.contextBudget = 0
		}
		if caller != nil {
			caller.status = ThreadRunning
		}
	}()

	var failure *Error
	if len(thread.frames) == 0 {
		failure = thread.startCoroutine(arguments)
	} else {
		failure = thread.continueCoroutine(arguments)
	}
	if failure != nil {
		positionExecutionFailure(thread, failure)
		finalizeExecutionFailure(thread, 0, failure)
		thread.discardCoroutineExecution()
		thread.status = ThreadDead
		finished = true
		return threadResumeResult{
			thread:  thread,
			failure: failure,
			status:  ThreadDead,
		}
	}

	execution := execute(thread, 0)
	switch execution.kind {
	case executionYielded:
		if thread.status != ThreadSuspended ||
			len(thread.frames) == 0 ||
			thread.frames[len(thread.frames)-1].function.prototype != nil ||
			len(thread.continuations) != 0 ||
			thread.activeNativeToken != 0 ||
			thread.nativeCallDepth != 0 {
			panic("lua: invalid suspended coroutine state")
		}
		first := int(thread.frames[len(thread.frames)-1].resultBase)
		if first < 0 || first > thread.top {
			panic("lua: invalid coroutine yield window")
		}
		finished = true
		return threadResumeResult{
			thread: thread,
			first:  first,
			count:  thread.top - first,
			status: ThreadSuspended,
		}
	case executionReturned:
		if len(thread.frames) != 0 ||
			len(thread.continuations) != 0 ||
			thread.openUpvalues != nil ||
			thread.frameExtent != 0 {
			panic("lua: returned coroutine retained execution state")
		}
		thread.status = ThreadDead
		finished = true
		return threadResumeResult{
			thread: thread,
			first:  0,
			count:  thread.top,
			status: ThreadDead,
		}
	case executionFailed:
		thread.discardCoroutineExecution()
		thread.status = ThreadDead
		finished = true
		return threadResumeResult{
			thread:  thread,
			failure: execution.err,
			status:  ThreadDead,
		}
	default:
		panic("lua: invalid coroutine execution result")
	}
}

func (thread *Thread) preflightExternalResume(argumentCount int) *Error {
	if thread.status != ThreadSuspended {
		return newCoroutineFailure(
			thread.state,
			fmt.Sprintf(
				"cannot resume %s coroutine",
				coroutineStatusName(nil, thread),
			),
		)
	}
	return thread.preflightResume(argumentCount)
}

func (thread *Thread) preflightResume(argumentCount int) *Error {
	if argumentCount < 0 {
		panic("lua: negative coroutine argument count")
	}
	base := 1
	required := base + argumentCount
	if len(thread.frames) != 0 {
		frame := thread.frames[len(thread.frames)-1]
		if frame.function == nil || frame.function.prototype != nil {
			panic("lua: suspended coroutine has no native yield activation")
		}
		base = int(frame.resultBase)
		outputCount := argumentCount
		if wanted := int(frame.wantedResults); wanted != allResults {
			outputCount = wanted
		}
		required = base + outputCount
	}
	if base < 0 ||
		required < base ||
		required > thread.state.options.MaxValues ||
		uint64(required) > uint64(^uint32(0)) {
		return newResourceError("too many arguments to resume")
	}
	return nil
}

func (thread *Thread) startCoroutine(arguments resumeArguments) *Error {
	if thread.top != 1 || thread.frameExtent != 0 {
		panic("lua: invalid initial coroutine stack")
	}
	argumentCount := arguments.count()
	required := 1 + argumentCount
	thread.reserveValues(required)
	arguments.copyTo(thread.values[1:required], argumentCount)
	thread.top = required

	callable := thread.values[0]
	if function, direct := functionSlot(callable); direct {
		return thread.pushFunctionCall(
			function,
			0,
			argumentCount,
			allResults,
		)
	}
	if function := callMetamethodFunction(thread, callable); function != nil {
		return thread.pushFunctionMetamethodCall(
			function,
			0,
			argumentCount,
			allResults,
		)
	}
	message := fmt.Sprintf(
		"attempt to call a %s value",
		callable.kind(),
	)
	return newCoroutineFailure(thread.state, message)
}

func (thread *Thread) continueCoroutine(arguments resumeArguments) *Error {
	frame := &thread.frames[len(thread.frames)-1]
	if frame.function == nil || frame.function.prototype != nil {
		panic("lua: coroutine did not suspend in a native activation")
	}
	resultBase := int(frame.resultBase)
	supplied := arguments.count()
	outputCount := supplied
	if wanted := int(frame.wantedResults); wanted != allResults {
		outputCount = wanted
	}
	required := resultBase + outputCount
	thread.reserveValues(required)
	copied := supplied
	if copied > outputCount {
		copied = outputCount
	}
	arguments.copyTo(
		thread.values[resultBase:resultBase+copied],
		copied,
	)
	thread.fillNil(resultBase+copied, resultBase+outputCount)
	thread.finishNativeCall(outputCount)
	return nil
}

func (thread *Thread) discardCoroutineExecution() {
	previousExtent := thread.liveValueExtent()
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
	thread.clearInactive(0, previousExtent)
	thread.values = nil
	thread.frames = nil
	thread.continuations = nil
}

func coroutineStatusName(current, thread *Thread) string {
	if thread == current || thread.status == ThreadRunning {
		return "running"
	}
	switch thread.status {
	case ThreadNormal:
		return "normal"
	case ThreadSuspended:
		return "suspended"
	case ThreadReady:
		if thread.main {
			return "dead"
		}
		return "suspended"
	default:
		return "dead"
	}
}

func newCoroutineFailure(state *State, message string) *Error {
	return &Error{
		value:       state.String(message),
		description: message,
		category:    RuntimeError,
	}
}
