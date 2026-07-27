package lua

import (
	"fmt"
	"reflect"
	"testing"
)

func TestUserDataFinalizersRunInReverseCreationOrder(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()

	var order []int
	handler := newFinalizerFunction(state, func(frame Frame) Outcome {
		data, present := frame.userDataObject(0)
		if !present || frame.ArgumentCount() != 1 {
			t.Fatal("finalizer did not receive exactly one userdata")
		}
		order = append(order, data.payload.(int))
		return frame.Return()
	})
	metatable := newFinalizerMetatable(
		t,
		state,
		slotFromFunctionObject(handler),
	)
	var data [3]*userDataObject
	for index := range data {
		data[index] = newFinalizerUserData(
			state,
			metatable,
			index+1,
		)
	}

	result, failure := state.collectAndFinalize()
	if failure != nil {
		t.Fatal(failure)
	}
	if result.userData != 0 {
		t.Fatalf("finalizer cycle swept %d userdata; want 0", result.userData)
	}
	if !reflect.DeepEqual(order, []int{3, 2, 1}) {
		t.Fatalf("finalizer order = %v; want [3 2 1]", order)
	}
	if entries, keys, _ := hostDirectoryCounts(
		&state.runtime.hosts,
	); entries != 0 || keys != 0 {
		t.Fatalf(
			"compact finalization published host handles: entries=%d keys=%d",
			entries,
			keys,
		)
	}
	for index, current := range data {
		if current.owner != state.runtime ||
			current.flags&userDataFinalized == 0 {
			t.Fatalf("userdata %d was not retained and finalized", index+1)
		}
	}

	result, failure = state.collectAndFinalize()
	if failure != nil {
		t.Fatal(failure)
	}
	if result.userData != len(data) {
		t.Fatalf(
			"post-finalizer sweep = %d userdata; want %d",
			result.userData,
			len(data),
		)
	}
	if !reflect.DeepEqual(order, []int{3, 2, 1}) {
		t.Fatalf("finalizer ran more than once: %v", order)
	}
}

func TestUserDataFinalizerEligibilityIsRawAndCurrent(t *testing.T) {
	t.Run("inherited handler does not qualify", func(t *testing.T) {
		state := newCollectorTestState(t)
		defer state.Close()

		called := false
		handler := newFinalizerFunction(state, func(frame Frame) Outcome {
			called = true
			return frame.Return()
		})
		provider := newFinalizerMetatable(
			t,
			state,
			slotFromFunctionObject(handler),
		)
		metatable := newTable(state, 0, 0)
		metatable.metatable = provider
		data := newFinalizerUserData(state, metatable, nil)

		result, failure := state.collectAndFinalize()
		if failure != nil {
			t.Fatal(failure)
		}
		if called {
			t.Fatal("inherited __gc handler ran")
		}
		if result.userData != 1 || data.owner != nil {
			t.Fatalf(
				"non-finalizable userdata sweep = %+v, owner %p",
				result,
				data.owner,
			)
		}
	})

	t.Run("reachable userdata may gain a handler", func(t *testing.T) {
		state := newCollectorTestState(t)
		defer state.Close()

		calls := 0
		data := newFinalizerUserData(
			state,
			newTable(state, 0, 1),
			nil,
		)
		mustRootCollectorObject(
			t,
			state,
			"late-finalizer",
			slotFromUserDataObject(data),
		)
		state.collectUnreachable()
		if data.flags&userDataFinalized != 0 {
			t.Fatal("reachable userdata was prematurely finalized")
		}

		handler := newFinalizerFunction(state, func(frame Frame) Outcome {
			calls++
			return frame.Return()
		})
		setFinalizerHandler(
			t,
			data.metatable,
			slotFromFunctionObject(handler),
		)
		unrootCollectorObject(t, state, "late-finalizer")
		if _, failure := state.collectAndFinalize(); failure != nil {
			t.Fatal(failure)
		}
		if calls != 1 {
			t.Fatalf("late-installed finalizer calls = %d; want 1", calls)
		}
	})

	t.Run("tables never finalize", func(t *testing.T) {
		state := newCollectorTestState(t)
		defer state.Close()

		called := false
		handler := newFinalizerFunction(state, func(frame Frame) Outcome {
			called = true
			return frame.Return()
		})
		table := newTable(state, 0, 0)
		table.metatable = newFinalizerMetatable(
			t,
			state,
			slotFromFunctionObject(handler),
		)

		result, failure := state.collectAndFinalize()
		if failure != nil {
			t.Fatal(failure)
		}
		if called {
			t.Fatal("table __gc handler ran")
		}
		if result.tables < 2 || table.owner != nil {
			t.Fatalf(
				"table with __gc was not swept normally: %+v",
				result,
			)
		}
	})
}

