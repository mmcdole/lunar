package lua

import (
	"errors"
	"strings"
	"testing"
)

func TestStopGCSuspendsAutomaticCollection(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenString(); err != nil {
		t.Fatal(err)
	}

	finalized := 0
	handler := newFinalizerFunction(state, func(frame Frame) Outcome {
		finalized++
		return frame.Return()
	})
	metatable := newFinalizerMetatable(
		t,
		state,
		slotFromFunctionObject(handler),
	)
	newFinalizerUserData(state, metatable, nil)

	// Enough allocation to cross the automatic collection budget.
	churn := `
		for index = 1, 512 do
			local scratch = string.rep("x", 16 * 1024)
			local _ = #scratch
		end
	`

	if err := state.StopGC(); err != nil {
		t.Fatal(err)
	}
	if _, err := state.DoString("@churn.lua", churn); err != nil {
		t.Fatal(err)
	}
	if finalized != 0 {
		t.Fatalf("a stopped collector ran %d finalizers", finalized)
	}

	// Explicit collection still runs while automatic collection is off.
	if err := state.Collect(); err != nil {
		t.Fatal(err)
	}
	if finalized != 1 {
		t.Fatalf("explicit Collect ran %d finalizers; want 1", finalized)
	}
}

// Restarting requests a cycle, which the runtime services at the next
// execution safe point.
func TestRestartGCResumesAutomaticCollection(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	finalized := 0
	handler := newFinalizerFunction(state, func(frame Frame) Outcome {
		finalized++
		return frame.Return()
	})
	metatable := newFinalizerMetatable(
		t,
		state,
		slotFromFunctionObject(handler),
	)
	newFinalizerUserData(state, metatable, nil)

	if err := state.StopGC(); err != nil {
		t.Fatal(err)
	}
	if _, err := state.DoString("@idle.lua", "return 1"); err != nil {
		t.Fatal(err)
	}
	if finalized != 0 {
		t.Fatalf("a stopped collector ran %d finalizers", finalized)
	}

	if err := state.RestartGC(); err != nil {
		t.Fatal(err)
	}
	if _, err := state.DoString("@resumed.lua", "return 1"); err != nil {
		t.Fatal(err)
	}
	if finalized != 1 {
		t.Fatalf("after RestartGC %d finalizers ran; want 1", finalized)
	}
}

func TestStoppedHeapChecksPreserveCollectionState(t *testing.T) {
	for _, resume := range []string{"restart", "collect"} {
		t.Run(resume, func(t *testing.T) {
			state, err := New(Options{MaxHeapBytes: 4 << 20})
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			idle, err := state.LoadString("@stopped-heap.lua", "return")
			if err != nil {
				t.Fatal(err)
			}

			weak, _ := newWeakTableForTest(t, state, "v", 1, 0)
			unreachable := newTable(state, 0, 0)
			weak.rawSetIntegerSlot(1, slotFromTableObject(unreachable))
			rootWeakTableForTest(t, state, weak)
			finalized := 0
			handler := newFinalizerFunction(state, func(frame Frame) Outcome {
				finalized++
				return frame.Return()
			})
			metatable := newFinalizerMetatable(t, state, slotFromFunctionObject(handler))
			data := newFinalizerUserData(state, metatable, nil)
			if err := state.StopGC(); err != nil {
				t.Fatal(err)
			}

			// Cross the collection budget while remaining below the heap limit.
			// The next execution must measure without changing Lua reachability.
			if err := state.RawSetGlobal("kept", String(strings.Repeat("x", 512<<10))); err != nil {
				t.Fatal(err)
			}
			if err := state.CallDiscard(idle.Value()); err != nil {
				t.Fatal(err)
			}
			control := &state.runtime.collection
			if !control.stopped {
				t.Fatal("heap measurement restarted automatic collection")
			}
			if control.requested {
				t.Fatal("completed heap measurement remained pending at every safe point")
			}
			if finalized != 0 || data.flags&userDataFinalized != 0 {
				t.Fatal("heap measurement scheduled or ran a finalizer")
			}
			if value, found := weak.rawIntSlot(1); !found ||
				!rawSlotEqual(value, slotFromTableObject(unreachable)) {
				t.Fatal("heap measurement cleared a weak entry")
			}

			switch resume {
			case "restart":
				if err := state.RestartGC(); err != nil {
					t.Fatal(err)
				}
				if err := state.CallDiscard(idle.Value()); err != nil {
					t.Fatal(err)
				}
			case "collect":
				if err := state.Collect(); err != nil {
					t.Fatal(err)
				}
			}
			if control.stopped {
				t.Fatal("collection did not resume")
			}
			if finalized != 1 {
				t.Fatalf("resumed collection ran %d finalizers; want 1", finalized)
			}
			if _, found := weak.rawIntSlot(1); found || unreachable.owner != nil {
				t.Fatal("resumed collection did not clear and sweep the weak value")
			}
		})
	}
}

