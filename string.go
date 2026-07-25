package lua

const (
	shortStringLimit          = 64
	stringCacheWays           = 4
	stringSetsPerShard        = 8
	stringProbationShardCount = 8
	stringProtectedShardCount = 16
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
	hash := pool.hash(text)
	if text == "" && !pool.closed {
		if pool.empty == nil {
			pool.empty = pool.newString("", hash)
		}
		return pool.empty
	}
	if pool.closed || len(text) > shortStringLimit {
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
