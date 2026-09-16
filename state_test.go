package lua

import (
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestClosePreservesReadsAndRejectsMutation(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	table, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := table.RawSetString("answer", Number(42)); err != nil {
		t.Fatal(err)
	}
	data, err := state.NewUserData("alive")
	if err != nil {
		t.Fatal(err)
	}
	main := state.MainThread()
	environment, err := threadEnvironment(main)
	if err != nil {
		t.Fatal(err)
	}
	if err := environment.RawSetString("retained", Number(17)); err != nil {
		t.Fatal(err)
	}

	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if state.main.globals != nil || state.registry != nil {
		t.Fatal("Close retained global runtime roots")
	}
	if main.Status() != ThreadClosed {
		t.Fatalf("main thread status = %v, want ThreadClosed", main.Status())
	}
	if got := rawStr(table, "answer"); got.String() != "42" {
		t.Fatalf("retained table read = %v, want 42", got)
	}
	if data.Data() != "alive" {
		t.Fatal("retained userdata payload became unreadable")
	}
	if got, ok := rawStr(environment, "retained").AsNumber(); !ok ||
		got != 17 {
		t.Fatalf("retained environment read = (%v, %v); want 17", got, ok)
	}
	if _, err := threadEnvironment(main); !errors.Is(err, ErrClosed) {
		t.Fatalf("ThreadEnvironment after close = %v; want ErrClosed", err)
	}
	if err := setThreadEnvironment(
		main,
		environment,
	); !errors.Is(err, ErrClosed) {
		t.Fatalf("SetThreadEnvironment after close = %v; want ErrClosed", err)
	}
	if err := table.RawSetString("answer", Number(43)); !errors.Is(err, ErrClosed) {
		t.Fatalf("table mutation after close = %v, want ErrClosed", err)
	}
	if err := data.SetData("changed"); !errors.Is(err, ErrClosed) {
		t.Fatalf("userdata mutation after close = %v, want ErrClosed", err)
	}
	if _, err := state.NewTableWithCapacity(0, 0); !errors.Is(err, ErrClosed) {
		t.Fatalf("NewTable after close = %v, want ErrClosed", err)
	}
}

func TestRetainedObjectDoesNotPinClosedStateRoots(t *testing.T) {
	collected := make(chan struct{}, 1)
	retained := closeStateWithUnrelatedRoot(collected)

	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		runtime.GC()
		select {
		case <-collected:
			runtime.KeepAlive(retained)
			return
		case <-deadline.C:
			t.Fatal("retained string pinned an unrelated closed-State root")
		case <-ticker.C:
		}
	}
}

func TestZeroStateRejectsOperations(t *testing.T) {
	var state State
	if err := state.Close(); err != nil {
		t.Fatalf("zero State Close: %v", err)
	}
	if state.main != nil {
		t.Fatal("zero State has a main thread")
	}
	if state.String("x").Valid() {
		t.Fatal("zero State constructed a valid string")
	}
	if _, err := state.NewTable(); !errors.Is(err, ErrClosed) {
		t.Fatalf("zero State NewTable = %v, want ErrClosed", err)
	}
	if _, err := state.NewTableWithCapacity(0, 0); !errors.Is(
		err,
		ErrClosed,
	) {
		t.Fatalf("zero State NewTableWithCapacity = %v, want ErrClosed", err)
	}
	if _, err := state.NewUserData(nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("zero State NewUserData = %v, want ErrClosed", err)
	}
	if _, err := state.RawGlobal("x"); !errors.Is(err, ErrClosed) {
		t.Fatalf("zero State Global = %v, want ErrClosed", err)
	}
}

func TestInvalidOptions(t *testing.T) {
	for _, test := range []struct {
		name    string
		options Options
	}{
		{name: "MaxValues", options: Options{MaxValues: -1}},
		{name: "MaxFrames", options: Options{MaxFrames: -1}},
		{name: "MaxLoadBytes", options: Options{MaxLoadBytes: -1}},
		{name: "MaxHeapBytes", options: Options{MaxHeapBytes: -1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, optionErr := New(test.options)
			if !errors.Is(optionErr, ErrNegativeCapacity) ||
				!strings.Contains(optionErr.Error(), test.name) {
				t.Fatalf(
					"negative %s error = %v; want named ErrNegativeCapacity",
					test.name,
					optionErr,
				)
			}
		})
	}
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if state.options.MaxValues != 65_536 ||
		state.options.MaxFrames != 20_000 ||
		state.options.MaxLoadBytes != 64<<20 {
		t.Fatalf("default options = %+v", state.options)
	}
	if _, err := state.NewTableWithCapacity(maxTableHint+1, 0); !errors.Is(err, ErrCapacity) {
		t.Fatalf("large array hint error = %v, want ErrCapacity", err)
	}
	if _, err := state.NewTableWithCapacity(0, maxTableHint+1); !errors.Is(err, ErrCapacity) {
		t.Fatalf("large record hint error = %v, want ErrCapacity", err)
	}
}

func BenchmarkNewUserData(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer state.Close()

	var data *UserData
	b.ReportAllocs()
	for range b.N {
		data, err = state.NewUserData(nil)
		if err != nil {
			b.Fatal(err)
		}
	}
	runtime.KeepAlive(data)
}

func BenchmarkNewTable(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer state.Close()

	var table *Table
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		table, err = state.NewTableWithCapacity(0, 0)
		if err != nil {
			b.Fatal(err)
		}
	}
	runtime.KeepAlive(table)
}
