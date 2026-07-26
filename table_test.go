package lua

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestTableRawScalarAccess(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	table, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		key   Value
		value Value
	}{
		{Number(0), state.String("zero")},
		{Number(-4), state.String("negative")},
		{Bool(true), Number(1)},
		{Bool(false), Number(2)},
		{state.String("name"), state.String("badger")},
	}
	for _, test := range tests {
		if err := table.RawSet(test.key, test.value); err != nil {
			t.Fatalf("RawSet(%v): %v", test.key, err)
		}
		got, err := table.RawGet(test.key)
		if err != nil {
			t.Fatalf("RawGet(%v): %v", test.key, err)
		}
		equal, err := state.RawEqual(got, test.value)
		if err != nil || !equal {
			t.Fatalf("RawGet(%v) = %v, want %v", test.key, got, test.value)
		}
	}

	if err := table.RawSet(Number(math.Copysign(0, -1)), Number(9)); err != nil {
		t.Fatal(err)
	}
	if got := table.RawGetInt(0); got.String() != "9" {
		t.Fatalf("-0/+0 canonicalization = %v, want 9", got)
	}
	if err := table.RawSet(Nil(), Bool(true)); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("nil key error = %v, want ErrInvalidKey", err)
	}
	if err := table.RawSet(Number(math.NaN()), Bool(true)); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("NaN key error = %v, want ErrInvalidKey", err)
	}
	for _, key := range []Value{Nil(), Number(math.NaN())} {
		got, err := table.RawGet(key)
		if err != nil || !got.IsNil() {
			t.Fatalf("RawGet(%v) = (%v, %v), want (nil, nil)", key, got, err)
		}
	}
	var invalid Value
	if err := table.RawSet(Bool(true), invalid); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("invalid value error = %v, want ErrInvalidValue", err)
	}
	if err := table.RawSet(invalid, Bool(true)); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("invalid key error = %v, want ErrInvalidValue", err)
	}
	other, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	foreign, err := other.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := table.RawSet(
		foreign.Value(),
		Bool(true),
	); !errors.Is(err, ErrForeignValue) {
		t.Fatalf("foreign key error = %v, want ErrForeignValue", err)
	}
}

func TestTableRetainsFlatStringKeysAcrossGC(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	table, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("dynamic flat key", func(t *testing.T) {
		keyText := strings.Repeat("dynamic-key-", 16)
		key := state.String(keyText)
		if stringSlotLen(slotFromValue(key)) <= shortStringLimit {
			t.Fatal("test key unexpectedly entered the short-string cache")
		}
		if err := table.RawSet(key, state.String("dynamic")); err != nil {
			t.Fatal(err)
		}

		key = Value{}
		keyText = ""
		for range 3 {
			runtime.GC()
		}

		lookup := state.String(strings.Repeat("dynamic-key-", 16))
		got, err := table.RawGet(lookup)
		if err != nil {
			t.Fatal(err)
		}
		if text, ok := got.AsString(); !ok || text != "dynamic" {
			t.Fatalf("dynamic-key lookup = %v", got)
		}
	})

	t.Run("long key with hash collision", func(t *testing.T) {
		const collisionHash stringHash = 29

		longText := strings.Repeat("l", stringLengthSentinel)
		longKey := stringValue(newHashedStringRef(
			longText,
			collisionHash,
		))
		shortKey := stringValue(newHashedStringRef(
			"short collision",
			collisionHash,
		))
		if rawEqual(longKey, shortKey) {
			t.Fatal("unequal strings with the same hash compared equal")
		}
		equalLong := stringValue(newHashedStringRef(
			strings.Clone(longText),
			collisionHash,
		))
		if !rawEqual(longKey, equalLong) {
			t.Fatal("equal long strings with different backing compared unequal")
		}
		if err := table.RawSet(longKey, Number(1)); err != nil {
			t.Fatal(err)
		}
		if err := table.RawSet(shortKey, Number(2)); err != nil {
			t.Fatal(err)
		}

		equalLong = Value{}
		longKey = Value{}
		shortKey = Value{}
		longText = ""
		for range 3 {
			runtime.GC()
		}

		equalLong = stringValue(newHashedStringRef(
			strings.Repeat("l", stringLengthSentinel),
			collisionHash,
		))
		got, err := table.RawGet(equalLong)
		if err != nil {
			t.Fatal(err)
		}
		if number, ok := got.AsNumber(); !ok || number != 1 {
			t.Fatalf("long-key lookup = %v, want 1", got)
		}
		got, err = table.RawGet(stringValue(newHashedStringRef(
			"short collision",
			collisionHash,
		)))
		if err != nil {
			t.Fatal(err)
		}
		if number, ok := got.AsNumber(); !ok || number != 2 {
			t.Fatalf("colliding short-key lookup = %v, want 2", got)
		}
	})
}

