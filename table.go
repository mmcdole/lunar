package lua

import (
	"cmp"
	"math"
	"math/bits"
	"slices"
	"unsafe"
)

const (
	initialArrayCapacity = 4
	// Lua 5.1 limits the array candidate exponent so array indices and byte
	// sizes remain representable during table rehash.
	maximumTableArrayBits     = 26
	maximumTableArrayCapacity = 1 << maximumTableArrayBits
	minimumStoreCapacity      = 4
	recordIntegerAboveArray   = maximumTableArrayBits + 2
)

type tableLane uint8

const (
	tableArrayLane tableLane = iota
	tableHashLane
)

// tableLocation is valid only until the table's next structural mutation.
// It lets an immediately following update use the slot already found by a
// lookup, mirroring Lua's resolve-once table-set path without retaining an
// interior pointer into movable Go slices.
type tableLocation struct {
	index int
	lane  tableLane
}

type integerTableValue struct {
	key   int
	value slot
}

// Table is the canonical representation of a Lua table.
//
// Its storage, metatable, traversal state, and cache generations are private.
// Table methods are raw: they never invoke Lua or consult metamethods.
// Metamethod-aware operations belong to State and Frame.
//
// A Table must not be copied after first use. Retain and pass its pointer.
type Table struct {
	objectHeader
	array             []slot
	arrayUsed         int
	store             tableStore
	metatable         *Table
	structuralVersion uint64
	absentMetamethods uint32
	// recordIntegerFloor is one plus the smallest power-of-two exponent
	// containing a positive integer record key. The value above every array
	// exponent means all known integer records exceed the array limit.
	// Deletion may leave a conservative lower value; zero means none.
	recordIntegerFloor uint8
}

func newTable(owner *runtimeState, arrayHint, recordHint int) *Table {
	table := &Table{objectHeader: objectHeader{owner: owner}}
	if arrayHint > 0 {
		table.array = make([]slot, 0, arrayHint)
	}
	table.store.init(recordHint)
	return table
}

// Value returns the owning Lua value for table.
func (table *Table) Value() Value {
	if table == nil || table.owner == nil {
		return Value{}
	}
	return objectValue(TableKind, unsafe.Pointer(table))
}

func slotFromTable(table *Table) slot {
	return objectSlot(TableKind, unsafe.Pointer(table))
}

// RawGet returns the value associated with key without invoking metamethods.
// A missing key returns Nil.
func (table *Table) RawGet(key Value) (Value, error) {
	if table == nil || table.owner == nil {
		return Value{}, ErrClosed
	}
	if err := table.owner.accept(key); err != nil {
		return Value{}, err
	}
	if value, found := table.rawSlot(slotFromValue(key)); found {
		return value.owningValue(), nil
	}
	return nilValue, nil
}

// RawSet associates key with value without invoking metamethods. Assigning Nil
// deletes the key.
func (table *Table) RawSet(key, value Value) error {
	if err := table.checkMutable(); err != nil {
		return err
	}
	if err := table.owner.accept(value); err != nil {
		return err
	}
	if err := table.owner.accept(key); err != nil {
		return err
	}
	if table.rawSetSlot(
		slotFromValue(key),
		slotFromValue(value),
	) != tableKeyValid {
		return ErrInvalidKey
	}
	return nil
}

// RawGetInt returns the value associated with an integer key without invoking
// metamethods. A missing key returns Nil.
func (table *Table) RawGetInt(key int) Value {
	if table == nil {
		return nilValue
	}
	if value, found := table.rawIntSlot(key); found {
		return value.owningValue()
	}
	return nilValue
}

// RawSetInt associates an integer key with value without invoking metamethods.
func (table *Table) RawSetInt(key int, value Value) error {
	if err := table.checkMutable(); err != nil {
		return err
	}
	if err := table.owner.accept(value); err != nil {
		return err
	}
	table.rawSetIntegerSlot(key, slotFromValue(value))
	return nil
}

// RawGetString returns the value associated with a string key without
// constructing a temporary Value or invoking metamethods.
func (table *Table) RawGetString(key string) Value {
	if value, found := table.rawStringSlot(key); found {
		return value.owningValue()
	}
	return nilValue
}

