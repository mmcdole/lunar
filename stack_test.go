package lua

import "testing"

func TestFrameWhereAttributesTheCallSite(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

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
		t.Errorf("Where(0) = %q; want empty for a native activation", nativeLevel)
	}
	if callerLevel != "probe.lua:2: " {
		t.Errorf("Where(1) = %q; want %q", callerLevel, "probe.lua:2: ")
	}
	if deepLevel != "" {
		t.Errorf("Where(99) = %q; want empty past the stack bottom", deepLevel)
	}
}
