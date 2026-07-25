package lua

import (
	"errors"
	"runtime"
	"strings"
	"testing"
)

func TestThreadResumePreservesYieldAndResumeValues(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	installYieldTestFunction(t, state)

	entry := mustLoadString(t, state, "@resume-values.lua", `
local first, second = ...
local a, b, c = native_yield(first, nil, second)
return a, b, c
`)
	thread, err := state.NewThread(entry.Value())
	if err != nil {
		t.Fatal(err)
	}
	if thread.Status() != ThreadSuspended || thread.IsMain() {
		t.Fatalf("new coroutine status = %v", thread.Status())
	}

	results, status, err := thread.Resume(Number(10), Number(20))
	if err != nil {
		t.Fatal(err)
	}
	if status != ThreadSuspended {
		t.Fatalf("first resume status = %v; want suspended", status)
	}
	assertTestValues(
		t,
		results,
		Number(10),
		Nil(),
		Number(20),
	)
	if len(thread.frames) == 0 ||
		thread.openUpvalues != nil ||
		thread.activeNativeToken != 0 ||
		thread.nativeCallDepth != 0 {
		t.Fatal("yielded coroutine did not retain clean executable state")
	}
	if retained := thread.frames[len(thread.frames)-1]; thread.frameExtent !=
		int(retained.callerExtent) {
		t.Fatalf(
			"suspended extent = %d; want caller extent %d",
			thread.frameExtent,
			retained.callerExtent,
		)
	}

	results, status, err = thread.Resume(Number(30), Nil(), Number(50))
	if err != nil {
		t.Fatal(err)
	}
	if status != ThreadDead {
		t.Fatalf("final resume status = %v; want dead", status)
	}
	assertTestValues(
		t,
		results,
		Number(30),
		Nil(),
		Number(50),
	)
	assertDeadCoroutineClean(t, thread)

	if _, status, err = thread.Resume(); err == nil ||
		err.Error() != "cannot resume dead coroutine" ||
		status != ThreadDead {
		t.Fatalf("dead resume = (%v, %v); want dead-coroutine error", status, err)
	}
	assertStateExecutionIdle(t, state)
}

func TestThreadResumeAppliesCallResultAdjustmentAfterYield(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	installYieldTestFunction(t, state)

	entry := mustLoadString(t, state, "@resume-adjustment.lua", `
local one = native_yield("first", "second")
local a, b, c = native_yield("third")
return one, a, b, c
`)
	thread, err := state.NewThread(entry.Value())
	if err != nil {
		t.Fatal(err)
	}

	results, status, err := thread.Resume()
	if err != nil {
		t.Fatal(err)
	}
	if status != ThreadSuspended {
		t.Fatalf("first status = %v", status)
	}
	assertTestValues(
		t,
		results,
		state.String("first"),
		state.String("second"),
	)

	results, status, err = thread.Resume(Number(7), Number(8))
	if err != nil {
		t.Fatal(err)
	}
	if status != ThreadSuspended {
		t.Fatalf("second status = %v", status)
	}
	assertTestValues(t, results, state.String("third"))

	results, status, err = thread.Resume(Number(9))
	if err != nil {
		t.Fatal(err)
	}
	if status != ThreadDead {
		t.Fatalf("final status = %v", status)
	}
	assertTestValues(
		t,
		results,
		Number(7),
		Number(9),
		Nil(),
		Nil(),
	)
}

func TestThreadResumeCompletesTailCalledNativeYield(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	installYieldTestFunction(t, state)

	entry := mustLoadString(
		t,
		state,
		"@tail-yield.lua",
		`return native_yield("tail")`,
	)
	thread, err := state.NewThread(entry.Value())
	if err != nil {
		t.Fatal(err)
	}
	results, status, err := thread.Resume()
	if err != nil {
		t.Fatal(err)
	}
	if status != ThreadSuspended {
		t.Fatalf("yield status = %v", status)
	}
	assertTestValues(t, results, state.String("tail"))

	results, status, err = thread.Resume(Number(1), Nil(), Number(3))
	if err != nil {
		t.Fatal(err)
	}
	if status != ThreadDead {
		t.Fatalf("return status = %v", status)
	}
	assertTestValues(t, results, Number(1), Nil(), Number(3))
}

