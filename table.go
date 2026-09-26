package lua

import (
	"math"
	"runtime"
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

// tableVector stores the same typed backing pointer as a Go slice while using
// 32-bit lengths permitted by Lua's table capacity bounds. values returns a
// fixed-capacity view so growth remains explicit through construction or
// withLength. Like a slice element pointer, an at result or values view must
// not survive replacement of the descriptor's backing storage.
type tableVector[T any] struct {
	data     *T
	length   uint32
	capacity uint32
}

func makeTableVector[T any](length, capacity int) tableVector[T] {
	if length < 0 ||
		capacity < length ||
		uint64(capacity) > uint64(^uint32(0)) {
		panic("lua: table capacity overflow")
	}
	if capacity == 0 {
		return tableVector[T]{}
	}
	values := make([]T, capacity)
	return tableVector[T]{
		data:     unsafe.SliceData(values),
		length:   uint32(length),
		capacity: uint32(capacity),
	}
}

func (vector tableVector[T]) len() int {
	return int(vector.length)
}

func (vector tableVector[T]) cap() int {
	return int(vector.capacity)
}

func (vector tableVector[T]) values() []T {
	return unsafe.Slice(vector.data, int(vector.length))
}

func (vector tableVector[T]) at(index int) *T {
	if uint(index) >= uint(vector.length) {
		panic("lua: table vector index out of range")
	}
	return (*T)(unsafe.Add(
		unsafe.Pointer(vector.data),
		uintptr(index)*unsafe.Sizeof(*vector.data),
	))
}

func (vector tableVector[T]) withLength(length int) tableVector[T] {
	if length < 0 || uint64(length) > uint64(vector.capacity) {
		panic("lua: table capacity overflow")
	}
	vector.length = uint32(length)
	return vector
}

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

// Table is an opaque owning handle for a Lua table.
//
// Table methods are raw: they never invoke Lua or consult metamethods.
// Metamethod-aware operations belong to State and Frame.
//
// Repeated publication of the same live Lua object returns the same handle
// pointer. Execution slots retain the compact table directly and do not pass
// through this handle.
//
// Table must not be copied after first use. Retain and pass its pointer.
type Table hostToken

type tableObject struct {
	objectHeader
	array             tableVector[slot]
	arrayUsed         int
	store             tableStore
	metatable         *tableObject
	absentMetamethods uint32
	// recordIntegerFloor is one plus the smallest power-of-two exponent
	// containing a positive integer record key. The value above every array
	// exponent means all known integer records exceed the array limit.
	// Deletion may leave a conservative lower value; zero means none.
	recordIntegerFloor uint8
	gcMark             objectMark
}

func newTable(state *State, arrayHint, recordHint int) *tableObject {
	if state == nil || state.runtime == nil {
		panic("lua: invalid table state")
	}
	table := &tableObject{}
	if arrayHint > 0 {
		table.array = makeTableVector[slot](0, arrayHint)
	}
	table.store.init(recordHint)
	state.registerTable(table)
	return table
}

func (table *tableObject) chargeStorageGrowth(previous uint64) {
	if table == nil || table.owner == nil {
		return
	}
	current := tableStorageRetainedBytes(table)
	if current > previous {
		table.owner.collection.charge(current - previous)
	}
}

func (table *tableObject) rehashStore(capacity int) {
	previousCapacity := table.store.entries.cap()
	table.store.rehash(capacity)
	if table.owner != nil {
		table.owner.collection.chargeCapacityGrowth(
			previousCapacity,
			table.store.entries.cap(),
			uint64(unsafe.Sizeof(tableEntry{})),
		)
	}
}

// Value returns the owning Lua value for table.
func (table *Table) Value() Value {
	token := table.token()
	if token == nil ||
		token.owner == nil ||
		token.object == nil ||
		token.kind != TableKind {
		return Value{}
	}
	value := Value{ref: unsafe.Pointer(token), bits: uint64(TableKind)}
	runtime.KeepAlive(table)
	return value
}

func (table *Table) token() *hostToken {
	return (*hostToken)(table)
}

func (table *Table) runtimeObject() *tableObject {
	token := table.token()
	if token == nil ||
		token.kind != TableKind ||
		token.object == nil {
		return nil
	}
	return (*tableObject)(token.object)
}

func (table *tableObject) owningHandle() *Table {
	if table == nil {
		return nil
	}
	token := table.objectHeader.owningToken(
		TableKind,
		unsafe.Pointer(table),
	)
	return (*Table)(token)
}

func (table *tableObject) owningValue() Value {
	handle := table.owningHandle()
	return handle.Value()
}

func slotFromTableObject(table *tableObject) slot {
	return objectSlot(TableKind, unsafe.Pointer(table))
}

func tableObjectFromSlot(value slot) *tableObject {
	if !value.isTable() {
		panic("lua: slot is not a table")
	}
	return (*tableObject)(value.ref)
}

func tableHandleFromSlot(value slot) *Table {
	return tableObjectFromSlot(value).owningHandle()
}

// RawGet returns the value associated with key without invoking metamethods.
// A missing key returns Nil.
func (table *Table) RawGet(key Value) (Value, error) {
	object := table.runtimeObject()
	result, err := object.rawGetValue(key)
	runtime.KeepAlive(table)
	return result, err
}

func (table *tableObject) rawGetValue(key Value) (Value, error) {
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
	object := table.runtimeObject()
	err := object.rawSetValue(key, value)
	runtime.KeepAlive(table)
	return err
}

func (table *tableObject) rawSetValue(key, value Value) error {
	if err := table.checkMutable(); err != nil {
		return err
	}
	var incoming slot
	if value.ref == numberMarkerPointer {
		incoming.bits = canonicalNumberBits(value.bits)
	} else {
		if err := table.owner.accept(value); err != nil {
			return err
		}
		incoming = slotFromValue(value)
	}
	if err := table.owner.accept(key); err != nil {
		return err
	}

	normalized, index, arrayKey, hash, status :=
		normalizeTableKey(slotFromValue(key))
	if status != tableKeyValid {
		return ErrInvalidKey
	}
	table.rawSetBoundaryNormalizedSlot(
		normalized,
		index,
		arrayKey,
		hash,
		incoming,
	)
	return nil
}

// RawGetInt returns the value associated with an integer key without invoking
// metamethods. A missing key returns Nil.
func (table *Table) RawGetInt(key int) Value {
	object := table.runtimeObject()
	result := object.rawGetIntValue(key)
	runtime.KeepAlive(table)
	return result
}

func (table *tableObject) rawGetIntValue(key int) Value {
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
	object := table.runtimeObject()
	err := object.rawSetIntValue(key, value)
	runtime.KeepAlive(table)
	return err
}

func (table *tableObject) rawSetIntValue(key int, value Value) error {
	if err := table.checkMutable(); err != nil {
		return err
	}
	var incoming slot
	if value.ref == numberMarkerPointer {
		incoming.bits = canonicalNumberBits(value.bits)
	} else {
		if err := table.owner.accept(value); err != nil {
			return err
		}
		incoming = slotFromValue(value)
	}
	if incoming.isString() {
		if current, found := table.rawIntSlot(key); found &&
			sameSlotRepresentation(current, incoming) {
			return nil
		}
		incoming = table.owner.importAcceptedSlot(incoming)
	}
	table.rawSetIntegerSlot(key, incoming)
	return nil
}

// RawGetString returns the value associated with a string key without
// constructing a temporary Value or invoking metamethods.
func (table *Table) RawGetString(key string) Value {
	object := table.runtimeObject()
	result := object.rawGetStringValue(key)
	runtime.KeepAlive(table)
	return result
}

func (table *tableObject) rawGetStringValue(key string) Value {
	if value, found := table.rawStringSlot(key); found {
		return value.owningValue()
	}
	return nilValue
}

func (table *tableObject) rawStringSlot(key string) (slot, bool) {
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
	object := table.runtimeObject()
	err := object.rawSetStringValue(key, value)
	runtime.KeepAlive(table)
	return err
}

func (table *tableObject) rawSetStringValue(key string, value Value) error {
	if err := table.checkMutable(); err != nil {
		return err
	}
	var incoming slot
	if value.ref == numberMarkerPointer {
		incoming.bits = canonicalNumberBits(value.bits)
	} else {
		if err := table.owner.accept(value); err != nil {
			return err
		}
		incoming = slotFromValue(value)
	}
	if incoming.isString() {
		if current, found := table.rawStringSlot(key); found &&
			sameSlotRepresentation(current, incoming) {
			return nil
		}
		incoming = table.owner.importAcceptedSlot(incoming)
	}
	return table.setStringSlot(key, incoming)
}

func (table *tableObject) rawSetStringSlot(key string, value slot) error {
	if err := table.checkMutable(); err != nil {
		return err
	}
	if err := table.owner.acceptSlot(value); err != nil {
		return err
	}
	return table.setStringSlot(key, value)
}

func (table *tableObject) setStringSlot(key string, value slot) error {
	hash := uint32(table.owner.strings.hash(key))
	index, stored := table.store.findStoredString(key, hash)
	var entry *tableEntry
	if stored {
		entry = table.store.entries.at(index)
	}
	changed := false
	switch {
	case stored &&
		!entry.value.isNil() &&
		value.isNil():
		table.store.deleteAt(index)
		changed = true
	case stored &&
		!entry.value.isNil():
		if replaceTableValue(&entry.value, value) {
			changed = true
		}
	case stored && !value.isNil():
		if table.store.shouldCompact() {
			storedKey := entry.key
			table.rehashStore(table.store.entries.len())
			table.insertNewField(storedKey, value, hash, 0)
		} else {
			table.store.reviveAt(index, value)
		}
		changed = true
	case !value.isNil():
		keySlot := stringSlot(
			table.owner.strings.makeKnownHash(
				key,
				stringHash(hash),
			),
		)
		table.insertNewField(keySlot, value, hash, 0)
		changed = true
	}
	if changed {
		table.absentMetamethods = 0
	}
	return nil
}

// Next returns the table field after after in Lua's raw traversal order.
//
// Pass Nil() to begin a traversal, then pass each returned key to the next
// call. If no field remains, ok is false and key and value are Nil. Next does
// not invoke metamethods.
//
// Deleting the current field or changing an existing field's value is
// permitted between calls. Adding a new field during traversal makes the
// traversal order and visited set undefined. A continuation key that Lua
// cannot locate returns ErrInvalidNextKey.
//
// Like the other raw readers, Next observes a closed State's tables as the
// frozen snapshot State.Close leaves behind.
func (table *Table) Next(
	after Value,
) (key, value Value, ok bool, err error) {
	object := table.runtimeObject()
	if object == nil || object.owner == nil {
		runtime.KeepAlive(table)
		return Value{}, Value{}, false, ErrClosed
	}
	if err := object.owner.accept(after); err != nil {
		runtime.KeepAlive(table)
		return Value{}, Value{}, false, err
	}
	nextKey, nextValue, found, err := object.next(slotFromValue(after))
	runtime.KeepAlive(table)
	if err != nil {
		return Value{}, Value{}, false, err
	}
	if !found {
		return Nil(), Nil(), false, nil
	}
	return nextKey.owningValue(), nextValue.owningValue(), true, nil
}

// RawLen returns a valid Lua border for table without invoking __len.
//
// As in Lua 5.1, the result is undefined when a table has more than one
// border.
func (table *Table) RawLen() int {
	object := table.runtimeObject()
	result := object.rawLen()
	runtime.KeepAlive(table)
	return result
}

func (table *tableObject) rawLen() int {
	if table == nil {
		return 0
	}

	arrayLength := table.array.len()
	if arrayLength > 0 &&
		table.array.at(arrayLength-1).isNil() {
		array := table.array.values()
		low, high := 0, arrayLength
		for high-low > 1 {
			middle := low + (high-low)/2
			if array[middle-1].isNil() {
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

func (table *tableObject) next(previous slot) (key, value slot, found bool, err error) {
	if table == nil || table.owner == nil {
		return nilSlot, nilSlot, false, ErrClosed
	}

	arrayLength := table.array.len()
	storeLength := table.store.entries.len()
	arrayStart := 0
	storeStart := 0
	if !previous.isNil() {
		// PUC Lua 5.1 treats an exact positive integer within the allocated
		// array part as a traversal position even when its slot is nil. This
		// permits next to continue after deletion of the current array field.
		if index, ok := arrayIndex(previous); ok && index <= arrayLength {
			arrayStart = index
		} else {
			hash, hashErr := hashTableKey(previous)
			if hashErr != nil {
				return nilSlot, nilSlot, false, ErrInvalidNextKey
			}
			index, exists := table.store.findStored(previous, hash)
			if !exists {
				return nilSlot, nilSlot, false, ErrInvalidNextKey
			}
			arrayStart = arrayLength
			storeStart = index + 1
		}
	}

	for index := arrayStart; index < arrayLength; index++ {
		candidate := *table.array.at(index)
		if candidate.isNil() {
			continue
		}
		return slot{bits: math.Float64bits(float64(index + 1))},
			candidate,
			true,
			nil
	}
	for index := storeStart; index < storeLength; index++ {
		entry := table.store.entries.at(index)
		if entry.hash == entryHashEmpty || entry.value.isNil() {
			continue
		}
		return entry.key, entry.value, true, nil
	}
	return nilSlot, nilSlot, false, nil
}

func (table *tableObject) checkMutable() error {
	if table == nil || table.owner == nil || table.owner.closed.Load() {
		return ErrClosed
	}
	return nil
}
