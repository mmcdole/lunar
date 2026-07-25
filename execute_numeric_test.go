package lua

import (
	"math"
	"slices"
	"strings"
	"testing"
)

func TestExecutorRunsNumericOperations(t *testing.T) {
	const source = `
local left, right = ...
return left + right, left - right, left * right, left / right,
	left % right, left ^ right, -left
`
	tests := []struct {
		name  string
		left  Value
		right Value
		want  []Value
	}{
		{
			name:  "numbers",
			left:  Number(-3),
			right: Number(2),
			want: []Value{
				Number(-1),
				Number(-5),
				Number(-6),
				Number(-1.5),
				Number(1),
				Number(9),
				Number(3),
			},
		},
		{
			name:  "numeric strings",
			left:  stateNeutralString(" 0x10 "),
			right: stateNeutralString("+2.0"),
			want: []Value{
				Number(18),
				Number(14),
				Number(32),
				Number(8),
				Number(0),
				Number(256),
				Number(-16),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, thread, result := executeTestChunk(
				t,
				source,
				test.left,
				test.right,
			)
			defer state.Close()
			assertExecutionReturned(t, result)
			assertExecutionValues(t, thread, test.want...)
		})
	}
}

func TestExecutorNumericIEEEAndModuloSemantics(t *testing.T) {
	state, thread, result := executeTestChunk(t, `
local positive, negative, zero = ...
return positive / zero, negative / zero, zero / zero,
	negative % positive, positive % negative, positive % zero, -zero
`, Number(3), Number(-3), Number(0))
	defer state.Close()
	assertExecutionReturned(t, result)
	if thread.top != 7 {
		t.Fatalf("numeric result count = %d; want 7", thread.top)
	}
	numbers := make([]float64, thread.top)
	for index := range numbers {
		var ok bool
		numbers[index], ok = thread.values[index].owningValue().AsNumber()
		if !ok {
			t.Fatalf("numeric result %d is %s", index, thread.values[index].kind())
		}
	}
	if !math.IsInf(numbers[0], 1) ||
		!math.IsInf(numbers[1], -1) ||
		!math.IsNaN(numbers[2]) ||
		numbers[3] != 0 ||
		numbers[4] != 0 ||
		!math.IsNaN(numbers[5]) ||
		!math.Signbit(numbers[6]) {
		t.Fatalf("IEEE numeric results = %#v", numbers)
	}

	state, thread, result = executeTestChunk(
		t,
		`local a, b = ...; return a % b, b % a`,
		Number(-3),
		Number(2),
	)
	defer state.Close()
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Number(1), Number(-1))
}

func TestExecutorArithmeticMetamethodSelection(t *testing.T) {
	t.Run("left then right", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		leftHandler := compileTestFunction(
			t,
			state,
			"@left.lua",
			`return "left"`,
		)
		rightHandler := compileTestFunction(
			t,
			state,
			"@right.lua",
			`return "right"`,
		)
		left := numericTestTable(t, state, "__add", leftHandler.Value())
		right := numericTestTable(t, state, "__add", rightHandler.Value())
		caller := compileTestFunction(
			t,
			state,
			"@caller.lua",
			`local left, right = ...; return left + right`,
		)

		thread, result := executeTestFunction(
			t,
			state,
			caller,
			left.Value(),
			right.Value(),
		)
		assertExecutionReturned(t, result)
		assertExecutionValues(t, thread, state.String("left"))

		plain, err := state.NewTable(0, 0)
		if err != nil {
			t.Fatal(err)
		}
		thread, result = executeTestFunction(
			t,
			state,
			caller,
			plain.Value(),
			right.Value(),
		)
		assertExecutionReturned(t, result)
		assertExecutionValues(t, thread, state.String("right"))
	})

	t.Run("non-callable left blocks right", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		rightHandler := compileTestFunction(
			t,
			state,
			"@right.lua",
			`return "right"`,
		)
		left := numericTestTable(t, state, "__add", Bool(false))
		right := numericTestTable(t, state, "__add", rightHandler.Value())
		caller := compileTestFunction(
			t,
			state,
			"@caller.lua",
			`local left, right = ...; return left + right`,
		)

		_, result := executeTestFunction(
			t,
			state,
			caller,
			left.Value(),
			right.Value(),
		)
		if result.kind != executionFailed ||
			result.err == nil ||
			!strings.Contains(
				result.err.Error(),
				"attempt to call a boolean value",
			) {
			t.Fatalf("non-callable arithmetic result = %+v", result)
		}
	})

	t.Run("numeric coercion wins", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		trap := compileTestFunction(
			t,
			state,
			"@trap.lua",
			`return "metamethod"`,
		)
		metatable, err := state.NewTable(0, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := metatable.RawSetString("__add", trap.Value()); err != nil {
			t.Fatal(err)
		}
		if err := state.SetMetatable(state.String(""), metatable); err != nil {
			t.Fatal(err)
		}
		caller := compileTestFunction(
			t,
			state,
			"@caller.lua",
			`local left, right = ...; return left + right`,
		)

		thread, result := executeTestFunction(
			t,
			state,
			caller,
			state.String(" 40 "),
			state.String("2"),
		)
		assertExecutionReturned(t, result)
		assertExecutionValues(t, thread, Number(42))
	})
}

