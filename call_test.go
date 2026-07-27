package lua

import (
	"runtime"
	"slices"
	"testing"
	"unsafe"
)

func TestLuaCallPlacesFixedArguments(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	function := newTestLuaFunction(t, state, 2, 5, 0, 0)
	thread := state.main

	setTestCall(thread, 0, function, Number(10))
	if callErr := thread.pushFunctionCall(function, 0, 1, 1); callErr != nil {
		t.Fatal(callErr)
	}
	frame := thread.frames[0]
	if frame.function != function ||
		frame.base != 1 ||
		frame.resultBase != 0 ||
		frame.tailCalls != 0 ||
		frame.wantedResults != 1 ||
		thread.top != 6 {
		t.Fatalf("fixed activation = %+v, top %d", frame, thread.top)
	}
	assertTestSlot(t, thread.values[1], Number(10))
	for index := 2; index < 6; index++ {
		assertTestSlot(t, thread.values[index], Nil())
	}
	thread.finishLuaCall(1, 0)

	setTestCall(
		thread,
		0,
		function,
		Number(20),
		Number(30),
		Number(40),
		Number(50),
	)
	if callErr := thread.pushFunctionCall(function, 0, 4, 0); callErr != nil {
		t.Fatal(callErr)
	}
	assertTestSlot(t, thread.values[1], Number(20))
	assertTestSlot(t, thread.values[2], Number(30))
	for index := 3; index < 6; index++ {
		assertTestSlot(t, thread.values[index], Nil())
	}
	thread.finishLuaCall(1, 0)
}

func TestLuaCallUsesPaddedVarargLayout(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	thread := state.main

	function := newTestLuaFunction(
		t,
		state,
		2,
		5,
		varargHasArg|varargIsVararg,
		0,
	)
	setTestCall(
		thread,
		0,
		function,
		Number(10),
		Number(20),
		Nil(),
		Number(40),
	)
	if callErr := thread.pushFunctionCall(function, 0, 4, allResults); callErr != nil {
		t.Fatal(callErr)
	}
	frame := thread.frames[0]
	if frame.base != 5 ||
		frame.varargCount() != 2 ||
		frame.wantedResults != allResults ||
		thread.top != 10 {
		t.Fatalf("vararg activation = %+v, top %d", frame, thread.top)
	}
	assertTestSlot(t, thread.values[1], Nil())
	assertTestSlot(t, thread.values[2], Nil())
	assertTestSlot(t, thread.values[3], Nil())
	assertTestSlot(t, thread.values[4], Number(40))
	assertTestSlot(t, thread.values[5], Number(10))
	assertTestSlot(t, thread.values[6], Number(20))
	for index := 7; index < 10; index++ {
		assertTestSlot(t, thread.values[index], Nil())
	}
	thread.finishLuaCall(5, 0)

	setTestCall(thread, 0, function)
	if callErr := thread.pushFunctionCall(function, 0, 0, 0); callErr != nil {
		t.Fatal(callErr)
	}
	frame = thread.frames[0]
	if frame.base != 3 || frame.varargCount() != 0 || thread.top != 8 {
		t.Fatalf("padded empty vararg activation = %+v, top %d", frame, thread.top)
	}
	for index := 1; index < 8; index++ {
		assertTestSlot(t, thread.values[index], Nil())
	}
	thread.finishLuaCall(3, 0)

	setTestCall(thread, 0, function, Number(50), Number(60))
	if callErr := thread.pushFunctionCall(function, 0, 2, 0); callErr != nil {
		t.Fatal(callErr)
	}
	frame = thread.frames[0]
	if frame.varargCount() != 0 {
		t.Fatalf("equal-arity vararg count = %d; want 0", frame.varargCount())
	}
	thread.finishLuaCall(int(frame.base), 0)
}

func TestLuaCallBuildsLegacyArgOnlyWhenRequired(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	thread := state.main

	legacy := newTestLuaFunction(
		t,
		state,
		2,
		4,
		varargHasArg|varargIsVararg|varargNeedsArg,
		0,
	)
	setTestCall(
		thread,
		0,
		legacy,
		Number(10),
		Number(20),
		Nil(),
		Number(40),
	)
	if callErr := thread.pushFunctionCall(legacy, 0, 4, 0); callErr != nil {
		t.Fatal(callErr)
	}
	frame := thread.frames[0]
	argValue := thread.values[int(frame.base)+2].owningValue()
	arg, ok := argValue.Table()
	if !ok {
		t.Fatalf("legacy arg register = %v, want table", argValue)
	}
	assertTestValue(t, arg.RawGetInt(1), Nil())
	assertTestValue(t, arg.RawGetInt(2), Number(40))
	assertTestValue(t, arg.RawGetString("n"), Number(2))
	thread.finishLuaCall(int(frame.base), 0)

	modern := newTestLuaFunction(
		t,
		state,
		2,
		4,
		varargHasArg|varargIsVararg,
		0,
	)
	setTestCall(
		thread,
		0,
		modern,
		Number(10),
		Number(20),
		Number(30),
	)
	if callErr := thread.pushFunctionCall(modern, 0, 3, 0); callErr != nil {
		t.Fatal(callErr)
	}
	frame = thread.frames[0]
	assertTestSlot(t, thread.values[int(frame.base)+2], Nil())
	thread.finishLuaCall(int(frame.base), 0)
}

