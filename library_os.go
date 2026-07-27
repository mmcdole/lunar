package lua

import "os"

var osLibraryFunctions = [...]struct {
	name  string
	entry NativeFunc
}{
	{name: "clock", entry: osClock},
	{name: "date", entry: osDate},
	{name: "difftime", entry: osDifferenceTime},
	{name: "execute", entry: osExecute},
	{name: "exit", entry: osExit},
	{name: "getenv", entry: osGetEnvironment},
	{name: "remove", entry: osRemove},
	{name: "rename", entry: osRename},
	{name: "setlocale", entry: osSetLocale},
	{name: "time", entry: osTime},
	{name: "tmpname", entry: osTemporaryName},
}

// OpenOS installs Lua 5.1's operating-system library.
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
	library := newTable(state.runtime, 0, len(osLibraryFunctions))
	for _, definition := range osLibraryFunctions {
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
		"os",
		slotFromTableObject(library),
	); err != nil {
		return err
	}
	state.setLoadedModule(loaded, "os", slotFromTableObject(library))
	return nil
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

func osExecute(frame Frame) Outcome {
	value, present := frame.argument(0)
	if !present || value.isNil() {
		if hostShellAvailable() {
			return frame.ReturnNumber(1)
		}
		return frame.ReturnNumber(0)
	}
	command, ok := frame.textArgument(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "string")
	}
	status, cancelled := executeHostShell(
		frame.Context(),
		luaCString(command),
	)
	if cancelled {
		failure := pollExecutionContext(frame.thread)
		if failure == nil {
			failure = newContextError(frame.Context(), true)
		}
		return frame.sealError(failure)
	}
	return frame.ReturnNumber(float64(status))
}

func osExit(frame Frame) Outcome {
	code, outcome, failed := optionalLibraryInteger(frame, 0, 0)
	if failed {
		return outcome
	}
	return frame.sealError(newExitError(code))
}

func osRemove(frame Frame) Outcome {
	name, ok := frame.textArgument(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "string")
	}
	name = luaCString(name)
	if err := os.Remove(name); err != nil {
		return ioNamedFailureResult(frame, name, err)
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
		return ioNamedFailureResult(frame, from, err)
	}
	return frame.ReturnBool(true)
}

func osSetLocale(frame Frame) Outcome {
	locale := ""
	query := true
	if argument, present := frame.argument(0); present &&
		!argument.isNil() {
		var ok bool
		locale, ok = frame.textArgument(0)
		if !ok {
			return baseArgumentTypeError(frame, 0, "string")
		}
		locale = luaCString(locale)
		query = false
	}

	category := "all"
	if argument, present := frame.argument(1); present &&
		!argument.isNil() {
		var ok bool
		category, ok = frame.textArgument(1)
		if !ok {
			return baseArgumentTypeError(frame, 1, "string")
		}
		category = luaCString(category)
	}
	switch category {
	case "all", "collate", "ctype", "monetary", "numeric", "time":
	default:
		return baseArgumentError(
			frame,
			1,
			"invalid option '"+category+"'",
		)
	}

	// Locale is process-global in C, while Badger States may execute
	// independently. Numeric parsing, byte-string ordering, and date names
	// therefore use one deterministic C locale. Querying it and selecting C,
	// POSIX, or the host-default request all resolve to that same locale.
	if query || locale == "" || locale == "C" || locale == "POSIX" {
		return frame.ReturnString("C")
	}
	return frame.ReturnNil()
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
