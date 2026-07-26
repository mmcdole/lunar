package lua

import (
	"os"
)

var osLibraryFunctions = [...]struct {
	name  string
	entry NativeFunc
}{
	{name: "difftime", entry: osDifferenceTime},
	{name: "getenv", entry: osGetEnvironment},
	{name: "remove", entry: osRemove},
	{name: "rename", entry: osRename},
	{name: "tmpname", entry: osTemporaryName},
}

// OpenOS installs the implemented portion of Lua 5.1's operating-system
// library.
//
// Opening is explicit and idempotent in effect. Each call replaces the global
// os table and every installed function with fresh canonical objects.
func (state *State) OpenOS() error {
	if err := state.checkOpen(); err != nil {
		return err
	}
	loaded, err := state.ensureLoadedModules()
	if err != nil {
		return err
	}
	library, err := state.NewTable(0, len(osLibraryFunctions))
	if err != nil {
		return err
	}
	for _, definition := range osLibraryFunctions {
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
		"os",
		library.Value(),
	); err != nil {
		return err
	}
	state.setLoadedModule(loaded, "os", slotFromTable(library))
	return nil
}

func osDifferenceTime(frame Frame) Outcome {
	later, ok := frame.numberArgument(0)
	if !ok {
		return numberArgumentError(frame, 0)
	}
	earlier := float64(0)
	if argument, present := frame.argument(1); present &&
		argument.kind() != NilKind {
		earlier, ok = frame.numberArgument(1)
		if !ok {
			return numberArgumentError(frame, 1)
		}
	}
	// PUC converts both Lua numbers to time_t before calling difftime. A
	// portable Go implementation defines the C conversion's out-of-range
	// cases by using the runtime's saturating signed-64-bit conversion.
	return frame.ReturnNumber(
		float64(saturatingInt64(later)) -
			float64(saturatingInt64(earlier)),
	)
}

func osGetEnvironment(frame Frame) Outcome {
	name, ok := frame.textArgument(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "string")
	}
	value, found := os.LookupEnv(luaCString(name))
	if !found {
		return frame.ReturnNil()
	}
	return frame.ReturnString(value)
}

func osRemove(frame Frame) Outcome {
	name, ok := frame.textArgument(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "string")
	}
	name = luaCString(name)
	if err := os.Remove(name); err != nil {
		return ioFailureResult(frame, name, err)
	}
	return frame.ReturnBool(true)
}

func osRename(frame Frame) Outcome {
	from, ok := frame.textArgument(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "string")
	}
	to, ok := frame.textArgument(1)
	if !ok {
		return baseArgumentTypeError(frame, 1, "string")
	}
	from = luaCString(from)
	to = luaCString(to)
	if err := os.Rename(from, to); err != nil {
		return ioFailureResult(frame, from, err)
	}
	return frame.ReturnBool(true)
}

func osTemporaryName(frame Frame) Outcome {
	file, err := os.CreateTemp("", "badger-lua-")
	if err != nil {
		return libraryError(
			frame,
			"unable to generate a unique filename",
		)
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return libraryError(
			frame,
			"unable to generate a unique filename",
		)
	}
	return frame.ReturnString(name)
}
