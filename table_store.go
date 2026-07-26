package lua

const (
	entryHashEmpty            uint32 = 0
	maximumTableStoreCapacity uint64 = 1 << 31
)

// The record store is one indexed chained-scatter array. A zero hash marks an
// unused node; next stores a successor index plus one, leaving zero as the
// chain terminator. lastFree scans downward for empty or reclaimable dead
// nodes; deletion retargets it to the newly dead node, and a partial scan
// wraps once before declaring the store full. A node displaced from its main
// position is always linked from that main chain. Deletion clears only the
// value, retaining the key and links so next can legally continue until a
// later insertion is allowed to reclaim, compact, or reorder it.
type tableEntry struct {
	key   slot
	value slot
	hash  uint32
	next  uint32
}

type tableStore struct {
	entries     tableVector[tableEntry]
	live        uint32
	dead        uint32
	integerKeys uint32
	lastFree    uint32
}

func (store *tableStore) init(hint int) {
	if hint <= 0 {
		return
	}
	capacity := nextPowerOfTwo(hint)
	store.entries = makeTableVector[tableEntry](capacity, capacity)
	store.lastFree = uint32(capacity)
}

func nextPowerOfTwo(value int) int {
	if value <= 0 {
		panic("lua: table capacity overflow")
	}
	capacity := 1
	for capacity < value {
		capacity = growTableStoreCapacity(capacity)
	}
	return capacity
}

// Reads keep their live-only chain walks local. The mutation path uses
// findStored because it must also recognize tombstones for reinsertion and
// next continuation; putting that distinction behind another result layer
// makes every ordinary table access pay for mutation-only state.
func (store *tableStore) get(key slot, hash uint32) (slot, bool) {
	if store.entries.len() == 0 {
		return nilSlot, false
	}
	index := store.mainIndex(hash)
	for {
		entry := store.entries.at(index)
		if entry.hash == entryHashEmpty {
			return nilSlot, false
		}
		if entry.hash == hash && entry.value.kind() != NilKind {
			if entry.key.ref == nil && key.ref == nil {
				if tableNumberBitsEqual(entry.key.bits, key.bits) {
					return entry.value, true
				}
			} else if rawSlotEqual(entry.key, key) {
				return entry.value, true
			}
		}
		if entry.next == 0 {
			return nilSlot, false
		}
		index = int(entry.next - 1)
	}
}

func (store *tableStore) getString(text string, hash uint32) (slot, bool) {
	if store.entries.len() == 0 {
		return nilSlot, false
	}
	index := store.mainIndex(hash)
	for {
		entry := store.entries.at(index)
		if entry.hash == entryHashEmpty {
			return nilSlot, false
		}
		if entry.hash == hash &&
			entry.key.kind() == StringKind &&
			stringSlotText(entry.key) == text {
			value := entry.value
			return value, value.kind() != NilKind
		}
		if entry.next == 0 {
			return nilSlot, false
		}
		index = int(entry.next - 1)
	}
}

func (store *tableStore) findStoredString(
	text string,
	hash uint32,
) (int, bool) {
	if store.entries.len() == 0 {
		return 0, false
	}
	index := store.mainIndex(hash)
	for {
		entry := store.entries.at(index)
		if entry.hash == entryHashEmpty {
			return 0, false
		}
		if entry.hash == hash &&
			entry.key.kind() == StringKind &&
			stringSlotText(entry.key) == text {
			return index, true
		}
		if entry.next == 0 {
			return 0, false
		}
		index = int(entry.next - 1)
	}
}

func (store *tableStore) reviveAt(index int, value slot) {
	entry := store.entries.at(index)
	if entry.value.kind() != NilKind || value.kind() == NilKind {
		panic("lua: invalid table tombstone revival")
	}
	writeSlot(&entry.value, value)
	store.dead--
	store.recordInsert(entry.key)
	store.consumeDeadCandidate(index)
}

func growTableStoreCapacity(capacity int) int {
	maxInt := int(^uint(0) >> 1)
	if capacity <= 0 ||
		uint64(capacity) >= maximumTableStoreCapacity ||
		capacity > maxInt/2 {
		panic("lua: table capacity overflow")
	}
	return capacity * 2
}

