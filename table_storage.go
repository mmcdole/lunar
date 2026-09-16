package lua

import (
	"cmp"
	"math"
	"math/bits"
	"slices"
	"unsafe"
)

type tableKeyStatus uint8

const (
	tableKeyValid tableKeyStatus = iota
	tableKeyNil
	tableKeyNaN
)

func normalizeTableKey(
	key slot,
) (
	normalized slot,
	index int,
	arrayKey bool,
	hash uint32,
	status tableKeyStatus,
) {
	normalized = key
	switch normalized.kind() {
	case NilKind:
		status = tableKeyNil
		return
	case NumberKind:
		number := math.Float64frombits(normalized.bits)
		if math.IsNaN(number) {
			status = tableKeyNaN
			return
		}
		if number == 0 {
			normalized.bits = 0
		}
		if candidate, ok := positiveIntegerIndex(number); ok {
			index = candidate
			arrayKey = true
		}
		if !arrayKey {
			hash = hashNumber(number)
		}
	case StringKind:
		hash = uint32(stringSlotHash(normalized))
	default:
		hash = hashReference(normalized)
	}
	return
}

func (table *tableObject) rawSlot(key slot) (slot, bool) {
	switch key.kind() {
	case NilKind:
		return nilSlot, false
	case NumberKind:
		number := math.Float64frombits(key.bits)
		index := int(number)
		// Only an exact index inside the allocated array can use this slot.
		// Other numeric keys continue directly to the record store.
		if uint(index-1) < uint(table.array.len()) && float64(index) == number {
			value := *table.array.at(index - 1)
			return value, !value.isNil()
		}
		if math.IsNaN(number) {
			return nilSlot, false
		}
		if number == 0 {
			key.bits = 0
		}
		return table.store.get(key, hashNumber(number))
	case StringKind:
		return table.store.get(key, uint32(stringSlotHash(key)))
	default:
		return table.store.get(key, hashReference(key))
	}
}

func (table *tableObject) rawStringKeySlot(
	key slot,
	hash uint32,
) (slot, bool) {
	return table.store.getStringSlot(key, hash)
}

func (table *tableObject) resolveNormalizedSlot(
	key slot,
	index int,
	arrayKey bool,
	hash uint32,
) (slot, tableLocation, bool) {
	if arrayKey {
		if index <= table.array.len() {
			value := *table.array.at(index - 1)
			return value,
				tableLocation{
					index: index - 1,
					lane:  tableArrayLane,
				},
				!value.isNil()
		}
		number := float64(index)
		key = slot{bits: math.Float64bits(number)}
		hash = hashNumber(number)
	}
	storeIndex, found := table.store.find(key, hash)
	if !found {
		return nilSlot, tableLocation{}, false
	}
	return table.store.entries.at(storeIndex).value,
		tableLocation{
			index: storeIndex,
			lane:  tableHashLane,
		},
		true
}

func (table *tableObject) resolveStringKeySlot(
	key slot,
	hash uint32,
) (slot, tableLocation, bool) {
	storeIndex, found := table.store.findStringSlot(key, hash)
	if !found {
		return nilSlot, tableLocation{}, false
	}
	return table.store.entries.at(storeIndex).value,
		tableLocation{
			index: storeIndex,
			lane:  tableHashLane,
		},
		true
}

func (table *tableObject) replaceResolvedSlot(
	location tableLocation,
	value slot,
) {
	var current *slot
	switch location.lane {
	case tableArrayLane:
		current = table.array.at(location.index)
	case tableHashLane:
		current = &table.store.entries.at(location.index).value
	default:
		panic("lua: invalid table storage lane")
	}
	if current.isNil() {
		panic("lua: stale table location")
	}

	if value.isNil() {
		switch location.lane {
		case tableArrayLane:
			*current = nilSlot
			table.arrayUsed--
		case tableHashLane:
			table.store.deleteAt(location.index)
			table.recordIntegerDeleted()
		}
		table.absentMetamethods = 0
		return
	}
	if !replaceTableValue(current, value) {
		return
	}
	table.absentMetamethods = 0
}