func TestStoppedHeapChecksRechargeReimportedStrings(t *testing.T) {
	state, err := New(Options{MaxHeapBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	idle, err := state.LoadString("@stopped-string-accounting.lua", "return")
	if err != nil {
		t.Fatal(err)
	}
	if err := state.StopGC(); err != nil {
		t.Fatal(err)
	}

	// Raw writes may exceed the limit. Drop the string before execution so
	// its old allocation charge must not be mistaken for retained Lua data.
	external := String(strings.Repeat("x", 2<<20))
	if err := state.RawSetGlobal("scratch", external); err != nil {
		t.Fatal(err)
	}
	if err := state.RawSetGlobal("scratch", Nil()); err != nil {
		t.Fatal(err)
	}
	if err := state.CallDiscard(idle.Value()); err != nil {
		t.Fatalf("discarded string counted against the heap limit: %v", err)
	}

	// Reusing the same immutable backing must charge it again. Otherwise a
	// stale attribution entry can hide an over-limit import after measurement.
	if err := state.RawSetGlobal("scratch", external); err != nil {
		t.Fatal(err)
	}
	err = state.CallDiscard(idle.Value())
	var failure *Error
	if !errors.As(err, &failure) || failure.Category() != LimitError {
		t.Fatalf("reimported over-limit string error = %v; want LimitError", err)
	}
	if !state.runtime.collection.stopped {
		t.Fatal("heap enforcement restarted automatic collection")
	}
}

func TestCollectionHostSurfaceUsesTheSemanticCollector(t *testing.T) {
	state := newCollectorTestState(t)

	initial, err := state.HeapBytes()
	if err != nil {
		t.Fatal(err)
	}
	if initial == 0 || initial != state.semanticHeap().bytes {
		t.Fatalf(
			"initial HeapBytes = %d; semantic heap = %d",
			initial,
			state.semanticHeap().bytes,
		)
	}

	var stateCollectError error
	entry, err := state.NewNativeFunction(func(frame Frame) Outcome {
		before := frame.thread.state.semanticHeap().bytes
		stateCollectError = state.Collect()
		if err := frame.Collect(); err != nil {
			t.Fatal(err)
		}
		after := frame.thread.state.semanticHeap().bytes
		return frame.ReturnValues(Number(float64(before)), Number(float64(after)))
	})
	if err != nil {
		t.Fatal(err)
	}
	results, err := state.Call(entry.Value())
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(stateCollectError, ErrRunning) {
		t.Fatalf(
			"State.Collect during callback = %v; want ErrRunning",
			stateCollectError,
		)
	}
	if len(results) != 2 {
		t.Fatalf("Frame collector returned %d observations; want 2", len(results))
	}
	for index, result := range results {
		number, ok := result.AsNumber()
		if !ok || number <= 0 {
			t.Fatalf("Frame collection observation %d = %v", index, result)
		}
	}

	if err := state.Collect(); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := state.HeapBytes(); !errors.Is(err, ErrClosed) {
		t.Fatalf("HeapBytes after Close = %v; want ErrClosed", err)
	}
	if err := state.Collect(); !errors.Is(err, ErrClosed) {
		t.Fatalf("Collect after Close = %v; want ErrClosed", err)
	}
}

func TestAutomaticCollectionRunsOnlyAtRootedExecutorSafePoints(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()

	target := mustLoadString(
		t,
		state,
		"@automatic-newtable.lua",
		`return 41, {answer = 42}`,
	)
	garbage := newTable(state, 0, 0)
	state.main.reserveValues(32)
	state.main.reserveFrames(4)
	state.resetCollectionDebt()
	state.runtime.collection.budget = 1
	if state.runtime.collection.requested {
		t.Fatal("fresh debt interval began with a due cycle")
	}

	results, err := state.Call(target.Value())
	if err != nil {
		t.Fatal(err)
	}
	if garbage.owner != nil {
		t.Fatal("automatic collection did not sweep prior garbage")
	}
	if len(results) != 2 {
		t.Fatalf("automatic collection changed result count to %d", len(results))
	}
	if number, ok := results[0].AsNumber(); !ok || number != 41 {
		t.Fatalf("first rooted result = %v; want 41", results[0])
	}
	table, ok := results[1].AsTable()
	if !ok {
		t.Fatalf("second rooted result = %v; want table", results[1])
	}
	assertTestValue(t, rawStr(table, "answer"), Number(42))
	if state.runtime.collection.requested {
		t.Fatal("completed automatic cycle remained requested")
	}
}

func TestAutomaticCollectionServicesPreexistingDebtAtRootEntry(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()

	target, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.ReturnNumber(42)
	})
	if err != nil {
		t.Fatal(err)
	}
	garbage := newTable(state, 0, 0)
	state.main.reserveValues(4)
	state.main.reserveFrames(1)
	state.resetCollectionDebt()
	state.runtime.collection.requestCycle()

	results, err := state.Call(target.Value())
	if err != nil {
		t.Fatal(err)
	}
	if garbage.owner != nil {
		t.Fatal("root-entry collection did not sweep prior garbage")
	}
	if len(results) != 1 {
		t.Fatalf("native result count = %d; want 1", len(results))
	}
	if number, ok := results[0].AsNumber(); !ok || number != 42 {
		t.Fatalf("native result = %v; want 42", results[0])
	}
	if state.runtime.collection.requested {
		t.Fatal("root-entry collection remained requested")
	}
}