func TestLuaCallAdjustsOverlappingResults(t *testing.T) {
	tests := []struct {
		name     string
		wanted   int
		actual   []Value
		expected []Value
	}{
		{
			name:     "open",
			wanted:   allResults,
			actual:   []Value{Number(10), Nil(), Number(30)},
			expected: []Value{Number(10), Nil(), Number(30)},
		},
		{
			name:     "pad",
			wanted:   3,
			actual:   []Value{Number(10), Number(20)},
			expected: []Value{Number(10), Number(20), Nil()},
		},
		{
			name:     "truncate",
			wanted:   1,
			actual:   []Value{Number(10), Number(20), Number(30)},
			expected: []Value{Number(10)},
		},
		{
			name:     "discard",
			wanted:   0,
			actual:   []Value{Number(10), Number(20)},
			expected: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, err := New(Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			thread := state.main
			function := newTestLuaFunction(t, state, 0, 6, 0, 0)

			setTestCall(thread, 0, function)
			if callErr := thread.pushFunctionCall(
				function,
				0,
				0,
				test.wanted,
			); callErr != nil {
				t.Fatal(callErr)
			}
			frame := thread.frames[0]
			first := int(frame.base) + 1
			for index, value := range test.actual {
				thread.values[first+index] = slotFromValue(value)
			}
			thread.finishLuaCall(first, len(test.actual))

			if len(thread.frames) != 0 || thread.top != len(test.expected) {
				t.Fatalf(
					"return left %d frames and top %d; want 0 and %d",
					len(thread.frames),
					thread.top,
					len(test.expected),
				)
			}
			for index, value := range test.expected {
				assertTestSlot(t, thread.values[index], value)
			}
			for index := thread.top; index < 7; index++ {
				if thread.values[index] != (slot{}) {
					t.Fatalf("inactive slot %d retained %v", index, thread.values[index])
				}
			}
		})
	}
}

func TestLuaCallPreservesSuspendedCallerRegisters(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	thread := state.main
	caller := newTestLuaFunction(t, state, 0, 8, 0, 0)
	callee := newTestLuaFunction(t, state, 1, 2, 0, 0)

	setTestCall(thread, 0, caller)
	if callErr := thread.pushFunctionCall(caller, 0, 0, 0); callErr != nil {
		t.Fatal(callErr)
	}
	callerFrame := thread.frames[0]
	retainedIndex := int(callerFrame.base) + 6
	retained := state.String("caller register")
	thread.values[retainedIndex] = slotFromValue(retained)

	callBase := int(callerFrame.base) + 2
	thread.values[callBase] = slotFromFunctionObject(callee)
	thread.values[callBase+1] = slotFromValue(Number(7))
	if callErr := thread.pushFunctionCall(callee, callBase, 1, 1); callErr != nil {
		t.Fatal(callErr)
	}
	assertTestSlot(t, thread.values[retainedIndex], retained)

	calleeFrame := thread.frames[1]
	thread.values[int(calleeFrame.base)] = slotFromValue(Number(9))
	thread.finishLuaCall(int(calleeFrame.base), 1)
	if len(thread.frames) != 1 ||
		thread.top != int(callerFrame.base)+int(caller.prototype.registers) {
		t.Fatalf("caller was not restored: frames %d, top %d", len(thread.frames), thread.top)
	}
	assertTestSlot(t, thread.values[callBase], Number(9))
	assertTestSlot(t, thread.values[retainedIndex], retained)

	openBase := int(callerFrame.base) + 3
	thread.values[openBase] = slotFromFunctionObject(callee)
	thread.values[openBase+1] = slotFromValue(Number(11))
	if callErr := thread.pushFunctionCall(callee, openBase, 1, allResults); callErr != nil {
		t.Fatal(callErr)
	}
	openFrame := thread.frames[1]
	thread.values[int(openFrame.base)] = slotFromValue(Number(12))
	thread.values[int(openFrame.base)+1] = slotFromValue(Nil())
	thread.finishLuaCall(int(openFrame.base), 2)
	if thread.top != openBase+2 ||
		thread.frameExtent != int(callerFrame.base)+int(caller.prototype.registers) {
		t.Fatalf(
			"open return left top %d and frame extent %d",
			thread.top,
			thread.frameExtent,
		)
	}
	assertTestSlot(t, thread.values[openBase], Number(12))
	assertTestSlot(t, thread.values[openBase+1], Nil())
	assertTestSlot(t, thread.values[retainedIndex], retained)

	thread.finishLuaCall(int(callerFrame.base), 0)
	if thread.values[retainedIndex] != (slot{}) {
		t.Fatal("root return retained a dead caller register")
	}
}

