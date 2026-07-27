package lua

import (
	"io"
	"strconv"
	"strings"
)

var debugLibraryFunctions = [...]struct {
	name  string
	entry NativeFunc
}{
	{name: "debug", entry: debugConsole},
	{name: "getfenv", entry: debugGetEnvironment},
	{name: "getinfo", entry: debugGetInfo},
	{name: "getlocal", entry: debugGetLocal},
	{name: "getregistry", entry: debugGetRegistry},
	{name: "getmetatable", entry: debugGetMetatable},
	{name: "getupvalue", entry: debugGetUpvalue},
	{name: "setfenv", entry: debugSetEnvironment},
	{name: "setlocal", entry: debugSetLocal},
	{name: "setmetatable", entry: debugSetMetatable},
	{name: "setupvalue", entry: debugSetUpvalue},
	{name: "traceback", entry: debugTraceback},
}

// OpenDebug installs the Lua 5.1 debug inspection library.
//
// The library deliberately exposes mutable execution state, raw metatables,
// and the registry. Applications executing untrusted Lua should not open it.
// Instruction hooks are not installed because exact hooks would add work to
// ordinary execution, which Badger deliberately keeps unchanged.
// Opening again replaces the debug table and its functions with fresh
// canonical objects.
func (state *State) OpenDebug() error {
	if err := state.checkOpen(); err != nil {
		return err
	}
	loaded, err := state.ensureLoadedModules()
	if err != nil {
		return err
	}
	library := newTable(state, 0, len(debugLibraryFunctions))
	for _, definition := range debugLibraryFunctions {
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
		"debug",
		slotFromTableObject(library),
	); err != nil {
		return err
	}
	state.setLoadedModule(loaded, "debug", slotFromTableObject(library))
	return nil
}

func debugGetRegistry(frame Frame) Outcome {
	return frame.returnOne(
		frame.activation(),
		slotFromTableObject(frame.thread.state.registry),
	)
}

func debugGetMetatable(frame Frame) Outcome {
	value, present := frame.argument(0)
	if !present {
		return baseArgumentError(frame, 0, "value expected")
	}
	metatable := metatableForSlot(frame.thread, value)
	if metatable == nil {
		return frame.ReturnNil()
	}
	return frame.returnOne(
		frame.activation(),
		slotFromTableObject(metatable),
	)
}

func debugSetMetatable(frame Frame) Outcome {
	metatableValue, present := frame.argument(1)
	if !present ||
		(!metatableValue.isNil() && !metatableValue.isTable()) {
		return baseArgumentError(frame, 1, "nil or table expected")
	}
	var metatable *tableObject
	if metatableValue.isTable() {
		metatable = tableObjectFromSlot(metatableValue)
	}
	value, present := frame.argument(0)
	if !present {
		value = nilSlot
	}
	switch value.kind() {
	case TableKind:
		tableObjectFromSlot(value).metatable = metatable
	case UserDataKind:
		userDataObjectFromSlot(value).metatable = metatable
	default:
		frame.thread.state.typeMetatables[value.kind()] = metatable
	}
	return frame.ReturnBool(true)
}

func debugGetEnvironment(frame Frame) Outcome {
	value, present := frame.argument(0)
	if !present {
		return baseArgumentError(frame, 0, "value expected")
	}
	var environment *tableObject
	switch value.kind() {
	case FunctionKind:
		environment = functionObjectFromSlot(value).environment
	case UserDataKind:
		environment = userDataObjectFromSlot(value).environment
	case ThreadKind:
		environment = threadObjectFromSlot(value).globals
	}
	if environment == nil {
		return frame.ReturnNil()
	}
	return frame.returnOne(
		frame.activation(),
		slotFromTableObject(environment),
	)
}

