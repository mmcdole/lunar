package lua

import (
	"math"
	"strconv"
	"testing"
)

func TestTableDenseAndSparseIntegerPolicy(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	table, err := newTableObjectForTest(state, 0, 0)
	if err != nil {
		t.Fatal(err)
	}

	const denseCount = 10_000
	for index := 1; index <= denseCount; index++ {
		if err := table.rawSetIntValue(index, Number(float64(index))); err != nil {
			t.Fatal(err)
		}
	}
	if got := table.rawLen(); got != denseCount {
		t.Fatalf("dense RawLen = %d, want %d", got, denseCount)
	}
	if table.array.len() != 16_384 ||
		table.arrayUsed != denseCount ||
		table.store.live != 0 {
		t.Fatalf(
			"dense storage = array:%d used:%d records:%d",
			table.array.len(),
			table.arrayUsed,
			table.store.live,
		)
	}

	sparse, err := newTableObjectForTest(state, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	const sparseIndex = 50_000_000
	if err := sparse.rawSetIntValue(sparseIndex, Bool(true)); err != nil {
		t.Fatal(err)
	}
	if sparse.array.len() != 0 {
		t.Fatalf(
			"sparse assignment materialized %d array slots",
			sparse.array.len(),
		)
	}
	if got, ok := sparse.rawGetIntValue(sparseIndex).AsBool(); !ok || !got {
		t.Fatalf("sparse lookup = (%v, %v), want (true, true)", got, ok)
	}
	if sparse.store.live != 1 {
		t.Fatalf("sparse hash count = %d, want 1", sparse.store.live)
	}

	strided, err := newTableObjectForTest(state, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	const stridedCount = 256
	for offset := 0; offset < stridedCount; offset++ {
		key := 1 + offset*257
		if err := strided.rawSetIntValue(key, Number(float64(key))); err != nil {
			t.Fatal(err)
		}
	}
	if strided.array.len() != 1 ||
		strided.arrayUsed != 1 ||
		strided.store.live != stridedCount-1 {
		t.Fatalf(
			"strided storage = array:%d used:%d records:%d",
			strided.array.len(),
			strided.arrayUsed,
			strided.store.live,
		)
	}
	for offset := 0; offset < stridedCount; offset++ {
		key := 1 + offset*257
		if got, ok := strided.rawGetIntValue(key).AsNumber(); !ok ||
			got != float64(key) {
			t.Fatalf("strided key %d = (%v, %v)", key, got, ok)
		}
	}
	assertTableStoreInvariant(t, &table.store)
	assertTableStoreInvariant(t, &sparse.store)
	assertTableLaneInvariant(t, strided)
}

func TestTableDenseLayoutIsInsertionOrderIndependent(t *testing.T) {
	const count = 1_024
	ascending := make([]int, count)
	descending := make([]int, count)
	permuted := make([]int, count)
	for index := 0; index < count; index++ {
		ascending[index] = index + 1
		descending[index] = count - index
		permuted[index] = (index*40503)&(count-1) + 1
	}

	for _, test := range []struct {
		name  string
		order []int
	}{
		{name: "ascending", order: ascending},
		{name: "descending", order: descending},
		{name: "permuted", order: permuted},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, err := New(Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			table := newTable(state.runtime, 0, 0)
			for _, key := range test.order {
				table.rawSetIntegerSlot(
					key,
					numberSlot(float64(key)),
				)
			}
			if table.array.len() != count ||
				table.array.cap() != count ||
				table.arrayUsed != count ||
				table.store.live != 0 ||
				table.store.entries.len() != 0 {
				t.Fatalf(
					"layout = array:%d/%d used:%d records:%d/%d",
					table.array.len(),
					table.array.cap(),
					table.arrayUsed,
					table.store.live,
					table.store.entries.len(),
				)
			}
			if table.rawLen() != count {
				t.Fatalf("RawLen = %d, want %d", table.rawLen(), count)
			}
			for key := 1; key <= count; key++ {
				value, found := table.rawIntSlot(key)
				if !found ||
					!rawSlotEqual(value, numberSlot(float64(key))) {
					t.Fatalf(
						"key %d = (%v, %v)",
						key,
						value.owningValue(),
						found,
					)
				}
			}
			assertTableStoreInvariant(t, &table.store)
		})
	}
}

func TestTableArraySizingPolicy(t *testing.T) {
	nonInteger := slotFromValue(Bool(true))
	tests := []struct {
		name      string
		existing  []int
		pending   slot
		arraySize int
		arrayLive int
	}{
		{
			name:    "empty",
			pending: nonInteger,
		},
		{
			name:    "two alone is not dense",
			pending: numberSlot(2),
		},
		{
			name:      "one",
			existing:  []int{1},
			pending:   nonInteger,
			arraySize: 1,
			arrayLive: 1,
		},
		{
			name:      "exact half does not promote",
			existing:  []int{1},
			pending:   numberSlot(3),
			arraySize: 1,
			arrayLive: 1,
		},
		{
			name:      "over half promotes",
			existing:  []int{1, 3},
			pending:   numberSlot(4),
			arraySize: 4,
			arrayLive: 3,
		},
		{
			name:      "larger exact half",
			existing:  []int{1, 2, 4},
			pending:   numberSlot(8),
			arraySize: 4,
			arrayLive: 3,
		},
		{
			name:      "larger over half",
			existing:  []int{1, 2, 4, 8},
			pending:   numberSlot(7),
			arraySize: 8,
			arrayLive: 5,
		},
		{
			name:     "above maximum array span",
			existing: []int{maximumTableArrayCapacity + 1},
			pending:  nonInteger,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var table tableObject
			for _, integer := range test.existing {
				number := float64(integer)
				table.store.setFixture(
					numberSlot(number),
					numberSlot(number),
					hashNumber(number),
				)
			}
			arraySize, arrayLive, totalLive :=
				table.densityForInsert(test.pending)
			if arraySize != test.arraySize ||
				arrayLive != test.arrayLive ||
				totalLive != len(test.existing)+1 {
				t.Fatalf(
					"density = size:%d live:%d total:%d; want %d/%d/%d",
					arraySize,
					arrayLive,
					totalLive,
					test.arraySize,
					test.arrayLive,
					len(test.existing)+1,
				)
			}
		})
	}

	t.Run("maximum exact half", func(t *testing.T) {
		var counts [maximumTableArrayBits + 1]int
		counts[maximumTableArrayBits] = maximumTableArrayCapacity / 2
		size, live := selectArrayDensity(counts)
		if size != 0 || live != 0 {
			t.Fatalf("exact-half maximum = (%d, %d), want (0, 0)", size, live)
		}
		counts[maximumTableArrayBits]++
		size, live = selectArrayDensity(counts)
		if size != maximumTableArrayCapacity ||
			live != maximumTableArrayCapacity/2+1 {
			t.Fatalf(
				"over-half maximum = (%d, %d)",
				size,
				live,
			)
		}
	})

	t.Run("array summary matches full scan", func(t *testing.T) {
		var random uint64 = 0x13198a2e03707344
		for length := 1; length <= 128; length++ {
			for sample := 0; sample < 16; sample++ {
				table := tableObject{
					array: makeTableVector[slot](length, length),
				}
				array := table.array.values()
				for index := range array {
					random ^= random << 13
					random ^= random >> 7
					random ^= random << 17
					array[index] = nilSlot
					if random&3 != 0 {
						array[index] = numberSlot(
							float64(index + 1),
						)
						table.arrayUsed++
					}
				}
				pending := numberSlot(float64(
					length + 1 + int(random%128),
				))
				gotSize, gotLive, gotTotal :=
					table.densityForInsert(pending)

				var counts [maximumTableArrayBits + 1]int
				for index, value := range array {
					if value.kind() != NilKind {
						countArrayDensityIndex(&counts, index+1)
					}
				}
				if integer, ok := arrayIndex(pending); ok {
					countArrayDensityIndex(&counts, integer)
				}
				wantSize, wantLive := selectArrayDensity(counts)
				if gotSize != wantSize ||
					gotLive != wantLive ||
					gotTotal != table.arrayUsed+1 {
					t.Fatalf(
						"length %d sample %d = %d/%d/%d; want %d/%d/%d",
						length,
						sample,
						gotSize,
						gotLive,
						gotTotal,
						wantSize,
						wantLive,
						table.arrayUsed+1,
					)
				}
			}
		}
	})

	t.Run("initial array size class", func(t *testing.T) {
		var table tableObject
		for index := 1; index <= initialArrayCapacity; index++ {
			if !table.admitsArrayInsert(index) {
				t.Fatalf("initial index %d was not admitted", index)
			}
		}
		if table.admitsArrayInsert(initialArrayCapacity + 1) {
			t.Fatal("initial array class admitted index 5")
		}
		if table.admitsArrayInsert(maximumTableArrayCapacity + 1) {
			t.Fatal("array admitted an index above its maximum span")
		}
	})

	t.Run("maximum array index remains bounded", func(t *testing.T) {
		var table tableObject
		for _, index := range []int{
			maximumTableArrayCapacity,
			maximumTableArrayCapacity + 1,
		} {
			table.rawSetIntegerSlot(index, numberSlot(float64(index)))
		}
		if table.array.len() > initialArrayCapacity {
			t.Fatalf(
				"boundary keys grew array to %d slots",
				table.array.len(),
			)
		}
		for _, index := range []int{
			maximumTableArrayCapacity,
			maximumTableArrayCapacity + 1,
		} {
			value, found := table.rawIntSlot(index)
			if !found || value.bits != math.Float64bits(float64(index)) {
				t.Fatalf(
					"boundary key %d = (%v, %t)",
					index,
					value,
					found,
				)
			}
		}
		assertTableLaneInvariant(t, &table)
	})

	t.Run("proven dense growth skips redistribution", func(t *testing.T) {
		table := tableObject{
			array: makeTableVector[slot](
				initialArrayCapacity,
				initialArrayCapacity,
			),
			arrayUsed: initialArrayCapacity,
		}
		array := table.array.values()
		for index := range array {
			array[index] = numberSlot(float64(index + 1))
		}
		if got := table.directArrayGrowth(5); got != 8 {
			t.Fatalf("growth target = %d, want 8", got)
		}
		table.rawSetIntegerSlot(5, numberSlot(5))
		if table.array.len() != 8 ||
			table.array.cap() != 8 ||
			table.arrayUsed != 5 ||
			table.store.entries.len() != 0 {
			t.Fatalf(
				"direct growth = array:%d/%d used:%d records:%d",
				table.array.len(),
				table.array.cap(),
				table.arrayUsed,
				table.store.entries.len(),
			)
		}

		holey := tableObject{
			array: makeTableVector[slot](
				initialArrayCapacity,
				initialArrayCapacity,
			),
			arrayUsed: initialArrayCapacity - 1,
		}
		holeyArray := holey.array.values()
		for index := range holeyArray {
			holeyArray[index] = nilSlot
		}
		for index := 0; index < holey.arrayUsed; index++ {
			holeyArray[index] = numberSlot(float64(index + 1))
		}
		if got := holey.directArrayGrowth(5); got != 0 {
			t.Fatalf("exact-half growth target = %d, want 0", got)
		}
	})

	t.Run("bounded geometric growth", func(t *testing.T) {
		current := maximumTableArrayCapacity*2/3 + 1
		if got := growTableArrayCapacity(
			current,
			current+1,
		); got != maximumTableArrayCapacity {
			t.Fatalf(
				"growth from %d = %d; want %d",
				current,
				got,
				maximumTableArrayCapacity,
			)
		}
		if got := growTableArrayCapacity(
			maximumTableArrayCapacity,
			maximumTableArrayCapacity,
		); got != maximumTableArrayCapacity {
			t.Fatalf(
				"growth at maximum = %d; want %d",
				got,
				maximumTableArrayCapacity,
			)
		}
		for _, test := range []struct {
			current int
			length  int
		}{
			{current: -1, length: 1},
			{
				current: maximumTableArrayCapacity + 1,
				length:  maximumTableArrayCapacity,
			},
			{current: 0, length: 0},
			{
				current: maximumTableArrayCapacity,
				length:  maximumTableArrayCapacity + 1,
			},
		} {
			func() {
				defer func() {
					if recover() == nil {
						t.Fatalf(
							"growth (%d, %d) did not panic",
							test.current,
							test.length,
						)
					}
				}()
				growTableArrayCapacity(test.current, test.length)
			}()
		}
	})
}

