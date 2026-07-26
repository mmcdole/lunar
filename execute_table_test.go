package lua

import (
	"math"
	"strings"
	"testing"
)

func TestExecutorRunsRawTableOperationsAndConstructors(t *testing.T) {
	const source = `
local function tail()
	return 3, nil, 5
end
local table = {
	name = "badger",
	[false] = 17,
	1,
	2,
	tail(),
}
local nan = 0 / 0
return table[1], table[2], table[3], table[4], table[5],
	table.name, table[false], table.missing, table[nil], table[nan]
`
	state, thread, result := executeTestChunk(t, source)
	defer state.Close()
	assertExecutionReturned(t, result)
	assertExecutionValues(
		t,
		thread,
		Number(1),
		Number(2),
		Number(3),
		Nil(),
		Number(5),
		state.String("badger"),
		Number(17),
		Nil(),
		Nil(),
		Nil(),
	)
}

func TestExecutorTableWriteRejectsInvalidKeysBeforeMetamethods(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		message string
	}{
		{
			name:    "nil",
			source:  `local table = ...; table[nil] = 1`,
			message: "table index is nil",
		},
		{
			name:    "NaN",
			source:  `local table = ...; table[0 / 0] = 1`,
			message: "table index is NaN",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, err := New(Options{MaxFrames: 1})
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			handler := compileTestFunction(
				t,
				state,
				"@handler.lua",
				`local invalid = 1; return invalid()`,
			)
			metatable, err := state.NewTable(0, 1)
			if err != nil {
				t.Fatal(err)
			}
			if err := metatable.RawSetString(
				"__newindex",
				handler.Value(),
			); err != nil {
				t.Fatal(err)
			}
			target, err := state.NewTable(0, 0)
			if err != nil {
				t.Fatal(err)
			}
			if err := state.SetMetatable(
				target.Value(),
				metatable,
			); err != nil {
				t.Fatal(err)
			}
			caller := compileTestFunction(
				t,
				state,
				"@caller.lua",
				test.source,
			)

			_, result := executeTestFunction(
				t,
				state,
				caller,
				target.Value(),
			)
			if result.kind != executionFailed ||
				result.err == nil ||
				!strings.Contains(result.err.Error(), test.message) {
				t.Fatalf("invalid-key result = %+v", result)
			}
		})
	}
}

func TestExecutorRawTablePathObservesMetamethodChanges(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	getterTarget, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	getterMetatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(
		getterTarget.Value(),
		getterMetatable,
	); err != nil {
		t.Fatal(err)
	}
	getterCaller := compileTestFunction(t, state, "@get.lua", `
local target = ...
return target.missing
`)
	thread, result := executeTestFunction(
		t,
		state,
		getterCaller,
		getterTarget.Value(),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Nil())
	if getterMetatable.absentMetamethods&metaIndex.bit() == 0 {
		t.Fatal("initial read did not cache absent __index")
	}

	getter := compileTestFunction(t, state, "@index.lua", `return 41`)
	if err := getterMetatable.RawSetString(
		"__index",
		getter.Value(),
	); err != nil {
		t.Fatal(err)
	}
	thread, result = executeTestFunction(
		t,
		state,
		getterCaller,
		getterTarget.Value(),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Number(41))

	replacementGetter := compileTestFunction(
		t,
		state,
		"@replacement-index.lua",
		`return 42`,
	)
	replacementMetatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := replacementMetatable.RawSetString(
		"__index",
		replacementGetter.Value(),
	); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(
		getterTarget.Value(),
		replacementMetatable,
	); err != nil {
		t.Fatal(err)
	}
	thread, result = executeTestFunction(
		t,
		state,
		getterCaller,
		getterTarget.Value(),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Number(42))

	setterTarget, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := setterTarget.RawSetString("recorded", Number(0)); err != nil {
		t.Fatal(err)
	}
	setterMetatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(
		setterTarget.Value(),
		setterMetatable,
	); err != nil {
		t.Fatal(err)
	}
	setterCaller := compileTestFunction(t, state, "@set.lua", `
local target = ...
target.missing = nil
return target.recorded
`)
	thread, result = executeTestFunction(
		t,
		state,
		setterCaller,
		setterTarget.Value(),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Number(0))
	if setterMetatable.absentMetamethods&metaNewIndex.bit() == 0 {
		t.Fatal("initial write did not cache absent __newindex")
	}

	setter := compileTestFunction(t, state, "@newindex.lua", `
local target = ...
target.recorded = 23
`)
	if err := setterMetatable.RawSetString(
		"__newindex",
		setter.Value(),
	); err != nil {
		t.Fatal(err)
	}
	thread, result = executeTestFunction(
		t,
		state,
		setterCaller,
		setterTarget.Value(),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Number(23))
}

