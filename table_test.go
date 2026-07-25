package lua

import (
	"errors"
	"math"
	"runtime"
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

func TestTableDenseAndSparseIntegerPolicy(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	table, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}

	const denseCount = 10_000
	for index := 1; index <= denseCount; index++ {
		if err := table.RawSetInt(index, Number(float64(index))); err != nil {
			t.Fatal(err)
		}
	}
	if got := table.RawLen(); got != denseCount {
		t.Fatalf("dense RawLen = %d, want %d", got, denseCount)
	}
	if len(table.array) != denseCount || table.arrayUsed != denseCount {
		t.Fatalf("dense storage = len %d, used %d", len(table.array), table.arrayUsed)
	}

	sparse, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	const sparseIndex = 50_000_000
	if err := sparse.RawSetInt(sparseIndex, Bool(true)); err != nil {
		t.Fatal(err)
	}
	if len(sparse.array) != 0 {
		t.Fatalf("sparse assignment materialized %d array slots", len(sparse.array))
	}
	if got, ok := sparse.RawGetInt(sparseIndex).AsBool(); !ok || !got {
		t.Fatalf("sparse lookup = (%v, %v), want (true, true)", got, ok)
	}
	if sparse.store.count != 1 {
		t.Fatalf("sparse hash count = %d, want 1", sparse.store.count)
	}
}

func TestTableMovesExistingIntegerFromHashToArray(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	table, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}

	if err := table.RawSetInt(5, Number(1)); err != nil {
		t.Fatal(err)
	}
	if table.store.count != 1 || len(table.array) != 0 {
		t.Fatalf("initial sparse storage = hash %d, array %d", table.store.count, len(table.array))
	}
	if err := table.RawSetInt(1, Bool(true)); err != nil {
		t.Fatal(err)
	}
	if err := table.RawSetInt(2, Bool(true)); err != nil {
		t.Fatal(err)
	}
	version := table.structuralVersion
	if err := table.RawSetInt(5, Number(2)); err != nil {
		t.Fatal(err)
	}
	if table.store.count != 0 || table.store.integerKeys != 0 {
		t.Fatalf(
			"migrated key remains in hash: count=%d integerKeys=%d",
			table.store.count,
			table.store.integerKeys,
		)
	}
	if len(table.array) != 5 {
		t.Fatalf("migrated array length = %d, want 5", len(table.array))
	}
	if got, ok := table.RawGetInt(5).AsNumber(); !ok || got != 2 {
		t.Fatalf("migrated value = (%v, %v), want (2, true)", got, ok)
	}
	if table.structuralVersion != version {
		t.Fatalf(
			"storage move changed logical structural version from %d to %d",
			version,
			table.structuralVersion,
		)
	}
}

func TestTableHashGrowthDeletionAndIdentity(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	table, err := state.NewTable(0, 1)
	if err != nil {
		t.Fatal(err)
	}

	const count = 5000
	for index := 0; index < count; index++ {
		key := "key-" + Number(float64(index)).String()
		if err := table.RawSetString(key, Number(float64(index))); err != nil {
			t.Fatal(err)
		}
	}
	for index := 0; index < count; index++ {
		key := "key-" + Number(float64(index)).String()
		if got, ok := table.RawGetString(key).AsNumber(); !ok || got != float64(index) {
			t.Fatalf("lookup %q = %v", key, table.RawGetString(key))
		}
	}
	for index := 0; index < count; index += 2 {
		key := "key-" + Number(float64(index)).String()
		if err := table.RawSetString(key, Nil()); err != nil {
			t.Fatal(err)
		}
	}
	for index := count; index < count*2; index++ {
		key := "key-" + Number(float64(index)).String()
		if err := table.RawSetString(key, Number(float64(index))); err != nil {
			t.Fatal(err)
		}
	}

	objectKey, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := table.RawSet(objectKey.Value(), state.String("identity")); err != nil {
		t.Fatal(err)
	}
	got, err := table.RawGet(objectKey.Value())
	if err != nil {
		t.Fatal(err)
	}
	if text, ok := got.AsString(); !ok || text != "identity" {
		t.Fatalf("reference-key lookup = %v", got)
	}
}

func TestTableMutationBookkeepingAndMetamethodCache(t *testing.T) {
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
	if table.structuralVersion != 1 {
		t.Fatalf("initial structural version = %d", table.structuralVersion)
	}
	if err := table.RawSetString("ordinary", Number(2)); err != nil {
		t.Fatal(err)
	}
	if table.structuralVersion != 1 {
		t.Fatalf("value update changed structural version to %d", table.structuralVersion)
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

			version := table.structuralVersion
			table.absentMetamethods = metaIndex.bit()
			table.replaceResolvedSlot(location, numberSlot(2))
			if table.structuralVersion != version {
				t.Fatalf(
					"value update changed structural version from %d to %d",
					version,
					table.structuralVersion,
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

			table.absentMetamethods = metaIndex.bit()
			table.replaceResolvedSlot(location, nilSlot)
			if table.structuralVersion != version+1 {
				t.Fatalf(
					"deletion structural version = %d, want %d",
					table.structuralVersion,
					version+1,
				)
			}
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
			if test.lane == tableHashLane && table.store.count != 0 {
				t.Fatalf("hash count = %d, want 0", table.store.count)
			}
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
	if table.arrayUsed != 0 || table.store.count != 0 {
		t.Fatalf("table not empty after traversal deletion: array=%d hash=%d", table.arrayUsed, table.store.count)
	}

	missing := slotFromValue(state.String("missing"))
	if _, _, _, err := table.next(missing); !errors.Is(err, ErrInvalidNextKey) {
		t.Fatalf("missing continuation error = %v, want ErrInvalidNextKey", err)
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
	if err := hashTail.RawSetInt(4, Bool(true)); err != nil {
		t.Fatal(err)
	}
	if got := hashTail.RawLen(); got != 4 {
		t.Fatalf("RawLen across storage = %d, want 4", got)
	}
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