func TestTableKeepsExistingSparseIntegerPositionOnUpdate(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	table, err := newTableObjectForTest(state, 0, 0)
	if err != nil {
		t.Fatal(err)
	}

	if err := table.rawSetIntValue(5, Number(1)); err != nil {
		t.Fatal(err)
	}
	if table.store.live != 1 || table.array.len() != 0 {
		t.Fatalf(
			"initial sparse storage = hash %d, array %d",
			table.store.live,
			table.array.len(),
		)
	}
	if err := table.rawSetIntValue(1, Bool(true)); err != nil {
		t.Fatal(err)
	}
	if err := table.rawSetIntValue(2, Bool(true)); err != nil {
		t.Fatal(err)
	}
	number := float64(5)
	recordIndex, found := table.store.find(
		numberSlot(number),
		hashNumber(number),
	)
	if !found {
		t.Fatal("sparse key 5 was not in the record store")
	}
	if err := table.rawSetIntValue(5, Number(2)); err != nil {
		t.Fatal(err)
	}
	if table.store.live != 1 || table.store.integerKeys != 1 {
		t.Fatalf(
			"updated key moved out of hash: count=%d integerKeys=%d",
			table.store.live,
			table.store.integerKeys,
		)
	}
	if table.array.len() != 2 {
		t.Fatalf("updated array length = %d, want 2", table.array.len())
	}
	if got, ok := table.rawGetIntValue(5).AsNumber(); !ok || got != 2 {
		t.Fatalf("updated value = (%v, %v), want (2, true)", got, ok)
	}
	updatedIndex, found := table.store.find(
		numberSlot(number),
		hashNumber(number),
	)
	if !found || updatedIndex != recordIndex {
		t.Fatalf(
			"updated record moved from %d to (%d, %v)",
			recordIndex,
			updatedIndex,
			found,
		)
	}
	assertTableStoreInvariant(t, &table.store)
}

