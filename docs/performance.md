# Performance

Badger Lua targets PUC Lua 5.1-class interpreter performance without giving
up Go pointer safety, the compact runtime representation, or defined Lua 5.1
behavior. Performance work follows profiles and general workloads. A codec,
application schema, field name, or one benchmark trace is never part of the
runtime design.

This document records the active performance work. Long-lived representation
and ownership rules remain in [architecture.md](architecture.md).

## Current position

The first end-to-end large-CBOR diagnostic, before the active storage
tranches, used the same 9,208,046-byte input, Lua codec, workload, and
structural digest in fresh processes. The graph has 183,513 tables and
938,452 entries. Five samples are enough to identify the large gaps below,
but are not release qualification evidence.

| Mode and runtime | Median elapsed | Allocated bytes | Mallocs |
| --- | ---: | ---: | ---: |
| Load, Badger Lua | 1.734 s | 174.3 MB | 1.046 M |
| Load, Badger predecessor `169b37d` | 1.511 s | 80.6 MB | 0.504 M |
| Load, stock GopherLua | 1.876 s | 784.6 MB | 12.734 M |
| Save, Badger Lua | 1.056 s | 253.9 MB | 5.412 M |
| Save, Badger predecessor `169b37d` | 0.854 s | 234.9 MB | 3.080 M |
| Save, stock GopherLua | 1.226 s | 502.9 MB | 9.352 M |

Badger therefore already removes most of stock GopherLua's allocation volume
and is modestly faster. That is not the replacement bar. Load is about 15%
slower than the mature predecessor while allocating 2.16 times as many bytes
in 2.08 times as many allocations. Save is about 24% slower, allocates 8% more
bytes, and performs 76% more allocations. Those are active defects to close,
not acceptable costs of the new interface.

The predecessor evidence is pinned by revision as well as workload. An earlier
51.15 MiB retained-heap headline came from revision `48ca60b` with Go's default
sampling, while the timing and allocation rows above came from the later
`169b37d` engine. Exact rate-one post-GC profiles put those revisions at
49.76 MiB and 44.27 MiB respectively. The latter is the current predecessor
memory comparator; mixing the earlier retained number with the later timing
rows would weaken every subsequent gate.

A separate native runner measured PUC Lua 5.1.5 on the same host and corpus.
Its five-sample median load wall time was 0.803 seconds, with 0.757 seconds of
Lua operation CPU and a 60,680,814-byte decoded-graph heap delta
(57.870 MiB). Save operation CPU was 0.701 seconds; its parent wall time also
includes constructing the source graph and is not comparable to the Go
workers' save-only interval. PUC reports allocator request sizes while Go
profiles report size-class charges, so the memory comparison is directional.
These native numbers nevertheless show that matching the predecessor is an
intermediate gate rather than the final objective.

## Evidence

Profiles identify allocation policy as the first problem.

- In load, table-store rehashing allocates about 34 MB. Reading the 9.2 MB
  input grows about 32 MB of temporary backing before publishing another
  9 MB owned result. The workload creates 183,515 112-byte Table objects.
  Short string headers and copied backing account for another large group of
  small allocations.
- In save, table-store rehashing, dense-array growth, and Table objects account
  for about 84% of sampled allocated bytes. Builder growth and string objects
  are the next visible groups.
- On this Darwin host, `runtime.madvise` accounts for more than half of the
  sampled load CPU. It is downstream of allocation and span churn. Dispatch
  tuning cannot recover time spent asking the runtime for and returning that
  memory.

PUC's sources explain which differences matter:

- Badger's `slot` and PUC's `TValue` are both 16 bytes on the qualification
  ABI. Badger's hash entry and PUC's `Node` are both 40 bytes. Replacing the
  value representation or shaving an incidental field is not the first
  answer.
- PUC's chained scatter table remains useful at full node occupancy. Before
  the record-store tranche, Badger's linear-probe store grew before 75%
  occupancy. For 1, 2, 4, 8, and 16 live record fields, PUC needs 1, 2, 4, 8,
  and 16 nodes; Badger commonly reserved 4, 4, 8, 16, and 32.
- PUC re-evaluates all positive integer keys during rehash and chooses the
  largest power-of-two array span whose occupancy exceeds one half. Badger
  currently makes local growth and promotion decisions, which can allocate
  and move the same logical table several times.
- PUC stores a string header, bytes, and terminator in one allocation and
  interns every string. Its collector sweeps that interning table; the table
  is not a permanent root. Before the flat-string tranche, Badger allocated a
  separate runtime string header on every bounded-cache miss, and byte or
  borrowed-string misses could also allocate backing.

The frozen predecessor is useful evidence, not source to transplant. Its flat
short-string storage substantially reduced malloc count, and its recurring
record layouts reduced backing for repeated small maps. It also contains
compatibility materialization, parallel representations, identity
authentication, and cache machinery that this implementation does not need.
No predecessor mechanism is adopted unless it fits Badger's canonical object
model and improves general workloads.