func TestUserDataFinalizerAcceptsAnyCallableValue(t *testing.T) {
	t.Run("callable table", func(t *testing.T) {
		state := newCollectorTestState(t)
		defer state.Close()

		calls := 0
		var finalized *userDataObject
		call := newFinalizerFunction(state, func(frame Frame) Outcome {
			if frame.ArgumentCount() != 2 {
				t.Fatalf(
					"callable __gc argument count = %d; want 2",
					frame.ArgumentCount(),
				)
			}
			if _, present := frame.tableObject(0); !present {
				t.Fatal("callable __gc omitted its receiver")
			}
			var present bool
			finalized, present = frame.userDataObject(1)
			if !present {
				t.Fatal("callable __gc omitted the userdata")
			}
			calls++
			return frame.ReturnNumber(99)
		})
		callable := newTable(state, 0, 0)
		callable.metatable = newTable(state, 0, 1)
		if err := callable.metatable.rawSetStringSlot(
			metamethodNames[metaCall],
			slotFromFunctionObject(call),
		); err != nil {
			t.Fatal(err)
		}
		data := newFinalizerUserData(
			state,
			newFinalizerMetatable(
				t,
				state,
				slotFromTableObject(callable),
			),
			nil,
		)

		if _, failure := state.collectAndFinalize(); failure != nil {
			t.Fatal(failure)
		}
		if calls != 1 || finalized != data {
			t.Fatalf(
				"callable __gc = calls %d, data %p; want 1, %p",
				calls,
				finalized,
				data,
			)
		}
	})

	t.Run("non-callable value fails once", func(t *testing.T) {
		state := newCollectorTestState(t)
		defer state.Close()

		data := newFinalizerUserData(
			state,
			newFinalizerMetatable(t, state, numberSlot(7)),
			nil,
		)
		_, failure := state.collectAndFinalize()
		if failure == nil ||
			failure.Error() != "attempt to call a number value" {
			t.Fatalf("numeric __gc failure = %v", failure)
		}
		if data.flags&userDataFinalized == 0 ||
			pendingFinalizerCount(state) != 0 {
			t.Fatal("non-callable finalizer was not consumed")
		}
		if _, failure = state.collectAndFinalize(); failure != nil {
			t.Fatalf("non-callable finalizer retried: %v", failure)
		}
		if data.owner != nil {
			t.Fatal("consumed non-callable userdata was not swept")
		}
	})
}

func TestPendingFinalizersReadTheCurrentHandler(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()

	var order []string
	metatable := newTable(state, 0, 1)
	replacement := newFinalizerFunction(state, func(frame Frame) Outcome {
		data, _ := frame.userDataObject(0)
		order = append(order, fmt.Sprintf("B%d", data.payload.(int)))
		return frame.Return()
	})
	mustRootCollectorObject(
		t,
		state,
		"replacement-finalizer",
		slotFromFunctionObject(replacement),
	)
	first := newFinalizerFunction(state, func(frame Frame) Outcome {
		data, _ := frame.userDataObject(0)
		order = append(order, fmt.Sprintf("A%d", data.payload.(int)))
		if data.payload.(int) == 2 {
			setFinalizerHandler(
				t,
				metatable,
				slotFromFunctionObject(replacement),
			)
			unrootCollectorObject(t, state, "replacement-finalizer")
		}
		return frame.Return()
	})
	setFinalizerHandler(
		t,
		metatable,
		slotFromFunctionObject(first),
	)
	newFinalizerUserData(state, metatable, 1)
	newFinalizerUserData(state, metatable, 2)

	if _, failure := state.collectAndFinalize(); failure != nil {
		t.Fatal(failure)
	}
	if !reflect.DeepEqual(order, []string{"A2", "B1"}) {
		t.Fatalf("dynamic finalizer order = %v; want [A2 B1]", order)
	}

	state = newCollectorTestState(t)
	defer state.Close()
	order = nil
	metatable = newTable(state, 0, 1)
	removing := newFinalizerFunction(state, func(frame Frame) Outcome {
		data, _ := frame.userDataObject(0)
		order = append(order, fmt.Sprintf("%d", data.payload.(int)))
		setFinalizerHandler(t, metatable, nilSlot)
		return frame.Return()
	})
	setFinalizerHandler(
		t,
		metatable,
		slotFromFunctionObject(removing),
	)
	newFinalizerUserData(state, metatable, 1)
	newFinalizerUserData(state, metatable, 2)
	if _, failure := state.collectAndFinalize(); failure != nil {
		t.Fatal(failure)
	}
	if !reflect.DeepEqual(order, []string{"2"}) {
		t.Fatalf("removed finalizer calls = %v; want [2]", order)
	}
}

func TestWeakFinalizerHandlerMayDisappearBeforeInvocation(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()

	calls := 0
	handler := newFinalizerFunction(state, func(frame Frame) Outcome {
		calls++
		return frame.Return()
	})
	metatable := newFinalizerMetatable(
		t,
		state,
		slotFromFunctionObject(handler),
	)
	weakModeMetatable := newTable(state, 0, 1)
	if err := weakModeMetatable.rawSetStringSlot(
		metamethodNames[metaMode],
		stringSlot(state.runtime.strings.make("v")),
	); err != nil {
		t.Fatal(err)
	}
	metatable.metatable = weakModeMetatable
	data := newFinalizerUserData(state, metatable, nil)

	result, failure := state.collectAndFinalize()
	if failure != nil {
		t.Fatal(failure)
	}
	if calls != 0 {
		t.Fatal("weak __gc value survived until invocation")
	}
	if _, found := metatable.rawStringSlot(metamethodNames[metaGC]); found {
		t.Fatal("weak clearing retained the dead __gc handler")
	}
	if result.functions == 0 ||
		data.flags&userDataFinalized == 0 ||
		data.owner != state.runtime {
		t.Fatal("weak handler removal did not preserve finalized userdata")
	}
}

