package lua

import (
	"errors"
	"fmt"
	"runtime"
	"slices"
)

const (
	maxNativeCaptures  = 255
	maxNativeCallDepth = 200
	nativeTerminalBit  = uint64(1) << 63
	nativeTokenMask    = nativeTerminalBit - 1
)

// ErrInvalidNativeFunction reports construction with a nil native entry.
var ErrInvalidNativeFunction = errors.New("lua: native function is nil")

var errNativeCaptureLimit = errors.New(
	"lua: native function capture limit exceeded",
)

// NativeFunc is a Go function callable by Lua.
//
// The Frame is borrowed for the duration of the call and is the callback's
// active execution capability. A callback that calls Lua synchronously,
// directly or through helper code, must pass that Frame along and use its
// Call* methods. State.Call* methods are outer entry points and return
// ErrRunning while the callback is active.
//
// The callback must return an Outcome produced by that Frame. Retaining a
// Frame or using it after producing a terminal Outcome is a programming
// error. Go panics are propagated after the borrowed activation is removed.
// The Throw* methods are the protected Lua-error paths, while Yield* outcomes
// suspend a yieldable coroutine.
type NativeFunc func(Frame) Outcome

type nativeOutcomeKind uint8

const (
	nativeOutcomeInvalid nativeOutcomeKind = iota
	nativeOutcomeReturn
	nativeOutcomeError
	nativeOutcomeYield
)

// Outcome is the terminal result of a NativeFunc.
//
// Outcomes are bound to the Frame that produced them. The zero value and an
// Outcome returned from another invocation become Lua runtime failures. A
// successful Outcome does not retain the executing Thread or State object
// graph.
type Outcome struct {
	owner       *runtimeState
	failure     *Error
	token       uint64
	resultCount uint32
	kind        nativeOutcomeKind
}

// Frame is the borrowed activation and execution capability for one native
// call.
//
// Argument indexes are zero-based. Typed argument methods perform exact Lua
// type checks and do not coerce values. Call*, Index, and SetIndex continue
// execution reentrantly on the callback's Thread; equivalent State execution
// methods cannot reenter an already-running State. Owning Values and object
// handles read from a Frame may be retained, but the Frame itself is valid
// only until a terminal Return* or Yield* method is called, a Throw* method
// unwinds, or the NativeFunc returns.
type Frame struct {
	thread *threadObject
	token  uint64
	depth  int
}

// NewNativeFunction constructs a canonical native Function.
//
// Its initial environment is the currently executing Function's environment,
// or the main Thread's global environment outside a callback. State to carry
// alongside the function belongs in the Go closure; an owning Value held that
// way keeps its Lua object reachable.
func (state *State) NewNativeFunction(
	entry NativeFunc,
) (*Function, error) {
	if err := state.checkOpen(); err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, ErrInvalidNativeFunction
	}
	function := newNativeFunctionOwned(
		state,
		state.constructionEnvironment(),
		entry,
		nil,
	)
	return function.owningHandle(), nil
}

func (state *State) newNativeFunctionObject(
	entry NativeFunc,
	captures []slot,
) (*functionObject, error) {
	if err := state.checkOpen(); err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, ErrInvalidNativeFunction
	}
	if len(captures) > maxNativeCaptures {
		return nil, errNativeCaptureLimit
	}
	for _, capture := range captures {
		if err := state.runtime.acceptSlot(capture); err != nil {
			return nil, err
		}
	}
	return newNativeFunctionOwned(
		state,
		state.constructionEnvironment(),
		entry,
		captures,
	), nil
}

