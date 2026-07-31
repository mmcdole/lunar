package lua

import (
	"errors"
	"testing"
)

func TestGCStopAndRestart(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	running, err := state.GCRunning()
	if err != nil {
		t.Fatal(err)
	}
	if !running {
		t.Fatal("a new State reports the collector stopped")
	}

	if err := state.StopGC(); err != nil {
		t.Fatal(err)
	}
	if running, err = state.GCRunning(); err != nil {
		t.Fatal(err)
	} else if running {
		t.Fatal("StopGC did not stop the collector")
	}

	if err := state.RestartGC(); err != nil {
		t.Fatal(err)
	}
	if running, err = state.GCRunning(); err != nil {
		t.Fatal(err)
	} else if !running {
		t.Fatal("RestartGC did not resume the collector")
	}
}

// Whether the collector ran is observable through finalizers, not through
// HeapBytes: HeapBytes measures reachable bytes, so unreachable garbage is
// already excluded from it whether or not a cycle has happened.
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

func TestGCPauseAndStepMultiplierRoundTrip(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	pause, err := state.GCPause()
	if err != nil {
		t.Fatal(err)
	}
	if pause != defaultCollectionPause {
		t.Fatalf("default pause = %d; want %d", pause, defaultCollectionPause)
	}
	previous, err := state.SetGCPause(150)
	if err != nil {
		t.Fatal(err)
	}
	if previous != defaultCollectionPause {
		t.Fatalf("SetGCPause returned %d; want the previous %d",
			previous, defaultCollectionPause)
	}
	if pause, err = state.GCPause(); err != nil {
		t.Fatal(err)
	} else if pause != 150 {
		t.Fatalf("pause after SetGCPause = %d", pause)
	}

	multiplier, err := state.GCStepMultiplier()
	if err != nil {
		t.Fatal(err)
	}
	if multiplier != defaultCollectionStepMultiplier {
		t.Fatalf("default step multiplier = %d", multiplier)
	}
	previous, err = state.SetGCStepMultiplier(400)
	if err != nil {
		t.Fatal(err)
	}
	if previous != defaultCollectionStepMultiplier {
		t.Fatalf("SetGCStepMultiplier returned %d", previous)
	}
	if multiplier, err = state.GCStepMultiplier(); err != nil {
		t.Fatal(err)
	} else if multiplier != 400 {
		t.Fatalf("step multiplier after set = %d", multiplier)
	}
}

// The Go API and Lua's collectgarbage drive the same control block, so a
// change through one must be visible to the other.
func TestGCControlAgreesWithCollectGarbage(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	if _, err := state.SetGCPause(175); err != nil {
		t.Fatal(err)
	}
	results, err := state.DoString(
		"@pause.lua",
		`return collectgarbage("setpause", 190)`,
	)
	if err != nil {
		t.Fatal(err)
	}
	if previous, _ := results[0].AsNumber(); previous != 175 {
		t.Fatalf("collectgarbage saw pause %v; want the host's 175", previous)
	}
	pause, err := state.GCPause()
	if err != nil {
		t.Fatal(err)
	}
	if pause != 190 {
		t.Fatalf("host saw pause %d; want Lua's 190", pause)
	}

	if _, err := state.DoString("@stop.lua", `collectgarbage("stop")`); err != nil {
		t.Fatal(err)
	}
	running, err := state.GCRunning()
	if err != nil {
		t.Fatal(err)
	}
	if running {
		t.Fatal("host did not observe collectgarbage(\"stop\")")
	}
}

// Explicit Collect resumes automatic collection, so a stopped collector
// does not stay stopped through a host-driven cycle.
func TestCollectResumesAutomaticCollection(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()

	if err := state.StopGC(); err != nil {
		t.Fatal(err)
	}
	if err := state.Collect(); err != nil {
		t.Fatal(err)
	}
	running, err := state.GCRunning()
	if err != nil {
		t.Fatal(err)
	}
	if !running {
		t.Fatal("Collect left the collector stopped")
	}
}

// Policy changes from a running State would race the executor, so they are
// refused; the Frame forms are the executing path.
func TestGCControlRefusesARunningState(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	probe, err := state.NewNativeFunction(func(frame Frame) Outcome {
		host := frame.State()
		if err := host.StopGC(); !errors.Is(err, ErrRunning) {
			t.Errorf("StopGC while running = %v; want ErrRunning", err)
		}
		if _, err := host.SetGCPause(120); !errors.Is(err, ErrRunning) {
			t.Errorf("SetGCPause while running = %v; want ErrRunning", err)
		}

		// The Frame forms do work while executing.
		if !frame.GCRunning() {
			t.Error("Frame.GCRunning reported a stopped collector")
		}
		frame.StopGC()
		if frame.GCRunning() {
			t.Error("Frame.StopGC did not stop the collector")
		}
		frame.RestartGC()
		if !frame.GCRunning() {
			t.Error("Frame.RestartGC did not resume the collector")
		}
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("probe", probe.Value()); err != nil {
		t.Fatal(err)
	}
	if _, err := state.DoString("@probe.lua", "probe()"); err != nil {
		t.Fatal(err)
	}
}

func TestGCControlOnClosedState(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if err := state.StopGC(); !errors.Is(err, ErrClosed) {
		t.Errorf("StopGC = %v; want ErrClosed", err)
	}
	if err := state.RestartGC(); !errors.Is(err, ErrClosed) {
		t.Errorf("RestartGC = %v; want ErrClosed", err)
	}
	if _, err := state.GCRunning(); !errors.Is(err, ErrClosed) {
		t.Errorf("GCRunning = %v; want ErrClosed", err)
	}
	if _, err := state.GCPause(); !errors.Is(err, ErrClosed) {
		t.Errorf("GCPause = %v; want ErrClosed", err)
	}
	if _, err := state.SetGCPause(120); !errors.Is(err, ErrClosed) {
		t.Errorf("SetGCPause = %v; want ErrClosed", err)
	}
	if _, err := state.GCStepMultiplier(); !errors.Is(err, ErrClosed) {
		t.Errorf("GCStepMultiplier = %v; want ErrClosed", err)
	}
	if _, err := state.SetGCStepMultiplier(200); !errors.Is(err, ErrClosed) {
		t.Errorf("SetGCStepMultiplier = %v; want ErrClosed", err)
	}
}
