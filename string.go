package lua

import (
	"runtime"
	"strings"
	"unsafe"
)

const (
	// maximumConstructedStringBytes rejects library operations that would ask
	// the host allocator for an impractically large contiguous result.
	maximumConstructedStringBytes = 1 << 30
	shortStringLimit              = 64
	stringCacheWays               = 4
	stringSetsPerShard            = 8
	stringProbationShardCount     = 8
	stringProtectedShardCount     = 16

	stringLengthShift    = 8
	stringLengthBits     = 24
	stringLengthSentinel = 1<<stringLengthBits - 1
	stringHashShift      = 32
)

// stringRef is the representation shared by runtime values and executable
// prototype constants. Ordinary strings point directly at immutable bytes.
// The bits word carries the kind, length, and hash, so they need no per-string
// header.
//
// The zero value means absent metadata; it is never a Lua empty string.
type stringRef struct {
	ref  unsafe.Pointer
	bits uint64
}

type stringHash uint32

// internedText is compiler-only identity for source, local, and upvalue names.
// Executable constants cross into the flat stringRef representation.
type internedText struct {
	text string
	hash stringHash
}

// longString is the uncommon fallback for strings whose length cannot fit in
// stringRef's 24-bit length field.
type longString struct {
	text string
}

var (
	emptyStringMarker     byte
	emptyStringRef        = newUncachedHashedStringRef("", hashString(""))
	singleByteStringBytes [256]byte
	singleByteStringRefs  = makeSingleByteStringRefs()
)

func makeSingleByteStringRefs() [256]stringRef {
	var refs [256]stringRef
	for index := range refs {
		singleByteStringBytes[index] = byte(index)
		hash := hashBytes(singleByteStringBytes[index : index+1])
		refs[index] = stringRef{
			ref: unsafe.Pointer(&singleByteStringBytes[index]),
			bits: uint64(StringKind) |
				uint64(1)<<stringLengthShift |
				uint64(hash)<<stringHashShift,
		}
	}
	return refs
}

func singleByteStringRef(value byte) stringRef {
	return singleByteStringRefs[value]
}

type stringSet struct {
	entries [stringCacheWays]stringRef
	next    uint8
}

type stringSetShard struct {
	sets [stringSetsPerShard]stringSet
}

// stringPool gives recurring short strings canonical backing storage without
// rooting every transient string for the lifetime of the State. It is
// runtime-local; State's single-executor contract removes synchronization
// from this hot path.
type stringPool struct {
	closed    bool
	probation [stringProbationShardCount]*stringSetShard
	protected [stringProtectedShardCount]*stringSetShard
	owner     *runtimeState
}

func (pool *stringPool) make(text string) stringRef {
	return pool.makeHashed(text, hashString(text), false)
}

// makeBorrowed publishes text whose backing storage belongs to a larger
// value. Cache hits remain allocation-free; a miss clones before retaining the
// string so a small Lua value cannot pin an unrelated backing buffer.
func (pool *stringPool) makeBorrowed(text string) stringRef {
	return pool.makeHashed(text, hashString(text), true)
}

// makeBytes looks up caller-owned bytes without first allocating a Go string.
// A miss copies the bytes before the resulting Lua string can outlive the
// call; a hit retains no view of the caller's storage.
func (pool *stringPool) makeBytes(bytes []byte) stringRef {
	if len(bytes) == 0 {
		return emptyStringRef
	}
	if len(bytes) == 1 {
		return singleByteStringRef(bytes[0])
	}
	hash := hashBytes(bytes)
	if pool.closed || len(bytes) > shortStringLimit {
		return pool.makeUncachedBytes(bytes, hash)
	}

	if found := pool.lookupProtectedBytes(bytes, hash); found.valid() {
		return found
	}

	if found, set, way := pool.lookupProbationBytes(bytes, hash); found.valid() {
		set.entries[way] = stringRef{}
		pool.storeProtected(found)
		return found
	}

	return pool.makeBytesMiss(bytes, hash)
}

// makeKnownHash retains text using a hash already computed at a trusted
// runtime seam. It keeps table string insertion from hashing the same Go
// string once for lookup and again for storage.
func (pool *stringPool) makeKnownHash(
	text string,
	hash stringHash,
) stringRef {
	return pool.makeHashed(text, hash, false)
}

