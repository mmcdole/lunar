package lua

import "fmt"

var coroutineLibraryFunctions = [...]struct {
	name  string
	entry NativeFunc
}{
	{name: "create", entry: coroutineCreate},
	{name: "resume", entry: coroutineResume},
	{name: "running", entry: coroutineRunning},
	{name: "status", entry: coroutineStatus},
	{name: "wrap", entry: coroutineWrap},
	{name: "yield", entry: coroutineYield},
}

// OpenCoroutine installs the Lua 5.1 coroutine library.
//
// Opening is explicit and idempotent in effect. Each call replaces the global
// coroutine table and its functions with fresh canonical objects.
func (state *State) OpenCoroutine() error {
	if err := state.checkOpen(); err != nil {
		return err
	}
	loaded, err := state.ensureLoadedModules()
	if err != nil {
		return err
	}
	library, err := state.NewTable(0, len(coroutineLibraryFunctions))
	if err != nil {
		return err
	}
	for _, definition := range coroutineLibraryFunctions {
		function, functionErr := state.NewNativeFunction(definition.entry)
		if functionErr != nil {
			return functionErr
		}
		if setErr := library.RawSetString(
			definition.name,
			function.Value(),
		); setErr != nil {
			return setErr
		}
	}
	if err := state.globalEnvironment().RawSetString(
		"coroutine",
		library.Value(),
	); err != nil {
		return err
	}
	state.setLoadedModule(loaded, "coroutine", slotFromTable(library))
	return nil
}

func coroutineCreate(frame Frame) Outcome {
	function, ok := frame.Function(0)
	if !ok || function.prototype == nil {
		return baseArgumentError(
			frame,
			0,
			"Lua function expected",
		)
	}
	thread, err := frame.State().NewThread(function.Value())
	if err != nil {
		return frame.RaiseString(err.Error())
	}
	return frame.ReturnValue(thread.Value())
}

func coroutineResume(frame Frame) Outcome {
	thread, ok := frame.LuaThread(0)
	if !ok {
		return baseArgumentError(
			frame,
			0,
			"coroutine expected",
		)
	}
	call := frame.activation()
	base := int(call.base)
	parent := frame.thread
	run := resumeThread(
		parent,
		thread,
		resumeArguments{
			compact: parent.values[base+1 : parent.top],
		},
	)
	defer run.release()
	if run.failure != nil {
		if run.status == ThreadSuspended ||
			!isCatchableProtectedFailure(run.failure) {
			return frame.sealError(run.failure)
		}
		errorValue := run.failure.mustValueSlot()
		return frame.returnCompactValues(
			[2]slot{falseSlot, errorValue},
			2,
			nil,
		)
	}
	return frame.returnCompactValues(
		[2]slot{trueSlot},
		1,
		run.thread.values[run.first:run.first+run.count],
	)
}

func coroutineRunning(frame Frame) Outcome {
	if frame.Thread().main {
		return frame.ReturnNil()
	}
	return frame.ReturnValue(frame.Thread().Value())
}

func coroutineStatus(frame Frame) Outcome {
	thread, ok := frame.LuaThread(0)
	if !ok {
		return baseArgumentError(
			frame,
			0,
			"coroutine expected",
		)
	}
	return frame.ReturnString(
		coroutineStatusName(frame.thread, thread),
	)
}

func coroutineWrap(frame Frame) Outcome {
	function, ok := frame.Function(0)
	if !ok || function.prototype == nil {
		return baseArgumentError(
			frame,
			0,
			"Lua function expected",
		)
	}
	thread, err := frame.State().NewThread(function.Value())
	if err != nil {
		return frame.RaiseString(err.Error())
	}
	wrapper, err := frame.State().NewNativeFunction(
		coroutineWrappedResume,
		thread.Value(),
	)
	if err != nil {
		return frame.RaiseString(err.Error())
	}
	return frame.ReturnValue(wrapper.Value())
}

func coroutineYield(frame Frame) Outcome {
	return frame.YieldArguments()
}

func coroutineWrappedResume(frame Frame) Outcome {
	threadValue := frame.Capture(0)
	thread, ok := threadValue.Thread()
	if !ok {
		panic("lua: coroutine wrapper lost its thread")
	}
	call := frame.activation()
	base := int(call.base)
	parent := frame.thread
	run := resumeThread(
		parent,
		thread,
		resumeArguments{
			compact: parent.values[base:parent.top],
		},
	)
	defer run.release()
	if run.failure != nil {
		if run.status == ThreadSuspended ||
			!isCatchableProtectedFailure(run.failure) {
			return frame.sealError(run.failure)
		}
		value := run.failure.mustValueSlot()
		if kind := value.kind(); kind == StringKind || kind == NumberKind {
			text, _ := compactText(value)
			value = stringSlot(
				frame.thread.owner.strings.make(
					nativeCallerPrefix(frame) + text,
				),
			)
		}
		return frame.raiseCompact(value)
	}
	return frame.returnCompactValues(
		[2]slot{},
		0,
		run.thread.values[run.first:run.first+run.count],
	)
}

func nativeCallerPrefix(frame Frame) string {
	prototype, pc, found := immediateLuaCaller(frame)
	if !found {
		return ""
	}
	line := prototype.LineAt(pc)
	if line == 0 {
		return ""
	}
	return fmt.Sprintf(
		"%s:%d: ",
		sourceID(prototype.SourceName()),
		line,
	)
}
