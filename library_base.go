package lua

import (
	"fmt"
	"math"
	"strings"
)

var baseLibraryFunctions = [...]struct {
	name  string
	entry NativeFunc
}{
	{name: "assert", entry: baseAssert},
	{name: "collectgarbage", entry: baseCollectGarbage},
	{name: "dofile", entry: baseDoFile},
	{name: "error", entry: baseError},
	{name: "gcinfo", entry: baseGCInfo},
	{name: "getfenv", entry: baseGetEnvironment},
	{name: "getmetatable", entry: baseGetMetatable},
	{name: "load", entry: baseLoad},
	{name: "loadfile", entry: baseLoadFile},
	{name: "loadstring", entry: baseLoadString},
	{name: "next", entry: baseNext},
	{name: "pcall", entry: basePCall},
	{name: "print", entry: basePrint},
	{name: "rawequal", entry: baseRawEqual},
	{name: "rawget", entry: baseRawGet},
	{name: "rawset", entry: baseRawSet},
	{name: "select", entry: baseSelect},
	{name: "setfenv", entry: baseSetEnvironment},
	{name: "setmetatable", entry: baseSetMetatable},
	{name: "tonumber", entry: baseToNumber},
	{name: "tostring", entry: baseToString},
	{name: "type", entry: baseType},
	{name: "unpack", entry: baseUnpack},
	{name: "xpcall", entry: baseXPCall},
}

// OpenBase installs the Lua 5.1 base-library globals.
//
// loadfile and dofile obey the State's ScriptLoader; installing the base
// library grants no script-file access. Calling OpenBase again replaces every
// installed function and the coroutine table with fresh canonical objects and
// restores _G and _VERSION.
func (state *State) OpenBase() error {
	if err := state.checkOpen(); err != nil {
		return err
	}
	loaded, err := state.ensureLoadedModules()
	if err != nil {
		return err
	}
	globals := state.globalEnvironment()
	functions := make([]*functionObject, len(baseLibraryFunctions))
	for index, definition := range baseLibraryFunctions {
		function, functionErr := state.newNativeFunctionObject(
			definition.entry,
			nil,
		)
		if functionErr != nil {
			return functionErr
		}
		functions[index] = function
	}
	ipairsIterator, err := state.newNativeFunctionObject(
		baseIPairsIterator,
		nil,
	)
	if err != nil {
		return err
	}
	pairsIterator, err := state.newNativeFunctionObject(baseNext, nil)
	if err != nil {
		return err
	}
	pairs, err := state.newNativeFunctionObject(
		basePairs,
		[]slot{slotFromFunctionObject(pairsIterator)},
	)
	if err != nil {
		return err
	}
	ipairs, err := state.newNativeFunctionObject(
		baseIPairs,
		[]slot{slotFromFunctionObject(ipairsIterator)},
	)
	if err != nil {
		return err
	}
	proxyMetatables := newTable(state, 0, 1)
	proxyMetatables.metatable = proxyMetatables
	if err := proxyMetatables.rawSetStringSlot(
		metaMode.name(),
		stringSlot(state.runtime.strings.make("kv")),
	); err != nil {
		return err
	}
	newProxy, err := state.newNativeFunctionObject(
		baseNewProxy,
		[]slot{slotFromTableObject(proxyMetatables)},
	)
	if err != nil {
		return err
	}

	if err := globals.rawSetStringSlot("_G", slotFromTableObject(globals)); err != nil {
		return err
	}
	if err := globals.rawSetStringValue(
		"_VERSION",
		state.String("Lua 5.1"),
	); err != nil {
		return err
	}
	for index, definition := range baseLibraryFunctions {
		if err := globals.rawSetStringSlot(
			definition.name,
			slotFromFunctionObject(functions[index]),
		); err != nil {
			return err
		}
	}
	if err := globals.rawSetStringSlot(
		"pairs",
		slotFromFunctionObject(pairs),
	); err != nil {
		return err
	}
	if err := globals.rawSetStringSlot(
		"ipairs",
		slotFromFunctionObject(ipairs),
	); err != nil {
		return err
	}
	if err := globals.rawSetStringSlot(
		"newproxy",
		slotFromFunctionObject(newProxy),
	); err != nil {
		return err
	}
	state.setLoadedModule(loaded, "_G", slotFromTableObject(globals))
	return state.OpenCoroutine()
}