func TestExecutorIndexMetamethodSemantics(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	getter := compileTestFunction(t, state, "@getter.lua", `
local target, key = ...
return key, target
`)
	metatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString("__index", getter.Value()); err != nil {
		t.Fatal(err)
	}
	target, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(target.Value(), metatable); err != nil {
		t.Fatal(err)
	}
	caller := compileTestFunction(t, state, "@caller.lua", `
local target, key = ...
return target[key]
`)

	thread, result := executeTestFunction(
		t,
		state,
		caller,
		target.Value(),
		state.String("missing"),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, state.String("missing"))

	noResult := compileTestFunction(t, state, "@empty.lua", `return`)
	if err := metatable.RawSetString(
		"__index",
		noResult.Value(),
	); err != nil {
		t.Fatal(err)
	}
	thread, result = executeTestFunction(
		t,
		state,
		caller,
		target.Value(),
		state.String("absent"),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Nil())

	if err := target.RawSetString("present", Number(42)); err != nil {
		t.Fatal(err)
	}
	trap := compileTestFunction(t, state, "@trap.lua", `
local invalid = 1
return invalid()
`)
	if err := metatable.RawSetString("__index", trap.Value()); err != nil {
		t.Fatal(err)
	}
	thread, result = executeTestFunction(
		t,
		state,
		caller,
		target.Value(),
		state.String("present"),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Number(42))

	proxy, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := proxy.RawSetString("field", Number(19)); err != nil {
		t.Fatal(err)
	}
	callableMetatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := callableMetatable.RawSetString(
		"__call",
		trap.Value(),
	); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(
		proxy.Value(),
		callableMetatable,
	); err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString("__index", proxy.Value()); err != nil {
		t.Fatal(err)
	}
	thread, result = executeTestFunction(
		t,
		state,
		caller,
		target.Value(),
		state.String("field"),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Number(19))

	proxyMetatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	receiverGetter := compileTestFunction(
		t,
		state,
		"@receiver-getter.lua",
		`local receiver = ...; return receiver`,
	)
	if err := proxyMetatable.RawSetString(
		"__index",
		receiverGetter.Value(),
	); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(proxy.Value(), proxyMetatable); err != nil {
		t.Fatal(err)
	}
	thread, result = executeTestFunction(
		t,
		state,
		caller,
		target.Value(),
		state.String("another"),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, proxy.Value())

	scalarMetatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := scalarMetatable.RawSetString(
		"__index",
		getter.Value(),
	); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(Number(0), scalarMetatable); err != nil {
		t.Fatal(err)
	}
	thread, result = executeTestFunction(
		t,
		state,
		caller,
		Number(7),
		Nil(),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Nil())

	plainMetatable, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	parentMetatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := parentMetatable.RawSetString(
		"__index",
		getter.Value(),
	); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(
		plainMetatable.Value(),
		parentMetatable,
	); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(
		target.Value(),
		plainMetatable,
	); err != nil {
		t.Fatal(err)
	}
	thread, result = executeTestFunction(
		t,
		state,
		caller,
		target.Value(),
		state.String("not synthesized"),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Nil())
}

