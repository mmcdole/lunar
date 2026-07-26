package lua

import (
	"math"
	"strings"
)

// Several algorithms in this file follow Lua 5.1's lstrlib.c. See
// THIRD_PARTY_NOTICES.md for the reference implementation's license.

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
	if start == 1 && end == int64(len(text)) {
		return frame.ReturnString(text)
	}
	return frame.returnBorrowedString(text[start-1 : end])
}

func (frame Frame) returnBorrowedString(text string) Outcome {
	call := frame.activation()
	return frame.returnOne(
		call,
		stringSlot(frame.thread.owner.strings.makeBorrowed(text)),
	)
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
	return frame.ReturnString(stringFromOwnedBytes(reversed))
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
	return frame.ReturnString(stringFromOwnedBytes(mapped))
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
	if count > maxStringRepLength/len(text) {
		return libraryError(frame, "resulting string too large")
	}
	return frame.ReturnString(strings.Repeat(text, count))
}

// maxStringRepLength keeps string.rep from submitting an obviously hostile
// allocation to the Go runtime. It is a local safety bound, not a general
// State string budget; a cross-runtime output budget remains a separate
// design decision.
const maxStringRepLength = 1 << 30

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
	return frame.ReturnString(stringFromOwnedBytes(bytes))
}

func stringDump(frame Frame) Outcome {
	function, ok := frame.Function(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "function")
	}
	frame.discardArgumentsAfter(1)
	if function.prototype == nil {
		return libraryError(frame, "unable to dump given function")
	}
	dumped, err := dumpPrototype(function.prototype)
	if err != nil {
		return libraryError(frame, "%s", err)
	}
	return frame.ReturnString(dumped)
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

	stripped, anchored := patternAnchor(pattern)
	var state matchState
	state.reset(subject, stripped)
	state.bindContext(frame.thread)
	start, end, found := state.searchFrom(int(offset), anchored)
	if state.failed() {
		return frame.patternFailure(&state)
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
	return stringSlot(frame.thread.owner.strings.makeBorrowed(value.text))
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
	// PUC closes over exactly the subject and pattern. Release any supplied
	// tail before constructing the iterator so unrelated values are not kept
	// live through the native allocation.
	frame.discardArgumentsAfter(2)
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
	state.bindContext(frame.thread)
	start, end, found := state.searchFrom(offset, false)
	if state.failed() {
		return frame.patternFailure(&state)
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
	subjectValue, _ := frame.argument(0)
	subject, ok := frame.textArgument(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "string")
	}
	pattern, ok := frame.textArgument(1)
	if !ok {
		return baseArgumentTypeError(frame, 1, "string")
	}
	replacement, _ := frame.argument(2)
	limit := len(subject) + 1
	if _, present := frame.argument(3); present {
		limit, ok = frame.integerArgument(3)
		if !ok {
			return numberArgumentError(frame, 3)
		}
	}

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

	stripped, anchored := patternAnchor(pattern)
	var state matchState
	state.reset(subject, stripped)
	state.bindContext(frame.thread)

	var built strings.Builder
	position := 0
	copiedThrough := 0
	count := 0
	for count < limit {
		state.restart()
		end := state.match(position, 0)
		if end == matchFailed {
			return frame.patternFailure(&state)
		}
		if end >= 0 {
			if count == 0 {
				built.Grow(len(subject))
			}
			built.WriteString(subject[copiedThrough:position])
			count++
			failure := frame.appendReplacement(
				&built,
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
		}
		if end > position {
			// A non-empty match consumes its own text.
			position = end
			copiedThrough = position
		} else if position < len(subject) {
			// An empty match must emit the byte after its replacement. A miss
			// merely advances; that untouched run is copied if a later match
			// needs a builder.
			if end >= 0 {
				built.WriteByte(subject[position])
				copiedThrough = position + 1
			}
			position++
		} else {
			// No progress is possible at the end of the subject.
			break
		}
		if anchored {
			break
		}
	}

	result := subjectValue
	if count != 0 {
		built.WriteString(subject[copiedThrough:])
		result = stringSlot(
			frame.thread.owner.strings.make(built.String()),
		)
	} else if result.kind() != StringKind {
		result = stringSlot(frame.thread.owner.strings.make(subject))
	}

	return frame.returnCompactValues(
		[2]slot{
			result,
			numberSlot(float64(count)),
		},
		2,
		nil,
	)
}

func (frame Frame) patternFailure(state *matchState) Outcome {
	if state.contextFailure != nil {
		return frame.sealError(state.contextFailure)
	}
	return libraryError(frame, "%s", state.failure)
}

// appendReplacement is add_value: it resolves one match's replacement and
// appends it. A nil or false result keeps the matched text, and any other
// non-text result is Lua 5.1's invalid-replacement failure.
func (frame Frame) appendReplacement(
	built *strings.Builder,
	state *matchState,
	start int,
	end int,
	kind int,
	text string,
	replacement slot,
) *Error {
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
			return failure
		}
		return frame.appendResolved(built, state, start, end, result)
	default:
		key, ok := state.captureValue(0, start, end)
		if !ok {
			return libraryFailure(frame, "%s", state.failure)
		}
		result, failure := frame.indexCompact(
			replacement,
			frame.captureSlot(key),
		)
		if failure != nil {
			return failure
		}
		return frame.appendResolved(built, state, start, end, result)
	}
}

func (frame Frame) appendResolved(
	built *strings.Builder,
	state *matchState,
	start int,
	end int,
	result slot,
) *Error {
	if !truthySlot(result) {
		built.WriteString(state.source[start:end])
		return nil
	}
	switch result.kind() {
	case StringKind:
		built.WriteString((*luaString)(result.ref).text)
		return nil
	case NumberKind:
		var scratch [32]byte
		built.Write(appendLuaNumber(
			scratch[:0],
			math.Float64frombits(result.bits),
		))
		return nil
	}
	return libraryFailure(
		frame,
		"invalid replacement value (a %s)",
		result.kind(),
	)
}

// appendTemplate is add_s. A '%' followed by a digit selects a capture, '%0'
// selects the whole match, and '%' followed by anything else emits that byte
// literally — including the zero byte PUC reads past the end of the template.
func (frame Frame) appendTemplate(
	built *strings.Builder,
	state *matchState,
	start int,
	end int,
	template string,
) *Error {
	for index := 0; index < len(template); index++ {
		character := template[index]
		if character != patternEscape {
			built.WriteByte(character)
			continue
		}
		index++
		var next byte
		if index < len(template) {
			next = template[index]
		}
		switch {
		case !isPatternDigit(next):
			built.WriteByte(next)
		case next == '0':
			built.WriteString(state.source[start:end])
		default:
			value, ok := state.captureValue(
				int(next)-'1',
				start,
				end,
			)
			if !ok {
				return libraryFailure(frame, "%s", state.failure)
			}
			if value.isPosition {
				var scratch [32]byte
				built.Write(appendLuaNumber(
					scratch[:0],
					float64(value.position),
				))
				continue
			}
			built.WriteString(value.text)
		}
	}
	return nil
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
	return frame.callCompactOne(callable, arguments[:count])
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
