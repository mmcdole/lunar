package lua

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

var errInterrupted = errors.New("interrupted by the host")

func TestInterruptStopsARunawayLoop(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	polls := 0
	if err := state.SetInterrupt(func() error {
		polls++
		if polls > 16 {
			return errInterrupted
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// An ordinary call, with no context installed at all.
	_, err = state.DoString("@spin.lua", "while true do end")
	if err == nil {
		t.Fatal("interrupt did not stop the loop")
	}
	var failure *Error
	if !errors.As(err, &failure) {
		t.Fatalf("error is not a *Error: %v", err)
	}
	if failure.Category() != ContextError {
		t.Fatalf("category = %v; want ContextError", failure.Category())
	}
	if !errors.Is(err, errInterrupted) {
		t.Fatalf("interrupt error lost its Go cause: %v", err)
	}
}

// An interrupt is the host's, not the script's: pcall must not catch it.
func TestInterruptIsNotCatchableByLua(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	polls := 0
	if err := state.SetInterrupt(func() error {
		polls++
		if polls > 16 {
			return errInterrupted
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	_, err = state.DoString("@guarded.lua", `
		local ok = pcall(function() while true do end end)
		return ok
	`)
	if err == nil {
		t.Fatal("pcall swallowed a host interrupt")
	}
	if !errors.Is(err, errInterrupted) {
		t.Fatalf("error = %v; want the host interrupt", err)
	}
}

// A script must not escape an interrupt by looping inside a coroutine.
func TestInterruptReachesCoroutines(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	polls := 0
	if err := state.SetInterrupt(func() error {
		polls++
		if polls > 16 {
			return errInterrupted
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	_, err = state.DoString("@coroutine.lua", `
		local worker = coroutine.create(function() while true do end end)
		return coroutine.resume(worker)
	`)
	if err == nil {
		t.Fatal("interrupt did not reach the coroutine")
	}
	if !errors.Is(err, errInterrupted) {
		t.Fatalf("error = %v; want the host interrupt", err)
	}
}

// The interrupt guards host-resumed threads too, not only coroutines
// resumed from Lua.
func TestInterruptReachesHostResumedThreads(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	polls := 0
	if err := state.SetInterrupt(func() error {
		polls++
		if polls > 16 {
			return errInterrupted
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	body, err := state.LoadString("@spin.lua", "while true do end")
	if err != nil {
		t.Fatal(err)
	}
	thread, err := state.NewThread(body.Value())
	if err != nil {
		t.Fatal(err)
	}
	_, status, err := thread.Resume()
	if !errors.Is(err, errInterrupted) {
		t.Fatalf("host Resume error = %v; want the interrupt", err)
	}
	if status != ThreadDead {
		t.Fatalf("status = %v; want ThreadDead", status)
	}
}

// An interrupt installed by a callback inside a host-resumed coroutine must
// not strand a polling budget on the suspended thread; the next external
// resume treats a leftover budget as corrupted execution state.
func TestInterruptInstalledInsideHostResumedCoroutine(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	arm, err := state.NewNativeFunction(func(frame Frame) Outcome {
		if err := frame.State().SetInterrupt(func() error {
			return nil
		}); err != nil {
			return frame.RaiseError(err)
		}
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("arm", arm.Value()); err != nil {
		t.Fatal(err)
	}

	body, err := state.LoadString(
		"@armed.lua",
		"arm()\ncoroutine.yield()\nreturn 'resumed'",
	)
	if err != nil {
		t.Fatal(err)
	}
	thread, err := state.NewThread(body.Value())
	if err != nil {
		t.Fatal(err)
	}
	if _, status, err := thread.Resume(); err != nil {
		t.Fatal(err)
	} else if status != ThreadSuspended {
		t.Fatalf("status after yield = %v", status)
	}
	// The interrupt is removed while the thread is suspended; the second
	// resume must still be legal.
	if err := state.RemoveInterrupt(); err != nil {
		t.Fatal(err)
	}
	results, status, err := thread.Resume()
	if err != nil {
		t.Fatalf("second resume: %v", err)
	}
	if status != ThreadDead {
		t.Fatalf("final status = %v", status)
	}
	if text, _ := results[0].AsString(); text != "resumed" {
		t.Fatalf("final result = %q", text)
	}
}

// The interrupt is ambient, so it can be re-armed or detached while Lua is
// running. That is what a context captured at call entry cannot express.
func TestInterruptCanBeDetachedFromACallback(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	var tripped atomic.Bool
	if err := state.SetInterrupt(func() error {
		if tripped.Load() {
			return errInterrupted
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// The callback stands in for host work that legitimately blocks: it
	// detaches the interrupt, trips the flag, and re-arms afterwards.
	blocking, err := state.NewNativeFunction(func(frame Frame) Outcome {
		if err := frame.State().RemoveInterrupt(); err != nil {
			return frame.RaiseError(err)
		}
		tripped.Store(true)
		tripped.Store(false)
		if err := frame.State().SetInterrupt(func() error {
			if tripped.Load() {
				return errInterrupted
			}
			return nil
		}); err != nil {
			return frame.RaiseError(err)
		}
		return frame.ReturnString("done")
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("blocking", blocking.Value()); err != nil {
		t.Fatal(err)
	}

	results, err := state.DoString("@detach.lua", "return blocking()")
	if err != nil {
		t.Fatalf("detaching the interrupt failed: %v", err)
	}
	if text, _ := results[0].AsString(); text != "done" {
		t.Fatalf("result = %q", text)
	}

	// Re-armed: tripping the flag now stops the next call.
	tripped.Store(true)
	if _, err := state.DoString("@detach.lua", "while true do end"); !errors.Is(
		err,
		errInterrupted,
	) {
		t.Fatalf("re-armed interrupt error = %v", err)
	}
}

func TestInterruptInstalledDuringExecutionTakesEffect(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	arm, err := state.NewNativeFunction(func(frame Frame) Outcome {
		polls := 0
		if err := frame.State().SetInterrupt(func() error {
			polls++
			if polls > 16 {
				return errInterrupted
			}
			return nil
		}); err != nil {
			return frame.RaiseError(err)
		}
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("arm", arm.Value()); err != nil {
		t.Fatal(err)
	}

	_, err = state.DoString("@late.lua", "arm()\nwhile true do end")
	if !errors.Is(err, errInterrupted) {
		t.Fatalf("error = %v; want the interrupt armed mid-call", err)
	}
}

func TestRemoveInterruptLetsExecutionProceed(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	if err := state.SetInterrupt(func() error { return errInterrupted }); err != nil {
		t.Fatal(err)
	}
	if err := state.RemoveInterrupt(); err != nil {
		t.Fatal(err)
	}

	results, err := state.DoString("@clear.lua", `
		local total = 0
		for index = 1, 100000 do total = total + index end
		return total
	`)
	if err != nil {
		t.Fatalf("execution failed after RemoveInterrupt: %v", err)
	}
	if total, _ := results[0].AsNumber(); total != 5000050000 {
		t.Fatalf("total = %v", total)
	}
}

// A nil callback clears the interrupt rather than installing a panic.
func TestSetInterruptNilClears(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	if err := state.SetInterrupt(func() error { return errInterrupted }); err != nil {
		t.Fatal(err)
	}
	if err := state.SetInterrupt(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := state.DoString("@nil.lua", "return 1"); err != nil {
		t.Fatalf("execution failed after clearing the interrupt: %v", err)
	}
}

// Context cancellation and an interrupt can be armed together; whichever
// fires first stops execution, and neither corrupts the other's bookkeeping.
func TestInterruptCoexistsWithContextCancellation(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	if err := state.SetInterrupt(func() error { return nil }); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = state.DoStringContext(ctx, "@both.lua", "while true do end")
	if err == nil {
		t.Fatal("cancelled context did not stop execution")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v; want context cancellation", err)
	}

	// The interrupt still works on a later ordinary call, which proves the
	// cancelled call left no execution bookkeeping behind.
	polls := 0
	if err := state.SetInterrupt(func() error {
		polls++
		if polls > 16 {
			return errInterrupted
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := state.DoString("@after.lua", "while true do end"); !errors.Is(
		err,
		errInterrupted,
	) {
		t.Fatalf("error after a cancelled call = %v", err)
	}
}

// An interrupt that never refuses must not change observable behavior.
func TestPassiveInterruptDoesNotDisturbExecution(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	for _, open := range []func() error{
		state.OpenBase,
		state.OpenString,
		state.OpenTable,
	} {
		if err := open(); err != nil {
			t.Fatal(err)
		}
	}

	polls := 0
	if err := state.SetInterrupt(func() error {
		polls++
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	results, err := state.DoString("@work.lua", `
		local parts = {}
		for index = 1, 500 do parts[index] = tostring(index) end
		return table.concat(parts, ","):len()
	`)
	if err != nil {
		t.Fatalf("passive interrupt disturbed execution: %v", err)
	}
	if length, _ := results[0].AsNumber(); length <= 0 {
		t.Fatalf("length = %v", length)
	}
	if polls == 0 {
		t.Fatal("interrupt was never polled")
	}
}

func TestInterruptOnClosedState(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if err := state.SetInterrupt(func() error { return nil }); !errors.Is(
		err,
		ErrClosed,
	) {
		t.Fatalf("SetInterrupt on a closed State = %v; want ErrClosed", err)
	}
	if err := state.RemoveInterrupt(); !errors.Is(err, ErrClosed) {
		t.Fatalf("RemoveInterrupt on a closed State = %v; want ErrClosed", err)
	}
}

// The message the host sees carries the interrupt's own text.
func TestInterruptErrorCarriesTheHostMessage(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	if err := state.SetInterrupt(func() error {
		return errors.New("budget exhausted")
	}); err != nil {
		t.Fatal(err)
	}
	_, err = state.DoString("@budget.lua", "while true do end")
	if err == nil {
		t.Fatal("expected the interrupt to stop execution")
	}
	if !strings.Contains(err.Error(), "budget exhausted") {
		t.Fatalf("error = %q", err.Error())
	}
}