func baseAssert(frame Frame) Outcome {
	value, present := frame.argument(0)
	if !present {
		return baseArgumentError(frame, 0, "value expected")
	}
	if truthySlot(value) {
		return frame.ReturnArguments()
	}

	message := "assertion failed!"
	if supplied, present := frame.argument(1); present &&
		!supplied.isNil() {
		var ok bool
		message, ok = frame.textArgument(1)
		if !ok {
			return baseArgumentTypeError(frame, 1, "string")
		}
	}
	if end := strings.IndexByte(message, 0); end >= 0 {
		message = message[:end]
	}
	return libraryError(frame, "%s", message)
}

func baseCollectGarbage(frame Frame) Outcome {
	option := "collect"
	if value, present := frame.argument(0); present && !value.isNil() {
		var ok bool
		option, ok = compactText(value)
		if !ok {
			return baseArgumentTypeError(frame, 0, "string")
		}
		option = luaCString(option)
	}

	switch option {
	case "stop", "restart", "collect", "count", "step",
		"setpause", "setstepmul":
	default:
		return baseArgumentError(
			frame,
			0,
			"invalid option '"+option+"'",
		)
	}

	amount, outcome, failed := optionalLibraryInteger(frame, 1, 0)
	if failed {
		return outcome
	}

	state := frame.thread.state
	control := &state.runtime.collection
	switch option {
	case "stop":
		control.setStopped(true)
		return frame.ReturnNumber(0)
	case "restart":
		control.setStopped(false)
		// PUC installs the current allocation total as its threshold.
		// Lunar records the equivalent request and services it at the
		// next graph-stable executor seam.
		control.requestCycle()
		return frame.ReturnNumber(0)
	case "collect":
		// PUC encodes stop in its next-allocation threshold. A full
		// collection installs a normal threshold again, so an explicit
		// collection also resumes automatic scheduling.
		control.setStopped(false)
		if _, failure := frame.collectAndFinalize(); failure != nil {
			return frame.sealError(failure)
		}
		control.setStopped(false)
		return frame.ReturnNumber(0)
	case "count":
		return frame.ReturnNumber(
			float64(state.semanticHeap().bytes) / 1024,
		)
	case "step":
		// The current collector is synchronous. One explicit step
		// therefore completes one whole cycle; amount is still parsed
		// with Lua 5.1's unconditional second-argument contract.
		_ = amount
		control.setStopped(false)
		if _, failure := frame.collectAndFinalize(); failure != nil {
			return frame.sealError(failure)
		}
		control.setStopped(false)
		return frame.ReturnBool(true)
	case "setpause":
		previous := control.pause
		control.pause = amount
		return frame.ReturnNumber(float64(previous))
	case "setstepmul":
		previous := control.stepMultiplier
		control.stepMultiplier = amount
		return frame.ReturnNumber(float64(previous))
	default:
		panic("lua: unreachable collection option")
	}
}

func baseGCInfo(frame Frame) Outcome {
	return frame.ReturnNumber(
		float64(frame.thread.state.semanticHeap().bytes >> 10),
	)
}

func baseError(frame Frame) Outcome {
	value, present := frame.argument(0)
	if !present {
		value = nilSlot
	}
	level := 1
	if supplied, present := frame.argument(1); present &&
		!supplied.isNil() {
		var ok bool
		level, ok = frame.integerArgument(1)
		if !ok {
			return numberArgumentError(frame, 1)
		}
	}
	frame.discardArgumentsAfter(1)

	if level > 0 &&
		(value.isString() || value.isNumber()) {
		text, _ := compactText(value)
		if prototype, pc, found := luaCallerAtLevel(frame, level); found {
			text = executionErrorDescription(prototype, pc, text)
		}
		return frame.raiseString(text)
	}
	return frame.raiseCompact(value)
}