func TestFinalizerErrorConsumesCurrentAndPreservesQueue(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()

	var order []string
	good := newFinalizerFunction(state, func(frame Frame) Outcome {
		order = append(order, "old")
		return frame.Return()
	})
	bad := newFinalizerFunction(state, func(frame Frame) Outcome {
		order = append(order, "bad")
		return frame.RaiseString("boom")
	})
	newFinalizerUserData(
		state,
		newFinalizerMetatable(t, state, slotFromFunctionObject(good)),
		nil,
	)
	badData := newFinalizerUserData(
		state,
		newFinalizerMetatable(t, state, slotFromFunctionObject(bad)),
		nil,
	)

	_, failure := state.collectAndFinalize()
	if failure == nil || failure.Error() != "boom" {
		t.Fatalf("finalizer failure = %v; want boom", failure)
	}
	if !reflect.DeepEqual(order, []string{"bad"}) {
		t.Fatalf("calls after first collection = %v; want [bad]", order)
	}
	if pendingFinalizerCount(state) != 1 ||
		badData.flags&userDataFinalized == 0 {
		t.Fatal("finalizer error did not consume current and retain later work")
	}

	if _, failure = state.collectAndFinalize(); failure != nil {
		t.Fatal(failure)
	}
	if !reflect.DeepEqual(order, []string{"bad", "old"}) {
		t.Fatalf("calls after retry = %v; want [bad old]", order)
	}
	if pendingFinalizerCount(state) != 0 {
		t.Fatal("successful retry retained finalizer work")
	}
}

func TestFinalizerPanicRestoresStateAndPreservesQueue(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()

	var order []string
	old := newFinalizerFunction(state, func(frame Frame) Outcome {
		order = append(order, "old")
		return frame.Return()
	})
	bad := newFinalizerFunction(state, func(frame Frame) Outcome {
		order = append(order, "bad")
		panic("boom")
	})
	newFinalizerUserData(
		state,
		newFinalizerMetatable(t, state, slotFromFunctionObject(old)),
		nil,
	)
	newFinalizerUserData(
		state,
		newFinalizerMetatable(t, state, slotFromFunctionObject(bad)),
		nil,
	)

	var recovered any
	func() {
		defer func() {
			recovered = recover()
		}()
		_, _ = state.collectAndFinalize()
	}()
	if recovered != "boom" {
		t.Fatalf("finalizer panic = %v; want boom", recovered)
	}
	if state.active != nil ||
		state.main.status != ThreadReady ||
		pendingFinalizerCount(state) != 1 {
		t.Fatal("finalizer panic did not restore execution and queue state")
	}
	if !reflect.DeepEqual(order, []string{"bad"}) {
		t.Fatalf("panic finalizer order = %v; want [bad]", order)
	}
	if _, failure := state.collectAndFinalize(); failure != nil {
		t.Fatal(failure)
	}
	if !reflect.DeepEqual(order, []string{"bad", "old"}) {
		t.Fatalf("post-panic finalizer order = %v; want [bad old]", order)
	}
}

func TestFinalizerPreservesAnArbitraryLuaErrorValue(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()

	marker := newTable(state, 0, 0)
	markerSlot := slotFromTableObject(marker)
	mustRootCollectorObject(t, state, "error-marker", markerSlot)
	handler := newFinalizerFunction(state, func(frame Frame) Outcome {
		return frame.raiseCompact(markerSlot)
	})
	newFinalizerUserData(
		state,
		newFinalizerMetatable(
			t,
			state,
			slotFromFunctionObject(handler),
		),
		nil,
	)

	_, failure := state.collectAndFinalize()
	if failure == nil {
		t.Fatal("arbitrary finalizer error was lost")
	}
	value, valid := failure.valueSlot()
	if !valid || !rawSlotEqual(value, markerSlot) {
		t.Fatal("finalizer changed the arbitrary Lua error identity")
	}
}