func TestTableMixedKeyMutationModel(t *testing.T) {
	state, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	table, err := newTableObjectForTest(state, 0, 0)
	if err != nil {
		t.Fatal(err)
	}

	integers := []int{-8, 0}
	for key := 1; key <= 64; key++ {
		integers = append(integers, key)
	}
	integers = append(
		integers,
		127,
		257,
		4_096,
		50_000_000,
		maximumTableArrayCapacity,
		maximumTableArrayCapacity+1,
	)
	const stringCount = 23
	strings := make([]string, stringCount)
	for index := range strings {
		strings[index] = "mixed-key-" + strconv.Itoa(index)
	}
	integerValues := make(map[int]float64, len(integers))
	stringValues := make(map[string]float64, len(strings))

	verify := func(step int) {
		t.Helper()
		for _, key := range integers {
			got := table.rawGetIntValue(key)
			want, found := integerValues[key]
			if !found {
				if !got.IsNil() {
					t.Fatalf(
						"step %d: integer %d = %v, want nil",
						step,
						key,
						got,
					)
				}
				continue
			}
			number, ok := got.AsNumber()
			if !ok || number != want {
				t.Fatalf(
					"step %d: integer %d = %v, want %v",
					step,
					key,
					got,
					want,
				)
			}
		}
		for _, key := range strings {
			got := table.rawGetStringValue(key)
			want, found := stringValues[key]
			if !found {
				if !got.IsNil() {
					t.Fatalf(
						"step %d: string %q = %v, want nil",
						step,
						key,
						got,
					)
				}
				continue
			}
			number, ok := got.AsNumber()
			if !ok || number != want {
				t.Fatalf(
					"step %d: string %q = %v, want %v",
					step,
					key,
					got,
					want,
				)
			}
		}
		assertTableLaneInvariant(t, table)
	}

	var random uint64 = 0x243f6a8885a308d3
	const steps = 3_000
	for step := 1; step <= steps; step++ {
		random ^= random << 13
		random ^= random >> 7
		random ^= random << 17
		choice := int(random % uint64(len(integers)+len(strings)))
		deleting := random>>32&3 == 0
		value := float64(step)
		if choice < len(integers) {
			key := integers[choice]
			if deleting {
				if err := table.rawSetIntValue(key, Nil()); err != nil {
					t.Fatal(err)
				}
				delete(integerValues, key)
			} else {
				if err := table.rawSetIntValue(key, Number(value)); err != nil {
					t.Fatal(err)
				}
				integerValues[key] = value
			}
		} else {
			key := strings[choice-len(integers)]
			if deleting {
				if err := table.rawSetStringValue(key, Nil()); err != nil {
					t.Fatal(err)
				}
				delete(stringValues, key)
			} else {
				if err := table.rawSetStringValue(key, Number(value)); err != nil {
					t.Fatal(err)
				}
				stringValues[key] = value
			}
		}
		if step%47 == 0 {
			verify(step)
		}
	}
	verify(steps)

	fractional := Number(1.5)
	if err := table.rawSetValue(fractional, Number(17)); err != nil {
		t.Fatal(err)
	}
	if got, err := table.rawGetValue(fractional); err != nil {
		t.Fatal(err)
	} else if number, ok := got.AsNumber(); !ok || number != 17 {
		t.Fatalf("fractional key = %v, want 17", got)
	}
	if err := table.rawSetValue(fractional, Nil()); err != nil {
		t.Fatal(err)
	}
	if got, err := table.rawGetValue(fractional); err != nil || !got.IsNil() {
		t.Fatalf("deleted fractional key = (%v, %v), want nil", got, err)
	}
	assertTableLaneInvariant(t, table)
}