func TestLuaTailCallReusesActivationAndClosesUpvalues(t *testing.T) {
	state, err := New(Options{MaxFrames: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	thread := state.main
	first := newTestLuaFunction(t, state, 0, 8, 0, 0)
	second := newTestLuaFunction(t, state, 2, 4, 0, 0)
	third := newTestLuaFunction(
		t,
		state,
		2,
		4,
		varargHasArg|varargIsVararg,
		0,
	)

	setTestCall(thread, 0, first)
	if callErr := thread.pushFunctionCall(first, 0, 0, 2); callErr != nil {
		t.Fatal(callErr)
	}
	frame := thread.frames[0]
	capturedIndex := int(frame.base) + 1
	capturedValue := state.String("closed before reuse")
	thread.values[capturedIndex] = slotFromValue(capturedValue)
	captured := thread.captureUpvalue(capturedIndex)

	callBase := int(frame.base) + 3
	thread.values[callBase] = slotFromFunctionObject(second)
	thread.values[callBase+1] = slotFromValue(Number(10))
	thread.values[callBase+2] = slotFromValue(Number(20))
	if callErr := thread.replaceFunctionCall(second, callBase, 2); callErr != nil {
		t.Fatal(callErr)
	}
	frame = thread.frames[0]
	if len(thread.frames) != 1 ||
		frame.function != second ||
		frame.resultBase != 0 ||
		frame.base != 1 ||
		frame.tailCalls != 1 ||
		frame.wantedResults != 2 ||
		thread.top != 5 {
		t.Fatalf("first tail replacement = %+v, frames %d, top %d", frame, len(thread.frames), thread.top)
	}
	if thread.values[0] != slotFromFunctionObject(second) {
		t.Fatal("tail call did not retain the second function")
	}
	assertTestSlot(t, thread.values[1], Number(10))
	assertTestSlot(t, thread.values[2], Number(20))
	if testUpvalueIsOpen(captured) {
		t.Fatal("tail replacement left a current-frame upvalue open")
	}
	assertTestSlot(t, captured.read(), capturedValue)

	callBase = int(frame.base) + 2
	thread.values[callBase] = slotFromFunctionObject(third)
	thread.values[callBase+1] = slotFromValue(Number(30))
	if callErr := thread.replaceFunctionCall(third, callBase, 1); callErr != nil {
		t.Fatal(callErr)
	}
	frame = thread.frames[0]
	if frame.function != third ||
		frame.base != 3 ||
		frame.varargCount() != 0 ||
		frame.tailCalls != 2 ||
		frame.wantedResults != 2 ||
		thread.top != 7 {
		t.Fatalf("second tail replacement = %+v, top %d", frame, thread.top)
	}
	if thread.values[0] != slotFromFunctionObject(third) {
		t.Fatal("tail call did not retain the third function")
	}
	assertTestSlot(t, thread.values[1], Nil())
	assertTestSlot(t, thread.values[2], Nil())
	assertTestSlot(t, thread.values[3], Number(30))
	assertTestSlot(t, thread.values[4], Nil())
}

func TestLuaCallStackGrowthKeepsOpenUpvalueIndexesValid(t *testing.T) {
	state, err := New(Options{MaxValues: 128})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	thread := state.main
	caller := newTestLuaFunction(t, state, 0, 3, 0, 0)
	large := newTestLuaFunction(t, state, 0, 64, 0, 0)

	setTestCall(thread, 0, caller)
	if callErr := thread.pushFunctionCall(caller, 0, 0, 0); callErr != nil {
		t.Fatal(callErr)
	}
	frame := thread.frames[0]
	capturedIndex := int(frame.base)
	thread.values[capturedIndex] = slotFromValue(state.String("before growth"))
	captured := thread.captureUpvalue(capturedIndex)
	oldCell := captured.cell
	oldCapacity := cap(thread.values)

	callBase := int(frame.base) + 1
	thread.values[callBase] = slotFromFunctionObject(large)
	if callErr := thread.pushFunctionCall(large, callBase, 0, 0); callErr != nil {
		t.Fatal(callErr)
	}
	if cap(thread.values) <= oldCapacity {
		t.Fatalf("value stack capacity did not grow beyond %d", oldCapacity)
	}
	if captured.cell == oldCell ||
		captured.cell != &thread.values[capturedIndex] {
		t.Fatal("stack growth did not retarget the open upvalue cell")
	}
	assertTestSlot(t, captured.read(), state.String("before growth"))
	captured.write(slotFromValue(Number(42)))
	assertTestSlot(t, thread.values[capturedIndex], Number(42))
}

func TestValueStackGrowthRetargetsOpenUpvalueCells(t *testing.T) {
	state, err := New(Options{MaxValues: 256})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	thread := state.main
	thread.values = make([]slot, 4, 4)
	lowValue := state.String("low")
	highValue := state.String("high")
	thread.values[1] = slotFromValue(lowValue)
	thread.values[3] = slotFromValue(highValue)
	low := thread.captureUpvalue(1)
	high := thread.captureUpvalue(3)
	firstBacking := thread.values

	thread.reserveValues(32)
	if low.cell != &thread.values[1] ||
		high.cell != &thread.values[3] {
		t.Fatal("first growth did not retarget every open upvalue")
	}
	firstBacking[1] = slotFromValue(Number(1))
	firstBacking[3] = slotFromValue(Number(3))
	assertTestSlot(t, low.read(), lowValue)
	assertTestSlot(t, high.read(), highValue)
	runtime.GC()

	secondBacking := thread.values
	thread.reserveValues(128)
	if low.cell != &thread.values[1] ||
		high.cell != &thread.values[3] {
		t.Fatal("second growth did not retarget every open upvalue")
	}
	secondBacking[1] = slotFromValue(Number(11))
	secondBacking[3] = slotFromValue(Number(13))
	assertTestSlot(t, low.read(), lowValue)
	assertTestSlot(t, high.read(), highValue)
	runtime.GC()

	thread.closeUpvalues(0)
	if testUpvalueIsOpen(low) || testUpvalueIsOpen(high) {
		t.Fatal("closing did not retarget upvalues to embedded storage")
	}
	thread.values[1] = slotFromValue(Number(21))
	thread.values[3] = slotFromValue(Number(23))
	runtime.GC()
	assertTestSlot(t, low.read(), lowValue)
	assertTestSlot(t, high.read(), highValue)
}

func TestLuaCallUnwindClosesOnlyRemovedFrames(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	thread := state.main
	function := newTestLuaFunction(t, state, 0, 6, 0, 0)

	setTestCall(thread, 0, function)
	if callErr := thread.pushFunctionCall(function, 0, 0, 0); callErr != nil {
		t.Fatal(callErr)
	}
	root := thread.frames[0]
	rootIndex := int(root.base)
	thread.values[rootIndex] = slotFromValue(state.String("root"))
	rootUpvalue := thread.captureUpvalue(rootIndex)

	callBase := int(root.base) + 2
	thread.values[callBase] = slotFromFunctionObject(function)
	if callErr := thread.pushFunctionCall(function, callBase, 0, 0); callErr != nil {
		t.Fatal(callErr)
	}
	child := thread.frames[1]
	childIndex := int(child.base)
	thread.values[childIndex] = slotFromValue(state.String("child"))
	childUpvalue := thread.captureUpvalue(childIndex)
	childExtent := thread.frameExtent

	thread.unwindCalls(1)
	if len(thread.frames) != 1 ||
		thread.frameExtent != int(root.base)+int(root.function.prototype.registers) ||
		thread.top != thread.frameExtent {
		t.Fatalf(
			"partial unwind left %d frames, extent %d, top %d",
			len(thread.frames),
			thread.frameExtent,
			thread.top,
		)
	}
	if testUpvalueIsOpen(childUpvalue) || !testUpvalueIsOpen(rootUpvalue) {
		t.Fatal("partial unwind closed the wrong upvalues")
	}
	assertTestSlot(t, childUpvalue.read(), state.String("child"))
	for index := thread.frameExtent; index < childExtent; index++ {
		if thread.values[index] != (slot{}) {
			t.Fatalf("unwind retained dead slot %d", index)
		}
	}

	thread.unwindCalls(0)
	if len(thread.frames) != 0 ||
		thread.top != 0 ||
		thread.frameExtent != 0 ||
		testUpvalueIsOpen(rootUpvalue) {
		t.Fatal("root unwind did not release the execution stack")
	}
	assertTestSlot(t, rootUpvalue.read(), state.String("root"))
}

func TestFixedLuaCallFastMissIsAtomic(t *testing.T) {
	type snapshot struct {
		values      []slot
		frames      []activation
		top         int
		frameExtent int
		valueCap    int
		frameCap    int
		valueData   *slot
		frameData   *activation
	}
	takeSnapshot := func(thread *threadObject) snapshot {
		before := snapshot{
			values:      slices.Clone(thread.values),
			frames:      slices.Clone(thread.frames),
			top:         thread.top,
			frameExtent: thread.frameExtent,
			valueCap:    cap(thread.values),
			frameCap:    cap(thread.frames),
		}
		if len(thread.values) != 0 {
			before.valueData = &thread.values[0]
		}
		if len(thread.frames) != 0 {
			before.frameData = &thread.frames[0]
		}
		return before
	}
	assertUnchanged := func(t *testing.T, thread *threadObject, before snapshot) {
		t.Helper()
		if thread.top != before.top ||
			thread.frameExtent != before.frameExtent ||
			!slices.Equal(thread.values, before.values) ||
			!slices.Equal(thread.frames, before.frames) {
			t.Fatal("fixed-call fast miss mutated execution state")
		}
		if len(thread.values) != 0 && &thread.values[0] != before.valueData {
			t.Fatal("fixed-call fast miss replaced the value stack")
		}
		if len(thread.frames) != 0 && &thread.frames[0] != before.frameData {
			t.Fatal("fixed-call fast miss replaced the activation stack")
		}
	}
	stage := func(
		t *testing.T,
		options Options,
		calleeRegisters int,
	) (*State, *threadObject, *functionObject, int, instruction) {
		t.Helper()
		state, err := New(options)
		if err != nil {
			t.Fatal(err)
		}
		thread := state.main
		caller := newTestLuaFunction(t, state, 0, 4, 0, 0)
		callee := newTestLuaFunction(t, state, 0, calleeRegisters, 0, 0)
		setTestCall(thread, 0, caller)
		if callErr := thread.pushFunctionCall(caller, 0, 0, 0); callErr != nil {
			state.Close()
			t.Fatal(callErr)
		}
		callBase := int(thread.frames[0].base) + 1
		thread.values[callBase] = slotFromFunctionObject(callee)
		return state, thread, callee, callBase, makeABC(opCall, 1, 1, 1)
	}

	t.Run("value capacity", func(t *testing.T) {
		state, thread, callee, callBase, code := stage(
			t,
			Options{MaxValues: 128},
			64,
		)
		defer state.Close()
		before := takeSnapshot(thread)

		if thread.tryEnterFixedLuaCall(
			int(thread.frames[len(thread.frames)-1].base),
			code,
		) {
			t.Fatal("fixed call unexpectedly entered without value capacity")
		}
		assertUnchanged(t, thread, before)
		if callErr := thread.pushFunctionCall(callee, callBase, 0, 0); callErr != nil {
			t.Fatal(callErr)
		}
		if len(thread.frames) != 2 || cap(thread.values) <= before.valueCap {
			t.Fatal("checked fixed call did not grow the value stack")
		}
	})

	t.Run("value limit", func(t *testing.T) {
		state, thread, callee, callBase, code := stage(
			t,
			Options{MaxValues: 8},
			8,
		)
		defer state.Close()
		before := takeSnapshot(thread)

		if thread.tryEnterFixedLuaCall(
			int(thread.frames[len(thread.frames)-1].base),
			code,
		) {
			t.Fatal("fixed call unexpectedly entered beyond the value limit")
		}
		assertUnchanged(t, thread, before)
		callErr := thread.pushFunctionCall(callee, callBase, 0, 0)
		if callErr == nil || callErr.Category() != ResourceError {
			t.Fatalf("value limit error = %v", callErr)
		}
		assertUnchanged(t, thread, before)
	})

	t.Run("frame capacity", func(t *testing.T) {
		state, thread, callee, callBase, code := stage(
			t,
			Options{MaxFrames: 2},
			2,
		)
		defer state.Close()
		thread.frames = slices.Clip(thread.frames)
		before := takeSnapshot(thread)

		if thread.tryEnterFixedLuaCall(
			int(thread.frames[len(thread.frames)-1].base),
			code,
		) {
			t.Fatal("fixed call unexpectedly entered without frame capacity")
		}
		assertUnchanged(t, thread, before)
		if callErr := thread.pushFunctionCall(callee, callBase, 0, 0); callErr != nil {
			t.Fatal(callErr)
		}
		if len(thread.frames) != 2 || cap(thread.frames) <= before.frameCap {
			t.Fatal("checked fixed call did not grow the activation stack")
		}
	})

	t.Run("frame limit", func(t *testing.T) {
		state, thread, callee, callBase, code := stage(
			t,
			Options{MaxFrames: 1},
			2,
		)
		defer state.Close()
		before := takeSnapshot(thread)

		if thread.tryEnterFixedLuaCall(
			int(thread.frames[len(thread.frames)-1].base),
			code,
		) {
			t.Fatal("fixed call unexpectedly entered beyond the frame limit")
		}
		assertUnchanged(t, thread, before)
		callErr := thread.pushFunctionCall(callee, callBase, 0, 0)
		if callErr == nil || callErr.Category() != ResourceError {
			t.Fatalf("frame limit error = %v", callErr)
		}
		assertUnchanged(t, thread, before)
	})
}

func TestFixedLuaCallMatchesCheckedCallLayout(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	dirty, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name            string
		callerRegisters int
		callRegister    int
		parameters      int
		calleeRegisters int
		argumentCount   int
		wantedResults   int
	}{
		{
			name:            "missing arguments and padded results",
			callerRegisters: 8,
			callRegister:    2,
			parameters:      3,
			calleeRegisters: 5,
			argumentCount:   1,
			wantedResults:   2,
		},
		{
			name:            "exact arguments and one result",
			callerRegisters: 8,
			callRegister:    2,
			parameters:      2,
			calleeRegisters: 4,
			argumentCount:   2,
			wantedResults:   1,
		},
		{
			name:            "excess arguments and several results",
			callerRegisters: 7,
			callRegister:    2,
			parameters:      1,
			calleeRegisters: 3,
			argumentCount:   3,
			wantedResults:   3,
		},
		{
			name:            "callee extends the value stack",
			callerRegisters: 4,
			callRegister:    1,
			parameters:      1,
			calleeRegisters: 10,
			argumentCount:   1,
			wantedResults:   0,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			caller := newTestLuaFunction(
				t,
				state,
				0,
				test.callerRegisters,
				0,
				0,
			)
			callee := newTestLuaFunction(
				t,
				state,
				test.parameters,
				test.calleeRegisters,
				0,
				0,
			)
			stage := func() (*threadObject, int) {
				thread := &threadObject{
					objectHeader: objectHeader{owner: state.runtime},
					state:        state,
					values:       make([]slot, 0, 64),
					frames:       make([]activation, 0, 4),
					status:       ThreadReady,
				}
				setTestCall(thread, 0, caller)
				if callErr := thread.pushFunctionCall(
					caller,
					0,
					0,
					0,
				); callErr != nil {
					t.Fatal(callErr)
				}
				callerFrame := thread.frames[0]
				for index := int(callerFrame.base); index < thread.frameExtent; index++ {
					thread.values[index] = slotFromValue(dirty.Value())
				}
				callBase := int(callerFrame.base) + test.callRegister
				thread.values[callBase] = slotFromFunctionObject(callee)
				for index := 0; index < test.argumentCount; index++ {
					thread.values[callBase+1+index] = slotFromValue(
						Number(float64(index + 1)),
					)
				}
				return thread, callBase
			}

			fast, fastCallBase := stage()
			checked, checkedCallBase := stage()
			code := makeABC(
				opCall,
				test.callRegister,
				test.argumentCount+1,
				test.wantedResults+1,
			)
			if !fast.tryEnterFixedLuaCall(
				int(fast.frames[len(fast.frames)-1].base),
				code,
			) {
				t.Fatal("fixed call did not use the fast entry")
			}
			if callErr := checked.pushFunctionCall(
				callee,
				checkedCallBase,
				test.argumentCount,
				test.wantedResults,
			); callErr != nil {
				t.Fatal(callErr)
			}
			if fastCallBase != checkedCallBase {
				t.Fatal("staged calls use different result bases")
			}
			assertTestThreadStateEqual(t, fast, checked)
		})
	}
}