func TestPublicCollectionSurfacesOwnFinalizerErrors(t *testing.T) {
	t.Run("idle State", func(t *testing.T) {
		state := newCollectorTestState(t)
		defer state.Close()

		marker := newTable(state, 0, 0)
		handler := newNativeFunctionOwned(
			state,
			state.main.globals,
			func(frame Frame) Outcome {
				return frame.raiseCompact(frame.nativeCapture(0))
			},
			[]slot{slotFromTableObject(marker)},
		)
		newFinalizerUserData(
			state,
			newFinalizerMetatable(
				t,
				state,
				slotFromFunctionObject(handler),
			),
			nil,
		)

		err := state.Collect()
		failure, ok := err.(*Error)
		if !ok {
			t.Fatalf("State.Collect error = %T %v; want *Error", err, err)
		}
		if !failure.value.Valid() || failure.hasCompactValue {
			t.Fatal("State.Collect returned an unowned compact error value")
		}
		if got := tableObjectFromSlot(slotFromValue(failure.Value())); got != marker {
			t.Fatal("State.Collect changed finalizer error identity")
		}
		if err := state.Collect(); err != nil {
			t.Fatal(err)
		}
		if got := tableObjectFromSlot(slotFromValue(failure.Value())); got != marker ||
			marker.owner != state.runtime {
			t.Fatal("later collection invalidated the exposed error value")
		}
	})

	t.Run("live Frame", func(t *testing.T) {
		state := newCollectorTestState(t)
		defer state.Close()

		marker := newTable(state, 0, 0)
		handler := newNativeFunctionOwned(
			state,
			state.main.globals,
			func(frame Frame) Outcome {
				return frame.raiseCompact(frame.nativeCapture(0))
			},
			[]slot{slotFromTableObject(marker)},
		)
		newFinalizerUserData(
			state,
			newFinalizerMetatable(
				t,
				state,
				slotFromFunctionObject(handler),
			),
			nil,
		)

		var collectionError error
		collector, err := state.NewNativeFunction(func(frame Frame) Outcome {
			collectionError = frame.Collect()
			return frame.Return()
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := state.Call(collector.Value()); err != nil {
			t.Fatal(err)
		}
		failure, ok := collectionError.(*Error)
		if !ok {
			t.Fatalf(
				"Frame.Collect error = %T %v; want *Error",
				collectionError,
				collectionError,
			)
		}
		if !failure.value.Valid() || failure.hasCompactValue {
			t.Fatal("Frame.Collect returned an unowned compact error value")
		}
		if err := state.Collect(); err != nil {
			t.Fatal(err)
		}
		if got := tableObjectFromSlot(slotFromValue(failure.Value())); got != marker ||
			marker.owner != state.runtime {
			t.Fatal("Frame.Collect did not preserve finalizer error identity")
		}
	})
}

func TestBaseCollectionControlsFinalizerQueue(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	marker := newTable(state, 0, 0)
	var order []string
	good := newFinalizerFunction(state, func(frame Frame) Outcome {
		order = append(order, "good")
		return frame.Return()
	})
	bad := newNativeFunctionOwned(
		state,
		state.main.globals,
		func(frame Frame) Outcome {
			order = append(order, "bad")
			return frame.raiseCompact(frame.nativeCapture(0))
		},
		[]slot{slotFromTableObject(marker)},
	)
	newFinalizerUserData(
		state,
		newFinalizerMetatable(t, state, slotFromFunctionObject(good)),
		nil,
	)
	newFinalizerUserData(
		state,
		newFinalizerMetatable(t, state, slotFromFunctionObject(bad)),
		nil,
	)

	collector, err := state.Global("collectgarbage")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Call(collector, state.String("count")); err != nil {
		t.Fatal(err)
	}
	if len(order) != 0 {
		t.Fatalf("count invoked finalizers: %v", order)
	}

	_, err = state.Call(collector, state.String("collect"))
	failure, ok := err.(*Error)
	if !ok {
		t.Fatalf("collectgarbage error = %T %v; want *Error", err, err)
	}
	if got := tableObjectFromSlot(slotFromValue(failure.Value())); got != marker {
		t.Fatal("collectgarbage changed the arbitrary finalizer error")
	}
	if !reflect.DeepEqual(order, []string{"bad"}) {
		t.Fatalf("first collection order = %v; want [bad]", order)
	}

	results, err := state.Call(collector, state.String("collect"))
	if err != nil {
		t.Fatal(err)
	}
	if number, ok := results[0].AsNumber(); !ok || number != 0 {
		t.Fatalf("resumed collection result = %v; want 0", results)
	}
	if !reflect.DeepEqual(order, []string{"bad", "good"}) {
		t.Fatalf(
			"resumed collection order = %v; want [bad good]",
			order,
		)
	}
}

func TestSuccessfulFinalizerCannotStopOuterCollection(t *testing.T) {
	tests := []struct {
		name   string
		invoke func(*testing.T, *State) error
	}{
		{
			name: "State Collect",
			invoke: func(t *testing.T, state *State) error {
				return state.Collect()
			},
		},
		{
			name: "Frame Collect",
			invoke: func(t *testing.T, state *State) error {
				entry, err := state.NewNativeFunction(func(frame Frame) Outcome {
					if err := frame.Collect(); err != nil {
						t.Fatal(err)
					}
					return frame.Return()
				})
				if err != nil {
					return err
				}
				_, err = state.Call(entry.Value())
				return err
			},
		},
		{
			name: "base collect",
			invoke: func(t *testing.T, state *State) error {
				if err := state.OpenBase(); err != nil {
					return err
				}
				collector, err := state.Global("collectgarbage")
				if err != nil {
					return err
				}
				_, err = state.Call(
					collector,
					state.String("collect"),
				)
				return err
			},
		},
		{
			name: "base step",
			invoke: func(t *testing.T, state *State) error {
				if err := state.OpenBase(); err != nil {
					return err
				}
				collector, err := state.Global("collectgarbage")
				if err != nil {
					return err
				}
				_, err = state.Call(
					collector,
					state.String("step"),
					Number(1),
				)
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := newCollectorTestState(t)
			defer state.Close()

			handler := newFinalizerFunction(state, func(frame Frame) Outcome {
				frame.thread.owner.collection.stopped = true
				return frame.Return()
			})
			newFinalizerUserData(
				state,
				newFinalizerMetatable(
					t,
					state,
					slotFromFunctionObject(handler),
				),
				nil,
			)

			if err := test.invoke(t, state); err != nil {
				t.Fatal(err)
			}
			if state.runtime.collection.stopped {
				t.Fatal("successful finalizer stopped the outer collection")
			}
		})
	}
}

func TestFailingFinalizerMayLeaveCollectionStopped(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()

	handler := newFinalizerFunction(state, func(frame Frame) Outcome {
		frame.thread.owner.collection.stopped = true
		return frame.RaiseString("stopped")
	})
	newFinalizerUserData(
		state,
		newFinalizerMetatable(
			t,
			state,
			slotFromFunctionObject(handler),
		),
		nil,
	)

	if err := state.Collect(); err == nil || err.Error() != "stopped" {
		t.Fatalf("State.Collect error = %v; want stopped", err)
	}
	if !state.runtime.collection.stopped {
		t.Fatal("failed finalizer did not preserve its stopped policy")
	}
}

func TestPendingFinalizerGraphDoesNotDelayNewEligibility(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()

	var order []string
	holder := newTable(state, 0, 1)
	oldHandler := newFinalizerFunction(state, func(frame Frame) Outcome {
		order = append(order, "old")
		return frame.Return()
	})
	old := newFinalizerUserData(
		state,
		newFinalizerMetatable(
			t,
			state,
			slotFromFunctionObject(oldHandler),
		),
		nil,
	)
	old.environment = holder

	bad := newFinalizerFunction(state, func(frame Frame) Outcome {
		order = append(order, "bad")
		newHandler := newFinalizerFunction(
			state,
			func(frame Frame) Outcome {
				order = append(order, "new")
				return frame.Return()
			},
		)
		child := newFinalizerUserData(
			state,
			newFinalizerMetatable(
				t,
				state,
				slotFromFunctionObject(newHandler),
			),
			nil,
		)
		if err := holder.rawSetStringSlot(
			"child",
			slotFromUserDataObject(child),
		); err != nil {
			t.Fatal(err)
		}
		return frame.RaiseString("stop")
	})
	newFinalizerUserData(
		state,
		newFinalizerMetatable(t, state, slotFromFunctionObject(bad)),
		nil,
	)

	if _, failure := state.collectAndFinalize(); failure == nil {
		t.Fatal("first collection did not report the finalizer error")
	}
	if !reflect.DeepEqual(order, []string{"bad"}) {
		t.Fatalf("first collection order = %v; want [bad]", order)
	}
	if _, failure := state.collectAndFinalize(); failure != nil {
		t.Fatal(failure)
	}
	if !reflect.DeepEqual(order, []string{"bad", "old", "new"}) {
		t.Fatalf(
			"pending-graph finalizer order = %v; want [bad old new]",
			order,
		)
	}
}

func TestNoHandlerUserDataReachedOnlyFromPendingWorkFinalizesOnce(
	t *testing.T,
) {
	state := newCollectorTestState(t)
	defer state.Close()

	holder := newTable(state, 0, 1)
	childCalls := 0
	var child *userDataObject
	oldHandler := newFinalizerFunction(state, func(frame Frame) Outcome {
		childHandler := newFinalizerFunction(
			state,
			func(frame Frame) Outcome {
				childCalls++
				return frame.Return()
			},
		)
		setFinalizerHandler(
			t,
			child.metatable,
			slotFromFunctionObject(childHandler),
		)
		return frame.Return()
	})
	old := newFinalizerUserData(
		state,
		newFinalizerMetatable(
			t,
			state,
			slotFromFunctionObject(oldHandler),
		),
		nil,
	)
	old.environment = holder

	bad := newFinalizerFunction(state, func(frame Frame) Outcome {
		child = newFinalizerUserData(
			state,
			newTable(state, 0, 1),
			nil,
		)
		if err := holder.rawSetStringSlot(
			"child",
			slotFromUserDataObject(child),
		); err != nil {
			t.Fatal(err)
		}
		return frame.RaiseString("stop")
	})
	newFinalizerUserData(
		state,
		newFinalizerMetatable(t, state, slotFromFunctionObject(bad)),
		nil,
	)

	if _, failure := state.collectAndFinalize(); failure == nil {
		t.Fatal("initial finalizer did not fail")
	}
	if _, failure := state.collectAndFinalize(); failure != nil {
		t.Fatal(failure)
	}
	if child == nil ||
		child.owner != state.runtime ||
		child.flags&userDataFinalized == 0 {
		t.Fatal("pending graph did not preserve finalized no-handler userdata")
	}
	if _, failure := state.collectAndFinalize(); failure != nil {
		t.Fatal(failure)
	}
	if childCalls != 0 {
		t.Fatal("handler installed after finalization ran")
	}
	if child.owner != nil {
		t.Fatal("finalized child without a queued handler survived")
	}
}

func TestFinalizerResurrectionAndWeakTableOrdering(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()

	weakKeysTable, _ := newWeakTableForTest(t, state, "k", 0, 1)
	weakValuesTable, _ := newWeakTableForTest(t, state, "v", 0, 1)
	weakBothTable, _ := newWeakTableForTest(t, state, "kv", 0, 1)
	mustRootCollectorObject(
		t,
		state,
		"weak-keys-finalizer",
		slotFromTableObject(weakKeysTable),
	)
	mustRootCollectorObject(
		t,
		state,
		"weak-values-finalizer",
		slotFromTableObject(weakValuesTable),
	)
	mustRootCollectorObject(
		t,
		state,
		"weak-both-finalizer",
		slotFromTableObject(weakBothTable),
	)

	calls := 0
	var observed [3]bool
	handler := newFinalizerFunction(state, func(frame Frame) Outcome {
		calls++
		data, _ := frame.userDataObject(0)
		key := slotFromUserDataObject(data)
		if value, found := weakKeysTable.rawSlot(key); found &&
			value.isString() &&
			stringSlotText(value) == "K" {
			observed[0] = true
		}
		_, observed[1] = weakValuesTable.rawStringSlot("value")
		_, observed[2] = weakBothTable.rawSlot(key)
		if err := state.registry.rawSetStringSlot(
			"resurrected",
			key,
		); err != nil {
			t.Fatal(err)
		}
		return frame.Return()
	})
	data := newFinalizerUserData(
		state,
		newFinalizerMetatable(
			t,
			state,
			slotFromFunctionObject(handler),
		),
		nil,
	)
	dataSlot := slotFromUserDataObject(data)
	setWeakRecordForTest(
		t,
		weakKeysTable,
		dataSlot,
		slotFromValue(state.String("K")),
	)
	if err := weakValuesTable.rawSetStringSlot(
		"value",
		dataSlot,
	); err != nil {
		t.Fatal(err)
	}
	setWeakRecordForTest(t, weakBothTable, dataSlot, dataSlot)

	if _, failure := state.collectAndFinalize(); failure != nil {
		t.Fatal(failure)
	}
	if calls != 1 || observed != [3]bool{true, false, false} {
		t.Fatalf(
			"finalizer weak observations = calls %d, %v; want 1, [true false false]",
			calls,
			observed,
		)
	}
	if data.owner != state.runtime {
		t.Fatal("resurrected userdata was swept")
	}

	if err := weakValuesTable.rawSetStringSlot(
		"value",
		dataSlot,
	); err != nil {
		t.Fatal(err)
	}
	if _, failure := state.collectAndFinalize(); failure != nil {
		t.Fatal(failure)
	}
	if _, found := weakValuesTable.rawStringSlot("value"); found {
		t.Fatal("rooted finalized userdata survived as a weak value")
	}
	if _, found := weakKeysTable.rawSlot(dataSlot); !found {
		t.Fatal("rooted finalized userdata disappeared as a weak key")
	}

	unrootCollectorObject(t, state, "resurrected")
	if _, failure := state.collectAndFinalize(); failure != nil {
		t.Fatal(failure)
	}
	if _, found := weakKeysTable.rawSlot(dataSlot); found {
		t.Fatal("dead resurrected userdata remained as a weak key")
	}
	if calls != 1 {
		t.Fatalf("resurrected userdata finalized %d times; want 1", calls)
	}
}

func TestFinalizersPermitNestedCollectionAndDeferNewUserData(t *testing.T) {
	t.Run("nested collection drains older work", func(t *testing.T) {
		state := newCollectorTestState(t)
		defer state.Close()

		var order []string
		old := newFinalizerFunction(state, func(frame Frame) Outcome {
			order = append(order, "old")
			return frame.Return()
		})
		nested := newFinalizerFunction(state, func(frame Frame) Outcome {
			order = append(order, "new-enter")
			if _, failure := frame.collectAndFinalize(); failure != nil {
				return frame.RaiseError(failure)
			}
			order = append(order, "new-exit")
			return frame.Return()
		})
		newFinalizerUserData(
			state,
			newFinalizerMetatable(t, state, slotFromFunctionObject(old)),
			nil,
		)
		newFinalizerUserData(
			state,
			newFinalizerMetatable(t, state, slotFromFunctionObject(nested)),
			nil,
		)

		if _, failure := state.collectAndFinalize(); failure != nil {
			t.Fatal(failure)
		}
		want := []string{"new-enter", "old", "new-exit"}
		if !reflect.DeepEqual(order, want) {
			t.Fatalf("nested finalizer order = %v; want %v", order, want)
		}
	})

	t.Run("new finalizer waits for the next cycle", func(t *testing.T) {
		state := newCollectorTestState(t)
		defer state.Close()

		var order []string
		parent := newFinalizerFunction(state, func(frame Frame) Outcome {
			order = append(order, "parent")
			child := newFinalizerFunction(
				state,
				func(frame Frame) Outcome {
					order = append(order, "child")
					return frame.Return()
				},
			)
			newFinalizerUserData(
				state,
				newFinalizerMetatable(
					t,
					state,
					slotFromFunctionObject(child),
				),
				nil,
			)
			return frame.Return()
		})
		newFinalizerUserData(
			state,
			newFinalizerMetatable(t, state, slotFromFunctionObject(parent)),
			nil,
		)

		if _, failure := state.collectAndFinalize(); failure != nil {
			t.Fatal(failure)
		}
		if !reflect.DeepEqual(order, []string{"parent"}) {
			t.Fatalf("first-cycle finalizers = %v; want [parent]", order)
		}
		if _, failure := state.collectAndFinalize(); failure != nil {
			t.Fatal(failure)
		}
		if !reflect.DeepEqual(order, []string{"parent", "child"}) {
			t.Fatalf(
				"second-cycle finalizers = %v; want [parent child]",
				order,
			)
		}
	})
}

func TestFinalizerCannotYieldAcrossTheCollectingFrame(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()

	handler := newFinalizerFunction(state, func(frame Frame) Outcome {
		return frame.Yield()
	})
	newFinalizerUserData(
		state,
		newFinalizerMetatable(
			t,
			state,
			slotFromFunctionObject(handler),
		),
		nil,
	)
	collector := newFinalizerFunction(state, func(frame Frame) Outcome {
		if _, failure := frame.collectAndFinalize(); failure != nil {
			return frame.RaiseError(failure)
		}
		return frame.Return()
	})
	thread, err := state.newThreadObject(
		slotFromFunctionObject(collector),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, status, err := thread.owningHandle().Resume()
	if status != ThreadDead ||
		err == nil ||
		err.Error() != "attempt to yield across metamethod/C-call boundary" {
		t.Fatalf(
			"finalizer yield = status %d, error %v",
			status,
			err,
		)
	}
}

func TestStateCloseDrainsLuaFinalizersBeforeTeardown(t *testing.T) {
	state := newCollectorTestState(t)
	var order []string
	handler := newFinalizerFunction(state, func(frame Frame) Outcome {
		data, _ := frame.userDataObject(0)
		if state.runtime.closed.Load() ||
			state.streams == nil ||
			state.registry == nil {
			t.Fatal("State resources were torn down before __gc")
		}
		order = append(order, data.payload.(string))
		if data.payload == "bad" {
			return frame.RaiseString("ignored")
		}
		return frame.Return()
	})
	metatable := newFinalizerMetatable(
		t,
		state,
		slotFromFunctionObject(handler),
	)
	for _, name := range []string{"old", "bad", "new"} {
		data := newFinalizerUserData(state, metatable, name)
		mustRootCollectorObject(
			t,
			state,
			"close-"+name,
			slotFromUserDataObject(data),
		)
	}

	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"new", "bad", "old"}) {
		t.Fatalf(
			"close finalizer order = %v; want [new bad old]",
			order,
		)
	}
	if !state.runtime.closed.Load() || state.registry != nil {
		t.Fatal("State did not finish teardown after Lua finalizers")
	}
}

func TestStateCloseDoesNotFinalizeCallbackCreatedUserData(t *testing.T) {
	state := newCollectorTestState(t)
	var order []string
	parent := newFinalizerFunction(state, func(frame Frame) Outcome {
		order = append(order, "parent")
		child := newFinalizerFunction(state, func(frame Frame) Outcome {
			order = append(order, "child")
			return frame.Return()
		})
		newFinalizerUserData(
			state,
			newFinalizerMetatable(
				t,
				state,
				slotFromFunctionObject(child),
			),
			nil,
		)
		return frame.Return()
	})
	newFinalizerUserData(
		state,
		newFinalizerMetatable(t, state, slotFromFunctionObject(parent)),
		nil,
	)

	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"parent"}) {
		t.Fatalf("close-time created finalizers = %v; want [parent]", order)
	}
}