func (store *tableStore) deleteAt(index int) {
	entry := store.entries.at(index)
	if entry.value.kind() == NilKind {
		panic("lua: deleting an absent table entry")
	}
	if isPositiveIntegerKey(entry.key) {
		store.integerKeys--
	}
	writeSlot(&entry.value, nilSlot)
	store.live--
	store.dead++
	// The key remains linked for next until an insertion makes traversal
	// order undefined. Prefer this known tombstone over scanning unrelated
	// empty positions during a delete-A, insert-B replacement.
	store.lastFree = uint32(index + 1)
}

func (store *tableStore) find(key slot, hash uint32) (index int, found bool) {
	if store.entries.len() == 0 {
		return 0, false
	}
	index = store.mainIndex(hash)
	for {
		entry := store.entries.at(index)
		if entry.hash == entryHashEmpty {
			return 0, false
		}
		if entry.hash == hash && entry.value.kind() != NilKind {
			if entry.key.ref == nil && key.ref == nil {
				if tableNumberBitsEqual(entry.key.bits, key.bits) {
					return index, true
				}
			} else if rawSlotEqual(entry.key, key) {
				return index, true
			}
		}
		if entry.next == 0 {
			return 0, false
		}
		index = int(entry.next - 1)
	}
}

func (store *tableStore) findStored(
	key slot,
	hash uint32,
) (index int, stored bool) {
	if store.entries.len() == 0 {
		return 0, false
	}
	index = store.mainIndex(hash)
	for {
		entry := store.entries.at(index)
		if entry.hash == entryHashEmpty {
			return 0, false
		}
		if entry.hash == hash {
			if entry.key.ref == nil && key.ref == nil {
				if tableNumberBitsEqual(entry.key.bits, key.bits) {
					return index, true
				}
			} else if rawSlotEqual(entry.key, key) {
				return index, true
			}
		}
		if entry.next == 0 {
			return 0, false
		}
		index = int(entry.next - 1)
	}
}

func (store *tableStore) findContinuation(key slot, hash uint32) (int, bool) {
	return store.findStored(key, hash)
}

func tableNumberBitsEqual(left, right uint64) bool {
	return left == right || left<<1 == 0 && right<<1 == 0
}

func (store *tableStore) shouldCompact() bool {
	// A lone dead node can be recycled directly. Compact only a larger dead
	// majority so insertion also releases the other retained continuation
	// keys instead of carrying them indefinitely.
	return store.dead > 1 &&
		store.dead > uint32(store.entries.len()/4) &&
		store.dead > store.live
}

func (store *tableStore) rehash(capacity int) {
	// Every caller supplies a power of two: mainIndex uses masking rather
	// than division, and growth preserves that size class.
	if capacity <= 0 ||
		uint64(capacity) > maximumTableStoreCapacity {
		panic("lua: table capacity overflow")
	}
	previous := store.entries.values()
	live := int(store.live)
	if capacity < live {
		capacity = nextPowerOfTwo(live)
	}
	*store = tableStore{
		entries:  makeTableVector[tableEntry](capacity, capacity),
		lastFree: uint32(capacity),
	}
	for index := range previous {
		entry := previous[index]
		if entry.hash == entryHashEmpty || entry.value.kind() == NilKind {
			continue
		}
		if !store.insertAbsent(entry.key, entry.value, entry.hash) {
			panic("lua: table rehash exhausted its target store")
		}
	}
}

