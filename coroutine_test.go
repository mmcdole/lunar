package lua

import (
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"
	"weak"
)

func TestThreadRepresentationsAndCanonicalPublication(t *testing.T) {
	handleSize := unsafe.Sizeof(Thread{})
	wantHandleSize := 3 * unsafe.Sizeof(uintptr(0))
	if handleSize != wantHandleSize ||
		handleSize != unsafe.Sizeof(hostToken{}) {
		t.Fatalf(
			"Thread size = %d; want %d and host token size %d",
			handleSize,
			wantHandleSize,
			unsafe.Sizeof(hostToken{}),
		)
	}
	if unsafe.Sizeof(uintptr(0)) == 8 {
		if size := unsafe.Sizeof(threadObject{}); size != 136 {
			t.Fatalf("thread object size = %d; want 136", size)
		}
	}

	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if entries, keys, stale := hostDirectoryKindCounts(
		&state.runtime.hosts,
		ThreadKind,
	); entries != 0 || keys != 0 || stale != 0 {
		t.Fatalf(
			"new State published its main thread: entries=%d keys=%d stale=%d",
			entries,
			keys,
			stale,
		)
	}

	main := state.MainThread()
	if main == nil {
		t.Fatal("MainThread returned nil")
	}
	token := main.token()
	object := main.runtimeObject()
	if token == nil || object == nil {
		t.Fatal("main thread lost its public or compact representation")
	}
	if unsafe.Pointer(main) != unsafe.Pointer(token) {
		t.Fatal("Thread is not an offset-zero host-token view")
	}
	if unsafe.Pointer(&object.objectHeader) != unsafe.Pointer(object) {
		t.Fatal("thread object header is not at offset zero")
	}
	if unsafe.Pointer(main) == unsafe.Pointer(object) {
		t.Fatal("public Thread exposed the compact thread object")
	}
	key := weak.Make(&object.objectHeader)
	if state.runtime.hosts.entries[key].Value() != token {
		t.Fatal("thread object does not have its live token in the directory")
	}

	public := main.Value()
	if public.bits != uint64(ThreadKind) {
		t.Fatalf("public Thread bits = %#x; want ThreadKind", public.bits)
	}
	compact := slotFromValue(public)
	if compact != slotFromThreadObject(object) {
		t.Fatal("Thread Value did not restore the compact thread slot")
	}
	if compact.ref == public.ref {
		t.Fatal("public Thread Value exposed the compact object pointer")
	}
	fromValue, ok := public.AsThread()
	if !ok || fromValue != main {
		t.Fatalf(
			"Thread Value round trip = (%p, %v); want (%p, true)",
			fromValue,
			ok,
			main,
		)
	}
	if state.MainThread() != main {
		t.Fatal("MainThread did not return its canonical live handle")
	}
	fromCompact, ok := compact.owningValue().AsThread()
	if !ok || fromCompact != main {
		t.Fatalf(
			"compact Thread publication = (%p, %v); want (%p, true)",
			fromCompact,
			ok,
			main,
		)
	}
	if entries, keys, stale := hostDirectoryKindCounts(
		&state.runtime.hosts,
		ThreadKind,
	); entries != 1 || keys != 1 || stale != 0 {
		t.Fatalf(
			"main Thread directory = entries:%d keys:%d stale:%d; want 1/1/0",
			entries,
			keys,
			stale,
		)
	}
	runtime.KeepAlive(main)
}

func TestWarmThreadPublicationDoesNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	entry := compileTestFunction(
		t,
		state,
		"@warm-thread-publication.lua",
		`return`,
	)
	object, err := state.newThreadObject(slotFromFunctionObject(entry))
	if err != nil {
		t.Fatal(err)
	}
	first := object.owningHandle()
	compact := slotFromThreadObject(object)

	var published *Thread
	allocations := testing.AllocsPerRun(1_000, func() {
		value := compact.owningValue()
		published, _ = value.AsThread()
	})
	if allocations != 0 {
		t.Fatalf(
			"warm thread publication allocated %.2f times",
			allocations,
		)
	}
	if published != first {
		t.Fatalf(
			"warm thread publication = %p; want %p",
			published,
			first,
		)
	}
	runtime.KeepAlive(first)
}

