package lua

import (
	"errors"
	"runtime"
	"testing"
	"time"
	"unsafe"
	"weak"
)

func TestCompactUpvalueLifecycle(t *testing.T) {
	size := unsafe.Sizeof(upvalue{})
	wantSize := 2*unsafe.Sizeof(uintptr(0)) + unsafe.Sizeof(slot{})
	if size != wantSize {
		t.Fatalf("upvalue size = %d bytes; want %d", size, wantSize)
	}
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	thread := state.main
	thread.values = []slot{
		nilSlot,
		slotFromValue(Number(10)),
		slotFromValue(state.String("captured")),
	}

	high := thread.captureUpvalue(2)
	middle := thread.captureUpvalue(1)
	if thread.captureUpvalue(2) != high {
		t.Fatal("capturing the same register created a second upvalue")
	}
	if thread.openUpvalues != high || high.next != middle {
		t.Fatal("open upvalues are not ordered by descending stack index")
	}

	middle.write(slotFromValue(Number(11)))
	if got, ok := middle.read().owningValue().AsNumber(); !ok || got != 11 {
		t.Fatalf("open upvalue read = (%v, %v)", got, ok)
	}

	thread.closeUpvalues(2)
	if testUpvalueIsOpen(high) {
		t.Fatal("high upvalue did not close")
	}
	if thread.openUpvalues != middle {
		t.Fatal("closing one upvalue disturbed a lower open upvalue")
	}
	if text, ok := high.read().owningValue().AsString(); !ok || text != "captured" {
		t.Fatalf("closed upvalue = (%q, %v)", text, ok)
	}

	thread.closeUpvalues(0)
	if thread.openUpvalues != nil || testUpvalueIsOpen(middle) {
		t.Fatal("remaining upvalues did not close")
	}
	middle.write(slotFromValue(Bool(true)))
	if got, ok := middle.read().owningValue().AsBool(); !ok || !got {
		t.Fatalf("closed upvalue write = (%v, %v)", got, ok)
	}

	closed := newClosedUpvalue(nilSlot)
	if !closed.read().owningValue().IsNil() {
		t.Fatal("new closed upvalue did not retain nil")
	}
}

func testUpvalueIsOpen(upvalue *upvalue) bool {
	return upvalue != nil &&
		upvalue.cell != nil &&
		upvalue.cell != &upvalue.storage
}

func testFunctionUpvalue(function *functionObject, index int) *upvalue {
	return function.luaUpvalueUnchecked(index)
}

func TestLuaFunctionRejectsInvalidUpvalueCell(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	builder := testPrototypeBuilder(makeABC(opReturn, 0, 1, 0))
	builder.upvalues = 1
	prototype, syntaxError := builder.seal()
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("function accepted an upvalue without a value cell")
		}
	}()
	newLuaFunctionOwned(
		state,
		prototype,
		state.main.globals,
		[]*upvalue{{}},
	)
}

