# Semantic collection

Badger uses Go's collector to reclaim backing allocations, but Lua decides
which Lua objects are reachable. These are separate responsibilities.

The State-local collector owns Lua 5.1 weak-table behavior, userdata
finalization, memory accounting, and the `collectgarbage` and `gcinfo`
interfaces. It will also support `newproxy`. Go finalizers remain limited to
private native resources and never execute Lua.

The ownership boundary, State-owned object ledger, centralized tracer,
logical accounting, close detachment, synchronous sweep, Lua 5.1 weak-table
classification and clearing, userdata `__gc`, explicit Lua controls, and host
collection and measurement methods are implemented. Retained-allocation debt
now schedules automatic full cycles at graph-stable executor safe points.
Incremental collection is not yet implemented.

## Ownership boundary

Execution slots point directly at compact runtime objects. Table access,
function calls, and the interpreter must not pass through a public wrapper or
root table.

An object crossing into ordinary Go code needs separately tracked host
ownership. A State-local mark pass cannot discover a copied `Value`, a Go
closure capture, or a userdata payload stored in an arbitrary Go heap object.
The former direct public object pointers could not remain owning references
once semantic collection was enabled.

The implemented boundary uses two lifetimes:

- low-level callback state is borrowed for the documented `Frame` lifetime;
- friendly reference values are opaque owning handles, materialized only when
  a reference leaves the runtime.

Copying an owning `Value` or opaque handle pointer remains cheap. Go
reachability keeps its small root token alive. A State-local directory weakly
indexes compact objects to their
live host tokens; both the key and value are weak, so the directory pins
neither side. The token points at the one compact object, so this is ownership
metadata rather than a second table, function, thread, or userdata
representation. Scalars and strings remain direct values.

For migrated reference kinds, a public `Value` points at the host token rather
than the compact object. The current implementation covers `*UserData`,
`*Table`, `*Function`, and `*Thread`; the ownership boundary was completed
before the object ledger was enabled.
Each public handle is an opaque named view of the common token representation,
and its methods unwrap the compact object at the boundary. The weak directory
returns the same live Go pointer on repeated publication without repeated
allocation or per-object handle fields. Internal `slot` values continue to
point directly at the compact object. Conversion in either direction is one
boundary operation, never an interpreter-loop operation.

Current `Frame` scalar reads inspect compact borrowed slots. Methods that
return `Value`, `Table`, `Function`, `Thread`, or `UserData` promote the
reference to an owning token. Friendly calls likewise return owning values.
Ordinary consumers do not need a manual release operation for safety.
Borrowed reference views, cursors, builders, and explicit retention remain
planned low-level API work.

This design requires `weak.Pointer` and raises the minimum Go version to 1.24.
Host tokens contain pointers and are large enough to avoid the runtime's
tiny, pointer-free allocation batching exception. A permanent root for every
object ever exposed to Go is not an acceptable fallback: it would make weak
tables and long-running embedding memory depend on boundary history.

The collector treats every non-nil weak token as a root. Go may report an
unreachable token as live until a later Go collection, which can delay Lua
reclamation but cannot finalize an object still held by the host. State-local
collection never forces a process-wide Go collection merely to retire that
conservative root. Collection and bounded publication-time maintenance remove
directory entries whose object or token has disappeared.

Reference identity is the compact object's identity, not the address of a
temporary Go view. `SameObject` compares that identity. No public operation
may expose an untracked pointer to the compact object.

A thread is also an executable capability. Its compact object retains the
owning `State` needed for resume, limits, context, and active-executor
coordination. Consequently a retained `*Thread` keeps that State shell alive,
as the receiver-based API requires. It does not keep a public wrapper in the
executor. `Close` clears State roots and resources and leaves retained thread
handles read-only; the object ledger makes every suspended thread enumerable
for close-time stack release.

## Collected objects

The semantic ledger contains tables, functions, threads, and userdata.
Upvalues are subordinate to functions and threads. Immutable Prototypes and
strings remain Go-managed metadata or scalar storage rather than independent
ledger objects.