## Completed causal results

The regular-file read tranche reduced large-CBOR load time by about 4% and
allocated bytes by 19.2%, while leaving malloc count essentially unchanged.
It removed geometric buffer growth and one full-size publication copy.

The flat-string tranche was then measured against that exact-read revision in
five paired, alternating fresh-process samples:

| Mode | Elapsed | Allocated bytes | Mallocs |
| --- | ---: | ---: | ---: |
| Large-CBOR load | 1.77% faster | 4.33% lower | 24.96% lower |
| Large-CBOR save | 1.43% faster | 6.17% lower | 12.08% lower |

The allocation reductions are the primary causal evidence: load removed about
261,000 allocations and save removed about 654,000. General string-field,
global, concatenation, and builder benchmarks improved by roughly 1–12%.
Common builders also dropped one allocation per result. Varied-size decimal
and common-prefix string maps cover hits, misses, construction, and churn;
the accepted hash has no sequential-key cliff and no confirmed representative
regression.

The chained record-store tranche was then measured against that flat-string
revision. Its balanced five-round string-map matrix covers hits, misses,
same-key churn, different-key replacement, and construction for decimal and
common-prefix keys from 1 through 5,000 fields:

| Workload group | Elapsed |
| --- | ---: |
| All 90 string-map cells | 13.31% faster |
| Hits | 1.30% faster |
| Misses | 7.77% faster |
| Same-key churn | 27.71% faster |
| Different-key replacement | 17.10% faster |
| Construction | 10.27% faster |

Steady-state replacement remains allocation-free. Construction through 1,024
fields retains two allocations while using about half the previous backing
bytes; at 1,024 fields it falls from 82,032 to 41,072 bytes. At 5,000 fields
both policies round to the same 8,192-entry size class.

The 12-cell core-table geometric mean was 0.36% slower and the four-cell
iteration mean was 0.20% faster, both effectively flat. Dense insertion
improved 3.37%. The two representative outliers were an extreme sparse
integer shift, 14.16% slower while reducing bytes from 1,649 to 945, and
mixed array/record traversal, 8.26% slower. They remain explicit inputs to
the numeric-density work rather than reasons to add workload-specific paths.

Five alternating fresh-process large-CBOR pairs produced:

| Mode | Elapsed | Allocated bytes | Mallocs |
| --- | ---: | ---: | ---: |
| Load | 0.92% slower | 27.10% lower | 14.16% lower |
| Save | 1.86% slower | unchanged | unchanged |

No CBOR timing cell regressed by 5%. These results credit the record store for
its general map and allocation improvements, not for a codec speedup.

## Work order

### 1. Sequential input and constructed output — complete

Known-size regular files reserve their exact remaining size, and the owned
input buffer becomes an immutable Lua string without a second full copy.
Unknown readers retain bounded geometric growth and the existing error,
append, and context-polling semantics. The same ownership rules apply to
string and IO builders so a completed buffer is transferred once.

This small independent tranche removes roughly 32 MB of avoidable growth from
the current 9.2 MB load before the deeper representations are profiled again.
It is measured with fixed files, nonzero file offsets, short and adversarial
readers, growing files, concatenation, formatting, `table.concat`, and codec
load/save. It is not a special path for the benchmark fixture.

### 2. String storage and identity — flat representation complete

Runtime strings live directly in the two-word compact value as a GC-visible
byte pointer plus packed length and hash metadata. There is no mandatory
per-string Go wrapper. Borrowed source slices are copied when retaining them
would pin unrelated input, and completed owned buffers transfer their backing
without another copy.

The bounded reuse policy remains the ownership bound. A full PUC-style
interner must be integrated with semantic collection so unreachable entries
can be removed; an unbounded Go map that roots every historical string is not
an acceptable shortcut. Page packing is also deferred until retained-memory
measurements justify its page-pinning tradeoff. String equality always retains
a hash and byte fallback, so pointer identity is an optimization rather than
a semantic requirement.

The gate covers unique-string churn, repeated keys, substrings,
concatenation, patterns, table string fields, native string arguments and
results, source constants, retained small substrings, and cross-State scalar
use. Allocation count must fall without increasing retained memory or making
long-lived small strings pin unbounded pages.

String access is centralized behind text, hash, and equality operations.
Tables, the compiler, and libraries do not branch on the short and long
storage encodings. This tranche precedes the table-store replacement so the
new node design can rely on the final cached-hash representation.

### 3. Table storage and resizing — complete

The record half uses one PUC-informed indexed chained-scatter store. Cached
32-bit hashes select a power-of-two main position; colliding nodes use
index-plus-one links, and Brent-style relocation keeps every displaced node
reachable from its main chain. Source hints reserve the smallest sufficient
power of two and all nodes are usable. Unhinted tables still begin at four
nodes on their first record insertion, avoiding repeated one-node Go
allocations. There is no second backend, shape cache, or codec-specific lane.