func (table *tableObject) rawSetSlot(key, value slot) tableKeyStatus {
	normalized, index, arrayKey, hash, status :=
		normalizeTableKey(key)
	if status != tableKeyValid {
		return status
	}
	table.rawSetNormalizedSlot(
		normalized,
		index,
		arrayKey,
		hash,
		value,
	)
	return tableKeyValid
}

func (table *tableObject) rawSetNormalizedSlot(
	key slot,
	index int,
	arrayKey bool,
	hash uint32,
	value slot,
) {
	if table.set(
		key,
		index,
		arrayKey,
		hash,
		value,
	) {
		table.absentMetamethods = 0
	}
}

// rawSetBoundaryNormalizedSlot admits only backing that the table will retain.
// A deleted string record keeps its original immutable key, so revival imports
// the value but not an equal incoming key with different backing.
func (table *tableObject) rawSetBoundaryNormalizedSlot(
	key slot,
	index int,
	arrayKey bool,
	hash uint32,
	value slot,
) {
	if !key.isString() {
		if value.isString() {
			if current, _, present := table.resolveNormalizedSlot(
				key,
				index,
				arrayKey,
				hash,
			); present && sameSlotRepresentation(current, value) {
				return
			}
			value = table.owner.importAcceptedSlot(value)
		}
		table.rawSetNormalizedSlot(key, index, arrayKey, hash, value)
		return
	}

	storeIndex, stored := table.store.findStored(key, hash)
	if stored {
		current := table.store.entries.at(storeIndex).value
		if sameSlotRepresentation(current, value) {
			return
		}
		if value.isString() {
			value = table.owner.importAcceptedSlot(value)
		}
		if table.setStoredRecord(storeIndex, key, value) {
			table.absentMetamethods = 0
		}
		return
	}
	if value.isNil() {
		return
	}
	key = table.owner.importAcceptedSlot(key)
	if value.isString() {
		value = table.owner.importAcceptedSlot(value)
	}
	table.insertNewField(key, value, hash, 0)
	table.absentMetamethods = 0
}

// Integer keys cannot name string-keyed metamethods, so rawSetIntegerSlot
// preserves the absence cache.
func (table *tableObject) rawSetIntegerSlot(key int, value slot) {
	table.setInteger(key, value)
}

// shiftSparseIntegerRangeUp moves every raw integer field in [first, last] to
// the following integer key. It is the storage-aware counterpart to a
// descending field-by-field shift: holes delete their destination, while
// numeric keys that are not exact integers are left alone.
//
// Callers use this only when the integer range is much wider than the table's
// physical storage. The common dense case stays on the allocation-free raw
// loop. A small stack buffer also keeps sparse pathological ranges
// allocation-free.
func (table *tableObject) shiftSparseIntegerRangeUp(first, last int) {
	if first > last {
		return
	}

	const inlineValues = 32
	var inline [inlineValues]integerTableValue
	values := inline[:0]

	arrayFirst := first
	if arrayFirst < 1 {
		arrayFirst = 1
	}
	array := table.array.values()
	arrayLast := last
	if arrayLast > len(array) {
		arrayLast = len(array)
	}
	for key := arrayFirst; key <= arrayLast; key++ {
		value := array[key-1]
		if !value.isNil() {
			values = append(values, integerTableValue{
				key:   key,
				value: value,
			})
		}
	}

	entries := table.store.entries.values()
	for index := range entries {
		entry := &entries[index]
		if entry.hash == entryHashEmpty ||
			entry.value.isNil() {
			continue
		}
		key, ok := exactIntegerTableKey(entry.key)
		if !ok {
			continue
		}
		if key > 0 && key <= len(array) {
			// growArray maintains the single-lane invariant. Be defensive
			// against malformed internal state without treating a masked
			// hash entry as a visible source.
			continue
		}
		if key < first || key > last {
			continue
		}
		values = append(values, integerTableValue{
			key:   key,
			value: entry.value,
		})
	}

	slices.SortFunc(values, func(left, right integerTableValue) int {
		return cmp.Compare(left.key, right.key)
	})

	// The descending raw loop writes last+1 first. Preserve that mutation
	// order so array promotion behaves the same.
	if _, occupied := table.rawIntSlot(last + 1); occupied &&
		(len(values) == 0 || values[len(values)-1].key != last) {
		table.rawSetIntegerSlot(last+1, nilSlot)
	}
	for index := len(values) - 1; index >= 0; index-- {
		current := values[index]
		table.rawSetIntegerSlot(current.key+1, current.value)
		if current.key > first &&
			(index == 0 || values[index-1].key != current.key-1) {
			table.rawSetIntegerSlot(current.key, nilSlot)
		}
	}
}

