package lua

import (
	"runtime"
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
	dirty, err := state.NewTableWithCapacity(0, 0)
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
	dirty, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	firstReference, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	secondReference, err := state.NewTableWithCapacity(0, 0)
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
