package lua

import (
	"errors"
	"strings"
	"testing"
)

func TestFrameWhereAttributesTheCallSite(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	var nativeLevel, callerLevel, deepLevel string
	probe, err := state.NewNativeFunction(func(frame Frame) Outcome {
		nativeLevel = frame.Where(0)
		callerLevel = frame.Where(1)
		deepLevel = frame.Where(99)
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("probe", probe.Value()); err != nil {
		t.Fatal(err)
	}

	if _, err := state.DoString("@probe.lua", "\nprobe()"); err != nil {
		t.Fatal(err)
	}
	if nativeLevel != "" {
		t.Errorf("Where(0) = %q; want \"\" for a native activation", nativeLevel)
	}
	if callerLevel != "probe.lua:2: " {
		t.Errorf("Where(1) = %q; want %q", callerLevel, "probe.lua:2: ")
	}
	if deepLevel != "" {
		t.Errorf("Where(99) = %q; want \"\" past the stack bottom", deepLevel)
	}
}

// A message prefixed with Where(1) must be indistinguishable from one the
// runtime positioned itself.
func TestFrameWhereMatchesRuntimeAttribution(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	failing, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.RaiseString(frame.Where(1) + "host rejected the request")
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("failing", failing.Value()); err != nil {
		t.Fatal(err)
	}

	_, hostErr := state.DoString("@attribution.lua", "failing()")
	if hostErr == nil {
		t.Fatal("expected a failure")
	}
	const want = "attribution.lua:1: host rejected the request"
	if !strings.Contains(hostErr.Error(), want) {
		t.Fatalf("error = %q; want it to contain %q", hostErr.Error(), want)
	}

	// A runtime-positioned error from the same line, for comparison.
	_, runtimeErr := state.DoString("@attribution.lua", "error('runtime')")
	if runtimeErr == nil {
		t.Fatal("expected a runtime failure")
	}
	if !strings.Contains(runtimeErr.Error(), "attribution.lua:1: runtime") {
		t.Fatalf("runtime error = %q", runtimeErr.Error())
	}
}

func TestFrameStackWalksActivations(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	var levels []TraceFrame
	probe, err := state.NewNativeFunction(func(frame Frame) Outcome {
		for level := 0; ; level++ {
			entry, ok := frame.Stack(level)
			if !ok {
				break
			}
			levels = append(levels, entry)
		}
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("probe", probe.Value()); err != nil {
		t.Fatal(err)
	}

	_, err = state.DoString(
		"@walk.lua",
		"local function inner() probe() end\nlocal function outer() inner() end\nouter()",
	)
	if err != nil {
		t.Fatal(err)
	}

	// probe, inner, outer, main chunk.
	if len(levels) != 4 {
		t.Fatalf("walked %d levels: %+v", len(levels), levels)
	}
	if levels[0].Source != "=[Go]" {
		t.Errorf("level 0 source = %q; want the native activation", levels[0].Source)
	}
	// inner is declared on line 1, outer on line 2, and the main chunk
	// calls outer on line 3.
	for level, wantLine := range map[int]int{1: 1, 2: 2, 3: 3} {
		if levels[level].Source != "@walk.lua" {
			t.Errorf("level %d source = %q; want @walk.lua", level, levels[level].Source)
		}
		if levels[level].Line != wantLine {
			t.Errorf("level %d line = %d; want %d", level, levels[level].Line, wantLine)
		}
	}
}

func TestFrameTracebackMatchesErrorTraceback(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	var live []TraceFrame
	probe, err := state.NewNativeFunction(func(frame Frame) Outcome {
		live = frame.Traceback()
		return frame.RaiseString("stop")
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("probe", probe.Value()); err != nil {
		t.Fatal(err)
	}

	_, hostErr := state.DoString("@trace.lua", "local function step() probe() end\nstep()")
	if hostErr == nil {
		t.Fatal("expected a failure")
	}
	var failure *Error
	if !errors.As(hostErr, &failure) {
		t.Fatalf("error is not a *Error: %v", hostErr)
	}

	reported := failure.Traceback()
	if len(live) == 0 {
		t.Fatal("Frame.Traceback returned no activations")
	}
	if len(live) != len(reported) {
		t.Fatalf(
			"Frame.Traceback has %d activations; Error.Traceback has %d",
			len(live),
			len(reported),
		)
	}
	for index := range live {
		if live[index] != reported[index] {
			t.Errorf(
				"activation %d: live %+v; reported %+v",
				index,
				live[index],
				reported[index],
			)
		}
	}
}

func TestStateTracebackObservesTheExecutingThread(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	idle, err := state.Traceback()
	if err != nil {
		t.Fatal(err)
	}
	if len(idle) != 0 {
		t.Fatalf("idle State reported %d activations: %+v", len(idle), idle)
	}

	var executing []TraceFrame
	probe, err := state.NewNativeFunction(func(frame Frame) Outcome {
		executing, err = frame.State().Traceback()
		if err != nil {
			t.Errorf("State.Traceback during execution: %v", err)
		}
		return frame.Return()
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("probe", probe.Value()); err != nil {
		t.Fatal(err)
	}
	if _, err := state.DoString("@live.lua", "probe()"); err != nil {
		t.Fatal(err)
	}
	if len(executing) == 0 {
		t.Fatal("State.Traceback reported nothing while executing")
	}
	if executing[0].Source != "=[Go]" {
		t.Errorf("innermost activation = %q; want the native probe", executing[0].Source)
	}
}

// Where is what makes a shared checking helper attribute failures the way
// the runtime does, so the two features are exercised together.
func TestWhereComposesWithThrow(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}

	positive := func(frame Frame, index int) float64 {
		value, ok := frame.Number(index)
		if !ok {
			frame.ThrowArgTypeError(index, NumberKind)
		}
		if value <= 0 {
			frame.ThrowString(frame.Where(1) + "expected a positive number")
		}
		return value
	}

	scale, err := state.NewNativeFunction(func(frame Frame) Outcome {
		return frame.ReturnNumber(positive(frame, 0) * 2)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetGlobal("scale", scale.Value()); err != nil {
		t.Fatal(err)
	}

	_, err = state.DoString("@scale.lua", "return scale(-1)")
	if err == nil {
		t.Fatal("expected a failure")
	}
	const want = "scale.lua:1: expected a positive number"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q; want it to contain %q", err.Error(), want)
	}
}