func (table *tableObject) rawSetList(first int, values []slot) {
	if len(values) == 0 {
		return
	}
	if first == table.array.len()+1 && table.store.integerKeys == 0 {
		last := len(values)
		for last > 0 && values[last-1].isNil() {
			last--
		}
		if last == 0 {
			return
		}
		if table.array.len() <= maximumTableArrayCapacity &&
			last <= maximumTableArrayCapacity-table.array.len() {
			if table.store.shouldCompact() {
				// SETLIST appends new fields, so it is the same legal
				// compaction seam as any other insertion.
				table.rehashStore(table.store.entries.len())
			}

			oldLength := table.array.len()
			table.growArray(oldLength + last)
			array := table.array.values()
			inserted := 0
			for index, value := range values[:last] {
				writeSlot(&array[oldLength+index], value)
				if !value.isNil() {
					inserted++
				}
			}
			// SETLIST writes only positive integer keys, so the string-keyed
			// metamethod absence cache remains valid.
			table.arrayUsed += inserted
			return
		}
	}

	for offset, value := range values {
		table.rawSetIntegerSlot(first+offset, value)
	}
}

func (table *tableObject) set(
	key slot,
	index int,
	arrayKey bool,
	hash uint32,
	value slot,
) bool {
	if arrayKey {
		return table.setInteger(index, value)
	}

	if storeIndex, stored := table.store.findStored(key, hash); stored {
		return table.setStoredRecord(storeIndex, key, value)
	}
	if value.isNil() {
		return false
	}
	table.insertNewField(key, value, hash, 0)
	return true
}

func (table *tableObject) setStoredRecord(
	index int,
	key slot,
	value slot,
) bool {
	entry := table.store.entries.at(index)
	current := entry.value
	if !current.isNil() && !value.isNil() {
		return replaceTableValue(&entry.value, value)
	}
	return table.setStoredRecordSlow(index, key, value)
}

func (table *tableObject) setStoredRecordSlow(
	index int,
	key slot,
	value slot,
) bool {
	entry := table.store.entries.at(index)
	current := entry.value
	if current.isNil() {
		if value.isNil() {
			return false
		}
		if entry.key.isDeadReferenceKey() {
			if !deadReferenceKeyMatches(entry.key, key) {
				panic("lua: mismatched dead-key revival")
			}
			writeSlot(&entry.key, key)
		}
		if table.store.shouldCompact() {
			storedKey, hash := entry.key, entry.hash
			table.rehashStore(table.store.entries.len())
			table.insertNewField(
				storedKey,
				value,
				hash,
				recordIntegerClass(storedKey),
			)
		} else {
			table.store.reviveAt(index, value)
			table.recordIntegerInserted(entry.key)
		}
		return true
	}
	if value.isNil() {
		table.store.deleteAt(index)
		table.recordIntegerDeleted()
		return true
	}
	panic("lua: invalid slow table record update")
}

func (table *tableObject) insertNewField(
	key, value slot,
	hash uint32,
	integerClass uint8,
) {
	if value.isNil() {
		panic("lua: inserting a nil table record")
	}
	if table.store.entries.len() != 0 {
		if table.store.shouldCompact() {
			table.rehashStore(table.store.entries.len())
		}
		if table.store.insertAbsent(key, value, hash) {
			table.recordIntegerInsertedClass(integerClass)
			return
		}
	}
	if table.canGrowRecordStore(integerClass) {
		capacity := minimumStoreCapacity
		if table.store.entries.len() != 0 {
			capacity = growTableStoreCapacity(
				table.store.entries.len(),
			)
		}
		table.rehashStore(capacity)
		if !table.store.insertAbsent(key, value, hash) {
			panic("lua: table record growth exhausted its store")
		}
		table.recordIntegerInsertedClass(integerClass)
		return
	}
	table.redistributeForInsert(key, value, hash)
}

