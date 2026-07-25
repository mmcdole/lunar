package lua

const (
	varargHasArg   = 1
	varargIsVararg = 2
	varargNeedsArg = 4
	varargMask     = varargHasArg | varargIsVararg | varargNeedsArg

	maxLuaRegisters = 250
	maxLuaUpvalues  = 60
)

type localInfo struct {
	name    *luaString
	startPC uint32
	endPC   uint32
}

type prototypeDebug struct {
	lines    []uint32
	locals   []localInfo
	upvalues []*luaString
}

// Prototype is immutable, verified Lua executable metadata.
//
// A Prototype is independent of any State and may be shared safely among
// States. Its executable arrays and constants are private. Strings retained by
// constants are immutable and do not refer back to a State.
type Prototype struct {
	sourceName  *luaString
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
	sealed      bool
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

// ParameterCount returns the number of fixed parameters.
func (prototype *Prototype) ParameterCount() int {
	if prototype == nil {
		return 0
	}
	return int(prototype.parameters)
}

// RegisterCount returns the number of registers required by an activation.
func (prototype *Prototype) RegisterCount() int {
	if prototype == nil {
		return 0
	}
	return int(prototype.registers)
}

// UpvalueCount returns the fixed upvalue count.
func (prototype *Prototype) UpvalueCount() int {
	if prototype == nil {
		return 0
	}
	return int(prototype.upvalues)
}

// IsVararg reports whether prototype accepts variable arguments.
func (prototype *Prototype) IsVararg() bool {
	return prototype != nil && prototype.varargFlags&varargIsVararg != 0
}

// ChildCount returns the number of nested function prototypes.
func (prototype *Prototype) ChildCount() int {
	if prototype == nil {
		return 0
	}
	return len(prototype.children)
}

// LineAt returns the source line associated with an instruction. It returns
// zero when debug line information was omitted or pc is out of range.
func (prototype *Prototype) LineAt(pc int) int {
	if prototype == nil ||
		prototype.debug == nil ||
		pc < 0 ||
		pc >= len(prototype.debug.lines) {
		return 0
	}
	return int(prototype.debug.lines[pc])
}

// prototypeBuilder is the only path from mutable compiler or loader state to
// a Prototype. seal consumes it, verifies the complete instruction graph, and
// copies every retained slice to its exact length.
type prototypeBuilder struct {
	sourceName  *luaString
	lineDefined int
	lastLine    int
	parameters  int
	registers   int
	upvalues    int
	varargFlags int
	code        []instruction
	constants   []slot
	children    []*Prototype
	debug       *prototypeDebugBuilder
	consumed    bool
}

type prototypeDebugBuilder struct {
	lines    []int
	locals   []prototypeLocalBuilder
	upvalues []*luaString
}

type prototypeLocalBuilder struct {
	name    *luaString
	startPC int
	endPC   int
}

func (builder *prototypeBuilder) seal() (*Prototype, *Error) {
	if builder == nil {
		return nil, newPrototypeSyntaxError(nil, -1, "missing function prototype")
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
		code:       exactSlice(builder.code),
		constants:  exactSlice(builder.constants),
		children:   exactSlice(builder.children),
	}
	if syntaxError := verifyPrototypeHeader(prototype, builder); syntaxError != nil {
		return nil, syntaxError
	}
	if syntaxError := verifyPrototype(prototype); syntaxError != nil {
		return nil, syntaxError
	}
	prototype.sealed = true
	return prototype, nil
}

func exactSlice[Element any](values []Element) []Element {
	if len(values) == 0 {
		return nil
	}
	copied := make([]Element, len(values))
	copy(copied, values)
	return copied
}

func prototypeStringSlot(value *luaString) slot {
	return slotFromValue(stringValue(value))
}

func prototypeStringText(value *luaString) string {
	if value == nil {
		return ""
	}
	return value.text
}
