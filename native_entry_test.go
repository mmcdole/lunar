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

func TestFixedNativeEntryAfterOpenResultsPreservesCallerRoots(t *testing.T) {
	const resultCount = 64
	var values [resultCount]Value
	for index := range values {
		values[index] = Number(float64(index + 1))
	}
	for _, consumer := range []string{"setlist", "open arguments"} {
		for _, producer := range []string{"return", "resume"} {
			t.Run(consumer+"/"+producer, func(t *testing.T) {
				state, err := New(Options{})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = state.Close() })
				install := func(name string, callback NativeFunc) {
					t.Helper()
					function, err := state.NewNativeFunction(callback)
					if err != nil {
						t.Fatal(err)
					}
					if err := state.RawSetGlobal(name, function.Value()); err != nil {
						t.Fatal(err)
					}
				}
				install("produce", func(frame Frame) Outcome {
					if producer == "resume" {
						return frame.Yield()
					}
					return frame.ReturnValues(values[:]...)
				})
				install("consume", func(frame Frame) Outcome {
					if frame.ArgumentCount() != resultCount {
						t.Fatalf("open argument count = %d; want %d", frame.ArgumentCount(), resultCount)
					}
					last, ok := frame.Number(resultCount - 1)
					if !ok || last != resultCount {
						t.Fatalf("last open argument = (%v, %v)", last, ok)
					}
					return frame.ReturnValues(Number(resultCount), Number(last))
				})
				collections := 0
				install("collect_now", func(frame Frame) Outcome {
					thread := frame.thread
					if thread.top >= thread.frameExtent {
						t.Fatal("fixture did not retain caller locals above the native argument window")
					}
					for _, value := range thread.values[thread.liveValueExtent():] {
						if value != (slot{}) {
							t.Fatal("consumed open values remain beyond the live frame extent")
						}
					}
					state.collectUnreachable()
					collections++
					return frame.Return()
				})

				// Keep the retained table in R3 above the fixed callback at R0.
				// Open results beginning at R5 exceed this eight-register frame.
				builder := testPrototypeBuilder(
					makeABx(opGetGlobal, 0, 0),
					makeABC(opNewTable, 3, 0, 0),
					makeABC(opSetTable, 3, registerOrConstant(1, true), registerOrConstant(2, true)),
				)
				builder.registers = 8
				builder.constants = []slot{
					slotFromValue(stateNeutralString("collect_now")),
					slotFromValue(stateNeutralString("answer")),
					numberSlot(42),
					slotFromValue(stateNeutralString("produce")),
					numberSlot(resultCount),
					slotFromValue(stateNeutralString("consume")),
				}
				if consumer == "setlist" {
					builder.code = append(builder.code, makeABC(opNewTable, 4, 0, 0))
				} else {
					builder.code = append(builder.code, makeABx(opGetGlobal, 4, 5))
				}
				builder.code = append(builder.code,
					makeABx(opGetGlobal, 5, 3),
					makeABC(opCall, 5, 1, 0),
				)
				if consumer == "setlist" {
					builder.code = append(builder.code,
						makeABC(opSetList, 4, 0, 1),
						makeABC(opCall, 0, 1, 1),
						makeABC(opLength, 1, 4, 0),
						makeABC(opGetTable, 2, 4, registerOrConstant(4, true)),
					)
				} else {
					builder.code = append(builder.code,
						makeABC(opCall, 4, 0, 3),
						makeABC(opCall, 0, 1, 1),
						makeABC(opMove, 1, 4, 0),
						makeABC(opMove, 2, 5, 0),
					)
				}
				builder.code = append(builder.code,
					makeABC(opGetTable, 0, 3, registerOrConstant(1, true)),
					makeABC(opReturn, 0, 4, 0),
				)
				prototype, failure := builder.seal()
				if failure != nil {
					t.Fatal(failure)
				}
				entry := newLuaFunction(state, prototype, state.main.globals, nil).owningHandle()
				var results []Value
				if producer == "resume" {
					thread, err := state.NewThread(entry.Value())
					if err != nil {
						t.Fatal(err)
					}
					var status ThreadStatus
					results, status, err = thread.Resume()
					if err != nil || status != ThreadSuspended || len(results) != 0 {
						t.Fatalf("initial resume = (%v, %v, %v)", results, status, err)
					}
					results, status, err = thread.Resume(values[:]...)
					if err != nil || status != ThreadDead {
						t.Fatalf("resume with open results = (%v, %v)", status, err)
					}
				} else {
					results, err = state.Call(entry.Value())
					if err != nil {
						t.Fatal(err)
					}
				}
				assertTestValues(t, results, Number(42), Number(resultCount), Number(resultCount))
				if collections != 1 {
					t.Fatalf("collection callback ran %d times; want 1", collections)
				}
			})
		}
	}
}