Deletion retains the key and collision links so Lua 5.1 `next` can continue
from a deleted field. Updating an existing field leaves its physical position
unchanged. An absent-key insertion makes traversal order undefined, so it may
unlink and recycle one retained tombstone without allocating; a dead chain
head promotes its successor. A larger dead majority compacts on an insertion
seam to release the other retained Go pointer keys until semantic collection
can own that work.

The store header is now 32 bytes, down from 48, and the canonical Table header
is now 80 bytes, down from 112. Each record node remains 40 bytes, the same
size as PUC's `Node` and the previous Badger entry; its former 64-bit hash word
now contains a 32-bit cached hash and a 32-bit successor index. The named
capacity bound preserves index-plus-one encoding on 32- and 64-bit builds.

`Table` now owns both-lane allocation policy. When an absent insertion needs
new backing, it counts every live positive integer in power-of-two ranges,
includes the pending field, and selects the largest array span that is
strictly more than half occupied. The array candidate is bounded at `2^26`,
matching PUC Lua 5.1. Remaining live fields receive the smallest sufficient
record store, and each live field is moved at most once during that
redistribution.

A conservative summary of the smallest positive integer record class proves
when growing only the record store cannot change the global answer. Dense
growth with no integer records can likewise allocate the exact selected
power-of-two array directly instead of performing a preliminary full scan.
Neither mechanism is an identity cache or a second storage policy; stale
summary information can only request an unnecessary full calculation. It
shares the final compact suffix with the metamethod absence cache, so it adds
no allocation class to the canonical Table header.

Between allocation seams, existing array and record fields update or delete
in place, absent nil writes do nothing, and reserved array backing accepts a
new key only while projected occupancy remains above one half. The initial
`1..4` array class is a deliberate Go size-class adaptation: four compact
slots cost less than the first unhinted four-node record store. This replaces
`maxDenseArrayGap`, one-key-at-a-time tail promotion, and independent
array/record growth with one density invariant.

The policy keeps `t[50_000_000] = 1` as one record entry and never grows the
array above `2^26`. Updates and deletions cannot move lanes because Lua
permits them during `next`; a genuinely absent insertion is the legal
reordering and tombstone-release seam. Physical movement does not add a
second logical structural mutation or invalidate the string-keyed metamethod
absence cache.

Five-sample equal-layout comparisons against the completed record-store
revision show the intended order independence at 1,024 dense integer fields:

| Construction order | Elapsed | Allocated bytes | Allocations |
| --- | ---: | ---: | ---: |
| Ascending | 11.614 to 9.114 us, 21.5% faster | 61,488 to 37,296 | 14 to 10 |
| Descending | 76.842 to 27.845 us, 63.8% faster | 186,704 to 61,840 | 24 to 10 |
| Randomized | 75.741 to 31.503 us, 58.4% faster | 145,745 to 61,904 | 23 to 11 |

The final dense layout is the same 1,024-slot array regardless of insertion
order. Exact-half mixed construction at 1,024 fields is 4.6% faster and falls
from 39,504 to 30,928 bytes. The single insertion that crosses from exact
half to over half is deliberately expensive: it is about 21% slower while it
migrates the existing record fields into their globally selected array.
After that transition, the formerly recorded key is 66.6% faster to read,
19.4% faster to update, and mixed traversal is 29.2% faster.

Strided sparse construction retains exactly the same backing bytes and
allocation counts from 16 through 5,000 fields; medians range from 0.6% to
4.8% slower, while allocation-free lookup and update remain within roughly
5%. A fourteen-cell executor/raw-table screen has no median regression above
2.9% and no allocation change.

One seeded eight-key permuted raw-construction case remains slower by roughly
0.1 microsecond. Its order first makes the cheap initial array sparse, moves
those fields into records, and later crosses the strict-half threshold back
into an array. A tested maximum-class shortcut improved that cell but added
an allocation at 48 keys and slowed sparse construction, so it was rejected
rather than retained as benchmark-specific policy. Ascending dynamic arrays
and larger randomized tables do not share that cliff, and the standing
executor constructor cell is 0.7% faster.

Five alternating fresh-process large-CBOR pairs are intentionally uneventful:

| Mode | Elapsed | Allocated bytes | Mallocs |
| --- | ---: | ---: | ---: |
| Load | 0.10% faster | unchanged | unchanged |
| Save | unchanged | 1.00% lower | unchanged |

The density change therefore earns its place through generic table layout,
construction, and steady-state access rather than a codec timing claim. The
frozen mature predecessor remains the next end-to-end memory and speed gate.
A shared record layout remains a later option only if a fresh profile shows a
general recurring-record gap; the predecessor's shape system is not a
starting dependency.

### 4. Canonical byte strings — complete

The empty string and every one-byte string now have finite, process-wide
backing. These 257 immutable values are State-neutral, bypass adaptive
admission, and remain valid after a State closes. All ordinary string
publication paths select them before allocating or retaining caller storage.
`string.char` publishes its single-argument result directly as the same
compact value.

