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
	{name: "dofile", entry: baseDoFile},
	{name: "error", entry: baseError},
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

// OpenBase installs the implemented Lua 5.1 base-library globals.
//
// The garbage-collection and newproxy entries are still under construction.
// Opening is explicit: New returns an empty State. Calling OpenBase again
// replaces every installed function and the coroutine table with fresh
// canonical objects and restores _G and _VERSION.
func (state *State) OpenBase() error {
	if err := state.checkOpen(); err != nil {
		return err
	}
	loaded, err := state.ensureLoadedModules()
	if err != nil {
		return err
	}
	globals := state.globalEnvironment()
	functions := make([]*Function, len(baseLibraryFunctions))
	for index, definition := range baseLibraryFunctions {
		function, functionErr := state.NewNativeFunction(definition.entry)
		if functionErr != nil {
			return functionErr
		}
		functions[index] = function
	}
	ipairsIterator, err := state.NewNativeFunction(baseIPairsIterator)
	if err != nil {
		return err
	}
	pairsIterator, err := state.NewNativeFunction(baseNext)
	if err != nil {
		return err
	}
	pairs, err := state.NewNativeFunction(
		basePairs,
		pairsIterator.Value(),
	)
	if err != nil {
		return err
	}
	ipairs, err := state.NewNativeFunction(
		baseIPairs,
		ipairsIterator.Value(),
	)
	if err != nil {
		return err
	}

	if err := globals.RawSetString("_G", globals.Value()); err != nil {
		return err
	}
	if err := globals.RawSetString(
		"_VERSION",
		state.String("Lua 5.1"),
	); err != nil {
		return err
	}
	for index, definition := range baseLibraryFunctions {
		if err := globals.RawSetString(
			definition.name,
			functions[index].Value(),
		); err != nil {
			return err
		}
	}
	if err := globals.RawSetString("pairs", pairs.Value()); err != nil {
		return err
	}
	if err := globals.RawSetString("ipairs", ipairs.Value()); err != nil {
		return err
	}
	state.setLoadedModule(loaded, "_G", slotFromTable(globals))
	return state.OpenCoroutine()
}

func baseAssert(frame Frame) Outcome {
	value, present := frame.argument(0)
	if !present {
		return baseArgumentError(frame, 0, "value expected")
	}
	if truthySlot(value) {
		return frame.returnArguments()
	}

	message := "assertion failed!"
	if supplied, present := frame.argument(1); present &&
		supplied.kind() != NilKind {
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

func baseError(frame Frame) Outcome {
	value, present := frame.argument(0)
	if !present {
		value = nilSlot
	}
	level := 1
	if supplied, present := frame.argument(1); present &&
		supplied.kind() != NilKind {
		var ok bool
		level, ok = frame.integerArgument(1)
		if !ok {
			return numberArgumentError(frame, 1)
		}
	}
	frame.discardArgumentsAfter(1)

	if level > 0 &&
		(value.kind() == StringKind || value.kind() == NumberKind) {
		text, _ := compactText(value)
		if prototype, pc, found := luaCallerAtLevel(frame, level); found {
			text = executionErrorDescription(prototype, pc, text)
		}
		return frame.RaiseString(text)
	}
	return frame.Raise(value.owningValue())
}

func basePrint(frame Frame) Outcome {
	toString, failure := frame.indexCompact(
		slotFromTable(frame.thread.globals),
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
	function       *Function
	number         float64
	numberArgument bool
}

func resolveBaseEnvironmentTarget(
	frame Frame,
	optional bool,
) (baseEnvironmentTarget, Outcome, bool) {
	value, present := frame.argument(0)
	if present && value.kind() == FunctionKind {
		return baseEnvironmentTarget{
			function: (*Function)(value.ref),
		}, Outcome{}, false
	}

	number := float64(1)
	numberArgument := false
	if (present && value.kind() != NilKind) || !optional {
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
		slotFromTable(environment),
	)
}

func baseSetEnvironment(frame Frame) Outcome {
	environment, ok := frame.Table(1)
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
		slotFromFunction(target.function),
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
		slotFromTable(metatable),
	)
}

func baseSetMetatable(frame Frame) Outcome {
	table, ok := frame.Table(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "table")
	}
	value, present := frame.argument(1)
	if !present ||
		value.kind() != NilKind && value.kind() != TableKind {
		return baseArgumentError(frame, 1, "nil or table expected")
	}
	if _, protected := metamethodSlot(
		frame.thread,
		slotFromTable(table),
		metaMetatable,
	); protected {
		return libraryError(frame, "cannot change a protected metatable")
	}

	var metatable *Table
	if value.kind() == TableKind {
		metatable = (*Table)(value.ref)
	}
	frame.discardArgumentsAfter(2)
	table.metatable = metatable
	return frame.returnOne(frame.activation(), slotFromTable(table))
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
	table, ok := frame.Table(0)
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
	table, ok := frame.Table(0)
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
	return frame.returnOne(frame.activation(), slotFromTable(table))
}

func baseType(frame Frame) Outcome {
	value, present := frame.argument(0)
	if !present {
		return baseArgumentError(frame, 0, "value expected")
	}
	return frame.ReturnString(value.kind().String())
}

func baseNext(frame Frame) Outcome {
	table, ok := frame.Table(0)
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

func basePairs(frame Frame) Outcome {
	table, ok := frame.Table(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "table")
	}
	iterator := frame.nativeCapture(0)
	frame.discardArgumentsAfter(1)
	return frame.returnCompactValues(
		[2]slot{iterator, slotFromTable(table)},
		2,
		[]slot{nilSlot},
	)
}

func baseIPairs(frame Frame) Outcome {
	table, ok := frame.Table(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "table")
	}
	iterator := frame.nativeCapture(0)
	frame.discardArgumentsAfter(1)
	return frame.returnCompactValues(
		[2]slot{iterator, slotFromTable(table)},
		2,
		[]slot{numberSlot(0)},
	)
}

