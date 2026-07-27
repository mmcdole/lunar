package lua

import (
	"runtime"
	"testing"
	"unsafe"
)

func TestWeakTableModeClassification(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()

	table := newTable(state, 0, 0)
	if mode := tableWeakMode(table); mode != 0 {
		t.Fatalf("table without metatable mode = %d", mode)
	}

	tests := []struct {
		name string
		mode string
		want weakMode
	}{
		{name: "empty", mode: "", want: 0},
		{name: "unrelated", mode: "weak", want: weakKeys},
		{name: "uppercase", mode: "KV", want: 0},
		{name: "keys", mode: "k", want: weakKeys},
		{name: "values", mode: "v", want: weakValues},
		{name: "both", mode: "values-and-keys", want: weakKeys | weakValues},
		{name: "nul before", mode: "\x00kv", want: 0},
		{name: "nul after", mode: "vk\x00ignored", want: weakKeys | weakValues},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			table, _ := newWeakTableForTest(
				t,
				state,
				test.mode,
				0,
				0,
			)
			if got := tableWeakMode(table); got != test.want {
				t.Fatalf(
					"mode %q classified as %d; want %d",
					test.mode,
					got,
					test.want,
				)
			}
		})
	}

	t.Run("non-string", func(t *testing.T) {
		table := newTable(state, 0, 0)
		metatable := newTable(state, 0, 1)
		if err := metatable.rawSetStringSlot(
			metamethodNames[metaMode],
			numberSlot(1),
		); err != nil {
			t.Fatal(err)
		}
		table.metatable = metatable
		if mode := tableWeakMode(table); mode != 0 {
			t.Fatalf("numeric __mode classified as %d", mode)
		}
	})

	t.Run("raw lookup", func(t *testing.T) {
		table := newTable(state, 0, 0)
		metatable := newTable(state, 0, 0)
		provider, _ := newWeakTableForTest(t, state, "v", 0, 0)
		metatable.metatable = provider.metatable
		table.metatable = metatable
		if mode := tableWeakMode(table); mode != 0 {
			t.Fatalf("inherited __mode classified as %d", mode)
		}
	})

	t.Run("absence cache invalidation", func(t *testing.T) {
		table := newTable(state, 0, 0)
		metatable := newTable(state, 0, 1)
		table.metatable = metatable
		if mode := tableWeakMode(table); mode != 0 {
			t.Fatalf("initial mode = %d", mode)
		}
		if metatable.absentMetamethods&metaMode.bit() == 0 {
			t.Fatal("missing __mode was not cached")
		}
		if err := metatable.rawSetStringSlot(
			metamethodNames[metaMode],
			slotFromValue(state.String("v")),
		); err != nil {
			t.Fatal(err)
		}
		if mode := tableWeakMode(table); mode != weakValues {
			t.Fatalf("mode after raw mutation = %d", mode)
		}
	})
}

