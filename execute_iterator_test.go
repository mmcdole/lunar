package lua

import (
	"slices"
	"strings"
	"testing"
)

func TestExecutorRunsGenericFor(t *testing.T) {
	state, thread, result := executeTestChunk(t, `
local function iterator(limit, control)
	control = control + 1
	if control > limit then
		return nil
	end
	return control, control * 10, "discarded"
end

local sum = 0
local last = 0
for key, value in iterator, 3, 0 do
	sum = sum + key + value
	last = value
end
return sum, last
`)
	defer state.Close()
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Number(66), Number(30))
}

func TestExecutorGenericForAdjustsResults(t *testing.T) {
	state, thread, result := executeTestChunk(t, `
local function iterator(limit, control)
	control = control + 1
	if control > limit then
		return nil
	end
	return control
end

local missing_second = false
local missing_third = false
for first, second, third in iterator, 1, 0 do
	missing_second = second == nil
	missing_third = third == nil
end
return missing_second, missing_third
`)
	defer state.Close()
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Bool(true), Bool(true))
}

func TestExecutorGenericForUsesStateAndFeedsBackControl(t *testing.T) {
	state, thread, result := executeTestChunk(t, `
local calls = 0
local function iterator(state, control)
	calls = calls + 1
	if calls == 1 then
		if state ~= "state" or control ~= "seed" then
			return nil
		end
		return false, "first"
	end
	if calls == 2 then
		if state ~= "state" or control ~= false then
			return nil
		end
		return 2, "second"
	end
	if calls == 3 and control == 2 then
		return nil
	end
	return nil
end

local iterations = 0
local labels = ""
for control, label in iterator, "state", "seed" do
	iterations = iterations + 1
	labels = labels .. label
end
return iterations, calls, labels
`)
	defer state.Close()
	assertExecutionReturned(t, result)
	assertExecutionValues(
		t,
		thread,
		Number(2),
		Number(3),
		state.String("firstsecond"),
	)
}

func TestExecutorGenericForCallsCallableObjects(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	handler := compileTestFunction(t, state, "@iterator-call.lua", `
local self, limit, control = ...
if self ~= expected_generator then
	return nil
end
control = control + 1
if control > limit then
	return nil
end
return control, control * 2
`)
	metatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString("__call", handler.owningValue()); err != nil {
		t.Fatal(err)
	}
	generator, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(generator.Value(), metatable); err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal(
		"expected_generator",
		generator.Value(),
	); err != nil {
		t.Fatal(err)
	}

	caller := compileTestFunction(t, state, "@iterator.lua", `
local generator = ...
local sum = 0
for key, value in generator, 3, 0 do
	sum = sum + key + value
end
return sum
`)
	thread, result := executeTestFunction(
		t,
		state,
		caller,
		generator.Value(),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Number(18))
}

func TestExecutorGenericForRejectsNonCallableGenerators(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(testing.TB, *State) Value
	}{
		{
			name: "missing __call",
			configure: func(t testing.TB, state *State) Value {
				value, err := state.NewTable(0, 0)
				if err != nil {
					t.Fatal(err)
				}
				return value.Value()
			},
		},
		{
			name: "non-function __call",
			configure: func(t testing.TB, state *State) Value {
				metatable, err := state.NewTable(0, 1)
				if err != nil {
					t.Fatal(err)
				}
				if err := metatable.RawSetString(
					"__call",
					Number(1),
				); err != nil {
					t.Fatal(err)
				}
				value, err := state.NewTable(0, 0)
				if err != nil {
					t.Fatal(err)
				}
				if err := state.SetMetatable(
					value.Value(),
					metatable,
				); err != nil {
					t.Fatal(err)
				}
				return value.Value()
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, err := New(Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			generator := test.configure(t, state)
			caller := compileTestFunction(t, state, "@iterator.lua", `
local generator = ...
for value in generator, nil, nil do
	return value
end
return nil
`)
			thread, result := executeTestFunction(
				t,
				state,
				caller,
				generator,
			)
			if result.kind != executionFailed ||
				result.err == nil ||
				!strings.Contains(
					result.err.Error(),
					"attempt to call a table value",
				) {
				t.Fatalf("iterator call failure = %+v", result)
			}
			traceback := result.err.Traceback()
			if len(traceback) != 1 ||
				traceback[0].Source != "@iterator.lua" ||
				traceback[0].Line == 0 {
				t.Fatalf("iterator call traceback = %+v", traceback)
			}
			if len(thread.frames) != 0 ||
				len(thread.continuations) != 0 {
				t.Fatal("iterator call failure left executable state")
			}
		})
	}
}