func TestExecutorNewIndexMetamethodSemantics(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	sink, err := state.NewTable(3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("sink", sink.Value()); err != nil {
		t.Fatal(err)
	}
	setter := compileTestFunction(t, state, "@setter.lua", `
local target, key, value = ...
sink[1] = target
sink[2] = key
sink[3] = value
return "ignored", "also ignored"
`)
	metatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString(
		"__newindex",
		setter.Value(),
	); err != nil {
		t.Fatal(err)
	}
	target, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(target.Value(), metatable); err != nil {
		t.Fatal(err)
	}
	caller := compileTestFunction(t, state, "@caller.lua", `
local target, key, value = ...
target[key] = value
return target, key, value
`)

	thread, result := executeTestFunction(
		t,
		state,
		caller,
		target.Value(),
		state.String("missing"),
		Number(31),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(
		t,
		thread,
		target.Value(),
		state.String("missing"),
		Number(31),
	)
	assertTestSlot(t, slotFromValue(sink.RawGetInt(1)), target.Value())
	assertTestSlot(
		t,
		slotFromValue(sink.RawGetInt(2)),
		state.String("missing"),
	)
	assertTestSlot(t, slotFromValue(sink.RawGetInt(3)), Number(31))
	if got := target.RawGetString("missing"); !got.IsNil() {
		t.Fatalf("setter handler also assigned target = %v", got)
	}

	thread, result = executeTestFunction(
		t,
		state,
		caller,
		target.Value(),
		state.String("deleted"),
		Nil(),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(
		t,
		thread,
		target.Value(),
		state.String("deleted"),
		Nil(),
	)
	if got := sink.RawGetInt(3); !got.IsNil() {
		t.Fatalf("nil setter argument = %v", got)
	}

	if err := target.RawSetString("present", Number(1)); err != nil {
		t.Fatal(err)
	}
	if err := sink.RawSetInt(1, state.String("unchanged")); err != nil {
		t.Fatal(err)
	}
	thread, result = executeTestFunction(
		t,
		state,
		caller,
		target.Value(),
		state.String("present"),
		Number(2),
	)
	assertExecutionReturned(t, result)
	if got, ok := target.RawGetString("present").AsNumber(); !ok || got != 2 {
		t.Fatalf("existing-key update = (%v, %v)", got, ok)
	}
	if got, _ := sink.RawGetInt(1).AsString(); got != "unchanged" {
		t.Fatalf("existing-key update called __newindex: %q", got)
	}

	if err := state.SetMetatable(Number(0), metatable); err != nil {
		t.Fatal(err)
	}
	thread, result = executeTestFunction(
		t,
		state,
		caller,
		Number(7),
		Nil(),
		Number(9),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Number(7), Nil(), Number(9))
	if got, ok := sink.RawGetInt(1).AsNumber(); !ok || got != 7 {
		t.Fatalf("scalar setter target = (%v, %v)", got, ok)
	}
	if got := sink.RawGetInt(2); !got.IsNil() {
		t.Fatalf("scalar setter nil key = %v", got)
	}

	proxy, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	trap := compileTestFunction(t, state, "@trap.lua", `
local invalid = 1
return invalid()
`)
	proxyMetatable, err := state.NewTable(0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := proxyMetatable.RawSetString(
		"__call",
		trap.Value(),
	); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(
		proxy.Value(),
		proxyMetatable,
	); err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString(
		"__newindex",
		proxy.Value(),
	); err != nil {
		t.Fatal(err)
	}
	thread, result = executeTestFunction(
		t,
		state,
		caller,
		target.Value(),
		state.String("delegated"),
		Number(44),
	)
	assertExecutionReturned(t, result)
	if got, ok := proxy.RawGetString("delegated").AsNumber(); !ok ||
		got != 44 {
		t.Fatalf("delegated setter result = (%v, %v)", got, ok)
	}

	if err := proxyMetatable.RawSetString(
		"__newindex",
		setter.Value(),
	); err != nil {
		t.Fatal(err)
	}
	thread, result = executeTestFunction(
		t,
		state,
		caller,
		target.Value(),
		state.String("through proxy"),
		Number(45),
	)
	assertExecutionReturned(t, result)
	assertTestSlot(t, slotFromValue(sink.RawGetInt(1)), proxy.Value())
	assertTestSlot(
		t,
		slotFromValue(sink.RawGetInt(2)),
		state.String("through proxy"),
	)
	assertTestSlot(t, slotFromValue(sink.RawGetInt(3)), Number(45))
}

func TestExecutorTableMetamethodDelegationLimit(t *testing.T) {
	operations := []struct {
		name    string
		event   string
		source  string
		message string
	}{
		{
			name:    "index",
			event:   "__index",
			source:  `local target = ...; return target.field`,
			message: "loop in gettable",
		},
		{
			name:    "newindex",
			event:   "__newindex",
			source:  `local target = ...; target.field = 88; return target`,
			message: "loop in settable",
		},
	}
	limits := []struct {
		name        string
		targetCount int
		wantFailure bool
	}{
		{name: "99 hops resolve", targetCount: 100},
		{name: "100 hops fail", targetCount: 101, wantFailure: true},
	}

	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			for _, limit := range limits {
				t.Run(limit.name, func(t *testing.T) {
					state, err := New(Options{})
					if err != nil {
						t.Fatal(err)
					}
					defer state.Close()
					targets := make([]*Table, limit.targetCount)
					for index := range targets {
						targets[index], err = state.NewTable(0, 0)
						if err != nil {
							t.Fatal(err)
						}
					}
					for index := 0; index+1 < len(targets); index++ {
						metatable, tableErr := state.NewTable(0, 1)
						if tableErr != nil {
							t.Fatal(tableErr)
						}
						if tableErr = metatable.RawSetString(
							operation.event,
							targets[index+1].Value(),
						); tableErr != nil {
							t.Fatal(tableErr)
						}
						if tableErr = state.SetMetatable(
							targets[index].Value(),
							metatable,
						); tableErr != nil {
							t.Fatal(tableErr)
						}
					}
					if operation.event == "__index" {
						if err := targets[len(targets)-1].RawSetString(
							"field",
							Number(88),
						); err != nil {
							t.Fatal(err)
						}
					}
					caller := compileTestFunction(
						t,
						state,
						"@chain.lua",
						operation.source,
					)
					thread, result := executeTestFunction(
						t,
						state,
						caller,
						targets[0].Value(),
					)
					if limit.wantFailure {
						if result.kind != executionFailed ||
							result.err == nil ||
							!strings.Contains(
								result.err.Error(),
								operation.message,
							) {
							t.Fatalf("chain result = %+v", result)
						}
						return
					}
					assertExecutionReturned(t, result)
					if operation.event == "__index" {
						assertExecutionValues(t, thread, Number(88))
						return
					}
					assertExecutionValues(
						t,
						thread,
						targets[0].Value(),
					)
					if got, ok := targets[len(targets)-1].
						RawGetString("field").
						AsNumber(); !ok || got != 88 {
						t.Fatalf(
							"terminal set = (%v, %v)",
							got,
							ok,
						)
					}
				})
			}
		})
	}
}

func TestExecutorNewIndexFailureClearsContinuation(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	handler := compileTestFunction(t, state, "@setter.lua", `
local invalid = 1
return invalid()
`)
	metatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString(
		"__newindex",
		handler.Value(),
	); err != nil {
		t.Fatal(err)
	}
	target, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(target.Value(), metatable); err != nil {
		t.Fatal(err)
	}
	caller := compileTestFunction(t, state, "@caller.lua", `
local target = ...
target.missing = 1
return target
`)

	thread, result := executeTestFunction(
		t,
		state,
		caller,
		target.Value(),
	)
	if result.kind != executionFailed || result.err == nil {
		t.Fatalf("setter failure = %+v", result)
	}
	if len(thread.frames) != 0 ||
		len(thread.continuations) != 0 ||
		thread.top != 0 ||
		thread.frameExtent != 0 {
		t.Fatal("setter failure retained execution state")
	}
	if got := target.RawGetString("missing"); !got.IsNil() {
		t.Fatalf("failed setter also assigned %v", got)
	}
	traceback := result.err.Traceback()
	if len(traceback) != 2 ||
		traceback[0].Source != "@setter.lua" ||
		traceback[1].Source != "@caller.lua" {
		t.Fatalf("setter traceback = %+v", traceback)
	}
}

func TestExecutorNewIndexContinuationSupportsNestedAndTailCalls(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	sink, err := state.NewTable(2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("sink", sink.Value()); err != nil {
		t.Fatal(err)
	}
	inner := compileTestFunction(t, state, "@inner-setter.lua", `
local target, key, value = ...
sink[1] = key
sink[2] = value
`)
	innerMetatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := innerMetatable.RawSetString(
		"__newindex",
		inner.Value(),
	); err != nil {
		t.Fatal(err)
	}
	nested, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(
		nested.Value(),
		innerMetatable,
	); err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("nested", nested.Value()); err != nil {
		t.Fatal(err)
	}
	outer := compileTestFunction(t, state, "@outer-setter.lua", `
local target, key, value = ...
nested.forwarded = value
`)
	outerMetatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := outerMetatable.RawSetString(
		"__newindex",
		outer.Value(),
	); err != nil {
		t.Fatal(err)
	}
	target, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(
		target.Value(),
		outerMetatable,
	); err != nil {
		t.Fatal(err)
	}
	caller := compileTestFunction(t, state, "@caller.lua", `
local target, key, value = ...
target[key] = value
return target
`)

	thread, result := executeTestFunction(
		t,
		state,
		caller,
		target.Value(),
		state.String("outer"),
		Number(77),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, target.Value())
	assertTestSlot(
		t,
		slotFromValue(sink.RawGetInt(1)),
		state.String("forwarded"),
	)
	assertTestSlot(t, slotFromValue(sink.RawGetInt(2)), Number(77))
	if len(thread.continuations) != 0 {
		t.Fatal("nested setters retained continuations")
	}

	tailHandler := compileTestFunction(t, state, "@tail-setter.lua", `
local function relay(...)
	return ...
end
return relay(...)
`)
	if err := outerMetatable.RawSetString(
		"__newindex",
		tailHandler.Value(),
	); err != nil {
		t.Fatal(err)
	}
	thread, result = executeTestFunction(
		t,
		state,
		caller,
		target.Value(),
		state.String("tail"),
		Number(78),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, target.Value())
	if got := target.RawGetString("tail"); !got.IsNil() {
		t.Fatalf("tail-called setter also assigned %v", got)
	}
	if len(thread.continuations) != 0 {
		t.Fatal("tail-called setter retained a continuation")
	}
}

func TestExecutorGlobalsUseFunctionEnvironment(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	function := compileTestFunction(t, state, "@environment.lua", `
answer = ...
return answer
`)
	environment, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetFunctionEnvironment(
		function,
		environment,
	); err != nil {
		t.Fatal(err)
	}

	thread, result := executeTestFunction(
		t,
		state,
		function,
		Number(42),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Number(42))
	if got, ok := environment.RawGetString("answer").AsNumber(); !ok ||
		got != 42 {
		t.Fatalf("custom environment answer = (%v, %v)", got, ok)
	}
	global, err := state.Global("answer")
	if err != nil {
		t.Fatal(err)
	}
	if !global.IsNil() {
		t.Fatalf("state global was changed through custom environment: %v", global)
	}

	proxy, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := proxy.RawSetString("provided", Number(73)); err != nil {
		t.Fatal(err)
	}
	metatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString("__index", proxy.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(
		environment.Value(),
		metatable,
	); err != nil {
		t.Fatal(err)
	}
	getter := compileTestFunction(
		t,
		state,
		"@environment-get.lua",
		`return provided`,
	)
	if err := state.SetFunctionEnvironment(
		getter,
		environment,
	); err != nil {
		t.Fatal(err)
	}
	thread, result = executeTestFunction(t, state, getter)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Number(73))

	forwarded, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	forwardingMetatable, err := state.NewTable(0, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"__index", "__newindex"} {
		if err := forwardingMetatable.RawSetString(
			event,
			forwarded.Value(),
		); err != nil {
			t.Fatal(err)
		}
	}
	forwardingEnvironment, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(
		forwardingEnvironment.Value(),
		forwardingMetatable,
	); err != nil {
		t.Fatal(err)
	}
	forwardingFunction := compileTestFunction(
		t,
		state,
		"@environment-forward.lua",
		`forwarded = ...; return forwarded`,
	)
	if err := state.SetFunctionEnvironment(
		forwardingFunction,
		forwardingEnvironment,
	); err != nil {
		t.Fatal(err)
	}
	thread, result = executeTestFunction(
		t,
		state,
		forwardingFunction,
		Number(64),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, Number(64))
	if got, ok := forwarded.RawGetString("forwarded").AsNumber(); !ok ||
		got != 64 {
		t.Fatalf("forwarded global = (%v, %v)", got, ok)
	}
}

func TestExecutorSelfPreservesReceiverAndPUCOverlapOrder(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	method := compileTestFunction(t, state, "@method.lua", `
local self, argument = ...
return self, argument
`)
	target, err := state.NewTable(0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := target.RawSetString("method", method.Value()); err != nil {
		t.Fatal(err)
	}
	caller := compileTestFunction(t, state, "@caller.lua", `
local target = ...
return target:method(7)
`)
	thread, result := executeTestFunction(
		t,
		state,
		caller,
		target.Value(),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, target.Value(), Number(7))

	if err := target.RawSet(
		target.Value(),
		state.String("overlap"),
	); err != nil {
		t.Fatal(err)
	}
	builder := testPrototypeBuilder(
		makeABC(opSelf, 0, 0, 1),
		makeABC(opReturn, 0, 3, 0),
	)
	builder.parameters = 2
	builder.registers = 2
	prototype, syntaxError := builder.seal()
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	overlap := newLuaFunction(
		state.runtime,
		prototype,
		state.main.globals,
		nil,
	)
	thread, result = executeTestFunction(
		t,
		state,
		overlap,
		target.Value(),
		state.String("discarded key"),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(
		t,
		thread,
		state.String("overlap"),
		target.Value(),
	)

	if err := target.RawSet(target.Value(), Nil()); err != nil {
		t.Fatal(err)
	}
	index := compileTestFunction(t, state, "@overlap-index.lua", `
local _, key = ...
return key
`)
	metatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString("__index", index.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(target.Value(), metatable); err != nil {
		t.Fatal(err)
	}
	thread, result = executeTestFunction(
		t,
		state,
		overlap,
		target.Value(),
		state.String("discarded key"),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, target.Value(), target.Value())
}

func TestExecutorTableOperandsMayShareRegisters(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	target, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := target.RawSet(
		target.Value(),
		state.String("shared"),
	); err != nil {
		t.Fatal(err)
	}

	builder := testPrototypeBuilder(
		makeABC(opGetTable, 0, 0, 0),
		makeABC(opReturn, 0, 2, 0),
	)
	builder.parameters = 1
	builder.registers = 1
	prototype, syntaxError := builder.seal()
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	function := newLuaFunction(
		state.runtime,
		prototype,
		state.main.globals,
		nil,
	)
	thread, result := executeTestFunction(
		t,
		state,
		function,
		target.Value(),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, state.String("shared"))

	builder = testPrototypeBuilder(
		makeABC(opSetTable, 0, 0, 0),
		makeABC(opReturn, 0, 2, 0),
	)
	builder.parameters = 1
	builder.registers = 1
	prototype, syntaxError = builder.seal()
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	function = newLuaFunction(
		state.runtime,
		prototype,
		state.main.globals,
		nil,
	)
	thread, result = executeTestFunction(
		t,
		state,
		function,
		target.Value(),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, target.Value())
	stored, err := target.RawGet(target.Value())
	if err != nil {
		t.Fatal(err)
	}
	same, applicable := stored.SameObject(target.Value())
	if !applicable || !same {
		t.Fatalf("all-overlap SETTABLE stored %v", stored)
	}
}

func TestExecutorDecodesTableHintsAndExtendedSetList(t *testing.T) {
	if got := tableCapacityHint(255); got != maxTableHint {
		t.Fatalf("largest table hint = %d; want %d", got, maxTableHint)
	}
	arrayHint := 19
	recordHint := 11
	builder := testPrototypeBuilder(
		makeABC(
			opNewTable,
			0,
			intToFloatingByte(arrayHint),
			intToFloatingByte(recordHint),
		),
		makeABC(opReturn, 0, 2, 0),
	)
	builder.registers = 1
	prototype, syntaxError := builder.seal()
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	function := newLuaFunction(
		state.runtime,
		prototype,
		state.main.globals,
		nil,
	)
	thread, result := executeTestFunction(t, state, function)
	assertExecutionReturned(t, result)
	table, ok := thread.values[0].owningValue().Table()
	if !ok {
		t.Fatal("NEWTABLE did not return a table")
	}
	if cap(table.array) < arrayHint ||
		len(table.store.entries) < recordHint {
		t.Fatalf(
			"decoded capacities = array %d, record %d",
			cap(table.array),
			len(table.store.entries),
		)
	}
	if table.arrayUsed != 0 || table.store.live != 0 {
		t.Fatal("capacity hints changed table contents")
	}

	const block = maxOperandC + 1
	builder = testPrototypeBuilder(
		makeABC(opNewTable, 0, 0, 0),
		makeABx(opLoadK, 1, 0),
		makeABC(opSetList, 0, 1, 0),
		instruction(block),
		makeABC(opReturn, 0, 2, 0),
	)
	builder.registers = 2
	builder.constants = []slot{numberSlot(55)}
	prototype, syntaxError = builder.seal()
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	function = newLuaFunction(
		state.runtime,
		prototype,
		state.main.globals,
		nil,
	)
	thread, result = executeTestFunction(t, state, function)
	assertExecutionReturned(t, result)
	table, ok = thread.values[0].owningValue().Table()
	if !ok {
		t.Fatal("extended SETLIST did not return a table")
	}
	index := (block-1)*fieldsPerFlush + 1
	if got, ok := table.RawGetInt(index).AsNumber(); !ok || got != 55 {
		t.Fatalf("extended SETLIST[%d] = (%v, %v)", index, got, ok)
	}
}

func TestExecutorSetListIsRaw(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	trap := compileTestFunction(t, state, "@trap.lua", `
local invalid = 1
return invalid()
`)
	metatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString(
		"__newindex",
		trap.Value(),
	); err != nil {
		t.Fatal(err)
	}
	target, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(target.Value(), metatable); err != nil {
		t.Fatal(err)
	}
	builder := testPrototypeBuilder(
		makeABC(opSetList, 0, 1, 1),
		makeABC(opReturn, 0, 2, 0),
	)
	builder.parameters = 2
	builder.registers = 2
	prototype, syntaxError := builder.seal()
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	function := newLuaFunction(
		state.runtime,
		prototype,
		state.main.globals,
		nil,
	)

	thread, result := executeTestFunction(
		t,
		state,
		function,
		target.Value(),
		Number(91),
	)
	assertExecutionReturned(t, result)
	assertExecutionValues(t, thread, target.Value())
	if got, ok := target.RawGetInt(1).AsNumber(); !ok || got != 91 {
		t.Fatalf("raw SETLIST result = (%v, %v)", got, ok)
	}
}

func TestExecutorOpenSetListChecksIndexBeforeMutation(t *testing.T) {
	const block = maxSetListIndex/fieldsPerFlush + 1
	first := (block-1)*fieldsPerFlush + 1
	count := maxSetListIndex - first + 2

	builder := testPrototypeBuilder(
		makeABC(opVararg, 1, 0, 0),
		makeABC(opSetList, 0, 0, 0),
		instruction(block),
		makeABC(opReturn, 0, 2, 0),
	)
	builder.parameters = 1
	builder.registers = 2
	builder.varargFlags = varargIsVararg
	prototype, syntaxError := builder.seal()
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	target, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	function := newLuaFunction(
		state.runtime,
		prototype,
		state.main.globals,
		nil,
	)
	arguments := make([]Value, count+1)
	arguments[0] = target.Value()
	for index := 0; index < count; index++ {
		arguments[index+1] = Number(float64(index + 1))
	}

	_, result := executeTestFunction(
		t,
		state,
		function,
		arguments...,
	)
	if result.kind != executionFailed ||
		result.err == nil ||
		result.err.Category() != ResourceError ||
		!strings.Contains(result.err.Error(), "SETLIST index") {
		t.Fatalf("open SETLIST result = %+v", result)
	}
	if target.arrayUsed != 0 || target.store.live != 0 {
		t.Fatal("failed open SETLIST partially mutated its table")
	}
}

func TestExecutorTableKeyClassificationDoesNotUseCapacityLimit(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	table, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	key := maxTableHint + 1
	table.growArray(key)
	writeSlot(&table.array[key-1], numberSlot(17))
	table.arrayUsed = 1
	got, err := table.RawGet(Number(float64(key)))
	if err != nil {
		t.Fatal(err)
	}
	if number, ok := got.AsNumber(); !ok || number != 17 {
		t.Fatalf("generic lookup above hint limit = (%v, %v)", number, ok)
	}
	previous := numberSlot(float64(key))
	if _, _, _, err := table.next(previous); err != nil {
		t.Fatalf("continuation above hint limit: %v", err)
	}

	nan := Number(math.NaN())
	if got, err := table.RawGet(nan); err != nil || !got.IsNil() {
		t.Fatalf("NaN read = (%v, %v)", got, err)
	}
}

func TestExecutorWarmTablePathsDoNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	raw, err := state.NewTable(2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.RawSetInt(1, Number(1)); err != nil {
		t.Fatal(err)
	}
	if err := raw.RawSetString("field", Number(1)); err != nil {
		t.Fatal(err)
	}

	getter := compileTestFunction(t, state, "@getter.lua", `
local target, key = ...
return key
`)
	getterMetatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := getterMetatable.RawSetString(
		"__index",
		getter.Value(),
	); err != nil {
		t.Fatal(err)
	}
	getterTarget, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(
		getterTarget.Value(),
		getterMetatable,
	); err != nil {
		t.Fatal(err)
	}

	setter := compileTestFunction(t, state, "@setter.lua", `return`)
	setterMetatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := setterMetatable.RawSetString(
		"__newindex",
		setter.Value(),
	); err != nil {
		t.Fatal(err)
	}
	setterTarget, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(
		setterTarget.Value(),
		setterMetatable,
	); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		function  *Function
		arguments []slot
	}{
		{
			name: "dense",
			function: compileTestFunction(t, state, "@dense.lua", `
local target = ...
target[1] = 2
return target[1]
`),
			arguments: []slot{slotFromValue(raw.Value())},
		},
		{
			name: "string",
			function: compileTestFunction(t, state, "@string.lua", `
local target = ...
target.field = 2
return target.field
`),
			arguments: []slot{slotFromValue(raw.Value())},
		},
		{
			name: "global",
			function: compileTestFunction(t, state, "@global.lua", `
global = 2
return global
`),
		},
		{
			name: "index metamethod",
			function: compileTestFunction(t, state, "@index.lua", `
local target, key = ...
return target[key]
`),
			arguments: []slot{
				slotFromValue(getterTarget.Value()),
				slotFromValue(state.String("missing")),
			},
		},
		{
			name: "newindex metamethod",
			function: compileTestFunction(t, state, "@newindex.lua", `
local target = ...
target.missing = 2
return target
`),
			arguments: []slot{slotFromValue(setterTarget.Value())},
		},
	}

	thread := state.MainThread()
	thread.reserveValues(64)
	thread.reserveFrames(8)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			benchmarkRunExecutor(
				thread,
				test.function,
				test.arguments,
			)
			allocations := testing.AllocsPerRun(1000, func() {
				benchmarkRunExecutor(
					thread,
					test.function,
					test.arguments,
				)
			})
			if allocations != 0 {
				t.Fatalf("warm path allocated %.2f times", allocations)
			}
		})
	}
}

func BenchmarkExecutorDenseTableLoop(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = state.Close()
	})
	table, err := state.NewTable(128, 0)
	if err != nil {
		b.Fatal(err)
	}
	function := compileTestFunction(b, state, "@dense-table.lua", `
local table = ...
local sum = 0
for index = 1, 100 do
	table[index] = index
	sum = sum + table[index]
end
return sum
`)
	benchmarkExecutorFunction(b, state, function, table.Value())
}