func TestFunctionRepresentations(t *testing.T) {
	pointerSize := unsafe.Sizeof(uintptr(0))
	handleSize := unsafe.Sizeof(Function{})
	wantHandleSize := 3 * pointerSize
	if handleSize != wantHandleSize {
		t.Fatalf(
			"Function handle size = %d bytes; want %d",
			handleSize,
			wantHandleSize,
		)
	}
	functionSize := unsafe.Sizeof(functionObject{})
	wantFunctionSize := 5 * pointerSize
	if functionSize != wantFunctionSize {
		t.Fatalf(
			"function object size = %d bytes; want %d",
			functionSize,
			wantFunctionSize,
		)
	}
	if offset := unsafe.Offsetof(
		nativeFunctionAllocation{}.functionObject,
	); offset != 0 {
		t.Fatalf("native function-object prefix offset = %d; want 0", offset)
	}
	if offset := unsafe.Offsetof(nativeFunctionAllocation{}.data); offset != functionSize {
		t.Fatalf(
			"native data offset = %d; want Function size %d",
			offset,
			functionSize,
		)
	}
	nativeSize := unsafe.Sizeof(nativeFunctionAllocation{})
	wantNativeSize := functionSize +
		unsafe.Sizeof(nativeFunctionData{})
	if nativeSize != wantNativeSize {
		t.Fatalf(
			"native function size = %d bytes; want %d",
			nativeSize,
			wantNativeSize,
		)
	}

	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	prototype, syntaxError := testPrototypeBuilder(
		makeABC(opReturn, 0, 1, 0),
	).seal()
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	luaFunction := newLuaFunction(
		state,
		prototype,
		state.main.globals,
		nil,
	)
	if luaFunction.nativeBody() != nil {
		t.Fatal("Lua function was classified as native")
	}
	if slotFromFunctionObject(luaFunction).bits != uint64(FunctionKind) {
		t.Fatal("Lua function did not retain the direct-call slot tag")
	}
	if luaFunction.prototype != prototype {
		t.Fatal("Lua function lost its prototype")
	}
	luaHandle := luaFunction.owningHandle()
	if luaHandle.runtimeObject() != luaFunction ||
		unsafe.Pointer(luaHandle) == unsafe.Pointer(luaFunction) {
		t.Fatal("Lua function handle did not preserve the ownership boundary")
	}

	entry := NativeFunc(func(Frame) Outcome { return Outcome{} })
	captures := []slot{
		slotFromValue(Number(1)),
		slotFromValue(state.String("capture")),
	}
	native := newNativeFunctionOwned(
		state,
		state.main.globals,
		entry,
		captures,
	)
	body := native.nativeBody()
	if body == nil {
		t.Fatal("native function was not classified as native")
	}
	if native.nativeBodyUnchecked() != body {
		t.Fatal("trusted native body access did not preserve identity")
	}
	if slotFromFunctionObject(native).bits !=
		uint64(FunctionKind)|nativeFunctionSlotFlag {
		t.Fatal("native function did not retain its compact callable tag")
	}
	if unsafe.Pointer(body) != native.body {
		t.Fatal("native Function does not point at its explicit body")
	}
	nativeHandle := native.owningHandle()
	nativeValue := nativeHandle.Value()
	if nativeValue.bits != uint64(FunctionKind) {
		t.Fatalf(
			"public native Function bits = %#x; want FunctionKind",
			nativeValue.bits,
		)
	}
	fromValue, ok := nativeValue.AsFunction()
	if !ok || fromValue != nativeHandle {
		t.Fatal("native function did not round-trip through its canonical Value")
	}
	if slotFromValue(nativeValue) != slotFromFunctionObject(native) {
		t.Fatal("native function ingress did not restore its compact slot tag")
	}
	if nativeHandle.Prototype() != nil || native.body == nil {
		t.Fatal("native function has Lua executable metadata")
	}
	if upvalueCount(nativeHandle) != len(captures) {
		t.Fatalf(
			"native UpvalueCount = %d; want %d",
			upvalueCount(nativeHandle),
			len(captures),
		)
	}
	if body.entry == nil {
		t.Fatal("native function lost its entry point")
	}
	environment, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetFunctionEnvironment(nativeHandle, environment); err != nil {
		t.Fatal(err)
	}
	if got, err := state.FunctionEnvironment(
		nativeHandle,
	); err != nil || got != environment {
		t.Fatalf("native FunctionEnvironment = (%p, %v)", got, err)
	}
	if got, ok := body.captures[0].owningValue().AsNumber(); !ok || got != 1 {
		t.Fatalf("native capture = (%v, %v); want (1, true)", got, ok)
	}
	writeSlot(&body.captures[0], slotFromValue(Bool(true)))
	if got, ok := body.captures[0].owningValue().AsBool(); !ok || !got {
		t.Fatalf("updated native capture = (%v, %v); want (true, true)", got, ok)
	}
}

func TestNativeFunctionPrefixRetainsBodyAcrossGC(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	retained, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := retained.RawSetString("key", state.String("value")); err != nil {
		t.Fatal(err)
	}
	function := newNativeFunctionOwned(
		state,
		state.main.globals,
		func(Frame) Outcome { return Outcome{} },
		[]slot{slotFromValue(retained.Value())},
	)
	retained = nil

	for range 3 {
		runtime.GC()
	}
	capture, ok := function.nativeBody().captures[0].owningValue().AsTable()
	if !ok {
		t.Fatal("native capture did not retain its table")
	}
	if got, ok := rawStr(capture, "key").AsString(); !ok || got != "value" {
		t.Fatalf("retained capture value = (%q, %v)", got, ok)
	}
	runtime.KeepAlive(function)
}

func TestWarmFunctionPublicationDoesNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	function := newNativeFunctionOwned(
		state,
		state.main.globals,
		func(Frame) Outcome { return Outcome{} },
		nil,
	)
	first := function.owningHandle()
	compact := slotFromFunctionObject(function)

	var published *Function
	allocations := testing.AllocsPerRun(1_000, func() {
		value := compact.owningValue()
		published, _ = value.AsFunction()
	})
	if allocations != 0 {
		t.Fatalf(
			"warm function publication allocated %.2f times",
			allocations,
		)
	}
	if published != first {
		t.Fatalf(
			"warm function publication = %p; want %p",
			published,
			first,
		)
	}
	runtime.KeepAlive(first)
}

func TestFunctionRepublishAfterOwningTokenDies(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	function, token := rootedFunctionWithoutHandle(t, state)
	index := newTable(state, 0, 1)
	index.rawSetSlot(slotFromFunctionObject(function), numberSlot(37))
	waitForWeakFunctionToken(t, function, token)

	firstValue := state.registry.rawGetStringValue("rooted function")
	first, ok := firstValue.AsFunction()
	if !ok || first.runtimeObject() != function {
		t.Fatal("re-publication changed compact function identity")
	}
	second, ok := state.registry.rawGetStringValue(
		"rooted function",
	).AsFunction()
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
	if number, ok := stored.AsNumber(); !ok || number != 37 {
		t.Fatalf(
			"function-key lookup after token replacement = (%v, %v); want 37",
			number,
			ok,
		)
	}
	entries, keys, stale := hostDirectoryKindCounts(
		&state.runtime.hosts,
		FunctionKind,
	)
	if entries != 1 || keys != 1 || stale != 0 {
		t.Fatalf(
			"function directory = entries:%d keys:%d stale:%d; want 1/1/0",
			entries,
			keys,
			stale,
		)
	}
	runtime.KeepAlive(first)
}

func TestHostDirectoryDoesNotPinCyclicFunction(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	function, token := weakFunctionPublication(t, state)
	waitForWeakFunction(t, state, function, token)
	state.runtime.hosts.prune()
	entries, keys, stale := hostDirectoryKindCounts(
		&state.runtime.hosts,
		FunctionKind,
	)
	if entries != 0 || keys != 0 || stale != 0 {
		t.Fatalf(
			"dead function remains in host directory: entries=%d keys=%d stale=%d",
			entries,
			keys,
			stale,
		)
	}
	runtime.KeepAlive(state)
}

func TestFunctionHandleSupportsNestedPublicationAfterClose(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	outer, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	function, token := nestedFunctionWithoutHandle(t, state, outer)
	waitForWeakFunctionToken(t, function, token)

	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	first, ok := rawStr(outer, "function").AsFunction()
	if !ok || first.runtimeObject() != function {
		t.Fatal("post-close nested function was not published")
	}
	second, ok := rawStr(outer, "function").AsFunction()
	if !ok || second != first {
		t.Fatal("post-close nested function publication was not canonical")
	}
	value := first.Value()
	roundTrip, ok := value.AsFunction()
	if !ok || roundTrip != first {
		t.Fatal("post-close function did not round-trip through its Value")
	}
	if same, applicable := value.SameObject(second.Value()); !applicable ||
		!same {
		t.Fatalf(
			"post-close function identity = (%v, %v); want (true, true)",
			same,
			applicable,
		)
	}
	if first.Prototype() != function.prototype ||
		upvalueCount(first) != int(function.prototype.upvalues) {
		t.Fatal("post-close function metadata changed")
	}
	if err := state.SetFunctionEnvironment(
		first,
		outer,
	); !errors.Is(err, ErrClosed) {
		t.Fatalf("post-close function mutation = %v; want ErrClosed", err)
	}
	runtime.KeepAlive(outer)
	runtime.KeepAlive(first)
}

func TestNativeFunctionDiscardsUnusedCaptureCapacity(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	function, hidden := nativeFunctionWithHiddenCaptureTail(t, state)
	body := function.nativeBodyUnchecked()
	if len(body.captures) != 1 || cap(body.captures) != 1 {
		t.Fatalf(
			"native capture shape = len %d cap %d; want 1/1",
			len(body.captures),
			cap(body.captures),
		)
	}
	waitForWeakCaptureTail(t, state, hidden, function)
}