func basePrint(frame Frame) Outcome {
	toString, failure := frame.indexCompact(
		slotFromTableObject(frame.thread.globals),
		stringSlot(frame.thread.owner.strings.make("tostring")),
	)
	if failure != nil {
		return frame.sealError(failure)
	}

	writer := &frame.thread.state.streams.stdout
	for index := 0; index < frame.ArgumentCount(); index++ {
		value, _ := frame.argument(index)
		arguments := [1]slot{value}
		textValue, callFailure := frame.callCompactOne(
			toString,
			arguments[:],
		)
		if callFailure != nil {
			return frame.sealError(callFailure)
		}
		text, ok := compactText(textValue)
		if !ok {
			return libraryError(
				frame,
				"'tostring' must return a string to 'print'",
			)
		}
		if index != 0 {
			_, _ = writer.WriteString("\t")
		}
		_, _ = writer.WriteString(luaCString(text))
	}
	_, _ = writer.WriteString("\n")
	return frame.Return()
}

type baseEnvironmentTarget struct {
	function       *functionObject
	number         float64
	numberArgument bool
}

func resolveBaseEnvironmentTarget(
	frame Frame,
	optional bool,
) (baseEnvironmentTarget, Outcome, bool) {
	value, present := frame.argument(0)
	if present && value.isFunction() {
		return baseEnvironmentTarget{
			function: functionObjectFromSlot(value),
		}, Outcome{}, false
	}

	number := float64(1)
	numberArgument := false
	if (present && !value.isNil()) || !optional {
		var ok bool
		number, ok = slotToNumber(value)
		if !ok {
			return baseEnvironmentTarget{},
				numberArgumentError(frame, 0),
				true
		}
		numberArgument = true
	}
	level := libraryInteger(number)
	if level < 0 {
		return baseEnvironmentTarget{},
			baseArgumentError(
				frame,
				0,
				"level must be non-negative",
			),
			true
	}

	activation, status := frame.thread.logicalFrame(level)
	switch status {
	case logicalFramePhysical:
		return baseEnvironmentTarget{
			function:       activation.function,
			number:         number,
			numberArgument: numberArgument,
		}, Outcome{}, false
	case logicalFrameTail:
		return baseEnvironmentTarget{},
			libraryError(
				frame,
				"no function environment for tail call at level %d",
				level,
			),
			true
	default:
		return baseEnvironmentTarget{},
			baseArgumentError(frame, 0, "invalid level"),
			true
	}
}

func baseGetEnvironment(frame Frame) Outcome {
	target, outcome, failed := resolveBaseEnvironmentTarget(frame, true)
	if failed {
		return outcome
	}
	environment := target.function.environment
	if target.function.prototype == nil {
		environment = frame.thread.globals
	}
	return frame.returnOne(
		frame.activation(),
		slotFromTableObject(environment),
	)
}

func baseSetEnvironment(frame Frame) Outcome {
	environment, ok := frame.tableObject(1)
	if !ok {
		return baseArgumentTypeError(frame, 1, "table")
	}
	target, outcome, failed := resolveBaseEnvironmentTarget(frame, false)
	if failed {
		return outcome
	}
	if target.numberArgument && target.number == 0 {
		frame.discardArgumentsAfter(2)
		frame.thread.globals = environment
		return frame.Return()
	}
	if target.function.prototype == nil {
		return libraryError(
			frame,
			"'setfenv' cannot change environment of given object",
		)
	}
	frame.discardArgumentsAfter(2)
	target.function.environment = environment
	return frame.returnOne(
		frame.activation(),
		slotFromFunctionObject(target.function),
	)
}

