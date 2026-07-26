package lua

import (
	"math"
	"strconv"
	"strings"
)

var stringLibraryFunctions = [...]struct {
	name  string
	entry NativeFunc
}{
	{name: "byte", entry: stringByte},
	{name: "char", entry: stringChar},
	{name: "dump", entry: stringDump},
	{name: "find", entry: stringFind},
	{name: "format", entry: stringFormat},
	{name: "gmatch", entry: stringGMatch},
	{name: "gsub", entry: stringGSub},
	{name: "len", entry: stringLen},
	{name: "lower", entry: stringLower},
	{name: "match", entry: stringMatch},
	{name: "rep", entry: stringRep},
	{name: "reverse", entry: stringReverse},
	{name: "sub", entry: stringSub},
	{name: "upper", entry: stringUpper},
}

// OpenString installs the Lua 5.1 string library and the shared string
// metatable.
//
// Opening is explicit and idempotent in effect. Each call replaces the global
// string table, its functions, and the metatable every Lua string indexes
// through, so ("x"):upper() resolves to the freshly installed library.
//
// Positions follow Lua 5.1 exactly: they are one-based, a negative position
// counts back from the end, and out-of-range positions clamp rather than fail.
// Every operation is byte-oriented; nothing here interprets UTF-8, and the
// character classes are C's in the "C" locale.
func (state *State) OpenString() error {
	if err := state.checkOpen(); err != nil {
		return err
	}
	const aliasCount = 1 // gfind
	library, err := state.NewTable(
		0,
		len(stringLibraryFunctions)+aliasCount,
	)
	if err != nil {
		return err
	}
	for _, definition := range stringLibraryFunctions {
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
	// The standard Lua 5.1 distribution defines LUA_COMPAT_GFIND, which
	// publishes string.gmatch a second time under its former name. It aliases
	// the same canonical Function rather than registering a second one.
	if err := library.RawSetString(
		"gfind",
		library.RawGetString("gmatch"),
	); err != nil {
		return err
	}

	metatable, err := state.NewTable(0, 1)
	if err != nil {
		return err
	}
	if err := metatable.RawSetString("__index", library.Value()); err != nil {
		return err
	}
	if err := state.SetMetatable(state.String(""), metatable); err != nil {
		return err
	}
	return state.globals.RawSetString("string", library.Value())
}

func stringLen(frame Frame) Outcome {
	text, ok := frame.textArgument(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "string")
	}
	return frame.ReturnNumber(float64(len(text)))
}

func stringSub(frame Frame) Outcome {
	text, ok := frame.textArgument(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "string")
	}
	first, ok := frame.positionArgument(1)
	if !ok {
		return numberArgumentError(frame, 1)
	}
	last := int64(-1)
	if _, present := frame.argument(2); present {
		last, ok = frame.positionArgument(2)
		if !ok {
			return numberArgumentError(frame, 2)
		}
	}

	start := relativePosition(first, len(text))
	end := relativePosition(last, len(text))
	if start < 1 {
		start = 1
	}
	if end > int64(len(text)) {
		end = int64(len(text))
	}
	if start > end {
		return frame.ReturnString("")
	}
	return frame.ReturnString(text[start-1 : end])
}

func stringReverse(frame Frame) Outcome {
	text, ok := frame.textArgument(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "string")
	}
	reversed := make([]byte, len(text))
	for index := 0; index < len(text); index++ {
		reversed[len(text)-1-index] = text[index]
	}
	return frame.ReturnString(string(reversed))
}

func stringLower(frame Frame) Outcome {
	return frame.returnMappedBytes(0, lowerByte)
}

func stringUpper(frame Frame) Outcome {
	return frame.returnMappedBytes(0, upperByte)
}

// returnMappedBytes applies a byte mapping, returning the original string when
// nothing changed so an already-cased string costs no allocation.
func (frame Frame) returnMappedBytes(
	index int,
	convert func(byte) byte,
) Outcome {
	text, ok := frame.textArgument(index)
	if !ok {
		return baseArgumentTypeError(frame, index, "string")
	}
	changed := -1
	for offset := 0; offset < len(text); offset++ {
		if convert(text[offset]) != text[offset] {
			changed = offset
			break
		}
	}
	if changed < 0 {
		return frame.ReturnString(text)
	}
	mapped := make([]byte, len(text))
	copy(mapped, text[:changed])
	for offset := changed; offset < len(text); offset++ {
		mapped[offset] = convert(text[offset])
	}
	return frame.ReturnString(string(mapped))
}

// lowerByte and upperByte are C's tolower and toupper in the "C" locale, which
// leave every byte outside the ASCII letters alone.
func lowerByte(value byte) byte {
	if value >= 'A' && value <= 'Z' {
		return value + 'a' - 'A'
	}
	return value
}