Strings require one Lua 5.1 exception: weak tables never clear string keys or
values. Numbers, booleans, and nil are likewise noncollectable for weak-table
purposes.

Each collected object carries only its lightweight runtime owner in the
common header. Four State-owned typed pointer vectors make both kind and
membership implicit without adding a peer link to every object. This matters
for ownership: retaining an ordinary table must not retain its State or older
ledger peers. One transient mark bit lives in padding in each concrete object.
Thread stores it beside its main-thread flag; userdata carries one persistent
finalized bit in existing alignment padding.

Current mark work uses four reusable State-owned typed slices. Oversized work
frontiers are discarded after a pass, and object vectors release excess slack
after sweeping. Weak tables use a State-owned queue of table/mode pairs that
is cleared after every pass and discarded after an oversized frontier.
Pending finalizers use a persistent State-owned FIFO with a head cursor;
consumed entries are cleared, partial work survives Lua errors, and oversized
backing is discarded after the queue drains. Objects do not carry gray links,
cached weak modes, per-object reference counts, queue links, or host-handle
cache fields. Host metadata is paid only by objects that cross the Go
boundary.

The ledger and centralized tracer now support internal synchronous full
collection at executor safe points. Every canonical constructor registers
exactly once. Sweep closes open upvalues belonging to dead threads, releases
their stacks, removes dead objects from the typed vectors, and leaves
host-rooted objects intact. `State.Close` releases every object-vector and
collector-scratch backing allocation while preserving documented post-close
observations of owning handles. Thread execution backing is deliberately
released.

The collector is exposed only at safe entry points. Incremental barriers are
added only with the incremental collector; the synchronous collector does not
burden every table write with an unfinished tri-color protocol.

Logical accounting counts one pointer-sized used ledger entry per registered
object plus retained subordinate backing capacities, including deduplicated
upvalues, installed dead-reference-key holders, unique retained string-backing
views, string-cache shards, and each reachable immutable Prototype's code,
constants, children, and debug metadata. State-neutral strings retained only
by Go are not charged; a string becomes attributable when a State table,
stack, capture, upvalue, Prototype, or runtime cache retains it. A Prototype
shared by multiple States is charged once to each retaining State, matching
the logical memory each State requires even when the physical allocation is
shared.

The boundary deliberately excludes unused ledger-vector capacity, collector
scratch and pending-queue storage, Go's private weak-pointer metadata, opaque
userdata payloads, public host tokens, State infrastructure, and Go allocator
size-class rounding.

`State.HeapBytes`, `Frame.HeapBytes`, `collectgarbage("count")`, and `gcinfo`
all use this one target-architecture logical boundary. It is a Lua heap
measure, not process RSS or physical Go allocator usage. Measurement currently
scans the registered heap and retained metadata. Automatic scheduling instead
maintains allocation debt at object-creation and capacity-growth seams, so it
does not put a heap scan on ordinary allocation or mutation paths.

## Roots and graph traversal

A collection starts from:

- the main thread, active thread, registry, and per-type metatables;
- execution failures and pending exits that carry Lua values;
- the package sentinel and other State-held objects; and
- each live host-ownership token.

Tables trace their metatable and the strong portions of their array and
record storage. Functions trace their environment and Lua upvalues or native
captures. Reachable threads trace their globals, live register extent,
activations, and open upvalues. Userdata trace their environment and
metatable. Existing pending finalizers are intentionally not initial roots:
Lua 5.1 first separates newly dead userdata, then marks the complete pending
queue and drains its graph. This allows userdata reachable only through old
pending work to become eligible in the current cycle. A currently executing
finalizer is rooted by its compact call argument on the active thread.

Go callback closures and userdata payloads are opaque. If they retain Lua
objects, they do so through ordinary owning handles, which appear in the host
root set. Ownership back-pointers, State pointers, native-resource records,
and unused slice capacity are not Lua graph edges.

