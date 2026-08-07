# Architecture

Lunar is a Lua 5.1 compiler and virtual machine implemented in Go. Runtime
objects stay in private compact representations; the public Go API adds
ownership only when a value crosses the embedding boundary.

## Runtime model

A `State` owns:

- one registry;
- one main Lua thread and any coroutines created from it, each with a global
  environment pointer;
- runtime metatables, typed-userdata registrations, libraries, string caches,
  and native resources;
- the Lua object ledger used for semantic collection; and
- one active executor.

Only one goroutine may execute or mutate a State at a time. Separate States
are independent and may run concurrently.

The runtime follows these ownership rules:

1. Each table, function, thread, and userdata value has one canonical runtime
   object.
2. Registers, tables, arguments, results, and upvalues store private compact
   values directly.
3. A reference object published to Go receives an opaque ownership token. The
   token does not duplicate the object.
4. Reference values belong to one State and cannot be imported by another.
   Booleans, numbers, nil, and immutable strings are State-neutral.
5. A borrowed `Frame` is valid only during its native callback. An owning
   `Value` or typed object handle may be retained.
6. Closing a State stops execution and mutation. Previously returned owning
   values remain safe to inspect.

## Values and objects

`Value` is the owning public value type. Its zero value is invalid; Lua nil is
created with `Nil()`. Scalar observers use exact type checks. Reference values
can be converted to typed `*Table`, `*Function`, `*Thread`, and `*UserData`
handles.

`UserDataType[T]` adds a State-local Lua class identity to an opaque userdata
payload. Its private registration roots one canonical metatable and records
the declared Go type. Extraction requires both that metatable identity and a
successful assertion to `T`; Lua's visible registry is not the source of
truth.

The private execution value is a separate unexported type. It stores the same
Lua value without exposing mutable runtime objects or paying host-ownership
cost inside the VM. Publishing a reference creates or reuses the State's weakly
indexed host token. Lua's collector treats a live token as a root.

Raw equality belongs to the State because reference values must be checked
against the receiving runtime. `Value.SameObject` is available when reference
identity, rather than Lua equality, is the question.

### Tables

Tables combine dense integer storage with a hashed record store. Array and
record capacity hints affect initial allocation only; tables grow as needed.
Deletion retains only the continuation information required by Lua's `next`
semantics.

A closed State leaves its objects as a frozen snapshot: `Table` readers,
`Table.Next`, `UserData.Data`, `Function.Prototype`, and every `Value` observer
keep working, while mutation reports `ErrClosed`.

Public `Table` methods are raw operations. They never invoke `__index`,
`__newindex`, or another Lua function. Metamethod-aware indexing from a native
callback uses `Frame.Index` and `Frame.SetIndex`, which can invoke Lua
synchronously before restoring the current native Frame.

The VM uses the same table object and raw storage as the public methods. There
is no compatibility table, wrapper table, or interface-backed execution path.

### Strings

Strings are immutable scalar values. Equality is content-based; pointer
identity is only an internal fast path. Registers, table keys, constants,
upvalues, and public values use the same string representation.

Empty and one-byte strings use process-wide immutable backing. Longer strings
use bounded State-local reuse where it improves recurring runtime strings.
Substrings and captures own their published backing so a small result does not
retain an unrelated large input buffer.

Long strings admitted through the owning Go boundary are also tracked for
semantic heap accounting. A small fixed recent view of that swept attribution
set recognizes an exact backing address and length, and can avoid repeated
hashing and attribution lookup when the same host string recurs. It is not a
content cache: equal text with distinct backing takes the ordinary path. Recent
entries never outlive authoritative attribution membership and are cleared by
the same sweep, so the optimization does not change Lua equality or string
lifetime.

## Compiler and prototypes

The compiler is a direct recursive-descent compiler. It emits instructions and
immutable metadata while parsing and does not retain an abstract syntax tree.
Transient expression descriptors carry register placement, open-result, and
control-flow state until their consumer is known.

Important lowering rules include:

- calls occupy one contiguous callable, argument, and result window;
- only the last unparenthesized call or vararg in a list may produce an open
  result count;
- logical expressions remain control flow until a value is required;
- assignment captures indexed targets before evaluating right-hand values;
- table constructors batch list fields and install record fields in source
  order;
- captured locals are closed before their register range is reused; and
- tail-position calls become tail calls when Lua 5.1 permits it.

Compilation produces a `Prototype`. A prototype is immutable after sealing and
contains exact-size code, constants, child prototypes, upvalue descriptors, and
debug metadata. A verifier checks instruction operands, register windows,
control-flow targets, open-result adjacency, closure bindings, and loop shapes
before execution.

`Compile` creates a State-neutral prototype. `State.LoadPrototype` binds it to a
State and creates the executable function and environment.

## Loading and binary chunks

Source and binary loaders accept strings, readers, and `ScriptLoader`-controlled
logical files. `DoString` and `DoFile` combine loading and execution when a
compiled chunk does not need to be retained. Reader-backed loading uses bounded
refill windows and preserves reader errors. Loading polls the State's installed
context while opening, reading, compiling, and decoding.