This is the finite case of PUC's cache-before-allocation behavior, not a codec
special case or an unbounded interner. It makes all 256 warm single-byte
`string.char` results allocation-free and also applies to source constants,
substrings, byte inputs, table keys, and public string construction.

Five alternating fresh-process comparisons against the density revision
produced:

| Mode | Median elapsed | Allocated bytes | Mallocs |
| --- | ---: | ---: | ---: |
| Large-CBOR load | 1.6603 to 1.6223 s, 2.29% faster | unchanged | 673,641 to 669,549 |
| Large-CBOR save | 1.0426 to 1.0111 s, 3.02% faster | 235.87 to 232.69 MB | 4,757,750 to 3,251,503 |

The save path therefore removes about 1.51 million allocations at the exact
profiled cause. Its allocation count is now about 5.6% above the mature
predecessor rather than 54% above it, and its allocated bytes are slightly
lower. A representative string-construction screen retained identical
allocation counts and no timing cell moved by 2%.

The forced-GC decoded graph remained about 67.3 MiB, as expected: canonical
byte backing is process-wide and does not change the graph's table layout.
The matched `169b37d` predecessor's exact post-GC profile is 44.27 MiB. Closing
that live-memory gap therefore remains table-layout work rather than a reason
to add broader string retention.

A general 64-byte stack scratch path for multi-byte `string.char` results was
also tested. Go's escape analysis placed the scratch on the heap across the
result-publication seam, leaving one allocation and increasing its minimum
size. It was rejected. Multi-byte results retain one exact owned allocation
until a cache-before-allocation builder can prove stack ownership without
compiler directives or unsafe lifetime claims.

### 5. Table object layout — 96-byte header complete

The table's 64-bit structural generation had production writes but no
production reader. Lua 5.1 traversal uses physical array positions and
retained record keys rather than a generation, so the field was test
telemetry rather than runtime state. Removing it and its two-boolean mutation
plumbing moves each Table from a 104-byte logical header charged in Go's
112-byte class to a 96-byte header charged as 96 bytes. Tests now assert
actual value, storage-location, continuation, and metamethod-cache behavior
instead of the deleted counter.

Against the canonical-byte revision, exact allocation movement is:

| Mode | Allocated bytes | Mallocs | Forced-GC graph |
| --- | ---: | ---: | ---: |
| Large-CBOR load | 98.19 to 95.26 MB | unchanged | 67.34 to 64.52 MiB |
| Large-CBOR save | 232.69 to 226.82 MB | unchanged | unchanged loaded graph |

Load creates 183,513 retained graph tables; save also creates temporary
tables, hence its larger cumulative byte reduction. Ten fresh-process samples
with both process orders put load timing 1.29% slower and save 0.30% faster,
inside the tranche ceiling and downstream of a deterministic 2.82 MiB
retained-heap reduction. Longer generic table reruns kept every selected
median within 1.6%, retained zero-allocation steady-state access, and reduced
the standing constructor from 448 to 432 bytes per operation.

That result left two 24-byte Go slice headers consuming half of every Table,
even when their backing was empty. The following tranche replaces them
without changing either storage policy.

### 6. Compact table descriptors — 80-byte header complete

Lua's table bounds fit in 32 bits; a native Go slice nevertheless spends two
machine words on length and capacity. Both private table vectors now use one
typed backing pointer plus 32-bit length and capacity fields. The descriptor
is 16 bytes on 64-bit Go instead of 24, reducing the record-store header from
40 to 32 bytes and the canonical Table from 96 to 80 bytes. On 32-bit Go the
descriptor remains 12 bytes, the same size as a slice.

The backing pointer remains `*T`, so the collector retains the allocation's
typed pointer bitmap. Random access uses one inlined checked pointer
calculation; sequential scans and copies reconstruct one fixed-capacity slice
view outside the loop. Growth can occur only through the descriptor
constructor or a checked logical-length change. No pointer or view survives a
growth, redistribution, or rehash seam, and ordinary typed writes retain Go's
write barriers. A forced-GC regression test covers references in array
values, record keys, and record values.

Against the 96-byte-header revision:

| Mode | Allocated bytes | Mallocs | Forced-GC graph |
| --- | ---: | ---: | ---: |
| Large-CBOR load | 95.26 to 92.32 MB | unchanged | 64.53 to 61.73 MiB |
| Large-CBOR save | 226.82 to 220.95 MB | unchanged | unchanged loaded graph |

The load graph retains 183,513 tables, so the measured 2.80 MiB live reduction
is exactly 16 bytes per table. Save constructs roughly another 183,000
temporary tables and therefore removes about 5.87 MB of cumulative allocation
without changing the number of allocations.

Five alternating fresh-process pairs put load time 1.51% slower and save time
1.05% slower, inside the directional tranche ceiling. A second causal pairing
showed that the final point-versus-sequential accessor placement was neutral
(+0.02% load and -0.12% save); the remaining movement is the compact
descriptor itself plus process noise.