func (pool *stringPool) makeHashed(
	text string,
	hash stringHash,
	borrowed bool,
) stringRef {
	if text == "" {
		return emptyStringRef
	}
	if len(text) == 1 {
		return singleByteStringRef(text[0])
	}
	if pool.closed || len(text) > shortStringLimit {
		return pool.makeUncachedText(text, hash, borrowed)
	}

	if found := pool.lookupProtected(text, hash); found.valid() {
		return found
	}

	if found, set, way := pool.lookupProbation(text, hash); found.valid() {
		set.entries[way] = stringRef{}
		pool.storeProtected(found)
		return found
	}

	return pool.makeTextMiss(text, hash, borrowed)
}

// makeUncachedBytes is the cold path for a closed pool or a long byte string.
//
//go:noinline
func (pool *stringPool) makeUncachedBytes(
	bytes []byte,
	hash stringHash,
) stringRef {
	created := newUncachedHashedStringRef(string(bytes), hash)
	if !pool.closed {
		pool.chargeCreatedString(created)
	}
	return created
}

// makeBytesMiss copies, charges, and admits one cacheable byte string after
// both cache tiers miss.
//
//go:noinline
func (pool *stringPool) makeBytesMiss(
	bytes []byte,
	hash stringHash,
) stringRef {
	created := newUncachedHashedStringRef(string(bytes), hash)
	pool.chargeCreatedString(created)
	pool.storeProbation(created)
	return created
}

// makeUncachedText is the cold path for a closed pool or a long string.
//
//go:noinline
func (pool *stringPool) makeUncachedText(
	text string,
	hash stringHash,
	borrowed bool,
) stringRef {
	if borrowed {
		text = strings.Clone(text)
	}
	created := newUncachedHashedStringRef(text, hash)
	if !pool.closed {
		pool.chargeCreatedString(created)
	}
	return created
}

// makeTextMiss copies borrowed input, charges, and admits one cacheable string
// after both cache tiers miss.
//
//go:noinline
func (pool *stringPool) makeTextMiss(
	text string,
	hash stringHash,
	borrowed bool,
) stringRef {
	if borrowed {
		text = strings.Clone(text)
	}
	created := newUncachedHashedStringRef(text, hash)
	pool.chargeCreatedString(created)
	pool.storeProbation(created)
	return created
}

func (pool *stringPool) chargeCreatedString(value stringRef) {
	if pool == nil ||
		pool.owner == nil ||
		!value.valid() {
		return
	}
	pool.owner.collection.charge(stringRefRetainedBytes(value))
}

func (pool *stringPool) hash(text string) stringHash {
	return hashString(text)
}

func newStringRef(text string) stringRef {
	return newHashedStringRef(text, hashString(text))
}

// newStateNeutralLongString keeps hashing and uncached construction out of
// State.String's recurring short-string path.
//
//go:noinline
func newStateNeutralLongString(text string) stringRef {
	return newUncachedHashedStringRef(text, hashString(text))
}

func newHashedStringRef(text string, hash stringHash) stringRef {
	length := len(text)
	if length == 1 {
		return singleByteStringRef(text[0])
	}
	return newUncachedHashedStringRef(text, hash)
}

// newUncachedHashedStringRef constructs backing storage after the caller has
// handled the canonical single-byte case. The empty-string initializer is its
// one zero-length caller.
func newUncachedHashedStringRef(
	text string,
	hash stringHash,
) stringRef {
	length := len(text)
	bits := uint64(StringKind) |
		uint64(hash)<<stringHashShift
	if length >= stringLengthSentinel {
		value := &longString{text: text}
		return stringRef{
			ref:  unsafe.Pointer(value),
			bits: bits | uint64(stringLengthSentinel)<<stringLengthShift,
		}
	}
	if length == 0 {
		return stringRef{
			ref:  unsafe.Pointer(&emptyStringMarker),
			bits: bits,
		}
	}
	reference := unsafe.Pointer(unsafe.StringData(text))
	runtime.KeepAlive(text)
	return stringRef{
		ref:  reference,
		bits: bits | uint64(length)<<stringLengthShift,
	}
}

