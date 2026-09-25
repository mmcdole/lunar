package lua

import (
	"context"
	"io"
	"runtime"
	"slices"
	"testing"
)

// Helpers used only by white-box tests live here so the shipped runtime
// contains only paths used by production code.

func newLuaFunction(
	state *State,
	prototype *Prototype,
	environment *tableObject,
	upvalues []*upvalue,
) *functionObject {
	return newLuaFunctionOwned(
		state,
		prototype,
		environment,
		exactSlice(upvalues),
	)
}

func newClosedUpvalue(value slot) *upvalue {
	created := &upvalue{storage: value}
	created.cell = &created.storage
	return created
}

func floatingByteToInt(value int) int {
	decoded := floatingByteToUint64(value)
	maxInt := uint64(^uint(0) >> 1)
	if decoded > maxInt {
		panic("lua: floating byte exceeds integer range")
	}
	return int(decoded)
}

func newInternedText(text string) *internedText {
	return newHashedInternedText(text, hashString(text))
}

func rawInt(table *Table, key int) Value { return table.RawGetInt(key) }

func rawStr(table *Table, key string) Value { return table.RawGetString(key) }

func rawLen(table *Table) int { return table.RawLen() }

func registerCount(prototype *Prototype) int {
	return int(prototype.registers)
}

func parameterCount(prototype *Prototype) int {
	return int(prototype.parameters)
}

func childCount(prototype *Prototype) int {
	return len(prototype.children)
}

func isVararg(prototype *Prototype) bool {
	return prototype != nil && prototype.varargFlags&varargIsVararg != 0
}

func upvalueCount(target any) int {
	switch typed := target.(type) {
	case *Prototype:
		if typed == nil {
			return 0
		}
		return int(typed.upvalues)
	case *Function:
		object := typed.runtimeObject()
		if object == nil {
			return 0
		}
		if object.prototype != nil {
			return int(object.prototype.upvalues)
		}
		if native := object.nativeBody(); native != nil {
			return len(native.captures)
		}
		return 0
	default:
		panic("lua: unsupported upvalueCount target")
	}
}

func threadEnvironment(thread *Thread) (*Table, error) {
	object := thread.runtimeObject()
	if object == nil || object.owner == nil {
		return nil, ErrInvalidValue
	}
	if object.owner.closed.Load() {
		return nil, ErrClosed
	}
	return object.globals.owningHandle(), nil
}

func setThreadEnvironment(thread *Thread, environment *Table) error {
	object := thread.runtimeObject()
	if object == nil || object.owner == nil {
		return ErrInvalidValue
	}
	if object.owner.closed.Load() {
		return ErrClosed
	}
	target := environment.runtimeObject()
	if target == nil {
		return ErrInvalidValue
	}
	if target.owner != object.owner {
		return ErrForeignValue
	}
	object.globals = target
	return nil
}

func userDataEnvironment(data *UserData) (*Table, error) {
	object := data.runtimeObject()
	if object == nil || object.owner == nil {
		return nil, ErrInvalidValue
	}
	if object.owner.closed.Load() {
		return nil, ErrClosed
	}
	return object.environment.owningHandle(), nil
}

func setUserDataEnvironment(data *UserData, environment *Table) error {
	object := data.runtimeObject()
	if object == nil || object.owner == nil {
		return ErrInvalidValue
	}
	if object.owner.closed.Load() {
		return ErrClosed
	}
	if environment == nil {
		object.environment = nil
		return nil
	}
	target := environment.runtimeObject()
	if target == nil || target.owner != object.owner {
		return ErrForeignValue
	}
	object.environment = target
	return nil
}

func callCtx(
	t testing.TB,
	state *State,
	ctx context.Context,
	callable Value,
	arguments ...Value,
) ([]Value, error) {
	t.Helper()
	if err := state.SetContext(ctx); err != nil {
		return nil, err
	}
	defer func() { _ = state.RemoveContext() }()
	return state.Call(callable, arguments...)
}

func loadCtx(
	t testing.TB,
	state *State,
	ctx context.Context,
	sourceName string,
	reader io.Reader,
) (*Function, error) {
	t.Helper()
	if err := state.SetContext(ctx); err != nil {
		return nil, err
	}
	defer func() { _ = state.RemoveContext() }()
	return state.Load(sourceName, reader)
}

func loadStringCtx(
	t testing.TB,
	state *State,
	ctx context.Context,
	sourceName string,
	source string,
) (*Function, error) {
	t.Helper()
	if err := state.SetContext(ctx); err != nil {
		return nil, err
	}
	defer func() { _ = state.RemoveContext() }()
	return state.LoadString(sourceName, source)
}

