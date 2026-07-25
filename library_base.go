package lua

import "fmt"

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
	call := frame.call()
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
	call := frame.call()
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
	previousTop := thread.top
	previousExtent := thread.liveValueExtent()
	thread.clearInactive(base+2, previousTop)
	thread.top = base + 2
	thread.frameExtent = int(call.callerExtent)
	if thread.top > thread.frameExtent {
		thread.frameExtent = thread.top
	}
	thread.clearDeadSuffix(previousExtent)

	return runProtectedCall(
		frame,
		target,
		nil,
		handler,
		true,
	)
}

func baseArgumentError(
	frame Frame,
	index int,
	reason string,
) Outcome {
	prototype, pc, found := immediateLuaCaller(frame)
	name := "?"
	if found {
		call := prototype.code[pc]
		if operation := call.opcode(); operation == opCall ||
			operation == opTailCall {
			if _, candidate, named := prototype.describeOperand(
				pc,
				call.a(),
			); named {
				name = candidate
			}
		}
	}
	message := fmt.Sprintf(
		"bad argument #%d to '%s' (%s)",
		index+1,
		name,
		reason,
	)
	if found {
		message = executionErrorDescription(
			prototype,
			pc,
			message,
		)
	}
	return frame.raiseString(message)
}

func immediateLuaCaller(frame Frame) (*Prototype, int, bool) {
	frame.call()
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