func TestTableMetamethodAbsenceCache(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	table, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}

	if err := table.RawSetString("ordinary", Number(1)); err != nil {
		t.Fatal(err)
	}
	if err := table.RawSetString("ordinary", Number(2)); err != nil {
		t.Fatal(err)
	}
	if got, ok := table.RawGetString("ordinary").AsNumber(); !ok || got != 2 {
		t.Fatalf("updated ordinary field = (%v, %v), want (2, true)", got, ok)
	}
	target, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.SetMetatable(target.Value(), table); err != nil {
		t.Fatal(err)
	}
	if _, found := metamethodSlot(
		state.MainThread(),
		slotFromValue(target.Value()),
		metaIndex,
	); found || table.absentMetamethods&metaIndex.bit() == 0 {
		t.Fatal("missing __index was not cached")
	}

	if err := table.RawSetInt(1, Number(1)); err != nil {
		t.Fatal(err)
	}
	if err := table.RawSetInt(1, Number(2)); err != nil {
		t.Fatal(err)
	}
	if err := table.RawSetInt(1, Nil()); err != nil {
		t.Fatal(err)
	}
	table.rawSetList(2, []slot{numberSlot(2), numberSlot(3)})
	if err := table.RawSetInt(-1, Number(1)); err != nil {
		t.Fatal(err)
	}
	if err := table.RawSetInt(-1, Nil()); err != nil {
		t.Fatal(err)
	}
	if table.absentMetamethods&metaIndex.bit() == 0 {
		t.Fatal("integer mutations invalidated cached string metamethod absence")
	}

	if err := table.RawSetString("__index", Number(1)); err != nil {
		t.Fatal(err)
	}
	if table.absentMetamethods&metaIndex.bit() != 0 {
		t.Fatal("metamethod insertion did not invalidate cached absence")
	}
	method, found := metamethodSlot(
		state.MainThread(),
		slotFromValue(target.Value()),
		metaIndex,
	)
	if !found || !rawSlotEqual(method, numberSlot(1)) {
		t.Fatal("inserted __index was not found")
	}
	if err := table.RawSetString("__index", Nil()); err != nil {
		t.Fatal(err)
	}
	if table.absentMetamethods&metaIndex.bit() != 0 {
		t.Fatal("metamethod deletion retained stale absence state")
	}
	if _, found := metamethodSlot(
		state.MainThread(),
		slotFromValue(target.Value()),
		metaIndex,
	); found || table.absentMetamethods&metaIndex.bit() == 0 {
		t.Fatal("deleted __index was not recached as absent")
	}
	if got := table.RawGetString("__index"); !got.IsNil() {
		t.Fatalf("deleted key = %v, want nil", got)
	}
	assertTableStoreInvariant(t, &table.store)
}

func TestTableResolvedLocationUpdatesExactStorage(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	tests := []struct {
		name string
		key  Value
		lane tableLane
	}{
		{name: "array", key: Number(1), lane: tableArrayLane},
		{name: "hash", key: state.String("field"), lane: tableHashLane},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			table, err := state.NewTable(1, 1)
			if err != nil {
				t.Fatal(err)
			}
			if err := table.RawSet(test.key, Number(1)); err != nil {
				t.Fatal(err)
			}

			normalized, index, arrayKey, hash, status :=
				normalizeTableKey(slotFromValue(test.key))
			if status != tableKeyValid {
				t.Fatal("valid key was rejected")
			}
			value, location, found := table.resolveNormalizedSlot(
				normalized,
				index,
				arrayKey,
				hash,
			)
			if !found || !rawSlotEqual(value, numberSlot(1)) {
				t.Fatalf("resolved value = (%v, %v), want (1, true)", value, found)
			}
			if location.lane != test.lane {
				t.Fatalf("resolved lane = %d, want %d", location.lane, test.lane)
			}

			arrayUsed := table.arrayUsed
			storeLive, storeDead := table.store.live, table.store.dead
			table.absentMetamethods = metaIndex.bit()
			table.replaceResolvedSlot(location, numberSlot(2))
			if table.arrayUsed != arrayUsed ||
				table.store.live != storeLive ||
				table.store.dead != storeDead {
				t.Fatalf(
					"value update changed storage accounting: "+
						"array %d/%d live %d/%d dead %d/%d",
					table.arrayUsed,
					arrayUsed,
					table.store.live,
					storeLive,
					table.store.dead,
					storeDead,
				)
			}
			if table.absentMetamethods != 0 {
				t.Fatal("value update retained the absent-metamethod cache")
			}
			got, err := table.RawGet(test.key)
			if err != nil {
				t.Fatal(err)
			}
			if number, ok := got.AsNumber(); !ok || number != 2 {
				t.Fatalf("updated value = %v, want 2", got)
			}
			_, updatedLocation, found := table.resolveNormalizedSlot(
				normalized,
				index,
				arrayKey,
				hash,
			)
			if !found || updatedLocation != location {
				t.Fatalf(
					"updated location = (%v, %v), want (%v, true)",
					updatedLocation,
					found,
					location,
				)
			}

			table.absentMetamethods = metaIndex.bit()
			table.replaceResolvedSlot(location, nilSlot)
			if table.absentMetamethods != 0 {
				t.Fatal("deletion retained the absent-metamethod cache")
			}
			got, err = table.RawGet(test.key)
			if err != nil {
				t.Fatal(err)
			}
			if !got.IsNil() {
				t.Fatalf("deleted value = %v, want nil", got)
			}
			if test.lane == tableArrayLane && table.arrayUsed != 0 {
				t.Fatalf("array used = %d, want 0", table.arrayUsed)
			}
			if test.lane == tableHashLane && table.store.live != 0 {
				t.Fatalf("hash count = %d, want 0", table.store.live)
			}
			assertTableStoreInvariant(t, &table.store)
		})
	}
}