func (table *Table) rawStringSlot(key string) (slot, bool) {
	if table == nil || table.owner == nil {
		return nilSlot, false
	}
	return table.store.getString(
		key,
		uint32(table.owner.strings.hash(key)),
	)
}

// RawSetString associates a string key with value without invoking
// metamethods.
func (table *Table) RawSetString(key string, value Value) error {
	if err := table.checkMutable(); err != nil {
		return err
	}
	if err := table.owner.accept(value); err != nil {
		return err
	}
	hash := uint32(table.owner.strings.hash(key))
	valueSlot := slotFromValue(value)
	index, stored := table.store.findStoredString(key, hash)
	var structural, changed bool
	switch {
	case stored &&
		table.store.entries[index].value.kind() != NilKind &&
		valueSlot.kind() == NilKind:
		table.store.deleteAt(index)
		structural, changed = true, true
	case stored &&
		table.store.entries[index].value.kind() != NilKind:
		current := table.store.entries[index].value
		if !rawSlotEqual(current, valueSlot) {
			writeSlot(&table.store.entries[index].value, valueSlot)
			changed = true
		}
	case stored && valueSlot.kind() != NilKind:
		if table.store.shouldCompact() {
			storedKey := table.store.entries[index].key
			table.store.rehash(len(table.store.entries))
			table.insertNewField(storedKey, valueSlot, hash, 0)
		} else {
			table.store.reviveAt(index, valueSlot)
		}
		structural, changed = true, true
	case valueSlot.kind() != NilKind:
		keySlot := stringSlot(
			table.owner.strings.makeKnownHash(
				key,
				stringHash(hash),
			),
		)
		table.insertNewField(keySlot, valueSlot, hash, 0)
		structural, changed = true, true
	}
	if structural {
		table.structuralVersion++
	}
	if changed {
		table.absentMetamethods = 0
	}
	return nil
}

// RawLen returns a valid Lua border for table without invoking __len.
//
// As in Lua 5.1, the result is undefined when a table has more than one
// border.
func (table *Table) RawLen() int {
	if table == nil {
		return 0
	}

	arrayLength := len(table.array)
	if arrayLength > 0 && table.array[arrayLength-1].kind() == NilKind {
		low, high := 0, arrayLength
		for high-low > 1 {
			middle := low + (high-low)/2
			if table.array[middle-1].kind() == NilKind {
				high = middle
			} else {
				low = middle
			}
		}
		return low
	}

	if _, found := table.rawIntSlot(arrayLength + 1); !found {
		return arrayLength
	}

	low := arrayLength
	high := arrayLength + 1
	maxInt := int(^uint(0) >> 1)
	for {
		if high > maxInt/2 {
			high = maxInt
			break
		}
		next := high * 2
		if _, found := table.rawIntSlot(next); !found {
			high = next
			break
		}
		low = next
		high = next
	}
	for high-low > 1 {
		middle := low + (high-low)/2
		if _, found := table.rawIntSlot(middle); found {
			low = middle
		} else {
			high = middle
		}
	}
	return low
}

func (table *Table) next(previous slot) (key, value slot, found bool, err error) {
	if table == nil || table.owner == nil {
		return nilSlot, nilSlot, false, ErrClosed
	}

	arrayStart := 0
	storeStart := 0
	if previous.kind() != NilKind {
		// PUC Lua 5.1 treats an exact positive integer within the allocated
		// array part as a traversal position even when its slot is nil. This
		// permits next to continue after deletion of the current array field.
		if index, ok := arrayIndex(previous); ok && index <= len(table.array) {
			arrayStart = index
		} else {
			hash, hashErr := hashTableKey(previous)
			if hashErr != nil {
				return nilSlot, nilSlot, false, ErrInvalidNextKey
			}
			index, exists := table.store.findContinuation(previous, hash)
			if !exists {
				return nilSlot, nilSlot, false, ErrInvalidNextKey
			}
			arrayStart = len(table.array)
			storeStart = index + 1
		}
	}

	for index := arrayStart; index < len(table.array); index++ {
		candidate := table.array[index]
		if candidate.kind() == NilKind {
			continue
		}
		return slot{bits: math.Float64bits(float64(index + 1))},
			candidate,
			true,
			nil
	}
	for index := storeStart; index < len(table.store.entries); index++ {
		entry := &table.store.entries[index]
		if entry.hash == entryHashEmpty || entry.value.kind() == NilKind {
			continue
		}
		return entry.key, entry.value, true, nil
	}
	return nilSlot, nilSlot, false, nil
}

