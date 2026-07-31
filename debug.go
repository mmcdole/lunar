package lua

const debugTemporaryName = "(*temporary)"

// debugActivation is a cold, immutable view of one logical Lua stack level.
// The activation is copied because a nested call may grow and relocate a
// Thread's frame slice while a debug operation is assembling its result.
type debugActivation struct {
	frame         activation
	physicalIndex int
	status        logicalFrameStatus
}

func (thread *threadObject) debugActivation(
	level int,
) (debugActivation, bool) {
	index, status := thread.logicalFrameIndex(level)
	switch status {
	case logicalFramePhysical:
		return debugActivation{
			frame:         thread.frames[index],
			physicalIndex: index,
			status:        status,
		}, true
	case logicalFrameTail:
		return debugActivation{
			physicalIndex: index,
			status:        status,
		}, true
	default:
		return debugActivation{}, false
	}
}

func (record debugActivation) isTail() bool {
	return record.status == logicalFrameTail
}

func (record debugActivation) prototype() *Prototype {
	if record.status != logicalFramePhysical ||
		record.frame.function == nil {
		return nil
	}
	return record.frame.function.prototype
}

// currentPC translates the executor's published next-instruction PC into the
// instruction currently responsible for a suspended activation.
func (record debugActivation) currentPC() int {
	prototype := record.prototype()
	if prototype == nil {
		return -1
	}
	pc := int(record.frame.pc) - 1
	if pc < 0 || pc >= len(prototype.code) {
		return -1
	}
	return pc
}

func (record debugActivation) currentLine() int {
	prototype := record.prototype()
	pc := record.currentPC()
	if prototype == nil || pc < 0 {
		return -1
	}
	return prototype.lineAt(pc)
}

// functionName recovers lua_getinfo's call-site name. The name belongs to the
// instruction in the physical Lua caller, not to the callee's Prototype.
func (record debugActivation) functionName(
	thread *threadObject,
) (category, name string, found bool) {
	if record.status != logicalFramePhysical ||
		record.frame.function == nil ||
		(record.frame.function.prototype != nil &&
			record.frame.tailCalls != 0) ||
		record.physicalIndex <= 0 {
		return "", "", false
	}

	caller := thread.frames[record.physicalIndex-1]
	prototype := caller.function.prototype
	if prototype == nil {
		return "", "", false
	}
	pc := int(caller.pc) - 1
	if pc < 0 || pc >= len(prototype.code) {
		return "", "", false
	}
	code := prototype.code[pc]
	switch code.opcode() {
	case opCall, opTailCall, opIteratorLoop:
		return prototype.describeOperand(pc, code.a())
	default:
		return "", "", false
	}
}

// local resolves Lua 5.1's nth active local. Named locals come from immutable
// Prototype metadata. Remaining live stack cells are exposed as temporaries,
// matching lua_getlocal/lua_setlocal.
func (record debugActivation) local(
	thread *threadObject,
	ordinal int,
) (name string, stackIndex int, found bool) {
	if record.status != logicalFramePhysical || ordinal <= 0 {
		return "", 0, false
	}
	frame := record.frame
	pc := record.currentPC()
	if prototype := frame.function.prototype; prototype != nil && pc >= 0 {
		if local := prototype.activeLocal(pc, ordinal); local != nil {
			return local.text,
				int(frame.base) + ordinal - 1,
				true
		}
	}

	limit := thread.top
	if next := record.physicalIndex + 1; next < len(thread.frames) {
		limit = int(thread.frames[next].resultBase)
	}
	index := int(frame.base) + ordinal - 1
	if index < int(frame.base) || index >= limit {
		return "", 0, false
	}
	return debugTemporaryName, index, true
}

func (prototype *Prototype) activeLocal(
	pc int,
	ordinal int,
) *internedText {
	if prototype == nil ||
		prototype.debug == nil ||
		ordinal <= 0 {
		return nil
	}
	for index := range prototype.debug.locals {
		local := &prototype.debug.locals[index]
		if int(local.startPC) <= pc && pc < int(local.endPC) {
			ordinal--
			if ordinal == 0 {
				return local.name
			}
		}
	}
	return nil
}

func (prototype *Prototype) debugUpvalueName(index int) string {
	if prototype == nil ||
		index < 0 ||
		index >= int(prototype.upvalues) {
		return ""
	}
	if prototype.debug == nil ||
		index >= len(prototype.debug.upvalues) {
		return "?"
	}
	return prototype.debug.upvalues[index].text
}