func BenchmarkExecutorStringFieldLoop(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = state.Close()
	})
	table, err := state.NewTable(0, 1)
	if err != nil {
		b.Fatal(err)
	}
	function := compileTestFunction(b, state, "@string-table.lua", `
local table = ...
local sum = 0
for index = 1, 100 do
	table.value = index
	sum = sum + table.value
end
return sum
`)
	benchmarkExecutorFunction(b, state, function, table.Value())
}

func BenchmarkExecutorStringFieldReadLoop(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = state.Close()
	})
	table, err := state.NewTable(0, 1)
	if err != nil {
		b.Fatal(err)
	}
	if err := table.RawSetString("value", Number(1)); err != nil {
		b.Fatal(err)
	}
	function := compileTestFunction(b, state, "@string-read.lua", `
local table = ...
local sum = 0
for index = 1, 100 do
	sum = sum + table.value
end
return sum
`)
	benchmarkExecutorFunction(b, state, function, table.Value())
}

func BenchmarkExecutorStringFieldWriteLoop(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = state.Close()
	})
	table, err := state.NewTable(0, 1)
	if err != nil {
		b.Fatal(err)
	}
	if err := table.RawSetString("value", Number(0)); err != nil {
		b.Fatal(err)
	}
	function := compileTestFunction(b, state, "@string-write.lua", `
local table = ...
for index = 1, 100 do
	table.value = index
end
return table.value
`)
	benchmarkExecutorFunction(b, state, function, table.Value())
}