func TestStateClosePreservesWeakEntriesDuringFinalizers(t *testing.T) {
	state := newCollectorTestState(t)
	weakKeysTable, _ := newWeakTableForTest(t, state, "k", 0, 1)
	weakValuesTable, _ := newWeakTableForTest(t, state, "v", 0, 1)
	weakBothTable, _ := newWeakTableForTest(t, state, "kv", 0, 1)
	observed := [3]bool{}
	handler := newFinalizerFunction(state, func(frame Frame) Outcome {
		data, _ := frame.userDataObject(0)
		key := slotFromUserDataObject(data)
		_, observed[0] = weakKeysTable.rawSlot(key)
		_, observed[1] = weakValuesTable.rawStringSlot("value")
		_, observed[2] = weakBothTable.rawSlot(key)
		return frame.Return()
	})
	data := newFinalizerUserData(
		state,
		newFinalizerMetatable(
			t,
			state,
			slotFromFunctionObject(handler),
		),
		nil,
	)
	dataSlot := slotFromUserDataObject(data)
	setWeakRecordForTest(
		t,
		weakKeysTable,
		dataSlot,
		stringSlot(state.runtime.strings.make("K")),
	)
	if err := weakValuesTable.rawSetStringSlot(
		"value",
		dataSlot,
	); err != nil {
		t.Fatal(err)
	}
	setWeakRecordForTest(
		t,
		weakBothTable,
		dataSlot,
		trueSlot,
	)
	mustRootCollectorObject(
		t,
		state,
		"close-weak-k",
		slotFromTableObject(weakKeysTable),
	)
	mustRootCollectorObject(
		t,
		state,
		"close-weak-v",
		slotFromTableObject(weakValuesTable),
	)
	mustRootCollectorObject(
		t,
		state,
		"close-weak-kv",
		slotFromTableObject(weakBothTable),
	)

	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if observed != [3]bool{true, true, true} {
		t.Fatalf(
			"close-time weak observations = %v; want [true true true]",
			observed,
		)
	}
}