func upperByte(value byte) byte {
	if value >= 'a' && value <= 'z' {
		return value - 'a' + 'A'
	}
	return value
}

func stringRep(frame Frame) Outcome {
	text, ok := frame.textArgument(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "string")
	}
	count, ok := frame.integerArgument(1)
	if !ok {
		return numberArgumentError(frame, 1)
	}
	if count <= 0 || len(text) == 0 {
		return frame.ReturnString("")
	}
	// Lua 5.1 relies on its allocator to reject an impossible result. This
	// runtime has no string-size budget to consult, so the one case that
	// cannot be attempted at all is reported rather than left to the host.
	if count > maxStringLength/len(text) {
		return libraryError(frame, "resulting string too large")
	}
	return frame.ReturnString(strings.Repeat(text, count))
}

// maxStringLength bounds a constructed string at 1 GiB. It is a deterministic
// stand-in for Lua 5.1's allocator failure, not a configured runtime limit.
const maxStringLength = 1 << 30

func stringByte(frame Frame) Outcome {
	text, ok := frame.textArgument(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "string")
	}
	first := int64(1)
	if _, present := frame.argument(1); present {
		first, ok = frame.positionArgument(1)
		if !ok {
			return numberArgumentError(frame, 1)
		}
	}
	first = relativePosition(first, len(text))
	last := first
	if _, present := frame.argument(2); present {
		supplied, valid := frame.positionArgument(2)
		if !valid {
			return numberArgumentError(frame, 2)
		}
		last = relativePosition(supplied, len(text))
	}

	if first < 1 {
		first = 1
	}
	if last > int64(len(text)) {
		last = int64(len(text))
	}
	if first > last {
		return frame.Return()
	}
	count := int(last - first + 1)

	writer, failure := frame.beginResults(count)
	if failure != nil {
		return frame.sealError(failure)
	}
	for offset := 0; offset < count; offset++ {
		writer.put(numberSlot(float64(text[int(first)-1+offset])))
	}
	return frame.finishResults(&writer)
}

func stringChar(frame Frame) Outcome {
	count := frame.ArgumentCount()
	if count == 0 {
		return frame.ReturnString("")
	}
	bytes := make([]byte, count)
	for index := 0; index < count; index++ {
		value, ok := frame.integerArgument(index)
		if !ok {
			return numberArgumentError(frame, index)
		}
		if value < 0 || value > 255 {
			return baseArgumentError(frame, index, "invalid value")
		}
		bytes[index] = byte(value)
	}
	return frame.ReturnString(string(bytes))
}

// stringDump reports Lua 5.1's own failure for a function it cannot serialize.
//
// This runtime has no bytecode writer, so no function can be dumped. PUC
// raises exactly this message for every C function, and the argument check
// still matches, so the surface and the failure remain Lua 5.1's.
func stringDump(frame Frame) Outcome {
	if _, ok := frame.Function(0); !ok {
		return baseArgumentTypeError(frame, 0, "function")
	}
	return libraryError(frame, "unable to dump given function")
}

func stringFind(frame Frame) Outcome {
	return stringFindAux(frame, true)
}

func stringMatch(frame Frame) Outcome {
	return stringFindAux(frame, false)
}

// stringFindAux is str_find_aux: string.find reports the span and then the
// explicit captures, while string.match reports the captures alone, or the
// whole match when the pattern has none.
func stringFindAux(frame Frame, find bool) Outcome {
	subject, ok := frame.textArgument(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "string")
	}
	pattern, ok := frame.textArgument(1)
	if !ok {
		return baseArgumentTypeError(frame, 1, "string")
	}
	init := int64(1)
	if _, present := frame.argument(2); present {
		init, ok = frame.positionArgument(2)
		if !ok {
			return numberArgumentError(frame, 2)
		}
	}
	offset := relativePosition(init, len(subject)) - 1
	if offset < 0 {
		offset = 0
	} else if offset > int64(len(subject)) {
		offset = int64(len(subject))
	}

	if find {
		plain := false
		if value, present := frame.argument(3); present {
			plain = truthySlot(value)
		}
		if plain || !patternHasSpecials(pattern) {
			// A plain scan uses the pattern's full byte length even though
			// the specials test above stopped at an embedded NUL, exactly as
			// PUC's strpbrk and memchr pair does.
			at := strings.Index(subject[offset:], pattern)
			if at < 0 {
				return frame.ReturnNil()
			}
			start := offset + int64(at)
			return frame.returnCompactValues(
				[2]slot{
					numberSlot(float64(start + 1)),
					numberSlot(float64(start + int64(len(pattern)))),
				},
				2,
				nil,
			)
		}
	}

	var state matchState
	start, end, found := state.find(subject, pattern, int(offset))
	if state.failed() {
		return libraryError(frame, "%s", state.failure)
	}
	if !found {
		return frame.ReturnNil()
	}
	return frame.returnMatch(&state, start, end, find)
}