func debugSetEnvironment(frame Frame) Outcome {
	environment, ok := frame.tableObject(1)
	if !ok {
		return baseArgumentTypeError(frame, 1, "table")
	}
	value, present := frame.argument(0)
	if !present {
		value = nilSlot
	}
	switch value.kind() {
	case FunctionKind:
		functionObjectFromSlot(value).environment = environment
	case UserDataKind:
		userDataObjectFromSlot(value).environment = environment
	case ThreadKind:
		threadObjectFromSlot(value).globals = environment
	default:
		return libraryError(
			frame,
			"'setfenv' cannot change environment of given object",
		)
	}
	return frame.returnOne(frame.activation(), value)
}

func debugGetUpvalue(frame Frame) Outcome {
	index, ok := frame.integerArgument(1)
	if !ok {
		return numberArgumentError(frame, 1)
	}
	function, ok := frame.functionObject(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "function")
	}
	if function.prototype == nil ||
		index <= 0 ||
		index > int(function.prototype.upvalues) {
		return frame.Return()
	}
	upvalueIndex := index - 1
	name := function.prototype.debugUpvalueName(upvalueIndex)
	return frame.returnCompactValues(
		[2]slot{
			stringSlot(frame.thread.owner.strings.make(name)),
			function.luaUpvalueUnchecked(upvalueIndex).read(),
		},
		2,
		nil,
	)
}

func debugSetUpvalue(frame Frame) Outcome {
	value, present := frame.argument(2)
	if !present {
		return baseArgumentError(frame, 2, "value expected")
	}
	index, ok := frame.integerArgument(1)
	if !ok {
		return numberArgumentError(frame, 1)
	}
	function, ok := frame.functionObject(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "function")
	}
	if function.prototype == nil ||
		index <= 0 ||
		index > int(function.prototype.upvalues) {
		return frame.Return()
	}
	upvalueIndex := index - 1
	function.luaUpvalueUnchecked(upvalueIndex).write(value)
	return frame.ReturnString(
		function.prototype.debugUpvalueName(upvalueIndex),
	)
}

func debugTargetThread(frame Frame) (*threadObject, int) {
	frame.activation()
	if target, ok := frame.threadObject(0); ok {
		return target, 1
	}
	return frame.thread, 0
}

type debugInfoTarget struct {
	thread     *threadObject
	function   *functionObject
	activation debugActivation
	active     bool
	tail       bool
}

func debugGetInfo(frame Frame) Outcome {
	thread, offset := debugTargetThread(frame)
	options := "flnSu"
	if supplied, present := frame.argument(offset + 1); present &&
		!supplied.isNil() {
		var ok bool
		options, ok = frame.textArgument(offset + 1)
		if !ok {
			return baseArgumentTypeError(frame, offset+1, "string")
		}
		options = luaCString(options)
	}

	targetValue, present := frame.argument(offset)
	var target debugInfoTarget
	target.thread = thread
	if present {
		if levelNumber, numeric := slotToNumber(targetValue); numeric {
			level := libraryInteger(levelNumber)
			record, found := thread.debugActivation(level)
			if !found {
				return frame.ReturnNil()
			}
			target.activation = record
			target.active = true
			target.tail = record.isTail()
			if !target.tail {
				target.function = record.frame.function
			}
		} else if targetValue.isFunction() {
			target.function = functionObjectFromSlot(targetValue)
		} else {
			return baseArgumentError(
				frame,
				offset,
				"function or level expected",
			)
		}
	} else {
		return baseArgumentError(
			frame,
			offset,
			"function or level expected",
		)
	}

	for index := 0; index < len(options); index++ {
		switch options[index] {
		case 'S', 'l', 'u', 'n', 'L', 'f':
		default:
			return baseArgumentError(
				frame,
				offset+1,
				"invalid option",
			)
		}
	}

	result := newTable(frame.thread.state, 0, 12)
	if strings.IndexByte(options, 'S') >= 0 {
		source, short, what, first, last :=
			debugSourceInfo(target)
		debugSetString(result, "source", source)
		debugSetString(result, "short_src", short)
		debugSetNumber(result, "linedefined", first)
		debugSetNumber(result, "lastlinedefined", last)
		debugSetString(result, "what", what)
	}
	if strings.IndexByte(options, 'l') >= 0 {
		line := -1
		if target.active && !target.tail {
			line = target.activation.currentLine()
		}
		debugSetNumber(result, "currentline", line)
	}
	if strings.IndexByte(options, 'u') >= 0 {
		count := 0
		if target.function != nil {
			if target.function.prototype != nil {
				count = int(target.function.prototype.upvalues)
			} else {
				count = len(
					target.function.nativeBodyUnchecked().captures,
				)
			}
		}
		debugSetNumber(result, "nups", count)
	}
	if strings.IndexByte(options, 'n') >= 0 {
		category := ""
		if target.tail {
			debugSetString(result, "name", "")
		} else if target.active {
			if resolved, name, found :=
				target.activation.functionName(thread); found {
				category = resolved
				debugSetString(result, "name", name)
			}
		}
		debugSetString(result, "namewhat", category)
	}
	if strings.IndexByte(options, 'L') >= 0 {
		lines := debugActiveLines(frame.thread.state, target.function)
		debugSetSlot(result, "activelines", lines)
	}
	if strings.IndexByte(options, 'f') >= 0 {
		value := nilSlot
		if target.function != nil {
			value = slotFromFunctionObject(target.function)
		}
		debugSetSlot(result, "func", value)
	}
	return frame.returnOne(
		frame.activation(),
		slotFromTableObject(result),
	)
}