func TestThreadRepublishAfterOwningTokenDies(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	object, token := rootedThreadWithoutHandle(t, state)
	index := newTable(state, 0, 1)
	index.rawSetSlot(slotFromThreadObject(object), numberSlot(73))
	waitForWeakThreadToken(t, object, token)

	first, ok := state.registry.rawGetStringValue("rooted thread").AsThread()
	if !ok || first.runtimeObject() != object {
		t.Fatal("re-publication changed compact thread identity")
	}
	second, ok := state.registry.rawGetStringValue("rooted thread").AsThread()
	if !ok || second != first {
		t.Fatalf(
			"second re-publication = (%p, %v); want (%p, true)",
			second,
			ok,
			first,
		)
	}
	stored, err := index.rawGetValue(first.Value())
	if err != nil {
		t.Fatal(err)
	}
	if number, ok := stored.AsNumber(); !ok || number != 73 {
		t.Fatalf(
			"thread-key lookup after token replacement = (%v, %v); want 73",
			number,
			ok,
		)
	}
	if entries, keys, stale := hostDirectoryKindCounts(
		&state.runtime.hosts,
		ThreadKind,
	); entries != 1 || keys != 1 || stale != 0 {
		t.Fatalf(
			"thread directory = entries:%d keys:%d stale:%d; want 1/1/0",
			entries,
			keys,
			stale,
		)
	}

	results, status, err := first.Resume()
	if err != nil || status != ThreadDead {
		t.Fatalf(
			"re-published thread resume = (status=%v, err=%v)",
			status,
			err,
		)
	}
	assertTestValues(t, results, Number(42))
	runtime.KeepAlive(first)
}

func TestHostDirectoryDoesNotPinCyclicThread(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	object, token := weakCyclicThreadPublication(t, state)
	waitForWeakThread(t, state, object, token)
	state.runtime.hosts.prune()
	if entries, keys, stale := hostDirectoryKindCounts(
		&state.runtime.hosts,
		ThreadKind,
	); entries != 0 || keys != 0 || stale != 0 {
		t.Fatalf(
			"dead thread remains in host directory: entries=%d keys=%d stale=%d",
			entries,
			keys,
			stale,
		)
	}
	runtime.KeepAlive(state)
}

func TestThreadHandleSupportsObservationAfterClose(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	main := state.MainThread()
	entry := compileTestFunction(
		t,
		state,
		"@retained-thread.lua",
		`return 9`,
	)
	child, err := state.NewThread(entry.owningValue())
	if err != nil {
		t.Fatal(err)
	}
	mainValue := main.Value()
	childValue := child.Value()

	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if main.Status() != ThreadClosed || child.Status() != ThreadClosed {
		t.Fatalf(
			"closed statuses = (%v, %v); want closed/closed",
			main.Status(),
			child.Status(),
		)
	}
	if !main.IsMain() || child.IsMain() {
		t.Fatal("State close changed main-thread identity")
	}
	if main.State() != state || child.State() != state {
		t.Fatal("State close detached a retained Thread handle")
	}
	if state.MainThread() != main {
		t.Fatal("post-close MainThread publication changed identity")
	}
	publishedMain, mainOK := mainValue.AsThread()
	publishedChild, childOK := childValue.AsThread()
	if !mainOK || publishedMain != main ||
		!childOK || publishedChild != child {
		t.Fatalf(
			"post-close Value round trips = (%p, %v), (%p, %v)",
			publishedMain,
			mainOK,
			publishedChild,
			childOK,
		)
	}
	if same, applicable := childValue.SameObject(
		child.Value(),
	); !applicable || !same {
		t.Fatalf(
			"post-close child identity = (%v, %v); want (true, true)",
			same,
			applicable,
		)
	}
	if _, status, resumeErr := child.Resume(); status != ThreadClosed ||
		!errors.Is(resumeErr, ErrClosed) {
		t.Fatalf(
			"post-close resume = (status=%v, err=%v); want closed/ErrClosed",
			status,
			resumeErr,
		)
	}
	if _, environmentErr := threadEnvironment(
		child,
	); !errors.Is(environmentErr, ErrClosed) {
		t.Fatalf(
			"post-close ThreadEnvironment = %v; want ErrClosed",
			environmentErr,
		)
	}
	runtime.KeepAlive(main)
	runtime.KeepAlive(child)
}