func callIntoCtx(
	t testing.TB,
	state *State,
	ctx context.Context,
	callable Value,
	arguments []Value,
	destination []Value,
) (int, error) {
	t.Helper()
	if err := state.SetContext(ctx); err != nil {
		return 0, err
	}
	defer func() { _ = state.RemoveContext() }()
	return state.CallInto(callable, arguments, destination)
}

func indexCtx(
	t testing.TB,
	state *State,
	ctx context.Context,
	target Value,
	key Value,
) (Value, error) {
	t.Helper()
	if err := state.SetContext(ctx); err != nil {
		return Value{}, err
	}
	defer func() { _ = state.RemoveContext() }()
	return state.Index(target, key)
}

func loadFileCtx(
	t testing.TB,
	state *State,
	ctx context.Context,
	path string,
) (*Function, error) {
	t.Helper()
	if err := state.SetContext(ctx); err != nil {
		return nil, err
	}
	defer func() { _ = state.RemoveContext() }()
	return state.LoadFile(path)
}

func (function *functionObject) nativeBody() *nativeFunctionData {
	if function == nil ||
		function.owner == nil ||
		function.prototype != nil ||
		function.body == nil {
		return nil
	}
	return function.nativeBodyUnchecked()
}

// importValue composes the same validation and import steps used by public
// entry points for tests that inspect the compact result directly.
func (rt *runtimeState) importValue(value Value) (slot, error) {
	if err := rt.accept(value); err != nil {
		return nilSlot, err
	}
	return rt.importAcceptedSlot(slotFromValue(value)), nil
}

func mustLoadString(
	t testing.TB,
	state *State,
	sourceName string,
	source string,
) *Function {
	t.Helper()
	function, err := state.LoadString(sourceName, source)
	if err != nil {
		t.Fatal(err)
	}
	return function
}

func assertRootThreadReady(t *testing.T, thread *threadObject) {
	t.Helper()
	if thread.status != ThreadReady ||
		thread.top != 0 ||
		thread.frameExtent != 0 ||
		len(thread.frames) != 0 ||
		len(thread.continuations) != 0 ||
		thread.openUpvalues != nil ||
		thread.activeNativeToken != 0 ||
		thread.nativeCallDepth != 0 ||
		thread.errorHandlerDepth != 0 {
		t.Fatalf(
			"root thread retained execution state: status=%v top=%d extent=%d "+
				"frames=%d continuations=%d upvalues=%p token=%d native=%d handler=%d",
			thread.status,
			thread.top,
			thread.frameExtent,
			len(thread.frames),
			len(thread.continuations),
			thread.openUpvalues,
			thread.activeNativeToken,
			thread.nativeCallDepth,
			thread.errorHandlerDepth,
		)
	}
	for index, value := range thread.values {
		if value != (slot{}) {
			t.Fatalf("root stack slot %d retained %v", index, value.owningValue())
		}
	}
}

func assertTestValues(t *testing.T, got []Value, want ...Value) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("value count = %d; want %d", len(got), len(want))
	}
	for index := range want {
		assertTestValue(t, got[index], want[index])
	}
}

func newTestLuaFunction(
	t *testing.T,
	state *State,
	parameters int,
	registers int,
	varargFlags int,
	upvalueCount int,
) *functionObject {
	t.Helper()
	builder := testPrototypeBuilder(makeABC(opReturn, 0, 1, 0))
	builder.parameters = parameters
	builder.registers = registers
	builder.varargFlags = varargFlags
	builder.upvalues = upvalueCount
	prototype, syntaxError := builder.seal()
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	upvalues := make([]*upvalue, upvalueCount)
	for index := range upvalues {
		upvalues[index] = newClosedUpvalue(nilSlot)
	}
	return newLuaFunction(
		state,
		prototype,
		state.main.globals,
		upvalues,
	)
}

func setTestCall(
	thread *threadObject,
	callBase int,
	function *functionObject,
	arguments ...Value,
) {
	end := callBase + 1 + len(arguments)
	thread.reserveValues(end)
	thread.values[callBase] = slotFromFunctionObject(function)
	for index, argument := range arguments {
		thread.values[callBase+1+index] = slotFromValue(argument)
	}
	if end > thread.top {
		thread.top = end
	}
}

func assertTestSlot(t *testing.T, got slot, want Value) {
	t.Helper()
	assertTestValue(t, got.owningValue(), want)
}

func assertTestValue(t *testing.T, got, want Value) {
	t.Helper()
	if !rawSlotEqual(slotFromValue(got), slotFromValue(want)) {
		t.Fatalf("value = %v (%s), want %v (%s)", got, got.Kind(), want, want.Kind())
	}
}

