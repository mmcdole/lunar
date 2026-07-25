package lua

import (
	"math"
	"testing"
)

func TestParseLuaNumber(t *testing.T) {
	tests := []struct {
		text string
		want float64
	}{
		{"0", 0},
		{"-0", math.Copysign(0, -1)},
		{"+0.01", 0.01},
		{"+.01", 0.01},
		{".01", 0.01},
		{"+1.", 1},
		{"-1.", -1},
		{"-12", -12},
		{"-1.2e2", -120},
		{"+1.23E30", 1.23e30},
		{"1.3e-2", 0.013},
		{"1e2", 100},
		{"2.5E-2", 0.025},
		{"9007199254740993", 9007199254740992},
		{"  +0.001e+3 \n\t", 1},
		{"\v\f\r 42", 42},
		{"0x10", 16},
		{"-0Xf", -15},
		{"+0xABC", 2748},
		{"-0x000", math.Copysign(0, -1)},
		{"0x20000000000001", 1 << 53},
		{"0x20000000000003", 1<<53 + 4},
	}

	for _, test := range tests {
		t.Run(test.text, func(t *testing.T) {
			got, ok := parseLuaNumber(test.text)
			if !ok {
				t.Fatalf("parseLuaNumber(%q) rejected a valid number", test.text)
			}
			if math.Float64bits(got) != math.Float64bits(test.want) {
				t.Fatalf(
					"parseLuaNumber(%q) = %v (%x), want %v (%x)",
					test.text,
					got,
					math.Float64bits(got),
					test.want,
					math.Float64bits(test.want),
				)
			}
		})
	}
}

func TestParseLuaNumberRejectsNonLuaForms(t *testing.T) {
	for _, text := range []string{
		"",
		" \t\n",
		"+",
		"-",
		".",
		"e1",
		"1e",
		"1e+",
		"+.e1",
		"+ 0.01",
		"1 2",
		"1 a",
		"e 1",
		"3.4.5",
		"1foo",
		"1\x00",
		"\x001",
		"NaN",
		"nan",
		"Inf",
		"+Inf",
		"infinity",
		"1_0",
		"0x",
		"0xg",
		"0x1p0",
		"0x1.0",
		"0x1_0",
		"+ 1",
		"\u00a01\u00a0",
	} {
		if got, ok := parseLuaNumber(text); ok {
			t.Errorf("parseLuaNumber(%q) = %v, want rejection", text, got)
		}
	}
}

func TestParseLuaNumberAcceptsRangeResults(t *testing.T) {
	for _, test := range []struct {
		text string
		want float64
	}{
		{"1e4000", math.Inf(1)},
		{"-1e4000", math.Inf(-1)},
		{"1e-4000", 0},
		{"-1e-4000", math.Copysign(0, -1)},
	} {
		got, ok := parseLuaNumber(test.text)
		if !ok {
			t.Fatalf("parseLuaNumber(%q) rejected a range result", test.text)
		}
		if math.Float64bits(got) != math.Float64bits(test.want) {
			t.Fatalf("parseLuaNumber(%q) = %v, want %v", test.text, got, test.want)
		}
	}

	const hex = "-0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	got, ok := parseLuaNumber(hex)
	if !ok || !math.IsInf(got, -1) {
		t.Fatalf("parseLuaNumber(large hex) = (%v, %v), want (-Inf, true)", got, ok)
	}
}

func TestAppendLuaNumberUsesDeterministicLua51Formatting(t *testing.T) {
	for _, test := range []struct {
		name   string
		number float64
		want   string
	}{
		{"fixed lower boundary", 1e-4, "0.0001"},
		{"exponent lower boundary", 1e-5, "1e-05"},
		{"fixed upper boundary", 1e13, "10000000000000"},
		{"exponent upper boundary", 1e14, "1e+14"},
		{"rounding", 1.234567890123456, "1.2345678901235"},
		{
			"smallest subnormal",
			math.SmallestNonzeroFloat64,
			"4.9406564584125e-324",
		},
		{"negative zero", math.Copysign(0, -1), "-0"},
		{"positive infinity", math.Inf(1), "inf"},
		{"negative infinity", math.Inf(-1), "-inf"},
		{"not a number", math.NaN(), "nan"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := string(appendLuaNumber(nil, test.number))
			if got != test.want {
				t.Fatalf(
					"appendLuaNumber(%v) = %q; want %q",
					test.number,
					got,
					test.want,
				)
			}
		})
	}
}

func TestSlotToNumber(t *testing.T) {
	number := slot{bits: math.Float64bits(-12.5)}
	if got, ok := slotToNumber(number); !ok || got != -12.5 {
		t.Fatalf("numeric slot = (%v, %v), want (-12.5, true)", got, ok)
	}

	numericString := prototypeStringSlot(newLuaString(" 0x20 "))
	if got, ok := slotToNumber(numericString); !ok || got != 32 {
		t.Fatalf("numeric string = (%v, %v), want (32, true)", got, ok)
	}

	for _, value := range []slot{
		prototypeStringSlot(newLuaString("12x")),
		nilSlot,
		falseSlot,
	} {
		if got, ok := slotToNumber(value); ok {
			t.Errorf("slotToNumber(%v) = (%v, true), want rejection", value.kind(), got)
		}
	}
}

var (
	parsedNumberSink float64
	parsedOKSink     bool
	parsedSlot       = prototypeStringSlot(newLuaString(" +12345.625e-2 "))
)

func TestParseLuaNumberDoesNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	for _, text := range []string{
		" +12345.625e-2 ",
		" -0x123456789abcdef ",
	} {
		allocations := testing.AllocsPerRun(1000, func() {
			parsedNumberSink, parsedOKSink = parseLuaNumber(text)
		})
		if !parsedOKSink {
			t.Fatalf("parseLuaNumber(%q) failed", text)
		}
		if allocations != 0 {
			t.Fatalf("parseLuaNumber(%q) allocated %.2f times, want 0", text, allocations)
		}
	}

	allocations := testing.AllocsPerRun(1000, func() {
		parsedNumberSink, parsedOKSink = slotToNumber(parsedSlot)
	})
	if !parsedOKSink || parsedNumberSink != 123.45625 {
		t.Fatalf(
			"slotToNumber = (%v, %v), want (123.45625, true)",
			parsedNumberSink,
			parsedOKSink,
		)
	}
	if allocations != 0 {
		t.Fatalf("slotToNumber allocated %.2f times, want 0", allocations)
	}
}