func TestExecutorArithmeticMetamethodEvents(t *testing.T) {
	for _, test := range []struct {
		name       string
		event      string
		expression string
	}{
		{"add", "__add", "left + right"},
		{"subtract", "__sub", "left - right"},
		{"multiply", "__mul", "left * right"},
		{"divide", "__div", "left / right"},
		{"modulo", "__mod", "left % right"},
		{"power", "__pow", "left ^ right"},
		{"unary minus", "__unm", "-left"},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, err := New(Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			handler := compileTestFunction(
				t,
				state,
				"@handler.lua",
				`return "`+test.event+`"`,
			)
			left := numericTestTable(
				t,
				state,
				test.event,
				handler.Value(),
			)
			right, err := state.NewTable(0, 0)
			if err != nil {
				t.Fatal(err)
			}
			caller := compileTestFunction(
				t,
				state,
				"@caller.lua",
				"local left, right = ...; return "+test.expression,
			)

			thread, result := executeTestFunction(
				t,
				state,
				caller,
				left.Value(),
				right.Value(),
			)
			assertExecutionReturned(t, result)
			assertExecutionValues(t, thread, state.String(test.event))
		})
	}
}

func TestExecutorArithmeticMetamethodCallProtocol(t *testing.T) {
	t.Run("callable handler and original operands", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		callHandler := compileTestFunction(
			t,
			state,
			"@call.lua",
			`local callable, left, right = ...; return right`,
		)
		callable := numericTestTable(t, state, "__call", callHandler.Value())
		left := numericTestTable(t, state, "__add", callable.Value())
		right, err := state.NewTable(0, 0)
		if err != nil {
			t.Fatal(err)
		}
		caller := compileTestFunction(
			t,
			state,
			"@caller.lua",
			`local left, right = ...; return left + right`,
		)

		thread, result := executeTestFunction(
			t,
			state,
			caller,
			left.Value(),
			right.Value(),
		)
		assertExecutionReturned(t, result)
		assertExecutionValues(t, thread, right.Value())
	})

	t.Run("unary receives two operands", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		handler := compileTestFunction(
			t,
			state,
			"@unary.lua",
			`local left, right = ...; return left == right`,
		)
		value := numericTestTable(t, state, "__unm", handler.Value())
		caller := compileTestFunction(
			t,
			state,
			"@caller.lua",
			`local value = ...; return -value`,
		)

		thread, result := executeTestFunction(
			t,
			state,
			caller,
			value.Value(),
		)
		assertExecutionReturned(t, result)
		assertExecutionValues(t, thread, Bool(true))
	})

	t.Run("first or nil result", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		caller := compileTestFunction(
			t,
			state,
			"@caller.lua",
			`local left, right = ...; return left + right`,
		)
		right, err := state.NewTable(0, 0)
		if err != nil {
			t.Fatal(err)
		}

		multiple := compileTestFunction(
			t,
			state,
			"@multiple.lua",
			`return 1, 2`,
		)
		left := numericTestTable(t, state, "__add", multiple.Value())
		thread, result := executeTestFunction(
			t,
			state,
			caller,
			left.Value(),
			right.Value(),
		)
		assertExecutionReturned(t, result)
		assertExecutionValues(t, thread, Number(1))

		empty := compileTestFunction(t, state, "@empty.lua", `return`)
		left = numericTestTable(t, state, "__add", empty.Value())
		thread, result = executeTestFunction(
			t,
			state,
			caller,
			left.Value(),
			right.Value(),
		)
		assertExecutionReturned(t, result)
		assertExecutionValues(t, thread, Nil())
	})

	t.Run("tail-call handler", func(t *testing.T) {
		state, err := New(Options{MaxFrames: 2})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		handler := compileTestFunction(t, state, "@handler.lua", `
local left, right = ...
local function finish(value)
	return value
end
return finish(right)
`)
		if !prototypeContainsOpcode(handler.prototype, opTailCall) {
			t.Fatal("handler did not compile a tail call")
		}
		left := numericTestTable(t, state, "__add", handler.Value())
		right, err := state.NewTable(0, 0)
		if err != nil {
			t.Fatal(err)
		}
		caller := compileTestFunction(
			t,
			state,
			"@caller.lua",
			`local left, right = ...; return left + right`,
		)

		thread, result := executeTestFunction(
			t,
			state,
			caller,
			left.Value(),
			right.Value(),
		)
		assertExecutionReturned(t, result)
		assertExecutionValues(t, thread, right.Value())
		if len(thread.continuations) != 0 {
			t.Fatal("tail-called metamethod retained a continuation")
		}
	})
}

