# Semantic collection

Badger uses Go's collector to reclaim backing allocations, but Lua decides
which Lua objects are reachable. These are separate responsibilities.

The State-local collector owns Lua 5.1 weak-table behavior, userdata
finalization, memory accounting, and the `collectgarbage`, `gcinfo`, and
`newproxy` interfaces. Go finalizers remain limited to private native
resources and never execute Lua.

## Ownership boundary

Execution slots point directly at compact runtime objects. Table access,
function calls, and the interpreter must not pass through a public wrapper or
root table.

An object crossing into ordinary Go code needs separately tracked host
ownership. A State-local mark pass cannot discover a copied `Value`, a Go
closure capture, or a userdata payload stored in an arbitrary Go heap object.
The current direct object pointers therefore cannot remain owning public
references once semantic collection is enabled.

The boundary will use two lifetimes:

- low-level values and object views are borrowed for a documented Frame,
  cursor, or operation lifetime;
- friendly reference values are opaque owning handles, materialized only when
  a reference leaves the runtime.

Copying an owning handle remains cheap. Go reachability keeps its small root
token alive. A State-local directory weakly indexes compact objects to their
live host tokens; both the key and value are weak, so the directory pins
neither side. The token points at the one compact object, so this is ownership
metadata rather than a second table, function, thread, or userdata
representation. Scalars and strings remain direct values.

For migrated reference kinds, a public `Value` points at the host token rather
than the compact object. The current implementation covers `*UserData`,
`*Table`, and `*Function`; threads follow before the object ledger is enabled.
Each public handle is an opaque named view of the common token representation,
and its methods unwrap the compact object at the boundary. The weak directory
returns the same live Go pointer on repeated publication without repeated
allocation or per-object handle fields. Internal `slot` values continue to
point directly at the compact object. Conversion in either direction is one
boundary operation, never an interpreter-loop operation.

The low-level API promotes a borrow only when the caller explicitly retains
it. Friendly calls return owning values. Ordinary consumers do not need a
manual release operation for safety. The runtime may later offer scoped or
explicit release for deterministic host-root retirement, but correctness
must not depend on it.

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

## Collected objects

The semantic ledger contains tables, functions, threads, and userdata.
Upvalues are subordinate to functions and threads. Immutable Prototypes and
strings remain Go-managed metadata or scalar storage rather than independent
ledger objects.

Strings require one Lua 5.1 exception: weak tables never clear string keys or
values. Numbers, booleans, and nil are likewise noncollectable for weak-table
purposes.

Each collected object has a common intrusive header containing:

- its owning runtime;
- one ledger link;
- its object kind and mark epoch; and
- userdata finalization state where applicable.

Mark work, weak-table work, and pending finalizers use reusable State-owned
slices. Objects do not carry gray links, cached weak modes, per-object
reference counts, or host-handle cache fields. Host metadata is paid only by
objects that cross the Go boundary.

The ledger initially supports synchronous full collection at executor safe
points. Incremental barriers are added only with the incremental collector;
the first correct collector does not burden every table write with an
unfinished tri-color protocol.

## Roots and graph traversal

A collection starts from:

- the main thread, registry, active thread, and per-type metatables;
- every thread's globals, live register extent, activations, and open
  upvalues;
- pending and currently executing finalizers;
- internal sentinels that are real Lua objects; and
- each live host-ownership token.

Tables trace their metatable and the strong portions of their array and
record storage. Functions trace their environment and Lua upvalues or native
captures. Threads trace compact values and active functions. Userdata trace
their environment and metatable.

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

Sweeping unlinks unreachable objects from the State ledger. Go then reclaims
their backing allocations and any unreachable cycles. Sweep does not
destructively blank an object that a live host handle can observe, because
such an object is a root.

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
fractional part; `gcinfo` returns the integer KiB value. Step returns whether
it completed a cycle. Pause and step multiplier default to 200 and their
setters return the previous value.

Explicit collect and step continue to work while automatic collection is
stopped. No control invokes process-wide `runtime.GC` as a substitute for
State-local work.

`newproxy` follows after weak tables and userdata finalization. Its private
validity table is weak in both directions, and a true argument creates a
fresh registered metatable while a valid proxy shares its exact metatable.

## Delivery order

1. Replace direct public reference pointers with owning host tokens and
   borrowed low-level views. Prove reference identity, cross-State rejection,
   post-close observation, and zero-allocation borrowed access.
2. Add the intrusive object ledger, constructor registration, accounting, and
   a centralized tracer. Collection remains disabled until every root and edge
   is covered.
3. Add synchronous mark and sweep with explicit collection and count
   controls.
4. Add weak-table classification and clearing, including non-owning dead keys.
5. Add userdata separation, finalizer execution, resurrection, errors, and
   close-time draining.
6. Add automatic debt policy and incremental step behavior. Add write
   barriers only when the incremental state machine exists.
7. Add `newproxy` and complete the base-library surface.

The qualification suite compares these semantics with PUC Lua 5.1.5. It
covers the `k`/`v`/`kv` matrix, strings in weak tables, Lua 5.1's
value-to-weak-key cycle, traversal after collector deletion, reverse
finalization order, resurrection, dynamic handler replacement, finalizer
errors, close-time draining, two-State isolation, and every collection-control
return type.
