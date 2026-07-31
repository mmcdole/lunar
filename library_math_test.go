package lua

import (
	"errors"
	"math"
	"testing"
)

func TestMathLibraryInstallationAndSurface(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	before, err := state.RawGlobal("math")
	if err != nil {
		t.Fatal(err)
	}
	if !before.IsNil() {
		t.Fatalf("new state math = %v; want nil", before)
	}
	loadedBeforeOpen := mustLoadString(
		t,
		state,
		"@open-math.lua",
		`return math.floor(2.5)`,
	)
	if err := state.OpenMath(); err != nil {
		t.Fatal(err)
	}

	libraryValue, err := state.RawGlobal("math")
	if err != nil {
		t.Fatal(err)
	}
	library, ok := libraryValue.AsTable()
	if !ok {
		t.Fatalf("math = %v; want table", libraryValue)
	}

	// The surface is exactly Lua 5.1's: every registered entry, the mod alias
	// the standard distribution publishes, and the two constants.
	want := map[string]Kind{"pi": NumberKind, "huge": NumberKind}
	for _, definition := range mathLibraryFunctions {
		want[definition.name] = FunctionKind
	}
	for _, definition := range mathLibraryRandomFunctions {
		want[definition.name] = FunctionKind
	}
	want["mod"] = FunctionKind
	previous := make(map[string]Value, len(want))
	for name, kind := range want {
		value := rawStr(library, name)
		if value.Kind() != kind {
			t.Fatalf("math.%s = %v; want %v", name, value, kind)
		}
		previous[name] = value
	}
	found := 0
	for key := nilSlot; ; {
		nextKey, _, present, err := library.runtimeObject().next(key)
		if err != nil {
			t.Fatal(err)
		}
		if !present {
			break
		}
		name, isString := nextKey.owningValue().AsString()
		if !isString {
			t.Fatalf("math has a non-string key %v", nextKey.owningValue())
		}
		if _, expected := want[name]; !expected {
			t.Fatalf("math.%s is not part of the Lua 5.1 surface", name)
		}
		found++
		key = nextKey
	}
	if found != len(want) {
		t.Fatalf("math has %d entries; want %d", found, len(want))
	}

	if same, applicable := previous["mod"].SameObject(
		previous["fmod"],
	); !applicable || !same {
		t.Fatal("math.mod is not the canonical math.fmod Function")
	}
	pi, _ := previous["pi"].AsNumber()
	if pi != math.Pi {
		t.Fatalf("math.pi = %v; want %v", pi, math.Pi)
	}
	huge, _ := previous["huge"].AsNumber()
	if !math.IsInf(huge, 1) {
		t.Fatalf("math.huge = %v; want +Inf", huge)
	}

	results, err := state.Call(loadedBeforeOpen.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, results, Number(2))

	// Reopening replaces the table and every Function with fresh canonical
	// objects, as the other library openers do.
	if err := state.RawSetGlobal("math", Number(1)); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenMath(); err != nil {
		t.Fatal(err)
	}
	reopenedValue, err := state.RawGlobal("math")
	if err != nil {
		t.Fatal(err)
	}
	reopened, ok := reopenedValue.AsTable()
	if !ok {
		t.Fatalf("reopened math = %v; want table", reopenedValue)
	}
	if same, applicable := libraryValue.SameObject(
		reopenedValue,
	); !applicable || same {
		t.Fatal("reopening did not replace the math table")
	}
	for name, kind := range want {
		if kind != FunctionKind {
			continue
		}
		value := rawStr(reopened, name)
		if same, applicable := previous[name].SameObject(
			value,
		); !applicable || same {
			t.Fatalf("reopened math.%s is not a fresh Function", name)
		}
	}

	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenMath(); !errors.Is(err, ErrClosed) {
		t.Fatalf("OpenMath after Close = %v; want ErrClosed", err)
	}
}

func TestMathLibraryMatchesLua51(t *testing.T) {
	runLua51Cases(t, mathLibraryLua51Cases)
}