An owning handle hidden inside opaque Go data is necessarily a host root.
The collector cannot inspect a Go closure or arbitrary payload to discover
that the handle is reachable only through an otherwise dead Lua object.
Native functions should use explicit compact captures when the captured value
belongs to the Lua graph. Hosts that construct cycles through opaque payloads
must break those cycles; silently tracing arbitrary Go memory is not a
supported ownership model.

Sweeping removes unreachable objects from the State ledger. Go then reclaims
their backing allocations and any unreachable cycles. Sweep does not
destructively blank an object that a live host handle can observe, because
such an object is a root.

The following sections define the observable weak-table and finalization
phases implemented by the collector.

## Weak tables

The raw string value of a metatable's `__mode` field controls weakness:

- `k` makes record keys weak;
- `v` makes array and record values weak; and
- both characters make both sides weak.

Implicit integer array keys are not objects, so weak keys do not weaken array
values. The metatable itself is always strong. A non-string mode or a mode
without either character is strong. Lookup is raw and respects the ordinary
metamethod-absence cache; inherited or coerced values do not count. Matching
is case-sensitive and, like Lua 5.1's C-string scan, stops at the first
embedded NUL. Lua 5.1 leaves changing `__mode` after a table has used that
metatable undefined.

Lua 5.1 weak keys are not ephemerons. A weak-key table still marks every
value unconditionally. If a value points back to its weak key, that reference
can keep the key alive. Badger must not import Lua 5.2's later ephemeron
algorithm into the 5.1 runtime.

Reachable weak tables are classified while marking. Array values are skipped
only for `v`; record keys and values are skipped according to their respective
mode bits. Once the mark frontier is fully drained, weak entries are cleared
before sweeping. As in PUC Lua 5.1, record clearing checks both sides: the
configured strong side was already marked, while finalization will add the
special finalized-userdata rule. Clearing removes both key and value as
semantic edges and updates array occupancy and sparse-integer metadata.

When traversal continuation requires the old key identity, the record store
retains only a non-owning dead-key token; an ordinary `slot` tombstone would
keep the object alive through Go's collector. The token contains Go's
supported weak pointer, not a dangling `unsafe.Pointer` or integerized
address. It is installed during collection so ordinary deletion remains
allocation-free, and revival restores the canonical strong key before any
table rehash. Array entries clear to nil.

Strings and scalar values are never weak-cleared. Unreachable reference
values are. Deleted reference keys already use the non-owning continuation
form. Deleted string keys remain ordinary content-bearing tombstones until
insertion or rehash reclaims them; this preserves content-based string
continuation semantics without adding string objects to the semantic ledger.

## Userdata finalization

Only full userdata have Lua 5.1 `__gc` behavior. A table carrying `__gc` is
not finalizable.

When unreachable finalizable userdata are separated:

1. each is marked finalized before any callback;
2. the userdata and its reachable graph are retained for the finalizer pass;
3. weak entries are cleared with Lua 5.1's finalized-userdata ordering; and
4. finalizers run at a safe point in reverse userdata creation order.

The handler is looked up again immediately before its call. An earlier
finalizer may therefore replace or remove a later object's handler. Results
are discarded. Resurrection is allowed, but a userdata is finalized at most
once.

An explicit collection propagates a finalizer error and leaves the remaining
queue for a later collection. Recursive explicit collection from `__gc` is
legal because callbacks run only after the collector returns to its idle
phase. The current userdata has already left the queue and is an argument
root; later pending work remains ordered ahead of anything newly separated.

`State.Close` instead separates every still-unfinalized userdata once,
including reachable userdata, attempts every queued handler, ignores Lua
finalizer errors, and only then tears down Lua roots and native resources.
Userdata created by a close-time handler are not added by that Close. A native
callback panic does not abandon later finalizers or native cleanup: Close
remembers the first panic, completes teardown, and then re-panics.

Runtime-owned userdata keep their private native-resource token distinct from
Lua finalization. The resource is released exactly once when the userdata is
truly dead after finalization and possible resurrection, or during
deterministic State shutdown. A collection nested inside close still uses the
shutdown release policy. Close-time cleanup errors are returned to the host
without becoming Lua `__gc` errors.

