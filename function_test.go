package lua

import (
	"errors"
	"runtime"
	"testing"
	"unsafe"
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
	thread := state.MainThread()
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

func testFunctionUpvalue(function *Function, index int) *upvalue {
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
		state.runtime,
		prototype,
		state.globals,
		[]*upvalue{{}},
	)
}

func TestFunctionRepresentations(t *testing.T) {
	pointerSize := unsafe.Sizeof(uintptr(0))
	functionSize := unsafe.Sizeof(Function{})
	wantFunctionSize := 4 * pointerSize
	if functionSize != wantFunctionSize {
		t.Fatalf(
			"Function size = %d bytes; want %d",
			functionSize,
			wantFunctionSize,
		)
	}
	if offset := unsafe.Offsetof(nativeFunctionAllocation{}.Function); offset != 0 {
		t.Fatalf("native Function prefix offset = %d; want 0", offset)
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
		state.runtime,
		prototype,
		state.globals,
		nil,
	)
	if luaFunction.nativeBody() != nil {
		t.Fatal("Lua function was classified as native")
	}
	if slotFromFunction(luaFunction).bits != uint64(FunctionKind) {
		t.Fatal("Lua function did not retain the direct-call slot tag")
	}
	if luaFunction.Prototype() != prototype {
		t.Fatal("Lua function lost its prototype")
	}

	entry := NativeFunc(func(Frame) Outcome { return Outcome{} })
	captures := []slot{
		slotFromValue(Number(1)),
		slotFromValue(state.String("capture")),
	}
	native := newNativeFunctionOwned(
		state.runtime,
		state.globals,
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
	if slotFromFunction(native).bits !=
		uint64(FunctionKind)|nativeFunctionSlotFlag {
		t.Fatal("native function did not retain its compact callable tag")
	}
	if unsafe.Pointer(body) != native.body {
		t.Fatal("native Function does not point at its explicit body")
	}
	fromValue, ok := native.Value().Function()
	if !ok || fromValue != native {
		t.Fatal("native function did not round-trip through its canonical Value")
	}
	if native.Prototype() != nil || native.body == nil {
		t.Fatal("native function has Lua executable metadata")
	}
	if native.UpvalueCount() != len(captures) {
		t.Fatalf(
			"native UpvalueCount = %d; want %d",
			native.UpvalueCount(),
			len(captures),
		)
	}
	if body.entry == nil {
		t.Fatal("native function lost its entry point")
	}
	environment, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetFunctionEnvironment(native, environment); err != nil {
		t.Fatal(err)
	}
	if got, err := state.FunctionEnvironment(native); err != nil || got != environment {
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
	retained, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := retained.RawSetString("key", state.String("value")); err != nil {
		t.Fatal(err)
	}
	function := newNativeFunctionOwned(
		state.runtime,
		state.globals,
		func(Frame) Outcome { return Outcome{} },
		[]slot{slotFromTable(retained)},
	)
	retained = nil

	for range 3 {
		runtime.GC()
	}
	capture, ok := function.nativeBody().captures[0].owningValue().Table()
	if !ok {
		t.Fatal("native capture did not retain its table")
	}
	if got, ok := capture.RawGetString("key").AsString(); !ok || got != "value" {
		t.Fatalf("retained capture value = (%q, %v)", got, ok)
	}
	runtime.KeepAlive(function)
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
	foreign, err := other.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	entry := NativeFunc(func(Frame) Outcome { return Outcome{} })

	tests := []struct {
		name        string
		owner       *runtimeState
		environment *Table
		entry       NativeFunc
		captures    []slot
	}{
		{
			name:        "nil owner",
			environment: state.globals,
			entry:       entry,
		},
		{
			name:  "nil environment",
			owner: state.runtime,
			entry: entry,
		},
		{
			name:        "foreign environment",
			owner:       state.runtime,
			environment: other.globals,
			entry:       entry,
		},
		{
			name:        "nil entry",
			owner:       state.runtime,
			environment: state.globals,
		},
		{
			name:        "foreign capture",
			owner:       state.runtime,
			environment: state.globals,
			entry:       entry,
			captures:    []slot{slotFromTable(foreign)},
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
				test.owner,
				test.environment,
				test.entry,
				test.captures,
			)
		})
	}
}

func TestStateClosePreservesRetainedOpenUpvalue(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	thread := state.MainThread()
	retained, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	thread.values = []slot{slotFromTable(retained)}
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
	value, ok := upvalue.read().owningValue().Table()
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

	environment, err := state.NewTable(0, 0)
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
		state.runtime,
		prototype,
		state.globals,
		nil,
	)
	if err := state.SetFunctionEnvironment(function, environment); err != nil {
		t.Fatal(err)
	}
	if got, err := state.FunctionEnvironment(function); err != nil || got != environment {
		t.Fatalf("FunctionEnvironment = (%p, %v)", got, err)
	}

	foreignEnvironment, err := other.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetFunctionEnvironment(function, foreignEnvironment); !errors.Is(err, ErrForeignValue) {
		t.Fatalf("foreign function environment error = %v", err)
	}

	data, err := state.NewUserData("payload")
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetUserDataEnvironment(data, environment); err != nil {
		t.Fatal(err)
	}
	if got, err := state.UserDataEnvironment(data); err != nil || got != environment {
		t.Fatalf("UserDataEnvironment = (%p, %v)", got, err)
	}

	table, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	metatable, err := state.NewTable(0, 0)
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
