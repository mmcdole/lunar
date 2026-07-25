package lua

import (
	"strings"
)

// compileUnit owns data shared by every function in one source compilation.
// Its string table is temporary: sealed prototypes retain only the strings
// they use, not this map or the source buffer from which tokens were sliced.
type compileUnit struct {
	sourceName *luaString
	strings    map[string]*luaString
}

type activeLocal struct {
	name       *luaString
	register   int
	debugIndex int
}

type blockState struct {
	localBase     int
	registerFloor int
}

func newCompileUnit(sourceName string) *compileUnit {
	unit := &compileUnit{strings: make(map[string]*luaString)}
	unit.sourceName = unit.internBorrowed(sourceName)
	return unit
}

// internToken establishes the ownership seam between the lexer and retained
// compiler data. Source slices are cloned on the first occurrence. Decoded
// strings already own their backing bytes and can be adopted without another
// copy.
func (unit *compileUnit) internToken(value token) *luaString {
	if value.kind != tokenName && value.kind != tokenString {
		panic("lua: token has no internable text")
	}
	if value.ownedText {
		return unit.internOwned(value.text)
	}
	return unit.internBorrowed(value.text)
}

func (unit *compileUnit) internBorrowed(text string) *luaString {
	if existing := unit.strings[text]; existing != nil {
		return existing
	}
	value := newHashedString(strings.Clone(text), hashString(text))
	unit.strings[value.text] = value
	return value
}

func (unit *compileUnit) internOwned(text string) *luaString {
	if existing := unit.strings[text]; existing != nil {
		return existing
	}
	value := newHashedString(text, hashString(text))
	unit.strings[value.text] = value
	return value
}

// functionState is the mutable, compilation-only counterpart of a sealed
// Prototype. It emits compact instructions and constants directly; no syntax
// tree or boxed intermediate value is retained.
type functionState struct {
	unit            *compileUnit
	builder         prototypeBuilder
	constantIndexes map[slot]int
	locals          []activeLocal
	blocks          []blockState
	registerTop     int
	registerHigh    int
	registerFloor   int
	unresolvedJumps int
}

func (unit *compileUnit) newFunction(
	line uint32,
	parameters int,
	varargFlags int,
) (*functionState, *Error) {
	if parameters < 0 || parameters > maxLuaRegisters {
		return nil, newSourceSyntaxError(
			unit.sourceName.text,
			line,
			"function has %d parameters; maximum is %d",
			parameters,
			maxLuaRegisters,
		)
	}
	if varargFlags < 0 || varargFlags&^varargMask != 0 {
		return nil, newSourceSyntaxError(
			unit.sourceName.text,
			line,
			"invalid vararg flags %#x",
			varargFlags,
		)
	}
	if varargFlags&varargHasArg != 0 &&
		varargFlags&varargIsVararg == 0 {
		return nil, newSourceSyntaxError(
			unit.sourceName.text,
			line,
			"legacy arg table requires a vararg function",
		)
	}
	if varargFlags&varargNeedsArg != 0 &&
		varargFlags&(varargHasArg|varargIsVararg) !=
			varargHasArg|varargIsVararg {
		return nil, newSourceSyntaxError(
			unit.sourceName.text,
			line,
			"required arg table is not declared",
		)
	}

	registers := parameters
	if varargFlags&varargHasArg != 0 {
		registers++
	}
	if registers > maxLuaRegisters {
		return nil, newSourceSyntaxError(
			unit.sourceName.text,
			line,
			"parameters and legacy arg table exceed %d registers",
			maxLuaRegisters,
		)
	}
	high := registers
	if high < 2 {
		high = 2
	}
	return &functionState{
		unit:          unit,
		registerTop:   registers,
		registerHigh:  high,
		registerFloor: registers,
		builder: prototypeBuilder{
			sourceName:  unit.sourceName,
			lineDefined: int(line),
			lastLine:    int(line),
			parameters:  parameters,
			varargFlags: varargFlags,
			debug:       &prototypeDebugBuilder{},
		},
	}, nil
}

func (function *functionState) constant(
	value slot,
	line uint32,
) (int, *Error) {
	if !validPrototypeConstant(value) {
		return 0, newSourceSyntaxError(
			function.unit.sourceName.text,
			line,
			"constant is not a Lua scalar",
		)
	}
	if value.kind() == StringKind {
		text := (*luaString)(value.ref).text
		value = prototypeStringSlot(function.unit.internBorrowed(text))
	}
	if index, exists := function.constantIndexes[value]; exists {
		return index, nil
	}
	if len(function.builder.constants) == maxOperandBx+1 {
		return 0, newSourceSyntaxError(
			function.unit.sourceName.text,
			line,
			"function has too many constants",
		)
	}
	if function.constantIndexes == nil {
		function.constantIndexes = make(map[slot]int)
	}
	index := len(function.builder.constants)
	function.builder.constants = append(function.builder.constants, value)
	function.constantIndexes[value] = index
	return index, nil
}

func (function *functionState) emit(
	code instruction,
	line uint32,
) int {
	pc := len(function.builder.code)
	function.builder.code = append(function.builder.code, code)
	function.builder.debug.lines = append(
		function.builder.debug.lines,
		int(line),
	)
	return pc
}

func (function *functionState) emitABC(
	operation opcode,
	a, b, c int,
	line uint32,
) int {
	return function.emit(makeABC(operation, a, b, c), line)
}

func (function *functionState) emitABx(
	operation opcode,
	a, bx int,
	line uint32,
) int {
	return function.emit(makeABx(operation, a, bx), line)
}

