# Temporary performance investigation

This is a working document for the two published README benchmark rows where
Lunar did not lead at the recorded revision. It is intentionally temporary and
should be removed or folded into permanent documentation when the experiments
are complete.

The published measurements were collected from revision
`1d43aec413fa32e8db7eec1bb185c91b551e0028` on an Apple M3 Pro with Go 1.25.1.
The investigation below was performed at revision `ecda50c` on Linux/amd64
with Go 1.26.0. Current-machine timings and profiles are directional; the
canonical M3 matrix must be rerun before changing the README claims.

## Summary

Only two published rows lose:

| Embedding operation | Lunar | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| Echo a converted 128-byte Go string | 86.20 ns | **81.41 ns** | 144.00 ns |
| Build a table in Go, then checksum it in Lua | 2.230 us | **1.405 us** | 1.645 us |

Both losses are robust in the archived data: every one of the 15 Lunar samples
is slower than every GopherLua sample. The fresh-table samples also all trail
the go-lua samples. Lunar nevertheless allocates less:

| Embedding operation | Lunar | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| Convert and echo string | **0 B / 0** | 16 B / 1 | 176 B / 5 |
| Build, fill, and pass table | **650 B / 6** | 1,384 B / 37 | 1,536 B / 89 |

These rows primarily measure public embedding API semantics. They do not show
a slow Lua return instruction or slow table reads.

## 128-byte Go string echo

### Benchmark path

The Lunar benchmark performs the following work inside the timed loop:

1. `State.String(boundaryString)`
2. `State.CallInto(function, arguments, results)`
3. `Value.AsString()`

The Lua function itself is only `return value`.

GopherLua converts the input with `LString(boundaryString)`. `LString` is a
defined Go string type, so this does not perform Lua-style hashing or
interning. GopherLua then calls the function and retrieves the returned stack
value.

The comparison is valid as a practical public-API benchmark, but it is not an
isolated comparison of equivalent string constructors.

### Baseline Lunar implementation

`State.String` pools strings at most 64 bytes long. A 128-byte value instead
takes the state-neutral long-string path:

- `state.go`: `State.String` and `importAcceptedString`
- `string.go`: `newStateNeutralLongString`, `hashString`, and `stringRef`
- `collection.go`: `attributeString` and `sweepAttributedStrings`

Before the cache experiment, every iteration:

1. Recomputes the sampled hash of the same backing string.
2. Builds a non-owning `stringRef` without copying or allocating.
3. Imports that value when staging the call.
4. Performs a map lookup in `attributedStrings` to determine whether the
   backing has already been charged to the semantic heap.

The current hash follows Lua 5.1's bounded-sampling policy. For 128 bytes the
step is five, resulting in 25 serial byte-mix iterations plus Lunar's finalizer.

### Profile evidence

A current-HEAD CPU profile of the full string benchmark attributed roughly:

- 25% to `hashString`;
- 13% to `attributeString`, nearly all in the map lookup;
- 3% to Lua instruction execution; and
- 2% to `AsString`.

A focused diagnostic measured `State.String` alone at about 16 ns per repeated
128-byte input on the current machine. A `CallOne` variant saved only about
0.5 ns versus `CallInto`, so all-results handling does not explain the loss.

### PUC Lua comparison

Lua 5.1's `lua_pushlstring` calls `luaS_newlstr`. The latter computes the same
bounded-sampling hash, searches the intern bucket, compares matching strings,
and on a miss allocates a `TString`, copies the bytes, and links it into the
string table:

- <https://www.lua.org/source/5.1/lapi.c.html#lua_pushlstring>
- <https://www.lua.org/source/5.1/lstring.c.html#luaS_newlstr>

Lunar already avoids the interner traversal, allocation, and byte copy. There
is no missing Lua 5.1 return-side optimization to transplant. The useful
observation is that PUC must eagerly hash because it interns every string,
whereas Lunar does not intern strings longer than 64 bytes.

Modern PUC Lua 5.4 supplies two relevant precedents: long strings can defer
hashing, and zero-terminated API strings use a 53-by-2 cache whose set is
selected from the caller's pointer. PUC confirms a hit with `strcmp` and uses
this path for `lua_pushstring`; explicit-length `lua_pushlstring` goes directly
to `luaS_newlstr`. Lunar's exact pointer-plus-length cache is therefore a close
Go-specific analogue, not a direct copy:

- <https://www.lua.org/source/5.4/lstring.c.html>
- <https://www.lua.org/source/5.4/lapi.c.html#lua_pushstring>
- <https://www.lua.org/source/5.4/llimits.h.html>

### Optimization candidates

#### 1. Recent exact-backing identity cache

Add a one-entry or tiny fixed-size cache for recently admitted long strings.
Key it by the exact Go string backing address and length, not just content.
Cache the complete hashed `stringRef`.