func assertTableLaneInvariant(t *testing.T, table *tableObject) {
	t.Helper()
	arrayUsed := 0
	for _, value := range table.array.values() {
		if value.kind() != NilKind {
			arrayUsed++
		}
	}
	if arrayUsed != table.arrayUsed {
		t.Fatalf(
			"array usage = %d, metadata = %d",
			arrayUsed,
			table.arrayUsed,
		)
	}
	recordIntegerFloor := uint8(0)
	entries := table.store.entries.values()
	for index := range entries {
		entry := &entries[index]
		if entry.hash == entryHashEmpty ||
			entry.value.kind() == NilKind {
			continue
		}
		class := recordIntegerClass(entry.key)
		if class != 0 &&
			(recordIntegerFloor == 0 ||
				class < recordIntegerFloor) {
			recordIntegerFloor = class
		}
		if integer, ok := arrayIndex(entry.key); ok &&
			integer <= table.array.len() {
			t.Fatalf(
				"record integer %d overlaps array span %d",
				integer,
				table.array.len(),
			)
		}
	}
	switch {
	case table.store.integerKeys == 0 &&
		table.recordIntegerFloor != 0:
		t.Fatalf(
			"record integer floor = %d with no integer records",
			table.recordIntegerFloor,
		)
	case table.store.integerKeys != 0 &&
		(table.recordIntegerFloor == 0 ||
			table.recordIntegerFloor > recordIntegerFloor):
		t.Fatalf(
			"record integer floor = %d, actual minimum = %d",
			table.recordIntegerFloor,
			recordIntegerFloor,
		)
	}
	assertTableStoreInvariant(t, &table.store)
}