func BenchmarkExecutorMissingFieldLoop(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = state.Close()
	})
	table, err := state.NewTable(0, 0)
	if err != nil {
		b.Fatal(err)
	}
	function := compileTestFunction(b, state, "@missing-field.lua", `
local table = ...
local value
for index = 1, 100 do
	value = table.missing
end
return value
`)
	benchmarkExecutorFunction(b, state, function, table.Value())
}

func BenchmarkExecutorPolymorphicFieldLoop(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = state.Close()
	})
	left, err := state.NewTable(0, 1)
	if err != nil {
		b.Fatal(err)
	}
	right, err := state.NewTable(0, 1)
	if err != nil {
		b.Fatal(err)
	}
	if err := left.RawSetString("value", Number(1)); err != nil {
		b.Fatal(err)
	}
	if err := right.RawSetString("value", Number(2)); err != nil {
		b.Fatal(err)
	}
	function := compileTestFunction(b, state, "@polymorphic-field.lua", `
local left, right = ...
local sum = 0
for index = 1, 100 do
	local target = left
	if index % 2 == 0 then
		target = right
	end
	sum = sum + target.value
end
return sum
`)
	benchmarkExecutorFunction(
		b,
		state,
		function,
		left.Value(),
		right.Value(),
	)
}

func BenchmarkExecutorIndexMetamethodLoop(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = state.Close()
	})
	getter := compileTestFunction(b, state, "@index.lua", `return 1`)
	metatable, err := state.NewTable(0, 1)
	if err != nil {
		b.Fatal(err)
	}
	if err := metatable.RawSetString("__index", getter.Value()); err != nil {
		b.Fatal(err)
	}
	table, err := state.NewTable(0, 0)
	if err != nil {
		b.Fatal(err)
	}
	if err := state.SetMetatable(table.Value(), metatable); err != nil {
		b.Fatal(err)
	}
	function := compileTestFunction(b, state, "@index-loop.lua", `
local table = ...
local sum = 0
for index = 1, 100 do
	sum = sum + table.missing
end
return sum
`)
	benchmarkExecutorFunction(b, state, function, table.Value())
}