func TestExecutorNumericRegisterAndConstantOperands(t *testing.T) {
	builder := testPrototypeBuilder(
		makeABC(
			opAdd,
			0,
			registerOrConstant(0, false),
			registerOrConstant(0, true),
		),
		makeABC(
			opSub,
			0,
			registerOrConstant(1, true),
			registerOrConstant(0, false),
		),
		makeABC(opReturn, 0, 2, 0),
	)
	builder.parameters = 1
	builder.registers = 1
	builder.constants = []slot{numberSlot(2), numberSlot(20)}
	prototype, syntaxError := builder.seal()
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	t.Run("numbers", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		function := newLuaFunction(
			state.runtime,
			prototype,
			state.globals,
			nil,
		)
		thread, result := executeTestFunction(
			t,
			state,
			function,
			Number(5),
		)
		assertExecutionReturned(t, result)
		assertExecutionValues(t, thread, Number(13))
	})

	t.Run("metamethods receive operands before overlapping write", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		add := compileTestFunction(
			t,
			state,
			"@add.lua",
			`local left = ...; return left`,
		)
		sub := compileTestFunction(
			t,
			state,
			"@sub.lua",
			`local _, right = ...; return right`,
		)
		metatable, err := state.NewTable(0, 2)
		if err != nil {
			t.Fatal(err)
		}
		if err := metatable.RawSetString("__add", add.Value()); err != nil {
			t.Fatal(err)
		}
		if err := metatable.RawSetString("__sub", sub.Value()); err != nil {
			t.Fatal(err)
		}
		value, err := state.NewTable(0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := state.SetMetatable(value.Value(), metatable); err != nil {
			t.Fatal(err)
		}
		function := newLuaFunction(
			state.runtime,
			prototype,
			state.globals,
			nil,
		)

		thread, result := executeTestFunction(
			t,
			state,
			function,
			value.Value(),
		)
		assertExecutionReturned(t, result)
		assertExecutionValues(t, thread, value.Value())
	})
}