func TestWeakKeyTableSemantics(t *testing.T) {
	t.Run("array values remain strong", func(t *testing.T) {
		state := newCollectorTestState(t)
		defer state.Close()
		table, metatable := newWeakTableForTest(t, state, "k", 1, 0)
		value := newTable(state, 0, 0)
		table.rawSetIntegerSlot(1, slotFromTableObject(value))
		rootWeakTableForTest(t, state, table)

		state.collectUnreachable()
		if value.owner != state.runtime ||
			metatable.owner != state.runtime {
			t.Fatal("weak-key table lost a strong array edge or metatable")
		}
		if got, found := table.rawIntSlot(1); !found ||
			!rawSlotEqual(got, slotFromTableObject(value)) {
			t.Fatal("weak-key collection changed the array field")
		}
	})

	t.Run("dead key without backlink", func(t *testing.T) {
		state := newCollectorTestState(t)
		defer state.Close()
		table, _ := newWeakTableForTest(t, state, "k", 0, 1)
		key := newTable(state, 0, 0)
		value := newTable(state, 0, 0)
		keySlot := slotFromTableObject(key)
		setWeakRecordForTest(
			t,
			table,
			keySlot,
			slotFromTableObject(value),
		)
		rootWeakTableForTest(t, state, table)

		state.collectUnreachable()
		if key.owner != nil {
			t.Fatal("unreachable weak key survived")
		}
		if value.owner != state.runtime {
			t.Fatal("strong value did not survive the clearing cycle")
		}
		if _, found := table.rawSlot(keySlot); found {
			t.Fatal("record with dead weak key remained visible")
		}
		state.collectUnreachable()
		if value.owner != nil {
			t.Fatal("former strong value survived a second collection")
		}
	})

	t.Run("value backlink retains key", func(t *testing.T) {
		state := newCollectorTestState(t)
		defer state.Close()
		table, _ := newWeakTableForTest(t, state, "k", 0, 1)
		key := newTable(state, 0, 0)
		value := newTable(state, 0, 1)
		keySlot := slotFromTableObject(key)
		valueSlot := slotFromTableObject(value)
		if err := value.rawSetStringSlot("key", keySlot); err != nil {
			t.Fatal(err)
		}
		setWeakRecordForTest(t, table, keySlot, valueSlot)
		rootWeakTableForTest(t, state, table)

		state.collectUnreachable()
		if key.owner != state.runtime || value.owner != state.runtime {
			t.Fatal("Lua 5.1 weak-key backlink was treated as an ephemeron")
		}
		if got, found := table.rawSlot(keySlot); !found ||
			!rawSlotEqual(got, valueSlot) {
			t.Fatal("backlinked weak-key record was cleared")
		}
	})

	t.Run("value equal to key retains key", func(t *testing.T) {
		state := newCollectorTestState(t)
		defer state.Close()
		table, _ := newWeakTableForTest(t, state, "k", 0, 1)
		key := newTable(state, 0, 0)
		keySlot := slotFromTableObject(key)
		setWeakRecordForTest(t, table, keySlot, keySlot)
		rootWeakTableForTest(t, state, table)

		state.collectUnreachable()
		if key.owner != state.runtime {
			t.Fatal("weak-key value did not strongly retain the same object")
		}
		if _, found := table.rawSlot(keySlot); !found {
			t.Fatal("self-valued weak-key record was cleared")
		}
	})
}