The intended steady-state path is:

1. `State.String` checks the recent backing identity before hashing.
2. A hit returns the cached `stringRef`.
3. `attributeString` recognizes the same cached ref before consulting the map.

The cache must be state-local, add no unbounded retention, and be cleared when
attributed strings are swept and when the collection controller is released.
While valid, its backing must already be present in `attributedStrings`; the
cache therefore duplicates an existing root rather than extending lifetime.

This is the first implementation target on branch
`perf/long-string-identity-cache`.

##### Experiment status

The branch now implements a four-slot direct-mapped cache associated with the
State's collection controller. A Go backing address selects one slot, and a
hit still requires exact backing-pointer and length equality. Each slot
contains the already-hashed `stringRef` and is populated only after that ref is
confirmed in `attributedStrings`.

Both `State.String` and ingress derive the direct slot independently, so a
batched call with several cached long strings can bypass both the hash and the
attribution map. Cache misses follow the existing hash and map path. Semantic
reconciliation keeps entries whose strings remain in the Lua graph and clears
dead entries; releasing the collection controller clears the set in place.

After review, the mechanism was consolidated into one internal
`attributedStringSet` owned lazily by the collection controller: the swept
attribution map, its high-water mark, and the four recent slots now live in a
single struct with `lookup`, `admit`, `sweep`, and `clear` operations, and
`stringRefBacking` moved beside the `stringRef` representation in `string.go`.
The recent slots front only this attribution set; pool-built strings from
library and callback paths (for example `Frame.ReturnString`) charge debt per
creation and are never attributed, so routing them through the set would be an
accounting-model change, tracked as a separate follow-up.

`runtimeState` no longer carries a cache pointer. Its amd64 structural size is
344 bytes, below the 352-byte pre-branch base, so the per-State allocation
stays in the 352-byte size class and the earlier ~32-byte physical regression
is gone. The lazy first-admission allocation is now one 80-byte set (80-byte
size class) instead of a separate 64-byte slot array; there is still exactly
one first-use allocation, no steady-state allocation, and no
cardinality-dependent growth.

A post-refactor spot check re-ran `go_string_echo_128B` back to back against
the stashed no-cache baseline on the same host session: baseline median about
80.1 ns, consolidated cache about 55.9 ns, still 0 B/op. The relative
improvement matches the pre-refactor measurement; absolute numbers differ from
the earlier session because the host was in a different performance state, so
only the canonical M3 matrix should be quoted.

On the current Linux/amd64 host, the current-base baseline had a median of
about 78.63 ns across ten one-second samples. Five isolated one-second samples
with the lazy cache had a median of 52.21 ns, a roughly 34% reduction.
The corresponding GopherLua median was 68.04 ns. Lunar remained at 0 B/op and
0 allocs/op.

The component benchmark shows the intended working-set behavior:

| Input pattern | Before | Cache branch | Effect |
| --- | ---: | ---: | ---: |
| One recurring 128-byte backing | ~26.3 ns | ~7.2 ns | large win |
| Two backing identities in distinct slots | ~26.3 ns | ~7.3 ns | large win |
| Four backing identities in distinct slots | ~27.7 ns | ~7.3 ns | large win |
| Four slots plus one deliberate collision | ~27.4 ns | ~15.5 ns | partial-hit win |
| 4,096-backing cache-cold cycle | ~31.1 ns | ~32.8 ns | ~1.7 ns cost |

The 4,096-backing case is the deliberate worst case: every iteration pays the
small address/identity probe but almost none can reuse a cache entry. The
absolute miss cost is about 1.7 ns on this machine and remains allocation-free.
This tradeoff should be checked on the canonical M3 before merging.

### Other benchmark impact

Only the 128-byte embedding row and the component matrix directly execute the
new lookup in their timed loops. Existing short host-string, internal Lua
string, table, interpreter, and program rows bypass it. A local ten-sample
ABBA sweep of all 13 Lunar comparison rows and focused native, collection, and
table sentinels found no allocation-count changes. Unrelated timing deltas
moved in both directions across short runs, including reversals between the
broad and focused measurements, so they do not establish another regression
or win. One pinned broad run suggested about a 3% Lua-to-Go callback slowdown,
but dedicated native-call runs did not reproduce it consistently. Treat that
as an open signal, not a measured regression. The canonical M3 collection
remains the decision point.

#### 2. Lazy long-string hashing

Represent an unpooled host string without a hash and compute the hash only when
table-key or string-map machinery requires it. This helps streams of unique
pass-through strings where an identity cache cannot hit.

This is higher risk because `stringRef` is currently used as a Go map key for
attribution. Hash materialization must not mutate the equality identity of an
entry already stored in a map.

#### 3. Alternative bounded hash