One normalized `ScriptLoader` belongs to each State. Its zero value has no
opener. `HostLoader` snapshots `LUA_PATH` during `New`; `fs.FS` and custom-opener
modes use slash-separated names and default `package.path` to
`?.lua;?/init.lua`. `LoadFile`, `DoFile`, base-library file loading, and the
Lua package source searcher all pass through the same opener. Only
`fs.ErrNotExist` advances a module search to the next path candidate.

The script backend is immutable for the State's lifetime. Lua may mutate
`package.path`, which changes template expansion without changing authority.
Script-file access is independent of the IO library's file capabilities.

Lua 5.1 binary chunks describe a native ABI: byte order, integer widths,
`size_t`, instruction layout, and number layout are encoded in the header.
Lunar reads and writes chunks compatible with PUC Lua 5.1 when those ABI fields
match. Loaded bytecode still passes the prototype verifier.

`Options.MaxLoadBytes` bounds input consumed by one load and the projected
retained storage of a binary chunk.

## Calls and execution

Each Lua thread owns one compact value stack shared by its active calls. An
activation records the function, register base, result destination, program
counter, frame extent, and result policy. Register windows are ranges in that
stack rather than separately allocated Go slices.

Ordinary Lua calls are iterative. Fixed-arity Lua calls and fixed-result
returns commit an activation and jump to a shared executor reload point.
Varargs, open result counts, stack growth, metamethod calls, native calls,
yielding, and resource failures use checked paths around the same activation
and result-placement code.

Tail calls replace the current activation after closing its open upvalues.
Returns close upvalues before moving results and clear dead stack roots.
Lua-to-Lua calls do not recurse through Go.

### Executor

Execution is split between one instruction switch and a checked driver. The
switch keeps the current prototype, register base, program counter, constants,
upvalues, and stack in local variables. Instructions that require metamethod
dispatch, string coercion, stack replacement, open call shapes, or other cold
semantic work publish their state and return to the driver. The driver
completes that work and re-enters the same switch. Table growth and other raw
object operations may allocate without replacing the execution stack.

This division keeps one instruction implementation. It also keeps policy,
stack replacement, reentry, and uncommon failure handling out of the ordinary
dispatch path.

Lunar currently has no debug-hook dispatch. Stack, local, upvalue, function, and
traceback inspection are implemented as cold operations that read published
activation state.

### Upvalues

An upvalue keeps one typed pointer to its value. While open, the pointer refers
to a captured stack register. Closing copies the value into the upvalue's own
storage and redirects the pointer. Stack growth retargets open cells before
execution resumes.

Functions store their upvalues in fixed private arrays. Reads and writes do not
need a second open-versus-closed representation.

## Embedding boundary

The public API has an owning interface and a borrowed native-callback
interface over the same runtime:

- `State.Call*` methods enter Lua from an idle State; they return `ErrRunning`
  rather than recursively entering a State whose executor is already active.
- `State.Call` returns an owned result slice.
- `State.CallN` applies Lua's fixed-result adjustment for an exact arity.
- `State.CallOne` applies the common one-result adjustment, while
  `State.CallDiscard` requests no results without allocating a result slice.
- `State.CallInto` writes into caller-provided result storage.
- `NativeFunc` receives the active borrowed `Frame` with typed argument access,
  terminal `Outcome` methods, and the capability to reenter Lua synchronously.
- `Frame.Call`, `Frame.CallN`, `Frame.CallOne`, `Frame.CallDiscard`, and
  `Frame.CallInto` make protected reentrant calls.
- `Frame.Index` and `Frame.SetIndex` perform Lua indexing from a callback.

Frame argument indexes are zero-based. Exact typed accessors never coerce;
explicitly named coercion, strict-integer, range, and missing-or-nil helpers
cover common native-module validation without panic control flow.

`CallInto` does not write a partial result when its destination is too small.
It returns the required count in `ResultCapacityError`; `Results` recovers an
owned copy after the completed call or coroutine transition. Lua side effects
are not rolled back.

An `Outcome` is tied to the invocation that created it. A callback may retain
owning values read from the Frame, but it must not retain the Frame itself. A
callback ends by returning a `Return*` Outcome, returning a `Yield*` Outcome,
or calling `Throw*`. `Throw*` unwinds with a private panic recovered at the
native call boundary, so a helper called at any depth inside the callback can
report a failure that a returned Outcome cannot express.
`ThrowError` converts an ordinary Go error and preserves it as the cause;
`Rethrow` propagates an existing Lua error without losing its value, category,
or nested traceback.

See [Embedding Lunar](embedding.md) for examples and lifecycle guidance.

## Coroutines and reentry

Coroutines use the same compact thread and activation structures as the main
thread. Resume transfers arguments into the suspended thread and returns
yielded or final results through owned or caller-provided storage. An
undersized caller destination does not lose the completed transition's
results; `ResultCapacityError.Results` owns and recovers them.