func (table *tableObject) canGrowRecordStore(pendingClass uint8) bool {
	arraySize := table.array.len()
	arrayExponent := -1
	switch {
	case arraySize == 0:
	case arraySize&(arraySize-1) != 0:
		return false
	case table.arrayUsed <= arraySize/2:
		return false
	default:
		arrayExponent = bits.Len(uint(arraySize - 1))
	}

	floor := table.recordIntegerFloor
	if floor == 0 && table.store.integerKeys != 0 {
		// A directly assembled test fixture has no trustworthy summary.
		return false
	}
	if pendingClass != 0 &&
		(floor == 0 || pendingClass < floor) {
		floor = pendingClass
	}
	if floor == 0 || floor == recordIntegerAboveArray {
		return true
	}

	exponent := int(floor - 1)
	if exponent <= arrayExponent {
		return false
	}
	totalIntegers := uint64(table.arrayUsed) +
		uint64(table.store.integerKeys)
	if pendingClass != 0 {
		totalIntegers++
	}
	candidate := uint64(1) << exponent
	return totalIntegers <= candidate/2
}

func (table *tableObject) recordIntegerInserted(key slot) {
	table.recordIntegerInsertedClass(recordIntegerClass(key))
}

func (table *tableObject) recordIntegerInsertedClass(class uint8) {
	if class != 0 &&
		(table.recordIntegerFloor == 0 ||
			class < table.recordIntegerFloor) {
		table.recordIntegerFloor = class
	}
}

func (table *tableObject) recordIntegerDeleted() {
	if table.store.integerKeys == 0 {
		table.recordIntegerFloor = 0
	}
}

func recordIntegerClass(key slot) uint8 {
	integer, ok := arrayIndex(key)
	if !ok {
		return 0
	}
	return integerRecordClass(integer)
}

func integerRecordClass(integer int) uint8 {
	if integer <= 0 {
		return 0
	}
	if integer > maximumTableArrayCapacity {
		return recordIntegerAboveArray
	}
	return uint8(bits.Len(uint(integer-1)) + 1)
}

func (table *tableObject) setInteger(index int, value slot) bool {
	if index > 0 &&
		index <= table.array.len() &&
		(value.isNil() ||
			!table.array.at(index-1).isNil()) {
		// Existing array fields and absent nil writes cannot release record
		// tombstones. Keep the dense update path out of insertion policy.
		return table.setArray(index, value)
	}

	// Updating an existing field must retain its next continuation position.
	// Keep the common sparse update in this first mutation frame as well,
	// rather than entering allocation policy through another call.
	if index > table.array.len() && table.store.integerKeys != 0 {
		number := float64(index)
		key := slot{bits: math.Float64bits(number)}
		if storedIndex, found := table.store.find(
			key,
			hashNumber(number),
		); found {
			entry := table.store.entries.at(storedIndex)
			if value.isNil() {
				table.store.deleteAt(storedIndex)
				table.recordIntegerDeleted()
				return true
			}
			return replaceTableValue(&entry.value, value)
		}
	}
	return table.setIntegerUnresolved(index, value)
}