func TestWeakValueTableSemantics(t *testing.T) {
	t.Run("array and record references clear", func(t *testing.T) {
		state := newCollectorTestState(t)
		defer state.Close()
		table, _ := newWeakTableForTest(t, state, "v", 1, 1)
		arrayValue := newTable(state, 0, 1)
		arrayValue.rawSetIntegerSlot(1, slotFromTableObject(arrayValue))
		key := newTable(state, 0, 0)
		recordValue := newTable(state, 0, 0)
		keySlot := slotFromTableObject(key)
		table.rawSetIntegerSlot(1, slotFromTableObject(arrayValue))
		setWeakRecordForTest(
			t,
			table,
			keySlot,
			slotFromTableObject(recordValue),
		)
		rootWeakTableForTest(t, state, table)

		state.collectUnreachable()
		if arrayValue.owner != nil || recordValue.owner != nil {
			t.Fatal("unreachable weak value survived")
		}
		if table.arrayUsed != 0 {
			t.Fatalf("weak array occupancy = %d; want 0", table.arrayUsed)
		}
		if _, found := table.rawIntSlot(1); found {
			t.Fatal("weak array value remained visible")
		}
		if _, found := table.rawSlot(keySlot); found {
			t.Fatal("weak record value remained visible")
		}
		assertTableLaneInvariant(t, table)
		if key.owner != state.runtime {
			t.Fatal("strong record key did not survive the clearing cycle")
		}
		state.collectUnreachable()
		if key.owner != nil {
			t.Fatal("former strong key survived a second collection")
		}
	})

	t.Run("scalar and string values stay", func(t *testing.T) {
		state := newCollectorTestState(t)
		defer state.Close()
		table, _ := newWeakTableForTest(t, state, "v", 2, 2)
		text := slotFromValue(state.String("kept"))
		table.rawSetIntegerSlot(1, numberSlot(7))
		table.rawSetIntegerSlot(2, text)
		if err := table.rawSetStringSlot("boolean", trueSlot); err != nil {
			t.Fatal(err)
		}
		if err := table.rawSetStringSlot("string", text); err != nil {
			t.Fatal(err)
		}
		rootWeakTableForTest(t, state, table)

		state.collectUnreachable()
		if table.arrayUsed != 2 || table.store.live != 2 {
			t.Fatalf(
				"noncollectable weak values = array:%d record:%d",
				table.arrayUsed,
				table.store.live,
			)
		}
	})

	t.Run("strong key can retain weak value", func(t *testing.T) {
		state := newCollectorTestState(t)
		defer state.Close()
		table, _ := newWeakTableForTest(t, state, "v", 0, 1)
		key := newTable(state, 0, 1)
		value := newTable(state, 0, 0)
		keySlot := slotFromTableObject(key)
		valueSlot := slotFromTableObject(value)
		if err := key.rawSetStringSlot("value", valueSlot); err != nil {
			t.Fatal(err)
		}
		setWeakRecordForTest(t, table, keySlot, valueSlot)
		rootWeakTableForTest(t, state, table)

		state.collectUnreachable()
		if key.owner != state.runtime || value.owner != state.runtime {
			t.Fatal("strong weak-table key did not retain its graph")
		}
		if _, found := table.rawSlot(keySlot); !found {
			t.Fatal("reachable weak value was cleared")
		}
	})

	t.Run("sparse integer record metadata clears", func(t *testing.T) {
		state := newCollectorTestState(t)
		defer state.Close()
		table, _ := newWeakTableForTest(t, state, "v", 0, 1)
		value := newTable(state, 0, 0)
		const key = 1 << 20
		table.rawSetIntegerSlot(key, slotFromTableObject(value))
		if table.store.integerKeys != 1 ||
			table.recordIntegerFloor == 0 {
			t.Fatal("test did not establish an integer record key")
		}
		rootWeakTableForTest(t, state, table)

		state.collectUnreachable()
		if value.owner != nil {
			t.Fatal("sparse weak value survived")
		}
		if table.store.integerKeys != 0 ||
			table.recordIntegerFloor != 0 {
			t.Fatalf(
				"integer metadata after clear = keys:%d floor:%d",
				table.store.integerKeys,
				table.recordIntegerFloor,
			)
		}
		table.rawSetIntegerSlot(1, numberSlot(3))
		if table.array.len() == 0 ||
			table.arrayUsed != 1 ||
			table.store.integerKeys != 0 {
			t.Fatal("cleared integer metadata obstructed dense array growth")
		}
		assertTableLaneInvariant(t, table)
	})
}

func TestWeakKeyValueTableSemantics(t *testing.T) {
	t.Run("reference pair clears", func(t *testing.T) {
		state := newCollectorTestState(t)
		defer state.Close()
		table, _ := newWeakTableForTest(t, state, "kv", 0, 1)
		key := newTable(state, 0, 0)
		value := newTable(state, 0, 0)
		keySlot := slotFromTableObject(key)
		setWeakRecordForTest(
			t,
			table,
			keySlot,
			slotFromTableObject(value),
		)
		rootWeakTableForTest(t, state, table)

		state.collectUnreachable()
		if key.owner != nil || value.owner != nil {
			t.Fatal("weak key/value pair remained reachable")
		}
		if _, found := table.rawSlot(keySlot); found {
			t.Fatal("weak key/value record remained visible")
		}
	})

	t.Run("noncollectable pair stays", func(t *testing.T) {
		state := newCollectorTestState(t)
		defer state.Close()
		table, _ := newWeakTableForTest(t, state, "kv", 0, 1)
		key := slotFromValue(state.String("key"))
		value := slotFromValue(state.String("value"))
		setWeakRecordForTest(t, table, key, value)
		rootWeakTableForTest(t, state, table)

		state.collectUnreachable()
		if got, found := table.rawSlot(key); !found ||
			!rawSlotEqual(got, value) {
			t.Fatal("string weak key/value pair was cleared")
		}
	})

	t.Run("dead key clears scalar value", func(t *testing.T) {
		state := newCollectorTestState(t)
		defer state.Close()
		table, _ := newWeakTableForTest(t, state, "kv", 0, 1)
		key := newTable(state, 0, 0)
		keySlot := slotFromTableObject(key)
		setWeakRecordForTest(t, table, keySlot, numberSlot(1))
		rootWeakTableForTest(t, state, table)

		state.collectUnreachable()
		if key.owner != nil {
			t.Fatal("weak reference key survived")
		}
		if _, found := table.rawSlot(keySlot); found {
			t.Fatal("scalar value survived its dead weak key")
		}
	})
}