func invokeNativeCall(thread *threadObject) (failure *Error) {
	if thread == nil ||
		thread.state == nil ||
		thread.owner == nil ||
		len(thread.frames) == 0 {
		panic("lua: invalid native callback entry")
	}
	parentToken := thread.activeNativeToken
	switch {
	case parentToken == 0 && thread.nativeCallDepth != 0:
		panic("lua: native callback depth has no active token")
	case parentToken != 0 &&
		(parentToken&nativeTerminalBit != 0 || thread.nativeCallDepth == 0):
		panic("lua: invalid parent native callback")
	case thread.owner.nativeCallDepth < thread.nativeCallDepth:
		panic("lua: thread native depth exceeds runtime depth")
	case int(thread.owner.nativeCallDepth) >= thread.nativeCallLimit():
		return newResourceError("C stack overflow")
	}
	call := &thread.frames[len(thread.frames)-1]
	function := call.function
	if function == nil ||
		function.prototype != nil ||
		function.body == nil {
		panic("lua: invalid native function activation")
	}
	body := function.nativeBodyUnchecked()
	if body.entry == nil {
		panic("lua: invalid native function activation")
	}

	token := thread.nextNativeToken()
	thread.activeNativeToken = token
	thread.nativeCallDepth++
	thread.owner.nativeCallDepth++
	callbackReturned := false
	nativeFrameDepth := len(thread.frames) - 1
	defer func() {
		// Frame.Throw unwinds to here. Recovering in this defer rather
		// than in a wrapper around the callback keeps the native call
		// path at one defer, and callbackReturned gates the recover so a
		// callback that returned normally never pays for it: reaching
		// here without it means a panic is in flight.
		var propagate any
		if !callbackReturned {
			recovered := recover()
			if thrown, ok := recovered.(nativeThrow); ok &&
				thrown.token == token {
				// Mirror the returned-error path, whose outcome this
				// already is: the frames stay for the failure to unwind,
				// and a pending exit outranks the callback's failure.
				// One divergence: the returned path polls the context
				// after the callback, so an expired context overrides
				// the callback's error there but not here. The next
				// instruction-level poll fires the same ContextError, so
				// the difference is a few instructions of visibility,
				// not outcome.
				callbackReturned = true
				failure = thrown.outcome.failure
				if pending := thread.state.execution.pendingExit; pending != nil {
					failure = pending
				}
			} else {
				propagate = recovered
			}
		}
		thread.activeNativeToken = parentToken
		thread.nativeCallDepth--
		thread.owner.nativeCallDepth--
		if !callbackReturned {
			thread.unwindCalls(nativeFrameDepth)
		}
		if propagate != nil {
			panic(propagate)
		}
	}()

	outcome := body.entry(Frame{
		thread: thread,
		token:  token,
		depth:  len(thread.frames),
	})
	callbackReturned = true
	if failure := thread.state.execution.pendingExit; failure != nil {
		return failure
	}
	if failure := validateNativeOutcome(
		thread,
		outcome,
		token,
		nativeFrameDepth,
	); failure != nil {
		return failure
	}
	if thread.contextBudget != 0 {
		if failure := pollExecutionContext(thread); failure != nil {
			return failure
		}
	}

	switch outcome.kind {
	case nativeOutcomeReturn:
		thread.finishNativeCall(int(outcome.resultCount))
		return nil
	case nativeOutcomeError:
		return outcome.failure
	case nativeOutcomeYield:
		thread.status = ThreadSuspended
		return nil
	default:
		panic("lua: validated invalid native outcome")
	}
}

func validateNativeOutcome(
	thread *threadObject,
	outcome Outcome,
	token uint64,
	nativeFrameDepth int,
) *Error {
	if outcome.owner != thread.owner ||
		outcome.token != token ||
		thread.activeNativeToken != token|nativeTerminalBit {
		return newNativeRuntimeError(
			thread,
			"native function returned an invalid outcome",
		)
	}
	valid := false
	switch outcome.kind {
	case nativeOutcomeReturn:
		valid = outcome.failure == nil
	case nativeOutcomeError:
		valid = outcome.failure != nil &&
			outcome.resultCount == 0
	case nativeOutcomeYield:
		call := &thread.frames[nativeFrameDepth]
		resultBase := int(call.resultBase)
		valid = outcome.failure == nil &&
			resultBase >= 0 &&
			resultBase <= thread.top &&
			int(outcome.resultCount) == thread.top-resultBase
	default:
		valid = false
	}
	if valid {
		return nil
	}
	return newNativeRuntimeError(
		thread,
		"native function returned an invalid outcome",
	)
}

func (thread *threadObject) nextNativeToken() uint64 {
	if thread.owner.nativeSequence >= nativeTokenMask {
		panic("lua: native callback token space exhausted")
	}
	thread.owner.nativeSequence++
	return thread.owner.nativeSequence
}

func newNativeRuntimeError(thread *threadObject, message string) *Error {
	return &Error{
		value:       thread.state.String(message),
		description: message,
		category:    RuntimeError,
	}
}

// SetFunctions installs native functions into table.
//
// SetFunctions validates the State, table, and every function before changing
// table, so a validation failure never leaves a partial installation. Existing
// fields with matching names are replaced.
func (state *State) SetFunctions(
	table *Table,
	functions map[string]NativeFunc,
) error {
	if err := state.checkOpen(); err != nil {
		return err
	}
	target, err := state.acceptTable(table)
	if err != nil {
		return err
	}

	names := make([]string, 0, len(functions))
	for name, entry := range functions {
		if entry == nil {
			return fmt.Errorf(
				"lua: function %q: %w",
				name,
				ErrInvalidNativeFunction,
			)
		}
		names = append(names, name)
	}
	slices.Sort(names)
	if len(names) == 0 {
		runtime.KeepAlive(table)
		return nil
	}

	environment := state.constructionEnvironment()
	created := make([]*functionObject, len(names))
	for index, name := range names {
		created[index] = newNativeFunctionOwned(
			state,
			environment,
			functions[name],
			nil,
		)
	}
	for index, name := range names {
		if err := target.rawSetStringSlot(
			name,
			slotFromFunctionObject(created[index]),
		); err != nil {
			panic("lua: validated SetFunctions installation failed")
		}
	}
	runtime.KeepAlive(table)
	return nil
}