func baseGetMetatable(frame Frame) Outcome {
	value, present := frame.argument(0)
	if !present {
		return baseArgumentError(frame, 0, "value expected")
	}
	metatable := metatableForSlot(frame.thread, value)
	if metatable == nil {
		return frame.ReturnNil()
	}
	if protected, found := metamethodSlot(
		frame.thread,
		value,
		metaMetatable,
	); found {
		return frame.returnOne(frame.activation(), protected)
	}
	return frame.returnOne(
		frame.activation(),
		slotFromTableObject(metatable),
	)
}

func baseSetMetatable(frame Frame) Outcome {
	table, ok := frame.tableObject(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "table")
	}
	value, present := frame.argument(1)
	if !present ||
		!value.isNil() && !value.isTable() {
		return baseArgumentError(frame, 1, "nil or table expected")
	}
	if _, protected := metamethodSlot(
		frame.thread,
		slotFromTableObject(table),
		metaMetatable,
	); protected {
		return libraryError(frame, "cannot change a protected metatable")
	}

	var metatable *tableObject
	if value.isTable() {
		metatable = (*tableObject)(value.ref)
	}
	frame.discardArgumentsAfter(2)
	table.metatable = metatable
	return frame.returnOne(frame.activation(), slotFromTableObject(table))
}

func baseRawEqual(frame Frame) Outcome {
	left, present := frame.argument(0)
	if !present {
		return baseArgumentError(frame, 0, "value expected")
	}
	right, present := frame.argument(1)
	if !present {
		return baseArgumentError(frame, 1, "value expected")
	}
	return frame.ReturnBool(rawSlotEqual(left, right))
}

func baseRawGet(frame Frame) Outcome {
	table, ok := frame.tableObject(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "table")
	}
	key, present := frame.argument(1)
	if !present {
		return baseArgumentError(frame, 1, "value expected")
	}
	value, _ := table.rawSlot(key)
	return frame.returnOne(frame.activation(), value)
}

func baseRawSet(frame Frame) Outcome {
	table, ok := frame.tableObject(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "table")
	}
	key, present := frame.argument(1)
	if !present {
		return baseArgumentError(frame, 1, "value expected")
	}
	value, present := frame.argument(2)
	if !present {
		return baseArgumentError(frame, 2, "value expected")
	}
	switch table.rawSetSlot(key, value) {
	case tableKeyNil:
		return frame.raiseString("table index is nil")
	case tableKeyNaN:
		return frame.raiseString("table index is NaN")
	}
	frame.discardArgumentsAfter(3)
	return frame.returnOne(frame.activation(), slotFromTableObject(table))
}

func baseType(frame Frame) Outcome {
	value, present := frame.argument(0)
	if !present {
		return baseArgumentError(frame, 0, "value expected")
	}
	return frame.ReturnString(value.kind().String())
}

func baseNext(frame Frame) Outcome {
	table, ok := frame.tableObject(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "table")
	}
	previous, present := frame.argument(1)
	if !present {
		previous = nilSlot
	}
	key, value, found, err := table.next(previous)
	if err != nil {
		return frame.raiseString("invalid key to 'next'")
	}
	if !found {
		return frame.ReturnNil()
	}
	return frame.returnCompactValues(
		[2]slot{key, value},
		2,
		nil,
	)
}

func baseNewProxy(frame Frame) Outcome {
	argument, present := frame.argument(0)
	if !present {
		argument = nilSlot
	}
	frame.discardArgumentsAfter(1)

	validMetatables := tableObjectFromSlot(frame.nativeCapture(0))
	var metatable *tableObject
	switch argument.ref {
	case nilMarkerPointer, falseMarkerPointer:
	case trueMarkerPointer:
		metatable = newTable(frame.thread.state, 0, 0)
		if status := validMetatables.rawSetSlot(
			slotFromTableObject(metatable),
			trueSlot,
		); status != tableKeyValid {
			panic("lua: proxy metatable is not a valid table key")
		}
	default:
		metatable = metatableForSlot(frame.thread, argument)
		if metatable == nil {
			return baseArgumentError(
				frame,
				0,
				"boolean or proxy expected",
			)
		}
		valid, found := validMetatables.rawSlot(
			slotFromTableObject(metatable),
		)
		if !found || !truthySlot(valid) {
			return baseArgumentError(
				frame,
				0,
				"boolean or proxy expected",
			)
		}
	}

	proxy := newUserDataObject(
		frame.thread.state,
		nil,
		frame.activation().function.environment,
		nil,
	)
	proxy.metatable = metatable
	return frame.returnOne(
		frame.activation(),
		slotFromUserDataObject(proxy),
	)
}