func TestExecutorGenericForSupportsTailCalledIterators(t *testing.T) {
	state, thread, result := executeTestChunk(t, `
local function step(limit, control)
	control = control + 1
	if control <= limit then
		return control
	end
	return nil
end
local function iterator(state, control)
	return step(state, control)
end

local sum = 0
for value in iterator, 4, 0 do
	sum = sum + value
end
return sum
`)
	defer state.Close()
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Number(10))
}

func TestExecutorGenericForClosesCapturedIterationValues(t *testing.T) {
	state, thread, result := executeTestChunk(t, `
local function iterator(limit, control)
	control = control + 1
	if control <= limit then
		return control
	end
	return nil
end

local first, second, third
for value in iterator, 3, 0 do
	local function read()
		return value
	end
	if value == 1 then
		first = read
	elseif value == 2 then
		second = read
	else
		third = read
	end
end
return first(), second(), third()
`)
	defer state.Close()
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Number(1), Number(2), Number(3))
}

func TestExecutorGenericForClosesCapturedValueOnBreak(t *testing.T) {
	state, thread, result := executeTestChunk(t, `
local function iterator(limit, control)
	control = control + 1
	if control <= limit then
		return control
	end
	return nil
end

local saved
for value in iterator, 3, 0 do
	saved = function()
		return value
	end
	break
end
return saved()
`)
	defer state.Close()
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Number(1))
}

func TestExecutorGenericForNestsWithOtherContinuations(t *testing.T) {
	t.Run("metamethod inside iterator", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		add := compileTestFunction(t, state, "@add.lua", `
local left, right = ...
return left.value + right.value
`)
		left := metamethodTestTable(t, state, "__add", add.owningValue())
		right := metamethodTestTable(t, state, "__add", add.owningValue())
		if err := left.RawSetString("value", Number(3)); err != nil {
			t.Fatal(err)
		}
		if err := right.RawSetString("value", Number(4)); err != nil {
			t.Fatal(err)
		}
		iteratorState, err := state.NewTable(0, 2)
		if err != nil {
			t.Fatal(err)
		}
		if err := iteratorState.RawSetString("left", left.Value()); err != nil {
			t.Fatal(err)
		}
		if err := iteratorState.RawSetString("right", right.Value()); err != nil {
			t.Fatal(err)
		}
		iterator := compileTestFunction(t, state, "@iterator.lua", `
local state, control = ...
if control ~= nil then
	return nil
end
return state.left + state.right
`)
		caller := compileTestFunction(t, state, "@caller.lua", `
local iterator, state = ...
local sum = 0
for value in iterator, state, nil do
	sum = sum + value
end
return sum
`)
		thread, result := executeTestFunction(
			t,
			state,
			caller,
			iterator.owningValue(),
			iteratorState.Value(),
		)
		assertExecutionReturned(t, result)
		assertExecutionValues(t, thread, Number(7))
		if len(thread.continuations) != 0 {
			t.Fatal("nested arithmetic left an iterator continuation")
		}
	})

	t.Run("iterator inside metamethod", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		iterator := compileTestFunction(t, state, "@iterator.lua", `
local limit, control = ...
control = control + 1
if control <= limit then
	return control
end
return nil
`)
		add := compileTestFunction(t, state, "@add.lua", `
local left = ...
local sum = 0
for value in left.iterator, 3, 0 do
	sum = sum + value
end
return sum
`)
		left := metamethodTestTable(t, state, "__add", add.owningValue())
		right := metamethodTestTable(t, state, "__add", add.owningValue())
		if err := left.RawSetString(
			"iterator",
			iterator.owningValue(),
		); err != nil {
			t.Fatal(err)
		}
		caller := compileTestFunction(t, state, "@caller.lua", `
local left, right = ...
return left + right
`)
		thread, result := executeTestFunction(
			t,
			state,
			caller,
			left.Value(),
			right.Value(),
		)
		assertExecutionReturned(t, result)
		assertExecutionValues(t, thread, Number(6))
		if len(thread.continuations) != 0 {
			t.Fatal("nested iterator left an arithmetic continuation")
		}
	})
}

