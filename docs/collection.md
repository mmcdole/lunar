# Semantic collection

Badger uses Go's collector to reclaim backing allocations, but Lua decides
which Lua objects are reachable. These are separate responsibilities.

The State-local collector will own Lua 5.1 weak-table behavior, userdata
finalization, memory accounting, and the `collectgarbage`, `gcinfo`, and
`newproxy` interfaces. Go finalizers remain limited to private native
resources and never execute Lua.

The ownership boundary, State-owned object ledger, centralized tracer,
logical accounting, close detachment, and internal synchronous sweep are
implemented. Weak clearing, Lua `__gc`, automatic or incremental policy, and
the public collection functions are not yet implemented or exposed.

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
Thread stores it beside its main-thread flag; userdata alone will carry
finalization state.

Current mark work uses four reusable State-owned typed slices. Oversized work
frontiers are discarded after a pass, and object vectors release excess slack
after sweeping. Weak-table work and pending finalizers will use State-owned
queues when implemented. Objects do not carry gray links, cached weak modes,
per-object reference counts, or host-handle cache fields. Host metadata is
paid only by objects that cross the Go boundary.

The ledger and centralized tracer now support internal synchronous full
collection at executor safe points. Every canonical constructor registers
exactly once. Sweep closes open upvalues belonging to dead threads, releases
their stacks, removes dead objects from the typed vectors, and leaves
host-rooted objects intact. `State.Close` releases every object-vector and
collector-scratch backing allocation while preserving documented post-close
observations of owning handles. Thread execution backing is deliberately
released.

The internal collector remains unexposed while weak clearing and Lua
finalization are incomplete. Incremental barriers are added only with the
incremental collector; the synchronous collector does not burden every table
write with an unfinished tri-color protocol.

Logical accounting counts one pointer-sized ledger entry per registered object
plus retained subordinate backing capacities, including deduplicated
upvalues. It deliberately excludes unused ledger-vector capacity, collector
scratch, opaque userdata payloads, public host tokens, immutable Prototypes,
strings, and Go allocator size-class rounding. The public Lua count surface
will define and test its final accounting boundary before exposure.

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
metatable. Pending and currently executing finalizers become additional roots
when finalization lands.

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

The following sections specify observable collection phases that remain to be
implemented.

## Weak tables

The raw string value of a metatable's `__mode` field controls weakness:

- `k` makes record keys weak;
- `v` makes array and record values weak; and
- both characters make both sides weak.

Implicit integer array keys are not objects, so weak keys do not weaken array
values. The metatable itself is always strong. A non-string mode or a mode
without either character is strong. Lua 5.1 leaves changing `__mode` after a
table has used that metatable undefined.

Lua 5.1 weak keys are not ephemerons. A weak-key table still marks every
value unconditionally. If a value points back to its weak key, that reference
can keep the key alive. Badger must not import Lua 5.2's later ephemeron
algorithm into the 5.1 runtime.

Weak entries are cleared after marking and before finalizers run. Clearing a
record entry removes both key and value as semantic edges. When traversal
continuation requires the old key identity, the store retains only a
non-owning dead-key token; an ordinary `slot` tombstone would keep the object
alive through Go's collector. Array entries clear to nil.

Strings and scalar values are never weak-cleared. Unreachable reference
values are.

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
queue for a later collection. `State.Close` instead attempts every eligible
finalizer, including those on reachable userdata, ignores Lua finalizer
errors, and only then tears down Lua roots and native resources.

Runtime-owned userdata keep their private native-resource token distinct from
Lua finalization. The resource is released exactly once when the userdata is
truly dead after finalization and possible resurrection, or during
deterministic State shutdown.

## Collection controls

Once weak clearing and finalization are complete, the base library will
expose the Lua 5.1 operations:

- `collectgarbage("stop")`
- `collectgarbage("restart")`
- `collectgarbage("collect")`
- `collectgarbage("count")`
- `collectgarbage("step", amount)`
- `collectgarbage("setpause", value)`
- `collectgarbage("setstepmul", value)`
- `gcinfo()`

The default operation will be `collect`. Stop, restart, and collect will return
numeric zero. Count will return State-local accounted bytes in KiB, including
the fractional part; `gcinfo` will return the integer KiB value. Step will
report whether it completed a cycle. Pause and step multiplier will default to
200 and their setters will return the previous value.

Explicit collect and step will continue to work while automatic collection is
stopped. No control will invoke process-wide `runtime.GC` as a substitute for
State-local work.

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
3. Add weak-table classification and clearing, including non-owning dead keys.
4. Add userdata separation, finalizer execution, resurrection, errors, and
   close-time draining.
5. Expose synchronous collection and count controls after the weak and
   finalizer rules they can observe are complete.
6. Add automatic debt policy and incremental step behavior. Add write
   barriers only when the incremental state machine exists.
7. Add `newproxy` and complete the base-library surface.

The current ledger suite covers registration, every object-edge kind, State
and host roots, cycles, execution safe points, escaped upvalues, State
isolation, close detachment, logical accounting, and warm collection. The
completed qualification suite will compare the remaining semantics with PUC
Lua 5.1.5 and cover the `k`/`v`/`kv` matrix, strings in weak tables, Lua 5.1's
value-to-weak-key cycle, traversal after collector deletion, reverse
finalization order, resurrection, dynamic handler replacement, finalizer
errors, close-time draining, two-State isolation, and every collection-control
return type.