func normalizeStringHash(hash uint64) stringHash {
	normalized := stringHash(hash)
	if normalized == 0 {
		return 1
	}
	return normalized
}

// finalizeStringHash distributes the sampled hash's high bits before
// power-of-two table and cache indexing. The multiplication is bijective over
// 32-bit values; equality remains the authority for collisions.
func finalizeStringHash(hash stringHash) stringHash {
	hash *= 0x9e3779b1
	hash ^= hash >> 16
	return normalizeStringHash(uint64(hash))
}

func newHashedInternedText(
	text string,
	hash stringHash,
) *internedText {
	return &internedText{text: text, hash: hash}
}

func (value stringRef) valid() bool {
	return value.ref != nil
}

func stringText(reference unsafe.Pointer, bits uint64) string {
	length := int(bits >> stringLengthShift & stringLengthSentinel)
	if length == stringLengthSentinel {
		return (*longString)(reference).text
	}
	if length == 0 {
		return ""
	}
	return unsafe.String((*byte)(reference), length)
}

func stringLength(reference unsafe.Pointer, bits uint64) int {
	length := int(bits >> stringLengthShift & stringLengthSentinel)
	if length == stringLengthSentinel {
		return len((*longString)(reference).text)
	}
	return length
}

func (value stringRef) hash() stringHash {
	if !value.valid() || Kind(value.bits&0xff) != StringKind {
		panic("lua: invalid string reference")
	}
	return value.hashUnchecked()
}

func (value stringRef) hashUnchecked() stringHash {
	return stringHash(value.bits >> stringHashShift)
}

// The slot accessors are internal trusted seams. Their callers have already
// established StringKind; keeping validation out of these tiny operations
// lets them inline into table lookup, concatenation, and the executor.
func stringSlotText(value slot) string {
	return stringText(value.ref, value.bits)
}

func stringSlotHash(value slot) stringHash {
	return stringHash(value.bits >> stringHashShift)
}

func stringSlotLen(value slot) int {
	return stringLength(value.ref, value.bits)
}

func stringSlotsEqual(left, right slot) bool {
	if left.ref == right.ref && left.bits == right.bits {
		return true
	}
	if stringHash(left.bits>>stringHashShift) !=
		stringHash(right.bits>>stringHashShift) {
		return false
	}
	return stringText(left.ref, left.bits) ==
		stringText(right.ref, right.bits)
}

// stringFromOwnedBytes transfers an exact byte buffer into immutable string
// storage without copying it. The caller must not retain or mutate bytes after
// this call.
func stringFromOwnedBytes(bytes []byte) string {
	if len(bytes) == 0 {
		return ""
	}
	return unsafe.String(unsafe.SliceData(bytes), len(bytes))
}

func (pool *stringPool) lookupProtected(text string, hash stringHash) stringRef {
	shardIndex, setIndex := stringSetLocation(
		hash,
		stringProtectedShardCount,
	)
	shard := pool.protected[shardIndex]
	if shard == nil {
		return stringRef{}
	}
	set := &shard.sets[setIndex]
	for _, candidate := range set.entries {
		if stringRefMatchesText(candidate, text, hash) {
			return candidate
		}
	}
	return stringRef{}
}

func (pool *stringPool) lookupProtectedBytes(
	bytes []byte,
	hash stringHash,
) stringRef {
	shardIndex, setIndex := stringSetLocation(
		hash,
		stringProtectedShardCount,
	)
	shard := pool.protected[shardIndex]
	if shard == nil {
		return stringRef{}
	}
	set := &shard.sets[setIndex]
	for _, candidate := range set.entries {
		if stringMatchesBytes(candidate, bytes, hash) {
			return candidate
		}
	}
	return stringRef{}
}

func (pool *stringPool) lookupProbation(
	text string,
	hash stringHash,
) (stringRef, *stringSet, int) {
	shardIndex, setIndex := stringSetLocation(
		hash,
		stringProbationShardCount,
	)
	shard := pool.probation[shardIndex]
	if shard == nil {
		return stringRef{}, nil, -1
	}
	set := &shard.sets[setIndex]
	for index, candidate := range set.entries {
		if stringRefMatchesText(candidate, text, hash) {
			return candidate, set, index
		}
	}
	return stringRef{}, nil, -1
}

