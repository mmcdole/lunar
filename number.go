package lua

import (
	"math"
	"strconv"
)

// parseLuaNumber implements the numeric grammar shared by source literals and
// runtime string coercion. It is deliberately deterministic: unlike PUC Lua
// 5.1's libc conversion, it does not accept locale-dependent spellings,
// hexadecimal floats, NaN/Inf names, embedded NUL, or trailing data.
func parseLuaNumber(text string) (float64, bool) {
	text = trimLuaNumberSpace(text)
	if text == "" {
		return 0, false
	}

	offset := 0
	if text[0] == '+' || text[0] == '-' {
		offset = 1
		if offset == len(text) {
			return 0, false
		}
	}

	if offset+2 <= len(text) &&
		text[offset] == '0' &&
		(text[offset+1] == 'x' || text[offset+1] == 'X') {
		return parseLuaHexInteger(text, offset)
	}

	if !validLuaDecimal(text, offset) {
		return 0, false
	}
	number, err := strconv.ParseFloat(text, 64)
	if err == nil {
		return number, true
	}
	numberError, ok := err.(*strconv.NumError)
	if !ok || numberError.Err != strconv.ErrRange {
		return 0, false
	}
	return number, true
}

func slotToNumber(value slot) (float64, bool) {
	if value.ref == nil {
		return math.Float64frombits(value.bits), true
	}
	if !value.isString() {
		return 0, false
	}
	return parseLuaNumber(stringSlotText(value))
}

func appendLuaNumber(destination []byte, number float64) []byte {
	switch {
	case math.IsNaN(number):
		return append(destination, "nan"...)
	case math.IsInf(number, 1):
		return append(destination, "inf"...)
	case math.IsInf(number, -1):
		return append(destination, "-inf"...)
	default:
		return strconv.AppendFloat(destination, number, 'g', 14, 64)
	}
}

func trimLuaNumberSpace(text string) string {
	first := 0
	for first < len(text) && isLuaNumberSpace(text[first]) {
		first++
	}
	last := len(text)
	for last > first && isLuaNumberSpace(text[last-1]) {
		last--
	}
	return text[first:last]
}

func isLuaNumberSpace(value byte) bool {
	switch value {
	case ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	default:
		return false
	}
}

func validLuaDecimal(text string, offset int) bool {
	digits := 0
	for offset < len(text) && isDigit(text[offset]) {
		offset++
		digits++
	}
	if offset < len(text) && text[offset] == '.' {
		offset++
		for offset < len(text) && isDigit(text[offset]) {
			offset++
			digits++
		}
	}
	if digits == 0 {
		return false
	}
	if offset < len(text) && (text[offset] == 'e' || text[offset] == 'E') {
		offset++
		if offset < len(text) && (text[offset] == '+' || text[offset] == '-') {
			offset++
		}
		exponentStart := offset
		for offset < len(text) && isDigit(text[offset]) {
			offset++
		}
		if offset == exponentStart {
			return false
		}
	}
	return offset == len(text)
}

// parseLuaHexInteger converts a validated 0x-prefixed integer without building
// a normalized temporary string. Keeping the leading 53 significant bits plus
// a round and sticky bit gives the same round-to-nearest-even result as a
// direct integer-to-float conversion, including integers longer than uint64.
func parseLuaHexInteger(text string, prefixOffset int) (float64, bool) {
	firstDigit := prefixOffset + 2
	if firstDigit == len(text) {
		return 0, false
	}

	firstSignificant := len(text)
	for index := firstDigit; index < len(text); index++ {
		if !isHexDigit(text[index]) {
			return 0, false
		}
		if firstSignificant == len(text) && text[index] != '0' {
			firstSignificant = index
		}
	}

	negative := prefixOffset != 0 && text[0] == '-'
	if firstSignificant == len(text) {
		if negative {
			return math.Copysign(0, -1), true
		}
		return 0, true
	}

	var mantissa uint64
	bitCount := 0
	round := false
	sticky := false
	for index := firstSignificant; index < len(text); index++ {
		nibble := hexDigitValue(text[index])
		firstBit := 3
		if index == firstSignificant {
			for firstBit > 0 && nibble&(1<<firstBit) == 0 {
				firstBit--
			}
		}
		for bit := firstBit; bit >= 0; bit-- {
			set := nibble&(1<<bit) != 0
			switch {
			case bitCount < 53:
				mantissa <<= 1
				if set {
					mantissa++
				}
			case bitCount == 53:
				round = set
			case set:
				sticky = true
			}
			bitCount++
		}
	}

	exponent := 0
	if bitCount > 53 {
		exponent = bitCount - 53
		if round && (sticky || mantissa&1 != 0) {
			mantissa++
		}
	}
	number := math.Ldexp(float64(mantissa), exponent)
	if negative {
		number = -number
	}
	return number, true
}

func hexDigitValue(value byte) uint8 {
	switch {
	case value >= '0' && value <= '9':
		return value - '0'
	case value >= 'a' && value <= 'f':
		return value - 'a' + 10
	default:
		return value - 'A' + 10
	}
}