func TestFixedLuaReturnMatchesCheckedReturn(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	dirty, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	firstReference, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	secondReference, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	caller := newTestLuaFunction(t, state, 0, 6, 0, 0)
	callee := newTestLuaFunction(t, state, 0, 10, 0, 0)

	for _, test := range []struct {
		name          string
		firstOffset   int
		resultCount   int
		wantedResults int
		references    bool
	}{
		{
			name:          "no results",
			resultCount:   0,
			wantedResults: 0,
		},
		{
			name:          "missing results",
			resultCount:   1,
			wantedResults: 3,
		},
		{
			name:          "discarded results",
			resultCount:   3,
			wantedResults: 1,
		},
		{
			name:          "overlapping reference result windows",
			resultCount:   2,
			wantedResults: 2,
			references:    true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			stage := func() (*threadObject, int, *upvalue) {
				thread := &threadObject{
					objectHeader: objectHeader{owner: state.runtime},
					state:        state,
					values:       make([]slot, 0, 64),
					frames:       make([]activation, 0, 4),
					status:       ThreadReady,
				}
				setTestCall(thread, 0, caller)
				if callErr := thread.pushFunctionCall(
					caller,
					0,
					0,
					0,
				); callErr != nil {
					t.Fatal(callErr)
				}
				callerFrame := thread.frames[0]
				callBase := int(callerFrame.base) + 2
				thread.values[callBase] = slotFromFunctionObject(callee)
				if callErr := thread.pushFunctionCall(
					callee,
					callBase,
					0,
					test.wantedResults,
				); callErr != nil {
					t.Fatal(callErr)
				}
				calleeFrame := thread.frames[1]
				firstResult := int(calleeFrame.base) + test.firstOffset
				for index := int(calleeFrame.base); index < thread.frameExtent; index++ {
					thread.values[index] = slotFromValue(dirty.Value())
				}
				for index := 0; index < test.resultCount; index++ {
					value := numberSlot(float64(index + 1))
					if test.references {
						references := []*Table{
							firstReference,
							secondReference,
						}
						value = slotFromValue(references[index].Value())
					}
					thread.values[firstResult+index] = value
				}
				capturedIndex := thread.frameExtent - 1
				capturedValue := state.String("captured return register")
				thread.values[capturedIndex] = slotFromValue(capturedValue)
				return thread, firstResult, thread.captureUpvalue(capturedIndex)
			}

			fast, fastFirst, fastUpvalue := stage()
			checked, checkedFirst, checkedUpvalue := stage()
			code := makeABC(
				opReturn,
				test.firstOffset,
				test.resultCount+1,
				0,
			)
			if !fast.tryCompleteFixedLuaReturn(len(fast.frames)-1, code) {
				t.Fatal("fixed return did not use the fast completion")
			}
			checked.finishLuaCall(checkedFirst, test.resultCount)
			if fastFirst != checkedFirst {
				t.Fatal("staged returns use different source bases")
			}
			assertTestThreadStateEqual(t, fast, checked)
			if testUpvalueIsOpen(fastUpvalue) ||
				testUpvalueIsOpen(checkedUpvalue) {
				t.Fatal("return left a callee upvalue open")
			}
			if !rawSlotEqual(fastUpvalue.read(), checkedUpvalue.read()) {
				t.Fatal("fast and checked return closed different values")
			}
			if test.references {
				runtime.GC()
				resultBase := int(fast.frames[0].base) + 2
				assertTestSlot(
					t,
					fast.values[resultBase],
					firstReference.Value(),
				)
				assertTestSlot(
					t,
					fast.values[resultBase+1],
					secondReference.Value(),
				)
			}
		})
	}
}

