package lua

const (
	varargHasArg   = 1
	varargIsVararg = 2
	varargNeedsArg = 4
	varargMask     = varargHasArg | varargIsVararg | varargNeedsArg

	maxLuaRegisters = 250
	maxLuaUpvalues  = 60

	prototypeSealedBit    = uint32(1) << 31
	prototypeRetainedMask = prototypeSealedBit - 1
)

type localInfo struct {
	name    *internedText
	startPC uint32
	endPC   uint32
}

type prototypeDebug struct {
	lines    []uint32
	locals   []localInfo
	upvalues []*internedText
}

// Prototype is immutable, verified Lua executable metadata.
//
// A Prototype is independent of any State and may be shared safely among
// States. Its executable arrays and constants are private. Strings retained by
// constants are immutable and do not refer back to a State.
type Prototype struct {
	sourceName  *internedText
	lineDefined uint32
	lastLine    uint32
	code        []instruction
	constants   []slot
	children    []*Prototype
	debug       *prototypeDebug
	parameters  uint8
	registers   uint8
	upvalues    uint8
	varargFlags uint8
	metadata    uint32
}

func (prototype *Prototype) isSealed() bool {
	return prototype != nil &&
		prototype.metadata&prototypeSealedBit != 0
}

func (prototype *Prototype) markSealed(retainedBytes uint64) {
	if prototype == nil || prototype.isSealed() {
		panic("lua: invalid prototype seal")
	}
	if retainedBytes > uint64(prototypeRetainedMask) {
		retainedBytes = uint64(prototypeRetainedMask)
	}
	prototype.metadata = prototypeSealedBit | uint32(retainedBytes)
}

func (prototype *Prototype) schedulingBytes() uint64 {
	if !prototype.isSealed() {
		return 0
	}
	return uint64(prototype.metadata & prototypeRetainedMask)
}

// SourceName returns the source identifier recorded by the compiler or
// loader.
func (prototype *Prototype) SourceName() string {
	if prototype == nil {
		return ""
	}
	return prototypeStringText(prototype.sourceName)
}

// LineRange returns the inclusive source line range for prototype.
func (prototype *Prototype) LineRange() (first, last int) {
	if prototype == nil {
		return 0, 0
	}
	return int(prototype.lineDefined), int(prototype.lastLine)
}

// lineAt returns the source line associated with an instruction. It returns
// zero when debug line information was omitted or pc is out of range.
func (prototype *Prototype) lineAt(pc int) int {
	if prototype == nil ||
		prototype.debug == nil ||
		pc < 0 ||
		pc >= len(prototype.debug.lines) {
		return 0
	}
	return int(prototype.debug.lines[pc])
}

// describeOperand recovers the source-level name of a register on the cold
// runtime-error path. Locals take precedence. Otherwise, verified bytecode is
// traced forward to the last producer that can name the value.
func (prototype *Prototype) describeOperand(
	pc int,
	register int,
) (category string, name string, found bool) {
	if prototype == nil ||
		pc < 0 ||
		pc >= len(prototype.code) ||
		register < 0 ||
		register >= int(prototype.registers) {
		return "", "", false
	}
	if local := prototype.localAt(pc, register); local != nil {
		return "local", diagnosticName(local.text), true
	}

	producer, found := prototype.registerProducer(pc, register)
	if !found {
		return "", "", false
	}
	switch producer.opcode() {
	case opGetGlobal:
		value := prototype.constants[producer.bx()]
		return "global",
			diagnosticName(stringSlotText(value)),
			true
	case opGetUpvalue:
		index := producer.b()
		if prototype.debug != nil &&
			index < len(prototype.debug.upvalues) {
			return "upvalue",
				diagnosticName(prototype.debug.upvalues[index].text),
				true
		}
		return "upvalue", "?", true
	case opGetTable, opGetField:
		return "field", prototype.constantOperandName(producer.c()), true
	case opSelf, opSelfField:
		return "method", prototype.constantOperandName(producer.c()), true
	case opMove:
		if producer.b() < producer.a() {
			return prototype.describeOperand(pc, producer.b())
		}
	}
	return "", "", false
}

func (prototype *Prototype) localAt(
	pc int,
	register int,
) *internedText {
	if prototype.debug == nil {
		return nil
	}
	ordinal := register
	for index := range prototype.debug.locals {
		local := &prototype.debug.locals[index]
		if int(local.startPC) <= pc && pc < int(local.endPC) {
			if ordinal == 0 {
				return local.name
			}
			ordinal--
		}
	}
	return nil
}

func (prototype *Prototype) constantOperandName(operand int) string {
	if !isConstantOperand(operand) {
		return "?"
	}
	value := prototype.constants[constantIndex(operand)]
	if value.kind() != StringKind {
		return "?"
	}
	return diagnosticName(stringSlotText(value))
}

