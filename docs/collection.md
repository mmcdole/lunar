# Semantic collection

Lunik uses Go's garbage collector to reclaim backing allocations, but Lua
decides which Lua objects are reachable. The State-local collector implements
Lua 5.1 weak tables, userdata finalization, collection controls, automatic
scheduling, and logical heap accounting.

Collection is synchronous. A cycle runs at a graph-stable executor seam and
finishes before ordinary execution resumes.

## Ownership boundary

Registers, tables, upvalues, and activations point directly to private compact
runtime objects. Go code receives opaque owning values and handles instead of
those pointers.

When a table, function, thread, or userdata object crosses into Go, the State
creates or reuses a small host token. The token points to the canonical object;
it does not copy the object's mutable state. A weak State-local directory maps
objects to their live tokens without permanently rooting either side.

A live token is a Lua collection root. This is required because the collector
cannot inspect arbitrary Go closures, structs, or userdata payloads to find a
copied `Value`. Once Go can no longer reach a token, a later Go collection may
allow the State-local collector to retire that root. The delay is conservative:
it may keep an object alive longer, but it cannot finalize an object still
owned by Go.

Borrowed `Frame` access does not create a host root unless the callback asks
for an owning `Value` or typed object handle. Scalars and immutable strings do
not need host tokens.

Reference identity is the compact object's identity. Repeated publication can
return the same live token, but Lua semantics never depend on a temporary Go
wrapper address.

## Collected objects

The semantic ledger contains:

- tables;
- Lua and native functions;
- main and coroutine threads; and
- full userdata.

Upvalues belong to functions and threads. Immutable prototypes and strings are
traced and accounted as retained data rather than as independently finalized
objects.

Strings follow Lua 5.1's weak-table exception: weak tables do not clear string
keys or values. Numbers, booleans, and nil are also noncollectable values.

Each constructor registers its object once. Sweeping removes unreachable
objects from the State ledger, closes open upvalues owned by dead threads, and
releases dead execution stacks. Go then reclaims backing storage and cycles
that no live Go root retains.

`State.Close` releases State roots, native resources, collector work buffers,
and thread execution storage. Owning handles returned before close remain safe
for their documented read-only operations.

## Roots and graph traversal

A collection starts from:

- the main thread and currently active thread;
- the global environment, registry, and per-type metatables;
- State-held package and library objects;
- live failures or exit requests that carry Lua values;
- the argument of a currently executing finalizer; and
- every live host token.

Tables trace their metatable and strong keys and values. Functions trace their
environment, upvalues, and native captures. Threads trace their environment,
live stack extent, activations, and open upvalues. Userdata trace their
environment and metatable.

Go callback closures and userdata payloads are opaque. A Go value that needs
to retain a Lua object must use an owning Lunik value, which then appears in the
host root set. Hosts that create cycles through opaque Go payloads must break
those cycles themselves.

## Weak tables

A table's raw metatable field `__mode` controls weakness:

- `k` makes record keys weak;
- `v` makes array and record values weak; and
- both characters enable both forms.

The check is case-sensitive and stops at the first NUL byte. A non-string mode
or a mode containing neither character is strong. Lua 5.1 leaves changing
`__mode` after a table has used that metatable undefined.

Implicit array indexes are numbers, not stored objects. Weak keys therefore do
not weaken array values.

Lua 5.1 weak keys are not ephemerons. A weak-key table still marks its values.
If a value points back to its key, that path can keep the key alive. Lunik does
not apply Lua 5.2's later ephemeron rules.

Reachable weak tables are classified during marking. After the strong mark
frontier is drained, dead weak entries are cleared before the ordinary sweep.
Reference-key deletion retains only a non-owning continuation token so Lua's
`next` traversal rules do not accidentally keep the key alive through Go.
Deleted string keys retain content-based continuation information because
strings are never weak-cleared.

## Userdata finalization

Only full userdata have Lua 5.1 `__gc` behavior. A table with a `__gc` field is
not finalizable.

When a finalizable userdata becomes unreachable:

1. it is marked finalized so it cannot be scheduled twice;
2. the userdata and its reachable graph are retained for the finalizer pass;
3. weak entries are cleared using Lua 5.1's finalized-userdata ordering; and
4. finalizers run in reverse userdata creation order.

The handler is looked up immediately before its call. An earlier finalizer may
therefore replace or remove a later object's handler. Results are discarded.
Resurrection is allowed, but finalization still happens at most once.

An explicit collection returns a finalizer error and leaves later queued work
for another collection. Recursive explicit collection from `__gc` is valid
because callbacks run after the collector has returned to its idle phase.

`State.Close` attempts every remaining finalizer, including finalizers on
otherwise reachable userdata, and then releases native resources. Lua
finalizer failures do not abandon later finalizers or cleanup. A panic from a
native callback is re-raised only after teardown finishes.

Library-owned native resources use a separate exactly-once cleanup record.
Lua finalization and resource release may occur at different times when
resurrection is possible.

## Automatic scheduling

Each State records logical allocation debt at object creation and backing-store
growth. Writes within existing capacity, scalar execution, deletions, and
cache hits do not add debt.

After a completed cycle, the collector measures the live logical heap and
installs a growth budget. The Lua 5.1 pause control adjusts that budget. A
minimum batch prevents small heaps from collecting on every allocation.

Allocation only records debt. A due cycle runs later at a graph-stable seam,
including:

- entry to a rooted call or resume;
- completion of table construction, concatenation, or closure installation;
- publication of native or checked-call results;
- completion of a suspended metamethod or iterator continuation; and
- return from root execution to Go.

There is no collection branch on every instruction, loop backedge, or table
write. A long operation that reaches no safe seam defers collection until the
next one.

Automatic finalizers run synchronously on the thread that triggered the cycle.
Automatic reentry is suppressed while a finalizer batch is running; an
explicit nested collection remains allowed.

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

The current collector is synchronous, so one `step` completes one full cycle
and returns true. The amount is parsed for Lua 5.1 compatibility but does not
represent a partial phase. `setstepmul` stores and reports the compatibility
value; it has no incremental work rate to control until the runtime has a real
incremental collector.

Explicit collect and step still run while automatic collection is stopped.
A successful explicit collect or step resumes automatic scheduling, matching
Lua 5.1's threshold controls. Restart permits service at the next safe seam.
No control calls process-wide `runtime.GC` as a substitute for State-local Lua
reachability.

`State.Collect` performs an idle host collection. `Frame.Collect` performs the
same operation safely from a native callback. Either may run non-yielding Lua
finalizers.

## Heap accounting

`State.HeapBytes`, `Frame.HeapBytes`, `collectgarbage("count")`, and `gcinfo`
use one logical Lua heap definition. It includes registered objects currently
retained by the State and their subordinate storage, reachable prototype data,
captured upvalues, table capacity, thread stacks, and attributable runtime
strings. Callers that need a post-collection live-graph measurement should
collect first.

It excludes Go allocator size classes, unused collector work capacity, weak
pointer metadata, opaque userdata payloads, public host tokens, and fixed State
infrastructure. A prototype shared by several States is charged to each State
whose Lua graph requires it.

Logical heap bytes are not process RSS and are not the same as Go
`runtime.MemStats.HeapAlloc`. The CBOR benchmark uses separately labelled Go
heap measurements when comparing physical retained memory across runtimes.

## Current limit

The collector is not incremental. A cycle, including any Lua finalizers it
runs, is synchronous. Lunik does not yet expose a retained-heap quota.