func TestLuaOnlyLibrariesDoNotPublishFunctionHandles(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	for _, open := range []func() error{
		state.OpenBase,
		state.OpenPackage,
		state.OpenTable,
		state.OpenString,
		state.OpenMath,
		state.OpenIO,
		state.OpenOS,
	} {
		if err := open(); err != nil {
			t.Fatal(err)
		}
	}
	assertNoFunctionHandles(t, state, "opening libraries")

	function := compileTestFunction(t, state, "@compact-functions.lua", `
package.preload.compact=function()
	return 40
end
local loaded=require("compact")
local fields={"c","a","b"}
table.sort(fields)
local captures=0
for value in string.gmatch("a,b,c","[^,]+") do
	captures=captures+#value
end
local wrapped=coroutine.wrap(function(value)
	return value+1
end)
local raised=function() end
local protected,caught=pcall(function()
	error(raised,0)
end)
local file=assert(io.tmpfile())
assert(file:write("a\nbc\n"))
assert(file:seek("set")==0)
local lineBytes=0
for line in file:lines() do
	lineBytes=lineBytes+#line
end
assert(file:close())
return loaded+math.floor(2.9),
	fields[1]..fields[2]..fields[3],
	captures,
	wrapped(4),
	not protected and caught==raised,
	lineBytes
`)
	root := function.owningValue()
	assertFunctionHandleCount(t, state, "publishing the root", 1)
	results, err := state.Call(root)
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		Number(42),
		state.String("abc"),
		Number(3),
		Number(5),
		Bool(true),
		Number(3),
	)
	assertFunctionHandleCount(t, state, "Lua-only execution", 1)
	runtime.KeepAlive(root)
}

func TestNativeFunctionRejectsInvalidConstruction(t *testing.T) {
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
	foreign, err := other.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	entry := NativeFunc(func(Frame) Outcome { return Outcome{} })

	tests := []struct {
		name        string
		state       *State
		environment *tableObject
		entry       NativeFunc
		captures    []slot
	}{
		{
			name:        "nil owner",
			environment: state.main.globals,
			entry:       entry,
		},
		{
			name:  "nil environment",
			state: state,
			entry: entry,
		},
		{
			name:        "foreign environment",
			state:       state,
			environment: other.main.globals,
			entry:       entry,
		},
		{
			name:        "nil entry",
			state:       state,
			environment: state.main.globals,
		},
		{
			name:        "foreign capture",
			state:       state,
			environment: state.main.globals,
			entry:       entry,
			captures:    []slot{slotFromValue(foreign.Value())},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("invalid native function construction did not panic")
				}
			}()
			newNativeFunctionOwned(
				test.state,
				test.environment,
				test.entry,
				test.captures,
			)
		})
	}
}

func rootedFunctionWithoutHandle(
	t *testing.T,
	state *State,
) (*functionObject, weak.Pointer[hostToken]) {
	t.Helper()
	function := newTestLuaFunction(t, state, 0, 1, 0, 0)
	if err := state.registry.rawSetStringSlot(
		"rooted function",
		slotFromFunctionObject(function),
	); err != nil {
		t.Fatal(err)
	}
	handle := function.owningHandle()
	token := weak.Make(handle.token())
	runtime.KeepAlive(handle)
	return function, token
}

func weakFunctionPublication(
	t *testing.T,
	state *State,
) (
	weak.Pointer[functionObject],
	weak.Pointer[hostToken],
) {
	t.Helper()
	function := newTestLuaFunction(t, state, 0, 1, 0, 1)
	testFunctionUpvalue(function, 0).write(
		slotFromFunctionObject(function),
	)
	handle := function.owningHandle()
	functionReference := weak.Make(function)
	tokenReference := weak.Make(handle.token())
	runtime.KeepAlive(handle)
	return functionReference, tokenReference
}

func nestedFunctionWithoutHandle(
	t *testing.T,
	state *State,
	outer *Table,
) (*functionObject, weak.Pointer[hostToken]) {
	t.Helper()
	function := newTestLuaFunction(t, state, 0, 1, 0, 0)
	if err := outer.runtimeObject().rawSetStringSlot(
		"function",
		slotFromFunctionObject(function),
	); err != nil {
		t.Fatal(err)
	}
	handle := function.owningHandle()
	token := weak.Make(handle.token())
	runtime.KeepAlive(handle)
	return function, token
}

func nativeFunctionWithHiddenCaptureTail(
	t *testing.T,
	state *State,
) (*functionObject, weak.Pointer[tableObject]) {
	t.Helper()
	captures := make([]slot, 2)
	captures[0] = numberSlot(1)
	hidden := newTable(state, 0, 0)
	captures[1] = slotFromTableObject(hidden)
	reference := weak.Make(hidden)
	function := newNativeFunctionOwned(
		state,
		state.main.globals,
		func(Frame) Outcome { return Outcome{} },
		captures[:1],
	)
	runtime.KeepAlive(hidden)
	return function, reference
}