func TestThreadOwningHandleRejectsInvalidAndForeignUse(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	environment, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	other, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()

	var zero Thread
	for name, thread := range map[string]*Thread{
		"nil":  nil,
		"zero": &zero,
	} {
		t.Run(name, func(t *testing.T) {
			if thread.Value().Valid() {
				t.Fatal("invalid Thread manufactured a valid Value")
			}
			if thread.State() != nil ||
				thread.Status() != ThreadClosed ||
				thread.IsMain() {
				t.Fatal("invalid Thread exposed live metadata")
			}
			if _, threadErr := threadEnvironment(
				thread,
			); !errors.Is(threadErr, ErrInvalidValue) {
				t.Fatalf(
					"ThreadEnvironment = %v; want ErrInvalidValue",
					threadErr,
				)
			}
			if threadErr := setThreadEnvironment(
				thread,
				environment,
			); !errors.Is(threadErr, ErrInvalidValue) {
				t.Fatalf(
					"SetThreadEnvironment = %v; want ErrInvalidValue",
					threadErr,
				)
			}
			if _, status, resumeErr := thread.Resume(); status != ThreadClosed ||
				!errors.Is(resumeErr, ErrClosed) {
				t.Fatalf(
					"Resume = (status=%v, err=%v); want closed/ErrClosed",
					status,
					resumeErr,
				)
			}
		})
	}

	foreign := other.MainThread()
	if threadErr := state.RawSetGlobal(
		"foreign_thread",
		foreign.Value(),
	); !errors.Is(threadErr, ErrForeignValue) {
		t.Fatalf(
			"foreign Thread Value storage = %v; want ErrForeignValue",
			threadErr,
		)
	}
}