func TestStateCloseRunsExistingPendingWorkBeforeReachableUserData(
	t *testing.T,
) {
	state := newCollectorTestState(t)
	var order []string
	handler := func(name string, fail bool) *functionObject {
		return newFinalizerFunction(state, func(frame Frame) Outcome {
			order = append(order, name)
			if fail {
				return frame.RaiseString("stop")
			}
			return frame.Return()
		})
	}
	newFinalizerUserData(
		state,
		newFinalizerMetatable(
			t,
			state,
			slotFromFunctionObject(handler("pending", false)),
		),
		nil,
	)
	newFinalizerUserData(
		state,
		newFinalizerMetatable(
			t,
			state,
			slotFromFunctionObject(handler("bad", true)),
		),
		nil,
	)
	if _, failure := state.collectAndFinalize(); failure == nil {
		t.Fatal("initial collection did not preserve pending work")
	}
	reachable := newFinalizerUserData(
		state,
		newFinalizerMetatable(
			t,
			state,
			slotFromFunctionObject(handler("reachable", false)),
		),
		nil,
	)
	mustRootCollectorObject(
		t,
		state,
		"reachable-at-close",
		slotFromUserDataObject(reachable),
	)

	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	want := []string{"bad", "pending", "reachable"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("pending close order = %v; want %v", order, want)
	}
}