func TestAutomaticCollectionRootsNativeReturnAtDepthZero(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()

	var returned *tableObject
	target, err := state.NewNativeFunction(func(frame Frame) Outcome {
		returned = newTable(state, 0, 1)
		if setErr := returned.rawSetStringSlot(
			"answer",
			numberSlot(42),
		); setErr != nil {
			frame.ThrowString(setErr.Error())
		}
		return frame.returnOne(
			frame.activation(),
			slotFromTableObject(returned),
		)
	})
	if err != nil {
		t.Fatal(err)
	}
	garbage := newTable(state, 0, 0)
	state.main.reserveValues(8)
	state.main.reserveFrames(2)
	state.resetCollectionDebt()
	state.runtime.collection.budget = 1

	results, err := state.Call(target.Value())
	if err != nil {
		t.Fatal(err)
	}
	if garbage.owner != nil {
		t.Fatal("native-return collection did not sweep prior garbage")
	}
	if returned == nil || returned.owner != state.runtime {
		t.Fatal("collection swept the compact native result")
	}
	if len(results) != 1 {
		t.Fatalf("native result count = %d; want 1", len(results))
	}
	table, ok := results[0].AsTable()
	if !ok || table.runtimeObject() != returned {
		t.Fatal("native return did not preserve canonical table identity")
	}
	assertTestValue(t, rawStr(table, "answer"), Number(42))
	if state.runtime.collection.requested {
		t.Fatal("native-return collection remained requested")
	}
}