func (pool *stringPool) lookupProbationBytes(
	bytes []byte,
	hash stringHash,
) (stringRef, *stringSet, int) {
	shardIndex, setIndex := stringSetLocation(
		hash,
		stringProbationShardCount,
	)
	shard := pool.probation[shardIndex]
	if shard == nil {
		return stringRef{}, nil, -1
	}
	set := &shard.sets[setIndex]
	for index, candidate := range set.entries {
		if stringMatchesBytes(candidate, bytes, hash) {
			return candidate, set, index
		}
	}
	return stringRef{}, nil, -1
}

func stringMatchesBytes(
	candidate stringRef,
	bytes []byte,
	hash stringHash,
) bool {
	if !candidate.valid() ||
		candidate.hashUnchecked() != hash ||
		stringLength(candidate.ref, candidate.bits) != len(bytes) {
		return false
	}
	text := stringText(candidate.ref, candidate.bits)
	for index, current := range bytes {
		if text[index] != current {
			return false
		}
	}
	return true
}

func stringRefMatchesText(
	candidate stringRef,
	text string,
	hash stringHash,
) bool {
	return candidate.ref != nil &&
		stringHash(candidate.bits>>stringHashShift) == hash &&
		stringText(candidate.ref, candidate.bits) == text
}

func (pool *stringPool) storeProbation(value stringRef) {
	shardIndex, setIndex := stringSetLocation(
		value.hashUnchecked(),
		stringProbationShardCount,
	)
	shard := pool.probation[shardIndex]
	if shard == nil {
		shard = pool.newCacheShard()
		pool.probation[shardIndex] = shard
	}
	set := &shard.sets[setIndex]
	index := int(set.next % stringCacheWays)
	set.entries[index] = value
	set.next++
}

func (pool *stringPool) storeProtected(value stringRef) {
	shardIndex, setIndex := stringSetLocation(
		value.hashUnchecked(),
		stringProtectedShardCount,
	)
	shard := pool.protected[shardIndex]
	if shard == nil {
		shard = pool.newCacheShard()
		pool.protected[shardIndex] = shard
	}
	set := &shard.sets[setIndex]
	for _, candidate := range set.entries {
		if candidate == value {
			return
		}
	}
	index := int(set.next % stringCacheWays)
	set.entries[index] = value
	set.next++
}

//go:noinline
func (pool *stringPool) newCacheShard() *stringSetShard {
	shard := new(stringSetShard)
	if pool.owner != nil {
		pool.owner.collection.charge(uint64(unsafe.Sizeof(*shard)))
	}
	return shard
}

func (pool *stringPool) close() {
	pool.closed = true
	pool.probation = [stringProbationShardCount]*stringSetShard{}
	pool.protected = [stringProtectedShardCount]*stringSetShard{}
}

func stringSetLocation(hash stringHash, shardCount int) (shard, set int) {
	setCount := shardCount * stringSetsPerShard
	location := int(hash % stringHash(setCount))
	return location / stringSetsPerShard, location % stringSetsPerShard
}

// hashString uses Lua 5.1's bounded sampling policy: short strings are hashed
// completely, while long strings sample a fixed number of bytes. A small
// finalizer supplies the low-bit distribution required by Lunar's
// power-of-two table and cache indexing. Equality always checks contents.
func hashString(text string) stringHash {
	hash := stringHash(len(text))
	step := len(text)>>5 + 1
	for index := len(text); index >= step; index -= step {
		hash ^= hash<<5 + hash>>2 + stringHash(text[index-1])
	}
	return finalizeStringHash(hash)
}

func hashBytes(bytes []byte) stringHash {
	hash := stringHash(len(bytes))
	step := len(bytes)>>5 + 1
	for index := len(bytes); index >= step; index -= step {
		hash ^= hash<<5 + hash>>2 + stringHash(bytes[index-1])
	}
	return finalizeStringHash(hash)
}
