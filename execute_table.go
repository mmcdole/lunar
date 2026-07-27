package lua

const maxTableMetamethodChain = 100

const tableInstructionHandled = instruction(opTableHandled)

func rawTableMissOpcode(operation opcode) opcode {
	switch operation {
	case opGetGlobal:
		return opGetGlobalMiss
	case opGetTable:
		return opGetTableMiss
	case opSetGlobal:
		return opSetGlobalMiss
	case opSetTable:
		return opSetTableMiss
	case opSelf:
		return opSelfMiss
	case opGetField:
		return opGetFieldMiss
	case opSetField:
		return opSetFieldMiss
	case opSelfField:
		return opSelfFieldMiss
	default:
		panic("lua: invalid raw table opcode")
	}
}

func tableSourceOpcode(operation opcode) (opcode, bool) {
	switch operation {
	case opGetGlobalMiss:
		return opGetGlobal, true
	case opGetTableMiss:
		return opGetTable, true
	case opSetGlobalMiss:
		return opSetGlobal, true
	case opSetTableMiss:
		return opSetTable, true
	case opSelfMiss:
		return opSelf, true
	case opGetFieldMiss:
		return opGetField, true
	case opSetFieldMiss:
		return opSetField, true
	case opSelfFieldMiss:
		return opSelfField, true
	default:
		return operation, false
	}
}

// executeRawTableGet completes reads that cannot invoke Lua or construct an
// error. It is called directly by the instruction loop so common reads avoid
// a return through the cold semantic driver.
//
//go:noinline
func executeRawTableGet(
	thread *threadObject,
	code instruction,
) instruction {
	frame := thread.frames[len(thread.frames)-1]
	base := int(frame.base)
	var target, key slot

	switch code.opcode() {
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
		// SELF publishes its receiver before reading RK(C), including when
		// C aliases A+1.
		writeSlot(&thread.values[base+code.a()+1], target)
		key = operandSlot(
			thread.values,
			frame.function.prototype.constants,
			base,
			code.c(),
		)
	default:
		panic("lua: invalid raw table read opcode")
	}

	if !target.isTable() {
		return code
	}
	table := (*tableObject)(target.ref)
	result, found := table.rawSlot(key)
	if !found {
		if table.metatable == nil ||
			table.metatable.absentMetamethods&metaIndex.bit() != 0 {
			writeSlot(&thread.values[base+code.a()], nilSlot)
			return tableInstructionHandled
		}
		return code.withOpcode(rawTableMissOpcode(code.opcode()))
	}
	writeSlot(&thread.values[base+code.a()], result)
	return tableInstructionHandled
}

// executeRawStringTableGet is the compiler-proven constant-string counterpart
// to executeRawTableGet. The constant already carries its trusted hash, so
// field, method, and global access never enter dynamic key normalization.
//
//go:noinline
func executeRawStringTableGet(
	thread *threadObject,
	code instruction,
) instruction {
	frame := thread.frames[len(thread.frames)-1]
	base := int(frame.base)
	var target, key slot

	switch code.opcode() {
	case opGetGlobal:
		target = slotFromTableObject(frame.function.environment)
		key = frame.function.prototype.constants[code.bx()]
	case opGetField:
		target = thread.values[base+code.b()]
		key = frame.function.prototype.constants[constantIndex(code.c())]
	case opSelfField:
		target = thread.values[base+code.b()]
		writeSlot(&thread.values[base+code.a()+1], target)
		key = frame.function.prototype.constants[constantIndex(code.c())]
	default:
		panic("lua: invalid constant-string table read opcode")
	}

	if !target.isTable() {
		return code
	}
	table := (*tableObject)(target.ref)
	result, found := table.rawStringKeySlot(
		key,
		uint32(stringSlotHash(key)),
	)
	if !found {
		if table.metatable == nil ||
			table.metatable.absentMetamethods&metaIndex.bit() != 0 {
			writeSlot(&thread.values[base+code.a()], nilSlot)
			return tableInstructionHandled
		}
		return code.withOpcode(rawTableMissOpcode(code.opcode()))
	}
	writeSlot(&thread.values[base+code.a()], result)
	return tableInstructionHandled
}