The general screen did not conceal that tradeoff. Sixteen representative
string-map hit, miss, churn, and construction cells had a median movement of
0.73% faster and no slowdown above 1.73%. Dense Lua table execution was 0.98%
faster, sparse execution 0.18% slower, and construction 0.83% faster while
allocating 16 fewer bytes. Traversal ranged from 2.28% slower at 16 keys to
1.77% faster at 5,000 keys. The narrowest public integer set-plus-get
microcell was 2.78% slower, or about 0.25 ns; it remains checked rather than
adding an unchecked access path for one microbenchmark. Every steady-state
cell remains allocation-free.

Packing length and capacity into one 64-bit field was also measured. It saved
no object bytes and did not improve point access or combined length/capacity
reads, so the clearer two-field descriptor remains canonical. The loaded
graph itself is 61.595 MiB after removing non-graph allocations from the exact
profile, about 3.73 MiB above the measured PUC Lua 5.1 graph. The `169b37d`
predecessor remains leaner at 44.27 MiB. Shared recurring record storage was
evaluated next, but the descriptor did not pre-commit the runtime to retaining
it.

### 7. Shared record layouts — experiment rejected

An automatic shared-layout experiment separated recurring short string keys
from each table's compact value vector. On the large decoded graph it reduced
the exact forced-GC heap from 61.73 to 39.48 MiB. Repeated record lookup also
became cheaper. Those results proved that shared key metadata can matter, but
they did not prove that a classless Lua table should speculate on layouts.

The general counterexample was common-prefix, unique-tail construction:

```lua
{ kind = "room", room_123 = value }
{ kind = "room", room_456 = value }
```

Once `kind` became recurring, each later table briefly adopted its one-field
layout, missed the unique extension, and promoted back to the generic store.
Compared with the compact generic parent, four-, eight-, and sixteen-field
construction became about 54%, 40%, and 46% slower. Allocations rose from two
to three, four, and five, while charged bytes rose from 240/400/784 to
304/688/1,520. Completely unique first keys were much closer to neutral, so a
benchmark containing only those keys would have concealed the defect.

Shared property layouts are established designs when an object has a class,
prototype, type, or allocation template that scopes the metadata. Ordinary
PUC Lua, LuaJIT, and Luau tables instead retain their own key/value nodes.
Without such identity, Badger needed probabilistic admission, bounded
eviction, transition learning, promotion, and collision policy merely to
guess whether an ordinary table was record-like. That machinery failed the
complexity and non-record neutrality bar.

The experiment therefore remains off the production branch. A future
compiler allocation-site or explicit object design may revisit shared storage
only if it can select the final representation without speculative
conversion. CBOR memory alone is not sufficient evidence.

### 8. Exact table value replacement — complete

A CPU profile of allocation-free table updates found Lua-value equality in
the mutation path. Existing fields called `rawSlotEqual` before replacement,
even though assignment needs the latest representation rather than an
equality decision. This was both unnecessary work for changing numbers and a
Lua 5.1 error: computed `+0` and `-0` compare equal, but the stored sign is
observable through division.

Table updates now skip only an exact pointer-and-bits match. Otherwise they
use the existing barrier-aware slot write. The one small helper inlines into
array, record, public string, integer, and executor mutation paths. It adds no
state, cache, allocation, or alternate object representation.

An external embedding runner kept the harness and binary surface identical
while changing only the imported runtime. Eight alternating samples of 100
numeric string-field updates per Lua call improved from 2,113.4 to
1,975.1 ns/call, or 6.55%. The corresponding read-only loop was neutral
(2,070.1 versus 2,079.3 ns/call), and both remained allocation-free.
Existing public set-plus-get benchmarks improved by 14.58% for an integer
field and 6.33% for a string field. Twelve unrelated string-map hit and churn
cells stayed within 3.4% with no allocation movement.

Five alternating fresh-process large-CBOR pairs were likewise neutral:

| Mode | Elapsed | Allocated bytes | Mallocs |
| --- | ---: | ---: | ---: |
| Load | 0.03% faster | unchanged | unchanged |
| Save | 0.16% faster | unchanged | unchanged |

Tests cover the latest signed-zero representation through public array,
record, and string setters as well as both executor table lanes. Full, race,
checkptr, and vet gates pass.

### 9. Constant-string table instructions — complete

Profiles of allocation-free field loops put about 36% of read time in the
record-store walk, 27% in generic slot equality, and 7% in key normalization.
Writes showed the same distribution. This is ordinary field, global, and
method work; no codec was involved.

The compiler now emits private `GETFIELD`, `SETFIELD`, and `SELFFIELD`
instructions only when an RK operand is provably a string constant. Globals
use the same prehashed string-slot kernel. Register-held strings, numeric and
reference keys, and constants spilled beyond RK range retain the canonical
generic instructions. Equal strings with different backing still compare by
content after the cached-hash check.