func (function *functionState) emitAsBx(
	operation opcode,
	a, sbx int,
	line uint32,
) int {
	return function.emit(makeAsBx(operation, a, sbx), line)
}

func (function *functionState) currentPC() int {
	return len(function.builder.code)
}

func (function *functionState) enterBlock() {
	function.blocks = append(function.blocks, blockState{
		localBase:     len(function.locals),
		registerFloor: function.registerFloor,
	})
}

func (function *functionState) leaveBlock() {
	index := len(function.blocks) - 1
	if index < 0 {
		panic("lua: compiler block stack underflow")
	}
	block := function.blocks[index]
	function.blocks = function.blocks[:index]
	endPC := function.currentPC()
	for local := block.localBase; local < len(function.locals); local++ {
		debugIndex := function.locals[local].debugIndex
		function.builder.debug.locals[debugIndex].endPC = endPC
	}
	function.locals = function.locals[:block.localBase]
	function.registerFloor = block.registerFloor
	function.releaseRegisters(block.registerFloor)
}

func (function *functionState) activateLocals(
	names []*luaString,
	base int,
) {
	startPC := function.currentPC()
	for index, name := range names {
		debugIndex := len(function.builder.debug.locals)
		function.builder.debug.locals = append(
			function.builder.debug.locals,
			prototypeLocalBuilder{
				name:    name,
				startPC: startPC,
				endPC:   startPC,
			},
		)
		function.locals = append(
			function.locals,
			activeLocal{
				name:       name,
				register:   base + index,
				debugIndex: debugIndex,
			},
		)
	}
	function.registerFloor = base + len(names)
	function.registerTop = function.registerFloor
}

func (function *functionState) localRegister(name *luaString) (int, bool) {
	for index := len(function.locals) - 1; index >= 0; index-- {
		local := function.locals[index]
		if local.name == name {
			return local.register, true
		}
	}
	return 0, false
}

func (function *functionState) isVararg() bool {
	return function.builder.varargFlags&varargIsVararg != 0
}

func (function *functionState) reserveRegisters(
	count int,
	line uint32,
) (int, *Error) {
	if count < 0 {
		panic("lua: negative register reservation")
	}
	base := function.registerTop
	if count > maxLuaRegisters-base {
		return 0, newSourceSyntaxError(
			function.unit.sourceName.text,
			line,
			"function requires more than %d registers",
			maxLuaRegisters,
		)
	}
	function.registerTop += count
	if function.registerTop > function.registerHigh {
		function.registerHigh = function.registerTop
	}
	return base, nil
}

func (function *functionState) releaseRegisters(mark int) {
	if mark < function.registerFloor || mark > function.registerTop {
		panic("lua: invalid register release")
	}
	function.registerTop = mark
}

// jumpList is an allocation-free chain of pending JMP instructions. Each
// unresolved instruction's sBx field points to the next pc; -1 terminates the
// chain. Patching overwrites those links with their final destinations.
type jumpList int

const emptyJumpList jumpList = -1

func (function *functionState) emitJump(line uint32) jumpList {
	pc := function.emitAsBx(opJump, 0, -1, line)
	function.unresolvedJumps++
	return jumpList(pc)
}

func (function *functionState) joinJumps(
	first jumpList,
	second jumpList,
) (jumpList, *Error) {
	if first == emptyJumpList {
		return second, nil
	}
	if second == emptyJumpList {
		return first, nil
	}

	tail := first
	for {
		next := function.nextJump(tail)
		if next == emptyJumpList {
			break
		}
		tail = next
	}
	if syntaxError := function.setJump(tail, int(second)); syntaxError != nil {
		return emptyJumpList, syntaxError
	}
	return first, nil
}

func (function *functionState) patchJumps(
	list jumpList,
	target int,
) *Error {
	if target < 0 || target > len(function.builder.code) {
		panic("lua: invalid jump patch target")
	}
	for list != emptyJumpList {
		next := function.nextJump(list)
		if syntaxError := function.setJump(list, target); syntaxError != nil {
			return syntaxError
		}
		function.unresolvedJumps--
		list = next
	}
	return nil
}

func (function *functionState) patchJumpsToHere(list jumpList) *Error {
	return function.patchJumps(list, function.currentPC())
}

func (function *functionState) nextJump(list jumpList) jumpList {
	pc := int(list)
	if pc < 0 ||
		pc >= len(function.builder.code) ||
		function.builder.code[pc].opcode() != opJump {
		panic("lua: malformed pending jump list")
	}
	offset := function.builder.code[pc].sbx()
	if offset == -1 {
		return emptyJumpList
	}
	next := pc + 1 + offset
	if next < 0 ||
		next >= len(function.builder.code) ||
		function.builder.code[next].opcode() != opJump {
		panic("lua: malformed pending jump link")
	}
	return jumpList(next)
}

func (function *functionState) setJump(
	list jumpList,
	target int,
) *Error {
	pc := int(list)
	offset := target - (pc + 1)
	if offset < -maxOperandsBx || offset > maxOperandsBx {
		line := uint32(function.builder.debug.lines[pc])
		return newSourceSyntaxError(
			function.unit.sourceName.text,
			line,
			"control structure is too long",
		)
	}
	function.builder.code[pc] = function.builder.code[pc].withSBx(offset)
	return nil
}

func (function *functionState) finish(
	lastLine uint32,
) (*Prototype, *Error) {
	if function.unresolvedJumps != 0 {
		return nil, newSourceSyntaxError(
			function.unit.sourceName.text,
			lastLine,
			"function has unresolved control flow",
		)
	}
	function.builder.lastLine = int(lastLine)
	function.builder.registers = function.registerHigh
	return function.builder.seal()
}