func waitForWeakFunctionToken(
	t *testing.T,
	function *functionObject,
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
			if function == nil || function.owner == nil {
				t.Fatal("Lua-rooted compact function disappeared with its token")
			}
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("discarded function owning token remained reachable")
		case <-ticker.C:
		}
	}
}

func waitForWeakFunction(
	t *testing.T,
	state *State,
	function weak.Pointer[functionObject],
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
		if function.Value() == nil && token.Value() == nil {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("weak host directory pinned a discarded cyclic function")
		case <-ticker.C:
		}
	}
}

func waitForWeakCaptureTail(
	t *testing.T,
	state *State,
	hidden weak.Pointer[tableObject],
	function *functionObject,
) {
	t.Helper()
	handle := function.owningHandle()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		state.collectUnreachable()
		runtime.GC()
		if hidden.Value() == nil {
			runtime.KeepAlive(handle)
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("unused native capture capacity retained a Lua object")
		case <-ticker.C:
		}
	}
}

func assertNoFunctionHandles(
	t *testing.T,
	state *State,
	operation string,
) {
	t.Helper()
	assertFunctionHandleCount(t, state, operation, 0)
}

func assertFunctionHandleCount(
	t *testing.T,
	state *State,
	operation string,
	want int,
) {
	t.Helper()
	entries, keys, stale := hostDirectoryKindCounts(
		&state.runtime.hosts,
		FunctionKind,
	)
	if entries != want || keys != want || stale != 0 {
		t.Fatalf(
			"%s function handles: entries=%d keys=%d stale=%d; want %d/%d/0",
			operation,
			entries,
			keys,
			stale,
			want,
			want,
		)
	}
}

func TestStateClosePreservesRetainedOpenUpvalue(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	thread := state.main
	retained, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	thread.values = []slot{slotFromValue(retained.Value())}
	upvalue := thread.captureUpvalue(0)

	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if thread.openUpvalues != nil || testUpvalueIsOpen(upvalue) {
		t.Fatal("state close left a retained upvalue open")
	}
	if thread.values != nil {
		t.Fatal("state close retained the thread value stack")
	}

	runtime.GC()
	value, ok := upvalue.read().owningValue().AsTable()
	if !ok || value != retained {
		t.Fatalf("closed upvalue retained (%p, %v); want %p", value, ok, retained)
	}
}