func TestExecutorNestedArithmeticContinuation(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	addHandler := compileTestFunction(
		t,
		state,
		"@add.lua",
		`local left, right = ...; return left == right`,
	)
	equalHandler := compileTestFunction(
		t,
		state,
		"@equal.lua",
		`return "equal"`,
	)
	metatable, err := state.NewTable(0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString("__add", addHandler.Value()); err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString("__eq", equalHandler.Value()); err != nil {
		t.Fatal(err)
	}
	left, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	right, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(left.Value(), metatable); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(right.Value(), metatable); err != nil {
		t.Fatal(err)
	}
	caller := compileTestFunction(
		t,
		state,
		"@caller.lua",
		`local left, right = ...; return left + right`,
	)

	thread, result := executeTestFunction(
		t,
		state,
		caller,
		left.Value(),
		right.Value(),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Bool(true))
	if len(thread.continuations) != 0 {
		t.Fatal("nested arithmetic retained an execution continuation")
	}
}

func TestExecutorEqualitySemantics(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	caller := compileTestFunction(t, state, "@equality.lua", `
local left, right = ...
return left == right, left ~= right
`)

	for _, test := range []struct {
		name  string
		left  Value
		right Value
		equal bool
	}{
		{"nil", Nil(), Nil(), true},
		{"booleans", Bool(true), Bool(false), false},
		{"numbers", Number(0), Number(math.Copysign(0, -1)), true},
		{"nan", Number(math.NaN()), Number(math.NaN()), false},
		{"no coercion", Number(1), state.String("1"), false},
		{"strings", state.String("same"), stateNeutralString("same"), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			thread, result := executeTestFunction(
				t,
				state,
				caller,
				test.left,
				test.right,
			)
			assertExecutionReturned(t, result)
			assertExecutionValues(
				t,
				thread,
				Bool(test.equal),
				Bool(!test.equal),
			)
		})
	}

	handler := compileTestFunction(
		t,
		state,
		"@equal-handler.lua",
		`return "truthy"`,
	)
	left := numericTestTable(t, state, "__eq", handler.Value())
	right := numericTestTable(t, state, "__eq", handler.Value())
	thread, result := executeTestFunction(
		t,
		state,
		caller,
		left.Value(),
		right.Value(),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Bool(true), Bool(false))

	other := compileTestFunction(
		t,
		state,
		"@other.lua",
		`return true`,
	)
	right = numericTestTable(t, state, "__eq", other.Value())
	thread, result = executeTestFunction(
		t,
		state,
		caller,
		left.Value(),
		right.Value(),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Bool(false), Bool(true))

	falseHandler := compileTestFunction(
		t,
		state,
		"@false.lua",
		`return false`,
	)
	first := numericTestTable(t, state, "__eq", falseHandler.Value())
	second := numericTestTable(t, state, "__eq", falseHandler.Value())
	thread, result = executeTestFunction(
		t,
		state,
		caller,
		first.Value(),
		first.Value(),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Bool(true), Bool(false))

	thread, result = executeTestFunction(
		t,
		state,
		caller,
		first.Value(),
		second.Value(),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Bool(false), Bool(true))

	metatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString(
		"__eq",
		handler.Value(),
	); err != nil {
		t.Fatal(err)
	}
	leftData, err := state.NewUserData("left")
	if err != nil {
		t.Fatal(err)
	}
	rightData, err := state.NewUserData("right")
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(leftData.Value(), metatable); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(rightData.Value(), metatable); err != nil {
		t.Fatal(err)
	}
	thread, result = executeTestFunction(
		t,
		state,
		caller,
		leftData.Value(),
		rightData.Value(),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Bool(true), Bool(false))
}

