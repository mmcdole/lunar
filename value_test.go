package lua

import (
	"errors"
	"math"
	"reflect"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"
)

func TestValueRepresentation(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) == 8 {
		if size := unsafe.Sizeof(Value{}); size != 16 {
			t.Fatalf("Value size = %d, want 16", size)
		}
		if size := unsafe.Sizeof(slot{}); size != 16 {
			t.Fatalf("slot size = %d, want 16", size)
		}
		if size := unsafe.Sizeof(tableEntry{}); size != 40 {
			t.Fatalf("table entry size = %d, want 40", size)
		}
		if size := unsafe.Sizeof(luaString{}); size != 24 {
			t.Fatalf("luaString size = %d, want 24", size)
		}
		if size := unsafe.Sizeof(stringPool{}); size > 224 {
			t.Fatalf("stringPool header size = %d, want at most 224", size)
		}
	}
	if reflect.TypeOf(Value{}).Comparable() {
		t.Fatal("Value must not be Go-comparable")
	}

	var invalid Value
	if invalid.Valid() || invalid.Kind() != InvalidKind {
		t.Fatalf("zero Value = (%v, %v), want invalid", invalid.Valid(), invalid.Kind())
	}
	nilValue := Nil()
	if !nilValue.Valid() || !nilValue.IsNil() || nilValue.Kind() != NilKind {
		t.Fatalf("Nil = (%v, %v, %v)", nilValue.Valid(), nilValue.IsNil(), nilValue.Kind())
	}

	for _, test := range []struct {
		value Value
		want  bool
	}{
		{Bool(false), false},
		{Bool(true), true},
		{Bool(false), false},
		{Bool(true), true},
	} {
		got, ok := test.value.AsBool()
		if !ok || got != test.want {
			t.Fatalf("%v.AsBool() = (%v, %v), want (%v, true)", test.value, got, ok, test.want)
		}
	}
	if Nil().Truth() || Bool(false).Truth() || !Bool(true).Truth() || !Number(0).Truth() {
		t.Fatal("Lua truthiness is incorrect")
	}
	if (Value{}).Truth() {
		t.Fatal("invalid Value must not become truthy")
	}

	for _, number := range []float64{
		0,
		math.Copysign(0, -1),
		1.5,
		math.Inf(1),
		math.NaN(),
	} {
		value := Number(number)
		got, ok := value.AsNumber()
		if !ok || math.Float64bits(got) != math.Float64bits(number) {
			t.Fatalf("number bits = %x, want %x", math.Float64bits(got), math.Float64bits(number))
		}
		roundTrip := slotFromValue(value).owningValue()
		got, ok = roundTrip.AsNumber()
		if !ok || math.Float64bits(got) != math.Float64bits(number) {
			t.Fatalf("slot round trip = %x, want %x", math.Float64bits(got), math.Float64bits(number))
		}
	}

	zero, ok := slot{}.owningValue().AsNumber()
	if !ok || zero != 0 {
		t.Fatalf("zero slot = (%v, %v), want numeric zero", zero, ok)
	}
}

func TestInvalidValueCannotEnterCompactStorage(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("slotFromValue accepted an invalid Value")
		}
	}()
	_ = slotFromValue(Value{})
}

