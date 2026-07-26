package lua

import (
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
)

type luaString struct {
	text string
	hash uint64
}

type stringSet struct {
	entries [stringCacheWays]*luaString
	next    uint8
}

type stringSetShard struct {
	sets [stringSetsPerShard]stringSet
}

// stringPool gives recurring short strings canonical identity without rooting
// every transient string for the lifetime of the State. It is runtime-local;
// State's single-executor contract removes synchronization from this hot path.
type stringPool struct {
	closed    bool
	empty     *luaString
	probation [stringProbationShardCount]*stringSetShard
	protected [stringProtectedShardCount]*stringSetShard
}

func (pool *stringPool) make(text string) *luaString {
	return pool.makeText(text, false)
}

// makeBorrowed publishes text whose backing storage belongs to a larger
// value. Cache hits remain allocation-free; a miss clones before retaining the
// string so a small Lua value cannot pin an unrelated backing buffer.
func (pool *stringPool) makeBorrowed(text string) *luaString {
	return pool.makeText(text, true)
}

// makeBytes looks up caller-owned bytes without first allocating a Go string.
// A miss copies the bytes before the resulting Lua string can outlive the
// call; a hit retains no view of the caller's storage.
func (pool *stringPool) makeBytes(bytes []byte) *luaString {
	hash := hashBytes(bytes)
	if len(bytes) == 0 && !pool.closed {
		if pool.empty == nil {
			pool.empty = pool.newString("", hash)
		}
		return pool.empty
	}
	if pool.closed || len(bytes) > shortStringLimit {
		return pool.newString(string(bytes), hash)
	}

	if found := pool.lookupProtectedBytes(bytes, hash); found != nil {
		return found
	}

	if found, set, way := pool.lookupProbationBytes(bytes, hash); found != nil {
		set.entries[way] = nil
		pool.storeProtected(found)
		return found
	}

	created := pool.newString(string(bytes), hash)
	pool.storeProbation(created)
	return created
}

func (pool *stringPool) makeText(text string, borrowed bool) *luaString {
	hash := pool.hash(text)
	if text == "" && !pool.closed {
		if pool.empty == nil {
			pool.empty = pool.newString("", hash)
		}
		return pool.empty
	}
	if pool.closed || len(text) > shortStringLimit {
		if borrowed {
			text = strings.Clone(text)
		}
		return pool.newString(text, hash)
	}

	if found := pool.lookupProtected(text, hash); found != nil {
		return found
	}

	if found, set, way := pool.lookupProbation(text, hash); found != nil {
		set.entries[way] = nil
		pool.storeProtected(found)
		return found
	}

	if borrowed {
		text = strings.Clone(text)
	}
	created := pool.newString(text, hash)
	pool.storeProbation(created)
	return created
}

func (pool *stringPool) hash(text string) uint64 {
	return hashString(text)
}

func (pool *stringPool) newString(text string, hash uint64) *luaString {
	return newHashedString(text, hash)
}

func newLuaString(text string) *luaString {
	return newHashedString(text, hashString(text))
}

func newHashedString(text string, hash uint64) *luaString {
	return &luaString{
		text: text,
		hash: hash,
	}
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

func (pool *stringPool) lookupProtected(text string, hash uint64) *luaString {
	shardIndex, setIndex := stringSetLocation(
		hash,
		stringProtectedShardCount,
	)
	shard := pool.protected[shardIndex]
	if shard == nil {
		return nil
	}
	set := &shard.sets[setIndex]
	for _, candidate := range set.entries {
		if candidate != nil && candidate.hash == hash && candidate.text == text {
			return candidate
		}
	}
	return nil
}

func (pool *stringPool) lookupProtectedBytes(
	bytes []byte,
	hash uint64,
) *luaString {
	shardIndex, setIndex := stringSetLocation(
		hash,
		stringProtectedShardCount,
	)
	shard := pool.protected[shardIndex]
	if shard == nil {
		return nil
	}
	set := &shard.sets[setIndex]
	for _, candidate := range set.entries {
		if stringMatchesBytes(candidate, bytes, hash) {
			return candidate
		}
	}
	return nil
}

func (pool *stringPool) lookupProbation(
	text string,
	hash uint64,
) (*luaString, *stringSet, int) {
	shardIndex, setIndex := stringSetLocation(
		hash,
		stringProbationShardCount,
	)
	shard := pool.probation[shardIndex]
	if shard == nil {
		return nil, nil, -1
	}
	set := &shard.sets[setIndex]
	for index, candidate := range set.entries {
		if candidate != nil && candidate.hash == hash && candidate.text == text {
			return candidate, set, index
		}
	}
	return nil, nil, -1
}

func (pool *stringPool) lookupProbationBytes(
	bytes []byte,
	hash uint64,
) (*luaString, *stringSet, int) {
	shardIndex, setIndex := stringSetLocation(
		hash,
		stringProbationShardCount,
	)
	shard := pool.probation[shardIndex]
	if shard == nil {
		return nil, nil, -1
	}
	set := &shard.sets[setIndex]
	for index, candidate := range set.entries {
		if stringMatchesBytes(candidate, bytes, hash) {
			return candidate, set, index
		}
	}
	return nil, nil, -1
}

func stringMatchesBytes(
	candidate *luaString,
	bytes []byte,
	hash uint64,
) bool {
	if candidate == nil ||
		candidate.hash != hash ||
		len(candidate.text) != len(bytes) {
		return false
	}
	for index, value := range bytes {
		if candidate.text[index] != value {
			return false
		}
	}
	return true
}

func (pool *stringPool) storeProbation(value *luaString) {
	shardIndex, setIndex := stringSetLocation(
		value.hash,
		stringProbationShardCount,
	)
	shard := pool.probation[shardIndex]
	if shard == nil {
		shard = new(stringSetShard)
		pool.probation[shardIndex] = shard
	}
	set := &shard.sets[setIndex]
	index := int(set.next % stringCacheWays)
	set.entries[index] = value
	set.next++
}

func (pool *stringPool) storeProtected(value *luaString) {
	shardIndex, setIndex := stringSetLocation(
		value.hash,
		stringProtectedShardCount,
	)
	shard := pool.protected[shardIndex]
	if shard == nil {
		shard = new(stringSetShard)
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

func (pool *stringPool) close() {
	pool.closed = true
	pool.empty = nil
	pool.probation = [stringProbationShardCount]*stringSetShard{}
	pool.protected = [stringProtectedShardCount]*stringSetShard{}
}

func stringSetLocation(hash uint64, shardCount int) (shard, set int) {
	setCount := shardCount * stringSetsPerShard
	location := int(hash % uint64(setCount))
	return location / stringSetsPerShard, location % stringSetsPerShard
}

// hashString follows Lua's useful locality tradeoff: short strings are hashed
// completely, while long strings sample a bounded number of bytes. Equality
// always checks length and contents after the hash.
func hashString(text string) uint64 {
	hash := uint64(len(text))
	step := len(text)>>5 + 1
	for index := len(text); index >= step; index -= step {
		hash ^= hash<<5 + hash>>2 + uint64(text[index-1])
	}
	return mixHash(hash ^ 0x517cc1b727220a95)
}

func hashBytes(bytes []byte) uint64 {
	hash := uint64(len(bytes))
	step := len(bytes)>>5 + 1
	for index := len(bytes); index >= step; index -= step {
		hash ^= hash<<5 + hash>>2 + uint64(bytes[index-1])
	}
	return mixHash(hash ^ 0x517cc1b727220a95)
}
