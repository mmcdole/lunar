package lua

import (
	"math"
	"strings"
	"testing"
)

func TestExecutorLengthUsesLua51PrimitiveSemantics(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	trap := compileTestFunction(t, state, "@trap.lua", `return 99`)
	metatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString("__len", trap.owningValue()); err != nil {
		t.Fatal(err)
	}
	table, err := state.NewTable(3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := table.RawSetInt(1, Bool(true)); err != nil {
		t.Fatal(err)
	}
	if err := table.RawSetInt(2, Bool(true)); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(table.Value(), metatable); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(state.String(""), metatable); err != nil {
		t.Fatal(err)
	}

	caller := compileTestFunction(t, state, "@length.lua", `
local text, sequence = ...
return #text, #sequence
`)
	thread, result := executeTestFunction(
		t,
		state,
		caller,
		state.String("a\x00é"),
		table.Value(),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Number(4), Number(2))
}

func TestExecutorLengthMetamethodArgumentsAndNilFallback(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	handler := compileTestFunction(t, state, "@length-handler.lua", `
local value, fallback = ...
observed_value = value
observed_nil = fallback == nil
return marker
`)
	metatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString("__len", handler.owningValue()); err != nil {
		t.Fatal(err)
	}
	data, err := state.NewUserData("payload")
	if err != nil {
		t.Fatal(err)
	}
	marker, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("marker", marker.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(data.Value(), metatable); err != nil {
		t.Fatal(err)
	}
	caller := compileTestFunction(
		t,
		state,
		"@length.lua",
		`local value = ...; return #value`,
	)
	thread, result := executeTestFunction(
		t,
		state,
		caller,
		data.Value(),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, marker.Value())
	if observed, err := state.Global("observed_value"); err != nil {
		t.Fatal(err)
	} else if same, applicable := observed.SameObject(data.Value()); !applicable ||
		!same {
		t.Fatalf("length operand = %v", observed)
	}
	if observed, err := state.Global("observed_nil"); err != nil {
		t.Fatal(err)
	} else if boolean, ok := observed.AsBool(); !ok || !boolean {
		t.Fatalf("length fallback argument = %v", observed)
	}

	countHandler := compileTestFunction(
		t,
		state,
		"@length-count.lua",
		`
local function count(...)
	return arg.n
end
return count(...)
`,
	)
	countMetatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := countMetatable.RawSetString(
		"__len",
		countHandler.owningValue(),
	); err != nil {
		t.Fatal(err)
	}
	counted, err := state.NewUserData("counted")
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(
		counted.Value(),
		countMetatable,
	); err != nil {
		t.Fatal(err)
	}
	thread, result = executeTestFunction(t, state, caller, counted.Value())
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Number(2))

	empty, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(Number(0), empty); err != nil {
		t.Fatal(err)
	}
	fallback := compileTestFunction(
		t,
		state,
		"@nil-length.lua",
		`return 31`,
	)
	if err := metatable.RawSetString("__len", fallback.owningValue()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(Nil(), metatable); err != nil {
		t.Fatal(err)
	}
	thread, result = executeTestFunction(t, state, caller, Number(7))
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Number(31))
}

func TestExecutorLengthRejectsValuesWithoutHandler(t *testing.T) {
	state, thread, result := executeTestChunk(
		t,
		`local value = ...; return #value`,
		Bool(true),
	)
	defer state.Close()
	if result.kind != executionFailed || result.err == nil ||
		!strings.Contains(
			result.err.Error(),
			"attempt to get length of local 'value' (a boolean value)",
		) {
		t.Fatalf("length failure = %+v", result)
	}
	if len(thread.frames) != 0 || len(thread.continuations) != 0 {
		t.Fatal("length failure left executable state")
	}
}

func TestExecutorLengthDestinationMayOverlapSource(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	builder := testPrototypeBuilder(
		makeABC(opLength, 0, 0, 0),
		makeABC(opReturn, 0, 2, 0),
	)
	builder.parameters = 1
	builder.registers = 1
	prototype, syntaxError := builder.seal()
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	function := newLuaFunction(
		state,
		prototype,
		state.main.globals,
		nil,
	)
	thread, result := executeTestFunction(
		t,
		state,
		function,
		state.String("a\x00é"),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Number(4))
}

func TestExecutorConcatenatesStringsAndNumbers(t *testing.T) {
	state, thread, result := executeTestChunk(
		t,
		`local a, b, c, d = ...; return a .. b .. c .. d`,
		stateNeutralString("a\x00"),
		Number(12.5),
		stateNeutralString("é"),
		stateNeutralString(""),
	)
	defer state.Close()
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, state.String("a\x0012.5é"))

	state, thread, result = executeTestChunk(
		t,
		`local a, b, c, d = ...; return a .. "," .. b .. "," .. c .. "," .. d`,
		Number(math.Copysign(0, -1)),
		Number(math.Inf(1)),
		Number(math.Inf(-1)),
		Number(math.NaN()),
	)
	defer state.Close()
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, state.String("-0,inf,-inf,nan"))

	state, thread, result = executeTestChunk(
		t,
		`local number = ...; return number .. ""`,
		Number(1.234567890123456),
	)
	defer state.Close()
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, state.String("1.2345678901235"))
}