## Automatic scheduling

Each State records logical bytes newly retained since its last completed
cycle. Canonical object creation, table-storage growth, thread stack and frame
growth, continuation storage, upvalue cells, runtime-owned strings and string
cache shards, and loading an immutable Prototype tree charge this
debt. Replacements that retain no newly imported string backing, deletions,
writes within existing capacity, table compaction without capacity growth,
cache hits, and scalar execution do not.
Constructing an uncached long public string does not by itself make that
backing part of a State. Its first owning-API import enters a State-local
attribution set. Short strings constructed through `State.String` instead
cross the runtime-local bounded cache immediately. The completed-cycle heap
scan sweeps long-string attribution entries no longer present in the Lua graph,
so a still-live string is not recharged after every cycle and dead backing is
not permanently rooted. Runtime-created long
strings charge allocation debt directly and stay out of the set while they
remain compact. Exporting one to an owning Go value is read-only and adds no
scheduling metadata. A later public-to-compact import is a conservative
ownership admission: it may charge once even when that State originally
created the backing, then records attribution so repeated imports while the
string remains live are free. Cross-State imports charge each retaining State
independently. Debt is a conservative scheduling signal, not heap size; exact
heap reporting and `collectgarbage("count")` still deduplicate the backing
actually retained by each Lua graph. Conservative admission may request an
earlier cycle, but service remains deferred to a rooted executor safe point.
The attribution set records its high-water occupancy and rebuilds only after
a substantial drop below one quarter of a nontrivial peak. This releases Go
map buckets after bursty long-string churn without allocating during stable
collection cycles.
Prototype debt uses an allocation-free weight cached when the immutable tree
is sealed; repeated loading may conservatively charge shared metadata again
rather than maintaining a permanent attribution map on the load path.

After a cycle, the collector measures the surviving logical heap once, clears
old debt, and installs a growth budget. With the default pause of 200 the
budget is approximately one additional live heap, subject to a 256 KiB
batching floor. A pause above 100 scales that growth allowance by
`(pause-100)/100`; values at or below 100 use the floor. Saturating arithmetic
prevents control values or very large heaps from wrapping the schedule.

Allocation never enters Lua or starts tracing. A due cycle runs only at a
graph-stable executor seam:

- when root execution begins after its callable and arguments are rooted;
- after table construction, concatenation, or closure installation;
- after a native or checked Lua call has published its results;
- after a suspended metamethod or iterator continuation has completed; and
- before root execution returns to its host.

There is no collection branch on every instruction or loop backedge, and
the compact instruction loop does not poll at every nested fixed call or
return. Table mutation never invokes Lua synchronously. A long operation that
reaches none of these seams defers collection until the next one, matching Lua
5.1's allocation-accounting-versus-safe-check separation.

Automatic finalizers run synchronously on the Thread that triggered the
cycle, using the existing executor and compact call convention. Their
arguments and the interrupted operation's results are already rooted.
Automatic re-entry is suppressed while any finalizer batch runs; a finalizer
may still request an explicit nested collection. Lua 5.1 restores the outer
threshold around each successful `__gc`, so successful stop/restart requests
made inside a finalizer are discarded. The enclosing completed cycle then
installs its next schedule from the current pause and latest completed-cycle
baseline, retaining allocations made by the handler. A failing finalizer
retains its control state and leaves later queued work for another cycle.

## Collection controls

The base library exposes the Lua 5.1 operations:

- `collectgarbage("stop")`
- `collectgarbage("restart")`
- `collectgarbage("collect")`
- `collectgarbage("count")`
- `collectgarbage("step", amount)`
- `collectgarbage("setpause", value)`
- `collectgarbage("setstepmul", value)`
- `gcinfo()`

