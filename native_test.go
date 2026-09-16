package lua

import (
	"strings"
	"testing"
)

func TestConstructionUsesFunctionAndThreadEnvironmentsByObjectKind(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	functionEnvironment, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	threadEnvironment, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	prototype, err := Compile("@constructed-prototype.lua", "return 43")
	if err != nil {
		t.Fatal(err)
	}

	var createdNative *Function
	var createdData *UserData
	var loaded *Function
	var loadedPrototype *Function
	constructor, err := state.NewNativeFunction(func(frame Frame) Outcome {
		if frame.activation().function.environment.owningHandle() != functionEnvironment {
			frame.ThrowString("callback lost its function environment")
		}
		if frame.thread.globals.owningHandle() != threadEnvironment {
			frame.ThrowString("callback lost its thread environment")
		}

		var constructionErr error
		createdNative, constructionErr = state.NewNativeFunction(
			func(inner Frame) Outcome {
				return inner.Return()
			},
		)
		if constructionErr != nil {
			frame.ThrowString(constructionErr.Error())
		}
		createdData, constructionErr = state.NewUserData("created")
		if constructionErr != nil {
			frame.ThrowString(constructionErr.Error())
		}
		loaded, constructionErr = state.LoadString(
			"@constructed.lua",
			"return 42",
		)
		if constructionErr != nil {
			frame.ThrowString(constructionErr.Error())
		}
		loadedPrototype, constructionErr = state.LoadPrototype(prototype)
		if constructionErr != nil {
			frame.ThrowString(constructionErr.Error())
		}
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetFunctionEnvironment(
		constructor,
		functionEnvironment,
	); err != nil {
		t.Fatal(err)
	}

	thread, err := state.NewThread(constructor.Value())
	if err != nil {
		t.Fatal(err)
	}
	if err := setThreadEnvironment(thread, threadEnvironment); err != nil {
		t.Fatal(err)
	}
	results, status, err := thread.Resume()
	if err != nil || status != ThreadDead || len(results) != 0 {
		t.Fatalf(
			"constructor resume = (results=%v, status=%v, err=%v)",
			results,
			status,
			err,
		)
	}
	if createdNative == nil ||
		createdData == nil ||
		loaded == nil ||
		loadedPrototype == nil {
		t.Fatal("callback did not construct every object")
	}

	if environment, environmentErr := state.FunctionEnvironment(
		createdNative,
	); environmentErr != nil || environment != functionEnvironment {
		t.Fatalf(
			"created native environment = (%p, %v); want %p",
			environment,
			environmentErr,
			functionEnvironment,
		)
	}
	if environment, environmentErr := userDataEnvironment(
		createdData,
	); environmentErr != nil || environment != functionEnvironment {
		t.Fatalf(
			"created userdata environment = (%p, %v); want %p",
			environment,
			environmentErr,
			functionEnvironment,
		)
	}
	if environment, environmentErr := state.FunctionEnvironment(
		loaded,
	); environmentErr != nil || environment != threadEnvironment {
		t.Fatalf(
			"loaded function environment = (%p, %v); want %p",
			environment,
			environmentErr,
			threadEnvironment,
		)
	}
	if environment, environmentErr := state.FunctionEnvironment(
		loadedPrototype,
	); environmentErr != nil || environment != threadEnvironment {
		t.Fatalf(
			"LoadPrototype environment = (%p, %v); want %p",
			environment,
			environmentErr,
			threadEnvironment,
		)
	}
}

func TestExecutorPropagatesOpenNativeResults(t *testing.T) {
	many := NativeFunc(func(frame Frame) Outcome {
		return frame.ReturnValues(Number(1), Number(2))
	})
	none := NativeFunc(func(frame Frame) Outcome {
		return frame.Return()
	})
	tests := []struct {
		name     string
		source   string
		callback NativeFunc
		expected []Value
	}{
		{
			name:     "return suffix",
			source:   `return 9, host()`,
			callback: many,
			expected: []Value{Number(9), Number(1), Number(2)},
		},
		{
			name: "call arguments",
			source: `
local function consume(a, b)
	return a, b
end
return consume(host())
`,
			callback: many,
			expected: []Value{Number(1), Number(2)},
		},
		{
			name: "constructor suffix",
			source: `
local values = {9, host()}
return values[1], values[2], values[3]
`,
			callback: many,
			expected: []Value{Number(9), Number(1), Number(2)},
		},
		{
			name: "tail forwarding",
			source: `
local function forward()
	return host()
end
return forward()
`,
			callback: many,
			expected: []Value{Number(1), Number(2)},
		},
		{
			name: "nested open tail suffix",
			source: `
local function forward()
	return host()
end
return 9, forward()
`,
			callback: many,
			expected: []Value{Number(9), Number(1), Number(2)},
		},
		{
			name:     "zero-result suffix",
			source:   `return 9, host()`,
			callback: none,
			expected: []Value{Number(9)},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, err := New(Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			host, err := state.NewNativeFunction(test.callback)
			if err != nil {
				t.Fatal(err)
			}
			if err := state.RawSetGlobal("host", host.Value()); err != nil {
				t.Fatal(err)
			}
			chunk := compileTestFunction(
				t,
				state,
				"@native-open-results.lua",
				test.source,
			)
			thread, result := executeTestFunction(t, state, chunk)
			assertExecutionReturned(t, result)
			assertExecutionValues(t, thread, test.expected...)
			if len(thread.frames) != 0 ||
				len(thread.continuations) != 0 {
				t.Fatal("open native result retained execution state")
			}
		})
	}
}

func TestNativeFrameRaisesProtectedErrors(t *testing.T) {
	t.Run("arbitrary value", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		raised := state.String("native failure")
		function, err := state.NewNativeFunction(
			func(frame Frame) Outcome {
				frame.Throw(raised)
				// Unreachable: the throw above does not return.
				return Outcome{}
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		thread := stageNativeTestCall(t, state, function, allResults)
		failure := invokeNativeCall(thread)
		if failure == nil ||
			failure.Category() != RuntimeError ||
			!rawEqual(failure.Value(), raised) ||
			failure.Error() != "native failure" {
			t.Fatalf("failure = %#v", failure)
		}
		if len(thread.frames) != 1 || thread.activeNativeToken != 0 {
			t.Fatal("native failure did not leave one unwindable activation")
		}
		thread.unwindCalls(0)
	})

	t.Run("argument type", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		function, err := state.NewNativeFunction(
			func(frame Frame) Outcome {
				if _, ok := frame.Number(1); !ok {
					frame.ThrowArgTypeError(1, NumberKind)
				}
				return frame.Return()
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		thread := stageNativeTestCall(t, state, function, allResults, Bool(true))
		failure := invokeNativeCall(thread)
		if failure == nil ||
			!strings.Contains(
				failure.Error(),
				"bad argument #2 (number expected, got no value)",
			) {
			t.Fatalf("failure = %v", failure)
		}
		thread.unwindCalls(0)
	})

	t.Run("argument reason", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		function, err := state.NewNativeFunction(
			func(frame Frame) Outcome {
				frame.ThrowArgError(0, "value must be positive")
				// Unreachable: the throw above does not return.
				return Outcome{}
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		thread := stageNativeTestCall(t, state, function, allResults, Number(-1))
		failure := invokeNativeCall(thread)
		if failure == nil ||
			failure.Error() != "bad argument #1 (value must be positive)" {
			t.Fatalf("failure = %v", failure)
		}
		thread.unwindCalls(0)
	})

	t.Run("string error", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		function, err := state.NewNativeFunction(
			func(frame Frame) Outcome {
				frame.ThrowString("native string failure")
				// Unreachable: the throw above does not return.
				return Outcome{}
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		thread := stageNativeTestCall(t, state, function, allResults)
		failure := invokeNativeCall(thread)
		if failure == nil || failure.Error() != "native string failure" {
			t.Fatalf("failure = %v", failure)
		}
		thread.unwindCalls(0)
	})

	t.Run("executor traceback", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		host, err := state.NewNativeFunction(
			func(frame Frame) Outcome {
				frame.Throw(state.String("native traceback"))
				// Unreachable: the throw above does not return.
				return Outcome{}
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := state.RawSetGlobal("host", host.Value()); err != nil {
			t.Fatal(err)
		}
		chunk := compileTestFunction(t, state, "@native-trace.lua", `
local function inner()
	host()
	return 1
end
local function outer()
	local result = inner()
	return result
end
return outer()
`)
		thread, result := executeTestFunction(t, state, chunk)
		if result.kind != executionFailed ||
			result.err == nil ||
			result.err.Error() != "native traceback" {
			t.Fatalf("execution result = %+v", result)
		}
		traceback := result.err.Traceback()
		if len(traceback) < 3 ||
			traceback[0].Source != "=[Go]" ||
			traceback[0].Function != "native function" ||
			traceback[1].Source != "@native-trace.lua" {
			t.Fatalf("native traceback = %+v", traceback)
		}
		if len(thread.frames) != 0 ||
			len(thread.continuations) != 0 ||
			thread.top != 0 ||
			thread.frameExtent != 0 {
			t.Fatal("native failure left executable state")
		}
	})
}

func TestNativeErrorsUnwindEveryContinuationMode(t *testing.T) {
	tests := []struct {
		name   string
		event  string
		source string
	}{
		{
			name:   "store result",
			event:  "__index",
			source: `return target.missing`,
		},
		{
			name:   "discard result",
			event:  "__newindex",
			source: `target.missing = 1`,
		},
		{
			name:   "comparison",
			event:  "__lt",
			source: `return left < right`,
		},
		{
			name:   "concatenation",
			event:  "__concat",
			source: `return left .. right`,
		},
		{
			name:   "iterator",
			source: `for key in iterator, nil, nil do end`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, err := New(Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			failing, err := state.NewNativeFunction(
				func(frame Frame) Outcome {
					frame.ThrowString("continued native failure")
					// Unreachable: the throw above does not return.
					return Outcome{}
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			newTable := func(name string) *Table {
				t.Helper()
				table, tableErr := state.NewTableWithCapacity(0, 0)
				if tableErr != nil {
					t.Fatal(tableErr)
				}
				if tableErr = state.RawSetGlobal(
					name,
					table.Value(),
				); tableErr != nil {
					t.Fatal(tableErr)
				}
				return table
			}
			install := func(
				event string,
				values ...Value,
			) {
				t.Helper()
				metatable, tableErr := state.NewTableWithCapacity(0, 1)
				if tableErr != nil {
					t.Fatal(tableErr)
				}
				if tableErr = metatable.RawSetString(
					event,
					failing.Value(),
				); tableErr != nil {
					t.Fatal(tableErr)
				}
				for _, value := range values {
					if tableErr = state.SetMetatable(
						value,
						metatable,
					); tableErr != nil {
						t.Fatal(tableErr)
					}
				}
			}

			switch test.event {
			case "__index", "__newindex":
				target := newTable("target")
				install(test.event, target.Value())
			case "__lt":
				left := newTable("left")
				right := newTable("right")
				install(test.event, left.Value(), right.Value())
			case "__concat":
				left := newTable("left")
				newTable("right")
				install(test.event, left.Value())
			case "":
				if err := state.RawSetGlobal(
					"iterator",
					failing.Value(),
				); err != nil {
					t.Fatal(err)
				}
			default:
				t.Fatalf("unknown event %q", test.event)
			}

			chunk := compileTestFunction(
				t,
				state,
				"@native-continuation-error.lua",
				test.source,
			)
			thread, result := executeTestFunction(t, state, chunk)
			if result.kind != executionFailed ||
				result.err == nil ||
				!strings.Contains(
					result.err.Error(),
					"continued native failure",
				) {
				t.Fatalf("execution result = %#v", result)
			}
			traceback := result.err.Traceback()
			if len(traceback) < 2 ||
				traceback[0].Source != "=[Go]" {
				t.Fatalf("traceback = %#v", traceback)
			}
			if thread.activeNativeToken != 0 ||
				len(thread.frames) != 0 ||
				len(thread.continuations) != 0 ||
				thread.top != 0 ||
				thread.frameExtent != 0 ||
				thread.openUpvalues != nil {
				t.Fatal("native continuation failure left executable state")
			}
		})
	}
}

func TestNativeCallbackPanicCleansEveryTransition(t *testing.T) {
	tests := []struct {
		name            string
		source          string
		afterTerminal   bool
		indexMetamethod bool
		remainingCalls  int
	}{
		{
			name: "ordinary call",
			source: `
local retained = 23
local result = host(retained)
return result
`,
			remainingCalls: 1,
		},
		{
			name: "after terminal outcome",
			source: `
local result = host()
return result
`,
			afterTerminal:  true,
			remainingCalls: 1,
		},
		{
			name:            "metamethod continuation",
			source:          `return target.missing`,
			indexMetamethod: true,
			remainingCalls:  1,
		},
		{
			name:           "root tail call",
			source:         `return host()`,
			remainingCalls: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, err := New(Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			host, err := state.NewNativeFunction(
				func(frame Frame) Outcome {
					if test.afterTerminal {
						_ = frame.ReturnNumber(1)
					}
					panic("host panic")
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := state.RawSetGlobal("host", host.Value()); err != nil {
				t.Fatal(err)
			}
			if test.indexMetamethod {
				target, tableErr := state.NewTableWithCapacity(0, 0)
				if tableErr != nil {
					t.Fatal(tableErr)
				}
				metatable, tableErr := state.NewTableWithCapacity(0, 1)
				if tableErr != nil {
					t.Fatal(tableErr)
				}
				if tableErr = metatable.RawSetString(
					"__index",
					host.Value(),
				); tableErr != nil {
					t.Fatal(tableErr)
				}
				if tableErr = state.SetMetatable(
					target.Value(),
					metatable,
				); tableErr != nil {
					t.Fatal(tableErr)
				}
				if tableErr = state.RawSetGlobal(
					"target",
					target.Value(),
				); tableErr != nil {
					t.Fatal(tableErr)
				}
			}

			caller := compileTestFunction(
				t,
				state,
				"@native-panic.lua",
				test.source,
			)
			thread := state.main
			thread.reserveValues(int(caller.prototype.registers))
			thread.values[0] = slotFromFunctionObject(caller)
			thread.top = 1
			if failure := thread.pushFunctionCall(
				caller,
				0,
				0,
				allResults,
			); failure != nil {
				t.Fatal(failure)
			}

			recovered := func() (recovered any) {
				defer func() {
					recovered = recover()
				}()
				_ = runTestExecutor(t, thread, 0)
				return nil
			}()
			if recovered != "host panic" {
				t.Fatalf("panic = %v; want host panic", recovered)
			}
			if thread.activeNativeToken != 0 ||
				len(thread.continuations) != 0 ||
				len(thread.frames) != test.remainingCalls {
				t.Fatal("callback panic left borrowed execution state")
			}
			if test.remainingCalls != 0 {
				frame := thread.frames[0]
				wantExtent := int(frame.base) +
					int(frame.function.prototype.registers)
				if frame.function != caller ||
					thread.top != wantExtent ||
					thread.frameExtent != wantExtent {
					t.Fatal("callback panic did not restore the Lua caller")
				}
			} else if thread.top != 0 || thread.frameExtent != 0 {
				t.Fatal("tail callback panic retained a root stack")
			}

			thread.unwindCalls(0)
			if thread.top != 0 ||
				thread.frameExtent != 0 ||
				thread.openUpvalues != nil {
				t.Fatal("callback panic cleanup left executable roots")
			}
			for _, value := range thread.values {
				if value != (slot{}) {
					t.Fatal("callback panic retained a dead stack value")
				}
			}
		})
	}
}

func TestExecutorUsesNativeFunctionsAtEveryCallSeam(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	native := func(entry NativeFunc) *Function {
		t.Helper()
		function, functionErr := state.NewNativeFunction(entry)
		if functionErr != nil {
			t.Fatal(functionErr)
		}
		return function
	}
	installMetamethod := func(value Value, name string, function *Function) {
		t.Helper()
		metatable, tableErr := state.NewTableWithCapacity(0, 1)
		if tableErr != nil {
			t.Fatal(tableErr)
		}
		if tableErr = metatable.RawSetString(name, function.Value()); tableErr != nil {
			t.Fatal(tableErr)
		}
		if tableErr = state.SetMetatable(value, metatable); tableErr != nil {
			t.Fatal(tableErr)
		}
	}
	newGlobalTable := func(name string) *Table {
		t.Helper()
		table, tableErr := state.NewTableWithCapacity(0, 1)
		if tableErr != nil {
			t.Fatal(tableErr)
		}
		if tableErr = state.RawSetGlobal(name, table.Value()); tableErr != nil {
			t.Fatal(tableErr)
		}
		return table
	}

	callable := newGlobalTable("callable")
	installMetamethod(
		callable.Value(),
		"__call",
		native(func(frame Frame) Outcome {
			value, ok := frame.Number(1)
			if !ok {
				frame.ThrowArgTypeError(1, NumberKind)
			}
			return frame.ReturnNumber(value + 1)
		}),
	)

	indexed := newGlobalTable("indexed")
	installMetamethod(
		indexed.Value(),
		"__index",
		native(func(frame Frame) Outcome {
			key, _ := frame.Argument(1)
			return frame.ReturnValue(key)
		}),
	)

	assigned := newGlobalTable("assigned")
	installMetamethod(
		assigned.Value(),
		"__newindex",
		native(func(frame Frame) Outcome {
			target, ok := frame.Table(0)
			if !ok {
				frame.ThrowArgTypeError(0, TableKind)
			}
			key, _ := frame.Argument(1)
			value, _ := frame.Argument(2)
			if setErr := target.RawSet(key, value); setErr != nil {
				frame.Throw(state.String(setErr.Error()))
			}
			return frame.Return()
		}),
	)

	addLeft := newGlobalTable("add_left")
	newGlobalTable("add_right")
	installMetamethod(
		addLeft.Value(),
		"__add",
		native(func(frame Frame) Outcome {
			return frame.ReturnNumber(42)
		}),
	)

	concatLeft := newGlobalTable("concat_left")
	newGlobalTable("concat_right")
	installMetamethod(
		concatLeft.Value(),
		"__concat",
		native(func(frame Frame) Outcome {
			return frame.ReturnString("joined")
		}),
	)

	lengthValue, err := state.NewUserData("length")
	if err != nil {
		t.Fatal(err)
	}
	if err := state.RawSetGlobal("length_value", lengthValue.Value()); err != nil {
		t.Fatal(err)
	}
	installMetamethod(
		lengthValue.Value(),
		"__len",
		native(func(frame Frame) Outcome {
			return frame.ReturnNumber(9)
		}),
	)

	lessThan := native(func(frame Frame) Outcome {
		return frame.ReturnBool(true)
	})
	compareMetatable, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareMetatable.RawSetString("__lt", lessThan.Value()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"compare_left", "compare_right"} {
		value := newGlobalTable(name)
		if err := state.SetMetatable(value.Value(), compareMetatable); err != nil {
			t.Fatal(err)
		}
	}

	iterator := native(func(frame Frame) Outcome {
		control, ok := frame.Number(1)
		if !ok {
			frame.ThrowArgTypeError(1, NumberKind)
		}
		control++
		if control > 3 {
			return frame.ReturnNil()
		}
		return frame.ReturnValues(
			Number(control),
			Number(control*2),
		)
	})
	if err := state.RawSetGlobal("iterator", iterator.Value()); err != nil {
		t.Fatal(err)
	}

	method := native(func(frame Frame) Outcome {
		if value, ok := frame.Table(0); !ok || value == nil {
			frame.ThrowArgTypeError(0, TableKind)
		}
		number, ok := frame.Number(1)
		if !ok {
			frame.ThrowArgTypeError(1, NumberKind)
		}
		return frame.ReturnNumber(number * 2)
	})
	receiver := newGlobalTable("receiver")
	if err := receiver.RawSetString("method", method.Value()); err != nil {
		t.Fatal(err)
	}

	chunk := compileTestFunction(t, state, "@native-seams.lua", `
assigned.saved = 6
local sum = 0
for key, value in iterator, nil, 0 do
	sum = sum + key + value
end
return callable(4),
	indexed.answer,
	assigned.saved,
	add_left + add_right,
	concat_left .. concat_right,
	#length_value,
	compare_left < compare_right,
	sum,
	receiver:method(8)
`)
	thread, result := executeTestFunction(t, state, chunk)
	assertExecutionReturned(t, result)
	assertExecutionValues(
		t,
		thread,
		Number(5),
		state.String("answer"),
		Number(6),
		Number(42),
		state.String("joined"),
		Number(9),
		Bool(true),
		Number(18),
		Number(16),
	)
	if len(thread.continuations) != 0 {
		t.Fatal("native calls retained an execution continuation")
	}
}

func TestExecutorCallsAndTailCallsNativeFunctions(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{name: "call", source: `return host_add(20, 22)`},
		{name: "tail call", source: `local function run() return host_add(20, 22) end return run()`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, err := New(Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			add, err := state.NewNativeFunction(
				func(frame Frame) Outcome {
					left, leftOK := frame.Number(0)
					if !leftOK {
						frame.ThrowArgTypeError(0, NumberKind)
					}
					right, rightOK := frame.Number(1)
					if !rightOK {
						frame.ThrowArgTypeError(1, NumberKind)
					}
					return frame.ReturnNumber(left + right)
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := state.RawSetGlobal("host_add", add.Value()); err != nil {
				t.Fatal(err)
			}
			chunk := compileTestFunction(t, state, "@native.lua", test.source)
			thread, result := executeTestFunction(t, state, chunk)
			assertExecutionReturned(t, result)
			assertExecutionValues(t, thread, Number(42))
		})
	}
}

func BenchmarkExecutorNativeCall(b *testing.B) {
	const iterations = 1000
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = state.Close()
	})

	native, err := state.NewNativeFunction(
		func(frame Frame) Outcome {
			value, ok := frame.Number(0)
			if !ok {
				frame.ThrowArgTypeError(0, NumberKind)
			}
			return frame.ReturnNumber(value + 1)
		},
	)
	if err != nil {
		b.Fatal(err)
	}
	luaFunction := compileTestFunction(
		b,
		state,
		"@lua-callee.lua",
		`local value = ...; return value + 1`,
	)
	caller := compileTestFunction(b, state, "@native-call.lua", `
local target, iterations = ...
local value = 0
for _ = 1, iterations do
	value = target(value)
end
return value
`)

	for _, test := range []struct {
		name   string
		target Value
	}{
		{name: "native", target: native.Value()},
		{name: "lua control", target: luaFunction.owningValue()},
	} {
		b.Run(test.name, func(b *testing.B) {
			b.ReportMetric(iterations, "calls/op")
			benchmarkExecutorFunction(
				b,
				state,
				caller,
				test.target,
				Number(iterations),
			)
		})
	}
}
