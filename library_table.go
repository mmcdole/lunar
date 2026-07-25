package lua

import "math"

var tableLibraryFunctions = [...]struct {
	name  string
	entry NativeFunc
}{
	{name: "concat", entry: tableConcat},
	{name: "foreach", entry: tableForEach},
	{name: "foreachi", entry: tableForEachI},
	{name: "getn", entry: tableGetN},
	{name: "insert", entry: tableInsert},
	{name: "maxn", entry: tableMaxN},
	{name: "remove", entry: tableRemove},
	{name: "setn", entry: tableSetN},
	{name: "sort", entry: tableSort},
}

// OpenTable installs the Lua 5.1 table library.
//
// Opening is explicit and idempotent in effect. Each call replaces the global
// table library and its functions with fresh canonical objects.
//
// Every entry operates on raw storage, as Lua 5.1 does: element access uses
// raw integer reads and writes, and the sequence length is the same border the
// length operator reports. Only an explicit callback, a comparator, or an
// __lt handler can run Lua.
func (state *State) OpenTable() error {
	if err := state.checkOpen(); err != nil {
		return err
	}
	library, err := state.NewTable(0, len(tableLibraryFunctions))
	if err != nil {
		return err
	}
	for _, definition := range tableLibraryFunctions {
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
	return state.globals.RawSetString("table", library.Value())
}

// tableConcat follows Lua 5.1's argument order exactly: the separator is
// validated before the table, then the bounds. Values are appended straight
// into one byte buffer, so a numeric element is spelled once and no
// intermediate Lua string is created.
func tableConcat(frame Frame) Outcome {
	var separator []byte
	if value, present := frame.argument(1); present &&
		value.kind() != NilKind {
		text, ok := appendTextSlot(nil, value)
		if !ok {
			return baseArgumentTypeError(frame, 1, "string")
		}
		separator = text
	}
	target, ok := frame.Table(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "table")
	}
	first := 1
	if value, present := frame.argument(2); present &&
		value.kind() != NilKind {
		first, ok = frame.integerArgument(2)
		if !ok {
			return numberArgumentError(frame, 2)
		}
	}
	last := target.RawLen()
	if value, present := frame.argument(3); present &&
		value.kind() != NilKind {
		last, ok = frame.integerArgument(3)
		if !ok {
			return numberArgumentError(frame, 3)
		}
	}

	var text []byte
	for index := first; index <= last; index++ {
		element, _ := target.rawIntSlot(index)
		appended, valid := appendTextSlot(text, element)
		if !valid {
			return libraryError(
				frame,
				"invalid value (%s) at index %d in table for 'concat'",
				element.kind(),
				index,
			)
		}
		text = appended
		if index != last {
			text = append(text, separator...)
		}
	}
	return frame.ReturnString(string(text))
}

// tableForEach traverses the whole table and stops at the first non-nil
// callback result, which becomes its own result.
func tableForEach(frame Frame) Outcome {
	target, ok := frame.Table(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "table")
	}
	callback, present := frame.argument(1)
	if !present || callback.kind() != FunctionKind {
		return baseArgumentTypeError(frame, 1, "function")
	}

	key := nilSlot
	for {
		nextKey, value, found, err := target.next(key)
		if err != nil {
			return frame.RaiseString(err.Error())
		}
		if !found {
			return frame.Return()
		}
		result, failure := frame.callBinary(callback, nextKey, value)
		if failure != nil {
			return frame.sealError(failure)
		}
		if result.kind() != NilKind {
			return frame.returnCompactValues([2]slot{result}, 1, nil)
		}
		key = nextKey
	}
}

// tableForEachI visits 1..n over the sequence length captured before the first
// callback runs, as Lua 5.1 does. A hole inside that range is visited with a
// nil value rather than ending the traversal.
func tableForEachI(frame Frame) Outcome {
	target, ok := frame.Table(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "table")
	}
	length := target.RawLen()
	callback, present := frame.argument(1)
	if !present || callback.kind() != FunctionKind {
		return baseArgumentTypeError(frame, 1, "function")
	}

	for index := 1; index <= length; index++ {
		value, _ := target.rawIntSlot(index)
		result, failure := frame.callBinary(
			callback,
			numberSlot(float64(index)),
			value,
		)
		if failure != nil {
			return frame.sealError(failure)
		}
		if result.kind() != NilKind {
			return frame.returnCompactValues([2]slot{result}, 1, nil)
		}
	}
	return frame.Return()
}

