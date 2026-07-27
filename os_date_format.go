package lua

import (
	"strconv"
	"time"
)

var (
	shortWeekdays = [...]string{
		"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat",
	}
	longWeekdays = [...]string{
		"Sunday", "Monday", "Tuesday", "Wednesday",
		"Thursday", "Friday", "Saturday",
	}
	shortMonths = [...]string{
		"",
		"Jan", "Feb", "Mar", "Apr", "May", "Jun",
		"Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
	}
	longMonths = [...]string{
		"",
		"January", "February", "March", "April", "May", "June",
		"July", "August", "September", "October", "November",
		"December",
	}
)

func formatDate(format string, date time.Time) ([]byte, bool) {
	if len(format) > maximumConstructedStringBytes {
		return nil, true
	}
	capacity := len(format)
	if capacity > 4096 {
		capacity = 4096
	} else {
		if capacity <= 4096-32 {
			capacity += 32
		} else {
			capacity = 4096
		}
	}
	text := make([]byte, 0, capacity)
	for index := 0; index < len(format); index++ {
		if format[index] != '%' || index+1 == len(format) {
			if len(text) == maximumConstructedStringBytes {
				return nil, true
			}
			text = append(text, format[index])
			continue
		}
		index++
		directive := format[index]
		if directive == 'Z' {
			name, _ := date.Zone()
			if len(name) > maximumConstructedStringBytes-len(text) {
				return nil, true
			}
			text = append(text, name...)
			continue
		}
		if directive == '+' {
			growth := datePlusLength(date)
			if growth > maximumConstructedStringBytes-len(text) {
				return nil, true
			}
			text = appendDateDirective(text, directive, date)
			continue
		}

		var scratch [128]byte
		rendered := appendDateDirective(
			scratch[:0],
			directive,
			date,
		)
		if len(rendered) > maximumConstructedStringBytes-len(text) {
			return nil, true
		}
		text = append(text, rendered...)
	}
	return text, false
}

func datePlusLength(date time.Time) int {
	name, _ := date.Zone()
	return 3 + 1 + 3 + 1 + 2 + 1 + 8 + 1 +
		len(name) + 1 + dateIntegerLength(date.Year(), 4)
}

func dateIntegerLength(value, width int) int {
	negative, magnitude := integerMagnitude(value)
	if negative {
		digits := decimalIntegerLength(magnitude)
		if digits+1 > width {
			return digits + 1
		}
		return width
	}
	digits := decimalIntegerLength(magnitude)
	if digits > width {
		return digits
	}
	return width
}

func decimalIntegerLength(value uint64) int {
	length := 1
	for value >= 10 {
		value /= 10
		length++
	}
	return length
}

