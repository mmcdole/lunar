package lua

import "context"

// Operator identifies a Lua arithmetic operation.
type Operator uint8

const (
	// AddOperator applies Lua's + and honors __add.
	AddOperator Operator = iota
	// SubtractOperator applies Lua's binary - and honors __sub.
	SubtractOperator
	// MultiplyOperator applies Lua's * and honors __mul.
	MultiplyOperator
	// DivideOperator applies Lua's / and honors __div.
	DivideOperator
	// ModuloOperator applies Lua's % and honors __mod.
	ModuloOperator
	// PowerOperator applies Lua's ^ and honors __pow.
	PowerOperator
	// NegateOperator applies Lua's unary - and honors __unm. It reads one
	// operand; Arith ignores the second, matching Lua 5.1's __unm call
	// convention of passing the operand twice.
	NegateOperator
)

var operatorNames = [...]string{
	AddOperator:      "add",
	SubtractOperator: "subtract",
	MultiplyOperator: "multiply",
	DivideOperator:   "divide",
	ModuloOperator:   "modulo",
	PowerOperator:    "power",
	NegateOperator:   "negate",
}

// String names the operator.
func (operator Operator) String() string {
	if int(operator) < len(operatorNames) {
		return operatorNames[operator]
	}
	return "unknown"
}

func (operator Operator) valid() bool {
	return operator <= NegateOperator
}

// opcode maps an Operator onto the executor's instruction so arithmetic
// requested by a host shares the runtime's numeric and metamethod
// behavior rather than reimplementing it.
func (operator Operator) opcode() opcode {
	switch operator {
	case AddOperator:
		return opAdd
	case SubtractOperator:
		return opSub
	case MultiplyOperator:
		return opMul
	case DivideOperator:
		return opDiv
	case ModuloOperator:
		return opMod
	case PowerOperator:
		return opPow
	case NegateOperator:
		return opUnaryMinus
	default:
		panic("lua: invalid arithmetic operator")
	}
}

// Arith applies a Lua arithmetic operation on the main Thread.
//
// Numbers and complete numeric strings are computed directly. Otherwise
// Arith follows the operation's metamethod and may execute Lua. Use
// Frame.Arith from a native callback.
func (state *State) Arith(
	operator Operator,
	left, right Value,
) (Value, error) {
	if !operator.valid() {
		panic("lua: invalid arithmetic operator")
	}
	arguments := [2]Value{left, right}
	return state.luaOperationValue(
		nil,
		arithmeticOperation(operator),
		arguments[:],
	)
}

// ArithContext applies Arith while making ctx available to executing Lua.
func (state *State) ArithContext(
	ctx context.Context,
	operator Operator,
	left, right Value,
) (Value, error) {
	if ctx == nil {
		return Value{}, ErrNilContext
	}
	if !operator.valid() {
		panic("lua: invalid arithmetic operator")
	}
	arguments := [2]Value{left, right}
	return state.luaOperationValue(
		ctx,
		arithmeticOperation(operator),
		arguments[:],
	)
}

// Concat applies Lua's .. operation on the main Thread.
//
// Strings and numbers are joined directly. Otherwise Concat follows
// __concat and may execute Lua. Use Frame.Concat from a native callback.
func (state *State) Concat(left, right Value) (Value, error) {
	arguments := [2]Value{left, right}
	return state.luaOperationValue(nil, luaConcatOperation, arguments[:])
}

// ConcatContext applies Concat while making ctx available to executing Lua.
func (state *State) ConcatContext(
	ctx context.Context,
	left, right Value,
) (Value, error) {
	if ctx == nil {
		return Value{}, ErrNilContext
	}
	arguments := [2]Value{left, right}
	return state.luaOperationValue(ctx, luaConcatOperation, arguments[:])
}

// Arith applies a Lua arithmetic operation from a native callback.
func (frame Frame) Arith(
	operator Operator,
	left, right Value,
) (Value, error) {
	if !operator.valid() {
		panic("lua: invalid arithmetic operator")
	}
	if err := frame.admitOperands(left, right); err != nil {
		return Value{}, err
	}
	result, failure := frame.arithSlots(
		slotFromValue(left),
		slotFromValue(right),
		operator,
		true,
	)
	if failure != nil {
		return Value{}, failure.exposeValue()
	}
	return result.owningValue(), nil
}

// Concat applies Lua's .. operation from a native callback.
func (frame Frame) Concat(left, right Value) (Value, error) {
	if err := frame.admitOperands(left, right); err != nil {
		return Value{}, err
	}
	result, failure := frame.concatSlots(
		slotFromValue(left),
		slotFromValue(right),
		true,
	)
	if failure != nil {
		return Value{}, failure.exposeValue()
	}
	return result.owningValue(), nil
}

// admitOperands runs the shared entry checks for a two-operand Frame
// operation: a live activation, no pending exit, and both operands owned
// by this State. Failures are returned, matching Frame.Equal: a foreign
// Value is a caller error the callback can handle, not a panic.
func (frame Frame) admitOperands(left, right Value) error {
	frame.activation()
	if failure := frame.thread.state.execution.pendingExit; failure != nil {
		return failure.exposeValue()
	}
	if err := frame.thread.owner.accept(left); err != nil {
		return err
	}
	return frame.thread.owner.accept(right)
}

func (frame Frame) arithSlots(
	left, right slot,
	operator Operator,
	admitArguments bool,
) (slot, *Error) {
	if operator == NegateOperator {
		right = left
	}
	leftNumber, leftOK := slotToNumber(left)
	rightNumber, rightOK := slotToNumber(right)
	if leftOK && rightOK {
		if operator == NegateOperator {
			return numberSlot(-leftNumber), nil
		}
		return numberSlot(numericBinary(
			operator.opcode(),
			leftNumber,
			rightNumber,
		)), nil
	}
	method, found := binaryMetamethod(
		frame.thread,
		left,
		right,
		arithmeticMetamethod(operator.opcode()),
	)
	if !found {
		invalid := left
		if leftOK {
			invalid = right
		}
		return nilSlot, libraryFailure(
			frame,
			"attempt to perform arithmetic on a %s value",
			invalid.kind(),
		)
	}
	return frame.callOperandMetamethod(method, left, right, admitArguments)
}

func (frame Frame) concatSlots(
	left, right slot,
	admitArguments bool,
) (slot, *Error) {
	if isDirectConcatValue(left) && isDirectConcatValue(right) {
		operands := [2]slot{left, right}
		result, overflow := concatDirectValues(frame.thread, operands[:])
		if overflow {
			return nilSlot, libraryFailure(frame, "string length overflow")
		}
		return result, nil
	}
	method, found := binaryMetamethod(
		frame.thread,
		left,
		right,
		metaConcat,
	)
	if !found {
		invalid := left
		if isDirectConcatValue(left) {
			invalid = right
		}
		return nilSlot, libraryFailure(
			frame,
			"attempt to concatenate a %s value",
			invalid.kind(),
		)
	}
	return frame.callOperandMetamethod(method, left, right, admitArguments)
}

// callOperandMetamethod invokes a binary metamethod with both operands,
// admitting them when the operands came from a host Value rather than from
// slots the runtime already owns.
func (frame Frame) callOperandMetamethod(
	method slot,
	left, right slot,
	admitArguments bool,
) (slot, *Error) {
	arguments := [2]slot{left, right}
	if admitArguments {
		return frame.callBoundaryCompactOne(
			method,
			arguments[:],
			compactCallAdmission(0)|compactCallAdmission(1),
		)
	}
	return frame.callCompactOne(method, arguments[:])
}