// returnMatch publishes a match. find prepends the span and reports only the
// explicit captures; match reports the whole match when there are none.
func (frame Frame) returnMatch(
	state *matchState,
	start int,
	end int,
	find bool,
) Outcome {
	captureCount := state.captureCount(!find)
	supplied := captureCount
	if find {
		supplied += 2
	}
	writer, failure := frame.beginResults(supplied)
	if failure != nil {
		return frame.sealError(failure)
	}
	if find {
		writer.put(numberSlot(float64(start + 1)))
		writer.put(numberSlot(float64(end)))
	}
	for index := 0; index < captureCount; index++ {
		value, ok := state.captureValue(index, start, end)
		if !ok {
			return libraryError(frame, "%s", state.failure)
		}
		writer.put(frame.captureSlot(value))
	}
	return frame.finishResults(&writer)
}

func (frame Frame) captureSlot(value patternCaptureValue) slot {
	if value.isPosition {
		return numberSlot(float64(value.position))
	}
	return stringSlot(frame.thread.owner.strings.make(value.text))
}

// gmatch capture slots. The iterator keeps its subject, pattern, and resume
// offset in the native Function's private storage, which is Lua 5.1's C
// closure upvalues without a second mechanism.
const (
	gmatchSubject = 0
	gmatchPattern = 1
	gmatchOffset  = 2
)

func stringGMatch(frame Frame) Outcome {
	subject, ok := frame.textArgument(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "string")
	}
	pattern, ok := frame.textArgument(1)
	if !ok {
		return baseArgumentTypeError(frame, 1, "string")
	}
	state := frame.State()
	iterator, err := state.NewNativeFunction(
		gmatchStep,
		state.String(subject),
		state.String(pattern),
		Number(0),
	)
	if err != nil {
		return frame.RaiseString(err.Error())
	}
	return frame.ReturnValue(iterator.Value())
}

// gmatchStep resumes the iteration. '^' is an ordinary character here because
// Lua 5.1's gmatch never strips an anchor.
func gmatchStep(frame Frame) Outcome {
	captures := frame.activation().function.nativeBodyUnchecked().captures
	subject := (*luaString)(captures[gmatchSubject].ref).text
	pattern := (*luaString)(captures[gmatchPattern].ref).text
	offset := int(math.Float64frombits(captures[gmatchOffset].bits))
	if offset > len(subject) {
		return frame.Return()
	}

	var state matchState
	state.reset(subject, pattern)
	start, end, found := state.searchFrom(offset, false)
	if state.failed() {
		return libraryError(frame, "%s", state.failure)
	}
	if !found {
		return frame.Return()
	}

	next := end
	if end == start {
		// An empty match must still advance, or the iterator would never
		// terminate.
		next++
	}
	writeSlot(
		&captures[gmatchOffset],
		numberSlot(float64(next)),
	)
	return frame.returnMatch(&state, start, end, false)
}

// Replacement kinds accepted by gsub, in the order str_gsub tests them.
const (
	gsubText = iota
	gsubFunction
	gsubTable
)

func stringGSub(frame Frame) Outcome {
	subject, ok := frame.textArgument(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "string")
	}
	pattern, ok := frame.textArgument(1)
	if !ok {
		return baseArgumentTypeError(frame, 1, "string")
	}
	replacement, _ := frame.argument(2)
	replacementKind := gsubText
	var replacementText string
	switch replacement.kind() {
	case StringKind, NumberKind:
		replacementText, _ = frame.textArgument(2)
	case FunctionKind:
		replacementKind = gsubFunction
	case TableKind:
		replacementKind = gsubTable
	default:
		return baseArgumentError(
			frame,
			2,
			"string/function/table expected",
		)
	}
	limit := len(subject) + 1
	if _, present := frame.argument(3); present {
		limit, ok = frame.integerArgument(3)
		if !ok {
			return numberArgumentError(frame, 3)
		}
	}

	stripped, anchored := patternAnchor(pattern)
	var state matchState
	state.reset(subject, stripped)

	var built []byte
	position := 0
	count := 0
	for count < limit {
		state.restart()
		end := state.match(position, 0)
		if end == matchFailed {
			return libraryError(frame, "%s", state.failure)
		}
		if end >= 0 {
			count++
			appended, failure := frame.appendReplacement(
				built,
				&state,
				position,
				end,
				replacementKind,
				replacementText,
				replacement,
			)
			if failure != nil {
				return frame.sealError(failure)
			}
			built = appended
		}
		if end > position {
			// A non-empty match consumes its own text.
			position = end
		} else if position < len(subject) {
			// An empty match, or no match at all, copies one byte and moves
			// on, which is what makes an empty pattern terminate.
			built = append(built, subject[position])
			position++
		} else {
			// No progress is possible at the end of the subject.
			break
		}
		if anchored {
			break
		}
	}
	built = append(built, subject[position:]...)

	return frame.returnCompactValues(
		[2]slot{
			stringSlot(frame.thread.owner.strings.make(string(built))),
			numberSlot(float64(count)),
		},
		2,
		nil,
	)
}