func TestStateCloseFinishesAfterNativeFinalizerPanic(t *testing.T) {
	state := newCollectorTestState(t)
	var order []string
	old := newFinalizerFunction(state, func(frame Frame) Outcome {
		order = append(order, "old")
		return frame.Return()
	})
	bad := newFinalizerFunction(state, func(frame Frame) Outcome {
		order = append(order, "bad")
		panic("finalizer panic")
	})
	newFinalizerUserData(
		state,
		newFinalizerMetatable(t, state, slotFromFunctionObject(old)),
		nil,
	)
	newFinalizerUserData(
		state,
		newFinalizerMetatable(t, state, slotFromFunctionObject(bad)),
		nil,
	)

	var recovered any
	func() {
		defer func() {
			recovered = recover()
		}()
		_ = state.Close()
	}()
	if recovered != "finalizer panic" {
		t.Fatalf("Close panic = %v; want finalizer panic", recovered)
	}
	if !reflect.DeepEqual(order, []string{"bad", "old"}) {
		t.Fatalf("panic close order = %v; want [bad old]", order)
	}
	if !state.runtime.closed.Load() || state.registry != nil {
		t.Fatal("State remained open after a finalizer panic")
	}
}

func TestWarmCollectionWithNoFinalizerWorkDoesNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	state := newCollectorTestState(t)
	defer state.Close()

	state.collectAndFinalize()
	if allocations := testing.AllocsPerRun(1000, func() {
		if swept, failure := state.collectAndFinalize(); failure != nil ||
			swept.total() != 0 {
			panic("stable finalizer collection changed the heap")
		}
	}); allocations != 0 {
		t.Fatalf(
			"warm finalizer-aware collection allocations = %v; want 0",
			allocations,
		)
	}
}