The opcode split was measured against a simpler design before being retained.
That alternative kept canonical Lua 5.1 table instructions and selected the
same typed kernel at runtime. Its best control-neutral form improved the five
field read, write, combined, missing, and polymorphic cells by a 14% geometric
mean. Dedicated instructions improved the same group by about 23%, including
roughly twice the write gain, while dynamic-key controls remained neutral.
The simpler design also enlarged the generic read helper from a 96-byte arm64
frame to 128 bytes. The dedicated split leaves it at 96 bytes, reduces the
generic write helper from 176 to 160 bytes, and leaves the 160-byte
`runInstructions` frame unchanged.

Five-sample medians against the preceding exact-assignment revision were:

| Executor workload | Movement | Allocations |
| --- | ---: | ---: |
| Constant field read | 25% faster | unchanged at zero |
| Constant field write | 22% faster | unchanged at zero |
| Combined field read/write | 15% faster | unchanged at zero |
| Missing constant field | 35% faster | unchanged at zero |
| Two-table polymorphic field | 15% faster | unchanged at zero |
| Global read/write | 13% faster | unchanged at zero |
| Method call | 7% faster | unchanged at zero |
| `__index` function | 6% faster | unchanged at zero |
| Dynamic string key | within 1% | unchanged at zero |
| Dense and sparse numeric keys | within 2% | unchanged at zero |

Lua 5.1 chunk compatibility is an explicit boundary rather than an assumption.
The dumper lowers private field instructions to `GETTABLE`, `SETTABLE`, and
`SELF`; the decoder rejects nonstandard executable opcodes, validates
canonical chunks, then specializes eligible instructions internally.
`SETLIST` extended block words are never interpreted or rewritten as opcodes.
Round-trip and real PUC Lua 5.1 execution tests cover that contract.

### 10. Typed compact-value predicates — complete

The re-profile found a representation tax below several otherwise unrelated
operations. A one-type question such as "is this slot nil?" or "is this slot
a table?" called the complete `Kind` decoder. That decoder must distinguish
all scalar sentinels and validate the object tag; PUC's equivalent `ttis*`
operation is one typed tag test.

Private runtime slots now expose direct nil, number, string, function,
userdata, thread, and table predicates. The predicates do not change the
representation. They use facts already established at checked public
boundaries and prototype verification, and mask the low object tag so private
high flags such as the native-function bit remain valid. Full type dispatch
and diagnostics continue to use `Kind`.

Ten-sample medians against the constant-string-instruction revision showed:

| Executor workload | Movement | Allocations |
| --- | ---: | ---: |
| Dense dynamic integer table | 9% faster | unchanged at zero |
| Sparse dynamic integer table | 3% faster | unchanged at zero |
| Dynamic string-key table | 6% faster | unchanged at zero |
| Constant field read | 17% faster | unchanged at zero |
| Constant field write | 12% faster | unchanged at zero |
| Combined constant field | 20% faster | unchanged at zero |
| Global read/write | 18% faster | unchanged at zero |
| Polymorphic field | 10% faster | unchanged at zero |
| Missing field | 7% faster | unchanged at zero |
| Method lookup and call | 7% faster | unchanged at zero |
| `__index` function | 8% faster | unchanged at zero |

Unrelated numeric loops, fixed Lua calls, upvalue access, native calls, and
vararg calls remained within 1.5%. String-only concatenation and primitive
length also improved about 6%; mixed-number concatenation was neutral.
Allocation counts did not move.

The arm64 `runInstructions` frame remains 160 bytes while its code shrinks
from 5,952 to 5,664 bytes. The generic and constant-string table read and
write helpers each shrink by 96 bytes. This is a removal of repeated type
decoding, not a cache, alternative representation, or workload-specific
lane.

### 11. Deferred execution work

Feature-completion work resumes at this checkpoint. The next performance
sequence, when profiling work resumes, is:

1. recognize an exact positive numeric key once at the executor boundary and
   enter the existing integer table lane directly, matching PUC 5.1's
   `luaH_getnum` structure without inventing a constant-integer opcode;
2. evaluate PUC-style reusable concatenation scratch storage, with retained
   capacity and concurrent-State behavior explicitly bounded; and
3. measure capacity-proven fast admission for vararg Lua calls, native
   callbacks, and Lua tail calls separately. Fixed Lua calls are already at
   PUC parity and are not a redesign target.

Five alternating process pairs against the preceding revision improved the
same 11-cell pure-Lua matrix by 10.8% in direct A/B measurement. A separate
absolute run used five independent processes per engine and cell, with nine
timed samples per process, and placed the typed-predicate revision at 1.206
times PUC Lua 5.1.5:

| Workload | Badger / PUC |
| --- | ---: |
| Arithmetic and numeric `for` | 1.17x |
| Dynamic integer table read/write | 1.49x |
| Fixed Lua call | 0.87x |
| Ordinary recursion | 1.16x |
| Tail call | 1.58x |
| Eight-string concatenation | 1.85x |
| Constant field read | 1.04x |
| Constant field write | 1.56x |
| Global read | 0.98x |
| Global write | 0.89x |
| Method lookup and fixed call | 1.08x |