The default operation is `collect`. Stop, restart, and collect return numeric
zero. Count returns State-local accounted bytes in KiB, including the
fractional part; `gcinfo` returns the integer KiB value. Pause and step
multiplier default to 200 and their setters return the previous value,
including zero and negative values. The second argument is parsed for every
operation as in Lua 5.1, even when that operation does not otherwise use it.
Option comparison and diagnostics stop at an embedded NUL.

The current collector is synchronous. One `step` therefore completes one
whole cycle and returns true; the amount is parsed but does not manufacture a
fake partial phase. A later genuinely incremental collector may return false
when a step does not complete its cycle.

`setstepmul` stores and reports Lua 5.1's policy value, but a synchronous full
cycle has no honest incremental work rate for it to control. It becomes
operational only with a real incremental phase machine and write barriers.
`setpause` affects the budget installed by the next completed cycle.

Explicit collect and step continue to work while automatic collection is
stopped and, as in PUC's threshold-based implementation, either resumes
automatic scheduling afterward. Stop continues recording debt but suppresses
service; restart makes the next executor safe point service the due work. No
control invokes process-wide `runtime.GC` as a substitute for State-local
work.

`State.Collect` provides the idle high-level host operation.
`Frame.Collect` performs the same work safely from a live native callback.
Both may execute arbitrary non-yielding Lua finalizers, resume automatic
collection after success, and publish an arbitrary finalizer error before
returning it to Go, so the error's Lua reference remains an owning value.
Neither API exposes sweep counters or a second collector command model.

`newproxy` follows after weak tables and userdata finalization. Its private
validity table is weak in both directions, and a true argument creates a
fresh registered metatable while a valid proxy shares its exact metatable.

## Delivery order

1. **Complete.** Replace direct public reference pointers with owning host
   tokens and retain borrowed `Frame` access. Prove reference identity,
   cross-State rejection, post-close observation, and zero-allocation borrowed
   access.
2. **Complete.** Add the State-owned typed object vectors, constructor registration,
   logical accounting, centralized tracing, and an internal synchronous sweep.
   Root/edge, cycle, safe-point, upvalue, close-lifetime, and warm-allocation
   tests qualify the foundation.
3. **Complete.** Add non-owning deleted reference keys, raw `__mode`
   classification, Lua 5.1 strong-side marking, post-mark clearing, and
   traversal-safe tombstones.
4. **Complete.** Add userdata separation, finalizer execution, resurrection,
   errors, and close-time draining.
5. **Complete.** Expose synchronous collection and count controls after the
   weak and finalizer rules they can observe are complete.
6. **Complete.** Add retained-allocation debt and executor safe-point service.
   Stop, restart, and pause govern automatic full cycles without adding a
   mutation barrier or per-instruction collector check. Step multiplier,
   incremental step behavior, and write barriers belong together and follow
   only if latency measurements justify the additional state machine.
7. Add `newproxy` and complete the base-library surface.

The current ledger suite covers registration, every object-edge kind, State
and host roots, cycles, execution safe points, escaped upvalues, State
isolation, close detachment, logical accounting, warm collection, the
`k`/`v`/`kv` matrix, strings and scalars in weak tables, Lua 5.1's
value-to-weak-key cycles, every reference kind, sparse integer metadata,
collision chains, host roots, traversal after collector deletion, retry and
poison failure phases, bounded scratch, reverse finalization order, raw and
dynamic handler lookup, callable handlers, at-most-once errors, arbitrary
error values, resurrection, pending-graph separation, nested collection,
finalized-userdata weak ordering, close-time draining, close-time resource
policy, panic cleanup, bounded finalizer queues, the complete explicit Lua
control surface, argument coercion and validation, return types, exact logical
count reporting, State isolation, arbitrary finalizer errors, queue
resumption, and the high- and low-level host collection methods.

Automatic coverage additionally pins debt saturation and reset, capacity and
string-cache charging, Prototype attribution, rooted results, host mutation
non-reentrancy, native and Lua finalizer re-entry, triggering-coroutine
identity, protected arbitrary errors, stop/restart scheduling, successful
finalizer threshold restoration, nested-cycle baselines, and close-time
automatic suppression.