func TestTableTraversalContinuesAfterDeletingCurrentKey(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	table, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 8; index++ {
		if err := table.RawSetInt(index, Number(float64(index))); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []string{"north", "south", "east", "west"} {
		if err := table.RawSetString(key, Bool(true)); err != nil {
			t.Fatal(err)
		}
	}

	visited := 0
	previous := nilSlot
	for {
		key, _, found, err := table.next(previous)
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			break
		}
		visited++
		if err := table.RawSet(key.owningValue(), Nil()); err != nil {
			t.Fatal(err)
		}
		previous = key
	}
	if visited != 12 {
		t.Fatalf("visited %d keys, want 12", visited)
	}
	if table.arrayUsed != 0 || table.store.live != 0 {
		t.Fatalf("table not empty after traversal deletion: array=%d hash=%d", table.arrayUsed, table.store.live)
	}

	missing := slotFromValue(state.String("missing"))
	if _, _, _, err := table.next(missing); !errors.Is(err, ErrInvalidNextKey) {
		t.Fatalf("missing continuation error = %v, want ErrInvalidNextKey", err)
	}

	// Lua permits deletion during traversal. Two paused traversals therefore
	// need independent continuation keys that survive deletion until an
	// insertion makes traversal order undefined.
	nested, err := state.NewTable(0, 8)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 8; index++ {
		key := "nested-" + strconv.Itoa(index)
		if err := nested.RawSetString(key, Number(float64(index))); err != nil {
			t.Fatal(err)
		}
	}
	outer, _, found, err := nested.next(nilSlot)
	if err != nil || !found {
		t.Fatalf("outer first next = (found %v, err %v)", found, err)
	}
	if err := nested.RawSet(outer.owningValue(), Nil()); err != nil {
		t.Fatal(err)
	}
	inner, _, found, err := nested.next(nilSlot)
	if err != nil || !found {
		t.Fatalf("inner first next = (found %v, err %v)", found, err)
	}
	if err := nested.RawSet(inner.owningValue(), Nil()); err != nil {
		t.Fatal(err)
	}
	collect := func(name string, previous slot) map[slot]struct{} {
		t.Helper()
		seen := make(map[slot]struct{}, 6)
		for {
			key, value, found, err := nested.next(previous)
			if err != nil {
				t.Fatalf("%s traversal: %v", name, err)
			}
			if !found {
				return seen
			}
			if value.kind() == NilKind {
				t.Fatalf("%s traversal returned a deleted field", name)
			}
			if _, duplicate := seen[key]; duplicate {
				t.Fatalf(
					"%s traversal repeated key %v",
					name,
					key.owningValue(),
				)
			}
			seen[key] = struct{}{}
			previous = key
		}
	}
	remaining := collect("fresh", nilSlot)
	for _, traversal := range []struct {
		name string
		seen map[slot]struct{}
	}{
		{name: "outer", seen: collect("outer", outer)},
		{name: "inner", seen: collect("inner", inner)},
	} {
		if len(traversal.seen) != len(remaining) {
			t.Fatalf(
				"%s traversal visited %d remaining fields, want %d",
				traversal.name,
				len(traversal.seen),
				len(remaining),
			)
		}
		for key := range remaining {
			if _, found := traversal.seen[key]; !found {
				t.Fatalf(
					"%s traversal missed key %v",
					traversal.name,
					key.owningValue(),
				)
			}
		}
	}
	assertTableStoreInvariant(t, &nested.store)
}