func (table *tableObject) setIntegerUnresolved(
	index int,
	value slot,
) bool {
	arrayLength := table.array.len()
	if !value.isNil() &&
		index > 0 &&
		(index <= arrayLength ||
			table.store.integerKeys == 0) {
		// With no live sparse integer, this key cannot already exist in the
		// record lane. Compact before the cheap array-admission decision.
		if table.store.shouldCompact() {
			table.rehashStore(table.store.entries.len())
		}
		if target := table.directArrayGrowth(index); target != 0 {
			table.growArrayExact(target)
			writeSlot(table.array.at(index-1), value)
			table.arrayUsed++
			return true
		}
		if table.admitsArrayInsert(index) {
			return table.setArray(index, value)
		}
	}

	number := float64(index)
	key := slot{bits: math.Float64bits(number)}
	hash := hashNumber(number)

	// Existing record fields update or delete in place. A positive integer
	// covered by the array cannot also be live in the record store; ignoring
	// an old tombstone there keeps reinsertion in the visible array lane.
	if index <= 0 && table.store.entries.len() != 0 {
		if storedIndex, found := table.store.find(key, hash); found {
			entry := table.store.entries.at(storedIndex)
			if value.isNil() {
				table.store.deleteAt(storedIndex)
				table.recordIntegerDeleted()
				return true
			}
			return replaceTableValue(&entry.value, value)
		}
	}

	if value.isNil() {
		return false
	}
	if (index <= 0 || index > arrayLength) &&
		table.store.entries.len() != 0 {
		// The live lookup above is the steady-state path. Only a genuine
		// insertion needs to recognize a retained next tombstone.
		if storedIndex, stored :=
			table.store.findStored(key, hash); stored {
			return table.setStoredRecord(storedIndex, key, value)
		}
	}
	if index > 0 && table.store.shouldCompact() {
		// Any logical insertion makes next traversal order undefined. Use
		// that permitted seam to release dead record keys even when the new
		// integer itself belongs in the array lane. This check must follow
		// findStored: an existing sparse field must retain its position.
		table.rehashStore(table.store.entries.len())
	}
	if table.admitsArrayInsert(index) {
		return table.setArray(index, value)
	}

	table.insertNewField(
		key,
		value,
		hash,
		integerRecordClass(index),
	)
	return true
}

func (table *tableObject) admitsArrayInsert(index int) bool {
	if index <= 0 || index > maximumTableArrayCapacity {
		return false
	}
	if index <= table.array.len() {
		return true
	}
	// This small Go allocation class is intentionally more permissive than
	// the global PUC density rule: four slots cost less than the first
	// unhinted four-node record store.
	if index <= initialArrayCapacity {
		return true
	}
	if index > table.array.cap() || table.store.integerKeys != 0 {
		return false
	}
	// Reserved capacity has already paid the memory cost. It may therefore
	// accept a dense key without forcing the fresh allocation at which PUC
	// Lua would recompute both table parts.
	return table.arrayUsed+1 > index/2
}

func (table *tableObject) directArrayGrowth(index int) int {
	if index <= initialArrayCapacity ||
		index <= table.array.cap() ||
		index > maximumTableArrayCapacity ||
		table.store.integerKeys != 0 {
		return 0
	}
	exponent := bits.Len(uint(index - 1))
	target := 1 << exponent
	if table.arrayUsed+1 <= target/2 {
		return 0
	}
	return target
}

func (table *tableObject) growArrayExact(length int) {
	previousStorage := tableStorageRetainedBytes(table)
	arrayVector := makeTableVector[slot](length, length)
	array := arrayVector.values()
	oldArray := table.array.values()
	copy(array, oldArray)
	for index := len(oldArray); index < length; index++ {
		array[index] = nilSlot
	}
	table.array = arrayVector
	table.chargeStorageGrowth(previousStorage)
}