func TestWeakTablesClearEveryReferenceKind(t *testing.T) {
	for _, kind := range []Kind{
		TableKind,
		FunctionKind,
		UserDataKind,
		ThreadKind,
	} {
		t.Run(kind.String(), func(t *testing.T) {
			state := newCollectorTestState(t)
			defer state.Close()
			table, _ := newWeakTableForTest(t, state, "v", 0, 1)
			value := weakReferenceSlotForTest(t, state, kind)
			if err := table.rawSetStringSlot("value", value); err != nil {
				t.Fatal(err)
			}
			rootWeakTableForTest(t, state, table)

			state.collectUnreachable()
			if referenceSlotHeader(value).owner != nil {
				t.Fatalf("unreachable weak %s survived", kind)
			}
			if _, found := table.rawStringSlot("value"); found {
				t.Fatalf("weak %s remained visible", kind)
			}
		})
	}
}

func TestWeakTableHostRootsAndDeletedKeyContinuation(t *testing.T) {
	t.Run("host handles root both weak sides", func(t *testing.T) {
		state := newCollectorTestState(t)
		defer state.Close()
		table, metatable := newWeakTableForTest(t, state, "kv", 0, 1)
		key, err := state.NewTable(0, 0)
		if err != nil {
			t.Fatal(err)
		}
		value, err := state.NewTable(0, 0)
		if err != nil {
			t.Fatal(err)
		}
		keySlot := slotFromTableObject(key.runtimeObject())
		valueSlot := slotFromTableObject(value.runtimeObject())
		setWeakRecordForTest(t, table, keySlot, valueSlot)
		rootWeakTableForTest(t, state, table)

		state.collectUnreachable()
		if key.runtimeObject().owner != state.runtime ||
			value.runtimeObject().owner != state.runtime ||
			metatable.owner != state.runtime {
			t.Fatal("host root or weak-table metatable was swept")
		}
		if _, found := table.rawSlot(keySlot); !found {
			t.Fatal("host-rooted weak pair was cleared")
		}
		runtime.KeepAlive(key)
		runtime.KeepAlive(value)
	})

	t.Run("weak-value clearing preserves next", func(t *testing.T) {
		state := newCollectorTestState(t)
		defer state.Close()
		table, _ := newWeakTableForTest(t, state, "v", 0, 1)
		key := newTable(state, 0, 0)
		value := newTable(state, 0, 0)
		keySlot := slotFromTableObject(key)
		setWeakRecordForTest(
			t,
			table,
			keySlot,
			slotFromTableObject(value),
		)
		rootWeakTableForTest(t, state, table)
		mustRootCollectorObject(t, state, "weak-next-key", keySlot)

		state.collectUnreachable()
		if value.owner != nil {
			t.Fatal("weak value survived collection")
		}
		hash, err := hashTableKey(keySlot)
		if err != nil {
			t.Fatal(err)
		}
		index, found := table.store.findContinuation(keySlot, hash)
		if !found ||
			!table.store.entries.at(index).key.isDeadReferenceKey() {
			t.Fatal("weak clearing did not retain a non-owning continuation")
		}
		if _, _, found, nextErr := table.next(keySlot); nextErr != nil ||
			found {
			t.Fatalf(
				"next after weak clear = (found %v, err %v)",
				found,
				nextErr,
			)
		}
		if len(state.objects.weakTables) != 0 {
			t.Fatal("collection retained weak-table scratch entries")
		}
	})
}

