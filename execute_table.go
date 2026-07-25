package lua

const maxTableMetamethodChain = 100

//go:noinline
func slowTableGet(
	thread *Thread,
	frameIndex int,
	code instruction,
) *Error {
	frame := thread.frames[frameIndex]
	nextPC := int(frame.pc)
	instructionPC := nextPC - 1
	base := int(frame.base)
	var target, key slot

	switch code.opcode() {
	case opGetGlobal:
		target = slotFromTable(frame.function.environment)
		key = frame.function.prototype.constants[code.bx()]
	case opGetTable:
		target = thread.values[base+code.b()]
		key = operandSlot(
			thread.values,
			frame.function.prototype.constants,
			base,
			code.c(),
		)
	case opSelf:
		target = thread.values[base+code.b()]
		// Lua 5.1 publishes the receiver before reading RK(C). Preserve that
		// order for verified bytecode whose key register overlaps A+1.
		writeSlot(&thread.values[base+code.a()+1], target)
		key = operandSlot(
			thread.values,
			frame.function.prototype.constants,
			base,
			code.c(),
		)
	default:
		panic("lua: invalid table read opcode")
	}

	for range maxTableMetamethodChain {
		var method slot
		var found bool
		if target.kind() == TableKind {
			table := (*Table)(target.ref)
			if result, found := table.rawSlot(key); found {
				writeSlot(&thread.values[base+code.a()], result)
				return nil
			}
			method, found = metamethodSlot(thread, target, metaIndex)
			if !found {
				writeSlot(&thread.values[base+code.a()], nilSlot)
				return nil
			}
		} else {
			method, found = metamethodSlot(thread, target, metaIndex)
			if !found {
				return newExecutionRuntimeError(
					thread,
					frameIndex,
					instructionPC,
					"attempt to index a %s value",
					target.kind(),
				)
			}
		}
		if _, callable := luaFunctionSlot(method); callable {
			return startMetamethodCall(
				thread,
				frameIndex,
				instructionPC,
				method,
				target,
				key,
				nilSlot,
				2,
				1,
				executionContinuation{
					nextPC: uint32(nextPC),
					code:   code,
				},
			)
		}
		target = method
	}

	return newExecutionRuntimeError(
		thread,
		frameIndex,
		instructionPC,
		"loop in gettable",
	)
}

//go:noinline
func slowTableSet(
	thread *Thread,
	frameIndex int,
	code instruction,
) *Error {
	frame := thread.frames[frameIndex]
	nextPC := int(frame.pc)
	instructionPC := nextPC - 1
	base := int(frame.base)
	var target, key, value slot

	switch code.opcode() {
	case opSetGlobal:
		target = slotFromTable(frame.function.environment)
		key = frame.function.prototype.constants[code.bx()]
		value = thread.values[base+code.a()]
	case opSetTable:
		target = thread.values[base+code.a()]
		key = operandSlot(
			thread.values,
			frame.function.prototype.constants,
			base,
			code.b(),
		)
		value = operandSlot(
			thread.values,
			frame.function.prototype.constants,
			base,
			code.c(),
		)
	default:
		panic("lua: invalid table write opcode")
	}

	for range maxTableMetamethodChain {
		var method slot
		var found bool
		if target.kind() == TableKind {
			table := (*Table)(target.ref)
			normalized, index, arrayKey, hash, status :=
				normalizeTableKey(key)
			if status != tableKeyValid {
				return invalidTableWriteKey(
					thread,
					frameIndex,
					instructionPC,
					status,
				)
			}
			if _, present := table.rawNormalizedSlot(
				normalized,
				index,
				arrayKey,
				hash,
			); present {
				table.rawSetNormalizedSlot(
					normalized,
					index,
					arrayKey,
					hash,
					value,
				)
				return nil
			}
			method, found = metamethodSlot(
				thread,
				target,
				metaNewIndex,
			)
			if !found {
				table.rawSetNormalizedSlot(
					normalized,
					index,
					arrayKey,
					hash,
					value,
				)
				return nil
			}
		} else {
			method, found = metamethodSlot(
				thread,
				target,
				metaNewIndex,
			)
			if !found {
				return newExecutionRuntimeError(
					thread,
					frameIndex,
					instructionPC,
					"attempt to index a %s value",
					target.kind(),
				)
			}
		}
		if _, callable := luaFunctionSlot(method); callable {
			return startMetamethodCall(
				thread,
				frameIndex,
				instructionPC,
				method,
				target,
				key,
				value,
				3,
				0,
				executionContinuation{
					nextPC: uint32(nextPC),
					code:   code,
				},
			)
		}
		target = method
	}

	return newExecutionRuntimeError(
		thread,
		frameIndex,
		instructionPC,
		"loop in settable",
	)
}

func invalidTableWriteKey(
	thread *Thread,
	frameIndex int,
	instructionPC int,
	status tableKeyStatus,
) *Error {
	switch status {
	case tableKeyNil:
		return newExecutionRuntimeError(
			thread,
			frameIndex,
			instructionPC,
			"table index is nil",
		)
	case tableKeyNaN:
		return newExecutionRuntimeError(
			thread,
			frameIndex,
			instructionPC,
			"table index is NaN",
		)
	default:
		panic("lua: valid table key reported as invalid")
	}
}

//go:noinline
func executeNewTable(
	thread *Thread,
	frameIndex int,
	code instruction,
) {
	frame := thread.frames[frameIndex]
	table := newTable(
		thread.owner,
		tableCapacityHint(code.b()),
		tableCapacityHint(code.c()),
	)
	writeSlot(
		&thread.values[int(frame.base)+code.a()],
		slotFromTable(table),
	)
}

func tableCapacityHint(encoded int) int {
	hint := floatingByteToUint64(encoded)
	if hint > maxTableHint {
		return maxTableHint
	}
	return int(hint)
}

//go:noinline
func executeSetList(
	thread *Thread,
	frameIndex int,
	code instruction,
) *Error {
	frame := thread.frames[frameIndex]
	base := int(frame.base)
	tableSlot := thread.values[base+code.a()]
	if tableSlot.kind() != TableKind {
		return newExecutionRuntimeError(
			thread,
			frameIndex,
			int(frame.pc)-1,
			"attempt to index a %s value",
			tableSlot.kind(),
		)
	}

	block := code.c()
	extended := block == 0
	if extended {
		block = int(frame.function.prototype.code[frame.pc])
	}
	first := (block-1)*fieldsPerFlush + 1
	count := code.b()
	if count == 0 {
		count = thread.top - base - code.a() - 1
	}
	if count < 0 {
		panic("lua: SETLIST has a negative dynamic value count")
	}
	if count != 0 {
		last := uint64(first) + uint64(count) - 1
		if last > maxSetListIndex {
			return newResourceError(
				"lua: SETLIST index %d exceeds Lua's supported range",
				last,
			)
		}
	}

	table := (*Table)(tableSlot.ref)
	valueStart := base + code.a() + 1
	table.rawSetList(
		first,
		thread.values[valueStart:valueStart+count],
	)
	if extended {
		thread.frames[frameIndex].pc++
	}

	if code.b() == 0 {
		previousExtent := thread.liveValueExtent()
		thread.top = base + int(frame.function.prototype.registers)
		thread.clearDeadSuffix(previousExtent)
	}
	return nil
}