// appendReplacement is add_value: it resolves one match's replacement and
// appends it. A nil or false result keeps the matched text, and any other
// non-text result is Lua 5.1's invalid-replacement failure.
func (frame Frame) appendReplacement(
	built []byte,
	state *matchState,
	start int,
	end int,
	kind int,
	text string,
	replacement slot,
) ([]byte, *Error) {
	switch kind {
	case gsubText:
		return frame.appendTemplate(built, state, start, end, text)
	case gsubFunction:
		result, failure := frame.callCaptures(
			replacement,
			state,
			start,
			end,
		)
		if failure != nil {
			return built, failure
		}
		return frame.appendResolved(built, state, start, end, result)
	default:
		key, ok := state.captureValue(0, start, end)
		if !ok {
			return built, libraryFailure(frame, "%s", state.failure)
		}
		result, failure := frame.indexValue(
			replacement,
			frame.captureSlot(key),
		)
		if failure != nil {
			return built, failure
		}
		return frame.appendResolved(built, state, start, end, result)
	}
}

func (frame Frame) appendResolved(
	built []byte,
	state *matchState,
	start int,
	end int,
	result slot,
) ([]byte, *Error) {
	if !truthySlot(result) {
		return append(built, state.source[start:end]...), nil
	}
	switch result.kind() {
	case StringKind:
		return append(built, (*luaString)(result.ref).text...), nil
	case NumberKind:
		return appendLuaNumber(
			built,
			math.Float64frombits(result.bits),
		), nil
	}
	return built, libraryFailure(
		frame,
		"invalid replacement value (a %s)",
		result.kind(),
	)
}

// appendTemplate is add_s. A '%' followed by a digit selects a capture, '%0'
// selects the whole match, and '%' followed by anything else emits that byte
// literally — including the zero byte PUC reads past the end of the template.
func (frame Frame) appendTemplate(
	built []byte,
	state *matchState,
	start int,
	end int,
	template string,
) ([]byte, *Error) {
	for index := 0; index < len(template); index++ {
		character := template[index]
		if character != patternEscape {
			built = append(built, character)
			continue
		}
		index++
		var next byte
		if index < len(template) {
			next = template[index]
		}
		switch {
		case !isPatternDigit(next):
			built = append(built, next)
		case next == '0':
			built = append(built, state.source[start:end]...)
		default:
			value, ok := state.captureValue(
				int(next)-'1',
				start,
				end,
			)
			if !ok {
				return built, libraryFailure(frame, "%s", state.failure)
			}
			if value.isPosition {
				built = appendLuaNumber(built, float64(value.position))
				continue
			}
			built = append(built, value.text...)
		}
	}
	return built, nil
}

// callCaptures invokes a gsub replacement function with the match's captures
// and keeps one compact result.
//
// Lua 5.1 makes this call with lua_call, so a failure is not caught here; it
// is returned and the library propagates it after restoring its own state. The
// nested traceback segment is captured before restoration, preserving the
// executor's one-segment-per-frame rule.
func (frame Frame) callCaptures(
	callable slot,
	state *matchState,
	start int,
	end int,
) (slot, *Error) {
	var arguments [maxPatternCaptures]slot
	count := state.captureCount(true)
	for index := 0; index < count; index++ {
		value, ok := state.captureValue(index, start, end)
		if !ok {
			return nilSlot, libraryFailure(frame, "%s", state.failure)
		}
		arguments[index] = frame.captureSlot(value)
	}

	thread := frame.thread
	checkpoint := captureExecutionCheckpoint(frame)
	restored := false
	defer func() {
		if !restored {
			checkpoint.restore(thread, true)
		}
	}()

	resultBase, failure := startNestedCall(
		thread,
		callable,
		callArguments{compact: arguments[:count]},
		1,
	)
	if failure == nil {
		result := driveExecution(thread, checkpoint.frameDepth)
		switch result.kind {
		case executionReturned:
			if len(thread.frames) != checkpoint.frameDepth ||
				len(thread.continuations) != checkpoint.continuationDepth {
				panic("lua: library call returned invalid execution state")
			}
			value := thread.values[resultBase]
			checkpoint.restore(thread, true)
			restored = true
			return value, nil
		case executionFailed:
			if result.err == nil {
				panic("lua: library call failed without an error")
			}
			failure = result.err
			snapshotExecutionFailure(
				thread,
				checkpoint.frameDepth,
				failure,
			)
		default:
			panic("lua: library call produced an invalid execution result")
		}
	}
	checkpoint.restore(thread, true)
	restored = true
	return nilSlot, failure
}