A yielding native callback suspends its activation. When resumed, it receives
the resume arguments through that same activation. Nested `Frame.Call`,
metamethod-aware indexing, protected Lua calls, and library callbacks use
explicit continuation records when work remains after a nested call.

One State still has one active executor: a callback may reenter through the
Frame APIs, but another goroutine may not enter the State concurrently.

## Contexts and host control

`SetContext` installs one ambient `context.Context` for the State. It outlives
the operation that installed it, covers every thread including coroutines, and
governs loading as well as execution, so a script cannot escape it by looping
inside a coroutine or a `require`. It can be replaced from inside a native
callback and takes effect during the call that replaced it. A State with no
installed context arms no polling at all.

Execution, patterns, readers, and blocking library paths poll at bounded safe
points. Cancellation returns an `*Error` in the `ContextError` category and is
not catchable by Lua `pcall`. Polling cannot preempt an arbitrary caller
`io.Reader` while its own `Read` method is blocked.

`Options.MaxHeapBytes` bounds the logical heap `HeapBytes` measures. The
collector supplies the enforcement point: a charge that crosses the limit
schedules a cycle, and the safe point raises only if the freshly measured live
heap is still over. The failure is a `LimitError`, which Lua `pcall` cannot
catch — a script must not be able to absorb the ceiling that bounds it and keep
allocating. `ResourceError` remains the category for the limits Lua itself
defines, such as stack overflow, which a script may legitimately recover from.
While an xpcall handler runs, the enforced limit widens by
`max(64 KiB, MaxHeapBytes/8)` so the handler can allocate its report, mirroring
the emergency capacity `MaxValues` and `MaxFrames` already grant.

`os.exit` does not terminate the Go process. It returns an `*Error` in the
`ExitError` category that unwraps to `*ExitRequest`. The host decides whether
that request ends a plugin, request, service, or process.

Lua execution errors retain the original Lua value and a compact traceback.
The exported `Error` remains inspectable after State closure and classifies
runtime, syntax, resource, context, limit, and exit failures without replacing
the Lua error value. `TraceFrame.String` renders one activation the way Lua
positions it, and `Frame.Where` gives a callback the same prefix for a message
it composes itself.

## Semantic collection

Go reclaims backing allocations, while Lunar's State-local collector determines
Lua reachability. It implements Lua 5.1 weak-table behavior, userdata
finalization, `collectgarbage`, `gcinfo`, automatic collection scheduling, and
logical Lua heap accounting.

Collection is currently synchronous. It runs only at graph-stable executor
seams and does not add a write barrier or collection check to every table
mutation or instruction.

The complete ownership, weak-table, finalization, scheduling, and accounting
contracts are in [Semantic collection](collection.md).

## Libraries and native resources

`Options.Libraries` selects any standard-library subset during `New`; its zero
value installs none. `CoreLibraries` selects base, package, table, string, and
math, with coroutine supplied by Lua 5.1's base library. `FullLibraries` also
selects IO, OS, and debug. Selection is validated before construction,
duplicates are ignored, and installation follows one canonical order.

The individual `OpenBase`, `OpenString`, and other library methods remain
available for deliberate post-construction grants. Each opener installs fresh
native functions and any table owned by that library. `OpenBase` also installs
the coroutine library; `OpenCoroutine` permits that library without base
globals.

The standard library implementation uses native Frames and compact runtime
objects. Lua-visible calls, indexing, errors, and yields reenter through the
same VM paths used by host callbacks.

Files, process pipes, temporary files, and other library-owned resources use
exactly-once private cleanup records. Explicit close, State close, and semantic
collection converge on that cleanup while retaining their reason-specific
wait, flush, or termination behavior. Lua userdata finalization remains
separate from native resource cleanup.

The pure-Go runtime has no Lua C ABI. The package library installs no C-module
searchers, leaves `package.cpath` empty, and keeps `package.loadlib` as a stub
that reports dynamic libraries are unavailable.

## Limits and validation

`Options` provides deterministic limits for value-stack entries, activations,
loaded chunk bytes, and retained logical Lua heap bytes. Value-stack,
activation, load, and contiguous-result limits are ordinary Lua errors in the
`ResourceError` category. The host-enforced logical-heap ceiling instead
reports an uncatchable `LimitError`. `string.rep`, large IO reads, and
`os.date` formatting apply a shared 1 GiB contiguous-result limit.

Correctness tests cover public ownership, lifecycle, race behavior, compiler
and VM semantics, libraries, collection, and supported platform paths.
Recorded language and library cases can be re-run against a configured PUC Lua
5.1.5 executable with `LUNAR_LUA51`.

Cross-runtime performance claims use the version-pinned harness and collection
protocol in [`benchmarks/README.md`](../benchmarks/README.md). Algorithms
adapted from reference implementations are identified in
[`THIRD_PARTY_NOTICES.md`](../THIRD_PARTY_NOTICES.md).
