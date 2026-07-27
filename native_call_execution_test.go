package lua

import (
	"errors"
	"runtime"
	"testing"
)

func TestFrameCallCompletesMetamethodAndIteratorContinuations(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	object, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	other, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	metatable, err := state.NewTable(0, 5)
	if err != nil {
		t.Fatal(err)
	}
	index, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.ReturnString("indexed")
	})
	if err != nil {
		t.Fatal(err)
	}
	var assigned Value
	newIndex, err := state.NewNativeFunction(func(frame Frame) Outcome {
		assigned, _ = frame.Argument(2)
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	add, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.ReturnNumber(30)
	})
	if err != nil {
		t.Fatal(err)
	}
	lessThan, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.ReturnBool(true)
	})
	if err != nil {
		t.Fatal(err)
	}
	concat, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.ReturnString("joined")
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, function := range map[string]*Function{
		"__index":    index,
		"__newindex": newIndex,
		"__add":      add,
		"__lt":       lessThan,
		"__concat":   concat,
	} {
		if err := metatable.RawSetString(name, function.Value()); err != nil {
			t.Fatal(err)
		}
	}
	if err := state.SetMetatable(object.Value(), metatable); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(other.Value(), metatable); err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("continued_object", object.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("continued_other", other.Value()); err != nil {
		t.Fatal(err)
	}
	iteration := 0
	iterator, err := state.NewNativeFunction(func(frame Frame) Outcome {
		iteration++
		switch iteration {
		case 1:
			return frame.ReturnNumber(5)
		case 2:
			return frame.ReturnNumber(6)
		default:
			return frame.ReturnNil()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("continued_iterator", iterator.Value()); err != nil {
		t.Fatal(err)
	}
	target := mustLoadString(t, state, "@nested-continuations.lua", `
local indexed = continued_object.missing
continued_object.answer = 9
local added = continued_object + 2
local ordered = continued_object < continued_other
local joined = continued_object .. "!"
local total = 0
for value in continued_iterator, nil, nil do
	total = total + value
end
return indexed, added, ordered, joined, total
`)
	frameValid := false
	host, err := state.NewNativeFunction(func(frame Frame) Outcome {
		results, callErr := frame.Call(target.Value())
		if callErr != nil {
			return frame.RaiseString(callErr.Error())
		}
		_, present := frame.Argument(0)
		frameValid = present && frame.ArgumentCount() == 1
		return frame.ReturnValues(results...)
	})
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(host.Value(), Number(17))
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		state.String("indexed"),
		Number(30),
		Bool(true),
		state.String("joined"),
		Number(11),
	)
	assertTestValue(t, assigned, Number(9))
	if !frameValid {
		t.Fatal("continuation call did not restore the outer Frame")
	}
	assertRootThreadReady(t, state.main)
}

func TestFrameCallClosesFailedCalleeUpvaluesAndPreservesCallerUpvalues(
	t *testing.T,
) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	fail, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.RaiseString("close nested upvalues")
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("nested_upvalue_fail", fail.Value()); err != nil {
		t.Fatal(err)
	}
	target := mustLoadString(t, state, "@nested-upvalues.lua", `
local inner = 41
nested_escaped = function()
	return inner
end
inner = 42
nested_upvalue_fail()
inner = 99
`)

	var nestedFailure *Error
	bridge, err := state.NewNativeFunction(func(frame Frame) Outcome {
		view, ok := frame.Function(0)
		if !ok {
			return frame.ArgTypeError(0, FunctionKind)
		}
		_, callErr := frame.Call(target.Value())
		if !errors.As(callErr, &nestedFailure) {
			return frame.RaiseString("nested upvalue target did not fail")
		}
		escaped, globalErr := state.Global("nested_escaped")
		if globalErr != nil {
			return frame.RaiseString(globalErr.Error())
		}
		closedValues, callErr := frame.Call(escaped)
		if callErr != nil {
			return frame.RaiseString(callErr.Error())
		}
		openValues, callErr := frame.Call(view.Value(), Number(15))
		if callErr != nil {
			return frame.RaiseString(callErr.Error())
		}
		return frame.ReturnValues(closedValues[0], openValues[0])
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("nested_upvalue_bridge", bridge.Value()); err != nil {
		t.Fatal(err)
	}
	outer := mustLoadString(t, state, "@outer-upvalues.lua", `
local outer = 10
local function view(next)
	if next ~= nil then
		outer = next
	end
	return outer
end
local closed, open = nested_upvalue_bridge(view)
outer = 20
return closed, open, view()
`)
	results, err := state.Call(outer.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(42), Number(15), Number(20))
	if nestedFailure == nil {
		t.Fatal("nested upvalue failure was not observed")
	}
	assertRootThreadReady(t, state.main)
}

func TestFrameCallPropagatesGoPanicAndRestoresExecution(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	marker := &struct{ name string }{name: "nested panic"}
	panicking, err := state.NewNativeFunction(func(Frame) Outcome {
		panic(marker)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("nested_panic", panicking.Value()); err != nil {
		t.Fatal(err)
	}
	target := mustLoadString(
		t,
		state,
		"@nested-panic.lua",
		`return pcall(nested_panic)`,
	)
	host, err := state.NewNativeFunction(func(frame Frame) Outcome {
		_, _ = frame.Call(target.Value())
		panic("Frame.Call returned from a Go panic")
	})
	if err != nil {
		t.Fatal(err)
	}

	var recovered any
	func() {
		defer func() {
			recovered = recover()
		}()
		_, _ = state.Call(host.Value())
	}()
	if recovered != marker {
		t.Fatalf("recovered panic = %#v; want %#v", recovered, marker)
	}
	assertRootThreadReady(t, state.main)

	recovery := mustLoadString(t, state, "@after-nested-panic.lua", `return 42`)
	results, err := state.Call(recovery.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(42))
	runtime.KeepAlive(marker)
}

func TestFrameCallRestoresOuterFrameBeforePropagatingPanic(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	marker := &struct{ name string }{name: "caught nested panic"}
	panicking, err := state.NewNativeFunction(func(Frame) Outcome {
		panic(marker)
	})
	if err != nil {
		t.Fatal(err)
	}
	identity := mustLoadString(
		t,
		state,
		"@after-caught-nested-panic.lua",
		`return ...`,
	)
	var recovered any
	host, err := state.NewNativeFunction(func(frame Frame) Outcome {
		func() {
			defer func() {
				recovered = recover()
			}()
			_, _ = frame.Call(panicking.Value())
		}()
		if recovered != marker {
			return frame.RaiseString("nested panic was not propagated")
		}
		argument, present := frame.Argument(0)
		if !present || frame.ArgumentCount() != 1 {
			return frame.RaiseString("outer Frame was not restored")
		}
		results, callErr := frame.Call(identity.Value(), argument)
		if callErr != nil {
			return frame.RaiseString(callErr.Error())
		}
		return frame.ReturnValues(results...)
	})
	if err != nil {
		t.Fatal(err)
	}

	results, err := state.Call(host.Value(), Number(73))
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(73))
	assertRootThreadReady(t, state.main)
	runtime.KeepAlive(marker)
}

func TestFrameCallInvalidatesOuterFrameDuringNestedCallback(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	var outer Frame
	outerRejected := false
	nested, err := state.NewNativeFunction(func(frame Frame) Outcome {
		var recovered any
		func() {
			defer func() {
				recovered = recover()
			}()
			outer.ArgumentCount()
		}()
		outerRejected = recovered != nil
		return frame.ReturnNumber(41)
	})
	if err != nil {
		t.Fatal(err)
	}
	host, err := state.NewNativeFunction(func(frame Frame) Outcome {
		outer = frame
		results, callErr := frame.Call(nested.Value())
		if callErr != nil {
			return frame.RaiseString(callErr.Error())
		}
		if frame.ArgumentCount() != 1 {
			return frame.RaiseString("outer Frame stayed invalid")
		}
		return frame.ReturnValues(results...)
	})
	if err != nil {
		t.Fatal(err)
	}

	results, err := state.Call(host.Value(), Number(9))
	if err != nil {
		t.Fatal(err)
	}
	if !outerRejected {
		t.Fatal("nested callback could access its caller's borrowed Frame")
	}
	assertTestValues(t, results, Number(41))
	assertRootThreadReady(t, state.main)
}

func TestFrameCallRejectsSameThreadYieldThenOuterFrameRecoversAndYields(
	t *testing.T,
) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	yielding, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.YieldValue(state.String("blocked"))
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("nested_yield", yielding.Value()); err != nil {
		t.Fatal(err)
	}
	callable, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	callableMetatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := callableMetatable.RawSetString(
		"__call",
		yielding.Value(),
	); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(
		callable.Value(),
		callableMetatable,
	); err != nil {
		t.Fatal(err)
	}
	caughtTarget := mustLoadString(
		t,
		state,
		"@caught-nested-yield.lua",
		`return pcall(nested_yield)`,
	)
	recoveryTarget := mustLoadString(
		t,
		state,
		"@after-nested-yield.lua",
		`return 42`,
	)

	var directFailure *Error
	var callableFailure *Error
	host, err := state.NewNativeFunction(func(frame Frame) Outcome {
		_, directErr := frame.Call(yielding.Value())
		if !errors.As(directErr, &directFailure) {
			return frame.RaiseString("direct nested yield was not rejected")
		}
		_, callableErr := frame.Call(callable.Value())
		if !errors.As(callableErr, &callableFailure) {
			return frame.RaiseString("callable nested yield was not rejected")
		}
		caught, callErr := frame.Call(caughtTarget.Value())
		if callErr != nil {
			return frame.RaiseString(callErr.Error())
		}
		recovered, callErr := frame.Call(recoveryTarget.Value())
		if callErr != nil {
			return frame.RaiseString(callErr.Error())
		}
		return frame.YieldValues(
			directFailure.Value(),
			callableFailure.Value(),
			caught[0],
			caught[1],
			recovered[0],
		)
	})
	if err != nil {
		t.Fatal(err)
	}
	thread, err := state.NewThread(host.Value())
	if err != nil {
		t.Fatal(err)
	}
	results, status, err := thread.Resume()
	if err != nil || status != ThreadSuspended {
		t.Fatalf("outer yield = (status=%v, err=%v)", status, err)
	}
	const illegalYield = "attempt to yield across metamethod/C-call boundary"
	assertTestValues(
		t,
		results,
		state.String(illegalYield),
		state.String(illegalYield),
		Bool(false),
		state.String(illegalYield),
		Number(42),
	)
	for name, failure := range map[string]*Error{
		"direct":   directFailure,
		"callable": callableFailure,
	} {
		if failure == nil ||
			failure.Category() != RuntimeError ||
			failure.Error() != illegalYield {
			t.Fatalf("%s nested-yield failure = %#v", name, failure)
		}
	}

	results, status, err = thread.Resume(Number(9), Nil())
	if err != nil || status != ThreadDead {
		t.Fatalf("outer completion = (status=%v, err=%v)", status, err)
	}
	assertTestValues(t, results, Number(9), Nil())
	assertStateExecutionIdle(t, state)
}

func TestFrameCallAllowsYieldFromASeparateChildCoroutine(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	childEntry := mustLoadString(t, state, "@nested-child.lua", `
return coroutine.yield("child-yield")
`)
	child, err := state.NewThread(childEntry.Value())
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("nested_child", child.Value()); err != nil {
		t.Fatal(err)
	}
	target := mustLoadString(t, state, "@nested-child-resume.lua", `
return coroutine.resume(nested_child)
`)
	host, err := state.NewNativeFunction(func(frame Frame) Outcome {
		results, callErr := frame.Call(target.Value())
		if callErr != nil {
			return frame.RaiseString(callErr.Error())
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
	assertTestValues(
		t,
		results,
		Bool(true),
		state.String("child-yield"),
	)
	if child.Status() != ThreadSuspended {
		t.Fatalf("child status = %v; want suspended", child.Status())
	}

	results, status, err := child.Resume(Number(23))
	if err != nil || status != ThreadDead {
		t.Fatalf("child completion = (status=%v, err=%v)", status, err)
	}
	assertTestValues(t, results, Number(23))
	assertStateExecutionIdle(t, state)
}

func TestFrameCallHonorsFrameValueAndNativeDepthLimits(t *testing.T) {
	t.Run("frame staging", func(t *testing.T) {
		state, err := New(Options{MaxFrames: 1})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		target := mustLoadString(t, state, "@nested-frame-limit.lua", `return 1`)
		var failure *Error
		host, err := state.NewNativeFunction(func(frame Frame) Outcome {
			_, callErr := frame.Call(target.Value())
			if !errors.As(callErr, &failure) {
				return frame.RaiseString("frame limit did not fail")
			}
			return frame.ReturnBool(true)
		})
		if err != nil {
			t.Fatal(err)
		}
		results, err := state.Call(host.Value())
		if err != nil {
			t.Fatal(err)
		}
		assertTestValues(t, results, Bool(true))
		if failure.Category() != ResourceError ||
			failure.Error() != "stack overflow" {
			t.Fatalf("frame-limit failure = %#v", failure)
		}
		assertRootThreadReady(t, state.main)
	})

	t.Run("callable staging and result placement", func(t *testing.T) {
		state, err := New(Options{MaxValues: 4})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		calls := 0
		target, err := state.NewNativeFunction(func(frame Frame) Outcome {
			calls++
			return frame.ReturnValues(
				Number(1),
				Number(2),
				Number(3),
				Number(4),
			)
		})
		if err != nil {
			t.Fatal(err)
		}
		callable, err := state.NewTable(0, 0)
		if err != nil {
			t.Fatal(err)
		}
		metatable, err := state.NewTable(0, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := metatable.RawSetString("__call", target.Value()); err != nil {
			t.Fatal(err)
		}
		if err := state.SetMetatable(callable.Value(), metatable); err != nil {
			t.Fatal(err)
		}

		var stagingFailure *Error
		var resultFailure *Error
		host, err := state.NewNativeFunction(func(frame Frame) Outcome {
			_, stagingErr := frame.Call(
				callable.Value(),
				Number(1),
				Number(2),
				Number(3),
			)
			if !errors.As(stagingErr, &stagingFailure) {
				return frame.RaiseString("callable staging did not fail")
			}
			_, resultErr := frame.Call(target.Value())
			if !errors.As(resultErr, &resultFailure) {
				return frame.RaiseString("nested result placement did not fail")
			}
			return frame.ReturnBool(true)
		})
		if err != nil {
			t.Fatal(err)
		}
		results, err := state.Call(host.Value())
		if err != nil {
			t.Fatal(err)
		}
		assertTestValues(t, results, Bool(true))
		if stagingFailure.Category() != ResourceError ||
			resultFailure.Category() != ResourceError {
			t.Fatalf(
				"value-limit failures = (%#v, %#v)",
				stagingFailure,
				resultFailure,
			)
		}
		if calls != 1 {
			t.Fatalf("nested target calls = %d; want 1", calls)
		}
		assertRootThreadReady(t, state.main)
	})

	t.Run("aggregate native depth", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		depth := 0
		peak := 0
		var limitFailure *Error
		var recursive *Function
		recursive, err = state.NewNativeFunction(func(frame Frame) Outcome {
			depth++
			if depth > peak {
				peak = depth
			}
			if limitFailure == nil {
				_, callErr := frame.Call(recursive.Value())
				if callErr != nil &&
					!errors.As(callErr, &limitFailure) {
					panic(callErr)
				}
			}
			depth--
			return frame.Return()
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := state.Call(recursive.Value()); err != nil {
			t.Fatal(err)
		}
		if limitFailure == nil ||
			limitFailure.Category() != ResourceError ||
			limitFailure.Error() != "C stack overflow" {
			t.Fatalf("native-depth failure = %#v", limitFailure)
		}
		if peak != maxNativeCallDepth {
			t.Fatalf(
				"peak native depth = %d; want %d",
				peak,
				maxNativeCallDepth,
			)
		}
		assertRootThreadReady(t, state.main)
		recovery, err := state.NewNativeFunction(func(frame Frame) Outcome {
			return frame.ReturnNumber(7)
		})
		if err != nil {
			t.Fatal(err)
		}
		results, err := state.Call(recovery.Value())
		if err != nil {
			t.Fatal(err)
		}
		assertTestValues(t, results, Number(7))
	})
}
