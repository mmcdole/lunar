package lua

import (
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
