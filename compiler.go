package lua

import (
	"strings"
)

// compileUnit owns data shared by every function in one source compilation.
// Its string table is temporary: sealed prototypes retain only the strings
// they use, not this map or the source buffer from which tokens were sliced.
type compileUnit struct {
	sourceName *internedText
	strings    map[string]*internedText
}

type activeLocal struct {
	name       *internedText
	register   int
	debugIndex int
	blockIndex int
}

type blockState struct {
	localBase     int
	registerFloor int
	firstGoto     int
	firstLabel    int
	captured      bool
}

type upvalueSource uint8

const (
	upvalueFromLocal upvalueSource = iota
	upvalueFromUpvalue
)

type upvalueBinding struct {
	source upvalueSource
	index  int
}

func newCompileUnit(sourceName string) *compileUnit {
	unit := &compileUnit{strings: make(map[string]*internedText)}
	unit.sourceName = unit.internBorrowed(sourceName)
	return unit
}

// internToken establishes the ownership seam between the lexer and retained
// compiler data. Source slices are cloned on the first occurrence. Decoded
// strings already own their backing bytes and can be adopted without another
// copy.
func (unit *compileUnit) internToken(value token) *internedText {
	if value.kind != tokenName && value.kind != tokenString {
		panic("lua: token has no internable text")
	}
	if value.ownedText {
		return unit.internOwned(value.text)
	}
	return unit.internBorrowed(value.text)
}

func (unit *compileUnit) internBorrowed(text string) *internedText {
	if existing := unit.strings[text]; existing != nil {
		return existing
	}
	value := newHashedInternedText(strings.Clone(text), hashString(text))
	unit.strings[value.text] = value
	return value
}

func (unit *compileUnit) internOwned(text string) *internedText {
	if existing := unit.strings[text]; existing != nil {
		return existing
	}
	value := newHashedInternedText(text, hashString(text))
	unit.strings[value.text] = value
	return value
}

// functionState is the mutable, compilation-only counterpart of a sealed
// Prototype. It emits compact instructions and constants directly; no syntax
// tree or boxed intermediate value is retained.
type functionState struct {
	unit            *compileUnit
	parent          *functionState
	builder         prototypeBuilder
	constantIndexes map[slot]int
	locals          []activeLocal
	blocks          []blockState
	loops           []loopState
	upvalues        []upvalueBinding
	registerTop     int
	registerHigh    int
	registerFloor   int
	unresolvedJumps int
	pendingResults  int
	pendingGotos    []pendingGoto
	labels          []definedLabel
	gotoError       *Error
}