func TestOpenUpvalueSurvivesYieldAndClosesOnReturn(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	installYieldTestFunction(t, state)

	var retained *Function
	publish, err := state.NewNativeFunction(func(frame Frame) Outcome {
		function, ok := frame.Function(0)
		if !ok {
			return frame.ArgTypeError(0, FunctionKind)
		}
		retained = function
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("publish_closure", publish.Value()); err != nil {
		t.Fatal(err)
	}
	entry := mustLoadString(t, state, "@yield-upvalue.lua", `
local value = 5
publish_closure(function() return value end)
native_yield()
value = value + 1
return value
`)
	thread, err := state.NewThread(entry.Value())
	if err != nil {
		t.Fatal(err)
	}
	results, status, err := thread.Resume()
	if err != nil {
		t.Fatal(err)
	}
	if status != ThreadSuspended || len(results) != 0 || retained == nil {
		t.Fatalf("initial yield = (%v, %v), closure=%p", status, results, retained)
	}
	if thread.openUpvalues == nil {
		t.Fatal("yield closed a live upvalue")
	}

	results, err = state.Call(retained.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(5))

	results, status, err = thread.Resume()
	if err != nil {
		t.Fatal(err)
	}
	if status != ThreadDead {
		t.Fatalf("final status = %v", status)
	}
	assertTestValues(t, results, Number(6))
	if thread.openUpvalues != nil {
		t.Fatal("return retained an open upvalue")
	}
	results, err = state.Call(retained.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(6))
}

func TestYieldBoundaryMatchesLua51CallKinds(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	yield := installYieldTestFunction(t, state)

	if _, err := state.Call(yield.Value(), Number(1)); err == nil ||
		err.Error() != "attempt to yield across metamethod/C-call boundary" {
		t.Fatalf("main-thread yield error = %v", err)
	}

	indexed, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	metatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString("__index", yield.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(indexed.Value(), metatable); err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("indexed_for_yield", indexed.Value()); err != nil {
		t.Fatal(err)
	}
	indexEntry := mustLoadString(
		t,
		state,
		"@index-yield.lua",
		`return indexed_for_yield.missing`,
	)
	indexThread, err := state.NewThread(indexEntry.Value())
	if err != nil {
		t.Fatal(err)
	}
	if _, status, resumeErr := indexThread.Resume(); resumeErr == nil ||
		resumeErr.Error() !=
			"attempt to yield across metamethod/C-call boundary" ||
		status != ThreadDead {
		t.Fatalf("metamethod yield = (%v, %v)", status, resumeErr)
	}

	iteratorEntry := mustLoadString(t, state, "@iterator-yield.lua", `
for value in native_yield do
end
`)
	iteratorThread, err := state.NewThread(iteratorEntry.Value())
	if err != nil {
		t.Fatal(err)
	}
	if _, status, resumeErr := iteratorThread.Resume(); resumeErr == nil ||
		resumeErr.Error() !=
			"attempt to yield across metamethod/C-call boundary" ||
		status != ThreadDead {
		t.Fatalf("iterator yield = (%v, %v)", status, resumeErr)
	}

	callable, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	callMetatable, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := callMetatable.RawSetString("__call", yield.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(callable.Value(), callMetatable); err != nil {
		t.Fatal(err)
	}
	callThread, err := state.NewThread(callable.Value())
	if err != nil {
		t.Fatal(err)
	}
	results, status, err := callThread.Resume(Number(17))
	if err != nil {
		t.Fatal(err)
	}
	if status != ThreadSuspended || len(results) != 2 {
		t.Fatalf("__call yield = (%v, %v)", status, results)
	}
	if same, applicable := results[0].SameObject(callable.Value()); !applicable || !same {
		t.Fatal("__call yield lost the original callable")
	}
	assertTestValue(t, results[1], Number(17))
	results, status, err = callThread.Resume(Number(99))
	if err != nil {
		t.Fatal(err)
	}
	if status != ThreadDead {
		t.Fatalf("__call final status = %v", status)
	}
	assertTestValues(t, results, Number(99))
	assertStateExecutionIdle(t, state)
}

func TestCoroutinePanicAndCloseRestoreStateOwnership(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}

	panicEntry, err := state.NewNativeFunction(func(Frame) Outcome {
		panic("coroutine panic")
	})
	if err != nil {
		t.Fatal(err)
	}
	panicThread, err := state.NewThread(panicEntry.Value())
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if recovered := recover(); recovered != "coroutine panic" {
				t.Fatalf("panic = %v", recovered)
			}
		}()
		panicThread.Resume()
	}()
	if panicThread.Status() != ThreadDead {
		t.Fatalf("panicked coroutine status = %v", panicThread.Status())
	}
	assertDeadCoroutineClean(t, panicThread)
	assertStateExecutionIdle(t, state)

	var closeErr error
	closeEntry, err := state.NewNativeFunction(func(frame Frame) Outcome {
		closeErr = state.Close()
		return frame.Yield()
	})
	if err != nil {
		t.Fatal(err)
	}
	closeThread, err := state.NewThread(closeEntry.Value())
	if err != nil {
		t.Fatal(err)
	}
	if _, status, err := closeThread.Resume(); err != nil ||
		status != ThreadSuspended ||
		!errors.Is(closeErr, ErrRunning) {
		t.Fatalf(
			"close during coroutine = (status=%v, resume=%v, close=%v)",
			status,
			err,
			closeErr,
		)
	}
	if _, status, err := closeThread.Resume(); err != nil ||
		status != ThreadDead {
		t.Fatalf("final native resume = (%v, %v)", status, err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if closeThread.Status() != ThreadClosed {
		t.Fatalf("closed coroutine status = %v", closeThread.Status())
	}
	if _, _, err := closeThread.Resume(); !errors.Is(err, ErrClosed) {
		t.Fatalf("resume after Close = %v; want ErrClosed", err)
	}
}

func TestCoroutineFailureClosesUpvaluesAndPreservesErrorValue(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	var retained *Function
	publish, err := state.NewNativeFunction(func(frame Frame) Outcome {
		function, ok := frame.Function(0)
		if !ok {
			return frame.ArgTypeError(0, FunctionKind)
		}
		retained = function
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	marker, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	fail, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.Raise(marker.Value())
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("publish_closure", publish.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("fail_coroutine", fail.Value()); err != nil {
		t.Fatal(err)
	}

	entry := mustLoadString(t, state, "@failed-upvalue.lua", `
local value = 41
publish_closure(function() return value end)
fail_coroutine()
value = 99
`)
	thread, err := state.NewThread(entry.Value())
	if err != nil {
		t.Fatal(err)
	}
	results, status, err := thread.Resume()
	if err == nil || status != ThreadDead || len(results) != 0 {
		t.Fatalf(
			"failed resume = (results=%v, status=%v, err=%v)",
			results,
			status,
			err,
		)
	}
	failure, ok := err.(*Error)
	if !ok {
		t.Fatalf("failure type = %T; want *Error", err)
	}
	if same, applicable := failure.Value().SameObject(marker.Value()); !applicable || !same {
		t.Fatal("coroutine failure lost the arbitrary Lua error value")
	}
	assertDeadCoroutineClean(t, thread)
	if retained == nil {
		t.Fatal("failure lost the escaped closure")
	}
	results, err = state.Call(retained.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(41))
}

func TestThreadIngressAndResultCapacityAreAtomic(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	other, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	installYieldTestFunction(t, state)

	entry := mustLoadString(t, state, "@resume-ingress.lua", `
local first = ...
native_yield(first)
return 2, nil, 4
`)
	thread, err := state.NewThread(entry.Value())
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := other.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for name, argumentError := range map[string]struct {
		value Value
		want  error
	}{
		"invalid": {value: Value{}, want: ErrInvalidValue},
		"foreign": {value: foreign.Value(), want: ErrForeignValue},
	} {
		t.Run(name, func(t *testing.T) {
			if _, status, resumeErr := thread.Resume(argumentError.value); !errors.Is(resumeErr, argumentError.want) ||
				status != ThreadSuspended {
				t.Fatalf(
					"invalid resume = (status=%v, err=%v); want %v",
					status,
					resumeErr,
					argumentError.want,
				)
			}
			if thread.top != 1 || len(thread.frames) != 0 {
				t.Fatal("rejected argument changed the initial coroutine")
			}
		})
	}

	results, status, err := thread.Resume(Number(1))
	if err != nil || status != ThreadSuspended {
		t.Fatalf("valid resume = (status=%v, err=%v)", status, err)
	}
	assertTestValues(t, results, Number(1))

	destination := []Value{Number(99)}
	count, status, err := thread.ResumeInto(nil, destination)
	var capacityError *ResultCapacityError
	if !errors.As(err, &capacityError) ||
		count != 3 ||
		status != ThreadDead ||
		capacityError.Required != 3 ||
		capacityError.Available != 1 {
		t.Fatalf(
			"short destination = (count=%d, status=%v, err=%v)",
			count,
			status,
			err,
		)
	}
	assertTestValues(t, destination, Number(99))
	assertDeadCoroutineClean(t, thread)
}

func TestThreadConstructionAndResumePreflight(t *testing.T) {
	state, err := New(Options{MaxValues: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	other, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()

	if _, err := state.NewThread(Value{}); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("NewThread with invalid value = %v", err)
	}
	foreign, err := other.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.NewThread(foreign.Value()); !errors.Is(err, ErrForeignValue) {
		t.Fatalf("NewThread with foreign value = %v", err)
	}
	if _, err := state.NewThread(Number(1)); err == nil ||
		err.Error() != "attempt to call a number value" {
		t.Fatalf("NewThread with number = %v", err)
	}

	calls := 0
	entry, err := state.NewNativeFunction(func(frame Frame) Outcome {
		calls++
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	thread, err := state.NewThread(entry.Value())
	if err != nil {
		t.Fatal(err)
	}
	if _, status, err := thread.Resume(Number(1), Number(2)); err == nil ||
		status != ThreadSuspended ||
		calls != 0 {
		t.Fatalf(
			"oversized resume = (status=%v, calls=%d, err=%v)",
			status,
			calls,
			err,
		)
	} else if failure, ok := err.(*Error); !ok ||
		failure.Category() != ResourceError ||
		failure.Error() != "too many arguments to resume" {
		t.Fatalf("oversized resume failure = %#v", err)
	}
	if thread.top != 1 || len(thread.frames) != 0 {
		t.Fatal("resume preflight changed the suspended coroutine")
	}
	if results, status, err := thread.Resume(Number(1)); err != nil ||
		status != ThreadDead ||
		len(results) != 0 ||
		calls != 1 {
		t.Fatalf(
			"valid resume = (results=%v, status=%v, calls=%d, err=%v)",
			results,
			status,
			calls,
			err,
		)
	}
}

func TestCoroutineNativeDepthIsBoundAcrossThreads(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	var recursive *Function
	recursive, err = state.NewNativeFunction(func(frame Frame) Outcome {
		child, constructionError := state.NewThread(recursive.Value())
		if constructionError != nil {
			panic(constructionError)
		}
		run := resumeThread(
			frame.thread,
			child,
			resumeArguments{},
		)
		defer run.release()
		if run.failure != nil {
			return frame.sealError(run.failure)
		}
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	thread, err := state.NewThread(recursive.Value())
	if err != nil {
		t.Fatal(err)
	}
	if _, status, err := thread.Resume(); err == nil ||
		status != ThreadDead {
		t.Fatalf("recursive resume = (status=%v, err=%v)", status, err)
	} else if failure, ok := err.(*Error); !ok ||
		failure.Category() != ResourceError ||
		failure.Error() != "C stack overflow" {
		t.Fatalf("recursive resume failure = %#v", err)
	}
	assertDeadCoroutineClean(t, thread)
	assertStateExecutionIdle(t, state)
}

func TestWarmCoroutineResumeIntoDoesNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	installYieldTestFunction(t, state)
	entry := mustLoadString(t, state, "@warm-resume.lua", `
while true do
	native_yield(1)
end
`)
	thread, err := state.NewThread(entry.Value())
	if err != nil {
		t.Fatal(err)
	}
	destination := make([]Value, 1)
	for range 4 {
		count, status, resumeErr := thread.ResumeInto(nil, destination)
		if resumeErr != nil || count != 1 || status != ThreadSuspended {
			t.Fatalf(
				"warmup = (count=%d, status=%v, err=%v)",
				count,
				status,
				resumeErr,
			)
		}
	}
	allocations := testing.AllocsPerRun(1_000, func() {
		count, status, resumeErr := thread.ResumeInto(nil, destination)
		if resumeErr != nil || count != 1 || status != ThreadSuspended {
			panic("warm coroutine resume failed")
		}
	})
	if allocations != 0 {
		t.Fatalf("warm resume allocations = %v; want 0", allocations)
	}
	runtime.KeepAlive(thread)
}

func BenchmarkCoroutineResumeYield(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = state.Close()
	})
	installYieldTestFunction(b, state)
	entry := mustLoadString(b, state, "@benchmark-resume.lua", `
while true do
	native_yield(1)
end
`)
	thread, err := state.NewThread(entry.Value())
	if err != nil {
		b.Fatal(err)
	}
	destination := make([]Value, 1)
	if _, _, err := thread.ResumeInto(nil, destination); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, _, err := thread.ResumeInto(nil, destination); err != nil {
			b.Fatal(err)
		}
	}
}

func installYieldTestFunction(t testing.TB, state *State) *Function {
	t.Helper()
	yield, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.YieldArguments()
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("native_yield", yield.Value()); err != nil {
		t.Fatal(err)
	}
	return yield
}

func assertDeadCoroutineClean(t *testing.T, thread *Thread) {
	t.Helper()
	if thread.status != ThreadDead ||
		thread.top != 0 ||
		thread.frameExtent != 0 ||
		len(thread.frames) != 0 ||
		len(thread.continuations) != 0 ||
		thread.openUpvalues != nil ||
		thread.activeNativeToken != 0 ||
		thread.nativeCallDepth != 0 ||
		thread.errorHandlerDepth != 0 ||
		cap(thread.values) != 0 ||
		cap(thread.frames) != 0 ||
		cap(thread.continuations) != 0 {
		t.Fatalf(
			"dead coroutine retained execution: status=%v top=%d extent=%d "+
				"values=%d/%d frames=%d/%d continuations=%d/%d "+
				"upvalues=%p token=%d native=%d handler=%d",
			thread.status,
			thread.top,
			thread.frameExtent,
			len(thread.values),
			cap(thread.values),
			len(thread.frames),
			cap(thread.frames),
			len(thread.continuations),
			cap(thread.continuations),
			thread.openUpvalues,
			thread.activeNativeToken,
			thread.nativeCallDepth,
			thread.errorHandlerDepth,
		)
	}
	for index, value := range thread.values {
		if value != (slot{}) {
			t.Fatalf("dead coroutine slot %d retained %v", index, value.owningValue())
		}
	}
}

func assertStateExecutionIdle(t *testing.T, state *State) {
	t.Helper()
	if state.active != nil ||
		state.runtime.nativeCallDepth != 0 ||
		state.limits.values != state.options.MaxValues ||
		state.limits.frames != state.options.MaxFrames {
		t.Fatalf(
			"state retained execution: active=%p native=%d limits=%+v",
			state.active,
			state.runtime.nativeCallDepth,
			state.limits,
		)
	}
	assertRootThreadReady(t, state.MainThread())
}

func assertCoroutineErrorContains(t *testing.T, err error, text string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), text) {
		t.Fatalf("error = %v; want text %q", err, text)
	}
}