func basePairs(frame Frame) Outcome {
	table, ok := frame.tableObject(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "table")
	}
	iterator := frame.nativeCapture(0)
	frame.discardArgumentsAfter(1)
	return frame.returnCompactValues(
		[2]slot{iterator, slotFromTableObject(table)},
		2,
		[]slot{nilSlot},
	)
}

func baseIPairs(frame Frame) Outcome {
	table, ok := frame.tableObject(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "table")
	}
	iterator := frame.nativeCapture(0)
	frame.discardArgumentsAfter(1)
	return frame.returnCompactValues(
		[2]slot{iterator, slotFromTableObject(table)},
		2,
		[]slot{numberSlot(0)},
	)
}

func baseIPairsIterator(frame Frame) Outcome {
	index, ok := frame.integerArgument(1)
	if !ok {
		return numberArgumentError(frame, 1)
	}
	table, ok := frame.tableObject(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "table")
	}
	// PUC increments a signed C int here, which is undefined at MaxInt32.
	// Lunar defines the common two's-complement wrap instead.
	if index == math.MaxInt32 {
		index = math.MinInt32
	} else {
		index++
	}
	value, _ := table.rawIntSlot(index)
	if value.isNil() {
		return frame.Return()
	}
	return frame.returnCompactValues(
		[2]slot{numberSlot(float64(index)), value},
		2,
		nil,
	)
}

func baseSelect(frame Frame) Outcome {
	count := frame.ArgumentCount()
	selector, present := frame.argument(0)
	if selector.isString() {
		text := stringSlotText(selector)
		if len(text) != 0 && text[0] == '#' {
			return frame.ReturnNumber(float64(count - 1))
		}
	}
	if !present {
		return numberArgumentError(frame, 0)
	}
	index, ok := frame.integerArgument(0)
	if !ok {
		return numberArgumentError(frame, 0)
	}
	if index < 0 {
		index = count + index
	} else if index > count {
		index = count
	}
	if index < 1 {
		return baseArgumentError(frame, 0, "index out of range")
	}
	base := int(frame.activation().base)
	return frame.returnCompactValues(
		[2]slot{},
		0,
		frame.thread.values[base+index:frame.thread.top],
	)
}

func baseUnpack(frame Frame) Outcome {
	table, ok := frame.tableObject(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "table")
	}
	first, outcome, failed := optionalLibraryInteger(frame, 1, 1)
	if failed {
		return outcome
	}
	last, outcome, failed := optionalLibraryInteger(
		frame,
		2,
		table.rawLen(),
	)
	if failed {
		return outcome
	}
	if first > last {
		return frame.Return()
	}

	count := int64(last) - int64(first) + 1
	call := frame.activation()
	resultBase := int(call.resultBase)
	limit := frame.thread.valueLimit()
	if count <= 0 ||
		resultBase < 0 ||
		resultBase > limit ||
		count > int64(limit-resultBase) {
		return libraryError(frame, "too many results to unpack")
	}

	writer, failure := frame.beginResults(int(count))
	if failure != nil {
		return frame.sealError(failure)
	}
	for offset := 0; offset < writer.outputCount; offset++ {
		value, _ := table.rawIntSlot(first + offset)
		writer.put(value)
	}
	writer.written = int(count)
	return frame.finishResults(&writer)
}

