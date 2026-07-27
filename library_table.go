package lua

import (
	"math"
	"strings"
)

// The sorting algorithm follows Lua 5.1's ltablib.c. See
// THIRD_PARTY_NOTICES.md for the reference implementation's license.

const (
	// The sparse mover scans compact storage once and sorts only the integer
	// fields it finds. Keep the ordinary descending loop until the numeric
	// range is both substantial and decisively wider than that scan.
	tableInsertSparseShiftMinimum = 4 * 1024
	tableInsertSparseScanRatio    = 8
)

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
	loaded, err := state.ensureLoadedModules()
	if err != nil {
		return err
	}
	library := newTable(state.runtime, 0, len(tableLibraryFunctions))
	for _, definition := range tableLibraryFunctions {
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
		"table",
		slotFromTableObject(library),
	); err != nil {
		return err
	}
	state.setLoadedModule(loaded, "table", slotFromTableObject(library))
	return nil
}

// tableConcat follows Lua 5.1's argument order exactly: the separator is
// validated before the table, then the bounds. It validates and sizes the
// complete result before allocating one backing buffer. Numeric elements are
// formatted into stack scratch, and a strings.Builder transfers ownership of
// its buffer to the resulting string without another content copy.
func tableConcat(frame Frame) Outcome {
	var separatorString string
	var separatorNumber [32]byte
	var separatorBytes []byte
	if value, present := frame.argument(1); present &&
		!value.isNil() {
		switch value.kind() {
		case StringKind:
			separatorString = stringSlotText(value)
		case NumberKind:
			separatorBytes = appendLuaNumber(
				separatorNumber[:0],
				math.Float64frombits(value.bits),
			)
		default:
			return baseArgumentTypeError(frame, 1, "string")
		}
	}
	target, ok := frame.tableObject(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "table")
	}
	first := 1
	if value, present := frame.argument(2); present &&
		!value.isNil() {
		first, ok = frame.integerArgument(2)
		if !ok {
			return numberArgumentError(frame, 2)
		}
	}
	last := target.rawLen()
	if value, present := frame.argument(3); present &&
		!value.isNil() {
		last, ok = frame.integerArgument(3)
		if !ok {
			return numberArgumentError(frame, 3)
		}
	}

	if first > last {
		return frame.ReturnString("")
	}

	separatorLength := len(separatorString) + len(separatorBytes)
	maxInt := int(^uint(0) >> 1)
	total := 0
	var numberBuffer [32]byte
	for index := first; ; index++ {
		element, _ := target.rawIntSlot(index)
		length := 0
		switch element.kind() {
		case StringKind:
			length = stringSlotLen(element)
		case NumberKind:
			length = len(appendLuaNumber(
				numberBuffer[:0],
				math.Float64frombits(element.bits),
			))
		default:
			return libraryError(
				frame,
				"invalid value (%s) at index %d in table for 'concat'",
				element.kind(),
				index,
			)
		}
		if length > maxInt-total {
			return frame.sealError(
				newResourceError("string length overflow"),
			)
		}
		total += length
		if index == last {
			break
		}
		if separatorLength > maxInt-total {
			return frame.sealError(
				newResourceError("string length overflow"),
			)
		}
		total += separatorLength
	}

	if total == 0 {
		return frame.ReturnString("")
	}
	var builder strings.Builder
	builder.Grow(total)
	for index := first; ; index++ {
		element, _ := target.rawIntSlot(index)
		if element.isString() {
			builder.WriteString(stringSlotText(element))
		} else {
			formatted := appendLuaNumber(
				numberBuffer[:0],
				math.Float64frombits(element.bits),
			)
			_, _ = builder.Write(formatted)
		}
		if index == last {
			break
		}
		if separatorString != "" {
			builder.WriteString(separatorString)
		} else if len(separatorBytes) != 0 {
			_, _ = builder.Write(separatorBytes)
		}
	}
	return frame.ReturnString(builder.String())
}