func (table *tableObject) redistributeForInsert(
	key slot,
	value slot,
	hash uint32,
) {
	// Allocation seams are the legal point to reconsider both physical
	// lanes. Choose PUC Lua's largest power-of-two array candidate that is
	// strictly more than half occupied, rebuild the record lane to its exact
	// size class, and count the pending field once. Moving existing fields
	// between private lanes is not a logical mutation.
	previousStorage := tableStorageRetainedBytes(table)
	arraySize, arrayLive, totalLive :=
		table.densityForInsert(key)
	recordLive := totalLive - arrayLive

	oldArray := table.array.values()
	oldStore := table.store
	var arrayVector tableVector[slot]
	var array []slot
	switch {
	case arraySize == 0:
	case arraySize >= len(oldArray) &&
		arraySize <= table.array.cap():
		arrayVector = table.array.withLength(arraySize)
		array = arrayVector.values()
		for index := len(oldArray); index < arraySize; index++ {
			array[index] = nilSlot
		}
	default:
		arrayVector = makeTableVector[slot](arraySize, arraySize)
		array = arrayVector.values()
		for index := range array {
			array[index] = nilSlot
		}
	}

	var store tableStore
	recordHint := recordLive
	if recordHint > 0 &&
		oldStore.entries.len() == 0 &&
		recordHint < minimumStoreCapacity {
		recordHint = minimumStoreCapacity
	}
	recordCapacity := 0
	if recordHint > 0 {
		recordCapacity = nextPowerOfTwo(recordHint)
	}
	pendingInteger, pendingIsInteger := arrayIndex(key)
	reuseStore := pendingIsInteger &&
		pendingInteger <= arraySize &&
		arraySize >= len(oldArray) &&
		oldStore.dead == 0 &&
		oldStore.integerKeys == 0 &&
		int(oldStore.live) == recordLive &&
		oldStore.entries.len() == recordCapacity
	if reuseStore {
		store = oldStore
	} else {
		store.init(recordHint)
	}
	recordIntegerFloor := uint8(0)
	if reuseStore {
		recordIntegerFloor = table.recordIntegerFloor
	}

	arrayUnchanged := arraySize == len(oldArray)
	installedArray := 0
	if arrayUnchanged {
		installedArray = table.arrayUsed
	} else {
		for index, current := range oldArray {
			if current.isNil() {
				continue
			}
			integer := index + 1
			if integer <= arraySize {
				writeSlot(&array[index], current)
				installedArray++
				continue
			}
			number := float64(integer)
			mustInsertTableRecord(
				&store,
				&recordIntegerFloor,
				slot{bits: math.Float64bits(number)},
				current,
				hashNumber(number),
			)
		}
	}
	if !reuseStore {
		oldEntries := oldStore.entries.values()
		for index := range oldEntries {
			entry := &oldEntries[index]
			if entry.hash == entryHashEmpty ||
				entry.value.isNil() {
				continue
			}
			if integer, ok := arrayIndex(entry.key); ok &&
				integer <= arraySize {
				if !array[integer-1].isNil() {
					panic("lua: duplicate integer table field")
				}
				writeSlot(&array[integer-1], entry.value)
				installedArray++
				continue
			}
			mustInsertTableRecord(
				&store,
				&recordIntegerFloor,
				entry.key,
				entry.value,
				entry.hash,
			)
		}
	}
	if integer, ok := arrayIndex(key); ok && integer <= arraySize {
		if !array[integer-1].isNil() {
			panic("lua: redistributing an existing table field")
		}
		writeSlot(&array[integer-1], value)
		installedArray++
	} else {
		mustInsertTableRecord(
			&store,
			&recordIntegerFloor,
			key,
			value,
			hash,
		)
	}

	if installedArray != arrayLive ||
		int(store.live) != recordLive ||
		int(store.live)+installedArray != totalLive {
		panic("lua: inconsistent table redistribution")
	}
	table.array = arrayVector
	table.arrayUsed = installedArray
	table.store = store
	table.recordIntegerFloor = recordIntegerFloor
	table.chargeStorageGrowth(previousStorage)
}

func (table *tableObject) densityForInsert(
	pending slot,
) (arraySize, arrayLive, totalLive int) {
	total := uint64(table.arrayUsed) +
		uint64(table.store.live) +
		1
	maxInt := uint64(^uint(0) >> 1)
	if total > maxInt {
		panic("lua: table capacity overflow")
	}
	totalLive = int(total)

	var counts [maximumTableArrayBits + 1]int
	table.countArrayDensity(&counts)
	entries := table.store.entries.values()
	for index := range entries {
		entry := &entries[index]
		if entry.hash == entryHashEmpty ||
			entry.value.isNil() {
			continue
		}
		if integer, ok := arrayIndex(entry.key); ok {
			countArrayDensityIndex(&counts, integer)
		}
	}
	if integer, ok := arrayIndex(pending); ok {
		countArrayDensityIndex(&counts, integer)
	}

	arraySize, arrayLive = selectArrayDensity(counts)
	return arraySize, arrayLive, totalLive
}

func (table *tableObject) countArrayDensity(
	counts *[maximumTableArrayBits + 1]int,
) {
	if table.arrayUsed == 0 {
		return
	}
	exponent := bits.Len(uint(table.array.len() - 1))
	candidate := 1 << exponent
	if table.arrayUsed > candidate/2 {
		// Once the current span is itself a valid density candidate, only
		// larger candidates can win. They all include every live array slot,
		// so the exact distribution inside this span is irrelevant.
		counts[exponent] = table.arrayUsed
		return
	}
	for index, value := range table.array.values() {
		if !value.isNil() {
			countArrayDensityIndex(counts, index+1)
		}
	}
}

