package lua

import "testing"

func TestDebugActivationWalksPhysicalNativeAndTailFrames(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	var (
		records                    []debugActivation
		nativeCategory, nativeName string
		nativeNamed, luaFrameNamed bool
	)
	inspect, err := state.NewNativeFunction(func(frame Frame) Outcome {
		for level := 0; ; level++ {
			record, found := frame.thread.debugActivation(level)
			if !found {
				break
			}
			records = append(records, record)
		}
		nativeCategory, nativeName, nativeNamed =
			records[0].functionName(frame.thread)
		_, _, luaFrameNamed = records[1].functionName(frame.thread)
		return frame.ReturnNumber(41)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetRawGlobal("inspect", inspect.Value()); err != nil {
		t.Fatal(err)
	}

	chunk := mustLoadString(t, state, "@debug-tail.lua", `local function target()
	return inspect()
end
local function middle()
	return target()
end
return middle()
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(41))

	if len(records) != 4 {
		t.Fatalf("logical frame count = %d; want 4", len(records))
	}
	native := records[0]
	if native.status != logicalFramePhysical ||
		native.physicalIndex != 1 ||
		native.isTail() ||
		native.prototype() != nil ||
		native.currentPC() != -1 ||
		native.currentLine() != -1 {
		t.Fatalf("native debug activation = %+v", native)
	}
	if !nativeNamed ||
		nativeCategory != "global" ||
		nativeName != "inspect" {
		t.Fatalf(
			"native function name = (%q, %q, %v); want global inspect",
			nativeCategory,
			nativeName,
			nativeNamed,
		)
	}

	luaFrame := records[1]
	if luaFrame.status != logicalFramePhysical ||
		luaFrame.physicalIndex != 0 ||
		luaFrame.isTail() ||
		luaFrame.prototype() == nil ||
		luaFrame.prototype().SourceName() != "@debug-tail.lua" ||
		luaFrame.frame.tailCalls != 2 ||
		luaFrame.currentPC() < 0 ||
		luaFrame.currentLine() != 2 {
		t.Fatalf("surviving Lua activation = %+v", luaFrame)
	}
	if luaFrameNamed {
		t.Fatal("tail-replaced function unexpectedly had a call-site name")
	}

	for level := 2; level < 4; level++ {
		tail := records[level]
		if tail.status != logicalFrameTail ||
			tail.physicalIndex != 0 ||
			!tail.isTail() ||
			tail.prototype() != nil ||
			tail.currentPC() != -1 ||
			tail.currentLine() != -1 {
			t.Fatalf("logical tail activation %d = %+v", level, tail)
		}
	}
	if _, found := state.main.debugActivation(-1); found {
		t.Fatal("negative logical level resolved")
	}
	if _, found := state.main.debugActivation(len(records)); found {
		t.Fatal("out-of-range logical level resolved")
	}
}

func TestDebugActivationRecoversCallerNameCategories(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	type observedName struct {
		category string
		name     string
		found    bool
	}
	var observed []observedName
	probe, err := state.NewNativeFunction(func(frame Frame) Outcome {
		record, found := frame.thread.debugActivation(0)
		if !found {
			t.Error("current native activation is missing")
			return frame.Return()
		}
		category, name, named := record.functionName(frame.thread)
		observed = append(observed, observedName{
			category: category,
			name:     name,
			found:    named,
		})
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetRawGlobal("probe", probe.Value()); err != nil {
		t.Fatal(err)
	}
	holder, err := state.NewTableWithCapacity(0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.RawSetString("field", probe.Value()); err != nil {
		t.Fatal(err)
	}
	if err := holder.RawSetString("method", probe.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetRawGlobal("holder", holder.Value()); err != nil {
		t.Fatal(err)
	}

	chunk := mustLoadString(t, state, "@debug-names.lua", `local localProbe = probe
local localHolder = holder
probe()
localProbe()
localHolder.field()
localHolder:method()
local captured = probe
local function throughUpvalue()
	captured()
end
throughUpvalue()
return true
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Bool(true))

	want := []observedName{
		{category: "global", name: "probe", found: true},
		{category: "local", name: "localProbe", found: true},
		{category: "field", name: "field", found: true},
		{category: "method", name: "method", found: true},
		{category: "upvalue", name: "captured", found: true},
	}
	if len(observed) != len(want) {
		t.Fatalf("observed %d call-site names; want %d", len(observed), len(want))
	}
	for index := range want {
		if observed[index] != want[index] {
			t.Fatalf(
				"call-site name %d = %+v; want %+v",
				index,
				observed[index],
				want[index],
			)
		}
	}

	observed = observed[:0]
	if _, err := state.Call(probe.Value()); err != nil {
		t.Fatal(err)
	}
	if len(observed) != 1 || observed[0] != (observedName{}) {
		t.Fatalf("host-entered function name = %+v; want unavailable", observed)
	}
}

func TestDebugActivationResolvesNamedLocalsAndTemporaryBounds(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	builder := testPrototypeBuilder(makeABC(opReturn, 0, 1, 0))
	builder.registers = 5
	builder.debug = &prototypeDebugBuilder{
		lines: []int{19},
		locals: []prototypeLocalBuilder{
			{
				name:    newInternedText("named"),
				startPC: 0,
				endPC:   1,
			},
		},
	}
	prototype, syntaxError := builder.seal()
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	caller := newLuaFunction(
		state,
		prototype,
		state.main.globals,
		nil,
	)
	native, err := state.newNativeFunctionObject(
		func(frame Frame) Outcome { return frame.Return() },
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	thread := state.main
	thread.reserveValues(9)
	thread.top = 9
	thread.frameExtent = 9
	thread.frames = []activation{
		{
			function: caller,
			base:     2,
			pc:       1,
		},
		{
			function:   native,
			base:       7,
			resultBase: 5,
		},
	}

	luaRecord, found := thread.debugActivation(1)
	if !found ||
		luaRecord.currentPC() != 0 ||
		luaRecord.currentLine() != 19 {
		t.Fatalf(
			"Lua local activation = (%+v, %v)",
			luaRecord,
			found,
		)
	}
	assertDebugLocal(t, luaRecord, thread, 1, "named", 2, true)
	assertDebugLocal(t, luaRecord, thread, 2, debugTemporaryName, 3, true)
	assertDebugLocal(t, luaRecord, thread, 3, debugTemporaryName, 4, true)
	assertDebugLocal(t, luaRecord, thread, 4, "", 0, false)
	assertDebugLocal(t, luaRecord, thread, 0, "", 0, false)

	nativeRecord, found := thread.debugActivation(0)
	if !found {
		t.Fatal("native local activation is missing")
	}
	assertDebugLocal(t, nativeRecord, thread, 1, debugTemporaryName, 7, true)
	assertDebugLocal(t, nativeRecord, thread, 2, debugTemporaryName, 8, true)
	assertDebugLocal(t, nativeRecord, thread, 3, "", 0, false)

	thread.frames = nil
	thread.top = 0
	thread.frameExtent = 0
}

func TestDebugActivationMutatesCapturedLocal(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	var observed *upvalue
	mutate, err := state.NewNativeFunction(func(frame Frame) Outcome {
		record, found := frame.thread.debugActivation(1)
		if !found {
			t.Error("Lua caller activation is missing")
			return frame.Return()
		}
		name, stackIndex, found := record.local(frame.thread, 1)
		if !found || name != "captured" {
			t.Errorf(
				"captured local = (%q, %d, %v)",
				name,
				stackIndex,
				found,
			)
			return frame.Return()
		}
		closure, ok := frame.functionObject(0)
		if !ok || closure.prototype == nil ||
			closure.prototype.upvalues != 1 {
			t.Error("mutator did not receive the captured Lua closure")
			return frame.Return()
		}
		observed = closure.luaUpvalueUnchecked(0)
		if !testUpvalueIsOpen(observed) ||
			observed.cell != &frame.thread.values[stackIndex] {
			t.Error("captured upvalue does not reference the debug local cell")
			return frame.Return()
		}
		writeSlot(&frame.thread.values[stackIndex], numberSlot(73))
		if value, ok := observed.read().owningValue().AsNumber(); !ok || value != 73 {
			t.Errorf("open upvalue after local mutation = (%v, %v)", value, ok)
		}
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetRawGlobal("mutate_local", mutate.Value()); err != nil {
		t.Fatal(err)
	}

	chunk := mustLoadString(t, state, "@debug-local-mutation.lua", `return function()
	local captured = 10
	local function read()
		return captured
	end
	mutate_local(read)
	return read()
end
`)
	loaded, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded function count = %d; want 1", len(loaded))
	}
	results, err := state.Call(loaded[0])
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(73))
	if observed == nil {
		t.Fatal("mutator did not observe the captured upvalue")
	}
	if testUpvalueIsOpen(observed) {
		t.Fatal("captured upvalue remained open after its activation returned")
	}
	if value, ok := observed.read().owningValue().AsNumber(); !ok || value != 73 {
		t.Fatalf("closed upvalue after local mutation = (%v, %v)", value, ok)
	}
}

func TestDebugActivationInspectsSuspendedCoroutine(t *testing.T) {
	state := newStateWithBase(t, Options{})
	defer state.Close()

	chunk := mustLoadString(t, state, "@debug-suspended.lua", `local marker = 17
coroutine.yield(marker)
return marker
`)
	thread, err := state.NewThread(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	yielded, status, err := thread.Resume()
	if err != nil {
		t.Fatal(err)
	}
	if status != ThreadSuspended {
		t.Fatalf("coroutine status = %v; want suspended", status)
	}
	assertTestValues(t, yielded, Number(17))

	object := thread.runtimeObject()
	native, found := object.debugActivation(0)
	if !found ||
		native.prototype() != nil ||
		native.currentPC() != -1 ||
		native.currentLine() != -1 {
		t.Fatalf("suspended native activation = (%+v, %v)", native, found)
	}
	category, name, named := native.functionName(object)
	if !named || category != "field" || name != "yield" {
		t.Fatalf(
			"suspended yield name = (%q, %q, %v); want field yield",
			category,
			name,
			named,
		)
	}

	luaRecord, found := object.debugActivation(1)
	if !found ||
		luaRecord.prototype() == nil ||
		luaRecord.prototype().SourceName() != "@debug-suspended.lua" ||
		luaRecord.currentLine() != 2 {
		t.Fatalf("suspended Lua activation = (%+v, %v)", luaRecord, found)
	}
	localName, stackIndex, found := luaRecord.local(object, 1)
	if !found || localName != "marker" {
		t.Fatalf(
			"suspended local = (%q, %d, %v); want marker",
			localName,
			stackIndex,
			found,
		)
	}
	assertTestSlot(t, object.values[stackIndex], Number(17))
	if _, found := object.debugActivation(2); found {
		t.Fatal("suspended coroutine exposed an extra logical activation")
	}

	returned, status, err := thread.Resume()
	if err != nil {
		t.Fatal(err)
	}
	if status != ThreadDead {
		t.Fatalf("resumed coroutine status = %v; want dead", status)
	}
	assertTestValues(t, returned, Number(17))
	if _, found := object.debugActivation(0); found {
		t.Fatal("dead coroutine retained a debug activation")
	}
}

func TestPrototypeDebugLookupBoundaries(t *testing.T) {
	builder := testPrototypeBuilder(
		makeABC(opLoadNil, 0, 0, 0),
		makeABC(opMove, 1, 0, 0),
		makeABC(opReturn, 0, 1, 0),
	)
	builder.upvalues = 2
	builder.debug = &prototypeDebugBuilder{
		lines: []int{1, 2, 3},
		locals: []prototypeLocalBuilder{
			{
				name:    newInternedText("outer"),
				startPC: 0,
				endPC:   3,
			},
			{
				name:    newInternedText("inner"),
				startPC: 1,
				endPC:   2,
			},
			{
				name:    newInternedText("replacement"),
				startPC: 2,
				endPC:   3,
			},
		},
		upvalues: []*internedText{newInternedText("named")},
	}
	prototype, syntaxError := builder.seal()
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}

	assertActiveLocal(t, prototype, 0, 1, "outer")
	assertActiveLocal(t, prototype, 0, 2, "")
	assertActiveLocal(t, prototype, 1, 1, "outer")
	assertActiveLocal(t, prototype, 1, 2, "inner")
	assertActiveLocal(t, prototype, 2, 1, "outer")
	assertActiveLocal(t, prototype, 2, 2, "replacement")
	assertActiveLocal(t, prototype, 2, 3, "")
	assertActiveLocal(t, prototype, -1, 1, "")
	assertActiveLocal(t, prototype, 3, 1, "")
	assertActiveLocal(t, prototype, 1, 0, "")

	if got := prototype.debugUpvalueName(-1); got != "" {
		t.Fatalf("negative upvalue name = %q; want empty", got)
	}
	if got := prototype.debugUpvalueName(0); got != "named" {
		t.Fatalf("named upvalue = %q; want named", got)
	}
	if got := prototype.debugUpvalueName(1); got != "?" {
		t.Fatalf("unnamed upvalue = %q; want ?", got)
	}
	if got := prototype.debugUpvalueName(2); got != "" {
		t.Fatalf("out-of-range upvalue name = %q; want empty", got)
	}

	strippedBuilder := testPrototypeBuilder(makeABC(opReturn, 0, 1, 0))
	strippedBuilder.upvalues = 2
	stripped, syntaxError := strippedBuilder.seal()
	if syntaxError != nil {
		t.Fatal(syntaxError)
	}
	if got := stripped.debugUpvalueName(0); got != "?" {
		t.Fatalf("stripped first upvalue name = %q; want ?", got)
	}
	if got := stripped.debugUpvalueName(1); got != "?" {
		t.Fatalf("stripped second upvalue name = %q; want ?", got)
	}
	if got := (*Prototype)(nil).debugUpvalueName(0); got != "" {
		t.Fatalf("nil prototype upvalue name = %q; want empty", got)
	}
}

func assertDebugLocal(
	t *testing.T,
	record debugActivation,
	thread *threadObject,
	ordinal int,
	wantName string,
	wantIndex int,
	wantFound bool,
) {
	t.Helper()
	name, stackIndex, found := record.local(thread, ordinal)
	if name != wantName || stackIndex != wantIndex || found != wantFound {
		t.Fatalf(
			"local %d = (%q, %d, %v); want (%q, %d, %v)",
			ordinal,
			name,
			stackIndex,
			found,
			wantName,
			wantIndex,
			wantFound,
		)
	}
}

func assertActiveLocal(
	t *testing.T,
	prototype *Prototype,
	pc int,
	ordinal int,
	want string,
) {
	t.Helper()
	local := prototype.activeLocal(pc, ordinal)
	if want == "" {
		if local != nil {
			t.Fatalf(
				"active local at pc %d ordinal %d = %q; want none",
				pc,
				ordinal,
				local.text,
			)
		}
		return
	}
	if local == nil || local.text != want {
		var got string
		if local != nil {
			got = local.text
		}
		t.Fatalf(
			"active local at pc %d ordinal %d = %q; want %q",
			pc,
			ordinal,
			got,
			want,
		)
	}
}
