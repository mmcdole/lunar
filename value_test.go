package lua

import (
	"errors"
	"math"
	"reflect"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"
	"weak"
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

func TestTableVectorDescriptor(t *testing.T) {
	vector := makeTableVector[int](2, 5)
	if vector.len() != 2 || vector.cap() != 5 || vector.data == nil {
		t.Fatalf(
			"new vector = length:%d capacity:%d data:%p",
			vector.len(),
			vector.cap(),
			vector.data,
		)
	}
	values := vector.values()
	if cap(values) != len(values) {
		t.Fatalf(
			"vector view capacity = %d, want fixed length %d",
			cap(values),
			len(values),
		)
	}
	values[0], values[1] = 17, 23
	backing := vector.data
	vector = vector.withLength(5)
	values = vector.values()
	if vector.data != backing ||
		len(values) != 5 ||
		cap(values) != 5 ||
		values[0] != 17 ||
		values[1] != 23 {
		t.Fatalf(
			"grown vector = data:%p/%p length:%d capacity:%d values:%v",
			vector.data,
			backing,
			len(values),
			cap(values),
			values,
		)
	}
}

type tableVectorLifetimeMarker struct {
	id int
}

func TestTableVectorRetainsPointerBackingAcrossGC(t *testing.T) {
	finalized := make(chan int, 3)
	newMarker := func(id int) *tableVectorLifetimeMarker {
		marker := &tableVectorLifetimeMarker{id: id}
		runtime.SetFinalizer(
			marker,
			func(marker *tableVectorLifetimeMarker) {
				finalized <- marker.id
			},
		)
		return marker
	}

	array := makeTableVector[slot](1, 1)
	*array.at(0) = objectSlot(
		TableKind,
		unsafe.Pointer(newMarker(1)),
	)
	entries := makeTableVector[tableEntry](1, 1)
	*entries.at(0) = tableEntry{
		key: objectSlot(
			TableKind,
			unsafe.Pointer(newMarker(2)),
		),
		value: objectSlot(
			TableKind,
			unsafe.Pointer(newMarker(3)),
		),
		hash: 1,
	}

	for range 3 {
		runtime.GC()
	}
	select {
	case id := <-finalized:
		t.Fatalf("table vector lost marker %d during collection", id)
	default:
	}

	retained := []*tableVectorLifetimeMarker{
		(*tableVectorLifetimeMarker)(array.at(0).ref),
		(*tableVectorLifetimeMarker)(entries.at(0).key.ref),
		(*tableVectorLifetimeMarker)(entries.at(0).value.ref),
	}
	for index, marker := range retained {
		if marker.id != index+1 {
			t.Fatalf(
				"retained marker %d has id %d",
				index+1,
				marker.id,
			)
		}
		runtime.SetFinalizer(marker, nil)
	}
	runtime.KeepAlive(array)
	runtime.KeepAlive(entries)
}

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

	table, err := state.NewTable()
	if err != nil {
		t.Fatal(err)
	}
	tableValue := table.Value()
	gotTable, ok := tableValue.AsTable()
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
	gotData, ok := data.Value().AsUserData()
	if !ok || gotData != data || gotData.Data() != "payload" {
		t.Fatalf("userdata round trip failed")
	}

	main := state.MainThread()
	gotThread, ok := main.Value().AsThread()
	if !ok || gotThread != main || !main.IsMain() || main.State() != state {
		t.Fatal("main thread is not canonical")
	}

	runtime.GC()
	if got, ok := tableValue.AsTable(); !ok || got != table {
		t.Fatal("Value did not retain its canonical object across GC")
	}

	other, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	otherTable, err := other.NewTableWithCapacity(0, 0)
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
	if got := rawStr(table, "shared-string"); got.String() != "x" {
		t.Fatalf("state-neutral string = %v, want x", got)
	}
	if equal, err := state.RawEqual(shared, state.String("x")); err != nil || !equal {
		t.Fatalf("state-neutral equality after origin close = (%v, %v)", equal, err)
	}
	if err := table.RawSetString("number", Number(4)); err != nil {
		t.Fatalf("state-independent scalar rejected: %v", err)
	}
}

