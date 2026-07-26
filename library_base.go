package lua

import (
	"fmt"
	"math"
)

// OpenBase installs the currently implemented Lua 5.1 base-library globals.
//
// The base library is still under construction; this currently installs _G,
// _VERSION, pcall, xpcall, and the Lua 5.1 coroutine library. Opening is
// explicit: New returns an empty State. Calling OpenBase again replaces the
// functions and coroutine table with fresh canonical objects and restores the
// other globals.
func (state *State) OpenBase() error {
	if err := state.checkOpen(); err != nil {
		return err
	}
	pcall, err := state.NewNativeFunction(basePCall)
	if err != nil {
		return err
	}
	xpcall, err := state.NewNativeFunction(baseXPCall)
	if err != nil {
		return err
	}
	if err := state.globals.RawSetString("_G", state.globals.Value()); err != nil {
		return err
	}
	if err := state.globals.RawSetString(
		"_VERSION",
		state.String("Lua 5.1"),
	); err != nil {
		return err
	}
	if err := state.globals.RawSetString("pcall", pcall.Value()); err != nil {
		return err
	}
	if err := state.globals.RawSetString("xpcall", xpcall.Value()); err != nil {
		return err
	}
	return state.OpenCoroutine()
}

func basePCall(frame Frame) Outcome {
	call := frame.activation()
	thread := frame.thread
	base := int(call.base)
	count := thread.top - base
	if count == 0 {
		return baseArgumentError(frame, 0, "value expected")
	}
	target := thread.values[base]
	return runProtectedCall(
		frame,
		target,
		thread.values[base+1:base+count],
		nilSlot,
		false,
	)
}

func baseXPCall(frame Frame) Outcome {
	call := frame.activation()
	thread := frame.thread
	base := int(call.base)
	count := thread.top - base
	if count < 2 {
		return baseArgumentError(frame, 1, "value expected")
	}
	target := thread.values[base]
	handler := thread.values[base+1]

	// Lua 5.1 discards arguments beyond the target and handler before it
	// invokes the target. Apart from making those arguments unobservable,
	// this releases their roots and keeps them out of the protected call's
	// value-stack budget.
	frame.discardArgumentsAfter(2)

	return runProtectedCall(
		frame,
		target,
		nil,
		handler,
		true,
	)
}

// discardArgumentsAfter removes a native call's supplied argument tail.
//
// It is deliberately shrink-only: a missing argument remains absent rather
// than becoming an explicit nil. The caller's register extent stays live.
// Discarded slots are cleared in place, and any tail beyond that caller extent
// also stops consuming nested-call value budget.
func (frame Frame) discardArgumentsAfter(count int) {
	call := frame.activation()
	if count < 0 {
		panic("lua: negative retained argument count")
	}
	thread := frame.thread
	argumentEnd := int(call.base) + count
	if thread.top <= argumentEnd {
		return
	}

	previousTop := thread.top
	previousExtent := thread.liveValueExtent()
	thread.clearInactive(argumentEnd, previousTop)
	thread.top = argumentEnd
	thread.frameExtent = int(call.callerExtent)
	if argumentEnd > thread.frameExtent {
		thread.frameExtent = argumentEnd
	}
	thread.clearDeadSuffix(previousExtent)
}

// The remainder of this file is the Lua 5.1 auxiliary layer shared by every
// library_*.go file: the argument coercions PUC spells luaL_checknumber and
// luaL_checkint, and the two diagnostics it spells luaL_argerror and
// luaL_error. Both diagnostics resolve their name and source position from
// the immediate Lua caller, matching luaL_where(L, 1); a library entered
// directly from Go has neither and reports the bare message.

// numberArgument reads argument index with luaL_checknumber's coercion: an
// exact number passes through and a complete numeric string converts. The
// value is read as a compact slot, so a scalar argument never materializes an
// owning Value.
func (frame Frame) numberArgument(index int) (float64, bool) {
	value, present := frame.argument(index)
	if !present {
		return 0, false
	}
	return slotToNumber(value)
}

// integerArgument reads argument index with luaL_checkint's conversion.
func (frame Frame) integerArgument(index int) (int, bool) {
	number, ok := frame.numberArgument(index)
	if !ok {
		return 0, false
	}
	return libraryInteger(number), true
}

// libraryInteger converts a coerced library argument to the integer PUC would
// obtain from luaL_checkint.
//
// PUC casts the double straight to C int, which is undefined for NaN and for
// magnitudes outside that type. This truncates toward zero and saturates at
// the signed 32-bit bounds, so every input has one defined result while every
// value C could convert without undefined behavior converts identically.
func libraryInteger(number float64) int {
	switch {
	case math.IsNaN(number):
		return 0
	case number >= math.MaxInt32:
		return math.MaxInt32
	case number <= math.MinInt32:
		return math.MinInt32
	default:
		return int(math.Trunc(number))
	}
}

// baseArgumentError reports a library argument failure as luaL_argerror does.
//
// A method call does not count its receiver, so an error in the receiver of
// t:f(...) becomes Lua 5.1's distinct bad-self message and every later
// argument keeps the caller's visible position.
func baseArgumentError(
	frame Frame,
	index int,
	reason string,
) Outcome {
	prototype, pc, found := immediateLuaCaller(frame)
	name := "?"
	category := ""
	if found {
		call := prototype.code[pc]
		if operation := call.opcode(); operation == opCall ||
			operation == opTailCall {
			if resolved, candidate, named := prototype.describeOperand(
				pc,
				call.a(),
			); named {
				name = candidate
				category = resolved
			}
		}
	}

	var message string
	switch {
	case category != "method":
		message = fmt.Sprintf(
			"bad argument #%d to '%s' (%s)",
			index+1,
			name,
			reason,
		)
	case index == 0:
		message = fmt.Sprintf(
			"calling '%s' on bad self (%s)",
			name,
			reason,
		)
	default:
		message = fmt.Sprintf(
			"bad argument #%d to '%s' (%s)",
			index,
			name,
			reason,
		)
	}
	if found {
		message = executionErrorDescription(
			prototype,
			pc,
			message,
		)
	}
	return frame.raiseString(message)
}