func debugSourceInfo(
	target debugInfoTarget,
) (source, short, what string, first, last int) {
	if target.tail {
		source = "=(tail call)"
		return source, sourceID(source), "tail", -1, -1
	}
	if target.function == nil || target.function.prototype == nil {
		source = "=[C]"
		return source, sourceID(source), "C", -1, -1
	}
	prototype := target.function.prototype
	source = luaCString(prototype.SourceName())
	first, last = prototype.LineRange()
	what = "Lua"
	if first == 0 {
		what = "main"
	}
	return source, sourceID(source), what, first, last
}

func debugActiveLines(state *State, function *functionObject) slot {
	if function == nil || function.prototype == nil {
		return nilSlot
	}
	prototype := function.prototype
	hint := 0
	if prototype.debug != nil {
		hint = len(prototype.debug.lines)
	}
	lines := newTable(state, 0, hint)
	if prototype.debug != nil {
		for _, line := range prototype.debug.lines {
			lines.rawSetSlot(numberSlot(float64(line)), trueSlot)
		}
	}
	return slotFromTableObject(lines)
}

func debugSetString(table *tableObject, name, value string) {
	debugSetSlot(
		table,
		name,
		stringSlot(table.owner.strings.make(value)),
	)
}

func debugSetNumber(table *tableObject, name string, value int) {
	debugSetSlot(table, name, numberSlot(float64(value)))
}

func debugSetSlot(table *tableObject, name string, value slot) {
	if err := table.rawSetStringSlot(name, value); err != nil {
		panic(err)
	}
}

func debugGetLocal(frame Frame) Outcome {
	thread, offset := debugTargetThread(frame)
	level, ok := frame.integerArgument(offset)
	if !ok {
		return numberArgumentError(frame, offset)
	}
	record, found := thread.debugActivation(level)
	if !found {
		return baseArgumentError(frame, offset, "level out of range")
	}
	ordinal, ok := frame.integerArgument(offset + 1)
	if !ok {
		return numberArgumentError(frame, offset+1)
	}
	name, stackIndex, found := record.local(thread, ordinal)
	if !found {
		return frame.ReturnNil()
	}
	return frame.returnCompactValues(
		[2]slot{
			stringSlot(frame.thread.owner.strings.make(name)),
			thread.values[stackIndex],
		},
		2,
		nil,
	)
}