// executeRawTableSet completes writes that cannot invoke Lua or construct an
// error.
//
//go:noinline
func executeRawTableSet(
	thread *threadObject,
	code instruction,
) instruction {
	frame := thread.frames[len(thread.frames)-1]
	base := int(frame.base)
	var target, key, value slot

	switch code.opcode() {
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
		panic("lua: invalid raw table write opcode")
	}

	if !target.isTable() {
		return code
	}
	table := (*tableObject)(target.ref)
	normalized, index, arrayKey, hash, status :=
		normalizeTableKey(key)
	if status != tableKeyValid {
		return code
	}
	_, location, found := table.resolveNormalizedSlot(
		normalized,
		index,
		arrayKey,
		hash,
	)
	if !found {
		if table.metatable == nil ||
			table.metatable.absentMetamethods&metaNewIndex.bit() != 0 {
			table.rawSetNormalizedSlot(
				normalized,
				index,
				arrayKey,
				hash,
				value,
			)
			return tableInstructionHandled
		}
		return code.withOpcode(rawTableMissOpcode(code.opcode()))
	}
	table.replaceResolvedSlot(location, value)
	return tableInstructionHandled
}

// executeRawStringTableSet is the constant-string mutation path paired with
// executeRawStringTableGet.
//
//go:noinline
func executeRawStringTableSet(
	thread *threadObject,
	code instruction,
) instruction {
	frame := thread.frames[len(thread.frames)-1]
	base := int(frame.base)
	var target, key, value slot

	switch code.opcode() {
	case opSetGlobal:
		target = slotFromTableObject(frame.function.environment)
		key = frame.function.prototype.constants[code.bx()]
		value = thread.values[base+code.a()]
	case opSetField:
		target = thread.values[base+code.a()]
		key = frame.function.prototype.constants[constantIndex(code.b())]
		value = operandSlot(
			thread.values,
			frame.function.prototype.constants,
			base,
			code.c(),
		)
	default:
		panic("lua: invalid constant-string table write opcode")
	}

	if !target.isTable() {
		return code
	}
	table := (*tableObject)(target.ref)
	hash := uint32(stringSlotHash(key))
	_, location, found := table.resolveStringKeySlot(
		key,
		hash,
	)
	if !found {
		if table.metatable == nil ||
			table.metatable.absentMetamethods&metaNewIndex.bit() != 0 {
			table.rawSetNormalizedSlot(
				key,
				0,
				false,
				hash,
				value,
			)
			return tableInstructionHandled
		}
		return code.withOpcode(rawTableMissOpcode(code.opcode()))
	}
	table.replaceResolvedSlot(location, value)
	return tableInstructionHandled
}