func TestExecutorOrderSemantics(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	caller := compileTestFunction(t, state, "@order.lua", `
local left, right = ...
return left < right, left <= right, left > right, left >= right
`)

	thread, result := executeTestFunction(
		t,
		state,
		caller,
		Number(2),
		Number(3),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(
		t,
		thread,
		Bool(true),
		Bool(true),
		Bool(false),
		Bool(false),
	)

	thread, result = executeTestFunction(
		t,
		state,
		caller,
		state.String("a\x00z"),
		state.String("a\x01"),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(
		t,
		thread,
		Bool(true),
		Bool(true),
		Bool(false),
		Bool(false),
	)

	_, result = executeTestFunction(
		t,
		state,
		caller,
		Number(1),
		state.String("2"),
	)
	if result.kind != executionFailed ||
		result.err == nil ||
		!strings.Contains(result.err.Error(), "attempt to compare number with string") {
		t.Fatalf("mixed ordering result = %+v", result)
	}
}

func TestExecutorOrderMetamethods(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	caller := compileTestFunction(t, state, "@order.lua", `
local left, right = ...
return left < right, left <= right
`)
	less := compileTestFunction(t, state, "@less.lua", `return true`)
	left := numericTestTable(t, state, "__lt", less.Value())
	right := numericTestTable(t, state, "__lt", less.Value())

	thread, result := executeTestFunction(
		t,
		state,
		caller,
		left.Value(),
		right.Value(),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Bool(true), Bool(false))

	lessEqual := compileTestFunction(t, state, "@less-equal.lua", `return false`)
	if err := left.metatable.RawSetString("__le", lessEqual.Value()); err != nil {
		t.Fatal(err)
	}
	if err := right.metatable.RawSetString("__le", lessEqual.Value()); err != nil {
		t.Fatal(err)
	}
	thread, result = executeTestFunction(
		t,
		state,
		caller,
		left.Value(),
		right.Value(),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Bool(true), Bool(false))

	different := compileTestFunction(t, state, "@different.lua", `return true`)
	if err := right.metatable.RawSetString("__lt", different.Value()); err != nil {
		t.Fatal(err)
	}
	_, result = executeTestFunction(
		t,
		state,
		caller,
		left.Value(),
		right.Value(),
	)
	if result.kind != executionFailed ||
		result.err == nil ||
		!strings.Contains(result.err.Error(), "attempt to compare two table values") {
		t.Fatalf("mismatched ordering result = %+v", result)
	}
}

func TestExecutorNumericForSemantics(t *testing.T) {
	const source = `
local initial, limit, step = ...
local count, total = 0, 0
for value = initial, limit, step do
	count = count + 1
	total = total + value
	value = 1000
end
return count, total
`
	tests := []struct {
		name    string
		initial Value
		limit   Value
		step    Value
		count   float64
		total   float64
	}{
		{"positive", Number(1), Number(5), Number(2), 3, 9},
		{"negative", Number(5), Number(1), Number(-2), 3, 9},
		{
			"fractional strings",
			stateNeutralString("0.5"),
			stateNeutralString("1.5"),
			stateNeutralString("0.5"),
			3,
			3,
		},
		{"positive zero iterations", Number(3), Number(1), Number(1), 0, 0},
		{"negative zero iterations", Number(1), Number(3), Number(-1), 0, 0},
		{"nan initial", Number(math.NaN()), Number(3), Number(1), 0, 0},
		{"nan limit", Number(1), Number(math.NaN()), Number(1), 0, 0},
		{"nan step", Number(1), Number(3), Number(math.NaN()), 0, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, thread, result := executeTestChunk(
				t,
				source,
				test.initial,
				test.limit,
				test.step,
			)
			defer state.Close()
			assertExecutionReturned(t, result)
			assertExecutionValues(
				t,
				thread,
				Number(test.count),
				Number(test.total),
			)
		})
	}
}

func TestExecutorNumericForZeroStepAndErrors(t *testing.T) {
	for _, step := range []Value{
		Number(0),
		Number(math.Copysign(0, -1)),
	} {
		state, thread, result := executeTestChunk(t, `
local step = ...
local count = 0
for value = 3, 1, step do
	count = count + 1
	break
end
return count
`, step)
		assertExecutionReturned(t, result)
		assertExecutionValues(t, thread, Number(1))
		state.Close()

		state, thread, result = executeTestChunk(t, `
local step = ...
local count = 0
for value = 1, 3, step do
	count = count + 1
end
return count
`, step)
		assertExecutionReturned(t, result)
		assertExecutionValues(t, thread, Number(0))
		state.Close()
	}

	for _, test := range []struct {
		name string
		args []Value
		want string
	}{
		{
			"initial",
			[]Value{Bool(false), Number(2), Number(1)},
			"'for' initial value must be a number",
		},
		{
			"limit",
			[]Value{Number(1), Bool(false), Number(1)},
			"'for' limit must be a number",
		},
		{
			"step",
			[]Value{Number(1), Number(2), Bool(false)},
			"'for' step must be a number",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, _, result := executeTestChunk(t, `
local initial, limit, step = ...
for value = initial, limit, step do end
return true
`, test.args...)
			defer state.Close()
			if result.kind != executionFailed ||
				result.err == nil ||
				!strings.Contains(result.err.Error(), test.want) {
				t.Fatalf("numeric-for error = %+v; want %q", result, test.want)
			}
		})
	}
}

