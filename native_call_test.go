package lua

import (
	"errors"
	"runtime"
	"testing"
)

func TestFrameCallInvokesLuaNativeAndCallableValues(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	luaTarget := mustLoadString(t, state, "@nested-lua.lua", `
local first, middle, last = ...
return first, middle, last, nil
`)
	nativeTarget, err := state.NewNativeFunction(func(frame Frame) Outcome {
		first, _ := frame.Argument(0)
		last, _ := frame.Argument(2)
		return frame.ReturnValues(first, Nil(), last, Nil())
	})
	if err != nil {
		t.Fatal(err)
	}
	callable, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	callableMetatable, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	callHandler, err := state.NewNativeFunction(func(frame Frame) Outcome {
		self, _ := frame.Argument(0)
		first, _ := frame.Argument(1)
		last, _ := frame.Argument(3)
		return frame.ReturnValues(self, first, Nil(), last, Nil())
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := callableMetatable.RawSetString(
		"__call",
		callHandler.Value(),
	); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(
		callable.Value(),
		callableMetatable,
	); err != nil {
		t.Fatal(err)
	}

	arguments := []Value{
		Number(11),
		Nil(),
		state.String("last"),
	}
	for _, test := range []struct {
		name     string
		callable Value
		want     []Value
	}{
		{
			name:     "Lua function",
			callable: luaTarget.Value(),
			want: []Value{
				Number(11),
				Nil(),
				state.String("last"),
				Nil(),
			},
		},
		{
			name:     "native function",
			callable: nativeTarget.Value(),
			want: []Value{
				Number(11),
				Nil(),
				state.String("last"),
				Nil(),
			},
		},
		{
			name:     "callable table",
			callable: callable.Value(),
			want: []Value{
				callable.Value(),
				Number(11),
				Nil(),
				state.String("last"),
				Nil(),
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var nestedErr error
			frameRemainedValid := false
			host, constructionErr := state.NewNativeFunction(
				func(frame Frame) Outcome {
					before, beforePresent := frame.Argument(0)
					results, callErr := frame.Call(
						test.callable,
						arguments...,
					)
					nestedErr = callErr
					after, afterPresent := frame.Argument(0)
					frameRemainedValid =
						beforePresent &&
							afterPresent &&
							rawEqual(before, after) &&
							frame.ArgumentCount() == 1 &&
							frame.State() == state &&
							frame.CurrentThread() == state.MainThread()
					if callErr != nil {
						frame.ThrowString(callErr.Error())
					}
					return frame.ReturnValues(results...)
				},
			)
			if constructionErr != nil {
				t.Fatal(constructionErr)
			}
			results, callErr := state.Call(
				host.Value(),
				state.String("outer argument"),
			)
			if callErr != nil {
				t.Fatal(callErr)
			}
			if nestedErr != nil {
				t.Fatalf("nested call error = %v", nestedErr)
			}
			if !frameRemainedValid {
				t.Fatal("outer Frame was not restored after nested call")
			}
			assertTestValues(t, results, test.want...)
			assertRootThreadReady(t, state.main)
		})
	}
}

func TestFrameCallOneAndCallDiscardUseLuaResultAdjustment(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	empty := mustLoadString(t, state, "@nested-empty.lua", `return`)
	producer := mustLoadString(t, state, "@nested-producer.lua", `
nested_call_count = nested_call_count + 1
return 41, 42, 43
`)
	if err := state.RawSetGlobal("nested_call_count", Number(0)); err != nil {
		t.Fatal(err)
	}

	host, err := state.NewNativeFunction(func(frame Frame) Outcome {
		emptyResult, callErr := frame.CallOne(empty.Value())
		if callErr != nil {
			frame.ThrowError(callErr)
		}
		first, callErr := frame.CallOne(producer.Value())
		if callErr != nil {
			frame.ThrowError(callErr)
		}
		if callErr := frame.CallDiscard(producer.Value()); callErr != nil {
			frame.ThrowError(callErr)
		}
		return frame.ReturnValues(emptyResult, first)
	})
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(host.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Nil(), Number(41))
	calls, err := state.RawGlobal("nested_call_count")
	if err != nil {
		t.Fatal(err)
	}
	assertTestValue(t, calls, Number(2))
	assertRootThreadReady(t, state.main)
}

func TestFrameCallNAppliesFixedResultAdjustment(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	producer := mustLoadString(t, state, "@nested-call-n.lua", `
nested_call_n_count = nested_call_n_count + 1
return 41, 42, 43
`)
	if err := state.RawSetGlobal(
		"nested_call_n_count",
		Number(0),
	); err != nil {
		t.Fatal(err)
	}

	host, err := state.NewNativeFunction(func(frame Frame) Outcome {
		two, callErr := frame.CallN(producer.Value(), 2)
		if callErr != nil {
			frame.ThrowError(callErr)
		}
		padded, callErr := frame.CallN(producer.Value(), 5)
		if callErr != nil {
			frame.ThrowError(callErr)
		}
		discarded, callErr := frame.CallN(producer.Value(), 0)
		if callErr != nil {
			frame.ThrowError(callErr)
		}
		if discarded != nil {
			frame.ThrowString("zero-result slice is not nil")
		}
		if _, callErr := frame.CallN(producer.Value(), -1); !errors.Is(
			callErr,
			ErrInvalidResultCount,
		) {
			frame.ThrowString("negative result count was not rejected")
		}
		return frame.ReturnValues(append(two, padded...)...)
	})
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(host.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		Number(41),
		Number(42),
		Number(41),
		Number(42),
		Number(43),
		Nil(),
		Nil(),
	)
	calls, err := state.RawGlobal("nested_call_n_count")
	if err != nil {
		t.Fatal(err)
	}
	assertTestValue(t, calls, Number(3))
	assertRootThreadReady(t, state.main)
}

func TestFrameCallNRejectsUnavailableResultWindowAndRestoresFrame(
	t *testing.T,
) {
	state, err := New(Options{MaxValues: 8})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	marker, err := state.NewTable()
	if err != nil {
		t.Fatal(err)
	}
	targetCalls := 0
	target, err := state.NewNativeFunction(func(frame Frame) Outcome {
		targetCalls++
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	var nestedErr error
	frameValid := false
	host, err := state.NewNativeFunction(func(frame Frame) Outcome {
		_, nestedErr = frame.CallN(
			target.Value(),
			state.options.MaxValues,
		)
		discarded, recoveryErr := frame.CallN(target.Value(), 0)
		if recoveryErr != nil {
			frame.ThrowError(recoveryErr)
		}
		if discarded != nil {
			frame.ThrowString("recovery call returned a non-nil slice")
		}
		argument, present := frame.Argument(0)
		same, applicable := argument.SameObject(marker.Value())
		frameValid = present && applicable && same
		return frame.ReturnValue(argument)
	})
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(host.Value(), marker.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, marker.Value())
	var luaErr *Error
	if !errors.As(nestedErr, &luaErr) ||
		luaErr.Category() != ResourceError {
		t.Fatalf("nested result-window error = %#v", nestedErr)
	}
	if targetCalls != 1 {
		t.Fatalf("nested target calls = %d; want 1", targetCalls)
	}
	if !frameValid {
		t.Fatal("result-window failure invalidated outer Frame")
	}
	assertRootThreadReady(t, state.main)
}

func TestFrameIndexAppliesLuaTableSemantics(t *testing.T) {
	requireStableAllocationAccounting(t)
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	direct, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := direct.RawSetString("key", Number(41)); err != nil {
		t.Fatal(err)
	}
	fallback, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := fallback.RawSetString("key", Number(42)); err != nil {
		t.Fatal(err)
	}
	chained, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	chainedMetatable, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := chainedMetatable.RawSetString(
		"__index",
		fallback.Value(),
	); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(chained.Value(), chainedMetatable); err != nil {
		t.Fatal(err)
	}

	handler := mustLoadString(t, state, "@frame-index.lua", `
local target, key = ...
return "handled:" .. key
`)
	computed, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	computedMetatable, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := computedMetatable.RawSetString(
		"__index",
		handler.Value(),
	); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(computed.Value(), computedMetatable); err != nil {
		t.Fatal(err)
	}

	var nestedError error
	host, err := state.NewNativeFunction(func(frame Frame) Outcome {
		target, _ := frame.Argument(0)
		key, _ := frame.Argument(1)
		result, indexErr := frame.Index(target, key)
		nestedError = indexErr
		if indexErr != nil {
			var failure *Error
			if errors.As(indexErr, &failure) {
				frame.Rethrow(failure)
			}
			frame.ThrowString(indexErr.Error())
		}
		if frame.ArgumentCount() != 2 {
			frame.ThrowString("Index invalidated its Frame")
		}
		return frame.ReturnValue(result)
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name   string
		target Value
		key    Value
		want   Value
	}{
		{name: "raw hit", target: direct.Value(), key: state.String("key"), want: Number(41)},
		{name: "table chain", target: chained.Value(), key: state.String("key"), want: Number(42)},
		{name: "function handler", target: computed.Value(), key: state.String("key"), want: state.String("handled:key")},
		{name: "absent", target: direct.Value(), key: state.String("absent"), want: Nil()},
	} {
		t.Run(test.name, func(t *testing.T) {
			results, callErr := state.Call(
				host.Value(),
				test.target,
				test.key,
			)
			if callErr != nil {
				t.Fatal(callErr)
			}
			if nestedError != nil {
				t.Fatalf("Index error = %v", nestedError)
			}
			assertTestValues(t, results, test.want)
		})
	}

	var destination [1]Value
	for range 16 {
		if _, err := state.CallInto(
			host.Value(),
			[]Value{direct.Value(), state.String("key")},
			destination[:],
		); err != nil {
			t.Fatal(err)
		}
	}
	allocations := testing.AllocsPerRun(256, func() {
		if _, err := state.CallInto(
			host.Value(),
			[]Value{direct.Value(), state.String("key")},
			destination[:],
		); err != nil {
			panic(err)
		}
	})
	if allocations != 0 {
		t.Fatalf("warm raw Index allocated %v times", allocations)
	}
	assertTestValues(t, destination[:], Number(41))
}

func TestFrameSetIndexAppliesLuaTableSemantics(t *testing.T) {
	requireStableAllocationAccounting(t)
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	direct, err := state.NewTableWithCapacity(0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := direct.RawSetString("existing", Number(1)); err != nil {
		t.Fatal(err)
	}

	fallback, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	chained, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	chainedMetatable, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := chainedMetatable.RawSetString(
		"__newindex",
		fallback.Value(),
	); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(chained.Value(), chainedMetatable); err != nil {
		t.Fatal(err)
	}

	sink, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.RawSetGlobal("setIndexSink", sink.Value()); err != nil {
		t.Fatal(err)
	}
	handlerChunk := mustLoadString(t, state, "@frame-set-index.lua", `
return function(_, key, value)
	setIndexSink[key] = value + 1
	return "discarded"
end
`)
	handlerResults, err := state.Call(handlerChunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	handler, ok := handlerResults[0].AsFunction()
	if !ok {
		t.Fatalf("handler = %v; want Function", handlerResults[0])
	}
	computed, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	computedMetatable, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := computedMetatable.RawSetString(
		"__newindex",
		handler.Value(),
	); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(computed.Value(), computedMetatable); err != nil {
		t.Fatal(err)
	}

	var nestedError error
	host, err := state.NewNativeFunction(func(frame Frame) Outcome {
		target, _ := frame.Argument(0)
		key, _ := frame.Argument(1)
		value, _ := frame.Argument(2)
		nestedError = frame.SetIndex(target, key, value)
		if nestedError != nil {
			var failure *Error
			if errors.As(nestedError, &failure) {
				frame.Rethrow(failure)
			}
			frame.ThrowString(nestedError.Error())
		}
		if frame.ArgumentCount() != 3 {
			frame.ThrowString("SetIndex invalidated its Frame")
		}
		return frame.ReturnBool(true)
	})
	if err != nil {
		t.Fatal(err)
	}

	set := func(target *Table, key string, value Value) {
		t.Helper()
		results, callErr := state.Call(
			host.Value(),
			target.Value(),
			state.String(key),
			value,
		)
		if callErr != nil {
			t.Fatal(callErr)
		}
		if nestedError != nil {
			t.Fatalf("SetIndex error = %v", nestedError)
		}
		assertTestValues(t, results, Bool(true))
	}

	set(direct, "existing", Number(41))
	assertTestValue(t, rawStr(direct, "existing"), Number(41))
	set(direct, "new", Number(42))
	assertTestValue(t, rawStr(direct, "new"), Number(42))
	set(chained, "chained", Number(43))
	assertTestValue(t, rawStr(chained, "chained"), Nil())
	assertTestValue(t, rawStr(fallback, "chained"), Number(43))
	set(computed, "computed", Number(44))
	assertTestValue(t, rawStr(computed, "computed"), Nil())
	assertTestValue(t, rawStr(sink, "computed"), Number(45))
	set(direct, "existing", Nil())
	assertTestValue(t, rawStr(direct, "existing"), Nil())

	// Warm replacement takes the direct raw-hit path and allocates nothing.
	set(direct, "warm", Number(0))
	var destination [1]Value
	arguments := []Value{
		direct.Value(),
		state.String("warm"),
		Number(46),
	}
	for range 16 {
		if _, err := state.CallInto(
			host.Value(),
			arguments,
			destination[:],
		); err != nil {
			t.Fatal(err)
		}
	}
	allocations := testing.AllocsPerRun(256, func() {
		if _, err := state.CallInto(
			host.Value(),
			arguments,
			destination[:],
		); err != nil {
			panic(err)
		}
	})
	if allocations != 0 {
		t.Fatalf("warm raw SetIndex allocated %v times", allocations)
	}
	assertTestValue(t, rawStr(direct, "warm"), Number(46))
}

func TestFrameCallRejectsInputsAtomically(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	foreignState, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer foreignState.Close()

	foreign, err := foreignState.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	target, err := state.NewNativeFunction(func(frame Frame) Outcome {
		calls++
		return frame.ReturnNumber(1)
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name      string
		callable  Value
		arguments []Value
		want      error
	}{
		{
			name:     "invalid callable",
			callable: Value{},
			want:     ErrInvalidValue,
		},
		{
			name:     "foreign callable",
			callable: foreign.Value(),
			want:     ErrForeignValue,
		},
		{
			name:      "invalid argument",
			callable:  target.Value(),
			arguments: []Value{{}},
			want:      ErrInvalidValue,
		},
		{
			name:      "foreign argument",
			callable:  target.Value(),
			arguments: []Value{foreign.Value()},
			want:      ErrForeignValue,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			beforeCalls := calls
			destination := []Value{Number(70), Number(71)}
			var gotErr error
			frameRemainedValid := false
			host, constructionErr := state.NewNativeFunction(
				func(frame Frame) Outcome {
					before, _ := frame.Argument(0)
					count, callErr := frame.CallInto(
						test.callable,
						test.arguments,
						destination,
					)
					gotErr = callErr
					after, _ := frame.Argument(0)
					frameRemainedValid =
						count == 0 &&
							rawEqual(before, after) &&
							frame.ArgumentCount() == 1
					return frame.ReturnBool(true)
				},
			)
			if constructionErr != nil {
				t.Fatal(constructionErr)
			}
			results, callErr := state.Call(
				host.Value(),
				state.String("outer"),
			)
			if callErr != nil {
				t.Fatal(callErr)
			}
			assertTestValues(t, results, Bool(true))
			if !errors.Is(gotErr, test.want) {
				t.Fatalf("nested call error = %v; want %v", gotErr, test.want)
			}
			if calls != beforeCalls {
				t.Fatalf(
					"rejected nested call ran target %d times; want %d",
					calls,
					beforeCalls,
				)
			}
			if !frameRemainedValid {
				t.Fatal("rejected input changed the outer Frame")
			}
			assertTestValues(
				t,
				destination,
				Number(70),
				Number(71),
			)
			assertRootThreadReady(t, state.main)
		})
	}
}

func TestFrameCallIntoHandlesOverlapAndCapacityAtomically(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	identity := mustLoadString(t, state, "@nested-identity.lua", `return ...`)
	values := []Value{
		Number(10),
		Number(20),
		Number(90),
		Number(91),
	}
	var overlapCount int
	var overlapErr error
	overlapHost, err := state.NewNativeFunction(func(frame Frame) Outcome {
		overlapCount, overlapErr = frame.CallInto(
			identity.Value(),
			values[:2],
			values[1:3],
		)
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Call(overlapHost.Value()); err != nil {
		t.Fatal(err)
	}
	if overlapErr != nil || overlapCount != 2 {
		t.Fatalf(
			"overlap CallInto = (%d, %v); want (2, nil)",
			overlapCount,
			overlapErr,
		)
	}
	assertTestValues(
		t,
		values,
		Number(10),
		Number(10),
		Number(20),
		Number(91),
	)

	producer := mustLoadString(t, state, "@nested-producer.lua", `
nested_side_effect = nested_side_effect + 1
return 1, nil, 3
`)
	if err := state.RawSetGlobal("nested_side_effect", Number(0)); err != nil {
		t.Fatal(err)
	}
	destination := []Value{Number(80), Number(81)}
	var required int
	var capacityFailure error
	capacityHost, err := state.NewNativeFunction(func(frame Frame) Outcome {
		required, capacityFailure = frame.CallInto(
			producer.Value(),
			nil,
			destination,
		)
		if _, present := frame.Argument(0); !present {
			frame.ThrowString("outer Frame lost its argument")
		}
		return frame.ReturnBool(true)
	})
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(capacityHost.Value(), Number(7))
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Bool(true))
	var capacityError *ResultCapacityError
	if !errors.As(capacityFailure, &capacityError) ||
		required != 3 ||
		capacityError.Required != 3 ||
		capacityError.Available != 2 {
		t.Fatalf(
			"short nested destination = (%d, %#v)",
			required,
			capacityFailure,
		)
	}
	assertTestValues(
		t,
		capacityError.Results(),
		Number(1),
		Nil(),
		Number(3),
	)
	assertTestValues(t, destination, Number(80), Number(81))
	sideEffect, err := state.RawGlobal("nested_side_effect")
	if err != nil {
		t.Fatal(err)
	}
	assertTestValue(t, sideEffect, Number(1))

	failing := mustLoadString(t, state, "@nested-into-failure.lua", `
nested_failure_side_effect = 27
return nil + 1
`)
	failureDestination := []Value{Number(60), Number(61)}
	var nestedFailure *Error
	failureHost, err := state.NewNativeFunction(func(frame Frame) Outcome {
		count, callErr := frame.CallInto(
			failing.Value(),
			nil,
			failureDestination,
		)
		if count != 0 || !errors.As(callErr, &nestedFailure) {
			frame.ThrowString("ordinary nested failure was not returned")
		}
		return frame.ReturnBool(true)
	})
	if err != nil {
		t.Fatal(err)
	}
	results, err = state.Call(failureHost.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Bool(true))
	assertTestValues(
		t,
		failureDestination,
		Number(60),
		Number(61),
	)
	sideEffect, err = state.RawGlobal("nested_failure_side_effect")
	if err != nil {
		t.Fatal(err)
	}
	assertTestValue(t, sideEffect, Number(27))
	assertRootThreadReady(t, state.main)
}

func TestFrameCallSurvivesStackRelocationAndRejectsStateReentry(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	target := mustLoadString(t, state, "@nested-stack-growth.lua", `
local function grow(depth, payload)
	local first, second, third, fourth = depth, payload, depth, payload
	if depth == 0 then
		return payload, nil
	end
	local result, trailing = grow(depth - 1, payload)
	if first == -1 or second == nil or third == -1 or fourth == nil then
		return nil
	end
	return result, trailing
end
return grow(...)
`)
	marker, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var stateCallError error
	stacksGrew := false
	frameValid := false
	host, err := state.NewNativeFunction(
		func(frame Frame) Outcome {
			beforeValues := cap(frame.thread.values)
			beforeFrames := cap(frame.thread.frames)
			_, stateCallError = state.Call(target.Value())
			results, callErr := frame.Call(
				target.Value(),
				Number(64),
				marker.Value(),
			)
			if callErr != nil {
				frame.ThrowString(callErr.Error())
			}
			argument, argumentPresent := frame.Argument(0)
			argumentSame, argumentApplicable :=
				argument.SameObject(marker.Value())
			frameValid =
				argumentPresent &&
					argumentApplicable &&
					argumentSame
			stacksGrew =
				cap(frame.thread.values) > beforeValues &&
					cap(frame.thread.frames) > beforeFrames
			return frame.ReturnValues(results...)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(host.Value(), marker.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, marker.Value(), Nil())
	if !errors.Is(stateCallError, ErrRunning) {
		t.Fatalf("State.Call during callback = %v; want ErrRunning", stateCallError)
	}
	if !stacksGrew {
		t.Fatal("nested target did not force both execution stacks to grow")
	}
	if !frameValid {
		t.Fatal("stack relocation invalidated outer arguments or captures")
	}
	assertRootThreadReady(t, state.main)
}

func TestFrameCallDistinguishesLuaCallableFailures(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	plain, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	recursiveHandler, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	recursiveMetatable, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := recursiveMetatable.RawSetString(
		"__call",
		recursiveHandler.Value(),
	); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(
		recursiveHandler.Value(),
		recursiveMetatable,
	); err != nil {
		t.Fatal(err)
	}
	outer, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	outerMetatable, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := outerMetatable.RawSetString(
		"__call",
		recursiveHandler.Value(),
	); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(outer.Value(), outerMetatable); err != nil {
		t.Fatal(err)
	}

	for _, callable := range []Value{plain.Value(), outer.Value()} {
		var nestedError error
		host, constructionErr := state.NewNativeFunction(
			func(frame Frame) Outcome {
				_, nestedError = frame.Call(callable)
				if nestedError == nil {
					frame.ThrowString("noncallable nested value ran")
				}
				return frame.ReturnBool(true)
			},
		)
		if constructionErr != nil {
			t.Fatal(constructionErr)
		}
		results, callErr := state.Call(host.Value())
		if callErr != nil {
			t.Fatal(callErr)
		}
		assertTestValues(t, results, Bool(true))
		failure, ok := nestedError.(*Error)
		if !ok ||
			failure.Category() != RuntimeError ||
			failure.Error() != "attempt to call a table value" {
			t.Fatalf("noncallable nested failure = %#v", nestedError)
		}
	}
	assertRootThreadReady(t, state.main)
}

func TestFrameReraisePreservesFailureAndAppendsOuterTrace(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	marker, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	cause := errors.New("nested cause")
	original := &Error{
		value:       marker.Value(),
		description: "nested marker failure",
		category:    ResourceError,
		cause:       cause,
	}
	raiser, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.sealError(original)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.RawSetGlobal("nested_raise", raiser.Value()); err != nil {
		t.Fatal(err)
	}
	nested := mustLoadString(t, state, "@nested-trace.lua", `
local function leaf()
	nested_trace_side_effect = 19
	nested_raise()
end
leaf()
return 1
`)

	var nestedTrace []TraceFrame
	var nestedFailure *Error
	bridge, err := state.NewNativeFunction(func(frame Frame) Outcome {
		_, callErr := frame.Call(nested.Value())
		if !errors.As(callErr, &nestedFailure) {
			frame.ThrowString("nested failure was not a Lua error")
		}
		nestedTrace = nestedFailure.Traceback()
		frame.Rethrow(nestedFailure)
		// Unreachable: the throw above does not return.
		return Outcome{}
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.RawSetGlobal("nested_bridge", bridge.Value()); err != nil {
		t.Fatal(err)
	}
	outer := mustLoadString(t, state, "@outer-trace.lua", `
local function invoke()
	local result = nested_bridge()
	return result
end
return invoke()
`)
	_, callErr := state.Call(outer.Value())
	var failure *Error
	if !errors.As(callErr, &failure) {
		t.Fatalf("raised nested failure = %#v; want *Error", callErr)
	}
	if failure == nestedFailure {
		t.Fatal("Reraise reused and mutated the nested failure object")
	}
	if failure.Category() != ResourceError ||
		!errors.Is(failure, cause) ||
		failure.Error() != "nested marker failure" {
		t.Fatalf("propagated failure = %#v", failure)
	}
	if same, applicable := failure.Value().SameObject(marker.Value()); !applicable ||
		!same {
		t.Fatal("Reraise lost arbitrary error Value identity")
	}
	sideEffect, err := state.RawGlobal("nested_trace_side_effect")
	if err != nil {
		t.Fatal(err)
	}
	assertTestValue(t, sideEffect, Number(19))

	if len(nestedTrace) != 3 ||
		nestedTrace[0].Source != "=[Go]" ||
		nestedTrace[1].Source != "@nested-trace.lua" ||
		nestedTrace[2].Source != "@nested-trace.lua" {
		t.Fatalf("nested-only traceback = %+v", nestedTrace)
	}
	trace := failure.Traceback()
	if len(trace) != len(nestedTrace)+2 {
		t.Fatalf(
			"propagated traceback = %+v; want nested %d + outer 2",
			trace,
			len(nestedTrace),
		)
	}
	for index := range nestedTrace {
		if trace[index] != nestedTrace[index] {
			t.Fatalf(
				"propagated trace prefix %d = %+v; want %+v",
				index,
				trace[index],
				nestedTrace[index],
			)
		}
	}
	outerTrace := trace[len(nestedTrace):]
	if outerTrace[0].Source != "=[Go]" ||
		outerTrace[1].Source != "@outer-trace.lua" ||
		outerTrace[1].TailCalls != 1 {
		t.Fatalf("outer traceback = %+v", outerTrace)
	}
	assertRootThreadReady(t, state.main)
}

func TestFrameCallLetsNestedPCallAndXPCallCatchLuaErrors(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	marker, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	raiser, err := state.NewNativeFunction(func(frame Frame) Outcome {
		frame.Throw(marker.Value())
		// Unreachable: the throw above does not return.
		return Outcome{}
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.RawSetGlobal("nested_protected_raise", raiser.Value()); err != nil {
		t.Fatal(err)
	}
	target := mustLoadString(t, state, "@nested-protected-call.lua", `
local pcallOK, pcallValue = pcall(nested_protected_raise)
local xpcallOK, xpcallValue = xpcall(
	nested_protected_raise,
	function(value) return value end
)
return pcallOK, pcallValue, xpcallOK, xpcallValue
`)
	host, err := state.NewNativeFunction(func(frame Frame) Outcome {
		results, callErr := frame.Call(target.Value())
		if callErr != nil {
			frame.ThrowString(callErr.Error())
		}
		return frame.ReturnValues(results...)
	})
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(host.Value())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 4 {
		t.Fatalf("protected nested result count = %d; want 4", len(results))
	}
	assertTestValue(t, results[0], Bool(false))
	assertTestValue(t, results[2], Bool(false))
	for _, index := range []int{1, 3} {
		if same, applicable := results[index].SameObject(marker.Value()); !applicable ||
			!same {
			t.Fatalf(
				"protected nested error result %d lost marker identity",
				index,
			)
		}
	}
	assertRootThreadReady(t, state.main)
}

func TestFrameReraiseAppendsEachNestedTraceSegmentOnce(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	fail, err := state.NewNativeFunction(func(frame Frame) Outcome {
		frame.ThrowString("nested bridge failure")
		// Unreachable: the throw above does not return.
		return Outcome{}
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.RawSetGlobal("nested_trace_fail", fail.Value()); err != nil {
		t.Fatal(err)
	}
	inner := mustLoadString(t, state, "@trace-inner.lua", `
local value = nested_trace_fail()
return value
`)

	var innerTrace []TraceFrame
	bridgeOne, err := state.NewNativeFunction(func(frame Frame) Outcome {
		_, callErr := frame.Call(inner.Value())
		var failure *Error
		if !errors.As(callErr, &failure) {
			frame.ThrowString("inner bridge lost its Lua error")
		}
		innerTrace = failure.Traceback()
		frame.Rethrow(failure)
		// Unreachable: the throw above does not return.
		return Outcome{}
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.RawSetGlobal("nested_trace_bridge_one", bridgeOne.Value()); err != nil {
		t.Fatal(err)
	}
	middle := mustLoadString(t, state, "@trace-middle.lua", `
local value = nested_trace_bridge_one()
return value
`)

	var middleTrace []TraceFrame
	bridgeTwo, err := state.NewNativeFunction(func(frame Frame) Outcome {
		_, callErr := frame.Call(middle.Value())
		var failure *Error
		if !errors.As(callErr, &failure) {
			frame.ThrowString("outer bridge lost its Lua error")
		}
		middleTrace = failure.Traceback()
		frame.Rethrow(failure)
		// Unreachable: the throw above does not return.
		return Outcome{}
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.RawSetGlobal("nested_trace_bridge_two", bridgeTwo.Value()); err != nil {
		t.Fatal(err)
	}
	outer := mustLoadString(t, state, "@trace-outer.lua", `
local value = nested_trace_bridge_two()
return value
`)

	_, callErr := state.Call(outer.Value())
	var failure *Error
	if !errors.As(callErr, &failure) {
		t.Fatalf("nested bridge failure = %#v; want *Error", callErr)
	}
	if failure.Error() != "nested bridge failure" {
		t.Fatalf("nested bridge description = %q", failure.Error())
	}

	assertTraceSuffix := func(
		name string,
		trace []TraceFrame,
		prefix []TraceFrame,
		luaSource string,
	) {
		t.Helper()
		if len(trace) != len(prefix)+2 {
			t.Fatalf(
				"%s trace = %+v; want prefix %d + two frames",
				name,
				trace,
				len(prefix),
			)
		}
		for index := range prefix {
			if trace[index] != prefix[index] {
				t.Fatalf(
					"%s trace prefix %d = %+v; want %+v",
					name,
					index,
					trace[index],
					prefix[index],
				)
			}
		}
		suffix := trace[len(prefix):]
		if suffix[0].Source != "=[Go]" ||
			suffix[1].Source != luaSource {
			t.Fatalf("%s trace suffix = %+v", name, suffix)
		}
	}
	assertTraceSuffix("inner", innerTrace, nil, "@trace-inner.lua")
	assertTraceSuffix(
		"middle",
		middleTrace,
		innerTrace,
		"@trace-middle.lua",
	)
	assertTraceSuffix(
		"outer",
		failure.Traceback(),
		middleTrace,
		"@trace-outer.lua",
	)
	assertRootThreadReady(t, state.main)
}

func TestWarmFrameCallIntoDoesNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	luaTarget := mustLoadString(
		t,
		state,
		"@warm-nested-lua.lua",
		`return ...`,
	)
	emptyTarget := mustLoadString(
		t,
		state,
		"@warm-nested-empty.lua",
		`return`,
	)
	nativeTarget, err := state.NewNativeFunction(func(frame Frame) Outcome {
		number, ok := frame.Number(0)
		if !ok {
			frame.ThrowArgTypeError(0, NumberKind)
		}
		return frame.ReturnNumber(number)
	})
	if err != nil {
		t.Fatal(err)
	}
	prebuiltArguments := []Value{Number(41)}

	for _, test := range []struct {
		name   string
		target *Function
		mode   int
	}{
		{name: "Lua literal arguments", target: luaTarget},
		{
			name:   "Lua prebuilt arguments",
			target: luaTarget,
			mode:   1,
		},
		{
			name:   "Lua stack-array arguments",
			target: luaTarget,
			mode:   2,
		},
		{name: "native literal arguments", target: nativeTarget},
		{
			name:   "native prebuilt arguments",
			target: nativeTarget,
			mode:   1,
		},
		{
			name:   "native stack-array arguments",
			target: nativeTarget,
			mode:   2,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			nestedDestination := make([]Value, 1)
			outerDestination := make([]Value, 1)
			host, constructionErr := state.NewNativeFunction(
				func(frame Frame) Outcome {
					var stackArguments [1]Value
					var arguments []Value
					switch test.mode {
					case 0:
						arguments = []Value{Number(41)}
					case 1:
						arguments = prebuiltArguments
					case 2:
						stackArguments[0] = Number(41)
						arguments = stackArguments[:]
					default:
						panic("invalid warm argument mode")
					}
					count, callErr := frame.CallInto(
						test.target.Value(),
						arguments,
						nestedDestination,
					)
					if callErr != nil || count != 1 {
						frame.ThrowString("warm nested call failed")
					}
					return frame.ReturnValue(nestedDestination[0])
				},
			)
			if constructionErr != nil {
				t.Fatal(constructionErr)
			}
			for range 8 {
				count, callErr := state.CallInto(
					host.Value(),
					nil,
					outerDestination,
				)
				if callErr != nil || count != 1 {
					t.Fatalf(
						"warmup = (%d, %v)",
						count,
						callErr,
					)
				}
			}
			allocations := testing.AllocsPerRun(1_000, func() {
				count, callErr := state.CallInto(
					host.Value(),
					nil,
					outerDestination,
				)
				if callErr != nil || count != 1 {
					panic("warm nested CallInto failed")
				}
			})
			if allocations != 0 {
				t.Fatalf(
					"warm nested CallInto allocations = %v; want 0",
					allocations,
				)
			}
			assertTestValues(t, outerDestination, Number(41))
			runtime.KeepAlive(host)
		})
	}

	t.Run("no arguments or results", func(t *testing.T) {
		host, constructionErr := state.NewNativeFunction(
			func(frame Frame) Outcome {
				count, callErr := frame.CallInto(
					emptyTarget.Value(),
					nil,
					nil,
				)
				if callErr != nil || count != 0 {
					frame.ThrowString("empty nested call failed")
				}
				return frame.Return()
			},
		)
		if constructionErr != nil {
			t.Fatal(constructionErr)
		}
		for range 8 {
			count, callErr := state.CallInto(host.Value(), nil, nil)
			if callErr != nil || count != 0 {
				t.Fatalf("empty warmup = (%d, %v)", count, callErr)
			}
		}
		allocations := testing.AllocsPerRun(1_000, func() {
			count, callErr := state.CallInto(host.Value(), nil, nil)
			if callErr != nil || count != 0 {
				panic("empty nested CallInto failed")
			}
		})
		if allocations != 0 {
			t.Fatalf(
				"empty nested CallInto allocations = %v; want 0",
				allocations,
			)
		}
		runtime.KeepAlive(host)
	})
}

func TestWarmFrameCallAllocationContract(t *testing.T) {
	requireStableAllocationAccounting(t)
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	empty := mustLoadString(t, state, "@nested-empty.lua", `return`)
	one := mustLoadString(t, state, "@nested-one.lua", `return 41`)

	emptyHost, err := state.NewNativeFunction(func(frame Frame) Outcome {
		results, callErr := frame.Call(empty.Value())
		if callErr != nil || results != nil {
			frame.ThrowString("zero-result nested Call failed")
		}
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 8 {
		if count, callErr := state.CallInto(
			emptyHost.Value(),
			nil,
			nil,
		); callErr != nil || count != 0 {
			t.Fatalf("zero-result warmup = (%d, %v)", count, callErr)
		}
	}
	allocations := testing.AllocsPerRun(1_000, func() {
		if count, callErr := state.CallInto(
			emptyHost.Value(),
			nil,
			nil,
		); callErr != nil || count != 0 {
			panic("zero-result nested Call failed")
		}
	})
	if allocations != 0 {
		t.Fatalf(
			"zero-result nested Call allocations = %v; want 0",
			allocations,
		)
	}

	outerDestination := make([]Value, 1)
	oneHost, err := state.NewNativeFunction(func(frame Frame) Outcome {
		results, callErr := frame.Call(one.Value())
		if callErr != nil || len(results) != 1 {
			frame.ThrowString("one-result nested Call failed")
		}
		return frame.ReturnValue(results[0])
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 8 {
		if count, callErr := state.CallInto(
			oneHost.Value(),
			nil,
			outerDestination,
		); callErr != nil || count != 1 {
			t.Fatalf("one-result warmup = (%d, %v)", count, callErr)
		}
	}
	allocations = testing.AllocsPerRun(1_000, func() {
		if count, callErr := state.CallInto(
			oneHost.Value(),
			nil,
			outerDestination,
		); callErr != nil || count != 1 {
			panic("one-result nested Call failed")
		}
	})
	if allocations != 1 {
		t.Fatalf(
			"one-result nested Call allocations = %v; want 1",
			allocations,
		)
	}
	assertTestValues(t, outerDestination, Number(41))
	runtime.KeepAlive(emptyHost)
	runtime.KeepAlive(oneHost)
}

func BenchmarkFrameNestedCall(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = state.Close()
	})
	luaTarget := mustLoadString(
		b,
		state,
		"@benchmark-nested-lua.lua",
		`return ...`,
	)
	nativeTarget, err := state.NewNativeFunction(func(frame Frame) Outcome {
		value, _ := frame.Argument(0)
		return frame.ReturnValue(value)
	})
	if err != nil {
		b.Fatal(err)
	}
	callable, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		b.Fatal(err)
	}
	metatable, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		b.Fatal(err)
	}
	if err := metatable.RawSetString("__call", nativeTarget.Value()); err != nil {
		b.Fatal(err)
	}
	if err := state.SetMetatable(callable.Value(), metatable); err != nil {
		b.Fatal(err)
	}
	arguments := []Value{Number(41)}

	for _, test := range []struct {
		name     string
		callable Value
	}{
		{name: "Lua", callable: luaTarget.Value()},
		{name: "native", callable: nativeTarget.Value()},
		{name: "callable", callable: callable.Value()},
	} {
		b.Run("CallInto/"+test.name, func(b *testing.B) {
			nestedDestination := make([]Value, 1)
			outerDestination := make([]Value, 1)
			host, constructionErr := state.NewNativeFunction(
				func(frame Frame) Outcome {
					count, callErr := frame.CallInto(
						test.callable,
						arguments,
						nestedDestination,
					)
					if callErr != nil || count != 1 {
						panic("nested benchmark CallInto failed")
					}
					return frame.ReturnValue(nestedDestination[0])
				},
			)
			if constructionErr != nil {
				b.Fatal(constructionErr)
			}
			for range 8 {
				if _, callErr := state.CallInto(
					host.Value(),
					nil,
					outerDestination,
				); callErr != nil {
					b.Fatal(callErr)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, callErr := state.CallInto(
					host.Value(),
					nil,
					outerDestination,
				); callErr != nil {
					b.Fatal(callErr)
				}
			}
			runtime.KeepAlive(host)
		})
	}

	b.Run("Call/Lua", func(b *testing.B) {
		outerDestination := make([]Value, 1)
		host, constructionErr := state.NewNativeFunction(
			func(frame Frame) Outcome {
				results, callErr := frame.Call(
					luaTarget.Value(),
					Number(41),
				)
				if callErr != nil {
					panic(callErr)
				}
				return frame.ReturnValue(results[0])
			},
		)
		if constructionErr != nil {
			b.Fatal(constructionErr)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			if _, callErr := state.CallInto(
				host.Value(),
				nil,
				outerDestination,
			); callErr != nil {
				b.Fatal(callErr)
			}
		}
		runtime.KeepAlive(host)
	})
}