func selectArrayDensity(
	counts [maximumTableArrayBits + 1]int,
) (arraySize, arrayLive int) {
	cumulative := 0
	for exponent, count := range counts {
		cumulative += count
		candidate := 1 << exponent
		if cumulative > candidate/2 {
			arraySize = candidate
			arrayLive = cumulative
		}
	}
	return arraySize, arrayLive
}

func countArrayDensityIndex(
	counts *[maximumTableArrayBits + 1]int,
	index int,
) {
	if index <= 0 || index > maximumTableArrayCapacity {
		return
	}
	counts[bits.Len(uint(index-1))]++
}

func mustInsertTableRecord(
	store *tableStore,
	recordIntegerFloor *uint8,
	key slot,
	value slot,
	hash uint32,
) {
	if store.entries.len() == 0 ||
		!store.insertAbsent(key, value, hash) {
		panic("lua: table redistribution exhausted its record store")
	}
	class := recordIntegerClass(key)
	if class != 0 &&
		(*recordIntegerFloor == 0 ||
			class < *recordIntegerFloor) {
		*recordIntegerFloor = class
	}
}

func (table *tableObject) setArray(index int, value slot) bool {
	if index > table.array.len() {
		table.growArrayWith(index, value)
		table.arrayUsed++
		return true
	}
	target := table.array.at(index - 1)
	current := *target
	currentNil := current.isNil()
	valueNil := value.isNil()

	switch {
	case currentNil && valueNil:
		return false
	case currentNil:
		writeSlot(target, value)
		table.arrayUsed++
		return true
	case valueNil:
		*target = nilSlot
		table.arrayUsed--
		return true
	default:
		return replaceTableValue(target, value)
	}
}

func (table *tableObject) growArrayWith(length int, value slot) {
	table.growArray(length)
	writeSlot(table.array.at(length-1), value)
}

func (table *tableObject) growArray(length int) {
	oldArray := table.array.values()
	oldLength := len(oldArray)
	previousCapacity := table.array.cap()
	if length <= table.array.cap() {
		table.array = table.array.withLength(length)
	} else {
		capacity := growTableArrayCapacity(
			table.array.cap(),
			length,
		)
		grown := makeTableVector[slot](length, capacity)
		copy(grown.values(), oldArray)
		table.array = grown
	}
	if table.owner != nil {
		table.owner.collection.chargeCapacityGrowth(
			previousCapacity,
			table.array.cap(),
			uint64(unsafe.Sizeof(slot{})),
		)
	}
	array := table.array.values()
	for index := oldLength; index < length; index++ {
		array[index] = nilSlot
	}
	if table.store.integerKeys == 0 {
		return
	}
	// Direct growth is limited to the initial size class, reserved backing,
	// and SETLIST's bulk path. Probe only the newly covered integer keys
	// rather than scanning unrelated record fields.
	for key := oldLength + 1; key <= length; key++ {
		number := float64(key)
		keySlot := numberSlot(number)
		index, found := table.store.find(keySlot, hashNumber(number))
		if !found {
			continue
		}
		value := table.store.entries.at(index).value
		table.store.deleteAt(index)
		table.recordIntegerDeleted()
		writeSlot(&array[key-1], value)
		table.arrayUsed++
	}
}

func growTableArrayCapacity(current, length int) int {
	if current < 0 ||
		current > maximumTableArrayCapacity ||
		length <= 0 ||
		length > maximumTableArrayCapacity {
		panic("lua: table capacity overflow")
	}
	capacity := current
	if capacity == 0 {
		capacity = initialArrayCapacity
	}
	for capacity < length {
		switch {
		case capacity < 32:
			capacity *= 2
		case capacity > maximumTableArrayCapacity-capacity/2:
			capacity = maximumTableArrayCapacity
		default:
			capacity += capacity / 2
		}
	}
	return capacity
}