Fixed calls and both global cells now beat PUC in this matrix; field reads are
near parity. Concatenation, tail calls, field writes, and dynamic integer
tables remain the largest measured execution gaps and motivate the deferred
sequence above. The direct A/B is the causal tranche evidence; the absolute
PUC run is the standing destination rather than a claim that host timing
between separate days is perfectly stationary.

### 12. Host ownership checkpoints

The host-ownership checkpoint, measured before the semantic object ledger,
separates public handles from compact userdata, table, function, and thread
objects. Lua slots, tables, native captures, activations, coroutine transfers,
and the standard libraries retain the 48-byte userdata, 80-byte table,
32-byte function, or 136-byte thread of that checkpoint directly. A
State-local weak directory canonicalizes a public handle only when an object
actually crosses into Go; it adds no host-only field to every compact object.

On the same Apple M3 Pro, first publication through `State.NewUserData`
measures four allocations per object, compared with one allocation for the
former directly exposed object. The additional work is the host token and the
two Go weak registrations. Warm publication of an already exposed object
remains allocation-free at about 27 ns. A Lua-only file create, method,
read/write, and close sequence creates no host-directory entry, and a managed
resource held only by compact Lua storage remains live across forced Go
collections.

The table checkpoint makes that trade more explicit:

- the internal table-constructor benchmark remains three allocations and
  416 bytes, with its median moving only from about 204 ns to 205 ns;
- warm table publication is allocation-free at about 27 ns;
- `State.NewTable`, which necessarily publishes immediately, moves from one
  80-byte allocation at about 18.5 ns to four allocations and 121 bytes at
  about 712 ns;
- public raw integer and string update/read pairs remain allocation-free,
  measuring about 10.4 ns and 22.0 ns versus 9.2 ns and 20.8 ns before the
  split; and
- opening and exercising every standard library without returning a table to
  Go creates no table handle.

Functions now use the same boundary rule. `State.NewNativeFunction`, which
publishes immediately, moves from about 17.5 ns, 64 bytes, and one allocation
to about 0.8 microseconds, 106 bytes, and four allocations. Repeated publication
of an existing function is allocation-free at about 27 ns. Opening and
exercising every standard library creates no function handle: roughly 122
native library functions, dynamically created iterators, coroutine wrappers,
and internal protected calls stay compact. At that checkpoint a private Lua
function object remained 32 bytes; the 24-byte public token was paid only by
functions observed from Go. Native capture backing is copied to exact length
so unused Go slice capacity cannot hide untraced Lua ownership edges. Paired
internal-call measurements remained neutral within low-single-digit variation
and allocation-free.

Threads now complete the same boundary. The main executor, nested coroutine
resumes, `coroutine.create`, `running`, `resume`, `status`, and `wrap` all use
private thread objects and compact slots. Opening and exercising the Lua
coroutine library without returning a thread to Go creates no thread handle.
The public 24-byte token is paid only when `MainThread`, `NewThread`,
`Frame.Thread`, `Frame.LuaThread`, or a returned `Value` exposes a thread.
`State.NewThread`, which publishes immediately, moves from about 68 ns, 400
bytes, and two allocations to about 0.71 microseconds, 440 bytes, and five
allocations. Repeated publication is allocation-free at about 28 ns. Warm
public `ResumeInto` remains allocation-free and measures about 85 ns versus 83
ns before the split; the Lua `coroutine.resume`/yield loop remains neutral at
about 202 ns.

This is an explicit trade: boundary-created objects pay more so every Lua-only
table, function, or thread avoids a permanent handle-cache word and its
allocator size-class cost. Bulk Go-side graph construction needs the planned
low-level builder rather than thousands of friendly owning publications. If
representative embedding workloads show that first-publication cost dominates
despite that interface, change canonicalization as one coherent
representation decision rather than adding per-kind shortcuts.

### 13. Semantic-ledger checkpoint

State-local Lua collection uses four State-owned typed pointer vectors rather
than an intrusive peer link. Compact objects carry only their lightweight
runtime owner and one mark bit in existing padding. A retained table therefore
does not retain its State or unrelated ledger peers, and collection enumerates
contiguous pointer storage rather than chasing a linked list.

On 64-bit Go the compact object requests are 56 bytes for userdata, 80 for a
table, 40 for a Lua function, 72 for the combined native-function allocation,
and 136 for a thread. Their allocator classes are 64, 80, 48, 80, and 144
bytes. Each registered object also occupies one eight-byte vector entry.
Logical accounting therefore reports 64, 88, 48, 80, and 144 bytes before
subordinate storage. Compared with the rejected intrusive design, tables save
about eight physical bytes each while functions, userdata, and threads spend
about eight more on their side-vector entry because their object allocation
class does not shrink. This is an ownership and traversal design, not a claim
of a universal per-object memory win; constructor and representative workload
measurements remain gates.