func TestUserDataOwningHandleRepresentation(t *testing.T) {
	if unsafe.Sizeof(UserData{}) != unsafe.Sizeof(hostToken{}) {
		t.Fatalf(
			"UserData size = %d; host token size = %d",
			unsafe.Sizeof(UserData{}),
			unsafe.Sizeof(hostToken{}),
		)
	}

	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	data, err := state.NewUserData("payload")
	if err != nil {
		t.Fatal(err)
	}
	token := data.token()
	if unsafe.Pointer(data) != unsafe.Pointer(token) {
		t.Fatal("UserData is not an offset-zero host-token view")
	}
	object := data.runtimeObject()
	if object == nil {
		t.Fatal("userdata handle has no compact object")
	}
	key := weak.Make(&object.objectHeader)
	if state.runtime.hosts.entries[key].Value() != token {
		t.Fatal("userdata object does not have its live token in the directory")
	}

	public := data.Value()
	compact := slotFromValue(public)
	if compact.ref != unsafe.Pointer(object) {
		t.Fatal("compact userdata slot does not point directly at its object")
	}
	if compact.ref == public.ref {
		t.Fatal("public userdata Value exposed the compact object pointer")
	}

	table, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := table.RawSetString("data", public); err != nil {
		t.Fatal(err)
	}
	fromTable, ok := rawStr(table, "data").AsUserData()
	if !ok || fromTable != data {
		t.Fatalf(
			"re-published userdata = (%p, %v); want (%p, true)",
			fromTable,
			ok,
			data,
		)
	}
	runtime.KeepAlive(data)
}

func TestTableOwningHandleRepresentation(t *testing.T) {
	if unsafe.Sizeof(Table{}) != unsafe.Sizeof(hostToken{}) {
		t.Fatalf(
			"Table size = %d; host token size = %d",
			unsafe.Sizeof(Table{}),
			unsafe.Sizeof(hostToken{}),
		)
	}

	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	table, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	token := table.token()
	if unsafe.Pointer(table) != unsafe.Pointer(token) {
		t.Fatal("Table is not an offset-zero host-token view")
	}
	object := table.runtimeObject()
	if object == nil {
		t.Fatal("table handle has no compact object")
	}
	if unsafe.Pointer(&object.objectHeader) != unsafe.Pointer(object) {
		t.Fatal("table object header is not at offset zero")
	}
	key := weak.Make(&object.objectHeader)
	if state.runtime.hosts.entries[key].Value() != token {
		t.Fatal("table object does not have its live token in the directory")
	}

	public := table.Value()
	compact := slotFromValue(public)
	if compact.ref != unsafe.Pointer(object) {
		t.Fatal("compact table slot does not point directly at its object")
	}
	if compact.ref == public.ref {
		t.Fatal("public table Value exposed the compact object pointer")
	}
	published, ok := compact.owningValue().AsTable()
	if !ok || published != table {
		t.Fatalf(
			"re-published table = (%p, %v); want (%p, true)",
			published,
			ok,
			table,
		)
	}
	runtime.KeepAlive(table)
}

func TestWarmTablePublicationDoesNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	object := newTable(state, 0, 0)
	first := object.owningHandle()
	compact := slotFromTableObject(object)

	var published *Table
	allocations := testing.AllocsPerRun(1_000, func() {
		value := compact.owningValue()
		published, _ = value.AsTable()
	})
	if allocations != 0 {
		t.Fatalf(
			"warm table publication allocated %.2f times",
			allocations,
		)
	}
	if published != first {
		t.Fatalf(
			"warm table publication = %p; want %p",
			published,
			first,
		)
	}
	runtime.KeepAlive(first)
}

func TestTableRepublishAfterOwningTokenDies(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	object, token := rootedTableWithoutHandle(t, state)
	index := newTable(state, 0, 1)
	index.rawSetSlot(slotFromTableObject(object), numberSlot(91))
	waitForWeakTableToken(t, object, token)

	first, ok := state.registry.rawGetStringValue("rooted table").AsTable()
	if !ok || first.runtimeObject() != object {
		t.Fatal("re-publication changed compact table identity")
	}
	second, ok := state.registry.rawGetStringValue("rooted table").AsTable()
	if !ok || second != first {
		t.Fatalf(
			"second re-publication = (%p, %v); want (%p, true)",
			second,
			ok,
			first,
		)
	}
	stored, err := index.rawGetValue(first.Value())
	if err != nil {
		t.Fatal(err)
	}
	if number, ok := stored.AsNumber(); !ok || number != 91 {
		t.Fatalf(
			"table-key lookup after token replacement = (%v, %v); want 91",
			number,
			ok,
		)
	}
	entries, keys, stale := hostDirectoryKindCounts(
		&state.runtime.hosts,
		TableKind,
	)
	if entries != 1 || keys != 1 || stale != 0 {
		t.Fatalf(
			"table directory = entries:%d keys:%d stale:%d; want 1/1/0",
			entries,
			keys,
			stale,
		)
	}
	runtime.KeepAlive(first)
}