func TestFinalizerQueueDropsOversizedBackingAfterDrain(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()

	calls := 0
	handler := newFinalizerFunction(state, func(frame Frame) Outcome {
		calls++
		return frame.Return()
	})
	metatable := newFinalizerMetatable(
		t,
		state,
		slotFromFunctionObject(handler),
	)
	const count = maximumRetainedCollectionWork + 1
	for range count {
		newFinalizerUserData(state, metatable, nil)
	}

	if _, failure := state.collectAndFinalize(); failure != nil {
		t.Fatal(failure)
	}
	if calls != count {
		t.Fatalf("oversized finalizer calls = %d; want %d", calls, count)
	}
	if state.objects.finalizers != nil ||
		state.objects.finalizerHead != 0 {
		t.Fatal("drained collector retained oversized finalizer backing")
	}
}

func TestFinalizerQueueDropsOversizedBackingAfterLastError(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()

	const count = maximumRetainedCollectionWork + 1
	calls := 0
	handler := newFinalizerFunction(state, func(frame Frame) Outcome {
		calls++
		if calls == count {
			return frame.RaiseString("last")
		}
		return frame.Return()
	})
	metatable := newFinalizerMetatable(
		t,
		state,
		slotFromFunctionObject(handler),
	)
	for range count {
		newFinalizerUserData(state, metatable, nil)
	}

	if _, failure := state.collectAndFinalize(); failure == nil ||
		failure.Error() != "last" {
		t.Fatalf("last finalizer failure = %v; want last", failure)
	}
	if state.objects.finalizers != nil ||
		state.objects.finalizerHead != 0 {
		t.Fatal("last-call failure retained oversized finalizer backing")
	}
}

func newFinalizerFunction(
	state *State,
	entry NativeFunc,
) *functionObject {
	return newNativeFunctionOwned(
		state,
		state.main.globals,
		entry,
		nil,
	)
}

func newFinalizerMetatable(
	t *testing.T,
	state *State,
	handler slot,
) *tableObject {
	t.Helper()
	metatable := newTable(state, 0, 1)
	setFinalizerHandler(t, metatable, handler)
	return metatable
}

func setFinalizerHandler(
	t *testing.T,
	metatable *tableObject,
	handler slot,
) {
	t.Helper()
	if err := metatable.rawSetStringSlot(
		metamethodNames[metaGC],
		handler,
	); err != nil {
		t.Fatal(err)
	}
}

func newFinalizerUserData(
	state *State,
	metatable *tableObject,
	payload any,
) *userDataObject {
	data := newUserDataObject(state, payload, nil, nil)
	data.metatable = metatable
	return data
}

func pendingFinalizerCount(state *State) int {
	return len(state.objects.finalizers) - state.objects.finalizerHead
}