// TestMathLibraryInheritsTheDeterministicNumberGrammar records the one place
// the math library's argument coercion deliberately differs from PUC Lua 5.1.
// Runtime number coercion is the runtime's own documented grammar (number.go),
// which rejects the locale-dependent and hexadecimal-float spellings C's
// strtod happens to accept. The library must not add a second grammar.
func TestMathLibraryInheritsTheDeterministicNumberGrammar(t *testing.T) {
	rejected := []string{
		"math.floor('0x1p4')", // PUC accepts a hexadecimal float here.
		"math.floor('inf')",
		"math.floor('nan')",
		"math.floor('1e')",
		"math.floor('1\\0')",
	}
	for _, source := range rejected {
		got := runLua51Case(t, "return "+source)
		const want = "error 'case:1: bad argument #1 to 'floor' " +
			"(number expected, got string)'"
		if got != want {
			t.Errorf("%s\n got: %s\nwant: %s", source, got, want)
		}
	}
}

// TestMathLibraryIntegerArgumentsAreDefinedEverywhere records the second
// deliberate divergence. PUC's luaL_checkint casts the coerced double straight
// to C int, which is undefined for NaN and outside that range; the observed
// PUC result there is a platform artifact rather than Lua 5.1 behavior.
// Truncating and saturating gives every input one defined, portable result.
func TestMathLibraryIntegerArgumentsAreDefinedEverywhere(t *testing.T) {
	testCases := []struct {
		source string
		want   string
	}{
		// Saturating the exponent gives ldexp's mathematical limits.
		{source: "return math.ldexp(3, 1e308)", want: "ok inf"},
		{source: "return math.ldexp(3, -1e308)", want: "ok 0"},
		{source: "return math.ldexp(-3, 1e308)", want: "ok -inf"},
		{source: "return math.ldexp(3, 0/0)", want: "ok 3"},
		// In-range values keep C's truncation toward zero.
		{source: "return math.ldexp(1, 2.9), math.ldexp(1, -2.9)", want: "ok 4 0.25"},
		{source: "return math.ldexp(1, 2147483647)", want: "ok inf"},
		{source: "return math.ldexp(1, -2147483648)", want: "ok 0"},
		// A saturated interval bound stays an interval.
		{source: "local v = math.random(1e308) return v >= 1", want: "ok true"},
		{source: "return math.random(0/0)", want: "error 'case:1: bad argument #1 to 'random' (interval is empty)'"},
		// Seeding with a non-integral or out-of-range value is defined and
		// still reproducible.
		{source: "math.randomseed(1e308) local a = math.random() math.randomseed(1e308) return a == math.random()", want: "ok true"},
		{source: "math.randomseed(2.9) local a = math.random() math.randomseed(2) return a == math.random()", want: "ok true"},
	}
	for _, test := range testCases {
		if got := runLua51Case(t, test.source); got != test.want {
			t.Errorf("%s\n got: %s\nwant: %s", test.source, got, test.want)
		}
	}
}

// TestMathLibraryRandomIsPerLibraryAndReproducible covers what a differential
// case cannot: Lua 5.1 delegates math.random to C rand(), so only the
// interface is portable. The generator itself is Lunar's, and it must be
// deterministic from a seed, private to each opened library, and shared by
// random and randomseed.
func TestMathLibraryRandomIsPerLibraryAndReproducible(t *testing.T) {
	sequence := func(t *testing.T, state *State, source string) []Value {
		t.Helper()
		chunk := mustLoadString(t, state, "@random.lua", source)
		results, err := state.Call(chunk.Value())
		if err != nil {
			t.Fatal(err)
		}
		return results
	}
	const draw = `
math.randomseed(1234)
local out = {}
for index = 1, 8 do out[index] = math.random() end
return out[1], out[2], out[3], out[4], out[5], out[6], out[7], out[8]
`

	first := newStateWithMath(t)
	defer first.Close()
	second := newStateWithMath(t)
	defer second.Close()

	seeded := sequence(t, first, draw)
	if len(seeded) != 8 {
		t.Fatalf("draw produced %d results; want 8", len(seeded))
	}
	for index, value := range seeded {
		number, ok := value.AsNumber()
		if !ok || !(number >= 0 && number < 1) {
			t.Fatalf("draw %d = %v; want a number in [0, 1)", index, value)
		}
	}
	assertTestValues(t, sequence(t, second, draw), seeded...)
	assertTestValues(t, sequence(t, first, draw), seeded...)

	// An unseeded library starts from one fixed seed, so a program that never
	// calls randomseed still runs reproducibly.
	third := newStateWithMath(t)
	defer third.Close()
	fourth := newStateWithMath(t)
	defer fourth.Close()
	const unseeded = `return math.random(), math.random()`
	assertTestValues(
		t,
		sequence(t, fourth, unseeded),
		sequence(t, third, unseeded)...,
	)

	// Reopening the library installs a new generator rather than continuing
	// the old stream, matching how reopening replaces every other object.
	fifth := newStateWithMath(t)
	defer fifth.Close()
	before := sequence(t, fifth, unseeded)
	if err := fifth.OpenMath(); err != nil {
		t.Fatal(err)
	}
	assertTestValues(t, sequence(t, fifth, unseeded), before...)

	// Two libraries in one State draw from separate streams.
	sixth := newStateWithMath(t)
	defer sixth.Close()
	held, err := sixth.RawGlobal("math")
	if err != nil {
		t.Fatal(err)
	}
	if err := sixth.OpenMath(); err != nil {
		t.Fatal(err)
	}
	if err := sixth.RawSetGlobal("held", held); err != nil {
		t.Fatal(err)
	}
	independent := sequence(t, sixth, `
math.randomseed(5)
local a = math.random()
held.randomseed(5)
local b = held.random()
local c = math.random()
local d = held.random()
return a == b, c == d, a == c
`)
	assertTestValues(
		t,
		independent,
		Bool(true),
		Bool(true),
		Bool(false),
	)
}