func TestTableTraversalContinuesAfterUpdatingExistingKey(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	table, err := state.NewTable(0, 64)
	if err != nil {
		t.Fatal(err)
	}
	if err := table.RawSetInt(5, Number(1)); err != nil {
		t.Fatal(err)
	}
	if err := table.RawSetInt(1, Number(1)); err != nil {
		t.Fatal(err)
	}
	if err := table.RawSetInt(2, Number(2)); err != nil {
		t.Fatal(err)
	}
	for index := range 32 {
		if err := table.RawSetString(
			fmt.Sprintf("field-%02d", index),
			Number(float64(index)),
		); err != nil {
			t.Fatal(err)
		}
	}

	seen := make(map[slot]struct{}, 35)
	previous := nilSlot
	updated := false
	for {
		key, _, found, err := table.next(previous)
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			break
		}
		if _, duplicate := seen[key]; duplicate {
			t.Fatalf(
				"next repeated %v after an existing-key update",
				key.owningValue(),
			)
		}
		seen[key] = struct{}{}
		if key.kind() == NumberKind &&
			math.Float64frombits(key.bits) == 5 {
			if err := table.RawSetInt(5, Number(2)); err != nil {
				t.Fatal(err)
			}
			updated = true
		}
		previous = key
	}
	if !updated {
		t.Fatal("traversal never reached sparse integer key 5")
	}
	if len(seen) != 35 {
		t.Fatalf("traversal visited %d fields, want 35", len(seen))
	}
	if got, ok := table.RawGetInt(5).AsNumber(); !ok || got != 2 {
		t.Fatalf("updated value = (%v, %v), want (2, true)", got, ok)
	}
}

func TestLuaNextContinuesAfterRawSetUpdatesExistingKey(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.OpenBase(); err != nil {
		t.Fatal(err)
	}
	chunk := mustLoadString(t, state, "@next-update.lua", `
local fields = {[5]=1, [1]=1, [2]=2}
for index=0,31 do
	fields["field-" .. index] = index
end

local seen = {}
local key = nil
local count = 0
while true do
	key = next(fields, key)
	if key == nil then
		break
	end
	if seen[key] then
		return false, key
	end
	seen[key] = true
	count = count + 1
	if key == 5 then
		rawset(fields, 5, 2)
	end
end
return true, count
`)
	results, err := state.Call(chunk.Value())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("result count = %d, want 2", len(results))
	}
	ok, isBool := results[0].AsBool()
	if !isBool || !ok {
		t.Fatalf("rawset caused next to repeat key %v", results[1])
	}
	count, isNumber := results[1].AsNumber()
	if !isNumber || count != 35 {
		t.Fatalf("next visited (%v, %v), want (35, true)", count, isNumber)
	}
}

func TestTableTraversalAcceptsArrayHoleAsContinuation(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	table, err := state.NewTable(4, 0)
	if err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 4; index++ {
		if err := table.RawSetInt(index, Number(float64(index))); err != nil {
			t.Fatal(err)
		}
	}
	if err := table.RawSetInt(3, Nil()); err != nil {
		t.Fatal(err)
	}

	key, value, found, err := table.next(numberSlot(3))
	if err != nil {
		t.Fatal(err)
	}
	if !found ||
		math.Float64frombits(key.bits) != 4 ||
		math.Float64frombits(value.bits) != 4 {
		t.Fatalf(
			"next after array hole = (%v, %v, %v), want (4, 4, true)",
			key,
			value,
			found,
		)
	}

	if err := table.RawSetInt(4, Nil()); err != nil {
		t.Fatal(err)
	}
	if _, _, found, err := table.next(numberSlot(3)); err != nil || found {
		t.Fatalf("next after trailing array holes = (found %v, err %v)", found, err)
	}
}

