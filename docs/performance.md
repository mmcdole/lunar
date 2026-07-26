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
| Load, frozen Badger predecessor | 1.511 s | 80.6 MB | 0.504 M |
| Load, stock GopherLua | 1.876 s | 784.6 MB | 12.734 M |
| Save, Badger Lua | 1.056 s | 253.9 MB | 5.412 M |
| Save, frozen Badger predecessor | 0.854 s | 234.9 MB | 3.080 M |
| Save, stock GopherLua | 1.226 s | 502.9 MB | 9.352 M |

Badger therefore already removes most of stock GopherLua's allocation volume
and is modestly faster. That is not the replacement bar. Load is about 15%
slower than the mature predecessor while allocating 2.16 times as many bytes
in 2.08 times as many allocations. Save is about 24% slower, allocates 8% more
bytes, and performs 76% more allocations. Those are active defects to close,
not acceptable costs of the new interface.

A separate native runner measured PUC Lua 5.1.5 on the same host and corpus.
Its five-sample median load wall time was 0.803 seconds, with 0.757 seconds of
Lua operation CPU and a 60.7 MB decoded-graph heap delta. Save operation CPU
was 0.701 seconds; its parent wall time also includes constructing the source
graph and is not comparable to the Go workers' save-only interval. These
native numbers are directional until the full pinned protocol is run, but
they show that matching the predecessor is an intermediate gate rather than
the final objective.

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

The store header is 40 bytes, down from 48, and the canonical Table header is
104 bytes, down from 112. Each record node remains 40 bytes, the same size as
PUC's `Node` and the previous Badger entry; its former 64-bit hash word now
contains a 32-bit cached hash and a 32-bit successor index. The named capacity
bound preserves index-plus-one encoding on 32- and 64-bit builds.

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
occupies tail padding, so the canonical Table header remains 104 bytes.

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
The frozen predecessor's corresponding profile is 51.15 MiB. Closing that
live-memory gap therefore remains table-layout work rather than a reason to
add broader string retention.

A general 64-byte stack scratch path for multi-byte `string.char` results was
also tested. Go's escape analysis placed the scratch on the heap across the
result-publication seam, leaving one allocation and increasing its minimum
size. It was rejected. Multi-byte results retain one exact owned allocation
until a cache-before-allocation builder can prove stack ownership without
compiler directives or unsafe lifetime claims.

### 5. Re-profile execution

Only after storage churn falls do CPU profiles decide the next executor work.
Likely candidates are direct constant-string and integer table access, native
library call state, and remaining call/return bookkeeping. Builtins, inline
caches, or opcode specialization require a generic profile and shared semantic
kernel. They are not used to conceal an allocation design problem.

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