func TestExecutorMetamethodFailureClearsContinuations(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	handler := compileTestFunction(t, state, "@handler.lua", `
local invalid = 1
return invalid()
`)
	left := numericTestTable(t, state, "__add", handler.Value())
	right, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	caller := compileTestFunction(
		t,
		state,
		"@caller.lua",
		`local left, right = ...; return left + right`,
	)

	thread, result := executeTestFunction(
		t,
		state,
		caller,
		left.Value(),
		right.Value(),
	)
	if result.kind != executionFailed || result.err == nil {
		t.Fatalf("metamethod failure = %+v; want failure", result)
	}
	if len(thread.frames) != 0 ||
		len(thread.continuations) != 0 ||
		thread.top != 0 ||
		thread.frameExtent != 0 {
		t.Fatal("metamethod failure retained execution state")
	}
	traceback := result.err.Traceback()
	if len(traceback) != 2 ||
		traceback[0].Source != "@handler.lua" ||
		traceback[1].Source != "@caller.lua" {
		t.Fatalf("metamethod traceback = %+v", traceback)
	}
}

func TestExecutorMetamethodLimitFailuresAreAtomic(t *testing.T) {
	for _, test := range []struct {
		name             string
		options          Options
		handlerRegisters int
	}{
		{
			name:             "frames",
			options:          Options{MaxFrames: 1, MaxValues: 16},
			handlerRegisters: 3,
		},
		{
			name:             "scratch values",
			options:          Options{MaxFrames: 2, MaxValues: 5},
			handlerRegisters: 3,
		},
		{
			name:             "handler window",
			options:          Options{MaxFrames: 2, MaxValues: 8},
			handlerRegisters: 10,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, err := New(test.options)
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			thread := state.MainThread()
			caller := newTestLuaFunction(t, state, 0, 4, 0, 0)
			handler := newTestLuaFunction(
				t,
				state,
				2,
				test.handlerRegisters,
				0,
				0,
			)
			setTestCall(thread, 0, caller)
			if callErr := thread.pushLuaCall(
				caller,
				0,
				0,
				0,
			); callErr != nil {
				t.Fatal(callErr)
			}
			beforeValues := slices.Clone(thread.values)
			beforeFrames := slices.Clone(thread.frames)
			beforeTop := thread.top
			beforeExtent := thread.frameExtent

			callErr := startMetamethodCall(
				thread,
				0,
				0,
				slotFromValue(handler.Value()),
				numberSlot(1),
				numberSlot(2),
				numberSlot(3),
				3,
				0,
				executionContinuation{},
			)
			if callErr == nil || callErr.Category() != ResourceError {
				t.Fatalf("metamethod limit error = %v", callErr)
			}
			if thread.top != beforeTop ||
				thread.frameExtent != beforeExtent ||
				len(thread.continuations) != 0 ||
				!slices.Equal(thread.values, beforeValues) ||
				!slices.Equal(thread.frames, beforeFrames) {
				t.Fatal("metamethod limit failure mutated the thread")
			}
		})
	}
}