A dedicated registration-and-sweep benchmark creates unexposed tables,
performs the mark/sweep phase every 1,024 creations, and keeps only the State
roots. On the standing Apple M3 Pro it measures about 24 ns, 98 allocated
bytes, and one allocation per table. Complete-cycle benchmarks separately
measure dead-table churn, a stable retained graph, and a mixed live/dead graph.
They include exact heap accounting, debt reset, weak processing, and the empty
finalizer drain instead of labeling mark/sweep alone as a complete pause. The
80-byte object plus geometrically grown ledger storage accounts for the
ordinary churn bytes without adding another per-object allocation.

Registration occasionally grows one typed vector but adds no table mutation
or executor-instruction barrier while collection remains synchronous. Sweep
compacts vectors stably, drops excessive capacity, and clears every discarded
pointer. Four typed mark stacks retain at most a bounded ordinary frontier;
larger spikes are released. Marking uses one resettable bit, so a failed
internal trace can cleanly retry instead of carrying a half-colored graph.
`State.Close` drops every object vector, work buffer, accounting map, and
long-string attribution entry.

Tests cover every object-edge kind, State and host roots, cyclic graphs of all
four kinds, collection inside a live execution frame, escaped upvalue closure,
two-State isolation, retained-table ownership isolation, tracing-panic retry,
post-close handles and scratch release, and zero-allocation warm collections.
The complete-cycle benchmark, executor constructor benchmarks that naturally
cross the debt threshold, and focused finalizer tests together cover the
weak-table, finalizer, accounting, and automatic-scheduling operation. The
lower-level churn benchmark remains explicit about measuring registration and
mark/sweep alone.

Deleted table/function/thread/userdata keys are converted during semantic
tracing to small holders containing Go weak pointers. This keeps the normal
40-byte record entry unchanged and preserves Lua's `next` continuation
identity without retaining the deleted object. The ordinary deletion path
still allocates nothing. First retirement may allocate both the explicit
holder and Go's private canonical weak-handle metadata; subsequent matching
and collection are allocation-free. Compaction or reinsertion releases the
holder. Deleted string keys remain content-bearing tombstones because strings
are value-equal and are not independent objects in the current semantic
ledger.

Weakness adds no field to a table and no write barrier to mutation. Each full
pass classifies `__mode` once for every reachable table, queues only the tables
that are actually weak, and reuses bounded State-owned queue storage. The mode
travels with the queue entry rather than becoming persistent table state.
Warm collection of a stable weak table is allocation-free. Clearing mutates
array and chained-record storage in place, preserving collision links and
deferring compaction to the same insertion seams used by ordinary deletion.

### 14. Debug-hook experiment — rejected

A cached flag check in ordinary opcode dispatch regressed representative
unhooked loops by 7–13% without adding allocations. An active-only executor
restored the 160-byte frame and 5,664-byte compiled body of `runInstructions`,
but required generated duplicate dispatch code. Its driver selection and
pending-event seams still regressed vararg call/return and indexed-metamethod
workloads by roughly 3%.

That trade does not meet the project rule. The generated executor, hook side
state, hook tests, and all ordinary call, return, coroutine, collection, and
driver seams were removed before landing. The debug library retains cold stack
inspection and mutation, while `debug.sethook` and `debug.gethook` remain a
documented compatibility gap.

Host timeout enforcement uses `CallContext`, `CallIntoContext`,
`ResumeContext`, or `ResumeIntoContext`. A future hook design must preserve one
canonical dispatch implementation and show no material regression across
dispatch, numeric, table, vararg, metamethod, Lua/native call, coroutine, and
context workloads before those two functions are added.

## Gates

Every tranche must pass the full semantic, race, checkptr, vet, and supported
cross-platform build matrix. Focused allocation counts accompany elapsed time;
a noisy timing win without movement at the mechanism's profile site is not
credited.

Directional tranche review uses at least five randomized fresh-process
samples. Replacement qualification uses at least fifteen after warmups,
pinned clean binaries and fixtures, randomized order, coefficient-of-variation
checks, and confidence intervals.

Two performance bars apply:

1. During a deep representation tranche, the immediately preceding revision
   is the causal comparator. Representative workloads have a 5% regression
   ceiling unless the review explicitly identifies a dependent follow-up in
   the same series.
2. Before the implementation is called replacement-ready, it must be
   statistically no slower than the frozen mature predecessor in every
   representative generic, embedding, graph-search, message-replay, and CBOR
   lane. CBOR load and save must also use no more allocated bytes or mallocs.

PUC Lua 5.1.5 remains the absolute interpreter target. The program first aims
to bring the generic geometric mean within 1.5 times PUC, then within 1.25
times, with no sustained Lua-heavy category above 1.5 times. LuaJIT with JIT
disabled is reported as an additional native-interpreter reference; JIT-on is
a compiled ceiling, not a pure-interpreter promise.
