package lua

import (
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// setFixture inserts into a standalone store for collision-chain tests.
// Production mutation goes through Table so array and record sizing remain
// coordinated.
func (store *tableStore) setFixture(
	key slot,
	value slot,
	hash uint32,
) (inserted, changed bool) {
	if index, stored := store.findStored(key, hash); stored {
		entry := &store.entries[index]
		if entry.value.kind() == NilKind {
			store.reviveAt(index, value)
			return true, true
		}
		if rawSlotEqual(entry.value, value) {
			return false, false
		}
		writeSlot(&entry.value, value)
		return false, true
	}
	if len(store.entries) == 0 {
		store.rehash(minimumStoreCapacity)
	}
	for !store.insertAbsent(key, value, hash) {
		store.rehash(growTableStoreCapacity(len(store.entries)))
	}
	return true, true
}

func (store *tableStore) deleteFixture(key slot, hash uint32) bool {
	index, found := store.find(key, hash)
	if !found {
		return false
	}
	store.deleteAt(index)
	return true
}

func TestTableRecordHintUsesSmallestSufficientStore(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	tests := []struct {
		hint     int
		capacity int
	}{
		{hint: 1, capacity: 1},
		{hint: 2, capacity: 2},
		{hint: 3, capacity: 4},
		{hint: 4, capacity: 4},
		{hint: 7, capacity: 8},
		{hint: 8, capacity: 8},
		{hint: 13, capacity: 16},
		{hint: 16, capacity: 16},
	}
	for _, test := range tests {
		t.Run(strconv.Itoa(test.hint), func(t *testing.T) {
			table, err := state.NewTable(0, test.hint)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(table.store.entries); got != test.capacity {
				t.Fatalf(
					"record hint %d reserved %d entries, want %d",
					test.hint,
					got,
					test.capacity,
				)
			}

			// The chained store may use every node. Filling the rounded
			// capacity must not force the preemptive growth that the former
			// load-factor-limited store required.
			for index := 0; index < test.capacity; index++ {
				key := "hint-" + strconv.Itoa(index)
				if err := table.RawSetString(
					key,
					Number(float64(index)),
				); err != nil {
					t.Fatal(err)
				}
			}
			if got := len(table.store.entries); got != test.capacity {
				t.Fatalf(
					"record hint %d grew at %d live entries to %d",
					test.hint,
					test.capacity,
					got,
				)
			}
			for index := 0; index < test.capacity; index++ {
				key := "hint-" + strconv.Itoa(index)
				got, ok := table.RawGetString(key).AsNumber()
				if !ok || got != float64(index) {
					t.Fatalf("RawGetString(%q) = %v", key, table.RawGetString(key))
				}
			}
			assertTableStoreInvariant(t, &table.store)

			if test.capacity > 2 {
				return
			}
			if err := table.RawSetString("overflow", Number(99)); err != nil {
				t.Fatal(err)
			}
			if got := len(table.store.entries); got != test.capacity*2 {
				t.Fatalf(
					"full %d-node store grew to %d entries, want %d",
					test.capacity,
					got,
					test.capacity*2,
				)
			}
			if err := table.RawSetString("hint-0", Nil()); err != nil {
				t.Fatal(err)
			}
			if got := table.RawGetString("hint-0"); !got.IsNil() {
				t.Fatalf("deleted key = %v, want nil", got)
			}
			if got, ok := table.RawGetString("overflow").AsNumber(); !ok || got != 99 {
				t.Fatalf("overflow key = %v, want 99", table.RawGetString("overflow"))
			}
			assertTableStoreInvariant(t, &table.store)
		})
	}

	unhinted, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(unhinted.store.entries) != 0 {
		t.Fatalf(
			"zero record hint reserved %d entries",
			len(unhinted.store.entries),
		)
	}
}

func TestTableStoreCapacityBound(t *testing.T) {
	t.Run("growth", func(t *testing.T) {
		if got := growTableStoreCapacity(4); got != 8 {
			t.Fatalf("grown capacity = %d, want 8", got)
		}
	})

	t.Run("full store reports exhaustion", func(t *testing.T) {
		var store tableStore
		store.init(4)
		for index := 1; index <= 4; index++ {
			number := float64(index)
			if !store.insertAbsent(
				numberSlot(number),
				numberSlot(number),
				hashNumber(number),
			) {
				t.Fatalf("insert %d exhausted the store", index)
			}
		}
		backing := &store.entries[0]
		live, dead := store.live, store.dead
		integerKeys, lastFree := store.integerKeys, store.lastFree
		if store.insertAbsent(
			numberSlot(5),
			numberSlot(5),
			hashNumber(5),
		) {
			t.Fatal("full store accepted a fifth entry")
		}
		if &store.entries[0] != backing ||
			store.live != live ||
			store.dead != dead ||
			store.integerKeys != integerKeys ||
			store.lastFree != lastFree {
			t.Fatalf(
				"failed insertion mutated store: live:%d/%d dead:%d/%d integers:%d/%d cursor:%d/%d",
				store.live,
				live,
				store.dead,
				dead,
				store.integerKeys,
				integerKeys,
				store.lastFree,
				lastFree,
			)
		}
		for index := 1; index <= 4; index++ {
			number := float64(index)
			value, found := store.get(
				numberSlot(number),
				hashNumber(number),
			)
			if !found ||
				!rawSlotEqual(value, numberSlot(number)) {
				t.Fatalf(
					"key %d = (%v, %v)",
					index,
					value.owningValue(),
					found,
				)
			}
		}
		assertTableStoreInvariant(t, &store)
	})

	t.Run("overflow", func(t *testing.T) {
		defer func() {
			recovered := recover()
			if recovered != "lua: table capacity overflow" {
				t.Fatalf(
					"capacity overflow panic = %v, want table capacity overflow",
					recovered,
				)
			}
		}()
		growTableStoreCapacity(int(^uint(0) >> 1))
	})
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
	assertTableStoreInvariant(t, &table.store)
}

func TestTableHashStoreCollisionChains(t *testing.T) {
	key := func(index int) slot {
		return numberSlot(float64(-index))
	}
	value := func(index int) slot {
		return numberSlot(float64(index * 10))
	}
	assertValue := func(
		t *testing.T,
		store *tableStore,
		index int,
		hash uint32,
	) {
		t.Helper()
		got, found := store.get(key(index), hash)
		if !found || !rawSlotEqual(got, value(index)) {
			t.Fatalf(
				"lookup key %d = (%v, %v), want (%v, true)",
				index,
				got.owningValue(),
				found,
				value(index).owningValue(),
			)
		}
	}

	t.Run("chain insertion", func(t *testing.T) {
		var store tableStore
		store.init(4)
		const hash uint32 = 1
		for index := 1; index <= 4; index++ {
			inserted, changed := store.setFixture(key(index), value(index), hash)
			if !inserted || !changed {
				t.Fatalf(
					"insert key %d = (%v, %v), want (true, true)",
					index,
					inserted,
					changed,
				)
			}
		}
		if len(store.entries) != 4 {
			t.Fatalf("full collision chain grew to %d entries", len(store.entries))
		}
		for index := 1; index <= 4; index++ {
			assertValue(t, &store, index, hash)
		}
		assertTableStoreInvariant(t, &store)
	})

	t.Run("displaced main relocation", func(t *testing.T) {
		var store tableStore
		store.init(8)
		const collisionHash uint32 = 1
		store.setFixture(key(1), value(1), collisionHash)
		store.setFixture(key(2), value(2), collisionHash)
		store.setFixture(key(3), value(3), collisionHash)

		displacedIndex, found := store.find(key(3), collisionHash)
		if !found || displacedIndex == int(collisionHash)&(len(store.entries)-1) {
			t.Fatalf(
				"collision key index = (%d, %v), want displaced node",
				displacedIndex,
				found,
			)
		}
		if store.entries[displacedIndex].next == 0 {
			t.Fatal("test setup selected a collision-chain tail")
		}
		newMainHash := uint32(displacedIndex)
		store.setFixture(key(4), value(4), newMainHash)

		newMainIndex, found := store.find(key(4), newMainHash)
		if !found || newMainIndex != displacedIndex {
			t.Fatalf(
				"new main key index = (%d, %v), want (%d, true)",
				newMainIndex,
				found,
				displacedIndex,
			)
		}
		relocatedIndex, found := store.find(key(3), collisionHash)
		if !found || relocatedIndex == displacedIndex {
			t.Fatalf(
				"displaced key relocation = (%d, %v), want a new index",
				relocatedIndex,
				found,
			)
		}
		assertValue(t, &store, 1, collisionHash)
		assertValue(t, &store, 2, collisionHash)
		assertValue(t, &store, 3, collisionHash)
		assertValue(t, &store, 4, newMainHash)
		assertTableStoreInvariant(t, &store)
	})

	t.Run("main and middle tombstone revival", func(t *testing.T) {
		var store tableStore
		store.init(8)
		const hash uint32 = 1
		for index := 1; index <= 4; index++ {
			store.setFixture(key(index), value(index), hash)
		}

		mainIndex, found := store.find(key(1), hash)
		if !found || mainIndex != store.mainIndex(hash) {
			t.Fatalf(
				"main key index = (%d, %v), want (%d, true)",
				mainIndex,
				found,
				store.mainIndex(hash),
			)
		}
		middleIndex, found := store.find(key(3), hash)
		if !found ||
			middleIndex == mainIndex ||
			store.entries[middleIndex].next == 0 {
			t.Fatalf(
				"middle key index = (%d, %v), want a non-tail collision",
				middleIndex,
				found,
			)
		}
		if !store.deleteFixture(key(1), hash) ||
			!store.deleteFixture(key(3), hash) {
			t.Fatal("failed to delete main and middle collision keys")
		}
		for _, index := range []int{2, 4} {
			assertValue(t, &store, index, hash)
		}
		for _, deleted := range []struct {
			index    int
			location int
		}{
			{index: 1, location: mainIndex},
			{index: 3, location: middleIndex},
		} {
			if _, found := store.get(key(deleted.index), hash); found {
				t.Fatalf("deleted key %d remained live", deleted.index)
			}
			if continuation, found := store.findContinuation(
				key(deleted.index),
				hash,
			); !found {
				t.Fatalf(
					"deleted key %d stopped being a continuation",
					deleted.index,
				)
			} else if continuation != deleted.location {
				t.Fatalf(
					"deleted key %d moved from %d to %d",
					deleted.index,
					deleted.location,
					continuation,
				)
			}
		}

		capacity := len(store.entries)
		backing := &store.entries[0]
		for _, index := range []int{1, 3} {
			inserted, changed := store.setFixture(key(index), value(index), hash)
			if !inserted || !changed {
				t.Fatalf(
					"revive key %d = (%v, %v), want (true, true)",
					index,
					inserted,
					changed,
				)
			}
		}
		if len(store.entries) != capacity || &store.entries[0] != backing {
			t.Fatalf(
				"exact revival changed store layout: capacity %d/%d, backing %p/%p",
				len(store.entries),
				capacity,
				&store.entries[0],
				backing,
			)
		}
		for index := 1; index <= 4; index++ {
			assertValue(t, &store, index, hash)
		}
		if revived, _ := store.find(key(1), hash); revived != mainIndex {
			t.Fatalf("revived main moved from %d to %d", mainIndex, revived)
		}
		if revived, _ := store.find(key(3), hash); revived != middleIndex {
			t.Fatalf("revived middle moved from %d to %d", middleIndex, revived)
		}
		assertTableStoreInvariant(t, &store)
	})

	t.Run("deleted node reuse and rebuild", func(t *testing.T) {
		var store tableStore
		store.init(4)
		const hash uint32 = 1
		for index := 1; index <= 4; index++ {
			store.setFixture(key(index), value(index), hash)
		}
		if !store.deleteFixture(key(2), hash) {
			t.Fatal("delete did not find collision key")
		}
		if _, found := store.findContinuation(key(2), hash); !found {
			t.Fatal("deleted key stopped being a traversal continuation")
		}

		backing := &store.entries[0]
		store.setFixture(key(5), value(5), hash)
		if len(store.entries) != 4 {
			t.Fatalf(
				"reusing one dead node grew store to %d entries",
				len(store.entries),
			)
		}
		if &store.entries[0] != backing {
			t.Fatal("reusing one dead node replaced the backing store")
		}
		if _, found := store.get(key(2), hash); found {
			t.Fatal("deleted key remained live after node reuse")
		}
		if _, found := store.findContinuation(key(2), hash); found {
			t.Fatal("node reuse retained the replaced continuation key")
		}
		for _, index := range []int{1, 3, 4, 5} {
			assertValue(t, &store, index, hash)
		}
		assertTableStoreInvariant(t, &store)

		store.setFixture(key(6), value(6), hash)
		if len(store.entries) != 8 {
			t.Fatalf(
				"inserting beyond full capacity grew to %d entries, want 8",
				len(store.entries),
			)
		}
		for _, index := range []int{1, 3, 4, 5, 6} {
			assertValue(t, &store, index, hash)
		}
		assertTableStoreInvariant(t, &store)
	})

	t.Run("main tombstone promotion on reuse", func(t *testing.T) {
		var store tableStore
		store.init(4)
		store.setFixture(key(1), value(1), 1)
		store.setFixture(key(2), value(2), 1)
		store.setFixture(key(3), value(3), 2)
		store.setFixture(key(4), value(4), 3)
		if store.live != 4 {
			t.Fatalf("test setup live entries = %d, want 4", store.live)
		}
		if index, found := store.find(key(1), 1); !found || index != 1 {
			t.Fatalf("main collision key = (%d, %v), want (1, true)", index, found)
		}
		if !store.deleteFixture(key(1), 1) {
			t.Fatal("failed to delete the main collision key")
		}

		backing := &store.entries[0]
		store.setFixture(key(5), value(5), 6)
		if &store.entries[0] != backing {
			t.Fatal("main tombstone reuse replaced the backing store")
		}
		if store.live != 4 || store.dead != 0 {
			t.Fatalf(
				"reused store = live:%d dead:%d, want live:4 dead:0",
				store.live,
				store.dead,
			)
		}
		if _, found := store.findContinuation(key(1), 1); found {
			t.Fatal("main-node reuse retained the replaced continuation key")
		}
		if index, found := store.find(key(2), 1); !found || index != 1 {
			t.Fatalf(
				"promoted collision key = (%d, %v), want (1, true)",
				index,
				found,
			)
		}
		for _, item := range []struct {
			index int
			hash  uint32
		}{
			{index: 2, hash: 1},
			{index: 3, hash: 2},
			{index: 4, hash: 3},
			{index: 5, hash: 6},
		} {
			assertValue(t, &store, item.index, item.hash)
		}
		assertTableStoreInvariant(t, &store)
	})

	t.Run("promoted tombstone remains reclaimable", func(t *testing.T) {
		var store tableStore
		store.init(4)
		const hash uint32 = 1
		for index := 1; index <= 4; index++ {
			store.setFixture(key(index), value(index), hash)
		}
		main := store.mainIndex(hash)
		successor := int(store.entries[main].next - 1)
		successorKey := store.entries[successor].key
		if !store.deleteFixture(successorKey, hash) ||
			!store.deleteFixture(key(1), hash) {
			t.Fatal("failed to delete adjacent collision nodes")
		}
		if store.live != 2 || store.dead != 2 {
			t.Fatalf(
				"test setup = live:%d dead:%d, want live:2 dead:2",
				store.live,
				store.dead,
			)
		}

		backing := &store.entries[0]
		store.setFixture(key(5), value(5), 2)
		if store.live != 3 || store.dead != 1 {
			t.Fatalf(
				"first reuse = live:%d dead:%d, want live:3 dead:1",
				store.live,
				store.dead,
			)
		}
		if store.lastFree != uint32(main+1) {
			t.Fatalf(
				"promoted tombstone cursor = %d, want %d",
				store.lastFree,
				main+1,
			)
		}
		store.setFixture(key(6), value(6), 3)
		if &store.entries[0] != backing {
			t.Fatal("successive tombstone reuse replaced the backing store")
		}
		if store.live != 4 || store.dead != 0 || store.lastFree != 0 {
			t.Fatalf(
				"second reuse = live:%d dead:%d lastFree:%d",
				store.live,
				store.dead,
				store.lastFree,
			)
		}
		for _, item := range []struct {
			index int
			hash  uint32
		}{
			{index: 5, hash: 2},
			{index: 6, hash: 3},
		} {
			assertValue(t, &store, item.index, item.hash)
		}
		assertTableStoreInvariant(t, &store)
	})

	t.Run("cursor wraps after tombstone revival", func(t *testing.T) {
		var store tableStore
		store.init(8)
		initial := []struct {
			index int
			hash  uint32
		}{
			{index: 1, hash: 8},
			{index: 2, hash: 1},
			{index: 3, hash: 2},
			{index: 4, hash: 3},
			{index: 5, hash: 4},
		}
		for _, item := range initial {
			store.setFixture(key(item.index), value(item.index), item.hash)
		}
		if !store.deleteFixture(key(2), 1) {
			t.Fatal("failed to delete wrap-test key")
		}
		store.setFixture(key(2), value(2), 1)
		if store.lastFree != 1 {
			t.Fatalf("revived cursor = %d, want 1", store.lastFree)
		}

		backing := &store.entries[0]
		store.setFixture(key(6), value(6), 10)
		if &store.entries[0] != backing {
			t.Fatal("wrapped insertion replaced the backing store")
		}
		if store.live != 6 || store.dead != 0 {
			t.Fatalf(
				"wrapped insertion = live:%d dead:%d, want live:6 dead:0",
				store.live,
				store.dead,
			)
		}
		for _, item := range initial {
			assertValue(t, &store, item.index, item.hash)
		}
		assertValue(t, &store, 6, 10)
		assertTableStoreInvariant(t, &store)
	})

	t.Run("string lookup crosses tombstone", func(t *testing.T) {
		const hash uint32 = 37
		first := slotFromValue(stringValue(newHashedStringRef(
			"first collision",
			stringHash(hash),
		)))
		second := slotFromValue(stringValue(newHashedStringRef(
			"second collision",
			stringHash(hash),
		)))
		var store tableStore
		store.init(4)
		store.setFixture(first, value(1), hash)
		store.setFixture(second, value(2), hash)
		if !store.deleteFixture(first, hash) {
			t.Fatal("failed to delete leading string collision")
		}

		got, found := store.getString("second collision", hash)
		if !found || !rawSlotEqual(got, value(2)) {
			t.Fatalf(
				"string lookup beyond tombstone = (%v, %v), want (20, true)",
				got.owningValue(),
				found,
			)
		}
		if _, stored := store.findStoredString(
			"first collision",
			hash,
		); !stored {
			t.Fatal("specialized string mutation lookup lost tombstone")
		}
		if _, found := store.findContinuation(first, hash); !found {
			t.Fatal("deleted string stopped being a continuation")
		}
		assertTableStoreInvariant(t, &store)
	})
}

func TestTableTombstoneRevivalCompactsDeadRecordKeys(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	table, err := state.NewTable(0, 8)
	if err != nil {
		t.Fatal(err)
	}

	keys := make([]string, 5)
	keySlots := make([]slot, len(keys))
	for index := range keys {
		keys[index] = strings.Repeat(
			"uncached-revival-key-"+strconv.Itoa(index)+"-",
			4,
		)
		if len(keys[index]) <= shortStringLimit {
			t.Fatal("test key unexpectedly qualifies for the string cache")
		}
		if err := table.RawSetString(
			keys[index],
			Number(float64(index)),
		); err != nil {
			t.Fatal(err)
		}
		keySlots[index] = slotFromValue(state.String(keys[index]))
	}
	for index := 0; index < 4; index++ {
		if err := table.RawSetString(keys[index], Nil()); err != nil {
			t.Fatal(err)
		}
		hash := uint32(stringSlotHash(keySlots[index]))
		if _, found := table.store.findContinuation(
			keySlots[index],
			hash,
		); !found {
			t.Fatalf("deleted key %d stopped being a continuation", index)
		}
	}
	if table.store.live != 1 ||
		table.store.dead != 4 ||
		!table.store.shouldCompact() {
		t.Fatalf(
			"pre-revival store = live:%d dead:%d compact:%v",
			table.store.live,
			table.store.dead,
			table.store.shouldCompact(),
		)
	}

	capacity := len(table.store.entries)
	if err := table.RawSetString(keys[0], Number(100)); err != nil {
		t.Fatal(err)
	}
	if len(table.store.entries) != capacity ||
		table.store.live != 2 ||
		table.store.dead != 0 {
		t.Fatalf(
			"post-revival store = capacity:%d/%d live:%d dead:%d",
			len(table.store.entries),
			capacity,
			table.store.live,
			table.store.dead,
		)
	}
	for index := 1; index < 4; index++ {
		hash := uint32(stringSlotHash(keySlots[index]))
		if _, found := table.store.findContinuation(
			keySlots[index],
			hash,
		); found {
			t.Fatalf(
				"compaction retained deleted string key %d",
				index,
			)
		}
	}
	if got, ok := table.RawGetString(keys[0]).AsNumber(); !ok || got != 100 {
		t.Fatalf("revived value = (%v, %v), want (100, true)", got, ok)
	}
	if got, ok := table.RawGetString(keys[4]).AsNumber(); !ok || got != 4 {
		t.Fatalf("surviving value = (%v, %v), want (4, true)", got, ok)
	}
	assertTableStoreInvariant(t, &table.store)
	runtime.KeepAlive(keySlots)
}

func TestTableArrayInsertionsCompactDeadRecordKeys(t *testing.T) {
	tests := []struct {
		name   string
		insert func(*Table) error
	}{
		{
			name: "RawSetInt",
			insert: func(table *Table) error {
				return table.RawSetInt(1, Number(1))
			},
		},
		{
			name: "SETLIST",
			insert: func(table *Table) error {
				table.rawSetList(1, []slot{numberSlot(1)})
				return nil
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, err := New(Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			table, err := state.NewTable(0, 8)
			if err != nil {
				t.Fatal(err)
			}

			keys := make([]*Table, 5)
			keySlots := make([]slot, len(keys))
			for index := range keys {
				keys[index], err = state.NewTable(0, 0)
				if err != nil {
					t.Fatal(err)
				}
				keySlots[index] = slotFromTable(keys[index])
				if err := table.RawSet(
					keys[index].Value(),
					Number(float64(index)),
				); err != nil {
					t.Fatal(err)
				}
			}
			for index := 0; index < 4; index++ {
				if err := table.RawSet(keys[index].Value(), Nil()); err != nil {
					t.Fatal(err)
				}
				if _, found := table.store.findContinuation(
					keySlots[index],
					hashReference(keySlots[index]),
				); !found {
					t.Fatalf(
						"deleted object key %d stopped being a continuation",
						index,
					)
				}
			}
			if table.store.live != 1 ||
				table.store.dead != 4 ||
				!table.store.shouldCompact() {
				t.Fatalf(
					"pre-array-insert store = live:%d dead:%d compact:%v",
					table.store.live,
					table.store.dead,
					table.store.shouldCompact(),
				)
			}

			capacity := len(table.store.entries)
			if err := test.insert(table); err != nil {
				t.Fatal(err)
			}
			if len(table.store.entries) != capacity ||
				table.store.live != 1 ||
				table.store.dead != 0 ||
				table.arrayUsed != 1 {
				t.Fatalf(
					"post-array-insert storage = capacity:%d/%d live:%d dead:%d array:%d",
					len(table.store.entries),
					capacity,
					table.store.live,
					table.store.dead,
					table.arrayUsed,
				)
			}
			for index := 0; index < 4; index++ {
				if _, found := table.store.findContinuation(
					keySlots[index],
					hashReference(keySlots[index]),
				); found {
					t.Fatalf(
						"array insertion retained deleted object key %d",
						index,
					)
				}
			}
			if got, ok := table.RawGetInt(1).AsNumber(); !ok || got != 1 {
				t.Fatalf("array value = (%v, %v), want (1, true)", got, ok)
			}
			got, err := table.RawGet(keys[4].Value())
			if err != nil {
				t.Fatal(err)
			}
			if number, ok := got.AsNumber(); !ok || number != 4 {
				t.Fatalf(
					"surviving record value = (%v, %v), want (4, true)",
					number,
					ok,
				)
			}
			assertTableStoreInvariant(t, &table.store)
			runtime.KeepAlive(keys)
		})
	}
}

func TestTableValueUpdatePreservesDeadContinuations(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	table, err := state.NewTable(0, 8)
	if err != nil {
		t.Fatal(err)
	}

	keys := make([]string, 8)
	byLocation := make([]string, len(keys))
	for index := range keys {
		keys[index] = strings.Repeat(
			"uncached-update-key-"+strconv.Itoa(index)+"-",
			4,
		)
		if err := table.RawSetString(
			keys[index],
			Number(float64(index)),
		); err != nil {
			t.Fatal(err)
		}
	}
	for index := range keys {
		hash := uint32(table.owner.strings.hash(keys[index]))
		location, found := table.store.findStoredString(keys[index], hash)
		if !found {
			t.Fatalf("inserted key %d was not stored", index)
		}
		if byLocation[location] != "" {
			t.Fatalf("two keys occupy record location %d", location)
		}
		byLocation[location] = keys[index]
	}
	for location := 0; location < 5; location++ {
		if err := table.RawSetString(byLocation[location], Nil()); err != nil {
			t.Fatal(err)
		}
	}
	if table.store.live != 3 ||
		table.store.dead != 5 ||
		!table.store.shouldCompact() {
		t.Fatalf(
			"pre-update store = live:%d dead:%d compact:%v",
			table.store.live,
			table.store.dead,
			table.store.shouldCompact(),
		)
	}

	previous := slotFromValue(state.String(byLocation[0]))
	previousHash := uint32(stringSlotHash(previous))
	continuation, found := table.store.findContinuation(
		previous,
		previousHash,
	)
	if !found {
		t.Fatal("deleted key stopped being a continuation before update")
	}
	nextKey, nextValue, nextFound, nextErr := table.next(previous)
	if nextErr != nil || !nextFound {
		t.Fatalf(
			"next before update = (found %v, err %v)",
			nextFound,
			nextErr,
		)
	}

	entries := &table.store.entries[0]
	live, dead := table.store.live, table.store.dead
	lastFree := table.store.lastFree
	version := table.structuralVersion
	if err := table.RawSetString(byLocation[7], Number(100)); err != nil {
		t.Fatal(err)
	}
	if &table.store.entries[0] != entries ||
		table.store.live != live ||
		table.store.dead != dead ||
		table.store.lastFree != lastFree ||
		table.structuralVersion != version {
		t.Fatalf(
			"value update changed layout: live:%d/%d dead:%d/%d "+
				"lastFree:%d/%d version:%d/%d",
			table.store.live,
			live,
			table.store.dead,
			dead,
			table.store.lastFree,
			lastFree,
			table.structuralVersion,
			version,
		)
	}
	if after, found := table.store.findContinuation(
		previous,
		previousHash,
	); !found || after != continuation {
		t.Fatalf(
			"continuation after update = (%d, %v), want (%d, true)",
			after,
			found,
			continuation,
		)
	}
	afterKey, afterValue, afterFound, afterErr := table.next(previous)
	if afterErr != nil ||
		afterFound != nextFound ||
		!rawSlotEqual(afterKey, nextKey) ||
		!rawSlotEqual(afterValue, nextValue) {
		t.Fatalf(
			"next after update = (%v, %v, %v, %v), "+
				"want (%v, %v, %v, nil)",
			afterKey.owningValue(),
			afterValue.owningValue(),
			afterFound,
			afterErr,
			nextKey.owningValue(),
			nextValue.owningValue(),
			nextFound,
		)
	}
	if got, ok := table.RawGetString(byLocation[7]).AsNumber(); !ok || got != 100 {
		t.Fatalf("updated value = (%v, %v), want (100, true)", got, ok)
	}
	assertTableStoreInvariant(t, &table.store)
}

func TestTableHashStoreRandomizedMutationInvariants(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	table, err := state.NewTable(0, 0)
	if err != nil {
		t.Fatal(err)
	}

	const keyCount = 97
	expected := make(map[string]float64, keyCount)
	var random uint64 = 0x6a09e667f3bcc909
	for step := 0; step < 2_000; step++ {
		random ^= random << 13
		random ^= random >> 7
		random ^= random << 17
		index := int(random % keyCount)
		key := "mutation-key-" + strconv.Itoa(index)
		deleting := random>>32&3 == 0
		if deleting {
			if err := table.RawSetString(key, Nil()); err != nil {
				t.Fatal(err)
			}
			delete(expected, key)
		} else {
			value := float64(step + 1)
			if err := table.RawSetString(key, Number(value)); err != nil {
				t.Fatal(err)
			}
			expected[key] = value
		}
		assertTableStoreInvariant(t, &table.store)

		if step%31 != 0 {
			continue
		}
		for key, want := range expected {
			got, ok := table.RawGetString(key).AsNumber()
			if !ok || got != want {
				t.Fatalf("step %d: RawGetString(%q) = %v, want %v", step, key, got, want)
			}
		}
	}
	for index := 0; index < keyCount; index++ {
		key := "mutation-key-" + strconv.Itoa(index)
		got := table.RawGetString(key)
		want, found := expected[key]
		if !found {
			if !got.IsNil() {
				t.Fatalf("RawGetString(%q) = %v, want nil", key, got)
			}
			continue
		}
		number, ok := got.AsNumber()
		if !ok || number != want {
			t.Fatalf("RawGetString(%q) = %v, want %v", key, got, want)
		}
	}
}

func TestTableStoreRecyclesTombstoneForNewKeyWithoutAllocation(t *testing.T) {
	var store tableStore
	store.init(4)
	keys := [...]slot{
		numberSlot(1),
		numberSlot(2),
		numberSlot(3),
		numberSlot(4),
		numberSlot(5),
	}
	hashes := [...]uint32{1, 2, 3, 4, 6}
	for index := 0; index < 4; index++ {
		if inserted, changed := store.setFixture(
			keys[index],
			numberSlot(float64(index+1)),
			hashes[index],
		); !inserted || !changed {
			t.Fatalf("initial insert %d = (%v, %v)", index, inserted, changed)
		}
	}
	if store.live != 4 || store.dead != 0 {
		t.Fatalf("full store = live:%d dead:%d", store.live, store.dead)
	}

	backing := &store.entries[0]
	oldKey, oldHash := keys[0], hashes[0]
	newKey, newHash := keys[4], hashes[4]
	replace := func() {
		if !store.deleteFixture(oldKey, oldHash) {
			panic("replacement delete missed")
		}
		if inserted, changed := store.setFixture(
			newKey,
			numberSlot(10),
			newHash,
		); !inserted || !changed {
			panic("replacement insert did not change the store")
		}
		oldKey, newKey = newKey, oldKey
		oldHash, newHash = newHash, oldHash
		if store.lastFree != 0 {
			panic("full replacement left a stale insertion cursor")
		}
	}
	replace()
	if &store.entries[0] != backing {
		t.Fatal("replacement changed the store backing array")
	}
	if store.live != 4 || store.dead != 0 {
		t.Fatalf("replaced store = live:%d dead:%d", store.live, store.dead)
	}
	assertTableStoreInvariant(t, &store)

	t.Run("steady state allocations", func(t *testing.T) {
		requireStableAllocationAccounting(t)
		allocations := testing.AllocsPerRun(100, replace)
		if allocations != 0 {
			t.Fatalf("replacement allocations = %v, want 0", allocations)
		}
		if &store.entries[0] != backing {
			t.Fatal("replacement changed the store backing array")
		}
		assertTableStoreInvariant(t, &store)
	})
}

func assertTableStoreInvariant(t *testing.T, store *tableStore) {
	t.Helper()
	size := len(store.entries)
	if size == 0 {
		if store.live != 0 ||
			store.dead != 0 ||
			store.integerKeys != 0 ||
			store.lastFree != 0 {
			t.Fatalf(
				"empty store metadata = live:%d dead:%d integers:%d lastFree:%d",
				store.live,
				store.dead,
				store.integerKeys,
				store.lastFree,
			)
		}
		return
	}
	if size&(size-1) != 0 {
		t.Fatalf("store capacity %d is not a power of two", size)
	}
	if store.lastFree > uint32(size) {
		t.Fatalf("lastFree = %d, capacity = %d", store.lastFree, size)
	}

	var live, dead, integerKeys uint32
	for index := range store.entries {
		entry := &store.entries[index]
		if entry.hash == entryHashEmpty {
			if entry.next != 0 {
				t.Fatalf("empty entry %d links to %d", index, entry.next)
			}
			continue
		}
		if entry.value.kind() == NilKind {
			dead++
		} else {
			live++
			if isPositiveIntegerKey(entry.key) {
				integerKeys++
			}
		}
		if entry.next > uint32(size) {
			t.Fatalf("entry %d has out-of-range link %d", index, entry.next)
		}
	}

	reachable := make([]bool, size)
	for main := range store.entries {
		entry := &store.entries[main]
		if entry.hash == entryHashEmpty ||
			int(entry.hash)&(size-1) != main {
			continue
		}
		current := main
		for {
			if reachable[current] {
				t.Fatalf("chain rooted at %d contains a cycle at %d", main, current)
			}
			reachable[current] = true
			currentEntry := &store.entries[current]
			if currentEntry.hash == entryHashEmpty {
				t.Fatalf(
					"chain rooted at %d reached empty entry %d",
					main,
					current,
				)
			}
			if int(currentEntry.hash)&(size-1) != main {
				t.Fatalf(
					"chain rooted at %d contains entry %d with main position %d",
					main,
					current,
					int(currentEntry.hash)&(size-1),
				)
			}
			if currentEntry.next == 0 {
				break
			}
			if currentEntry.next > uint32(size) {
				t.Fatalf(
					"chain rooted at %d has out-of-range link %d",
					main,
					currentEntry.next,
				)
			}
			current = int(currentEntry.next - 1)
		}
	}
	for index := range store.entries {
		entry := &store.entries[index]
		if entry.hash == entryHashEmpty {
			continue
		}
		if !reachable[index] {
			t.Fatalf(
				"entry %d is not reachable from main position %d",
				index,
				int(entry.hash)&(size-1),
			)
		}
		if entry.value.kind() == NilKind {
			storedIndex, stored := store.findStored(entry.key, entry.hash)
			if !stored || storedIndex != index {
				t.Fatalf(
					"tombstone %d resolves to (%d, %v)",
					index,
					storedIndex,
					stored,
				)
			}
			if _, found := store.find(entry.key, entry.hash); found {
				t.Fatalf("tombstone %d is visible as a live key", index)
			}
			continue
		}
		foundIndex, found := store.find(entry.key, entry.hash)
		if !found || foundIndex != index {
			t.Fatalf(
				"live entry %d resolves to (%d, %v)",
				index,
				foundIndex,
				found,
			)
		}
		value, found := store.get(entry.key, entry.hash)
		if !found || !rawSlotEqual(value, entry.value) {
			t.Fatalf(
				"live entry %d lookup = (%v, %v), want (%v, true)",
				index,
				value.owningValue(),
				found,
				entry.value.owningValue(),
			)
		}
	}
	if live != store.live ||
		dead != store.dead ||
		integerKeys != store.integerKeys {
		t.Fatalf(
			"store metadata = live:%d/%d dead:%d/%d integers:%d/%d",
			store.live,
			live,
			store.dead,
			dead,
			store.integerKeys,
			integerKeys,
		)
	}
	if dead == 0 &&
		live == uint32(size) &&
		store.lastFree != 0 {
		t.Fatalf(
			"full live store retained insertion cursor %d",
			store.lastFree,
		)
	}
}

func BenchmarkTableStringMap(b *testing.B) {
	families := []struct {
		name   string
		prefix string
	}{
		{name: "decimal"},
		{name: "field", prefix: "record_field_"},
	}
	for _, family := range families {
		for _, count := range []int{1, 2, 4, 8, 16, 64, 256, 1_024, 5_000} {
			keys := make([]string, count)
			missing := make([]string, count)
			for index := range keys {
				suffix := strconv.Itoa(index)
				keys[index] = family.prefix + suffix
				missing[index] = family.prefix + "missing_" + suffix
			}
			name := family.name + "/" + strconv.Itoa(count)
			b.Run(name+"/hit", func(b *testing.B) {
				state, table := benchmarkStringTable(b, keys)
				defer state.Close()
				b.ReportAllocs()
				b.ResetTimer()
				for index := range b.N {
					runtime.KeepAlive(
						table.RawGetString(keys[index%count]),
					)
				}
			})
			b.Run(name+"/miss", func(b *testing.B) {
				state, table := benchmarkStringTable(b, keys)
				defer state.Close()
				b.ReportAllocs()
				b.ResetTimer()
				for index := range b.N {
					runtime.KeepAlive(
						table.RawGetString(missing[index%count]),
					)
				}
			})
			b.Run(name+"/churn", func(b *testing.B) {
				state, table := benchmarkStringTable(b, keys)
				defer state.Close()
				b.ReportAllocs()
				b.ResetTimer()
				for index := range b.N {
					key := keys[index%count]
					if err := table.RawSetString(key, Nil()); err != nil {
						b.Fatal(err)
					}
					if err := table.RawSetString(
						key,
						Number(float64(index)),
					); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run(name+"/replace", func(b *testing.B) {
				state, table := benchmarkStringTable(b, keys)
				defer state.Close()
				for _, key := range missing {
					runtime.KeepAlive(state.String(key))
				}
				active := make([]bool, count)
				b.ReportAllocs()
				b.ResetTimer()
				for iteration := range b.N {
					index := iteration % count
					oldKey, newKey := keys[index], missing[index]
					if active[index] {
						oldKey, newKey = newKey, oldKey
					}
					if err := table.RawSetString(oldKey, Nil()); err != nil {
						b.Fatal(err)
					}
					if err := table.RawSetString(
						newKey,
						Number(float64(iteration)),
					); err != nil {
						b.Fatal(err)
					}
					active[index] = !active[index]
				}
			})
			b.Run(name+"/build", func(b *testing.B) {
				state, err := New(Options{})
				if err != nil {
					b.Fatal(err)
				}
				defer state.Close()
				b.ReportAllocs()
				b.ReportMetric(float64(count), "keys/op")
				for range b.N {
					table, err := state.NewTable(0, count)
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
					runtime.KeepAlive(table)
				}
			})
		}
	}
}

func benchmarkStringTable(
	b *testing.B,
	keys []string,
) (*State, *Table) {
	b.Helper()
	state, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	table, err := state.NewTable(0, len(keys))
	if err != nil {
		state.Close()
		b.Fatal(err)
	}
	for index, key := range keys {
		if err := table.RawSetString(
			key,
			Number(float64(index)),
		); err != nil {
			state.Close()
			b.Fatal(err)
		}
	}
	return state, table
}