func assertTestThreadStateEqual(t *testing.T, got, want *threadObject) {
	t.Helper()
	if got.top != want.top ||
		got.frameExtent != want.frameExtent ||
		!slices.Equal(got.values, want.values) ||
		!slices.Equal(got.frames, want.frames) {
		t.Fatalf(
			"thread state differs:\nfast: top=%d extent=%d values=%v frames=%+v\nchecked: top=%d extent=%d values=%v frames=%+v",
			got.top,
			got.frameExtent,
			got.values,
			got.frames,
			want.top,
			want.frameExtent,
			want.values,
			want.frames,
		)
	}
}

func stageNativeTestCall(
	t *testing.T,
	state *State,
	function *Function,
	wantedResults int,
	arguments ...Value,
) *threadObject {
	t.Helper()
	thread := state.main
	if len(thread.frames) != 0 || thread.activeNativeToken != 0 {
		t.Fatal("test Thread has active native work")
	}
	oldExtent := thread.liveValueExtent()
	thread.top = 0
	thread.frameExtent = 0
	thread.clearInactive(0, oldExtent)

	required := 1 + len(arguments)
	thread.reserveValues(required)
	object := function.runtimeObject()
	thread.values[0] = slotFromFunctionObject(object)
	for index, argument := range arguments {
		thread.values[index+1] = slotFromValue(argument)
	}
	thread.top = required
	if failure := thread.pushFunctionCall(
		object,
		0,
		len(arguments),
		wantedResults,
	); failure != nil {
		t.Fatal(failure)
	}
	runtime.KeepAlive(function)
	return thread
}

func assertNativeTestResults(
	t *testing.T,
	thread *threadObject,
	expected ...Value,
) {
	t.Helper()
	if len(thread.frames) != 0 {
		t.Fatalf("native activation count = %d; want 0", len(thread.frames))
	}
	if thread.top != len(expected) {
		t.Fatalf("native result count = %d; want %d", thread.top, len(expected))
	}
	for index, value := range expected {
		assertTestSlot(t, thread.values[index], value)
	}
}

func assertNativePanic(t *testing.T, operation func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("operation did not panic")
		}
	}()
	operation()
}

func executeTestChunk(
	t *testing.T,
	source string,
	arguments ...Value,
) (*State, *threadObject, executionResult) {
	t.Helper()
	prototype, syntaxError := compileSource("@test.lua", source)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	function := newLuaFunction(
		state,
		prototype,
		state.main.globals,
		nil,
	)
	thread := state.main
	setTestCall(thread, 0, function, arguments...)
	if callErr := thread.pushFunctionCall(
		function,
		0,
		len(arguments),
		allResults,
	); callErr != nil {
		state.Close()
		t.Fatal(callErr)
	}
	return state, thread, runTestExecutor(t, thread, 0)
}

func compileTestFunction(
	t testing.TB,
	state *State,
	sourceName string,
	source string,
) *functionObject {
	t.Helper()
	prototype, syntaxError := compileSource(sourceName, source)
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	return newLuaFunction(state, prototype, state.main.globals, nil)
}

// enterTestExecution installs the same active-Thread invariant as public call
// and resume entry points. Executor tests bypass those boundaries so they can
// inspect compact stacks directly, but collection safe points must still see
// a real running Thread.
func enterTestExecution(t testing.TB, thread *threadObject) func() {
	t.Helper()
	if thread == nil ||
		thread.state == nil ||
		thread.owner != thread.state.runtime {
		t.Fatal("test executor received an invalid Thread")
	}
	state := thread.state
	if state.active != nil || thread.status == ThreadRunning {
		t.Fatal("test State already has an active Thread")
	}
	previousStatus := thread.status
	state.active = thread
	thread.status = ThreadRunning
	return func() {
		valid := state.active == thread
		thread.status = previousStatus
		state.active = nil
		if !valid {
			t.Fatal("test executor changed the active Thread")
		}
	}
}

func runTestExecutor(
	t testing.TB,
	thread *threadObject,
	stopDepth int,
) executionResult {
	t.Helper()
	leave := enterTestExecution(t, thread)
	defer leave()
	return execute(thread, stopDepth)
}

func assertExecutionReturned(t *testing.T, result executionResult) {
	t.Helper()
	if result.kind != executionReturned || result.err != nil {
		t.Fatalf("execution result = %+v; want return", result)
	}
}

func assertExecutionValues(t *testing.T, thread *threadObject, expected ...Value) {
	t.Helper()
	if thread.top != len(expected) {
		t.Fatalf("result count = %d; want %d", thread.top, len(expected))
	}
	for index, value := range expected {
		assertTestSlot(t, thread.values[index], value)
	}
}

func (result collectionResult) total() int {
	return result.tables +
		result.functions +
		result.threads +
		result.userData
}