func (table *Table) checkMutable() error {
	if table == nil || table.owner == nil || table.owner.closed.Load() {
		return ErrClosed
	}
	return nil
}

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

func (table *Table) rawSlot(key slot) (slot, bool) {
	normalized, index, arrayKey, hash, status :=
		normalizeTableKey(key)
	if status != tableKeyValid {
		return nilSlot, false
	}
	return table.rawNormalizedSlot(
		normalized,
		index,
		arrayKey,
		hash,
	)
}

func (table *Table) rawNormalizedSlot(
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

func (table *Table) resolveNormalizedSlot(
	key slot,
	index int,
	arrayKey bool,
	hash uint32,
) (slot, tableLocation, bool) {
	if arrayKey {
		if index <= len(table.array) {
			value := table.array[index-1]
			return value,
				tableLocation{
					index: index - 1,
					lane:  tableArrayLane,
				},
				value.kind() != NilKind
		}
		number := float64(index)
		key = slot{bits: math.Float64bits(number)}
		hash = hashNumber(number)
	}
	storeIndex, found := table.store.find(key, hash)
	if !found {
		return nilSlot, tableLocation{}, false
	}
	return table.store.entries[storeIndex].value,
		tableLocation{
			index: storeIndex,
			lane:  tableHashLane,
		},
		true
}

func (table *Table) replaceResolvedSlot(
	location tableLocation,
	value slot,
) {
	var current slot
	switch location.lane {
	case tableArrayLane:
		current = table.array[location.index]
	case tableHashLane:
		current = table.store.entries[location.index].value
	default:
		panic("lua: invalid table storage lane")
	}
	if current.kind() == NilKind {
		panic("lua: stale table location")
	}

	if value.kind() == NilKind {
		switch location.lane {
		case tableArrayLane:
			table.array[location.index] = nilSlot
			table.arrayUsed--
		case tableHashLane:
			table.store.deleteAt(location.index)
			table.recordIntegerDeleted()
		}
		table.recordMutation(true, true)
		return
	}
	if rawSlotEqual(current, value) {
		return
	}
	switch location.lane {
	case tableArrayLane:
		writeSlot(&table.array[location.index], value)
	case tableHashLane:
		writeSlot(&table.store.entries[location.index].value, value)
	}
	table.recordMutation(false, true)
}

func (table *Table) rawSetSlot(key, value slot) tableKeyStatus {
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

func (table *Table) rawSetNormalizedSlot(
	key slot,
	index int,
	arrayKey bool,
	hash uint32,
	value slot,
) {
	structural, changed := table.set(
		key,
		index,
		arrayKey,
		hash,
		value,
	)
	table.recordMutation(structural, changed)
}

// Integer keys cannot name string-keyed metamethods, so rawSetIntegerSlot
// preserves the absence cache.
func (table *Table) rawSetIntegerSlot(key int, value slot) {
	structural, _ := table.setInteger(key, value)
	if structural {
		table.structuralVersion++
	}
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
func (table *Table) shiftSparseIntegerRangeUp(first, last int) {
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
	arrayLast := last
	if arrayLast > len(table.array) {
		arrayLast = len(table.array)
	}
	for key := arrayFirst; key <= arrayLast; key++ {
		value := table.array[key-1]
		if value.kind() != NilKind {
			values = append(values, integerTableValue{
				key:   key,
				value: value,
			})
		}
	}

	for index := range table.store.entries {
		entry := &table.store.entries[index]
		if entry.hash == entryHashEmpty ||
			entry.value.kind() == NilKind {
			continue
		}
		key, ok := exactIntegerTableKey(entry.key)
		if !ok {
			continue
		}
		if key > 0 && key <= len(table.array) {
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
	// order so array promotion and the structural generation behave the same.
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

func (table *Table) rawSetList(first int, values []slot) {
	if len(values) == 0 {
		return
	}
	if first == len(table.array)+1 && table.store.integerKeys == 0 {
		last := len(values)
		for last > 0 && values[last-1].kind() == NilKind {
			last--
		}
		if last == 0 {
			return
		}
		if len(table.array) <= maximumTableArrayCapacity &&
			last <= maximumTableArrayCapacity-len(table.array) {
			if table.store.shouldCompact() {
				// SETLIST appends new fields, so it is the same legal
				// compaction seam as any other insertion.
				table.store.rehash(len(table.store.entries))
			}

			oldLength := len(table.array)
			table.growArray(oldLength + last)
			inserted := 0
			for index, value := range values[:last] {
				writeSlot(&table.array[oldLength+index], value)
				if value.kind() != NilKind {
					inserted++
				}
			}
			// SETLIST writes only positive integer keys, so the string-keyed
			// metamethod absence cache remains valid.
			table.arrayUsed += inserted
			table.structuralVersion += uint64(inserted)
			return
		}
	}

	for offset, value := range values {
		table.rawSetIntegerSlot(first+offset, value)
	}
}

func (table *Table) recordMutation(
	structural bool,
	changed bool,
) {
	if structural {
		table.structuralVersion++
	}
	if changed {
		table.absentMetamethods = 0
	}
}

func (table *Table) set(
	key slot,
	index int,
	arrayKey bool,
	hash uint32,
	value slot,
) (structural, changed bool) {
	if arrayKey {
		return table.setInteger(index, value)
	}

	if storeIndex, stored := table.store.findStored(key, hash); stored {
		return table.setStoredRecord(storeIndex, value)
	}
	if value.kind() == NilKind {
		return false, false
	}
	table.insertNewField(key, value, hash, 0)
	return true, true
}

func (table *Table) setStoredRecord(
	index int,
	value slot,
) (structural, changed bool) {
	entry := &table.store.entries[index]
	current := entry.value
	if current.kind() != NilKind && value.kind() != NilKind {
		if rawSlotEqual(current, value) {
			return false, false
		}
		writeSlot(&entry.value, value)
		return false, true
	}
	return table.setStoredRecordSlow(index, value)
}

func (table *Table) setStoredRecordSlow(
	index int,
	value slot,
) (structural, changed bool) {
	entry := &table.store.entries[index]
	current := entry.value
	if current.kind() == NilKind {
		if value.kind() == NilKind {
			return false, false
		}
		if table.store.shouldCompact() {
			key, hash := entry.key, entry.hash
			table.store.rehash(len(table.store.entries))
			table.insertNewField(
				key,
				value,
				hash,
				recordIntegerClass(key),
			)
		} else {
			table.store.reviveAt(index, value)
			table.recordIntegerInserted(entry.key)
		}
		return true, true
	}
	if value.kind() == NilKind {
		table.store.deleteAt(index)
		table.recordIntegerDeleted()
		return true, true
	}
	panic("lua: invalid slow table record update")
}

func (table *Table) insertNewField(
	key, value slot,
	hash uint32,
	integerClass uint8,
) {
	if value.kind() == NilKind {
		panic("lua: inserting a nil table record")
	}
	if len(table.store.entries) != 0 {
		if table.store.shouldCompact() {
			table.store.rehash(len(table.store.entries))
		}
		if table.store.insertAbsent(key, value, hash) {
			table.recordIntegerInsertedClass(integerClass)
			return
		}
	}
	if table.canGrowRecordStore(integerClass) {
		capacity := minimumStoreCapacity
		if len(table.store.entries) != 0 {
			capacity = growTableStoreCapacity(
				len(table.store.entries),
			)
		}
		table.store.rehash(capacity)
		if !table.store.insertAbsent(key, value, hash) {
			panic("lua: table record growth exhausted its store")
		}
		table.recordIntegerInsertedClass(integerClass)
		return
	}
	table.redistributeForInsert(key, value, hash)
}

func (table *Table) canGrowRecordStore(pendingClass uint8) bool {
	arraySize := len(table.array)
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

func (table *Table) recordIntegerInserted(key slot) {
	table.recordIntegerInsertedClass(recordIntegerClass(key))
}

func (table *Table) recordIntegerInsertedClass(class uint8) {
	if class != 0 &&
		(table.recordIntegerFloor == 0 ||
			class < table.recordIntegerFloor) {
		table.recordIntegerFloor = class
	}
}

func (table *Table) recordIntegerDeleted() {
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

func (table *Table) setInteger(index int, value slot) (structural, changed bool) {
	if index > 0 &&
		index <= len(table.array) &&
		(value.kind() == NilKind ||
			table.array[index-1].kind() != NilKind) {
		// Existing array fields and absent nil writes cannot release record
		// tombstones. Keep the dense update path out of insertion policy.
		return table.setArray(index, value)
	}

	// Updating an existing field must retain its next continuation position.
	// Keep the common sparse update in this first mutation frame as well,
	// rather than entering allocation policy through another call.
	if index > len(table.array) && table.store.integerKeys != 0 {
		number := float64(index)
		key := slot{bits: math.Float64bits(number)}
		if storedIndex, found := table.store.find(
			key,
			hashNumber(number),
		); found {
			entry := &table.store.entries[storedIndex]
			if value.kind() == NilKind {
				table.store.deleteAt(storedIndex)
				table.recordIntegerDeleted()
				return true, true
			}
			if rawSlotEqual(entry.value, value) {
				return false, false
			}
			writeSlot(&entry.value, value)
			return false, true
		}
	}
	return table.setIntegerUnresolved(index, value)
}

func (table *Table) setIntegerUnresolved(
	index int,
	value slot,
) (structural, changed bool) {
	if value.kind() != NilKind &&
		index > 0 &&
		(index <= len(table.array) ||
			table.store.integerKeys == 0) {
		// With no live sparse integer, this key cannot already exist in the
		// record lane. Compact before the cheap array-admission decision.
		if table.store.shouldCompact() {
			table.store.rehash(len(table.store.entries))
		}
		if target := table.directArrayGrowth(index); target != 0 {
			table.growArrayExact(target)
			writeSlot(&table.array[index-1], value)
			table.arrayUsed++
			return true, true
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
	if index <= 0 && len(table.store.entries) != 0 {
		if storedIndex, found := table.store.find(key, hash); found {
			entry := &table.store.entries[storedIndex]
			if value.kind() == NilKind {
				table.store.deleteAt(storedIndex)
				table.recordIntegerDeleted()
				return true, true
			}
			if rawSlotEqual(entry.value, value) {
				return false, false
			}
			writeSlot(&entry.value, value)
			return false, true
		}
	}

	if value.kind() == NilKind {
		return false, false
	}
	if (index <= 0 || index > len(table.array)) &&
		len(table.store.entries) != 0 {
		// The live lookup above is the steady-state path. Only a genuine
		// insertion needs to recognize a retained next tombstone.
		if storedIndex, stored :=
			table.store.findStored(key, hash); stored {
			return table.setStoredRecord(storedIndex, value)
		}
	}
	if index > 0 && table.store.shouldCompact() {
		// Any logical insertion makes next traversal order undefined. Use
		// that permitted seam to release dead record keys even when the new
		// integer itself belongs in the array lane. This check must follow
		// findStored: an existing sparse field must retain its position.
		table.store.rehash(len(table.store.entries))
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
	return true, true
}

func (table *Table) admitsArrayInsert(index int) bool {
	if index <= 0 || index > maximumTableArrayCapacity {
		return false
	}
	if index <= len(table.array) {
		return true
	}
	// This small Go allocation class is intentionally more permissive than
	// the global PUC density rule: four slots cost less than the first
	// unhinted four-node record store.
	if index <= initialArrayCapacity {
		return true
	}
	if index > cap(table.array) || table.store.integerKeys != 0 {
		return false
	}
	// Reserved capacity has already paid the memory cost. It may therefore
	// accept a dense key without forcing the fresh allocation at which PUC
	// Lua would recompute both table parts.
	return table.arrayUsed+1 > index/2
}

func (table *Table) directArrayGrowth(index int) int {
	if index <= initialArrayCapacity ||
		index <= cap(table.array) ||
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

func (table *Table) growArrayExact(length int) {
	array := make([]slot, length)
	copy(array, table.array)
	for index := len(table.array); index < length; index++ {
		array[index] = nilSlot
	}
	table.array = array
}

func (table *Table) redistributeForInsert(
	key slot,
	value slot,
	hash uint32,
) {
	// Allocation seams are the legal point to reconsider both physical
	// lanes. Choose PUC Lua's largest power-of-two array candidate that is
	// strictly more than half occupied, rebuild the record lane to its exact
	// size class, and count the pending field once. Moving existing fields
	// between private lanes is not a logical mutation.
	arraySize, arrayLive, totalLive :=
		table.densityForInsert(key)
	recordLive := totalLive - arrayLive

	oldArray := table.array
	oldStore := table.store
	var array []slot
	switch {
	case arraySize == 0:
	case arraySize >= len(oldArray) && arraySize <= cap(oldArray):
		array = oldArray[:arraySize]
		for index := len(oldArray); index < arraySize; index++ {
			array[index] = nilSlot
		}
	default:
		array = make([]slot, arraySize)
		for index := range array {
			array[index] = nilSlot
		}
	}

	var store tableStore
	recordHint := recordLive
	if recordHint > 0 &&
		len(oldStore.entries) == 0 &&
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
		len(oldStore.entries) == recordCapacity
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
			if current.kind() == NilKind {
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
		for index := range oldStore.entries {
			entry := &oldStore.entries[index]
			if entry.hash == entryHashEmpty ||
				entry.value.kind() == NilKind {
				continue
			}
			if integer, ok := arrayIndex(entry.key); ok &&
				integer <= arraySize {
				if array[integer-1].kind() != NilKind {
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
		if array[integer-1].kind() != NilKind {
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
	table.array = array
	table.arrayUsed = installedArray
	table.store = store
	table.recordIntegerFloor = recordIntegerFloor
}

func (table *Table) densityForInsert(
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
	for index := range table.store.entries {
		entry := &table.store.entries[index]
		if entry.hash == entryHashEmpty ||
			entry.value.kind() == NilKind {
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

func (table *Table) countArrayDensity(
	counts *[maximumTableArrayBits + 1]int,
) {
	if table.arrayUsed == 0 {
		return
	}
	exponent := bits.Len(uint(len(table.array) - 1))
	candidate := 1 << exponent
	if table.arrayUsed > candidate/2 {
		// Once the current span is itself a valid density candidate, only
		// larger candidates can win. They all include every live array slot,
		// so the exact distribution inside this span is irrelevant.
		counts[exponent] = table.arrayUsed
		return
	}
	for index, value := range table.array {
		if value.kind() != NilKind {
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
	if len(store.entries) == 0 ||
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

func (table *Table) setArray(index int, value slot) (structural, changed bool) {
	if index > len(table.array) {
		table.growArrayWith(index, value)
		table.arrayUsed++
		return true, true
	}
	current := table.array[index-1]
	currentNil := current.kind() == NilKind
	valueNil := value.kind() == NilKind

	switch {
	case currentNil && valueNil:
		return false, false
	case currentNil:
		writeSlot(&table.array[index-1], value)
		table.arrayUsed++
		return true, true
	case valueNil:
		table.array[index-1] = nilSlot
		table.arrayUsed--
		return true, true
	default:
		if rawSlotEqual(current, value) {
			return false, false
		}
		writeSlot(&table.array[index-1], value)
		return false, true
	}
}

func (table *Table) growArrayWith(length int, value slot) {
	table.growArray(length)
	writeSlot(&table.array[length-1], value)
}

func (table *Table) growArray(length int) {
	oldLength := len(table.array)
	if length <= cap(table.array) {
		table.array = table.array[:length]
	} else {
		capacity := growTableArrayCapacity(
			cap(table.array),
			length,
		)
		grown := make([]slot, length, capacity)
		copy(grown, table.array)
		table.array = grown
	}
	for index := oldLength; index < length; index++ {
		table.array[index] = nilSlot
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
		value := table.store.entries[index].value
		table.store.deleteAt(index)
		table.recordIntegerDeleted()
		writeSlot(&table.array[key-1], value)
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

func (table *Table) rawIntSlot(key int) (slot, bool) {
	if key <= 0 {
		number := float64(key)
		return table.store.get(
			slot{bits: math.Float64bits(number)},
			hashNumber(number),
		)
	}
	if key <= len(table.array) {
		value := table.array[key-1]
		return value, value.kind() != NilKind
	}
	number := float64(key)
	return table.store.get(
		slot{bits: math.Float64bits(number)},
		hashNumber(number),
	)
}

func isPositiveIntegerKey(key slot) bool {
	if key.kind() != NumberKind {
		return false
	}
	_, ok := positiveIntegerIndex(math.Float64frombits(key.bits))
	return ok
}

func arrayIndex(key slot) (int, bool) {
	if key.kind() != NumberKind {
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
	if key.kind() != NumberKind {
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