func TestWeakClearingPreservesCollisionChainsAndCompactsOnRevival(
	t *testing.T,
) {
	state := newCollectorTestState(t)
	defer state.Close()
	table, _ := newWeakTableForTest(t, state, "v", 0, 8)

	const chainLength = 4
	var keys [chainLength]slot
	var hashes [chainLength]uint32
	var groups [8][]slot
	for attempts := 0; attempts < 128; attempts++ {
		candidate := slotFromTableObject(newTable(state, 0, 0))
		hash, err := hashTableKey(candidate)
		if err != nil {
			t.Fatal(err)
		}
		group := hash & 7
		groups[group] = append(groups[group], candidate)
		if len(groups[group]) < chainLength {
			continue
		}
		copy(keys[:], groups[group][:chainLength])
		for index, key := range keys {
			hashes[index], err = hashTableKey(key)
			if err != nil {
				t.Fatal(err)
			}
		}
		break
	}
	if keys[chainLength-1].ref == nil {
		t.Fatal("could not construct a reference-key collision chain")
	}
	for index := 0; index < chainLength-1; index++ {
		setWeakRecordForTest(
			t,
			table,
			keys[index],
			slotFromTableObject(newTable(state, 0, 0)),
		)
	}
	setWeakRecordForTest(
		t,
		table,
		keys[chainLength-1],
		numberSlot(9),
	)
	rootWeakTableForTest(t, state, table)

	state.collectUnreachable()
	if table.store.live != 1 ||
		table.store.dead != chainLength-1 ||
		!table.store.shouldCompact() {
		t.Fatalf(
			"cleared collision store = live:%d dead:%d compact:%v",
			table.store.live,
			table.store.dead,
			table.store.shouldCompact(),
		)
	}
	if got, found := table.rawSlot(keys[chainLength-1]); !found ||
		!rawSlotEqual(got, numberSlot(9)) {
		t.Fatal("lookup beyond cleared collision nodes failed")
	}
	for index := 0; index < chainLength-1; index++ {
		if _, found := table.store.findContinuation(
			keys[index],
			hashes[index],
		); !found {
			t.Fatalf("cleared collision key %d lost continuation", index)
		}
		if _, _, _, err := table.next(keys[index]); err != nil {
			t.Fatalf("next from cleared collision key %d: %v", index, err)
		}
	}

	if status := table.rawSetSlot(
		keys[0],
		numberSlot(10),
	); status != tableKeyValid {
		t.Fatalf("revival status = %d", status)
	}
	if table.store.live != 2 ||
		table.store.dead != 0 ||
		table.store.shouldCompact() {
		t.Fatalf(
			"revived collision store = live:%d dead:%d compact:%v",
			table.store.live,
			table.store.dead,
			table.store.shouldCompact(),
		)
	}
	for _, test := range []struct {
		key  slot
		want slot
	}{
		{key: keys[0], want: numberSlot(10)},
		{key: keys[chainLength-1], want: numberSlot(9)},
	} {
		if got, found := table.rawSlot(test.key); !found ||
			!rawSlotEqual(got, test.want) {
			t.Fatal("compaction lost a live collision field")
		}
	}
	assertTableLaneInvariant(t, table)
}

func TestWeakCollectionFailurePhases(t *testing.T) {
	t.Run("marking failure is retryable", func(t *testing.T) {
		state := newCollectorTestState(t)
		defer state.Close()
		table, _ := newWeakTableForTest(t, state, "k", 1, 0)
		rogue := &tableObject{}
		table.rawSetIntegerSlot(1, slot{
			ref:  unsafe.Pointer(rogue),
			bits: uint64(TableKind),
		})
		rootWeakTableForTest(t, state, table)

		assertCollectorPanic(t, func() {
			state.collectUnreachable()
		})
		if state.objects.phase != collectionIdle ||
			len(state.objects.weakTables) != 0 {
			t.Fatal("marking failure retained weak-table state")
		}
		table.rawSetIntegerSlot(1, nilSlot)
		if swept := state.collectUnreachable(); swept.total() != 0 {
			t.Fatalf("retry swept a live object: %+v", swept)
		}
	})

	t.Run("clearing failure poisons collector", func(t *testing.T) {
		state := newCollectorTestState(t)
		defer state.Close()
		foreign := newCollectorTestState(t)
		defer foreign.Close()
		table, _ := newWeakTableForTest(t, state, "v", 0, 1)
		local := newTable(state, 0, 0)
		if err := table.rawSetStringSlot(
			"value",
			slotFromTableObject(local),
		); err != nil {
			t.Fatal(err)
		}
		hash := uint32(hashString("value"))
		index, found := table.store.findStoredString("value", hash)
		if !found {
			t.Fatal("could not locate weak-value test field")
		}
		writeSlot(
			&table.store.entries.at(index).value,
			slotFromTableObject(newTable(foreign, 0, 0)),
		)
		rootWeakTableForTest(t, state, table)

		assertCollectorPanic(t, func() {
			state.collectUnreachable()
		})
		if state.objects.phase != collectionBroken ||
			len(state.objects.weakTables) != 0 {
			t.Fatal("destructive weak-clear failure did not poison cleanly")
		}
		assertCollectorPanic(t, func() {
			state.collectUnreachable()
		})
	})
}