func baseToNumber(frame Frame) Outcome {
	base, outcome, failed := optionalLibraryInteger(frame, 1, 10)
	if failed {
		return outcome
	}
	value, present := frame.argument(0)
	if base == 10 {
		if !present {
			return baseArgumentError(frame, 0, "value expected")
		}
		if number, ok := slotToNumber(value); ok {
			return frame.ReturnNumber(number)
		}
		return frame.ReturnNil()
	}

	text, ok := frame.textArgument(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "string")
	}
	if base < 2 || base > 36 {
		return baseArgumentError(frame, 1, "base out of range")
	}
	number, ok := parseBaseNumber(text, base)
	if !ok {
		return frame.ReturnNil()
	}
	return frame.ReturnNumber(number)
}

func baseToString(frame Frame) Outcome {
	value, present := frame.argument(0)
	if !present {
		return baseArgumentError(frame, 0, "value expected")
	}
	if method, found := metamethodSlot(
		frame.thread,
		value,
		metaToString,
	); found {
		arguments := [1]slot{value}
		result, failure := frame.callCompactOne(method, arguments[:])
		if failure != nil {
			return frame.sealError(failure)
		}
		return frame.returnOne(frame.activation(), result)
	}

	switch value.kind() {
	case StringKind:
		return frame.returnOne(frame.activation(), value)
	case NumberKind:
		var scratch [32]byte
		return frame.returnStringBytes(appendLuaNumber(
			scratch[:0],
			math.Float64frombits(value.bits),
		))
	case BoolKind:
		if value.ref == trueMarkerPointer {
			return frame.ReturnString("true")
		}
		return frame.ReturnString("false")
	case NilKind:
		return frame.ReturnString("nil")
	default:
		return frame.ReturnString(fmt.Sprintf(
			"%s: %p",
			value.kind(),
			value.ref,
		))
	}
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

// parseBaseNumber implements the strtoul conversion used by Lua 5.1's
// non-decimal tonumber form. It uses a deterministic 64-bit unsigned range;
// a leading minus sign wraps modulo 2**64 unless the magnitude overflowed,
// in which case the result remains saturated.
func parseBaseNumber(text string, base int) (float64, bool) {
	for index := 0; index < len(text); index++ {
		if text[index] == 0 {
			text = text[:index]
			break
		}
	}

	index := 0
	for index < len(text) && isLuaNumberSpace(text[index]) {
		index++
	}
	negative := false
	if index < len(text) {
		switch text[index] {
		case '-':
			negative = true
			index++
		case '+':
			index++
		}
	}
	if base == 16 &&
		index+2 < len(text) &&
		text[index] == '0' &&
		(text[index+1] == 'x' || text[index+1] == 'X') {
		if digit, ok := baseDigitValue(text[index+2]); ok &&
			digit < uint64(base) {
			index += 2
		}
	}

	const maximum = uint64(math.MaxUint64)
	var (
		number   uint64
		digits   int
		overflow bool
	)
	for index < len(text) {
		digit, ok := baseDigitValue(text[index])
		if !ok || digit >= uint64(base) {
			break
		}
		digits++
		if !overflow {
			if number > (maximum-digit)/uint64(base) {
				number = maximum
				overflow = true
			} else {
				number = number*uint64(base) + digit
			}
		}
		index++
	}
	if digits == 0 {
		return 0, false
	}
	for index < len(text) && isLuaNumberSpace(text[index]) {
		index++
	}
	if index != len(text) {
		return 0, false
	}
	if negative && !overflow {
		number = 0 - number
	}
	return float64(number), true
}

func baseDigitValue(value byte) (uint64, bool) {
	switch {
	case value >= '0' && value <= '9':
		return uint64(value - '0'), true
	case value >= 'a' && value <= 'z':
		return uint64(value-'a') + 10, true
	case value >= 'A' && value <= 'Z':
		return uint64(value-'A') + 10, true
	default:
		return 0, false
	}
}