func TestCanonicalObjectsAndOwnership(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	table, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	tableValue := table.Value()
	gotTable, ok := tableValue.Table()
	if !ok || gotTable != table {
		t.Fatalf("table round trip = (%p, %v), want %p", gotTable, ok, table)
	}
	if same, applicable := tableValue.SameObject(table.Value()); !applicable || !same {
		t.Fatalf("SameObject = (%v, %v), want (true, true)", same, applicable)
	}

	data, err := state.NewUserData("payload")
	if err != nil {
		t.Fatal(err)
	}
	gotData, ok := data.Value().UserData()
	if !ok || gotData != data || gotData.Data() != "payload" {
		t.Fatalf("userdata round trip failed")
	}

	main := state.MainThread()
	gotThread, ok := main.Value().Thread()
	if !ok || gotThread != main || !main.IsMain() || main.State() != state {
		t.Fatal("main thread is not canonical")
	}

	runtime.GC()
	if got, ok := tableValue.Table(); !ok || got != table {
		t.Fatal("Value did not retain its canonical object across GC")
	}

	other, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	otherTable, err := other.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := table.RawSetString("foreign", otherTable.Value()); !errors.Is(err, ErrForeignValue) {
		t.Fatalf("foreign table error = %v, want ErrForeignValue", err)
	}
	shared := other.String("x")
	if err := table.RawSetString("shared-string", shared); err != nil {
		t.Fatalf("state-neutral string rejected: %v", err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	if got := table.RawGetString("shared-string"); got.String() != "x" {
		t.Fatalf("state-neutral string = %v, want x", got)
	}
	if equal, err := state.RawEqual(shared, state.String("x")); err != nil || !equal {
		t.Fatalf("state-neutral equality after origin close = (%v, %v)", equal, err)
	}
	if err := table.RawSetString("number", Number(4)); err != nil {
		t.Fatalf("state-independent scalar rejected: %v", err)
	}
}

func TestStringIdentityAndBoundedAdmission(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}

	first := state.String("destination")
	second := state.String("destination")
	third := state.String("destination")
	if first.ref != second.ref || second.ref != third.ref {
		t.Fatal("short string did not retain canonical cache identity")
	}
	if text, ok := third.AsString(); !ok || text != "destination" {
		t.Fatalf("AsString = (%q, %v)", text, ok)
	}
	if same, applicable := second.SameObject(third); same || applicable {
		t.Fatalf("Lua strings must use value equality, got (%v, %v)", same, applicable)
	}

	for index := 0; index < 10_000; index++ {
		_ = state.String("one-off-" + strconv.Itoa(index))
	}
	if state.String("destination").ref != first.ref {
		t.Fatal("probation churn evicted a protected string")
	}

	binary := string([]byte{0, 1, 0x7f, 0x80, 0xff})
	binaryValue := state.String(binary)
	if got, ok := binaryValue.AsString(); !ok || got != binary {
		t.Fatalf("arbitrary-byte string = (%q, %v)", got, ok)
	}
	equal, err := state.RawEqual(first, third)
	if err != nil || !equal {
		t.Fatalf("string raw equality = (%v, %v), want (true, nil)", equal, err)
	}

	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if text, ok := third.AsString(); !ok || text != "destination" {
		t.Fatalf("retained string after close = (%q, %v)", text, ok)
	}
}

func TestClosePreservesReadsAndRejectsMutation(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	table, err := state.NewTable(0, 0)
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
	environment, err := state.ThreadEnvironment(main)
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
	if state.MainThread().Status() != ThreadClosed {
		t.Fatalf("main thread status = %v, want ThreadClosed", state.MainThread().Status())
	}
	if got := table.RawGetString("answer"); got.String() != "42" {
		t.Fatalf("retained table read = %v, want 42", got)
	}
	if data.Data() != "alive" {
		t.Fatal("retained userdata payload became unreadable")
	}
	if got, ok := environment.RawGetString("retained").AsNumber(); !ok ||
		got != 17 {
		t.Fatalf("retained environment read = (%v, %v); want 17", got, ok)
	}
	if _, err := state.ThreadEnvironment(main); !errors.Is(err, ErrClosed) {
		t.Fatalf("ThreadEnvironment after close = %v; want ErrClosed", err)
	}
	if err := state.SetThreadEnvironment(
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
	if _, err := state.NewTable(0, 0); !errors.Is(err, ErrClosed) {
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

func closeStateWithUnrelatedRoot(collected chan<- struct{}) Value {
	state, err := New(Options{})
	if err != nil {
		panic(err)
	}
	unrelated, err := state.NewTable(0, 0)
	if err != nil {
		panic(err)
	}
	runtime.SetFinalizer(unrelated, func(*Table) {
		collected <- struct{}{}
	})
	if err := state.SetGlobal("unrelated", unrelated.Value()); err != nil {
		panic(err)
	}
	retained := state.String("retained")
	if err := state.Close(); err != nil {
		panic(err)
	}
	return retained
}

func TestZeroStateRejectsOperations(t *testing.T) {
	var state State
	if err := state.Close(); err != nil {
		t.Fatalf("zero State Close: %v", err)
	}
	if state.MainThread() != nil {
		t.Fatal("zero State has a main thread")
	}
	if state.String("x").Valid() {
		t.Fatal("zero State constructed a valid string")
	}
	if _, err := state.NewTable(0, 0); !errors.Is(err, ErrClosed) {
		t.Fatalf("zero State NewTable = %v, want ErrClosed", err)
	}
	if _, err := state.NewUserData(nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("zero State NewUserData = %v, want ErrClosed", err)
	}
	if _, err := state.Global("x"); !errors.Is(err, ErrClosed) {
		t.Fatalf("zero State Global = %v, want ErrClosed", err)
	}
}

func TestInvalidOptions(t *testing.T) {
	if _, err := New(Options{MaxValues: -1}); !errors.Is(err, ErrNegativeCapacity) {
		t.Fatalf("negative MaxValues error = %v", err)
	}
	if _, err := New(Options{MaxFrames: -1}); !errors.Is(err, ErrNegativeCapacity) {
		t.Fatalf("negative MaxFrames error = %v", err)
	}
	if _, err := New(Options{
		MaxLoadBytes: -1,
	}); !errors.Is(err, ErrNegativeCapacity) {
		t.Fatalf("negative MaxLoadBytes error = %v", err)
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
	if _, err := state.NewTable(maxTableHint+1, 0); !errors.Is(err, ErrCapacity) {
		t.Fatalf("large array hint error = %v, want ErrCapacity", err)
	}
	if _, err := state.NewTable(0, maxTableHint+1); !errors.Is(err, ErrCapacity) {
		t.Fatalf("large record hint error = %v, want ErrCapacity", err)
	}
}

func TestValueSlotConversionDoesNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	table, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	values := []Value{
		Nil(),
		Bool(false),
		Bool(true),
		Number(1.25),
		state.String("cached"),
		table.Value(),
	}

	allocations := testing.AllocsPerRun(1000, func() {
		for _, value := range values {
			converted := slotFromValue(value).owningValue()
			runtime.KeepAlive(converted)
		}
	})
	if allocations != 0 {
		t.Fatalf("Value/slot conversion allocated %.2f times", allocations)
	}
}

func requireStableAllocationAccounting(t testing.TB) {
	t.Helper()
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	for _, setting := range info.Settings {
		if setting.Key == "-gcflags" && strings.Contains(setting.Value, "checkptr") {
			t.Skip("checkptr instrumentation changes allocation accounting")
		}
	}
}

func BenchmarkValueSlotRoundTrip(b *testing.B) {
	value := Number(3.14159)
	b.ReportAllocs()
	for range b.N {
		value = slotFromValue(value).owningValue()
	}
	runtime.KeepAlive(value)
}

func BenchmarkCachedString(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer state.Close()
	_ = state.String("destination")
	_ = state.String("destination")

	b.ReportAllocs()
	for range b.N {
		runtime.KeepAlive(state.String("destination"))
	}
}