func diagnosticName(name string) string {
	for index := 0; index < len(name); index++ {
		if name[index] == 0 {
			return name[:index]
		}
	}
	return name
}

func (prototype *Prototype) registerProducer(
	beforePC int,
	register int,
) (instruction, bool) {
	producerPC := -1
	for pc := 0; pc < beforePC; pc++ {
		code := prototype.code[pc]
		operation := code.opcode()
		if instructionWritesRegister(code, register) {
			producerPC = pc
		}

		switch operation {
		case opForLoop, opForPrep, opJump:
			destination := pc + 1 + code.sbx()
			if destination > pc && destination <= beforePC {
				pc += code.sbx()
			}
		case opSetList:
			if code.c() == 0 {
				pc++
			}
		case opClosure:
			pc += int(prototype.children[code.bx()].upvalues)
		}
	}
	if producerPC < 0 {
		return 0, false
	}
	return prototype.code[producerPC], true
}

func instructionWritesRegister(code instruction, register int) bool {
	if instructionWritesA(code.opcode()) && code.a() == register {
		return true
	}
	switch code.opcode() {
	case opLoadNil:
		return code.a() <= register && register <= code.b()
	case opSelf, opSelfField:
		return register == code.a()+1
	case opCall, opTailCall:
		return register >= code.a()
	case opIteratorLoop:
		return register >= code.a()+2
	default:
		return false
	}
}

func instructionWritesA(operation opcode) bool {
	switch operation {
	case opMove,
		opLoadK,
		opLoadBool,
		opGetUpvalue,
		opGetGlobal,
		opGetTable,
		opGetField,
		opNewTable,
		opSelf,
		opSelfField,
		opAdd,
		opSub,
		opMul,
		opDiv,
		opMod,
		opPow,
		opUnaryMinus,
		opNot,
		opLength,
		opConcat,
		// Lua 5.1 marks TEST's A operand as defining A for symbolic
		// diagnostics. This deliberately breaks provenance across a
		// short-circuit expression even though TEST does not write at
		// runtime.
		opTest,
		opTestSet,
		opForLoop,
		opForPrep,
		opClosure,
		opVararg:
		return true
	default:
		return false
	}
}

// prototypeBuilder is the only path from mutable compiler or loader state to
// a Prototype. seal consumes it, verifies the complete instruction graph, and
// gives every retained slice exact capacity. Compiler slices are copied;
// hostile-input loaders may transfer exact, exclusively owned vectors.
type prototypeBuilder struct {
	sourceName   *internedText
	lineDefined  int
	lastLine     int
	parameters   int
	registers    int
	upvalues     int
	varargFlags  int
	code         []instruction
	constants    []slot
	children     []*Prototype
	debug        *prototypeDebugBuilder
	adoptVectors bool
	consumed     bool
}

type prototypeDebugBuilder struct {
	lines    []int
	locals   []prototypeLocalBuilder
	upvalues []*internedText
}

type prototypeLocalBuilder struct {
	name    *internedText
	startPC int
	endPC   int
}

func (builder *prototypeBuilder) seal() (*Prototype, *Error) {
	if builder == nil {
		return nil, newPrototypeSyntaxError(
			nil,
			-1,
			"missing function prototype",
		)
	}
	if builder.consumed {
		return nil, newPrototypeSyntaxError(
			builder.sourceName,
			-1,
			"function prototype builder was already consumed",
		)
	}
	builder.consumed = true

	prototype := &Prototype{
		sourceName: builder.sourceName,
		code: exactBuilderSlice(
			builder.code,
			builder.adoptVectors,
		),
		constants: exactBuilderSlice(
			builder.constants,
			builder.adoptVectors,
		),
		children: exactBuilderSlice(
			builder.children,
			builder.adoptVectors,
		),
	}
	if syntaxError := verifyPrototypeHeader(prototype, builder); syntaxError != nil {
		return nil, syntaxError
	}
	if syntaxError := verifyPrototype(prototype); syntaxError != nil {
		return nil, syntaxError
	}
	prototype.markSealed(prototypeTreeRetainedBytes(prototype))
	return prototype, nil
}

func exactBuilderSlice[Element any](
	values []Element,
	adopt bool,
) []Element {
	if len(values) == 0 {
		return nil
	}
	if adopt && len(values) == cap(values) {
		return values
	}
	return exactSlice(values)
}

func exactSlice[Element any](values []Element) []Element {
	if len(values) == 0 {
		return nil
	}
	copied := make([]Element, len(values))
	copy(copied, values)
	return copied
}

func prototypeStringSlot(value *internedText) slot {
	if value == nil {
		panic("lua: nil compiler string")
	}
	return stringSlot(newHashedStringRef(value.text, value.hash))
}

func prototypeStringText(value *internedText) string {
	if value == nil {
		return ""
	}
	return value.text
}