func (table *tableObject) rawIntSlot(key int) (slot, bool) {
	if key <= 0 {
		number := float64(key)
		return table.store.get(
			slot{bits: math.Float64bits(number)},
			hashNumber(number),
		)
	}
	if key <= table.array.len() {
		value := *table.array.at(key - 1)
		return value, !value.isNil()
	}
	number := float64(key)
	return table.store.get(
		slot{bits: math.Float64bits(number)},
		hashNumber(number),
	)
}

func isPositiveIntegerKey(key slot) bool {
	if !key.isNumber() {
		return false
	}
	_, ok := positiveIntegerIndex(math.Float64frombits(key.bits))
	return ok
}

func arrayIndex(key slot) (int, bool) {
	if !key.isNumber() {
		return 0, false
	}
	return positiveIntegerIndex(math.Float64frombits(key.bits))
}

func positiveIntegerIndex(number float64) (int, bool) {
	limit := float64(int(^uint(0) >> 1))
	const largestExactFloatInteger float64 = 1 << 53
	if limit > largestExactFloatInteger {
		limit = largestExactFloatInteger
	}
	if number < 1 || number > limit {
		return 0, false
	}
	index := int(number)
	return index, index > 0 && float64(index) == number
}

func exactIntegerTableKey(key slot) (int, bool) {
	if !key.isNumber() {
		return 0, false
	}
	number := math.Float64frombits(key.bits)
	maxInt := int(^uint(0) >> 1)
	minimum := float64(-maxInt - 1)
	maximum := float64(maxInt)
	const largestExactFloatInteger float64 = 1 << 53
	if float64(maxInt) > largestExactFloatInteger {
		minimum = -largestExactFloatInteger
		maximum = largestExactFloatInteger
	}
	if number < minimum || number > maximum ||
		math.Trunc(number) != number {
		return 0, false
	}
	index := int(number)
	return index, float64(index) == number
}

func hashTableKey(key slot) (uint32, error) {
	switch key.kind() {
	case NilKind:
		return 0, ErrInvalidKey
	case NumberKind:
		number := math.Float64frombits(key.bits)
		if math.IsNaN(number) {
			return 0, ErrInvalidKey
		}
		return hashNumber(number), nil
	case StringKind:
		return uint32(stringSlotHash(key)), nil
	default:
		return hashReference(key), nil
	}
}

func writeSlot(destination *slot, value slot) {
	if destination.ref == value.ref {
		destination.bits = value.bits
		return
	}
	destination.ref = value.ref
	destination.bits = value.bits
}

// replaceTableValue stores the latest representation unless those exact bits
// are already present. Lua equality is not the authority for assignment:
// +0 and -0 compare equal, but their sign remains observable.
func replaceTableValue(destination *slot, value slot) bool {
	current := *destination
	if sameSlotRepresentation(current, value) {
		return false
	}
	writeSlot(destination, value)
	return true
}

func sameSlotRepresentation(left, right slot) bool {
	return left.ref == right.ref && left.bits == right.bits
}

func hashNumber(number float64) uint32 {
	if number == 0 {
		number = 0
	}
	return normalizeTableHash(
		mixHash(math.Float64bits(number) ^ 0x9e3779b97f4a7c15),
	)
}

func hashReference(value slot) uint32 {
	switch value.kind() {
	case BoolKind:
		if value.ref == trueMarkerPointer {
			return normalizeTableHash(0x6eed0e9da4d94a4f)
		}
		return normalizeTableHash(0x8a5cd789635d2dff)
	case StringKind:
		return uint32(stringSlotHash(value))
	default:
		return normalizeTableHash(mixHash(uint64(uintptr(value.ref))))
	}
}

func normalizeTableHash(value uint64) uint32 {
	hash := uint32(value ^ value>>32)
	if hash == entryHashEmpty {
		return 1
	}
	return hash
}

func mixHash(value uint64) uint64 {
	value ^= value >> 30
	value *= 0xbf58476d1ce4e5b9
	value ^= value >> 27
	value *= 0x94d049bb133111eb
	value ^= value >> 31
	return value
}

func (table *tableObject) rawNormalizedSlot(
	key slot,
	index int,
	arrayKey bool,
	hash uint32,
) (slot, bool) {
	if arrayKey {
		return table.rawIntSlot(index)
	}
	return table.store.get(key, hash)
}