func debugSetLocal(frame Frame) Outcome {
	thread, offset := debugTargetThread(frame)
	level, ok := frame.integerArgument(offset)
	if !ok {
		return numberArgumentError(frame, offset)
	}
	record, found := thread.debugActivation(level)
	if !found {
		return baseArgumentError(frame, offset, "level out of range")
	}
	value, present := frame.argument(offset + 2)
	if !present {
		return baseArgumentError(frame, offset+2, "value expected")
	}
	ordinal, ok := frame.integerArgument(offset + 1)
	if !ok {
		return numberArgumentError(frame, offset+1)
	}
	name, stackIndex, found := record.local(thread, ordinal)
	if !found {
		return frame.ReturnNil()
	}
	writeSlot(&thread.values[stackIndex], value)
	return frame.ReturnString(name)
}

func debugTraceback(frame Frame) Outcome {
	thread, offset := debugTargetThread(frame)
	message := ""
	hasMessage := false
	if value, present := frame.argument(offset); present {
		var ok bool
		message, ok = compactText(value)
		if !ok {
			return frame.returnOne(frame.activation(), value)
		}
		hasMessage = true
	}

	level := 0
	if thread == frame.thread {
		level = 1
	}
	if value, present := frame.argument(offset + 1); present {
		if number, ok := slotToNumber(value); ok {
			level = libraryInteger(number)
		}
	}

	records := make([]debugActivation, 0, 16)
	for current := level; ; current++ {
		record, found := thread.debugActivation(current)
		if !found {
			break
		}
		records = append(records, record)
	}

	var output strings.Builder
	output.Grow(len(message) + 32 + len(records)*48)
	if hasMessage {
		output.WriteString(message)
		output.WriteByte('\n')
	}
	output.WriteString("stack traceback:")
	if len(records) <= 21 {
		for _, record := range records {
			debugAppendTraceFrame(&output, thread, record)
		}
	} else {
		for _, record := range records[:11] {
			debugAppendTraceFrame(&output, thread, record)
		}
		output.WriteString("\n\t...")
		for _, record := range records[len(records)-10:] {
			debugAppendTraceFrame(&output, thread, record)
		}
	}
	return frame.ReturnString(output.String())
}

func debugAppendTraceFrame(
	output *strings.Builder,
	thread *threadObject,
	record debugActivation,
) {
	source, _, what, first, _ := debugSourceInfo(debugInfoTarget{
		function:   record.frame.function,
		activation: record,
		active:     true,
		tail:       record.isTail(),
	})
	short := sourceID(source)
	output.WriteString("\n\t")
	output.WriteString(short)
	output.WriteByte(':')
	if line := record.currentLine(); line > 0 {
		output.WriteString(strconv.Itoa(line))
		output.WriteByte(':')
	}
	if _, name, found := record.functionName(thread); found {
		output.WriteString(" in function '")
		output.WriteString(name)
		output.WriteByte('\'')
		return
	}
	switch what {
	case "main":
		output.WriteString(" in main chunk")
	case "C", "tail":
		output.WriteString(" ?")
	default:
		output.WriteString(" in function <")
		output.WriteString(short)
		output.WriteByte(':')
		output.WriteString(strconv.Itoa(first))
		output.WriteByte('>')
	}
}

func debugConsole(frame Frame) Outcome {
	state := frame.thread.state
	input := &state.streams.stdin
	output := &state.streams.stderr
	for {
		_, _ = output.WriteString("lua_debug> ")
		_ = output.Flush()
		line, err := input.ReadString('\n')
		if err != nil && len(line) == 0 {
			return frame.Return()
		}
		if line == "cont\n" {
			return frame.Return()
		}

		function, loadErr := state.LoadString(
			"=(debug command)",
			line,
		)
		if loadErr == nil {
			if callErr := frame.callCompactNone(
				slotFromFunctionObject(function.runtimeObject()),
				nil,
			); callErr != nil {
				loadErr = callErr
			}
		}
		if loadErr != nil {
			_, _ = output.WriteString(loadErr.Error())
			_, _ = output.WriteString("\n")
			_ = output.Flush()
		}
		if err != nil {
			if err != io.EOF {
				_, _ = output.WriteString(err.Error())
				_, _ = output.WriteString("\n")
				_ = output.Flush()
			}
			return frame.Return()
		}
	}
}