func TestLuaOnlyCoroutinesDoNotPublishThreadHandles(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}
	assertThreadHandleCount(t, state, "opening base and coroutine libraries", 0)

	chunk := compileTestFunction(t, state, "@compact-coroutines.lua", `
local main=coroutine.running()
local thread
thread=coroutine.create(function(first)
	local self=coroutine.running()
	local resumed=coroutine.yield(
		self==thread,
		coroutine.status(self),
		first
	)
	return resumed+1
end)
local firstOK,same,running,first=coroutine.resume(thread,40)
local suspended=coroutine.status(thread)
local secondOK,result=coroutine.resume(thread,41)
local dead=coroutine.status(thread)
local wrapped=coroutine.wrap(function(value)
	return coroutine.running()~=nil,value+1
end)
local wrappedRunning,wrappedResult=wrapped(41)
return main==nil and
	firstOK and same and running=="running" and first==40 and
	suspended=="suspended" and secondOK and result==42 and
	dead=="dead" and wrappedRunning and wrappedResult==42
`)
	handle := chunk.owningHandle()
	results, err := state.Call(handle.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Bool(true))
	assertThreadHandleCount(t, state, "Lua-only coroutine execution", 0)
	runtime.KeepAlive(handle)
}

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
	object := thread.runtimeObject()
	if len(object.frames) == 0 ||
		object.openUpvalues != nil ||
		object.activeNativeToken != 0 ||
		object.nativeCallDepth != 0 {
		t.Fatal("yielded coroutine did not retain clean executable state")
	}
	if retained := object.frames[len(object.frames)-1]; object.frameExtent !=
		int(retained.callerExtent) {
		t.Fatalf(
			"suspended extent = %d; want caller extent %d",
			object.frameExtent,
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

func TestThreadGlobalEnvironmentsInheritAndRouteStateOperations(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	mainEnvironment, err := threadEnvironment(state.MainThread())
	if err != nil {
		t.Fatal(err)
	}
	if err := state.RawSetGlobal("environment_marker", state.String("main")); err != nil {
		t.Fatal(err)
	}

	childEnvironment, err := state.NewTableWithCapacity(0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := childEnvironment.RawSetString(
		"environment_marker",
		state.String("child"),
	); err != nil {
		t.Fatal(err)
	}

	var probe *Function
	var nested *Thread
	probe, err = state.NewNativeFunction(func(frame Frame) Outcome {
		if frame.activation().function.environment.owningHandle() != mainEnvironment {
			frame.ThrowString("native function environment changed")
		}
		if frame.thread.globals.owningHandle() != childEnvironment {
			frame.ThrowString("callback did not observe child globals")
		}
		marker, globalErr := state.RawGlobal("environment_marker")
		if globalErr != nil {
			frame.ThrowString(globalErr.Error())
		}
		if text, ok := marker.AsString(); !ok || text != "child" {
			frame.ThrowString("State.Global did not use child globals")
		}
		if globalErr := state.RawSetGlobal("child_write", Number(42)); globalErr != nil {
			frame.ThrowString(globalErr.Error())
		}
		nested, globalErr = state.NewThread(probe.Value())
		if globalErr != nil {
			frame.ThrowString(globalErr.Error())
		}
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}

	child, err := state.NewThread(probe.Value())
	if err != nil {
		t.Fatal(err)
	}
	if inherited, environmentErr := threadEnvironment(
		child,
	); environmentErr != nil || inherited != mainEnvironment {
		t.Fatalf(
			"initial child environment = (%p, %v); want %p",
			inherited,
			environmentErr,
			mainEnvironment,
		)
	}
	if err := setThreadEnvironment(child, childEnvironment); err != nil {
		t.Fatal(err)
	}
	results, status, err := child.Resume()
	if err != nil || status != ThreadDead || len(results) != 0 {
		t.Fatalf(
			"child resume = (results=%v, status=%v, err=%v)",
			results,
			status,
			err,
		)
	}
	if nested == nil {
		t.Fatal("callback did not construct a nested coroutine")
	}
	if inherited, environmentErr := threadEnvironment(
		nested,
	); environmentErr != nil || inherited != childEnvironment {
		t.Fatalf(
			"nested environment = (%p, %v); want %p",
			inherited,
			environmentErr,
			childEnvironment,
		)
	}
	if got, ok := rawStr(childEnvironment, "child_write").AsNumber(); !ok ||
		got != 42 {
		t.Fatalf("child environment write = (%v, %v); want 42", got, ok)
	}
	if got := rawStr(mainEnvironment, "child_write"); !got.IsNil() {
		t.Fatalf("child write leaked into main environment: %v", got)
	}
	marker, err := state.RawGlobal("environment_marker")
	if err != nil {
		t.Fatal(err)
	}
	if text, ok := marker.AsString(); !ok || text != "main" {
		t.Fatalf("idle State.Global = (%q, %v); want main", text, ok)
	}

	sibling, err := state.NewThread(probe.Value())
	if err != nil {
		t.Fatal(err)
	}
	if inherited, environmentErr := threadEnvironment(
		sibling,
	); environmentErr != nil || inherited != mainEnvironment {
		t.Fatalf(
			"idle sibling environment = (%p, %v); want %p",
			inherited,
			environmentErr,
			mainEnvironment,
		)
	}
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
			frame.ThrowArgTypeError(0, FunctionKind)
		}
		retained = function
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.RawSetGlobal("publish_closure", publish.Value()); err != nil {
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
	if thread.runtimeObject().openUpvalues == nil {
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
	if thread.runtimeObject().openUpvalues != nil {
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

	indexed, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	metatable, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := metatable.RawSetString("__index", yield.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(indexed.Value(), metatable); err != nil {
		t.Fatal(err)
	}
	if err := state.RawSetGlobal("indexed_for_yield", indexed.Value()); err != nil {
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

	callable, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	callMetatable, err := state.NewTableWithCapacity(0, 1)
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
			frame.ThrowArgTypeError(0, FunctionKind)
		}
		retained = function
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	marker, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	fail, err := state.NewNativeFunction(func(frame Frame) Outcome {
		frame.Throw(marker.Value())
		// Unreachable: the throw above does not return.
		return Outcome{}
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.RawSetGlobal("publish_closure", publish.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.RawSetGlobal("fail_coroutine", fail.Value()); err != nil {
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
	foreign, err := other.NewTableWithCapacity(0, 0)
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
			if thread.runtimeObject().top != 1 ||
				len(thread.runtimeObject().frames) != 0 {
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
	assertTestValues(
		t,
		capacityError.Results(),
		Number(2),
		Nil(),
		Number(4),
	)
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
	foreign, err := other.NewTableWithCapacity(0, 0)
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
	if thread.runtimeObject().top != 1 ||
		len(thread.runtimeObject().frames) != 0 {
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
			child.runtimeObject(),
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

func BenchmarkWarmThreadPublication(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = state.Close()
	})
	entry := compileTestFunction(
		b,
		state,
		"@benchmark-thread-publication.lua",
		`return`,
	)
	object, err := state.newThreadObject(slotFromFunctionObject(entry))
	if err != nil {
		b.Fatal(err)
	}
	first := object.owningHandle()
	compact := slotFromThreadObject(object)

	var published Value
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		published = compact.owningValue()
	}
	runtime.KeepAlive(published)
	runtime.KeepAlive(first)
}

func BenchmarkNewThread(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = state.Close()
	})
	entry, err := state.NewNativeFunction(func(Frame) Outcome {
		return Outcome{}
	})
	if err != nil {
		b.Fatal(err)
	}

	var thread *Thread
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		thread, err = state.NewThread(entry.Value())
		if err != nil {
			b.Fatal(err)
		}
	}
	runtime.KeepAlive(thread)
	runtime.KeepAlive(entry)
}

func rootedThreadWithoutHandle(
	t *testing.T,
	state *State,
) (*threadObject, weak.Pointer[hostToken]) {
	t.Helper()
	entry := compileTestFunction(
		t,
		state,
		"@rooted-thread.lua",
		`return 42`,
	)
	object, err := state.newThreadObject(slotFromFunctionObject(entry))
	if err != nil {
		t.Fatal(err)
	}
	if err := state.registry.rawSetStringSlot(
		"rooted thread",
		slotFromThreadObject(object),
	); err != nil {
		t.Fatal(err)
	}
	handle := object.owningHandle()
	token := weak.Make(handle.token())
	runtime.KeepAlive(handle)
	return object, token
}

func weakCyclicThreadPublication(
	t *testing.T,
	state *State,
) (
	weak.Pointer[threadObject],
	weak.Pointer[hostToken],
) {
	t.Helper()
	entry := compileTestFunction(
		t,
		state,
		"@cyclic-thread.lua",
		`return`,
	)
	object, err := state.newThreadObject(slotFromFunctionObject(entry))
	if err != nil {
		t.Fatal(err)
	}
	object.values = append(object.values, slotFromThreadObject(object))
	object.top = len(object.values)
	object.captureUpvalue(1)
	handle := object.owningHandle()
	objectReference := weak.Make(object)
	tokenReference := weak.Make(handle.token())
	runtime.KeepAlive(handle)
	return objectReference, tokenReference
}

func waitForWeakThreadToken(
	t *testing.T,
	object *threadObject,
	token weak.Pointer[hostToken],
) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		runtime.GC()
		if token.Value() == nil {
			if object == nil || object.owner == nil {
				t.Fatal("Lua-rooted compact thread disappeared with its token")
			}
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("discarded thread owning token remained reachable")
		case <-ticker.C:
		}
	}
}

func waitForWeakThread(
	t *testing.T,
	state *State,
	object weak.Pointer[threadObject],
	token weak.Pointer[hostToken],
) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		runtime.GC()
		if token.Value() == nil {
			state.collectUnreachable()
		}
		if object.Value() == nil && token.Value() == nil {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("weak host directory pinned a discarded cyclic thread")
		case <-ticker.C:
		}
	}
}

func assertThreadHandleCount(
	t *testing.T,
	state *State,
	operation string,
	want int,
) {
	t.Helper()
	entries, keys, stale := hostDirectoryKindCounts(
		&state.runtime.hosts,
		ThreadKind,
	)
	if entries != want || keys != want || stale != 0 {
		t.Fatalf(
			"%s thread handles: entries=%d keys=%d stale=%d; want %d/%d/0",
			operation,
			entries,
			keys,
			stale,
			want,
			want,
		)
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
	if err := state.RawSetGlobal("native_yield", yield.Value()); err != nil {
		t.Fatal(err)
	}
	return yield
}

func assertDeadCoroutineClean(t *testing.T, thread *Thread) {
	t.Helper()
	object := thread.runtimeObject()
	if object == nil {
		t.Fatal("dead coroutine lost its compact object")
	}
	if object.status != ThreadDead ||
		object.top != 0 ||
		object.frameExtent != 0 ||
		len(object.frames) != 0 ||
		len(object.continuations) != 0 ||
		object.openUpvalues != nil ||
		object.activeNativeToken != 0 ||
		object.nativeCallDepth != 0 ||
		object.errorHandlerDepth != 0 ||
		cap(object.values) != 0 ||
		cap(object.frames) != 0 ||
		cap(object.continuations) != 0 {
		t.Fatalf(
			"dead coroutine retained execution: status=%v top=%d extent=%d "+
				"values=%d/%d frames=%d/%d continuations=%d/%d "+
				"upvalues=%p token=%d native=%d handler=%d",
			object.status,
			object.top,
			object.frameExtent,
			len(object.values),
			cap(object.values),
			len(object.frames),
			cap(object.frames),
			len(object.continuations),
			cap(object.continuations),
			object.openUpvalues,
			object.activeNativeToken,
			object.nativeCallDepth,
			object.errorHandlerDepth,
		)
	}
	for index, value := range object.values {
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
	assertRootThreadReady(t, state.main)
}

func assertCoroutineErrorContains(t *testing.T, err error, text string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), text) {
		t.Fatalf("error = %v; want text %q", err, text)
	}
}