// tableForEach traverses the whole table and stops at the first non-nil
// callback result, which becomes its own result.
func tableForEach(frame Frame) Outcome {
	target, ok := frame.tableObject(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "table")
	}
	callback, present := frame.argument(1)
	if !present || !callback.isFunction() {
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
		if !result.isNil() {
			return frame.returnCompactValues([2]slot{result}, 1, nil)
		}
		key = nextKey
	}
}

// tableForEachI visits 1..n over the sequence length captured before the first
// callback runs, as Lua 5.1 does. A hole inside that range is visited with a
// nil value rather than ending the traversal.
func tableForEachI(frame Frame) Outcome {
	target, ok := frame.tableObject(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "table")
	}
	length := target.rawLen()
	callback, present := frame.argument(1)
	if !present || !callback.isFunction() {
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
		if !result.isNil() {
			return frame.returnCompactValues([2]slot{result}, 1, nil)
		}
	}
	return frame.Return()
}

func tableGetN(frame Frame) Outcome {
	target, ok := frame.tableObject(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "table")
	}
	return frame.ReturnNumber(float64(target.rawLen()))
}

// tableInsert shifts the tail up with raw reads and writes, exactly as Lua
// 5.1 does, so a position outside the sequence keeps PUC's observable result
// instead of being rejected.
func tableInsert(frame Frame) Outcome {
	target, ok := frame.tableObject(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "table")
	}
	count := frame.ArgumentCount()
	end := target.rawLen() + 1
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
		if position <= end-tableInsertSparseShiftMinimum &&
			useSparseTableInsertShift(target, position, end-1) {
			target.shiftSparseIntegerRangeUp(position, end-1)
		} else {
			for index := end; index > position; index-- {
				previous, _ := target.rawIntSlot(index - 1)
				target.rawSetIntegerSlot(index, previous)
			}
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

func useSparseTableInsertShift(
	target *tableObject,
	first, last int,
) bool {
	if first > last {
		return false
	}
	width := uint64(last) - uint64(first) + 1
	if width < tableInsertSparseShiftMinimum {
		return false
	}
	scanSlots := uint64(target.array.len()) +
		uint64(target.store.entries.len()) + 1
	return width/tableInsertSparseScanRatio > scanSlots
}

// tableMaxN returns the largest positive numeric key, or zero when the table
// has none. It never consults the sequence length.
func tableMaxN(frame Frame) Outcome {
	target, ok := frame.tableObject(0)
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
		if nextKey.isNumber() {
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
	target, ok := frame.tableObject(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "table")
	}
	end := target.rawLen()
	position := end
	if value, present := frame.argument(1); present &&
		!value.isNil() {
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
	if _, ok := frame.tableObject(0); !ok {
		return baseArgumentTypeError(frame, 0, "table")
	}
	return libraryError(frame, "'setn' is obsolete")
}

func tableSort(frame Frame) Outcome {
	target, ok := frame.tableObject(0)
	if !ok {
		return baseArgumentTypeError(frame, 0, "table")
	}
	length := target.rawLen()
	comparator := nilSlot
	if value, present := frame.argument(1); present &&
		!value.isNil() {
		if !value.isFunction() {
			return baseArgumentTypeError(frame, 1, "function")
		}
		comparator = value
	}

	// Lua 5.1's sort calls lua_settop(L, 2) before the first comparison.
	// Keep the compact equivalent: arguments beyond the comparator cease to
	// be roots and cannot consume the nested comparator call's value budget.
	// The caller extent remains live, so trimming the borrowed native frame
	// cannot discard registers owned by its Lua caller.
	frame.discardArgumentsAfter(2)

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
	target *tableObject,
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
	if comparator.isNil() {
		return frame.lessThan(left, right)
	}
	result, failure := frame.callBinary(comparator, left, right)
	if failure != nil {
		return false, failure
	}
	return truthySlot(result), nil
}
