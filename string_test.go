package lua

import (
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unsafe"
)

func TestFlatStringRepresentation(t *testing.T) {
	const collisionHash stringHash = 7

	backing := strings.Clone("prefix")
	short := stringSlot(newHashedStringRef(backing[:3], collisionHash))
	longer := stringSlot(newHashedStringRef(backing[:4], collisionHash))
	if short.ref != longer.ref {
		t.Fatal("test strings do not share their data pointer")
	}
	if rawSlotEqual(short, longer) {
		t.Fatal("one data pointer with different lengths compared equal")
	}

	collision := stringSlot(newHashedStringRef("other", collisionHash))
	if rawSlotEqual(short, collision) {
		t.Fatal("different strings with one hash compared equal")
	}
	equal := stringSlot(newHashedStringRef(
		strings.Clone("pre"),
		collisionHash,
	))
	if !rawSlotEqual(short, equal) {
		t.Fatal("equal strings with different backing storage compared unequal")
	}
	runtime.KeepAlive(backing)
}

func TestStringHashMatchesTextAndByteInputs(t *testing.T) {
	for _, text := range []string{
		"",
		"destination",
		string([]byte{0, 1, 0x7f, 0x80, 0xff}),
		strings.Repeat("bounded-hash-", 128),
	} {
		fromText := hashString(text)
		fromBytes := hashBytes([]byte(text))
		if fromText == 0 || fromBytes == 0 {
			t.Fatalf("zero hash for %d-byte string", len(text))
		}
		if fromText != fromBytes {
			t.Fatalf(
				"hash mismatch for %d-byte string: %08x != %08x",
				len(text),
				fromText,
				fromBytes,
			)
		}
	}
	if hash := finalizeStringHash(0); hash == 0 {
		t.Fatal("zero sampled hash was not normalized")
	}
}

func TestFlatStringLengthBoundarySurvivesGC(t *testing.T) {
	const testHash stringHash = 17

	tests := []struct {
		name        string
		length      int
		wantEncoded int
		wantFlat    bool
	}{
		{
			name:        "largest flat string",
			length:      stringLengthSentinel - 1,
			wantEncoded: stringLengthSentinel - 1,
			wantFlat:    true,
		},
		{
			name:        "sentinel",
			length:      stringLengthSentinel,
			wantEncoded: stringLengthSentinel,
		},
		{
			name:        "above sentinel",
			length:      stringLengthSentinel + 1,
			wantEncoded: stringLengthSentinel,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			text := strings.Repeat("x", test.length)
			reference := newHashedStringRef(text, testHash)
			encoded := int(
				reference.bits >> stringLengthShift &
					stringLengthSentinel,
			)
			if encoded != test.wantEncoded {
				t.Fatalf(
					"encoded length = %d, want %d",
					encoded,
					test.wantEncoded,
				)
			}
			if flat := reference.ref ==
				unsafe.Pointer(unsafe.StringData(text)); flat != test.wantFlat {
				t.Fatalf("flat storage = %v, want %v", flat, test.wantFlat)
			}
			if stringLength(reference.ref, reference.bits) != len(text) ||
				stringText(reference.ref, reference.bits) != text {
				t.Fatal("string boundary representation changed its contents")
			}

			// The reference is the only surviving owner of the bytes. Both
			// the interior-pointer and long-string forms must keep them live.
			text = ""
			for range 3 {
				runtime.GC()
			}
			retained := stringText(reference.ref, reference.bits)
			if len(retained) != test.length ||
				retained[0] != 'x' ||
				retained[len(retained)-1] != 'x' {
				t.Fatal("string boundary representation lost its backing")
			}
			runtime.KeepAlive(reference)
		})
	}
}

func TestFlatStringSurvivesGCStateCloseAndCrossState(t *testing.T) {
	origin, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Clone(strings.Repeat("retained-", 128))
	value := origin.String(text)
	text = ""
	if err := origin.Close(); err != nil {
		t.Fatal(err)
	}

	for range 3 {
		runtime.GC()
	}
	want := strings.Repeat("retained-", 128)
	if got, ok := value.AsString(); !ok || got != want {
		t.Fatalf("retained string = (%q, %v)", got, ok)
	}

	consumer, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()
	if err := consumer.RawSetGlobal("shared", value); err != nil {
		t.Fatalf("cross-State string: %v", err)
	}
	got, err := consumer.RawGlobal("shared")
	if err != nil {
		t.Fatal(err)
	}
	if equal, err := consumer.RawEqual(got, consumer.String(want)); err != nil ||
		!equal {
		t.Fatalf("cross-State equality = (%v, %v)", equal, err)
	}
	if origin.String("").ref != consumer.String("").ref {
		t.Fatal("empty string is not canonical across States")
	}
}

func TestPackageStringIsStateNeutral(t *testing.T) {
	text := strings.Clone(strings.Repeat("package-", 128))
	value := String(text)
	text = ""

	for range 3 {
		runtime.GC()
	}
	want := strings.Repeat("package-", 128)
	if got, ok := value.AsString(); !ok || got != want {
		t.Fatalf("package string = (%q, %v)", got, ok)
	}

	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.RawSetGlobal("shared", value); err != nil {
		t.Fatalf("share package String with State: %v", err)
	}
	got, err := state.RawGlobal("shared")
	if err != nil {
		t.Fatal(err)
	}
	if equal, err := state.RawEqual(got, state.String(want)); err != nil ||
		!equal {
		t.Fatalf("cross-State equality = (%v, %v)", equal, err)
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

func TestSingleByteStringsUseCanonicalStorage(t *testing.T) {
	firstState, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer firstState.Close()
	secondState, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer secondState.Close()

	for value := 0; value <= 255; value++ {
		firstText := strings.Clone(string([]byte{byte(value)}))
		secondText := strings.Clone(string([]byte{byte(value)}))
		first := firstState.String(firstText)
		second := secondState.String(secondText)
		if text, ok := first.AsString(); !ok || text != firstText {
			t.Fatalf("byte %d decoded as %q, %t", value, text, ok)
		}
		if first.ref != second.ref || first.bits != second.bits {
			t.Fatalf(
				"byte %d used distinct storage: (%p, %#x) and (%p, %#x)",
				value,
				first.ref,
				first.bits,
				second.ref,
				second.bits,
			)
		}
	}
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