func (unit *compileUnit) newFunction(
	parent *functionState,
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
		parent:        parent,
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
		text := stringSlotText(value)
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

func (function *functionState) tableOpcodeForKey(
	generic opcode,
	field opcode,
	key int,
) opcode {
	if !isConstantOperand(key) {
		return generic
	}
	index := constantIndex(key)
	if index >= len(function.builder.constants) ||
		function.builder.constants[index].kind() != StringKind {
		return generic
	}
	return field
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

func (function *functionState) fixLastLine(line uint32) {
	pc := len(function.builder.debug.lines) - 1
	if pc < 0 {
		panic("lua: compiler has no instruction line to fix")
	}
	function.builder.debug.lines[pc] = int(line)
}

func (function *functionState) emitABC(
	operation opcode,
	a, b, c int,
	line uint32,
) int {
	return function.emit(makeABC(operation, a, b, c), line)
}

func (function *functionState) emitDeferredABC(
	operation opcode,
	b, c int,
	line uint32,
) int {
	pc := function.emitABC(operation, noRegister, b, c, line)
	function.pendingResults++
	return pc
}

func (function *functionState) emitDeferredABx(
	operation opcode,
	bx int,
	line uint32,
) int {
	pc := function.emitABx(operation, noRegister, bx, line)
	function.pendingResults++
	return pc
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

func (function *functionState) bindResult(pc, register int) {
	if pc < 0 || pc >= len(function.builder.code) {
		panic("lua: invalid deferred expression result")
	}
	code := function.builder.code[pc]
	switch code.opcode() {
	case opGetUpvalue, opGetGlobal, opGetTable, opGetField, opNewTable,
		opAdd, opSub, opMul, opDiv, opMod, opPow,
		opUnaryMinus, opNot, opLength, opConcat, opClosure:
	default:
		panic("lua: instruction does not have a bindable scalar result")
	}
	if code.a() != noRegister {
		panic("lua: expression result is already bound")
	}
	if function.pendingResults == 0 {
		panic("lua: compiler bound too many expression results")
	}
	function.builder.code[pc] = code.withA(register)
	function.pendingResults--
}

// takeDeferredNot removes an unbound trailing NOT so control-flow consumers
// can branch on its input with inverted polarity. It returns false once any
// later instruction makes removing the result unsafe.
func (function *functionState) takeDeferredNot(
	value *compiledExpression,
) (int, bool) {
	if value.kind != expressionDeferred ||
		value.info != function.currentPC()-1 {
		return 0, false
	}
	pc := value.info
	code := function.builder.code[pc]
	if code.opcode() != opNot || code.a() != noRegister {
		return 0, false
	}
	if function.pendingResults == 0 ||
		len(function.builder.debug.lines) != len(function.builder.code) {
		panic("lua: deferred NOT bookkeeping is inconsistent")
	}
	function.builder.code = function.builder.code[:pc]
	function.builder.debug.lines =
		function.builder.debug.lines[:pc]
	function.pendingResults--
	value.kind = expressionInvalid
	return code.b(), true
}

func (function *functionState) enterBlock() {
	if function.registerTop != function.registerFloor {
		panic("lua: compiler block entered with live temporaries")
	}
	function.blocks = append(function.blocks, blockState{
		localBase:     len(function.locals),
		registerFloor: function.registerFloor,
		firstGoto:     len(function.pendingGotos),
		firstLabel:    len(function.labels),
	})
}

func (function *functionState) enterFunctionBlock() {
	if len(function.blocks) != 0 || len(function.locals) != 0 {
		panic("lua: function scope was not the first compiler block")
	}
	if function.registerTop != function.registerFloor {
		panic("lua: function scope has inconsistent parameter registers")
	}
	function.blocks = append(function.blocks, blockState{
		registerFloor: 0,
		firstGoto:     len(function.pendingGotos),
		firstLabel:    len(function.labels),
	})
	function.registerFloor = 0
}

func (function *functionState) leaveBlock(line uint32) {
	index := len(function.blocks) - 1
	if index < 0 {
		panic("lua: compiler block stack underflow")
	}
	block := function.blocks[index]
	endPC := function.currentPC()
	for local := block.localBase; local < len(function.locals); local++ {
		debugIndex := function.locals[local].debugIndex
		function.builder.debug.locals[debugIndex].endPC = endPC
	}
	if index != 0 {
		function.emitCloseForExit(index, line)
		function.moveGotosOut(index)
	}
	function.labels = function.labels[:block.firstLabel]
	function.blocks = function.blocks[:index]
	function.locals = function.locals[:block.localBase]
	function.registerFloor = block.registerFloor
	function.releaseRegisters(block.registerFloor)
}

func (function *functionState) activateLocals(
	names []*internedText,
	base int,
) {
	if len(function.blocks) == 0 {
		panic("lua: local activated outside a compiler block")
	}
	if base < function.registerFloor ||
		base+len(names) != function.registerTop {
		panic("lua: local activation does not cover the reserved register suffix")
	}
	startPC := function.currentPC()
	blockIndex := len(function.blocks) - 1
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
				blockIndex: blockIndex,
			},
		)
	}
	function.registerFloor = base + len(names)
	function.registerTop = function.registerFloor
}

func (function *functionState) localRegister(name *internedText) (int, bool) {
	index, ok := function.localIndex(name)
	if !ok {
		return 0, false
	}
	return function.locals[index].register, true
}

func (function *functionState) localIndex(name *internedText) (int, bool) {
	for index := len(function.locals) - 1; index >= 0; index-- {
		local := function.locals[index]
		if local.name == name {
			return index, true
		}
	}
	return 0, false
}