// indexValue is lua_gettable for a gsub table replacement: a raw hit wins, and
// a miss follows the same bounded __index chain the executor follows, calling
// a Function-valued handler and treating any other value as the next target.
func (frame Frame) indexValue(target, key slot) (slot, *Error) {
	for step := 0; step < maxTableMetamethodChain; step++ {
		var handler slot
		var found bool
		if target.kind() == TableKind {
			table := (*Table)(target.ref)
			if result, hit := table.rawSlot(key); hit &&
				result.kind() != NilKind {
				return result, nil
			}
			handler, found = metamethodSlot(frame.thread, target, metaIndex)
			if !found {
				return nilSlot, nil
			}
		} else {
			handler, found = metamethodSlot(frame.thread, target, metaIndex)
			if !found {
				return nilSlot, libraryFailure(
					frame,
					"attempt to index a %s value",
					target.kind(),
				)
			}
		}
		if _, callable := functionSlot(handler); callable {
			return frame.callBinary(handler, target, key)
		}
		target = handler
	}
	return nilSlot, libraryFailure(frame, "loop in gettable")
}

// relativePosition is posrelat: a negative position counts back from the end
// and anything still negative becomes zero.
func relativePosition(position int64, length int) int64 {
	if position < 0 {
		position += int64(length) + 1
	}
	if position < 0 {
		return 0
	}
	return position
}

// positionArgument reads a luaL_checkinteger position argument.
//
// PUC uses ptrdiff_t for string positions rather than int, so this saturates
// at the 64-bit bounds instead of the 32-bit bounds libraryInteger uses. Both
// truncate toward zero and both define the inputs C leaves undefined.
func (frame Frame) positionArgument(index int) (int64, bool) {
	number, ok := frame.numberArgument(index)
	if !ok {
		return 0, false
	}
	return saturatingInt64(number), true
}

// unsignedConversion is the conversion C performs for %o, %u, %x, and %X.
//
// C converts the double to an unsigned integer of the same width, which is
// undefined for a negative value and for a magnitude the type cannot hold; the
// two mainstream architectures disagree there, one saturating and the other
// wrapping. This truncates toward zero and then reduces modulo 2**64, which is
// C's rule wherever C defines one, is what Lua 5.3 and later produce now that
// they have real integers, and keeps %x and %d describing the same value.
// Magnitudes beyond the type saturate so every input has one defined result.
func unsignedConversion(number float64) uint64 {
	const (
		twoToThe63 = float64(1 << 63)
		twoToThe64 = twoToThe63 * 2
	)
	switch {
	case math.IsNaN(number):
		return 0
	case number >= twoToThe64:
		return math.MaxUint64
	case number >= twoToThe63:
		return uint64(math.Trunc(number))
	case number <= -twoToThe63:
		// The two's-complement pattern of the most negative int64.
		return 1 << 63
	default:
		return uint64(int64(math.Trunc(number)))
	}
}

func saturatingInt64(number float64) int64 {
	switch {
	case math.IsNaN(number):
		return 0
	case number >= math.MaxInt64:
		return math.MaxInt64
	case number <= math.MinInt64:
		return math.MinInt64
	default:
		return int64(math.Trunc(number))
	}
}