func TestLuaCallLimitFailuresAreAtomic(t *testing.T) {
	t.Run("values", func(t *testing.T) {
		state, err := New(Options{MaxValues: 4})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		thread := state.main
		function := newTestLuaFunction(t, state, 0, 5, 0, 0)
		setTestCall(thread, 0, function)
		before := slices.Clone(thread.values)
		beforeTop := thread.top

		callErr := thread.pushFunctionCall(function, 0, 0, 0)
		if callErr == nil || callErr.Category() != ResourceError {
			t.Fatalf("value limit error = %v", callErr)
		}
		if len(thread.frames) != 0 ||
			thread.top != beforeTop ||
			!slices.Equal(thread.values, before) {
			t.Fatal("value-limit failure mutated the thread")
		}
	})

	t.Run("frames", func(t *testing.T) {
		state, err := New(Options{MaxFrames: 1})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		thread := state.main
		caller := newTestLuaFunction(t, state, 0, 4, 0, 0)
		callee := newTestLuaFunction(t, state, 0, 2, 0, 0)
		setTestCall(thread, 0, caller)
		if callErr := thread.pushFunctionCall(caller, 0, 0, 0); callErr != nil {
			t.Fatal(callErr)
		}
		callBase := int(thread.frames[0].base) + 1
		thread.values[callBase] = slotFromFunctionObject(callee)
		beforeValues := slices.Clone(thread.values)
		beforeFrames := slices.Clone(thread.frames)
		beforeTop := thread.top

		callErr := thread.pushFunctionCall(callee, callBase, 0, 0)
		if callErr == nil || callErr.Category() != ResourceError {
			t.Fatalf("frame limit error = %v", callErr)
		}
		if thread.top != beforeTop ||
			!slices.Equal(thread.values, beforeValues) ||
			!slices.Equal(thread.frames, beforeFrames) {
			t.Fatal("frame-limit failure mutated the thread")
		}
	})

	t.Run("results", func(t *testing.T) {
		state, err := New(Options{MaxValues: 4})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		thread := state.main
		function := newTestLuaFunction(t, state, 0, 2, 0, 0)
		setTestCall(thread, 0, function)
		before := slices.Clone(thread.values)
		beforeTop := thread.top

		callErr := thread.pushFunctionCall(function, 0, 0, 5)
		if callErr == nil || callErr.Category() != ResourceError {
			t.Fatalf("result limit error = %v", callErr)
		}
		if len(thread.frames) != 0 ||
			thread.top != beforeTop ||
			!slices.Equal(thread.values, before) {
			t.Fatal("result-limit failure mutated the thread")
		}
	})

	t.Run("tail values", func(t *testing.T) {
		state, err := New(Options{MaxValues: 8, MaxFrames: 1})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		thread := state.main
		current := newTestLuaFunction(t, state, 0, 4, 0, 0)
		tooLarge := newTestLuaFunction(
			t,
			state,
			0,
			8,
			varargIsVararg,
			0,
		)
		setTestCall(thread, 0, current)
		if callErr := thread.pushFunctionCall(current, 0, 0, 0); callErr != nil {
			t.Fatal(callErr)
		}
		frame := thread.frames[0]
		callBase := int(frame.base) + 1
		thread.values[callBase] = slotFromFunctionObject(tooLarge)
		captured := thread.captureUpvalue(int(frame.base))
		beforeValues := slices.Clone(thread.values)
		beforeFrames := slices.Clone(thread.frames)
		beforeTop := thread.top

		callErr := thread.replaceFunctionCall(tooLarge, callBase, 0)
		if callErr == nil || callErr.Category() != ResourceError {
			t.Fatalf("tail value limit error = %v", callErr)
		}
		if !testUpvalueIsOpen(captured) ||
			captured.cell != &thread.values[int(frame.base)] ||
			thread.top != beforeTop ||
			!slices.Equal(thread.values, beforeValues) ||
			!slices.Equal(thread.frames, beforeFrames) {
			t.Fatal("tail-call limit failure partially replaced the activation")
		}
	})

	t.Run("call metamethod insertion", func(t *testing.T) {
		state, err := New(Options{MaxValues: 3})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		thread := state.main
		handler := newTestLuaFunction(t, state, 0, 1, 0, 0)
		target, err := state.NewTable(0, 0)
		if err != nil {
			t.Fatal(err)
		}
		thread.reserveValues(3)
		thread.values[0] = slotFromValue(target.Value())
		thread.values[1] = slotFromValue(Number(10))
		thread.values[2] = nilSlot
		thread.top = 3
		beforeValues := slices.Clone(thread.values)
		beforeTop := thread.top

		callErr := thread.pushFunctionMetamethodCall(handler, 0, 2, 0)
		if callErr == nil || callErr.Category() != ResourceError {
			t.Fatalf("metamethod value limit error = %v", callErr)
		}
		if len(thread.frames) != 0 ||
			thread.top != beforeTop ||
			!slices.Equal(thread.values, beforeValues) {
			t.Fatal("metamethod limit failure inserted a partial call")
		}
	})
}