// baseArgumentTypeError reports luaL_typerror's expected-versus-actual form.
func baseArgumentTypeError(
	frame Frame,
	index int,
	expected string,
) Outcome {
	return baseArgumentError(
		frame,
		index,
		expected+" expected, got "+argumentTypeName(frame, index),
	)
}

func numberArgumentError(frame Frame, index int) Outcome {
	return baseArgumentTypeError(frame, index, "number")
}

// argumentTypeName names argument index for a diagnostic. A missing argument
// is "no value", which luaL_typename distinguishes from an explicit nil.
func argumentTypeName(frame Frame, index int) string {
	kind := frame.Kind(index)
	if kind == InvalidKind {
		return "no value"
	}
	return kind.String()
}

func (frame Frame) returnCompactValues(
	leading [2]slot,
	leadingCount int,
	values []slot,
) Outcome {
	call := frame.activation()
	if leadingCount < 0 || leadingCount > len(leading) {
		panic("lua: invalid compact result prefix")
	}
	supplied := leadingCount + len(values)
	outputCount, failure := frame.prepareResults(call, supplied)
	if failure != nil {
		return frame.sealError(failure)
	}
	resultBase := int(call.resultBase)
	written := leadingCount
	if written > outputCount {
		written = outputCount
	}
	for index := 0; index < written; index++ {
		writeSlot(
			&frame.thread.values[resultBase+index],
			leading[index],
		)
	}
	copied := len(values)
	if available := outputCount - written; copied > available {
		copied = available
	}
	if copied > 0 {
		copy(
			frame.thread.values[resultBase+written:resultBase+written+copied],
			values[:copied],
		)
		written += copied
	}
	frame.thread.fillNil(resultBase+written, resultBase+outputCount)
	return frame.sealReturn(outputCount)
}

// callBinary invokes callable with exactly two compact arguments and keeps one
// compact result, the shape every library callback and comparator uses.
//
// Lua 5.1 makes these calls with lua_call, so a failure is not caught: it is
// returned here and the library propagates it. The nested traceback segment is
// captured before the private call machinery is restored, which keeps the
// executor's one-segment-per-frame rule intact. Neither the arguments nor the
// result becomes an owning Value.
func (frame Frame) callBinary(
	callable slot,
	first slot,
	second slot,
) (slot, *Error) {
	arguments := [2]slot{first, second}
	return frame.callCompactOne(callable, arguments[:])
}

// lessThan applies Lua 5.1's ordinary less-than to two compact values, as
// lua_lessthan does for a library.
//
// Numbers and byte strings compare directly; other like-typed values require
// both sides to name the same raw __lt handler. The runtime raises its
// comparison failures from inside the C frame, so they carry no source
// position.
func (frame Frame) lessThan(left, right slot) (bool, *Error) {
	leftKind := left.kind()
	rightKind := right.kind()
	if leftKind != rightKind {
		return false, newNativeRuntimeError(
			frame.thread,
			fmt.Sprintf(
				"attempt to compare %s with %s",
				leftKind,
				rightKind,
			),
		)
	}
	switch leftKind {
	case NumberKind:
		return math.Float64frombits(left.bits) <
			math.Float64frombits(right.bits), nil
	case StringKind:
		return (*luaString)(left.ref).text <
			(*luaString)(right.ref).text, nil
	}

	method, found := matchingMetamethod(
		frame.thread,
		left,
		right,
		metaLessThan,
	)
	if !found {
		return false, newNativeRuntimeError(
			frame.thread,
			fmt.Sprintf("attempt to compare two %s values", leftKind),
		)
	}
	result, failure := frame.callBinary(method, left, right)
	if failure != nil {
		return false, failure
	}
	return truthySlot(result), nil
}

// truthySlot applies Lua's truth rule: only nil and false are false.
func truthySlot(value slot) bool {
	return value.ref != nilMarkerPointer &&
		value.ref != falseMarkerPointer
}

// libraryError completes a callback with luaL_error's positioned failure.
func libraryError(
	frame Frame,
	format string,
	arguments ...any,
) Outcome {
	return frame.sealError(libraryFailure(frame, format, arguments...))
}

// libraryFailure builds luaL_error's positioned failure without completing the
// callback, for library operations that must unwind their own state first.
func libraryFailure(
	frame Frame,
	format string,
	arguments ...any,
) *Error {
	message := fmt.Sprintf(format, arguments...)
	if prototype, pc, found := immediateLuaCaller(frame); found {
		message = executionErrorDescription(prototype, pc, message)
	}
	return &Error{
		value:       frame.thread.state.String(message),
		description: message,
		category:    RuntimeError,
	}
}

func immediateLuaCaller(frame Frame) (*Prototype, int, bool) {
	frame.activation()
	if len(frame.thread.frames) < 2 {
		return nil, 0, false
	}
	caller := &frame.thread.frames[len(frame.thread.frames)-2]
	if caller.function == nil || caller.function.prototype == nil {
		return nil, 0, false
	}
	prototype := caller.function.prototype
	pc := int(caller.pc) - 1
	if pc < 0 || pc >= len(prototype.code) {
		return nil, 0, false
	}
	return prototype, pc, true
}
