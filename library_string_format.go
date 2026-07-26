package lua

import (
	"math"
	"strconv"
	"strings"
)

// This formatter follows Lua 5.1's lstrlib.c and C printf behavior. See
// THIRD_PARTY_NOTICES.md for the reference implementation's license.

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
	var scratch [640]byte

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

		rendered, direct, outcome, done := frame.formatOne(
			scratch[:0],
			item,
			verb,
			argument,
		)
		if done {
			return outcome
		}
		if direct {
			text, _ := frame.textArgument(argument)
			built = append(built, text...)
			continue
		}
		if end := bytesIndexZero(rendered); end >= 0 {
			rendered = rendered[:end]
		}
		built = append(built, rendered...)
	}
	return frame.ReturnString(stringFromOwnedBytes(built))
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
			return nil, true, Outcome{}, false
		}
		if end := strings.IndexByte(text, 0); end >= 0 {
			text = text[:end]
		}
		if item.hasPrecision {
			if precision := item.precisionValue(6); precision < len(text) {
				text = text[:precision]
			}
		}
		return item.pad(built, []byte(text), false), false, Outcome{}, false

	default:
		if verb == 0 {
			return built, false, libraryError(
				frame,
				"invalid option '%%' to 'format'",
			), true
		}
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
	digits = item.zeroDigits(digits, value == 0)
	prefix := ""
	if item.has('#') {
		switch verb {
		case 'o':
			if len(digits) == 0 || digits[0] != '0' {
				digits = append(digits, 0)
				copy(digits[1:], digits[:len(digits)-1])
				digits[0] = '0'
			}
		case 'x', 'X':
			if value != 0 {
				prefix = "0x"
				if verb == 'X' {
					prefix = "0X"
				}
			}
		}
	}
	return item.finishNumber(built, "", prefix, digits)
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
	if leading {
		// Zero still contributes one significant digit. strconv emits it as
		// "0", while C's alternate %g form keeps precision-1 trailing zeros.
		significant = 1
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