The Lua 5.1 byte-at-a-time loop was designed for portable C in 2008. A chunked
modern hash could reduce serial dependency, but it has a wider regression
surface across table keys, byte strings, and collision behavior. It should be
considered only after the cache and lazy-hash experiments.

### String benchmark additions

The branch adds a component matrix for 65-, 128-, and 4,096-byte strings and
for one, two, four, colliding, and cache-cold working sets. Further useful
cases would separate:

- string construction from a prebuilt-string call;
- lengths 63, 64, 65, and 128;
- repeated exact backing from equal bytes in distinct backings;
- one recurring string from two alternating strings; and
- pass-through strings from strings retained as table keys.

The headline row should continue to measure the public conversion cost.

## Fresh Go-built table

### Evidence that reads are not the problem

The prebuilt-table checksum is already substantially faster in Lunar:

| Operation | Lunar | GopherLua | go-lua |
| --- | ---: | ---: | ---: |
| Pass prebuilt table and checksum | **312.4 ns** | 578.3 ns | 972.9 ns |

As a rough, non-independent subtraction of this common call/checksum work from
the fresh-table row, construction and publication contribute approximately:

- Lunar: `2230 - 312.4 = 1917.6 ns`
- GopherLua: `1405 - 578.3 = 826.7 ns`
- go-lua: `1645 - 972.9 = 672.1 ns`

### Lunar implementation and profile

`State.NewTableWithCapacity` allocates a compact `tableObject`, reserves its
array and record stores, registers it with the semantic object ledger, and
immediately publishes a canonical owning Go handle.

Publication through `hostDirectory.publish` currently performs:

- mutex acquisition;
- a weak object-key construction and directory lookup;
- host-token allocation;
- a second directory membership check;
- weak token registration and map assignment; and
- one directory-maintenance unit.

The directory is necessary because arbitrary live Go values must act as roots
for Lunar's semantic heap without making every compact Lua object permanently
larger.

A current-HEAD profile attributed roughly:

- 32-33% to `hostDirectory.publish`;
- 29-31% to automatic synchronous semantic collection;
- 9-10% to all 20 public field writes; and
- about 9% to the compact table allocation itself.

The six allocations are the table, array backing, record backing, host token,
and two weak registrations.

### PUC Lua comparison

PUC Lua's `lua_createtable` creates the compact table directly on its API
stack. That stack is already a collector root, so PUC needs no Go-style owning
token or weak host directory:

- <https://www.lua.org/source/5.1/lapi.c.html#lua_createtable>
- <https://www.lua.org/source/5.1/ltable.c.html#luaH_new>

PUC also initializes its hinted array storage up front, allowing sequential
`lua_rawseti` operations to hit direct array slots. Lunar reserves capacity but
starts with logical length zero, so inserts 1 through 16 still take the general
growth and density path.

PUC's collector advances through bounded incremental `luaC_step` work. Lunar's
semantic collector currently performs a full synchronous cycle:

- <https://www.lua.org/source/5.1/lgc.c.html#luaC_step>

### Optimization candidates

1. Add a known-fresh publication path that skips canonical-token lookup and
   duplicate membership checks. Batch directory maintenance and investigate
   pruning stale entries while discovering host roots.
2. Add a scoped build-and-call API that can stage an internal compact table
   without publishing an owning Go handle unless the table escapes. A simpler
   mixed array/record bulk builder could defer publication and use internal
   setters. `NewTableFrom` does not directly express a mixed 16-element array
   and four-field record.
3. Fast-path sequential inserts within reserved array capacity, or expose an
   appropriate form of the VM's existing `rawSetList` operation.
4. Make semantic collection incremental, generational, or more adaptive for
   high-churn construction bursts.
5. Experiment with a per-object weak token only on a benchmark branch. Adding
   a word to every 80-byte table may move it to a 96-byte Go size class and
   materially weaken Lunar's retained-memory advantage.

Field-read specialization and general table probing are low priority for this
row because the prebuilt-table benchmark already wins.

## Acceptance criteria for the first experiment

The recent-long-string cache should:

- preserve zero allocations in the 128-byte echo benchmark;
- improve the repeated-backing case enough to beat or closely match the
  current GopherLua result on the same machine;
- avoid helping equal strings with distinct live backings by mistaken pointer
  or content identity;
- preserve semantic heap charging and re-attribution after a sweep;
- avoid retaining a string after it has left the Lua graph and collection has
  completed;
- remain state-local and obey the existing serialized `State` contract; and
- leave the full test suite and race tests clean.

All local criteria above pass. Validation included the root and benchmark
module test suites, the race detector, strict `checkptr` and `cgocheck2`,
`go vet`, repeated sweep/cache tests, a public batched-call case, allocation
checks, and a sentinel-length string collection cycle.

Before publishing any result, rerun the canonical benchmark matrix on the M3
configuration recorded under `benchmarks/results`.