func tableGetN(frame Frame) Outcome {
	target, ok := frame.Table(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "table")
	}
	return frame.ReturnNumber(float64(target.RawLen()))
}

// tableInsert shifts the tail up with raw reads and writes, exactly as Lua
// 5.1 does, so a position outside the sequence keeps PUC's observable result
// instead of being rejected.
func tableInsert(frame Frame) Outcome {
	target, ok := frame.Table(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "table")
	}
	count := frame.ArgumentCount()
	end := target.RawLen() + 1
	position := end
	switch count {
	case 2:
	case 3:
		position, ok = frame.integerArgument(1)
		if !ok {
			return numberArgumentError(frame, 1)
		}
		if position > end {
			end = position
		}
		for index := end; index > position; index-- {
			previous, _ := target.rawIntSlot(index - 1)
			target.rawSetIntegerSlot(index, previous)
		}
	default:
		return libraryError(
			frame,
			"wrong number of arguments to 'insert'",
		)
	}
	value, _ := frame.argument(count - 1)
	target.rawSetIntegerSlot(position, value)
	return frame.Return()
}

// tableMaxN returns the largest positive numeric key, or zero when the table
// has none. It never consults the sequence length.
func tableMaxN(frame Frame) Outcome {
	target, ok := frame.Table(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "table")
	}
	largest := 0.0
	key := nilSlot
	for {
		nextKey, _, found, err := target.next(key)
		if err != nil {
			return frame.RaiseString(err.Error())
		}
		if !found {
			return frame.ReturnNumber(largest)
		}
		if nextKey.kind() == NumberKind {
			if number := math.Float64frombits(
				nextKey.bits,
			); number > largest {
				largest = number
			}
		}
		key = nextKey
	}
}

// tableRemove returns no results when the position lies outside the sequence,
// which Lua 5.1 distinguishes from removing a nil element.
func tableRemove(frame Frame) Outcome {
	target, ok := frame.Table(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "table")
	}
	end := target.RawLen()
	position := end
	if value, present := frame.argument(1); present &&
		value.kind() != NilKind {
		position, ok = frame.integerArgument(1)
		if !ok {
			return numberArgumentError(frame, 1)
		}
	}
	if position < 1 || position > end {
		return frame.Return()
	}

	removed, _ := target.rawIntSlot(position)
	for ; position < end; position++ {
		next, _ := target.rawIntSlot(position + 1)
		target.rawSetIntegerSlot(position, next)
	}
	target.rawSetIntegerSlot(end, nilSlot)
	return frame.returnCompactValues([2]slot{removed}, 1, nil)
}

// tableSetN reports Lua 5.1's obsolescence failure. The standard distribution
// leaves LUA_COMPAT_GETN undefined, which makes the stored size a no-op and
// setn nothing but this error; only the table argument is validated first.
func tableSetN(frame Frame) Outcome {
	if _, ok := frame.Table(0); !ok {
		return baseArgumentTypeError(frame, 0, "table")
	}
	return libraryError(frame, "'setn' is obsolete")
}

func tableSort(frame Frame) Outcome {
	target, ok := frame.Table(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "table")
	}
	length := target.RawLen()
	comparator := nilSlot
	if value, present := frame.argument(1); present &&
		value.kind() != NilKind {
		if value.kind() != FunctionKind {
			return baseArgumentTypeError(frame, 1, "function")
		}
		comparator = value
	}
	if failure := sortRange(
		frame,
		target,
		comparator,
		1,
		length,
	); failure != nil {
		return frame.sealError(failure)
	}
	return frame.Return()
}

