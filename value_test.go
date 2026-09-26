package lua

import (
	"math"
	"reflect"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
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
		if size := unsafe.Sizeof(tableVector[slot]{}); size != 16 {
			t.Fatalf("table vector size = %d, want 16", size)
		}
		if size := unsafe.Sizeof(tableStore{}); size != 32 {
			t.Fatalf("table store size = %d, want 32", size)
		}
		if size := unsafe.Sizeof(tableObject{}); size != 80 {
			t.Fatalf("table size = %d, want 80", size)
		}
		if size := unsafe.Sizeof(hostToken{}); size != 24 {
			t.Fatalf("host token size = %d, want 24", size)
		}
		if size := unsafe.Sizeof(userDataObject{}); size != 56 {
			t.Fatalf("compact userdata size = %d, want 56", size)
		}
		if size := unsafe.Sizeof(stringRef{}); size != 16 {
			t.Fatalf("stringRef size = %d, want 16", size)
		}
		if size := unsafe.Sizeof((*internedText)(nil)); size != 8 {
			t.Fatalf("compiler text reference size = %d, want 8", size)
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

func TestSlotTypedPredicatesMatchCanonicalKinds(t *testing.T) {
	marker := new(byte)
	reference := unsafe.Pointer(marker)
	tests := []struct {
		name  string
		value slot
		want  Kind
	}{
		{name: "nil", value: nilSlot, want: NilKind},
		{name: "false", value: falseSlot, want: BoolKind},
		{name: "true", value: trueSlot, want: BoolKind},
		{name: "number", value: numberSlot(17), want: NumberKind},
		{
			name:  "number with table kind bits",
			value: slot{bits: uint64(TableKind)},
			want:  NumberKind,
		},
		{
			name:  "string",
			value: slot{ref: reference, bits: uint64(StringKind)},
			want:  StringKind,
		},
		{
			name:  "function",
			value: objectSlot(FunctionKind, reference),
			want:  FunctionKind,
		},
		{
			name: "native function flag",
			value: slot{
				ref:  reference,
				bits: uint64(FunctionKind) | nativeFunctionSlotFlag,
			},
			want: FunctionKind,
		},
		{
			name:  "userdata",
			value: objectSlot(UserDataKind, reference),
			want:  UserDataKind,
		},
		{
			name:  "thread",
			value: objectSlot(ThreadKind, reference),
			want:  ThreadKind,
		},
		{
			name:  "table",
			value: objectSlot(TableKind, reference),
			want:  TableKind,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.value.kind(); got != test.want {
				t.Fatalf("kind = %s; want %s", got, test.want)
			}
			got := map[Kind]bool{
				NilKind:      test.value.isNil(),
				NumberKind:   test.value.isNumber(),
				StringKind:   test.value.isString(),
				FunctionKind: test.value.isFunction(),
				UserDataKind: test.value.isUserData(),
				ThreadKind:   test.value.isThread(),
				TableKind:    test.value.isTable(),
			}
			for kind, match := range got {
				if match != (test.want == kind) {
					t.Fatalf(
						"is%s = %v for %s",
						kind,
						match,
						test.want,
					)
				}
			}
		})
	}
}

type tableVectorLifetimeMarker struct {
	id int
}

func TestInvalidValueCannotEnterCompactStorage(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("slotFromValue accepted an invalid Value")
		}
	}()
	_ = slotFromValue(Value{})
}

func closeStateWithUnrelatedRoot(collected chan<- struct{}) Value {
	state, err := New(Options{})
	if err != nil {
		panic(err)
	}
	unrelated, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		panic(err)
	}
	runtime.SetFinalizer(unrelated, func(*Table) {
		collected <- struct{}{}
	})
	if err := state.RawSetGlobal("unrelated", unrelated.Value()); err != nil {
		panic(err)
	}
	retained := state.String("retained")
	if err := state.Close(); err != nil {
		panic(err)
	}
	return retained
}

func TestValueSlotConversionDoesNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	table, err := state.NewTableWithCapacity(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	function, err := state.NewNativeFunction(
		func(Frame) Outcome { return Outcome{} },
	)
	if err != nil {
		t.Fatal(err)
	}
	values := []Value{
		Nil(),
		Bool(false),
		Bool(true),
		Number(1.25),
		state.String("cached"),
		function.Value(),
		state.MainThread().Value(),
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

func TestHostNaNPayloadsCannotForgeScalarSlots(t *testing.T) {
	state, err := New(Options{Libraries: CoreLibraries()})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	// Each payload would reach a reserved nil/false/true pattern directly,
	// through negation, or through quieting.
	for _, bits := range []uint64{
		nilSlotBits,
		falseSlotBits,
		trueSlotBits,
		0x7fffffffffffffff,
		0x7ffffffffffffffd,
		0xfff7ffffffffffff,
		0x7ff7fffffffffffe,
	} {
		value := Number(math.Float64frombits(bits))
		converted := slotFromValue(value)
		if !converted.isNumber() || converted.bits|1<<63|1<<51 >= firstReservedSlotBits {
			t.Fatalf("host NaN %#x became slot %#x", bits, converted.bits)
		}
		if err := state.SetGlobal("x", value); err != nil {
			t.Fatal(err)
		}
		results, err := state.DoString("=nan", `
			local y = -x
			return type(x), type(y), type(math.abs(x)), type(x + 1), x ~= x, y ~= y
		`)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"number", "number", "number", "number", "true", "true"}
		for index, result := range results {
			if got := result.String(); got != want[index] {
				t.Fatalf("%#x result %d = %s, want %s", bits, index, got, want[index])
			}
		}
	}
	for _, bits := range []uint64{0x7ff8000000000001, 0x7ff8000000000042, 0xfff8000000000000} {
		if got := canonicalNumberBits(bits); got != bits {
			t.Fatalf("safe NaN %#x canonicalized to %#x", bits, got)
		}
	}
}

// Number pairs whose bit patterns OR into the reserved scalar range miss the
// interpreter's single-compare bothNumbers test and must still behave as
// numbers on the slow paths.
func TestNumberPairsOutsideBothNumbersFastPath(t *testing.T) {
	state, err := New(Options{Libraries: CoreLibraries()})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	pairs := [][2]uint64{
		{0xfff0000000000000, 0x000fffffffffffff}, // -Inf, largest subnormal
		{0x000fffffffffffff, 0xfff0000000000000},
		{0xfff8000000000000, 0x0007ffffffffffff}, // -NaN, subnormal
		{0xfffffffffffffff0, 0x000000000000000f}, // payload NaN, subnormal
		{0xfffffffffffffff0, 0xfff000000000000f}, // two NaNs
	}
	for _, pair := range pairs {
		a := math.Float64frombits(pair[0])
		b := math.Float64frombits(pair[1])
		if bothNumbers(numberSlot(a), numberSlot(b)) {
			t.Fatalf("%#x, %#x unexpectedly took the fast path", pair[0], pair[1])
		}
		if err := state.SetGlobal("a", Number(a)); err != nil {
			t.Fatal(err)
		}
		if err := state.SetGlobal("b", Number(b)); err != nil {
			t.Fatal(err)
		}
		results, err := state.DoString("=pairs", `
			local a, b = a, b
			local t = {}
			t[#t+1] = a < b
			t[#t+1] = a <= b
			t[#t+1] = a > b
			t[#t+1] = a >= b
			t[#t+1] = a == b
			t[#t+1] = a ~= b
			t[#t+1] = type(a + b)
			t[#t+1] = type(a - b)
			t[#t+1] = type(a * b)
			t[#t+1] = type(a / b)
			t[#t+1] = type(a % b)
			return unpack(t)
		`)
		if err != nil {
			t.Fatalf("%#x, %#x: %v", pair[0], pair[1], err)
		}
		want := []string{
			strconv.FormatBool(a < b),
			strconv.FormatBool(a <= b),
			strconv.FormatBool(a > b),
			strconv.FormatBool(a >= b),
			strconv.FormatBool(a == b),
			strconv.FormatBool(a != b),
			"number", "number", "number", "number", "number",
		}
		if len(results) != len(want) {
			t.Fatalf("%#x, %#x: %d results", pair[0], pair[1], len(results))
		}
		for index, result := range results {
			if got := result.String(); got != want[index] {
				t.Fatalf("%#x, %#x result %d = %s, want %s",
					pair[0], pair[1], index, got, want[index])
			}
		}
	}
}
