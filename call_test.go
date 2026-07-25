package lua

import (
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
	thread := state.MainThread()

	setTestCall(thread, 0, function, Number(10))
	if callErr := thread.pushLuaCall(function, 0, 1, 1); callErr != nil {
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
	if callErr := thread.pushLuaCall(function, 0, 4, 0); callErr != nil {
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
	thread := state.MainThread()

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
	if callErr := thread.pushLuaCall(function, 0, 4, allResults); callErr != nil {
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
	if callErr := thread.pushLuaCall(function, 0, 0, 0); callErr != nil {
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
	if callErr := thread.pushLuaCall(function, 0, 2, 0); callErr != nil {
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
	thread := state.MainThread()

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
	if callErr := thread.pushLuaCall(legacy, 0, 4, 0); callErr != nil {
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
	if callErr := thread.pushLuaCall(modern, 0, 3, 0); callErr != nil {
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
			thread := state.MainThread()
			function := newTestLuaFunction(t, state, 0, 6, 0, 0)

			setTestCall(thread, 0, function)
			if callErr := thread.pushLuaCall(
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
	thread := state.MainThread()
	caller := newTestLuaFunction(t, state, 0, 8, 0, 0)
	callee := newTestLuaFunction(t, state, 1, 2, 0, 0)

	setTestCall(thread, 0, caller)
	if callErr := thread.pushLuaCall(caller, 0, 0, 0); callErr != nil {
		t.Fatal(callErr)
	}
	callerFrame := thread.frames[0]
	retainedIndex := int(callerFrame.base) + 6
	retained := state.String("caller register")
	thread.values[retainedIndex] = slotFromValue(retained)

	callBase := int(callerFrame.base) + 2
	thread.values[callBase] = slotFromValue(callee.Value())
	thread.values[callBase+1] = slotFromValue(Number(7))
	if callErr := thread.pushLuaCall(callee, callBase, 1, 1); callErr != nil {
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
	thread.values[openBase] = slotFromValue(callee.Value())
	thread.values[openBase+1] = slotFromValue(Number(11))
	if callErr := thread.pushLuaCall(callee, openBase, 1, allResults); callErr != nil {
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
	thread := state.MainThread()
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
	if callErr := thread.pushLuaCall(first, 0, 0, 2); callErr != nil {
		t.Fatal(callErr)
	}
	frame := thread.frames[0]
	capturedIndex := int(frame.base) + 1
	capturedValue := state.String("closed before reuse")
	thread.values[capturedIndex] = slotFromValue(capturedValue)
	captured := thread.captureUpvalue(capturedIndex)

	callBase := int(frame.base) + 3
	thread.values[callBase] = slotFromValue(second.Value())
	thread.values[callBase+1] = slotFromValue(Number(10))
	thread.values[callBase+2] = slotFromValue(Number(20))
	if callErr := thread.replaceLuaCall(second, callBase, 2); callErr != nil {
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
	assertTestSlot(t, thread.values[0], second.Value())
	assertTestSlot(t, thread.values[1], Number(10))
	assertTestSlot(t, thread.values[2], Number(20))
	if captured.thread != nil {
		t.Fatal("tail replacement left a current-frame upvalue open")
	}
	assertTestSlot(t, captured.read(), capturedValue)

	callBase = int(frame.base) + 2
	thread.values[callBase] = slotFromValue(third.Value())
	thread.values[callBase+1] = slotFromValue(Number(30))
	if callErr := thread.replaceLuaCall(third, callBase, 1); callErr != nil {
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
	assertTestSlot(t, thread.values[0], third.Value())
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
	thread := state.MainThread()
	caller := newTestLuaFunction(t, state, 0, 3, 0, 0)
	large := newTestLuaFunction(t, state, 0, 64, 0, 0)

	setTestCall(thread, 0, caller)
	if callErr := thread.pushLuaCall(caller, 0, 0, 0); callErr != nil {
		t.Fatal(callErr)
	}
	frame := thread.frames[0]
	capturedIndex := int(frame.base)
	thread.values[capturedIndex] = slotFromValue(state.String("before growth"))
	captured := thread.captureUpvalue(capturedIndex)
	oldCapacity := cap(thread.values)

	callBase := int(frame.base) + 1
	thread.values[callBase] = slotFromValue(large.Value())
	if callErr := thread.pushLuaCall(large, callBase, 0, 0); callErr != nil {
		t.Fatal(callErr)
	}
	if cap(thread.values) <= oldCapacity {
		t.Fatalf("value stack capacity did not grow beyond %d", oldCapacity)
	}
	assertTestSlot(t, captured.read(), state.String("before growth"))
	captured.write(slotFromValue(Number(42)))
	assertTestSlot(t, thread.values[capturedIndex], Number(42))
}

func TestLuaCallUnwindClosesOnlyRemovedFrames(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	thread := state.MainThread()
	function := newTestLuaFunction(t, state, 0, 6, 0, 0)

	setTestCall(thread, 0, function)
	if callErr := thread.pushLuaCall(function, 0, 0, 0); callErr != nil {
		t.Fatal(callErr)
	}
	root := thread.frames[0]
	rootIndex := int(root.base)
	thread.values[rootIndex] = slotFromValue(state.String("root"))
	rootUpvalue := thread.captureUpvalue(rootIndex)

	callBase := int(root.base) + 2
	thread.values[callBase] = slotFromValue(function.Value())
	if callErr := thread.pushLuaCall(function, callBase, 0, 0); callErr != nil {
		t.Fatal(callErr)
	}
	child := thread.frames[1]
	childIndex := int(child.base)
	thread.values[childIndex] = slotFromValue(state.String("child"))
	childUpvalue := thread.captureUpvalue(childIndex)
	childExtent := thread.frameExtent

	thread.unwindLuaCalls(1)
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
	if childUpvalue.thread != nil || rootUpvalue.thread != thread {
		t.Fatal("partial unwind closed the wrong upvalues")
	}
	assertTestSlot(t, childUpvalue.read(), state.String("child"))
	for index := thread.frameExtent; index < childExtent; index++ {
		if thread.values[index] != (slot{}) {
			t.Fatalf("unwind retained dead slot %d", index)
		}
	}

	thread.unwindLuaCalls(0)
	if len(thread.frames) != 0 ||
		thread.top != 0 ||
		thread.frameExtent != 0 ||
		rootUpvalue.thread != nil {
		t.Fatal("root unwind did not release the execution stack")
	}
	assertTestSlot(t, rootUpvalue.read(), state.String("root"))
}

func TestLuaCallLimitFailuresAreAtomic(t *testing.T) {
	t.Run("values", func(t *testing.T) {
		state, err := New(Options{MaxValues: 4})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		thread := state.MainThread()
		function := newTestLuaFunction(t, state, 0, 5, 0, 0)
		setTestCall(thread, 0, function)
		before := slices.Clone(thread.values)
		beforeTop := thread.top

		callErr := thread.pushLuaCall(function, 0, 0, 0)
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
		thread := state.MainThread()
		caller := newTestLuaFunction(t, state, 0, 4, 0, 0)
		callee := newTestLuaFunction(t, state, 0, 2, 0, 0)
		setTestCall(thread, 0, caller)
		if callErr := thread.pushLuaCall(caller, 0, 0, 0); callErr != nil {
			t.Fatal(callErr)
		}
		callBase := int(thread.frames[0].base) + 1
		thread.values[callBase] = slotFromValue(callee.Value())
		beforeValues := slices.Clone(thread.values)
		beforeFrames := slices.Clone(thread.frames)
		beforeTop := thread.top

		callErr := thread.pushLuaCall(callee, callBase, 0, 0)
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
		thread := state.MainThread()
		function := newTestLuaFunction(t, state, 0, 2, 0, 0)
		setTestCall(thread, 0, function)
		before := slices.Clone(thread.values)
		beforeTop := thread.top

		callErr := thread.pushLuaCall(function, 0, 0, 5)
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
		thread := state.MainThread()
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
		if callErr := thread.pushLuaCall(current, 0, 0, 0); callErr != nil {
			t.Fatal(callErr)
		}
		frame := thread.frames[0]
		callBase := int(frame.base) + 1
		thread.values[callBase] = slotFromValue(tooLarge.Value())
		captured := thread.captureUpvalue(int(frame.base))
		beforeValues := slices.Clone(thread.values)
		beforeFrames := slices.Clone(thread.frames)
		beforeTop := thread.top

		callErr := thread.replaceLuaCall(tooLarge, callBase, 0)
		if callErr == nil || callErr.Category() != ResourceError {
			t.Fatalf("tail value limit error = %v", callErr)
		}
		if captured.thread != thread ||
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
		thread := state.MainThread()
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

		callErr := thread.pushLuaMetamethodCall(handler, 0, 2, 0)
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
	thread := state.MainThread()
	function := newTestLuaFunction(t, state, 0, 2, 0, 0)

	setTestCall(thread, 0, function)
	for index := 0; index < depth; index++ {
		callBase := 0
		if index != 0 {
			callBase = int(thread.frames[len(thread.frames)-1].base)
			thread.values[callBase] = slotFromValue(function.Value())
		}
		if callErr := thread.pushLuaCall(function, callBase, 0, 0); callErr != nil {
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
	thread := state.MainThread()
	first := newTestLuaFunction(t, state, 2, 4, 0, 0)
	tail := newTestLuaFunction(t, state, 1, 3, 0, 0)
	thread.reserveValues(16)
	thread.reserveFrames(1)

	run := func() {
		thread.values[0] = slotFromValue(first.Value())
		thread.values[1] = slotFromValue(Number(10))
		thread.values[2] = slotFromValue(Number(20))
		thread.top = 3
		if callErr := thread.pushLuaCall(first, 0, 2, 0); callErr != nil {
			panic(callErr)
		}
		callBase := int(thread.frames[0].base) + 2
		thread.values[callBase] = slotFromValue(tail.Value())
		thread.values[callBase+1] = slotFromValue(Number(30))
		if callErr := thread.replaceLuaCall(tail, callBase, 1); callErr != nil {
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
) *Function {
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
		state.globals,
		upvalues,
	)
}

func setTestCall(
	thread *Thread,
	callBase int,
	function *Function,
	arguments ...Value,
) {
	end := callBase + 1 + len(arguments)
	thread.reserveValues(end)
	thread.values[callBase] = slotFromValue(function.Value())
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