// sortRange is Lua 5.1's quicksort over t[low..high].
//
// The algorithm is reproduced rather than replaced by Go's sort because its
// behavior is observable: it is unstable, an inconsistent order function must
// produce Lua's "invalid order function for sorting" failure rather than a Go
// panic or a silent result, and a comparator can see and mutate the table
// mid-sort. Each element access is a raw compact read or write and the
// recursion always takes the smaller partition, bounding depth at log2 of the
// range.
func sortRange(
	frame Frame,
	target *Table,
	comparator slot,
	low int,
	high int,
) *Error {
	for low < high {
		// Order t[low], t[middle], and t[high] so the median becomes the
		// pivot and the outer elements become sentinels.
		lowValue, _ := target.rawIntSlot(low)
		highValue, _ := target.rawIntSlot(high)
		less, failure := sortLess(
			frame,
			comparator,
			highValue,
			lowValue,
		)
		if failure != nil {
			return failure
		}
		if less {
			target.rawSetIntegerSlot(low, highValue)
			target.rawSetIntegerSlot(high, lowValue)
		}
		if high-low == 1 {
			return nil
		}

		middle := (low + high) / 2
		middleValue, _ := target.rawIntSlot(middle)
		lowValue, _ = target.rawIntSlot(low)
		less, failure = sortLess(
			frame,
			comparator,
			middleValue,
			lowValue,
		)
		if failure != nil {
			return failure
		}
		if less {
			target.rawSetIntegerSlot(middle, lowValue)
			target.rawSetIntegerSlot(low, middleValue)
		} else {
			highValue, _ = target.rawIntSlot(high)
			less, failure = sortLess(
				frame,
				comparator,
				highValue,
				middleValue,
			)
			if failure != nil {
				return failure
			}
			if less {
				target.rawSetIntegerSlot(middle, highValue)
				target.rawSetIntegerSlot(high, middleValue)
			}
		}
		if high-low == 2 {
			return nil
		}

		pivot, _ := target.rawIntSlot(middle)
		penultimate, _ := target.rawIntSlot(high - 1)
		target.rawSetIntegerSlot(middle, penultimate)
		target.rawSetIntegerSlot(high-1, pivot)

		lower, upper := low, high-1
		var lowerValue, upperValue slot
		for {
			for {
				lower++
				lowerValue, _ = target.rawIntSlot(lower)
				less, failure = sortLess(
					frame,
					comparator,
					lowerValue,
					pivot,
				)
				if failure != nil {
					return failure
				}
				if !less {
					break
				}
				if lower > high {
					return libraryFailure(
						frame,
						"invalid order function for sorting",
					)
				}
			}
			for {
				upper--
				upperValue, _ = target.rawIntSlot(upper)
				less, failure = sortLess(
					frame,
					comparator,
					pivot,
					upperValue,
				)
				if failure != nil {
					return failure
				}
				if !less {
					break
				}
				if upper < low {
					return libraryFailure(
						frame,
						"invalid order function for sorting",
					)
				}
			}
			if upper < lower {
				break
			}
			target.rawSetIntegerSlot(lower, upperValue)
			target.rawSetIntegerSlot(upper, lowerValue)
		}

		penultimate, _ = target.rawIntSlot(high - 1)
		lowerValue, _ = target.rawIntSlot(lower)
		target.rawSetIntegerSlot(high-1, lowerValue)
		target.rawSetIntegerSlot(lower, penultimate)

		// Recur into the smaller partition and iterate over the larger one.
		var nestedLow, nestedHigh int
		if lower-low < high-lower {
			nestedLow, nestedHigh = low, lower-1
			low = lower + 1
		} else {
			nestedLow, nestedHigh = lower+1, high
			high = lower - 1
		}
		if failure := sortRange(
			frame,
			target,
			comparator,
			nestedLow,
			nestedHigh,
		); failure != nil {
			return failure
		}
	}
	return nil
}

func sortLess(
	frame Frame,
	comparator slot,
	left slot,
	right slot,
) (bool, *Error) {
	if comparator.kind() == NilKind {
		return frame.lessThan(left, right)
	}
	result, failure := frame.callBinary(comparator, left, right)
	if failure != nil {
		return false, failure
	}
	return truthySlot(result), nil
}

// appendTextSlot appends value's Lua string spelling and reports whether the
// value had one. Strings and numbers do, matching lua_isstring; a number uses
// the same primitive spelling the runtime uses elsewhere.
func appendTextSlot(destination []byte, value slot) ([]byte, bool) {
	switch value.kind() {
	case StringKind:
		return append(destination, (*luaString)(value.ref).text...), true
	case NumberKind:
		return appendLuaNumber(
			destination,
			math.Float64frombits(value.bits),
		), true
	default:
		return destination, false
	}
}
