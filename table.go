package lua

import (
	"math"
	"unsafe"
)

const (
	initialArrayCapacity = 4
	maxDenseArrayGap     = 8
	minimumStoreCapacity = 4
)

const (
	entryHashEmpty uint64 = 0
)

type tableEntry struct {
	key   slot
	value slot
	hash  uint64
}

type tableStore struct {
	entries     []tableEntry
	count       int
	deleted     int
	integerKeys int
}

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
	return table.store.getString(key, table.owner.strings.hash(key))
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
	hash := table.owner.strings.hash(key)
	valueSlot := slotFromValue(value)
	index, found := table.store.findString(key, hash)
	var structural, changed bool
	switch {
	case found && valueSlot.kind() == NilKind:
		table.store.deleteAt(index)
		structural, changed = true, true
	case found:
		current := table.store.entries[index].value
		if !rawSlotEqual(current, valueSlot) {
			writeSlot(&table.store.entries[index].value, valueSlot)
			changed = true
		}
	case valueSlot.kind() != NilKind:
		keySlot := slotFromValue(stringValue(table.owner.strings.make(key)))
		structural, changed = table.store.set(keySlot, valueSlot, hash)
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
	hash uint64,
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
		hash = (*luaString)(normalized.ref).hash
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
	hash uint64,
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
	hash uint64,
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
	hash uint64,
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
	hash uint64,
	value slot,
) (structural, changed bool) {
	if arrayKey {
		return table.setInteger(index, value)
	}

	if value.kind() == NilKind {
		deleted := table.store.delete(key, hash)
		return deleted, deleted
	}
	inserted, changed := table.store.set(key, value, hash)
	return inserted, changed
}

func (table *Table) setInteger(index int, value slot) (structural, changed bool) {
	useArray := table.shouldUseArray(index, value.kind() != NilKind)
	if useArray {
		var (
			hash        uint64
			storedValue slot
			stored      bool
		)
		if table.store.integerKeys != 0 {
			number := float64(index)
			hash = hashNumber(number)
			storedValue, stored = table.store.get(
				slot{bits: math.Float64bits(number)},
				hash,
			)
		}
		if stored {
			key := slot{bits: math.Float64bits(float64(index))}
			table.store.delete(key, hash)
			if value.kind() == NilKind {
				return true, true
			}
			_, arrayChanged := table.setArray(index, value)
			return false, arrayChanged || !rawSlotEqual(storedValue, value)
		}
		return table.setArray(index, value)
	}

	number := float64(index)
	key := slot{bits: math.Float64bits(number)}
	hash := hashNumber(number)
	if value.kind() == NilKind {
		deleted := table.store.delete(key, hash)
		return deleted, deleted
	}
	return table.store.set(key, value, hash)
}

func (table *Table) shouldUseArray(index int, inserting bool) bool {
	if index <= 0 {
		return false
	}
	if index <= len(table.array) {
		return true
	}
	if !inserting {
		return false
	}
	if index == len(table.array)+1 {
		return true
	}
	if len(table.array) == 0 && index <= initialArrayCapacity {
		return true
	}
	gap := index - len(table.array)
	if gap > maxDenseArrayGap {
		return false
	}
	return index <= 2*(table.arrayUsed+1)
}

func (table *Table) setArray(index int, value slot) (structural, changed bool) {
	if index > len(table.array) {
		table.growArrayWith(index, value)
		table.arrayUsed++
		table.promoteArrayTail()
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
		table.promoteArrayTail()
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
		capacity := cap(table.array)
		if capacity == 0 {
			capacity = initialArrayCapacity
		}
		for capacity < length {
			if capacity < 32 {
				capacity *= 2
			} else {
				capacity += capacity / 2
			}
		}
		grown := make([]slot, length, capacity)
		copy(grown, table.array)
		table.array = grown
	}
	for index := oldLength; index < length; index++ {
		table.array[index] = nilSlot
	}
}

