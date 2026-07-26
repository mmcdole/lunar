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
- PUC's chained scatter table remains useful at full node occupancy. Badger's
  linear-probe store grows before 75% occupancy. For 1, 2, 4, 8, and 16 live
  record fields, PUC needs 1, 2, 4, 8, and 16 nodes; Badger commonly reserves
  4, 4, 8, 16, and 32.
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

### 3. Table storage and resizing

Replace the current 75%-occupied linear-probe policy with one coherent
PUC-informed store. The design work must cover hash placement, collision
chains, free-node discovery, deletion continuations, and rehash as one
mechanism; a second table backend or a CBOR-only lane is not acceptable.
PUC's one-node initial allocation is not copied blindly: a small initial Go
allocation can avoid several non-in-place grows. Initial capacity and maximum
occupancy are measured as separate decisions.

Rehash will consider the complete positive-integer population and select the
dense array and hash capacities together. It must preserve sparse-key safety,
Lua 5.1 `next` behavior after deletion, legal undefined behavior after
insertion during traversal, metamethod invalidation, and allocation hints from
source constructors. Dense growth separately tests the current four-slot
start followed by a direct jump to 16 slots on spill, avoiding the common
4-to-8-to-16 allocation sequence without restoring a large unconditional
default.

The 112-byte canonical Table header is reviewed against PUC's 64-byte Table,
but a field is removed or narrowed only when its ownership and range
invariants make that safe.

The comparison covers:

- empty and one-field tables;
- unique and recurring 2-, 4-, 8-, and 16-field records;
- sequential and out-of-order dense growth;
- sparse and mixed numeric/string keys;
- lookup hits and misses at each occupancy;
- deletion, churn, and `next`/`pairs`; and
- the generic graph-search, message-replay, and CBOR lanes.

The change lands only if profiles show less backing growth, no correctness
regression, and no representative table workload is materially slower. A
shared record layout remains a later option only if the PUC-style store leaves
a measured recurring-record gap; the predecessor's shape system is not a
starting dependency.

### 4. Re-profile execution

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