func (store *tableStore) insertAbsent(
	key slot,
	value slot,
	hash uint32,
) bool {
	if hash == entryHashEmpty {
		panic("lua: zero table hash")
	}
	main := store.mainIndex(hash)
	mainEntry := store.entries.at(main)
	if mainEntry.hash == entryHashEmpty {
		*mainEntry = tableEntry{
			key:   key,
			value: value,
			hash:  hash,
		}
		store.recordInsert(key)
		return true
	}
	if mainEntry.value.kind() == NilKind {
		// Insertion invalidates next order, so the dead node can be removed
		// from its old chain. A native chain head is overwritten in place to
		// retain its successors; a displaced node is first unlinked.
		var next uint32
		if store.mainIndex(mainEntry.hash) == main {
			next = mainEntry.next
			store.dead--
		} else if free := store.reclaimDeadAt(main); free != main {
			panic("lua: displaced table node reclaimed another position")
		}
		*mainEntry = tableEntry{
			key:   key,
			value: value,
			hash:  hash,
			next:  next,
		}
		store.recordInsert(key)
		store.consumeDeadCandidate(main)
		return true
	}

	free, found := store.takeFree()
	if !found {
		return false
	}
	entries := store.entries.values()
	if entries[main].hash == entryHashEmpty {
		entries[main] = tableEntry{
			key:   key,
			value: value,
			hash:  hash,
		}
		store.recordInsert(key)
		return true
	}
	occupant := entries[main]
	occupantMain := store.mainIndex(occupant.hash)
	if occupantMain != main {
		predecessor := occupantMain
		for entries[predecessor].next != uint32(main+1) {
			link := entries[predecessor].next
			if link == 0 {
				panic("lua: broken table collision chain")
			}
			predecessor = int(link - 1)
		}
		entries[free] = occupant
		entries[predecessor].next = uint32(free + 1)
		entries[main] = tableEntry{
			key:   key,
			value: value,
			hash:  hash,
		}
	} else {
		next := entries[main].next
		entries[main].next = uint32(free + 1)
		entries[free] = tableEntry{
			key:   key,
			value: value,
			hash:  hash,
			next:  next,
		}
	}
	store.recordInsert(key)
	return true
}

func (store *tableStore) recordInsert(key slot) {
	store.live++
	if isPositiveIntegerKey(key) {
		store.integerKeys++
	}
	if store.dead == 0 && store.live == uint32(store.entries.len()) {
		store.lastFree = 0
	}
}

func (store *tableStore) takeFree() (int, bool) {
	start := store.lastFree
	if free, found := store.takeFreeDownTo(0); found {
		return free, true
	}
	if store.dead == 0 && store.live == uint32(store.entries.len()) {
		return 0, false
	}

	// Deletion may retarget the cursor below empty nodes that an earlier
	// downward scan had not reached. Wrap once through that skipped suffix.
	store.lastFree = uint32(store.entries.len())
	if free, found := store.takeFreeDownTo(start); found {
		return free, true
	}
	panic("lua: table insertion cursor lost a free node")
}

func (store *tableStore) takeFreeDownTo(limit uint32) (int, bool) {
	entries := store.entries.values()
	for store.lastFree > limit {
		store.lastFree--
		index := int(store.lastFree)
		if entries[index].hash == entryHashEmpty {
			return index, true
		}
		if entries[index].value.kind() == NilKind {
			free := store.reclaimDeadAt(index)
			remaining := &entries[index]
			if remaining.hash != entryHashEmpty &&
				remaining.value.kind() == NilKind {
				// Reclaiming a dead chain head may promote another dead
				// successor. Keep that position eligible for the next
				// insertion instead of skipping it below the cursor.
				store.lastFree = uint32(index + 1)
			}
			return free, true
		}
	}
	return 0, false
}

func (store *tableStore) reclaimDeadAt(index int) int {
	entries := store.entries.values()
	entry := entries[index]
	if entry.hash == entryHashEmpty || entry.value.kind() != NilKind {
		panic("lua: reclaiming a live table entry")
	}
	main := store.mainIndex(entry.hash)
	if main != index {
		predecessor := main
		for entries[predecessor].next != uint32(index+1) {
			link := entries[predecessor].next
			if link == 0 {
				panic("lua: broken table collision chain")
			}
			predecessor = int(link - 1)
		}
		entries[predecessor].next = entry.next
		entries[index] = tableEntry{}
		store.dead--
		return index
	}
	if entry.next == 0 {
		entries[index] = tableEntry{}
		store.dead--
		return index
	}

	successor := int(entry.next - 1)
	entries[index] = entries[successor]
	entries[successor] = tableEntry{}
	store.dead--
	return successor
}

func (store *tableStore) consumeDeadCandidate(index int) {
	if store.lastFree == uint32(index+1) {
		store.lastFree--
	}
}

func (store *tableStore) mainIndex(hash uint32) int {
	return int(hash & uint32(store.entries.len()-1))
}