// textArgument reads an argument with luaL_checklstring's coercion: a string
// passes through by reference and a number is spelled with the runtime's own
// primitive.
func (frame Frame) textArgument(index int) (string, bool) {
	value, present := frame.argument(index)
	if !present {
		return "", false
	}
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

// resultWriter publishes a native result window one value at a time, so a
// variable number of scalar results never passes through a slice.
type resultWriter struct {
	thread      *Thread
	base        int
	outputCount int
	written     int
}

func (frame Frame) beginResults(supplied int) (resultWriter, *Error) {
	call := frame.activation()
	outputCount, failure := frame.prepareResults(call, supplied)
	if failure != nil {
		return resultWriter{}, failure
	}
	return resultWriter{
		thread:      frame.thread,
		base:        int(call.resultBase),
		outputCount: outputCount,
	}, nil
}

// put publishes the next result, discarding values the caller did not request.
func (writer *resultWriter) put(value slot) {
	if writer.written < writer.outputCount {
		writeSlot(
			&writer.thread.values[writer.base+writer.written],
			value,
		)
	}
	writer.written++
}

func (frame Frame) finishResults(writer *resultWriter) Outcome {
	written := writer.written
	if written > writer.outputCount {
		written = writer.outputCount
	}
	writer.thread.fillNil(
		writer.base+written,
		writer.base+writer.outputCount,
	)
	return frame.sealReturn(writer.outputCount)
}

// String formatting.
//
// Lua 5.1 hands each item to C's sprintf, so the accepted specification and
// the produced text are C's, not Go's. The scanner below is PUC's scanformat
// (at most five flags, a two-digit width, and a two-digit precision), and each
// conversion is rendered to match C rather than Go's defaults: %g without an
// explicit precision means six significant digits, a non-finite number is
// spelled inf or nan, %c writes one byte, %u has no Go verb, and %s stops at
// an embedded NUL exactly as sprintf does.

const (
	formatFlags = "-+ #0"
	// maxFormatFlags is sizeof(FLAGS) in PUC, which bounds the flag run at
	// one byte more than the flag set itself.
	maxFormatFlags = len(formatFlags) + 1
)

// formatItem is one scanned %-specification.
type formatItem struct {
	flags        string
	width        string
	precision    string
	hasPrecision bool
}

func stringFormat(frame Frame) Outcome {
	template, ok := frame.textArgument(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "string")
	}
	top := frame.ArgumentCount()
	argument := 0
	var built []byte

	for index := 0; index < len(template); {
		if template[index] != patternEscape {
			built = append(built, template[index])
			index++
			continue
		}
		index++
		// PUC reads the byte after '%' from a NUL-terminated string, so a
		// trailing '%' behaves as a specification with no conversion.
		var next byte
		if index < len(template) {
			next = template[index]
		}
		if next == patternEscape {
			built = append(built, patternEscape)
			index++
			continue
		}

		argument++
		if argument >= top {
			return baseArgumentError(frame, argument, "no value")
		}
		item, verb, scanned, failure := scanFormatItem(template, index)
		if failure != "" {
			return libraryError(frame, "%s", failure)
		}
		index = scanned

		var scratch [640]byte
		rendered, direct, outcome, done := frame.formatOne(
			scratch[:0],
			item,
			verb,
			argument,
		)
		if done {
			return outcome
		}
		if !direct {
			if end := bytesIndexZero(rendered); end >= 0 {
				rendered = rendered[:end]
			}
		}
		built = append(built, rendered...)
	}
	return frame.ReturnString(string(built))
}

// bytesIndexZero is strlen over an item PUC would have produced in its fixed
// buffer.
func bytesIndexZero(text []byte) int {
	for index, character := range text {
		if character == 0 {
			return index
		}
	}
	return -1
}

// scanFormatItem is scanformat. It returns the parsed item, its conversion
// byte, the offset just past it, and a failure message.
func scanFormatItem(
	template string,
	index int,
) (item formatItem, verb byte, next int, failure string) {
	at := func(offset int) byte {
		if offset >= len(template) {
			return 0
		}
		return template[offset]
	}

	start := index
	for strings.IndexByte(formatFlags, at(index)) >= 0 && at(index) != 0 {
		index++
	}
	if index-start >= maxFormatFlags {
		return item, 0, index, "invalid format (repeated flags)"
	}
	item.flags = template[start:index]

	widthStart := index
	if isPatternDigit(at(index)) {
		index++
	}
	if isPatternDigit(at(index)) {
		index++
	}
	item.width = template[widthStart:index]

	if at(index) == '.' {
		index++
		item.hasPrecision = true
		precisionStart := index
		if isPatternDigit(at(index)) {
			index++
		}
		if isPatternDigit(at(index)) {
			index++
		}
		item.precision = template[precisionStart:index]
	}
	if isPatternDigit(at(index)) {
		return item, 0, index, "invalid format (width or precision too long)"
	}
	return item, at(index), index + 1, ""
}