// TestMathLibraryRandomGeneratorSequenceIsStable pins the generator named in
// OpenMath's contract. Changing either the SplitMix64 seeding or xoshiro256**
// transition is an observable compatibility change, even though PUC's C rand
// sequence is deliberately not part of Lua 5.1.
func TestMathLibraryRandomGeneratorSequenceIsStable(t *testing.T) {
	source := &randomSource{}
	source.seed(1234)
	want := [...]uint64{
		0x0bab45d9a0e3ae53,
		0xd7c640660c19433e,
		0xb0dedaa0d09a6691,
		0xdec9f41b58ec86eb,
		0x19e4a6b7acda0ae0,
		0xe4bc1c79fd36e5cb,
		0x737261121dbf96e7,
		0x33dc37ab08116070,
	}
	for index, expected := range want {
		if got := source.nextBits(); got != expected {
			t.Fatalf(
				"seed 1234 draw %d = %#016x; want %#016x",
				index+1,
				got,
				expected,
			)
		}
	}
}

// TestWarmMathLibraryScalarCallsDoNotAllocate holds the scalar fast path to
// the compact contract: an exact-typed argument and a number result cross the
// native boundary without materializing an owning Value.
func TestWarmMathLibraryScalarCallsDoNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	state := newStateWithMath(t)
	defer state.Close()

	testCases := []struct {
		name   string
		source string
	}{
		{name: "one argument", source: `
local floor = math.floor
return function()
	local total = 0
	for index = 1, 100 do total = total + floor(index + 0.5) end
	return total
end
`},
		{name: "two arguments", source: `
local fmod = math.fmod
return function()
	local total = 0
	for index = 1, 100 do total = total + fmod(index, 7) end
	return total
end
`},
		{name: "variadic scan", source: `
local max = math.max
return function()
	local total = 0
	for index = 1, 100 do total = total + max(index, 3, 9) end
	return total
end
`},
		{name: "two results", source: `
local modf = math.modf
return function()
	local total = 0
	for index = 1, 100 do
		local whole, fraction = modf(index + 0.25)
		total = total + whole + fraction
	end
	return total
end
`},
		{name: "generator", source: `
local random = math.random
return function()
	local total = 0
	for index = 1, 100 do total = total + random(10) end
	return total
end
`},
		{name: "string coercion", source: `
local abs = math.abs
return function()
	local total = 0
	for index = 1, 100 do total = total + abs("-2.5") end
	return total
end
`},
	}

	for _, test := range testCases {
		t.Run(test.name, func(t *testing.T) {
			chunk := mustLoadString(t, state, "@math-alloc.lua", test.source)
			results, err := state.Call(chunk.Value())
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 {
				t.Fatalf("loader produced %d results; want 1", len(results))
			}
			body := results[0]
			var destination [1]Value
			for index := 0; index < 64; index++ {
				if _, err := state.CallInto(
					body,
					nil,
					destination[:],
				); err != nil {
					t.Fatal(err)
				}
			}
			allocations := testing.AllocsPerRun(64, func() {
				if _, err := state.CallInto(
					body,
					nil,
					destination[:],
				); err != nil {
					t.Fatal(err)
				}
			})
			if allocations != 0 {
				t.Fatalf("warm calls allocated %v times per run", allocations)
			}
		})
	}
}