//go:noinline
func slowTableGet(
	thread *threadObject,
	frameIndex int,
	code instruction,
) *Error {
	frame := thread.frames[frameIndex]
	nextPC := int(frame.pc)
	instructionPC := nextPC - 1
	base := int(frame.base)
	var target, key slot
	var keyHash uint32
	stringKey := false

	operation, skipInitialRaw := tableSourceOpcode(code.opcode())
	switch operation {
	case opGetGlobal:
		target = slotFromTableObject(frame.function.environment)
		key = frame.function.prototype.constants[code.bx()]
		keyHash = uint32(stringSlotHash(key))
		stringKey = true
	case opGetTable:
		target = thread.values[base+code.b()]
		key = operandSlot(
			thread.values,
			frame.function.prototype.constants,
			base,
			code.c(),
		)
	case opGetField:
		target = thread.values[base+code.b()]
		key = frame.function.prototype.constants[constantIndex(code.c())]
		keyHash = uint32(stringSlotHash(key))
		stringKey = true
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
	case opSelfField:
		target = thread.values[base+code.b()]
		writeSlot(&thread.values[base+code.a()+1], target)
		key = frame.function.prototype.constants[constantIndex(code.c())]
		keyHash = uint32(stringSlotHash(key))
		stringKey = true
	default:
		panic("lua: invalid table read opcode")
	}

	firstTarget := true
	for range maxTableMetamethodChain {
		var method slot
		var found bool
		if target.isTable() {
			table := (*tableObject)(target.ref)
			if !skipInitialRaw || !firstTarget {
				var result slot
				if stringKey {
					result, found = table.rawStringKeySlot(
						key,
						keyHash,
					)
				} else {
					result, found = table.rawSlot(key)
				}
				if found {
					writeSlot(&thread.values[base+code.a()], result)
					return nil
				}
			}
			method, found = metamethodSlot(thread, target, metaIndex)
			if !found {
				writeSlot(&thread.values[base+code.a()], nilSlot)
				return nil
			}
		} else {
			method, found = metamethodSlot(thread, target, metaIndex)
			if !found {
				register := -1
				if firstTarget {
					switch operation {
					case opGetTable, opGetField,
						opSelf, opSelfField:
						register = code.b()
					}
				}
				return newExecutionTypeError(
					thread,
					frameIndex,
					instructionPC,
					register,
					"index",
					target.kind(),
				)
			}
		}
		if _, callable := functionSlot(method); callable {
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
		firstTarget = false
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
	thread *threadObject,
	frameIndex int,
	code instruction,
) *Error {
	frame := thread.frames[frameIndex]
	nextPC := int(frame.pc)
	instructionPC := nextPC - 1
	base := int(frame.base)
	var target, key, value slot
	var keyHash uint32
	stringKey := false

	operation, skipInitialRaw := tableSourceOpcode(code.opcode())
	switch operation {
	case opSetGlobal:
		target = slotFromTableObject(frame.function.environment)
		key = frame.function.prototype.constants[code.bx()]
		value = thread.values[base+code.a()]
		keyHash = uint32(stringSlotHash(key))
		stringKey = true
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
	case opSetField:
		target = thread.values[base+code.a()]
		key = frame.function.prototype.constants[constantIndex(code.b())]
		value = operandSlot(
			thread.values,
			frame.function.prototype.constants,
			base,
			code.c(),
		)
		keyHash = uint32(stringSlotHash(key))
		stringKey = true
	default:
		panic("lua: invalid table write opcode")
	}

	firstTarget := true
	for range maxTableMetamethodChain {
		var method slot
		var found bool
		if target.isTable() {
			table := (*tableObject)(target.ref)
			normalized := key
			index := 0
			arrayKey := false
			hash := keyHash
			if !stringKey {
				var status tableKeyStatus
				normalized, index, arrayKey, hash, status =
					normalizeTableKey(key)
				if status != tableKeyValid {
					return invalidTableWriteKey(
						thread,
						frameIndex,
						instructionPC,
						status,
					)
				}
			}
			if !skipInitialRaw || !firstTarget {
				var location tableLocation
				var present bool
				if stringKey {
					_, location, present =
						table.resolveStringKeySlot(normalized, hash)
				} else {
					_, location, present =
						table.resolveNormalizedSlot(
							normalized,
							index,
							arrayKey,
							hash,
						)
				}
				if present {
					table.replaceResolvedSlot(location, value)
					return nil
				}
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
				register := -1
				if firstTarget &&
					(operation == opSetTable ||
						operation == opSetField) {
					register = code.a()
				}
				return newExecutionTypeError(
					thread,
					frameIndex,
					instructionPC,
					register,
					"index",
					target.kind(),
				)
			}
		}
		if _, callable := functionSlot(method); callable {
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
		firstTarget = false
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
	thread *threadObject,
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
	thread *threadObject,
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
		slotFromTableObject(table),
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
	thread *threadObject,
	frameIndex int,
	code instruction,
) *Error {
	frame := thread.frames[frameIndex]
	base := int(frame.base)
	tableSlot := thread.values[base+code.a()]
	if !tableSlot.isTable() {
		return newExecutionTypeError(
			thread,
			frameIndex,
			int(frame.pc)-1,
			code.a(),
			"index",
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
				"SETLIST index %d exceeds Lua's supported range",
				last,
			)
		}
	}

	table := (*tableObject)(tableSlot.ref)
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