// formatOne renders one conversion into scratch.
//
// It reports direct when the item bypasses PUC's fixed sprintf buffer, and
// done when a terminal outcome has already been produced. Everything else goes
// through that buffer, whose content PUC measures with strlen, so the caller
// truncates the item at its first NUL.
func (frame Frame) formatOne(
	built []byte,
	item formatItem,
	verb byte,
	argument int,
) (rendered []byte, direct bool, outcome Outcome, done bool) {
	switch verb {
	case 'c':
		number, ok := frame.numberArgument(argument)
		if !ok {
			return built, false, numberArgumentError(frame, argument), true
		}
		// C converts through int and then to unsigned char, so only the
		// low byte of the same conversion luaL_checkint performs survives.
		return item.pad(
			built,
			[]byte{byte(libraryInteger(number))},
			false,
		), false, Outcome{}, false

	case 'd', 'i':
		number, ok := frame.numberArgument(argument)
		if !ok {
			return built, false, numberArgumentError(frame, argument), true
		}
		var scratch [24]byte
		value := saturatingInt64(number)
		return item.appendInteger(
			built,
			strconv.AppendInt(scratch[:0], value, 10),
			value == 0,
		), false, Outcome{}, false

	case 'o', 'u', 'x', 'X':
		number, ok := frame.numberArgument(argument)
		if !ok {
			return built, false, numberArgumentError(frame, argument), true
		}
		return item.appendUnsigned(
			built,
			unsignedConversion(number),
			verb,
		), false, Outcome{}, false

	case 'e', 'E', 'f', 'g', 'G':
		number, ok := frame.numberArgument(argument)
		if !ok {
			return built, false, numberArgumentError(frame, argument), true
		}
		return item.appendFloat(built, number, verb), false, Outcome{}, false

	case 'q':
		text, ok := frame.textArgument(argument)
		if !ok {
			return built, false, baseArgumentTypeError(
				frame,
				argument,
				"string",
			), true
		}
		return appendQuoted(built, text), false, Outcome{}, false

	case 's':
		text, ok := frame.textArgument(argument)
		if !ok {
			return built, false, baseArgumentTypeError(
				frame,
				argument,
				"string",
			), true
		}
		if !item.hasPrecision && len(text) >= 100 {
			// PUC keeps a long unformatted string as-is rather than passing
			// it through its fixed sprintf buffer. Because a width is limited
			// to two digits, no padding could apply anyway; the visible effect
			// is that embedded NULs survive.
			return append(built, text...), true, Outcome{}, false
		}
		if item.hasPrecision {
			if precision := item.precisionValue(6); precision < len(text) {
				text = text[:precision]
			}
		}
		return item.pad(built, []byte(text), false), false, Outcome{}, false

	default:
		return built, false, libraryError(
			frame,
			"invalid option '%%%c' to 'format'",
			verb,
		), true
	}
}

func (item formatItem) widthValue() int {
	if item.width == "" {
		return 0
	}
	value, _ := strconv.Atoi(item.width)
	return value
}

func (item formatItem) precisionValue(fallback int) int {
	if !item.hasPrecision {
		return fallback
	}
	if item.precision == "" {
		return 0
	}
	value, _ := strconv.Atoi(item.precision)
	return value
}

func (item formatItem) has(flag byte) bool {
	return strings.IndexByte(item.flags, flag) >= 0
}

// pad applies the width and the '-' and '0' flags. Zero padding follows C: it
// is ignored when the value is left-aligned, and it goes after any sign.
func (item formatItem) pad(
	built []byte,
	text []byte,
	numeric bool,
) []byte {
	width := item.widthValue()
	if len(text) >= width {
		return append(built, text...)
	}
	fill := width - len(text)
	if item.has('-') {
		built = append(built, text...)
		return appendRepeatedByte(built, ' ', fill)
	}
	if item.has('0') {
		prefix := 0
		if numeric {
			if len(text) != 0 &&
				(text[0] == '-' || text[0] == '+' || text[0] == ' ') {
				prefix = 1
			}
			if prefix == 0 && len(text) > 1 && text[0] == '0' &&
				(text[1] == 'x' || text[1] == 'X') {
				prefix = 2
			}
		}
		built = append(built, text[:prefix]...)
		built = appendRepeatedByte(built, '0', fill)
		return append(built, text[prefix:]...)
	}
	built = appendRepeatedByte(built, ' ', fill)
	return append(built, text...)
}

func appendRepeatedByte(built []byte, value byte, count int) []byte {
	for index := 0; index < count; index++ {
		built = append(built, value)
	}
	return built
}

// appendInteger applies C's integer conversion rules: a precision sets a
// minimum digit count and suppresses zero padding, and the sign flags apply to
// a non-negative value.
func (item formatItem) appendInteger(
	built []byte,
	digits []byte,
	zero bool,
) []byte {
	sign := ""
	if digits[0] == '-' {
		sign, digits = "-", digits[1:]
	} else if item.has('+') {
		sign = "+"
	} else if item.has(' ') {
		sign = " "
	}
	return item.finishNumber(built, sign, "", item.zeroDigits(digits, zero))
}

// zeroDigits applies C's rule that converting a zero value with an explicit
// precision of zero produces no digits at all.
func (item formatItem) zeroDigits(digits []byte, zero bool) []byte {
	if zero && item.hasPrecision && item.precisionValue(0) == 0 {
		return digits[:0]
	}
	return digits
}