func TestHostDirectoryDoesNotPinCyclicTable(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	object, token := weakTablePublication(t, state)
	waitForWeakTable(t, state, object, token)
	state.runtime.hosts.prune()
	entries, keys, stale := hostDirectoryKindCounts(
		&state.runtime.hosts,
		TableKind,
	)
	if entries != 0 || keys != 0 || stale != 0 {
		t.Fatalf(
			"dead table remains in host directory: entries=%d keys=%d stale=%d",
			entries,
			keys,
			stale,
		)
	}
	runtime.KeepAlive(state)
}

func TestTableHandleSupportsNestedPublicationAfterClose(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	outer, err := state.NewTableWithCapacity(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	outerObject := outer.runtimeObject()
	inner := newTable(state, 0, 1)
	inner.rawSetIntegerSlot(1, numberSlot(17))
	outerObject.rawSetIntegerSlot(1, numberSlot(11))
	if err := outerObject.rawSetStringSlot(
		"inner",
		slotFromTableObject(inner),
	); err != nil {
		t.Fatal(err)
	}
	longText := strings.Repeat("post-close-string-", 8)
	if err := outerObject.rawSetStringSlot(
		"long",
		stringSlot(state.runtime.strings.make(longText)),
	); err != nil {
		t.Fatal(err)
	}

	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if rawLen(outer) != 1 {
		t.Fatalf("post-close outer length = %d; want 1", rawLen(outer))
	}
	if number, ok := rawInt(outer, 1).AsNumber(); !ok || number != 11 {
		t.Fatalf("post-close scalar = (%v, %v); want 11", number, ok)
	}
	first, ok := rawStr(outer, "inner").AsTable()
	if !ok || first.runtimeObject() != inner {
		t.Fatal("post-close nested table was not published")
	}
	second, ok := rawStr(outer, "inner").AsTable()
	if !ok || second != first {
		t.Fatal("post-close nested table publication was not canonical")
	}
	if text, ok := rawStr(outer, "long").AsString(); !ok ||
		text != longText {
		t.Fatalf("post-close string = (%q, %v)", text, ok)
	}
	if state.runtime.collection.attributedStrings != nil ||
		state.runtime.collection.attributedStringHighWater != 0 {
		t.Fatal("post-close string read recreated collection attribution")
	}
	if err := outer.RawSetString("blocked", Bool(true)); !errors.Is(
		err,
		ErrClosed,
	) {
		t.Fatalf("post-close mutation = %v; want ErrClosed", err)
	}
	runtime.KeepAlive(outer)
	runtime.KeepAlive(first)
}

func TestLuaOnlyLibrariesDoNotPublishTableHandles(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	for _, open := range []func() error{
		state.OpenBase,
		state.OpenPackage,
		state.OpenTable,
		state.OpenString,
		state.OpenMath,
		state.OpenIO,
		state.OpenOS,
	} {
		if err := open(); err != nil {
			t.Fatal(err)
		}
	}
	if entries, keys, stale := hostDirectoryKindCounts(
		&state.runtime.hosts,
		TableKind,
	); entries != 0 || keys != 0 || stale != 0 {
		t.Fatalf(
			"opening libraries published tables: entries=%d keys=%d stale=%d",
			entries,
			keys,
			stale,
		)
	}

	chunk := mustLoadString(t, state, "@compact-tables.lua", `
local sequence={1,2,3}
table.insert(sequence,4)
package.preload.compact=function()
	return {answer=40}
end
local module=require("compact")
local file=assert(io.tmpfile())
assert(file:write("compact"))
assert(file:close())
local protected,caught=pcall(function()
	error(sequence,0)
end)
local thread=coroutine.create(function()
	error({thread=true},0)
end)
local resumed,raised=coroutine.resume(thread)
return module.answer+math.floor(2.9),
	not protected and caught==sequence,
	not resumed and type(raised)=="table"
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	assertTestValues(
		t,
		results,
		Number(42),
		Bool(true),
		Bool(true),
	)
	if entries, keys, stale := hostDirectoryKindCounts(
		&state.runtime.hosts,
		TableKind,
	); entries != 0 || keys != 0 || stale != 0 {
		t.Fatalf(
			"Lua-only execution published tables: entries=%d keys=%d stale=%d",
			entries,
			keys,
			stale,
		)
	}
}

func TestUserDataOwningHandleEnforcesStateOwnership(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	table, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}

	other, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	foreign, err := other.NewUserData("foreign")
	if err != nil {
		t.Fatal(err)
	}
	if err := table.RawSetString(
		"foreign",
		foreign.Value(),
	); !errors.Is(err, ErrForeignValue) {
		t.Fatalf("foreign userdata table value = %v; want ErrForeignValue", err)
	}

	var zero UserData
	for name, data := range map[string]*UserData{
		"nil":  nil,
		"zero": &zero,
	} {
		if data.Value().Valid() {
			t.Fatalf("%s userdata manufactured a valid Value", name)
		}
		if _, err := userDataEnvironment(
			data,
		); !errors.Is(err, ErrInvalidValue) {
			t.Fatalf("%s userdata environment = %v; want ErrInvalidValue", name, err)
		}
	}
}

func TestWarmUserDataPublicationDoesNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	data, err := state.NewUserData(nil)
	if err != nil {
		t.Fatal(err)
	}
	compact := slotFromValue(data.Value())
	var published *UserData
	allocations := testing.AllocsPerRun(1000, func() {
		value := compact.owningValue()
		published, _ = value.AsUserData()
	})
	if allocations != 0 {
		t.Fatalf(
			"warm userdata publication allocated %.2f times",
			allocations,
		)
	}
	if published != data {
		t.Fatalf("warm userdata publication = %p; want %p", published, data)
	}
	runtime.KeepAlive(data)
}

func TestUserDataHandleIdentitySurvivesStateClose(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := state.NewUserData("payload")
	if err != nil {
		t.Fatal(err)
	}
	table, err := state.NewTableWithCapacity(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := table.RawSetString("data", data.Value()); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	published, ok := rawStr(table, "data").AsUserData()
	if !ok || published != data {
		t.Fatalf(
			"post-close userdata = (%p, %v); want (%p, true)",
			published,
			ok,
			data,
		)
	}
	if got := published.Data(); got != "payload" {
		t.Fatalf("post-close userdata payload = %v; want payload", got)
	}
}

func TestHostDirectoryDoesNotPinUserData(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	object, token := weakUserDataPublication(t, state)
	waitForWeakUserData(t, state, []weak.Pointer[userDataObject]{object},
		[]weak.Pointer[hostToken]{token})
	state.runtime.hosts.prune()
	if len(state.runtime.hosts.entries) != 0 {
		t.Fatal("dead userdata publication remains in host directory")
	}
	runtime.KeepAlive(state)
}

func TestUserDataRepublishAfterOwningTokenDies(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	table, object, token := rootedUserDataWithoutHandle(t, state)
	waitForWeakUserDataToken(t, object, token)

	first, ok := table.rawGetStringValue("data").AsUserData()
	if !ok {
		t.Fatal("re-published compact userdata is not userdata")
	}
	if first.runtimeObject() != object.Value() {
		t.Fatal("re-publication changed compact userdata identity")
	}
	second, ok := table.rawGetStringValue("data").AsUserData()
	if !ok || second != first {
		t.Fatalf(
			"second re-publication = (%p, %v); want (%p, true)",
			second,
			ok,
			first,
		)
	}

	state.runtime.hosts.mutex.Lock()
	entryCount := len(state.runtime.hosts.entries)
	keyCount := len(state.runtime.hosts.keys)
	state.runtime.hosts.mutex.Unlock()
	if entryCount != 1 || keyCount != 1 {
		t.Fatalf(
			"re-published directory size = entries:%d keys:%d; want 1/1",
			entryCount,
			keyCount,
		)
	}
	runtime.KeepAlive(first)
	runtime.KeepAlive(table)
}

func TestConcurrentUserDataRepublishAfterStateClose(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	table, object, token := rootedUserDataWithoutHandle(t, state)
	waitForWeakUserDataToken(t, object, token)
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	const workers = 32
	start := make(chan struct{})
	published := make([]*UserData, workers)
	var group sync.WaitGroup
	group.Add(workers)
	for index := range published {
		go func() {
			defer group.Done()
			<-start
			value := table.rawGetStringValue("data")
			data, ok := value.AsUserData()
			if !ok {
				return
			}
			published[index] = data
		}()
	}
	close(start)
	group.Wait()

	first := published[0]
	if first == nil {
		t.Fatal("concurrent re-publication did not return userdata")
	}
	for index, data := range published {
		if data != first {
			t.Fatalf(
				"concurrent re-publication %d = %p; want %p",
				index,
				data,
				first,
			)
		}
	}
	if first.runtimeObject() != object.Value() {
		t.Fatal("post-close re-publication changed compact object identity")
	}
	runtime.KeepAlive(table)
}

func TestHostDirectoryIncrementalMaintenanceBoundsStaleMetadata(
	t *testing.T,
) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	const abandoned = 256
	objects := make([]weak.Pointer[userDataObject], abandoned)
	tokens := make([]weak.Pointer[hostToken], abandoned)
	for index := range objects {
		objects[index], tokens[index] = weakUserDataPublication(t, state)
	}
	waitForWeakUserData(t, state, objects, tokens)

	live := make([]*UserData, 0, abandoned*2)
	previousStale := abandoned
	for index := 0; index < abandoned*2 && previousStale != 0; index++ {
		data, createErr := state.NewUserData(index)
		if createErr != nil {
			t.Fatal(createErr)
		}
		live = append(live, data)

		entryCount, keyCount, stale := hostDirectoryCounts(
			&state.runtime.hosts,
		)
		if stale > previousStale {
			t.Fatalf(
				"maintenance step %d increased stale entries from %d to %d",
				index,
				previousStale,
				stale,
			)
		}
		if entryCount > len(live)+abandoned ||
			keyCount > len(live)+abandoned {
			t.Fatalf(
				"maintenance step %d left unbounded metadata entries:%d keys:%d live:%d",
				index,
				entryCount,
				keyCount,
				len(live),
			)
		}
		previousStale = stale
	}

	entryCount, keyCount, stale := hostDirectoryCounts(
		&state.runtime.hosts,
	)
	if stale != 0 {
		t.Fatalf(
			"incremental maintenance left %d stale entries after %d publications",
			stale,
			len(live),
		)
	}
	if entryCount != len(live) || keyCount != len(live) {
		t.Fatalf(
			"maintained directory size = entries:%d keys:%d; want %d/%d",
			entryCount,
			keyCount,
			len(live),
			len(live),
		)
	}
	runtime.KeepAlive(live)
}

func hostDirectoryCounts(
	directory *hostDirectory,
) (entries, keys, stale int) {
	directory.mutex.Lock()
	defer directory.mutex.Unlock()
	for object, token := range directory.entries {
		if object.Value() == nil || token.Value() == nil {
			stale++
		}
	}
	return len(directory.entries), len(directory.keys), stale
}

func hostDirectoryKindCounts(
	directory *hostDirectory,
	kind Kind,
) (entries, keys, staleAllKinds int) {
	directory.mutex.Lock()
	defer directory.mutex.Unlock()
	for object, reference := range directory.entries {
		token := reference.Value()
		if object.Value() == nil || token == nil {
			// A dead weak endpoint no longer carries enough information to
			// attribute the entry to one object kind.
			staleAllKinds++
			continue
		}
		if token.kind == kind {
			entries++
		}
	}
	for _, object := range directory.keys {
		reference, found := directory.entries[object]
		if !found {
			continue
		}
		token := reference.Value()
		if object.Value() != nil &&
			token != nil &&
			token.kind == kind {
			keys++
		}
	}
	return entries, keys, staleAllKinds
}

func rootedTableWithoutHandle(
	t *testing.T,
	state *State,
) (*tableObject, weak.Pointer[hostToken]) {
	t.Helper()
	object := newTable(state, 0, 0)
	if err := state.registry.rawSetStringSlot(
		"rooted table",
		slotFromTableObject(object),
	); err != nil {
		t.Fatal(err)
	}
	handle := object.owningHandle()
	token := weak.Make(handle.token())
	runtime.KeepAlive(handle)
	return object, token
}

func weakTablePublication(
	t *testing.T,
	state *State,
) (weak.Pointer[tableObject], weak.Pointer[hostToken]) {
	t.Helper()
	object := newTable(state, 0, 1)
	if err := object.rawSetStringSlot(
		"self",
		slotFromTableObject(object),
	); err != nil {
		t.Fatal(err)
	}
	handle := object.owningHandle()
	objectReference := weak.Make(object)
	tokenReference := weak.Make(handle.token())
	runtime.KeepAlive(handle)
	return objectReference, tokenReference
}

func waitForWeakTable(
	t *testing.T,
	state *State,
	object weak.Pointer[tableObject],
	token weak.Pointer[hostToken],
) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		runtime.GC()
		if token.Value() == nil {
			state.collectUnreachable()
		}
		if object.Value() == nil && token.Value() == nil {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("weak host directory pinned a discarded cyclic table")
		case <-ticker.C:
		}
	}
}

func waitForWeakTableToken(
	t *testing.T,
	object *tableObject,
	token weak.Pointer[hostToken],
) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		runtime.GC()
		if token.Value() == nil {
			if object == nil || object.owner == nil {
				t.Fatal("Lua-rooted compact table disappeared with its token")
			}
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("discarded table owning token remained reachable")
		case <-ticker.C:
		}
	}
}

func rootedUserDataWithoutHandle(
	t *testing.T,
	state *State,
) (
	*tableObject,
	weak.Pointer[userDataObject],
	weak.Pointer[hostToken],
) {
	t.Helper()
	table := state.registry
	data, err := state.NewUserData("payload")
	if err != nil {
		t.Fatal(err)
	}
	object := data.runtimeObject()
	token := data.token()
	if err := table.rawSetStringValue("data", data.Value()); err != nil {
		t.Fatal(err)
	}
	return table, weak.Make(object), weak.Make(token)
}

func waitForWeakUserDataToken(
	t *testing.T,
	object weak.Pointer[userDataObject],
	token weak.Pointer[hostToken],
) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		runtime.GC()
		if token.Value() == nil {
			if object.Value() == nil {
				t.Fatal("Lua-rooted compact userdata was collected with its token")
			}
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("discarded userdata owning token remained reachable")
		case <-ticker.C:
		}
	}
}

func waitForWeakUserData(
	t *testing.T,
	state *State,
	objects []weak.Pointer[userDataObject],
	tokens []weak.Pointer[hostToken],
) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		runtime.GC()
		allTokensDead := true
		for _, token := range tokens {
			if token.Value() != nil {
				allTokensDead = false
				break
			}
		}
		if allTokensDead {
			state.collectUnreachable()
		}
		allDead := true
		for index := range objects {
			if objects[index].Value() != nil ||
				tokens[index].Value() != nil {
				allDead = false
				break
			}
		}
		if allDead {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("weak host directory pinned discarded userdata")
		case <-ticker.C:
		}
	}
}

func weakUserDataPublication(
	t *testing.T,
	state *State,
) (
	weak.Pointer[userDataObject],
	weak.Pointer[hostToken],
) {
	t.Helper()
	data, err := state.NewUserData(nil)
	if err != nil {
		t.Fatal(err)
	}
	return weak.Make(data.runtimeObject()), weak.Make(data.token())
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

func BenchmarkWarmUserDataPublication(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer state.Close()
	data, err := state.NewUserData(nil)
	if err != nil {
		b.Fatal(err)
	}
	compact := slotFromValue(data.Value())

	var published Value
	b.ReportAllocs()
	for range b.N {
		published = compact.owningValue()
	}
	runtime.KeepAlive(published)
	runtime.KeepAlive(data)
}

func BenchmarkWarmTablePublication(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer state.Close()
	object := newTable(state, 0, 0)
	first := object.owningHandle()
	compact := slotFromTableObject(object)

	var published Value
	b.ReportAllocs()
	for range b.N {
		published = compact.owningValue()
	}
	runtime.KeepAlive(published)
	runtime.KeepAlive(first)
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