func TestControlledObjectMetadata(t *testing.T) {
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

	environment, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	mainEnvironment, err := threadEnvironment(state.MainThread())
	if err != nil {
		t.Fatal(err)
	}
	prototype, syntaxError := testPrototypeBuilder(
		makeABC(opReturn, 0, 1, 0),
	).seal()
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	function := newLuaFunction(
		state,
		prototype,
		state.main.globals,
		nil,
	)
	handle := function.owningHandle()
	if err := state.SetFunctionEnvironment(handle, environment); err != nil {
		t.Fatal(err)
	}
	if got, err := state.FunctionEnvironment(
		handle,
	); err != nil || got != environment {
		t.Fatalf("FunctionEnvironment = (%p, %v)", got, err)
	}

	foreignEnvironment, err := other.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetFunctionEnvironment(
		handle,
		foreignEnvironment,
	); !errors.Is(err, ErrForeignValue) {
		t.Fatalf("foreign function environment error = %v", err)
	}

	data, err := state.NewUserData("payload")
	if err != nil {
		t.Fatal(err)
	}
	if got, environmentErr := userDataEnvironment(
		data,
	); environmentErr != nil || got != mainEnvironment {
		t.Fatalf(
			"initial UserDataEnvironment = (%p, %v); want %p",
			got,
			environmentErr,
			mainEnvironment,
		)
	}
	if err := setUserDataEnvironment(data, environment); err != nil {
		t.Fatal(err)
	}
	if got, err := userDataEnvironment(data); err != nil || got != environment {
		t.Fatalf("UserDataEnvironment = (%p, %v)", got, err)
	}

	entry, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	thread, err := state.NewThread(entry.Value())
	if err != nil {
		t.Fatal(err)
	}
	if got, environmentErr := threadEnvironment(
		thread,
	); environmentErr != nil || got != mainEnvironment {
		t.Fatalf(
			"initial ThreadEnvironment = (%p, %v); want %p",
			got,
			environmentErr,
			mainEnvironment,
		)
	}
	if err := setThreadEnvironment(thread, environment); err != nil {
		t.Fatal(err)
	}
	if got, environmentErr := threadEnvironment(
		thread,
	); environmentErr != nil || got != environment {
		t.Fatalf("ThreadEnvironment = (%p, %v)", got, environmentErr)
	}
	if _, environmentErr := threadEnvironment(
		nil,
	); !errors.Is(environmentErr, ErrInvalidValue) {
		t.Fatalf("nil ThreadEnvironment error = %v", environmentErr)
	}
	if environmentErr := setThreadEnvironment(
		thread,
		nil,
	); !errors.Is(environmentErr, ErrInvalidValue) {
		t.Fatalf("nil thread environment error = %v", environmentErr)
	}
	if environmentErr := setThreadEnvironment(
		thread,
		foreignEnvironment,
	); !errors.Is(environmentErr, ErrForeignValue) {
		t.Fatalf("foreign thread environment error = %v", environmentErr)
	}
	if environmentErr := setThreadEnvironment(
		other.MainThread(),
		environment,
	); !errors.Is(environmentErr, ErrForeignValue) {
		t.Fatalf("foreign thread error = %v", environmentErr)
	}
	if got, environmentErr := threadEnvironment(
		thread,
	); environmentErr != nil || got != environment {
		t.Fatalf(
			"rejected setters changed ThreadEnvironment = (%p, %v)",
			got,
			environmentErr,
		)
	}

	table, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	metatable, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(table.Value(), metatable); err != nil {
		t.Fatal(err)
	}
	if got, err := state.Metatable(table.Value()); err != nil || got != metatable {
		t.Fatalf("table metatable = (%p, %v)", got, err)
	}
	if err := state.SetMetatable(Number(1), metatable); err != nil {
		t.Fatal(err)
	}
	if got, err := state.Metatable(Number(2)); err != nil || got != metatable {
		t.Fatalf("number type metatable = (%p, %v)", got, err)
	}
	if err := state.SetMetatable(table.Value(), foreignEnvironment); !errors.Is(err, ErrForeignValue) {
		t.Fatalf("foreign metatable error = %v", err)
	}
}

func TestFunctionOwningHandleEnforcesStateOwnership(t *testing.T) {
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

	environment, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := other.NewNativeFunction(
		func(frame Frame) Outcome { return frame.Return() },
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Call(foreign.Value()); !errors.Is(
		err,
		ErrForeignValue,
	) {
		t.Fatalf("foreign function call = %v; want ErrForeignValue", err)
	}
	if _, err := state.FunctionEnvironment(foreign); !errors.Is(
		err,
		ErrForeignValue,
	) {
		t.Fatalf("foreign function environment = %v; want ErrForeignValue", err)
	}
	if err := state.SetFunctionEnvironment(
		foreign,
		environment,
	); !errors.Is(err, ErrForeignValue) {
		t.Fatalf("foreign function environment setter = %v; want ErrForeignValue", err)
	}

	var zero Function
	for name, function := range map[string]*Function{
		"nil":  nil,
		"zero": &zero,
	} {
		if function.Value().Valid() {
			t.Fatalf("%s Function manufactured a valid Value", name)
		}
		if _, err := state.FunctionEnvironment(function); !errors.Is(
			err,
			ErrInvalidValue,
		) {
			t.Fatalf(
				"%s Function environment = %v; want ErrInvalidValue",
				name,
				err,
			)
		}
		if err := state.SetFunctionEnvironment(
			function,
			environment,
		); !errors.Is(err, ErrInvalidValue) {
			t.Fatalf(
				"%s Function environment setter = %v; want ErrInvalidValue",
				name,
				err,
			)
		}
	}
}

func BenchmarkWarmFunctionPublication(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer state.Close()
	function := newNativeFunctionOwned(
		state,
		state.main.globals,
		func(Frame) Outcome { return Outcome{} },
		nil,
	)
	first := function.owningHandle()
	compact := slotFromFunctionObject(function)

	var published Value
	b.ReportAllocs()
	for range b.N {
		published = compact.owningValue()
	}
	runtime.KeepAlive(published)
	runtime.KeepAlive(first)
}

func BenchmarkNewNativeFunction(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer state.Close()

	entry := func(Frame) Outcome { return Outcome{} }
	var function *Function
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		function, err = state.NewNativeFunction(entry)
		if err != nil {
			b.Fatal(err)
		}
	}
	runtime.KeepAlive(function)
}