// emitCloseForExit closes every captured local owned by the blocks from
// firstExited outward. One CLOSE at the lowest captured register covers the
// whole suffix. RETURN and TAILCALL do not use this helper: frame teardown
// closes the complete activation.
func (function *functionState) emitCloseForExit(
	firstExited int,
	line uint32,
) {
	if firstExited < 0 || firstExited > len(function.blocks) {
		panic("lua: invalid compiler block exit")
	}
	closeBase := noRegister
	for index := firstExited; index < len(function.blocks); index++ {
		block := function.blocks[index]
		if block.captured && block.registerFloor < closeBase {
			closeBase = block.registerFloor
		}
	}
	if closeBase != noRegister {
		function.emitABC(opClose, closeBase, 0, 0, line)
	}
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

// jumpList is an allocation-free chain of pending JMP instructions. Zero is
// empty, so expression descriptors need no sentinel initialization. Non-empty
// values encode the head pc plus one. Each unresolved instruction's sBx field
// points to the next pc; -1 terminates the chain.
type jumpList int

const emptyJumpList jumpList = 0

func jumpAt(pc int) jumpList {
	return jumpList(pc + 1)
}

func (list jumpList) pc() int {
	if list == emptyJumpList {
		panic("lua: empty jump list has no program counter")
	}
	return int(list) - 1
}

func (function *functionState) emitJump(line uint32) jumpList {
	pc := function.emitAsBx(opJump, 0, -1, line)
	function.unresolvedJumps++
	return jumpAt(pc)
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
	if syntaxError := function.setJump(tail, second.pc()); syntaxError != nil {
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

func (function *functionState) invertComparison(jump jumpList) {
	pc := jump.pc()
	if pc == 0 {
		panic("lua: comparison jump has no controlling instruction")
	}
	control := function.builder.code[pc-1]
	switch control.opcode() {
	case opEqual, opLessThan, opLessEqual:
	default:
		panic("lua: jump is not controlled by a comparison")
	}
	function.builder.code[pc-1] = control.withA(1 - control.a())
}

func (function *functionState) patchTestDestination(
	jump jumpList,
	register int,
) bool {
	pc := jump.pc()
	if pc == 0 {
		return false
	}
	controlPC := pc - 1
	control := function.builder.code[controlPC]
	if control.opcode() != opTestSet {
		return false
	}
	if register != noRegister && register != control.b() {
		function.builder.code[controlPC] = control.withA(register)
	} else {
		function.builder.code[controlPC] = makeABC(
			opTest,
			control.b(),
			0,
			control.c(),
		)
	}
	return true
}

func (function *functionState) jumpListNeedsValue(list jumpList) bool {
	for list != emptyJumpList {
		pc := list.pc()
		if pc == 0 ||
			function.builder.code[pc-1].opcode() != opTestSet {
			return true
		}
		list = function.nextJump(list)
	}
	return false
}

func (function *functionState) patchConditionalJumps(
	list jumpList,
	valueTarget int,
	register int,
	defaultTarget int,
) *Error {
	for list != emptyJumpList {
		next := function.nextJump(list)
		target := defaultTarget
		if function.patchTestDestination(list, register) {
			target = valueTarget
		}
		if syntaxError := function.setJump(list, target); syntaxError != nil {
			return syntaxError
		}
		function.unresolvedJumps--
		list = next
	}
	return nil
}

func (function *functionState) patchConditionToHere(
	list jumpList,
) *Error {
	return function.patchCondition(list, function.currentPC())
}

func (function *functionState) patchCondition(
	list jumpList,
	target int,
) *Error {
	return function.patchConditionalJumps(
		list,
		target,
		noRegister,
		target,
	)
}

func (function *functionState) discardJumpValues(list jumpList) {
	for list != emptyJumpList {
		function.patchTestDestination(list, noRegister)
		list = function.nextJump(list)
	}
}

func (function *functionState) nextJump(list jumpList) jumpList {
	pc := list.pc()
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
	return jumpAt(next)
}

func (function *functionState) setJump(
	list jumpList,
	target int,
) *Error {
	return function.setControlTarget(list.pc(), target)
}

func (function *functionState) setControlTarget(
	pc int,
	target int,
) *Error {
	if pc < 0 || pc >= len(function.builder.code) {
		panic("lua: invalid control instruction")
	}
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
	if len(function.blocks) != 0 || len(function.locals) != 0 {
		panic("lua: compiler sealed a function with an active lexical scope")
	}
	if len(function.loops) != 0 {
		panic("lua: compiler sealed a function with an active loop")
	}
	if function.gotoError != nil {
		return nil, function.gotoError
	}
	if len(function.pendingGotos) != 0 {
		pending := function.pendingGotos[0]
		return nil, newSourceSyntaxError(
			function.unit.sourceName.text,
			pending.line,
			"no visible label '%s' for <goto>",
			pending.name.text,
		)
	}
	if function.unresolvedJumps != 0 {
		return nil, newSourceSyntaxError(
			function.unit.sourceName.text,
			lastLine,
			"function has unresolved control flow",
		)
	}
	if function.pendingResults != 0 {
		return nil, newSourceSyntaxError(
			function.unit.sourceName.text,
			lastLine,
			"function has unresolved expression results",
		)
	}
	function.builder.lastLine = int(lastLine)
	function.builder.registers = function.registerHigh
	function.builder.upvalues = len(function.upvalues)
	return function.builder.seal()
}