func BenchmarkExecutorSparseTableLoop(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = state.Close()
	})
	table, err := state.NewTable(0, 128)
	if err != nil {
		b.Fatal(err)
	}
	for index := 1; index <= 100; index++ {
		if err := table.RawSetInt(
			index*1_000_000,
			Number(float64(index)),
		); err != nil {
			b.Fatal(err)
		}
	}
	function := compileTestFunction(b, state, "@sparse-table.lua", `
local table = ...
local sum = 0
for index = 1, 100 do
	local key = index * 1000000
	table[key] = index
	sum = sum + table[key]
end
return sum
`)
	benchmarkExecutorFunction(b, state, function, table.Value())
}

func BenchmarkExecutorMethodLoop(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = state.Close()
	})
	method := compileTestFunction(b, state, "@method.lua", `
local self, value = ...
return value
`)
	table, err := state.NewTable(0, 1)
	if err != nil {
		b.Fatal(err)
	}
	if err := table.RawSetString("method", method.Value()); err != nil {
		b.Fatal(err)
	}
	function := compileTestFunction(b, state, "@method-loop.lua", `
local object = ...
local sum = 0
for index = 1, 100 do
	sum = sum + object:method(index)
end
return sum
`)
	benchmarkExecutorFunction(b, state, function, table.Value())
}

func BenchmarkExecutorGlobalLoop(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = state.Close()
	})
	function := compileTestFunction(b, state, "@globals.lua", `
value = 0
for index = 1, 100 do
	value = value + index
end
return value
`)
	benchmarkExecutorFunction(b, state, function)
}

func BenchmarkExecutorTableConstructor(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = state.Close()
	})
	function := compileTestFunction(b, state, "@constructor.lua", `
return {
	1, 2, 3, 4, 5, 6, 7, 8,
	9, 10, 11, 12, 13, 14, 15, 16,
	left = 17,
	right = 18,
}
`)
	benchmarkExecutorFunction(b, state, function)
}