func newStateWithMath(t *testing.T) *State {
	t.Helper()
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}
	if err := state.OpenMath(); err != nil {
		t.Fatal(err)
	}
	return state
}

var mathLibraryLua51Cases = []lua51Case{
	{
		name:   "abs",
		source: "return math.abs(-3), math.abs(3), math.abs(0), math.abs(-0.5)",
		want:   "ok 3 3 0 0.5",
	},
	{
		name:   "abs_negative_zero",
		source: "local z = 0.0 local n = -z return math.abs(n), 1/math.abs(n), 1/n",
		want:   "ok 0 inf -inf",
	},
	{
		name:   "abs_infinity",
		source: "return math.abs(-1/0), math.abs(0/0)",
		want:   "ok inf nan",
	},
	{
		name:   "ceil_floor",
		source: "return math.ceil(1.2), math.ceil(-1.2), math.floor(1.8), math.floor(-1.8)",
		want:   "ok 2 -1 1 -2",
	},
	{
		name:   "ceil_floor_integers",
		source: "return math.ceil(3), math.floor(-3), math.ceil(-0.5), 1/math.ceil(-0.5)",
		want:   "ok 3 -3 -0 -inf",
	},
	{
		name:   "sqrt",
		source: "return math.sqrt(4), math.sqrt(2), math.sqrt(0), math.sqrt(-1)",
		want:   "ok 2 1.4142135623731 0 nan",
	},
	{
		name:   "pow",
		source: "return math.pow(2, 10), math.pow(2, 0.5), math.pow(-8, 1/3), math.pow(0, 0)",
		want:   "ok 1024 1.4142135623731 nan 1",
	},
	{
		name:   "pow_agrees_with_the_operator",
		source: "return math.pow(2, 0.5) == 2^0.5, math.pow(7, 3) == 7^3, math.pow(-2, 3) == (-2)^3, math.pow(0.375, -4.25) == 0.375^-4.25",
		want:   "ok true true true true",
	},
	{
		name:   "fmod",
		source: "return math.fmod(7, 3), math.fmod(-7, 3), math.fmod(7, -3), math.fmod(-7, -3)",
		want:   "ok 1 -1 1 -1",
	},
	{
		name:   "fmod_edges",
		source: "return math.fmod(7, 0), math.fmod(1/0, 2), math.fmod(2, 1/0)",
		want:   "ok nan nan 2",
	},
	{
		name:   "fmod_is_not_the_modulo_operator",
		source: "return math.fmod(-7, 3), -7 % 3",
		want:   "ok -1 2",
	},
	{
		name:   "mod_alias",
		source: "return math.mod == math.fmod, math.mod(7, 3)",
		want:   "ok true 1",
	},
	{
		name:   "modf",
		source: "return math.modf(3.7)",
		want:   "ok 3 0.7",
	},
	{
		name:   "modf_negative",
		source: "return math.modf(-3.7)",
		want:   "ok -3 -0.7",
	},
	{
		name:   "modf_integral",
		source: "return math.modf(5)",
		want:   "ok 5 0",
	},
	{
		name:   "modf_infinity",
		source: "return math.modf(1/0)",
		want:   "ok inf 0",
	},
	{
		name:   "modf_negative_infinity",
		source: "return math.modf(-1/0)",
		want:   "ok -inf -0",
	},
	{
		name:   "modf_infinite_fraction_sign",
		source: "local i, f = math.modf(-1/0) return i, 1/f",
		want:   "ok -inf -inf",
	},
	{
		name:   "frexp",
		source: "return math.frexp(8)",
		want:   "ok 0.5 4",
	},
	{
		name:   "frexp_zero",
		source: "return math.frexp(0)",
		want:   "ok 0 0",
	},
	{
		name:   "frexp_negative",
		source: "return math.frexp(-1234.5)",
		want:   "ok -0.602783203125 11",
	},
	{
		name:   "frexp_infinity",
		source: "return math.frexp(1/0)",
		want:   "ok inf 0",
	},
	{
		name:   "frexp_roundtrip",
		source: "local m, e = math.frexp(1234.5) return math.ldexp(m, e)",
		want:   "ok 1234.5",
	},
	{
		name:   "ldexp",
		source: "return math.ldexp(1, 3), math.ldexp(3, -2), math.ldexp(1, 0)",
		want:   "ok 8 0.75 1",
	},
	{
		name:   "ldexp_truncates_exponent",
		source: "return math.ldexp(1, 2.9), math.ldexp(1, -2.9)",
		want:   "ok 4 0.25",
	},
	{
		name:   "ldexp_extremes",
		source: "return math.ldexp(1, 5000), math.ldexp(1, -5000)",
		want:   "ok inf 0",
	},
	{
		name:   "max_min",
		source: "return math.max(1, 2, 3), math.min(1, 2, 3), math.max(7), math.min(7)",
		want:   "ok 3 1 7 7",
	},
	{
		name:   "max_min_nan_first",
		source: "return math.max(0/0, 1), math.min(0/0, 1)",
		want:   "ok nan nan",
	},
	{
		name:   "max_min_nan_later",
		source: "return math.max(1, 0/0), math.min(1, 0/0)",
		want:   "ok 1 1",
	},
	{
		name:   "max_min_zero_signs",
		source: "local z = 0.0 local n = -z return 1/math.max(z, n), 1/math.min(n, z)",
		want:   "ok inf -inf",
	},
	{
		name:   "deg_rad",
		source: "return math.deg(math.pi), math.rad(180), math.deg(1), math.rad(1)",
		want:   "ok 180 3.1415926535898 57.295779513082 0.017453292519943",
	},
	{
		name:   "deg_rad_roundtrip",
		source: "return math.rad(math.deg(1))",
		want:   "ok 1",
	},
	{
		name:   "pi_huge",
		source: "return math.pi, math.huge, -math.huge, math.huge == 1/0",
		want:   "ok 3.1415926535898 inf -inf true",
	},
	{
		name:   "exp_log",
		source: "return math.exp(0), math.log(1), math.log10(1000), math.log10(1)",
		want:   "ok 1 0 3 0",
	},
	{
		name:   "log_edges",
		source: "return math.log(0), math.log(-1), math.exp(1/0), math.exp(-1/0)",
		want:   "ok -inf nan inf 0",
	},
	{
		name:   "trig_zero",
		source: "return math.sin(0), math.cos(0), math.tan(0), math.sinh(0), math.cosh(0), math.tanh(0)",
		want:   "ok 0 1 0 0 1 0",
	},
	{
		name:   "inverse_trig",
		source: "return math.asin(0), math.acos(1), math.atan(0), math.atan2(0, 1)",
		want:   "ok 0 0 0 0",
	},
	{
		name:   "atan2_quadrants",
		source: "return math.atan2(1, 0) == math.pi/2, math.atan2(0, -1) == math.pi",
		want:   "ok true true",
	},
	{
		name:   "tanh_saturates",
		source: "return math.tanh(1/0), math.tanh(-1/0)",
		want:   "ok 1 -1",
	},
	{
		name:   "string_coercion",
		source: "return math.abs('-3'), math.floor(' 2.7 '), math.max('1', 2)",
		want:   "ok 3 2 2",
	},
	{
		name:   "hex_string_coercion",
		source: "return math.abs('0x10'), math.floor('0x1f')",
		want:   "ok 16 31",
	},
	{
		name:   "rejects_trailing_text",
		source: "return math.abs('10a')",
		want:   "error 'case:1: bad argument #1 to 'abs' (number expected, got string)'",
	},
	{
		name:   "rejects_empty_string",
		source: "return math.abs('')",
		want:   "error 'case:1: bad argument #1 to 'abs' (number expected, got string)'",
	},
	{
		name:   "missing_argument",
		source: "return math.abs()",
		want:   "error 'case:1: bad argument #1 to 'abs' (number expected, got no value)'",
	},
	{
		name:   "nil_argument",
		source: "return math.abs(nil)",
		want:   "error 'case:1: bad argument #1 to 'abs' (number expected, got nil)'",
	},
	{
		name:   "table_argument",
		source: "return math.abs({})",
		want:   "error 'case:1: bad argument #1 to 'abs' (number expected, got table)'",
	},
	{
		name:   "boolean_argument",
		source: "return math.floor(true)",
		want:   "error 'case:1: bad argument #1 to 'floor' (number expected, got boolean)'",
	},
	{
		name:   "second_argument_missing",
		source: "return math.atan2(1)",
		want:   "error 'case:1: bad argument #2 to 'atan2' (number expected, got no value)'",
	},
	{
		name:   "second_argument_wrong",
		source: "return math.fmod(1, {})",
		want:   "error 'case:1: bad argument #2 to 'fmod' (number expected, got table)'",
	},
	{
		name:   "named_through_local",
		source: "local m = math return m.sqrt('x')",
		want:   "error 'case:1: bad argument #1 to 'sqrt' (number expected, got string)'",
	},
	{
		name:   "named_through_method_self",
		source: "local o = {abs = math.abs} return o:abs()",
		want:   "error 'case:1: calling 'abs' on bad self (number expected, got table)'",
	},
	{
		name:   "modf_negative_zero",
		source: "local z = 0.0 local n = -z local i, f = math.modf(n) return 1/i, 1/f",
		want:   "ok -inf -inf",
	},
	{
		name:   "max_reports_the_failing_index",
		source: "return math.max(1, 2, {})",
		want:   "error 'case:1: bad argument #3 to 'max' (number expected, got table)'",
	},
	{
		name:   "min_needs_one_argument",
		source: "return math.min()",
		want:   "error 'case:1: bad argument #1 to 'min' (number expected, got no value)'",
	},
	{
		name:   "random_unit_range",
		source: "local r = math.random() return r >= 0 and r < 1",
		want:   "ok true",
	},
	{
		name:   "random_upper_range",
		source: "local ok = true for i = 1, 200 do local v = math.random(6) ok = ok and v == math.floor(v) and v >= 1 and v <= 6 end return ok",
		want:   "ok true",
	},
	{
		name:   "random_interval_range",
		source: "local ok = true for i = 1, 200 do local v = math.random(-3, 3) ok = ok and v == math.floor(v) and v >= -3 and v <= 3 end return ok",
		want:   "ok true",
	},
	{
		name:   "random_single_point",
		source: "return math.random(4, 4), math.random(1, 1)",
		want:   "ok 4 1",
	},
	{
		name:   "random_empty_upper",
		source: "return math.random(0)",
		want:   "error 'case:1: bad argument #1 to 'random' (interval is empty)'",
	},
	{
		name:   "random_empty_negative",
		source: "return math.random(-1)",
		want:   "error 'case:1: bad argument #1 to 'random' (interval is empty)'",
	},
	{
		name:   "random_empty_interval",
		source: "return math.random(2, 1)",
		want:   "error 'case:1: bad argument #2 to 'random' (interval is empty)'",
	},
	{
		name:   "random_arity",
		source: "return math.random(1, 2, 3)",
		want:   "error 'case:1: wrong number of arguments'",
	},
	{
		name:   "random_truncates_bounds",
		source: "local ok = true for i = 1, 100 do local v = math.random(3.9) ok = ok and v >= 1 and v <= 3 end return ok",
		want:   "ok true",
	},
	{
		name:   "randomseed_is_repeatable",
		source: "math.randomseed(7) local a = math.random() math.randomseed(7) return a == math.random()",
		want:   "ok true",
	},
	{
		name:   "randomseed_returns_nothing",
		source: "return math.randomseed(1)",
		want:   "ok",
	},
	{
		name:   "randomseed_needs_a_number",
		source: "return math.randomseed('x')",
		want:   "error 'case:1: bad argument #1 to 'randomseed' (number expected, got string)'",
	},
	{
		name:   "random_error_still_advances",
		source: "math.randomseed(3) local a = math.random() math.randomseed(3) pcall(math.random, 0) return a == math.random()",
		want:   "ok false",
	},
}
