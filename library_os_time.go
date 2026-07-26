package lua

import (
	"runtime/metrics"
	"time"
)

func osClock(frame Frame) Outcome {
	seconds, ok := hostProcessCPUSeconds()
	if !ok {
		seconds = runtimeProcessCPUSeconds()
	}
	return frame.ReturnNumber(seconds)
}

func runtimeProcessCPUSeconds() float64 {
	samples := [...]metrics.Sample{
		{Name: "/cpu/classes/user:cpu-seconds"},
		{Name: "/cpu/classes/gc/total:cpu-seconds"},
		{Name: "/cpu/classes/scavenge/total:cpu-seconds"},
	}
	metrics.Read(samples[:])
	total := float64(0)
	for index := range samples {
		if samples[index].Value.Kind() == metrics.KindFloat64 {
			total += samples[index].Value.Float64()
		}
	}
	return total
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

func osDate(frame Frame) Outcome {
	format := "%c"
	if argument, present := frame.argument(0); present &&
		argument.kind() != NilKind {
		var ok bool
		format, ok = frame.textArgument(0)
		if !ok {
			return baseArgumentTypeError(frame, 0, "string")
		}
		format = luaCString(format)
	}

	var seconds int64
	if argument, present := frame.argument(1); present &&
		argument.kind() != NilKind {
		number, ok := frame.numberArgument(1)
		if !ok {
			return numberArgumentError(frame, 1)
		}
		seconds = saturatingInt64(number)
	} else {
		seconds = frame.thread.state.now().Unix()
	}

	location := frame.thread.state.location
	if len(format) != 0 && format[0] == '!' {
		location = time.UTC
		format = format[1:]
	}
	date := time.Unix(seconds, 0).In(location)
	if format == "*t" {
		return returnDateTable(frame, date)
	}
	text, tooLarge := formatDate(format, date)
	if tooLarge {
		return frame.sealError(
			newResourceError("resulting string too large"),
		)
	}
	return frame.ReturnString(stringFromOwnedBytes(text))
}

func returnDateTable(frame Frame, date time.Time) Outcome {
	table := newTable(frame.thread.owner, 0, 9)
	fields := [...]struct {
		name  string
		value slot
	}{
		{name: "sec", value: numberSlot(float64(date.Second()))},
		{name: "min", value: numberSlot(float64(date.Minute()))},
		{name: "hour", value: numberSlot(float64(date.Hour()))},
		{name: "day", value: numberSlot(float64(date.Day()))},
		{name: "month", value: numberSlot(float64(date.Month()))},
		{name: "year", value: numberSlot(float64(date.Year()))},
		{name: "wday", value: numberSlot(float64(date.Weekday()) + 1)},
		{name: "yday", value: numberSlot(float64(date.YearDay()))},
		{name: "isdst", value: falseSlot},
	}
	if date.IsDST() {
		fields[len(fields)-1].value = trueSlot
	}
	for _, field := range fields {
		status := table.rawSetSlot(
			stringSlot(frame.thread.owner.strings.make(field.name)),
			field.value,
		)
		if status != tableKeyValid {
			panic("lua: date field produced an invalid table key")
		}
	}
	return frame.returnOne(frame.activation(), slotFromTable(table))
}

func osTime(frame Frame) Outcome {
	value, present := frame.argument(0)
	if !present || value.kind() == NilKind {
		return frame.ReturnNumber(
			float64(frame.thread.state.now().Unix()),
		)
	}
	if value.kind() != TableKind {
		return baseArgumentTypeError(frame, 0, "table")
	}
	frame.discardArgumentsAfter(1)

	second, outcome, done := dateTableInteger(
		frame,
		value,
		"sec",
		0,
	)
	if done {
		return outcome
	}
	minute, outcome, done := dateTableInteger(
		frame,
		value,
		"min",
		0,
	)
	if done {
		return outcome
	}
	hour, outcome, done := dateTableInteger(
		frame,
		value,
		"hour",
		12,
	)
	if done {
		return outcome
	}
	day, outcome, done := dateTableInteger(
		frame,
		value,
		"day",
		-1,
	)
	if done {
		return outcome
	}
	month, outcome, done := dateTableInteger(
		frame,
		value,
		"month",
		-1,
	)
	if done {
		return outcome
	}
	year, outcome, done := dateTableInteger(
		frame,
		value,
		"year",
		-1,
	)
	if done {
		return outcome
	}
	daylight, failure := dateTableDaylight(frame, value)
	if failure != nil {
		return frame.sealError(failure)
	}

	date := makeLocalDate(
		year,
		time.Month(month),
		day,
		hour,
		minute,
		second,
		daylight,
		frame.thread.state.location,
	)
	seconds := date.Unix()
	// PUC reserves time_t(-1) as the mktime failure sentinel even though it
	// also denotes a real instant.
	if seconds == -1 {
		return frame.ReturnNil()
	}
	return frame.ReturnNumber(float64(seconds))
}

func dateTableInteger(
	frame Frame,
	table slot,
	name string,
	fallback int,
) (int, Outcome, bool) {
	value, failure := frame.indexCompact(
		table,
		stringSlot(frame.thread.owner.strings.make(name)),
	)
	if failure != nil {
		return 0, frame.sealError(failure), true
	}
	if number, ok := slotToNumber(value); ok {
		return libraryInteger(number), Outcome{}, false
	}
	if fallback >= 0 {
		return fallback, Outcome{}, false
	}
	return 0, libraryError(
		frame,
		"field '%s' missing in date table",
		name,
	), true
}

func dateTableDaylight(frame Frame, table slot) (int, *Error) {
	value, failure := frame.indexCompact(
		table,
		stringSlot(frame.thread.owner.strings.make("isdst")),
	)
	if failure != nil {
		return 0, failure
	}
	if value.kind() == NilKind {
		return -1, nil
	}
	if truthySlot(value) {
		return 1, nil
	}
	return 0, nil
}

func makeLocalDate(
	year int,
	month time.Month,
	day int,
	hour int,
	minute int,
	second int,
	daylight int,
	location *time.Location,
) time.Time {
	wall := time.Date(
		year,
		month,
		day,
		hour,
		minute,
		second,
		0,
		time.UTC,
	)
	seed := time.Date(
		wall.Year(),
		wall.Month(),
		wall.Day(),
		wall.Hour(),
		wall.Minute(),
		wall.Second(),
		0,
		location,
	)
	choices := nearbyZoneChoices(seed)

	if daylight >= 0 {
		if choice, found := nearestZoneChoice(
			choices,
			daylight != 0,
		); found {
			if candidate, ok := dateWithZoneOffset(
				wall,
				choice.offset,
				location,
			); ok {
				return candidate
			}
		}
		return seed
	}

	var standard *zoneChoice
	var fallback *zoneChoice
	for index := 0; index < choices.count; index++ {
		choice := &choices.values[index]
		candidate, ok := dateWithZoneOffset(
			wall,
			choice.offset,
			location,
		)
		if !ok {
			continue
		}
		if sameWallDate(candidate, wall) {
			if fallback == nil ||
				choice.distance < fallback.distance {
				fallback = choice
			}
			// Darwin's mktime and the common Lua 5.1 builds prefer the
			// daylight occurrence when a repeated wall time is ambiguous.
			if choice.daylight {
				return candidate
			}
		}
		if !choice.daylight &&
			(standard == nil ||
				choice.distance < standard.distance) {
			standard = choice
		}
	}
	if fallback != nil {
		candidate, _ := dateWithZoneOffset(
			wall,
			fallback.offset,
			location,
		)
		return candidate
	}
	// No instant names this wall time. Interpreting it with the nearest
	// standard offset normalizes a spring gap forward, matching mktime's
	// default rule on the reference platforms.
	if standard != nil {
		candidate, _ := dateWithZoneOffset(
			wall,
			standard.offset,
			location,
		)
		return candidate
	}
	return seed
}

type zoneChoice struct {
	offset   int
	daylight bool
	distance time.Duration
}

const nearbyZoneTransitionLimit = 8

type zoneChoiceSet struct {
	values [nearbyZoneTransitionLimit*2 + 1]zoneChoice
	count  int
}

func (choices *zoneChoiceSet) add(sample, seed time.Time) {
	_, offset := sample.Zone()
	daylight := sample.IsDST()
	distance := absoluteDuration(sample.Sub(seed))
	for index := 0; index < choices.count; index++ {
		choice := &choices.values[index]
		if choice.offset == offset &&
			choice.daylight == daylight {
			if distance < choice.distance {
				choice.distance = distance
			}
			return
		}
	}
	if choices.count == len(choices.values) {
		panic("lua: too many nearby timezone choices")
	}
	choices.values[choices.count] = zoneChoice{
		offset:   offset,
		daylight: daylight,
		distance: distance,
	}
	choices.count++
}

func nearbyZoneChoices(seed time.Time) zoneChoiceSet {
	var choices zoneChoiceSet
	choices.add(seed, seed)

	before := seed
	after := seed
	for range nearbyZoneTransitionLimit {
		start, _ := before.ZoneBounds()
		if !start.IsZero() {
			sample := start.Add(-time.Second)
			if sample.Before(before) {
				choices.add(sample, seed)
				before = sample
			}
		}

		_, end := after.ZoneBounds()
		if !end.IsZero() && end.After(after) {
			choices.add(end, seed)
			after = end
		}
	}
	return choices
}

func nearestZoneChoice(
	choices zoneChoiceSet,
	daylight bool,
) (zoneChoice, bool) {
	var best zoneChoice
	found := false
	for index := 0; index < choices.count; index++ {
		choice := choices.values[index]
		if choice.daylight != daylight {
			continue
		}
		if !found || choice.distance < best.distance {
			best = choice
			found = true
		}
	}
	return best, found
}

func dateWithZoneOffset(
	wall time.Time,
	offset int,
	location *time.Location,
) (time.Time, bool) {
	seconds := wall.Unix()
	offsetSeconds := int64(offset)
	if offsetSeconds > 0 &&
		seconds < mathMinInt64+offsetSeconds {
		return time.Time{}, false
	}
	if offsetSeconds < 0 &&
		seconds > mathMaxInt64+offsetSeconds {
		return time.Time{}, false
	}
	return time.Unix(seconds-offsetSeconds, 0).In(location), true
}

const (
	mathMaxInt64 = int64(^uint64(0) >> 1)
	mathMinInt64 = -mathMaxInt64 - 1
)

func sameWallDate(left, right time.Time) bool {
	return left.Year() == right.Year() &&
		left.Month() == right.Month() &&
		left.Day() == right.Day() &&
		left.Hour() == right.Hour() &&
		left.Minute() == right.Minute() &&
		left.Second() == right.Second()
}

func absoluteDuration(value time.Duration) time.Duration {
	if value >= 0 {
		return value
	}
	if value == time.Duration(mathMinInt64) {
		return time.Duration(mathMaxInt64)
	}
	return -value
}