func TestExecutorConcatReducesMetamethodsRightToLeft(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	handler := compileTestFunction(t, state, "@concat-handler.lua", `
local left, right = ...
if phase == 0 and left == expected_b and right == expected_c then
	phase = 1
	return joined
end
if phase == 1 and left == expected_a and right == joined then
	phase = 2
	return "done"
end
phase = -1
return "bad"
`)
	metatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString("__concat", handler.owningValue()); err != nil {
		t.Fatal(err)
	}
	values := make([]*Table, 4)
	for index := range values {
		values[index], err = state.NewTable(0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := state.SetMetatable(values[index].Value(), metatable); err != nil {
			t.Fatal(err)
		}
	}
	if err := state.SetGlobal("expected_a", values[0].Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("expected_b", values[1].Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("expected_c", values[2].Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("joined", values[3].Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("phase", Number(0)); err != nil {
		t.Fatal(err)
	}
	caller := compileTestFunction(t, state, "@concat.lua", `
local a, b, c = ...
return a .. b .. c
`)
	thread, result := executeTestFunction(
		t,
		state,
		caller,
		values[0].Value(),
		values[1].Value(),
		values[2].Value(),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, state.String("done"))
	if phase, err := state.Global("phase"); err != nil {
		t.Fatal(err)
	} else if number, ok := phase.AsNumber(); !ok || number != 2 {
		t.Fatalf("concat phase = %v", phase)
	}
}

func TestExecutorConcatSelectsLeftThenRightMetamethod(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	leftHandler := compileTestFunction(t, state, "@left.lua", `return "left"`)
	rightHandler := compileTestFunction(t, state, "@right.lua", `return "right"`)
	left := metamethodTestTable(t, state, "__concat", leftHandler.owningValue())
	right := metamethodTestTable(t, state, "__concat", rightHandler.owningValue())
	plain, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	caller := compileTestFunction(t, state, "@concat.lua", `
local left, right = ...
return left .. right
`)

	thread, result := executeTestFunction(
		t,
		state,
		caller,
		left.Value(),
		right.Value(),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, state.String("left"))

	thread, result = executeTestFunction(
		t,
		state,
		caller,
		plain.Value(),
		right.Value(),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, state.String("right"))
}

func TestExecutorConcatObservesMetamethodMutationBetweenPairs(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	replacement := compileTestFunction(
		t,
		state,
		"@replacement.lua",
		`return "updated"`,
	)
	first := compileTestFunction(t, state, "@first.lua", `
meta.__concat = replacement
return joined
`)
	metatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString("__concat", first.owningValue()); err != nil {
		t.Fatal(err)
	}
	values := make([]*Table, 4)
	for index := range values {
		values[index], err = state.NewTable(0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := state.SetMetatable(values[index].Value(), metatable); err != nil {
			t.Fatal(err)
		}
	}
	if err := state.SetGlobal("meta", metatable.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal(
		"replacement",
		replacement.owningValue(),
	); err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("joined", values[3].Value()); err != nil {
		t.Fatal(err)
	}
	caller := compileTestFunction(t, state, "@concat.lua", `
local a, b, c = ...
return a .. b .. c
`)
	thread, result := executeTestFunction(
		t,
		state,
		caller,
		values[0].Value(),
		values[1].Value(),
		values[2].Value(),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, state.String("updated"))
}

func TestExecutorConcatTransitionsBetweenDirectAndMetamethodPairs(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	handler := compileTestFunction(t, state, "@handler.lua", `
local _, right = ...
if right == "2x" then
	return "middle"
end
return "bad"
`)
	middle := metamethodTestTable(t, state, "__concat", handler.owningValue())
	caller := compileTestFunction(t, state, "@concat.lua", `
local prefix, middle, number, suffix = ...
return prefix .. middle .. number .. suffix
`)
	thread, result := executeTestFunction(
		t,
		state,
		caller,
		state.String("prefix"),
		middle.Value(),
		Number(2),
		state.String("x"),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, state.String("prefixmiddle"))
}

func TestExecutorConcatMayFailAfterEarlierMetamethod(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	handler := compileTestFunction(t, state, "@handler.lua", `
phase = 1
return replacement
`)
	right := metamethodTestTable(t, state, "__concat", handler.owningValue())
	last, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("phase", Number(0)); err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("replacement", replacement.Value()); err != nil {
		t.Fatal(err)
	}
	caller := compileTestFunction(t, state, "@concat.lua", `
local left, middle, right = ...
return left .. middle .. right
`)
	thread, result := executeTestFunction(
		t,
		state,
		caller,
		Bool(true),
		right.Value(),
		last.Value(),
	)
	if result.kind != executionFailed ||
		result.err == nil ||
		!strings.Contains(
			result.err.Error(),
			"attempt to concatenate local 'left' (a boolean value)",
		) {
		t.Fatalf("resumed concat failure = %+v", result)
	}
	if phase, err := state.Global("phase"); err != nil {
		t.Fatal(err)
	} else if number, ok := phase.AsNumber(); !ok || number != 1 {
		t.Fatalf("earlier concat metamethod phase = %v", phase)
	}
	if len(thread.frames) != 0 || len(thread.continuations) != 0 {
		t.Fatal("resumed concat failure left executable state")
	}
}

func TestExecutorPrimitiveConcatIgnoresMetamethods(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	trap := compileTestFunction(t, state, "@trap.lua", `return "trap"`)
	metatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString("__concat", trap.owningValue()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(Number(0), metatable); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(state.String(""), metatable); err != nil {
		t.Fatal(err)
	}
	caller := compileTestFunction(t, state, "@concat.lua", `
local left, right = ...
return left .. right
`)
	thread, result := executeTestFunction(
		t,
		state,
		caller,
		Number(12),
		state.String("x"),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, state.String("12x"))
}

func TestExecutorConcatMetamethodMayReturnNil(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	handler := compileTestFunction(t, state, "@concat.lua", `return`)
	left := metamethodTestTable(t, state, "__concat", handler.owningValue())
	right, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	caller := compileTestFunction(t, state, "@caller.lua", `
local left, right = ...
return left .. right
`)
	thread, result := executeTestFunction(
		t,
		state,
		caller,
		left.Value(),
		right.Value(),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Nil())
}

func TestExecutorConcatReportsFirstNonCoercibleOperand(t *testing.T) {
	for _, test := range []struct {
		name  string
		left  Value
		right Value
		local string
	}{
		{
			name:  "left",
			left:  Bool(true),
			right: Number(1),
			local: "left",
		},
		{
			name:  "right",
			left:  stateNeutralString("prefix"),
			right: Bool(false),
			local: "right",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, thread, result := executeTestChunk(
				t,
				`local left, right = ...; return left .. right`,
				test.left,
				test.right,
			)
			defer state.Close()
			if result.kind != executionFailed ||
				result.err == nil ||
				!strings.Contains(
					result.err.Error(),
					"attempt to concatenate local '"+test.local+
						"' (a boolean value)",
				) {
				t.Fatalf("concat failure = %+v", result)
			}
			if len(thread.frames) != 0 ||
				len(thread.continuations) != 0 {
				t.Fatal("concat failure left executable state")
			}
		})
	}
}

func TestExecutorConcatDestinationMayOverlapOperands(t *testing.T) {
	for _, destination := range []int{0, 1, 2} {
		t.Run(string(rune('A'+destination)), func(t *testing.T) {
			state, err := New(Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			builder := testPrototypeBuilder(
				makeABC(opConcat, destination, 0, 2),
				makeABC(opReturn, destination, 2, 0),
			)
			builder.parameters = 3
			builder.registers = 3
			prototype, syntaxError := builder.seal()
			if syntaxError != nil {
				t.Fatal(syntaxError)
			}
			function := newLuaFunction(
				state,
				prototype,
				state.main.globals,
				nil,
			)
			thread, result := executeTestFunction(
				t,
				state,
				function,
				state.String("a"),
				state.String("b"),
				state.String("c"),
			)
			assertExecutionReturned(t, result)
			assertExecutionValues(t, thread, state.String("abc"))
		})
	}
}

func TestExecutorConcatFailuresClearContinuationState(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	handler := compileTestFunction(t, state, "@handler.lua", `
local invalid = 1
return invalid()
`)
	left := metamethodTestTable(t, state, "__concat", handler.owningValue())
	right, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	caller := compileTestFunction(t, state, "@caller.lua", `
local left, right = ...
return left .. right
`)
	thread, result := executeTestFunction(
		t,
		state,
		caller,
		left.Value(),
		right.Value(),
	)
	if result.kind != executionFailed || result.err == nil {
		t.Fatalf("concat execution = %+v; want failure", result)
	}
	if len(thread.frames) != 0 ||
		len(thread.continuations) != 0 ||
		thread.top != 0 ||
		thread.frameExtent != 0 {
		t.Fatal("concat failure left executable state")
	}
}

func TestExecutorConcatFrameLimitFailureIsAtomic(t *testing.T) {
	state, err := New(Options{MaxFrames: 1, MaxValues: 32})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	handler := compileTestFunction(t, state, "@handler.lua", `return "joined"`)
	left := metamethodTestTable(t, state, "__concat", handler.owningValue())
	right, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	caller := compileTestFunction(t, state, "@caller.lua", `
local left, right = ...
return left .. right
`)
	thread, result := executeTestFunction(
		t,
		state,
		caller,
		left.Value(),
		right.Value(),
	)
	if result.kind != executionFailed ||
		result.err == nil ||
		result.err.Category() != ResourceError {
		t.Fatalf("concat frame-limit result = %+v", result)
	}
	if len(thread.frames) != 0 || len(thread.continuations) != 0 {
		t.Fatal("concat frame-limit failure left executable state")
	}
}

func TestExecutorStringOperatorAllocationContracts(t *testing.T) {
	requireStableAllocationAccounting(t)
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	table, err := state.NewTable(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := table.RawSetInt(1, Bool(true)); err != nil {
		t.Fatal(err)
	}
	length := compileTestFunction(t, state, "@length.lua", `
local text, sequence = ...
local total = 0
for index = 1, 20 do
	total = total + #text + #sequence
end
return total
`)
	primitive := compileTestFunction(t, state, "@concat.lua", `
local a, b, c, d = ...
return a .. b .. c .. d
`)
	handler := compileTestFunction(t, state, "@handler.lua", `return "joined"`)
	left := metamethodTestTable(t, state, "__concat", handler.owningValue())
	right, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	metamethod := compileTestFunction(t, state, "@metamethod.lua", `
local left, right = ...
return left .. right
`)

	thread := state.main
	thread.reserveValues(64)
	thread.reserveFrames(8)
	tests := []struct {
		name      string
		function  *functionObject
		arguments []slot
		maxAllocs float64
	}{
		{
			name:     "length",
			function: length,
			arguments: []slot{
				stringSlot(state.runtime.strings.make("length")),
				slotFromValue(table.Value()),
			},
			maxAllocs: 0,
		},
		{
			name:     "primitive concat",
			function: primitive,
			arguments: []slot{
				stringSlot(state.runtime.strings.make("a")),
				numberSlot(2),
				stringSlot(state.runtime.strings.make("c")),
				stringSlot(state.runtime.strings.make("d")),
			},
			maxAllocs: 1,
		},
		{
			name:     "metamethod concat",
			function: metamethod,
			arguments: []slot{
				slotFromValue(left.Value()),
				slotFromValue(right.Value()),
			},
			maxAllocs: 0,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			leave := enterTestExecution(t, thread)
			defer leave()
			benchmarkRunExecutor(thread, test.function, test.arguments)
			benchmarkRunExecutor(thread, test.function, test.arguments)
			allocations := testing.AllocsPerRun(1000, func() {
				benchmarkRunExecutor(
					thread,
					test.function,
					test.arguments,
				)
			})
			if allocations > test.maxAllocs {
				t.Fatalf(
					"warm path allocated %.2f times; want at most %.0f",
					allocations,
					test.maxAllocs,
				)
			}
		})
	}
}

func BenchmarkExecutorLengthLoop(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = state.Close()
	})
	table, err := state.NewTable(1, 0)
	if err != nil {
		b.Fatal(err)
	}
	if err := table.RawSetInt(1, Bool(true)); err != nil {
		b.Fatal(err)
	}
	function := compileTestFunction(b, state, "@length.lua", `
local text, sequence = ...
local total = 0
for index = 1, 100 do
	total = total + #text + #sequence
end
return total
`)
	benchmarkExecutorFunction(
		b,
		state,
		function,
		state.String("badger"),
		table.Value(),
	)
}

func BenchmarkExecutorConcatEight(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = state.Close()
	})
	function := compileTestFunction(b, state, "@concat.lua", `
local a, b, c, d, e, f, g, h = ...
return a .. b .. c .. d .. e .. f .. g .. h
`)
	for _, test := range []struct {
		name      string
		arguments []Value
	}{
		{
			name: "strings",
			arguments: []Value{
				state.String("a"),
				state.String("b"),
				state.String("c"),
				state.String("d"),
				state.String("e"),
				state.String("f"),
				state.String("g"),
				state.String("h"),
			},
		},
		{
			name: "mixed numbers",
			arguments: []Value{
				state.String("a"),
				Number(2),
				state.String("c"),
				Number(4.5),
				state.String("e"),
				Number(-6),
				state.String("g"),
				Number(8.25),
			},
		},
	} {
		b.Run(test.name, func(b *testing.B) {
			benchmarkExecutorFunction(
				b,
				state,
				function,
				test.arguments...,
			)
		})
	}
}