func TestTableRawLenSequence(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	table, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}

	for index := 1; index <= 64; index++ {
		if err := table.RawSetInt(index, Bool(true)); err != nil {
			t.Fatal(err)
		}
	}
	if got := table.RawLen(); got != 64 {
		t.Fatalf("RawLen = %d, want 64", got)
	}
	if err := table.RawSetInt(64, Nil()); err != nil {
		t.Fatal(err)
	}
	if got := table.RawLen(); got != 63 {
		t.Fatalf("RawLen after tail deletion = %d, want 63", got)
	}

	hashTail, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 3; index++ {
		if err := hashTail.RawSetInt(index, Bool(true)); err != nil {
			t.Fatal(err)
		}
	}
	number := float64(4)
	hashTail.store.init(1)
	if !hashTail.store.insertAbsent(
		numberSlot(number),
		slotFromValue(Bool(true)),
		hashNumber(number),
	) {
		t.Fatal("failed to seed record tail")
	}
	hashTail.recordIntegerInserted(numberSlot(number))
	if got := hashTail.RawLen(); got != 4 {
		t.Fatalf("RawLen across storage = %d, want 4", got)
	}
	assertTableLaneInvariant(t, hashTail)
}

func TestTableSteadyStateRawAccessDoesNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	table, err := state.NewTable(8, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := table.RawSetInt(1, Number(1)); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		if err := table.RawSetString("field", Number(1)); err != nil {
			t.Fatal(err)
		}
	}
	if capacity := len(table.store.entries); capacity != minimumStoreCapacity {
		t.Fatalf("one-field store capacity = %d, want %d", capacity, minimumStoreCapacity)
	}

	allocations := testing.AllocsPerRun(1000, func() {
		if err := table.RawSetInt(1, Number(2)); err != nil {
			panic(err)
		}
		_ = table.RawGetInt(1)
		if err := table.RawSetString("field", Number(2)); err != nil {
			panic(err)
		}
		_ = table.RawGetString("field")
		if err := table.RawSetString("absent", Nil()); err != nil {
			panic(err)
		}
	})
	if allocations != 0 {
		t.Fatalf("steady-state raw access allocated %.2f times", allocations)
	}
}

func BenchmarkTableRawInteger(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer state.Close()
	table, err := state.NewTable(1, 0)
	if err != nil {
		b.Fatal(err)
	}
	if err := table.RawSetInt(1, Number(0)); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	for index := range b.N {
		if err := table.RawSetInt(1, Number(float64(index))); err != nil {
			b.Fatal(err)
		}
		runtime.KeepAlive(table.RawGetInt(1))
	}
}

func BenchmarkTableRawString(b *testing.B) {
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer state.Close()
	table, err := state.NewTable(0, 1)
	if err != nil {
		b.Fatal(err)
	}
	if err := table.RawSetString("field", Number(0)); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	for index := range b.N {
		if err := table.RawSetString("field", Number(float64(index))); err != nil {
			b.Fatal(err)
		}
		runtime.KeepAlive(table.RawGetString("field"))
	}
}

func BenchmarkTableNext(b *testing.B) {
	for _, count := range []int{16, 256, 5_000} {
		keys := make([]string, count)
		for index := range keys {
			keys[index] = "next-key-" + strconv.Itoa(index)
		}
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			state, table := benchmarkStringTable(b, keys)
			defer state.Close()
			b.ReportAllocs()
			b.ReportMetric(float64(count), "keys/op")
			b.ResetTimer()
			for range b.N {
				previous := nilSlot
				visited := 0
				for {
					key, value, found, err := table.next(previous)
					if err != nil {
						b.Fatal(err)
					}
					if !found {
						break
					}
					previous = key
					visited++
					runtime.KeepAlive(value)
				}
				if visited != count {
					b.Fatalf("visited %d keys, want %d", visited, count)
				}
			}
		})
	}

	const deleteCount = 256
	keys := make([]string, deleteCount)
	for index := range keys {
		keys[index] = "delete-next-key-" + strconv.Itoa(index)
	}
	b.Run("delete-current/256", func(b *testing.B) {
		state, err := New(Options{})
		if err != nil {
			b.Fatal(err)
		}
		defer state.Close()
		b.ReportAllocs()
		b.ReportMetric(deleteCount, "keys/op")
		for range b.N {
			table, err := state.NewTable(0, deleteCount)
			if err != nil {
				b.Fatal(err)
			}
			for index, key := range keys {
				if err := table.RawSetString(
					key,
					Number(float64(index)),
				); err != nil {
					b.Fatal(err)
				}
			}
			previous := nilSlot
			visited := 0
			for {
				key, _, found, err := table.next(previous)
				if err != nil {
					b.Fatal(err)
				}
				if !found {
					break
				}
				if err := table.RawSet(key.owningValue(), Nil()); err != nil {
					b.Fatal(err)
				}
				previous = key
				visited++
			}
			if visited != deleteCount {
				b.Fatalf("visited %d keys, want %d", visited, deleteCount)
			}
		}
	})
}
