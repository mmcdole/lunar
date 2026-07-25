package lua

import "fmt"

// OpenBase installs the currently implemented Lua 5.1 base-library globals.
//
// The base library is still under construction; this currently installs _G,
// _VERSION, pcall, and xpcall. Opening is explicit: New returns an empty
// State. Calling OpenBase again replaces the functions with fresh canonical
// objects and restores the other globals.
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
	return state.globals.RawSetString("xpcall", xpcall.Value())
}

func basePCall(frame Frame) Outcome {
	call := frame.call()
	thread := frame.thread
	base := int(call.base)
	count := thread.top - base
	if count == 0 {
		return baseArgumentError(frame, "pcall", 0, "value expected")
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
		return baseArgumentError(frame, "xpcall", 1, "value expected")
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
	function string,
	index int,
	reason string,
) Outcome {
	frame.call()
	message := fmt.Sprintf(
		"bad argument #%d to '%s' (%s)",
		index+1,
		function,
		reason,
	)
	for caller := len(frame.thread.frames) - 2; caller >= 0; caller-- {
		activation := frame.thread.frames[caller]
		if activation.function == nil || activation.function.prototype == nil {
			continue
		}
		message = executionErrorDescription(
			activation.function.prototype,
			int(activation.pc)-1,
			message,
		)
		break
	}
	return frame.raiseString(message)
}
