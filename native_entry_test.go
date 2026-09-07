package lua

import (
	"slices"
	"testing"
)

func stageFixedNativeEntryTest(t *testing.T) (*State, *threadObject, int, *functionObject) {
	t.Helper()
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	caller := newTestLuaFunction(t, state, 0, 8, 0, 0)
	native, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.ReturnNumber(7)
	})
	if err != nil {
		t.Fatal(err)
	}
	thread := state.main
	setTestCall(thread, 0, caller)
	if failure := thread.pushFunctionCall(caller, 0, 0, 0); failure != nil {
		t.Fatal(failure)
	}
	base := int(thread.frames[0].base)
	callee := native.runtimeObject()
	thread.values[base+1] = slotFromFunctionObject(callee)
	thread.values[base+2] = numberSlot(11)
	thread.values[base+3] = numberSlot(22)
	thread.values[base+4] = stringSlot(state.runtime.strings.make("retained"))
	return state, thread, base, callee
}

func TestFixedNativeEntryMatchesCheckedCall(t *testing.T) {
	for _, shape := range []struct {
		name    string
		args    int
		results int
	}{
		{"no arguments or results", 0, 0},
		{"one argument and result", 1, 1},
		{"discard arguments", 3, 0},
		{"larger result window", 1, 5},
	} {
		t.Run(shape.name, func(t *testing.T) {
			_, thread, base, callee := stageFixedNativeEntryTest(t)
			beforeValues := slices.Clone(thread.values)
			beforeFrames := slices.Clone(thread.frames)
			beforeTop, beforeExtent := thread.top, thread.frameExtent
			if failure := thread.pushFunctionCall(callee, base+1, shape.args, shape.results); failure != nil {
				t.Fatal(failure)
			}
			want := &threadObject{
				values:      slices.Clone(thread.values),
				frames:      slices.Clone(thread.frames),
				top:         thread.top,
				frameExtent: thread.frameExtent,
			}
			thread.frames = thread.frames[:len(beforeFrames)]
			copy(thread.frames, beforeFrames)
			copy(thread.values, beforeValues)
			thread.top, thread.frameExtent = beforeTop, beforeExtent
			if !thread.tryEnterFixedNativeCall(base, makeABC(opCall, 1, shape.args+1, shape.results+1)) {
				t.Fatal("fixed native entry missed a reserved call")
			}
			assertTestThreadStateEqual(t, thread, want)
		})
	}
}

func TestFixedNativeEntryMissIsAtomic(t *testing.T) {
	for _, name := range []string{"open arguments", "open results", "Lua callee", "frame capacity", "frame limit", "value extent", "value limit"} {
		t.Run(name, func(t *testing.T) {
			state, thread, base, _ := stageFixedNativeEntryTest(t)
			code := makeABC(opCall, 1, 2, 2)
			switch name {
			case "open arguments":
				code = makeABC(opCall, 1, 0, 2)
			case "open results":
				code = makeABC(opCall, 1, 2, 0)
			case "Lua callee":
				thread.values[base+1] = slotFromFunctionObject(thread.frames[0].function)
			case "frame capacity":
				thread.frames = slices.Clip(thread.frames)
			case "frame limit":
				state.limits.frames = len(thread.frames)
			case "value extent":
				thread.values = thread.values[:base+2]
			case "value limit":
				state.limits.values = base + 2
			}
			want := &threadObject{
				values:      slices.Clone(thread.values),
				frames:      slices.Clone(thread.frames),
				top:         thread.top,
				frameExtent: thread.frameExtent,
			}
			valueData, frameData := &thread.values[0], &thread.frames[0]
			valueCap, frameCap := cap(thread.values), cap(thread.frames)
			if thread.tryEnterFixedNativeCall(base, code) {
				t.Fatal("fixed native entry unexpectedly succeeded")
			}
			assertTestThreadStateEqual(t, thread, want)
			if &thread.values[0] != valueData || &thread.frames[0] != frameData ||
				cap(thread.values) != valueCap || cap(thread.frames) != frameCap {
				t.Fatal("failed native entry replaced execution storage")
			}
			// Restore the synthetic limit/extent mutations before State.Close.
			state.limits.values = state.options.MaxValues
			state.limits.frames = state.options.MaxFrames
			thread.values = thread.values[:cap(thread.values)]
		})
	}
}