func (table *Table) promoteArrayTail() {
	if table.store.integerKeys == 0 {
		return
	}
	for {
		index := len(table.array) + 1
		key := slot{bits: math.Float64bits(float64(index))}
		hash := hashNumber(float64(index))
		value, found := table.store.get(key, hash)
		if !found {
			return
		}
		table.store.delete(key, hash)
		table.growArrayWith(index, value)
		table.arrayUsed++
	}
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

func (store *tableStore) init(hint int) {
	if hint <= 0 {
		return
	}
	capacity := minimumStoreCapacity
	for capacity*3/4 < hint {
		capacity *= 2
	}
	store.entries = make([]tableEntry, capacity)
}

func (store *tableStore) get(key slot, hash uint64) (slot, bool) {
	if len(store.entries) == 0 {
		return nilSlot, false
	}
	mask := len(store.entries) - 1
	for probe := 0; probe < len(store.entries); probe++ {
		index := (int(hash&uint64(mask)) + probe) & mask
		entry := &store.entries[index]
		switch {
		case entry.hash == entryHashEmpty:
			return nilSlot, false
		case entry.value.kind() == NilKind:
			continue
		default:
			if entry.hash == hash && rawSlotEqual(entry.key, key) {
				return entry.value, true
			}
		}
	}
	return nilSlot, false
}

func (store *tableStore) getString(text string, hash uint64) (slot, bool) {
	index, found := store.findString(text, hash)
	if !found {
		return nilSlot, false
	}
	return store.entries[index].value, true
}

func (store *tableStore) findString(text string, hash uint64) (int, bool) {
	if len(store.entries) == 0 {
		return 0, false
	}
	mask := len(store.entries) - 1
	for probe := 0; probe < len(store.entries); probe++ {
		index := (int(hash&uint64(mask)) + probe) & mask
		entry := &store.entries[index]
		switch {
		case entry.hash == entryHashEmpty:
			return 0, false
		case entry.value.kind() == NilKind:
			continue
		default:
			if entry.hash != hash || entry.key.kind() != StringKind {
				continue
			}
			if (*luaString)(entry.key.ref).text == text {
				return index, true
			}
		}
	}
	return 0, false
}

func (store *tableStore) set(key, value slot, hash uint64) (inserted, changed bool) {
	if len(store.entries) == 0 {
		store.rehash(minimumStoreCapacity)
	}
	index, found := store.find(key, hash)
	if found {
		if rawSlotEqual(store.entries[index].value, value) {
			return false, false
		}
		writeSlot(&store.entries[index].value, value)
		return false, true
	}
	if store.needsInsertRehash() {
		capacity := len(store.entries)
		if store.deleted <= store.count {
			capacity *= 2
		}
		store.rehash(capacity)
		index, _ = store.find(key, hash)
	}
	if store.entries[index].value.kind() == NilKind {
		store.deleted--
	}
	store.entries[index] = tableEntry{
		key:   key,
		value: value,
		hash:  hash,
	}
	store.count++
	if isPositiveIntegerKey(key) {
		store.integerKeys++
	}
	return true, true
}

func (store *tableStore) delete(key slot, hash uint64) bool {
	if len(store.entries) == 0 {
		return false
	}
	index, found := store.find(key, hash)
	if !found {
		return false
	}
	store.deleteAt(index)
	return true
}

func (store *tableStore) deleteAt(index int) {
	entry := &store.entries[index]
	if isPositiveIntegerKey(entry.key) {
		store.integerKeys--
	}
	writeSlot(&entry.value, nilSlot)
	store.count--
	store.deleted++
}

func (store *tableStore) find(key slot, hash uint64) (index int, found bool) {
	mask := len(store.entries) - 1
	firstDeleted := -1
	for probe := 0; probe < len(store.entries); probe++ {
		index = (int(hash&uint64(mask)) + probe) & mask
		entry := &store.entries[index]
		switch {
		case entry.hash == entryHashEmpty:
			if firstDeleted >= 0 {
				return firstDeleted, false
			}
			return index, false
		case entry.value.kind() == NilKind:
			if firstDeleted < 0 {
				firstDeleted = index
			}
		default:
			if entry.hash == hash && rawSlotEqual(entry.key, key) {
				return index, true
			}
		}
	}
	return firstDeleted, false
}

func (store *tableStore) findContinuation(key slot, hash uint64) (int, bool) {
	if len(store.entries) == 0 {
		return 0, false
	}
	mask := len(store.entries) - 1
	for probe := 0; probe < len(store.entries); probe++ {
		index := (int(hash&uint64(mask)) + probe) & mask
		entry := &store.entries[index]
		if entry.hash == entryHashEmpty {
			return 0, false
		}
		if entry.hash == hash && rawSlotEqual(entry.key, key) {
			return index, true
		}
	}
	return 0, false
}

func (store *tableStore) needsInsertRehash() bool {
	occupied := store.count + store.deleted + 1
	return occupied*4 > len(store.entries)*3 ||
		store.deleted > len(store.entries)/4 && store.deleted > store.count
}

func (store *tableStore) rehash(capacity int) {
	previous := store.entries
	store.entries = make([]tableEntry, capacity)
	store.count = 0
	store.deleted = 0
	store.integerKeys = 0
	for index := range previous {
		entry := previous[index]
		if entry.hash == entryHashEmpty || entry.value.kind() == NilKind {
			continue
		}
		target, _ := store.find(entry.key, entry.hash)
		store.entries[target] = entry
		store.count++
		if isPositiveIntegerKey(entry.key) {
			store.integerKeys++
		}
	}
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
	const largestExactFloatInteger = 1 << 53
	if limit > largestExactFloatInteger {
		limit = largestExactFloatInteger
	}
	if number < 1 || number > limit {
		return 0, false
	}
	index := int(number)
	return index, index > 0 && float64(index) == number
}

func hashTableKey(key slot) (uint64, error) {
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
		return (*luaString)(key.ref).hash, nil
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

func hashNumber(number float64) uint64 {
	if number == 0 {
		number = 0
	}
	return mixHash(math.Float64bits(number) ^ 0x9e3779b97f4a7c15)
}

func hashReference(value slot) uint64 {
	switch value.kind() {
	case BoolKind:
		if value.ref == trueMarkerPointer {
			return 0x6eed0e9da4d94a4f
		}
		return 0x8a5cd789635d2dff
	case StringKind:
		return (*luaString)(value.ref).hash
	default:
		return mixHash(uint64(uintptr(value.ref)))
	}
}

func mixHash(value uint64) uint64 {
	value ^= value >> 30
	value *= 0xbf58476d1ce4e5b9
	value ^= value >> 27
	value *= 0x94d049bb133111eb
	value ^= value >> 31
	if value < 2 {
		return value + 2
	}
	return value
}