func TestExecutorUnwindClearsSuspendedContinuation(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	thread := state.MainThread()
	caller := newTestLuaFunction(t, state, 0, 4, 0, 0)
	handler := compileTestFunction(t, state, "@handler.lua", `
local invalid = 1
return invalid()
`)
	setTestCall(thread, 0, caller)
	if callErr := thread.pushLuaCall(caller, 0, 0, 0); callErr != nil {
		t.Fatal(callErr)
	}
	callerFrame := thread.frames[0]
	if callErr := startMetamethodCall(
		thread,
		0,
		0,
		slotFromValue(handler.Value()),
		numberSlot(1),
		numberSlot(2),
		nilSlot,
		2,
		1,
		executionContinuation{},
	); callErr != nil {
		t.Fatal(callErr)
	}
	if len(thread.frames) != 2 || len(thread.continuations) != 1 {
		t.Fatal("metamethod call did not suspend its caller")
	}

	result := execute(thread, 1)
	if result.kind != executionFailed || result.err == nil {
		t.Fatalf("metamethod execution = %+v; want failure", result)
	}
	if len(thread.frames) != 1 ||
		thread.frames[0] != callerFrame ||
		len(thread.continuations) != 0 ||
		thread.top != int(callerFrame.base)+
			int(caller.prototype.registers) ||
		thread.frameExtent != thread.top {
		t.Fatalf(
			"partial unwind left %d frames, %d continuations, top %d, extent %d",
			len(thread.frames),
			len(thread.continuations),
			thread.top,
			thread.frameExtent,
		)
	}
}

func TestExecutorNumericHotPathsDoNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	function := compileTestFunction(t, state, "@numeric.lua", `
local total = 0
for value = 1, 20 do
	total = total + value * 2
end
return total
`)
	thread := state.MainThread()
	thread.reserveValues(32)
	thread.reserveFrames(4)

	run := func() {
		benchmarkRunExecutor(thread, function, nil)
		if number, ok := thread.values[0].owningValue().AsNumber(); !ok ||
			number != 420 {
			panic("unexpected numeric loop result")
		}
	}
	run()
	if allocations := testing.AllocsPerRun(1000, run); allocations != 0 {
		t.Fatalf("warm numeric executor allocations = %v; want 0", allocations)
	}
}

func TestExecutorWarmMetamethodContinuationDoesNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	handler := compileTestFunction(
		t,
		state,
		"@handler.lua",
		`return 42`,
	)
	left := numericTestTable(t, state, "__add", handler.Value())
	right, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	caller := compileTestFunction(
		t,
		state,
		"@caller.lua",
		`local left, right = ...; return left + right`,
	)
	arguments := []slot{
		slotFromValue(left.Value()),
		slotFromValue(right.Value()),
	}
	thread := state.MainThread()
	thread.reserveValues(32)
	thread.reserveFrames(8)

	run := func() {
		benchmarkRunExecutor(thread, caller, arguments)
		if number, ok := thread.values[0].owningValue().AsNumber(); !ok ||
			number != 42 {
			panic("unexpected metamethod result")
		}
	}
	run()
	if allocations := testing.AllocsPerRun(1000, run); allocations != 0 {
		t.Fatalf(
			"warm metamethod continuation allocations = %v; want 0",
			allocations,
		)
	}
}

func BenchmarkExecutorNumericLoop100(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = state.Close()
	})
	function := compileTestFunction(b, state, "@numeric.lua", `
local total = 0
for value = 1, 100 do
	total = total + value
end
return total
`)
	benchmarkExecutorFunction(b, state, function)
}

func numericTestTable(
	t testing.TB,
	state *State,
	event string,
	method Value,
) *Table {
	t.Helper()
	metatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString(event, method); err != nil {
		t.Fatal(err)
	}
	value, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(value.Value(), metatable); err != nil {
		t.Fatal(err)
	}
	return value
}

func prototypeContainsOpcode(
	prototype *Prototype,
	operation opcode,
) bool {
	for _, code := range prototype.code {
		if code.opcode() == operation {
			return true
		}
	}
	for _, child := range prototype.children {
		if prototypeContainsOpcode(child, operation) {
			return true
		}
	}
	return false
}