func TestExecutorGenericForFailureClearsContinuation(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	iterator := compileTestFunction(t, state, "@iterator.lua", `
local invalid = 1
return invalid()
`)
	caller := compileTestFunction(t, state, "@caller.lua", `
local iterator = ...
for value in iterator, nil, nil do
	return value
end
return nil
`)
	thread, result := executeTestFunction(
		t,
		state,
		caller,
		iterator.owningValue(),
	)
	if result.kind != executionFailed ||
		result.err == nil ||
		!strings.Contains(
			result.err.Error(),
			"attempt to call local 'invalid' (a number value)",
		) {
		t.Fatalf("iterator execution = %+v; want failure", result)
	}
	traceback := result.err.Traceback()
	if len(traceback) != 2 ||
		traceback[0].Source != "@iterator.lua" ||
		traceback[0].Line == 0 ||
		traceback[1].Source != "@caller.lua" ||
		traceback[1].Line == 0 {
		t.Fatalf("iterator traceback = %+v", traceback)
	}
	if len(thread.frames) != 0 ||
		len(thread.continuations) != 0 ||
		thread.top != 0 ||
		thread.frameExtent != 0 {
		t.Fatal("iterator failure left executable state")
	}
}

func TestExecutorIteratorLimitFailuresDoNotStageCallWindow(t *testing.T) {
	for _, test := range []struct {
		name     string
		options  Options
		callable bool
	}{
		{
			name:    "frames",
			options: Options{MaxFrames: 1, MaxValues: 32},
		},
		{
			name:    "values",
			options: Options{MaxFrames: 2, MaxValues: 8},
		},
		{
			name:     "callable values",
			options:  Options{MaxFrames: 2, MaxValues: 8},
			callable: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, err := New(test.options)
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			generator := newTestLuaFunction(
				t,
				state,
				2,
				3,
				0,
				0,
			).owningValue()
			if test.callable {
				handler := newTestLuaFunction(
					t,
					state,
					3,
					3,
					0,
					0,
				)
				metatable, tableErr := state.NewTable(0, 1)
				if tableErr != nil {
					t.Fatal(tableErr)
				}
				if tableErr = metatable.RawSetString(
					"__call",
					handler.owningValue(),
				); tableErr != nil {
					t.Fatal(tableErr)
				}
				value, tableErr := state.NewTable(0, 0)
				if tableErr != nil {
					t.Fatal(tableErr)
				}
				if tableErr = state.SetMetatable(
					value.Value(),
					metatable,
				); tableErr != nil {
					t.Fatal(tableErr)
				}
				generator = value.Value()
			}
			root, syntaxError := compileSource("@iterator.lua", `
return function(iterator)
	for value in iterator, nil, nil do
		return value
	end
	return nil
end
`)
			if syntaxError != nil {
				t.Fatal(syntaxError)
			}
			if len(root.children) != 1 ||
				root.children[0].upvalues != 0 ||
				root.children[0].varargFlags&varargIsVararg != 0 {
				t.Fatal("iterator limit fixture is not one fixed child")
			}
			caller := newLuaFunction(
				state,
				root.children[0],
				state.main.globals,
				nil,
			)
			thread := state.main
			setTestCall(thread, 0, caller, generator)
			if callErr := thread.pushFunctionCall(
				caller,
				0,
				1,
				allResults,
			); callErr != nil {
				t.Fatal(callErr)
			}
			current := runInstructions(thread, 0)
			if current.opcode() != opIteratorLoop {
				t.Fatalf("executor stopped at %s; want TFORLOOP", current.opcode())
			}
			beforeValues := slices.Clone(thread.values)
			beforeFrames := slices.Clone(thread.frames)
			beforeTop := thread.top
			beforeExtent := thread.frameExtent

			callErr := startIteratorCall(thread, 0, current)
			if callErr == nil || callErr.Category() != ResourceError {
				t.Fatalf("iterator limit error = %v", callErr)
			}
			if !slices.Equal(thread.values, beforeValues) ||
				!slices.Equal(thread.frames, beforeFrames) ||
				thread.top != beforeTop ||
				thread.frameExtent != beforeExtent ||
				len(thread.continuations) != 0 {
				t.Fatal("iterator limit failure mutated the thread")
			}
		})
	}
}

func TestExecutorWarmGenericForDoesNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	iterator := compileTestFunction(t, state, "@iterator.lua", `
local limit, control = ...
control = control + 1
if control <= limit then
	return control
end
return nil
`)
	caller := compileTestFunction(t, state, "@caller.lua", `
local iterator, limit = ...
local sum = 0
for value in iterator, limit, 0 do
	sum = sum + value
end
return sum
`)
	arguments := []slot{
		slotFromFunctionObject(iterator),
		numberSlot(100),
	}
	thread := state.main
	thread.reserveValues(64)
	thread.reserveFrames(8)
	leave := enterTestExecution(t, thread)
	defer leave()
	benchmarkRunExecutor(thread, caller, arguments)
	allocations := testing.AllocsPerRun(1000, func() {
		benchmarkRunExecutor(thread, caller, arguments)
	})
	if allocations != 0 {
		t.Fatalf(
			"warm generic for allocated %.2f times; want 0",
			allocations,
		)
	}
}

func BenchmarkExecutorGenericFor(b *testing.B) {
	for _, test := range []struct {
		name           string
		iteratorSource string
		callerSource   string
	}{
		{
			name: "one result",
			iteratorSource: `
local limit, control = ...
control = control + 1
if control <= limit then
	return control
end
return nil
`,
			callerSource: `
local iterator, limit = ...
local sum = 0
for value in iterator, limit, 0 do
	sum = sum + value
end
return sum
`,
		},
		{
			name: "two results",
			iteratorSource: `
local limit, control = ...
control = control + 1
if control <= limit then
	return control, control * 2
end
return nil
`,
			callerSource: `
local iterator, limit = ...
local sum = 0
for key, value in iterator, limit, 0 do
	sum = sum + key + value
end
return sum
`,
		},
		{
			name: "empty",
			iteratorSource: `
return nil
`,
			callerSource: `
local iterator = ...
for value in iterator, nil, nil do
	return value
end
return nil
`,
		},
	} {
		b.Run(test.name, func(b *testing.B) {
			state, err := New(Options{})
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() {
				_ = state.Close()
			})
			iterator := compileTestFunction(
				b,
				state,
				"@iterator.lua",
				test.iteratorSource,
			)
			caller := compileTestFunction(
				b,
				state,
				"@caller.lua",
				test.callerSource,
			)
			arguments := []Value{iterator.owningValue()}
			if test.name != "empty" {
				arguments = append(arguments, Number(100))
			}
			benchmarkExecutorFunction(
				b,
				state,
				caller,
				arguments...,
			)
		})
	}
}