func TestLuaCallTracksDeepFrameExtentExactly(t *testing.T) {
	const depth = 2048
	state, err := New(Options{
		MaxValues: depth + 2,
		MaxFrames: depth,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	thread := state.main
	function := newTestLuaFunction(t, state, 0, 2, 0, 0)

	setTestCall(thread, 0, function)
	for index := 0; index < depth; index++ {
		callBase := 0
		if index != 0 {
			callBase = int(thread.frames[len(thread.frames)-1].base)
			thread.values[callBase] = slotFromFunctionObject(function)
		}
		if callErr := thread.pushFunctionCall(function, callBase, 0, 0); callErr != nil {
			t.Fatalf("depth %d: %v", index, callErr)
		}
	}
	if len(thread.frames) != depth ||
		thread.frameExtent != int(thread.frames[depth-1].base)+2 {
		t.Fatalf(
			"deep stack has %d frames and extent %d",
			len(thread.frames),
			thread.frameExtent,
		)
	}

	for len(thread.frames) != 0 {
		frame := thread.frames[len(thread.frames)-1]
		thread.finishLuaCall(int(frame.base), 0)
	}
	if thread.top != 0 || thread.frameExtent != 0 {
		t.Fatalf("deep return left top %d and extent %d", thread.top, thread.frameExtent)
	}
}

func TestLuaCallFoundationStaysCompactAndAllocationFreeWhenWarm(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) == 8 {
		if size := unsafe.Sizeof(activation{}); size != 32 {
			t.Fatalf("activation size = %d; want 32", size)
		}
	}
	requireStableAllocationAccounting(t)

	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	thread := state.main
	first := newTestLuaFunction(t, state, 2, 4, 0, 0)
	tail := newTestLuaFunction(t, state, 1, 3, 0, 0)
	thread.reserveValues(16)
	thread.reserveFrames(1)

	run := func() {
		thread.values[0] = slotFromFunctionObject(first)
		thread.values[1] = slotFromValue(Number(10))
		thread.values[2] = slotFromValue(Number(20))
		thread.top = 3
		if callErr := thread.pushFunctionCall(first, 0, 2, 0); callErr != nil {
			panic(callErr)
		}
		callBase := int(thread.frames[0].base) + 2
		thread.values[callBase] = slotFromFunctionObject(tail)
		thread.values[callBase+1] = slotFromValue(Number(30))
		if callErr := thread.replaceFunctionCall(tail, callBase, 1); callErr != nil {
			panic(callErr)
		}
		thread.finishLuaCall(int(thread.frames[0].base), 0)
	}
	run()
	if allocations := testing.AllocsPerRun(1000, run); allocations != 0 {
		t.Fatalf("warm fixed/tail call allocations = %v; want 0", allocations)
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
		state.runtime,
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