func (item formatItem) appendUnsigned(
	built []byte,
	value uint64,
	verb byte,
) []byte {
	base := 10
	switch verb {
	case 'o':
		base = 8
	case 'x', 'X':
		base = 16
	}
	var scratch [24]byte
	digits := strconv.AppendUint(scratch[:0], value, base)
	if verb == 'X' {
		for index := range digits {
			digits[index] = upperByte(digits[index])
		}
	}
	prefix := ""
	if item.has('#') && value != 0 {
		switch verb {
		case 'o':
			if len(digits) == 0 || digits[0] != '0' {
				digits = append([]byte{'0'}, digits...)
			}
		case 'x':
			prefix = "0x"
		case 'X':
			prefix = "0X"
		}
	}
	return item.finishNumber(built, "", prefix, item.zeroDigits(digits, value == 0))
}

// finishNumber assembles an integer conversion: sign, base prefix, the zeros a
// precision requires, then width. C ignores the '0' flag once a precision is
// given for an integer conversion.
func (item formatItem) finishNumber(
	built []byte,
	sign string,
	prefix string,
	digits []byte,
) []byte {
	var body [64]byte
	assembled := append(body[:0], sign...)
	assembled = append(assembled, prefix...)
	if item.hasPrecision {
		if zeros := item.precisionValue(0) - len(digits); zeros > 0 {
			assembled = appendRepeatedByte(assembled, '0', zeros)
		}
		assembled = append(assembled, digits...)
		return item.withoutZeroFlag().pad(built, assembled, true)
	}
	assembled = append(assembled, digits...)
	return item.pad(built, assembled, true)
}

// withoutZeroFlag drops '0' where C ignores it.
func (item formatItem) withoutZeroFlag() formatItem {
	item.flags = strings.ReplaceAll(item.flags, "0", "")
	return item
}

// appendFloat renders a floating conversion the way C does, including the
// spelling of infinities and NaN and %g's default precision of six.
func (item formatItem) appendFloat(
	built []byte,
	number float64,
	verb byte,
) []byte {
	sign := ""
	switch {
	case math.Signbit(number):
		sign = "-"
	case item.has('+'):
		sign = "+"
	case item.has(' '):
		sign = " "
	}

	if math.IsNaN(number) || math.IsInf(number, 0) {
		word := "inf"
		if math.IsNaN(number) {
			word = "nan"
		}
		if verb == 'E' || verb == 'G' {
			word = strings.ToUpper(word)
		}
		var body [8]byte
		spelled := append(append(body[:0], sign...), word...)
		// C never zero-pads a non-finite value.
		return item.withoutZeroFlag().pad(built, spelled, true)
	}

	precision := item.precisionValue(6)
	format := byte('f')
	switch verb {
	case 'e', 'E':
		format = verb
	case 'g', 'G':
		format = verb
		if precision == 0 {
			// C reads a zero precision as one significant digit.
			precision = 1
		}
	}
	var scratch [512]byte
	digits := strconv.AppendFloat(
		scratch[:0],
		math.Abs(number),
		format,
		precision,
		64,
	)
	var body [576]byte
	assembled := append(body[:0], sign...)
	if item.has('#') {
		assembled = appendAlternateFloat(assembled, digits, verb, precision)
	} else {
		assembled = append(assembled, digits...)
	}
	return item.pad(built, assembled, true)
}

// appendAlternateFloat applies C's '#' flag: the result always keeps its
// decimal point, and %g keeps the trailing zeros it would otherwise drop.
//
// digits must not alias dst; the point and the padding are inserted between
// the mantissa and any exponent.
func appendAlternateFloat(
	dst []byte,
	digits []byte,
	verb byte,
	precision int,
) []byte {
	exponent := len(digits)
	for index, character := range digits {
		if character == 'e' || character == 'E' {
			exponent = index
			break
		}
	}
	mantissa, suffix := digits[:exponent], digits[exponent:]

	point := false
	significant := 0
	leading := true
	for _, character := range mantissa {
		if character == '.' {
			point = true
			continue
		}
		if character == '0' && leading {
			continue
		}
		leading = false
		significant++
	}

	dst = append(dst, mantissa...)
	if !point {
		dst = append(dst, '.')
	}
	if verb == 'g' || verb == 'G' {
		for ; significant < precision; significant++ {
			dst = append(dst, '0')
		}
	}
	return append(dst, suffix...)
}

// appendQuoted is addquoted: a Lua string literal that reads back as the same
// bytes.
func appendQuoted(built []byte, text string) []byte {
	built = append(built, '"')
	for index := 0; index < len(text); index++ {
		switch character := text[index]; character {
		case '"', '\\', '\n':
			built = append(built, '\\', character)
		case '\r':
			built = append(built, '\\', 'r')
		case 0:
			built = append(built, '\\', '0', '0', '0')
		default:
			built = append(built, character)
		}
	}
	return append(built, '"')
}