func TestWeakCollectionDropsOversizedScratch(t *testing.T) {
	state := newCollectorTestState(t)
	defer state.Close()
	root := newTable(state, maximumRetainedCollectionWork+1, 0)
	metatable := newTable(state, 0, 1)
	if err := metatable.rawSetStringSlot(
		metamethodNames[metaMode],
		slotFromValue(state.String("kv")),
	); err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= maximumRetainedCollectionWork+1; index++ {
		table := newTable(state, 0, 0)
		table.metatable = metatable
		root.rawSetIntegerSlot(index, slotFromTableObject(table))
	}
	mustRootCollectorObject(
		t,
		state,
		"weak-table-root",
		slotFromTableObject(root),
	)

	state.collectUnreachable()
	if state.objects.weakTables != nil {
		t.Fatal("collection retained oversized weak-table scratch")
	}
}

func TestWeakTableWarmCollectionDoesNotAllocate(t *testing.T) {
	requireStableAllocationAccounting(t)
	state := newCollectorTestState(t)
	defer state.Close()
	table, _ := newWeakTableForTest(t, state, "kv", 0, 1)
	if err := table.rawSetStringSlot("scalar", numberSlot(1)); err != nil {
		t.Fatal(err)
	}
	rootWeakTableForTest(t, state, table)
	state.collectUnreachable()

	allocations := testing.AllocsPerRun(1000, func() {
		if swept := state.collectUnreachable(); swept.total() != 0 {
			panic("stable weak collection swept a live object")
		}
	})
	if allocations != 0 {
		t.Fatalf("warm weak collection allocations = %v; want 0", allocations)
	}
}

func newWeakTableForTest(
	t *testing.T,
	state *State,
	mode string,
	arrayHint int,
	recordHint int,
) (*tableObject, *tableObject) {
	t.Helper()
	metatable := newTable(state, 0, 1)
	if err := metatable.rawSetStringSlot(
		metamethodNames[metaMode],
		slotFromValue(state.String(mode)),
	); err != nil {
		t.Fatal(err)
	}
	table := newTable(state, arrayHint, recordHint)
	table.metatable = metatable
	return table, metatable
}

func rootWeakTableForTest(
	t *testing.T,
	state *State,
	table *tableObject,
) {
	t.Helper()
	mustRootCollectorObject(
		t,
		state,
		"weak-table",
		slotFromTableObject(table),
	)
}

func setWeakRecordForTest(
	t *testing.T,
	table *tableObject,
	key slot,
	value slot,
) {
	t.Helper()
	if status := table.rawSetSlot(key, value); status != tableKeyValid {
		t.Fatalf("weak record insertion status = %d", status)
	}
}

func weakReferenceSlotForTest(
	t *testing.T,
	state *State,
	kind Kind,
) slot {
	t.Helper()
	switch kind {
	case TableKind:
		return slotFromTableObject(newTable(state, 0, 0))
	case FunctionKind:
		function := newNativeFunctionOwned(
			state,
			state.main.globals,
			func(frame Frame) Outcome {
				return frame.Return()
			},
			nil,
		)
		return slotFromFunctionObject(function)
	case UserDataKind:
		return slotFromUserDataObject(
			newUserDataObject(state, nil, nil, nil),
		)
	case ThreadKind:
		entry := newNativeFunctionOwned(
			state,
			state.main.globals,
			func(frame Frame) Outcome {
				return frame.Return()
			},
			nil,
		)
		thread, err := state.newThreadObject(slotFromFunctionObject(entry))
		if err != nil {
			t.Fatal(err)
		}
		return slotFromThreadObject(thread)
	default:
		t.Fatalf("unsupported weak reference kind %s", kind)
		return nilSlot
	}
}