func appendDateDirective(
	text []byte,
	directive byte,
	date time.Time,
) []byte {
	weekday := int(date.Weekday())
	month := int(date.Month())
	switch directive {
	case '%':
		return append(text, '%')
	case 'a':
		return append(text, shortWeekdays[weekday]...)
	case 'A':
		return append(text, longWeekdays[weekday]...)
	case 'b', 'h':
		return append(text, shortMonths[month]...)
	case 'B':
		return append(text, longMonths[month]...)
	case 'c':
		text = append(text, shortWeekdays[weekday]...)
		text = append(text, ' ')
		text = append(text, shortMonths[month]...)
		text = append(text, ' ')
		text = appendDateInteger(text, date.Day(), 2, ' ')
		text = append(text, ' ')
		text = appendDateClock(text, date)
		text = append(text, ' ')
		return appendDateInteger(text, date.Year(), 4, '0')
	case 'C':
		return appendDateInteger(
			text,
			floorDivision(date.Year(), 100),
			2,
			'0',
		)
	case 'd':
		return appendDateInteger(text, date.Day(), 2, '0')
	case 'D':
		text = appendDateInteger(text, month, 2, '0')
		text = append(text, '/')
		text = appendDateInteger(text, date.Day(), 2, '0')
		text = append(text, '/')
		return appendDateInteger(
			text,
			positiveMod(date.Year(), 100),
			2,
			'0',
		)
	case 'e':
		return appendDateInteger(text, date.Day(), 2, ' ')
	case 'F':
		text = appendDateInteger(text, date.Year(), 4, '0')
		text = append(text, '-')
		text = appendDateInteger(text, month, 2, '0')
		text = append(text, '-')
		return appendDateInteger(text, date.Day(), 2, '0')
	case 'g':
		isoYear, _ := date.ISOWeek()
		return appendDateInteger(
			text,
			positiveMod(isoYear, 100),
			2,
			'0',
		)
	case 'G':
		isoYear, _ := date.ISOWeek()
		return appendDateInteger(text, isoYear, 4, '0')
	case 'H':
		return appendDateInteger(text, date.Hour(), 2, '0')
	case 'I':
		return appendDateInteger(text, twelveHour(date.Hour()), 2, '0')
	case 'j':
		return appendDateInteger(text, date.YearDay(), 3, '0')
	case 'k':
		return appendDateInteger(text, date.Hour(), 2, ' ')
	case 'l':
		return appendDateInteger(text, twelveHour(date.Hour()), 2, ' ')
	case 'm':
		return appendDateInteger(text, month, 2, '0')
	case 'M':
		return appendDateInteger(text, date.Minute(), 2, '0')
	case 'n':
		return append(text, '\n')
	case 'p':
		if date.Hour() < 12 {
			return append(text, "AM"...)
		}
		return append(text, "PM"...)
	case 'r':
		text = appendDateInteger(
			text,
			twelveHour(date.Hour()),
			2,
			'0',
		)
		text = append(text, ':')
		text = appendDateInteger(text, date.Minute(), 2, '0')
		text = append(text, ':')
		text = appendDateInteger(text, date.Second(), 2, '0')
		text = append(text, ' ')
		if date.Hour() < 12 {
			return append(text, "AM"...)
		}
		return append(text, "PM"...)
	case 'R':
		text = appendDateInteger(text, date.Hour(), 2, '0')
		text = append(text, ':')
		return appendDateInteger(text, date.Minute(), 2, '0')
	case 'S':
		return appendDateInteger(text, date.Second(), 2, '0')
	case 't':
		return append(text, '\t')
	case 'T', 'X':
		return appendDateClock(text, date)
	case 'u':
		day := weekday
		if day == 0 {
			day = 7
		}
		return appendDateInteger(text, day, 1, '0')
	case 'U':
		week := (date.YearDay() - 1 + 7 - weekday) / 7
		return appendDateInteger(text, week, 2, '0')
	case 'V':
		_, week := date.ISOWeek()
		return appendDateInteger(text, week, 2, '0')
	case 'w':
		return appendDateInteger(text, weekday, 1, '0')
	case 'W':
		mondayDay := (weekday + 6) % 7
		week := (date.YearDay() - 1 + 7 - mondayDay) / 7
		return appendDateInteger(text, week, 2, '0')
	case 'x':
		text = appendDateInteger(text, month, 2, '0')
		text = append(text, '/')
		text = appendDateInteger(text, date.Day(), 2, '0')
		text = append(text, '/')
		return appendDateInteger(
			text,
			positiveMod(date.Year(), 100),
			2,
			'0',
		)
	case 'y':
		return appendDateInteger(
			text,
			positiveMod(date.Year(), 100),
			2,
			'0',
		)
	case 'Y':
		return appendDateInteger(text, date.Year(), 4, '0')
	case 'z':
		_, offset := date.Zone()
		sign := byte('+')
		negative, magnitude := integerMagnitude(offset)
		if negative {
			sign = '-'
		}
		text = append(text, sign)
		text = appendDateUnsigned(text, magnitude/3600, 2, '0')
		return appendDateUnsigned(
			text,
			magnitude/60%60,
			2,
			'0',
		)
	case 'Z':
		name, _ := date.Zone()
		return append(text, name...)
	case '+':
		text = append(text, shortWeekdays[weekday]...)
		text = append(text, ' ')
		text = append(text, shortMonths[month]...)
		text = append(text, ' ')
		text = appendDateInteger(text, date.Day(), 2, ' ')
		text = append(text, ' ')
		text = appendDateClock(text, date)
		text = append(text, ' ')
		name, _ := date.Zone()
		text = append(text, name...)
		text = append(text, ' ')
		return appendDateInteger(text, date.Year(), 4, '0')
	default:
		// The host C library decides unknown conversion behavior in PUC.
		// Lugo keeps it deterministic by copying the conversion byte.
		return append(text, directive)
	}
}

func appendDateClock(text []byte, date time.Time) []byte {
	text = appendDateInteger(text, date.Hour(), 2, '0')
	text = append(text, ':')
	text = appendDateInteger(text, date.Minute(), 2, '0')
	text = append(text, ':')
	return appendDateInteger(text, date.Second(), 2, '0')
}

func appendDateInteger(
	text []byte,
	value int,
	width int,
	padding byte,
) []byte {
	negative, magnitude := integerMagnitude(value)
	if negative {
		text = append(text, '-')
		width--
	}
	return appendDateUnsigned(text, magnitude, width, padding)
}

func appendDateUnsigned(
	text []byte,
	value uint64,
	width int,
	padding byte,
) []byte {
	var scratch [32]byte
	number := strconv.AppendUint(scratch[:0], value, 10)
	if len(number) >= width {
		return append(text, number...)
	}
	for count := len(number); count < width; count++ {
		text = append(text, padding)
	}
	return append(text, number...)
}

func integerMagnitude(value int) (bool, uint64) {
	number := int64(value)
	if number >= 0 {
		return false, uint64(number)
	}
	return true, uint64(-(number + 1)) + 1
}

func twelveHour(hour int) int {
	hour %= 12
	if hour == 0 {
		return 12
	}
	return hour
}

func floorDivision(value, divisor int) int {
	quotient := value / divisor
	if value < 0 && value%divisor != 0 {
		quotient--
	}
	return quotient
}

func positiveMod(value, divisor int) int {
	result := value % divisor
	if result < 0 {
		result += divisor
	}
	return result
}