func TestTableRedistribution(t *testing.T) {
	t.Run("natural mixed sequence", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		table := newTable(state.runtime, 0, 0)

		for key := 1; key <= 10; key++ {
			table.rawSetIntegerSlot(key, numberSlot(float64(key)))
		}
		table.rawSetIntegerSlot(14, numberSlot(140))
		if table.array.len() != 16 ||
			table.arrayUsed != 11 ||
			table.store.integerKeys != 0 {
			t.Fatalf(
				"pre-spill storage = array:%d used:%d integers:%d",
				table.array.len(),
				table.arrayUsed,
				table.store.integerKeys,
			)
		}

		table.rawSetIntegerSlot(18, numberSlot(180))
		if table.array.len() != 16 ||
			table.arrayUsed != 11 ||
			table.store.integerKeys != 1 {
			t.Fatalf(
				"post-spill storage = array:%d used:%d integers:%d",
				table.array.len(),
				table.arrayUsed,
				table.store.integerKeys,
			)
		}
		if value, found := table.rawIntSlot(18); !found ||
			!rawSlotEqual(value, numberSlot(180)) {
			t.Fatalf(
				"record key 18 = (%v, %v); want (180, true)",
				value.owningValue(),
				found,
			)
		}
		assertTableStoreInvariant(t, &table.store)
	})

	t.Run("record migration", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		table := newTable(state.runtime, 0, 0)

		table.growArray(10)
		array := table.array.values()
		for key := 1; key <= 10; key++ {
			writeSlot(&array[key-1], numberSlot(float64(key)))
			table.arrayUsed++
		}
		table.store.init(4)
		number := float64(14)
		if !table.store.insertAbsent(
			numberSlot(number),
			numberSlot(140),
			hashNumber(number),
		) {
			t.Fatal("failed to seed sparse integer field")
		}
		table.recordIntegerInserted(numberSlot(number))
		for index := 0; index < 3; index++ {
			text := "record-" + strconv.Itoa(index)
			key := stringSlot(state.runtime.strings.make(text))
			if !table.store.insertAbsent(
				key,
				numberSlot(float64(index)),
				uint32(stringSlotHash(key)),
			) {
				t.Fatal("failed to seed record field")
			}
		}
		if table.array.len() != 10 || table.store.integerKeys != 1 {
			t.Fatalf(
				"setup storage = array:%d hash integers:%d",
				table.array.len(),
				table.store.integerKeys,
			)
		}

		table.rawSetIntegerSlot(18, numberSlot(180))
		if table.array.len() != 16 {
			t.Fatalf("array length = %d; want 16", table.array.len())
		}
		if value, found := table.rawIntSlot(14); !found ||
			!rawSlotEqual(value, numberSlot(140)) {
			t.Fatalf(
				"covered key 14 = (%v, %v); want (140, true)",
				value.owningValue(),
				found,
			)
		}
		if table.store.integerKeys != 1 {
			t.Fatalf(
				"record integer count = %d; want 1",
				table.store.integerKeys,
			)
		}
		if value, found := table.rawIntSlot(18); !found ||
			!rawSlotEqual(value, numberSlot(180)) {
			t.Fatalf(
				"record key 18 = (%v, %v); want (180, true)",
				value.owningValue(),
				found,
			)
		}
		if table.arrayUsed != 11 {
			t.Fatalf("arrayUsed = %d; want 11", table.arrayUsed)
		}
		for index := 0; index < 3; index++ {
			text := "record-" + strconv.Itoa(index)
			value, found := table.rawStringSlot(text)
			if !found ||
				!rawSlotEqual(value, numberSlot(float64(index))) {
				t.Fatalf(
					"record field %q = (%v, %v)",
					text,
					value.owningValue(),
					found,
				)
			}
		}
		assertTableStoreInvariant(t, &table.store)
	})

	t.Run("sparse insertion shrinks deleted array backing", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		table := newTable(state.runtime, 0, 0)
		for key := 1; key <= 8; key++ {
			table.rawSetIntegerSlot(key, numberSlot(float64(key)))
		}
		for key := 2; key <= 8; key++ {
			table.rawSetIntegerSlot(key, nilSlot)
		}
		if table.array.len() != 8 || table.arrayUsed != 1 {
			t.Fatalf(
				"deleted layout = array:%d used:%d",
				table.array.len(),
				table.arrayUsed,
			)
		}
		if _, _, _, err := table.next(numberSlot(2)); err != nil {
			t.Fatalf("deleted array key stopped next: %v", err)
		}

		table.absentMetamethods = ^uint32(0)
		const sparse = 50_000_000
		table.rawSetIntegerSlot(sparse, numberSlot(500))
		if table.array.len() != 1 ||
			table.array.cap() != 1 ||
			table.arrayUsed != 1 ||
			table.store.live != 1 ||
			table.store.dead != 0 ||
			table.store.entries.len() != minimumStoreCapacity {
			t.Fatalf(
				"redistributed layout = array:%d/%d used:%d records:%d/%d dead:%d",
				table.array.len(),
				table.array.cap(),
				table.arrayUsed,
				table.store.live,
				table.store.entries.len(),
				table.store.dead,
			)
		}
		if table.absentMetamethods != ^uint32(0) {
			t.Fatal("numeric redistribution cleared metamethod absence cache")
		}
		for key, want := range map[int]float64{1: 1, sparse: 500} {
			value, found := table.rawIntSlot(key)
			if !found ||
				!rawSlotEqual(value, numberSlot(want)) {
				t.Fatalf(
					"key %d = (%v, %v), want %v",
					key,
					value.owningValue(),
					found,
					want,
				)
			}
		}
		assertTableStoreInvariant(t, &table.store)
	})

	t.Run("string insertion reaches the coordinator", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		table := newTable(state.runtime, 0, 0)
		for key := 1; key <= 8; key++ {
			table.rawSetIntegerSlot(key, numberSlot(float64(key)))
		}
		for key := 2; key < 8; key++ {
			table.rawSetIntegerSlot(key, nilSlot)
		}
		if err := table.rawSetStringValue("field", Number(9)); err != nil {
			t.Fatal(err)
		}
		if table.array.len() != 1 ||
			table.array.cap() != 1 ||
			table.arrayUsed != 1 ||
			table.store.live != 2 ||
			table.store.entries.len() != minimumStoreCapacity {
			t.Fatalf(
				"string redistribution = array:%d/%d used:%d records:%d/%d",
				table.array.len(),
				table.array.cap(),
				table.arrayUsed,
				table.store.live,
				table.store.entries.len(),
			)
		}
		value, found := table.rawStringSlot("field")
		if !found || !rawSlotEqual(value, numberSlot(9)) {
			t.Fatalf(
				"string field = (%v, %v)",
				value.owningValue(),
				found,
			)
		}
		if value, found := table.rawIntSlot(8); !found ||
			!rawSlotEqual(value, numberSlot(8)) {
			t.Fatalf(
				"spilled integer 8 = (%v, %v)",
				value.owningValue(),
				found,
			)
		}
		seen := make(map[string]bool, 3)
		previous := nilSlot
		for {
			key, _, found, err := table.next(previous)
			if err != nil {
				t.Fatal(err)
			}
			if !found {
				break
			}
			previous = key
			var label string
			switch key.kind() {
			case NumberKind:
				label = strconv.Itoa(
					int(math.Float64frombits(key.bits)),
				)
			case StringKind:
				label = stringSlotText(key)
			default:
				t.Fatalf("unexpected traversal key %v", key.owningValue())
			}
			if seen[label] {
				t.Fatalf("traversal repeated key %q", label)
			}
			seen[label] = true
		}
		for _, key := range []string{"1", "8", "field"} {
			if !seen[key] {
				t.Fatalf("traversal missed key %q: %v", key, seen)
			}
		}
		assertTableLaneInvariant(t, table)
	})

	t.Run("non-array numbers survive redistribution", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		table := newTable(state.runtime, 0, 0)
		for key := 1; key <= 4; key++ {
			table.rawSetIntegerSlot(key, numberSlot(float64(key)))
		}
		for _, field := range []struct {
			key   Value
			value float64
		}{
			{key: Number(-2), value: 20},
			{key: Number(1.5), value: 15},
			{key: state.String("first"), value: 30},
			{key: state.String("second"), value: 40},
		} {
			if err := table.rawSetValue(field.key, Number(field.value)); err != nil {
				t.Fatal(err)
			}
		}
		if table.store.live != 4 ||
			table.store.entries.len() != 4 {
			t.Fatalf(
				"record setup = %d/%d",
				table.store.live,
				table.store.entries.len(),
			)
		}

		table.rawSetIntegerSlot(5, numberSlot(5))
		if table.array.len() != 8 ||
			table.arrayUsed != 5 ||
			table.store.live != 4 {
			t.Fatalf(
				"redistributed storage = array:%d used:%d records:%d",
				table.array.len(),
				table.arrayUsed,
				table.store.live,
			)
		}
		for _, field := range []struct {
			key   Value
			value float64
		}{
			{key: Number(-2), value: 20},
			{key: Number(1.5), value: 15},
			{key: state.String("first"), value: 30},
			{key: state.String("second"), value: 40},
		} {
			got, err := table.rawGetValue(field.key)
			if err != nil {
				t.Fatal(err)
			}
			number, ok := got.AsNumber()
			if !ok || number != field.value {
				t.Fatalf(
					"key %v = %v, want %v",
					field.key,
					got,
					field.value,
				)
			}
		}
		assertTableLaneInvariant(t, table)
	})

	t.Run("array spill reuses unchanged record store", func(t *testing.T) {
		state, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		table := newTable(state.runtime, 0, 0)
		for key := 1; key <= 4; key++ {
			table.rawSetIntegerSlot(key, numberSlot(float64(key)))
		}
		for index := 0; index < minimumStoreCapacity; index++ {
			text := "field-" + strconv.Itoa(index)
			if err := table.rawSetStringValue(
				text,
				Number(float64(index)),
			); err != nil {
				t.Fatal(err)
			}
		}
		if table.store.entries.len() != minimumStoreCapacity ||
			table.store.live != minimumStoreCapacity {
			t.Fatalf(
				"record setup = %d/%d",
				table.store.live,
				table.store.entries.len(),
			)
		}
		backing := table.store.entries.data

		table.rawSetIntegerSlot(5, numberSlot(5))
		if table.array.len() != 8 ||
			table.arrayUsed != 5 ||
			table.store.entries.data != backing {
			t.Fatalf(
				"spill = array:%d used:%d backing:%p/%p",
				table.array.len(),
				table.arrayUsed,
				table.store.entries.data,
				backing,
			)
		}
		for index := 0; index < minimumStoreCapacity; index++ {
			text := "field-" + strconv.Itoa(index)
			value, found := table.rawStringSlot(text)
			if !found ||
				!rawSlotEqual(value, numberSlot(float64(index))) {
				t.Fatalf(
					"field %q = (%v, %v)",
					text,
					value.owningValue(),
					found,
				)
			}
		}
		assertTableStoreInvariant(t, &table.store)
	})
}
