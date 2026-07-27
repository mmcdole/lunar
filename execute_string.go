package lua

import (
	"math"
	"strings"
)

// Keep the table border search and its loop temporaries out of the dispatch
// frame while preserving a direct non-reentrant length path.
//
//go:noinline
func writeTableLength(destination *slot, table *tableObject) {
	writeSlot(destination, numberSlot(float64(table.rawLen())))
}

//go:noinline
func slowLength(
	thread *Thread,
	frameIndex int,
	code instruction,
) *Error {
	frame := thread.frames[frameIndex]
	nextPC := int(frame.pc)
	instructionPC := nextPC - 1
	base := int(frame.base)
	source := thread.values[base+code.b()]

	switch source.kind() {
	case StringKind:
		writeSlot(
			&thread.values[base+code.a()],
			numberSlot(float64(stringSlotLen(source))),
		)
		return nil
	case TableKind:
		writeSlot(
			&thread.values[base+code.a()],
			numberSlot(float64((*tableObject)(source.ref).rawLen())),
		)
		return nil
	}

	method, found := binaryMetamethod(
		thread,
		source,
		nilSlot,
		metaLength,
	)
	if !found {
		return newExecutionTypeError(
			thread,
			frameIndex,
			instructionPC,
			code.b(),
			"get length of",
			source.kind(),
		)
	}
	return startMetamethodCall(
		thread,
		frameIndex,
		instructionPC,
		method,
		source,
		nilSlot,
		nilSlot,
		2,
		1,
		executionContinuation{
			nextPC: uint32(nextPC),
			code:   code,
		},
	)
}

// slowConcat reduces a contiguous operand range from right to left. Adjacent
// strings and numbers collapse in one backing-buffer build; a metamethod
// result resumes at the exact pair that produced it.
//
//go:noinline
func slowConcat(
	thread *Thread,
	frameIndex int,
	code instruction,
) *Error {
	frame := thread.frames[frameIndex]
	nextPC := int(frame.pc)
	instructionPC := nextPC - 1
	base := int(frame.base)
	first := code.b()
	last := code.c()

	for last > first {
		left := thread.values[base+last-1]
		right := thread.values[base+last]
		if isDirectConcatValue(left) && isDirectConcatValue(right) {
			start := last - 1
			for start > first &&
				isDirectConcatValue(thread.values[base+start-1]) {
				start--
			}
			result, overflow := concatDirectValues(
				thread,
				thread.values[base+start:base+last+1],
			)
			if overflow {
				return newExecutionRuntimeError(
					thread,
					frameIndex,
					instructionPC,
					"string length overflow",
				)
			}
			writeSlot(&thread.values[base+start], result)
			last = start
			continue
		}

		method, found := binaryMetamethod(
			thread,
			left,
			right,
			metaConcat,
		)
		if !found {
			invalid := left
			invalidRegister := last - 1
			if isDirectConcatValue(left) {
				invalid = right
				invalidRegister = last
			}
			return newExecutionTypeError(
				thread,
				frameIndex,
				instructionPC,
				invalidRegister,
				"concatenate",
				invalid.kind(),
			)
		}

		resume := makeABC(opConcat, code.a(), first, last-1)
		return startMetamethodCall(
			thread,
			frameIndex,
			instructionPC,
			method,
			left,
			right,
			nilSlot,
			2,
			1,
			executionContinuation{
				nextPC: uint32(nextPC),
				code:   resume,
				mode:   continuationConcat,
			},
		)
	}

	writeSlot(
		&thread.values[base+code.a()],
		thread.values[base+first],
	)
	return nil
}

func isDirectConcatValue(value slot) bool {
	return value.ref == nil || value.isString()
}

func concatDirectValues(
	thread *Thread,
	values []slot,
) (slot, bool) {
	onlyText := -1
	allStrings := true
	for index, value := range values {
		if !value.isString() {
			allStrings = false
			break
		}
		if stringSlotLen(value) != 0 {
			if onlyText >= 0 {
				allStrings = false
				break
			}
			onlyText = index
		}
	}
	if allStrings {
		if onlyText >= 0 {
			return values[onlyText], false
		}
		return values[0], false
	}

	total := 0
	var numberBuffer [32]byte
	for _, value := range values {
		length := 0
		if value.ref == nil {
			formatted := appendLuaNumber(
				numberBuffer[:0],
				math.Float64frombits(value.bits),
			)
			length = len(formatted)
		} else {
			length = stringSlotLen(value)
		}
		if length > int(^uint(0)>>1)-total {
			return nilSlot, true
		}
		total += length
	}

	var builder strings.Builder
	builder.Grow(total)
	for _, value := range values {
		if value.ref == nil {
			formatted := appendLuaNumber(
				numberBuffer[:0],
				math.Float64frombits(value.bits),
			)
			_, _ = builder.Write(formatted)
		} else {
			builder.WriteString(stringSlotText(value))
		}
	}
	return stringSlot(thread.owner.strings.make(builder.String())), false
}
