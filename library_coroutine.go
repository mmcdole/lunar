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
	library := newTable(
		state.runtime,
		0,
		len(coroutineLibraryFunctions),
	)
	for _, definition := range coroutineLibraryFunctions {
		function, functionErr := state.newNativeFunctionObject(
			definition.entry,
			nil,
		)
		if functionErr != nil {
			return functionErr
		}
		if setErr := library.rawSetStringSlot(
			definition.name,
			slotFromFunctionObject(function),
		); setErr != nil {
			return setErr
		}
	}
	if err := state.globalEnvironment().rawSetStringSlot(
		"coroutine",
		slotFromTableObject(library),
	); err != nil {
		return err
	}
	state.setLoadedModule(loaded, "coroutine", slotFromTableObject(library))
	return nil
}

func coroutineCreate(frame Frame) Outcome {
	function, ok := frame.functionObject(0)
	if !ok || function.prototype == nil {
		return baseArgumentError(
			frame,
			0,
			"Lua function expected",
		)
	}
	thread, err := frame.thread.state.newThreadObject(
		slotFromFunctionObject(function),
	)
	if err != nil {
		return frame.RaiseString(err.Error())
	}
	return frame.returnOne(frame.activation(), slotFromThreadObject(thread))
}

func coroutineResume(frame Frame) Outcome {
	thread, ok := frame.threadObject(0)
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
	if frame.thread.main {
		return frame.ReturnNil()
	}
	return frame.returnOne(
		frame.activation(),
		slotFromThreadObject(frame.thread),
	)
}

func coroutineStatus(frame Frame) Outcome {
	thread, ok := frame.threadObject(0)
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
	function, ok := frame.functionObject(0)
	if !ok || function.prototype == nil {
		return baseArgumentError(
			frame,
			0,
			"Lua function expected",
		)
	}
	thread, err := frame.thread.state.newThreadObject(
		slotFromFunctionObject(function),
	)
	if err != nil {
		return frame.RaiseString(err.Error())
	}
	wrapper, err := frame.thread.state.newNativeFunctionObject(
		coroutineWrappedResume,
		[]slot{slotFromThreadObject(thread)},
	)
	if err != nil {
		return frame.RaiseString(err.Error())
	}
	return frame.returnOne(
		frame.activation(),
		slotFromFunctionObject(wrapper),
	)
}

func coroutineYield(frame Frame) Outcome {
	return frame.YieldArguments()
}

func coroutineWrappedResume(frame Frame) Outcome {
	threadValue := frame.nativeCapture(0)
	if !threadValue.isThread() {
		panic("lua: coroutine wrapper lost its thread")
	}
	thread := threadObjectFromSlot(threadValue)
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