func baseIPairsIterator(frame Frame) Outcome {
	index, ok := frame.integerArgument(1)
	if !ok {
		return numberArgumentError(frame, 1)
	}
	table, ok := frame.Table(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "table")
	}
	// PUC increments a signed C int here, which is undefined at MaxInt32.
	// Badger defines the common two's-complement wrap instead.
	if index == math.MaxInt32 {
		index = math.MinInt32
	} else {
		index++
	}
	value, _ := table.rawIntSlot(index)
	if value.kind() == NilKind {
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
	if selector.kind() == StringKind {
		text := (*luaString)(selector.ref).text
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
	table, ok := frame.Table(0)
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
		table.RawLen(),
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

func optionalLibraryInteger(
	frame Frame,
	index int,
	fallback int,
) (int, Outcome, bool) {
	value, present := frame.argument(index)
	if !present || value.kind() == NilKind {
		return fallback, Outcome{}, false
	}
	number, ok := slotToNumber(value)
	if !ok {
		return 0, numberArgumentError(frame, index), true
	}
	return libraryInteger(number), Outcome{}, false
}

// compactText applies lua_tolstring's string-or-number conversion without
// replacing a compact stack slot with an owning string.
func compactText(value slot) (string, bool) {
	switch value.kind() {
	case StringKind:
		return (*luaString)(value.ref).text, true
	case NumberKind:
		var buffer [32]byte
		return string(appendLuaNumber(
			buffer[:0],
			math.Float64frombits(value.bits),
		)), true
	default:
		return "", false
	}
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
	return luaCallerAtLevel(frame, 1)
}

// luaCallerAtLevel resolves lua_getstack's logical activation levels,
// including native activations and tail calls whose physical frames were
// replaced. A native or elided-tail target has no Lua source position.
func luaCallerAtLevel(
	frame Frame,
	level int,
) (*Prototype, int, bool) {
	frame.activation()
	if level <= 0 {
		return nil, 0, false
	}
	caller, status := frame.thread.logicalFrame(level)
	if status != logicalFramePhysical ||
		caller.function == nil ||
		caller.function.prototype == nil {
		return nil, 0, false
	}
	prototype := caller.function.prototype
	pc := int(caller.pc) - 1
	if pc < 0 || pc >= len(prototype.code) {
		return nil, 0, false
	}
	return prototype, pc, true
}
